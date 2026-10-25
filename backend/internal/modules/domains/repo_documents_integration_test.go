//go:build integration

package domains_test

import (
	"context"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// namesOf returns the set of domain names in list.
func namesOf(list []domain.Domain) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, d := range list {
		out[d.Name] = true
	}
	return out
}

func TestDomainRepoVerificationColumnsRoundTrip(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)
	clientID := fixtureClient(t, d)
	regID := rdashID(t, d)

	rejectedAt := time.Date(2026, 10, 2, 3, 4, 5, 0, time.UTC)
	refundedAt := rejectedAt.Add(24 * time.Hour)
	dom := fixtureDomain(t, repo, clientID, regID, func(x *domain.Domain) {
		x.Name = "it-" + uuid.NewString() + ".co.id"
		x.Status = domain.DomainSuspended
		x.DocumentRequired = true
		x.Verification = domain.VerificationDocumentValidating
		x.StatusReason = "Document not uploaded"
		x.DocumentRejectedAt = &rejectedAt
		x.RegistrarRefundable = true
	})

	got, err := repo.GetByID(ctx, dom.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.DomainSuspended, got.Status)
	assert.True(t, got.DocumentRequired)
	assert.Equal(t, domain.VerificationDocumentValidating, got.Verification)
	assert.Equal(t, "Document not uploaded", got.StatusReason)
	require.NotNil(t, got.DocumentRejectedAt)
	assert.True(t, rejectedAt.Equal(*got.DocumentRejectedAt))
	assert.True(t, got.RegistrarRefundable)
	assert.Zero(t, got.RefundAmount)
	assert.Nil(t, got.RefundedAt)

	got.Status = domain.DomainRejected
	got.Verification = domain.VerificationWaiting
	got.DocumentRejectedAt = nil
	got.RefundAmount = 166500
	got.RefundedAt = &refundedAt
	require.NoError(t, repo.Update(ctx, got))

	again, err := repo.GetByID(ctx, dom.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.DomainRejected, again.Status)
	assert.Equal(t, domain.VerificationWaiting, again.Verification)
	assert.Nil(t, again.DocumentRejectedAt)
	assert.EqualValues(t, 166500, again.RefundAmount)
	require.NotNil(t, again.RefundedAt)
	assert.True(t, refundedAt.Equal(*again.RefundedAt))

	// the row locker reads the same columns
	require.NoError(t, db.NewTxManager(d).WithinTx(ctx, func(txCtx context.Context) error {
		locked, err := repo.GetByIDForUpdate(txCtx, dom.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 166500, locked.RefundAmount)
		return nil
	}))
}

func TestDomainRepoRejectsUnknownStatusAndVerification(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)
	clientID := fixtureClient(t, d)
	regID := rdashID(t, d)

	bad := &domain.Domain{
		ClientID: clientID, RegistrarID: regID, Name: "it-" + uuid.NewString() + ".com",
		Status: "banned", Nameservers: []byte(`[]`),
	}
	assert.Error(t, repo.Create(ctx, bad), "status CHECK")

	bad.Status = domain.DomainActive
	bad.Verification = "approved"
	assert.Error(t, repo.Create(ctx, bad), "verification_status CHECK")
}

func TestDomainRepoRejectedFreesTheName(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)
	clientID := fixtureClient(t, d)
	regID := rdashID(t, d)

	dom := fixtureDomain(t, repo, clientID, regID, func(x *domain.Domain) { x.Status = domain.DomainActive })

	dup := *dom
	dup.ID = 0
	require.Equal(t, apperr.CodeConflict, apperr.From(repo.Create(ctx, &dup)).Code, "suspended/active names stay unique")

	require.NoError(t, repo.UpdateStatus(ctx, dom.ID, domain.DomainSuspended))
	dup.ID = 0
	require.Equal(t, apperr.CodeConflict, apperr.From(repo.Create(ctx, &dup)).Code, "a suspended domain still holds its name")

	require.NoError(t, repo.UpdateStatus(ctx, dom.ID, domain.DomainRejected))
	dup.ID = 0
	require.NoError(t, repo.Create(ctx, &dup), "a rejected registration frees the name, like a cancelled one")

	byName, err := repo.GetByName(ctx, dom.Name)
	require.NoError(t, err)
	assert.Equal(t, dup.ID, byName.ID, "GetByName resolves the live row, not the rejected one")
}

func TestDomainRepoListForSyncIncludesSuspended(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)
	clientID := fixtureClient(t, d)
	regID := rdashID(t, d)

	byStatus := map[domain.DomainStatus]string{}
	for _, st := range []domain.DomainStatus{
		domain.DomainActive, domain.DomainSuspended, domain.DomainPendingTransfer, domain.DomainExpired,
		domain.DomainRejected, domain.DomainCancelled, domain.DomainPending,
	} {
		dom := fixtureDomain(t, repo, clientID, regID, func(x *domain.Domain) { x.Status = st })
		byStatus[st] = dom.Name
	}

	list, err := repo.ListForSync(ctx, 100000)
	require.NoError(t, err)
	got := namesOf(list)
	for _, st := range []domain.DomainStatus{domain.DomainActive, domain.DomainSuspended, domain.DomainPendingTransfer, domain.DomainExpired} {
		assert.True(t, got[byStatus[st]], "%s must be synced", st)
	}
	for _, st := range []domain.DomainStatus{domain.DomainRejected, domain.DomainCancelled, domain.DomainPending} {
		assert.False(t, got[byStatus[st]], "%s must not be synced", st)
	}
}

func TestDomainRepoListAwaitingVerification(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)
	clientID := fixtureClient(t, d)
	regID := rdashID(t, d)

	mk := func(label, tld string, over func(*domain.Domain)) string {
		dom := fixtureDomain(t, repo, clientID, regID, func(x *domain.Domain) {
			x.Name = "it-" + label + "-" + uuid.NewString() + "." + tld
			x.Status = domain.DomainActive
			x.Verification = domain.VerificationWaiting
			if over != nil {
				over(x)
			}
		})
		return dom.Name
	}
	docWaiting := mk("doc", "co.id", func(x *domain.Domain) { x.DocumentRequired = true })
	docValidating := mk("val", "sch.id", func(x *domain.Domain) {
		x.DocumentRequired = true
		x.Verification = domain.VerificationDocumentValidating
	})
	docSuspended := mk("susp", "ac.id", func(x *domain.Domain) {
		x.DocumentRequired = true
		x.Status = domain.DomainSuspended
	})
	docHeldPending := mk("held", "or.id", func(x *domain.Domain) {
		x.DocumentRequired = true
		x.Status = domain.DomainPending
	})
	docPendingTransfer := mk("xfer", "co.id", func(x *domain.Domain) {
		x.DocumentRequired = true
		x.Status = domain.DomainPendingTransfer
	})
	emailVerifying := map[string]string{}
	for _, tld := range []string{"id", "my.id", "biz.id", "web.id"} {
		emailVerifying[tld] = mk("mail", tld, func(x *domain.Domain) { x.Verification = domain.VerificationVerifying })
	}

	docVerified := mk("ok", "co.id", func(x *domain.Domain) {
		x.DocumentRequired = true
		x.Verification = domain.VerificationActive
	})
	docRejected := mk("rej", "co.id", func(x *domain.Domain) {
		x.DocumentRequired = true
		x.Status = domain.DomainRejected
	})
	docExpired := mk("exp", "co.id", func(x *domain.Domain) {
		x.DocumentRequired = true
		x.Status = domain.DomainExpired
	})
	gTLDWaiting := mk("com", "com", nil) // a gTLD the registrar reports as "waiting"
	unreported := mk("none", "my.id", func(x *domain.Domain) { x.Verification = domain.VerificationNone })
	lookalike := mk("look", "id.com", nil) // ends in "id.com", not ".id"

	list, err := repo.ListAwaitingVerification(ctx, 100000)
	require.NoError(t, err)
	got := namesOf(list)

	for label, name := range map[string]string{
		"documents waiting": docWaiting, "documents validating": docValidating, "suspended": docSuspended,
		"held pending": docHeldPending, "pending transfer": docPendingTransfer,
		".id": emailVerifying["id"], ".my.id": emailVerifying["my.id"],
		".biz.id": emailVerifying["biz.id"], ".web.id": emailVerifying["web.id"],
	} {
		assert.True(t, got[name], "%s must be polled", label)
	}
	for label, name := range map[string]string{
		"verified": docVerified, "rejected": docRejected, "expired": docExpired,
		"gTLD waiting": gTLDWaiting, "never reported": unreported, "lookalike tld": lookalike,
	} {
		assert.False(t, got[name], "%s must not be polled", label)
	}

	// least-recently-updated first, bounded by limit
	one, err := repo.ListAwaitingVerification(ctx, 1)
	require.NoError(t, err)
	assert.Len(t, one, 1)
	def, err := repo.ListAwaitingVerification(ctx, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, def, "a non-positive limit falls back to the default batch")
}

// registrationFixture creates a domain plus the order -> order item ->
// invoice -> invoice item chain a checkout produces, and removes it again.
type registrationFixture struct {
	domainID  int64
	invoiceID int64
}

func newRegistrationFixture(t *testing.T, d *db.DB, repo *domains.Repo, clientID, regID int64, inv struct {
	status                    string
	subtotal, discount, total int64
	line                      int64
	otherLine                 int64
	linkItem                  bool
}) registrationFixture {
	t.Helper()
	ctx := context.Background()
	q := d.Querier(ctx)
	suffix := uuid.NewString()

	dom := fixtureDomain(t, repo, clientID, regID, func(x *domain.Domain) {
		x.Name = "it-pay-" + suffix + ".co.id"
		x.Status = domain.DomainRejected
	})

	var orderID, itemID, otherItemID, invoiceID int64
	require.NoError(t, q.QueryRow(ctx,
		`INSERT INTO orders (order_number, client_id) VALUES ($1, $2) RETURNING id`,
		"IT-"+suffix, clientID).Scan(&orderID))
	itemSQL := `INSERT INTO order_items (order_id, item_type, description, domain, cycle, unit_price, domain_id)
		VALUES ($1, 'domain_register', 'domain', $2, 'annually', $3, $4) RETURNING id`
	require.NoError(t, q.QueryRow(ctx, itemSQL, orderID, dom.Name, inv.line, dom.ID).Scan(&itemID))
	if inv.otherLine > 0 {
		require.NoError(t, q.QueryRow(ctx,
			`INSERT INTO order_items (order_id, item_type, description, cycle, unit_price)
			 VALUES ($1, 'domain_register', 'other', 'annually', $2) RETURNING id`, orderID, inv.otherLine).Scan(&otherItemID))
	}
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO invoices (invoice_number, client_id, status, subtotal, discount, total, due_date)
		VALUES ($1, $2, $3, $4, $5, $6, CURRENT_DATE) RETURNING id`,
		"IT-INV-"+suffix, clientID, inv.status, inv.subtotal, inv.discount, inv.total).Scan(&invoiceID))
	if inv.linkItem {
		_, err := q.Exec(ctx, `INSERT INTO invoice_items (invoice_id, description, amount, related_type, related_id)
			VALUES ($1, 'domain', $2, 'order_item', $3)`, invoiceID, inv.line, itemID)
		require.NoError(t, err)
	}
	if otherItemID > 0 {
		_, err := q.Exec(ctx, `INSERT INTO invoice_items (invoice_id, description, amount, related_type, related_id)
			VALUES ($1, 'other', $2, 'order_item', $3)`, invoiceID, inv.otherLine, otherItemID)
		require.NoError(t, err)
	}

	t.Cleanup(func() { // runs before fixtureClient's cleanup (LIFO)
		_, _ = q.Exec(ctx, `DELETE FROM invoices WHERE client_id = $1`, clientID) // cascades items
		_, _ = q.Exec(ctx, `DELETE FROM orders WHERE client_id = $1`, clientID)   // cascades order items
	})
	return registrationFixture{domainID: dom.ID, invoiceID: invoiceID}
}

func TestDomainRepoRegistrationPayment(t *testing.T) {
	ctx := context.Background()
	d := intDB(t)
	repo := domains.NewRepo(d)

	type in = struct {
		status                    string
		subtotal, discount, total int64
		line                      int64
		otherLine                 int64
		linkItem                  bool
	}

	t.Run("the line carries its share of discount and tax", func(t *testing.T) {
		clientID := fixtureClient(t, d)
		// 150.000 + 50.000 = 200.000, 20.000 discount -> 180.000 charged;
		// the 150.000 line paid 150.000 * 180.000 / 200.000 = 135.000
		f := newRegistrationFixture(t, d, repo, clientID, rdashID(t, d), in{"paid", 200000, 20000, 180000, 150000, 50000, true})
		pay, err := repo.RegistrationPayment(ctx, f.domainID)
		require.NoError(t, err)
		assert.EqualValues(t, 135000, pay.Amount)
		assert.Equal(t, f.invoiceID, pay.InvoiceID)
		assert.Contains(t, pay.InvoiceNumber, "IT-INV-")
	})

	t.Run("tax added on top is refunded with the line", func(t *testing.T) {
		clientID := fixtureClient(t, d)
		// 100.000 + 11% tax = 111.000 charged for a single line
		f := newRegistrationFixture(t, d, repo, clientID, rdashID(t, d), in{"paid", 100000, 0, 111000, 100000, 0, true})
		pay, err := repo.RegistrationPayment(ctx, f.domainID)
		require.NoError(t, err)
		assert.EqualValues(t, 111000, pay.Amount)
	})

	t.Run("128-bit arithmetic: huge amounts cannot overflow", func(t *testing.T) {
		clientID := fixtureClient(t, d)
		const big = int64(4_000_000_000_000) // 4e12 * 4e12 = 1.6e25 > int64
		f := newRegistrationFixture(t, d, repo, clientID, rdashID(t, d), in{"paid", big, 0, big, big, 0, true})
		pay, err := repo.RegistrationPayment(ctx, f.domainID)
		require.NoError(t, err)
		assert.EqualValues(t, big, pay.Amount)
	})

	t.Run("nothing was collected", func(t *testing.T) {
		for _, status := range []string{"unpaid", "overdue", "cancelled", "refunded", "draft"} {
			clientID := fixtureClient(t, d)
			f := newRegistrationFixture(t, d, repo, clientID, rdashID(t, d), in{status, 100000, 0, 100000, 100000, 0, true})
			_, err := repo.RegistrationPayment(ctx, f.domainID)
			assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code, "invoice %s", status)
		}
	})

	t.Run("no invoice line for the order item", func(t *testing.T) {
		clientID := fixtureClient(t, d)
		f := newRegistrationFixture(t, d, repo, clientID, rdashID(t, d), in{"paid", 100000, 0, 100000, 100000, 0, false})
		_, err := repo.RegistrationPayment(ctx, f.domainID)
		assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	})

	t.Run("a domain with no order at all (admin-added, hunt)", func(t *testing.T) {
		clientID := fixtureClient(t, d)
		dom := fixtureDomain(t, repo, clientID, rdashID(t, d), nil)
		_, err := repo.RegistrationPayment(ctx, dom.ID)
		assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	})

	t.Run("a zero-subtotal invoice yields nothing to refund", func(t *testing.T) {
		clientID := fixtureClient(t, d)
		f := newRegistrationFixture(t, d, repo, clientID, rdashID(t, d), in{"paid", 0, 0, 0, 0, 0, true})
		pay, err := repo.RegistrationPayment(ctx, f.domainID)
		require.NoError(t, err)
		assert.Zero(t, pay.Amount)
	})
}
