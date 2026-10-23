package tickets

import (
	"io"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
)

// Request DTOs

// CreateTicketInput opens a new ticket (client side).
type CreateTicketInput struct {
	DepartmentID int64  `json:"department_id" validate:"required,gt=0"`
	Subject      string `json:"subject" validate:"required,min=3,max=200"`
	Priority     string `json:"priority" validate:"omitempty,oneof=low medium high"`
	Message      string `json:"message" validate:"required,min=2"`
}

// ReplyInput adds a message to a ticket thread. IsInternal is honoured only
// on the staff path; client replies are always public.
type ReplyInput struct {
	Message    string `json:"message" validate:"required,min=2"`
	IsInternal bool   `json:"is_internal"`
}

// AssignInput assigns a ticket to a staff/admin user (0 unassigns).
type AssignInput struct {
	AssignedUserID int64 `json:"assigned_user_id" validate:"gte=0"`
}

// SetStatusInput sets the ticket status (staff/admin).
type SetStatusInput struct {
	Status string `json:"status" validate:"required,oneof=open answered customer_reply on_hold closed"`
}

// SetPriorityInput sets the ticket priority (staff/admin).
type SetPriorityInput struct {
	Priority string `json:"priority" validate:"required,oneof=low medium high"`
}

// DepartmentInput creates/updates a ticket department (admin).
type DepartmentInput struct {
	Name   string `json:"name" validate:"required,min=2,max=100"`
	Email  string `json:"email" validate:"omitempty,email"`
	Active *bool  `json:"active"`
	Sort   int    `json:"sort" validate:"gte=0"`
}

// AdminListFilter filters the staff/admin ticket list.
type AdminListFilter struct {
	Status         string `json:"status" validate:"omitempty,oneof=open answered customer_reply on_hold closed"`
	Priority       string `json:"priority" validate:"omitempty,oneof=low medium high"`
	DepartmentID   int64  `json:"department_id" validate:"gte=0"`
	AssignedUserID int64  `json:"assigned_user_id" validate:"gte=0"`
	ClientID       int64  `json:"client_id" validate:"gte=0"`
	Search         string `json:"search"`
	Page           int    `json:"page"`
	PerPage        int    `json:"per_page"`
}

// Limit returns the SQL limit with defaults applied (1..ports.MaxPerPage, default 25).
func (f AdminListFilter) Limit() int {
	if f.PerPage < 1 {
		return 25
	}
	if f.PerPage > ports.MaxPerPage {
		return ports.MaxPerPage
	}
	return f.PerPage
}

// Offset returns the SQL offset.
func (f AdminListFilter) Offset() int {
	page := f.Page
	if page < 1 {
		page = 1
	}
	return (page - 1) * f.Limit()
}

// AttachmentUpload is one uploaded file handed from the transport layer to
// the service. Reader is consumed by Storage.Put; the handler owns closing.
type AttachmentUpload struct {
	Filename    string
	Size        int64
	ContentType string
	Reader      io.Reader
}

// Response DTOs

// AttachmentView is one downloadable attachment. Index is the position used
// by GET /tickets/:id/attachments/:idx (computed over the replies visible to
// the caller, so clients can never address internal-note attachments).
type AttachmentView struct {
	Index       int    `json:"index"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// ReplyView is one thread message with resolved attachment views.
type ReplyView struct {
	ID          int64            `json:"id"`
	UserID      *int64           `json:"user_id"`
	AuthorName  string           `json:"author_name"`
	Message     string           `json:"message"`
	IsInternal  bool             `json:"is_internal"`
	Attachments []AttachmentView `json:"attachments"`
	CreatedAt   time.Time        `json:"created_at"`
}

// TicketDetail is a ticket with its department and visible thread.
type TicketDetail struct {
	Ticket     domain.Ticket            `json:"ticket"`
	Department *domain.TicketDepartment `json:"department,omitempty"`
	Replies    []ReplyView              `json:"replies"`
}
