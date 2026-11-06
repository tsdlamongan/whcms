package clients

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/jackc/pgx/v5"
)

// Repo is the pgx repository for the clients module. It implements
// ports.ClientRepo and ports.CreditRepo plus the module-local ContactStore,
// SearchStore, AggregateStore and ExportStore interfaces. Every query goes
// through db.Querier(ctx) so it joins any TxManager transaction.
type Repo struct {
	db *db.DB
}

// NewRepo builds the clients Repo.
func NewRepo(d *db.DB) *Repo { return &Repo{db: d} }

// Compile-time interface checks.
var (
	_ ports.ClientRepo = (*Repo)(nil)
	_ ports.CreditRepo = (*Repo)(nil)
	_ ContactStore     = (*Repo)(nil)
	_ SearchStore      = (*Repo)(nil)
	_ AggregateStore   = (*Repo)(nil)
	_ ExportStore      = (*Repo)(nil)
)

// clientColumns is the shared column list scanned into domain.Client
// (no SELECT * - search_name is a generated helper column we never read).
const clientColumns = `id, user_id, first_name, last_name, company, address1, address2,
	city, state, postcode, country, phone, currency, credit_balance, status,
	notes_admin, deleted_at, created_at, updated_at`

func scanClient(row pgx.Row, c *domain.Client) error {
	return row.Scan(&c.ID, &c.UserID, &c.FirstName, &c.LastName, &c.Company,
		&c.Address1, &c.Address2, &c.City, &c.State, &c.Postcode, &c.Country,
		&c.Phone, &c.Currency, &c.CreditBalance, &c.Status, &c.NotesAdmin,
		&c.DeletedAt, &c.CreatedAt, &c.UpdatedAt)
}

// ports.ClientRepo

// Create inserts the client profile and fills the generated fields.
func (r *Repo) Create(ctx context.Context, c *domain.Client) error {
	if c.Country == "" {
		c.Country = "ID"
	}
	if c.Currency == "" {
		c.Currency = "IDR"
	}
	if c.Status == "" {
		c.Status = domain.ClientActive
	}
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO clients (user_id, first_name, last_name, company, address1,
			address2, city, state, postcode, country, phone, currency, status, notes_admin)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, credit_balance, created_at, updated_at`,
		c.UserID, c.FirstName, c.LastName, c.Company, c.Address1, c.Address2,
		c.City, c.State, c.Postcode, c.Country, c.Phone, c.Currency, c.Status, c.NotesAdmin,
	).Scan(&c.ID, &c.CreditBalance, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return fmt.Errorf("clients: create: %w", err)
	}
	return nil
}

// GetByID returns a non-deleted client by id.
func (r *Repo) GetByID(ctx context.Context, id int64) (*domain.Client, error) {
	var c domain.Client
	err := scanClient(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+clientColumns+` FROM clients WHERE id = $1 AND deleted_at IS NULL`, id), &c)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("client")
	}
	if err != nil {
		return nil, fmt.Errorf("clients: get %d: %w", id, err)
	}
	return &c, nil
}

// GetByUserID returns the non-deleted client owned by userID.
func (r *Repo) GetByUserID(ctx context.Context, userID int64) (*domain.Client, error) {
	var c domain.Client
	err := scanClient(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+clientColumns+` FROM clients WHERE user_id = $1 AND deleted_at IS NULL`, userID), &c)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("client")
	}
	if err != nil {
		return nil, fmt.Errorf("clients: get by user %d: %w", userID, err)
	}
	return &c, nil
}

// Update persists the mutable profile fields (credit_balance is only changed
// via AdjustCredit).
func (r *Repo) Update(ctx context.Context, c *domain.Client) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE clients SET first_name = $2, last_name = $3, company = $4,
			address1 = $5, address2 = $6, city = $7, state = $8, postcode = $9,
			country = $10, phone = $11, status = $12, notes_admin = $13
		WHERE id = $1 AND deleted_at IS NULL`,
		c.ID, c.FirstName, c.LastName, c.Company, c.Address1, c.Address2,
		c.City, c.State, c.Postcode, c.Country, c.Phone, c.Status, c.NotesAdmin)
	if err != nil {
		return fmt.Errorf("clients: update %d: %w", c.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("client")
	}
	return nil
}

// List returns non-deleted clients matching ListParams (Search -> search_name
// ILIKE, Status exact), newest first.
func (r *Repo) List(ctx context.Context, p ports.ListParams) ([]domain.Client, int64, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT `+clientColumns+`, count(*) OVER() AS total
		FROM clients
		WHERE deleted_at IS NULL
		  AND ($1 = '' OR search_name ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR status = $2)
		ORDER BY id DESC
		LIMIT $3 OFFSET $4`,
		p.Search, p.Status, p.Limit(), p.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("clients: list: %w", err)
	}
	defer rows.Close()

	var out []domain.Client
	var total int64
	for rows.Next() {
		var c domain.Client
		if err := rows.Scan(&c.ID, &c.UserID, &c.FirstName, &c.LastName, &c.Company,
			&c.Address1, &c.Address2, &c.City, &c.State, &c.Postcode, &c.Country,
			&c.Phone, &c.Currency, &c.CreditBalance, &c.Status, &c.NotesAdmin,
			&c.DeletedAt, &c.CreatedAt, &c.UpdatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("clients: list scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("clients: list rows: %w", err)
	}
	return out, total, nil
}

// SoftDelete marks the client deleted (idempotent-safe: second call -> NOT_FOUND).
func (r *Repo) SoftDelete(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE clients SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("clients: soft delete %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("client")
	}
	return nil
}

// AdjustCredit atomically applies delta to credit_balance and writes the
// ledger row in a single statement (safe with or without an outer tx).
// Returns the new balance; CONFLICT when the result would be negative.
func (r *Repo) AdjustCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID *int64) (int64, error) {
	var balance int64
	err := r.db.Querier(ctx).QueryRow(ctx, `
		WITH updated AS (
			UPDATE clients
			SET credit_balance = credit_balance + $2
			WHERE id = $1 AND deleted_at IS NULL AND credit_balance + $2 >= 0
			RETURNING id, credit_balance
		)
		INSERT INTO credit_ledger (client_id, delta, balance_after, reason, related_invoice_id)
		SELECT id, $2, credit_balance, $3, $4 FROM updated
		RETURNING balance_after`,
		clientID, delta, reason, relatedInvoiceID).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		// Distinguish missing client from insufficient balance.
		var exists bool
		if e := r.db.Querier(ctx).QueryRow(ctx,
			`SELECT true FROM clients WHERE id = $1 AND deleted_at IS NULL`, clientID,
		).Scan(&exists); errors.Is(e, pgx.ErrNoRows) {
			return 0, apperr.NotFound("client")
		} else if e != nil {
			return 0, fmt.Errorf("clients: adjust credit check %d: %w", clientID, e)
		}
		return 0, apperr.Conflict("insufficient credit balance")
	}
	if err != nil {
		return 0, fmt.Errorf("clients: adjust credit %d: %w", clientID, err)
	}
	return balance, nil
}

// ports.CreditRepo

// ListByClient returns the credit ledger of a client, newest first.
func (r *Repo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.CreditLedgerEntry, int64, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT id, client_id, delta, balance_after, reason, related_invoice_id,
		       created_at, updated_at, count(*) OVER() AS total
		FROM credit_ledger
		WHERE client_id = $1
		ORDER BY id DESC
		LIMIT $2 OFFSET $3`,
		clientID, p.Limit(), p.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("clients: ledger list %d: %w", clientID, err)
	}
	defer rows.Close()

	var out []domain.CreditLedgerEntry
	var total int64
	for rows.Next() {
		var e domain.CreditLedgerEntry
		if err := rows.Scan(&e.ID, &e.ClientID, &e.Delta, &e.BalanceAfter, &e.Reason,
			&e.RelatedInvoiceID, &e.CreatedAt, &e.UpdatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("clients: ledger scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("clients: ledger rows: %w", err)
	}
	return out, total, nil
}

// ContactStore

// CreateContact inserts a contact for the client.
func (r *Repo) CreateContact(ctx context.Context, ct *Contact) error {
	perms := ct.Permissions
	if len(perms) == 0 {
		perms = json.RawMessage(`{}`)
	}
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO client_contacts (client_id, first_name, last_name, email, phone, permissions)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, created_at, updated_at`,
		ct.ClientID, ct.FirstName, ct.LastName, ct.Email, ct.Phone, perms,
	).Scan(&ct.ID, &ct.CreatedAt, &ct.UpdatedAt)
	if err != nil {
		return fmt.Errorf("clients: create contact: %w", err)
	}
	ct.Permissions = perms
	return nil
}

// GetContact returns a contact scoped to the client (other clients' contacts
// -> NOT_FOUND, never FORBIDDEN - CONTRACTS §3).
func (r *Repo) GetContact(ctx context.Context, clientID, id int64) (*Contact, error) {
	var ct Contact
	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT id, client_id, first_name, last_name, email, phone, permissions, created_at, updated_at
		FROM client_contacts WHERE id = $1 AND client_id = $2`, id, clientID,
	).Scan(&ct.ID, &ct.ClientID, &ct.FirstName, &ct.LastName, &ct.Email, &ct.Phone,
		&ct.Permissions, &ct.CreatedAt, &ct.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("contact")
	}
	if err != nil {
		return nil, fmt.Errorf("clients: get contact %d: %w", id, err)
	}
	return &ct, nil
}

// ListContacts returns all contacts of a client, oldest first.
func (r *Repo) ListContacts(ctx context.Context, clientID int64) ([]Contact, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT id, client_id, first_name, last_name, email, phone, permissions, created_at, updated_at
		FROM client_contacts WHERE client_id = $1 ORDER BY id`, clientID)
	if err != nil {
		return nil, fmt.Errorf("clients: list contacts %d: %w", clientID, err)
	}
	defer rows.Close()

	var out []Contact
	for rows.Next() {
		var ct Contact
		if err := rows.Scan(&ct.ID, &ct.ClientID, &ct.FirstName, &ct.LastName,
			&ct.Email, &ct.Phone, &ct.Permissions, &ct.CreatedAt, &ct.UpdatedAt); err != nil {
			return nil, fmt.Errorf("clients: contacts scan: %w", err)
		}
		out = append(out, ct)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clients: contacts rows: %w", err)
	}
	return out, nil
}

// UpdateContact persists contact fields, scoped to its client.
func (r *Repo) UpdateContact(ctx context.Context, ct *Contact) error {
	perms := ct.Permissions
	if len(perms) == 0 {
		perms = json.RawMessage(`{}`)
	}
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE client_contacts SET first_name = $3, last_name = $4, email = $5, phone = $6, permissions = $7
		WHERE id = $1 AND client_id = $2`,
		ct.ID, ct.ClientID, ct.FirstName, ct.LastName, ct.Email, ct.Phone, perms)
	if err != nil {
		return fmt.Errorf("clients: update contact %d: %w", ct.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("contact")
	}
	ct.Permissions = perms
	return nil
}

// DeleteContact removes a contact scoped to the client.
func (r *Repo) DeleteContact(ctx context.Context, clientID, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`DELETE FROM client_contacts WHERE id = $1 AND client_id = $2`, id, clientID)
	if err != nil {
		return fmt.Errorf("clients: delete contact %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("contact")
	}
	return nil
}

// SearchStore

// SearchClients runs the admin search: ILIKE across the generated search_name
// column (trigram-indexed) and users.email, exact status filter and an
// optional has-product EXISTS join on services. Only listed columns are
// selected (p95 < 300ms design).
func (r *Repo) SearchClients(ctx context.Context, f SearchInput) ([]ClientListRow, int64, error) {
	ids := f.IDs
	if ids == nil {
		ids = []int64{} // a nil slice binds as NULL, which would filter every row out
	}
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT c.id, u.email, c.first_name, c.last_name, c.company, c.country,
		       c.phone, c.status, c.credit_balance, c.created_at,
		       count(*) OVER() AS total
		FROM clients c
		JOIN users u ON u.id = c.user_id
		WHERE c.deleted_at IS NULL
		  AND ($1 = '' OR c.search_name ILIKE '%' || $1 || '%' OR u.email ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR c.status = $2)
		  AND (NOT $3 OR EXISTS (SELECT 1 FROM services s WHERE s.client_id = c.id))
		  AND (cardinality($4::bigint[]) = 0 OR c.id = ANY($4))
		ORDER BY c.id DESC
		LIMIT $5 OFFSET $6`,
		f.Search, f.Status, f.HasProduct, ids, f.limit(), f.offset())
	if err != nil {
		return nil, 0, fmt.Errorf("clients: search: %w", err)
	}
	defer rows.Close()

	var out []ClientListRow
	var total int64
	for rows.Next() {
		var row ClientListRow
		if err := rows.Scan(&row.ID, &row.Email, &row.FirstName, &row.LastName,
			&row.Company, &row.Country, &row.Phone, &row.Status,
			&row.CreditBalance, &row.CreatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("clients: search scan: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("clients: search rows: %w", err)
	}
	return out, total, nil
}

// AggregateStore

// Counts returns the per-client counters for the detail aggregate in one
// round trip (all subqueries hit client_id FK indexes).
func (r *Repo) Counts(ctx context.Context, clientID int64) (*ClientCounts, error) {
	var c ClientCounts
	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM services WHERE client_id = $1),
			(SELECT count(*) FROM services WHERE client_id = $1 AND status = 'active'),
			(SELECT count(*) FROM domains  WHERE client_id = $1),
			(SELECT count(*) FROM invoices WHERE client_id = $1),
			(SELECT count(*) FROM invoices WHERE client_id = $1 AND status IN ('unpaid','overdue')),
			(SELECT COALESCE(SUM(total), 0) FROM invoices WHERE client_id = $1 AND status IN ('unpaid','overdue')),
			(SELECT count(*) FROM tickets  WHERE client_id = $1),
			(SELECT count(*) FROM tickets  WHERE client_id = $1 AND status <> 'closed'),
			(SELECT count(*) FROM transactions t JOIN invoices i ON i.id = t.invoice_id WHERE i.client_id = $1)`,
		clientID,
	).Scan(&c.Services, &c.ServicesActive, &c.Domains, &c.Invoices, &c.InvoicesUnpaid,
		&c.UnpaidTotal, &c.Tickets, &c.TicketsOpen, &c.Transactions)
	if err != nil {
		return nil, fmt.Errorf("clients: counts %d: %w", clientID, err)
	}
	return &c, nil
}

// Recent returns the newest `limit` rows of each related entity.
func (r *Repo) Recent(ctx context.Context, clientID int64, limit int) (*ClientRecent, error) {
	if limit < 1 {
		limit = 5
	}
	q := r.db.Querier(ctx)
	out := &ClientRecent{}

	rows, err := q.Query(ctx, `
		SELECT s.id, p.name, s.domain, s.status, s.billing_cycle, s.recurring_amount, s.next_due_date
		FROM services s JOIN products p ON p.id = s.product_id
		WHERE s.client_id = $1 ORDER BY s.id DESC LIMIT $2`, clientID, limit)
	if err != nil {
		return nil, fmt.Errorf("clients: recent services %d: %w", clientID, err)
	}
	for rows.Next() {
		var s ServiceSummary
		if err := rows.Scan(&s.ID, &s.ProductName, &s.Domain, &s.Status,
			&s.BillingCycle, &s.RecurringAmount, &s.NextDueDate); err != nil {
			rows.Close()
			return nil, fmt.Errorf("clients: recent services scan: %w", err)
		}
		out.Services = append(out.Services, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clients: recent services rows: %w", err)
	}

	rows, err = q.Query(ctx, `
		SELECT id, name, status, expiry_date, next_due_date, recurring_amount
		FROM domains WHERE client_id = $1 ORDER BY id DESC LIMIT $2`, clientID, limit)
	if err != nil {
		return nil, fmt.Errorf("clients: recent domains %d: %w", clientID, err)
	}
	for rows.Next() {
		var d DomainSummary
		if err := rows.Scan(&d.ID, &d.Name, &d.Status, &d.ExpiryDate,
			&d.NextDueDate, &d.RecurringAmount); err != nil {
			rows.Close()
			return nil, fmt.Errorf("clients: recent domains scan: %w", err)
		}
		out.Domains = append(out.Domains, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clients: recent domains rows: %w", err)
	}

	rows, err = q.Query(ctx, `
		SELECT id, invoice_number, status, total, due_date, created_at
		FROM invoices WHERE client_id = $1 ORDER BY id DESC LIMIT $2`, clientID, limit)
	if err != nil {
		return nil, fmt.Errorf("clients: recent invoices %d: %w", clientID, err)
	}
	for rows.Next() {
		var i InvoiceSummary
		if err := rows.Scan(&i.ID, &i.InvoiceNumber, &i.Status, &i.Total,
			&i.DueDate, &i.CreatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("clients: recent invoices scan: %w", err)
		}
		out.Invoices = append(out.Invoices, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clients: recent invoices rows: %w", err)
	}

	rows, err = q.Query(ctx, `
		SELECT id, ticket_number, subject, status, priority, last_reply_at
		FROM tickets WHERE client_id = $1 ORDER BY id DESC LIMIT $2`, clientID, limit)
	if err != nil {
		return nil, fmt.Errorf("clients: recent tickets %d: %w", clientID, err)
	}
	for rows.Next() {
		var t TicketSummary
		if err := rows.Scan(&t.ID, &t.TicketNumber, &t.Subject, &t.Status,
			&t.Priority, &t.LastReplyAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("clients: recent tickets scan: %w", err)
		}
		out.Tickets = append(out.Tickets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clients: recent tickets rows: %w", err)
	}

	rows, err = q.Query(ctx, `
		SELECT t.id, t.invoice_id, t.gateway, t.amount, t.status, t.created_at
		FROM transactions t JOIN invoices i ON i.id = t.invoice_id
		WHERE i.client_id = $1 ORDER BY t.id DESC LIMIT $2`, clientID, limit)
	if err != nil {
		return nil, fmt.Errorf("clients: recent transactions %d: %w", clientID, err)
	}
	for rows.Next() {
		var t TransactionSummary
		if err := rows.Scan(&t.ID, &t.InvoiceID, &t.Gateway, &t.Amount,
			&t.Status, &t.CreatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("clients: recent transactions scan: %w", err)
		}
		out.Transactions = append(out.Transactions, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clients: recent transactions rows: %w", err)
	}

	return out, nil
}

// ExportStore

// ForEachExportRow streams every non-deleted client (joined with the user
// email) through fn, ordered by id - used by the CSV export.
func (r *Repo) ForEachExportRow(ctx context.Context, fn func(ExportRow) error) error {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT c.id, u.email, c.first_name, c.last_name, c.company, c.city,
		       c.country, c.phone, c.status, c.credit_balance, c.created_at
		FROM clients c
		JOIN users u ON u.id = c.user_id
		WHERE c.deleted_at IS NULL
		ORDER BY c.id`)
	if err != nil {
		return fmt.Errorf("clients: export: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var row ExportRow
		if err := rows.Scan(&row.ID, &row.Email, &row.FirstName, &row.LastName,
			&row.Company, &row.City, &row.Country, &row.Phone, &row.Status,
			&row.CreditBalance, &row.CreatedAt); err != nil {
			return fmt.Errorf("clients: export scan: %w", err)
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("clients: export rows: %w", err)
	}
	return nil
}
