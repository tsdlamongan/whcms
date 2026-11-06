//go:build integration

// Integration tests against the real local Postgres (whmcs_e2e, migrations
// applied). Run: go test -tags integration ./internal/modules/snipe/
//
// Fixtures use uuid-suffixed names and are removed afterwards; shared or
// seeded rows are never truncated.
package snipe_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/modules/snipe"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

func intDB(t *testing.T) *db.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://root:postgres@localhost:5432/whmcs_e2e?sslmode=disable"
	}
	d, err := db.Connect(context.Background(), url)
	require.NoError(t, err, "integration tests need the local whmcs_e2e database")
	t.Cleanup(d.Close)
	return d
}

// fixtureClient inserts an own user+client pair; deleting the user cascades to
// the client and from there to its snipes and events.
func fixtureClient(t *testing.T, d *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()
	var userID int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role) VALUES ($1, 'x', 'client') RETURNING id`,
		"snipe-it-"+suffix+"@test.local").Scan(&userID))
	var clientID int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO clients (user_id, first_name, last_name) VALUES ($1, 'Snipe', 'Integration') RETURNING id`,
		userID).Scan(&clientID))
	t.Cleanup(func() {
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM domains WHERE client_id = $1`, clientID)
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})
	return clientID
}

func newSnipe(clientID int64, over func(*domain.DomainSnipe)) *domain.DomainSnipe {
	deadline := time.Now().Add(24 * time.Hour)
	s := &domain.DomainSnipe{
		ClientID:   clientID,
		Source:     domain.SnipeSourceAdmin,
		DomainName: "it-" + uuid.NewString() + ".com",
		Years:      1,
		Status:     domain.SnipeActive,
		DeadlineAt: &deadline,
	}
	if over != nil {
		over(s)
	}
	return s
}

func create(t *testing.T, repo *snipe.Repo, clientID int64, over func(*domain.DomainSnipe)) *domain.DomainSnipe {
	t.Helper()
	s := newSnipe(clientID, over)
	require.NoError(t, repo.Create(context.Background(), s))
	return s
}

func TestRepoCreateGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	clientID := fixtureClient(t, d)

	from := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	until := from.Add(time.Hour)
	in := newSnipe(clientID, func(s *domain.DomainSnipe) {
		s.Years = 3
		s.HuntFrom, s.HuntUntil = &from, &until
		s.Nameservers = []string{"ns1.example.net", "ns2.example.net"}
		s.RenewPerYear = 160000
		s.FeeAmount, s.DomainAmount = 50000, 150000
		s.RefundableDomain, s.RefundableFee = 166500, 55500
	})
	require.NoError(t, repo.Create(ctx, in))
	assert.NotZero(t, in.ID)
	assert.Equal(t, domain.SnipePhasePatient, in.Phase, "defaults are reflected back")

	got, err := repo.GetByID(ctx, in.ID)
	require.NoError(t, err)
	assert.Equal(t, in.DomainName, got.DomainName)
	assert.Equal(t, 3, got.Years)
	assert.Equal(t, domain.SnipeActive, got.Status)
	assert.Equal(t, domain.SnipeSourceAdmin, got.Source)
	assert.Equal(t, []string{"ns1.example.net", "ns2.example.net"}, got.Nameservers)
	assert.True(t, from.Equal(*got.HuntFrom))
	assert.True(t, until.Equal(*got.HuntUntil))
	assert.EqualValues(t, 160000, got.RenewPerYear)
	assert.EqualValues(t, 166500, got.RefundableDomain)
	assert.EqualValues(t, 55500, got.RefundableFee)
	assert.Empty(t, got.LeaseOwner)
	assert.Nil(t, got.LeaseUntil)
	assert.Nil(t, got.WonDomainID)

	// A bare snipe defaults to a pending, patient, client hunt of one year.
	bare := &domain.DomainSnipe{ClientID: clientID, DomainName: "it-" + uuid.NewString() + ".com"}
	require.NoError(t, repo.Create(ctx, bare))
	assert.Equal(t, domain.SnipePendingPayment, bare.Status)
	assert.Equal(t, domain.SnipeSourceClient, bare.Source)
	assert.Equal(t, 1, bare.Years)
	g2, err := repo.GetByID(ctx, bare.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{}, g2.Nameservers)

	_, err = repo.GetByID(ctx, -1)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
}

func TestRepoOneOpenHuntPerDomain(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	clientID := fixtureClient(t, d)

	first := create(t, repo, clientID, nil)

	dup := newSnipe(clientID, func(s *domain.DomainSnipe) { s.DomainName = first.DomainName; s.Status = domain.SnipePendingPayment })
	err := repo.Create(ctx, dup)
	assert.Equal(t, apperr.CodeConflict, apperr.From(err).Code, "a second open hunt for the same name is refused")

	// Once the first ends, the name is free again.
	ok, err := repo.Finish(ctx, ports.SnipeFinish{ID: first.ID, From: domain.SnipeActive, To: domain.SnipeCancelled, Now: time.Now()})
	require.NoError(t, err)
	require.True(t, ok)
	dup2 := newSnipe(clientID, func(s *domain.DomainSnipe) { s.DomainName = first.DomainName })
	require.NoError(t, repo.Create(ctx, dup2))
}

func TestRepoListFiltersAndCounts(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	a, b := fixtureClient(t, d), fixtureClient(t, d)
	tag := uuid.NewString()[:8]

	s1 := create(t, repo, a, func(s *domain.DomainSnipe) { s.DomainName = "alpha-" + tag + ".com" })
	s2 := create(t, repo, a, func(s *domain.DomainSnipe) {
		s.DomainName = "beta_" + tag + ".com"
		s.Status = domain.SnipePendingPayment
	})
	s3 := create(t, repo, b, func(s *domain.DomainSnipe) { s.DomainName = "gamma-" + tag + ".com" })

	list, total, err := repo.ListByClient(ctx, a, ports.ListParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, list, 2)
	assert.Equal(t, s2.ID, list[0].ID, "newest first")
	assert.Equal(t, s1.ID, list[1].ID)

	// A missing client id must never widen into "every client's hunts".
	for _, bad := range []int64{0, -1} {
		list, total, err = repo.ListByClient(ctx, bad, ports.ListParams{Page: 1, PerPage: 10})
		require.NoError(t, err)
		assert.Zero(t, total)
		assert.Empty(t, list)
	}

	list, total, err = repo.List(ctx, ports.ListParams{ClientID: a, Status: "pending_payment"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Equal(t, s2.ID, list[0].ID)

	list, total, err = repo.List(ctx, ports.ListParams{Search: "ALPHA-" + tag})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total, "search is case-insensitive on the lowercase name")
	assert.Equal(t, s1.ID, list[0].ID)

	// LIKE wildcards in the search text are literal: "_" must not match any char.
	_, total, err = repo.List(ctx, ports.ListParams{Search: "beta_" + tag})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	_, total, err = repo.List(ctx, ports.ListParams{Search: "betaX" + tag})
	require.NoError(t, err)
	assert.Zero(t, total)
	_, total, err = repo.List(ctx, ports.ListParams{Search: "%" + tag})
	require.NoError(t, err)
	assert.Zero(t, total, "a leading % is escaped, not a wildcard")

	// Pagination.
	list, total, err = repo.List(ctx, ports.ListParams{ClientID: a, Page: 2, PerPage: 1})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, list, 1)
	assert.Equal(t, s1.ID, list[0].ID)

	n, err := repo.CountOpenByClient(ctx, a)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	n, err = repo.CountOpenByClient(ctx, b)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	ok, err := repo.Finish(ctx, ports.SnipeFinish{ID: s3.ID, From: domain.SnipeActive, To: domain.SnipeExpired, Now: time.Now()})
	require.NoError(t, err)
	require.True(t, ok)
	n, err = repo.CountOpenByClient(ctx, b)
	require.NoError(t, err)
	assert.Zero(t, n, "a finished hunt is no longer open")

	counts, err := repo.CountByStatus(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, counts[domain.SnipeActive], 1)
	assert.GreaterOrEqual(t, counts[domain.SnipeExpired], 1)
}

func TestRepoClaimLeasesWithoutOverlap(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	clientID := fixtureClient(t, d)
	now := time.Now()

	ids := map[int64]bool{}
	for i := 0; i < 8; i++ {
		ids[create(t, repo, clientID, nil).ID] = true
	}
	// Not claimable: a pending one.
	create(t, repo, clientID, func(s *domain.DomainSnipe) { s.Status = domain.SnipePendingPayment })

	// Two workers race. Rows other tests left active may be claimed too, so
	// only our own ids are examined.
	var wg sync.WaitGroup
	var mu sync.Mutex
	claimedBy := map[int64]string{}
	claim := func(owner string) {
		defer wg.Done()
		rows, err := repo.Claim(ctx, owner, 100, now, now.Add(30*time.Second))
		assert.NoError(t, err)
		mu.Lock()
		defer mu.Unlock()
		for _, r := range rows {
			if !ids[r.ID] {
				continue
			}
			prev, dup := claimedBy[r.ID]
			assert.False(t, dup, "snipe %d claimed by both %s and %s", r.ID, prev, owner)
			claimedBy[r.ID] = owner
			assert.Equal(t, owner, r.LeaseOwner)
			require.NotNil(t, r.LeaseUntil)
		}
	}
	wg.Add(2)
	go claim("worker-a")
	go claim("worker-b")
	wg.Wait()

	// A third pass picks up whatever the two skipped, and nothing twice.
	rows, err := repo.Claim(ctx, "worker-c", 100, now, now.Add(30*time.Second))
	require.NoError(t, err)
	for _, r := range rows {
		if ids[r.ID] {
			_, dup := claimedBy[r.ID]
			assert.False(t, dup, "snipe %d was already leased", r.ID)
			claimedBy[r.ID] = "worker-c"
		}
	}
	assert.Len(t, claimedBy, 8, "every active snipe ended up leased exactly once")

	// A live lease is not stolen; an expired one is.
	again, err := repo.Claim(ctx, "worker-d", 100, now.Add(10*time.Second), now.Add(40*time.Second))
	require.NoError(t, err)
	for _, r := range again {
		assert.False(t, ids[r.ID], "snipe %d still has a live lease", r.ID)
	}
	later, err := repo.Claim(ctx, "worker-d", 100, now.Add(31*time.Second), now.Add(61*time.Second))
	require.NoError(t, err)
	reclaimed := 0
	for _, r := range later {
		if ids[r.ID] {
			reclaimed++
			assert.Equal(t, "worker-d", r.LeaseOwner)
		}
	}
	assert.Equal(t, 8, reclaimed, "expired leases are re-claimed")

	leased, err := repo.CountLeased(ctx, now.Add(32*time.Second))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, leased, 8)

	// limit is respected, and zero claims nothing.
	none, err := repo.Claim(ctx, "worker-e", 0, now, now.Add(time.Minute))
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestRepoHeartbeatReleaseAndFencing(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	clientID := fixtureClient(t, d)
	now := time.Now().Truncate(time.Microsecond)

	s := create(t, repo, clientID, nil)
	rows, err := repo.Claim(ctx, "owner-1", 1000, now, now.Add(30*time.Second))
	require.NoError(t, err)
	require.Contains(t, ids(rows), s.ID)

	last := now.Add(time.Second)
	hb := ports.SnipeHeartbeat{
		ID: s.ID, Owner: "owner-1", LeaseUntil: now.Add(60 * time.Second),
		Attempts: 42, Phase: domain.SnipePhaseBurst, LastAttemptAt: &last, LastError: "still taken",
	}
	ok, err := repo.Heartbeat(ctx, hb)
	require.NoError(t, err)
	assert.True(t, ok)
	got, err := repo.GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 42, got.Attempts)
	assert.Equal(t, domain.SnipePhaseBurst, got.Phase)
	assert.Equal(t, "still taken", got.LastError)
	assert.True(t, last.Equal(*got.LastAttemptAt))
	assert.True(t, now.Add(60*time.Second).Equal(*got.LeaseUntil))

	// A nil LastAttemptAt keeps the previous value.
	hb.LastAttemptAt = nil
	hb.Attempts = 43
	ok, err = repo.Heartbeat(ctx, hb)
	require.NoError(t, err)
	assert.True(t, ok)
	got, _ = repo.GetByID(ctx, s.ID)
	assert.True(t, last.Equal(*got.LastAttemptAt))

	// Someone else's lease: refused.
	hb.Owner = "intruder"
	ok, err = repo.Heartbeat(ctx, hb)
	require.NoError(t, err)
	assert.False(t, ok)

	// Release only works for the owner.
	require.NoError(t, repo.Release(ctx, s.ID, "intruder"))
	got, _ = repo.GetByID(ctx, s.ID)
	assert.Equal(t, "owner-1", got.LeaseOwner)
	require.NoError(t, repo.Release(ctx, s.ID, "owner-1"))
	got, _ = repo.GetByID(ctx, s.ID)
	assert.Empty(t, got.LeaseOwner)
	assert.Nil(t, got.LeaseUntil)

	// A heartbeat on a finished snipe is refused.
	ok, err = repo.Finish(ctx, ports.SnipeFinish{ID: s.ID, From: domain.SnipeActive, To: domain.SnipeCancelled, Now: now})
	require.NoError(t, err)
	require.True(t, ok)
	hb.Owner = "owner-1"
	ok, err = repo.Heartbeat(ctx, hb)
	require.NoError(t, err)
	assert.False(t, ok)
}

func ids(rows []domain.DomainSnipe) []int64 {
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func TestRepoFinishIsACompareAndSwap(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	clientID := fixtureClient(t, d)
	now := time.Now().Truncate(time.Microsecond)

	t.Run("wrong from-status changes nothing", func(t *testing.T) {
		s := create(t, repo, clientID, nil)
		ok, err := repo.Finish(ctx, ports.SnipeFinish{ID: s.ID, From: domain.SnipePendingPayment, To: domain.SnipeCancelled, Now: now})
		require.NoError(t, err)
		assert.False(t, ok)
		got, _ := repo.GetByID(ctx, s.ID)
		assert.Equal(t, domain.SnipeActive, got.Status)
	})

	t.Run("only one of two racing writers wins", func(t *testing.T) {
		s := create(t, repo, clientID, nil)
		var wg sync.WaitGroup
		results := make([]bool, 2)
		for i, to := range []domain.SnipeStatus{domain.SnipeCancelled, domain.SnipeExpired} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := repo.Finish(ctx, ports.SnipeFinish{ID: s.ID, From: domain.SnipeActive, To: to, Now: now})
				assert.NoError(t, err)
				results[i] = ok
			}()
		}
		wg.Wait()
		assert.NotEqual(t, results[0], results[1], "exactly one transition applied")
	})

	t.Run("lease fencing", func(t *testing.T) {
		s := create(t, repo, clientID, nil)
		_, err := repo.Claim(ctx, "owner-1", 1000, now, now.Add(30*time.Second))
		require.NoError(t, err)

		ok, err := repo.Finish(ctx, ports.SnipeFinish{ID: s.ID, From: domain.SnipeActive, To: domain.SnipeExpired, Owner: "intruder", Now: now})
		require.NoError(t, err)
		assert.False(t, ok, "not the lease owner")

		ok, err = repo.Finish(ctx, ports.SnipeFinish{ID: s.ID, From: domain.SnipeActive, To: domain.SnipeExpired, RequireIdle: true, Now: now})
		require.NoError(t, err)
		assert.False(t, ok, "a live lease blocks the idle sweep")

		ok, err = repo.Finish(ctx, ports.SnipeFinish{
			ID: s.ID, From: domain.SnipeActive, To: domain.SnipeExpired, Owner: "owner-1", Now: now,
			RefundAmount: 1234, LastError: "window closed", Attempts: 77,
		})
		require.NoError(t, err)
		require.True(t, ok)
		got, _ := repo.GetByID(ctx, s.ID)
		assert.Equal(t, domain.SnipeExpired, got.Status)
		assert.EqualValues(t, 1234, got.RefundAmount)
		assert.EqualValues(t, 77, got.Attempts, "the hunt's final count is persisted on finish")
		assert.Equal(t, "window closed", got.LastError)
		assert.Empty(t, got.LeaseOwner, "finishing frees the lease")
		assert.Nil(t, got.LeaseUntil)
		assert.Nil(t, got.WonAt)
	})

	t.Run("a lower final count never reduces the stored one", func(t *testing.T) {
		s := create(t, repo, clientID, nil)
		_, err := repo.Claim(ctx, "owner-1", 1000, now, now.Add(30*time.Second))
		require.NoError(t, err)
		ok, err := repo.Heartbeat(ctx, ports.SnipeHeartbeat{
			ID: s.ID, Owner: "owner-1", LeaseUntil: now.Add(time.Minute), Attempts: 50, Phase: domain.SnipePhaseBurst,
		})
		require.NoError(t, err)
		require.True(t, ok)
		ok, err = repo.Finish(ctx, ports.SnipeFinish{ID: s.ID, From: domain.SnipeActive, To: domain.SnipeCancelled, Now: now, Attempts: 10})
		require.NoError(t, err)
		require.True(t, ok)
		got, _ := repo.GetByID(ctx, s.ID)
		assert.EqualValues(t, 50, got.Attempts)
	})

	t.Run("the idle sweep takes an expired lease", func(t *testing.T) {
		s := create(t, repo, clientID, nil)
		_, err := repo.Claim(ctx, "owner-1", 1000, now, now.Add(30*time.Second))
		require.NoError(t, err)
		ok, err := repo.Finish(ctx, ports.SnipeFinish{
			ID: s.ID, From: domain.SnipeActive, To: domain.SnipeExpired, RequireIdle: true, Now: now.Add(time.Minute),
		})
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("won records when and which domain", func(t *testing.T) {
		s := create(t, repo, clientID, nil)
		reg, err := domains.NewRegistrarRepo(d).GetByName(ctx, "rdash")
		require.NoError(t, err)
		dom := &domain.Domain{ClientID: clientID, RegistrarID: reg.ID, Name: s.DomainName, Status: domain.DomainActive}
		require.NoError(t, domains.NewRepo(d).Create(ctx, dom))

		ok, err := repo.Finish(ctx, ports.SnipeFinish{
			ID: s.ID, From: domain.SnipeActive, To: domain.SnipeWon, Now: now, WonDomainID: &dom.ID,
		})
		require.NoError(t, err)
		require.True(t, ok)
		got, _ := repo.GetByID(ctx, s.ID)
		assert.Equal(t, domain.SnipeWon, got.Status)
		require.NotNil(t, got.WonAt)
		assert.True(t, now.Equal(*got.WonAt))
		require.NotNil(t, got.WonDomainID)
		assert.Equal(t, dom.ID, *got.WonDomainID)
	})
}

func TestRepoExpirableRefundAndCustomer(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	clientID := fixtureClient(t, d)
	now := time.Now()

	overdue := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	due := create(t, repo, clientID, func(s *domain.DomainSnipe) { s.DeadlineAt = &overdue })
	notDue := create(t, repo, clientID, func(s *domain.DomainSnipe) { s.DeadlineAt = &future })
	leased := create(t, repo, clientID, func(s *domain.DomainSnipe) { s.DeadlineAt = &overdue })
	require.NoError(t, repo.SetCustomerID(ctx, due.ID, 501))
	// Lease just `leased`: claim everything, then free the other two.
	rows, err := repo.Claim(ctx, "w", 1000, now, now.Add(time.Minute))
	require.NoError(t, err)
	for _, r := range rows {
		if r.ID == due.ID || r.ID == notDue.ID {
			require.NoError(t, repo.Release(ctx, r.ID, "w"))
		}
	}

	list, err := repo.ListExpirable(ctx, now, 1000)
	require.NoError(t, err)
	got := ids(list)
	assert.Contains(t, got, due.ID, "overdue and idle")
	assert.NotContains(t, got, notDue.ID, "deadline not reached")
	assert.NotContains(t, got, leased.ID, "a live hunt expires itself")

	cached, err := repo.GetByID(ctx, due.ID)
	require.NoError(t, err)
	require.NotNil(t, cached.RegistrarCustomerID)
	assert.EqualValues(t, 501, *cached.RegistrarCustomerID)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.SetCustomerID(ctx, -1, 1)).Code)

	refund := time.Now().Truncate(time.Microsecond)
	require.NoError(t, repo.MarkRefunded(ctx, due.ID, refund))
	after, _ := repo.GetByID(ctx, due.ID)
	require.NotNil(t, after.RefundedAt)
	assert.True(t, refund.Equal(*after.RefundedAt))
}

func TestRepoEvents(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	s := create(t, repo, fixtureClient(t, d), nil)

	for _, kind := range []string{"created", "phase", "error", "won"} {
		e := &domain.SnipeEvent{SnipeID: s.ID, Kind: kind, Message: "m-" + kind}
		require.NoError(t, repo.AddEvent(ctx, e))
		assert.NotZero(t, e.ID)
		assert.False(t, e.CreatedAt.IsZero())
	}
	got, err := repo.ListEvents(ctx, s.ID, 3)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "won", got[0].Kind, "newest first")
	assert.Equal(t, "error", got[1].Kind)

	assert.Error(t, repo.AddEvent(ctx, &domain.SnipeEvent{SnipeID: -1, Kind: "x"}), "events need a real snipe")
}

func TestMigrationAllowsSnipeInvoiceItems(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	var def string
	require.NoError(t, d.Querier(ctx).QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'invoice_items_related_type_check'`).Scan(&def))
	assert.Contains(t, def, "domain_snipe")
}

// fixtureInvoice inserts a bare invoice for the client (due dueInDays from
// today, negative = past) and removes it again before the client goes: the
// invoices table has no cascade from clients.
func fixtureInvoice(t *testing.T, d *db.DB, clientID int64, status string, dueInDays int) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO invoices (invoice_number, client_id, status, due_date)
		VALUES ($1, $2, $3, current_date + $4::int) RETURNING id`,
		"IT-"+uuid.NewString(), clientID, status, dueInDays).Scan(&id))
	t.Cleanup(func() {
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM invoices WHERE id = $1`, id)
	})
	return id
}

func TestRepoActivateRestartsTheDeadlineFromThePayment(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	clientID := fixtureClient(t, d)

	// A pending snipe sold with a 10-day window (deadline provisional at creation).
	provisional := time.Now().Add(10 * 24 * time.Hour)
	s := create(t, repo, clientID, func(s *domain.DomainSnipe) {
		s.Status = domain.SnipePendingPayment
		s.Source = domain.SnipeSourceClient
		s.DeadlineAt = &provisional
	})

	paidAt := time.Now().Add(26 * time.Hour).Truncate(time.Microsecond) // pays a day late
	ok, err := repo.Activate(ctx, s.ID, paidAt)
	require.NoError(t, err)
	require.True(t, ok)

	got, err := repo.GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.SnipeActive, got.Status)
	require.NotNil(t, got.PaidAt)
	assert.True(t, paidAt.Equal(*got.PaidAt))
	require.NotNil(t, got.DeadlineAt)
	assert.WithinDuration(t, paidAt.Add(10*24*time.Hour), *got.DeadlineAt, 5*time.Second,
		"a late payer still gets the plan's full 10 days")

	// A replay (already active) and a finished snipe are untouched.
	ok, err = repo.Activate(ctx, s.ID, paidAt.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, ok)
	again, _ := repo.GetByID(ctx, s.ID)
	assert.True(t, got.DeadlineAt.Equal(*again.DeadlineAt))

	cancelled := create(t, repo, clientID, func(s *domain.DomainSnipe) { s.Status = domain.SnipePendingPayment })
	fin, err := repo.Finish(ctx, ports.SnipeFinish{ID: cancelled.ID, From: domain.SnipePendingPayment, To: domain.SnipeCancelled, Now: time.Now()})
	require.NoError(t, err)
	require.True(t, fin)
	ok, err = repo.Activate(ctx, cancelled.ID, paidAt)
	require.NoError(t, err)
	assert.False(t, ok, "a cancelled hunt is never revived by a late payment")

	// No deadline at all falls back to 30 days.
	bare := create(t, repo, clientID, func(s *domain.DomainSnipe) {
		s.Status = domain.SnipePendingPayment
		s.DeadlineAt = nil
	})
	ok, err = repo.Activate(ctx, bare.ID, paidAt)
	require.NoError(t, err)
	require.True(t, ok)
	b, _ := repo.GetByID(ctx, bare.ID)
	assert.WithinDuration(t, paidAt.Add(30*24*time.Hour), *b.DeadlineAt, time.Second)
}

func TestRepoSetInvoiceAndRecordRefund(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	clientID := fixtureClient(t, d)
	invoiceID := fixtureInvoice(t, d, clientID, "unpaid", 1)

	s := create(t, repo, clientID, func(s *domain.DomainSnipe) { s.Status = domain.SnipePendingPayment })
	require.NoError(t, repo.SetInvoice(ctx, s.ID, invoiceID, 166_500, 55_500))
	got, err := repo.GetByID(ctx, s.ID)
	require.NoError(t, err)
	require.NotNil(t, got.InvoiceID)
	assert.Equal(t, invoiceID, *got.InvoiceID)
	assert.EqualValues(t, 166_500, got.RefundableDomain)
	assert.EqualValues(t, 55_500, got.RefundableFee)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.SetInvoice(ctx, -1, invoiceID, 1, 1)).Code)

	// A refund can only be recorded on an ended snipe, and only once.
	at := time.Now().Truncate(time.Microsecond)
	ok, err := repo.RecordRefund(ctx, s.ID, 222_000, at)
	require.NoError(t, err)
	assert.False(t, ok, "still pending: nothing to refund yet")

	fin, err := repo.Finish(ctx, ports.SnipeFinish{ID: s.ID, From: domain.SnipePendingPayment, To: domain.SnipeCancelled, Now: at})
	require.NoError(t, err)
	require.True(t, fin)
	ok, err = repo.RecordRefund(ctx, s.ID, 222_000, at)
	require.NoError(t, err)
	assert.True(t, ok)
	got, _ = repo.GetByID(ctx, s.ID)
	assert.EqualValues(t, 222_000, got.RefundAmount)
	require.NotNil(t, got.RefundedAt)
	assert.True(t, at.Equal(*got.RefundedAt))

	ok, err = repo.RecordRefund(ctx, s.ID, 222_000, at)
	require.NoError(t, err)
	assert.False(t, ok, "a replayed payment cannot be refunded twice")
}

func TestRepoListStalePending(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewRepo(d)
	clientID := fixtureClient(t, d)

	pendingWith := func(status string, dueInDays int) int64 {
		inv := fixtureInvoice(t, d, clientID, status, dueInDays)
		s := create(t, repo, clientID, func(s *domain.DomainSnipe) {
			s.Status = domain.SnipePendingPayment
			s.Source = domain.SnipeSourceClient
		})
		require.NoError(t, repo.SetInvoice(ctx, s.ID, inv, 100, 0))
		return s.ID
	}
	cancelled := pendingWith("cancelled", 5)
	overdueYesterday := pendingWith("unpaid", -1)
	overdueLong := pendingWith("overdue", -3)
	dueToday := pendingWith("unpaid", 0)
	dueTomorrow := pendingWith("unpaid", 1)
	paid := pendingWith("paid", -2)

	// An ACTIVE snipe whose invoice is cancelled is not this sweep's business.
	activeInv := fixtureInvoice(t, d, clientID, "cancelled", 0)
	active := create(t, repo, clientID, nil)
	require.NoError(t, repo.SetInvoice(ctx, active.ID, activeInv, 1, 1))

	// A pending snipe with no invoice at all is never selected (nothing to judge by).
	noInvoice := create(t, repo, clientID, func(s *domain.DomainSnipe) { s.Status = domain.SnipePendingPayment })

	rows, err := repo.ListStalePending(ctx, time.Now(), 1000)
	require.NoError(t, err)
	got := ids(rows)
	assert.Contains(t, got, cancelled, "its invoice was cancelled")
	assert.Contains(t, got, overdueYesterday, "overdue for a full day")
	assert.Contains(t, got, overdueLong)
	assert.NotContains(t, got, dueToday, "due today is still payable")
	assert.NotContains(t, got, dueTomorrow)
	assert.NotContains(t, got, paid, "a paid invoice is never stale")
	assert.NotContains(t, got, active.ID)
	assert.NotContains(t, got, noInvoice.ID)

	limited, err := repo.ListStalePending(ctx, time.Now(), 1)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(limited), 1)
}

func TestPlanRepo(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := snipe.NewPlanRepo(d)

	mk := func(name string, sort int, active bool) *domain.SnipePlan {
		p := &domain.SnipePlan{Name: name + "-" + uuid.NewString()[:8], WindowDays: 30, Fee: 50_000, SuccessFee: 5_000, Active: active, Sort: sort}
		require.NoError(t, repo.Create(ctx, p))
		t.Cleanup(func() { _, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM snipe_plans WHERE id = $1`, p.ID) })
		return p
	}
	// Sort values far outside anything else the suite uses so ordering is unambiguous.
	b := mk("b", 900_002, true)
	a := mk("a", 900_001, true)
	hidden := mk("c", 900_003, false)

	assert.NotZero(t, a.ID)
	assert.False(t, a.CreatedAt.IsZero())

	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, a.Name, got.Name)
	assert.Equal(t, 30, got.WindowDays)
	assert.EqualValues(t, 50_000, got.Fee)
	assert.EqualValues(t, 5_000, got.SuccessFee)
	assert.True(t, got.Active)
	_, err = repo.GetByID(ctx, -1)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)

	pick := func(list []domain.SnipePlan) []int64 {
		var out []int64
		for _, p := range list {
			if p.ID == a.ID || p.ID == b.ID || p.ID == hidden.ID {
				out = append(out, p.ID)
			}
		}
		return out
	}
	all, err := repo.List(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, []int64{a.ID, b.ID, hidden.ID}, pick(all), "ordered by sort, includes deactivated")
	active, err := repo.List(ctx, true)
	require.NoError(t, err)
	assert.Equal(t, []int64{a.ID, b.ID}, pick(active), "activeOnly hides deactivated plans")

	a.Name, a.WindowDays, a.Fee, a.SuccessFee, a.Active, a.Sort = "renamed-"+uuid.NewString()[:6], 90, 75_000, 0, false, 900_004
	require.NoError(t, repo.Update(ctx, a))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, a.Name, got.Name)
	assert.Equal(t, 90, got.WindowDays)
	assert.EqualValues(t, 75_000, got.Fee)
	assert.False(t, got.Active)
	assert.Equal(t, 900_004, got.Sort)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.Update(ctx, &domain.SnipePlan{ID: -1, Name: "x", WindowDays: 1})).Code)

	// A plan's price is frozen on the snipe, so removing the plan keeps the snipe.
	clientID := fixtureClient(t, d)
	planID := b.ID
	s := create(t, snipe.NewRepo(d), clientID, func(s *domain.DomainSnipe) { s.PlanID = &planID })
	_, err = d.Querier(ctx).Exec(ctx, `DELETE FROM snipe_plans WHERE id = $1`, b.ID)
	require.NoError(t, err)
	kept, err := snipe.NewRepo(d).GetByID(ctx, s.ID)
	require.NoError(t, err)
	assert.Nil(t, kept.PlanID)
}
