// Package tickets implements the M-TICKETS module (FR-TIC-001..006):
// support departments (admin CRUD + public list), client ticket
// creation/reply/close with multipart attachments stored in S3
// (tickets/<ticket_number>/<uuid>-<filename>, extension/size validated from
// settings), staff/admin ticket management (filters, assignment, replies with
// internal notes, status changes) and attachment downloads via presigned URL
// redirects with ownership checks.
//
// Route table (mounted on the /api/v1 group by the composition root):
//
//	GET    /ticket-departments                     active departments          [public]
//	GET    /tickets                                my tickets                  [client]
//	POST   /tickets                                open ticket (multipart)     [client]
//	GET    /tickets/:id                            detail, no internal notes   [client]
//	POST   /tickets/:id/replies                    reply (multipart)           [client]
//	POST   /tickets/:id/close                      close ticket                [client]
//	GET    /tickets/:id/attachments/:idx           presigned redirect          [client]
//	GET    /admin/tickets                          list w/ filters             [perm: support]
//	GET    /admin/tickets/:id                      detail incl internal notes  [perm: support]
//	POST   /admin/tickets/:id/assign               assign staff                [perm: support]
//	POST   /admin/tickets/:id/replies              reply / internal note       [perm: support]
//	POST   /admin/tickets/:id/status               set status                  [perm: support]
//	GET    /admin/tickets/:id/attachments/:idx     presigned redirect          [perm: support]
//	GET    /admin/ticket-departments               all departments             [admin]
//	POST   /admin/ticket-departments               create department           [admin]
//	PATCH  /admin/ticket-departments/:id           update department           [admin]
//	DELETE /admin/ticket-departments/:id           delete department           [admin]
//
// Status rules (MODULES §3): client reply -> customer_reply; staff public
// reply -> answered; close -> closed (either side); closed tickets reject
// further replies (staff internal notes excepted) until reopened via the
// admin status endpoint.
package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/validate"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/google/uuid"
)

// presignTTL is how long attachment download URLs stay valid.
const presignTTL = 15 * time.Minute

// maxAttachments is the maximum number of files per message.
const maxAttachments = 5

// Default attachment policy when the settings keys are absent
// (tickets.allowed_extensions / tickets.max_attachment_mb).
var defaultAllowedExtensions = []string{"jpg", "jpeg", "png", "gif", "pdf", "txt", "zip"}

const defaultMaxAttachmentMB = 8

// safeExtensionContentTypes maps a lowercased file extension (no leading dot)
// to the ONE content type WHCMS will ever store/serve for it. The
// client-supplied multipart Content-Type header is never trusted (it is
// attacker-controlled and can be spoofed independently of the file's real
// bytes/extension, e.g. a ".txt" upload sent with "Content-Type: text/html"),
// so both upload (storeAttachments) and download (AttachmentURL) derive the
// content type from this allowlist instead. Anything not listed here falls
// back to application/octet-stream, which browsers never render inline.
var safeExtensionContentTypes = map[string]string{
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"png":  "image/png",
	"gif":  "image/gif",
	"pdf":  "application/pdf",
	"txt":  "text/plain; charset=utf-8",
	"zip":  "application/zip",
}

// safeContentType derives a content type for filename from
// safeExtensionContentTypes, ignoring any client-supplied value. Unknown
// extensions fall back to application/octet-stream so browsers download
// rather than render them.
func safeContentType(filename string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	if ct, ok := safeExtensionContentTypes[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}

// contentDispositionAttachment builds a Content-Disposition header value that
// forces the browser to download filename rather than render it inline.
// filename is expected to already be sanitizeFilename-cleaned (alnum, '.',
// '-', '_' only) so no header-injection escaping is needed here.
func contentDispositionAttachment(filename string) string {
	return fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(filename))
}

// AdminSearchStore is the extra repository surface beyond ports.TicketRepo
// needed for the filtered staff list (implemented by *Repo).
type AdminSearchStore interface {
	ListAdmin(ctx context.Context, f AdminListFilter) ([]domain.Ticket, int64, error)
}

// Deps are the service dependencies (small interfaces, wired by the
// composition root).
type Deps struct {
	Tx       ports.TxManager
	Tickets  ports.TicketRepo
	Search   AdminSearchStore // implemented by *Repo
	Clients  ports.ClientRepo // impl provided by the clients module
	Users    ports.UserRepo   // impl provided by the auth module
	Storage  ports.Storage
	Settings ports.SettingsRepo
	Notifier ports.NotificationSender // impl provided by the notifications module
	Audit    ports.AuditLogger
	Clock    ports.Clock
	// FrontendURL builds the TicketURL template variable (cfg.FrontendURL).
	FrontendURL string
}

// Service implements the tickets use-cases.
type Service struct {
	d   Deps
	val *validate.Validator
}

// New builds the tickets Service.
func New(d Deps) *Service {
	return &Service{d: d, val: validate.New()}
}

// Service also implements the cross-service GuestTicketCreator port consumed by
// the public contact module.
var _ ports.GuestTicketCreator = (*Service)(nil)

// Client use-cases

// CreateTicket opens a ticket for the client with an initial message and
// optional attachments. Returns the full detail (client view).
func (s *Service) CreateTicket(ctx context.Context, clientID int64, in CreateTicketInput, files []AttachmentUpload) (*TicketDetail, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	dept, err := s.d.Tickets.GetDepartmentByID(ctx, in.DepartmentID)
	if err != nil {
		return nil, err
	}
	if dept == nil || !dept.Active {
		return nil, apperr.Validation("invalid department",
			apperr.FieldError{Field: "department_id", Message: "department not found or inactive"})
	}
	if err := s.validateAttachments(ctx, files); err != nil {
		return nil, err
	}
	client, err := s.d.Clients.GetByID(ctx, clientID)
	if err != nil {
		return nil, err
	}

	priority := domain.TicketPriority(in.Priority)
	if priority == "" {
		priority = domain.PriorityMedium
	}
	now := s.d.Clock.Now()

	var ticket *domain.Ticket
	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		n, err := s.d.Tickets.NextNumber(txCtx, domain.TicketCounterScope)
		if err != nil {
			return err
		}
		cid := clientID
		t := &domain.Ticket{
			TicketNumber: domain.FormatTicketNumber(n),
			ClientID:     &cid,
			DepartmentID: dept.ID,
			Subject:      in.Subject,
			Status:       domain.TicketOpen,
			Priority:     priority,
			LastReplyAt:  &now,
		}
		if err := s.d.Tickets.Create(txCtx, t); err != nil {
			return err
		}
		attachments, err := s.storeAttachments(txCtx, t.TicketNumber, files)
		if err != nil {
			return err
		}
		userID := client.UserID
		reply := &domain.TicketReply{
			TicketID:    t.ID,
			UserID:      &userID,
			AuthorName:  client.FullName(),
			Message:     in.Message,
			IsInternal:  false,
			Attachments: attachments,
		}
		if err := s.d.Tickets.AddReply(txCtx, reply); err != nil {
			return err
		}
		ticket = t
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.d.Audit.Log(ctx, client.UserID, "ticket.create", "ticket", ticket.ID, nil,
		map[string]any{"ticket_number": ticket.TicketNumber, "subject": ticket.Subject, "department_id": dept.ID})

	// Notifications are best-effort: a mail failure never fails the request.
	data := s.templateData(ticket, s.clientTicketURL(ticket.ID))
	data["Name"] = client.FullName()
	_ = s.d.Notifier.SendTemplate(ctx, client.UserID, "ticket_opened", data)
	_ = s.d.Notifier.AlertAdmin(ctx,
		fmt.Sprintf("New ticket %s [%s]", ticket.TicketNumber, dept.Name),
		fmt.Sprintf("Ticket %s (%s) opened by %s in department %s (%s).",
			ticket.TicketNumber, ticket.Subject, client.FullName(), dept.Name, dept.Email))

	return s.detail(ctx, ticket, false)
}

// CreateGuestTicket opens a ticket on behalf of an unauthenticated visitor
// (the public contact form). Unlike CreateTicket there is no client/user
// account: the ticket stores the guest's name/email and the opening reply is
// attributed to the guest with a nil user id. Attachments are not supported on
// the guest path. Returns the created ticket.
func (s *Service) CreateGuestTicket(ctx context.Context, in ports.GuestTicketInput) (*domain.Ticket, error) {
	var dept *domain.TicketDepartment
	if in.DepartmentID == 0 {
		// The visitor didn't pick a department: route to the first active one.
		active, err := s.d.Tickets.ListDepartments(ctx, true)
		if err != nil {
			return nil, err
		}
		if len(active) == 0 {
			return nil, apperr.Validation("no department available",
				apperr.FieldError{Field: "department_id", Message: "no active department is available"})
		}
		dept = &active[0]
	} else {
		d, err := s.d.Tickets.GetDepartmentByID(ctx, in.DepartmentID)
		if err != nil {
			return nil, err
		}
		if d == nil || !d.Active {
			return nil, apperr.Validation("invalid department",
				apperr.FieldError{Field: "department_id", Message: "department not found or inactive"})
		}
		dept = d
	}

	now := s.d.Clock.Now()
	var ticket *domain.Ticket
	err := s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		n, err := s.d.Tickets.NextNumber(txCtx, domain.TicketCounterScope)
		if err != nil {
			return err
		}
		t := &domain.Ticket{
			TicketNumber: domain.FormatTicketNumber(n),
			ClientID:     nil,
			DepartmentID: dept.ID,
			Subject:      in.Subject,
			Status:       domain.TicketOpen,
			Priority:     domain.PriorityMedium,
			GuestName:    in.GuestName,
			GuestEmail:   in.GuestEmail,
			LastReplyAt:  &now,
		}
		if err := s.d.Tickets.Create(txCtx, t); err != nil {
			return err
		}
		if err := s.d.Tickets.AddReply(txCtx, &domain.TicketReply{
			TicketID:   t.ID,
			UserID:     nil,
			AuthorName: in.GuestName,
			Message:    in.Message,
			IsInternal: false,
		}); err != nil {
			return err
		}
		ticket = t
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.d.Audit.Log(ctx, 0, "ticket.create", "ticket", ticket.ID, nil,
		map[string]any{"ticket_number": ticket.TicketNumber, "guest_email": in.GuestEmail})

	// Best-effort admin alert: a mail failure never fails the request.
	_ = s.d.Notifier.AlertAdmin(ctx,
		fmt.Sprintf("New guest ticket %s [%s]", ticket.TicketNumber, dept.Name),
		fmt.Sprintf("Guest ticket %s (%s) opened by %s <%s> in department %s (%s).",
			ticket.TicketNumber, ticket.Subject, in.GuestName, in.GuestEmail, dept.Name, dept.Email))

	return ticket, nil
}

// ListMine lists the client's tickets (optional status filter).
func (s *Service) ListMine(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Ticket, int64, error) {
	if p.Status != "" && !validTicketStatus(p.Status) {
		return nil, 0, apperr.Validation("invalid status filter",
			apperr.FieldError{Field: "status", Message: "must be one of: open, answered, customer_reply, on_hold, closed"})
	}
	return s.d.Tickets.ListByClient(ctx, clientID, p)
}

// GetMine returns the client's ticket detail without internal notes.
// Other clients' tickets yield NOT_FOUND (never FORBIDDEN).
func (s *Service) GetMine(ctx context.Context, clientID, ticketID int64) (*TicketDetail, error) {
	ticket, err := s.getOwned(ctx, clientID, ticketID)
	if err != nil {
		return nil, err
	}
	return s.detail(ctx, ticket, false)
}

// ReplyMine adds a client reply: status -> customer_reply, notifies the
// assigned staff user (or alerts the admin address when unassigned).
func (s *Service) ReplyMine(ctx context.Context, clientID, ticketID int64, in ReplyInput, files []AttachmentUpload) (*TicketDetail, error) {
	in.IsInternal = false // clients can never write internal notes
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	ticket, err := s.getOwned(ctx, clientID, ticketID)
	if err != nil {
		return nil, err
	}
	if ticket.Status == domain.TicketClosed {
		return nil, apperr.Conflict("ticket is closed")
	}
	if err := s.validateAttachments(ctx, files); err != nil {
		return nil, err
	}
	client, err := s.d.Clients.GetByID(ctx, clientID)
	if err != nil {
		return nil, err
	}

	now := s.d.Clock.Now()
	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		attachments, err := s.storeAttachments(txCtx, ticket.TicketNumber, files)
		if err != nil {
			return err
		}
		userID := client.UserID
		if err := s.d.Tickets.AddReply(txCtx, &domain.TicketReply{
			TicketID:    ticket.ID,
			UserID:      &userID,
			AuthorName:  client.FullName(),
			Message:     in.Message,
			Attachments: attachments,
		}); err != nil {
			return err
		}
		ticket.Status = domain.TicketCustomerReply
		ticket.LastReplyAt = &now
		return s.d.Tickets.Update(txCtx, ticket)
	})
	if err != nil {
		return nil, err
	}

	s.d.Audit.Log(ctx, client.UserID, "ticket.reply", "ticket", ticket.ID, nil,
		map[string]any{"ticket_number": ticket.TicketNumber})

	data := s.templateData(ticket, s.adminTicketURL(ticket.ID))
	if ticket.AssignedUserID != nil {
		_ = s.d.Notifier.SendTemplate(ctx, *ticket.AssignedUserID, "ticket_replied", data)
	} else {
		_ = s.d.Notifier.AlertAdmin(ctx,
			fmt.Sprintf("Client reply on ticket %s", ticket.TicketNumber),
			fmt.Sprintf("Ticket %s (%s) received a client reply.", ticket.TicketNumber, ticket.Subject))
	}
	return s.detail(ctx, ticket, false)
}

// CloseMine closes the client's own ticket.
func (s *Service) CloseMine(ctx context.Context, clientID, ticketID int64) (*domain.Ticket, error) {
	ticket, err := s.getOwned(ctx, clientID, ticketID)
	if err != nil {
		return nil, err
	}
	closed, err := s.close(ctx, ticket)
	if err != nil {
		return nil, err
	}
	if client, cerr := s.d.Clients.GetByID(ctx, clientID); cerr == nil && client != nil {
		s.d.Audit.Log(ctx, client.UserID, "ticket.close", "ticket", closed.ID, nil,
			map[string]any{"ticket_number": closed.TicketNumber})
	}
	return closed, nil
}

// Staff/admin use-cases

// AdminList lists tickets with status/department/assignee filters.
func (s *Service) AdminList(ctx context.Context, f AdminListFilter) ([]domain.Ticket, int64, error) {
	if err := s.val.Struct(f); err != nil {
		return nil, 0, err
	}
	return s.d.Search.ListAdmin(ctx, f)
}

// AdminGet returns the full detail including internal notes.
func (s *Service) AdminGet(ctx context.Context, ticketID int64) (*TicketDetail, error) {
	ticket, err := s.get(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	return s.detail(ctx, ticket, true)
}

// Assign sets (or clears, with 0) the assigned staff user. Audited.
func (s *Service) Assign(ctx context.Context, actorUserID, ticketID int64, in AssignInput) (*domain.Ticket, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	ticket, err := s.get(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	before := ticket.AssignedUserID
	if in.AssignedUserID == 0 {
		ticket.AssignedUserID = nil
	} else {
		assignee, err := s.d.Users.GetByID(ctx, in.AssignedUserID)
		if err != nil {
			return nil, err
		}
		if assignee == nil || (assignee.Role != domain.RoleAdmin && assignee.Role != domain.RoleStaff) {
			return nil, apperr.Validation("invalid assignee",
				apperr.FieldError{Field: "assigned_user_id", Message: "must be an admin or staff user"})
		}
		ticket.AssignedUserID = &in.AssignedUserID
	}
	if err := s.d.Tickets.Update(ctx, ticket); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "ticket.assign", "ticket", ticket.ID,
		map[string]any{"assigned_user_id": before},
		map[string]any{"assigned_user_id": ticket.AssignedUserID})
	return ticket, nil
}

// AdminReply adds a staff reply. Public replies move the ticket to answered
// and notify the client; internal notes change nothing and stay hidden from
// clients. Public replies on closed tickets are rejected; internal notes are
// always allowed.
func (s *Service) AdminReply(ctx context.Context, actorUserID, ticketID int64, in ReplyInput, files []AttachmentUpload) (*TicketDetail, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	ticket, err := s.get(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if ticket.Status == domain.TicketClosed && !in.IsInternal {
		return nil, apperr.Conflict("ticket is closed")
	}
	if err := s.validateAttachments(ctx, files); err != nil {
		return nil, err
	}
	actor, err := s.d.Users.GetByID(ctx, actorUserID)
	if err != nil {
		return nil, err
	}
	authorName := "Staff"
	if actor != nil && actor.Email != "" {
		authorName = actor.Email
	}

	now := s.d.Clock.Now()
	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		attachments, err := s.storeAttachments(txCtx, ticket.TicketNumber, files)
		if err != nil {
			return err
		}
		uid := actorUserID
		if err := s.d.Tickets.AddReply(txCtx, &domain.TicketReply{
			TicketID:    ticket.ID,
			UserID:      &uid,
			AuthorName:  authorName,
			Message:     in.Message,
			IsInternal:  in.IsInternal,
			Attachments: attachments,
		}); err != nil {
			return err
		}
		if in.IsInternal {
			return nil
		}
		ticket.Status = domain.TicketAnswered
		ticket.LastReplyAt = &now
		return s.d.Tickets.Update(txCtx, ticket)
	})
	if err != nil {
		return nil, err
	}

	s.d.Audit.Log(ctx, actorUserID, "ticket.reply", "ticket", ticket.ID, nil,
		map[string]any{"ticket_number": ticket.TicketNumber, "is_internal": in.IsInternal})

	if !in.IsInternal && ticket.ClientID != nil {
		if client, cerr := s.d.Clients.GetByID(ctx, *ticket.ClientID); cerr == nil && client != nil {
			data := s.templateData(ticket, s.clientTicketURL(ticket.ID))
			data["Name"] = client.FullName()
			_ = s.d.Notifier.SendTemplate(ctx, client.UserID, "ticket_replied", data)
		}
	}
	return s.detail(ctx, ticket, true)
}

// SetStatus sets an arbitrary valid ticket status (staff/admin), managing
// closed_at on close/reopen. Audited.
func (s *Service) SetStatus(ctx context.Context, actorUserID, ticketID int64, in SetStatusInput) (*domain.Ticket, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	ticket, err := s.get(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	to := domain.TicketStatus(in.Status)
	if ticket.Status == to {
		return nil, apperr.Conflict("ticket already has status " + in.Status)
	}
	before := ticket.Status
	ticket.Status = to
	if to == domain.TicketClosed {
		now := s.d.Clock.Now()
		ticket.ClosedAt = &now
	} else {
		ticket.ClosedAt = nil
	}
	if err := s.d.Tickets.Update(ctx, ticket); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "ticket.status", "ticket", ticket.ID,
		map[string]any{"status": before}, map[string]any{"status": to})
	return ticket, nil
}

// SetPriority changes the ticket priority (staff/admin). Setting the value the
// ticket already has is a CONFLICT, mirroring SetStatus.
func (s *Service) SetPriority(ctx context.Context, actorUserID, ticketID int64, in SetPriorityInput) (*domain.Ticket, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	ticket, err := s.get(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	to := domain.TicketPriority(in.Priority)
	if ticket.Priority == to {
		return nil, apperr.Conflict("ticket already has priority " + in.Priority)
	}
	before := ticket.Priority
	ticket.Priority = to
	if err := s.d.Tickets.Update(ctx, ticket); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "ticket.priority", "ticket", ticket.ID,
		map[string]any{"priority": before}, map[string]any{"priority": to})
	return ticket, nil
}

// Attachments

// AttachmentURL resolves attachment idx of a ticket to a presigned download
// URL. clientID != 0 enforces ownership and hides internal-note attachments;
// clientID == 0 is the staff path (all attachments).
func (s *Service) AttachmentURL(ctx context.Context, clientID, ticketID int64, idx int) (string, error) {
	var (
		ticket *domain.Ticket
		err    error
	)
	includeInternal := clientID == 0
	if includeInternal {
		ticket, err = s.get(ctx, ticketID)
	} else {
		ticket, err = s.getOwned(ctx, clientID, ticketID)
	}
	if err != nil {
		return "", err
	}
	replies, err := s.d.Tickets.ListReplies(ctx, ticket.ID, includeInternal)
	if err != nil {
		return "", err
	}
	var atts []domain.TicketAttachment
	for _, r := range replies {
		atts = append(atts, decodeAttachments(r.Attachments)...)
	}
	if idx < 0 || idx >= len(atts) {
		return "", apperr.NotFound("attachment")
	}
	att := atts[idx]
	// Force a safe response Content-Type and an "attachment" disposition
	// regardless of what content-type the object happens to be stored with,
	// so the browser always downloads the file instead of rendering it
	// inline (defense-in-depth against stored XSS via a spoofed upload
	// content-type or legacy objects stored before this fix).
	url, err := s.d.Storage.PresignGet(ctx, att.ObjectKey, presignTTL,
		ports.WithResponseContentType(safeContentType(att.Filename)),
		ports.WithResponseContentDisposition(contentDispositionAttachment(att.Filename)),
	)
	if err != nil {
		return "", apperr.Internal(err)
	}
	return url, nil
}

// Departments

// ListDepartments lists departments; activeOnly for the public endpoint.
func (s *Service) ListDepartments(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error) {
	return s.d.Tickets.ListDepartments(ctx, activeOnly)
}

// CreateDepartment creates a department (admin). Audited.
func (s *Service) CreateDepartment(ctx context.Context, actorUserID int64, in DepartmentInput) (*domain.TicketDepartment, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	dept := &domain.TicketDepartment{Name: in.Name, Email: in.Email, Active: active, Sort: in.Sort}
	if err := s.d.Tickets.CreateDepartment(ctx, dept); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "ticket_department.create", "ticket_department", dept.ID, nil, dept)
	return dept, nil
}

// UpdateDepartment updates a department (admin). Audited.
func (s *Service) UpdateDepartment(ctx context.Context, actorUserID, id int64, in DepartmentInput) (*domain.TicketDepartment, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	dept, err := s.d.Tickets.GetDepartmentByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if dept == nil {
		return nil, apperr.NotFound("department")
	}
	before := *dept
	dept.Name = in.Name
	dept.Email = in.Email
	if in.Active != nil {
		dept.Active = *in.Active
	}
	dept.Sort = in.Sort
	if err := s.d.Tickets.UpdateDepartment(ctx, dept); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "ticket_department.update", "ticket_department", dept.ID, before, dept)
	return dept, nil
}

// DeleteDepartment deletes a department (admin); departments with tickets
// yield CONFLICT. Audited.
func (s *Service) DeleteDepartment(ctx context.Context, actorUserID, id int64) error {
	dept, err := s.d.Tickets.GetDepartmentByID(ctx, id)
	if err != nil {
		return err
	}
	if dept == nil {
		return apperr.NotFound("department")
	}
	if err := s.d.Tickets.DeleteDepartment(ctx, id); err != nil {
		return err
	}
	s.d.Audit.Log(ctx, actorUserID, "ticket_department.delete", "ticket_department", id, dept, nil)
	return nil
}

// Internals

// get loads a ticket or returns NOT_FOUND.
func (s *Service) get(ctx context.Context, ticketID int64) (*domain.Ticket, error) {
	ticket, err := s.d.Tickets.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if ticket == nil {
		return nil, apperr.NotFound("ticket")
	}
	return ticket, nil
}

// getOwned loads a ticket owned by clientID; anything else is NOT_FOUND
// (other clients' resources are 404, never 403 - CONTRACTS §1).
func (s *Service) getOwned(ctx context.Context, clientID, ticketID int64) (*domain.Ticket, error) {
	ticket, err := s.get(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if ticket.ClientID == nil || *ticket.ClientID != clientID {
		return nil, apperr.NotFound("ticket")
	}
	return ticket, nil
}

// close transitions any non-closed status to closed and stamps closed_at.
func (s *Service) close(ctx context.Context, ticket *domain.Ticket) (*domain.Ticket, error) {
	if ticket.Status == domain.TicketClosed {
		return nil, apperr.Conflict("ticket is already closed")
	}
	now := s.d.Clock.Now()
	ticket.Status = domain.TicketClosed
	ticket.ClosedAt = &now
	if err := s.d.Tickets.Update(ctx, ticket); err != nil {
		return nil, err
	}
	return ticket, nil
}

// detail assembles the ticket + department + visible thread.
func (s *Service) detail(ctx context.Context, ticket *domain.Ticket, includeInternal bool) (*TicketDetail, error) {
	replies, err := s.d.Tickets.ListReplies(ctx, ticket.ID, includeInternal)
	if err != nil {
		return nil, err
	}
	dept, err := s.d.Tickets.GetDepartmentByID(ctx, ticket.DepartmentID)
	if err != nil {
		return nil, err
	}
	views := make([]ReplyView, 0, len(replies))
	idx := 0
	for _, r := range replies {
		v := ReplyView{
			ID:          r.ID,
			UserID:      r.UserID,
			AuthorName:  r.AuthorName,
			Message:     r.Message,
			IsInternal:  r.IsInternal,
			Attachments: []AttachmentView{},
			CreatedAt:   r.CreatedAt,
		}
		for _, a := range decodeAttachments(r.Attachments) {
			v.Attachments = append(v.Attachments, AttachmentView{
				Index:       idx,
				Filename:    a.Filename,
				Size:        a.Size,
				ContentType: a.ContentType,
			})
			idx++
		}
		views = append(views, v)
	}
	return &TicketDetail{Ticket: *ticket, Department: dept, Replies: views}, nil
}

// validateAttachments enforces count, extension whitelist and size limit from
// settings (tickets.allowed_extensions / tickets.max_attachment_mb).
func (s *Service) validateAttachments(ctx context.Context, files []AttachmentUpload) error {
	if len(files) == 0 {
		return nil
	}
	if len(files) > maxAttachments {
		return apperr.Validation("too many attachments",
			apperr.FieldError{Field: "attachments", Message: fmt.Sprintf("at most %d files per message", maxAttachments)})
	}
	allowed := append([]string(nil), defaultAllowedExtensions...)
	if err := s.d.Settings.GetJSON(ctx, "tickets.allowed_extensions", &allowed); err != nil {
		return apperr.Internal(err)
	}
	maxMB, err := s.d.Settings.GetInt(ctx, "tickets.max_attachment_mb", defaultMaxAttachmentMB)
	if err != nil {
		return apperr.Internal(err)
	}
	maxBytes := int64(maxMB) * 1024 * 1024
	allowedSet := make(map[string]bool, len(allowed))
	for _, e := range allowed {
		allowedSet[strings.ToLower(strings.TrimPrefix(e, "."))] = true
	}

	var details []apperr.FieldError
	for i, f := range files {
		field := "attachments[" + strconv.Itoa(i) + "]"
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(f.Filename)), ".")
		if ext == "" || !allowedSet[ext] {
			details = append(details, apperr.FieldError{
				Field:   field,
				Message: fmt.Sprintf("file type not allowed (allowed: %s)", strings.Join(allowed, ", ")),
			})
		}
		if f.Size <= 0 || f.Size > maxBytes {
			details = append(details, apperr.FieldError{
				Field:   field,
				Message: fmt.Sprintf("file size must be between 1 byte and %d MB", maxMB),
			})
		}
	}
	if len(details) > 0 {
		return apperr.Validation("invalid attachments", details...)
	}
	return nil
}

// storeAttachments uploads files to S3 under
// tickets/<ticket_number>/<uuid>-<filename> and returns the attachments JSON
// for the reply row. Returns []byte("[]")-equivalent nil handling upstream.
func (s *Service) storeAttachments(ctx context.Context, ticketNumber string, files []AttachmentUpload) (json.RawMessage, error) {
	metas := make([]domain.TicketAttachment, 0, len(files))
	for _, f := range files {
		key := fmt.Sprintf("tickets/%s/%s-%s", ticketNumber, uuid.NewString(), sanitizeFilename(f.Filename))
		// The client-supplied multipart Content-Type header is never trusted:
		// it is attacker-controlled and can be spoofed independently of the
		// file's real bytes (e.g. a ".txt" upload sent with "Content-Type:
		// text/html" to get served back as renderable HTML). Derive the
		// stored content type solely from the (already extension-allowlisted)
		// filename instead.
		contentType := safeContentType(f.Filename)
		if err := s.d.Storage.Put(ctx, key, f.Reader, f.Size, contentType); err != nil {
			return nil, apperr.Internal(err)
		}
		metas = append(metas, domain.TicketAttachment{
			ObjectKey:   key,
			Filename:    sanitizeFilename(f.Filename),
			Size:        f.Size,
			ContentType: contentType,
		})
	}
	raw, err := json.Marshal(metas)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return raw, nil
}

// sanitizeFilename strips any path and replaces unsafe characters so the
// object key stays predictable.
func sanitizeFilename(name string) string {
	base := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		return "file"
	}
	return out
}

// decodeAttachments parses the reply attachments JSONB ([] on nil/garbage).
func decodeAttachments(raw json.RawMessage) []domain.TicketAttachment {
	if len(raw) == 0 {
		return nil
	}
	var out []domain.TicketAttachment
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// templateData builds the common template variables for ticket emails.
func (s *Service) templateData(t *domain.Ticket, url string) map[string]any {
	return map[string]any{
		"TicketNumber": t.TicketNumber,
		"Subject":      t.Subject,
		"TicketURL":    url,
	}
}

func (s *Service) clientTicketURL(id int64) string {
	return strings.TrimSuffix(s.d.FrontendURL, "/") + "/support/" + strconv.FormatInt(id, 10)
}

func (s *Service) adminTicketURL(id int64) string {
	return strings.TrimSuffix(s.d.FrontendURL, "/") + "/admin/tickets/" + strconv.FormatInt(id, 10)
}

func validTicketStatus(s string) bool {
	switch domain.TicketStatus(s) {
	case domain.TicketOpen, domain.TicketAnswered, domain.TicketCustomerReply, domain.TicketOnHold, domain.TicketClosed:
		return true
	}
	return false
}
