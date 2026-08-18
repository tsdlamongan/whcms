package domains_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fakes

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

func (m *fakeMW) RateLimit(prefix string, limit int, window time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error {
		if m.rateLimited {
			return apperr.New(apperr.CodeRateLimited, "too many requests")
		}
		return c.Next()
	}
}

var _ domains.Middlewares = (*fakeMW)(nil)

// fakeService is a function-field fake of domains.DomainService.
type fakeService struct {
	CheckAvailabilityFn      func(ctx context.Context, names []string) ([]ports.DomainAvailability, error)
	ListForClientFn          func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Domain, int64, error)
	GetForClientFn           func(ctx context.Context, clientID, domainID int64) (*domain.Domain, error)
	UpdateDomainFn           func(ctx context.Context, clientID, domainID int64, in domains.UpdateDomainRequest) (*domain.Domain, error)
	UpdateNameserversFn      func(ctx context.Context, clientID, domainID int64, ns []string) (*domain.Domain, error)
	GetDNSFn                 func(ctx context.Context, clientID, domainID int64) ([]ports.DNSRecord, error)
	UpdateDNSFn              func(ctx context.Context, clientID, domainID int64, recs []ports.DNSRecord) ([]ports.DNSRecord, error)
	GetEPPFn                 func(ctx context.Context, clientID, domainID int64) (string, error)
	GetContactFn             func(ctx context.Context, clientID, domainID int64) (*ports.RegistrantContact, error)
	RenewNowFn               func(ctx context.Context, clientID, domainID int64) (*domain.Invoice, error)
	AdminListFn              func(ctx context.Context, p ports.ListParams) ([]domain.Domain, int64, error)
	AdminCreateFn            func(ctx context.Context, actorUserID int64, in domains.AdminCreateDomainRequest) (*domain.Domain, error)
	AdminGetFn               func(ctx context.Context, id int64) (*domain.Domain, error)
	AdminUpdateFn            func(ctx context.Context, actorUserID, id int64, in domains.AdminUpdateDomainRequest) (*domain.Domain, error)
	AdminSyncFn              func(ctx context.Context, actorUserID, id int64) (*domain.Domain, error)
	AdminForceRenewFn        func(ctx context.Context, actorUserID, id int64) error
	ListRegistrarsFn         func(ctx context.Context) ([]domain.Registrar, error)
	GetRegistrarFn           func(ctx context.Context, id int64) (*domain.Registrar, error)
	UpdateRegistrarFn        func(ctx context.Context, actorUserID, id int64, in domains.UpdateRegistrarRequest) (*domain.Registrar, error)
	TestRegistrarFn          func(ctx context.Context, id int64) error
	RegistrarAPIKeyPresentFn func() bool
	ListRegistrarCatalogFn   func(ctx context.Context, registrarID int64) ([]domains.RegistrarCatalogResponse, error)

	UpdateDomainAddonsFn func(ctx context.Context, clientID, domainID int64, keys []string) (*domain.Domain, error)

	ListActiveTLDsFn         func(ctx context.Context) ([]domain.TLDPricing, error)
	ListActiveDomainAddonsFn func(ctx context.Context) ([]domain.DomainAddon, error)

	ListTLDPricingFn   func(ctx context.Context) ([]domain.TLDPricing, error)
	GetTLDPricingFn    func(ctx context.Context, id int64) (*domain.TLDPricing, error)
	CreateTLDPricingFn func(ctx context.Context, actorUserID int64, in domains.TLDPricingRequest) (*domain.TLDPricing, error)
	UpdateTLDPricingFn func(ctx context.Context, actorUserID, id int64, in domains.TLDPricingRequest) (*domain.TLDPricing, error)
	DeleteTLDPricingFn func(ctx context.Context, actorUserID, id int64) error
	ImportTLDPricingFn func(ctx context.Context, actorUserID int64, in domains.ImportTLDPricingRequest) (*domains.ImportTLDPricingResponse, error)

	ListPremiumPricingFn   func(ctx context.Context) ([]domain.PremiumDomainPricing, error)
	CreatePremiumPricingFn func(ctx context.Context, actorUserID int64, in domains.PremiumDomainPricingRequest) (*domain.PremiumDomainPricing, error)
	UpdatePremiumPricingFn func(ctx context.Context, actorUserID, id int64, in domains.PremiumDomainPricingRequest) (*domain.PremiumDomainPricing, error)
	DeletePremiumPricingFn func(ctx context.Context, actorUserID, id int64) error

	ListPremiumLengthPricingFn   func(ctx context.Context) ([]domain.PremiumLengthPricing, error)
	CreatePremiumLengthPricingFn func(ctx context.Context, actorUserID int64, in domains.PremiumLengthPricingRequest) (*domain.PremiumLengthPricing, error)
	UpdatePremiumLengthPricingFn func(ctx context.Context, actorUserID, id int64, in domains.PremiumLengthPricingRequest) (*domain.PremiumLengthPricing, error)
	DeletePremiumLengthPricingFn func(ctx context.Context, actorUserID, id int64) error

	ListDomainAddonsFn  func(ctx context.Context) ([]domain.DomainAddon, error)
	UpdateDomainAddonFn func(ctx context.Context, actorUserID, id int64, in domains.UpdateDomainAddonRequest) (*domain.DomainAddon, error)
}

func (f *fakeService) CheckAvailability(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
	if f.CheckAvailabilityFn != nil {
		return f.CheckAvailabilityFn(ctx, names)
	}
	return nil, nil
}

func (f *fakeService) ListForClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Domain, int64, error) {
	if f.ListForClientFn != nil {
		return f.ListForClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (f *fakeService) GetForClient(ctx context.Context, clientID, domainID int64) (*domain.Domain, error) {
	if f.GetForClientFn != nil {
		return f.GetForClientFn(ctx, clientID, domainID)
	}
	return &domain.Domain{}, nil
}

func (f *fakeService) UpdateDomain(ctx context.Context, clientID, domainID int64, in domains.UpdateDomainRequest) (*domain.Domain, error) {
	if f.UpdateDomainFn != nil {
		return f.UpdateDomainFn(ctx, clientID, domainID, in)
	}
	return &domain.Domain{}, nil
}

func (f *fakeService) UpdateNameservers(ctx context.Context, clientID, domainID int64, ns []string) (*domain.Domain, error) {
	if f.UpdateNameserversFn != nil {
		return f.UpdateNameserversFn(ctx, clientID, domainID, ns)
	}
	return &domain.Domain{}, nil
}

func (f *fakeService) GetDNS(ctx context.Context, clientID, domainID int64) ([]ports.DNSRecord, error) {
	if f.GetDNSFn != nil {
		return f.GetDNSFn(ctx, clientID, domainID)
	}
	return nil, nil
}

func (f *fakeService) UpdateDNS(ctx context.Context, clientID, domainID int64, recs []ports.DNSRecord) ([]ports.DNSRecord, error) {
	if f.UpdateDNSFn != nil {
		return f.UpdateDNSFn(ctx, clientID, domainID, recs)
	}
	return recs, nil
}

func (f *fakeService) GetEPP(ctx context.Context, clientID, domainID int64) (string, error) {
	if f.GetEPPFn != nil {
		return f.GetEPPFn(ctx, clientID, domainID)
	}
	return "", nil
}

func (f *fakeService) GetContact(ctx context.Context, clientID, domainID int64) (*ports.RegistrantContact, error) {
	if f.GetContactFn != nil {
		return f.GetContactFn(ctx, clientID, domainID)
	}
	return &ports.RegistrantContact{}, nil
}

func (f *fakeService) RenewNow(ctx context.Context, clientID, domainID int64) (*domain.Invoice, error) {
	if f.RenewNowFn != nil {
		return f.RenewNowFn(ctx, clientID, domainID)
	}
	return &domain.Invoice{}, nil
}

func (f *fakeService) AdminList(ctx context.Context, p ports.ListParams) ([]domain.Domain, int64, error) {
	if f.AdminListFn != nil {
		return f.AdminListFn(ctx, p)
	}
	return nil, 0, nil
}

func (f *fakeService) AdminCreate(ctx context.Context, actorUserID int64, in domains.AdminCreateDomainRequest) (*domain.Domain, error) {
	if f.AdminCreateFn != nil {
		return f.AdminCreateFn(ctx, actorUserID, in)
	}
	return &domain.Domain{}, nil
}

func (f *fakeService) AdminGet(ctx context.Context, id int64) (*domain.Domain, error) {
	if f.AdminGetFn != nil {
		return f.AdminGetFn(ctx, id)
	}
	return &domain.Domain{}, nil
}

func (f *fakeService) AdminUpdate(ctx context.Context, actorUserID, id int64, in domains.AdminUpdateDomainRequest) (*domain.Domain, error) {
	if f.AdminUpdateFn != nil {
		return f.AdminUpdateFn(ctx, actorUserID, id, in)
	}
	return &domain.Domain{}, nil
}

func (f *fakeService) AdminSync(ctx context.Context, actorUserID, id int64) (*domain.Domain, error) {
	if f.AdminSyncFn != nil {
		return f.AdminSyncFn(ctx, actorUserID, id)
	}
	return &domain.Domain{}, nil
}

func (f *fakeService) AdminForceRenew(ctx context.Context, actorUserID, id int64) error {
	if f.AdminForceRenewFn != nil {
		return f.AdminForceRenewFn(ctx, actorUserID, id)
	}
	return nil
}

func (f *fakeService) ListRegistrars(ctx context.Context) ([]domain.Registrar, error) {
	if f.ListRegistrarsFn != nil {
		return f.ListRegistrarsFn(ctx)
	}
	return nil, nil
}

func (f *fakeService) GetRegistrar(ctx context.Context, id int64) (*domain.Registrar, error) {
	if f.GetRegistrarFn != nil {
		return f.GetRegistrarFn(ctx, id)
	}
	return &domain.Registrar{}, nil
}

func (f *fakeService) UpdateRegistrar(ctx context.Context, actorUserID, id int64, in domains.UpdateRegistrarRequest) (*domain.Registrar, error) {
	if f.UpdateRegistrarFn != nil {
		return f.UpdateRegistrarFn(ctx, actorUserID, id, in)
	}
	return &domain.Registrar{}, nil
}

func (f *fakeService) TestRegistrar(ctx context.Context, id int64) error {
	if f.TestRegistrarFn != nil {
		return f.TestRegistrarFn(ctx, id)
	}
	return nil
}

func (f *fakeService) RegistrarAPIKeyPresent() bool {
	if f.RegistrarAPIKeyPresentFn != nil {
		return f.RegistrarAPIKeyPresentFn()
	}
	return false
}

func (f *fakeService) ListRegistrarCatalog(ctx context.Context, registrarID int64) ([]domains.RegistrarCatalogResponse, error) {
	if f.ListRegistrarCatalogFn != nil {
		return f.ListRegistrarCatalogFn(ctx, registrarID)
	}
	return nil, nil
}

func (f *fakeService) UpdateDomainAddons(ctx context.Context, clientID, domainID int64, keys []string) (*domain.Domain, error) {
	if f.UpdateDomainAddonsFn != nil {
		return f.UpdateDomainAddonsFn(ctx, clientID, domainID, keys)
	}
	return nil, nil
}

func (f *fakeService) ListActiveTLDs(ctx context.Context) ([]domain.TLDPricing, error) {
	if f.ListActiveTLDsFn != nil {
		return f.ListActiveTLDsFn(ctx)
	}
	return nil, nil
}

func (f *fakeService) ListActiveDomainAddons(ctx context.Context) ([]domain.DomainAddon, error) {
	if f.ListActiveDomainAddonsFn != nil {
		return f.ListActiveDomainAddonsFn(ctx)
	}
	return nil, nil
}

func (f *fakeService) ListTLDPricing(ctx context.Context) ([]domain.TLDPricing, error) {
	if f.ListTLDPricingFn != nil {
		return f.ListTLDPricingFn(ctx)
	}
	return nil, nil
}

func (f *fakeService) GetTLDPricing(ctx context.Context, id int64) (*domain.TLDPricing, error) {
	if f.GetTLDPricingFn != nil {
		return f.GetTLDPricingFn(ctx, id)
	}
	return nil, nil
}

func (f *fakeService) CreateTLDPricing(ctx context.Context, actorUserID int64, in domains.TLDPricingRequest) (*domain.TLDPricing, error) {
	if f.CreateTLDPricingFn != nil {
		return f.CreateTLDPricingFn(ctx, actorUserID, in)
	}
	return nil, nil
}

func (f *fakeService) UpdateTLDPricing(ctx context.Context, actorUserID, id int64, in domains.TLDPricingRequest) (*domain.TLDPricing, error) {
	if f.UpdateTLDPricingFn != nil {
		return f.UpdateTLDPricingFn(ctx, actorUserID, id, in)
	}
	return nil, nil
}

func (f *fakeService) DeleteTLDPricing(ctx context.Context, actorUserID, id int64) error {
	if f.DeleteTLDPricingFn != nil {
		return f.DeleteTLDPricingFn(ctx, actorUserID, id)
	}
	return nil
}

func (f *fakeService) ImportTLDPricing(ctx context.Context, actorUserID int64, in domains.ImportTLDPricingRequest) (*domains.ImportTLDPricingResponse, error) {
	if f.ImportTLDPricingFn != nil {
		return f.ImportTLDPricingFn(ctx, actorUserID, in)
	}
	return nil, nil
}

func (f *fakeService) ListPremiumPricing(ctx context.Context) ([]domain.PremiumDomainPricing, error) {
	if f.ListPremiumPricingFn != nil {
		return f.ListPremiumPricingFn(ctx)
	}
	return nil, nil
}

func (f *fakeService) CreatePremiumPricing(ctx context.Context, actorUserID int64, in domains.PremiumDomainPricingRequest) (*domain.PremiumDomainPricing, error) {
	if f.CreatePremiumPricingFn != nil {
		return f.CreatePremiumPricingFn(ctx, actorUserID, in)
	}
	return nil, nil
}

func (f *fakeService) UpdatePremiumPricing(ctx context.Context, actorUserID, id int64, in domains.PremiumDomainPricingRequest) (*domain.PremiumDomainPricing, error) {
	if f.UpdatePremiumPricingFn != nil {
		return f.UpdatePremiumPricingFn(ctx, actorUserID, id, in)
	}
	return nil, nil
}

func (f *fakeService) DeletePremiumPricing(ctx context.Context, actorUserID, id int64) error {
	if f.DeletePremiumPricingFn != nil {
		return f.DeletePremiumPricingFn(ctx, actorUserID, id)
	}
	return nil
}

func (f *fakeService) ListPremiumLengthPricing(ctx context.Context) ([]domain.PremiumLengthPricing, error) {
	if f.ListPremiumLengthPricingFn != nil {
		return f.ListPremiumLengthPricingFn(ctx)
	}
	return nil, nil
}

func (f *fakeService) CreatePremiumLengthPricing(ctx context.Context, actorUserID int64, in domains.PremiumLengthPricingRequest) (*domain.PremiumLengthPricing, error) {
	if f.CreatePremiumLengthPricingFn != nil {
		return f.CreatePremiumLengthPricingFn(ctx, actorUserID, in)
	}
	return nil, nil
}

func (f *fakeService) UpdatePremiumLengthPricing(ctx context.Context, actorUserID, id int64, in domains.PremiumLengthPricingRequest) (*domain.PremiumLengthPricing, error) {
	if f.UpdatePremiumLengthPricingFn != nil {
		return f.UpdatePremiumLengthPricingFn(ctx, actorUserID, id, in)
	}
	return nil, nil
}

func (f *fakeService) DeletePremiumLengthPricing(ctx context.Context, actorUserID, id int64) error {
	if f.DeletePremiumLengthPricingFn != nil {
		return f.DeletePremiumLengthPricingFn(ctx, actorUserID, id)
	}
	return nil
}

func (f *fakeService) ListDomainAddons(ctx context.Context) ([]domain.DomainAddon, error) {
	if f.ListDomainAddonsFn != nil {
		return f.ListDomainAddonsFn(ctx)
	}
	return nil, nil
}

func (f *fakeService) UpdateDomainAddon(ctx context.Context, actorUserID, id int64, in domains.UpdateDomainAddonRequest) (*domain.DomainAddon, error) {
	if f.UpdateDomainAddonFn != nil {
		return f.UpdateDomainAddonFn(ctx, actorUserID, id, in)
	}
	return nil, nil
}

var _ domains.DomainService = (*fakeService)(nil)

// Harness

func clientIdentity() httpx.AuthIdentity {
	return httpx.AuthIdentity{UserID: 77, Role: "client", ClientID: 5}
}

func adminIdentity() httpx.AuthIdentity {
	return httpx.AuthIdentity{UserID: 1, Role: "admin"}
}

func newApp(svc domains.DomainService, mw *fakeMW) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: transporthttp.ErrorHandler(slog.New(slog.NewTextHandler(io.Discard, nil))),
	})
	api := app.Group("/api/v1")
	domains.NewHandler(svc, mw).RegisterRoutes(api)
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
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(raw, &envelope), "body: %s", raw)
	return resp.StatusCode, envelope
}

// Public check

func TestHandlerCheck(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		svc := &fakeService{
			CheckAvailabilityFn: func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
				assert.Equal(t, []string{"example.com"}, names)
				return []ports.DomainAvailability{{Name: "example.com", Available: true, Price: 150000}}, nil
			},
		}
		app := newApp(svc, &fakeMW{})
		status, env := doJSON(t, app, "POST", "/api/v1/domains/check", `{"names":["example.com"]}`)
		assert.Equal(t, 200, status)
		data := env["data"].([]any)
		require.Len(t, data, 1)
		assert.Equal(t, true, data[0].(map[string]any)["available"])
	})

	t.Run("rate limited", func(t *testing.T) {
		app := newApp(&fakeService{}, &fakeMW{rateLimited: true})
		status, env := doJSON(t, app, "POST", "/api/v1/domains/check", `{"names":["example.com"]}`)
		assert.Equal(t, 429, status)
		assert.Equal(t, "RATE_LIMITED", env["error"].(map[string]any)["code"])
	})

	t.Run("empty names rejected", func(t *testing.T) {
		app := newApp(&fakeService{}, &fakeMW{})
		status, _ := doJSON(t, app, "POST", "/api/v1/domains/check", `{"names":[]}`)
		assert.Equal(t, 422, status)
	})

	t.Run("bad body", func(t *testing.T) {
		app := newApp(&fakeService{}, &fakeMW{})
		status, _ := doJSON(t, app, "POST", "/api/v1/domains/check", `{`)
		assert.Equal(t, 422, status)
	})
}

// Client routes

func TestHandlerClientList(t *testing.T) {
	svc := &fakeService{
		ListForClientFn: func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Domain, int64, error) {
			assert.EqualValues(t, 5, clientID)
			assert.Equal(t, 2, p.Page)
			assert.Equal(t, "active", p.Status)
			return []domain.Domain{{ID: 10, Name: "example.com"}}, 26, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})
	status, env := doJSON(t, app, "GET", "/api/v1/domains/?page=2&status=active", "")
	assert.Equal(t, 200, status)
	meta := env["meta"].(map[string]any)
	assert.EqualValues(t, 26, meta["total"])
	assert.EqualValues(t, 2, meta["page"])
}

func TestHandlerClientGuards(t *testing.T) {
	app := newApp(&fakeService{}, &fakeMW{identity: httpx.AuthIdentity{UserID: 2, Role: "staff"}})

	// Staff (no client profile) cannot use client domain routes.
	status, _ := doJSON(t, app, "GET", "/api/v1/domains/", "")
	assert.Equal(t, 403, status)

	// Unauthenticated.
	app = newApp(&fakeService{}, &fakeMW{authFail: true})
	status, _ = doJSON(t, app, "GET", "/api/v1/domains/", "")
	assert.Equal(t, 401, status)
}

func TestHandlerClientGet(t *testing.T) {
	svc := &fakeService{
		GetForClientFn: func(ctx context.Context, clientID, domainID int64) (*domain.Domain, error) {
			if domainID != 10 {
				return nil, apperr.NotFound("domain")
			}
			return &domain.Domain{ID: 10, Name: "example.com", ClientID: clientID}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	status, env := doJSON(t, app, "GET", "/api/v1/domains/10", "")
	assert.Equal(t, 200, status)
	assert.Equal(t, "example.com", env["data"].(map[string]any)["name"])

	status, _ = doJSON(t, app, "GET", "/api/v1/domains/999", "")
	assert.Equal(t, 404, status)

	status, _ = doJSON(t, app, "GET", "/api/v1/domains/abc", "")
	assert.Equal(t, 422, status)
}

func TestHandlerUpdateNameservers(t *testing.T) {
	svc := &fakeService{
		UpdateNameserversFn: func(ctx context.Context, clientID, domainID int64, ns []string) (*domain.Domain, error) {
			assert.Equal(t, []string{"ns1.a.com", "ns2.a.com"}, ns)
			return &domain.Domain{ID: domainID}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	status, _ := doJSON(t, app, "PATCH", "/api/v1/domains/10/nameservers",
		`{"nameservers":["ns1.a.com","ns2.a.com"]}`)
	assert.Equal(t, 200, status)

	// Fewer than 2 rejected by DTO validation.
	status, _ = doJSON(t, app, "PATCH", "/api/v1/domains/10/nameservers",
		`{"nameservers":["ns1.a.com"]}`)
	assert.Equal(t, 422, status)
}

func TestHandlerDNS(t *testing.T) {
	recs := []ports.DNSRecord{{Type: "A", Host: "@", Value: "1.2.3.4", TTL: 3600}}
	svc := &fakeService{
		GetDNSFn: func(ctx context.Context, clientID, domainID int64) ([]ports.DNSRecord, error) {
			return recs, nil
		},
		UpdateDNSFn: func(ctx context.Context, clientID, domainID int64, in []ports.DNSRecord) ([]ports.DNSRecord, error) {
			require.Len(t, in, 1)
			assert.Equal(t, 3600, in[0].TTL) // defaulted
			assert.Equal(t, "A", in[0].Type)
			return in, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	status, env := doJSON(t, app, "GET", "/api/v1/domains/10/dns", "")
	assert.Equal(t, 200, status)
	assert.Len(t, env["data"].([]any), 1)

	status, _ = doJSON(t, app, "PUT", "/api/v1/domains/10/dns",
		`{"records":[{"type":"a","value":"1.2.3.4"}]}`)
	assert.Equal(t, 422, status) // lowercase type fails oneof

	status, _ = doJSON(t, app, "PUT", "/api/v1/domains/10/dns",
		`{"records":[{"type":"A","value":"1.2.3.4"}]}`)
	assert.Equal(t, 200, status)
}

func TestHandlerEPPAndRenew(t *testing.T) {
	svc := &fakeService{
		GetEPPFn: func(ctx context.Context, clientID, domainID int64) (string, error) {
			return "epp-secret", nil
		},
		RenewNowFn: func(ctx context.Context, clientID, domainID int64) (*domain.Invoice, error) {
			return &domain.Invoice{ID: 42, Status: domain.InvoiceUnpaid}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	status, env := doJSON(t, app, "GET", "/api/v1/domains/10/epp", "")
	assert.Equal(t, 200, status)
	assert.Equal(t, "epp-secret", env["data"].(map[string]any)["epp_code"])

	status, env = doJSON(t, app, "POST", "/api/v1/domains/10/renew", "")
	assert.Equal(t, 201, status)
	assert.EqualValues(t, 42, env["data"].(map[string]any)["id"])
}

func TestHandlerGetContact(t *testing.T) {
	svc := &fakeService{
		GetContactFn: func(_ context.Context, clientID, domainID int64) (*ports.RegistrantContact, error) {
			assert.Equal(t, int64(10), domainID)
			return &ports.RegistrantContact{FirstName: "Budi", Email: "budi@example.com"}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	status, env := doJSON(t, app, "GET", "/api/v1/domains/10/contact", "")
	assert.Equal(t, 200, status)
	assert.Equal(t, "budi@example.com", env["data"].(map[string]any)["email"])
}

func TestHandlerUpdateDomain(t *testing.T) {
	var got domains.UpdateDomainRequest
	svc := &fakeService{
		UpdateDomainFn: func(ctx context.Context, clientID, domainID int64, in domains.UpdateDomainRequest) (*domain.Domain, error) {
			got = in
			return &domain.Domain{ID: domainID}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	status, _ := doJSON(t, app, "PATCH", "/api/v1/domains/10", `{"auto_renew":false}`)
	assert.Equal(t, 200, status)
	require.NotNil(t, got.AutoRenew)
	assert.False(t, *got.AutoRenew)

	// Contact missing required fields fails DTO validation.
	status, _ = doJSON(t, app, "PATCH", "/api/v1/domains/10", `{"contact":{"first_name":"A"}}`)
	assert.Equal(t, 422, status)
}

// Admin routes

func TestHandlerAdminGuards(t *testing.T) {
	// Client role cannot access admin routes.
	app := newApp(&fakeService{}, &fakeMW{identity: clientIdentity()})
	status, _ := doJSON(t, app, "GET", "/api/v1/admin/domains/", "")
	assert.Equal(t, 403, status)

	// Staff without the domains permission.
	app = newApp(&fakeService{}, &fakeMW{
		identity:    httpx.AuthIdentity{UserID: 2, Role: "staff"},
		permissions: map[string]bool{},
	})
	status, _ = doJSON(t, app, "GET", "/api/v1/admin/domains/", "")
	assert.Equal(t, 403, status)

	// Staff cannot manage registrars (admin only).
	app = newApp(&fakeService{}, &fakeMW{
		identity:    httpx.AuthIdentity{UserID: 2, Role: "staff"},
		permissions: map[string]bool{"domains": true},
	})
	status, _ = doJSON(t, app, "GET", "/api/v1/admin/registrars/", "")
	assert.Equal(t, 403, status)
}

func TestHandlerAdminDomains(t *testing.T) {
	svc := &fakeService{
		AdminListFn: func(ctx context.Context, p ports.ListParams) ([]domain.Domain, int64, error) {
			return []domain.Domain{{ID: 10, Name: "example.com"}}, 1, nil
		},
		AdminGetFn: func(ctx context.Context, id int64) (*domain.Domain, error) {
			return &domain.Domain{ID: id, Name: "example.com"}, nil
		},
		AdminSyncFn: func(ctx context.Context, actorUserID, id int64) (*domain.Domain, error) {
			assert.EqualValues(t, 1, actorUserID)
			return &domain.Domain{ID: id, Status: domain.DomainActive}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: adminIdentity()})

	status, _ := doJSON(t, app, "GET", "/api/v1/admin/domains/", "")
	assert.Equal(t, 200, status)

	status, _ = doJSON(t, app, "GET", "/api/v1/admin/domains/10", "")
	assert.Equal(t, 200, status)

	status, _ = doJSON(t, app, "POST", "/api/v1/admin/domains/10/sync", "")
	assert.Equal(t, 200, status)

	status, env := doJSON(t, app, "POST", "/api/v1/admin/domains/10/renew", "")
	assert.Equal(t, 200, status)
	assert.Equal(t, true, env["data"].(map[string]any)["enqueued"])
}

func TestHandlerAdminCreate(t *testing.T) {
	svc := &fakeService{
		AdminCreateFn: func(ctx context.Context, actorUserID int64, in domains.AdminCreateDomainRequest) (*domain.Domain, error) {
			assert.EqualValues(t, 1, actorUserID)
			assert.EqualValues(t, 5, in.ClientID)
			assert.Equal(t, "imported.example.com", in.Name)
			assert.EqualValues(t, 180000, in.RecurringAmount)
			return &domain.Domain{ID: 33, Name: in.Name, Status: domain.DomainActive}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: adminIdentity()})

	status, env := doJSON(t, app, "POST", "/api/v1/admin/domains/",
		`{"client_id":5,"name":"imported.example.com","next_due_date":"2027-03-01","recurring_amount":180000}`)
	assert.Equal(t, 201, status)
	assert.EqualValues(t, 33, env["data"].(map[string]any)["id"])

	// DTO validation happens in the handler: a missing next_due_date -> 422.
	status, _ = doJSON(t, app, "POST", "/api/v1/admin/domains/",
		`{"client_id":5,"name":"imported.example.com"}`)
	assert.Equal(t, 422, status)

	// Malformed body -> 422.
	status, _ = doJSON(t, app, "POST", "/api/v1/admin/domains/", `{`)
	assert.Equal(t, 422, status)
}

func TestHandlerAdminUpdate(t *testing.T) {
	svc := &fakeService{
		AdminUpdateFn: func(ctx context.Context, actorUserID, id int64, in domains.AdminUpdateDomainRequest) (*domain.Domain, error) {
			assert.EqualValues(t, 1, actorUserID)
			require.NotNil(t, in.AutoRenew)
			assert.False(t, *in.AutoRenew)
			return &domain.Domain{ID: id, Status: domain.DomainActive, AutoRenew: *in.AutoRenew}, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: adminIdentity()})

	status, env := doJSON(t, app, "PATCH", "/api/v1/admin/domains/10", `{"auto_renew":false}`)
	assert.Equal(t, 200, status)
	assert.Equal(t, false, env["data"].(map[string]any)["auto_renew"])

	t.Run("invalid status rejected by dto validation", func(t *testing.T) {
		status, _ := doJSON(t, app, "PATCH", "/api/v1/admin/domains/10", `{"status":"bogus"}`)
		assert.Equal(t, 422, status)
	})

	t.Run("conflict propagated", func(t *testing.T) {
		svc := &fakeService{
			AdminUpdateFn: func(ctx context.Context, actorUserID, id int64, in domains.AdminUpdateDomainRequest) (*domain.Domain, error) {
				return nil, apperr.Conflict("domain cannot transition from active to pending")
			},
		}
		app := newApp(svc, &fakeMW{identity: adminIdentity()})
		status, _ := doJSON(t, app, "PATCH", "/api/v1/admin/domains/10", `{"status":"pending"}`)
		assert.Equal(t, 409, status)
	})

	t.Run("not found propagated", func(t *testing.T) {
		svc := &fakeService{
			AdminUpdateFn: func(ctx context.Context, actorUserID, id int64, in domains.AdminUpdateDomainRequest) (*domain.Domain, error) {
				return nil, apperr.NotFound("domain")
			},
		}
		app := newApp(svc, &fakeMW{identity: adminIdentity()})
		status, _ := doJSON(t, app, "PATCH", "/api/v1/admin/domains/999", `{"auto_renew":true}`)
		assert.Equal(t, 404, status)
	})

	t.Run("guards: staff without permission cannot patch", func(t *testing.T) {
		app := newApp(&fakeService{}, &fakeMW{
			identity:    httpx.AuthIdentity{UserID: 2, Role: "staff"},
			permissions: map[string]bool{},
		})
		status, _ := doJSON(t, app, "PATCH", "/api/v1/admin/domains/10", `{"auto_renew":true}`)
		assert.Equal(t, 403, status)
	})
}

func TestHandlerRegistrars(t *testing.T) {
	svc := &fakeService{
		ListRegistrarsFn: func(ctx context.Context) ([]domain.Registrar, error) {
			return []domain.Registrar{{ID: 1, Name: "rdash", Active: true, ResellerID: "584", BaseURL: "http://localhost:9090/v1"}}, nil
		},
		UpdateRegistrarFn: func(ctx context.Context, actorUserID, id int64, in domains.UpdateRegistrarRequest) (*domain.Registrar, error) {
			assert.EqualValues(t, 1, actorUserID)
			require.NotNil(t, in.Active)
			reg := &domain.Registrar{ID: id, Name: "rdash", Active: *in.Active}
			if in.BaseURL != nil {
				reg.BaseURL = *in.BaseURL
			}
			return reg, nil
		},
		RegistrarAPIKeyPresentFn: func() bool { return true },
	}
	app := newApp(svc, &fakeMW{identity: adminIdentity()})

	status, env := doJSON(t, app, "GET", "/api/v1/admin/registrars/", "")
	assert.Equal(t, 200, status)
	list := env["data"].([]any)
	assert.Len(t, list, 1)
	assert.Equal(t, true, list[0].(map[string]any)["api_key_present"])
	assert.Equal(t, "584", list[0].(map[string]any)["reseller_id"])
	assert.Equal(t, "http://localhost:9090/v1", list[0].(map[string]any)["base_url"])

	status, env = doJSON(t, app, "GET", "/api/v1/admin/registrars/1", "")
	assert.Equal(t, 200, status)
	assert.Equal(t, true, env["data"].(map[string]any)["api_key_present"])

	status, env = doJSON(t, app, "PUT", "/api/v1/admin/registrars/1", `{"active":false}`)
	assert.Equal(t, 200, status)
	assert.Equal(t, true, env["data"].(map[string]any)["api_key_present"])

	t.Run("base_url passes through the update request", func(t *testing.T) {
		status, env := doJSON(t, app, "PUT", "/api/v1/admin/registrars/1", `{"active":true,"base_url":"http://localhost:9090/v1"}`)
		assert.Equal(t, 200, status)
		assert.Equal(t, "http://localhost:9090/v1", env["data"].(map[string]any)["base_url"])
	})

	t.Run("api key absent from both DB and env", func(t *testing.T) {
		svc := &fakeService{
			ListRegistrarsFn: func(ctx context.Context) ([]domain.Registrar, error) {
				return []domain.Registrar{{ID: 1, Name: "rdash", Active: true}}, nil
			},
			RegistrarAPIKeyPresentFn: func() bool { return false },
		}
		app := newApp(svc, &fakeMW{identity: adminIdentity()})
		_, env := doJSON(t, app, "GET", "/api/v1/admin/registrars/", "")
		assert.Equal(t, false, env["data"].([]any)[0].(map[string]any)["api_key_present"])
	})

	t.Run("api key present from DB alone (env unset)", func(t *testing.T) {
		svc := &fakeService{
			ListRegistrarsFn: func(ctx context.Context) ([]domain.Registrar, error) {
				return []domain.Registrar{{ID: 1, Name: "rdash", Active: true, APIKeyEnc: "ciphertext"}}, nil
			},
			RegistrarAPIKeyPresentFn: func() bool { return false },
		}
		app := newApp(svc, &fakeMW{identity: adminIdentity()})
		_, env := doJSON(t, app, "GET", "/api/v1/admin/registrars/", "")
		row := env["data"].([]any)[0].(map[string]any)
		assert.Equal(t, true, row["api_key_present"], "DB-stored key alone is enough")
		_, hasEnc := row["api_key_enc"]
		assert.False(t, hasEnc, "the ciphertext itself must never appear in a response")
	})

	t.Run("test ok / fail / missing", func(t *testing.T) {
		svc := &fakeService{}
		app := newApp(svc, &fakeMW{identity: adminIdentity()})
		status, env := doJSON(t, app, "POST", "/api/v1/admin/registrars/1/test", "")
		assert.Equal(t, 200, status)
		assert.Equal(t, true, env["data"].(map[string]any)["ok"])

		svc.TestRegistrarFn = func(ctx context.Context, id int64) error {
			return apperr.External("rdash", assert.AnError)
		}
		status, env = doJSON(t, app, "POST", "/api/v1/admin/registrars/1/test", "")
		assert.Equal(t, 200, status)
		assert.Equal(t, false, env["data"].(map[string]any)["ok"])

		svc.TestRegistrarFn = func(ctx context.Context, id int64) error {
			return apperr.NotFound("registrar")
		}
		status, _ = doJSON(t, app, "POST", "/api/v1/admin/registrars/99/test", "")
		assert.Equal(t, 404, status)
	})
}
