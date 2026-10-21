package clients

import (
	"bufio"
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

// ClientService is the use-case surface consumed by the handler (implemented
// by *Service).
type ClientService interface {
	SearchClients(ctx context.Context, f SearchInput) ([]ClientListRow, int64, error)
	CreateClient(ctx context.Context, actorUserID int64, in CreateClientInput) (*ClientWithEmail, error)
	GetClientDetail(ctx context.Context, id int64) (*ClientDetail, error)
	UpdateClient(ctx context.Context, actorUserID, id int64, in UpdateClientInput) (*domain.Client, error)
	SetStatus(ctx context.Context, actorUserID, id int64, in SetStatusInput) (*domain.Client, error)
	DeleteClient(ctx context.Context, actorUserID, id int64) error
	GetProfile(ctx context.Context, clientID int64) (*domain.Client, error)
	UpdateProfile(ctx context.Context, clientID int64, in UpdateProfileInput) (*domain.Client, error)
	ListContacts(ctx context.Context, clientID int64) ([]Contact, error)
	CreateContact(ctx context.Context, actorUserID, clientID int64, in ContactInput) (*Contact, error)
	UpdateContact(ctx context.Context, actorUserID, clientID, contactID int64, in ContactInput) (*Contact, error)
	DeleteContact(ctx context.Context, actorUserID, clientID, contactID int64) error
	AdminAdjustCredit(ctx context.Context, actorUserID, clientID int64, in AdjustCreditInput) (int64, error)
	Credit(ctx context.Context, clientID int64, p ports.ListParams) (*CreditOverview, int64, error)
	Deposit(ctx context.Context, clientID int64, in DepositInput) (*domain.Invoice, error)
	ExportCSV(ctx context.Context, w io.Writer) error
}

var _ ClientService = (*Service)(nil)

// Handler exposes the clients HTTP endpoints.
type Handler struct {
	svc ClientService
	mw  Middlewares
}

// NewHandler builds the clients Handler.
func NewHandler(svc ClientService, mw Middlewares) *Handler {
	return &Handler{svc: svc, mw: mw}
}

// RegisterRoutes mounts the clients routes on r (the /api/v1 group). See the
// package doc for the full route table.
func (h *Handler) RegisterRoutes(r fiber.Router) {
	account := r.Group("/account", h.mw.RequireAuth(), h.mw.RequireClient())
	account.Get("/profile", h.GetProfile)
	account.Patch("/profile", h.UpdateProfile)
	account.Get("/credit", h.Credit)
	account.Post("/credit/deposit", h.Deposit)
	account.Get("/contacts", h.ListMyContacts)
	account.Post("/contacts", h.CreateMyContact)
	account.Patch("/contacts/:contactId", h.UpdateMyContact)
	account.Delete("/contacts/:contactId", h.DeleteMyContact)

	admin := r.Group("/admin/clients",
		h.mw.RequireAuth(), h.mw.RequireRole("admin", "staff"), h.mw.RequirePermission("clients"))
	admin.Get("/", h.AdminList)
	admin.Post("/", h.AdminCreate)
	admin.Get("/export", h.AdminExport) // must register before /:id
	admin.Get("/:id", h.AdminDetail)
	admin.Patch("/:id", h.AdminUpdate)
	admin.Delete("/:id", h.AdminDelete)
	admin.Post("/:id/status", h.AdminSetStatus)
	admin.Get("/:id/credit", h.AdminCredit)
	admin.Post("/:id/credit", h.AdminAdjustCredit)
	admin.Get("/:id/contacts", h.AdminListContacts)
	admin.Post("/:id/contacts", h.AdminCreateContact)
	admin.Patch("/:id/contacts/:contactId", h.AdminUpdateContact)
	admin.Delete("/:id/contacts/:contactId", h.AdminDeleteContact)
}

// Helpers

func parseParamID(c fiber.Ctx, name string) (int64, error) {
	id, err := strconv.ParseInt(c.Params(name), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.Validation("invalid " + name)
	}
	return id, nil
}

// Client self-service

// redactAdminNotes strips the admin-only internal note before a client-facing
// response - notes_admin is intentionally not visible to the client it's
// written about.
func redactAdminNotes(client *domain.Client) domain.Client {
	view := *client
	view.NotesAdmin = ""
	return view
}

// GetProfile returns the caller's client profile.
func (h *Handler) GetProfile(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	client, err := h.svc.GetProfile(c.Context(), id.ClientID)
	if err != nil {
		return err
	}
	return httpx.OK(c, redactAdminNotes(client))
}

// UpdateProfile patches the caller's address/contact fields.
func (h *Handler) UpdateProfile(c fiber.Ctx) error {
	var in UpdateProfileInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	client, err := h.svc.UpdateProfile(c.Context(), id.ClientID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, redactAdminNotes(client))
}

// Credit returns the caller's balance plus one ledger page.
func (h *Handler) Credit(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	page := httpx.ParsePage(c)
	overview, total, err := h.svc.Credit(c.Context(), id.ClientID,
		ports.ListParams{Page: page.Page, PerPage: page.PerPage})
	if err != nil {
		return err
	}
	return httpx.OK(c, overview, page.Meta(total))
}

// Deposit creates a credit deposit invoice for the caller.
func (h *Handler) Deposit(c fiber.Ctx) error {
	var in DepositInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	inv, err := h.svc.Deposit(c.Context(), id.ClientID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, inv)
}

// ListMyContacts lists the caller's contacts.
func (h *Handler) ListMyContacts(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	contacts, err := h.svc.ListContacts(c.Context(), id.ClientID)
	if err != nil {
		return err
	}
	return httpx.OK(c, contacts)
}

// CreateMyContact adds a contact to the caller's account.
func (h *Handler) CreateMyContact(c fiber.Ctx) error {
	var in ContactInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	ct, err := h.svc.CreateContact(c.Context(), id.UserID, id.ClientID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, ct)
}

// UpdateMyContact updates one of the caller's contacts.
func (h *Handler) UpdateMyContact(c fiber.Ctx) error {
	contactID, err := parseParamID(c, "contactId")
	if err != nil {
		return err
	}
	var in ContactInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	id := httpx.MustIdentity(c)
	ct, err := h.svc.UpdateContact(c.Context(), id.UserID, id.ClientID, contactID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, ct)
}

// DeleteMyContact removes one of the caller's contacts.
func (h *Handler) DeleteMyContact(c fiber.Ctx) error {
	contactID, err := parseParamID(c, "contactId")
	if err != nil {
		return err
	}
	id := httpx.MustIdentity(c)
	if err := h.svc.DeleteContact(c.Context(), id.UserID, id.ClientID, contactID); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// Admin

// AdminList searches clients (?search&status&has_product&ids&page&per_page).
func (h *Handler) AdminList(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	f := SearchInput{
		Search:  c.Query("search"),
		Status:  c.Query("status"),
		Page:    page.Page,
		PerPage: page.PerPage,
	}
	ids, err := httpx.QueryIDs(c, "ids")
	if err != nil {
		return err
	}
	f.IDs = ids
	if raw := c.Query("has_product"); raw != "" {
		ok, err := strconv.ParseBool(raw)
		if err != nil {
			return apperr.Validation("invalid has_product flag")
		}
		f.HasProduct = ok
	}
	rows, total, err := h.svc.SearchClients(c.Context(), f)
	if err != nil {
		return err
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// AdminCreate creates a user+client pair.
func (h *Handler) AdminCreate(c fiber.Ctx) error {
	var in CreateClientInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	client, err := h.svc.CreateClient(c.Context(), actor.UserID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, client)
}

// AdminExport streams the clients CSV export.
func (h *Handler) AdminExport(c fiber.Ctx) error {
	ctx := c.Context()
	c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="clients.csv"`)
	return c.SendStreamWriter(func(w *bufio.Writer) {
		// Errors mid-stream can only truncate the download; the service has
		// already validated access and the query plan by this point.
		_ = h.svc.ExportCSV(ctx, w)
	})
}

// AdminDetail returns the client detail aggregate.
func (h *Handler) AdminDetail(c fiber.Ctx) error {
	id, err := parseParamID(c, "id")
	if err != nil {
		return err
	}
	detail, err := h.svc.GetClientDetail(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, detail)
}

// AdminUpdate patches profile fields and admin notes.
func (h *Handler) AdminUpdate(c fiber.Ctx) error {
	id, err := parseParamID(c, "id")
	if err != nil {
		return err
	}
	var in UpdateClientInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	client, err := h.svc.UpdateClient(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, client)
}

// AdminDelete soft-deletes a client.
func (h *Handler) AdminDelete(c fiber.Ctx) error {
	id, err := parseParamID(c, "id")
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeleteClient(c.Context(), actor.UserID, id); err != nil {
		return err
	}
	return httpx.NoContent(c)
}

// AdminSetStatus changes a client's status.
func (h *Handler) AdminSetStatus(c fiber.Ctx) error {
	id, err := parseParamID(c, "id")
	if err != nil {
		return err
	}
	var in SetStatusInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	client, err := h.svc.SetStatus(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, client)
}

// AdminCredit returns a client's balance plus one ledger page.
func (h *Handler) AdminCredit(c fiber.Ctx) error {
	id, err := parseParamID(c, "id")
	if err != nil {
		return err
	}
	page := httpx.ParsePage(c)
	overview, total, err := h.svc.Credit(c.Context(), id,
		ports.ListParams{Page: page.Page, PerPage: page.PerPage})
	if err != nil {
		return err
	}
	return httpx.OK(c, overview, page.Meta(total))
}

// AdminAdjustCredit manually adds/deducts client credit.
func (h *Handler) AdminAdjustCredit(c fiber.Ctx) error {
	id, err := parseParamID(c, "id")
	if err != nil {
		return err
	}
	var in AdjustCreditInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	balance, err := h.svc.AdminAdjustCredit(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, map[string]int64{"balance": balance})
}

// AdminListContacts lists a client's contacts.
func (h *Handler) AdminListContacts(c fiber.Ctx) error {
	id, err := parseParamID(c, "id")
	if err != nil {
		return err
	}
	contacts, err := h.svc.ListContacts(c.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(c, contacts)
}

// AdminCreateContact adds a contact to a client.
func (h *Handler) AdminCreateContact(c fiber.Ctx) error {
	id, err := parseParamID(c, "id")
	if err != nil {
		return err
	}
	var in ContactInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	ct, err := h.svc.CreateContact(c.Context(), actor.UserID, id, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, ct)
}

// AdminUpdateContact updates a client's contact.
func (h *Handler) AdminUpdateContact(c fiber.Ctx) error {
	id, err := parseParamID(c, "id")
	if err != nil {
		return err
	}
	contactID, err := parseParamID(c, "contactId")
	if err != nil {
		return err
	}
	var in ContactInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	actor := httpx.MustIdentity(c)
	ct, err := h.svc.UpdateContact(c.Context(), actor.UserID, id, contactID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, ct)
}

// AdminDeleteContact deletes a client's contact.
func (h *Handler) AdminDeleteContact(c fiber.Ctx) error {
	id, err := parseParamID(c, "id")
	if err != nil {
		return err
	}
	contactID, err := parseParamID(c, "contactId")
	if err != nil {
		return err
	}
	actor := httpx.MustIdentity(c)
	if err := h.svc.DeleteContact(c.Context(), actor.UserID, id, contactID); err != nil {
		return err
	}
	return httpx.NoContent(c)
}
