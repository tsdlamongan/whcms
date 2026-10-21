package orders

import (
	"context"
	"strconv"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
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
}

var _ Middlewares = (*transporthttp.Middleware)(nil)

// OrderService is the use-case surface consumed by the handler (implemented
// by *Service).
type OrderService interface {
	CreateOrder(ctx context.Context, clientID int64, ip string, in CreateOrderRequest) (*CheckoutResponse, error)
	ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Order, int64, error)
	List(ctx context.Context, p ports.ListParams) ([]domain.Order, int64, error)
	Get(ctx context.Context, clientID, orderID int64) (*OrderDetail, error)
	Accept(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error)
	Cancel(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error)
	MarkFraud(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error)
}

var _ OrderService = (*Service)(nil)

// Handler exposes the orders HTTP endpoints.
type Handler struct {
	svc OrderService
	mw  Middlewares
}

// NewHandler builds the orders Handler.
func NewHandler(svc OrderService, mw Middlewares) *Handler {
	return &Handler{svc: svc, mw: mw}
}

// RegisterRoutes mounts the orders routes on r (the /api/v1 group). See the
// package doc for the full route table.
func (h *Handler) RegisterRoutes(r fiber.Router) {
	client := r.Group("/orders", h.mw.RequireAuth(), h.mw.RequireClient())
	client.Post("/", h.Create)
	client.Get("/", h.ListMine)
	client.Get("/:id", h.GetMine)

	admin := r.Group("/admin/orders",
		h.mw.RequireAuth(), h.mw.RequireRole("admin", "staff"), h.mw.RequirePermission("orders"))
	admin.Get("/", h.AdminList)
	admin.Get("/:id", h.AdminGet)
	admin.Post("/:id/accept", h.Accept)
	admin.Post("/:id/cancel", h.Cancel)
	admin.Post("/:id/fraud", h.Fraud)
}

// Create handles POST /orders (checkout).
func (h *Handler) Create(c fiber.Ctx) error {
	var in CreateOrderRequest
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	res, err := h.svc.CreateOrder(c.Context(), id.ClientID, c.IP(), in)
	if err != nil {
		return err
	}
	return httpx.Created(c, res)
}

// ListMine handles GET /orders.
func (h *Handler) ListMine(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	page := httpx.ParsePage(c)
	orders, total, err := h.svc.ListByClient(c.Context(), id.ClientID, ports.ListParams{
		Page: page.Page, PerPage: page.PerPage, Status: c.Query("status"),
	})
	if err != nil {
		return err
	}
	return httpx.OK(c, orders, page.Meta(total))
}

// GetMine handles GET /orders/:id.
func (h *Handler) GetMine(c fiber.Ctx) error {
	orderID, err := paramID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	detail, err := h.svc.Get(c.Context(), id.ClientID, orderID)
	if err != nil {
		return err
	}
	return httpx.OK(c, detail)
}

// AdminList handles GET /admin/orders (?search&status&client_id&page&per_page).
func (h *Handler) AdminList(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	clientID, err := httpx.QueryID(c, "client_id")
	if err != nil {
		return err
	}
	orders, total, err := h.svc.List(c.Context(), ports.ListParams{
		Page: page.Page, PerPage: page.PerPage,
		Status: c.Query("status"), Search: c.Query("search"),
		ClientID: clientID,
	})
	if err != nil {
		return err
	}
	return httpx.OK(c, orders, page.Meta(total))
}

// AdminGet handles GET /admin/orders/:id.
func (h *Handler) AdminGet(c fiber.Ctx) error {
	orderID, err := paramID(c)
	if err != nil {
		return err
	}
	detail, err := h.svc.Get(c.Context(), 0, orderID)
	if err != nil {
		return err
	}
	return httpx.OK(c, detail)
}

// Accept handles POST /admin/orders/:id/accept (manual activation).
func (h *Handler) Accept(c fiber.Ctx) error {
	return h.adminAction(c, h.svc.Accept)
}

// Cancel handles POST /admin/orders/:id/cancel.
func (h *Handler) Cancel(c fiber.Ctx) error {
	return h.adminAction(c, h.svc.Cancel)
}

// Fraud handles POST /admin/orders/:id/fraud.
func (h *Handler) Fraud(c fiber.Ctx) error {
	return h.adminAction(c, h.svc.MarkFraud)
}

func (h *Handler) adminAction(c fiber.Ctx, fn func(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error)) error {
	orderID, err := paramID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	order, err := fn(c.Context(), id.UserID, orderID)
	if err != nil {
		return err
	}
	return httpx.OK(c, order)
}

func paramID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.Validation("invalid id", apperr.FieldError{Field: "id", Message: "must be a positive integer"})
	}
	return id, nil
}
