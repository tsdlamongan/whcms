package tickets

import (
	"context"
	"mime/multipart"
	"strconv"
	"strings"

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

// TicketService is the use-case surface consumed by the handler (implemented
// by *Service).
type TicketService interface {
	CreateTicket(ctx context.Context, clientID int64, in CreateTicketInput, files []AttachmentUpload) (*TicketDetail, error)
	ListMine(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Ticket, int64, error)
	GetMine(ctx context.Context, clientID, ticketID int64) (*TicketDetail, error)
	ReplyMine(ctx context.Context, clientID, ticketID int64, in ReplyInput, files []AttachmentUpload) (*TicketDetail, error)
	CloseMine(ctx context.Context, clientID, ticketID int64) (*domain.Ticket, error)
	AdminList(ctx context.Context, f AdminListFilter) ([]domain.Ticket, int64, error)
	AdminGet(ctx context.Context, ticketID int64) (*TicketDetail, error)
	Assign(ctx context.Context, actorUserID, ticketID int64, in AssignInput) (*domain.Ticket, error)
	AdminReply(ctx context.Context, actorUserID, ticketID int64, in ReplyInput, files []AttachmentUpload) (*TicketDetail, error)
	SetStatus(ctx context.Context, actorUserID, ticketID int64, in SetStatusInput) (*domain.Ticket, error)
	SetPriority(ctx context.Context, actorUserID, ticketID int64, in SetPriorityInput) (*domain.Ticket, error)
	AttachmentURL(ctx context.Context, clientID, ticketID int64, idx int) (string, error)
	ListDepartments(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error)
	CreateDepartment(ctx context.Context, actorUserID int64, in DepartmentInput) (*domain.TicketDepartment, error)
	UpdateDepartment(ctx context.Context, actorUserID, id int64, in DepartmentInput) (*domain.TicketDepartment, error)
	DeleteDepartment(ctx context.Context, actorUserID, id int64) error
}

var _ TicketService = (*Service)(nil)

// Handler exposes the tickets HTTP endpoints.
type Handler struct {
	svc TicketService
	mw  Middlewares
}

// NewHandler builds the tickets Handler.
func NewHandler(svc TicketService, mw Middlewares) *Handler {
	return &Handler{svc: svc, mw: mw}
}

// RegisterRoutes mounts the tickets routes on r (the /api/v1 group). See the
// package doc for the full route table.
func (h *Handler) RegisterRoutes(r fiber.Router) {
	r.Get("/ticket-departments", h.PublicDepartments)

	client := r.Group("/tickets", h.mw.RequireAuth(), h.mw.RequireClient())
	client.Get("/", h.MyList)
	client.Post("/", h.MyCreate)
	client.Get("/:id", h.MyDetail)
	client.Post("/:id/replies", h.MyReply)
	client.Post("/:id/close", h.MyClose)
	client.Get("/:id/attachments/:idx", h.MyAttachment)

	admin := r.Group("/admin/tickets",
		h.mw.RequireAuth(), h.mw.RequireRole("admin", "staff"), h.mw.RequirePermission("support"))
	admin.Get("/", h.AdminList)
	admin.Get("/:id", h.AdminDetail)
	admin.Post("/:id/assign", h.AdminAssign)
	admin.Post("/:id/replies", h.AdminReply)
	admin.Post("/:id/status", h.AdminSetStatus)
	admin.Post("/:id/priority", h.AdminSetPriority)
	admin.Get("/:id/attachments/:idx", h.AdminAttachment)

	dept := r.Group("/admin/ticket-departments",
		h.mw.RequireAuth(), h.mw.RequireRole("admin"))
	dept.Get("/", h.AdminDepartments)
	dept.Post("/", h.AdminCreateDepartment)
	dept.Patch("/:id", h.AdminUpdateDepartment)
	dept.Delete("/:id", h.AdminDeleteDepartment)
}

// Helpers

func parseParamID(c fiber.Ctx) (int64, error) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.Validation("invalid id")
	}
	return id, nil
}

// formFiles extracts the uploaded "attachments" files from a multipart body.
// The returned close func must be called after the service consumed the
// readers. Non-multipart requests return no files.
func formFiles(c fiber.Ctx) ([]AttachmentUpload, func(), error) {
	noop := func() {}
	if !c.IsMultipart() {
		return nil, noop, nil
	}
	form, err := c.MultipartForm()
	if err != nil {
		return nil, noop, apperr.Validation("invalid multipart form")
	}
	headers := form.File["attachments"]
	files := make([]AttachmentUpload, 0, len(headers))
	opened := make([]multipart.File, 0, len(headers))
	closeAll := func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}
	for _, fh := range headers {
		f, err := fh.Open()
		if err != nil {
			closeAll()
			return nil, noop, apperr.Validation("cannot read attachment " + fh.Filename)
		}
		opened = append(opened, f)
		files = append(files, AttachmentUpload{
			Filename:    fh.Filename,
			Size:        fh.Size,
			ContentType: fh.Header.Get("Content-Type"),
			Reader:      f,
		})
	}
	return files, closeAll, nil
}

// bindTicketBody reads DTO fields from either a JSON body or multipart form
// values (multipart is used when attachments are present).
func bindCreateTicket(c fiber.Ctx) (CreateTicketInput, error) {
	var in CreateTicketInput
	if c.IsMultipart() {
		deptID, _ := strconv.ParseInt(c.FormValue("department_id"), 10, 64)
		in.DepartmentID = deptID
		in.Subject = c.FormValue("subject")
		in.Priority = c.FormValue("priority")
		in.Message = c.FormValue("message")
		return in, nil
	}
	if err := c.Bind().Body(&in); err != nil {
		return in, apperr.Validation("invalid request body")
	}
	return in, nil
}

func bindReply(c fiber.Ctx) (ReplyInput, error) {
	var in ReplyInput
	if c.IsMultipart() {
		in.Message = c.FormValue("message")
		in.IsInternal = isTruthy(c.FormValue("is_internal"))
		return in, nil
	}
	if err := c.Bind().Body(&in); err != nil {
		return in, apperr.Validation("invalid request body")
	}
	return in, nil
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Public

// PublicDepartments lists active departments for the ticket form.
func (h *Handler) PublicDepartments(c fiber.Ctx) error {
	depts, err := h.svc.ListDepartments(c.Context(), true)
	if err != nil {
		return err
	}
	if depts == nil {
		depts = []domain.TicketDepartment{}
	}
	return httpx.OK(c, depts)
}

// Client

// MyList lists the caller's tickets.
func (h *Handler) MyList(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	page := httpx.ParsePage(c)
	rows, total, err := h.svc.ListMine(c.Context(), id.ClientID, ports.ListParams{
		Page:    page.Page,
		PerPage: page.PerPage,
		Status:  c.Query("status"),
		Search:  c.Query("search"),
	})
	if err != nil {
		return err
	}
	if rows == nil {
		rows = []domain.Ticket{}
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// MyCreate opens a ticket (JSON or multipart with "attachments" files).
func (h *Handler) MyCreate(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	in, err := bindCreateTicket(c)
	if err != nil {
		return err
	}
	files, closeFiles, err := formFiles(c)
	if err != nil {
		return err
	}
	defer closeFiles()
	detail, err := h.svc.CreateTicket(c.Context(), id.ClientID, in, files)
	if err != nil {
		return err
	}
	return httpx.Created(c, detail)
}

// MyDetail returns the caller's ticket thread (internal notes hidden).
func (h *Handler) MyDetail(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	ticketID, err := parseParamID(c)
	if err != nil {
		return err
	}
	detail, err := h.svc.GetMine(c.Context(), id.ClientID, ticketID)
	if err != nil {
		return err
	}
	return httpx.OK(c, detail)
}

// MyReply adds a client reply (JSON or multipart).
func (h *Handler) MyReply(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	ticketID, err := parseParamID(c)
	if err != nil {
		return err
	}
	in, err := bindReply(c)
	if err != nil {
		return err
	}
	files, closeFiles, err := formFiles(c)
	if err != nil {
		return err
	}
	defer closeFiles()
	detail, err := h.svc.ReplyMine(c.Context(), id.ClientID, ticketID, in, files)
	if err != nil {
		return err
	}
	return httpx.Created(c, detail)
}

// MyClose closes the caller's ticket.
func (h *Handler) MyClose(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	ticketID, err := parseParamID(c)
	if err != nil {
		return err
	}
	ticket, err := h.svc.CloseMine(c.Context(), id.ClientID, ticketID)
	if err != nil {
		return err
	}
	return httpx.OK(c, ticket)
}

// MyAttachment redirects to a presigned download URL (ownership checked,
// internal-note attachments unreachable).
func (h *Handler) MyAttachment(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	return h.attachment(c, id.ClientID)
}

// Staff/admin

// AdminList lists tickets with filters
// status/priority/department_id/assigned_user_id/client_id/search.
func (h *Handler) AdminList(c fiber.Ctx) error {
	page := httpx.ParsePage(c)
	deptID, _ := strconv.ParseInt(c.Query("department_id"), 10, 64)
	assignedID, _ := strconv.ParseInt(c.Query("assigned_user_id"), 10, 64)
	clientID, err := httpx.QueryID(c, "client_id")
	if err != nil {
		return err
	}
	rows, total, err := h.svc.AdminList(c.Context(), AdminListFilter{
		Status:         c.Query("status"),
		Priority:       c.Query("priority"),
		DepartmentID:   deptID,
		AssignedUserID: assignedID,
		ClientID:       clientID,
		Search:         c.Query("search"),
		Page:           page.Page,
		PerPage:        page.PerPage,
	})
	if err != nil {
		return err
	}
	if rows == nil {
		rows = []domain.Ticket{}
	}
	return httpx.OK(c, rows, page.Meta(total))
}

// AdminDetail returns the full thread including internal notes.
func (h *Handler) AdminDetail(c fiber.Ctx) error {
	ticketID, err := parseParamID(c)
	if err != nil {
		return err
	}
	detail, err := h.svc.AdminGet(c.Context(), ticketID)
	if err != nil {
		return err
	}
	return httpx.OK(c, detail)
}

// AdminAssign assigns the ticket to a staff user (0 unassigns).
func (h *Handler) AdminAssign(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	ticketID, err := parseParamID(c)
	if err != nil {
		return err
	}
	var in AssignInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	ticket, err := h.svc.Assign(c.Context(), id.UserID, ticketID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, ticket)
}

// AdminReply adds a staff reply or internal note (JSON or multipart).
func (h *Handler) AdminReply(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	ticketID, err := parseParamID(c)
	if err != nil {
		return err
	}
	in, err := bindReply(c)
	if err != nil {
		return err
	}
	files, closeFiles, err := formFiles(c)
	if err != nil {
		return err
	}
	defer closeFiles()
	detail, err := h.svc.AdminReply(c.Context(), id.UserID, ticketID, in, files)
	if err != nil {
		return err
	}
	return httpx.Created(c, detail)
}

// AdminSetStatus sets the ticket status.
func (h *Handler) AdminSetStatus(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	ticketID, err := parseParamID(c)
	if err != nil {
		return err
	}
	var in SetStatusInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	ticket, err := h.svc.SetStatus(c.Context(), id.UserID, ticketID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, ticket)
}

// AdminSetPriority sets the ticket priority.
func (h *Handler) AdminSetPriority(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	ticketID, err := parseParamID(c)
	if err != nil {
		return err
	}
	var in SetPriorityInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	ticket, err := h.svc.SetPriority(c.Context(), id.UserID, ticketID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, ticket)
}

// AdminAttachment redirects staff to a presigned download URL (all
// attachments, including internal notes).
func (h *Handler) AdminAttachment(c fiber.Ctx) error {
	return h.attachment(c, 0)
}

func (h *Handler) attachment(c fiber.Ctx, clientID int64) error {
	ticketID, err := parseParamID(c)
	if err != nil {
		return err
	}
	idx, err := strconv.Atoi(c.Params("idx"))
	if err != nil || idx < 0 {
		return apperr.Validation("invalid idx")
	}
	url, err := h.svc.AttachmentURL(c.Context(), clientID, ticketID, idx)
	if err != nil {
		return err
	}
	return c.Redirect().Status(fiber.StatusFound).To(url)
}

// Departments (admin)

// AdminDepartments lists all departments including inactive ones.
func (h *Handler) AdminDepartments(c fiber.Ctx) error {
	depts, err := h.svc.ListDepartments(c.Context(), false)
	if err != nil {
		return err
	}
	if depts == nil {
		depts = []domain.TicketDepartment{}
	}
	return httpx.OK(c, depts)
}

// AdminCreateDepartment creates a department.
func (h *Handler) AdminCreateDepartment(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	var in DepartmentInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	dept, err := h.svc.CreateDepartment(c.Context(), id.UserID, in)
	if err != nil {
		return err
	}
	return httpx.Created(c, dept)
}

// AdminUpdateDepartment updates a department.
func (h *Handler) AdminUpdateDepartment(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	deptID, err := parseParamID(c)
	if err != nil {
		return err
	}
	var in DepartmentInput
	if err := c.Bind().Body(&in); err != nil {
		return apperr.Validation("invalid request body")
	}
	dept, err := h.svc.UpdateDepartment(c.Context(), id.UserID, deptID, in)
	if err != nil {
		return err
	}
	return httpx.OK(c, dept)
}

// AdminDeleteDepartment deletes a department (CONFLICT when it has tickets).
func (h *Handler) AdminDeleteDepartment(c fiber.Ctx) error {
	id := httpx.MustIdentity(c)
	deptID, err := parseParamID(c)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteDepartment(c.Context(), id.UserID, deptID); err != nil {
		return err
	}
	return httpx.NoContent(c)
}
