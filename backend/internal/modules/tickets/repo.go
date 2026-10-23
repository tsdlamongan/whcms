package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Repo is the pgx ports.TicketRepo (+ AdminSearchStore) for the tickets,
// ticket_replies and ticket_departments tables. Every query runs through
// db.Querier(ctx) so it joins the transaction started by TxManager.WithinTx.
type Repo struct {
	db *db.DB
}

// NewRepo builds the tickets Repo.
func NewRepo(d *db.DB) *Repo { return &Repo{db: d} }

const ticketColumns = `id, ticket_number, client_id, department_id, subject, status,
	priority, assigned_user_id, guest_name, guest_email, last_reply_at, closed_at,
	created_at, updated_at`

func scanTicket(row pgx.Row, t *domain.Ticket) error {
	var guestName, guestEmail sql.NullString
	if err := row.Scan(&t.ID, &t.TicketNumber, &t.ClientID, &t.DepartmentID, &t.Subject,
		&t.Status, &t.Priority, &t.AssignedUserID, &guestName, &guestEmail, &t.LastReplyAt, &t.ClosedAt,
		&t.CreatedAt, &t.UpdatedAt); err != nil {
		return err
	}
	t.GuestName = guestName.String
	t.GuestEmail = guestEmail.String
	return nil
}

// nullIfEmpty maps "" to a NULL text value (guest_name/guest_email are only set
// for guest tickets; client tickets leave them NULL rather than empty string).
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Create inserts a ticket and fills ID/timestamps.
func (r *Repo) Create(ctx context.Context, t *domain.Ticket) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO tickets (ticket_number, client_id, department_id, subject, status,
			priority, assigned_user_id, guest_name, guest_email, last_reply_at, closed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at`,
		t.TicketNumber, t.ClientID, t.DepartmentID, t.Subject, t.Status,
		t.Priority, t.AssignedUserID, nullIfEmpty(t.GuestName), nullIfEmpty(t.GuestEmail),
		t.LastReplyAt, t.ClosedAt).
		Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("tickets: create: %w", err)
	}
	return nil
}

// GetByID returns the ticket or a NOT_FOUND apperr.
func (r *Repo) GetByID(ctx context.Context, id int64) (*domain.Ticket, error) {
	var t domain.Ticket
	err := scanTicket(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+ticketColumns+` FROM tickets WHERE id = $1`, id), &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("ticket")
	}
	if err != nil {
		return nil, fmt.Errorf("tickets: get %d: %w", id, err)
	}
	return &t, nil
}

// Update persists the mutable ticket fields.
func (r *Repo) Update(ctx context.Context, t *domain.Ticket) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE tickets SET department_id = $2, subject = $3, status = $4, priority = $5,
			assigned_user_id = $6, last_reply_at = $7, closed_at = $8
		WHERE id = $1`,
		t.ID, t.DepartmentID, t.Subject, t.Status, t.Priority,
		t.AssignedUserID, t.LastReplyAt, t.ClosedAt)
	if err != nil {
		return fmt.Errorf("tickets: update %d: %w", t.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("ticket")
	}
	return nil
}

// List lists tickets with the common ListParams filters (status, search on
// number/subject), newest activity first.
func (r *Repo) List(ctx context.Context, p ports.ListParams) ([]domain.Ticket, int64, error) {
	return r.list(ctx, 0, p)
}

// ListByClient lists one client's tickets.
func (r *Repo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Ticket, int64, error) {
	return r.list(ctx, clientID, p)
}

func (r *Repo) list(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Ticket, int64, error) {
	where, args := []string{"TRUE"}, []any{}
	if clientID != 0 {
		args = append(args, clientID)
		where = append(where, "client_id = $"+strconv.Itoa(len(args)))
	}
	if p.Status != "" {
		args = append(args, p.Status)
		where = append(where, "status = $"+strconv.Itoa(len(args)))
	}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		n := strconv.Itoa(len(args))
		where = append(where, "(ticket_number ILIKE $"+n+" OR subject ILIKE $"+n+")")
	}
	return r.queryTickets(ctx, strings.Join(where, " AND "), args, p.Limit(), p.Offset())
}

// ListAdmin lists tickets with the staff filters (status, department,
// assignee, search), newest activity first.
func (r *Repo) ListAdmin(ctx context.Context, f AdminListFilter) ([]domain.Ticket, int64, error) {
	where, args := []string{"TRUE"}, []any{}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, "status = $"+strconv.Itoa(len(args)))
	}
	if f.Priority != "" {
		args = append(args, f.Priority)
		where = append(where, "priority = $"+strconv.Itoa(len(args)))
	}
	if f.ClientID != 0 {
		args = append(args, f.ClientID)
		where = append(where, "client_id = $"+strconv.Itoa(len(args)))
	}
	if f.DepartmentID != 0 {
		args = append(args, f.DepartmentID)
		where = append(where, "department_id = $"+strconv.Itoa(len(args)))
	}
	if f.AssignedUserID != 0 {
		args = append(args, f.AssignedUserID)
		where = append(where, "assigned_user_id = $"+strconv.Itoa(len(args)))
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		n := strconv.Itoa(len(args))
		where = append(where, "(ticket_number ILIKE $"+n+" OR subject ILIKE $"+n+")")
	}
	return r.queryTickets(ctx, strings.Join(where, " AND "), args, f.Limit(), f.Offset())
}

func (r *Repo) queryTickets(ctx context.Context, where string, args []any, limit, offset int) ([]domain.Ticket, int64, error) {
	q := r.db.Querier(ctx)
	var total int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM tickets WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("tickets: count: %w", err)
	}
	args = append(args, limit, offset)
	rows, err := q.Query(ctx, `
		SELECT `+ticketColumns+` FROM tickets WHERE `+where+`
		ORDER BY COALESCE(last_reply_at, created_at) DESC, id DESC
		LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("tickets: list: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Ticket, 0, limit)
	for rows.Next() {
		var t domain.Ticket
		if err := scanTicket(rows, &t); err != nil {
			return nil, 0, fmt.Errorf("tickets: scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("tickets: rows: %w", err)
	}
	return out, total, nil
}

// NextNumber increments and returns the counter for scope ("ticket"); call
// inside the ticket-creation transaction.
func (r *Repo) NextNumber(ctx context.Context, scope string) (int64, error) {
	var n int64
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO counters (scope, value) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET value = counters.value + 1
		RETURNING value`, scope).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("tickets: next number %s: %w", scope, err)
	}
	return n, nil
}

// Replies

// AddReply inserts a reply and fills ID/timestamps. Nil attachments become [].
func (r *Repo) AddReply(ctx context.Context, reply *domain.TicketReply) error {
	attachments := reply.Attachments
	if len(attachments) == 0 {
		attachments = []byte("[]")
	}
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO ticket_replies (ticket_id, user_id, author_name, message, is_internal, attachments)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`,
		reply.TicketID, reply.UserID, reply.AuthorName, reply.Message, reply.IsInternal, attachments).
		Scan(&reply.ID, &reply.CreatedAt, &reply.UpdatedAt)
	if err != nil {
		return fmt.Errorf("tickets: add reply: %w", err)
	}
	return nil
}

// ListReplies returns the thread oldest-first; internal notes only when
// includeInternal (staff view).
func (r *Repo) ListReplies(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.TicketReply, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT id, ticket_id, user_id, author_name, message, is_internal, attachments, created_at, updated_at
		FROM ticket_replies
		WHERE ticket_id = $1 AND ($2 OR NOT is_internal)
		ORDER BY created_at ASC, id ASC`, ticketID, includeInternal)
	if err != nil {
		return nil, fmt.Errorf("tickets: list replies: %w", err)
	}
	defer rows.Close()
	var out []domain.TicketReply
	for rows.Next() {
		var reply domain.TicketReply
		if err := rows.Scan(&reply.ID, &reply.TicketID, &reply.UserID, &reply.AuthorName,
			&reply.Message, &reply.IsInternal, &reply.Attachments, &reply.CreatedAt, &reply.UpdatedAt); err != nil {
			return nil, fmt.Errorf("tickets: scan reply: %w", err)
		}
		out = append(out, reply)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tickets: reply rows: %w", err)
	}
	return out, nil
}

// Departments

// CreateDepartment inserts a department and fills ID/timestamps.
func (r *Repo) CreateDepartment(ctx context.Context, d *domain.TicketDepartment) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO ticket_departments (name, email, active, sort)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`,
		d.Name, d.Email, d.Active, d.Sort).
		Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return fmt.Errorf("tickets: create department: %w", err)
	}
	return nil
}

// GetDepartmentByID returns the department or a NOT_FOUND apperr.
func (r *Repo) GetDepartmentByID(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
	var d domain.TicketDepartment
	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT id, name, email, active, sort, created_at, updated_at
		FROM ticket_departments WHERE id = $1`, id).
		Scan(&d.ID, &d.Name, &d.Email, &d.Active, &d.Sort, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("department")
	}
	if err != nil {
		return nil, fmt.Errorf("tickets: get department %d: %w", id, err)
	}
	return &d, nil
}

// UpdateDepartment persists the department fields.
func (r *Repo) UpdateDepartment(ctx context.Context, d *domain.TicketDepartment) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE ticket_departments SET name = $2, email = $3, active = $4, sort = $5
		WHERE id = $1`,
		d.ID, d.Name, d.Email, d.Active, d.Sort)
	if err != nil {
		return fmt.Errorf("tickets: update department %d: %w", d.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("department")
	}
	return nil
}

// ListDepartments returns departments ordered by sort, name.
func (r *Repo) ListDepartments(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT id, name, email, active, sort, created_at, updated_at
		FROM ticket_departments
		WHERE ($1 = FALSE OR active)
		ORDER BY sort ASC, name ASC`, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("tickets: list departments: %w", err)
	}
	defer rows.Close()
	var out []domain.TicketDepartment
	for rows.Next() {
		var d domain.TicketDepartment
		if err := rows.Scan(&d.ID, &d.Name, &d.Email, &d.Active, &d.Sort, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("tickets: scan department: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tickets: department rows: %w", err)
	}
	return out, nil
}

// DeleteDepartment removes a department; departments referenced by tickets
// yield a CONFLICT apperr (FK violation).
func (r *Repo) DeleteDepartment(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM ticket_departments WHERE id = $1`, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
			return apperr.Conflict("department has tickets and cannot be deleted")
		}
		return fmt.Errorf("tickets: delete department %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("department")
	}
	return nil
}
