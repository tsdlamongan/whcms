package adminops

import (
	"context"
	"fmt"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Repo runs the adminops aggregate SQL. It implements ports.DashboardRepo
// plus the module-local DashboardStore, LogStore and StaffLister interfaces.
// Every query goes through db.Querier(ctx) so it joins any transaction
// started by TxManager.WithinTx.
type Repo struct {
	db *db.DB
}

// NewRepo builds the adminops Repo.
func NewRepo(d *db.DB) *Repo { return &Repo{db: d} }

// Compile-time interface checks.
var (
	_ ports.DashboardRepo = (*Repo)(nil)
	_ DashboardStore      = (*Repo)(nil)
	_ LogStore            = (*Repo)(nil)
	_ StaffLister         = (*Repo)(nil)
)

// Dashboard aggregates (ports.DashboardRepo)

// Stats returns the admin dashboard KPI counters in one round trip. The
// "today"/"this month" boundaries are UTC midnights (CONTRACTS §3: internal
// time is UTC) regardless of the DB session timezone - plain
// date_trunc(..., now()) would truncate in the session TZ instead.
func (r *Repo) Stats(ctx context.Context) (*ports.DashboardStats, error) {
	var st ports.DashboardStats
	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM clients  WHERE status = 'active' AND deleted_at IS NULL),
			(SELECT count(*) FROM services WHERE status = 'active'),
			(SELECT count(*) FROM invoices WHERE status = 'unpaid'),
			(SELECT count(*) FROM invoices WHERE status = 'overdue'),
			(SELECT count(*) FROM tickets  WHERE status <> 'closed'),
			(SELECT count(*) FROM orders   WHERE status = 'pending'),
			(SELECT COALESCE(SUM(amount), 0) FROM transactions
				WHERE status = 'success'
				AND paid_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
			(SELECT COALESCE(SUM(amount), 0) FROM transactions
				WHERE status = 'success'
				AND paid_at >= date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
			(SELECT count(*) FROM domains  WHERE status = 'active'),
			(SELECT count(*) FROM services WHERE status = 'suspended')`,
	).Scan(&st.ClientsActive, &st.ServicesActive, &st.InvoicesUnpaid, &st.InvoicesOverdue,
		&st.TicketsOpen, &st.OrdersPending, &st.RevenueToday, &st.RevenueThisMonth,
		&st.DomainsActive, &st.ServicesSuspended)
	if err != nil {
		return nil, fmt.Errorf("adminops: stats: %w", err)
	}
	return &st, nil
}

// ExtraStats returns the dashboard KPIs beyond ports.DashboardStats. The
// "today" boundary is the UTC midnight regardless of the DB session
// timezone (see Stats).
func (r *Repo) ExtraStats(ctx context.Context) (*ExtraStats, error) {
	var ex ExtraStats
	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM orders
				WHERE created_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
			(SELECT COALESCE(SUM(total), 0) FROM invoices WHERE status = 'unpaid'),
			(SELECT COALESCE(SUM(total), 0) FROM invoices WHERE status = 'overdue'),
			(SELECT count(*) FROM services WHERE status = 'pending')`,
	).Scan(&ex.OrdersToday, &ex.UnpaidTotal, &ex.OverdueTotal, &ex.ServicesPending)
	if err != nil {
		return nil, fmt.Errorf("adminops: extra stats: %w", err)
	}
	return &ex, nil
}

// RecentOrders returns the newest orders (for the dashboard widget), newest
// first, joined to the client's display name.
func (r *Repo) RecentOrders(ctx context.Context, limit int) ([]RecentOrderRow, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT o.id, o.order_number, o.client_id,
		       COALESCE(NULLIF(trim(c.first_name || ' ' || c.last_name), ''), ''),
		       o.status, o.total, o.created_at
		FROM orders o
		LEFT JOIN clients c ON c.id = o.client_id
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("adminops: recent orders: %w", err)
	}
	defer rows.Close()

	var out []RecentOrderRow
	for rows.Next() {
		var row RecentOrderRow
		if err := rows.Scan(&row.ID, &row.OrderNumber, &row.ClientID, &row.ClientName,
			&row.Status, &row.Total, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("adminops: recent orders scan: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminops: recent orders rows: %w", err)
	}
	return out, nil
}

// RecentTickets returns the newest tickets (for the dashboard widget),
// newest first.
func (r *Repo) RecentTickets(ctx context.Context, limit int) ([]RecentTicketRow, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT id, ticket_number, client_id, subject, status, priority, last_reply_at, created_at
		FROM tickets
		ORDER BY created_at DESC, id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("adminops: recent tickets: %w", err)
	}
	defer rows.Close()

	var out []RecentTicketRow
	for rows.Next() {
		var row RecentTicketRow
		if err := rows.Scan(&row.ID, &row.TicketNumber, &row.ClientID, &row.Subject,
			&row.Status, &row.Priority, &row.LastReplyAt, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("adminops: recent tickets scan: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminops: recent tickets rows: %w", err)
	}
	return out, nil
}

// periodFormat maps a group_by value to a to_char format (whitelisted - the
// format string is passed as a bind parameter, never interpolated).
func periodFormat(groupBy string) (string, error) {
	switch groupBy {
	case "day":
		return "YYYY-MM-DD", nil
	case "month":
		return "YYYY-MM", nil
	}
	return "", apperr.Validation("group_by must be one of: day, month")
}

// Revenue returns successful-transaction sums grouped per day or month in
// [from, to). Periods are bucketed in UTC (AT TIME ZONE 'UTC') so the labels
// always agree with the UTC range bounds regardless of the DB session
// timezone (CONTRACTS §3: internal time is UTC; display TZ is a frontend
// concern).
func (r *Repo) Revenue(ctx context.Context, from, to time.Time, groupBy string) ([]ports.RevenuePoint, error) {
	format, err := periodFormat(groupBy)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT to_char(paid_at AT TIME ZONE 'UTC', $3) AS period,
		       COALESCE(SUM(amount), 0)::bigint,
		       count(*)::bigint
		FROM transactions
		WHERE status = 'success' AND paid_at >= $1 AND paid_at < $2
		GROUP BY period
		ORDER BY period`, from, to, format)
	if err != nil {
		return nil, fmt.Errorf("adminops: revenue: %w", err)
	}
	defer rows.Close()

	var out []ports.RevenuePoint
	for rows.Next() {
		var p ports.RevenuePoint
		if err := rows.Scan(&p.Period, &p.Amount, &p.Count); err != nil {
			return nil, fmt.Errorf("adminops: revenue scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminops: revenue rows: %w", err)
	}
	return out, nil
}

// RevenueByGateway returns the successful-transaction split per gateway in
// [from, to).
func (r *Repo) RevenueByGateway(ctx context.Context, from, to time.Time) ([]GatewayRevenue, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT gateway,
		       COALESCE(SUM(amount), 0)::bigint,
		       count(*)::bigint
		FROM transactions
		WHERE status = 'success' AND paid_at >= $1 AND paid_at < $2
		GROUP BY gateway
		ORDER BY gateway`, from, to)
	if err != nil {
		return nil, fmt.Errorf("adminops: revenue by gateway: %w", err)
	}
	defer rows.Close()

	var out []GatewayRevenue
	for rows.Next() {
		var g GatewayRevenue
		if err := rows.Scan(&g.Gateway, &g.Amount, &g.Count); err != nil {
			return nil, fmt.Errorf("adminops: revenue by gateway scan: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminops: revenue by gateway rows: %w", err)
	}
	return out, nil
}

// OrdersReport returns order totals/counts grouped per day in [from, to).
// Periods are bucketed in UTC to agree with the UTC range bounds (see Revenue).
func (r *Repo) OrdersReport(ctx context.Context, from, to time.Time) ([]ports.RevenuePoint, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS period,
		       COALESCE(SUM(total), 0)::bigint,
		       count(*)::bigint
		FROM orders
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY period
		ORDER BY period`, from, to)
	if err != nil {
		return nil, fmt.Errorf("adminops: orders report: %w", err)
	}
	defer rows.Close()

	var out []ports.RevenuePoint
	for rows.Next() {
		var p ports.RevenuePoint
		if err := rows.Scan(&p.Period, &p.Amount, &p.Count); err != nil {
			return nil, fmt.Errorf("adminops: orders report scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminops: orders report rows: %w", err)
	}
	return out, nil
}

// OrdersByStatus returns order counts and totals per status in [from, to),
// most numerous first (ties broken by status name for a stable order).
func (r *Repo) OrdersByStatus(ctx context.Context, from, to time.Time) ([]OrderStatusCount, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT status, count(*)::bigint, COALESCE(SUM(total), 0)::bigint
		FROM orders
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY status
		ORDER BY count(*) DESC, status`, from, to)
	if err != nil {
		return nil, fmt.Errorf("adminops: orders by status: %w", err)
	}
	defer rows.Close()

	var out []OrderStatusCount
	for rows.Next() {
		var s OrderStatusCount
		if err := rows.Scan(&s.Status, &s.Count, &s.Amount); err != nil {
			return nil, fmt.Errorf("adminops: orders by status scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminops: orders by status rows: %w", err)
	}
	return out, nil
}

// ServicesReport returns service counts grouped by status.
func (r *Repo) ServicesReport(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT status, count(*)::bigint FROM services GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("adminops: services report: %w", err)
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("adminops: services report scan: %w", err)
		}
		out[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminops: services report rows: %w", err)
	}
	return out, nil
}

// ServicesByProduct returns service counts grouped by product name + status.
func (r *Repo) ServicesByProduct(ctx context.Context) ([]ProductStatusCount, error) {
	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT p.name, s.status, count(*)::bigint
		FROM services s
		JOIN products p ON p.id = s.product_id
		GROUP BY p.name, s.status
		ORDER BY p.name, s.status`)
	if err != nil {
		return nil, fmt.Errorf("adminops: services by product: %w", err)
	}
	defer rows.Close()

	var out []ProductStatusCount
	for rows.Next() {
		var row ProductStatusCount
		if err := rows.Scan(&row.Product, &row.Status, &row.Count); err != nil {
			return nil, fmt.Errorf("adminops: services by product scan: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminops: services by product rows: %w", err)
	}
	return out, nil
}

// Staff listing

// ListStaff lists users with role=staff; Search matches email ILIKE, Status
// filters exact. Secrets (password hash, TOTP secret) are never selected.
func (r *Repo) ListStaff(ctx context.Context, p ports.ListParams) ([]domain.User, int64, error) {
	q := r.db.Querier(ctx)
	search := "%" + p.Search + "%"

	var total int64
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM users
		WHERE role = 'staff' AND ($1 = '%%' OR email ILIKE $1) AND ($2 = '' OR status = $2)`,
		search, p.Status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("adminops: staff count: %w", err)
	}
	rows, err := q.Query(ctx, `
		SELECT id, email, role, status, permissions, twofa_enabled, locale,
		       email_verified_at, last_login_at, created_at, updated_at
		FROM users
		WHERE role = 'staff' AND ($1 = '%%' OR email ILIKE $1) AND ($2 = '' OR status = $2)
		ORDER BY id
		LIMIT $3 OFFSET $4`, search, p.Status, p.Limit(), p.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("adminops: staff list: %w", err)
	}
	defer rows.Close()

	var out []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.Status, &u.Permissions,
			&u.TwoFAEnabled, &u.Locale, &u.EmailVerifiedAt, &u.LastLoginAt,
			&u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("adminops: staff scan: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("adminops: staff rows: %w", err)
	}
	return out, total, nil
}

// Log listing with filters

func pageLimit(perPage int) int {
	if perPage < 1 {
		return 25
	}
	if perPage > ports.MaxPerPage {
		return ports.MaxPerPage
	}
	return perPage
}

func pageOffset(page, perPage int) int {
	if page < 1 {
		page = 1
	}
	return (page - 1) * pageLimit(perPage)
}

// ListAudit lists audit rows newest first, filtered by actor/entity/action
// prefix/date range ([From, To) - the service converts inclusive dates).
func (r *Repo) ListAudit(ctx context.Context, f AuditLogFilter) ([]domain.AuditLog, int64, error) {
	q := r.db.Querier(ctx)
	const where = `
		($1::bigint IS NULL OR user_id = $1)
		AND ($2 = '' OR entity = $2)
		AND ($3 = '' OR action LIKE $3 || '%')
		AND ($4::timestamptz IS NULL OR created_at >= $4)
		AND ($5::timestamptz IS NULL OR created_at < $5)`

	var total int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE `+where,
		f.UserID, f.Entity, f.Action, f.From, f.To).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("adminops: audit count: %w", err)
	}
	rows, err := q.Query(ctx, `
		SELECT id, user_id, action, entity, entity_id, before, after, ip, created_at
		FROM audit_logs
		WHERE `+where+`
		ORDER BY id DESC
		LIMIT $6 OFFSET $7`,
		f.UserID, f.Entity, f.Action, f.From, f.To,
		pageLimit(f.PerPage), pageOffset(f.Page, f.PerPage))
	if err != nil {
		return nil, 0, fmt.Errorf("adminops: audit list: %w", err)
	}
	defer rows.Close()

	var out []domain.AuditLog
	for rows.Next() {
		var a domain.AuditLog
		if err := rows.Scan(&a.ID, &a.UserID, &a.Action, &a.Entity, &a.EntityID,
			&a.Before, &a.After, &a.IP, &a.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("adminops: audit scan: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("adminops: audit rows: %w", err)
	}
	return out, total, nil
}

// ListIntegration lists integration log rows newest first, filtered by
// provider and success.
func (r *Repo) ListIntegration(ctx context.Context, f IntegrationLogFilter) ([]domain.IntegrationLog, int64, error) {
	q := r.db.Querier(ctx)
	const where = `($1 = '' OR provider = $1) AND ($2::boolean IS NULL OR success = $2)`

	var total int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM integration_logs WHERE `+where,
		f.Provider, f.Success).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("adminops: integration count: %w", err)
	}
	rows, err := q.Query(ctx, `
		SELECT id, provider, endpoint, method, status_code, success, latency_ms,
		       request, response, error, created_at
		FROM integration_logs
		WHERE `+where+`
		ORDER BY id DESC
		LIMIT $3 OFFSET $4`,
		f.Provider, f.Success, pageLimit(f.PerPage), pageOffset(f.Page, f.PerPage))
	if err != nil {
		return nil, 0, fmt.Errorf("adminops: integration list: %w", err)
	}
	defer rows.Close()

	var out []domain.IntegrationLog
	for rows.Next() {
		var l domain.IntegrationLog
		if err := rows.Scan(&l.ID, &l.Provider, &l.Endpoint, &l.Method, &l.StatusCode,
			&l.Success, &l.LatencyMS, &l.Request, &l.Response, &l.Error, &l.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("adminops: integration scan: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("adminops: integration rows: %w", err)
	}
	return out, total, nil
}

// Housekeeping purges

// PurgeIntegrationLogsBefore deletes integration_logs older than before.
func (r *Repo) PurgeIntegrationLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`DELETE FROM integration_logs WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("adminops: purge integration_logs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PurgeEmailLogBefore deletes email_log rows older than before.
func (r *Repo) PurgeEmailLogBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`DELETE FROM email_log WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("adminops: purge email_log: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PurgeAuditLogsBefore deletes audit_logs older than before.
func (r *Repo) PurgeAuditLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`DELETE FROM audit_logs WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("adminops: purge audit_logs: %w", err)
	}
	return tag.RowsAffected(), nil
}
