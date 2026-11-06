package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgUniqueViolation is the PostgreSQL unique_violation SQLSTATE (same
// constant/pattern as domains/repo.go and auth/repo.go).
const pgUniqueViolation = "23505"

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// Repo is the pgx implementation of ports.TransactionRepo. Every query runs
// through db.Querier(ctx) so it joins the transaction started by
// TxManager.WithinTx when one is present.
type Repo struct {
	db *db.DB
}

// Compile-time contract check.
var _ ports.TransactionRepo = (*Repo)(nil)

// NewRepo builds the transactions repository.
func NewRepo(d *db.DB) *Repo { return &Repo{db: d} }

const txColumns = `id, invoice_id, gateway, method_code, merchant_order_id,
	gateway_reference, amount, fee, status, raw, paid_at, expires_at, created_at, updated_at`

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTransaction(row rowScanner) (*domain.Transaction, error) {
	var t domain.Transaction
	err := row.Scan(
		&t.ID, &t.InvoiceID, &t.Gateway, &t.MethodCode, &t.MerchantOrderID,
		&t.GatewayReference, &t.Amount, &t.Fee, &t.Status, &t.Raw, &t.PaidAt,
		&t.ExpiresAt, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Create inserts a transaction and fills its ID/timestamps. A duplicate
// merchant_order_id (unique constraint) or a second `success` row for the
// same invoice (partial unique index) surfaces as a clean CONFLICT apperr
// instead of a raw driver error - defense in depth alongside NextAttempt's
// atomic numbering (see payments/service.go payWithGateway).
func (r *Repo) Create(ctx context.Context, t *domain.Transaction) error {
	raw := t.Raw
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO transactions
			(invoice_id, gateway, method_code, merchant_order_id, gateway_reference,
			 amount, fee, status, raw, paid_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at`,
		t.InvoiceID, t.Gateway, t.MethodCode, t.MerchantOrderID, t.GatewayReference,
		t.Amount, t.Fee, t.Status, raw, t.PaidAt, t.ExpiresAt,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
	if isUniqueViolation(err) {
		return apperr.Conflict("a transaction with this merchant order id already exists")
	}
	if err != nil {
		return fmt.Errorf("transactions: create: %w", err)
	}
	return nil
}

// NextAttempt atomically increments and returns the payment-attempt counter
// for invoiceID (ports.TransactionRepo doc). Scoped per invoice so concurrent
// PayInvoice calls for the SAME invoice (double-click, client retry) each get
// a distinct, monotonically increasing attempt number - the counters INSERT
// ... ON CONFLICT DO UPDATE is a single atomic statement, so there is no
// read-then-write window to race, unlike deriving the number from
// len(ListByInvoice(...)).
func (r *Repo) NextAttempt(ctx context.Context, invoiceID int64) (int64, error) {
	scope := fmt.Sprintf("payment_attempt:%d", invoiceID)
	var n int64
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO counters (scope, value) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET value = counters.value + 1
		RETURNING value`, scope).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("transactions: next attempt %s: %w", scope, err)
	}
	return n, nil
}

// GetByID returns one transaction or a NOT_FOUND apperr.
func (r *Repo) GetByID(ctx context.Context, id int64) (*domain.Transaction, error) {
	t, err := scanTransaction(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+txColumns+` FROM transactions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("transaction")
	}
	if err != nil {
		return nil, fmt.Errorf("transactions: get %d: %w", id, err)
	}
	return t, nil
}

// GetByMerchantOrderID returns the transaction with the given gateway order
// id or a NOT_FOUND apperr.
func (r *Repo) GetByMerchantOrderID(ctx context.Context, merchantOrderID string) (*domain.Transaction, error) {
	t, err := scanTransaction(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+txColumns+` FROM transactions WHERE merchant_order_id = $1`, merchantOrderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("transaction")
	}
	if err != nil {
		return nil, fmt.Errorf("transactions: get by merchant order %s: %w", merchantOrderID, err)
	}
	return t, nil
}

// Update persists all mutable transaction fields.
func (r *Repo) Update(ctx context.Context, t *domain.Transaction) error {
	raw := t.Raw
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE transactions SET
			gateway = $2, method_code = $3, merchant_order_id = $4,
			gateway_reference = $5, amount = $6, fee = $7, status = $8,
			raw = $9, paid_at = $10
		WHERE id = $1`,
		t.ID, t.Gateway, t.MethodCode, t.MerchantOrderID, t.GatewayReference,
		t.Amount, t.Fee, t.Status, raw, t.PaidAt)
	if err != nil {
		return fmt.Errorf("transactions: update %d: %w", t.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("transaction")
	}
	return nil
}

// ListByInvoice returns every transaction for the invoice, oldest first.
func (r *Repo) ListByInvoice(ctx context.Context, invoiceID int64) ([]domain.Transaction, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+txColumns+` FROM transactions WHERE invoice_id = $1 ORDER BY id`, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("transactions: list by invoice %d: %w", invoiceID, err)
	}
	return collect(rows)
}

// ListPending returns pending transactions for the gateway created at or
// before olderThan (reconciliation input), oldest first.
func (r *Repo) ListPending(ctx context.Context, gateway domain.Gateway, olderThan time.Time) ([]domain.Transaction, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT `+txColumns+` FROM transactions
		WHERE gateway = $1 AND status = 'pending' AND created_at <= $2
		ORDER BY id`, gateway, olderThan)
	if err != nil {
		return nil, fmt.Errorf("transactions: list pending: %w", err)
	}
	return collect(rows)
}

// List returns a filtered page of transactions (newest first) plus the total
// count. Search matches merchant_order_id / gateway_reference; Status filters
// the transaction status.
func (r *Repo) List(ctx context.Context, p ports.ListParams) ([]domain.Transaction, int64, error) {
	where := []string{"TRUE"}
	args := []any{}
	if p.Status != "" {
		args = append(args, p.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if p.Gateway != "" {
		args = append(args, p.Gateway)
		where = append(where, fmt.Sprintf("gateway = $%d", len(args)))
	}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(
			"(merchant_order_id ILIKE $%d OR gateway_reference ILIKE $%d)", n, n))
	}
	if p.InvoiceID > 0 {
		args = append(args, p.InvoiceID)
		where = append(where, fmt.Sprintf("invoice_id = $%d", len(args)))
	}
	from, toExclusive := p.CreatedBounds()
	if from != nil {
		args = append(args, *from)
		where = append(where, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if toExclusive != nil {
		args = append(args, *toExclusive)
		where = append(where, fmt.Sprintf("created_at < $%d", len(args)))
	}
	cond := strings.Join(where, " AND ")

	var total int64
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE `+cond, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("transactions: count: %w", err)
	}

	args = append(args, p.Limit(), p.Offset())
	rows, err := r.db.Querier(ctx).Query(ctx, fmt.Sprintf(
		`SELECT %s FROM transactions WHERE %s ORDER BY id DESC LIMIT $%d OFFSET $%d`,
		txColumns, cond, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("transactions: list: %w", err)
	}
	out, err := collect(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListForClient lists transactions for invoices owned by clientID, joining
// through invoices (the transactions table itself carries no client_id).
func (r *Repo) ListForClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Transaction, int64, error) {
	where := []string{"i.client_id = $1"}
	args := []any{clientID}
	if p.Status != "" {
		args = append(args, p.Status)
		where = append(where, fmt.Sprintf("t.status = $%d", len(args)))
	}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(
			"(t.merchant_order_id ILIKE $%d OR t.gateway_reference ILIKE $%d)", n, n))
	}
	cond := strings.Join(where, " AND ")

	var total int64
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM transactions t JOIN invoices i ON i.id = t.invoice_id WHERE `+cond,
		args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("transactions: count for client: %w", err)
	}

	txCols := "t." + strings.ReplaceAll(txColumns, ", ", ", t.")
	args = append(args, p.Limit(), p.Offset())
	rows, err := r.db.Querier(ctx).Query(ctx, fmt.Sprintf(
		`SELECT %s FROM transactions t JOIN invoices i ON i.id = t.invoice_id
		 WHERE %s ORDER BY t.id DESC LIMIT $%d OFFSET $%d`,
		txCols, cond, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("transactions: list for client: %w", err)
	}
	out, err := collect(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func collect(rows pgx.Rows) ([]domain.Transaction, error) {
	defer rows.Close()
	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("transactions: scan: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("transactions: rows: %w", err)
	}
	return out, nil
}
