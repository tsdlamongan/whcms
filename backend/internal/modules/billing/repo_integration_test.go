//go:build integration

// Integration tests against the real local Postgres (whmcs DB, migrations
// applied). They create their OWN fixture rows with uuid-suffixed
// identifiers and clean up after themselves - never touching shared or
// seeded data. Run: go test -tags integration ./internal/modules/billing/
package billing_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/billing"
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

// newClientFixture inserts a user+client pair and returns the client id.
func newClientFixture(t *testing.T, d *db.DB, tag string) int64 {
	t.Helper()
	ctx := context.Background()
	q := d.Querier(ctx)

	var userID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role) VALUES ($1, 'x', 'client')
		RETURNING id`, "billing-it-"+tag+"@example.com").Scan(&userID))
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

func newInvoiceFixture(t *testing.T, repo *billing.Repo, clientID int64, tag string, status domain.InvoiceStatus, due time.Time) *domain.Invoice {
	t.Helper()
	ctx := context.Background()
	inv := &domain.Invoice{
		InvoiceNumber: "IT-" + tag,
		ClientID:      clientID,
		Status:        status,
		Subtotal:      100_000,
		Total:         100_000,
		Currency:      "IDR",
		DueDate:       due,
	}
	items := []domain.InvoiceItem{
		{Description: "Fixture item " + tag, Amount: 100_000, Taxed: true, RelatedType: domain.RelatedManual},
	}
	require.NoError(t, repo.Create(ctx, inv, items))
	return inv
}

func cleanupInvoice(t *testing.T, d *db.DB, invoiceID int64) {
	t.Cleanup(func() {
		ctx := context.Background()
		q := d.Querier(ctx)
		_, _ = q.Exec(ctx, `DELETE FROM invoice_items WHERE invoice_id = $1`, invoiceID)
		_, _ = q.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, invoiceID)
	})
}

func TestRepoCreateAndGet(t *testing.T) {
	d := testDB(t)
	repo := billing.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	clientID := newClientFixture(t, d, tag)

	due := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	inv := newInvoiceFixture(t, repo, clientID, tag, domain.InvoiceUnpaid, due)
	cleanupInvoice(t, d, inv.ID)
	require.NotZero(t, inv.ID)

	got, err := repo.GetByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, inv.InvoiceNumber, got.InvoiceNumber)
	assert.Equal(t, domain.InvoiceUnpaid, got.Status)
	assert.Equal(t, int64(100_000), got.Total)
	assert.Equal(t, due.Format("2006-01-02"), got.DueDate.Format("2006-01-02"))

	byNum, err := repo.GetByNumber(ctx, inv.InvoiceNumber)
	require.NoError(t, err)
	assert.Equal(t, inv.ID, byNum.ID)

	items, err := repo.GetItems(ctx, inv.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(100_000), items[0].Amount)
	assert.True(t, items[0].Taxed)

	_, err = repo.GetByID(ctx, -1)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
}

func TestRepoUpdateAndStatus(t *testing.T) {
	d := testDB(t)
	repo := billing.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	clientID := newClientFixture(t, d, tag)

	due := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	inv := newInvoiceFixture(t, repo, clientID, tag, domain.InvoiceUnpaid, due)
	cleanupInvoice(t, d, inv.ID)

	// UpdateStatus with paidAt
	paidAt := time.Now().UTC()
	require.NoError(t, repo.UpdateStatus(ctx, inv.ID, domain.InvoicePaid, &paidAt))
	got, err := repo.GetByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.InvoicePaid, got.Status)
	require.NotNil(t, got.PaidAt)

	// UpdateStatus without paidAt keeps existing paid_at
	require.NoError(t, repo.UpdateStatus(ctx, inv.ID, domain.InvoiceRefunded, nil))
	got, err = repo.GetByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.InvoiceRefunded, got.Status)
	assert.NotNil(t, got.PaidAt)

	// Full update
	got.Notes = "updated-" + tag
	got.Total = 123_456
	require.NoError(t, repo.Update(ctx, got))
	got2, err := repo.GetByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, "updated-"+tag, got2.Notes)
	assert.Equal(t, int64(123_456), got2.Total)

	// SetPDFObjectKey
	require.NoError(t, repo.SetPDFObjectKey(ctx, inv.ID, "invoices/"+tag+".pdf"))
	got3, err := repo.GetByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, "invoices/"+tag+".pdf", got3.PDFObjectKey)

	// Not-found paths
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.UpdateStatus(ctx, -1, domain.InvoicePaid, nil)).Code)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.SetPDFObjectKey(ctx, -1, "x")).Code)
}

func TestRepoListsAndDue(t *testing.T) {
	d := testDB(t)
	repo := billing.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	clientID := newClientFixture(t, d, tag)

	past := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	future := time.Date(2030, 1, 5, 0, 0, 0, 0, time.UTC)
	invPast := newInvoiceFixture(t, repo, clientID, tag+"-past", domain.InvoiceUnpaid, past)
	cleanupInvoice(t, d, invPast.ID)
	invFuture := newInvoiceFixture(t, repo, clientID, tag+"-future", domain.InvoiceUnpaid, future)
	cleanupInvoice(t, d, invFuture.ID)

	// ListByClient
	list, total, err := repo.ListByClient(ctx, clientID, ports.ListParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, list, 2)

	// List with search on unique number
	list, total, err = repo.List(ctx, ports.ListParams{Search: "IT-" + tag + "-past"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	assert.Equal(t, invPast.ID, list[0].ID)

	// List with status filter scoped by client
	list, _, err = repo.ListByClient(ctx, clientID, ports.ListParams{Status: "paid"})
	require.NoError(t, err)
	assert.Empty(t, list)

	// ListDueForStatus picks only the past-due invoice
	due, err := repo.ListDueForStatus(ctx, domain.InvoiceUnpaid, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	found := false
	for _, inv := range due {
		if inv.ID == invPast.ID {
			found = true
		}
		assert.NotEqual(t, invFuture.ID, inv.ID)
	}
	assert.True(t, found)
}

func TestRepoNextNumber(t *testing.T) {
	d := testDB(t)
	repo := billing.NewRepo(d)
	ctx := context.Background()
	scope := "invoice-it:" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM counters WHERE scope = $1`, scope)
	})

	n1, err := repo.NextNumber(ctx, scope)
	require.NoError(t, err)
	n2, err := repo.NextNumber(ctx, scope)
	require.NoError(t, err)
	assert.Equal(t, n1+1, n2)
}

func TestRepoModuleLocalQueries(t *testing.T) {
	d := testDB(t)
	repo := billing.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	clientID := newClientFixture(t, d, tag)
	q := d.Querier(ctx)

	// --- OrderIDForOrderItem ------------------------------------------
	var orderID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO orders (order_number, client_id, status, subtotal, total)
		VALUES ($1, $2, 'pending', 100000, 100000) RETURNING id`,
		"IT-ORD-"+tag, clientID).Scan(&orderID))
	var orderItemID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO order_items (order_id, item_type, description, cycle, unit_price)
		VALUES ($1, 'product', 'it item', 'monthly', 100000) RETURNING id`,
		orderID).Scan(&orderItemID))
	t.Cleanup(func() {
		_, _ = q.Exec(ctx, `DELETE FROM order_items WHERE id = $1`, orderItemID)
		_, _ = q.Exec(ctx, `DELETE FROM orders WHERE id = $1`, orderID)
	})

	gotOrderID, err := repo.OrderIDForOrderItem(ctx, orderItemID)
	require.NoError(t, err)
	assert.Equal(t, orderID, gotOrderID)
	_, err = repo.OrderIDForOrderItem(ctx, -1)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)

	// --- HasOpenRenewalInvoice ----------------------------------------
	due := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	inv := &domain.Invoice{
		InvoiceNumber: "IT-REN-" + tag, ClientID: clientID,
		Status: domain.InvoiceUnpaid, Subtotal: 50_000, Total: 50_000,
		Currency: "IDR", DueDate: due,
	}
	relatedServiceID := int64(999_999_999) // synthetic related id, no FK on related_id
	require.NoError(t, repo.Create(ctx, inv, []domain.InvoiceItem{{
		Description: "renewal", Amount: 50_000, Taxed: true,
		RelatedType: domain.RelatedServiceRenewal, RelatedID: &relatedServiceID,
	}}))
	cleanupInvoice(t, d, inv.ID)

	open, err := repo.HasOpenRenewalInvoice(ctx, domain.RelatedServiceRenewal, relatedServiceID)
	require.NoError(t, err)
	assert.True(t, open)

	open, err = repo.HasOpenRenewalInvoice(ctx, domain.RelatedDomainRenewal, relatedServiceID)
	require.NoError(t, err)
	assert.False(t, open, "different related_type does not match")

	require.NoError(t, repo.UpdateStatus(ctx, inv.ID, domain.InvoiceCancelled, nil))
	open, err = repo.HasOpenRenewalInvoice(ctx, domain.RelatedServiceRenewal, relatedServiceID)
	require.NoError(t, err)
	assert.False(t, open, "cancelled invoice is not open")

	// --- HasEmailSince -------------------------------------------------
	ref := "IT-EMAIL-" + tag
	var emailID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO email_log (to_email, template_key, subject, status)
		VALUES ($1, 'invoice_reminder', $2, 'sent') RETURNING id`,
		"billing-it-"+tag+"@example.com", "Reminder: invoice "+ref+" due soon").Scan(&emailID))
	t.Cleanup(func() {
		_, _ = q.Exec(ctx, `DELETE FROM email_log WHERE id = $1`, emailID)
	})

	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	has, err := repo.HasEmailSince(ctx, "invoice_reminder", ref, yesterday)
	require.NoError(t, err)
	assert.True(t, has)

	has, err = repo.HasEmailSince(ctx, "invoice_overdue", ref, yesterday)
	require.NoError(t, err)
	assert.False(t, has, "different template does not match")

	has, err = repo.HasEmailSince(ctx, "invoice_reminder", ref, time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, has, "future cutoff does not match")
}

func TestRepoAddItemAndForUpdate(t *testing.T) {
	d := testDB(t)
	repo := billing.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	clientID := newClientFixture(t, d, tag)

	due := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	inv := newInvoiceFixture(t, repo, clientID, tag, domain.InvoiceOverdue, due)
	cleanupInvoice(t, d, inv.ID)

	tm := db.NewTxManager(d)
	err := tm.WithinTx(ctx, func(txCtx context.Context) error {
		locked, err := repo.GetByIDForUpdate(txCtx, inv.ID)
		if err != nil {
			return err
		}
		if err := repo.AddItem(txCtx, &domain.InvoiceItem{
			InvoiceID: locked.ID, Description: "Late fee", Amount: 5_000,
			Taxed: false, RelatedType: domain.RelatedLateFee,
		}); err != nil {
			return err
		}
		locked.Subtotal += 5_000
		locked.Total += 5_000
		return repo.Update(txCtx, locked)
	})
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(105_000), got.Total)
	items, err := repo.GetItems(ctx, inv.ID)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, domain.RelatedLateFee, items[1].RelatedType)
}

func TestRepoAdminListInvoiceFilters(t *testing.T) {
	d := testDB(t)
	repo := billing.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	clientA := newClientFixture(t, d, tag+"-a")
	clientB := newClientFixture(t, d, tag+"-b")

	due := time.Date(2030, 1, 5, 0, 0, 0, 0, time.UTC)
	invA := newInvoiceFixture(t, repo, clientA, tag+"-a", domain.InvoiceUnpaid, due)
	cleanupInvoice(t, d, invA.ID)
	invB := newInvoiceFixture(t, repo, clientB, tag+"-b", domain.InvoiceUnpaid, due)
	cleanupInvoice(t, d, invB.ID)

	// client_id: the client-detail tab must not leak another client's invoices.
	list, total, err := repo.List(ctx, ports.ListParams{Search: tag, ClientID: clientA})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	assert.Equal(t, invA.ID, list[0].ID)

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "no client filter sees both")

	// ids: resolve exactly the invoices a list screen points at (e.g. transaction rows).
	list, total, err = repo.List(ctx, ports.ListParams{IDs: []int64{invB.ID}})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	assert.Equal(t, invB.ID, list[0].ID)

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag, IDs: []int64{}})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "an empty ids list means no restriction")

	// Date range on the creation date (both inclusive whole UTC days).
	today := time.Now().UTC()
	yesterday := today.AddDate(0, 0, -1)
	tomorrow := today.AddDate(0, 0, 1)

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag, From: today, To: today})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "an invoice created today is inside [today, today]")

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag, From: tomorrow})
	require.NoError(t, err)
	assert.Zero(t, total, "date_from in the future excludes everything")

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag, To: yesterday})
	require.NoError(t, err)
	assert.Zero(t, total, "date_to in the past excludes everything")

	_, total, err = repo.List(ctx, ports.ListParams{Search: tag, From: yesterday, To: tomorrow})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)

	// The client-area listing ignores the admin-only filters' absence/presence
	// of dates the same way (From/To work there too, ClientID is its own arg).
	_, total, err = repo.ListByClient(ctx, clientB, ports.ListParams{From: tomorrow})
	require.NoError(t, err)
	assert.Zero(t, total)
}
