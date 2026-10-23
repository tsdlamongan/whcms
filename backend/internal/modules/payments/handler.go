package payments

import (
	"context"
	"errors"
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

// PaymentsService is the use-case surface consumed by the handler
// (implemented by *Service).
type PaymentsService interface {
	GetPaymentMethods(ctx context.Context, clientID, invoiceID int64) ([]ports.PaymentMethod, error)
	PayInvoice(ctx context.Context, actorUserID, clientID, invoiceID int64, method string) (*PaymentInstruction, error)
	HandleCallback(ctx context.Context, p ports.CallbackPayload) error
	ResolveReturn(ctx context.Context, merchantOrderID string) string
	ResolveReturnInfo(ctx context.Context, merchantOrderID string, clientID int64) (*ReturnInfo, error)
	ListTransactions(ctx context.Context, p ports.ListParams) ([]domain.Transaction, int64, error)
	ListMyTransactions(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Transaction, int64, error)
	ConfirmManualTransaction(ctx context.Context, actorUserID, transactionID int64) error
}

var _ PaymentsService = (*Service)(nil)

// Handler exposes the payments HTTP endpoints.
type Handler struct {
	svc PaymentsService
	mw  Middlewares
	val *validate.Validator
}

// NewHandler builds the payments Handler.
func NewHandler(svc PaymentsService, mw Middlewares) *Handler {
	return &Handler{svc: svc, mw: mw, val: validate.New()}
}

// RegisterRoutes mounts the payments routes on r (the /api/v1 group). See
// the package doc for the full route table.
func (h *Handler) RegisterRoutes(r fiber.Router) {
	r.Get("/payments/methods", h.mw.RequireAuth(), h.Methods)
	r.Post("/invoices/:id/pay", h.mw.RequireAuth(), h.mw.RequireClient(), h.Pay)
	r.Get("/transactions", h.mw.RequireAuth(), h.mw.RequireClient(), h.ListMyTransactions)

	// Public gateway endpoint: the signed webhook has no user session at all,
	// so it's rate-limited (per source IP, same mechanism as every other
	// public endpoint below/in auth) to blunt scripted signature-guessing -
	// generous enough to never throttle Duitku's own legitimate retries.
	r.Post("/webhooks/duitku", h.mw.RateLimit("webhooks:duitku", 120, time.Minute), h.Webhook)
	// Requires auth: merchantOrderId is sequential/guessable, so callers must
	// be identified and are only shown invoices they own (staff/admin see
	// any, like Methods below) - see Return's doc comment.
	r.Get("/payments/return", h.mw.RequireAuth(), h.mw.RateLimit("payments:return", 60, time.Minute), h.Return)

	admin := r.Group("/admin", h.mw.RequireAuth(), h.mw.RequireRole("admin", "staff"))
	admin.Get("/transactions", h.mw.RequirePermission("payments"), h.AdminListTransactions)
	admin.Post("/transactions/:id/confirm", h.mw.RequirePermission("payments"), h.ConfirmManualTransaction)
}

// Methods returns payable gateway methods for ?invoice_id= (cached 10m per
// amount). Clients see only their own invoices; admin/staff any.
func (h *Handler) Methods(c fiber.Ctx) error {
	invoiceID, err := strconv.ParseInt(c.Query("invoice_id"), 10, 64)
	if err != nil || invoiceID < 1 {
		return apperr.Validation("invalid invoice_id",
			apperr.FieldError{Field: "invoice_id", Message: "must be a positive integer"})
	}
	id := httpx.MustIdentity(c)
	methods, err := h.svc.GetPaymentMethods(c.Context(), id.ClientID, invoiceID)
	if err != nil {
		return err
	}
	return httpx.OK(c, methods)
}

// Pay starts a payment for the invoice: gateway methods return payment
// instructions (URL/VA/QR); method "credit" settles immediately from credit.
func (h *Handler) Pay(c fiber.Ctx) error {
	invoiceID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || invoiceID < 1 {
		return apperr.Validation("invalid id")
	}
	var in PayInvoiceRequest
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	if err := h.val.Struct(in); err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	res, err := h.svc.PayInvoice(c.Context(), id.UserID, id.ClientID, invoiceID, in.Method)
	if err != nil {
		return err
	}
	return httpx.OK(c, res)
}

// maxWebhookBodyBytes caps the Duitku callback body well above any real
// payload (a form-encoded CallbackPayload is a few hundred bytes) but far
// below Fiber's app-wide default (4MiB) - this route has no session/CSRF
// protection to fall back on, so an oversized body is rejected before
// parsing rather than relying solely on the global limit.
const maxWebhookBodyBytes = 16 * 1024

// Webhook receives the Duitku callback (form-urlencoded, no auth). Oversized
// body -> 413; bad signature -> 400; unknown merchantOrderId -> 404; every
// processed (or idempotently re-delivered) callback -> 200 "OK".
func (h *Handler) Webhook(c fiber.Ctx) error {
	if len(c.Body()) > maxWebhookBodyBytes {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "request body too large")
	}
	var p ports.CallbackPayload
	if err := c.Bind().Form(&p); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid callback payload")
	}
	if p.MerchantOrderID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "missing merchantOrderId")
	}
	if err := h.svc.HandleCallback(c.Context(), p); err != nil {
		if errors.Is(err, ErrBadSignature) {
			return fiber.NewError(fiber.StatusBadRequest, "invalid signature")
		}
		return err
	}
	return c.SendString("OK")
}

// Return resolves a gateway return visit's merchantOrderId into
// {invoice_id, status} JSON (RECONCILE.md/CONTRACTS.md §9). The Duitku mock's
// (and real Duitku's) returnUrl points straight at the FRONTEND's own
// /payments/return page, which calls this endpoint itself to find out
// whether the callback has settled yet - so this is a JSON lookup API, not a
// browser-visited redirect.
//
// merchantOrderId is derived from the sequential invoice number
// (InvoiceNumber + "-" + attempt), so it is guessable; auth is required and
// the result is filtered to the caller's own invoices (clients see only
// their own, admin/staff any - same convention as Methods) so it cannot be
// used to enumerate other clients' invoice status.
func (h *Handler) Return(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	info, err := h.svc.ResolveReturnInfo(c.Context(), c.Query("merchantOrderId"), id.ClientID)
	if err != nil {
		return err
	}
	return httpx.OK(c, info)
}

// ListMyTransactions returns the caller's own transaction history.
func (h *Handler) ListMyTransactions(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	id := httpx.MustIdentity(c)
	rows, total, err := h.svc.ListMyTransactions(c.Context(), id.ClientID, ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
	})
	if err != nil {
		return err
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// AdminListTransactions lists transactions
// (?search&status&gateway&invoice_id&date_from&date_to&page&per_page).
func (h *Handler) AdminListTransactions(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	p := ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
		Search:  c.Query("search"),
		Status:  c.Query("status"),
		Gateway: c.Query("gateway"),
	}
	var err error
	if p.InvoiceID, err = httpx.QueryID(c, "invoice_id"); err != nil {
		return err
	}
	if p.From, err = httpx.QueryDate(c, "date_from"); err != nil {
		return err
	}
	if p.To, err = httpx.QueryDate(c, "date_to"); err != nil {
		return err
	}
	rows, total, err := h.svc.ListTransactions(c.Context(), p)
	if err != nil {
		return err
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// ConfirmManualTransaction settles a pending manual bank-transfer
// transaction once an admin has confirmed the funds arrived.
func (h *Handler) ConfirmManualTransaction(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return apperr.Validation("invalid id")
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.ConfirmManualTransaction(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.OK(c, map[string]any{"confirmed": true})
}
