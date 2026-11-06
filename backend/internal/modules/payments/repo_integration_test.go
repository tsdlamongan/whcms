//go:build integration

// Integration tests against the real local Postgres (whmcs DB, migrations
// applied). They create their OWN fixture rows with uuid-suffixed
// identifiers and clean up after themselves - never touching shared or
// seeded data. Run: go test -tags integration ./internal/modules/payments/
package payments_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/payments"
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
		t.Skipf("skipping integration test: cannot connect to postgres: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

// newInvoiceFixture inserts user -> client -> invoice and returns the invoice id.
func newInvoiceFixture(t *testing.T, d *db.DB, tag string) int64 {
	t.Helper()
	ctx := context.Background()
	q := d.Querier(ctx)

	var userID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role) VALUES ($1, 'x', 'client')
		RETURNING id`, "payments-it-"+tag+"@example.com").Scan(&userID))
	var clientID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO clients (user_id, first_name, last_name) VALUES ($1, 'IT', $2)
		RETURNING id`, userID, tag).Scan(&clientID))
	var invoiceID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO invoices (invoice_number, client_id, status, subtotal, total, due_date)
		VALUES ($1, $2, 'unpaid', 150000, 150000, CURRENT_DATE + 3)
		RETURNING id`, "IT-"+tag, clientID).Scan(&invoiceID))

	t.Cleanup(func() {
		_, _ = q.Exec(ctx, `DELETE FROM transactions WHERE invoice_id = $1`, invoiceID)
		_, _ = q.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, invoiceID)
		_, _ = q.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
		_, _ = q.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})
	return invoiceID
}

func TestRepoCRUDRoundTrip(t *testing.T) {
	d := testDB(t)
	repo := payments.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	invoiceID := newInvoiceFixture(t, d, tag)

	mo := "IT-" + tag + "-01"
	expiresAt := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second)
	tx := &domain.Transaction{
		InvoiceID:        invoiceID,
		Gateway:          domain.GatewayDuitku,
		MethodCode:       "VA",
		MerchantOrderID:  &mo,
		GatewayReference: "DREF-" + tag,
		Amount:           150000,
		Status:           domain.TxPending,
		Raw:              []byte(`{"paymentUrl":"https://pay.example"}`),
		ExpiresAt:        &expiresAt,
	}
	require.NoError(t, repo.Create(ctx, tx))
	require.NotZero(t, tx.ID)
	assert.False(t, tx.CreatedAt.IsZero())

	got, err := repo.GetByID(ctx, tx.ID)
	require.NoError(t, err)
	assert.Equal(t, invoiceID, got.InvoiceID)
	assert.Equal(t, domain.TxPending, got.Status)
	require.NotNil(t, got.MerchantOrderID)
	assert.Equal(t, mo, *got.MerchantOrderID)
	assert.Nil(t, got.PaidAt)
	require.NotNil(t, got.ExpiresAt, "expires_at must round-trip so a reload can resume this pending transaction")
	assert.True(t, expiresAt.Equal(*got.ExpiresAt), "want %v, got %v", expiresAt, *got.ExpiresAt)

	byMO, err := repo.GetByMerchantOrderID(ctx, mo)
	require.NoError(t, err)
	assert.Equal(t, tx.ID, byMO.ID)

	now := time.Now().UTC().Truncate(time.Second)
	got.Status = domain.TxSuccess
	got.PaidAt = &now
	got.Fee = 4000
	require.NoError(t, repo.Update(ctx, got))

	updated, err := repo.GetByID(ctx, tx.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TxSuccess, updated.Status)
	assert.Equal(t, int64(4000), updated.Fee)
	require.NotNil(t, updated.PaidAt)

	byInvoice, err := repo.ListByInvoice(ctx, invoiceID)
	require.NoError(t, err)
	require.Len(t, byInvoice, 1)
}

func TestRepoNotFound(t *testing.T) {
	d := testDB(t)
	repo := payments.NewRepo(d)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, -1)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)

	_, err = repo.GetByMerchantOrderID(ctx, "does-not-exist-"+uuid.NewString())
	require.Error(t, err)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)

	err = repo.Update(ctx, &domain.Transaction{ID: -1, Gateway: domain.GatewayManual, Status: domain.TxPending})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
}

func TestRepoOneSuccessPerInvoiceUniqueIndex(t *testing.T) {
	d := testDB(t)
	repo := payments.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	invoiceID := newInvoiceFixture(t, d, tag)
	now := time.Now().UTC()

	first := &domain.Transaction{
		InvoiceID: invoiceID, Gateway: domain.GatewayCredit, MethodCode: "credit",
		Amount: 150000, Status: domain.TxSuccess, PaidAt: &now,
	}
	require.NoError(t, repo.Create(ctx, first))

	second := &domain.Transaction{
		InvoiceID: invoiceID, Gateway: domain.GatewayManual, MethodCode: "manual",
		Amount: 150000, Status: domain.TxSuccess, PaidAt: &now,
	}
	err := repo.Create(ctx, second)
	require.Error(t, err, "partial unique index allows only one success per invoice")
}

func TestRepoListPending(t *testing.T) {
	d := testDB(t)
	repo := payments.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	invoiceID := newInvoiceFixture(t, d, tag)

	mo := "IT-" + tag + "-01"
	pending := &domain.Transaction{
		InvoiceID: invoiceID, Gateway: domain.GatewayDuitku, MethodCode: "VA",
		MerchantOrderID: &mo, Amount: 150000, Status: domain.TxPending,
	}
	require.NoError(t, repo.Create(ctx, pending))

	// Old enough: cutoff in the future includes it.
	rows, err := repo.ListPending(ctx, domain.GatewayDuitku, time.Now().Add(time.Hour))
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		if r.ID == pending.ID {
			found = true
		}
	}
	assert.True(t, found, "pending tx returned when older than cutoff")

	// Too fresh: cutoff before creation excludes it.
	rows, err = repo.ListPending(ctx, domain.GatewayDuitku, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, pending.ID, r.ID, "fresh tx excluded by cutoff")
	}
}

func TestRepoListFilters(t *testing.T) {
	d := testDB(t)
	repo := payments.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	invoiceID := newInvoiceFixture(t, d, tag)

	mo := "IT-" + tag + "-01"
	tx := &domain.Transaction{
		InvoiceID: invoiceID, Gateway: domain.GatewayDuitku, MethodCode: "VA",
		MerchantOrderID: &mo, GatewayReference: "SEARCHREF-" + tag,
		Amount: 150000, Status: domain.TxPending,
	}
	require.NoError(t, repo.Create(ctx, tx))

	rows, total, err := repo.List(ctx, ports.ListParams{Search: tag, Status: "pending"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, tx.ID, rows[0].ID)

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag, Status: "success"})
	require.NoError(t, err)
	assert.Zero(t, total, "status filter excludes the pending row")

	mo2 := "IT-" + tag + "-02"
	manualTx := &domain.Transaction{
		InvoiceID: invoiceID, Gateway: domain.GatewayManual, MethodCode: "bank_transfer",
		MerchantOrderID: &mo2, Amount: 150000, Status: domain.TxPending,
	}
	require.NoError(t, repo.Create(ctx, manualTx))

	rows, total, err = repo.List(ctx, ports.ListParams{Search: tag, Gateway: "manual"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "gateway filter excludes the duitku row")
	require.Len(t, rows, 1)
	assert.Equal(t, manualTx.ID, rows[0].ID)

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag, Gateway: "duitku"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "gateway filter excludes the manual row")
}

func TestRepoListForClient(t *testing.T) {
	d := testDB(t)
	repo := payments.NewRepo(d)
	ctx := context.Background()
	tagA := uuid.NewString()
	tagB := uuid.NewString()
	invoiceA := newInvoiceFixture(t, d, tagA)
	invoiceB := newInvoiceFixture(t, d, tagB)

	var clientA, clientB int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx,
		`SELECT client_id FROM invoices WHERE id = $1`, invoiceA).Scan(&clientA))
	require.NoError(t, d.Querier(ctx).QueryRow(ctx,
		`SELECT client_id FROM invoices WHERE id = $1`, invoiceB).Scan(&clientB))

	moA := "IT-" + tagA + "-01"
	require.NoError(t, repo.Create(ctx, &domain.Transaction{
		InvoiceID: invoiceA, Gateway: domain.GatewayDuitku, MethodCode: "VA",
		MerchantOrderID: &moA, GatewayReference: "REF-" + tagA,
		Amount: 150000, Status: domain.TxPending,
	}))
	moB := "IT-" + tagB + "-01"
	require.NoError(t, repo.Create(ctx, &domain.Transaction{
		InvoiceID: invoiceB, Gateway: domain.GatewayDuitku, MethodCode: "VA",
		MerchantOrderID: &moB, GatewayReference: "REF-" + tagB,
		Amount: 150000, Status: domain.TxPending,
	}))

	rows, total, err := repo.ListForClient(ctx, clientA, ports.ListParams{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, invoiceA, rows[0].InvoiceID, "must not see the other client's transaction")

	rows, total, err = repo.ListForClient(ctx, clientB, ports.ListParams{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, invoiceB, rows[0].InvoiceID)
}

// TestRepoNextAttemptConcurrentIsRaceFree is the DB-level regression test for
// the "Pay Now" double-click / client-retry race (see
// TestPayInvoiceConcurrentAttemptsGetDistinctMerchantOrderIDs in
// service_test.go for the service-level counterpart against mocks). It fires
// many concurrent NextAttempt calls for the SAME invoice straight at real
// Postgres and asserts every value 1..n comes back exactly once - proving the
// counters-table UPDATE ... RETURNING is atomic under real concurrent
// connections, not just single-threaded-safe.
func TestRepoNextAttemptConcurrentIsRaceFree(t *testing.T) {
	d := testDB(t)
	repo := payments.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	invoiceID := newInvoiceFixture(t, d, tag)
	t.Cleanup(func() {
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM counters WHERE scope = $1`,
			fmt.Sprintf("payment_attempt:%d", invoiceID))
	})

	const n = 20
	results := make([]int64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = repo.NextAttempt(ctx, invoiceID)
		}(i)
	}
	wg.Wait()

	seen := make(map[int64]bool, n)
	for i, err := range errs {
		require.NoError(t, err, "call %d", i)
		assert.False(t, seen[results[i]], "attempt number %d returned more than once", results[i])
		seen[results[i]] = true
	}
	for want := int64(1); want <= n; want++ {
		assert.True(t, seen[want], "attempt %d never returned", want)
	}
}

func TestRepoListInvoiceAndDateFilters(t *testing.T) {
	d := testDB(t)
	repo := payments.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	invoiceA := newInvoiceFixture(t, d, tag+"-a")
	invoiceB := newInvoiceFixture(t, d, tag+"-b")

	mk := func(invoiceID int64, suffix string) *domain.Transaction {
		mo := "IT-" + tag + "-" + suffix
		tx := &domain.Transaction{
			InvoiceID: invoiceID, Gateway: domain.GatewayDuitku, MethodCode: "VA",
			MerchantOrderID: &mo, GatewayReference: "REF-" + tag + "-" + suffix,
			Amount: 10000, Status: domain.TxPending,
		}
		require.NoError(t, repo.Create(ctx, tx))
		return tx
	}
	txA := mk(invoiceA, "a")
	mk(invoiceB, "b")

	// invoice_id: the invoice detail page must list only that invoice's transactions.
	rows, total, err := repo.List(ctx, ports.ListParams{Search: tag, InvoiceID: invoiceA})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, txA.ID, rows[0].ID)

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)

	// Date range on creation (inclusive whole UTC days).
	today := time.Now().UTC()
	_, total, err = repo.List(ctx, ports.ListParams{Search: tag, From: today, To: today})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag, From: today.AddDate(0, 0, 1)})
	require.NoError(t, err)
	assert.Zero(t, total)

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag, To: today.AddDate(0, 0, -1)})
	require.NoError(t, err)
	assert.Zero(t, total)
}
