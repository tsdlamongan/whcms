package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/jackc/pgx/v5"
)

// Repo is the pgx implementation of ports.InvoiceRepo plus the module-local
// queries billing needs (order-item lookups, open-renewal checks, reminder
// dedupe). Every query goes through db.Querier(ctx) so it joins any
// transaction started by TxManager.WithinTx.
type Repo struct {
	db *db.DB
}

// NewRepo builds the billing Repo.
func NewRepo(d *db.DB) *Repo { return &Repo{db: d} }

// Compile-time interface checks.
var (
	_ ports.InvoiceRepo = (*Repo)(nil)
	_ InvoiceStore      = (*Repo)(nil)
)

const invoiceColumns = `id, invoice_number, client_id, status, subtotal, discount, tax_rate,
	tax_total, credit_applied, total, currency, due_date, paid_at, notes, pdf_object_key,
	created_at, updated_at`

func scanInvoice(row pgx.Row) (*domain.Invoice, error) {
	var inv domain.Invoice
	err := row.Scan(&inv.ID, &inv.InvoiceNumber, &inv.ClientID, &inv.Status, &inv.Subtotal,
		&inv.Discount, &inv.TaxRate, &inv.TaxTotal, &inv.CreditApplied, &inv.Total,
		&inv.Currency, &inv.DueDate, &inv.PaidAt, &inv.Notes, &inv.PDFObjectKey,
		&inv.CreatedAt, &inv.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("invoice")
	}
	if err != nil {
		return nil, fmt.Errorf("billing: scan invoice: %w", err)
	}
	return &inv, nil
}

// Create inserts the invoice and its items in one go (call inside a tx).
func (r *Repo) Create(ctx context.Context, inv *domain.Invoice, items []domain.InvoiceItem) error {
	q := r.db.Querier(ctx)
	err := q.QueryRow(ctx, `
		INSERT INTO invoices (invoice_number, client_id, status, subtotal, discount, tax_rate,
			tax_total, credit_applied, total, currency, due_date, paid_at, notes, pdf_object_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, created_at, updated_at`,
		inv.InvoiceNumber, inv.ClientID, inv.Status, inv.Subtotal, inv.Discount, inv.TaxRate,
		inv.TaxTotal, inv.CreditApplied, inv.Total, inv.Currency, inv.DueDate, inv.PaidAt,
		inv.Notes, inv.PDFObjectKey,
	).Scan(&inv.ID, &inv.CreatedAt, &inv.UpdatedAt)
	if err != nil {
		return fmt.Errorf("billing: create invoice: %w", err)
	}
	for i := range items {
		items[i].InvoiceID = inv.ID
		if err := r.AddItem(ctx, &items[i]); err != nil {
			return err
		}
	}
	return nil
}

// GetByID returns one invoice by id.
func (r *Repo) GetByID(ctx context.Context, id int64) (*domain.Invoice, error) {
	return scanInvoice(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+invoiceColumns+` FROM invoices WHERE id = $1`, id))
}

// GetByNumber returns one invoice by its unique number.
func (r *Repo) GetByNumber(ctx context.Context, number string) (*domain.Invoice, error) {
	return scanInvoice(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+invoiceColumns+` FROM invoices WHERE invoice_number = $1`, number))
}

// GetByIDForUpdate row-locks and returns the invoice; call inside WithinTx.
func (r *Repo) GetByIDForUpdate(ctx context.Context, id int64) (*domain.Invoice, error) {
	return scanInvoice(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+invoiceColumns+` FROM invoices WHERE id = $1 FOR UPDATE`, id))
}

// GetItems lists the invoice's line items.
func (r *Repo) GetItems(ctx context.Context, invoiceID int64) ([]domain.InvoiceItem, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT id, invoice_id, description, amount, taxed, related_type, related_id,
			created_at, updated_at
		FROM invoice_items WHERE invoice_id = $1 ORDER BY id`, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("billing: get items: %w", err)
	}
	defer rows.Close()

	var out []domain.InvoiceItem
	for rows.Next() {
		var it domain.InvoiceItem
		if err := rows.Scan(&it.ID, &it.InvoiceID, &it.Description, &it.Amount, &it.Taxed,
			&it.RelatedType, &it.RelatedID, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("billing: scan item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetItemsByInvoiceIDs batch-loads items for many invoices in one query,
// keyed by invoice_id, to avoid an N+1 query per invoice.
func (r *Repo) GetItemsByInvoiceIDs(ctx context.Context, invoiceIDs []int64) (map[int64][]domain.InvoiceItem, error) {
	out := make(map[int64][]domain.InvoiceItem)
	if len(invoiceIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT id, invoice_id, description, amount, taxed, related_type, related_id,
			created_at, updated_at
		FROM invoice_items WHERE invoice_id = ANY($1) ORDER BY invoice_id, id`, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("billing: get items by invoice ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var it domain.InvoiceItem
		if err := rows.Scan(&it.ID, &it.InvoiceID, &it.Description, &it.Amount, &it.Taxed,
			&it.RelatedType, &it.RelatedID, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("billing: scan item: %w", err)
		}
		out[it.InvoiceID] = append(out[it.InvoiceID], it)
	}
	return out, rows.Err()
}

// AddItem inserts one invoice item.
func (r *Repo) AddItem(ctx context.Context, item *domain.InvoiceItem) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO invoice_items (invoice_id, description, amount, taxed, related_type, related_id)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, created_at, updated_at`,
		item.InvoiceID, item.Description, item.Amount, item.Taxed, item.RelatedType, item.RelatedID,
	).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return fmt.Errorf("billing: add item: %w", err)
	}
	return nil
}

// Update persists the mutable invoice columns.
func (r *Repo) Update(ctx context.Context, inv *domain.Invoice) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE invoices SET status=$2, subtotal=$3, discount=$4, tax_rate=$5, tax_total=$6,
			credit_applied=$7, total=$8, due_date=$9, paid_at=$10, notes=$11, pdf_object_key=$12
		WHERE id = $1`,
		inv.ID, inv.Status, inv.Subtotal, inv.Discount, inv.TaxRate, inv.TaxTotal,
		inv.CreditApplied, inv.Total, inv.DueDate, inv.PaidAt, inv.Notes, inv.PDFObjectKey)
	if err != nil {
		return fmt.Errorf("billing: update invoice: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("invoice")
	}
	return nil
}

// UpdateStatus sets the status; paid_at is updated only when paidAt != nil.
func (r *Repo) UpdateStatus(ctx context.Context, id int64, status domain.InvoiceStatus, paidAt *time.Time) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE invoices SET status = $2, paid_at = COALESCE($3, paid_at) WHERE id = $1`,
		id, status, paidAt)
	if err != nil {
		return fmt.Errorf("billing: update status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("invoice")
	}
	return nil
}

// SetPDFObjectKey stores the rendered PDF's object key.
func (r *Repo) SetPDFObjectKey(ctx context.Context, id int64, key string) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE invoices SET pdf_object_key = $2 WHERE id = $1`, id, key)
	if err != nil {
		return fmt.Errorf("billing: set pdf key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("invoice")
	}
	return nil
}

func (r *Repo) list(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error) {
	where := `WHERE ($1 = 0 OR client_id = $1)
		AND ($2 = '' OR status = $2)
		AND ($3 = '' OR invoice_number ILIKE '%' || $3 || '%')
		AND ($4::timestamptz IS NULL OR created_at >= $4)
		AND ($5::timestamptz IS NULL OR created_at < $5)
		AND (cardinality($6::bigint[]) = 0 OR id = ANY($6))`
	from, toExclusive := p.CreatedBounds()
	ids := p.IDs
	if ids == nil {
		ids = []int64{} // a nil slice binds as NULL, which would filter every row out
	}
	args := []any{clientID, p.Status, p.Search, from, toExclusive, ids}

	var total int64
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM invoices `+where, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("billing: count invoices: %w", err)
	}

	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+invoiceColumns+` FROM invoices `+where+
			` ORDER BY id DESC LIMIT $7 OFFSET $8`,
		append(args, p.Limit(), p.Offset())...)
	if err != nil {
		return nil, 0, fmt.Errorf("billing: list invoices: %w", err)
	}
	defer rows.Close()

	var out []domain.Invoice
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *inv)
	}
	return out, total, rows.Err()
}

// List returns invoices for the admin listing (Status exact, Search matches
// the invoice number, ClientID narrows to one client, IDs to a set of
// invoices, From/To bound the creation date).
func (r *Repo) List(ctx context.Context, p ports.ListParams) ([]domain.Invoice, int64, error) {
	return r.list(ctx, p.ClientID, p)
}

// ListByClient returns one client's invoices.
func (r *Repo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error) {
	return r.list(ctx, clientID, p)
}

// ListDueForStatus returns invoices in status with due_date <= before.
func (r *Repo) ListDueForStatus(ctx context.Context, status domain.InvoiceStatus, before time.Time) ([]domain.Invoice, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+invoiceColumns+` FROM invoices
		 WHERE status = $1 AND due_date <= $2 ORDER BY due_date, id`, status, before)
	if err != nil {
		return nil, fmt.Errorf("billing: list due: %w", err)
	}
	defer rows.Close()

	var out []domain.Invoice
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inv)
	}
	return out, rows.Err()
}

// NextNumber increments and returns the counter for scope (upserting the
// scope row); call inside the invoice-creation transaction.
func (r *Repo) NextNumber(ctx context.Context, scope string) (int64, error) {
	var n int64
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO counters (scope, value) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET value = counters.value + 1
		RETURNING value`, scope).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("billing: next number %s: %w", scope, err)
	}
	return n, nil
}

// Module-local queries

// OrderIDForOrderItem resolves the order an order_item belongs to (used by
// ProcessPaid to activate orders from order_item invoice lines).
func (r *Repo) OrderIDForOrderItem(ctx context.Context, orderItemID int64) (int64, error) {
	var orderID int64
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT order_id FROM order_items WHERE id = $1`, orderItemID).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, apperr.NotFound("order item")
	}
	if err != nil {
		return 0, fmt.Errorf("billing: order id for item: %w", err)
	}
	return orderID, nil
}

// HasOpenRenewalInvoice reports whether an unpaid/overdue invoice already
// exists with an item of relatedType pointing at relatedID (renewal dedupe).
func (r *Repo) HasOpenRenewalInvoice(ctx context.Context, relatedType domain.InvoiceItemRelatedType, relatedID int64) (bool, error) {
	var exists bool
	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM invoice_items ii
			JOIN invoices i ON i.id = ii.invoice_id
			WHERE ii.related_type = $1 AND ii.related_id = $2
			  AND i.status IN ('unpaid','overdue'))`,
		relatedType, relatedID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("billing: open renewal check: %w", err)
	}
	return exists, nil
}

// HasEmailSince reports whether an email with templateKey whose subject
// references ref (e.g. the invoice number) was logged at/after since -
// the reminder dedupe check.
func (r *Repo) HasEmailSince(ctx context.Context, templateKey, ref string, since time.Time) (bool, error) {
	var exists bool
	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM email_log
			WHERE template_key = $1 AND subject LIKE '%' || $2 || '%'
			  AND created_at >= $3)`,
		templateKey, ref, since).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("billing: email dedupe check: %w", err)
	}
	return exists, nil
}
