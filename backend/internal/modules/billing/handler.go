package billing

import (
	"context"
	"io"
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

// BillingService is the use-case surface consumed by the handler
// (implemented by *Service).
type BillingService interface {
	ListClientInvoices(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error)
	GetInvoice(ctx context.Context, clientID, invoiceID int64) (*InvoiceDetail, error)
	DownloadPDF(ctx context.Context, clientID, invoiceID int64) (io.ReadCloser, string, error)
	AdminListInvoices(ctx context.Context, p ports.ListParams) ([]domain.Invoice, int64, error)
	CreateManualInvoice(ctx context.Context, actorUserID int64, in ManualInvoiceInput) (*domain.Invoice, error)
	GenerateSelectedRenewalInvoices(ctx context.Context, actorUserID int64, in GenerateSelectedInvoicesInput) (*GenerateSelectedInvoicesResult, error)
	AddManualPayment(ctx context.Context, actorUserID, invoiceID int64, in ManualPaymentInput) (*domain.Invoice, error)
	CancelInvoice(ctx context.Context, actorUserID, invoiceID int64) (*domain.Invoice, error)
	RefundInvoice(ctx context.Context, actorUserID, invoiceID int64, in RefundInput) (*domain.Invoice, error)
	UpdateInvoice(ctx context.Context, actorUserID, invoiceID int64, in UpdateInvoiceInput) (*domain.Invoice, error)
}

var _ BillingService = (*Service)(nil)

// Handler exposes the billing HTTP endpoints.
type Handler struct {
	svc BillingService
	mw  Middlewares
}

// NewHandler builds the billing Handler.
func NewHandler(svc BillingService, mw Middlewares) *Handler {
	return &Handler{svc: svc, mw: mw}
}

// RegisterRoutes mounts the billing routes on r (the /api/v1 group). See the
// package doc for the full route table.
func (h *Handler) RegisterRoutes(r fiber.Router) {
	client := r.Group("/invoices", h.mw.RequireAuth(), h.mw.RequireClient())
	client.Get("/", h.ListInvoices)
	client.Get("/:id", h.GetInvoice)
	client.Get("/:id/pdf", h.InvoicePDF)

	admin := r.Group("/admin/invoices",
		h.mw.RequireAuth(), h.mw.RequireRole("admin", "staff"), h.mw.RequirePermission("billing"))
	admin.Get("/", h.AdminListInvoices)
	admin.Post("/", h.AdminCreateInvoice)
	admin.Post("/generate-selected", h.AdminGenerateSelectedInvoices)
	admin.Get("/:id", h.AdminGetInvoice)
	admin.Get("/:id/pdf", h.AdminInvoicePDF)
	admin.Patch("/:id", h.AdminUpdateInvoice)
	admin.Post("/:id/payment", h.AdminAddPayment)
	admin.Post("/:id/cancel", h.AdminCancelInvoice)
	admin.Post("/:id/refund", h.AdminRefundInvoice)
}

func parseID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.Validation("invalid id")
	}
	return id, nil
}

func listParams(c fiber.Ctx, page httpx.Page) ports.ListParams {
	return ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
		Search:  c.Query("search"),
		Status:  c.Query("status"),
	}
}

// Client endpoints

// ListInvoices lists the authenticated client's invoices
// (?search&status&page&per_page).
func (h *Handler) ListInvoices(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	page := httpx.ParsePage(c)
	list, total, err := h.svc.ListClientInvoices(c.Context(), id.ClientID, listParams(c, page))
	if err != nil {
		return err
	}
	return httpx.OK(c, list, page.Meta(total))
}

// GetInvoice returns one of the client's invoices with items.
func (h *Handler) GetInvoice(c fiber.Ctx) error {
	invoiceID, err := parseID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	detail, err := h.svc.GetInvoice(c.Context(), id.ClientID, invoiceID)
	if err != nil {
		return err
	}
	return httpx.OK(c, detail)
}

// InvoicePDF streams the invoice PDF.
func (h *Handler) InvoicePDF(c fiber.Ctx) error {
	invoiceID, err := parseID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	rc, filename, err := h.svc.DownloadPDF(c.Context(), id.ClientID, invoiceID)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, "application/pdf")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+filename+`"`)
	return c.SendStream(rc)
}

// Admin endpoints

// AdminListInvoices lists all invoices
// (?search&status&client_id&ids&date_from&date_to&page&per_page).
func (h *Handler) AdminListInvoices(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	p := listParams(c, page)
	var err error
	if p.ClientID, err = httpx.QueryID(c, "client_id"); err != nil {
		return err
	}
	if p.From, err = httpx.QueryDate(c, "date_from"); err != nil {
		return err
	}
	if p.To, err = httpx.QueryDate(c, "date_to"); err != nil {
		return err
	}
	if p.IDs, err = httpx.QueryIDs(c, "ids"); err != nil {
		return err
	}
	list, total, err := h.svc.AdminListInvoices(c.Context(), p)
	if err != nil {
		return err
	}
	return httpx.OK(c, list, page.Meta(total))
}

// AdminCreateInvoice creates a manual invoice.
func (h *Handler) AdminCreateInvoice(c fiber.Ctx) error {
	var in ManualInvoiceInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	inv, err := h.svc.CreateManualInvoice(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, inv)
}

// AdminGenerateSelectedInvoices force-generates renewal invoices for the
// given service_ids/domain_ids ("Invoice Selected Items"), skipping any that
// already have an open renewal invoice or aren't eligible.
func (h *Handler) AdminGenerateSelectedInvoices(c fiber.Ctx) error {
	var in GenerateSelectedInvoicesInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	res, err := h.svc.GenerateSelectedRenewalInvoices(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, res)
}

// AdminGetInvoice returns any invoice with items.
func (h *Handler) AdminGetInvoice(c fiber.Ctx) error {
	invoiceID, err := parseID(c)
	if err != nil {
		return err
	}
	detail, err := h.svc.GetInvoice(c.Context(), 0, invoiceID)
	if err != nil {
		return err
	}
	return httpx.OK(c, detail)
}

// AdminInvoicePDF streams any invoice's PDF (bypasses client ownership).
func (h *Handler) AdminInvoicePDF(c fiber.Ctx) error {
	invoiceID, err := parseID(c)
	if err != nil {
		return err
	}
	rc, filename, err := h.svc.DownloadPDF(c.Context(), 0, invoiceID)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, "application/pdf")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+filename+`"`)
	return c.SendStream(rc)
}

// AdminUpdateInvoice edits due date / notes of an open invoice.
func (h *Handler) AdminUpdateInvoice(c fiber.Ctx) error {
	invoiceID, err := parseID(c)
	if err != nil {
		return err
	}
	var in UpdateInvoiceInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	inv, err := h.svc.UpdateInvoice(c.Context(), id.UserID, invoiceID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, inv)
}

// AdminAddPayment records a manual payment ({amount, method}).
func (h *Handler) AdminAddPayment(c fiber.Ctx) error {
	invoiceID, err := parseID(c)
	if err != nil {
		return err
	}
	var in ManualPaymentInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	inv, err := h.svc.AddManualPayment(c.Context(), id.UserID, invoiceID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, inv)
}

// AdminCancelInvoice cancels an unpaid/overdue invoice.
func (h *Handler) AdminCancelInvoice(c fiber.Ctx) error {
	invoiceID, err := parseID(c)
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	inv, err := h.svc.CancelInvoice(c.Context(), id.UserID, invoiceID)
	if err != nil {
		return err
	}
	return httpx.OK(c, inv)
}

// AdminRefundInvoice marks a paid invoice refunded ({to_credit}).
func (h *Handler) AdminRefundInvoice(c fiber.Ctx) error {
	invoiceID, err := parseID(c)
	if err != nil {
		return err
	}
	var in RefundInput
	if len(c.Body()) > 0 {
		if err := c.Bind().Body(&in); err != nil {
			return apperr.Validation("invalid request body")
		}
	}
	id := httpx.MustIdentity(c)
	inv, err := h.svc.RefundInvoice(c.Context(), id.UserID, invoiceID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, inv)
}
