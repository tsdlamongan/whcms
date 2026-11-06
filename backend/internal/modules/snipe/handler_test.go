package snipe

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"
)

// fakeMW stubs the middleware bundle mirroring production semantics.
type fakeMW struct {
	identity    httpx.AuthIdentity
	authFail    bool
	rateLimited bool
	permissions map[string]bool
}

func (m *fakeMW) RequireAuth() fiber.Handler {
	return func(c fiber.Ctx) error {
		if m.authFail {
			return apperr.Unauthorized("missing bearer token")
		}
		httpx.SetIdentity(c, m.identity)
		return c.Next()
	}
}

func (m *fakeMW) RequireRole(roles ...string) fiber.Handler {
	allowed := map[string]bool{}
	for _, r := range roles {
		allowed[r] = true
	}
	return func(c fiber.Ctx) error {
		id, _ := httpx.Identity(c)
		if !allowed[id.Role] {
			return apperr.Forbidden("insufficient role")
		}
		return c.Next()
	}
}

func (m *fakeMW) RequirePermission(module string) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, _ := httpx.Identity(c)
		if id.Role == "admin" || m.permissions[module] {
			return c.Next()
		}
		return apperr.Forbidden("missing permission: " + module)
	}
}

func (m *fakeMW) RequireClient() fiber.Handler {
	return func(c fiber.Ctx) error {
		id, ok := httpx.Identity(c)
		if !ok {
			return apperr.Unauthorized("authentication required")
		}
		if id.ClientID == 0 {
			return apperr.Forbidden("client profile required")
		}
		return c.Next()
	}
}

func (m *fakeMW) RateLimit(string, int, time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error {
		if m.rateLimited {
			return apperr.New(apperr.CodeRateLimited, "too many requests")
		}
		return c.Next()
	}
}

var _ Middlewares = (*fakeMW)(nil)

// fakeSvc is a function-field fake of SnipeService.
type fakeSvc struct {
	CreateFn func(ctx context.Context, actor int64, in AdminCreateRequest) (*domain.DomainSnipe, error)
	GetFn    func(ctx context.Context, id int64) (*Detail, error)
	ListFn   func(ctx context.Context, p ports.ListParams) ([]domain.DomainSnipe, int64, error)
	CancelFn func(ctx context.Context, actor, id int64, refundFee bool) (*domain.DomainSnipe, error)
	StatusFn func(ctx context.Context) (*StatusResponse, error)

	ListActivePlansFn func(ctx context.Context) ([]PlanView, error)
	QuoteFn           func(ctx context.Context, in QuoteRequest) (*Quote, error)
	ClientCreateFn    func(ctx context.Context, who ClientIdentity, in CreateRequest) (*ClientSnipe, error)
	ListForClientFn   func(ctx context.Context, clientID int64, p ports.ListParams) ([]ClientSnipe, int64, error)
	GetForClientFn    func(ctx context.Context, clientID, id int64) (*ClientDetail, error)
	CancelForClientFn func(ctx context.Context, who ClientIdentity, id int64) (*ClientSnipe, error)
	ListPlansFn       func(ctx context.Context) ([]domain.SnipePlan, error)
	CreatePlanFn      func(ctx context.Context, actor int64, in PlanRequest) (*domain.SnipePlan, error)
	UpdatePlanFn      func(ctx context.Context, actor, id int64, in PlanRequest) (*domain.SnipePlan, error)
}

func (f *fakeSvc) AdminCreate(ctx context.Context, actor int64, in AdminCreateRequest) (*domain.DomainSnipe, error) {
	if f.CreateFn != nil {
		return f.CreateFn(ctx, actor, in)
	}
	return &domain.DomainSnipe{ID: 1}, nil
}

func (f *fakeSvc) AdminGet(ctx context.Context, id int64) (*Detail, error) {
	if f.GetFn != nil {
		return f.GetFn(ctx, id)
	}
	return &Detail{Snipe: &domain.DomainSnipe{ID: id}, Events: []domain.SnipeEvent{}}, nil
}

func (f *fakeSvc) AdminList(ctx context.Context, p ports.ListParams) ([]domain.DomainSnipe, int64, error) {
	if f.ListFn != nil {
		return f.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (f *fakeSvc) AdminCancel(ctx context.Context, actor, id int64, refundFee bool) (*domain.DomainSnipe, error) {
	if f.CancelFn != nil {
		return f.CancelFn(ctx, actor, id, refundFee)
	}
	return &domain.DomainSnipe{ID: id, Status: domain.SnipeCancelled}, nil
}

func (f *fakeSvc) Status(ctx context.Context) (*StatusResponse, error) {
	if f.StatusFn != nil {
		return f.StatusFn(ctx)
	}
	return &StatusResponse{Enabled: true, Counts: map[string]int{}}, nil
}

func newApp(svc SnipeService, mw *fakeMW) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: transporthttp.ErrorHandler(slog.New(slog.NewTextHandler(io.Discard, nil))),
	})
	NewHandler(svc, mw).RegisterRoutes(app.Group("/api/v1"))
	return app
}

func doJSON(t *testing.T, app *fiber.App, method, path, body string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var env map[string]any
	require.NoError(t, json.Unmarshal(raw, &env), "body: %s", raw)
	return resp.StatusCode, env
}

func admin() *fakeMW {
	return &fakeMW{identity: httpx.AuthIdentity{UserID: 9, Role: "admin"}}
}

func errCode(env map[string]any) string {
	e, _ := env["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestHandlerList(t *testing.T) {
	t.Run("passes filters and paginates", func(t *testing.T) {
		svc := &fakeSvc{ListFn: func(_ context.Context, p ports.ListParams) ([]domain.DomainSnipe, int64, error) {
			assert.Equal(t, 2, p.Page)
			assert.Equal(t, 25, p.PerPage)
			assert.Equal(t, "active", p.Status)
			assert.Equal(t, "hunt", p.Search)
			assert.EqualValues(t, 7, p.ClientID)
			return []domain.DomainSnipe{{ID: 1, DomainName: "hunt.com"}}, 41, nil
		}}
		status, env := doJSON(t, newApp(svc, admin()), "GET", "/api/v1/admin/snipes?page=2&per_page=25&status=active&search=hunt&client_id=7", "")
		assert.Equal(t, 200, status)
		items := env["data"].([]any)
		require.Len(t, items, 1)
		assert.Equal(t, "hunt.com", items[0].(map[string]any)["domain_name"])
		meta := env["meta"].(map[string]any)
		assert.EqualValues(t, 41, meta["total"])
	})

	t.Run("an empty list is [] not null", func(t *testing.T) {
		status, env := doJSON(t, newApp(&fakeSvc{}, admin()), "GET", "/api/v1/admin/snipes", "")
		assert.Equal(t, 200, status)
		assert.Equal(t, []any{}, env["data"])
	})

	t.Run("a bad client_id is rejected", func(t *testing.T) {
		status, env := doJSON(t, newApp(&fakeSvc{}, admin()), "GET", "/api/v1/admin/snipes?client_id=abc", "")
		assert.Equal(t, 422, status)
		assert.Equal(t, "VALIDATION", errCode(env))
	})

	t.Run("service errors map to the envelope", func(t *testing.T) {
		svc := &fakeSvc{ListFn: func(context.Context, ports.ListParams) ([]domain.DomainSnipe, int64, error) {
			return nil, 0, apperr.Internal(errBoom)
		}}
		status, env := doJSON(t, newApp(svc, admin()), "GET", "/api/v1/admin/snipes", "")
		assert.Equal(t, 500, status)
		assert.Equal(t, "INTERNAL", errCode(env))
	})
}

func TestHandlerCreate(t *testing.T) {
	t.Run("creates and returns 201", func(t *testing.T) {
		svc := &fakeSvc{CreateFn: func(_ context.Context, actor int64, in AdminCreateRequest) (*domain.DomainSnipe, error) {
			assert.EqualValues(t, 9, actor)
			assert.EqualValues(t, 7, in.ClientID)
			assert.Equal(t, "hunt.com", in.Domain)
			require.NotNil(t, in.HuntFrom)
			assert.Equal(t, 2024, in.HuntFrom.Year())
			return &domain.DomainSnipe{ID: 5, DomainName: in.Domain}, nil
		}}
		status, env := doJSON(t, newApp(svc, admin()), "POST", "/api/v1/admin/snipes",
			`{"client_id":7,"domain":"hunt.com","hunt_from":"2024-01-02T03:04:05Z"}`)
		assert.Equal(t, 201, status)
		assert.EqualValues(t, 5, env["data"].(map[string]any)["id"])
	})

	t.Run("rejects a malformed body and missing fields", func(t *testing.T) {
		status, _ := doJSON(t, newApp(&fakeSvc{}, admin()), "POST", "/api/v1/admin/snipes", `{not json`)
		assert.Equal(t, 422, status)
		status, env := doJSON(t, newApp(&fakeSvc{}, admin()), "POST", "/api/v1/admin/snipes", `{"domain":"hunt.com"}`)
		assert.Equal(t, 422, status)
		assert.Equal(t, "VALIDATION", errCode(env))
		status, _ = doJSON(t, newApp(&fakeSvc{}, admin()), "POST", "/api/v1/admin/snipes", `{"client_id":7,"domain":"hunt.com","years":11}`)
		assert.Equal(t, 422, status)
	})

	t.Run("service errors map to the envelope", func(t *testing.T) {
		svc := &fakeSvc{CreateFn: func(context.Context, int64, AdminCreateRequest) (*domain.DomainSnipe, error) {
			return nil, apperr.Conflict("a snipe for hunt.com is already open")
		}}
		status, env := doJSON(t, newApp(svc, admin()), "POST", "/api/v1/admin/snipes", `{"client_id":7,"domain":"hunt.com"}`)
		assert.Equal(t, 409, status)
		assert.Equal(t, "CONFLICT", errCode(env))
	})
}

func TestHandlerGetCancelStatus(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		status, env := doJSON(t, newApp(&fakeSvc{}, admin()), "GET", "/api/v1/admin/snipes/4", "")
		assert.Equal(t, 200, status)
		data := env["data"].(map[string]any)
		assert.EqualValues(t, 4, data["snipe"].(map[string]any)["id"])
		assert.Equal(t, []any{}, data["events"])

		status, env = doJSON(t, newApp(&fakeSvc{}, admin()), "GET", "/api/v1/admin/snipes/abc", "")
		assert.Equal(t, 422, status)
		assert.Equal(t, "VALIDATION", errCode(env))

		status, _ = doJSON(t, newApp(&fakeSvc{}, admin()), "GET", "/api/v1/admin/snipes/0", "")
		assert.Equal(t, 422, status)

		svc := &fakeSvc{GetFn: func(context.Context, int64) (*Detail, error) { return nil, apperr.NotFound("snipe") }}
		status, _ = doJSON(t, newApp(svc, admin()), "GET", "/api/v1/admin/snipes/4", "")
		assert.Equal(t, 404, status)
	})

	t.Run("cancel with and without a body", func(t *testing.T) {
		var gotRefund bool
		svc := &fakeSvc{CancelFn: func(_ context.Context, actor, id int64, refund bool) (*domain.DomainSnipe, error) {
			assert.EqualValues(t, 9, actor)
			assert.EqualValues(t, 4, id)
			gotRefund = refund
			return &domain.DomainSnipe{ID: id, Status: domain.SnipeCancelled}, nil
		}}
		app := newApp(svc, admin())
		status, env := doJSON(t, app, "POST", "/api/v1/admin/snipes/4/cancel", `{"refund_fee":true}`)
		assert.Equal(t, 200, status)
		assert.True(t, gotRefund)
		assert.Equal(t, "cancelled", env["data"].(map[string]any)["status"])

		status, _ = doJSON(t, app, "POST", "/api/v1/admin/snipes/4/cancel", "")
		assert.Equal(t, 200, status)
		assert.False(t, gotRefund, "no body means no fee refund")

		status, _ = doJSON(t, app, "POST", "/api/v1/admin/snipes/4/cancel", `{bad`)
		assert.Equal(t, 422, status)
		status, _ = doJSON(t, app, "POST", "/api/v1/admin/snipes/zero/cancel", "")
		assert.Equal(t, 422, status)
	})

	t.Run("cancel conflict maps to 409", func(t *testing.T) {
		svc := &fakeSvc{CancelFn: func(context.Context, int64, int64, bool) (*domain.DomainSnipe, error) {
			return nil, apperr.Conflict("the snipe changed state")
		}}
		status, _ := doJSON(t, newApp(svc, admin()), "POST", "/api/v1/admin/snipes/4/cancel", "")
		assert.Equal(t, 409, status)
	})

	t.Run("status", func(t *testing.T) {
		bal := int64(123)
		svc := &fakeSvc{StatusFn: func(context.Context) (*StatusResponse, error) {
			return &StatusResponse{Enabled: true, ResellerBalance: &bal, Counts: map[string]int{"active": 2}}, nil
		}}
		status, env := doJSON(t, newApp(svc, admin()), "GET", "/api/v1/admin/snipes/status", "")
		assert.Equal(t, 200, status)
		data := env["data"].(map[string]any)
		assert.EqualValues(t, 123, data["reseller_balance"])
		assert.EqualValues(t, 2, data["counts"].(map[string]any)["active"])

		svc.StatusFn = func(context.Context) (*StatusResponse, error) { return nil, apperr.Internal(errBoom) }
		status, _ = doJSON(t, newApp(svc, admin()), "GET", "/api/v1/admin/snipes/status", "")
		assert.Equal(t, 500, status)
	})

	t.Run("the overview is rate limited", func(t *testing.T) {
		mw := admin()
		mw.rateLimited = true
		status, env := doJSON(t, newApp(&fakeSvc{}, mw), "GET", "/api/v1/admin/snipes/status", "")
		assert.Equal(t, 429, status)
		assert.Equal(t, "RATE_LIMITED", errCode(env))
	})
}

func TestHandlerGuards(t *testing.T) {
	cases := []struct {
		name   string
		mw     *fakeMW
		status int
	}{
		{"anonymous", &fakeMW{authFail: true}, 401},
		{"a client account", &fakeMW{identity: httpx.AuthIdentity{UserID: 3, Role: "client", ClientID: 4}}, 403},
		{"staff without the domains permission", &fakeMW{identity: httpx.AuthIdentity{UserID: 5, Role: "staff"}}, 403},
		{"staff with the domains permission", &fakeMW{identity: httpx.AuthIdentity{UserID: 5, Role: "staff"}, permissions: map[string]bool{"domains": true}}, 200},
		{"admin", admin(), 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := doJSON(t, newApp(&fakeSvc{}, tc.mw), "GET", "/api/v1/admin/snipes", "")
			assert.Equal(t, tc.status, status)
		})
	}
}

func TestParseIDRejectsBadValues(t *testing.T) {
	app := fiber.New()
	app.Get("/x/:id", func(c fiber.Ctx) error {
		id, err := parseID(c)
		if err != nil {
			return c.SendStatus(400)
		}
		return c.SendString(string(rune('0' + id)))
	})
	for path, want := range map[string]int{"/x/3": 200, "/x/-1": 400, "/x/0": 400, "/x/q": 400} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		require.NoError(t, err)
		assert.Equal(t, want, resp.StatusCode, path)
	}
}

func (f *fakeSvc) ListActivePlans(ctx context.Context) ([]PlanView, error) {
	if f.ListActivePlansFn != nil {
		return f.ListActivePlansFn(ctx)
	}
	return nil, nil
}

func (f *fakeSvc) Quote(ctx context.Context, in QuoteRequest) (*Quote, error) {
	if f.QuoteFn != nil {
		return f.QuoteFn(ctx, in)
	}
	return &Quote{Domain: in.Domain}, nil
}

func (f *fakeSvc) Create(ctx context.Context, who ClientIdentity, in CreateRequest) (*ClientSnipe, error) {
	if f.ClientCreateFn != nil {
		return f.ClientCreateFn(ctx, who, in)
	}
	return &ClientSnipe{ID: 1, DomainName: in.Domain}, nil
}

func (f *fakeSvc) ListForClient(ctx context.Context, clientID int64, p ports.ListParams) ([]ClientSnipe, int64, error) {
	if f.ListForClientFn != nil {
		return f.ListForClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (f *fakeSvc) GetForClient(ctx context.Context, clientID, id int64) (*ClientDetail, error) {
	if f.GetForClientFn != nil {
		return f.GetForClientFn(ctx, clientID, id)
	}
	return &ClientDetail{Snipe: ClientSnipe{ID: id}, Events: []ClientEvent{}}, nil
}

func (f *fakeSvc) CancelForClient(ctx context.Context, who ClientIdentity, id int64) (*ClientSnipe, error) {
	if f.CancelForClientFn != nil {
		return f.CancelForClientFn(ctx, who, id)
	}
	return &ClientSnipe{ID: id, Status: domain.SnipeCancelled}, nil
}

func (f *fakeSvc) ListPlans(ctx context.Context) ([]domain.SnipePlan, error) {
	if f.ListPlansFn != nil {
		return f.ListPlansFn(ctx)
	}
	return nil, nil
}

func (f *fakeSvc) CreatePlan(ctx context.Context, actor int64, in PlanRequest) (*domain.SnipePlan, error) {
	if f.CreatePlanFn != nil {
		return f.CreatePlanFn(ctx, actor, in)
	}
	return &domain.SnipePlan{ID: 1, Name: in.Name}, nil
}

func (f *fakeSvc) UpdatePlan(ctx context.Context, actor, id int64, in PlanRequest) (*domain.SnipePlan, error) {
	if f.UpdatePlanFn != nil {
		return f.UpdatePlanFn(ctx, actor, id, in)
	}
	return &domain.SnipePlan{ID: id, Name: in.Name}, nil
}

func clientMW() *fakeMW {
	return &fakeMW{identity: httpx.AuthIdentity{UserID: 31, Role: "client", ClientID: 7}}
}

func TestHandlerClientPlansAndQuote(t *testing.T) {
	t.Run("plans", func(t *testing.T) {
		svc := &fakeSvc{ListActivePlansFn: func(context.Context) ([]PlanView, error) {
			return []PlanView{{ID: 1, Name: "Standard", WindowDays: 30, Fee: 50000}}, nil
		}}
		status, env := doJSON(t, newApp(svc, clientMW()), "GET", "/api/v1/snipe/plans", "")
		assert.Equal(t, 200, status)
		assert.Equal(t, "Standard", env["data"].([]any)[0].(map[string]any)["name"])

		svc.ListActivePlansFn = func(context.Context) ([]PlanView, error) { return nil, apperr.Internal(errBoom) }
		status, _ = doJSON(t, newApp(svc, clientMW()), "GET", "/api/v1/snipe/plans", "")
		assert.Equal(t, 500, status)
	})

	t.Run("quote", func(t *testing.T) {
		svc := &fakeSvc{QuoteFn: func(_ context.Context, in QuoteRequest) (*Quote, error) {
			assert.Equal(t, "hunt.com", in.Domain)
			assert.EqualValues(t, 3, in.PlanID)
			assert.Equal(t, 2, in.Years)
			return &Quote{Domain: in.Domain, Total: 123456}, nil
		}}
		app := newApp(svc, clientMW())
		status, env := doJSON(t, app, "POST", "/api/v1/snipe/quote", `{"domain":"hunt.com","plan_id":3,"years":2}`)
		assert.Equal(t, 200, status)
		assert.EqualValues(t, 123456, env["data"].(map[string]any)["total"])

		status, _ = doJSON(t, app, "POST", "/api/v1/snipe/quote", `{bad`)
		assert.Equal(t, 422, status)
		status, _ = doJSON(t, app, "POST", "/api/v1/snipe/quote", `{"domain":"hunt.com"}`)
		assert.Equal(t, 422, status, "a plan is required")
		status, _ = doJSON(t, app, "POST", "/api/v1/snipe/quote", `{"domain":"hunt.com","plan_id":3,"years":11}`)
		assert.Equal(t, 422, status)

		svc.QuoteFn = func(context.Context, QuoteRequest) (*Quote, error) {
			return nil, apperr.Validation("no pricing is configured for .xyz")
		}
		status, _ = doJSON(t, app, "POST", "/api/v1/snipe/quote", `{"domain":"hunt.xyz","plan_id":3}`)
		assert.Equal(t, 422, status)
	})

	t.Run("rate limited", func(t *testing.T) {
		mw := clientMW()
		mw.rateLimited = true
		status, env := doJSON(t, newApp(&fakeSvc{}, mw), "POST", "/api/v1/snipe/quote", `{"domain":"hunt.com","plan_id":3}`)
		assert.Equal(t, 429, status)
		assert.Equal(t, "RATE_LIMITED", errCode(env))
	})
}

func TestHandlerClientSnipes(t *testing.T) {
	t.Run("list is scoped to the caller's client", func(t *testing.T) {
		svc := &fakeSvc{ListForClientFn: func(_ context.Context, clientID int64, p ports.ListParams) ([]ClientSnipe, int64, error) {
			assert.EqualValues(t, 7, clientID, "the client id comes from the token, never the query")
			assert.Equal(t, 3, p.Page)
			assert.Equal(t, 50, p.PerPage)
			assert.Equal(t, "active", p.Status)
			assert.Equal(t, "hunt", p.Search)
			return []ClientSnipe{{ID: 4, DomainName: "hunt.com"}}, 121, nil
		}}
		status, env := doJSON(t, newApp(svc, clientMW()), "GET",
			"/api/v1/snipes?client_id=999&page=3&per_page=50&status=active&search=hunt", "")
		assert.Equal(t, 200, status)
		assert.EqualValues(t, 121, env["meta"].(map[string]any)["total"])
		assert.Equal(t, "hunt.com", env["data"].([]any)[0].(map[string]any)["domain_name"])
	})

	t.Run("an empty list is [] not null", func(t *testing.T) {
		status, env := doJSON(t, newApp(&fakeSvc{}, clientMW()), "GET", "/api/v1/snipes", "")
		assert.Equal(t, 200, status)
		assert.Equal(t, []any{}, env["data"])

		svc := &fakeSvc{ListForClientFn: func(context.Context, int64, ports.ListParams) ([]ClientSnipe, int64, error) {
			return nil, 0, apperr.Internal(errBoom)
		}}
		status, _ = doJSON(t, newApp(svc, clientMW()), "GET", "/api/v1/snipes", "")
		assert.Equal(t, 500, status)
	})

	t.Run("create returns 201 with the identity of the caller", func(t *testing.T) {
		inv := int64(900)
		svc := &fakeSvc{ClientCreateFn: func(_ context.Context, who ClientIdentity, in CreateRequest) (*ClientSnipe, error) {
			assert.Equal(t, ClientIdentity{UserID: 31, ClientID: 7}, who)
			assert.Equal(t, "hunt.com", in.Domain)
			assert.EqualValues(t, 3, in.PlanID)
			require.NotNil(t, in.HuntFrom)
			assert.Equal(t, []string{"ns1.example.net", "ns2.example.net"}, in.Nameservers)
			return &ClientSnipe{ID: 5, DomainName: in.Domain, InvoiceID: &inv, Status: domain.SnipePendingPayment}, nil
		}}
		status, env := doJSON(t, newApp(svc, clientMW()), "POST", "/api/v1/snipes",
			`{"domain":"hunt.com","plan_id":3,"hunt_from":"2030-01-02T03:04:05Z","nameservers":["ns1.example.net","ns2.example.net"]}`)
		assert.Equal(t, 201, status)
		data := env["data"].(map[string]any)
		assert.EqualValues(t, 900, data["invoice_id"])
		assert.Equal(t, "pending_payment", data["status"])
	})

	t.Run("create validates the body and maps service errors", func(t *testing.T) {
		app := newApp(&fakeSvc{}, clientMW())
		status, _ := doJSON(t, app, "POST", "/api/v1/snipes", `{bad`)
		assert.Equal(t, 422, status)
		status, _ = doJSON(t, app, "POST", "/api/v1/snipes", `{"plan_id":3}`)
		assert.Equal(t, 422, status)
		status, _ = doJSON(t, app, "POST", "/api/v1/snipes", `{"domain":"hunt.com","plan_id":3,"nameservers":["a","b","c","d","e"]}`)
		assert.Equal(t, 422, status)

		for code, want := range map[apperr.Code]int{
			apperr.CodeConflict: 409, apperr.CodeForbidden: 403, apperr.CodeValidation: 422,
		} {
			svc := &fakeSvc{ClientCreateFn: func(context.Context, ClientIdentity, CreateRequest) (*ClientSnipe, error) {
				return nil, apperr.New(code, "nope")
			}}
			status, _ = doJSON(t, newApp(svc, clientMW()), "POST", "/api/v1/snipes", `{"domain":"hunt.com","plan_id":3}`)
			assert.Equal(t, want, status, code)
		}

		mw := clientMW()
		mw.rateLimited = true
		status, _ = doJSON(t, newApp(&fakeSvc{}, mw), "POST", "/api/v1/snipes", `{"domain":"hunt.com","plan_id":3}`)
		assert.Equal(t, 429, status)
	})

	t.Run("get and cancel use the caller's client", func(t *testing.T) {
		svc := &fakeSvc{
			GetForClientFn: func(_ context.Context, clientID, id int64) (*ClientDetail, error) {
				assert.EqualValues(t, 7, clientID)
				assert.EqualValues(t, 4, id)
				return &ClientDetail{Snipe: ClientSnipe{ID: id}, Events: []ClientEvent{{Kind: "won"}}}, nil
			},
			CancelForClientFn: func(_ context.Context, who ClientIdentity, id int64) (*ClientSnipe, error) {
				assert.Equal(t, ClientIdentity{UserID: 31, ClientID: 7}, who)
				return &ClientSnipe{ID: id, Status: domain.SnipeCancelled}, nil
			},
		}
		app := newApp(svc, clientMW())
		status, env := doJSON(t, app, "GET", "/api/v1/snipes/4", "")
		assert.Equal(t, 200, status)
		assert.Equal(t, "won", env["data"].(map[string]any)["events"].([]any)[0].(map[string]any)["kind"])
		status, env = doJSON(t, app, "POST", "/api/v1/snipes/4/cancel", "")
		assert.Equal(t, 200, status)
		assert.Equal(t, "cancelled", env["data"].(map[string]any)["status"])

		for _, path := range []string{"/api/v1/snipes/abc", "/api/v1/snipes/0"} {
			status, _ = doJSON(t, app, "GET", path, "")
			assert.Equal(t, 422, status, path)
		}
		status, _ = doJSON(t, app, "POST", "/api/v1/snipes/zero/cancel", "")
		assert.Equal(t, 422, status)

		svc.GetForClientFn = func(context.Context, int64, int64) (*ClientDetail, error) { return nil, apperr.NotFound("snipe") }
		svc.CancelForClientFn = func(context.Context, ClientIdentity, int64) (*ClientSnipe, error) {
			return nil, apperr.Conflict("this snipe has already ended")
		}
		status, _ = doJSON(t, newApp(svc, clientMW()), "GET", "/api/v1/snipes/4", "")
		assert.Equal(t, 404, status)
		status, _ = doJSON(t, newApp(svc, clientMW()), "POST", "/api/v1/snipes/4/cancel", "")
		assert.Equal(t, 409, status)
	})

	t.Run("only clients reach the client routes", func(t *testing.T) {
		for name, mw := range map[string]*fakeMW{
			"anonymous":       {authFail: true},
			"staff/no-client": {identity: httpx.AuthIdentity{UserID: 5, Role: "staff"}},
		} {
			for _, path := range []string{"/api/v1/snipes", "/api/v1/snipe/plans"} {
				status, _ := doJSON(t, newApp(&fakeSvc{}, mw), "GET", path, "")
				assert.Contains(t, []int{401, 403}, status, "%s %s", name, path)
			}
		}
	})
}

func TestHandlerAdminPlans(t *testing.T) {
	t.Run("list, create, update", func(t *testing.T) {
		var created, updated PlanRequest
		svc := &fakeSvc{
			ListPlansFn: func(context.Context) ([]domain.SnipePlan, error) {
				return []domain.SnipePlan{{ID: 1, Name: "Standard", Active: true}}, nil
			},
			CreatePlanFn: func(_ context.Context, actor int64, in PlanRequest) (*domain.SnipePlan, error) {
				assert.EqualValues(t, 9, actor)
				created = in
				return &domain.SnipePlan{ID: 2, Name: in.Name}, nil
			},
			UpdatePlanFn: func(_ context.Context, actor, id int64, in PlanRequest) (*domain.SnipePlan, error) {
				assert.EqualValues(t, 9, actor)
				assert.EqualValues(t, 2, id)
				updated = in
				return &domain.SnipePlan{ID: id, Name: in.Name}, nil
			},
		}
		app := newApp(svc, admin())

		status, env := doJSON(t, app, "GET", "/api/v1/admin/snipe-plans", "")
		assert.Equal(t, 200, status)
		assert.Equal(t, "Standard", env["data"].([]any)[0].(map[string]any)["name"])

		status, env = doJSON(t, app, "POST", "/api/v1/admin/snipe-plans",
			`{"name":"Pro","window_days":30,"fee":50000,"success_fee":25000,"sort":2}`)
		assert.Equal(t, 201, status)
		assert.EqualValues(t, 2, env["data"].(map[string]any)["id"])
		assert.Equal(t, PlanRequest{Name: "Pro", WindowDays: 30, Fee: 50000, SuccessFee: 25000, Sort: 2}, created)

		status, _ = doJSON(t, app, "PUT", "/api/v1/admin/snipe-plans/2",
			`{"name":"Pro+","window_days":90,"fee":75000,"active":false}`)
		assert.Equal(t, 200, status)
		require.NotNil(t, updated.Active)
		assert.False(t, *updated.Active)
		assert.Equal(t, 90, updated.WindowDays)
	})

	t.Run("validation and errors", func(t *testing.T) {
		app := newApp(&fakeSvc{}, admin())
		for _, body := range []string{
			`{bad`,
			`{"window_days":30}`,                                     // name required
			`{"name":"x","window_days":0}`,                           // window required
			`{"name":"x","window_days":366}`,                         // window too long
			`{"name":"x","window_days":30,"fee":-1}`,                 // negative fee
			`{"name":"x","window_days":30,"fee":1000000001}`,         // fee beyond any real price
			`{"name":"x","window_days":30,"success_fee":1000000001}`, // success fee beyond any real price
			`{"name":"x","window_days":30,"sort":-1}`,                // negative sort
		} {
			status, _ := doJSON(t, app, "POST", "/api/v1/admin/snipe-plans", body)
			assert.Equal(t, 422, status, body)
			status, _ = doJSON(t, app, "PUT", "/api/v1/admin/snipe-plans/2", body)
			assert.Equal(t, 422, status, body)
		}
		status, _ := doJSON(t, app, "PUT", "/api/v1/admin/snipe-plans/zero", `{"name":"x","window_days":30}`)
		assert.Equal(t, 422, status)

		svc := &fakeSvc{
			ListPlansFn: func(context.Context) ([]domain.SnipePlan, error) { return nil, apperr.Internal(errBoom) },
			CreatePlanFn: func(context.Context, int64, PlanRequest) (*domain.SnipePlan, error) {
				return nil, apperr.Internal(errBoom)
			},
			UpdatePlanFn: func(context.Context, int64, int64, PlanRequest) (*domain.SnipePlan, error) {
				return nil, apperr.NotFound("snipe plan")
			},
		}
		app = newApp(svc, admin())
		status, _ = doJSON(t, app, "GET", "/api/v1/admin/snipe-plans", "")
		assert.Equal(t, 500, status)
		status, _ = doJSON(t, app, "POST", "/api/v1/admin/snipe-plans", `{"name":"x","window_days":30}`)
		assert.Equal(t, 500, status)
		status, _ = doJSON(t, app, "PUT", "/api/v1/admin/snipe-plans/2", `{"name":"x","window_days":30}`)
		assert.Equal(t, 404, status)
	})

	t.Run("plan pricing is admin-only, staff are refused even with the domains permission", func(t *testing.T) {
		staff := &fakeMW{identity: httpx.AuthIdentity{UserID: 5, Role: "staff"}, permissions: map[string]bool{"domains": true}}
		status, _ := doJSON(t, newApp(&fakeSvc{}, staff), "GET", "/api/v1/admin/snipe-plans", "")
		assert.Equal(t, 403, status)
		status, _ = doJSON(t, newApp(&fakeSvc{}, &fakeMW{authFail: true}), "GET", "/api/v1/admin/snipe-plans", "")
		assert.Equal(t, 401, status)
	})
}
