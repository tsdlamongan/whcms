package snipe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgUniqueViolation is the PostgreSQL unique_violation SQLSTATE.
const pgUniqueViolation = "23505"

const snipeCols = `id, client_id, plan_id, source, domain_name, years, status, phase,
	hunt_from, hunt_until, nameservers, invoice_id, fee_amount, domain_amount,
	renew_per_year, refundable_domain, refundable_fee, refund_amount, refunded_at,
	registrar_customer_id, attempts, last_attempt_at, last_error, lease_owner,
	lease_until, paid_at, deadline_at, won_at, won_domain_id, created_at, updated_at`

// Repo is the pgx ports.SnipeRepo.
type Repo struct {
	db *db.DB
}

var (
	_ ports.SnipeRepo     = (*Repo)(nil)
	_ ports.SnipePlanRepo = (*PlanRepo)(nil)
)

// NewRepo builds the snipe Repo.
func NewRepo(d *db.DB) *Repo { return &Repo{db: d} }

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSnipe(row rowScanner) (*domain.DomainSnipe, error) {
	var (
		s     domain.DomainSnipe
		ns    []byte
		owner *string
	)
	err := row.Scan(
		&s.ID, &s.ClientID, &s.PlanID, &s.Source, &s.DomainName, &s.Years, &s.Status, &s.Phase,
		&s.HuntFrom, &s.HuntUntil, &ns, &s.InvoiceID, &s.FeeAmount, &s.DomainAmount,
		&s.RenewPerYear, &s.RefundableDomain, &s.RefundableFee, &s.RefundAmount, &s.RefundedAt,
		&s.RegistrarCustomerID, &s.Attempts, &s.LastAttemptAt, &s.LastError, &owner,
		&s.LeaseUntil, &s.PaidAt, &s.DeadlineAt, &s.WonAt, &s.WonDomainID, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if owner != nil {
		s.LeaseOwner = *owner
	}
	s.Nameservers = []string{}
	if len(ns) > 0 {
		if err := json.Unmarshal(ns, &s.Nameservers); err != nil {
			return nil, fmt.Errorf("snipe: decode nameservers of %d: %w", s.ID, err)
		}
	}
	return &s, nil
}

func collectSnipes(rows pgx.Rows) ([]domain.DomainSnipe, error) {
	defer rows.Close()
	var out []domain.DomainSnipe
	for rows.Next() {
		s, err := scanSnipe(rows)
		if err != nil {
			return nil, fmt.Errorf("snipe: scan: %w", err)
		}
		out = append(out, *s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("snipe: rows: %w", err)
	}
	return out, nil
}

// Create inserts a snipe. The partial unique index allows only one open
// (pending_payment/active) hunt per domain name; a second one is CONFLICT.
func (r *Repo) Create(ctx context.Context, s *domain.DomainSnipe) error {
	nsJSON, err := json.Marshal(nonNilStrings(s.Nameservers))
	if err != nil {
		return apperr.Internal(err)
	}
	status := s.Status
	if status == "" {
		status = domain.SnipePendingPayment
	}
	phase := s.Phase
	if phase == "" {
		phase = domain.SnipePhasePatient
	}
	source := s.Source
	if source == "" {
		source = domain.SnipeSourceClient
	}
	years := s.Years
	if years < 1 {
		years = 1
	}
	err = r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO domain_snipes (client_id, plan_id, source, domain_name, years, status, phase,
			hunt_from, hunt_until, nameservers, invoice_id, fee_amount, domain_amount, renew_per_year,
			refundable_domain, refundable_fee, paid_at, deadline_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING id, created_at, updated_at`,
		s.ClientID, s.PlanID, source, s.DomainName, years, status, phase,
		s.HuntFrom, s.HuntUntil, nsJSON, s.InvoiceID, s.FeeAmount, s.DomainAmount, s.RenewPerYear,
		s.RefundableDomain, s.RefundableFee, s.PaidAt, s.DeadlineAt,
	).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return apperr.Conflict("a snipe for " + s.DomainName + " is already open")
		}
		return fmt.Errorf("snipe: create %s: %w", s.DomainName, err)
	}
	s.Status, s.Phase, s.Source, s.Years = status, phase, source, years
	return nil
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// GetByID fetches one snipe.
func (r *Repo) GetByID(ctx context.Context, id int64) (*domain.DomainSnipe, error) {
	s, err := scanSnipe(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+snipeCols+` FROM domain_snipes WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("snipe")
	}
	if err != nil {
		return nil, fmt.Errorf("snipe: get %d: %w", id, err)
	}
	return s, nil
}

// likeEscaper neutralises the LIKE wildcards in user-typed search text.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// List is the admin list (ListParams.Status, .Search on the domain name,
// .ClientID), newest first.
func (r *Repo) List(ctx context.Context, p ports.ListParams) ([]domain.DomainSnipe, int64, error) {
	return r.list(ctx, p, p.ClientID)
}

// ListByClient is the client-area list, always scoped to one client.
func (r *Repo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.DomainSnipe, int64, error) {
	if clientID <= 0 {
		return []domain.DomainSnipe{}, 0, nil // never fall back to "every client's hunts"
	}
	return r.list(ctx, p, clientID)
}

func (r *Repo) list(ctx context.Context, p ports.ListParams, clientID int64) ([]domain.DomainSnipe, int64, error) {
	where := ` WHERE 1=1`
	args := []any{}
	if clientID > 0 {
		args = append(args, clientID)
		where += ` AND client_id = $` + strconv.Itoa(len(args))
	}
	if p.Status != "" {
		args = append(args, p.Status)
		where += ` AND status = $` + strconv.Itoa(len(args))
	}
	if p.Search != "" {
		args = append(args, "%"+likeEscaper.Replace(strings.ToLower(p.Search))+"%")
		where += ` AND domain_name LIKE $` + strconv.Itoa(len(args))
	}

	var total int64
	if err := r.db.Querier(ctx).QueryRow(ctx, `SELECT count(*) FROM domain_snipes`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("snipe: count: %w", err)
	}

	args = append(args, p.Limit(), p.Offset())
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+snipeCols+` FROM domain_snipes`+where+
			` ORDER BY created_at DESC, id DESC LIMIT $`+strconv.Itoa(len(args)-1)+
			` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("snipe: list: %w", err)
	}
	out, err := collectSnipes(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// CountOpenByClient counts the client's pending_payment + active snipes.
func (r *Repo) CountOpenByClient(ctx context.Context, clientID int64) (int, error) {
	var n int
	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT count(*) FROM domain_snipes
		WHERE client_id = $1 AND status IN ('pending_payment', 'active')`, clientID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("snipe: count open for client %d: %w", clientID, err)
	}
	return n, nil
}

// CountByStatus tallies snipes per status.
func (r *Repo) CountByStatus(ctx context.Context) (map[domain.SnipeStatus]int, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `SELECT status, count(*) FROM domain_snipes GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("snipe: count by status: %w", err)
	}
	defer rows.Close()
	out := map[domain.SnipeStatus]int{}
	for rows.Next() {
		var (
			st domain.SnipeStatus
			n  int
		)
		if err := rows.Scan(&st, &n); err != nil {
			return nil, fmt.Errorf("snipe: scan status count: %w", err)
		}
		out[st] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("snipe: status count rows: %w", err)
	}
	return out, nil
}

// CountLeased counts active snipes whose lease is still live at now.
func (r *Repo) CountLeased(ctx context.Context, now time.Time) (int, error) {
	var n int
	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT count(*) FROM domain_snipes
		WHERE status = 'active' AND lease_until IS NOT NULL AND lease_until >= $1`, now).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("snipe: count leased: %w", err)
	}
	return n, nil
}

// Claim leases up to limit active snipes whose lease is free or expired. SKIP
// LOCKED lets two workers claim concurrently without blocking or overlapping.
func (r *Repo) Claim(ctx context.Context, owner string, limit int, now, leaseUntil time.Time) ([]domain.DomainSnipe, error) {
	if limit < 1 {
		return nil, nil
	}
	rows, err := r.db.Querier(ctx).Query(ctx, `
		UPDATE domain_snipes SET lease_owner = $1, lease_until = $2
		WHERE id IN (
			SELECT id FROM domain_snipes
			WHERE status = 'active' AND (lease_until IS NULL OR lease_until < $3)
			ORDER BY id LIMIT $4
			FOR UPDATE SKIP LOCKED)
		RETURNING `+snipeCols, owner, leaseUntil, now, limit)
	if err != nil {
		return nil, fmt.Errorf("snipe: claim: %w", err)
	}
	return collectSnipes(rows)
}

// Heartbeat renews the owner's lease and flushes progress counters. It reports
// false when the row is no longer active or no longer leased to owner.
func (r *Repo) Heartbeat(ctx context.Context, h ports.SnipeHeartbeat) (bool, error) {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE domain_snipes
		SET lease_until = $3, attempts = $4, phase = $5,
		    last_attempt_at = COALESCE($6, last_attempt_at), last_error = $7
		WHERE id = $1 AND lease_owner = $2 AND status = 'active'`,
		h.ID, h.Owner, h.LeaseUntil, h.Attempts, h.Phase, h.LastAttemptAt, h.LastError)
	if err != nil {
		return false, fmt.Errorf("snipe: heartbeat %d: %w", h.ID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Release frees owner's lease on id (a no-op once someone else holds it or the
// snipe finished).
func (r *Repo) Release(ctx context.Context, id int64, owner string) error {
	if _, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE domain_snipes SET lease_owner = NULL, lease_until = NULL
		WHERE id = $1 AND lease_owner = $2`, id, owner); err != nil {
		return fmt.Errorf("snipe: release %d: %w", id, err)
	}
	return nil
}

// SetCustomerID caches the resolved registrar customer id on the row.
func (r *Repo) SetCustomerID(ctx context.Context, id int64, customerID int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE domain_snipes SET registrar_customer_id = $2 WHERE id = $1`, id, customerID)
	if err != nil {
		return fmt.Errorf("snipe: set customer %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("snipe")
	}
	return nil
}

// Finish applies a guarded status transition. The WHERE clause makes the move
// a compare-and-swap: only one of two racing writers (the hunt, an API cancel,
// the expiry sweep) can win, and the loser gets false with nothing changed.
func (r *Repo) Finish(ctx context.Context, f ports.SnipeFinish) (bool, error) {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE domain_snipes SET
			status = $3,
			refund_amount = $4,
			last_error = CASE WHEN $5 <> '' THEN $5 ELSE last_error END,
			won_at = CASE WHEN $3 = 'won' THEN $6 ELSE won_at END,
			won_domain_id = COALESCE($7, won_domain_id),
			attempts = GREATEST(attempts, $10),
			lease_owner = NULL, lease_until = NULL
		WHERE id = $1 AND status = $2
		  AND ($8 = '' OR lease_owner = $8)
		  AND (NOT $9 OR lease_until IS NULL OR lease_until < $6)`,
		f.ID, f.From, f.To, f.RefundAmount, f.LastError, f.Now, f.WonDomainID, f.Owner, f.RequireIdle, f.Attempts)
	if err != nil {
		return false, fmt.Errorf("snipe: finish %d: %w", f.ID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkRefunded stamps refunded_at on a finished snipe.
func (r *Repo) MarkRefunded(ctx context.Context, id int64, at time.Time) error {
	if _, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE domain_snipes SET refunded_at = $2 WHERE id = $1`, id, at); err != nil {
		return fmt.Errorf("snipe: mark refunded %d: %w", id, err)
	}
	return nil
}

// RecordRefund stores a credit-back decided after the snipe ended.
func (r *Repo) RecordRefund(ctx context.Context, id int64, amount int64, at time.Time) (bool, error) {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE domain_snipes SET refund_amount = $2, refunded_at = $3
		WHERE id = $1 AND refund_amount = 0 AND refunded_at IS NULL
		  AND status IN ('cancelled', 'failed', 'expired')`, id, amount, at)
	if err != nil {
		return false, fmt.Errorf("snipe: record refund %d: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetInvoice links the invoice and freezes the refundable split of its total.
func (r *Repo) SetInvoice(ctx context.Context, id, invoiceID, refundableDomain, refundableFee int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE domain_snipes SET invoice_id = $2, refundable_domain = $3, refundable_fee = $4
		WHERE id = $1`, id, invoiceID, refundableDomain, refundableFee)
	if err != nil {
		return fmt.Errorf("snipe: set invoice %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("snipe")
	}
	return nil
}

// Activate moves a paid snipe pending_payment -> active. The hunting deadline
// restarts at the payment while keeping the window length it was created with
// (deadline_at - created_at), so a client who pays late still gets the full
// plan duration.
func (r *Repo) Activate(ctx context.Context, id int64, paidAt time.Time) (bool, error) {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE domain_snipes
		SET status = 'active', paid_at = $2::timestamptz,
		    deadline_at = $2::timestamptz + COALESCE(deadline_at - created_at, interval '30 days')
		WHERE id = $1 AND status = 'pending_payment'`, id, paidAt)
	if err != nil {
		return false, fmt.Errorf("snipe: activate %d: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListStalePending returns pending_payment snipes whose invoice was cancelled
// or has been overdue for a full day (the invoice due date is a DATE).
func (r *Repo) ListStalePending(ctx context.Context, now time.Time, limit int) ([]domain.DomainSnipe, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT `+snipeCols+` FROM domain_snipes WHERE id IN (
			SELECT s.id FROM domain_snipes s JOIN invoices i ON i.id = s.invoice_id
			WHERE s.status = 'pending_payment'
			  AND (i.status = 'cancelled'
			       OR (i.status IN ('unpaid', 'overdue') AND i.due_date < $1::date))
			ORDER BY s.id LIMIT $2)
		ORDER BY id`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("snipe: list stale pending: %w", err)
	}
	return collectSnipes(rows)
}

// ListExpirable returns active snipes past their deadline whose lease is idle
// (a live hunt expires itself).
func (r *Repo) ListExpirable(ctx context.Context, now time.Time, limit int) ([]domain.DomainSnipe, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT `+snipeCols+` FROM domain_snipes
		WHERE status = 'active' AND deadline_at IS NOT NULL AND deadline_at <= $1
		  AND (lease_until IS NULL OR lease_until < $1)
		ORDER BY deadline_at, id LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("snipe: list expirable: %w", err)
	}
	return collectSnipes(rows)
}

// AddEvent appends one entry to a snipe's activity trail.
func (r *Repo) AddEvent(ctx context.Context, e *domain.SnipeEvent) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO domain_snipe_events (snipe_id, kind, message) VALUES ($1,$2,$3)
		RETURNING id, created_at`, e.SnipeID, e.Kind, e.Message).Scan(&e.ID, &e.CreatedAt)
	if err != nil {
		return fmt.Errorf("snipe: add event for %d: %w", e.SnipeID, err)
	}
	return nil
}

// ListEvents returns the newest events of a snipe first.
func (r *Repo) ListEvents(ctx context.Context, snipeID int64, limit int) ([]domain.SnipeEvent, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT id, snipe_id, kind, message, created_at FROM domain_snipe_events
		WHERE snipe_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, snipeID, limit)
	if err != nil {
		return nil, fmt.Errorf("snipe: list events of %d: %w", snipeID, err)
	}
	defer rows.Close()
	var out []domain.SnipeEvent
	for rows.Next() {
		var e domain.SnipeEvent
		if err := rows.Scan(&e.ID, &e.SnipeID, &e.Kind, &e.Message, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("snipe: scan event: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("snipe: event rows: %w", err)
	}
	return out, nil
}

// PlanRepo is the pgx ports.SnipePlanRepo (a separate type from Repo: both
// ports have a Create/GetByID/List with different signatures).
type PlanRepo struct {
	db *db.DB
}

// NewPlanRepo builds the snipe plan repo.
func NewPlanRepo(d *db.DB) *PlanRepo { return &PlanRepo{db: d} }

const planCols = `id, name, window_days, fee, success_fee, active, sort, created_at, updated_at`

func scanPlan(row rowScanner) (*domain.SnipePlan, error) {
	var p domain.SnipePlan
	if err := row.Scan(&p.ID, &p.Name, &p.WindowDays, &p.Fee, &p.SuccessFee, &p.Active, &p.Sort,
		&p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// Create inserts a plan.
func (r *PlanRepo) Create(ctx context.Context, p *domain.SnipePlan) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO snipe_plans (name, window_days, fee, success_fee, active, sort)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at, updated_at`,
		p.Name, p.WindowDays, p.Fee, p.SuccessFee, p.Active, p.Sort).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("snipe: create plan: %w", err)
	}
	return nil
}

// GetByID fetches one plan.
func (r *PlanRepo) GetByID(ctx context.Context, id int64) (*domain.SnipePlan, error) {
	p, err := scanPlan(r.db.Querier(ctx).QueryRow(ctx, `SELECT `+planCols+` FROM snipe_plans WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("snipe plan")
	}
	if err != nil {
		return nil, fmt.Errorf("snipe: get plan %d: %w", id, err)
	}
	return p, nil
}

// Update replaces a plan's editable fields.
func (r *PlanRepo) Update(ctx context.Context, p *domain.SnipePlan) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		UPDATE snipe_plans SET name = $2, window_days = $3, fee = $4, success_fee = $5, active = $6, sort = $7
		WHERE id = $1 RETURNING updated_at`,
		p.ID, p.Name, p.WindowDays, p.Fee, p.SuccessFee, p.Active, p.Sort).Scan(&p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("snipe plan")
	}
	if err != nil {
		return fmt.Errorf("snipe: update plan %d: %w", p.ID, err)
	}
	return nil
}

// List returns plans ordered by sort then id.
func (r *PlanRepo) List(ctx context.Context, activeOnly bool) ([]domain.SnipePlan, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT `+planCols+` FROM snipe_plans WHERE ($1 = false OR active) ORDER BY sort, id`, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("snipe: list plans: %w", err)
	}
	defer rows.Close()
	var out []domain.SnipePlan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("snipe: scan plan: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("snipe: plan rows: %w", err)
	}
	return out, nil
}
