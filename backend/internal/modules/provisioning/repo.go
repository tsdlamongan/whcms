package provisioning

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

// isFKViolation reports whether err is a foreign-key violation (23503).
func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// Repo is the pgx implementation of ports.ServiceRepo, ports.ServerRepo and
// the module-local ServiceStore extras (automation queries). Every query goes
// through db.Querier(ctx) so it joins any transaction started by
// TxManager.WithinTx.
type Repo struct {
	db *db.DB
}

// NewRepo builds the provisioning Repo.
func NewRepo(d *db.DB) *Repo { return &Repo{db: d} }

// Compile-time interface checks. *Repo implements ports.ServiceRepo directly;
// ports.ServerRepo is exposed through the Servers() view because the two
// interfaces share method names with different signatures.
var (
	_ ports.ServiceRepo = (*Repo)(nil)
	_ ports.ServerRepo  = (serverRepoView{})
	_ ServiceStore      = (*Repo)(nil)
	_ ServerStore       = (*Repo)(nil)
)

// Services (ports.ServiceRepo)

const serviceCols = `id, client_id, order_item_id, product_id, server_id, domain, username,
	password_enc, status, billing_cycle, recurring_amount, setup_fee, next_due_date,
	registration_date, terminated_at, suspend_reason, panel_meta, coupon_id,
	pending_upgrade, notes, created_at, updated_at`

func scanService(row pgx.Row) (*domain.Service, error) {
	var s domain.Service
	err := row.Scan(&s.ID, &s.ClientID, &s.OrderItemID, &s.ProductID, &s.ServerID,
		&s.Domain, &s.Username, &s.PasswordEnc, &s.Status, &s.BillingCycle,
		&s.RecurringAmount, &s.SetupFee, &s.NextDueDate, &s.RegistrationDate,
		&s.TerminatedAt, &s.SuspendReason, &s.PanelMeta, &s.CouponID,
		&s.PendingUpgrade, &s.Notes, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repo) queryServices(ctx context.Context, query string, args ...any) ([]domain.Service, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("provisioning: services query: %w", err)
	}
	defer rows.Close()

	var out []domain.Service
	for rows.Next() {
		s, err := scanService(rows)
		if err != nil {
			return nil, fmt.Errorf("provisioning: scan service: %w", err)
		}
		out = append(out, *s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("provisioning: services rows: %w", err)
	}
	return out, nil
}

// Create inserts a service row.
func (r *Repo) Create(ctx context.Context, s *domain.Service) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO services (client_id, order_item_id, product_id, server_id, domain,
			username, password_enc, status, billing_cycle, recurring_amount, setup_fee,
			next_due_date, registration_date, terminated_at, suspend_reason, panel_meta,
			coupon_id, pending_upgrade, notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,COALESCE($16,'{}'::jsonb),$17,$18,$19)
		RETURNING id, created_at, updated_at`,
		s.ClientID, s.OrderItemID, s.ProductID, s.ServerID, s.Domain,
		s.Username, s.PasswordEnc, s.Status, s.BillingCycle, s.RecurringAmount, s.SetupFee,
		s.NextDueDate, s.RegistrationDate, s.TerminatedAt, s.SuspendReason, s.PanelMeta,
		s.CouponID, s.PendingUpgrade, s.Notes,
	).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("provisioning: create service: %w", err)
	}
	return nil
}

// GetByID fetches one service.
func (r *Repo) GetByID(ctx context.Context, id int64) (*domain.Service, error) {
	s, err := scanService(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+serviceCols+` FROM services WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("service")
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning: get service %d: %w", id, err)
	}
	return s, nil
}

// GetByIDForUpdate row-locks and returns the service; call inside WithinTx.
func (r *Repo) GetByIDForUpdate(ctx context.Context, id int64) (*domain.Service, error) {
	s, err := scanService(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+serviceCols+` FROM services WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("service")
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning: get service for update %d: %w", id, err)
	}
	return s, nil
}

// GetByIDs returns the services matching ids (order not guaranteed); ids not
// found are simply omitted.
func (r *Repo) GetByIDs(ctx context.Context, ids []int64) ([]domain.Service, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return r.queryServices(ctx,
		`SELECT `+serviceCols+` FROM services WHERE id = ANY($1) ORDER BY id`, ids)
}

// Update persists every mutable service field.
func (r *Repo) Update(ctx context.Context, s *domain.Service) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE services SET client_id=$2, order_item_id=$3, product_id=$4, server_id=$5,
			domain=$6, username=$7, password_enc=$8, status=$9, billing_cycle=$10,
			recurring_amount=$11, setup_fee=$12, next_due_date=$13, registration_date=$14,
			terminated_at=$15, suspend_reason=$16, panel_meta=COALESCE($17,'{}'::jsonb),
			coupon_id=$18, pending_upgrade=$19, notes=$20
		WHERE id = $1`,
		s.ID, s.ClientID, s.OrderItemID, s.ProductID, s.ServerID,
		s.Domain, s.Username, s.PasswordEnc, s.Status, s.BillingCycle,
		s.RecurringAmount, s.SetupFee, s.NextDueDate, s.RegistrationDate,
		s.TerminatedAt, s.SuspendReason, s.PanelMeta,
		s.CouponID, s.PendingUpgrade, s.Notes)
	if err != nil {
		return fmt.Errorf("provisioning: update service %d: %w", s.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("service")
	}
	return nil
}

// UpdateStatus sets the service status only.
func (r *Repo) UpdateStatus(ctx context.Context, id int64, status domain.ServiceStatus) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE services SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("provisioning: update service status %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("service")
	}
	return nil
}

// List returns services filtered by status/search/client/product/server
// (ListParams.ClientID/ProductID/ServerID, 0 = unset) with pagination.
func (r *Repo) List(ctx context.Context, p ports.ListParams) ([]domain.Service, int64, error) {
	return r.listServices(ctx, p.ClientID, p)
}

// ListByClient returns one client's services.
func (r *Repo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Service, int64, error) {
	return r.listServices(ctx, clientID, p)
}

func (r *Repo) listServices(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Service, int64, error) {
	where := ` WHERE ($1 = 0 OR client_id = $1)
		AND ($2 = '' OR status = $2)
		AND ($3 = '' OR domain ILIKE '%' || $3 || '%' OR username ILIKE '%' || $3 || '%')
		AND ($4 = 0 OR product_id = $4)
		AND ($5 = 0 OR server_id = $5)`
	args := []any{clientID, p.Status, p.Search, p.ProductID, p.ServerID}

	var total int64
	if err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM services`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("provisioning: count services: %w", err)
	}

	list, err := r.queryServices(ctx,
		`SELECT `+serviceCols+` FROM services`+where+
			` ORDER BY id DESC LIMIT $6 OFFSET $7`,
		append(args, p.Limit(), p.Offset())...)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListRenewalsDue returns active services with next_due_date <= before.
func (r *Repo) ListRenewalsDue(ctx context.Context, before time.Time) ([]domain.Service, error) {
	return r.queryServices(ctx,
		`SELECT `+serviceCols+` FROM services
		 WHERE status = 'active' AND next_due_date IS NOT NULL AND next_due_date <= $1
		 ORDER BY next_due_date, id`, before)
}

// CountByServer counts the accounts occupying a server (pending/active/suspended).
func (r *Repo) CountByServer(ctx context.Context, serverID int64) (int64, error) {
	var n int64
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM services
		 WHERE server_id = $1 AND status IN ('pending','active','suspended')`,
		serverID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("provisioning: count by server %d: %w", serverID, err)
	}
	return n, nil
}

// CountByServerAndPackage counts non-terminal services on a server whose
// panel_meta.package_name matches name, excluding excludeServiceID.
func (r *Repo) CountByServerAndPackage(ctx context.Context, serverID int64, name string, excludeServiceID int64) (int64, error) {
	var n int64
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM services
		 WHERE server_id = $1 AND panel_meta->>'package_name' = $2
		   AND id <> $3 AND status NOT IN ('terminated','cancelled')`,
		serverID, name, excludeServiceID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("provisioning: count by server %d and package %s: %w", serverID, name, err)
	}
	return n, nil
}

// ListOverdueSuspendable returns active services having an overdue renewal
// invoice with due_date <= before (cron:auto_suspend input).
func (r *Repo) ListOverdueSuspendable(ctx context.Context, before time.Time) ([]domain.Service, error) {
	return r.queryServices(ctx, `
		SELECT DISTINCT ON (s.id) `+prefixCols("s.", serviceCols)+`
		FROM services s
		JOIN invoice_items ii ON ii.related_type = 'service_renewal' AND ii.related_id = s.id
		JOIN invoices i ON i.id = ii.invoice_id AND i.status = 'overdue' AND i.due_date <= $1
		WHERE s.status = 'active'
		ORDER BY s.id`, before)
}

// ListTerminatable returns services due for cron:auto_terminate: suspended
// since before suspendedBefore, or flagged cancel_at_period_end with
// next_due_date <= dueBefore.
func (r *Repo) ListTerminatable(ctx context.Context, suspendedBefore, dueBefore time.Time) ([]domain.Service, error) {
	return r.queryServices(ctx, `
		SELECT `+serviceCols+` FROM services
		WHERE (status = 'suspended'
			AND COALESCE((panel_meta->>'suspended_at')::timestamptz, updated_at) <= $1)
		   OR (status IN ('active','suspended')
			AND COALESCE((panel_meta->>'cancel_at_period_end')::boolean, false)
			AND next_due_date IS NOT NULL AND next_due_date <= $2)
		ORDER BY id`, suspendedBefore, dueBefore)
}

// prefixCols prefixes every column in a comma-separated list (helper for
// joined queries).
func prefixCols(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// Servers + groups (ports.ServerRepo)

const serverCols = `id, group_id, name, module, hostname, port, username, password_enc,
	api_token_enc, use_ssl, nameserver1, nameserver2, nameserver3, nameserver4,
	max_accounts, package_prefix, ip_address, active, created_at, updated_at`

func scanServer(row pgx.Row) (*domain.Server, error) {
	var s domain.Server
	err := row.Scan(&s.ID, &s.GroupID, &s.Name, &s.Module, &s.Hostname, &s.Port,
		&s.Username, &s.PasswordEnc, &s.APITokenEnc, &s.UseSSL,
		&s.Nameserver1, &s.Nameserver2, &s.Nameserver3, &s.Nameserver4,
		&s.MaxAccounts, &s.PackagePrefix, &s.IPAddress, &s.Active, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateServer inserts a server row. (Named CreateServer on the local
// ServerStore; ports.ServerRepo.Create is satisfied below.)
func (r *Repo) createServer(ctx context.Context, s *domain.Server) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO servers (group_id, name, module, hostname, port, username,
			password_enc, api_token_enc, use_ssl, nameserver1, nameserver2,
			nameserver3, nameserver4, max_accounts, package_prefix, ip_address, active)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING id, created_at, updated_at`,
		s.GroupID, s.Name, s.Module, s.Hostname, s.Port, s.Username,
		s.PasswordEnc, s.APITokenEnc, s.UseSSL, s.Nameserver1, s.Nameserver2,
		s.Nameserver3, s.Nameserver4, s.MaxAccounts, s.PackagePrefix, s.IPAddress, s.Active,
	).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("provisioning: create server: %w", err)
	}
	return nil
}

// CreateServer implements the server half of ports.ServerRepo.Create.
//
// NOTE: ports.ServerRepo declares Create(*domain.Server) but this repo also
// implements ports.ServiceRepo whose Create takes *domain.Service - Go cannot
// overload, so *Repo satisfies ports.ServiceRepo directly and ports.ServerRepo
// through the serverRepoView adapter returned by Servers().
func (r *Repo) CreateServer(ctx context.Context, s *domain.Server) error {
	return r.createServer(ctx, s)
}

// GetServerByID fetches one server.
func (r *Repo) GetServerByID(ctx context.Context, id int64) (*domain.Server, error) {
	s, err := scanServer(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+serverCols+` FROM servers WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("server")
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning: get server %d: %w", id, err)
	}
	return s, nil
}

// UpdateServer persists every mutable server field.
func (r *Repo) UpdateServer(ctx context.Context, s *domain.Server) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE servers SET group_id=$2, name=$3, module=$4, hostname=$5, port=$6,
			username=$7, password_enc=$8, api_token_enc=$9, use_ssl=$10,
			nameserver1=$11, nameserver2=$12, nameserver3=$13, nameserver4=$14,
			max_accounts=$15, package_prefix=$16, ip_address=$17, active=$18
		WHERE id = $1`,
		s.ID, s.GroupID, s.Name, s.Module, s.Hostname, s.Port,
		s.Username, s.PasswordEnc, s.APITokenEnc, s.UseSSL,
		s.Nameserver1, s.Nameserver2, s.Nameserver3, s.Nameserver4,
		s.MaxAccounts, s.PackagePrefix, s.IPAddress, s.Active)
	if err != nil {
		return fmt.Errorf("provisioning: update server %d: %w", s.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("server")
	}
	return nil
}

// ListServers returns servers with optional search (name/hostname) and
// status filter ("active"/"inactive").
func (r *Repo) ListServers(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error) {
	where := ` WHERE ($1 = '' OR name ILIKE '%' || $1 || '%' OR hostname ILIKE '%' || $1 || '%')
		AND ($2 = '' OR ($2 = 'active' AND active) OR ($2 = 'inactive' AND NOT active))`
	args := []any{p.Search, p.Status}

	var total int64
	if err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM servers`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("provisioning: count servers: %w", err)
	}

	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+serverCols+` FROM servers`+where+` ORDER BY id LIMIT $3 OFFSET $4`,
		append(args, p.Limit(), p.Offset())...)
	if err != nil {
		return nil, 0, fmt.Errorf("provisioning: list servers: %w", err)
	}
	defer rows.Close()

	var out []domain.Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("provisioning: scan server: %w", err)
		}
		out = append(out, *s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("provisioning: servers rows: %w", err)
	}
	return out, total, nil
}

// DeleteServer removes a server row.
func (r *Repo) DeleteServer(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM servers WHERE id = $1`, id)
	if isFKViolation(err) {
		return apperr.Conflict("server is still referenced by services")
	}
	if err != nil {
		return fmt.Errorf("provisioning: delete server %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("server")
	}
	return nil
}

// CreateGroup inserts a server group.
func (r *Repo) CreateGroup(ctx context.Context, g *domain.ServerGroup) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO server_groups (name, strategy) VALUES ($1, $2)
		RETURNING id, created_at, updated_at`,
		g.Name, g.Strategy).Scan(&g.ID, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return fmt.Errorf("provisioning: create server group: %w", err)
	}
	return nil
}

// GetGroupByID fetches one server group.
func (r *Repo) GetGroupByID(ctx context.Context, id int64) (*domain.ServerGroup, error) {
	var g domain.ServerGroup
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT id, name, strategy, created_at, updated_at FROM server_groups WHERE id = $1`,
		id).Scan(&g.ID, &g.Name, &g.Strategy, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("server group")
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning: get server group %d: %w", id, err)
	}
	return &g, nil
}

// UpdateGroup persists a server group.
func (r *Repo) UpdateGroup(ctx context.Context, g *domain.ServerGroup) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE server_groups SET name = $2, strategy = $3 WHERE id = $1`,
		g.ID, g.Name, g.Strategy)
	if err != nil {
		return fmt.Errorf("provisioning: update server group %d: %w", g.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("server group")
	}
	return nil
}

// ListGroups returns all server groups.
func (r *Repo) ListGroups(ctx context.Context) ([]domain.ServerGroup, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT id, name, strategy, created_at, updated_at FROM server_groups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("provisioning: list server groups: %w", err)
	}
	defer rows.Close()

	var out []domain.ServerGroup
	for rows.Next() {
		var g domain.ServerGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Strategy, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("provisioning: scan server group: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("provisioning: server groups rows: %w", err)
	}
	return out, nil
}

// DeleteGroup removes a server group.
func (r *Repo) DeleteGroup(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM server_groups WHERE id = $1`, id)
	if isFKViolation(err) {
		return apperr.Conflict("server group is still referenced by servers or products")
	}
	if err != nil {
		return fmt.Errorf("provisioning: delete server group %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("server group")
	}
	return nil
}

// PickServer selects an active server in the group per the group strategy,
// capacity-aware (max_accounts = 0 means unlimited):
//   - least_used:   fewest occupied accounts first
//   - round_robin:  server whose latest assignment is oldest first
func (r *Repo) PickServer(ctx context.Context, groupID int64) (*domain.Server, error) {
	g, err := r.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}

	order := `c.last_assigned ASC NULLS FIRST, s.id ASC` // round_robin
	if g.Strategy == domain.StrategyLeastUsed {
		order = `c.cnt ASC, s.id ASC`
	}

	s, err := scanServer(r.db.Querier(ctx).QueryRow(ctx, `
		SELECT `+prefixCols("s.", serverCols)+`
		FROM servers s
		LEFT JOIN LATERAL (
			SELECT count(*) AS cnt, max(sv.created_at) AS last_assigned
			FROM services sv
			WHERE sv.server_id = s.id AND sv.status IN ('pending','active','suspended')
		) c ON TRUE
		WHERE s.group_id = $1 AND s.active
		  AND (s.max_accounts = 0 OR c.cnt < s.max_accounts)
		ORDER BY `+order+`
		LIMIT 1`, groupID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.Conflict("no active server with available capacity in group")
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning: pick server group %d: %w", groupID, err)
	}
	return s, nil
}

// ports.ServerRepo adapter

// serverRepoView adapts *Repo to ports.ServerRepo (whose Create/GetByID/...
// names collide with the ports.ServiceRepo methods implemented on *Repo).
type serverRepoView struct{ r *Repo }

// Servers returns the ports.ServerRepo view of this repo for wiring.
func (r *Repo) Servers() ports.ServerRepo { return serverRepoView{r} }

func (v serverRepoView) Create(ctx context.Context, s *domain.Server) error {
	return v.r.createServer(ctx, s)
}

func (v serverRepoView) GetByID(ctx context.Context, id int64) (*domain.Server, error) {
	return v.r.GetServerByID(ctx, id)
}

func (v serverRepoView) Update(ctx context.Context, s *domain.Server) error {
	return v.r.UpdateServer(ctx, s)
}

func (v serverRepoView) List(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error) {
	return v.r.ListServers(ctx, p)
}

func (v serverRepoView) Delete(ctx context.Context, id int64) error {
	return v.r.DeleteServer(ctx, id)
}

func (v serverRepoView) CreateGroup(ctx context.Context, g *domain.ServerGroup) error {
	return v.r.CreateGroup(ctx, g)
}

func (v serverRepoView) GetGroupByID(ctx context.Context, id int64) (*domain.ServerGroup, error) {
	return v.r.GetGroupByID(ctx, id)
}

func (v serverRepoView) UpdateGroup(ctx context.Context, g *domain.ServerGroup) error {
	return v.r.UpdateGroup(ctx, g)
}

func (v serverRepoView) ListGroups(ctx context.Context) ([]domain.ServerGroup, error) {
	return v.r.ListGroups(ctx)
}

func (v serverRepoView) DeleteGroup(ctx context.Context, id int64) error {
	return v.r.DeleteGroup(ctx, id)
}

func (v serverRepoView) PickServer(ctx context.Context, groupID int64) (*domain.Server, error) {
	return v.r.PickServer(ctx, groupID)
}
