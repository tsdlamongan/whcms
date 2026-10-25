package snipe

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

// SnipeService is the use-case surface consumed by the handler (implemented
// by *Service).
type SnipeService interface {
	AdminCreate(ctx context.Context, actorUserID int64, in AdminCreateRequest) (*domain.DomainSnipe, error)
	AdminGet(ctx context.Context, id int64) (*Detail, error)
	AdminList(ctx context.Context, p ports.ListParams) ([]domain.DomainSnipe, int64, error)
	AdminCancel(ctx context.Context, actorUserID, id int64, refundFee bool) (*domain.DomainSnipe, error)
	Status(ctx context.Context) (*StatusResponse, error)

	// Client area (always scoped to the caller's client).
	ListActivePlans(ctx context.Context) ([]PlanView, error)
	Quote(ctx context.Context, in QuoteRequest) (*Quote, error)
	Create(ctx context.Context, who ClientIdentity, in CreateRequest) (*ClientSnipe, error)
	ListForClient(ctx context.Context, clientID int64, p ports.ListParams) ([]ClientSnipe, int64, error)
	GetForClient(ctx context.Context, clientID, id int64) (*ClientDetail, error)
	CancelForClient(ctx context.Context, who ClientIdentity, id int64) (*ClientSnipe, error)

	// Admin plans.
	ListPlans(ctx context.Context) ([]domain.SnipePlan, error)
	CreatePlan(ctx context.Context, actorUserID int64, in PlanRequest) (*domain.SnipePlan, error)
	UpdatePlan(ctx context.Context, actorUserID, id int64, in PlanRequest) (*domain.SnipePlan, error)
}

var _ SnipeService = (*Service)(nil)

// Handler exposes the domain-snipe HTTP endpoints.
type Handler struct {
	svc SnipeService
	mw  Middlewares
	val *validate.Validator
}

// NewHandler builds the snipe Handler.
func NewHandler(svc SnipeService, mw Middlewares) *Handler {
	return &Handler{svc: svc, mw: mw, val: validate.New()}
}

// RegisterRoutes mounts the snipe routes on r (the /api/v1 group):
//
//	GET  /snipe/plans              purchasable plans (client)
//	POST /snipe/quote              price a hunt without creating it (client)
//	GET  /snipes                   the client's snipes (?status&search&page&per_page)
//	POST /snipes                   start a paid hunt: returns the snipe + invoice_id
//	GET  /snipes/:id               one snipe with its visible trail
//	POST /snipes/:id/cancel        stop a hunt (domain price credited back if paid)
//
//	GET  /admin/snipes             list (?status&search&client_id&page&per_page)
//	POST /admin/snipes             start a free hunt on behalf of a client
//	GET  /admin/snipes/status      supervisor/reseller overview
//	GET  /admin/snipes/:id         one snipe with its activity trail
//	POST /admin/snipes/:id/cancel  stop a hunt ({refund_fee})
//
//	GET|POST /admin/snipe-plans    list / create plans (admin only)
//	PUT      /admin/snipe-plans/:id  edit a plan, incl. active on/off
//
// Admin snipe routes need the "domains" permission, like the rest of domain
// management; plan pricing is admin-only.
func (h *Handler) RegisterRoutes(r fiber.Router) {
	sp := r.Group("/snipe", h.mw.RequireAuth(), h.mw.RequireClient())
	sp.Get("/plans", h.ListActivePlans)
	sp.Post("/quote", h.mw.RateLimit("snipe_quote", 30, time.Minute), h.Quote)

	cl := r.Group("/snipes", h.mw.RequireAuth(), h.mw.RequireClient())
	cl.Get("/", h.ClientList)
	cl.Post("/", h.mw.RateLimit("snipe_create", 10, time.Minute), h.ClientCreate)
	cl.Get("/:id", h.ClientGet)
	cl.Post("/:id/cancel", h.ClientCancel)

	plans := r.Group("/admin/snipe-plans", h.mw.RequireAuth(), h.mw.RequireRole("admin"))
	plans.Get("/", h.AdminListPlans)
	plans.Post("/", h.AdminCreatePlan)
	plans.Put("/:id", h.AdminUpdatePlan)

	ad := r.Group("/admin/snipes",
		h.mw.RequireAuth(), h.mw.RequireRole("admin", "staff"), h.mw.RequirePermission("domains"))
	ad.Get("/", h.AdminList)
	ad.Post("/", h.AdminCreate)
	// The overview calls the registrar (profile + balance): keep it cheap.
	ad.Get("/status", h.mw.RateLimit("snipe_status", 30, time.Minute), h.Status)
	ad.Get("/:id", h.AdminGet)
	ad.Post("/:id/cancel", h.AdminCancel)
}

// AdminList handles GET /admin/snipes.
func (h *Handler) AdminList(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	clientID, err := httpx.QueryID(c, "client_id")
	if err != nil {
		return err
	}
	items, total, err := h.svc.AdminList(c.Context(), ports.ListParams{
		Page:     page.Page,
		PerPage:  page.PerPage,
		Status:   c.Query("status"),
		Search:   c.Query("search"),
		ClientID: clientID,
	})
	if err != nil {
		return err
	}
	if items == nil {
		items = []domain.DomainSnipe{}
	}
	return httpx.OK(c, items, page.Meta(total))
}

// AdminCreate handles POST /admin/snipes.
func (h *Handler) AdminCreate(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	var req AdminCreateRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	sn, err := h.svc.AdminCreate(c.Context(), actor.UserID, req)
	if err != nil {
		return err
	}
	return httpx.Created(c, sn)
}

// AdminGet handles GET /admin/snipes/:id.
func (h *Handler) AdminGet(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	d, err := h.svc.AdminGet(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, d, nil)
}

// AdminCancel handles POST /admin/snipes/:id/cancel. The body is optional.
func (h *Handler) AdminCancel(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var req AdminCancelRequest
	if len(c.Body()) > 0 {
		if err := c.Bind().Body(&req); err != nil {
			return apperr.Validation("invalid request body")
		}
	}
	sn, err := h.svc.AdminCancel(c.Context(), actor.UserID, id, req.RefundFee)
	if err != nil {
		return err
	}
	return httpx.OK(c, sn, nil)
}

// Status handles GET /admin/snipes/status.
func (h *Handler) Status(c fiber.Ctx) error {
	st, err := h.svc.Status(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, st, nil)
}

func parseID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.Validation("invalid id")
	}
	return id, nil
}

// Client area

func clientIdentity(c fiber.Ctx) ClientIdentity {
	id := httpx.MustIdentity(c)
	return ClientIdentity{UserID: id.UserID, ClientID: id.ClientID}
}

// ListActivePlans handles GET /snipe/plans.
func (h *Handler) ListActivePlans(c fiber.Ctx) error {
	plans, err := h.svc.ListActivePlans(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, plans, nil)
}

// Quote handles POST /snipe/quote.
func (h *Handler) Quote(c fiber.Ctx) error {
	var req QuoteRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	q, err := h.svc.Quote(c.Context(), req)
	if err != nil {
		return err
	}
	return httpx.OK(c, q, nil)
}

// ClientList handles GET /snipes.
func (h *Handler) ClientList(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	items, total, err := h.svc.ListForClient(c.Context(), clientIdentity(c).ClientID, ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
		Status:  c.Query("status"),
		Search:  c.Query("search"),
	})
	if err != nil {
		return err
	}
	if items == nil {
		items = []ClientSnipe{}
	}
	return httpx.OK(c, items, page.Meta(total))
}

// ClientCreate handles POST /snipes.
func (h *Handler) ClientCreate(c fiber.Ctx) error {
	var req CreateRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	sn, err := h.svc.Create(c.Context(), clientIdentity(c), req)
	if err != nil {
		return err
	}
	return httpx.Created(c, sn)
}

// ClientGet handles GET /snipes/:id.
func (h *Handler) ClientGet(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	d, err := h.svc.GetForClient(c.Context(), clientIdentity(c).ClientID, id)
	if err != nil {
		return err
	}
	return httpx.OK(c, d, nil)
}

// ClientCancel handles POST /snipes/:id/cancel.
func (h *Handler) ClientCancel(c fiber.Ctx) error {
	id, err := parseID(c)
	if err != nil {
		return err
	}
	sn, err := h.svc.CancelForClient(c.Context(), clientIdentity(c), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, sn, nil)
}

// Admin plans

// AdminListPlans handles GET /admin/snipe-plans.
func (h *Handler) AdminListPlans(c fiber.Ctx) error {
	plans, err := h.svc.ListPlans(c.Context())
	if err != nil {
		return err
	}
	return httpx.OK(c, plans, nil)
}

// AdminCreatePlan handles POST /admin/snipe-plans.
func (h *Handler) AdminCreatePlan(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	var req PlanRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	p, err := h.svc.CreatePlan(c.Context(), actor.UserID, req)
	if err != nil {
		return err
	}
	return httpx.Created(c, p)
}

// AdminUpdatePlan handles PUT /admin/snipe-plans/:id.
func (h *Handler) AdminUpdatePlan(c fiber.Ctx) error {
	actor := httpx.MustIdentity(c)
	id, err := parseID(c)
	if err != nil {
		return err
	}
	var req PlanRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(req); err != nil {
		return err
	}
	p, err := h.svc.UpdatePlan(c.Context(), actor.UserID, id, req)
	if err != nil {
		return err
	}
	return httpx.OK(c, p, nil)
}
