//go:build integration

// Integration tests against the real local Postgres (whmcs DB, migrations
// applied). Run: go test -tags integration ./internal/modules/domains/
//
// Fixtures are created with uuid-suffixed identifiers and cleaned up; shared
// or seeded rows are never truncated or deleted.
package domains_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intDB(t *testing.T) *db.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://root:postgres@localhost:5432/whmcs_e2e?sslmode=disable"
	}
	d, err := db.Connect(context.Background(), url)
	require.NoError(t, err, "integration tests need the local whmcs database")
	t.Cleanup(d.Close)
	return d
}

// fixtureClient inserts an own user+client pair and removes them (and any
// remaining owned domains, via explicit cleanup order) afterwards.
func fixtureClient(t *testing.T, d *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()

	var userID int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role)
		VALUES ($1, 'x', 'client') RETURNING id`,
		"domains-it-"+suffix+"@test.local").Scan(&userID))

	var clientID int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO clients (user_id, first_name, last_name)
		VALUES ($1, 'Domains', 'Integration') RETURNING id`,
		userID).Scan(&clientID))

	t.Cleanup(func() {
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM domains WHERE client_id = $1`, clientID)
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) // cascades to client
	})
	return clientID
}

func rdashID(t *testing.T, d *db.DB) int64 {
	t.Helper()
	reg, err := domains.NewRegistrarRepo(d).GetByName(context.Background(), "rdash")
	require.NoError(t, err, "seeded rdash registrar row expected")
	return reg.ID
}

func fixtureDomain(t *testing.T, repo *domains.Repo, clientID, registrarID int64, over func(*domain.Domain)) *domain.Domain {
	t.Helper()
	dom := &domain.Domain{
		ClientID:        clientID,
		RegistrarID:     registrarID,
		Name:            "it-" + uuid.NewString() + ".com",
		RecurringAmount: 150000,
		AutoRenew:       true,
		Nameservers:     json.RawMessage(`["ns1.test.id","ns2.test.id"]`),
	}
	if over != nil {
		over(dom)
	}
	require.NoError(t, repo.Create(context.Background(), dom))
	return dom
}

func TestDomainRepoCreateGet(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)
	clientID := fixtureClient(t, d)
	regID := rdashID(t, d)

	dom := fixtureDomain(t, repo, clientID, regID, nil)
	assert.Positive(t, dom.ID)
	assert.Equal(t, domain.DomainPending, dom.Status)       // default applied
	assert.Equal(t, domain.CycleAnnually, dom.BillingCycle) // default applied

	got, err := repo.GetByID(ctx, dom.ID)
	require.NoError(t, err)
	assert.Equal(t, dom.Name, got.Name)
	assert.EqualValues(t, 150000, got.RecurringAmount)
	assert.JSONEq(t, `["ns1.test.id","ns2.test.id"]`, string(got.Nameservers))

	byName, err := repo.GetByName(ctx, dom.Name)
	require.NoError(t, err)
	assert.Equal(t, dom.ID, byName.ID)

	_, err = repo.GetByID(ctx, -1)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	_, err = repo.GetByName(ctx, "does-not-exist-"+uuid.NewString()+".com")
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)

	// Unique among non-cancelled names -> CONFLICT.
	dup := *dom
	dup.ID = 0
	err = repo.Create(ctx, &dup)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeConflict, apperr.From(err).Code)

	// After cancelling, the name may be re-registered.
	require.NoError(t, repo.UpdateStatus(ctx, dom.ID, domain.DomainCancelled))
	redo := *dom
	redo.ID = 0
	redo.Status = domain.DomainPending
	require.NoError(t, repo.Create(ctx, &redo))
}

func TestDomainRepoUpdate(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)
	clientID := fixtureClient(t, d)
	regID := rdashID(t, d)

	dom := fixtureDomain(t, repo, clientID, regID, nil)
	reg := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	exp := reg.AddDate(1, 0, 0)
	dom.Status = domain.DomainActive
	dom.RegistrationDate = &reg
	dom.ExpiryDate = &exp
	dom.NextDueDate = &exp
	dom.RecurringAmount = 200000
	dom.AutoRenew = false
	dom.EPPCodeEnc = "enc:test"
	dom.RegistrarMeta = json.RawMessage(`{"order_id":"RD-9"}`)
	dom.DNSManagementEnabled = true
	dom.EmailForwardingEnabled = true
	require.NoError(t, repo.Update(ctx, dom))

	got, err := repo.GetByID(ctx, dom.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.DomainActive, got.Status)
	require.NotNil(t, got.ExpiryDate)
	assert.Equal(t, exp.Format("2006-01-02"), got.ExpiryDate.Format("2006-01-02"))
	assert.EqualValues(t, 200000, got.RecurringAmount)
	assert.False(t, got.AutoRenew)
	assert.Equal(t, "enc:test", got.EPPCodeEnc)
	assert.Contains(t, string(got.RegistrarMeta), "RD-9")
	assert.True(t, got.DNSManagementEnabled)
	assert.True(t, got.EmailForwardingEnabled)

	// Missing row -> NOT_FOUND.
	missing := *dom
	missing.ID = -1
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.Update(ctx, &missing)).Code)
	assert.Equal(t, apperr.CodeNotFound,
		apperr.From(repo.UpdateStatus(ctx, -1, domain.DomainActive)).Code)
}

func TestDomainRepoLists(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)
	clientID := fixtureClient(t, d)
	regID := rdashID(t, d)

	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	nextYear := time.Now().UTC().AddDate(1, 0, 0)

	due := fixtureDomain(t, repo, clientID, regID, func(x *domain.Domain) {
		x.Status = domain.DomainActive
		x.NextDueDate = &yesterday
	})
	notDue := fixtureDomain(t, repo, clientID, regID, func(x *domain.Domain) {
		x.Status = domain.DomainActive
		x.NextDueDate = &nextYear
	})
	noAutoRenew := fixtureDomain(t, repo, clientID, regID, func(x *domain.Domain) {
		x.Status = domain.DomainActive
		x.NextDueDate = &yesterday
		x.AutoRenew = false
	})
	cancelled := fixtureDomain(t, repo, clientID, regID, func(x *domain.Domain) {
		x.Status = domain.DomainCancelled
	})

	// ListByClient sees all four fixtures.
	items, total, err := repo.ListByClient(ctx, clientID, ports.ListParams{PerPage: 100})
	require.NoError(t, err)
	assert.EqualValues(t, 4, total)
	assert.Len(t, items, 4)

	// Status filter.
	_, total, err = repo.ListByClient(ctx, clientID, ports.ListParams{Status: "cancelled"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// Search filter (unique name substring).
	_, total, err = repo.List(ctx, ports.ListParams{Search: due.Name[:20]})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// Pagination.
	page1, total, err := repo.ListByClient(ctx, clientID, ports.ListParams{Page: 1, PerPage: 2})
	require.NoError(t, err)
	assert.EqualValues(t, 4, total)
	assert.Len(t, page1, 2)

	// ListRenewalsDue: only active + auto_renew + due.
	dueList, err := repo.ListRenewalsDue(ctx, time.Now().UTC())
	require.NoError(t, err)
	ids := map[int64]bool{}
	for _, x := range dueList {
		ids[x.ID] = true
	}
	assert.True(t, ids[due.ID])
	assert.False(t, ids[notDue.ID])
	assert.False(t, ids[noAutoRenew.ID], "auto_renew=false excluded")
	assert.False(t, ids[cancelled.ID])

	// ListForSync excludes cancelled and honors the limit.
	syncList, err := repo.ListForSync(ctx, 1000)
	require.NoError(t, err)
	syncIDs := map[int64]bool{}
	for _, x := range syncList {
		syncIDs[x.ID] = true
	}
	assert.True(t, syncIDs[due.ID])
	assert.False(t, syncIDs[cancelled.ID])

	one, err := repo.ListForSync(ctx, 1)
	require.NoError(t, err)
	assert.Len(t, one, 1)
}

func TestDomainRepoGetByIDForUpdateAndGetByIDs(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)
	clientID := fixtureClient(t, d)
	regID := rdashID(t, d)

	dom1 := fixtureDomain(t, repo, clientID, regID, nil)
	dom2 := fixtureDomain(t, repo, clientID, regID, nil)

	locked, err := repo.GetByIDForUpdate(ctx, dom1.ID)
	require.NoError(t, err)
	assert.Equal(t, dom1.Name, locked.Name)

	_, err = repo.GetByIDForUpdate(ctx, -1)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)

	list, err := repo.GetByIDs(ctx, []int64{dom1.ID, dom2.ID, -1})
	require.NoError(t, err)
	ids := map[int64]bool{}
	for _, x := range list {
		ids[x.ID] = true
	}
	assert.True(t, ids[dom1.ID])
	assert.True(t, ids[dom2.ID])
	assert.Len(t, list, 2, "unknown id is simply omitted")

	empty, err := repo.GetByIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestDomainRepoJoinsTransaction(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)
	clientID := fixtureClient(t, d)
	regID := rdashID(t, d)
	tm := db.NewTxManager(d)

	name := "it-tx-" + uuid.NewString() + ".com"
	err := tm.WithinTx(ctx, func(txCtx context.Context) error {
		dom := &domain.Domain{ClientID: clientID, RegistrarID: regID, Name: name}
		if err := repo.Create(txCtx, dom); err != nil {
			return err
		}
		return assert.AnError // force rollback
	})
	require.ErrorIs(t, err, assert.AnError)

	_, err = repo.GetByName(ctx, name)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code, "rollback must discard the insert")
}

func TestRegistrarRepo(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRegistrarRepo(d)

	reg, err := repo.GetByName(ctx, "rdash")
	require.NoError(t, err)
	assert.Equal(t, "rdash", reg.Name)

	byID, err := repo.GetByID(ctx, reg.ID)
	require.NoError(t, err)
	assert.Equal(t, reg.Name, byID.Name)

	list, err := repo.List(ctx)
	require.NoError(t, err)
	found := false
	for _, r := range list {
		if r.Name == "rdash" {
			found = true
		}
	}
	assert.True(t, found)

	// Update config and restore the original value afterwards.
	original := *reg
	t.Cleanup(func() { _ = repo.Update(ctx, &original) })

	marker := uuid.NewString()
	reg.Config = json.RawMessage(`{"it_marker":"` + marker + `"}`)
	reg.ResellerID = "test-reseller-1"
	reg.APIKeyEnc = "ciphertext-" + marker
	reg.BaseURL = "https://mock-endpoint-" + marker + ".test/v1"
	require.NoError(t, repo.Update(ctx, reg))
	got, err := repo.GetByID(ctx, reg.ID)
	require.NoError(t, err)
	assert.Contains(t, string(got.Config), marker)
	assert.Equal(t, "test-reseller-1", got.ResellerID)
	assert.Equal(t, "ciphertext-"+marker, got.APIKeyEnc)
	assert.Equal(t, "https://mock-endpoint-"+marker+".test/v1", got.BaseURL)

	// Missing row -> NOT_FOUND.
	_, err = repo.GetByID(ctx, -1)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	missing := *reg
	missing.ID = -1
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.Update(ctx, &missing)).Code)
}

func TestTLDPricingRepo(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewTLDPricingRepo(d)
	rdash, err := domains.NewRegistrarRepo(d).GetByName(ctx, "rdash")
	require.NoError(t, err)

	marker := uuid.NewString()[:8]
	tld := marker + ".test"
	p := &domain.TLDPricing{
		TLD: tld, RegistrarID: rdash.ID, Active: true, MinYears: 1, MaxYears: 5,
		RegisterPrices: map[string]int64{"1": 150000, "2": 290000},
		RenewPrices:    map[string]int64{"1": 160000},
		TransferPrice:  120000,
		RestorePrice:   500000,
	}
	require.NoError(t, repo.Create(ctx, p))
	require.NotZero(t, p.ID)
	t.Cleanup(func() { _ = repo.Delete(ctx, p.ID) })

	byID, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, tld, byID.TLD)
	assert.Equal(t, map[string]int64{"1": 150000, "2": 290000}, byID.RegisterPrices)
	assert.EqualValues(t, 120000, byID.TransferPrice)
	assert.EqualValues(t, 500000, byID.RestorePrice)

	byTLD, err := repo.GetByTLD(ctx, tld)
	require.NoError(t, err)
	assert.Equal(t, p.ID, byTLD.ID)

	active, err := repo.ListActive(ctx)
	require.NoError(t, err)
	found := false
	for _, r := range active {
		if r.TLD == tld {
			found = true
		}
	}
	assert.True(t, found)

	p.Active = false
	p.RenewPrices = map[string]int64{"1": 170000}
	p.RestorePrice = 600000
	require.NoError(t, repo.Update(ctx, p))
	got, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	assert.False(t, got.Active)
	assert.Equal(t, map[string]int64{"1": 170000}, got.RenewPrices)
	assert.EqualValues(t, 600000, got.RestorePrice)

	active, err = repo.ListActive(ctx)
	require.NoError(t, err)
	for _, r := range active {
		assert.NotEqual(t, tld, r.TLD, "deactivated row must not appear in ListActive")
	}

	list, err := repo.List(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, list)

	// Duplicate tld -> CONFLICT.
	dup := &domain.TLDPricing{TLD: tld, RegistrarID: rdash.ID, MinYears: 1, MaxYears: 1}
	assert.Equal(t, apperr.CodeConflict, apperr.From(repo.Create(ctx, dup)).Code)

	require.NoError(t, repo.Delete(ctx, p.ID))
	_, err = repo.GetByID(ctx, p.ID)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.Delete(ctx, p.ID)).Code)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.Update(ctx, p)).Code)
}

func TestPremiumDomainPricingRepo(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewPremiumDomainPricingRepo(d)

	name := uuid.NewString()[:8] + ".test"
	p := &domain.PremiumDomainPricing{DomainName: name, RegisterPrice: 5000000, RenewPrice: 4500000, TransferPrice: 3000000}
	require.NoError(t, repo.Create(ctx, p))
	require.NotZero(t, p.ID)
	t.Cleanup(func() { _ = repo.Delete(ctx, p.ID) })

	got, err := repo.GetByName(ctx, name)
	require.NoError(t, err)
	assert.EqualValues(t, 5000000, got.RegisterPrice)

	list, err := repo.List(ctx)
	require.NoError(t, err)
	found := false
	for _, r := range list {
		if r.DomainName == name {
			found = true
		}
	}
	assert.True(t, found)

	p.RegisterPrice = 6000000
	require.NoError(t, repo.Update(ctx, p))
	got, err = repo.GetByName(ctx, name)
	require.NoError(t, err)
	assert.EqualValues(t, 6000000, got.RegisterPrice)

	dup := &domain.PremiumDomainPricing{DomainName: name}
	assert.Equal(t, apperr.CodeConflict, apperr.From(repo.Create(ctx, dup)).Code)

	require.NoError(t, repo.Delete(ctx, p.ID))
	_, err = repo.GetByName(ctx, name)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.Delete(ctx, p.ID)).Code)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.Update(ctx, p)).Code)
}

func TestPremiumLengthPricingRepo(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewPremiumLengthPricingRepo(d)

	tld := uuid.NewString()[:8] + ".test"
	p := &domain.PremiumLengthPricing{TLD: tld, CharLength: 2, Price: 485000000}
	require.NoError(t, repo.Create(ctx, p))
	require.NotZero(t, p.ID)
	t.Cleanup(func() { _ = repo.Delete(ctx, p.ID) })

	got, err := repo.GetByTLDAndLength(ctx, tld, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 485000000, got.Price)

	_, err = repo.GetByTLDAndLength(ctx, tld, 3)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)

	list, err := repo.List(ctx)
	require.NoError(t, err)
	found := false
	for _, r := range list {
		if r.TLD == tld && r.CharLength == 2 {
			found = true
		}
	}
	assert.True(t, found)

	p.Price = 500000000
	require.NoError(t, repo.Update(ctx, p))
	got, err = repo.GetByTLDAndLength(ctx, tld, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 500000000, got.Price)

	// Duplicate (tld, char_length) -> CONFLICT.
	dup := &domain.PremiumLengthPricing{TLD: tld, CharLength: 2}
	assert.Equal(t, apperr.CodeConflict, apperr.From(repo.Create(ctx, dup)).Code)

	require.NoError(t, repo.Delete(ctx, p.ID))
	_, err = repo.GetByTLDAndLength(ctx, tld, 2)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.Delete(ctx, p.ID)).Code)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.Update(ctx, p)).Code)
}

func TestDomainAddonRepo(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewDomainAddonRepo(d)

	seeded, err := repo.GetByKey(ctx, "dns_management")
	require.NoError(t, err)
	assert.Equal(t, "dns_management", seeded.Key)
	original := *seeded
	t.Cleanup(func() { _ = repo.Update(ctx, &original) })

	seeded.Price = 25000
	seeded.Active = true
	require.NoError(t, repo.Update(ctx, seeded))
	got, err := repo.GetByKey(ctx, "dns_management")
	require.NoError(t, err)
	assert.EqualValues(t, 25000, got.Price)
	assert.True(t, got.Active)

	active, err := repo.ListActive(ctx)
	require.NoError(t, err)
	found := false
	for _, a := range active {
		if a.Key == "dns_management" {
			found = true
		}
	}
	assert.True(t, found)

	list, err := repo.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 3, "the fixed seeded catalog: id_protection, dns_management, email_forwarding")

	_, err = repo.GetByKey(ctx, "not_a_real_addon")
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	missing := domain.DomainAddon{ID: -1}
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.Update(ctx, &missing)).Code)
}
