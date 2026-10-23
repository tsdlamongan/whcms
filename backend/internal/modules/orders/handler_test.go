package orders_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/orders"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fakes

// fakeMW injects a fixed identity and mirrors production role/permission
// semantics.
type fakeMW struct {
	identity    httpx.AuthIdentity
	authFail    bool
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

// fakeSvc is a function-field fake of orders.OrderService.
type fakeSvc struct {
	CreateOrderFn  func(ctx context.Context, clientID int64, ip string, in orders.CreateOrderRequest) (*orders.CheckoutResponse, error)
	ListByClientFn func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Order, int64, error)
	ListFn         func(ctx context.Context, p ports.ListParams) ([]domain.Order, int64, error)
	GetFn          func(ctx context.Context, clientID, orderID int64) (*orders.OrderDetail, error)
	AcceptFn       func(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error)
	CancelFn       func(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error)
	MarkFraudFn    func(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error)
}

func (f *fakeSvc) CreateOrder(ctx context.Context, clientID int64, ip string, in orders.CreateOrderRequest) (*orders.CheckoutResponse, error) {
	if f.CreateOrderFn != nil {
		return f.CreateOrderFn(ctx, clientID, ip, in)
	}
	return &orders.CheckoutResponse{}, nil
}

func (f *fakeSvc) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Order, int64, error) {
	if f.ListByClientFn != nil {
		return f.ListByClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (f *fakeSvc) List(ctx context.Context, p ports.ListParams) ([]domain.Order, int64, error) {
	if f.ListFn != nil {
		return f.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (f *fakeSvc) Get(ctx context.Context, clientID, orderID int64) (*orders.OrderDetail, error) {
	if f.GetFn != nil {
		return f.GetFn(ctx, clientID, orderID)
	}
	return &orders.OrderDetail{}, nil
}

func (f *fakeSvc) Accept(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error) {
	if f.AcceptFn != nil {
		return f.AcceptFn(ctx, actorUserID, orderID)
	}
	return &domain.Order{}, nil
}

func (f *fakeSvc) Cancel(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error) {
	if f.CancelFn != nil {
		return f.CancelFn(ctx, actorUserID, orderID)
	}
	return &domain.Order{}, nil
}

func (f *fakeSvc) MarkFraud(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error) {
	if f.MarkFraudFn != nil {
		return f.MarkFraudFn(ctx, actorUserID, orderID)
	}
	return &domain.Order{}, nil
}

func newApp(svc orders.OrderService, mw *fakeMW) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
		return httpx.Fail(c, err)
	}})
	h := orders.NewHandler(svc, mw)
	h.RegisterRoutes(app.Group("/api/v1"))
	return app
}

func clientIdentity() httpx.AuthIdentity {
	return httpx.AuthIdentity{UserID: 3, Role: "client", ClientID: 7}
}

func adminIdentity() httpx.AuthIdentity {
	return httpx.AuthIdentity{UserID: 1, Role: "admin"}
}

func decodeEnvelope(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var env map[string]any
	require.NoError(t, json.NewDecoder(body).Decode(&env))
	return env
}

// Client endpoints

func TestHandlerCreateOrder(t *testing.T) {
	var gotClient int64
	var gotIn orders.CreateOrderRequest
	svc := &fakeSvc{
		CreateOrderFn: func(_ context.Context, clientID int64, ip string, in orders.CreateOrderRequest) (*orders.CheckoutResponse, error) {
			gotClient, gotIn = clientID, in
			return &orders.CheckoutResponse{
				Order: domain.Order{ID: 100, OrderNumber: "ORD-202607-000042", Status: domain.OrderPending},
			}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	body := `{"items":[{"item_type":"product","product_id":10,"cycle":"monthly"}],"coupon_code":"SAVE10"}`
	req := httptest.NewRequest("POST", "/api/v1/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)
	assert.Equal(t, int64(7), gotClient, "clientID comes from the token identity")
	assert.Equal(t, "SAVE10", gotIn.CouponCode)
	require.Len(t, gotIn.Items, 1)
	assert.Equal(t, int64(10), gotIn.Items[0].ProductID)

	env := decodeEnvelope(t, resp.Body)
	data := env["data"].(map[string]any)
	order := data["order"].(map[string]any)
	assert.Equal(t, "ORD-202607-000042", order["order_number"])
}

func TestHandlerCreateOrderBadBody(t *testing.T) {
	app := newApp(&fakeSvc{}, &fakeMW{identity: clientIdentity()})

	req := httptest.NewRequest("POST", "/api/v1/orders", strings.NewReader(`{bad json`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
	env := decodeEnvelope(t, resp.Body)
	assert.Equal(t, "VALIDATION", env["error"].(map[string]any)["code"])
}

func TestHandlerCreateOrderServiceError(t *testing.T) {
	svc := &fakeSvc{
		CreateOrderFn: func(_ context.Context, _ int64, _ string, _ orders.CreateOrderRequest) (*orders.CheckoutResponse, error) {
			return nil, apperr.Forbidden("email verification required before checkout")
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	req := httptest.NewRequest("POST", "/api/v1/orders", strings.NewReader(`{"items":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestHandlerListMine(t *testing.T) {
	var gotParams ports.ListParams
	svc := &fakeSvc{
		ListByClientFn: func(_ context.Context, clientID int64, p ports.ListParams) ([]domain.Order, int64, error) {
			gotParams = p
			return []domain.Order{{ID: 1, ClientID: clientID}}, 1, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/orders?page=2&per_page=10&status=pending", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 2, gotParams.Page)
	assert.Equal(t, 10, gotParams.PerPage)
	assert.Equal(t, "pending", gotParams.Status)

	env := decodeEnvelope(t, resp.Body)
	meta := env["meta"].(map[string]any)
	assert.Equal(t, float64(1), meta["total"])
}

func TestHandlerGetMine(t *testing.T) {
	var gotClient, gotOrder int64
	svc := &fakeSvc{
		GetFn: func(_ context.Context, clientID, orderID int64) (*orders.OrderDetail, error) {
			gotClient, gotOrder = clientID, orderID
			return &orders.OrderDetail{Order: domain.Order{ID: orderID}}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/orders/100", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(7), gotClient)
	assert.Equal(t, int64(100), gotOrder)

	// Bad id.
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/orders/abc", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlerClientRoutesRequireClient(t *testing.T) {
	app := newApp(&fakeSvc{}, &fakeMW{identity: httpx.AuthIdentity{UserID: 1, Role: "admin", ClientID: 0}})

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/orders", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode, "no client profile -> forbidden")
}

func TestHandlerRoutesRequireAuth(t *testing.T) {
	app := newApp(&fakeSvc{}, &fakeMW{authFail: true})

	for _, path := range []string{"/api/v1/orders", "/api/v1/admin/orders"} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		require.NoError(t, err)
		assert.Equal(t, 401, resp.StatusCode, path)
	}
}

// Admin endpoints

func TestHandlerAdminList(t *testing.T) {
	var gotParams ports.ListParams
	svc := &fakeSvc{
		ListFn: func(_ context.Context, p ports.ListParams) ([]domain.Order, int64, error) {
			gotParams = p
			return []domain.Order{{ID: 1}, {ID: 2}}, 2, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: adminIdentity()})

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/orders?search=ORD&status=fraud", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "ORD", gotParams.Search)
	assert.Equal(t, "fraud", gotParams.Status)
}

func TestHandlerAdminListClientFilter(t *testing.T) {
	var gotParams ports.ListParams
	called := false
	svc := &fakeSvc{
		ListFn: func(_ context.Context, p ports.ListParams) ([]domain.Order, int64, error) {
			called, gotParams = true, p
			return nil, 0, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: adminIdentity()})

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/orders?client_id=8", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(8), gotParams.ClientID)

	called = false
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/orders?client_id=-1", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
	assert.False(t, called)
}

func TestHandlerAdminGetUnscoped(t *testing.T) {
	var gotClient int64 = -1
	svc := &fakeSvc{
		GetFn: func(_ context.Context, clientID, orderID int64) (*orders.OrderDetail, error) {
			gotClient = clientID
			return &orders.OrderDetail{Order: domain.Order{ID: orderID}}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: adminIdentity()})

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/orders/55", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(0), gotClient, "admin lookup is unscoped")
}

func TestHandlerAdminActions(t *testing.T) {
	calls := map[string]int64{}
	svc := &fakeSvc{
		AcceptFn: func(_ context.Context, actor, orderID int64) (*domain.Order, error) {
			calls["accept"] = orderID
			return &domain.Order{ID: orderID, Status: domain.OrderActive}, nil
		},
		CancelFn: func(_ context.Context, actor, orderID int64) (*domain.Order, error) {
			calls["cancel"] = orderID
			return &domain.Order{ID: orderID, Status: domain.OrderCancelled}, nil
		},
		MarkFraudFn: func(_ context.Context, actor, orderID int64) (*domain.Order, error) {
			calls["fraud"] = orderID
			return &domain.Order{ID: orderID, Status: domain.OrderFraud}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: adminIdentity()})

	for _, action := range []string{"accept", "cancel", "fraud"} {
		resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/orders/9/"+action, nil))
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode, action)
		assert.Equal(t, int64(9), calls[action])
	}
}

func TestHandlerAdminActionConflict(t *testing.T) {
	svc := &fakeSvc{
		CancelFn: func(_ context.Context, _, _ int64) (*domain.Order, error) {
			return nil, apperr.Conflict("order cannot transition from active to cancelled")
		},
	}
	app := newApp(svc, &fakeMW{identity: adminIdentity()})

	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/orders/9/cancel", nil))
	require.NoError(t, err)
	assert.Equal(t, 409, resp.StatusCode)
}

func TestHandlerAdminRequiresRoleAndPermission(t *testing.T) {
	// Client role hitting admin endpoints.
	app := newApp(&fakeSvc{}, &fakeMW{identity: clientIdentity()})
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/orders", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)

	// Staff without the orders permission.
	staff := &fakeMW{identity: httpx.AuthIdentity{UserID: 2, Role: "staff"}, permissions: map[string]bool{}}
	app = newApp(&fakeSvc{}, staff)
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/orders", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)

	// Staff with the orders permission.
	staff.permissions["orders"] = true
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/orders", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandlerErrorBranches(t *testing.T) {
	failSvc := &fakeSvc{
		ListByClientFn: func(_ context.Context, _ int64, _ ports.ListParams) ([]domain.Order, int64, error) {
			return nil, 0, apperr.Internal(errBoom)
		},
		ListFn: func(_ context.Context, _ ports.ListParams) ([]domain.Order, int64, error) {
			return nil, 0, apperr.Internal(errBoom)
		},
		GetFn: func(_ context.Context, _, _ int64) (*orders.OrderDetail, error) {
			return nil, apperr.NotFound("order")
		},
	}

	app := newApp(failSvc, &fakeMW{identity: clientIdentity()})
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/orders", nil))
	require.NoError(t, err)
	assert.Equal(t, 500, resp.StatusCode)
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/orders/5", nil))
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)

	admin := newApp(failSvc, &fakeMW{identity: adminIdentity()})
	resp, err = admin.Test(httptest.NewRequest("GET", "/api/v1/admin/orders", nil))
	require.NoError(t, err)
	assert.Equal(t, 500, resp.StatusCode)
	resp, err = admin.Test(httptest.NewRequest("GET", "/api/v1/admin/orders/5", nil))
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)
	resp, err = admin.Test(httptest.NewRequest("GET", "/api/v1/admin/orders/zero", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
	resp, err = admin.Test(httptest.NewRequest("POST", "/api/v1/admin/orders/bad/accept", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}
