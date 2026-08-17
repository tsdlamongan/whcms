//go:build integration

// Integration tests against the real local Postgres (whmcs DB, migrations
// applied). Run: go test -tags integration ./internal/modules/provisioning/
//
// Every test creates its OWN fixture rows with uuid-suffixed identifiers and
// never touches shared/seeded data.
package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

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
		t.Skipf("integration tests need the local whmcs database: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

// fixtures creates a user+client and a product (with group) unique to the test.
type fixtures struct {
	d         *db.DB
	uid       string
	clientID  int64
	productID int64
}

func seedFixtures(t *testing.T, d *db.DB) *fixtures {
	t.Helper()
	ctx := context.Background()
	f := &fixtures{d: d, uid: uuid.NewString()[:8]}

	var userID int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, status)
		VALUES ($1, 'x', 'client', 'active') RETURNING id`,
		fmt.Sprintf("prov-%s@test.local", f.uid)).Scan(&userID))
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO clients (user_id, first_name, last_name)
		VALUES ($1, 'Prov', 'Test') RETURNING id`, userID).Scan(&f.clientID))

	var groupID int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO product_groups (name, slug) VALUES ($1, $2) RETURNING id`,
		"prov-grp-"+f.uid, "prov-grp-"+f.uid).Scan(&groupID))
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO products (group_id, name, slug, type, module, package_name)
		VALUES ($1, $2, $3, 'shared_hosting', 'cpanel', 'basic') RETURNING id`,
		groupID, "prov-prod-"+f.uid, "prov-prod-"+f.uid).Scan(&f.productID))
	return f
}

func (f *fixtures) newService(t *testing.T, repo *Repo, status domain.ServiceStatus, serverID *int64) *domain.Service {
	t.Helper()
	due := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	s := &domain.Service{
		ClientID: f.clientID, ProductID: f.productID, ServerID: serverID,
		Domain:   fmt.Sprintf("svc-%s-%s.test", f.uid, uuid.NewString()[:6]),
		Username: "u" + f.uid, PasswordEnc: "enc", Status: status,
		BillingCycle: domain.CycleMonthly, RecurringAmount: 100_000,
		NextDueDate: &due,
	}
	require.NoError(t, repo.Create(context.Background(), s))
	return s
}

func (f *fixtures) newInvoice(t *testing.T, status domain.InvoiceStatus, due time.Time, serviceID int64) int64 {
	t.Helper()
	ctx := context.Background()
	var invID int64
	require.NoError(t, f.d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO invoices (invoice_number, client_id, status, subtotal, total, due_date)
		VALUES ($1, $2, $3, 100000, 100000, $4) RETURNING id`,
		"INV-PROV-"+uuid.NewString()[:13], f.clientID, status, due).Scan(&invID))
	_, err := f.d.Querier(ctx).Exec(ctx, `
		INSERT INTO invoice_items (invoice_id, description, amount, related_type, related_id)
		VALUES ($1, 'Renewal', 100000, 'service_renewal', $2)`, invID, serviceID)
	require.NoError(t, err)
	return invID
}

func TestRepoServiceCRUD(t *testing.T) {
	d := testDB(t)
	repo := NewRepo(d)
	f := seedFixtures(t, d)
	ctx := context.Background()

	s := f.newService(t, repo, domain.ServicePending, nil)
	require.NotZero(t, s.ID)

	got, err := repo.GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, s.Domain, got.Domain)
	assert.Equal(t, domain.ServicePending, got.Status)
	assert.Equal(t, int64(100_000), got.RecurringAmount)

	// Update all mutable fields.
	got.Status = domain.ServiceActive
	got.SuspendReason = ""
	reg := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	got.RegistrationDate = &reg
	got.PanelMeta = []byte(`{"account_ip":"10.0.0.9"}`)
	require.NoError(t, repo.Update(ctx, got))

	got2, err := repo.GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ServiceActive, got2.Status)
	assert.Contains(t, string(got2.PanelMeta), "10.0.0.9")

	// UpdateStatus.
	require.NoError(t, repo.UpdateStatus(ctx, s.ID, domain.ServiceSuspended))
	got3, err := repo.GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ServiceSuspended, got3.Status)

	// ListByClient with status + search filter.
	list, total, err := repo.ListByClient(ctx, f.clientID, ports.ListParams{Status: "suspended", Search: s.Domain})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	assert.Equal(t, s.ID, list[0].ID)

	// Unknown id -> NOT_FOUND.
	_, err = repo.GetByID(ctx, -1)
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperr.CodeNotFound, ae.Code)
	assert.ErrorAs(t, repo.UpdateStatus(ctx, -1, domain.ServiceActive), &ae)
	missing := *got3
	missing.ID = -1
	assert.ErrorAs(t, repo.Update(ctx, &missing), &ae)
}

func TestRepoListRenewalsDueAndCountByServer(t *testing.T) {
	d := testDB(t)
	repo := NewRepo(d)
	f := seedFixtures(t, d)
	ctx := context.Background()

	group := &domain.ServerGroup{Name: "grp-" + f.uid, Strategy: domain.StrategyLeastUsed}
	require.NoError(t, repo.CreateGroup(ctx, group))
	server := &domain.Server{GroupID: &group.ID, Name: "srv-" + f.uid,
		Module: domain.ModuleCpanel, Hostname: f.uid + ".host.test", Port: 2087, Active: true}
	require.NoError(t, repo.createServer(ctx, server))

	active := f.newService(t, repo, domain.ServiceActive, &server.ID)
	f.newService(t, repo, domain.ServicePending, &server.ID)
	terminated := f.newService(t, repo, domain.ServiceActive, &server.ID)
	require.NoError(t, repo.UpdateStatus(ctx, terminated.ID, domain.ServiceTerminated))

	// next_due_date 2026-08-01 -> due before 2026-09-01, not before 2026-07-01.
	due, err := repo.ListRenewalsDue(ctx, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	ids := map[int64]bool{}
	for _, s := range due {
		ids[s.ID] = true
	}
	assert.True(t, ids[active.ID])
	assert.False(t, ids[terminated.ID])

	none, err := repo.ListRenewalsDue(ctx, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	for _, s := range none {
		assert.NotEqual(t, active.ID, s.ID)
	}

	// pending + active count; terminated does not.
	n, err := repo.CountByServer(ctx, server.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

func TestRepoGetByIDForUpdateAndGetByIDs(t *testing.T) {
	d := testDB(t)
	repo := NewRepo(d)
	f := seedFixtures(t, d)
	ctx := context.Background()

	svc1 := f.newService(t, repo, domain.ServiceActive, nil)
	svc2 := f.newService(t, repo, domain.ServiceActive, nil)

	locked, err := repo.GetByIDForUpdate(ctx, svc1.ID)
	require.NoError(t, err)
	assert.Equal(t, svc1.Domain, locked.Domain)

	_, err = repo.GetByIDForUpdate(ctx, -1)
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperr.CodeNotFound, ae.Code)

	list, err := repo.GetByIDs(ctx, []int64{svc1.ID, svc2.ID, -1})
	require.NoError(t, err)
	ids := map[int64]bool{}
	for _, s := range list {
		ids[s.ID] = true
	}
	assert.True(t, ids[svc1.ID])
	assert.True(t, ids[svc2.ID])
	assert.Len(t, list, 2, "unknown id is simply omitted")

	empty, err := repo.GetByIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestRepoCountByServerAndPackage(t *testing.T) {
	d := testDB(t)
	repo := NewRepo(d)
	f := seedFixtures(t, d)
	ctx := context.Background()

	group := &domain.ServerGroup{Name: "grp-" + f.uid, Strategy: domain.StrategyLeastUsed}
	require.NoError(t, repo.CreateGroup(ctx, group))
	server := &domain.Server{GroupID: &group.ID, Name: "srv-" + f.uid,
		Module: domain.ModuleCpanel, Hostname: f.uid + ".host.test", Port: 2087, Active: true}
	require.NoError(t, repo.createServer(ctx, server))
	otherServer := &domain.Server{GroupID: &group.ID, Name: "srv2-" + f.uid,
		Module: domain.ModuleCpanel, Hostname: f.uid + "-2.host.test", Port: 2087, Active: true}
	require.NoError(t, repo.createServer(ctx, otherServer))

	pkgName := "whcms_spec_" + f.uid
	setPackage := func(s *domain.Service) {
		s.PanelMeta = json.RawMessage(`{"package_name":"` + pkgName + `"}`)
		require.NoError(t, repo.Update(ctx, s))
	}

	sibling := f.newService(t, repo, domain.ServiceActive, &server.ID)
	setPackage(sibling)
	self := f.newService(t, repo, domain.ServiceActive, &server.ID)
	setPackage(self)
	terminatedSibling := f.newService(t, repo, domain.ServiceActive, &server.ID)
	setPackage(terminatedSibling)
	require.NoError(t, repo.UpdateStatus(ctx, terminatedSibling.ID, domain.ServiceTerminated))
	otherServerSibling := f.newService(t, repo, domain.ServiceActive, &otherServer.ID)
	setPackage(otherServerSibling)
	otherPackageSibling := f.newService(t, repo, domain.ServiceActive, &server.ID)
	otherPackageSibling.PanelMeta = json.RawMessage(`{"package_name":"whcms_spec_different"}`)
	require.NoError(t, repo.Update(ctx, otherPackageSibling))

	n, err := repo.CountByServerAndPackage(ctx, server.ID, pkgName, self.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "only the active sibling on the same server with the same package should count")
}

func TestRepoPickServerStrategies(t *testing.T) {
	d := testDB(t)
	repo := NewRepo(d)
	f := seedFixtures(t, d)
	ctx := context.Background()

	// least_used: pick the server with fewer live accounts, capacity-aware.
	lu := &domain.ServerGroup{Name: "lu-" + f.uid, Strategy: domain.StrategyLeastUsed}
	require.NoError(t, repo.CreateGroup(ctx, lu))
	s1 := &domain.Server{GroupID: &lu.ID, Name: "lu1-" + f.uid, Module: domain.ModuleCpanel,
		Hostname: "lu1-" + f.uid + ".test", Port: 2087, Active: true}
	s2 := &domain.Server{GroupID: &lu.ID, Name: "lu2-" + f.uid, Module: domain.ModuleCpanel,
		Hostname: "lu2-" + f.uid + ".test", Port: 2087, Active: true}
	require.NoError(t, repo.createServer(ctx, s1))
	require.NoError(t, repo.createServer(ctx, s2))

	f.newService(t, repo, domain.ServiceActive, &s1.ID) // s1 has 1, s2 has 0

	picked, err := repo.PickServer(ctx, lu.ID)
	require.NoError(t, err)
	assert.Equal(t, s2.ID, picked.ID)

	// Fill s2 to capacity -> falls back to s1.
	_, err = d.Querier(ctx).Exec(ctx, `UPDATE servers SET max_accounts = 1 WHERE id = $1`, s2.ID)
	require.NoError(t, err)
	f.newService(t, repo, domain.ServiceActive, &s2.ID)
	picked, err = repo.PickServer(ctx, lu.ID)
	require.NoError(t, err)
	assert.Equal(t, s1.ID, picked.ID)

	// Inactive servers are skipped; empty group -> CONFLICT.
	_, err = d.Querier(ctx).Exec(ctx,
		`UPDATE servers SET active = FALSE, max_accounts = 0 WHERE id IN ($1, $2)`, s1.ID, s2.ID)
	require.NoError(t, err)
	_, err = repo.PickServer(ctx, lu.ID)
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperr.CodeConflict, ae.Code)

	// round_robin: server never assigned (or with oldest last assignment) first.
	rr := &domain.ServerGroup{Name: "rr-" + f.uid, Strategy: domain.StrategyRoundRobin}
	require.NoError(t, repo.CreateGroup(ctx, rr))
	r1 := &domain.Server{GroupID: &rr.ID, Name: "rr1-" + f.uid, Module: domain.ModuleCpanel,
		Hostname: "rr1-" + f.uid + ".test", Port: 2087, Active: true}
	r2 := &domain.Server{GroupID: &rr.ID, Name: "rr2-" + f.uid, Module: domain.ModuleCpanel,
		Hostname: "rr2-" + f.uid + ".test", Port: 2087, Active: true}
	require.NoError(t, repo.createServer(ctx, r1))
	require.NoError(t, repo.createServer(ctx, r2))

	f.newService(t, repo, domain.ServiceActive, &r1.ID) // r1 just used
	picked, err = repo.PickServer(ctx, rr.ID)
	require.NoError(t, err)
	assert.Equal(t, r2.ID, picked.ID)

	// Unknown group -> NOT_FOUND.
	_, err = repo.PickServer(ctx, -1)
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperr.CodeNotFound, ae.Code)
}

func TestRepoAutomationQueries(t *testing.T) {
	d := testDB(t)
	repo := NewRepo(d)
	f := seedFixtures(t, d)
	ctx := context.Background()

	// Overdue-suspendable: active service + overdue renewal invoice due long ago.
	suspendable := f.newService(t, repo, domain.ServiceActive, nil)
	f.newInvoice(t, domain.InvoiceOverdue, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), suspendable.ID)

	fresh := f.newService(t, repo, domain.ServiceActive, nil)
	f.newInvoice(t, domain.InvoiceOverdue, time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), fresh.ID)

	list, err := repo.ListOverdueSuspendable(ctx, time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	ids := map[int64]bool{}
	for _, s := range list {
		ids[s.ID] = true
	}
	assert.True(t, ids[suspendable.ID])
	assert.False(t, ids[fresh.ID])

	// Terminatable: suspended long ago via panel_meta.suspended_at.
	longSuspended := f.newService(t, repo, domain.ServiceActive, nil)
	longSuspended.Status = domain.ServiceSuspended
	longSuspended.PanelMeta = []byte(`{"suspended_at":"2026-06-01T00:00:00Z"}`)
	require.NoError(t, repo.Update(ctx, longSuspended))

	recentSuspended := f.newService(t, repo, domain.ServiceActive, nil)
	recentSuspended.Status = domain.ServiceSuspended
	recentSuspended.PanelMeta = []byte(`{"suspended_at":"2026-06-29T00:00:00Z"}`)
	require.NoError(t, repo.Update(ctx, recentSuspended))

	// cancel_at_period_end past its due date (2026-08-01).
	eot := f.newService(t, repo, domain.ServiceActive, nil)
	eot.PanelMeta = []byte(`{"cancel_at_period_end":true}`)
	require.NoError(t, repo.Update(ctx, eot))

	list, err = repo.ListTerminatable(ctx,
		time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC), // suspended before
		time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC))  // due before
	require.NoError(t, err)
	ids = map[int64]bool{}
	for _, s := range list {
		ids[s.ID] = true
	}
	assert.True(t, ids[longSuspended.ID])
	assert.False(t, ids[recentSuspended.ID])
	assert.True(t, ids[eot.ID])

	// eot not returned before its due date.
	list, err = repo.ListTerminatable(ctx,
		time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	for _, s := range list {
		assert.NotEqual(t, eot.ID, s.ID)
	}
}

func TestRepoServerAndGroupCRUD(t *testing.T) {
	d := testDB(t)
	repo := NewRepo(d)
	view := repo.Servers() // ports.ServerRepo adapter
	f := seedFixtures(t, d)
	ctx := context.Background()

	g := &domain.ServerGroup{Name: "crud-" + f.uid, Strategy: domain.StrategyRoundRobin}
	require.NoError(t, view.CreateGroup(ctx, g))
	require.NotZero(t, g.ID)

	g.Name = "crud2-" + f.uid
	g.Strategy = domain.StrategyLeastUsed
	require.NoError(t, view.UpdateGroup(ctx, g))
	got, err := view.GetGroupByID(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StrategyLeastUsed, got.Strategy)

	groups, err := view.ListGroups(ctx)
	require.NoError(t, err)
	found := false
	for _, x := range groups {
		if x.ID == g.ID {
			found = true
		}
	}
	assert.True(t, found)

	server := &domain.Server{GroupID: &g.ID, Name: "crud-srv-" + f.uid,
		Module: domain.ModuleDirectAdmin, Hostname: f.uid + ".da.test", Port: 2222,
		Username: "admin", PasswordEnc: "enc-pw", APITokenEnc: "enc-tok",
		UseSSL: true, Nameserver1: "ns1.test", Nameserver2: "ns2.test", MaxAccounts: 50, Active: true}
	require.NoError(t, view.Create(ctx, server))
	require.NotZero(t, server.ID)

	gotSrv, err := view.GetByID(ctx, server.ID)
	require.NoError(t, err)
	assert.Equal(t, "enc-tok", gotSrv.APITokenEnc)
	assert.Equal(t, 50, gotSrv.MaxAccounts)
	assert.Empty(t, gotSrv.PackagePrefix, "blank by default")
	assert.Empty(t, gotSrv.IPAddress, "blank by default")

	gotSrv.Name = "crud-srv2-" + f.uid
	gotSrv.Active = false
	gotSrv.PackagePrefix = "reseller_"
	gotSrv.IPAddress = "203.0.113.10"
	require.NoError(t, view.Update(ctx, gotSrv))

	gotSrv2, err := view.GetByID(ctx, server.ID)
	require.NoError(t, err)
	assert.Equal(t, "reseller_", gotSrv2.PackagePrefix, "package_prefix persists across an update")
	assert.Equal(t, "203.0.113.10", gotSrv2.IPAddress, "ip_address persists across an update")

	list, total, err := view.List(ctx, ports.ListParams{Search: "crud-srv2-" + f.uid, Status: "inactive"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	assert.Equal(t, server.ID, list[0].ID)

	// Group with a server cannot be deleted (FK -> CONFLICT).
	err = view.DeleteGroup(ctx, g.ID)
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperr.CodeConflict, ae.Code)

	require.NoError(t, view.Delete(ctx, server.ID))
	require.NoError(t, view.DeleteGroup(ctx, g.ID))
	_, err = view.GetByID(ctx, server.ID)
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperr.CodeNotFound, ae.Code)

	// Missing rows.
	assert.Error(t, view.Delete(ctx, -1))
	assert.Error(t, view.DeleteGroup(ctx, -1))
	_, err = view.GetGroupByID(ctx, -1)
	assert.Error(t, err)
	missing := *server
	missing.ID = -1
	assert.Error(t, view.Update(ctx, &missing))
	gm := *g
	gm.ID = -1
	assert.Error(t, view.UpdateGroup(ctx, &gm))
}

func TestRepoCancellationRequestCRUD(t *testing.T) {
	d := testDB(t)
	repo := NewRepo(d)
	f := seedFixtures(t, d)
	ctx := context.Background()
	view := repo.CancellationRequests()

	svc := f.newService(t, repo, domain.ServiceActive, nil)

	cr := &domain.CancellationRequest{
		ServiceID: svc.ID, ClientID: f.clientID,
		Mode: CancelModeEndOfTerm, Reason: "too expensive", Status: domain.CancellationPending,
	}
	require.NoError(t, view.Create(ctx, cr))
	require.NotZero(t, cr.ID)
	assert.False(t, cr.RequestedAt.IsZero())

	got, err := view.GetByID(ctx, cr.ID)
	require.NoError(t, err)
	assert.Equal(t, svc.ID, got.ServiceID)
	assert.Equal(t, domain.CancellationPending, got.Status)
	assert.Nil(t, got.DecidedAt)

	pending, err := view.GetPendingByService(ctx, svc.ID)
	require.NoError(t, err)
	require.NotNil(t, pending)
	assert.Equal(t, cr.ID, pending.ID)

	// Partial unique index: a second pending request for the same service
	// conflicts (the service layer's GetPendingByService guard is the normal
	// path; this proves the DB-level backstop too).
	dup := &domain.CancellationRequest{
		ServiceID: svc.ID, ClientID: f.clientID,
		Mode: CancelModeEndOfTerm, Status: domain.CancellationPending,
	}
	err = view.Create(ctx, dup)
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperr.CodeConflict, ae.Code)

	// Accept: mark decided.
	now := time.Now().UTC()
	got.Status = domain.CancellationAccepted
	got.DecidedAt = &now
	adminID := int64(1)
	got.DecidedBy = &adminID
	require.NoError(t, view.Update(ctx, got))

	got2, err := view.GetByID(ctx, cr.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.CancellationAccepted, got2.Status)
	require.NotNil(t, got2.DecidedBy)
	assert.Equal(t, adminID, *got2.DecidedBy)

	// No longer pending, so a fresh request on the same service is allowed.
	noPending, err := view.GetPendingByService(ctx, svc.ID)
	require.NoError(t, err)
	assert.Nil(t, noPending)

	// Immediate mode, auto_processed at creation (no admin decision needed).
	autoSvc := f.newService(t, repo, domain.ServiceActive, nil)
	auto := &domain.CancellationRequest{
		ServiceID: autoSvc.ID, ClientID: f.clientID,
		Mode: CancelModeImmediate, Status: domain.CancellationAutoProcessed, DecidedAt: &now,
	}
	require.NoError(t, view.Create(ctx, auto))

	// List filtered by status + service_id.
	list, total, err := view.List(ctx, ports.ListParams{Status: "accepted", ServiceID: svc.ID, Page: 1, PerPage: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	assert.Equal(t, cr.ID, list[0].ID)

	autoList, autoTotal, err := view.List(ctx, ports.ListParams{Status: "auto_processed"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, autoTotal, int64(1))
	found := false
	for _, r := range autoList {
		if r.ID == auto.ID {
			found = true
		}
	}
	assert.True(t, found)

	// Unknown id -> NOT_FOUND.
	_, err = view.GetByID(ctx, -1)
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apperr.CodeNotFound, ae.Code)
	missingUpdate := *got2
	missingUpdate.ID = -1
	assert.ErrorAs(t, view.Update(ctx, &missingUpdate), &ae)
}
