package domains

import (
	"context"
	"strconv"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/validate"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
)

// Middlewares is the narrow middleware surface the handler needs; satisfied
// by *transporthttp.Middleware.
type Middlewares interface {
	RequireAuth() fiber.Handler
	RequireRole(roles ...string) fiber.Handler
	RequirePermission(module string) fiber.Handler
	RequireClient() fiber.Handler
	RateLimit(prefix string, limit int, window time.Duration) fiber.Handler
}

var _ Middlewares = (*transporthttp.Middleware)(nil)

// DomainService is the use-case surface consumed by the handler (implemented
// by *Service).
type DomainService interface {
	CheckAvailability(ctx context.Context, names []string) ([]ports.DomainAvailability, error)
	ListForClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Domain, int64, error)
	GetForClient(ctx context.Context, clientID, domainID int64) (*domain.Domain, error)
	UpdateDomain(ctx context.Context, clientID, domainID int64, in UpdateDomainRequest) (*domain.Domain, error)
	UpdateNameservers(ctx context.Context, clientID, domainID int64, ns []string) (*domain.Domain, error)
	GetDNS(ctx context.Context, clientID, domainID int64) ([]ports.DNSRecord, error)
	UpdateDNS(ctx context.Context, clientID, domainID int64, recs []ports.DNSRecord) ([]ports.DNSRecord, error)
	GetEPP(ctx context.Context, clientID, domainID int64) (string, error)
	GetContact(ctx context.Context, clientID, domainID int64) (*ports.RegistrantContact, error)
	RenewNow(ctx context.Context, clientID, domainID int64) (*domain.Invoice, error)
	UpdateDomainAddons(ctx context.Context, clientID, domainID int64, keys []string) (*domain.Domain, error)

	AdminList(ctx context.Context, p ports.ListParams) ([]domain.Domain, int64, error)
	AdminCreate(ctx context.Context, actorUserID int64, in AdminCreateDomainRequest) (*domain.Domain, error)
	AdminGet(ctx context.Context, id int64) (*domain.Domain, error)
	AdminUpdate(ctx context.Context, actorUserID, id int64, in AdminUpdateDomainRequest) (*domain.Domain, error)
	AdminSync(ctx context.Context, actorUserID, id int64) (*domain.Domain, error)
	AdminForceRenew(ctx context.Context, actorUserID, id int64) error
	ListRegistrars(ctx context.Context) ([]domain.Registrar, error)
	GetRegistrar(ctx context.Context, id int64) (*domain.Registrar, error)
	UpdateRegistrar(ctx context.Context, actorUserID, id int64, in UpdateRegistrarRequest) (*domain.Registrar, error)
	TestRegistrar(ctx context.Context, id int64) error
	RegistrarAPIKeyPresent() bool
	ListRegistrarCatalog(ctx context.Context, registrarID int64) ([]RegistrarCatalogResponse, error)

	ListActiveTLDs(ctx context.Context) ([]domain.TLDPricing, error)
	ListActiveDomainAddons(ctx context.Context) ([]domain.DomainAddon, error)

	ListTLDPricing(ctx context.Context) ([]domain.TLDPricing, error)
	GetTLDPricing(ctx context.Context, id int64) (*domain.TLDPricing, error)
	CreateTLDPricing(ctx context.Context, actorUserID int64, in TLDPricingRequest) (*domain.TLDPricing, error)
	UpdateTLDPricing(ctx context.Context, actorUserID, id int64, in TLDPricingRequest) (*domain.TLDPricing, error)
	DeleteTLDPricing(ctx context.Context, actorUserID, id int64) error
	ImportTLDPricing(ctx context.Context, actorUserID int64, in ImportTLDPricingRequest) (*ImportTLDPricingResponse, error)

	ListPremiumPricing(ctx context.Context) ([]domain.PremiumDomainPricing, error)
	CreatePremiumPricing(ctx context.Context, actorUserID int64, in PremiumDomainPricingRequest) (*domain.PremiumDomainPricing, error)
	UpdatePremiumPricing(ctx context.Context, actorUserID, id int64, in PremiumDomainPricingRequest) (*domain.PremiumDomainPricing, error)
	DeletePremiumPricing(ctx context.Context, actorUserID, id int64) error

	ListPremiumLengthPricing(ctx context.Context) ([]domain.PremiumLengthPricing, error)
	CreatePremiumLengthPricing(ctx context.Context, actorUserID int64, in PremiumLengthPricingRequest) (*domain.PremiumLengthPricing, error)
	UpdatePremiumLengthPricing(ctx context.Context, actorUserID, id int64, in PremiumLengthPricingRequest) (*domain.PremiumLengthPricing, error)
	DeletePremiumLengthPricing(ctx context.Context, actorUserID, id int64) error

	ListDomainAddons(ctx context.Context) ([]domain.DomainAddon, error)
	UpdateDomainAddon(ctx context.Context, actorUserID, id int64, in UpdateDomainAddonRequest) (*domain.DomainAddon, error)
}

var _ DomainService = (*Service)(nil)

// Handler exposes the domains HTTP endpoints.
type Handler struct {
	svc DomainService
	mw  Middlewares
	val *validate.Validator
}

// NewHandler builds the domains Handler.
func NewHandler(svc DomainService, mw Middlewares) *Handler {
	return &Handler{svc: svc, mw: mw, val: validate.New()}
}

// RegisterRoutes mounts the domains routes on r (the /api/v1 group). See the
// package doc for the full route table.
func (h *Handler) RegisterRoutes(r fiber.Router) {
	// Public availability check, rate-limited 20/min/IP.
	r.Post("/domains/check", h.mw.RateLimit("domain_check", 20, time.Minute), h.Check)
	r.Get("/domains/tlds", h.ListActiveTLDs)
	r.Get("/domains/addons", h.ListActiveDomainAddons)

	cl := r.Group("/domains", h.mw.RequireAuth(), h.mw.RequireClient())
	cl.Get("/", h.List)
	cl.Get("/:id", h.Get)
	cl.Patch("/:id", h.Update)
	cl.Get("/:id/contact", h.GetContact)
	cl.Patch("/:id/nameservers", h.UpdateNameservers)
	cl.Get("/:id/dns", h.GetDNS)
	cl.Put("/:id/dns", h.UpdateDNS)
	cl.Get("/:id/epp", h.GetEPP)
	cl.Post("/:id/renew", h.Renew)
	cl.Post("/:id/addons", h.UpdateDomainAddons)

	ad := r.Group("/admin/domains",
		h.mw.RequireAuth(), h.mw.RequireRole("admin", "staff"), h.mw.RequirePermission("domains"))
	ad.Get("/", h.AdminList)
	ad.Post("/", h.AdminCreate)
	ad.Get("/:id", h.AdminGet)
	ad.Patch("/:id", h.AdminUpdate)
	ad.Post("/:id/sync", h.AdminSync)
	ad.Post("/:id/renew", h.AdminForceRenew)

	reg := r.Group("/admin/registrars", h.mw.RequireAuth(), h.mw.RequireRole("admin"))
	reg.Get("/", h.ListRegistrars)
	reg.Get("/:id", h.GetRegistrar)
	reg.Put("/:id", h.UpdateRegistrar)
	reg.Post("/:id/test", h.TestRegistrar)
	reg.Get("/:id/catalog", h.ListRegistrarCatalog)

	tp := r.Group("/admin/tld-pricing", h.mw.RequireAuth(), h.mw.RequireRole("admin"))
	tp.Get("/", h.ListTLDPricing)
	tp.Post("/", h.CreateTLDPricing)
	tp.Post("/import", h.ImportTLDPricing)
	tp.Get("/:id", h.GetTLDPricing)
	tp.Put("/:id", h.UpdateTLDPricing)
	tp.Delete("/:id", h.DeleteTLDPricing)

	pp := r.Group("/admin/premium-domain-pricing", h.mw.RequireAuth(), h.mw.RequireRole("admin"))
	pp.Get("/", h.ListPremiumPricing)
	pp.Post("/", h.CreatePremiumPricing)
	pp.Put("/:id", h.UpdatePremiumPricing)
	pp.Delete("/:id", h.DeletePremiumPricing)

	pl := r.Group("/admin/premium-length-pricing", h.mw.RequireAuth(), h.mw.RequireRole("admin"))
	pl.Get("/", h.ListPremiumLengthPricing)
	pl.Post("/", h.CreatePremiumLengthPricing)
	pl.Put("/:id", h.UpdatePremiumLengthPricing)
	pl.Delete("/:id", h.DeletePremiumLengthPricing)

	da := r.Group("/admin/domain-addons", h.mw.RequireAuth(), h.mw.RequireRole("admin"))
	da.Get("/", h.ListDomainAddons)
	da.Put("/:id", h.UpdateDomainAddon)
}

// Public

// Check handles POST /domains/check.
func (h *Handler) Check(c fiber.Ctx) error {
	var req CheckRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	res, err := h.svc.CheckAvailability(c.Context(), req.Names)
	if err != nil {
		return err
	}
	return httpx.OK(c, res)
}

// ListActiveTLDs handles GET /domains/tlds - the storefront's sellable TLD
// list (replaces a hardcoded frontend list).
func (h *Handler) ListActiveTLDs(c fiber.Ctx) error {
	tlds, err := h.svc.ListActiveTLDs(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, tlds)
}

// ListActiveDomainAddons handles GET /domains/addons - the storefront's
// sellable domain-addon list.
func (h *Handler) ListActiveDomainAddons(c fiber.Ctx) error {
	addons, err := h.svc.ListActiveDomainAddons(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, addons)
}

// Client

// List handles GET /domains.
func (h *Handler) List(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	page := httpx.ParsePage(c)
	items, total, err := h.svc.ListForClient(c.Context(), id.ClientID, ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
		Status:  c.Query("status"),
		Search:  c.Query("search"),
	})
	if err != nil {
		return err
	}
	return httpx.OK(c, items, page.Meta(total))
}

// Get handles GET /domains/:id.
func (h *Handler) Get(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	domainID, err := parseID(c)
	if err != nil {
		return err
	}
	dom, err := h.svc.GetForClient(c.Context(), id.ClientID, domainID)
	if err != nil {
		return err
	}
	return httpx.OK(c, dom)
}

// Update handles PATCH /domains/:id (auto_renew, contact).
func (h *Handler) Update(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	domainID, err := parseID(c)
	if err != nil {
		return err
	}
	var req UpdateDomainRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if req.Contact != nil {
		if err := h.val.Struct(*req.Contact); err != nil {
			return err
		}
	}
	dom, err := h.svc.UpdateDomain(c.Context(), id.ClientID, domainID, req)
	if err != nil {
		return err
	}
	return httpx.OK(c, dom)
}

// UpdateNameservers handles PATCH /domains/:id/nameservers.
func (h *Handler) UpdateNameservers(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	domainID, err := parseID(c)
	if err != nil {
		return err
	}
	var req UpdateNameserversRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	dom, err := h.svc.UpdateNameservers(c.Context(), id.ClientID, domainID, req.Nameservers)
	if err != nil {
		return err
	}
	return httpx.OK(c, dom)
}

// GetDNS handles GET /domains/:id/dns.
func (h *Handler) GetDNS(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	domainID, err := parseID(c)
	if err != nil {
		return err
	}
	recs, err := h.svc.GetDNS(c.Context(), id.ClientID, domainID)
	if err != nil {
		return err
	}
	return httpx.OK(c, recs)
}

// UpdateDNS handles PUT /domains/:id/dns.
func (h *Handler) UpdateDNS(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	domainID, err := parseID(c)
	if err != nil {
		return err
	}
	var req UpdateDNSRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	recs, err := h.svc.UpdateDNS(c.Context(), id.ClientID, domainID, req.Ports())
	if err != nil {
		return err
	}
	return httpx.OK(c, recs)
}

// GetEPP handles GET /domains/:id/epp.
func (h *Handler) GetEPP(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	domainID, err := parseID(c)
	if err != nil {
		return err
	}
	code, err := h.svc.GetEPP(c.Context(), id.ClientID, domainID)
	if err != nil {
		return err
	}
	return httpx.OK(c, EPPResponse{EPPCode: code})
}

// GetContact handles GET /domains/:id/contact.
func (h *Handler) GetContact(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	domainID, err := parseID(c)
	if err != nil {
		return err
	}
	contact, err := h.svc.GetContact(c.Context(), id.ClientID, domainID)
	if err != nil {
		return err
	}
	return httpx.OK(c, contact)
}

// Renew handles POST /domains/:id/renew - creates a renewal invoice now.
func (h *Handler) Renew(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	domainID, err := parseID(c)
	if err != nil {
		return err
	}
	inv, err := h.svc.RenewNow(c.Context(), id.ClientID, domainID)
	if err != nil {
		return err
	}
	return httpx.Created(c, inv)
}

// UpdateDomainAddons handles POST /domains/:id/addons (client self-service -
// replaces the set of active addons and adjusts the recurring amount).
func (h *Handler) UpdateDomainAddons(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	domainID, err := parseID(c)
	if err != nil {
		return err
	}
	var req UpdateDomainAddonsRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	dom, err := h.svc.UpdateDomainAddons(c.Context(), id.ClientID, domainID, req.Addons)
	if err != nil {
		return err
	}
	return httpx.OK(c, dom)
}

// Admin

// AdminList handles GET /admin/domains.
func (h *Handler) AdminList(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	items, total, err := h.svc.AdminList(c.Context(), ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
		Status:  c.Query("status"),
		Search:  c.Query("search"),
	})
	if err != nil {
		return err
	}
	return httpx.OK(c, items, page.Meta(total))
}

// AdminCreate handles POST /admin/domains - records an already-registered
// domain on a client with no order, payment, or registrar call.
func (h *Handler) AdminCreate(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	var req AdminCreateDomainRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	dom, err := h.svc.AdminCreate(c.Context(), actor.UserID, req)
	if err != nil {
		return err
	}
	return httpx.Created(c, dom)
}

// AdminGet handles GET /admin/domains/:id.
func (h *Handler) AdminGet(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	dom, err := h.svc.AdminGet(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, dom)
}

// AdminUpdate handles PATCH /admin/domains/:id (status, auto_renew, nameservers).
func (h *Handler) AdminUpdate(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var req AdminUpdateDomainRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	dom, err := h.svc.AdminUpdate(c.Context(), actor.UserID, id, req)
	if err != nil {
		return err
	}
	return httpx.OK(c, dom)
}

// AdminSync handles POST /admin/domains/:id/sync (synchronous registrar sync).
func (h *Handler) AdminSync(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	dom, err := h.svc.AdminSync(c.Context(), actor.UserID, id)
	if err != nil {
		return err
	}
	return httpx.OK(c, dom)
}

// AdminForceRenew handles POST /admin/domains/:id/renew (enqueues registrar renew).
func (h *Handler) AdminForceRenew(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := h.svc.AdminForceRenew(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{"enqueued": true})
}

// ListRegistrars handles GET /admin/registrars.
func (h *Handler) ListRegistrars(c fiber.Ctx) error {
	regs, err := h.svc.ListRegistrars(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, toRegistrarResponses(regs, h.svc.RegistrarAPIKeyPresent()))
}

// GetRegistrar handles GET /admin/registrars/:id.
func (h *Handler) GetRegistrar(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	reg, err := h.svc.GetRegistrar(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, toRegistrarResponse(reg, h.svc.RegistrarAPIKeyPresent()))
}

// UpdateRegistrar handles PUT /admin/registrars/:id.
func (h *Handler) UpdateRegistrar(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var req UpdateRegistrarRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	reg, err := h.svc.UpdateRegistrar(c.Context(), actor.UserID, id, req)
	if err != nil {
		return err
	}
	return httpx.OK(c, toRegistrarResponse(reg, h.svc.RegistrarAPIKeyPresent()))
}

// TestRegistrar handles POST /admin/registrars/:id/test.
func (h *Handler) TestRegistrar(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := h.svc.TestRegistrar(c.Context(), id); err != nil {
		e := apperr.From(err)
		if e.Code == apperr.CodeNotFound {
			return err
		}
		return httpx.OK(c, TestRegistrarResponse{OK: false, Message: e.Message})
	}
	return httpx.OK(c, TestRegistrarResponse{OK: true})
}

// ListRegistrarCatalog handles GET /admin/registrars/:id/catalog - the
// registrar's full TLD pricelist, for the "Import from Registrar" preview.
func (h *Handler) ListRegistrarCatalog(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	list, err := h.svc.ListRegistrarCatalog(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, list)
}

// ListTLDPricing handles GET /admin/tld-pricing.
func (h *Handler) ListTLDPricing(c fiber.Ctx) error {
	list, err := h.svc.ListTLDPricing(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, list)
}

// GetTLDPricing handles GET /admin/tld-pricing/:id.
func (h *Handler) GetTLDPricing(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	p, err := h.svc.GetTLDPricing(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, p)
}

// CreateTLDPricing handles POST /admin/tld-pricing.
func (h *Handler) CreateTLDPricing(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	var req TLDPricingRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	p, err := h.svc.CreateTLDPricing(c.Context(), actor.UserID, req)
	if err != nil {
		return err
	}
	return httpx.Created(c, p)
}

// UpdateTLDPricing handles PUT /admin/tld-pricing/:id.
func (h *Handler) UpdateTLDPricing(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var req TLDPricingRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	p, err := h.svc.UpdateTLDPricing(c.Context(), actor.UserID, id, req)
	if err != nil {
		return err
	}
	return httpx.OK(c, p)
}

// DeleteTLDPricing handles DELETE /admin/tld-pricing/:id.
func (h *Handler) DeleteTLDPricing(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteTLDPricing(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// ImportTLDPricing handles POST /admin/tld-pricing/import.
func (h *Handler) ImportTLDPricing(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	var req ImportTLDPricingRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	res, err := h.svc.ImportTLDPricing(c.Context(), actor.UserID, req)
	if err != nil {
		return err
	}
	return httpx.OK(c, res)
}

// ListPremiumPricing handles GET /admin/premium-domain-pricing.
func (h *Handler) ListPremiumPricing(c fiber.Ctx) error {
	list, err := h.svc.ListPremiumPricing(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, list)
}

// CreatePremiumPricing handles POST /admin/premium-domain-pricing.
func (h *Handler) CreatePremiumPricing(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	var req PremiumDomainPricingRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	p, err := h.svc.CreatePremiumPricing(c.Context(), actor.UserID, req)
	if err != nil {
		return err
	}
	return httpx.Created(c, p)
}

// UpdatePremiumPricing handles PUT /admin/premium-domain-pricing/:id.
func (h *Handler) UpdatePremiumPricing(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var req PremiumDomainPricingRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	p, err := h.svc.UpdatePremiumPricing(c.Context(), actor.UserID, id, req)
	if err != nil {
		return err
	}
	return httpx.OK(c, p)
}

// DeletePremiumPricing handles DELETE /admin/premium-domain-pricing/:id.
func (h *Handler) DeletePremiumPricing(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := h.svc.DeletePremiumPricing(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// ListPremiumLengthPricing handles GET /admin/premium-length-pricing.
func (h *Handler) ListPremiumLengthPricing(c fiber.Ctx) error {
	list, err := h.svc.ListPremiumLengthPricing(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, list)
}

// CreatePremiumLengthPricing handles POST /admin/premium-length-pricing.
func (h *Handler) CreatePremiumLengthPricing(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	var req PremiumLengthPricingRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	p, err := h.svc.CreatePremiumLengthPricing(c.Context(), actor.UserID, req)
	if err != nil {
		return err
	}
	return httpx.Created(c, p)
}

// UpdatePremiumLengthPricing handles PUT /admin/premium-length-pricing/:id.
func (h *Handler) UpdatePremiumLengthPricing(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var req PremiumLengthPricingRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	p, err := h.svc.UpdatePremiumLengthPricing(c.Context(), actor.UserID, id, req)
	if err != nil {
		return err
	}
	return httpx.OK(c, p)
}

// DeletePremiumLengthPricing handles DELETE /admin/premium-length-pricing/:id.
func (h *Handler) DeletePremiumLengthPricing(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	if err := h.svc.DeletePremiumLengthPricing(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// ListDomainAddons handles GET /admin/domain-addons.
func (h *Handler) ListDomainAddons(c fiber.Ctx) error {
	list, err := h.svc.ListDomainAddons(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, list)
}

// UpdateDomainAddon handles PUT /admin/domain-addons/:id.
func (h *Handler) UpdateDomainAddon(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var req UpdateDomainAddonRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	a, err := h.svc.UpdateDomainAddon(c.Context(), actor.UserID, id, req)
	if err != nil {
		return err
	}
	return httpx.OK(c, a)
}

// Helpers

func parseID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.Validation("invalid id")
	}
	return id, nil
}
