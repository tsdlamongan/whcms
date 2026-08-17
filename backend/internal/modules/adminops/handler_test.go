package adminops_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/adminops"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fakes

// fakeMW stubs the middleware bundle: it injects a fixed identity and
// mirrors the production role/permission semantics.
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

// fakeAdminService is a function-field fake of adminops.AdminService.
type fakeAdminService struct {
	DashboardFn               func(ctx context.Context, force bool) (*adminops.DashboardData, error)
	RevenueReportFn           func(ctx context.Context, from, to time.Time, groupBy string) (*adminops.RevenueReportData, error)
	OrdersReportFn            func(ctx context.Context, from, to time.Time) (*adminops.OrdersReportData, error)
	ServicesReportFn          func(ctx context.Context) (*adminops.ServicesReportData, error)
	ListStaffFn               func(ctx context.Context, p ports.ListParams) ([]domain.User, int64, error)
	OnlineStaffFn             func(ctx context.Context) ([]ports.PresenceEntry, error)
	CreateStaffFn             func(ctx context.Context, actorUserID int64, in adminops.CreateStaffInput) (*domain.User, error)
	GetStaffFn                func(ctx context.Context, id int64) (*domain.User, error)
	UpdateStaffFn             func(ctx context.Context, actorUserID, id int64, in adminops.UpdateStaffInput) (*domain.User, error)
	DeactivateStaffFn         func(ctx context.Context, actorUserID, id int64) (*domain.User, error)
	AuditLogsFn               func(ctx context.Context, f adminops.AuditLogFilter) ([]domain.AuditLog, int64, error)
	EmailLogsFn               func(ctx context.Context, p ports.ListParams) ([]domain.EmailLogEntry, int64, error)
	IntegrationLogsFn         func(ctx context.Context, f adminops.IntegrationLogFilter) ([]domain.IntegrationLog, int64, error)
	PendingModuleActionsFn    func(ctx context.Context, f ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error)
	RetryModuleActionFn       func(ctx context.Context, actorUserID int64, queue, id string) error
	DeleteModuleActionFn      func(ctx context.Context, actorUserID int64, queue, id string) error
	DismissAllModuleActionsFn func(ctx context.Context, actorUserID int64, f ports.ModuleActionFilter) (int, error)
	GatewaysFn                func(ctx context.Context) (*adminops.GatewaysConfig, error)
	UpdateGatewaysFn          func(ctx context.Context, actorUserID int64, in adminops.UpdateGatewaysInput) (*adminops.GatewaysConfig, error)
	UpdateManualGatewayFn     func(ctx context.Context, actorUserID int64, in adminops.UpdateManualGatewayInput) (*adminops.ManualGatewayConfig, error)
}

func (f *fakeAdminService) Dashboard(ctx context.Context, force bool) (*adminops.DashboardData, error) {
	if f.DashboardFn != nil {
		return f.DashboardFn(ctx, force)
	}
	return &adminops.DashboardData{}, nil
}

func (f *fakeAdminService) RevenueReport(ctx context.Context, from, to time.Time, groupBy string) (*adminops.RevenueReportData, error) {
	if f.RevenueReportFn != nil {
		return f.RevenueReportFn(ctx, from, to, groupBy)
	}
	return &adminops.RevenueReportData{}, nil
}

func (f *fakeAdminService) OrdersReport(ctx context.Context, from, to time.Time) (*adminops.OrdersReportData, error) {
	if f.OrdersReportFn != nil {
		return f.OrdersReportFn(ctx, from, to)
	}
	return &adminops.OrdersReportData{}, nil
}

func (f *fakeAdminService) ServicesReport(ctx context.Context) (*adminops.ServicesReportData, error) {
	if f.ServicesReportFn != nil {
		return f.ServicesReportFn(ctx)
	}
	return &adminops.ServicesReportData{}, nil
}

func (f *fakeAdminService) ListStaff(ctx context.Context, p ports.ListParams) ([]domain.User, int64, error) {
	if f.ListStaffFn != nil {
		return f.ListStaffFn(ctx, p)
	}
	return nil, 0, nil
}

func (f *fakeAdminService) OnlineStaff(ctx context.Context) ([]ports.PresenceEntry, error) {
	if f.OnlineStaffFn != nil {
		return f.OnlineStaffFn(ctx)
	}
	return nil, nil
}

func (f *fakeAdminService) CreateStaff(ctx context.Context, actorUserID int64, in adminops.CreateStaffInput) (*domain.User, error) {
	if f.CreateStaffFn != nil {
		return f.CreateStaffFn(ctx, actorUserID, in)
	}
	return &domain.User{}, nil
}

func (f *fakeAdminService) GetStaff(ctx context.Context, id int64) (*domain.User, error) {
	if f.GetStaffFn != nil {
		return f.GetStaffFn(ctx, id)
	}
	return &domain.User{ID: id}, nil
}

func (f *fakeAdminService) UpdateStaff(ctx context.Context, actorUserID, id int64, in adminops.UpdateStaffInput) (*domain.User, error) {
	if f.UpdateStaffFn != nil {
		return f.UpdateStaffFn(ctx, actorUserID, id, in)
	}
	return &domain.User{ID: id}, nil
}

func (f *fakeAdminService) DeactivateStaff(ctx context.Context, actorUserID, id int64) (*domain.User, error) {
	if f.DeactivateStaffFn != nil {
		return f.DeactivateStaffFn(ctx, actorUserID, id)
	}
	return &domain.User{ID: id}, nil
}

func (f *fakeAdminService) AuditLogs(ctx context.Context, flt adminops.AuditLogFilter) ([]domain.AuditLog, int64, error) {
	if f.AuditLogsFn != nil {
		return f.AuditLogsFn(ctx, flt)
	}
	return nil, 0, nil
}

func (f *fakeAdminService) EmailLogs(ctx context.Context, p ports.ListParams) ([]domain.EmailLogEntry, int64, error) {
	if f.EmailLogsFn != nil {
		return f.EmailLogsFn(ctx, p)
	}
	return nil, 0, nil
}

func (f *fakeAdminService) IntegrationLogs(ctx context.Context, flt adminops.IntegrationLogFilter) ([]domain.IntegrationLog, int64, error) {
	if f.IntegrationLogsFn != nil {
		return f.IntegrationLogsFn(ctx, flt)
	}
	return nil, 0, nil
}

func (f *fakeAdminService) PendingModuleActions(ctx context.Context, flt ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error) {
	if f.PendingModuleActionsFn != nil {
		return f.PendingModuleActionsFn(ctx, flt)
	}
	return nil, 0, nil
}

func (f *fakeAdminService) RetryModuleAction(ctx context.Context, actorUserID int64, queue, id string) error {
	if f.RetryModuleActionFn != nil {
		return f.RetryModuleActionFn(ctx, actorUserID, queue, id)
	}
	return nil
}

func (f *fakeAdminService) DeleteModuleAction(ctx context.Context, actorUserID int64, queue, id string) error {
	if f.DeleteModuleActionFn != nil {
		return f.DeleteModuleActionFn(ctx, actorUserID, queue, id)
	}
	return nil
}

func (f *fakeAdminService) DismissAllModuleActions(ctx context.Context, actorUserID int64, filter ports.ModuleActionFilter) (int, error) {
	if f.DismissAllModuleActionsFn != nil {
		return f.DismissAllModuleActionsFn(ctx, actorUserID, filter)
	}
	return 0, nil
}

func (f *fakeAdminService) Gateways(ctx context.Context) (*adminops.GatewaysConfig, error) {
	if f.GatewaysFn != nil {
		return f.GatewaysFn(ctx)
	}
	return &adminops.GatewaysConfig{}, nil
}

func (f *fakeAdminService) UpdateGateways(ctx context.Context, actorUserID int64, in adminops.UpdateGatewaysInput) (*adminops.GatewaysConfig, error) {
	if f.UpdateGatewaysFn != nil {
		return f.UpdateGatewaysFn(ctx, actorUserID, in)
	}
	return &adminops.GatewaysConfig{}, nil
}

func (f *fakeAdminService) UpdateManualGateway(ctx context.Context, actorUserID int64, in adminops.UpdateManualGatewayInput) (*adminops.ManualGatewayConfig, error) {
	if f.UpdateManualGatewayFn != nil {
		return f.UpdateManualGatewayFn(ctx, actorUserID, in)
	}
	return &adminops.ManualGatewayConfig{}, nil
}

// Harness

var discardLog = slog.New(slog.NewTextHandler(io.Discard, nil))

func newApp(svc adminops.AdminService, mw adminops.Middlewares) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: transporthttp.ErrorHandler(discardLog)})
	adminops.NewHandler(svc, mw).RegisterRoutes(app.Group("/api/v1"))
	return app
}

func adminMW() *fakeMW {
	return &fakeMW{identity: httpx.AuthIdentity{UserID: 42, Role: "admin"}}
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Meta  *httpx.Meta     `json:"meta"`
	Error *struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Details []apperr.FieldError `json:"details"`
	} `json:"error"`
}

func readEnvelope(t *testing.T, body io.Reader) envelope {
	t.Helper()
	var env envelope
	require.NoError(t, json.NewDecoder(body).Decode(&env))
	return env
}

// Tests

func TestDashboardEndpoint(t *testing.T) {
	var gotForce bool
	svc := &fakeAdminService{
		DashboardFn: func(_ context.Context, force bool) (*adminops.DashboardData, error) {
			gotForce = force
			return &adminops.DashboardData{Stats: ports.DashboardStats{ClientsActive: 4}}, nil
		},
	}
	app := newApp(svc, adminMW())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/dashboard", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := readEnvelope(t, resp.Body)
	require.Nil(t, env.Error)
	assert.Contains(t, string(env.Data), `"clients_active":4`)
	assert.False(t, gotForce, "plain GET must not force-bypass the cache")
}

func TestDashboardEndpointRefreshQueryForcesCacheBypass(t *testing.T) {
	var gotForce bool
	svc := &fakeAdminService{
		DashboardFn: func(_ context.Context, force bool) (*adminops.DashboardData, error) {
			gotForce = force
			return &adminops.DashboardData{}, nil
		},
	}
	app := newApp(svc, adminMW())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/dashboard?refresh=1", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.True(t, gotForce, "?refresh=1 must force-bypass the cache")
}

func TestOnlineStaffEndpoint(t *testing.T) {
	svc := &fakeAdminService{
		OnlineStaffFn: func(context.Context) ([]ports.PresenceEntry, error) {
			return []ports.PresenceEntry{{UserID: 1, Email: "admin@example.com", Role: "admin"}}, nil
		},
	}

	// Admin can see it.
	app := newApp(svc, adminMW())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/staff/online", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := readEnvelope(t, resp.Body)
	require.Nil(t, env.Error)
	assert.Contains(t, string(env.Data), "admin@example.com")

	// Staff can see it too - unlike the admin-only /staff CRUD group below,
	// this widget must be visible to every logged-in staff member.
	app = newApp(svc, &fakeMW{identity: httpx.AuthIdentity{UserID: 7, Role: "staff"}})
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/staff/online", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Client cannot.
	app = newApp(&fakeAdminService{}, &fakeMW{identity: httpx.AuthIdentity{UserID: 9, Role: "client"}})
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/staff/online", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestOnlineStaffEndpointError(t *testing.T) {
	svc := &fakeAdminService{
		OnlineStaffFn: func(context.Context) ([]ports.PresenceEntry, error) {
			return nil, apperr.Internal(errors.New("redis down"))
		},
	}
	app := newApp(svc, adminMW())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/staff/online", nil))
	require.NoError(t, err)
	assert.Equal(t, 500, resp.StatusCode)
}

func TestDashboardRequiresAuth(t *testing.T) {
	mw := adminMW()
	mw.authFail = true
	app := newApp(&fakeAdminService{}, mw)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/dashboard", nil))
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestDashboardRejectsClientRole(t *testing.T) {
	mw := &fakeMW{identity: httpx.AuthIdentity{UserID: 9, Role: "client", ClientID: 3}}
	app := newApp(&fakeAdminService{}, mw)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/dashboard", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestDashboardRequiresPermission(t *testing.T) {
	mw := &fakeMW{identity: httpx.AuthIdentity{UserID: 8, Role: "staff"}, permissions: map[string]bool{}}
	app := newApp(&fakeAdminService{}, mw)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/dashboard", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)

	mw.permissions["reports"] = true
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/dashboard", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestRevenueReportEndpoint(t *testing.T) {
	var gotFrom, gotTo time.Time
	var gotGroupBy string
	svc := &fakeAdminService{
		RevenueReportFn: func(_ context.Context, from, to time.Time, groupBy string) (*adminops.RevenueReportData, error) {
			gotFrom, gotTo, gotGroupBy = from, to, groupBy
			return &adminops.RevenueReportData{From: "2026-06-01", To: "2026-06-30", TotalAmount: 100}, nil
		},
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("GET",
		"/api/v1/admin/reports/revenue?from=2026-06-01&to=2026-06-30&group_by=month", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), gotFrom)
	assert.Equal(t, time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), gotTo)
	assert.Equal(t, "month", gotGroupBy)
}

func TestRevenueReportCSV(t *testing.T) {
	svc := &fakeAdminService{
		RevenueReportFn: func(context.Context, time.Time, time.Time, string) (*adminops.RevenueReportData, error) {
			return &adminops.RevenueReportData{
				From: "2026-06-01", To: "2026-06-30",
				Points:      []ports.RevenuePoint{{Period: "2026-06-01", Amount: 100, Count: 1}},
				TotalAmount: 100, TotalCount: 1,
			}, nil
		},
	}
	app := newApp(svc, adminMW())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/reports/revenue?format=csv", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/csv")
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "revenue_2026-06-01_2026-06-30.csv")
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "period,amount,count\n2026-06-01,100,1\nTOTAL,100,1\n", string(body))
}

func TestRevenueReportBadDate(t *testing.T) {
	app := newApp(&fakeAdminService{}, adminMW())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/reports/revenue?from=01-06-2026", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
	env := readEnvelope(t, resp.Body)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestReportsRequirePermission(t *testing.T) {
	mw := &fakeMW{identity: httpx.AuthIdentity{UserID: 8, Role: "staff"}, permissions: map[string]bool{}}
	app := newApp(&fakeAdminService{}, mw)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/reports/revenue", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)

	mw.permissions["reports"] = true
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/reports/revenue", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestOrdersReportEndpointAndCSV(t *testing.T) {
	svc := &fakeAdminService{
		OrdersReportFn: func(context.Context, time.Time, time.Time) (*adminops.OrdersReportData, error) {
			return &adminops.OrdersReportData{
				From: "2026-06-01", To: "2026-06-30",
				Points:      []ports.RevenuePoint{{Period: "2026-06-02", Amount: 50, Count: 1}},
				TotalAmount: 50, TotalCount: 1,
			}, nil
		},
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/reports/orders", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := readEnvelope(t, resp.Body)
	assert.Contains(t, string(env.Data), `"total_amount":50`)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/reports/orders?format=csv", nil))
	require.NoError(t, err)
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "orders_2026-06-01_2026-06-30.csv")

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/reports/orders?to=xx", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestServicesReportEndpointAndCSV(t *testing.T) {
	svc := &fakeAdminService{
		ServicesReportFn: func(context.Context) (*adminops.ServicesReportData, error) {
			return &adminops.ServicesReportData{
				ByStatus:  map[string]int64{"active": 2},
				ByProduct: []adminops.ProductStatusCount{{Product: "Basic", Status: "active", Count: 2}},
			}, nil
		},
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/reports/services", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/reports/services?format=csv", nil))
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "product,status,count\nBasic,active,2\n", string(body))
}

func TestListStaffEndpoint(t *testing.T) {
	var gotParams ports.ListParams
	svc := &fakeAdminService{
		ListStaffFn: func(_ context.Context, p ports.ListParams) ([]domain.User, int64, error) {
			gotParams = p
			return []domain.User{{ID: 1, Email: "s@x.co", Role: domain.RoleStaff}}, 1, nil
		},
	}
	app := newApp(svc, adminMW())
	resp, err := app.Test(httptest.NewRequest("GET",
		"/api/v1/admin/staff?search=s@x&status=active&page=2&per_page=10", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, ports.ListParams{Page: 2, PerPage: 10, Search: "s@x", Status: "active"}, gotParams)
	env := readEnvelope(t, resp.Body)
	require.NotNil(t, env.Meta)
	assert.Equal(t, int64(1), env.Meta.Total)
}

func TestStaffRoutesAdminOnly(t *testing.T) {
	mw := &fakeMW{
		identity:    httpx.AuthIdentity{UserID: 8, Role: "staff"},
		permissions: map[string]bool{"staff": true},
	}
	app := newApp(&fakeAdminService{}, mw)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/staff", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode, "staff management is admin-only")
}

func TestCreateStaffEndpoint(t *testing.T) {
	var gotActor int64
	var gotIn adminops.CreateStaffInput
	svc := &fakeAdminService{
		CreateStaffFn: func(_ context.Context, actor int64, in adminops.CreateStaffInput) (*domain.User, error) {
			gotActor, gotIn = actor, in
			return &domain.User{ID: 5, Email: in.Email, Role: domain.RoleStaff}, nil
		},
	}
	app := newApp(svc, adminMW())

	body := `{"email":"new@staff.co","password":"supersecret","permissions":{"billing":true},"locale":"en"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/staff", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)
	assert.Equal(t, int64(42), gotActor)
	assert.Equal(t, "new@staff.co", gotIn.Email)
	assert.True(t, gotIn.Permissions["billing"])
	assert.Equal(t, "en", gotIn.Locale)
}

func TestCreateStaffBadBody(t *testing.T) {
	app := newApp(&fakeAdminService{}, adminMW())
	req := httptest.NewRequest("POST", "/api/v1/admin/staff", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestGetStaffEndpoint(t *testing.T) {
	svc := &fakeAdminService{
		GetStaffFn: func(_ context.Context, id int64) (*domain.User, error) {
			if id != 7 {
				return nil, apperr.NotFound("staff user")
			}
			return &domain.User{ID: 7, Role: domain.RoleStaff}, nil
		},
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/staff/7", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/staff/8", nil))
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/staff/abc", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestUpdateStaffEndpoint(t *testing.T) {
	var gotID, gotActor int64
	var gotIn adminops.UpdateStaffInput
	svc := &fakeAdminService{
		UpdateStaffFn: func(_ context.Context, actor, id int64, in adminops.UpdateStaffInput) (*domain.User, error) {
			gotActor, gotID, gotIn = actor, id, in
			return &domain.User{ID: id, Role: domain.RoleStaff}, nil
		},
	}
	app := newApp(svc, adminMW())

	req := httptest.NewRequest("PATCH", "/api/v1/admin/staff/7",
		strings.NewReader(`{"status":"inactive","permissions":{"logs":true}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(7), gotID)
	assert.Equal(t, int64(42), gotActor)
	require.NotNil(t, gotIn.Status)
	assert.Equal(t, "inactive", *gotIn.Status)
	assert.True(t, gotIn.Permissions["logs"])

	// invalid id
	req = httptest.NewRequest("PATCH", "/api/v1/admin/staff/0", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestDeactivateStaffEndpoint(t *testing.T) {
	var gotID int64
	svc := &fakeAdminService{
		DeactivateStaffFn: func(_ context.Context, actor, id int64) (*domain.User, error) {
			gotID = id
			return &domain.User{ID: id, Status: domain.UserInactive}, nil
		},
	}
	app := newApp(svc, adminMW())
	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/staff/7/deactivate", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(7), gotID)
}

func TestAuditLogsEndpoint(t *testing.T) {
	var got adminops.AuditLogFilter
	svc := &fakeAdminService{
		AuditLogsFn: func(_ context.Context, f adminops.AuditLogFilter) ([]domain.AuditLog, int64, error) {
			got = f
			return []domain.AuditLog{{ID: 1}}, 1, nil
		},
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("GET",
		"/api/v1/admin/logs/audit?user_id=4&entity=user&action=staff.&from=2026-07-01&to=2026-07-02&page=1&per_page=50", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	require.NotNil(t, got.UserID)
	assert.Equal(t, int64(4), *got.UserID)
	assert.Equal(t, "user", got.Entity)
	assert.Equal(t, "staff.", got.Action)
	require.NotNil(t, got.From)
	require.NotNil(t, got.To)
	assert.Equal(t, 50, got.PerPage)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/logs/audit?user_id=abc", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/logs/audit?from=bad", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/logs/audit?to=bad", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestEmailLogsEndpoint(t *testing.T) {
	var got ports.ListParams
	svc := &fakeAdminService{
		EmailLogsFn: func(_ context.Context, p ports.ListParams) ([]domain.EmailLogEntry, int64, error) {
			got = p
			return nil, 0, nil
		},
	}
	app := newApp(svc, adminMW())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/logs/email?search=x@&status=failed", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "x@", got.Search)
	assert.Equal(t, "failed", got.Status)
}

func TestIntegrationLogsEndpoint(t *testing.T) {
	var got adminops.IntegrationLogFilter
	svc := &fakeAdminService{
		IntegrationLogsFn: func(_ context.Context, f adminops.IntegrationLogFilter) ([]domain.IntegrationLog, int64, error) {
			got = f
			return nil, 0, nil
		},
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/logs/integration?provider=duitku&success=false", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "duitku", got.Provider)
	require.NotNil(t, got.Success)
	assert.False(t, *got.Success)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/logs/integration?success=maybe", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestPendingModuleActionsEndpoint(t *testing.T) {
	var got ports.ModuleActionFilter
	svc := &fakeAdminService{
		PendingModuleActionsFn: func(_ context.Context, f ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error) {
			got = f
			return []ports.ModuleAction{{ID: "t1", Queue: "default", Type: "domain:register"}}, 1, nil
		},
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/logs/queue?type=domain:register&state=archived&page=2&per_page=25", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "domain:register", got.Type)
	assert.Equal(t, "archived", got.State)
	assert.Equal(t, 2, got.Page)
	assert.Equal(t, 25, got.PerPage)
}

func TestRetryModuleActionEndpoint(t *testing.T) {
	var gotActor int64
	var gotQueue, gotID string
	svc := &fakeAdminService{
		RetryModuleActionFn: func(_ context.Context, actorUserID int64, queue, id string) error {
			gotActor, gotQueue, gotID = actorUserID, queue, id
			return nil
		},
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/logs/queue/default/task-1/retry", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "default", gotQueue)
	assert.Equal(t, "task-1", gotID)
	assert.NotZero(t, gotActor)
}

func TestRetryModuleActionEndpointPropagatesNotFound(t *testing.T) {
	svc := &fakeAdminService{
		RetryModuleActionFn: func(context.Context, int64, string, string) error {
			return apperr.NotFound("module action")
		},
	}
	app := newApp(svc, adminMW())
	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/logs/queue/default/missing/retry", nil))
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)
}

func TestDeleteModuleActionEndpoint(t *testing.T) {
	var gotQueue, gotID string
	svc := &fakeAdminService{
		DeleteModuleActionFn: func(_ context.Context, _ int64, queue, id string) error {
			gotQueue, gotID = queue, id
			return nil
		},
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("DELETE", "/api/v1/admin/logs/queue/critical/task-2", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "critical", gotQueue)
	assert.Equal(t, "task-2", gotID)
}

func TestDismissAllModuleActionsEndpoint(t *testing.T) {
	var gotActor int64
	var got ports.ModuleActionFilter
	svc := &fakeAdminService{
		DismissAllModuleActionsFn: func(_ context.Context, actorUserID int64, f ports.ModuleActionFilter) (int, error) {
			gotActor, got = actorUserID, f
			return 5, nil
		},
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("DELETE", "/api/v1/admin/logs/queue?type=provision:create&state=retry", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "provision:create", got.Type)
	assert.Equal(t, "retry", got.State)
	assert.NotZero(t, gotActor)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	data := body["data"].(map[string]any)
	assert.Equal(t, float64(5), data["dismissed"])
}

func TestLogsRequirePermission(t *testing.T) {
	mw := &fakeMW{identity: httpx.AuthIdentity{UserID: 8, Role: "staff"}, permissions: map[string]bool{}}
	app := newApp(&fakeAdminService{}, mw)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/logs/audit", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestGatewaysEndpoints(t *testing.T) {
	svc := &fakeAdminService{
		GatewaysFn: func(context.Context) (*adminops.GatewaysConfig, error) {
			return &adminops.GatewaysConfig{Duitku: adminops.DuitkuGatewayConfig{
				MerchantCode: "D1", Mode: "sandbox", BaseURL: "https://sandbox.duitku.com", APIKeySet: true,
			}}, nil
		},
	}
	var gotIn adminops.UpdateGatewaysInput
	svc.UpdateGatewaysFn = func(_ context.Context, actor int64, in adminops.UpdateGatewaysInput) (*adminops.GatewaysConfig, error) {
		gotIn = in
		return &adminops.GatewaysConfig{Duitku: adminops.DuitkuGatewayConfig{
			MerchantCode: in.MerchantCode, Mode: in.Mode, BaseURL: in.BaseURL, APIKeySet: true,
		}}, nil
	}
	app := newApp(svc, adminMW())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/gateways", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := readEnvelope(t, resp.Body)
	assert.Contains(t, string(env.Data), `"api_key_set":true`)
	assert.Contains(t, string(env.Data), `"base_url":"https://sandbox.duitku.com"`)
	assert.NotContains(t, string(env.Data), "api_key\"", "never expose the secret itself")

	req := httptest.NewRequest("PUT", "/api/v1/admin/gateways",
		strings.NewReader(`{"merchant_code":"D2","mode":"production","base_url":"http://mockserver:9090"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "D2", gotIn.MerchantCode)
	assert.Equal(t, "production", gotIn.Mode)
	assert.Equal(t, "http://mockserver:9090", gotIn.BaseURL)

	req = httptest.NewRequest("PUT", "/api/v1/admin/gateways", strings.NewReader("nope"))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestUpdateManualGatewayEndpoint(t *testing.T) {
	var gotIn adminops.UpdateManualGatewayInput
	svc := &fakeAdminService{
		UpdateManualGatewayFn: func(_ context.Context, actor int64, in adminops.UpdateManualGatewayInput) (*adminops.ManualGatewayConfig, error) {
			gotIn = in
			return &adminops.ManualGatewayConfig{Enabled: in.Enabled, Instructions: in.Instructions}, nil
		},
	}
	app := newApp(svc, adminMW())

	req := httptest.NewRequest("PUT", "/api/v1/admin/gateways/manual",
		strings.NewReader(`{"enabled":true,"accounts":[{"bank_name":"BCA","account_number":"123","account_holder":"WHCMS"}],"instructions":"note"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.True(t, gotIn.Enabled)
	require.Len(t, gotIn.Accounts, 1)
	assert.Equal(t, "BCA", gotIn.Accounts[0].BankName)
	assert.Equal(t, "note", gotIn.Instructions)

	req = httptest.NewRequest("PUT", "/api/v1/admin/gateways/manual", strings.NewReader("nope"))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlersPropagateServiceErrors(t *testing.T) {
	boom := apperr.Internal(errors.New("boom"))
	svc := &fakeAdminService{
		DashboardFn: func(context.Context, bool) (*adminops.DashboardData, error) { return nil, boom },
		RevenueReportFn: func(context.Context, time.Time, time.Time, string) (*adminops.RevenueReportData, error) {
			return nil, boom
		},
		OrdersReportFn: func(context.Context, time.Time, time.Time) (*adminops.OrdersReportData, error) {
			return nil, boom
		},
		ServicesReportFn: func(context.Context) (*adminops.ServicesReportData, error) { return nil, boom },
		ListStaffFn: func(context.Context, ports.ListParams) ([]domain.User, int64, error) {
			return nil, 0, boom
		},
		CreateStaffFn: func(context.Context, int64, adminops.CreateStaffInput) (*domain.User, error) {
			return nil, boom
		},
		UpdateStaffFn: func(context.Context, int64, int64, adminops.UpdateStaffInput) (*domain.User, error) {
			return nil, boom
		},
		DeactivateStaffFn: func(context.Context, int64, int64) (*domain.User, error) { return nil, boom },
		AuditLogsFn: func(context.Context, adminops.AuditLogFilter) ([]domain.AuditLog, int64, error) {
			return nil, 0, boom
		},
		EmailLogsFn: func(context.Context, ports.ListParams) ([]domain.EmailLogEntry, int64, error) {
			return nil, 0, boom
		},
		IntegrationLogsFn: func(context.Context, adminops.IntegrationLogFilter) ([]domain.IntegrationLog, int64, error) {
			return nil, 0, boom
		},
		GatewaysFn: func(context.Context) (*adminops.GatewaysConfig, error) { return nil, boom },
		UpdateGatewaysFn: func(context.Context, int64, adminops.UpdateGatewaysInput) (*adminops.GatewaysConfig, error) {
			return nil, boom
		},
		UpdateManualGatewayFn: func(context.Context, int64, adminops.UpdateManualGatewayInput) (*adminops.ManualGatewayConfig, error) {
			return nil, boom
		},
	}
	app := newApp(svc, adminMW())

	cases := []struct{ method, path, body string }{
		{"GET", "/api/v1/admin/dashboard", ""},
		{"GET", "/api/v1/admin/reports/revenue", ""},
		{"GET", "/api/v1/admin/reports/orders", ""},
		{"GET", "/api/v1/admin/reports/services", ""},
		{"GET", "/api/v1/admin/staff", ""},
		{"POST", "/api/v1/admin/staff", `{"email":"a@b.co","password":"supersecret"}`},
		{"PATCH", "/api/v1/admin/staff/1", `{}`},
		{"POST", "/api/v1/admin/staff/1/deactivate", ""},
		{"GET", "/api/v1/admin/logs/audit", ""},
		{"GET", "/api/v1/admin/logs/email", ""},
		{"GET", "/api/v1/admin/logs/integration", ""},
		{"GET", "/api/v1/admin/gateways", ""},
		{"PUT", "/api/v1/admin/gateways", `{"merchant_code":"D1","mode":"sandbox"}`},
		{"PUT", "/api/v1/admin/gateways/manual", `{"enabled":true}`},
	}
	for _, tc := range cases {
		var body io.Reader
		if tc.body != "" {
			body = strings.NewReader(tc.body)
		}
		req := httptest.NewRequest(tc.method, tc.path, body)
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := app.Test(req)
		require.NoError(t, err, "%s %s", tc.method, tc.path)
		assert.Equal(t, 500, resp.StatusCode, "%s %s", tc.method, tc.path)
	}
}

func TestGatewaysAdminOnly(t *testing.T) {
	mw := &fakeMW{identity: httpx.AuthIdentity{UserID: 8, Role: "staff"},
		permissions: map[string]bool{"gateways": true}}
	app := newApp(&fakeAdminService{}, mw)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/gateways", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)
}
