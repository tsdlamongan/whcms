package adminops_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/adminops"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fixedNow = time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)

// Local fakes for the module-local store interfaces (function-field, nil-safe)

type fakeDashboardStore struct {
	StatsFn             func(ctx context.Context) (*ports.DashboardStats, error)
	RevenueFn           func(ctx context.Context, from, to time.Time, groupBy string) ([]ports.RevenuePoint, error)
	OrdersReportFn      func(ctx context.Context, from, to time.Time) ([]ports.RevenuePoint, error)
	ServicesReportFn    func(ctx context.Context) (map[string]int64, error)
	ExtraStatsFn        func(ctx context.Context) (*adminops.ExtraStats, error)
	RevenueByGatewayFn  func(ctx context.Context, from, to time.Time) ([]adminops.GatewayRevenue, error)
	ServicesByProductFn func(ctx context.Context) ([]adminops.ProductStatusCount, error)
	RecentOrdersFn      func(ctx context.Context, limit int) ([]adminops.RecentOrderRow, error)
	RecentTicketsFn     func(ctx context.Context, limit int) ([]adminops.RecentTicketRow, error)
}

func (f *fakeDashboardStore) Stats(ctx context.Context) (*ports.DashboardStats, error) {
	if f.StatsFn != nil {
		return f.StatsFn(ctx)
	}
	return &ports.DashboardStats{}, nil
}

func (f *fakeDashboardStore) Revenue(ctx context.Context, from, to time.Time, groupBy string) ([]ports.RevenuePoint, error) {
	if f.RevenueFn != nil {
		return f.RevenueFn(ctx, from, to, groupBy)
	}
	return nil, nil
}

func (f *fakeDashboardStore) OrdersReport(ctx context.Context, from, to time.Time) ([]ports.RevenuePoint, error) {
	if f.OrdersReportFn != nil {
		return f.OrdersReportFn(ctx, from, to)
	}
	return nil, nil
}

func (f *fakeDashboardStore) ServicesReport(ctx context.Context) (map[string]int64, error) {
	if f.ServicesReportFn != nil {
		return f.ServicesReportFn(ctx)
	}
	return nil, nil
}

func (f *fakeDashboardStore) ExtraStats(ctx context.Context) (*adminops.ExtraStats, error) {
	if f.ExtraStatsFn != nil {
		return f.ExtraStatsFn(ctx)
	}
	return &adminops.ExtraStats{}, nil
}

func (f *fakeDashboardStore) RevenueByGateway(ctx context.Context, from, to time.Time) ([]adminops.GatewayRevenue, error) {
	if f.RevenueByGatewayFn != nil {
		return f.RevenueByGatewayFn(ctx, from, to)
	}
	return nil, nil
}

func (f *fakeDashboardStore) ServicesByProduct(ctx context.Context) ([]adminops.ProductStatusCount, error) {
	if f.ServicesByProductFn != nil {
		return f.ServicesByProductFn(ctx)
	}
	return nil, nil
}

func (f *fakeDashboardStore) RecentOrders(ctx context.Context, limit int) ([]adminops.RecentOrderRow, error) {
	if f.RecentOrdersFn != nil {
		return f.RecentOrdersFn(ctx, limit)
	}
	return nil, nil
}

func (f *fakeDashboardStore) RecentTickets(ctx context.Context, limit int) ([]adminops.RecentTicketRow, error) {
	if f.RecentTicketsFn != nil {
		return f.RecentTicketsFn(ctx, limit)
	}
	return nil, nil
}

type fakeLogStore struct {
	ListAuditFn                  func(ctx context.Context, f adminops.AuditLogFilter) ([]domain.AuditLog, int64, error)
	ListIntegrationFn            func(ctx context.Context, f adminops.IntegrationLogFilter) ([]domain.IntegrationLog, int64, error)
	PurgeIntegrationLogsBeforeFn func(ctx context.Context, before time.Time) (int64, error)
	PurgeEmailLogBeforeFn        func(ctx context.Context, before time.Time) (int64, error)
	PurgeAuditLogsBeforeFn       func(ctx context.Context, before time.Time) (int64, error)
}

func (f *fakeLogStore) ListAudit(ctx context.Context, flt adminops.AuditLogFilter) ([]domain.AuditLog, int64, error) {
	if f.ListAuditFn != nil {
		return f.ListAuditFn(ctx, flt)
	}
	return nil, 0, nil
}

func (f *fakeLogStore) ListIntegration(ctx context.Context, flt adminops.IntegrationLogFilter) ([]domain.IntegrationLog, int64, error) {
	if f.ListIntegrationFn != nil {
		return f.ListIntegrationFn(ctx, flt)
	}
	return nil, 0, nil
}

func (f *fakeLogStore) PurgeIntegrationLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	if f.PurgeIntegrationLogsBeforeFn != nil {
		return f.PurgeIntegrationLogsBeforeFn(ctx, before)
	}
	return 0, nil
}

func (f *fakeLogStore) PurgeEmailLogBefore(ctx context.Context, before time.Time) (int64, error) {
	if f.PurgeEmailLogBeforeFn != nil {
		return f.PurgeEmailLogBeforeFn(ctx, before)
	}
	return 0, nil
}

func (f *fakeLogStore) PurgeAuditLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	if f.PurgeAuditLogsBeforeFn != nil {
		return f.PurgeAuditLogsBeforeFn(ctx, before)
	}
	return 0, nil
}

type fakeStaffLister struct {
	ListStaffFn func(ctx context.Context, p ports.ListParams) ([]domain.User, int64, error)
}

func (f *fakeStaffLister) ListStaff(ctx context.Context, p ports.ListParams) ([]domain.User, int64, error) {
	if f.ListStaffFn != nil {
		return f.ListStaffFn(ctx, p)
	}
	return nil, 0, nil
}

// Test harness

type fixture struct {
	dash      *fakeDashboardStore
	logs      *fakeLogStore
	staff     *fakeStaffLister
	users     *mocks.MockUserRepo
	audit     *mocks.MockAuditRepo
	email     *mocks.MockEmailLogRepo
	settings  *mocks.MockSettingsRepo
	cache     *mocks.MockCache
	hasher    *mocks.MockPasswordHasher
	auditLog  *mocks.MockAuditLogger
	presence  *mocks.MockPresenceTracker
	secrets   adminops.GatewaySecrets
	encryptor *mocks.MockEncryptor
	jobs      *mocks.MockJobInspector
}

func newFixture() *fixture {
	return &fixture{
		dash:      &fakeDashboardStore{},
		logs:      &fakeLogStore{},
		staff:     &fakeStaffLister{},
		users:     &mocks.MockUserRepo{},
		audit:     &mocks.MockAuditRepo{},
		email:     &mocks.MockEmailLogRepo{},
		settings:  &mocks.MockSettingsRepo{},
		cache:     &mocks.MockCache{},
		hasher:    &mocks.MockPasswordHasher{},
		auditLog:  &mocks.MockAuditLogger{},
		presence:  &mocks.MockPresenceTracker{},
		encryptor: &mocks.MockEncryptor{},
		jobs:      &mocks.MockJobInspector{},
	}
}

func (f *fixture) service() *adminops.Service {
	return adminops.New(adminops.Deps{
		Dashboard: f.dash,
		Logs:      f.logs,
		Staff:     f.staff,
		Users:     f.users,
		Audit:     f.audit,
		EmailLogs: f.email,
		Settings:  f.settings,
		Cache:     f.cache,
		Hasher:    f.hasher,
		AuditLog:  f.auditLog,
		Clock:     &mocks.MockClock{FixedTime: fixedNow},
		Presence:  f.presence,
		Secrets:   f.secrets,
		Encryptor: f.encryptor,
		Jobs:      f.jobs,
	})
}

func requireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, code, apperr.From(err).Code)
}

// Dashboard

func TestDashboardCacheMissComputesAndCaches(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	f.dash.StatsFn = func(context.Context) (*ports.DashboardStats, error) {
		return &ports.DashboardStats{ClientsActive: 3, RevenueToday: 150000}, nil
	}
	f.dash.ExtraStatsFn = func(context.Context) (*adminops.ExtraStats, error) {
		return &adminops.ExtraStats{OrdersToday: 2, UnpaidTotal: 500000}, nil
	}
	f.audit.ListFn = func(_ context.Context, p ports.ListParams) ([]domain.AuditLog, int64, error) {
		assert.Equal(t, 1, p.Page)
		assert.Equal(t, 10, p.PerPage)
		return []domain.AuditLog{{ID: 9, Action: "x"}}, 1, nil
	}
	f.dash.RecentOrdersFn = func(_ context.Context, limit int) ([]adminops.RecentOrderRow, error) {
		assert.Equal(t, 5, limit)
		return []adminops.RecentOrderRow{{ID: 1, OrderNumber: "ORD-202607-000001", ClientName: "Budi Santoso", Status: "active", Total: 150000}}, nil
	}
	f.dash.RecentTicketsFn = func(_ context.Context, limit int) ([]adminops.RecentTicketRow, error) {
		assert.Equal(t, 5, limit)
		return []adminops.RecentTicketRow{{ID: 2, TicketNumber: "TKT-000002", Subject: "Help", Status: "open"}}, nil
	}
	f.jobs.ListModuleActionsFn = func(context.Context, ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error) {
		return nil, 4, nil
	}
	var setKey string
	var setTTL time.Duration
	f.cache.SetJSONFn = func(_ context.Context, key string, _ any, ttl time.Duration) error {
		setKey, setTTL = key, ttl
		return nil
	}

	data, err := f.service().Dashboard(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, int64(3), data.Stats.ClientsActive)
	assert.Equal(t, int64(150000), data.Stats.RevenueToday)
	assert.Equal(t, int64(2), data.Extra.OrdersToday)
	require.Len(t, data.RecentActivity, 1)
	require.Len(t, data.RecentOrders, 1)
	assert.Equal(t, "ORD-202607-000001", data.RecentOrders[0].OrderNumber)
	assert.Equal(t, "Budi Santoso", data.RecentOrders[0].ClientName)
	require.Len(t, data.RecentTickets, 1)
	assert.Equal(t, "TKT-000002", data.RecentTickets[0].TicketNumber)
	assert.Equal(t, int64(4), data.ModuleActionsPending)
	assert.Equal(t, fixedNow, data.GeneratedAt)
	assert.Equal(t, "adminops:dashboard", setKey)
	assert.Equal(t, 60*time.Second, setTTL, "dashboard cached 60s")
}

// TestDashboardModuleActionsPendingBestEffort covers a Redis hiccup (or Jobs
// simply not wired) never blocking the rest of the dashboard - the count
// just stays 0 instead of failing the whole request.
func TestDashboardModuleActionsPendingBestEffort(t *testing.T) {
	f := newFixture()
	f.jobs.ListModuleActionsFn = func(context.Context, ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error) {
		return nil, 0, errors.New("redis down")
	}
	data, err := f.service().Dashboard(context.Background(), false)
	require.NoError(t, err)
	assert.Zero(t, data.ModuleActionsPending)
}

func TestDashboardCacheHitSkipsQueries(t *testing.T) {
	f := newFixture()
	f.cache.GetJSONFn = func(_ context.Context, key string, out any) (bool, error) {
		assert.Equal(t, "adminops:dashboard", key)
		*(out.(*adminops.DashboardData)) = adminops.DashboardData{
			Stats: ports.DashboardStats{ClientsActive: 42},
		}
		return true, nil
	}
	f.dash.StatsFn = func(context.Context) (*ports.DashboardStats, error) {
		t.Fatal("Stats must not be called on cache hit")
		return nil, nil
	}

	data, err := f.service().Dashboard(context.Background(), false)
	require.NoError(t, err)
	assert.Equal(t, int64(42), data.Stats.ClientsActive)
}

func TestDashboardForceBypassesCacheReadButStillRepopulatesIt(t *testing.T) {
	f := newFixture()
	var getCalled, setCalled bool
	var setValue any
	f.cache.GetJSONFn = func(_ context.Context, key string, out any) (bool, error) {
		getCalled = true
		// Even a stale cache HIT must be ignored when force=true.
		*(out.(*adminops.DashboardData)) = adminops.DashboardData{Stats: ports.DashboardStats{ClientsActive: 999}}
		return true, nil
	}
	f.cache.SetJSONFn = func(_ context.Context, key string, v any, _ time.Duration) error {
		setCalled = true
		setValue = v
		return nil
	}
	f.dash.StatsFn = func(context.Context) (*ports.DashboardStats, error) {
		return &ports.DashboardStats{ClientsActive: 5}, nil
	}

	data, err := f.service().Dashboard(context.Background(), true)
	require.NoError(t, err)

	assert.False(t, getCalled, "force=true must skip the cache read entirely")
	assert.Equal(t, int64(5), data.Stats.ClientsActive, "must be the freshly-computed value, not the stale cached 999")
	assert.True(t, setCalled, "the fresh result must still repopulate the cache for subsequent non-forced reads")
	require.IsType(t, &adminops.DashboardData{}, setValue)
	assert.Equal(t, int64(5), setValue.(*adminops.DashboardData).Stats.ClientsActive)
}

func TestDashboardCacheErrorFailsOpen(t *testing.T) {
	f := newFixture()
	f.cache.GetJSONFn = func(context.Context, string, any) (bool, error) {
		return false, errors.New("redis down")
	}
	f.dash.StatsFn = func(context.Context) (*ports.DashboardStats, error) {
		return &ports.DashboardStats{ClientsActive: 7}, nil
	}
	data, err := f.service().Dashboard(context.Background(), false)
	require.NoError(t, err)
	assert.Equal(t, int64(7), data.Stats.ClientsActive)
}

func TestDashboardNilAggregatesAreGuarded(t *testing.T) {
	f := newFixture()
	f.dash.StatsFn = func(context.Context) (*ports.DashboardStats, error) { return nil, nil }
	f.dash.ExtraStatsFn = func(context.Context) (*adminops.ExtraStats, error) { return nil, nil }
	data, err := f.service().Dashboard(context.Background(), false)
	require.NoError(t, err)
	assert.Zero(t, data.Stats.ClientsActive)
	assert.Zero(t, data.Extra.OrdersToday)
}

func TestDashboardErrors(t *testing.T) {
	t.Run("stats", func(t *testing.T) {
		f := newFixture()
		f.dash.StatsFn = func(context.Context) (*ports.DashboardStats, error) { return nil, errors.New("boom") }
		_, err := f.service().Dashboard(context.Background(), false)
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("extra", func(t *testing.T) {
		f := newFixture()
		f.dash.ExtraStatsFn = func(context.Context) (*adminops.ExtraStats, error) { return nil, errors.New("boom") }
		_, err := f.service().Dashboard(context.Background(), false)
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("recent activity", func(t *testing.T) {
		f := newFixture()
		f.audit.ListFn = func(context.Context, ports.ListParams) ([]domain.AuditLog, int64, error) {
			return nil, 0, errors.New("boom")
		}
		_, err := f.service().Dashboard(context.Background(), false)
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("recent orders", func(t *testing.T) {
		f := newFixture()
		f.dash.RecentOrdersFn = func(context.Context, int) ([]adminops.RecentOrderRow, error) {
			return nil, errors.New("boom")
		}
		_, err := f.service().Dashboard(context.Background(), false)
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("recent tickets", func(t *testing.T) {
		f := newFixture()
		f.dash.RecentTicketsFn = func(context.Context, int) ([]adminops.RecentTicketRow, error) {
			return nil, errors.New("boom")
		}
		_, err := f.service().Dashboard(context.Background(), false)
		requireCode(t, err, apperr.CodeInternal)
	})
}

// Reports

func TestRevenueReportComputesTotalsAndSplit(t *testing.T) {
	f := newFixture()
	var gotFrom, gotTo time.Time
	var gotGroupBy string
	f.dash.RevenueFn = func(_ context.Context, from, to time.Time, groupBy string) ([]ports.RevenuePoint, error) {
		gotFrom, gotTo, gotGroupBy = from, to, groupBy
		return []ports.RevenuePoint{
			{Period: "2026-06-01", Amount: 100000, Count: 2},
			{Period: "2026-06-02", Amount: 50000, Count: 1},
		}, nil
	}
	f.dash.RevenueByGatewayFn = func(context.Context, time.Time, time.Time) ([]adminops.GatewayRevenue, error) {
		return []adminops.GatewayRevenue{{Gateway: "duitku", Amount: 150000, Count: 3}}, nil
	}

	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	data, err := f.service().RevenueReport(context.Background(), from, to, "day")
	require.NoError(t, err)

	assert.Equal(t, from, gotFrom)
	assert.Equal(t, to.AddDate(0, 0, 1), gotTo, "'to' date is inclusive -> exclusive bound")
	assert.Equal(t, "day", gotGroupBy)
	assert.Equal(t, int64(150000), data.TotalAmount)
	assert.Equal(t, int64(3), data.TotalCount)
	assert.Equal(t, "2026-06-01", data.From)
	assert.Equal(t, "2026-06-30", data.To)
	require.Len(t, data.ByGateway, 1)
	assert.Equal(t, "duitku", data.ByGateway[0].Gateway)
}

func TestRevenueReportDefaultsToLast30Days(t *testing.T) {
	f := newFixture()
	var gotFrom, gotTo time.Time
	var gotGroupBy string
	f.dash.RevenueFn = func(_ context.Context, from, to time.Time, groupBy string) ([]ports.RevenuePoint, error) {
		gotFrom, gotTo, gotGroupBy = from, to, groupBy
		return nil, nil
	}
	data, err := f.service().RevenueReport(context.Background(), time.Time{}, time.Time{}, "")
	require.NoError(t, err)
	today := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, today.AddDate(0, 0, -30), gotFrom)
	assert.Equal(t, today.AddDate(0, 0, 1), gotTo)
	assert.Equal(t, "day", gotGroupBy, "group_by defaults to day")
	assert.Equal(t, "2026-07-03", data.To)
}

func TestRevenueReportValidation(t *testing.T) {
	f := newFixture()
	_, err := f.service().RevenueReport(context.Background(), time.Time{}, time.Time{}, "week")
	requireCode(t, err, apperr.CodeValidation)

	from := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	_, err = f.service().RevenueReport(context.Background(), from, to, "day")
	requireCode(t, err, apperr.CodeValidation)
}

func TestRevenueReportErrors(t *testing.T) {
	t.Run("revenue", func(t *testing.T) {
		f := newFixture()
		f.dash.RevenueFn = func(context.Context, time.Time, time.Time, string) ([]ports.RevenuePoint, error) {
			return nil, errors.New("boom")
		}
		_, err := f.service().RevenueReport(context.Background(), time.Time{}, time.Time{}, "month")
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("gateway split", func(t *testing.T) {
		f := newFixture()
		f.dash.RevenueByGatewayFn = func(context.Context, time.Time, time.Time) ([]adminops.GatewayRevenue, error) {
			return nil, errors.New("boom")
		}
		_, err := f.service().RevenueReport(context.Background(), time.Time{}, time.Time{}, "day")
		requireCode(t, err, apperr.CodeInternal)
	})
}

func TestOrdersReport(t *testing.T) {
	f := newFixture()
	f.dash.OrdersReportFn = func(_ context.Context, from, to time.Time) ([]ports.RevenuePoint, error) {
		return []ports.RevenuePoint{
			{Period: "2026-07-01", Amount: 200000, Count: 4},
			{Period: "2026-07-02", Amount: 100000, Count: 1},
		}, nil
	}
	data, err := f.service().OrdersReport(context.Background(), time.Time{}, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, int64(300000), data.TotalAmount)
	assert.Equal(t, int64(5), data.TotalCount)

	_, err = f.service().OrdersReport(context.Background(),
		time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	requireCode(t, err, apperr.CodeValidation)

	f.dash.OrdersReportFn = func(context.Context, time.Time, time.Time) ([]ports.RevenuePoint, error) {
		return nil, errors.New("boom")
	}
	_, err = f.service().OrdersReport(context.Background(), time.Time{}, time.Time{})
	requireCode(t, err, apperr.CodeInternal)
}

func TestServicesReport(t *testing.T) {
	f := newFixture()
	f.dash.ServicesReportFn = func(context.Context) (map[string]int64, error) {
		return map[string]int64{"active": 5, "pending": 2}, nil
	}
	f.dash.ServicesByProductFn = func(context.Context) ([]adminops.ProductStatusCount, error) {
		return []adminops.ProductStatusCount{{Product: "Basic", Status: "active", Count: 5}}, nil
	}
	data, err := f.service().ServicesReport(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(5), data.ByStatus["active"])
	require.Len(t, data.ByProduct, 1)

	// nil map guard
	f.dash.ServicesReportFn = nil
	data, err = f.service().ServicesReport(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, data.ByStatus)

	f.dash.ServicesReportFn = func(context.Context) (map[string]int64, error) { return nil, errors.New("boom") }
	_, err = f.service().ServicesReport(context.Background())
	requireCode(t, err, apperr.CodeInternal)

	f.dash.ServicesReportFn = nil
	f.dash.ServicesByProductFn = func(context.Context) ([]adminops.ProductStatusCount, error) {
		return nil, errors.New("boom")
	}
	_, err = f.service().ServicesReport(context.Background())
	requireCode(t, err, apperr.CodeInternal)
}

func TestReportCSVRendering(t *testing.T) {
	rev := &adminops.RevenueReportData{
		Points:      []ports.RevenuePoint{{Period: "2026-07-01", Amount: 100, Count: 1}},
		TotalAmount: 100, TotalCount: 1,
	}
	assert.Equal(t, "period,amount,count\n2026-07-01,100,1\nTOTAL,100,1\n", string(rev.CSV()))

	ord := &adminops.OrdersReportData{
		Points:      []ports.RevenuePoint{{Period: "2026-07-01", Amount: 250, Count: 2}},
		TotalAmount: 250, TotalCount: 2,
	}
	assert.Equal(t, "period,amount,count\n2026-07-01,250,2\nTOTAL,250,2\n", string(ord.CSV()))

	svc := &adminops.ServicesReportData{
		ByProduct: []adminops.ProductStatusCount{{Product: "Basic", Status: "active", Count: 3}},
	}
	assert.Equal(t, "product,status,count\nBasic,active,3\n", string(svc.CSV()))
}

// Staff management

func TestListStaff(t *testing.T) {
	f := newFixture()
	f.staff.ListStaffFn = func(_ context.Context, p ports.ListParams) ([]domain.User, int64, error) {
		assert.Equal(t, "ops@", p.Search)
		return []domain.User{{ID: 1, Role: domain.RoleStaff}}, 1, nil
	}
	users, total, err := f.service().ListStaff(context.Background(), ports.ListParams{Search: "ops@"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, users, 1)

	f.staff.ListStaffFn = func(context.Context, ports.ListParams) ([]domain.User, int64, error) {
		return nil, 0, errors.New("boom")
	}
	_, _, err = f.service().ListStaff(context.Background(), ports.ListParams{})
	requireCode(t, err, apperr.CodeInternal)
}

func TestOnlineStaff(t *testing.T) {
	f := newFixture()
	f.presence.ListFn = func(context.Context) ([]ports.PresenceEntry, error) {
		return []ports.PresenceEntry{{UserID: 1, Email: "admin@example.com", Role: "admin"}}, nil
	}
	entries, err := f.service().OnlineStaff(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "admin@example.com", entries[0].Email)
}

func TestOnlineStaffRepoError(t *testing.T) {
	f := newFixture()
	f.presence.ListFn = func(context.Context) ([]ports.PresenceEntry, error) {
		return nil, errors.New("redis down")
	}
	_, err := f.service().OnlineStaff(context.Background())
	requireCode(t, err, apperr.CodeInternal)
}

func TestOnlineStaffNilPresenceReturnsEmpty(t *testing.T) {
	f := newFixture()
	f.presence = nil
	svc := adminops.New(adminops.Deps{
		Dashboard: f.dash, Logs: f.logs, Staff: f.staff, Users: f.users,
		Audit: f.audit, EmailLogs: f.email, Settings: f.settings, Cache: f.cache,
		Hasher: f.hasher, AuditLog: f.auditLog, Clock: &mocks.MockClock{FixedTime: fixedNow},
	})
	entries, err := svc.OnlineStaff(context.Background())
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestCreateStaffSuccess(t *testing.T) {
	f := newFixture()
	f.users.GetByEmailFn = func(context.Context, string) (*domain.User, error) {
		return nil, apperr.NotFound("user")
	}
	var created *domain.User
	f.users.CreateFn = func(_ context.Context, u *domain.User) error {
		u.ID = 77
		created = u
		return nil
	}

	user, err := f.service().CreateStaff(context.Background(), 1, adminops.CreateStaffInput{
		Email:       "  Staff@Example.COM ",
		Password:    "supersecret",
		Permissions: map[string]bool{"billing": true, "support": false},
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, "staff@example.com", user.Email, "email normalized")
	assert.Equal(t, domain.RoleStaff, user.Role)
	assert.Equal(t, domain.UserActive, user.Status)
	assert.Equal(t, "id", user.Locale, "locale defaults to id")
	assert.Equal(t, "hashed:supersecret", user.PasswordHash)

	var perms map[string]bool
	require.NoError(t, json.Unmarshal(user.Permissions, &perms))
	assert.True(t, perms["billing"])

	require.Len(t, f.auditLog.Entries, 1)
	assert.Equal(t, "staff.create", f.auditLog.Entries[0].Action)
	assert.Equal(t, int64(77), f.auditLog.Entries[0].EntityID)
	assert.Equal(t, int64(1), f.auditLog.Entries[0].ActorUserID)
}

func TestCreateStaffValidation(t *testing.T) {
	f := newFixture()
	svc := f.service()

	_, err := svc.CreateStaff(context.Background(), 1, adminops.CreateStaffInput{
		Email: "not-an-email", Password: "supersecret",
	})
	requireCode(t, err, apperr.CodeValidation)

	_, err = svc.CreateStaff(context.Background(), 1, adminops.CreateStaffInput{
		Email: "a@b.co", Password: "short",
	})
	requireCode(t, err, apperr.CodeValidation)

	_, err = svc.CreateStaff(context.Background(), 1, adminops.CreateStaffInput{
		Email: "a@b.co", Password: "supersecret",
		Permissions: map[string]bool{"bogus_module": true},
	})
	requireCode(t, err, apperr.CodeValidation)
	e := apperr.From(err)
	require.Len(t, e.Details, 1)
	assert.Equal(t, "permissions.bogus_module", e.Details[0].Field)
}

func TestCreateStaffDuplicateEmail(t *testing.T) {
	f := newFixture()
	f.users.GetByEmailFn = func(context.Context, string) (*domain.User, error) {
		return &domain.User{ID: 5, Email: "a@b.co"}, nil
	}
	_, err := f.service().CreateStaff(context.Background(), 1, adminops.CreateStaffInput{
		Email: "a@b.co", Password: "supersecret",
	})
	requireCode(t, err, apperr.CodeConflict)
}

func TestCreateStaffRepoErrors(t *testing.T) {
	t.Run("lookup error", func(t *testing.T) {
		f := newFixture()
		f.users.GetByEmailFn = func(context.Context, string) (*domain.User, error) {
			return nil, errors.New("db down")
		}
		_, err := f.service().CreateStaff(context.Background(), 1, adminops.CreateStaffInput{
			Email: "a@b.co", Password: "supersecret",
		})
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("hash error", func(t *testing.T) {
		f := newFixture()
		f.hasher.HashFn = func(string) (string, error) { return "", errors.New("argon2 fail") }
		_, err := f.service().CreateStaff(context.Background(), 1, adminops.CreateStaffInput{
			Email: "a@b.co", Password: "supersecret",
		})
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("create generic error", func(t *testing.T) {
		f := newFixture()
		f.users.CreateFn = func(context.Context, *domain.User) error { return errors.New("boom") }
		_, err := f.service().CreateStaff(context.Background(), 1, adminops.CreateStaffInput{
			Email: "a@b.co", Password: "supersecret",
		})
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("create apperr passthrough", func(t *testing.T) {
		f := newFixture()
		f.users.CreateFn = func(context.Context, *domain.User) error { return apperr.Conflict("dup") }
		_, err := f.service().CreateStaff(context.Background(), 1, adminops.CreateStaffInput{
			Email: "a@b.co", Password: "supersecret",
		})
		requireCode(t, err, apperr.CodeConflict)
	})
}

func TestGetStaff(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
			return &domain.User{ID: id, Role: domain.RoleStaff}, nil
		}
		u, err := f.service().GetStaff(context.Background(), 9)
		require.NoError(t, err)
		assert.Equal(t, int64(9), u.ID)
	})
	t.Run("not found", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) {
			return nil, apperr.NotFound("user")
		}
		_, err := f.service().GetStaff(context.Background(), 9)
		requireCode(t, err, apperr.CodeNotFound)
	})
	t.Run("nil user", func(t *testing.T) {
		f := newFixture()
		_, err := f.service().GetStaff(context.Background(), 9)
		requireCode(t, err, apperr.CodeNotFound)
	})
	t.Run("admin hidden", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) {
			return &domain.User{ID: 1, Role: domain.RoleAdmin}, nil
		}
		_, err := f.service().GetStaff(context.Background(), 1)
		requireCode(t, err, apperr.CodeNotFound)
	})
	t.Run("client hidden", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) {
			return &domain.User{ID: 2, Role: domain.RoleClient}, nil
		}
		_, err := f.service().GetStaff(context.Background(), 2)
		requireCode(t, err, apperr.CodeNotFound)
	})
	t.Run("repo error", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) {
			return nil, errors.New("boom")
		}
		_, err := f.service().GetStaff(context.Background(), 9)
		requireCode(t, err, apperr.CodeInternal)
	})
}

func strPtr(s string) *string { return &s }

func TestUpdateStaffSuccess(t *testing.T) {
	f := newFixture()
	f.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
		return &domain.User{
			ID: id, Role: domain.RoleStaff, Status: domain.UserActive,
			Permissions: json.RawMessage(`{"billing":true}`), Locale: "id",
		}, nil
	}
	var updated *domain.User
	f.users.UpdateFn = func(_ context.Context, u *domain.User) error {
		updated = u
		return nil
	}
	var newHash string
	f.users.UpdatePasswordFn = func(_ context.Context, id int64, hash string) error {
		newHash = hash
		return nil
	}

	user, err := f.service().UpdateStaff(context.Background(), 1, 9, adminops.UpdateStaffInput{
		Permissions: map[string]bool{"clients": true},
		Status:      strPtr("inactive"),
		Password:    strPtr("newpassword"),
		Locale:      strPtr("en"),
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, domain.UserInactive, user.Status)
	assert.Equal(t, "en", user.Locale)
	assert.JSONEq(t, `{"clients":true}`, string(user.Permissions))
	assert.Equal(t, "hashed:newpassword", newHash)

	require.Len(t, f.auditLog.Entries, 1)
	assert.Equal(t, "staff.update", f.auditLog.Entries[0].Action)
}

func TestUpdateStaffPartialLeavesFieldsUnchanged(t *testing.T) {
	f := newFixture()
	f.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
		return &domain.User{
			ID: id, Role: domain.RoleStaff, Status: domain.UserActive,
			Permissions: json.RawMessage(`{"billing":true}`), Locale: "id",
		}, nil
	}
	updateCalled := false
	f.users.UpdateFn = func(context.Context, *domain.User) error { updateCalled = true; return nil }
	f.users.UpdatePasswordFn = func(context.Context, int64, string) error {
		t.Fatal("UpdatePassword must not be called without a password")
		return nil
	}

	user, err := f.service().UpdateStaff(context.Background(), 1, 9, adminops.UpdateStaffInput{
		Status: strPtr("inactive"),
	})
	require.NoError(t, err)
	assert.True(t, updateCalled)
	assert.Equal(t, domain.UserInactive, user.Status)
	assert.JSONEq(t, `{"billing":true}`, string(user.Permissions), "permissions unchanged")
	assert.Equal(t, "id", user.Locale, "locale unchanged")
}

func TestUpdateStaffValidationAndErrors(t *testing.T) {
	staffUser := func(id int64) (*domain.User, error) {
		return &domain.User{ID: id, Role: domain.RoleStaff, Status: domain.UserActive}, nil
	}

	t.Run("bad status", func(t *testing.T) {
		f := newFixture()
		_, err := f.service().UpdateStaff(context.Background(), 1, 9, adminops.UpdateStaffInput{
			Status: strPtr("closed"),
		})
		requireCode(t, err, apperr.CodeValidation)
	})
	t.Run("unknown permission", func(t *testing.T) {
		f := newFixture()
		_, err := f.service().UpdateStaff(context.Background(), 1, 9, adminops.UpdateStaffInput{
			Permissions: map[string]bool{"nope": true},
		})
		requireCode(t, err, apperr.CodeValidation)
	})
	t.Run("not found", func(t *testing.T) {
		f := newFixture()
		_, err := f.service().UpdateStaff(context.Background(), 1, 9, adminops.UpdateStaffInput{})
		requireCode(t, err, apperr.CodeNotFound)
	})
	t.Run("update error", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) { return staffUser(id) }
		f.users.UpdateFn = func(context.Context, *domain.User) error { return errors.New("boom") }
		_, err := f.service().UpdateStaff(context.Background(), 1, 9, adminops.UpdateStaffInput{})
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("hash error", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) { return staffUser(id) }
		f.hasher.HashFn = func(string) (string, error) { return "", errors.New("boom") }
		_, err := f.service().UpdateStaff(context.Background(), 1, 9, adminops.UpdateStaffInput{
			Password: strPtr("newpassword"),
		})
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("update password error", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) { return staffUser(id) }
		f.users.UpdatePasswordFn = func(context.Context, int64, string) error { return errors.New("boom") }
		_, err := f.service().UpdateStaff(context.Background(), 1, 9, adminops.UpdateStaffInput{
			Password: strPtr("newpassword"),
		})
		requireCode(t, err, apperr.CodeInternal)
	})
}

func TestDeactivateStaff(t *testing.T) {
	t.Run("active -> inactive + audit", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
			return &domain.User{ID: id, Role: domain.RoleStaff, Status: domain.UserActive}, nil
		}
		var updated *domain.User
		f.users.UpdateFn = func(_ context.Context, u *domain.User) error { updated = u; return nil }

		user, err := f.service().DeactivateStaff(context.Background(), 1, 9)
		require.NoError(t, err)
		assert.Equal(t, domain.UserInactive, user.Status)
		require.NotNil(t, updated)
		require.Len(t, f.auditLog.Entries, 1)
		assert.Equal(t, "staff.deactivate", f.auditLog.Entries[0].Action)
	})
	t.Run("already inactive is idempotent", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
			return &domain.User{ID: id, Role: domain.RoleStaff, Status: domain.UserInactive}, nil
		}
		f.users.UpdateFn = func(context.Context, *domain.User) error {
			t.Fatal("Update must not be called")
			return nil
		}
		user, err := f.service().DeactivateStaff(context.Background(), 1, 9)
		require.NoError(t, err)
		assert.Equal(t, domain.UserInactive, user.Status)
		assert.Empty(t, f.auditLog.Entries)
	})
	t.Run("not found", func(t *testing.T) {
		f := newFixture()
		_, err := f.service().DeactivateStaff(context.Background(), 1, 9)
		requireCode(t, err, apperr.CodeNotFound)
	})
	t.Run("update error", func(t *testing.T) {
		f := newFixture()
		f.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
			return &domain.User{ID: id, Role: domain.RoleStaff, Status: domain.UserActive}, nil
		}
		f.users.UpdateFn = func(context.Context, *domain.User) error { return errors.New("boom") }
		_, err := f.service().DeactivateStaff(context.Background(), 1, 9)
		requireCode(t, err, apperr.CodeInternal)
	})
}

// Logs

func TestAuditLogsNormalizesDates(t *testing.T) {
	f := newFixture()
	var got adminops.AuditLogFilter
	f.logs.ListAuditFn = func(_ context.Context, flt adminops.AuditLogFilter) ([]domain.AuditLog, int64, error) {
		got = flt
		return []domain.AuditLog{{ID: 1}}, 1, nil
	}
	from := time.Date(2026, 7, 1, 15, 30, 0, 0, time.UTC)
	to := time.Date(2026, 7, 2, 8, 0, 0, 0, time.UTC)
	uid := int64(4)
	rows, total, err := f.service().AuditLogs(context.Background(), adminops.AuditLogFilter{
		UserID: &uid, Entity: "user", Action: "staff.", From: &from, To: &to,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	require.NotNil(t, got.From)
	assert.Equal(t, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), *got.From, "from truncated to date")
	require.NotNil(t, got.To)
	assert.Equal(t, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), *got.To, "to inclusive date -> exclusive next day")
	assert.Equal(t, "user", got.Entity)
	assert.Equal(t, "staff.", got.Action)

	f.logs.ListAuditFn = func(context.Context, adminops.AuditLogFilter) ([]domain.AuditLog, int64, error) {
		return nil, 0, errors.New("boom")
	}
	_, _, err = f.service().AuditLogs(context.Background(), adminops.AuditLogFilter{})
	requireCode(t, err, apperr.CodeInternal)
}

func TestEmailLogs(t *testing.T) {
	f := newFixture()
	f.email.ListFn = func(_ context.Context, p ports.ListParams) ([]domain.EmailLogEntry, int64, error) {
		assert.Equal(t, "failed", p.Status)
		return []domain.EmailLogEntry{{ID: 3}}, 1, nil
	}
	rows, total, err := f.service().EmailLogs(context.Background(), ports.ListParams{Status: "failed"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)

	f.email.ListFn = func(context.Context, ports.ListParams) ([]domain.EmailLogEntry, int64, error) {
		return nil, 0, errors.New("boom")
	}
	_, _, err = f.service().EmailLogs(context.Background(), ports.ListParams{})
	requireCode(t, err, apperr.CodeInternal)
}

func TestIntegrationLogs(t *testing.T) {
	f := newFixture()
	var got adminops.IntegrationLogFilter
	f.logs.ListIntegrationFn = func(_ context.Context, flt adminops.IntegrationLogFilter) ([]domain.IntegrationLog, int64, error) {
		got = flt
		return []domain.IntegrationLog{{ID: 2}}, 1, nil
	}
	success := true
	rows, total, err := f.service().IntegrationLogs(context.Background(), adminops.IntegrationLogFilter{
		Provider: "duitku", Success: &success,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, "duitku", got.Provider)
	require.NotNil(t, got.Success)
	assert.True(t, *got.Success)

	f.logs.ListIntegrationFn = func(context.Context, adminops.IntegrationLogFilter) ([]domain.IntegrationLog, int64, error) {
		return nil, 0, errors.New("boom")
	}
	_, _, err = f.service().IntegrationLogs(context.Background(), adminops.IntegrationLogFilter{})
	requireCode(t, err, apperr.CodeInternal)
}

// Pending Module Actions

func TestPendingModuleActions(t *testing.T) {
	f := newFixture()
	var got ports.ModuleActionFilter
	f.jobs.ListModuleActionsFn = func(_ context.Context, flt ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error) {
		got = flt
		return []ports.ModuleAction{{ID: "t1", Type: "domain:register"}}, 1, nil
	}
	rows, total, err := f.service().PendingModuleActions(context.Background(), ports.ModuleActionFilter{
		Type: "domain:register", Page: 2, PerPage: 25,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, "domain:register", got.Type)
	assert.Equal(t, 2, got.Page)
	assert.Equal(t, 25, got.PerPage)

	f.jobs.ListModuleActionsFn = func(context.Context, ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error) {
		return nil, 0, errors.New("redis down")
	}
	_, _, err = f.service().PendingModuleActions(context.Background(), ports.ModuleActionFilter{})
	requireCode(t, err, apperr.CodeInternal)
}

func TestRetryModuleAction(t *testing.T) {
	f := newFixture()
	var gotQueue, gotID string
	f.jobs.RetryModuleActionFn = func(_ context.Context, queue, id string) error {
		gotQueue, gotID = queue, id
		return nil
	}
	require.NoError(t, f.service().RetryModuleAction(context.Background(), 9, "default", "task-1"))
	assert.Equal(t, "default", gotQueue)
	assert.Equal(t, "task-1", gotID)
	require.Len(t, f.auditLog.Entries, 1)
	assert.Equal(t, "module_action.retry", f.auditLog.Entries[0].Action)
	assert.Equal(t, int64(9), f.auditLog.Entries[0].ActorUserID)
}

func TestRetryModuleActionPropagatesError(t *testing.T) {
	f := newFixture()
	f.jobs.RetryModuleActionFn = func(context.Context, string, string) error {
		return apperr.NotFound("module action")
	}
	err := f.service().RetryModuleAction(context.Background(), 9, "default", "missing")
	requireCode(t, err, apperr.CodeNotFound)
	assert.Empty(t, f.auditLog.Entries, "no audit entry when the retry itself failed")
}

func TestDeleteModuleAction(t *testing.T) {
	f := newFixture()
	var gotQueue, gotID string
	f.jobs.DeleteModuleActionFn = func(_ context.Context, queue, id string) error {
		gotQueue, gotID = queue, id
		return nil
	}
	require.NoError(t, f.service().DeleteModuleAction(context.Background(), 9, "critical", "task-2"))
	assert.Equal(t, "critical", gotQueue)
	assert.Equal(t, "task-2", gotID)
	require.Len(t, f.auditLog.Entries, 1)
	assert.Equal(t, "module_action.delete", f.auditLog.Entries[0].Action)
}

func TestDeleteModuleActionPropagatesError(t *testing.T) {
	f := newFixture()
	f.jobs.DeleteModuleActionFn = func(context.Context, string, string) error {
		return apperr.NotFound("module action")
	}
	err := f.service().DeleteModuleAction(context.Background(), 9, "default", "missing")
	requireCode(t, err, apperr.CodeNotFound)
	assert.Empty(t, f.auditLog.Entries)
}

func TestDismissAllModuleActions(t *testing.T) {
	f := newFixture()
	var got ports.ModuleActionFilter
	f.jobs.DismissAllModuleActionsFn = func(_ context.Context, flt ports.ModuleActionFilter) (int, error) {
		got = flt
		return 7, nil
	}
	n, err := f.service().DismissAllModuleActions(context.Background(), 9,
		ports.ModuleActionFilter{Type: "provision:create", State: "archived"})
	require.NoError(t, err)
	assert.Equal(t, 7, n)
	assert.Equal(t, "provision:create", got.Type)
	assert.Equal(t, "archived", got.State)
	require.Len(t, f.auditLog.Entries, 1)
	assert.Equal(t, "module_action.dismiss_all", f.auditLog.Entries[0].Action)
	assert.Equal(t, int64(9), f.auditLog.Entries[0].ActorUserID)
}

func TestDismissAllModuleActionsPropagatesError(t *testing.T) {
	f := newFixture()
	f.jobs.DismissAllModuleActionsFn = func(context.Context, ports.ModuleActionFilter) (int, error) {
		return 0, errors.New("redis down")
	}
	_, err := f.service().DismissAllModuleActions(context.Background(), 9, ports.ModuleActionFilter{})
	requireCode(t, err, apperr.CodeInternal)
	assert.Empty(t, f.auditLog.Entries, "no audit entry when the bulk dismiss itself failed")
}

// Gateways

func TestGatewaysDefaultsFromEnvSecrets(t *testing.T) {
	f := newFixture()
	f.secrets = adminops.GatewaySecrets{
		DuitkuMerchantCode: "DENV1",
		DuitkuAPIKeySet:    true,
	}
	cfg, err := f.service().Gateways(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "DENV1", cfg.Duitku.MerchantCode, "env fallback")
	assert.Equal(t, "sandbox", cfg.Duitku.Mode, "mode defaults to sandbox")
	assert.True(t, cfg.Duitku.APIKeySet, "secret exposed as presence boolean only")
}

func TestGatewaysEnvModeFallback(t *testing.T) {
	f := newFixture()
	f.secrets = adminops.GatewaySecrets{DuitkuMode: "production"}
	cfg, err := f.service().Gateways(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "production", cfg.Duitku.Mode)
	assert.False(t, cfg.Duitku.APIKeySet)
}

func TestGatewaysEnvBaseURLFallback(t *testing.T) {
	f := newFixture()
	f.secrets = adminops.GatewaySecrets{DuitkuBaseURL: "http://mockserver:9090"}
	cfg, err := f.service().Gateways(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "http://mockserver:9090", cfg.Duitku.BaseURL, "env fallback when no base_url setting is stored")
}

func TestGatewaysSettingsOverride(t *testing.T) {
	f := newFixture()
	f.secrets = adminops.GatewaySecrets{DuitkuMerchantCode: "DENV1"}
	f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
		switch key {
		case "gateway.duitku.merchant_code":
			return "D9999", nil
		case "gateway.duitku.mode":
			return "production", nil
		case "gateway.duitku.base_url":
			return "https://sandbox.duitku.com", nil
		}
		return def, nil
	}
	cfg, err := f.service().Gateways(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "D9999", cfg.Duitku.MerchantCode)
	assert.Equal(t, "production", cfg.Duitku.Mode)
	assert.Equal(t, "https://sandbox.duitku.com", cfg.Duitku.BaseURL, "stored override wins over the env fallback")
}

func TestGatewaysSettingsError(t *testing.T) {
	f := newFixture()
	f.settings.GetStringFn = func(_ context.Context, _, def string) (string, error) {
		return def, errors.New("db down")
	}
	_, err := f.service().Gateways(context.Background())
	requireCode(t, err, apperr.CodeInternal)

	// error on the second (mode) lookup only
	f = newFixture()
	f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
		if key == "gateway.duitku.mode" {
			return def, errors.New("db down")
		}
		return def, nil
	}
	_, err = f.service().Gateways(context.Background())
	requireCode(t, err, apperr.CodeInternal)

	// error on the third (base_url) lookup only
	f = newFixture()
	f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
		if key == "gateway.duitku.base_url" {
			return def, errors.New("db down")
		}
		return def, nil
	}
	_, err = f.service().Gateways(context.Background())
	requireCode(t, err, apperr.CodeInternal)
}

func TestUpdateGateways(t *testing.T) {
	f := newFixture()
	stored := map[string]any{}
	f.settings.SetFn = func(_ context.Context, key string, value any) error {
		stored[key] = value
		return nil
	}
	f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
		if v, ok := stored[key]; ok {
			return v.(string), nil
		}
		return def, nil
	}

	cfg, err := f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D7777", Mode: "production", BaseURL: "https://sandbox.duitku.com",
	})
	require.NoError(t, err)
	assert.Equal(t, "D7777", cfg.Duitku.MerchantCode)
	assert.Equal(t, "production", cfg.Duitku.Mode)
	assert.Equal(t, "https://sandbox.duitku.com", cfg.Duitku.BaseURL)
	assert.Equal(t, "D7777", stored["gateway.duitku.merchant_code"])
	assert.Equal(t, "production", stored["gateway.duitku.mode"])
	assert.Equal(t, "https://sandbox.duitku.com", stored["gateway.duitku.base_url"])

	require.Len(t, f.auditLog.Entries, 1)
	assert.Equal(t, "gateways.update", f.auditLog.Entries[0].Action)
}

func TestUpdateGatewaysBaseURLBlankClearsOverride(t *testing.T) {
	f := newFixture()
	stored := map[string]any{"gateway.duitku.base_url": "https://sandbox.duitku.com"}
	f.settings.SetFn = func(_ context.Context, key string, value any) error { stored[key] = value; return nil }
	f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
		if v, ok := stored[key]; ok {
			return v.(string), nil
		}
		return def, nil
	}

	cfg, err := f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D1", Mode: "sandbox", BaseURL: "  ",
	})
	require.NoError(t, err)
	assert.Equal(t, "", stored["gateway.duitku.base_url"], "blank/whitespace base_url clears the override")
	assert.Equal(t, "", cfg.Duitku.BaseURL)
}

func TestUpdateGatewaysValidation(t *testing.T) {
	f := newFixture()
	_, err := f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "", Mode: "production",
	})
	requireCode(t, err, apperr.CodeValidation)

	_, err = f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D1", Mode: "staging",
	})
	requireCode(t, err, apperr.CodeValidation)
}

func TestUpdateGatewaysSetError(t *testing.T) {
	f := newFixture()
	f.settings.SetFn = func(context.Context, string, any) error { return errors.New("boom") }
	_, err := f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D1", Mode: "sandbox",
	})
	requireCode(t, err, apperr.CodeInternal)

	// error only on the second key (mode)
	f = newFixture()
	f.settings.SetFn = func(_ context.Context, key string, _ any) error {
		if key == "gateway.duitku.mode" {
			return errors.New("boom")
		}
		return nil
	}
	_, err = f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D1", Mode: "sandbox",
	})
	requireCode(t, err, apperr.CodeInternal)

	// error only on the third key (base_url)
	f = newFixture()
	f.settings.SetFn = func(_ context.Context, key string, _ any) error {
		if key == "gateway.duitku.base_url" {
			return errors.New("boom")
		}
		return nil
	}
	_, err = f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D1", Mode: "sandbox",
	})
	requireCode(t, err, apperr.CodeInternal)
}

func TestUpdateGatewaysBeforeSnapshotError(t *testing.T) {
	f := newFixture()
	f.settings.GetStringFn = func(_ context.Context, _, def string) (string, error) {
		return def, errors.New("db down")
	}
	_, err := f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D1", Mode: "sandbox",
	})
	requireCode(t, err, apperr.CodeInternal)
}

// Gateways: dynamic, encrypted, admin-configurable Duitku API key

func TestGatewaysAPIKeySetFromDBOrEnv(t *testing.T) {
	f := newFixture()
	f.encryptor.EncryptFn = func(pt string) (string, error) { return "enc:" + pt, nil }
	stored := map[string]any{}
	f.settings.SetFn = func(_ context.Context, key string, value any) error { stored[key] = value; return nil }
	f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
		if v, ok := stored[key]; ok {
			return v.(string), nil
		}
		return def, nil
	}

	cfg, err := f.service().Gateways(context.Background())
	require.NoError(t, err)
	assert.False(t, cfg.Duitku.APIKeySet, "neither DB nor env has one")

	newKey := "new-duitku-key"
	_, err = f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D1", Mode: "sandbox", APIKey: &newKey,
	})
	require.NoError(t, err)
	assert.NotEqual(t, newKey, stored["gateway.duitku.api_key_enc"], "stored value must be ciphertext, not plaintext")

	cfg, err = f.service().Gateways(context.Background())
	require.NoError(t, err)
	assert.True(t, cfg.Duitku.APIKeySet, "DB-stored key")

	// Env-only presence, no DB value, also reports set.
	f2 := newFixture()
	f2.secrets = adminops.GatewaySecrets{DuitkuAPIKeySet: true}
	cfg, err = f2.service().Gateways(context.Background())
	require.NoError(t, err)
	assert.True(t, cfg.Duitku.APIKeySet)
}

func TestUpdateGatewaysAPIKeyNilLeavesStoredValueUntouched(t *testing.T) {
	f := newFixture()
	setCalls := map[string]int{}
	f.settings.SetFn = func(_ context.Context, key string, _ any) error { setCalls[key]++; return nil }

	_, err := f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D1", Mode: "sandbox", // APIKey: nil
	})
	require.NoError(t, err)
	assert.Zero(t, setCalls["gateway.duitku.api_key_enc"], "nil APIKey must not touch the stored key")
}

func TestUpdateGatewaysAPIKeyClearedByExplicitEmptyString(t *testing.T) {
	f := newFixture()
	stored := map[string]any{"gateway.duitku.api_key_enc": "some-ciphertext"}
	f.settings.SetFn = func(_ context.Context, key string, value any) error { stored[key] = value; return nil }
	f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
		if v, ok := stored[key]; ok {
			return v.(string), nil
		}
		return def, nil
	}

	empty := ""
	cfg, err := f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D1", Mode: "sandbox", APIKey: &empty,
	})
	require.NoError(t, err)
	assert.Equal(t, "", stored["gateway.duitku.api_key_enc"])
	assert.False(t, cfg.Duitku.APIKeySet)
}

func TestUpdateGatewaysAPIKeyEncryptError(t *testing.T) {
	f := newFixture()
	f.encryptor.EncryptFn = func(string) (string, error) { return "", errors.New("boom") }
	newKey := "new-key"
	_, err := f.service().UpdateGateways(context.Background(), 1, adminops.UpdateGatewaysInput{
		MerchantCode: "D1", Mode: "sandbox", APIKey: &newKey,
	})
	requireCode(t, err, apperr.CodeInternal)
}

// Gateways: manual bank-transfer gateway (non-secret - full config
// round-trips, unlike Duitku's API key)

func TestGatewaysManualDefaultsToDisabled(t *testing.T) {
	f := newFixture()
	cfg, err := f.service().Gateways(context.Background())
	require.NoError(t, err)
	assert.False(t, cfg.Manual.Enabled)
	assert.Empty(t, cfg.Manual.Accounts)
}

func TestGatewaysManualSettingsOverride(t *testing.T) {
	f := newFixture()
	stored := adminops.ManualGatewayConfig{
		Enabled:      true,
		Accounts:     []ports.BankAccount{{BankName: "BCA", AccountNumber: "123", AccountHolder: "WHCMS"}},
		Instructions: "note",
	}
	f.settings.GetJSONFn = func(_ context.Context, key string, out any) error {
		if key == "gateway.manual.config" {
			*(out.(*adminops.ManualGatewayConfig)) = stored
		}
		return nil
	}
	cfg, err := f.service().Gateways(context.Background())
	require.NoError(t, err)
	assert.True(t, cfg.Manual.Enabled)
	require.Len(t, cfg.Manual.Accounts, 1)
	assert.Equal(t, "BCA", cfg.Manual.Accounts[0].BankName)
	assert.Equal(t, "note", cfg.Manual.Instructions)
}

func TestGatewaysManualSettingsError(t *testing.T) {
	f := newFixture()
	f.settings.GetJSONFn = func(_ context.Context, key string, out any) error {
		if key == "gateway.manual.config" {
			return errors.New("db down")
		}
		return nil
	}
	_, err := f.service().Gateways(context.Background())
	requireCode(t, err, apperr.CodeInternal)
}

func TestUpdateManualGateway(t *testing.T) {
	f := newFixture()
	var stored any
	f.settings.SetFn = func(_ context.Context, key string, value any) error {
		if key == "gateway.manual.config" {
			stored = value
		}
		return nil
	}

	cfg, err := f.service().UpdateManualGateway(context.Background(), 1, adminops.UpdateManualGatewayInput{
		Enabled: true,
		Accounts: []adminops.BankAccountInput{
			{BankName: "BCA", AccountNumber: "1234567890", AccountHolder: "WHCMS Hosting"},
		},
		Instructions: "  Include the invoice number in your transfer note.  ",
	})
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	require.Len(t, cfg.Accounts, 1)
	assert.Equal(t, "BCA", cfg.Accounts[0].BankName)
	assert.Equal(t, "Include the invoice number in your transfer note.", cfg.Instructions, "trimmed")

	storedCfg, ok := stored.(adminops.ManualGatewayConfig)
	require.True(t, ok)
	assert.True(t, storedCfg.Enabled)
	require.Len(t, storedCfg.Accounts, 1)
	assert.Equal(t, "1234567890", storedCfg.Accounts[0].AccountNumber)

	require.Len(t, f.auditLog.Entries, 1)
	assert.Equal(t, "gateways.manual.update", f.auditLog.Entries[0].Action)
}

func TestUpdateManualGatewayValidation(t *testing.T) {
	f := newFixture()
	_, err := f.service().UpdateManualGateway(context.Background(), 1, adminops.UpdateManualGatewayInput{
		Enabled: true,
		Accounts: []adminops.BankAccountInput{
			{BankName: "", AccountNumber: "123", AccountHolder: "WHCMS"},
		},
	})
	requireCode(t, err, apperr.CodeValidation)
}

func TestUpdateManualGatewayBeforeSnapshotError(t *testing.T) {
	f := newFixture()
	f.settings.GetJSONFn = func(_ context.Context, key string, out any) error {
		return errors.New("db down")
	}
	_, err := f.service().UpdateManualGateway(context.Background(), 1, adminops.UpdateManualGatewayInput{})
	requireCode(t, err, apperr.CodeInternal)
}

func TestUpdateManualGatewaySetError(t *testing.T) {
	f := newFixture()
	f.settings.SetFn = func(_ context.Context, key string, _ any) error {
		if key == "gateway.manual.config" {
			return errors.New("boom")
		}
		return nil
	}
	_, err := f.service().UpdateManualGateway(context.Background(), 1, adminops.UpdateManualGatewayInput{})
	requireCode(t, err, apperr.CodeInternal)
}

// Housekeeping

func TestHousekeepRetentionWindows(t *testing.T) {
	f := newFixture()
	var intBefore, emailBefore, auditBefore time.Time
	f.logs.PurgeIntegrationLogsBeforeFn = func(_ context.Context, before time.Time) (int64, error) {
		intBefore = before
		return 3, nil
	}
	f.logs.PurgeEmailLogBeforeFn = func(_ context.Context, before time.Time) (int64, error) {
		emailBefore = before
		return 2, nil
	}
	f.logs.PurgeAuditLogsBeforeFn = func(_ context.Context, before time.Time) (int64, error) {
		auditBefore = before
		return 5, nil
	}

	n, err := f.service().Housekeep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 10, n, "returns the total purged count")
	assert.Equal(t, fixedNow.AddDate(0, 0, -90), intBefore, "integration logs kept 90d")
	assert.Equal(t, fixedNow.AddDate(0, 0, -90), emailBefore, "email log kept 90d")
	assert.Equal(t, fixedNow.AddDate(0, 0, -365), auditBefore, "audit kept 365d")
}

func TestHousekeepErrors(t *testing.T) {
	t.Run("integration purge", func(t *testing.T) {
		f := newFixture()
		f.logs.PurgeIntegrationLogsBeforeFn = func(context.Context, time.Time) (int64, error) {
			return 0, errors.New("boom")
		}
		_, err := f.service().Housekeep(context.Background())
		requireCode(t, err, apperr.CodeInternal)
	})
	t.Run("email purge", func(t *testing.T) {
		f := newFixture()
		f.logs.PurgeEmailLogBeforeFn = func(context.Context, time.Time) (int64, error) {
			return 0, errors.New("boom")
		}
		n, err := f.service().Housekeep(context.Background())
		requireCode(t, err, apperr.CodeInternal)
		assert.Equal(t, 0, n)
	})
	t.Run("audit purge", func(t *testing.T) {
		f := newFixture()
		f.logs.PurgeAuditLogsBeforeFn = func(context.Context, time.Time) (int64, error) {
			return 0, errors.New("boom")
		}
		_, err := f.service().Housekeep(context.Background())
		requireCode(t, err, apperr.CodeInternal)
	})
}
