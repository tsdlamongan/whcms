package clients_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/clients"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fakes

// fakeMW stubs the middleware bundle: it injects a fixed identity and mirrors
// the production role/permission semantics.
type fakeMW struct {
	identity    httpx.AuthIdentity
	authFail    bool
	permissions map[string]bool // staff permission map; admin bypasses
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
		if id.Role == "admin" {
			return c.Next()
		}
		if !m.permissions[module] {
			return apperr.Forbidden("missing permission: " + module)
		}
		return c.Next()
	}
}

func (m *fakeMW) RequireClient() fiber.Handler {
	return func(c fiber.Ctx) error {
		id, _ := httpx.Identity(c)
		if id.ClientID == 0 {
			return apperr.Forbidden("client profile required")
		}
		return c.Next()
	}
}

// fakeClientService is a function-field fake of clients.ClientService.
type fakeClientService struct {
	SearchClientsFn     func(ctx context.Context, f clients.SearchInput) ([]clients.ClientListRow, int64, error)
	CreateClientFn      func(ctx context.Context, actorUserID int64, in clients.CreateClientInput) (*clients.ClientWithEmail, error)
	GetClientDetailFn   func(ctx context.Context, id int64) (*clients.ClientDetail, error)
	UpdateClientFn      func(ctx context.Context, actorUserID, id int64, in clients.UpdateClientInput) (*domain.Client, error)
	SetStatusFn         func(ctx context.Context, actorUserID, id int64, in clients.SetStatusInput) (*domain.Client, error)
	DeleteClientFn      func(ctx context.Context, actorUserID, id int64) error
	GetProfileFn        func(ctx context.Context, clientID int64) (*domain.Client, error)
	UpdateProfileFn     func(ctx context.Context, clientID int64, in clients.UpdateProfileInput) (*domain.Client, error)
	ListContactsFn      func(ctx context.Context, clientID int64) ([]clients.Contact, error)
	CreateContactFn     func(ctx context.Context, actorUserID, clientID int64, in clients.ContactInput) (*clients.Contact, error)
	UpdateContactFn     func(ctx context.Context, actorUserID, clientID, contactID int64, in clients.ContactInput) (*clients.Contact, error)
	DeleteContactFn     func(ctx context.Context, actorUserID, clientID, contactID int64) error
	AdminAdjustCreditFn func(ctx context.Context, actorUserID, clientID int64, in clients.AdjustCreditInput) (int64, error)
	CreditFn            func(ctx context.Context, clientID int64, p ports.ListParams) (*clients.CreditOverview, int64, error)
	DepositFn           func(ctx context.Context, clientID int64, in clients.DepositInput) (*domain.Invoice, error)
	ExportCSVFn         func(ctx context.Context, w io.Writer) error
}

func (f *fakeClientService) SearchClients(ctx context.Context, in clients.SearchInput) ([]clients.ClientListRow, int64, error) {
	if f.SearchClientsFn != nil {
		return f.SearchClientsFn(ctx, in)
	}
	return nil, 0, nil
}

func (f *fakeClientService) CreateClient(ctx context.Context, actor int64, in clients.CreateClientInput) (*clients.ClientWithEmail, error) {
	if f.CreateClientFn != nil {
		return f.CreateClientFn(ctx, actor, in)
	}
	return &clients.ClientWithEmail{}, nil
}

func (f *fakeClientService) GetClientDetail(ctx context.Context, id int64) (*clients.ClientDetail, error) {
	if f.GetClientDetailFn != nil {
		return f.GetClientDetailFn(ctx, id)
	}
	return &clients.ClientDetail{}, nil
}

func (f *fakeClientService) UpdateClient(ctx context.Context, actor, id int64, in clients.UpdateClientInput) (*domain.Client, error) {
	if f.UpdateClientFn != nil {
		return f.UpdateClientFn(ctx, actor, id, in)
	}
	return &domain.Client{ID: id}, nil
}

func (f *fakeClientService) SetStatus(ctx context.Context, actor, id int64, in clients.SetStatusInput) (*domain.Client, error) {
	if f.SetStatusFn != nil {
		return f.SetStatusFn(ctx, actor, id, in)
	}
	return &domain.Client{ID: id}, nil
}

func (f *fakeClientService) DeleteClient(ctx context.Context, actor, id int64) error {
	if f.DeleteClientFn != nil {
		return f.DeleteClientFn(ctx, actor, id)
	}
	return nil
}

func (f *fakeClientService) GetProfile(ctx context.Context, clientID int64) (*domain.Client, error) {
	if f.GetProfileFn != nil {
		return f.GetProfileFn(ctx, clientID)
	}
	return &domain.Client{ID: clientID}, nil
}

func (f *fakeClientService) UpdateProfile(ctx context.Context, clientID int64, in clients.UpdateProfileInput) (*domain.Client, error) {
	if f.UpdateProfileFn != nil {
		return f.UpdateProfileFn(ctx, clientID, in)
	}
	return &domain.Client{ID: clientID}, nil
}

func (f *fakeClientService) ListContacts(ctx context.Context, clientID int64) ([]clients.Contact, error) {
	if f.ListContactsFn != nil {
		return f.ListContactsFn(ctx, clientID)
	}
	return nil, nil
}

func (f *fakeClientService) CreateContact(ctx context.Context, actor, clientID int64, in clients.ContactInput) (*clients.Contact, error) {
	if f.CreateContactFn != nil {
		return f.CreateContactFn(ctx, actor, clientID, in)
	}
	return &clients.Contact{ClientContact: domain.ClientContact{ClientID: clientID}}, nil
}

func (f *fakeClientService) UpdateContact(ctx context.Context, actor, clientID, contactID int64, in clients.ContactInput) (*clients.Contact, error) {
	if f.UpdateContactFn != nil {
		return f.UpdateContactFn(ctx, actor, clientID, contactID, in)
	}
	return &clients.Contact{ClientContact: domain.ClientContact{ID: contactID, ClientID: clientID}}, nil
}

func (f *fakeClientService) DeleteContact(ctx context.Context, actor, clientID, contactID int64) error {
	if f.DeleteContactFn != nil {
		return f.DeleteContactFn(ctx, actor, clientID, contactID)
	}
	return nil
}

func (f *fakeClientService) AdminAdjustCredit(ctx context.Context, actor, clientID int64, in clients.AdjustCreditInput) (int64, error) {
	if f.AdminAdjustCreditFn != nil {
		return f.AdminAdjustCreditFn(ctx, actor, clientID, in)
	}
	return 0, nil
}

func (f *fakeClientService) Credit(ctx context.Context, clientID int64, p ports.ListParams) (*clients.CreditOverview, int64, error) {
	if f.CreditFn != nil {
		return f.CreditFn(ctx, clientID, p)
	}
	return &clients.CreditOverview{Entries: []domain.CreditLedgerEntry{}}, 0, nil
}

func (f *fakeClientService) Deposit(ctx context.Context, clientID int64, in clients.DepositInput) (*domain.Invoice, error) {
	if f.DepositFn != nil {
		return f.DepositFn(ctx, clientID, in)
	}
	return &domain.Invoice{}, nil
}

func (f *fakeClientService) ExportCSV(ctx context.Context, w io.Writer) error {
	if f.ExportCSVFn != nil {
		return f.ExportCSVFn(ctx, w)
	}
	return nil
}

// Compile-time check that the fake matches the handler's service surface.
var _ clients.ClientService = (*fakeClientService)(nil)

// Harness

func newApp(svc clients.ClientService, mw clients.Middlewares) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error { return httpx.Fail(c, err) },
	})
	clients.NewHandler(svc, mw).RegisterRoutes(app.Group("/api/v1"))
	return app
}

func adminMW() *fakeMW {
	return &fakeMW{identity: httpx.AuthIdentity{UserID: 1, Role: "admin"}}
}

func clientMW() *fakeMW {
	return &fakeMW{identity: httpx.AuthIdentity{UserID: 10, Role: "client", ClientID: 5}}
}

func doJSON(t *testing.T, app *fiber.App, method, path, body string) (*fiber.App, int, httpx.Envelope) {
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
	var env httpx.Envelope
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &env)
	}
	return app, resp.StatusCode, env
}

// Auth wiring

func TestRouteGuards(t *testing.T) {
	t.Run("unauthenticated gets 401", func(t *testing.T) {
		app := newApp(&fakeClientService{}, &fakeMW{authFail: true})
		_, status, _ := doJSON(t, app, "GET", "/api/v1/account/profile", "")
		assert.Equal(t, 401, status)
		_, status, _ = doJSON(t, app, "GET", "/api/v1/admin/clients/", "")
		assert.Equal(t, 401, status)
	})

	t.Run("client role cannot reach admin routes", func(t *testing.T) {
		app := newApp(&fakeClientService{}, clientMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/", "")
		assert.Equal(t, 403, status)
	})

	t.Run("staff without clients permission is forbidden", func(t *testing.T) {
		mw := &fakeMW{identity: httpx.AuthIdentity{UserID: 2, Role: "staff"}, permissions: map[string]bool{}}
		app := newApp(&fakeClientService{}, mw)
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/", "")
		assert.Equal(t, 403, status)
	})

	t.Run("staff with clients permission allowed", func(t *testing.T) {
		mw := &fakeMW{
			identity:    httpx.AuthIdentity{UserID: 2, Role: "staff"},
			permissions: map[string]bool{"clients": true},
		}
		app := newApp(&fakeClientService{}, mw)
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/", "")
		assert.Equal(t, 200, status)
	})

	t.Run("staff/admin without client profile cannot use account routes", func(t *testing.T) {
		app := newApp(&fakeClientService{}, adminMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/account/profile", "")
		assert.Equal(t, 403, status)
	})
}

// Client self-service endpoints

func TestAccountEndpoints(t *testing.T) {
	t.Run("get profile uses identity client id", func(t *testing.T) {
		svc := &fakeClientService{GetProfileFn: func(_ context.Context, clientID int64) (*domain.Client, error) {
			assert.Equal(t, int64(5), clientID)
			return &domain.Client{ID: clientID, FirstName: "Budi"}, nil
		}}
		app := newApp(svc, clientMW())
		_, status, env := doJSON(t, app, "GET", "/api/v1/account/profile", "")
		assert.Equal(t, 200, status)
		require.NotNil(t, env.Data)
	})

	t.Run("get profile redacts admin notes", func(t *testing.T) {
		svc := &fakeClientService{GetProfileFn: func(_ context.Context, clientID int64) (*domain.Client, error) {
			return &domain.Client{ID: clientID, FirstName: "Budi", NotesAdmin: "flagged for fraud review"}, nil
		}}
		app := newApp(svc, clientMW())
		_, status, env := doJSON(t, app, "GET", "/api/v1/account/profile", "")
		assert.Equal(t, 200, status)
		body, ok := env.Data.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "", body["notes_admin"], "client must never see the admin-only note")
	})

	t.Run("patch profile invalid body", func(t *testing.T) {
		app := newApp(&fakeClientService{}, clientMW())
		_, status, _ := doJSON(t, app, "PATCH", "/api/v1/account/profile", "{not json")
		assert.Equal(t, 422, status)
	})

	t.Run("patch profile forwards fields", func(t *testing.T) {
		var got clients.UpdateProfileInput
		svc := &fakeClientService{UpdateProfileFn: func(_ context.Context, clientID int64, in clients.UpdateProfileInput) (*domain.Client, error) {
			got = in
			return &domain.Client{ID: clientID}, nil
		}}
		app := newApp(svc, clientMW())
		_, status, _ := doJSON(t, app, "PATCH", "/api/v1/account/profile", `{"city":"Lamongan"}`)
		assert.Equal(t, 200, status)
		require.NotNil(t, got.City)
		assert.Equal(t, "Lamongan", *got.City)
	})

	t.Run("patch profile redacts admin notes", func(t *testing.T) {
		svc := &fakeClientService{UpdateProfileFn: func(_ context.Context, clientID int64, _ clients.UpdateProfileInput) (*domain.Client, error) {
			return &domain.Client{ID: clientID, NotesAdmin: "flagged for fraud review"}, nil
		}}
		app := newApp(svc, clientMW())
		_, status, env := doJSON(t, app, "PATCH", "/api/v1/account/profile", `{"city":"Lamongan"}`)
		assert.Equal(t, 200, status)
		body, ok := env.Data.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "", body["notes_admin"], "client must never see the admin-only note")
	})

	t.Run("credit returns meta and balance", func(t *testing.T) {
		svc := &fakeClientService{CreditFn: func(_ context.Context, clientID int64, p ports.ListParams) (*clients.CreditOverview, int64, error) {
			assert.Equal(t, int64(5), clientID)
			assert.Equal(t, 2, p.Page)
			return &clients.CreditOverview{Balance: 75000}, 30, nil
		}}
		app := newApp(svc, clientMW())
		_, status, env := doJSON(t, app, "GET", "/api/v1/account/credit?page=2", "")
		assert.Equal(t, 200, status)
		require.NotNil(t, env.Meta)
		assert.Equal(t, int64(30), env.Meta.Total)
	})

	t.Run("deposit created", func(t *testing.T) {
		svc := &fakeClientService{DepositFn: func(_ context.Context, clientID int64, in clients.DepositInput) (*domain.Invoice, error) {
			assert.Equal(t, int64(50000), in.Amount)
			return &domain.Invoice{ID: 88}, nil
		}}
		app := newApp(svc, clientMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/account/credit/deposit", `{"amount":50000}`)
		assert.Equal(t, 201, status)
	})

	t.Run("deposit invalid body", func(t *testing.T) {
		app := newApp(&fakeClientService{}, clientMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/account/credit/deposit", "oops")
		assert.Equal(t, 422, status)
	})

	t.Run("deposit service error maps", func(t *testing.T) {
		svc := &fakeClientService{DepositFn: func(context.Context, int64, clients.DepositInput) (*domain.Invoice, error) {
			return nil, apperr.Validation("amount too small")
		}}
		app := newApp(svc, clientMW())
		_, status, env := doJSON(t, app, "POST", "/api/v1/account/credit/deposit", `{"amount":1}`)
		assert.Equal(t, 422, status)
		require.NotNil(t, env.Error)
		assert.Equal(t, "VALIDATION", env.Error.Code)
	})

	t.Run("contacts crud", func(t *testing.T) {
		var createIn clients.ContactInput
		svc := &fakeClientService{
			ListContactsFn: func(_ context.Context, clientID int64) ([]clients.Contact, error) {
				return []clients.Contact{{
					ClientContact: domain.ClientContact{ID: 1, ClientID: clientID},
					Permissions:   json.RawMessage(`{"invoices":true}`),
				}}, nil
			},
			CreateContactFn: func(_ context.Context, _, clientID int64, in clients.ContactInput) (*clients.Contact, error) {
				createIn = in
				return &clients.Contact{ClientContact: domain.ClientContact{ID: 2, ClientID: clientID}}, nil
			},
			DeleteContactFn: func(_ context.Context, actor, clientID, contactID int64) error {
				assert.Equal(t, int64(10), actor)
				assert.Equal(t, int64(5), clientID)
				assert.Equal(t, int64(7), contactID)
				return nil
			},
		}
		app := newApp(svc, clientMW())
		_, status, env := doJSON(t, app, "GET", "/api/v1/account/contacts", "")
		assert.Equal(t, 200, status)
		rows, ok := env.Data.([]any)
		require.True(t, ok)
		require.Len(t, rows, 1)
		row, ok := rows[0].(map[string]any)
		require.True(t, ok)
		perms, ok := row["permissions"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, true, perms["invoices"])
		_, status, _ = doJSON(t, app, "POST", "/api/v1/account/contacts",
			`{"first_name":"Siti","permissions":{"invoices":true,"tickets":false}}`)
		assert.Equal(t, 201, status)
		assert.Equal(t, map[string]bool{"invoices": true, "tickets": false}, createIn.Permissions)
		_, status, _ = doJSON(t, app, "PATCH", "/api/v1/account/contacts/7", `{"first_name":"Siti"}`)
		assert.Equal(t, 200, status)
		_, status, _ = doJSON(t, app, "DELETE", "/api/v1/account/contacts/7", "")
		assert.Equal(t, 204, status)
		_, status, _ = doJSON(t, app, "PATCH", "/api/v1/account/contacts/abc", `{"first_name":"Siti"}`)
		assert.Equal(t, 422, status)
	})
}

// Admin endpoints

func TestAdminEndpoints(t *testing.T) {
	t.Run("list parses filters", func(t *testing.T) {
		var got clients.SearchInput
		svc := &fakeClientService{SearchClientsFn: func(_ context.Context, f clients.SearchInput) ([]clients.ClientListRow, int64, error) {
			got = f
			return []clients.ClientListRow{{ID: 1}}, 1, nil
		}}
		app := newApp(svc, adminMW())
		_, status, env := doJSON(t, app, "GET",
			"/api/v1/admin/clients/?search=budi&status=active&has_product=true&page=2&per_page=10", "")
		assert.Equal(t, 200, status)
		assert.Equal(t, "budi", got.Search)
		assert.Equal(t, "active", got.Status)
		assert.True(t, got.HasProduct)
		assert.Equal(t, 2, got.Page)
		require.NotNil(t, env.Meta)
		assert.Equal(t, int64(1), env.Meta.Total)
	})

	t.Run("list parses the ids filter", func(t *testing.T) {
		var got clients.SearchInput
		svc := &fakeClientService{SearchClientsFn: func(_ context.Context, f clients.SearchInput) ([]clients.ClientListRow, int64, error) {
			got = f
			return nil, 0, nil
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/?ids=3,%204,5", "")
		assert.Equal(t, 200, status)
		assert.Equal(t, []int64{3, 4, 5}, got.IDs)
	})

	t.Run("list rejects malformed or oversized ids", func(t *testing.T) {
		app := newApp(&fakeClientService{}, adminMW())
		for _, q := range []string{"3,abc", "0", "-2", "1,,2"} {
			_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/?ids="+q, "")
			assert.Equal(t, 422, status, q)
		}
		many := strings.Repeat("1,", 1000) + "1" // 1001 ids
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/?ids="+many, "")
		assert.Equal(t, 422, status)
	})

	t.Run("list invalid has_product flag", func(t *testing.T) {
		app := newApp(&fakeClientService{}, adminMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/?has_product=banana", "")
		assert.Equal(t, 422, status)
	})

	t.Run("create returns 201 with actor", func(t *testing.T) {
		svc := &fakeClientService{CreateClientFn: func(_ context.Context, actor int64, in clients.CreateClientInput) (*clients.ClientWithEmail, error) {
			assert.Equal(t, int64(1), actor)
			assert.Equal(t, "budi@example.com", in.Email)
			return &clients.ClientWithEmail{Client: domain.Client{ID: 7}, Email: in.Email}, nil
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/admin/clients/",
			`{"email":"budi@example.com","password":"supersecret","first_name":"Budi","last_name":"Santoso"}`)
		assert.Equal(t, 201, status)
	})

	t.Run("create invalid body", func(t *testing.T) {
		app := newApp(&fakeClientService{}, adminMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/admin/clients/", "{")
		assert.Equal(t, 422, status)
	})

	t.Run("export streams csv", func(t *testing.T) {
		svc := &fakeClientService{ExportCSVFn: func(_ context.Context, w io.Writer) error {
			_, err := w.Write([]byte("id,email\n1,budi@example.com\n"))
			return err
		}}
		app := newApp(svc, adminMW())
		resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/clients/export", nil))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/csv")
		assert.Contains(t, resp.Header.Get("Content-Disposition"), "clients.csv")
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "id,email\n1,budi@example.com\n", string(body))
	})

	t.Run("detail parses id", func(t *testing.T) {
		svc := &fakeClientService{GetClientDetailFn: func(_ context.Context, id int64) (*clients.ClientDetail, error) {
			assert.Equal(t, int64(5), id)
			return &clients.ClientDetail{Email: "budi@example.com"}, nil
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/5", "")
		assert.Equal(t, 200, status)
		_, status, _ = doJSON(t, app, "GET", "/api/v1/admin/clients/abc", "")
		assert.Equal(t, 422, status)
	})

	t.Run("detail not found maps to 404", func(t *testing.T) {
		svc := &fakeClientService{GetClientDetailFn: func(context.Context, int64) (*clients.ClientDetail, error) {
			return nil, apperr.NotFound("client")
		}}
		app := newApp(svc, adminMW())
		_, status, env := doJSON(t, app, "GET", "/api/v1/admin/clients/9", "")
		assert.Equal(t, 404, status)
		require.NotNil(t, env.Error)
		assert.Equal(t, "NOT_FOUND", env.Error.Code)
	})

	t.Run("update patch", func(t *testing.T) {
		svc := &fakeClientService{UpdateClientFn: func(_ context.Context, actor, id int64, in clients.UpdateClientInput) (*domain.Client, error) {
			assert.Equal(t, int64(1), actor)
			require.NotNil(t, in.NotesAdmin)
			assert.Equal(t, "vip", *in.NotesAdmin)
			return &domain.Client{ID: id}, nil
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "PATCH", "/api/v1/admin/clients/5", `{"notes_admin":"vip"}`)
		assert.Equal(t, 200, status)
	})

	t.Run("delete returns 204", func(t *testing.T) {
		app := newApp(&fakeClientService{}, adminMW())
		_, status, _ := doJSON(t, app, "DELETE", "/api/v1/admin/clients/5", "")
		assert.Equal(t, 204, status)
	})

	t.Run("set status", func(t *testing.T) {
		svc := &fakeClientService{SetStatusFn: func(_ context.Context, actor, id int64, in clients.SetStatusInput) (*domain.Client, error) {
			assert.Equal(t, "inactive", in.Status)
			return &domain.Client{ID: id, Status: domain.ClientInactive}, nil
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/admin/clients/5/status", `{"status":"inactive"}`)
		assert.Equal(t, 200, status)
	})

	t.Run("admin credit page", func(t *testing.T) {
		svc := &fakeClientService{CreditFn: func(_ context.Context, clientID int64, p ports.ListParams) (*clients.CreditOverview, int64, error) {
			assert.Equal(t, int64(5), clientID)
			return &clients.CreditOverview{Balance: 10}, 1, nil
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/5/credit", "")
		assert.Equal(t, 200, status)
	})

	t.Run("adjust credit returns balance", func(t *testing.T) {
		svc := &fakeClientService{AdminAdjustCreditFn: func(_ context.Context, actor, clientID int64, in clients.AdjustCreditInput) (int64, error) {
			assert.Equal(t, int64(-5000), in.Delta)
			return 45000, nil
		}}
		app := newApp(svc, adminMW())
		_, status, env := doJSON(t, app, "POST", "/api/v1/admin/clients/5/credit",
			`{"delta":-5000,"reason":"correction"}`)
		assert.Equal(t, 200, status)
		data, ok := env.Data.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(45000), data["balance"])
	})

	t.Run("adjust credit conflict maps to 409", func(t *testing.T) {
		svc := &fakeClientService{AdminAdjustCreditFn: func(context.Context, int64, int64, clients.AdjustCreditInput) (int64, error) {
			return 0, apperr.Conflict("insufficient credit balance")
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/admin/clients/5/credit",
			`{"delta":-5000,"reason":"correction"}`)
		assert.Equal(t, 409, status)
	})

	t.Run("admin contacts crud", func(t *testing.T) {
		svc := &fakeClientService{
			UpdateContactFn: func(_ context.Context, actor, clientID, contactID int64, in clients.ContactInput) (*clients.Contact, error) {
				assert.Equal(t, int64(1), actor)
				assert.Equal(t, int64(5), clientID)
				assert.Equal(t, int64(7), contactID)
				return &clients.Contact{ClientContact: domain.ClientContact{ID: contactID}}, nil
			},
		}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/5/contacts", "")
		assert.Equal(t, 200, status)
		_, status, _ = doJSON(t, app, "POST", "/api/v1/admin/clients/5/contacts", `{"first_name":"Siti"}`)
		assert.Equal(t, 201, status)
		_, status, _ = doJSON(t, app, "PATCH", "/api/v1/admin/clients/5/contacts/7", `{"first_name":"Siti"}`)
		assert.Equal(t, 200, status)
		_, status, _ = doJSON(t, app, "DELETE", "/api/v1/admin/clients/5/contacts/7", "")
		assert.Equal(t, 204, status)
	})
}

// Client self-service endpoints: remaining error branches

func TestAccountEndpointsErrorPaths(t *testing.T) {
	t.Run("get profile service error maps", func(t *testing.T) {
		svc := &fakeClientService{GetProfileFn: func(context.Context, int64) (*domain.Client, error) {
			return nil, apperr.NotFound("client")
		}}
		app := newApp(svc, clientMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/account/profile", "")
		assert.Equal(t, 404, status)
	})

	t.Run("patch profile service error maps", func(t *testing.T) {
		svc := &fakeClientService{UpdateProfileFn: func(context.Context, int64, clients.UpdateProfileInput) (*domain.Client, error) {
			return nil, apperr.Validation("bad address")
		}}
		app := newApp(svc, clientMW())
		_, status, _ := doJSON(t, app, "PATCH", "/api/v1/account/profile", `{"city":"Lamongan"}`)
		assert.Equal(t, 422, status)
	})

	t.Run("credit service error maps", func(t *testing.T) {
		svc := &fakeClientService{CreditFn: func(context.Context, int64, ports.ListParams) (*clients.CreditOverview, int64, error) {
			return nil, 0, apperr.NotFound("client")
		}}
		app := newApp(svc, clientMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/account/credit", "")
		assert.Equal(t, 404, status)
	})

	t.Run("list contacts service error maps", func(t *testing.T) {
		svc := &fakeClientService{ListContactsFn: func(context.Context, int64) ([]clients.Contact, error) {
			return nil, apperr.Internal(assert.AnError)
		}}
		app := newApp(svc, clientMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/account/contacts", "")
		assert.Equal(t, 500, status)
	})

	t.Run("create contact invalid body and service error", func(t *testing.T) {
		svc := &fakeClientService{CreateContactFn: func(context.Context, int64, int64, clients.ContactInput) (*clients.Contact, error) {
			return nil, apperr.Validation("first_name required")
		}}
		app := newApp(svc, clientMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/account/contacts", "{bad")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "POST", "/api/v1/account/contacts", `{"first_name":"Siti"}`)
		assert.Equal(t, 422, status)
	})

	t.Run("update contact invalid body and service error", func(t *testing.T) {
		svc := &fakeClientService{UpdateContactFn: func(context.Context, int64, int64, int64, clients.ContactInput) (*clients.Contact, error) {
			return nil, apperr.NotFound("contact")
		}}
		app := newApp(svc, clientMW())
		_, status, _ := doJSON(t, app, "PATCH", "/api/v1/account/contacts/7", "{bad")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "PATCH", "/api/v1/account/contacts/7", `{"first_name":"Siti"}`)
		assert.Equal(t, 404, status)
	})

	t.Run("delete contact invalid id and service error", func(t *testing.T) {
		svc := &fakeClientService{DeleteContactFn: func(context.Context, int64, int64, int64) error {
			return apperr.NotFound("contact")
		}}
		app := newApp(svc, clientMW())
		_, status, _ := doJSON(t, app, "DELETE", "/api/v1/account/contacts/abc", "")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "DELETE", "/api/v1/account/contacts/7", "")
		assert.Equal(t, 404, status)
	})
}

// Admin endpoints: remaining error branches (invalid :id/:contactId params,
// invalid bodies and service-error propagation not already exercised above)

func TestAdminEndpointsErrorPaths(t *testing.T) {
	t.Run("list service error maps", func(t *testing.T) {
		svc := &fakeClientService{SearchClientsFn: func(context.Context, clients.SearchInput) ([]clients.ClientListRow, int64, error) {
			return nil, 0, apperr.Internal(assert.AnError)
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/", "")
		assert.Equal(t, 500, status)
	})

	t.Run("create service error maps", func(t *testing.T) {
		svc := &fakeClientService{CreateClientFn: func(context.Context, int64, clients.CreateClientInput) (*clients.ClientWithEmail, error) {
			return nil, apperr.Conflict("email already in use")
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/admin/clients/",
			`{"email":"budi@example.com","password":"supersecret","first_name":"Budi","last_name":"Santoso"}`)
		assert.Equal(t, 409, status)
	})

	t.Run("update invalid id, invalid body and service error", func(t *testing.T) {
		svc := &fakeClientService{UpdateClientFn: func(context.Context, int64, int64, clients.UpdateClientInput) (*domain.Client, error) {
			return nil, apperr.NotFound("client")
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "PATCH", "/api/v1/admin/clients/abc", `{"notes_admin":"vip"}`)
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "PATCH", "/api/v1/admin/clients/5", "{bad")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "PATCH", "/api/v1/admin/clients/5", `{"notes_admin":"vip"}`)
		assert.Equal(t, 404, status)
	})

	t.Run("delete invalid id and service error", func(t *testing.T) {
		svc := &fakeClientService{DeleteClientFn: func(context.Context, int64, int64) error {
			return apperr.NotFound("client")
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "DELETE", "/api/v1/admin/clients/abc", "")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "DELETE", "/api/v1/admin/clients/5", "")
		assert.Equal(t, 404, status)
	})

	t.Run("set status invalid id, invalid body and service error", func(t *testing.T) {
		svc := &fakeClientService{SetStatusFn: func(context.Context, int64, int64, clients.SetStatusInput) (*domain.Client, error) {
			return nil, apperr.Validation("invalid status filter")
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/admin/clients/abc/status", `{"status":"inactive"}`)
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "POST", "/api/v1/admin/clients/5/status", "{bad")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "POST", "/api/v1/admin/clients/5/status", `{"status":"inactive"}`)
		assert.Equal(t, 422, status)
	})

	t.Run("admin credit invalid id and service error", func(t *testing.T) {
		svc := &fakeClientService{CreditFn: func(context.Context, int64, ports.ListParams) (*clients.CreditOverview, int64, error) {
			return nil, 0, apperr.NotFound("client")
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/abc/credit", "")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "GET", "/api/v1/admin/clients/5/credit", "")
		assert.Equal(t, 404, status)
	})

	t.Run("adjust credit invalid id and invalid body", func(t *testing.T) {
		app := newApp(&fakeClientService{}, adminMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/admin/clients/abc/credit", `{"delta":-5000,"reason":"x"}`)
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "POST", "/api/v1/admin/clients/5/credit", "{bad")
		assert.Equal(t, 422, status)
	})

	t.Run("admin list contacts invalid id and service error", func(t *testing.T) {
		svc := &fakeClientService{ListContactsFn: func(context.Context, int64) ([]clients.Contact, error) {
			return nil, apperr.NotFound("client")
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "GET", "/api/v1/admin/clients/abc/contacts", "")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "GET", "/api/v1/admin/clients/5/contacts", "")
		assert.Equal(t, 404, status)
	})

	t.Run("admin create contact invalid id, invalid body and service error", func(t *testing.T) {
		svc := &fakeClientService{CreateContactFn: func(context.Context, int64, int64, clients.ContactInput) (*clients.Contact, error) {
			return nil, apperr.NotFound("client")
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "POST", "/api/v1/admin/clients/abc/contacts", `{"first_name":"Siti"}`)
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "POST", "/api/v1/admin/clients/5/contacts", "{bad")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "POST", "/api/v1/admin/clients/5/contacts", `{"first_name":"Siti"}`)
		assert.Equal(t, 404, status)
	})

	t.Run("admin update contact invalid id, invalid contact id, invalid body and service error", func(t *testing.T) {
		svc := &fakeClientService{UpdateContactFn: func(context.Context, int64, int64, int64, clients.ContactInput) (*clients.Contact, error) {
			return nil, apperr.NotFound("contact")
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "PATCH", "/api/v1/admin/clients/abc/contacts/7", `{"first_name":"Siti"}`)
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "PATCH", "/api/v1/admin/clients/5/contacts/abc", `{"first_name":"Siti"}`)
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "PATCH", "/api/v1/admin/clients/5/contacts/7", "{bad")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "PATCH", "/api/v1/admin/clients/5/contacts/7", `{"first_name":"Siti"}`)
		assert.Equal(t, 404, status)
	})

	t.Run("admin delete contact invalid id, invalid contact id and service error", func(t *testing.T) {
		svc := &fakeClientService{DeleteContactFn: func(context.Context, int64, int64, int64) error {
			return apperr.NotFound("contact")
		}}
		app := newApp(svc, adminMW())
		_, status, _ := doJSON(t, app, "DELETE", "/api/v1/admin/clients/abc/contacts/7", "")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "DELETE", "/api/v1/admin/clients/5/contacts/abc", "")
		assert.Equal(t, 422, status)
		_, status, _ = doJSON(t, app, "DELETE", "/api/v1/admin/clients/5/contacts/7", "")
		assert.Equal(t, 404, status)
	})
}

// Ensure the real middleware type still satisfies the handler contract.
var _ clients.Middlewares = (*transporthttp.Middleware)(nil)
