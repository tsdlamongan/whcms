//go:build integration

// Integration tests against the real local Postgres (whmcs DB, migrations
// applied). They create their OWN fixture rows with uuid-suffixed
// identifiers and clean up after themselves - never touching shared or
// seeded data. Run: go test -tags integration ./internal/modules/adminops/
package adminops_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/modules/adminops"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://root:postgres@localhost:5432/whmcs_e2e?sslmode=disable"
	}
	d, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Skipf("skipping integration test: cannot connect to postgres: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

// newClientFixture inserts a user+client pair and returns the client id.
func newClientFixture(t *testing.T, d *db.DB, tag string) int64 {
	t.Helper()
	ctx := context.Background()
	q := d.Querier(ctx)

	var userID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role) VALUES ($1, 'x', 'client')
		RETURNING id`, "adminops-it-"+tag+"@example.com").Scan(&userID))
	var clientID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO clients (user_id, first_name, last_name) VALUES ($1, 'IT', $2)
		RETURNING id`, userID, tag).Scan(&clientID))

	t.Cleanup(func() {
		_, _ = q.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
		_, _ = q.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})
	return clientID
}

func TestRepoStatsAndExtraStats(t *testing.T) {
	repo := adminops.NewRepo(testDB(t))
	ctx := context.Background()

	stats, err := repo.Stats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)
	assert.GreaterOrEqual(t, stats.ClientsActive, int64(0))
	assert.GreaterOrEqual(t, stats.RevenueToday, int64(0))

	extra, err := repo.ExtraStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, extra)
	assert.GreaterOrEqual(t, extra.OrdersToday, int64(0))
	assert.GreaterOrEqual(t, extra.UnpaidTotal, int64(0))
}

func TestRepoRevenueAndGatewaySplit(t *testing.T) {
	d := testDB(t)
	repo := adminops.NewRepo(d)
	ctx := context.Background()
	q := d.Querier(ctx)
	tag := uuid.NewString()[:8]
	clientID := newClientFixture(t, d, tag)

	// Two paid invoices in an isolated far-past window (May 2001).
	paidAt := time.Date(2001, 5, 15, 12, 0, 0, 0, time.UTC)
	var invoiceIDs []int64
	for i, amount := range []int64{100000, 50000} {
		var invID int64
		require.NoError(t, q.QueryRow(ctx, `
			INSERT INTO invoices (invoice_number, client_id, status, subtotal, total, due_date, paid_at)
			VALUES ($1, $2, 'paid', $3, $3, '2001-05-10', $4)
			RETURNING id`,
			"IT-"+tag+"-"+string(rune('A'+i)), clientID, amount, paidAt).Scan(&invID))
		invoiceIDs = append(invoiceIDs, invID)
	}
	gateways := []string{"duitku", "credit"}
	for i, invID := range invoiceIDs {
		_, err := q.Exec(ctx, `
			INSERT INTO transactions (invoice_id, gateway, amount, status, paid_at)
			VALUES ($1, $2, $3, 'success', $4)`,
			invID, gateways[i], []int64{100000, 50000}[i], paidAt)
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		for _, invID := range invoiceIDs {
			_, _ = q.Exec(ctx, `DELETE FROM transactions WHERE invoice_id = $1`, invID)
			_, _ = q.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, invID)
		}
	})

	from := time.Date(2001, 5, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2001, 6, 1, 0, 0, 0, 0, time.UTC)

	points, err := repo.Revenue(ctx, from, to, "day")
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.Equal(t, "2001-05-15", points[0].Period)
	assert.Equal(t, int64(150000), points[0].Amount)
	assert.Equal(t, int64(2), points[0].Count)

	monthly, err := repo.Revenue(ctx, from, to, "month")
	require.NoError(t, err)
	require.Len(t, monthly, 1)
	assert.Equal(t, "2001-05", monthly[0].Period)

	_, err = repo.Revenue(ctx, from, to, "'; DROP TABLE users; --")
	require.Error(t, err, "group_by must be whitelisted")

	split, err := repo.RevenueByGateway(ctx, from, to)
	require.NoError(t, err)
	require.Len(t, split, 2)
	byGw := map[string]int64{}
	for _, g := range split {
		byGw[g.Gateway] = g.Amount
	}
	assert.Equal(t, int64(50000), byGw["credit"])
	assert.Equal(t, int64(100000), byGw["duitku"])
}

// testDBWithSessionTZ connects with a forced session timezone (pgx passes
// unknown URL query params as server runtime parameters).
func testDBWithSessionTZ(t *testing.T, tz string) *db.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://root:postgres@localhost:5432/whmcs_e2e?sslmode=disable"
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	d, err := db.Connect(context.Background(), url+sep+"TimeZone="+tz)
	if err != nil {
		t.Skipf("skipping integration test: cannot connect to postgres: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

// Regression: report periods must be bucketed in UTC - consistent with the
// UTC [from, to) bounds - regardless of the DB session timezone. With a
// non-UTC session TZ, plain to_char(paid_at, ...) would label a 20:00 UTC
// payment with the NEXT day (outside the requested range at month edges).
func TestRepoReportsGroupInUTCRegardlessOfSessionTZ(t *testing.T) {
	d := testDBWithSessionTZ(t, "Asia/Jakarta")
	repo := adminops.NewRepo(d)
	ctx := context.Background()
	q := d.Querier(ctx)
	tag := uuid.NewString()[:8]
	clientID := newClientFixture(t, d, tag)

	// 2001-07-15 20:00 UTC == 2001-07-16 03:00 Asia/Jakarta.
	at := time.Date(2001, 7, 15, 20, 0, 0, 0, time.UTC)

	var invID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO invoices (invoice_number, client_id, status, subtotal, total, due_date, paid_at)
		VALUES ($1, $2, 'paid', 70000, 70000, '2001-07-10', $3)
		RETURNING id`, "IT-TZ-"+tag, clientID, at).Scan(&invID))
	_, err := q.Exec(ctx, `
		INSERT INTO transactions (invoice_id, gateway, amount, status, paid_at)
		VALUES ($1, 'duitku', 70000, 'success', $2)`, invID, at)
	require.NoError(t, err)

	var orderID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO orders (order_number, client_id, status, subtotal, total, created_at)
		VALUES ($1, $2, 'active', 70000, 70000, $3)
		RETURNING id`, "IT-TZ-"+tag, clientID, at).Scan(&orderID))

	t.Cleanup(func() {
		_, _ = q.Exec(ctx, `DELETE FROM transactions WHERE invoice_id = $1`, invID)
		_, _ = q.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, invID)
		_, _ = q.Exec(ctx, `DELETE FROM orders WHERE id = $1`, orderID)
	})

	from := time.Date(2001, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2001, 8, 1, 0, 0, 0, 0, time.UTC)

	points, err := repo.Revenue(ctx, from, to, "day")
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.Equal(t, "2001-07-15", points[0].Period, "revenue period must be the UTC day")
	assert.Equal(t, int64(70000), points[0].Amount)

	monthly, err := repo.Revenue(ctx, from, to, "month")
	require.NoError(t, err)
	require.Len(t, monthly, 1)
	assert.Equal(t, "2001-07", monthly[0].Period)

	orders, err := repo.OrdersReport(ctx, from, to)
	require.NoError(t, err)
	require.Len(t, orders, 1)
	assert.Equal(t, "2001-07-15", orders[0].Period, "orders period must be the UTC day")
}

// Regression: the dashboard "today" boundary must be the UTC midnight -
// consistent with CONTRACTS §3 (internal time is UTC) and with the report
// bucketing above - regardless of the DB session timezone. With a non-UTC
// session TZ, plain date_trunc('day', now()) shifts the boundary by the TZ
// offset (7h for Asia/Jakarta), so RevenueToday/OrdersToday would include
// or drop transactions near midnight.
//
// Stats/ExtraStats aggregate the whole (shared) database, so the assertion
// is delta-based: insert a fixture whose paid_at falls on exactly one side
// of the UTC/session-TZ boundary pair and check its contribution. Retries
// absorb concurrent writers (their contribution only shows when they commit
// between the two reads of one attempt).
func TestRepoStatsTodayBoundaryIsUTCRegardlessOfSessionTZ(t *testing.T) {
	d := testDBWithSessionTZ(t, "Asia/Jakarta")
	repo := adminops.NewRepo(d)
	ctx := context.Background()
	q := d.Querier(ctx)
	clientID := newClientFixture(t, d, uuid.NewString()[:8])

	const amount = int64(77777)
	nowUTC := time.Now().UTC()
	utcMidnight := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)

	// Jakarta = UTC+7. Its "today" boundary instant is utcMidnight-7h while
	// the UTC hour is < 17, and utcMidnight+17h once the UTC hour reaches 17.
	var at time.Time
	var wantRevenueDelta, wantOrdersDelta int64
	if nowUTC.Hour() < 17 {
		// Yesterday 20:00 UTC: inside Jakarta's current day (starts -7h),
		// but before the UTC midnight -> must NOT count.
		at = utcMidnight.Add(-4 * time.Hour)
		wantRevenueDelta, wantOrdersDelta = 0, 0
	} else {
		// Today 01:00 UTC: after the UTC midnight, but before Jakarta's
		// current day start (utcMidnight+17h) -> MUST count.
		at = utcMidnight.Add(1 * time.Hour)
		wantRevenueDelta, wantOrdersDelta = amount, 1
	}

	attempt := func(i int) (revDelta, ordDelta int64) {
		tag := uuid.NewString()[:8]
		before, err := repo.Stats(ctx)
		require.NoError(t, err)
		beforeExtra, err := repo.ExtraStats(ctx)
		require.NoError(t, err)

		var invID int64
		require.NoError(t, q.QueryRow(ctx, `
			INSERT INTO invoices (invoice_number, client_id, status, subtotal, total, due_date, paid_at)
			VALUES ($1, $2, 'paid', $3, $3, $4, $5)
			RETURNING id`, "IT-STATS-"+tag, clientID, amount, at.Format("2006-01-02"), at).Scan(&invID))
		_, err = q.Exec(ctx, `
			INSERT INTO transactions (invoice_id, gateway, amount, status, paid_at)
			VALUES ($1, 'duitku', $2, 'success', $3)`, invID, amount, at)
		require.NoError(t, err)
		var orderID int64
		require.NoError(t, q.QueryRow(ctx, `
			INSERT INTO orders (order_number, client_id, status, subtotal, total, created_at)
			VALUES ($1, $2, 'active', $3, $3, $4)
			RETURNING id`, "IT-STATS-"+tag, clientID, amount, at).Scan(&orderID))
		defer func() {
			_, _ = q.Exec(ctx, `DELETE FROM transactions WHERE invoice_id = $1`, invID)
			_, _ = q.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, invID)
			_, _ = q.Exec(ctx, `DELETE FROM orders WHERE id = $1`, orderID)
		}()

		after, err := repo.Stats(ctx)
		require.NoError(t, err)
		afterExtra, err := repo.ExtraStats(ctx)
		require.NoError(t, err)
		return after.RevenueToday - before.RevenueToday, afterExtra.OrdersToday - beforeExtra.OrdersToday
	}

	ok := false
	var revDelta, ordDelta int64
	for i := 0; i < 3 && !ok; i++ {
		revDelta, ordDelta = attempt(i)
		ok = revDelta == wantRevenueDelta && ordDelta == wantOrdersDelta
	}
	assert.True(t, ok,
		"dashboard 'today' must be bucketed at the UTC midnight: got revenue delta %d (want %d), orders delta %d (want %d) for paid_at=%s",
		revDelta, wantRevenueDelta, ordDelta, wantOrdersDelta, at)
}

func TestRepoOrdersReport(t *testing.T) {
	d := testDB(t)
	repo := adminops.NewRepo(d)
	ctx := context.Background()
	q := d.Querier(ctx)
	tag := uuid.NewString()[:8]
	clientID := newClientFixture(t, d, tag)

	createdAt := time.Date(2001, 5, 20, 12, 0, 0, 0, time.UTC)
	var orderID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO orders (order_number, client_id, status, subtotal, total, created_at)
		VALUES ($1, $2, 'active', 120000, 120000, $3)
		RETURNING id`, "IT-ORD-"+tag, clientID, createdAt).Scan(&orderID))
	t.Cleanup(func() { _, _ = q.Exec(ctx, `DELETE FROM orders WHERE id = $1`, orderID) })

	points, err := repo.OrdersReport(ctx,
		time.Date(2001, 5, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2001, 6, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.Equal(t, "2001-05-20", points[0].Period)
	assert.Equal(t, int64(120000), points[0].Amount)
	assert.Equal(t, int64(1), points[0].Count)

	// A second order with a different status shows up in the breakdown.
	var pendingID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO orders (order_number, client_id, status, subtotal, total, created_at)
		VALUES ($1, $2, 'pending', 30000, 30000, $3)
		RETURNING id`, "IT-ORD2-"+tag, clientID, createdAt).Scan(&pendingID))
	t.Cleanup(func() { _, _ = q.Exec(ctx, `DELETE FROM orders WHERE id = $1`, pendingID) })

	byStatus, err := repo.OrdersByStatus(ctx,
		time.Date(2001, 5, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2001, 6, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Len(t, byStatus, 2)
	assert.Equal(t, adminops.OrderStatusCount{Status: "active", Count: 1, Amount: 120000}, byStatus[0],
		"ties on count break by status name: active before pending")
	assert.Equal(t, adminops.OrderStatusCount{Status: "pending", Count: 1, Amount: 30000}, byStatus[1])

	empty, err := repo.OrdersByStatus(ctx,
		time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(1999, 2, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestRepoRecentOrdersAndTickets(t *testing.T) {
	d := testDB(t)
	repo := adminops.NewRepo(d)
	ctx := context.Background()
	q := d.Querier(ctx)
	tag := uuid.NewString()[:8]
	clientID := newClientFixture(t, d, tag)

	var orderID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO orders (order_number, client_id, status, subtotal, total)
		VALUES ($1, $2, 'active', 150000, 150000)
		RETURNING id`, "IT-RECENT-"+tag, clientID).Scan(&orderID))
	t.Cleanup(func() { _, _ = q.Exec(ctx, `DELETE FROM orders WHERE id = $1`, orderID) })

	var deptID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO ticket_departments (name, email, active, sort)
		VALUES ($1, 'it@example.com', true, 99)
		RETURNING id`, "IT Dept "+tag).Scan(&deptID))
	t.Cleanup(func() { _, _ = q.Exec(ctx, `DELETE FROM ticket_departments WHERE id = $1`, deptID) })

	var ticketID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO tickets (ticket_number, client_id, department_id, subject, status, priority)
		VALUES ($1, $2, $3, 'IT recent ticket', 'open', 'high')
		RETURNING id`, "IT-TKT-"+tag, clientID, deptID).Scan(&ticketID))
	t.Cleanup(func() { _, _ = q.Exec(ctx, `DELETE FROM tickets WHERE id = $1`, ticketID) })

	orders, err := repo.RecentOrders(ctx, 5)
	require.NoError(t, err)
	require.NotEmpty(t, orders)
	var foundOrder bool
	for _, o := range orders {
		if o.ID == orderID {
			foundOrder = true
			assert.Equal(t, "IT-RECENT-"+tag, o.OrderNumber)
			assert.Equal(t, clientID, o.ClientID)
			assert.Equal(t, "IT "+tag, o.ClientName)
			assert.Equal(t, "active", o.Status)
			assert.Equal(t, int64(150000), o.Total)
		}
	}
	assert.True(t, foundOrder, "freshly inserted order must appear in RecentOrders (limit=5, newest-first)")

	tickets, err := repo.RecentTickets(ctx, 5)
	require.NoError(t, err)
	require.NotEmpty(t, tickets)
	var foundTicket bool
	for _, tk := range tickets {
		if tk.ID == ticketID {
			foundTicket = true
			assert.Equal(t, "IT-TKT-"+tag, tk.TicketNumber)
			require.NotNil(t, tk.ClientID)
			assert.Equal(t, clientID, *tk.ClientID)
			assert.Equal(t, "IT recent ticket", tk.Subject)
			assert.Equal(t, "open", tk.Status)
			assert.Equal(t, "high", tk.Priority)
			assert.Nil(t, tk.LastReplyAt)
		}
	}
	assert.True(t, foundTicket, "freshly inserted ticket must appear in RecentTickets (limit=5, newest-first)")
}

func TestRepoServicesReports(t *testing.T) {
	repo := adminops.NewRepo(testDB(t))
	ctx := context.Background()

	byStatus, err := repo.ServicesReport(ctx)
	require.NoError(t, err)
	require.NotNil(t, byStatus)

	byProduct, err := repo.ServicesByProduct(ctx)
	require.NoError(t, err)
	for _, row := range byProduct {
		assert.NotEmpty(t, row.Product)
		assert.GreaterOrEqual(t, row.Count, int64(1))
	}
}

func TestRepoListStaff(t *testing.T) {
	d := testDB(t)
	repo := adminops.NewRepo(d)
	ctx := context.Background()
	q := d.Querier(ctx)
	tag := uuid.NewString()[:8]

	var staffID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, status, permissions)
		VALUES ($1, 'secret-hash', 'staff', 'active', '{"billing":true}')
		RETURNING id`, "adminops-staff-"+tag+"@example.com").Scan(&staffID))
	t.Cleanup(func() { _, _ = q.Exec(ctx, `DELETE FROM users WHERE id = $1`, staffID) })

	rows, total, err := repo.ListStaff(ctx, ports.ListParams{Search: "adminops-staff-" + tag})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, staffID, rows[0].ID)
	assert.Empty(t, rows[0].PasswordHash, "hash never selected")
	assert.JSONEq(t, `{"billing":true}`, string(rows[0].Permissions))

	// status filter excludes
	_, total, err = repo.ListStaff(ctx, ports.ListParams{Search: "adminops-staff-" + tag, Status: "inactive"})
	require.NoError(t, err)
	assert.Zero(t, total)
}

func TestRepoListAuditFilters(t *testing.T) {
	d := testDB(t)
	repo := adminops.NewRepo(d)
	ctx := context.Background()
	q := d.Querier(ctx)
	tag := uuid.NewString()[:8]
	action := "it-test." + tag

	old := time.Now().UTC().AddDate(0, 0, -10)
	for i, entity := range []string{"user", "invoice"} {
		_, err := q.Exec(ctx, `
			INSERT INTO audit_logs (action, entity, entity_id, ip, created_at)
			VALUES ($1, $2, $3, '127.0.0.1', $4)`,
			action, entity, int64(i+1), old.AddDate(0, 0, i))
		require.NoError(t, err)
	}
	t.Cleanup(func() { _, _ = q.Exec(ctx, `DELETE FROM audit_logs WHERE action = $1`, action) })

	// action prefix filter
	rows, total, err := repo.ListAudit(ctx, adminops.AuditLogFilter{Action: "it-test." + tag})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, rows, 2)
	assert.Greater(t, rows[0].ID, rows[1].ID, "newest first")

	// entity filter
	_, total, err = repo.ListAudit(ctx, adminops.AuditLogFilter{Action: action, Entity: "invoice"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// date range: only the first (older) row
	end := old.AddDate(0, 0, 1)
	_, total, err = repo.ListAudit(ctx, adminops.AuditLogFilter{Action: action, To: &end})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	start := old.AddDate(0, 0, 1)
	_, total, err = repo.ListAudit(ctx, adminops.AuditLogFilter{Action: action, From: &start})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
}

func TestRepoListIntegrationFilters(t *testing.T) {
	d := testDB(t)
	repo := adminops.NewRepo(d)
	ctx := context.Background()
	q := d.Querier(ctx)
	provider := "it-prov-" + uuid.NewString()[:8]

	for _, success := range []bool{true, false} {
		_, err := q.Exec(ctx, `
			INSERT INTO integration_logs (provider, endpoint, method, status_code, success, latency_ms)
			VALUES ($1, '/x', 'POST', 200, $2, 5)`, provider, success)
		require.NoError(t, err)
	}
	t.Cleanup(func() { _, _ = q.Exec(ctx, `DELETE FROM integration_logs WHERE provider = $1`, provider) })

	rows, total, err := repo.ListIntegration(ctx, adminops.IntegrationLogFilter{Provider: provider})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, rows, 2)

	ok := true
	_, total, err = repo.ListIntegration(ctx, adminops.IntegrationLogFilter{Provider: provider, Success: &ok})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	fail := false
	rows, total, err = repo.ListIntegration(ctx, adminops.IntegrationLogFilter{Provider: provider, Success: &fail})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.False(t, rows[0].Success)
}

func TestRepoPurges(t *testing.T) {
	d := testDB(t)
	repo := adminops.NewRepo(d)
	ctx := context.Background()
	q := d.Querier(ctx)
	tag := uuid.NewString()[:8]

	// Very old fixture rows (400 days) + narrowly-newer cutoff (399 days) so
	// the purge cannot touch other agents' fresh fixtures.
	old := time.Now().UTC().AddDate(0, 0, -400)
	cutoff := time.Now().UTC().AddDate(0, 0, -399)

	provider := "it-purge-" + tag
	_, err := q.Exec(ctx, `
		INSERT INTO integration_logs (provider, endpoint, method, status_code, success, latency_ms, created_at)
		VALUES ($1, '/x', 'GET', 200, true, 1, $2)`, provider, old)
	require.NoError(t, err)

	email := "it-purge-" + tag + "@example.com"
	_, err = q.Exec(ctx, `
		INSERT INTO email_log (to_email, template_key, subject, status, created_at)
		VALUES ($1, 'x', 'x', 'sent', $2)`, email, old)
	require.NoError(t, err)

	action := "it-purge." + tag
	_, err = q.Exec(ctx, `
		INSERT INTO audit_logs (action, entity, entity_id, ip, created_at)
		VALUES ($1, 'test', 1, '', $2)`, action, old)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = q.Exec(ctx, `DELETE FROM integration_logs WHERE provider = $1`, provider)
		_, _ = q.Exec(ctx, `DELETE FROM email_log WHERE to_email = $1`, email)
		_, _ = q.Exec(ctx, `DELETE FROM audit_logs WHERE action = $1`, action)
	})

	n, err := repo.PurgeIntegrationLogsBefore(ctx, cutoff)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))
	n, err = repo.PurgeEmailLogBefore(ctx, cutoff)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))
	n, err = repo.PurgeAuditLogsBefore(ctx, cutoff)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))

	// Fixture rows are gone.
	var count int64
	require.NoError(t, q.QueryRow(ctx,
		`SELECT count(*) FROM integration_logs WHERE provider = $1`, provider).Scan(&count))
	assert.Zero(t, count)
	require.NoError(t, q.QueryRow(ctx,
		`SELECT count(*) FROM email_log WHERE to_email = $1`, email).Scan(&count))
	assert.Zero(t, count)
	require.NoError(t, q.QueryRow(ctx,
		`SELECT count(*) FROM audit_logs WHERE action = $1`, action).Scan(&count))
	assert.Zero(t, count)
}
