package provisioning

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMW injects a fixed identity and lets everything pass.
type fakeMW struct{ id httpx.AuthIdentity }

func pass(c fiber.Ctx) error { return c.Next() }

func (s fakeMW) RequireAuth() fiber.Handler {
	return func(c fiber.Ctx) error {
		httpx.SetIdentity(c, s.id)
		return c.Next()
	}
}
func (s fakeMW) RequireRole(...string) fiber.Handler    { return pass }
func (s fakeMW) RequirePermission(string) fiber.Handler { return pass }
func (s fakeMW) RequireClient() fiber.Handler           { return pass }

// fakeService implements ProvisioningService with function fields.
type fakeService struct {
	listServices              func(ctx context.Context, clientID int64, p ports.ListParams) ([]ServiceView, int64, error)
	getService                func(ctx context.Context, clientID, serviceID int64) (*ServiceView, error)
	changePassword            func(ctx context.Context, actorUserID, clientID, serviceID int64, password string) error
	sso                       func(ctx context.Context, actorUserID, clientID, serviceID int64) (string, error)
	cancelService             func(ctx context.Context, actorUserID, clientID, serviceID int64, in CancelServiceInput) (*domain.Service, error)
	upgradeService            func(ctx context.Context, actorUserID, clientID, serviceID int64, in UpgradeServiceInput) (*UpgradeResult, error)
	adminCreateService        func(ctx context.Context, actorUserID int64, in AdminCreateServiceInput) (*domain.Service, error)
	adminAction               func(ctx context.Context, actorUserID, serviceID int64, action, reason string, async bool) error
	adminChangePackage        func(ctx context.Context, actorUserID, serviceID int64, in AdminChangePackageInput) error
	adminUpdateService        func(ctx context.Context, actorUserID, serviceID int64, in AdminUpdateServiceInput) (*domain.Service, error)
	listCancellationRequests  func(ctx context.Context, p ports.ListParams) ([]CancellationRequestView, int64, error)
	acceptCancellationRequest func(ctx context.Context, actorUserID, requestID int64) error
	rejectCancellationRequest func(ctx context.Context, actorUserID, requestID int64) error
	listServers               func(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error)
	getServer                 func(ctx context.Context, id int64) (*domain.Server, error)
	createServer              func(ctx context.Context, actorUserID int64, in ServerInput) (*domain.Server, error)
	updateServer              func(ctx context.Context, actorUserID, id int64, in ServerInput) (*domain.Server, error)
	deleteServer              func(ctx context.Context, actorUserID, id int64) error
	testConnection            func(ctx context.Context, in TestConnectionInput) (*TestConnectionResult, error)
	listGroups                func(ctx context.Context) ([]domain.ServerGroup, error)
	getGroup                  func(ctx context.Context, id int64) (*domain.ServerGroup, error)
	createGroup               func(ctx context.Context, actorUserID int64, in ServerGroupInput) (*domain.ServerGroup, error)
	updateGroup               func(ctx context.Context, actorUserID, id int64, in ServerGroupInput) (*domain.ServerGroup, error)
	deleteGroup               func(ctx context.Context, actorUserID, id int64) error
	listPackages              func(ctx context.Context, groupID int64) (*PackageListResult, error)
}

func (f *fakeService) AdminCreateService(ctx context.Context, actorUserID int64, in AdminCreateServiceInput) (*domain.Service, error) {
	if f.adminCreateService != nil {
		return f.adminCreateService(ctx, actorUserID, in)
	}
	return nil, nil
}

func (f *fakeService) ListServices(ctx context.Context, clientID int64, p ports.ListParams) ([]ServiceView, int64, error) {
	if f.listServices != nil {
		return f.listServices(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (f *fakeService) GetService(ctx context.Context, clientID, serviceID int64) (*ServiceView, error) {
	if f.getService != nil {
		return f.getService(ctx, clientID, serviceID)
	}
	return nil, apperr.NotFound("service")
}

func (f *fakeService) ChangePassword(ctx context.Context, actorUserID, clientID, serviceID int64, password string) error {
	if f.changePassword != nil {
		return f.changePassword(ctx, actorUserID, clientID, serviceID, password)
	}
	return nil
}

func (f *fakeService) SSO(ctx context.Context, actorUserID, clientID, serviceID int64) (string, error) {
	if f.sso != nil {
		return f.sso(ctx, actorUserID, clientID, serviceID)
	}
	return "", nil
}

func (f *fakeService) CancelService(ctx context.Context, actorUserID, clientID, serviceID int64, in CancelServiceInput) (*domain.Service, error) {
	if f.cancelService != nil {
		return f.cancelService(ctx, actorUserID, clientID, serviceID, in)
	}
	return nil, nil
}

func (f *fakeService) UpgradeService(ctx context.Context, actorUserID, clientID, serviceID int64, in UpgradeServiceInput) (*UpgradeResult, error) {
	if f.upgradeService != nil {
		return f.upgradeService(ctx, actorUserID, clientID, serviceID, in)
	}
	return nil, nil
}

func (f *fakeService) AdminAction(ctx context.Context, actorUserID, serviceID int64, action, reason string, async bool) error {
	if f.adminAction != nil {
		return f.adminAction(ctx, actorUserID, serviceID, action, reason, async)
	}
	return nil
}

func (f *fakeService) AdminChangePackage(ctx context.Context, actorUserID, serviceID int64, in AdminChangePackageInput) error {
	if f.adminChangePackage != nil {
		return f.adminChangePackage(ctx, actorUserID, serviceID, in)
	}
	return nil
}

func (f *fakeService) AdminUpdateService(ctx context.Context, actorUserID, serviceID int64, in AdminUpdateServiceInput) (*domain.Service, error) {
	if f.adminUpdateService != nil {
		return f.adminUpdateService(ctx, actorUserID, serviceID, in)
	}
	return nil, apperr.NotFound("service")
}

func (f *fakeService) ListCancellationRequests(ctx context.Context, p ports.ListParams) ([]CancellationRequestView, int64, error) {
	if f.listCancellationRequests != nil {
		return f.listCancellationRequests(ctx, p)
	}
	return nil, 0, nil
}

func (f *fakeService) AcceptCancellationRequest(ctx context.Context, actorUserID, requestID int64) error {
	if f.acceptCancellationRequest != nil {
		return f.acceptCancellationRequest(ctx, actorUserID, requestID)
	}
	return nil
}

func (f *fakeService) RejectCancellationRequest(ctx context.Context, actorUserID, requestID int64) error {
	if f.rejectCancellationRequest != nil {
		return f.rejectCancellationRequest(ctx, actorUserID, requestID)
	}
	return nil
}

func (f *fakeService) ListServers(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error) {
	if f.listServers != nil {
		return f.listServers(ctx, p)
	}
	return nil, 0, nil
}

func (f *fakeService) GetServer(ctx context.Context, id int64) (*domain.Server, error) {
	if f.getServer != nil {
		return f.getServer(ctx, id)
	}
	return nil, apperr.NotFound("server")
}

func (f *fakeService) CreateServer(ctx context.Context, actorUserID int64, in ServerInput) (*domain.Server, error) {
	if f.createServer != nil {
		return f.createServer(ctx, actorUserID, in)
	}
	return nil, nil
}

func (f *fakeService) UpdateServer(ctx context.Context, actorUserID, id int64, in ServerInput) (*domain.Server, error) {
	if f.updateServer != nil {
		return f.updateServer(ctx, actorUserID, id, in)
	}
	return nil, nil
}

func (f *fakeService) DeleteServer(ctx context.Context, actorUserID, id int64) error {
	if f.deleteServer != nil {
		return f.deleteServer(ctx, actorUserID, id)
	}
	return nil
}

func (f *fakeService) TestConnection(ctx context.Context, in TestConnectionInput) (*TestConnectionResult, error) {
	if f.testConnection != nil {
		return f.testConnection(ctx, in)
	}
	return nil, nil
}

func (f *fakeService) ListGroups(ctx context.Context) ([]domain.ServerGroup, error) {
	if f.listGroups != nil {
		return f.listGroups(ctx)
	}
	return nil, nil
}

func (f *fakeService) GetGroup(ctx context.Context, id int64) (*domain.ServerGroup, error) {
	if f.getGroup != nil {
		return f.getGroup(ctx, id)
	}
	return nil, apperr.NotFound("server group")
}

func (f *fakeService) CreateGroup(ctx context.Context, actorUserID int64, in ServerGroupInput) (*domain.ServerGroup, error) {
	if f.createGroup != nil {
		return f.createGroup(ctx, actorUserID, in)
	}
	return nil, nil
}

func (f *fakeService) UpdateGroup(ctx context.Context, actorUserID, id int64, in ServerGroupInput) (*domain.ServerGroup, error) {
	if f.updateGroup != nil {
		return f.updateGroup(ctx, actorUserID, id, in)
	}
	return nil, nil
}

func (f *fakeService) DeleteGroup(ctx context.Context, actorUserID, id int64) error {
	if f.deleteGroup != nil {
		return f.deleteGroup(ctx, actorUserID, id)
	}
	return nil
}

func (f *fakeService) ListPackages(ctx context.Context, groupID int64) (*PackageListResult, error) {
	if f.listPackages != nil {
		return f.listPackages(ctx, groupID)
	}
	return nil, nil
}

var _ ProvisioningService = (*fakeService)(nil)

func newApp(svc ProvisioningService, id httpx.AuthIdentity) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: transporthttp.ErrorHandler(slog.New(slog.NewTextHandler(io.Discard, nil))),
	})
	h := NewHandler(svc, fakeMW{id: id})
	h.RegisterRoutes(app.Group("/api/v1"))
	return app
}

func clientIdentity() httpx.AuthIdentity {
	return httpx.AuthIdentity{UserID: 10, Role: "client", ClientID: 7}
}

func adminIdentity() httpx.AuthIdentity {
	return httpx.AuthIdentity{UserID: 1, Role: "admin"}
}

func decodeEnvelope(t *testing.T, body io.Reader) httpx.Envelope {
	t.Helper()
	var env httpx.Envelope
	require.NoError(t, json.NewDecoder(body).Decode(&env))
	return env
}

func TestHandlerListMyServices(t *testing.T) {
	fs := &fakeService{
		listServices: func(_ context.Context, clientID int64, p ports.ListParams) ([]ServiceView, int64, error) {
			assert.Equal(t, int64(7), clientID)
			assert.Equal(t, 2, p.Page)
			assert.Equal(t, "active", p.Status)
			return []ServiceView{{Service: domain.Service{ID: 1, ClientID: 7}, ProductName: "Hosting Basic"}}, 26, nil
		},
	}
	app := newApp(fs, clientIdentity())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/services?page=2&status=active", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := decodeEnvelope(t, resp.Body)
	require.NotNil(t, env.Meta)
	assert.Equal(t, int64(26), env.Meta.Total)
	assert.Equal(t, 2, env.Meta.Page)
}

func TestHandlerGetMyService(t *testing.T) {
	fs := &fakeService{
		getService: func(_ context.Context, clientID, serviceID int64) (*ServiceView, error) {
			assert.Equal(t, int64(7), clientID)
			assert.Equal(t, int64(42), serviceID)
			return &ServiceView{Service: domain.Service{ID: 42, ClientID: 7}, ProductName: "Hosting Basic"}, nil
		},
	}
	app := newApp(fs, clientIdentity())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/services/42", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Invalid id -> 422 VALIDATION.
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/services/abc", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)

	// Not found -> 404.
	fs.getService = func(context.Context, int64, int64) (*ServiceView, error) {
		return nil, apperr.NotFound("service")
	}
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/services/43", nil))
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)
}

func TestHandlerChangePassword(t *testing.T) {
	var gotPassword string
	fs := &fakeService{
		changePassword: func(_ context.Context, actor, clientID, serviceID int64, password string) error {
			assert.Equal(t, int64(10), actor)
			assert.Equal(t, int64(7), clientID)
			gotPassword = password
			return nil
		},
	}
	app := newApp(fs, clientIdentity())

	req := httptest.NewRequest("POST", "/api/v1/services/42/change-password",
		strings.NewReader(`{"password":"Sup3rStrongPass"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "Sup3rStrongPass", gotPassword)
}

func TestHandlerSSO(t *testing.T) {
	fs := &fakeService{
		sso: func(_ context.Context, actorUserID, clientID, serviceID int64) (string, error) {
			return "https://panel/session", nil
		},
	}
	app := newApp(fs, clientIdentity())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/services/42/sso", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := decodeEnvelope(t, resp.Body)
	data, _ := json.Marshal(env.Data)
	assert.Contains(t, string(data), "https://panel/session")
}

func TestHandlerCancel(t *testing.T) {
	var gotMode string
	fs := &fakeService{
		cancelService: func(_ context.Context, actor, clientID, serviceID int64, in CancelServiceInput) (*domain.Service, error) {
			gotMode = in.Mode
			return &domain.Service{ID: serviceID}, nil
		},
	}
	app := newApp(fs, clientIdentity())

	req := httptest.NewRequest("POST", "/api/v1/services/42/cancel",
		strings.NewReader(`{"mode":"end_of_term"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, CancelModeEndOfTerm, gotMode)
}

func TestHandlerUpgrade(t *testing.T) {
	fs := &fakeService{
		upgradeService: func(_ context.Context, actor, clientID, serviceID int64, in UpgradeServiceInput) (*UpgradeResult, error) {
			assert.Equal(t, int64(6), in.ProductID)
			assert.Equal(t, domain.CycleMonthly, in.Cycle)
			return &UpgradeResult{Applied: false, ProratedDiff: 48387,
				Invoice: &domain.Invoice{ID: 77}}, nil
		},
	}
	app := newApp(fs, clientIdentity())

	req := httptest.NewRequest("POST", "/api/v1/services/42/upgrade",
		strings.NewReader(`{"product_id":6,"cycle":"monthly"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

// TestHandlerAdminUpgrade proves the admin route runs the SAME prorated
// upgrade flow as the client endpoint (not AdminChangePackage's immediate,
// no-invoice swap) - clientID 0 bypasses the ownership check while the
// actor id is still the admin's own.
func TestHandlerAdminUpgrade(t *testing.T) {
	var gotClientID int64 = -1
	fs := &fakeService{
		upgradeService: func(_ context.Context, actor, clientID, serviceID int64, in UpgradeServiceInput) (*UpgradeResult, error) {
			assert.Equal(t, int64(1), actor) // adminIdentity()'s UserID
			gotClientID = clientID
			assert.Equal(t, int64(6), in.ProductID)
			assert.Equal(t, domain.CycleMonthly, in.Cycle)
			return &UpgradeResult{Applied: false, ProratedDiff: 48387,
				Invoice: &domain.Invoice{ID: 77}}, nil
		},
	}
	app := newApp(fs, adminIdentity())

	req := httptest.NewRequest("POST", "/api/v1/admin/services/42/upgrade",
		strings.NewReader(`{"product_id":6,"cycle":"monthly"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Zero(t, gotClientID, "admin bypasses ownership")
}

func TestHandlerAdminActions(t *testing.T) {
	type call struct {
		Action, Reason string
		Async          bool
	}
	var calls []call
	fs := &fakeService{
		adminAction: func(_ context.Context, actor, serviceID int64, action, reason string, async bool) error {
			assert.Equal(t, int64(1), actor)
			assert.Equal(t, int64(42), serviceID)
			calls = append(calls, call{action, reason, async})
			return nil
		},
	}
	app := newApp(fs, adminIdentity())

	// No body -> sync with empty reason.
	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/services/42/create", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Suspend with reason + async.
	req := httptest.NewRequest("POST", "/api/v1/admin/services/42/suspend",
		strings.NewReader(`{"reason":"abuse","async":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("POST", "/api/v1/admin/services/42/unsuspend", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	resp, err = app.Test(httptest.NewRequest("POST", "/api/v1/admin/services/42/terminate", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	require.Len(t, calls, 4)
	assert.Equal(t, call{ActionCreate, "", false}, calls[0])
	assert.Equal(t, call{ActionSuspend, "abuse", true}, calls[1])
	assert.Equal(t, call{ActionUnsuspend, "", false}, calls[2])
	assert.Equal(t, call{ActionTerminate, "", false}, calls[3])
}

func TestHandlerAdminChangePackageAndPassword(t *testing.T) {
	var gotIn AdminChangePackageInput
	var gotPassword string
	fs := &fakeService{
		adminChangePackage: func(_ context.Context, actor, serviceID int64, in AdminChangePackageInput) error {
			gotIn = in
			return nil
		},
		changePassword: func(_ context.Context, actor, clientID, serviceID int64, password string) error {
			assert.Equal(t, int64(0), clientID) // admin bypasses ownership
			gotPassword = password
			return nil
		},
	}
	app := newApp(fs, adminIdentity())

	req := httptest.NewRequest("POST", "/api/v1/admin/services/42/change-package",
		strings.NewReader(`{"product_id":6,"async":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	require.NotNil(t, gotIn.ProductID)
	assert.Equal(t, int64(6), *gotIn.ProductID)
	assert.True(t, gotIn.Async)

	req = httptest.NewRequest("POST", "/api/v1/admin/services/42/change-password",
		strings.NewReader(`{"password":"Sup3rStrongPass"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "Sup3rStrongPass", gotPassword)
}

func TestHandlerServerCRUD(t *testing.T) {
	var gotTestInput TestConnectionInput
	fs := &fakeService{
		createServer: func(_ context.Context, actor int64, in ServerInput) (*domain.Server, error) {
			assert.Equal(t, "srv1", in.Name)
			return &domain.Server{ID: 11, Name: in.Name}, nil
		},
		updateServer: func(_ context.Context, actor, id int64, in ServerInput) (*domain.Server, error) {
			return &domain.Server{ID: id, Name: in.Name}, nil
		},
		getServer: func(_ context.Context, id int64) (*domain.Server, error) {
			return &domain.Server{ID: id}, nil
		},
		listServers: func(_ context.Context, p ports.ListParams) ([]domain.Server, int64, error) {
			return []domain.Server{{ID: 11}}, 1, nil
		},
		testConnection: func(_ context.Context, in TestConnectionInput) (*TestConnectionResult, error) {
			gotTestInput = in
			return &TestConnectionResult{
				OK:          true,
				Message:     "connection successful (11.134.0.45)",
				Version:     "11.134.0.45",
				Nameservers: []string{"ns1.host.id", "ns2.host.id"},
			}, nil
		},
	}
	app := newApp(fs, adminIdentity())

	req := httptest.NewRequest("POST", "/api/v1/admin/servers",
		strings.NewReader(`{"name":"srv1","module":"cpanel","hostname":"srv1.host.id"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/servers", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/servers/11", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	req = httptest.NewRequest("PATCH", "/api/v1/admin/servers/11",
		strings.NewReader(`{"name":"srv1b","module":"cpanel","hostname":"srv1.host.id"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("DELETE", "/api/v1/admin/servers/11", nil))
	require.NoError(t, err)
	assert.Equal(t, 204, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("POST", "/api/v1/admin/servers/11/test-connection", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := decodeEnvelope(t, resp.Body)
	data, _ := json.Marshal(env.Data)
	assert.Contains(t, string(data), `"ok":true`)
	assert.Equal(t, int64(11), gotTestInput.ID) // :id route forwards the id

	// Pre-save probe: form values are bound and forwarded; nameservers returned.
	req = httptest.NewRequest("POST", "/api/v1/admin/servers/test-connection",
		strings.NewReader(`{"module":"cpanel","hostname":"srv1.host.id","username":"root","api_token":"tok"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env = decodeEnvelope(t, resp.Body)
	data, _ = json.Marshal(env.Data)
	assert.Contains(t, string(data), `"ns1.host.id"`)
	assert.Equal(t, int64(0), gotTestInput.ID)
	assert.Equal(t, "srv1.host.id", gotTestInput.Hostname)
	assert.Equal(t, "tok", gotTestInput.APIToken)

	// Malformed pre-save body -> 422 VALIDATION.
	req = httptest.NewRequest("POST", "/api/v1/admin/servers/test-connection",
		strings.NewReader(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlerServerGroupCRUD(t *testing.T) {
	fs := &fakeService{
		createGroup: func(_ context.Context, actor int64, in ServerGroupInput) (*domain.ServerGroup, error) {
			return &domain.ServerGroup{ID: 9, Name: in.Name}, nil
		},
		updateGroup: func(_ context.Context, actor, id int64, in ServerGroupInput) (*domain.ServerGroup, error) {
			return &domain.ServerGroup{ID: id, Name: in.Name}, nil
		},
		getGroup: func(_ context.Context, id int64) (*domain.ServerGroup, error) {
			return &domain.ServerGroup{ID: id}, nil
		},
		listGroups: func(context.Context) ([]domain.ServerGroup, error) {
			return []domain.ServerGroup{{ID: 9}}, nil
		},
	}
	app := newApp(fs, adminIdentity())

	req := httptest.NewRequest("POST", "/api/v1/admin/server-groups",
		strings.NewReader(`{"name":"g1","strategy":"round_robin"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/server-groups", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/server-groups/9", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	req = httptest.NewRequest("PATCH", "/api/v1/admin/server-groups/9",
		strings.NewReader(`{"name":"g2","strategy":"least_used"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("DELETE", "/api/v1/admin/server-groups/9", nil))
	require.NoError(t, err)
	assert.Equal(t, 204, resp.StatusCode)
}

func TestHandlerListPackages(t *testing.T) {
	fs := &fakeService{
		listPackages: func(_ context.Context, groupID int64) (*PackageListResult, error) {
			assert.Equal(t, int64(9), groupID)
			return &PackageListResult{OK: true, Packages: []string{"business", "default"}}, nil
		},
	}
	app := newApp(fs, adminIdentity())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/server-groups/9/packages", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := decodeEnvelope(t, resp.Body)
	data, ok := env.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, data["ok"])

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/server-groups/bogus/packages", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlerAdminListAndGetService(t *testing.T) {
	fs := &fakeService{
		listServices: func(_ context.Context, clientID int64, p ports.ListParams) ([]ServiceView, int64, error) {
			assert.Zero(t, clientID) // admin sees everything
			return []ServiceView{{Service: domain.Service{ID: 1}}}, 1, nil
		},
		getService: func(_ context.Context, clientID, serviceID int64) (*ServiceView, error) {
			assert.Zero(t, clientID)
			return &ServiceView{Service: domain.Service{ID: serviceID}}, nil
		},
	}
	app := newApp(fs, adminIdentity())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/services", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/services/42", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/services/x", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlerListCancellationRequests(t *testing.T) {
	fs := &fakeService{
		listCancellationRequests: func(_ context.Context, p ports.ListParams) ([]CancellationRequestView, int64, error) {
			assert.Equal(t, "pending", p.Status)
			return []CancellationRequestView{{
				CancellationRequest: domain.CancellationRequest{ID: 1, Status: domain.CancellationPending},
				ServiceDomain:       "example.com",
			}}, 1, nil
		},
	}
	app := newApp(fs, adminIdentity())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/services/cancellation-requests?status=pending", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := decodeEnvelope(t, resp.Body)
	require.NotNil(t, env.Meta)
	assert.Equal(t, int64(1), env.Meta.Total)
}

func TestHandlerAcceptRejectCancellationRequest(t *testing.T) {
	var acceptedID, rejectedID int64
	fs := &fakeService{
		acceptCancellationRequest: func(_ context.Context, actorUserID, requestID int64) error {
			assert.Equal(t, int64(1), actorUserID)
			acceptedID = requestID
			return nil
		},
		rejectCancellationRequest: func(_ context.Context, actorUserID, requestID int64) error {
			assert.Equal(t, int64(1), actorUserID)
			rejectedID = requestID
			return nil
		},
	}
	app := newApp(fs, adminIdentity())

	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/services/cancellation-requests/5/accept", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(5), acceptedID)

	resp, err = app.Test(httptest.NewRequest("POST", "/api/v1/admin/services/cancellation-requests/9/reject", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(9), rejectedID)

	resp, err = app.Test(httptest.NewRequest("POST", "/api/v1/admin/services/cancellation-requests/x/accept", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)

	fs.acceptCancellationRequest = func(context.Context, int64, int64) error {
		return apperr.Conflict("already decided")
	}
	resp, err = app.Test(httptest.NewRequest("POST", "/api/v1/admin/services/cancellation-requests/5/accept", nil))
	require.NoError(t, err)
	assert.Equal(t, 409, resp.StatusCode)
}

func TestHandlerAdminCreateService(t *testing.T) {
	fs := &fakeService{
		adminCreateService: func(_ context.Context, actorUserID int64, in AdminCreateServiceInput) (*domain.Service, error) {
			assert.Equal(t, int64(1), actorUserID)
			assert.Equal(t, int64(7), in.ClientID)
			assert.Equal(t, int64(5), in.ProductID)
			assert.Equal(t, "imported.example.com", in.Domain)
			assert.Equal(t, "monthly", in.BillingCycle)
			return &domain.Service{ID: 77, ClientID: in.ClientID, Status: domain.ServiceActive}, nil
		},
	}
	app := newApp(fs, adminIdentity())

	req := httptest.NewRequest("POST", "/api/v1/admin/services",
		strings.NewReader(`{"client_id":7,"product_id":5,"domain":"imported.example.com","billing_cycle":"monthly","recurring_amount":150000,"next_due_date":"2026-08-01"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)

	// Malformed body -> 422.
	req = httptest.NewRequest("POST", "/api/v1/admin/services", strings.NewReader(`{`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlerAdminUpdateService(t *testing.T) {
	fs := &fakeService{
		adminUpdateService: func(_ context.Context, actorUserID, serviceID int64, in AdminUpdateServiceInput) (*domain.Service, error) {
			assert.Equal(t, int64(1), actorUserID)
			assert.Equal(t, int64(7), serviceID)
			require.NotNil(t, in.NextDueDate)
			assert.Equal(t, "2026-08-01", *in.NextDueDate)
			require.NotNil(t, in.Notes)
			assert.Equal(t, "flagged", *in.Notes)
			return &domain.Service{ID: serviceID}, nil
		},
	}
	app := newApp(fs, adminIdentity())

	req := httptest.NewRequest("PATCH", "/api/v1/admin/services/7",
		strings.NewReader(`{"next_due_date":"2026-08-01","notes":"flagged"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("PATCH", "/api/v1/admin/services/x", strings.NewReader(`{}`)))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlerAdminUpdateServiceNewFields(t *testing.T) {
	fs := &fakeService{
		adminUpdateService: func(_ context.Context, _, serviceID int64, in AdminUpdateServiceInput) (*domain.Service, error) {
			require.NotNil(t, in.Domain)
			assert.Equal(t, "corrected.example", *in.Domain)
			require.NotNil(t, in.Username)
			assert.Equal(t, "corrected1", *in.Username)
			require.NotNil(t, in.ServerID)
			assert.Equal(t, int64(3), *in.ServerID)
			require.NotNil(t, in.BillingCycle)
			assert.Equal(t, "annually", *in.BillingCycle)
			require.NotNil(t, in.RecurringAmount)
			assert.Equal(t, int64(1_200_000), *in.RecurringAmount)
			require.NotNil(t, in.RegistrationDate)
			assert.Equal(t, "2026-01-15", *in.RegistrationDate)
			require.NotNil(t, in.TerminatedAt)
			assert.Equal(t, "2026-10-01", *in.TerminatedAt)
			require.NotNil(t, in.SuspendReason)
			assert.Equal(t, "manually cleared", *in.SuspendReason)
			return &domain.Service{ID: serviceID}, nil
		},
	}
	app := newApp(fs, adminIdentity())

	body := `{"domain":"corrected.example","username":"corrected1","server_id":3,` +
		`"billing_cycle":"annually","recurring_amount":1200000,` +
		`"registration_date":"2026-01-15","terminated_at":"2026-10-01","suspend_reason":"manually cleared"}`
	req := httptest.NewRequest("PATCH", "/api/v1/admin/services/7", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandlerBadBodies(t *testing.T) {
	app := newApp(&fakeService{}, adminIdentity())

	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/services/1/change-password"},
		{"POST", "/api/v1/services/1/cancel"},
		{"POST", "/api/v1/services/1/upgrade"},
		{"POST", "/api/v1/admin/services/1/suspend"},
		{"POST", "/api/v1/admin/services/1/change-package"},
		{"POST", "/api/v1/admin/services/1/change-password"},
		{"POST", "/api/v1/admin/servers"},
		{"PATCH", "/api/v1/admin/servers/1"},
		{"POST", "/api/v1/admin/server-groups"},
		{"PATCH", "/api/v1/admin/server-groups/1"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"broken`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		require.NoError(t, err, tc.path)
		assert.Equal(t, 422, resp.StatusCode, tc.path)
	}
}

func TestHandlerErrorMapping(t *testing.T) {
	fs := &fakeService{
		getService: func(context.Context, int64, int64) (*ServiceView, error) {
			return nil, apperr.Conflict("nope")
		},
	}
	app := newApp(fs, clientIdentity())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/services/42", nil))
	require.NoError(t, err)
	assert.Equal(t, 409, resp.StatusCode)
	env := decodeEnvelope(t, resp.Body)
	require.NotNil(t, env.Error)
	assert.Equal(t, "CONFLICT", env.Error.Code)
}
