package provisioning

import (
	"context"
	"errors"
	"fmt"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var _ ports.CancellationRequestRepo = (cancellationRepoView{})

// isUniqueViolation reports whether err is a unique-constraint violation
// (23505) - used to translate a race on the pending-request partial unique
// index into a normal Conflict.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

const cancellationRequestCols = `id, service_id, client_id, mode, reason, status,
	requested_at, decided_at, decided_by, created_at, updated_at`

func scanCancellationRequest(row pgx.Row) (*domain.CancellationRequest, error) {
	var cr domain.CancellationRequest
	err := row.Scan(&cr.ID, &cr.ServiceID, &cr.ClientID, &cr.Mode, &cr.Reason, &cr.Status,
		&cr.RequestedAt, &cr.DecidedAt, &cr.DecidedBy, &cr.CreatedAt, &cr.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &cr, nil
}

// CreateCancellationRequest inserts a cancellation request row. Immediate-mode
// callers pass Status/DecidedAt/DecidedBy already set (auto-processed);
// end_of_term callers leave them at their pending zero values. A race against
// another pending request for the same service surfaces as apperr.Conflict
// via the partial unique index (the service layer already checks
// GetPendingByService first - this is the backstop).
func (r *Repo) CreateCancellationRequest(ctx context.Context, cr *domain.CancellationRequest) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO service_cancellation_requests (service_id, client_id, mode, reason, status, decided_at, decided_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, requested_at, created_at, updated_at`,
		cr.ServiceID, cr.ClientID, cr.Mode, cr.Reason, cr.Status, cr.DecidedAt, cr.DecidedBy,
	).Scan(&cr.ID, &cr.RequestedAt, &cr.CreatedAt, &cr.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return apperr.Conflict("a cancellation request is already pending for this service")
		}
		return fmt.Errorf("provisioning: create cancellation request: %w", err)
	}
	return nil
}

// GetCancellationRequestByID fetches one cancellation request.
func (r *Repo) GetCancellationRequestByID(ctx context.Context, id int64) (*domain.CancellationRequest, error) {
	cr, err := scanCancellationRequest(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+cancellationRequestCols+` FROM service_cancellation_requests WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("cancellation request")
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning: get cancellation request %d: %w", id, err)
	}
	return cr, nil
}

// GetPendingCancellationRequestByService returns the service's pending
// request, or nil (no error) when none exists.
func (r *Repo) GetPendingCancellationRequestByService(ctx context.Context, serviceID int64) (*domain.CancellationRequest, error) {
	cr, err := scanCancellationRequest(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+cancellationRequestCols+` FROM service_cancellation_requests
		 WHERE service_id = $1 AND status = 'pending'`, serviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning: get pending cancellation request for service %d: %w", serviceID, err)
	}
	return cr, nil
}

// UpdateCancellationRequest persists the request's decision fields
// (status/decided_at/decided_by - mode/reason/service/client are immutable
// after creation).
func (r *Repo) UpdateCancellationRequest(ctx context.Context, cr *domain.CancellationRequest) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE service_cancellation_requests
		SET status = $2, decided_at = $3, decided_by = $4
		WHERE id = $1`,
		cr.ID, cr.Status, cr.DecidedAt, cr.DecidedBy)
	if err != nil {
		return fmt.Errorf("provisioning: update cancellation request %d: %w", cr.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("cancellation request")
	}
	return nil
}

// ListCancellationRequests returns requests filtered by status/service_id
// (both optional, via ports.ListParams.Status/ServiceID) with pagination,
// newest request first.
func (r *Repo) ListCancellationRequests(ctx context.Context, p ports.ListParams) ([]domain.CancellationRequest, int64, error) {
	where := ` WHERE ($1 = '' OR status = $1) AND ($2 = 0 OR service_id = $2)`
	args := []any{p.Status, p.ServiceID}

	var total int64
	if err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM service_cancellation_requests`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("provisioning: count cancellation requests: %w", err)
	}

	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+cancellationRequestCols+` FROM service_cancellation_requests`+where+
			` ORDER BY requested_at DESC LIMIT $3 OFFSET $4`,
		append(args, p.Limit(), p.Offset())...)
	if err != nil {
		return nil, 0, fmt.Errorf("provisioning: list cancellation requests: %w", err)
	}
	defer rows.Close()

	var out []domain.CancellationRequest
	for rows.Next() {
		cr, err := scanCancellationRequest(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("provisioning: scan cancellation request: %w", err)
		}
		out = append(out, *cr)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("provisioning: cancellation requests rows: %w", err)
	}
	return out, total, nil
}

// CancellationRequests exposes *Repo as ports.CancellationRequestRepo through
// the cancellationRepoView adapter (its Create/GetByID/Update/List names
// collide with ports.ServiceRepo's, same reason Servers() wraps serverRepoView).
func (r *Repo) CancellationRequests() ports.CancellationRequestRepo { return cancellationRepoView{r} }

// cancellationRepoView adapts *Repo to ports.CancellationRequestRepo.
type cancellationRepoView struct{ r *Repo }

func (v cancellationRepoView) Create(ctx context.Context, cr *domain.CancellationRequest) error {
	return v.r.CreateCancellationRequest(ctx, cr)
}

func (v cancellationRepoView) GetByID(ctx context.Context, id int64) (*domain.CancellationRequest, error) {
	return v.r.GetCancellationRequestByID(ctx, id)
}

func (v cancellationRepoView) GetPendingByService(ctx context.Context, serviceID int64) (*domain.CancellationRequest, error) {
	return v.r.GetPendingCancellationRequestByService(ctx, serviceID)
}

func (v cancellationRepoView) Update(ctx context.Context, cr *domain.CancellationRequest) error {
	return v.r.UpdateCancellationRequest(ctx, cr)
}

func (v cancellationRepoView) List(ctx context.Context, p ports.ListParams) ([]domain.CancellationRequest, int64, error) {
	return v.r.ListCancellationRequests(ctx, p)
}
