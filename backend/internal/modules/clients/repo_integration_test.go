//go:build integration

// Integration tests against the real local Postgres (whmcs DB, migrations
// applied). They create their OWN fixture rows with uuid-suffixed
// identifiers and clean up after themselves - never touching shared or
// seeded data. Run: go test -tags integration ./internal/modules/clients/
package clients_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/clients"
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

// newUserFixture inserts a users row (role client) and returns its id.
func newUserFixture(t *testing.T, d *db.DB, tag string) int64 {
	t.Helper()
	ctx := context.Background()
	var userID int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role) VALUES ($1, 'x', 'client')
		RETURNING id`, "clients-it-"+tag+"@example.com").Scan(&userID))
	t.Cleanup(func() {
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})
	return userID
}

// newClientFixture creates a user+client via the repo and returns the client.
func newClientFixture(t *testing.T, d *db.DB, repo *clients.Repo, tag string) *domain.Client {
	t.Helper()
	ctx := context.Background()
	userID := newUserFixture(t, d, tag)
	c := &domain.Client{
		UserID:    userID,
		FirstName: "IT",
		LastName:  tag,
		Company:   "PT " + tag,
	}
	require.NoError(t, repo.Create(ctx, c))
	t.Cleanup(func() {
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM credit_ledger WHERE client_id = $1`, c.ID)
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM client_contacts WHERE client_id = $1`, c.ID)
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM invoices WHERE client_id = $1`, c.ID)
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM clients WHERE id = $1`, c.ID)
	})
	return c
}

func TestRepoClientCRUD(t *testing.T) {
	d := testDB(t)
	repo := clients.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()

	c := newClientFixture(t, d, repo, tag)
	require.NotZero(t, c.ID)
	assert.Equal(t, "ID", c.Country)   // repo default
	assert.Equal(t, "IDR", c.Currency) // repo default
	assert.Equal(t, domain.ClientActive, c.Status)

	got, err := repo.GetByID(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, tag, got.LastName)

	byUser, err := repo.GetByUserID(ctx, c.UserID)
	require.NoError(t, err)
	assert.Equal(t, c.ID, byUser.ID)

	got.City = "Lamongan"
	got.NotesAdmin = "vip"
	got.Status = domain.ClientInactive
	require.NoError(t, repo.Update(ctx, got))
	got2, err := repo.GetByID(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "Lamongan", got2.City)
	assert.Equal(t, "vip", got2.NotesAdmin)
	assert.Equal(t, domain.ClientInactive, got2.Status)

	// List finds it via the generated search_name column (unique uuid tag).
	rows, total, err := repo.List(ctx, ports.ListParams{Search: tag})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, c.ID, rows[0].ID)

	// Soft delete hides it everywhere.
	require.NoError(t, repo.SoftDelete(ctx, c.ID))
	_, err = repo.GetByID(ctx, c.ID)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.SoftDelete(ctx, c.ID)).Code)
	_, total, err = repo.List(ctx, ports.ListParams{Search: tag})
	require.NoError(t, err)
	assert.Zero(t, total)
}

func TestRepoAdjustCreditAndLedger(t *testing.T) {
	d := testDB(t)
	repo := clients.NewRepo(d)
	ctx := context.Background()
	c := newClientFixture(t, d, repo, uuid.NewString())

	// Add credit.
	balance, err := repo.AdjustCredit(ctx, c.ID, 100000, "deposit", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(100000), balance)

	// Deduct part of it.
	balance, err = repo.AdjustCredit(ctx, c.ID, -30000, "invoice paid", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(70000), balance)

	// Negative-balance guard.
	_, err = repo.AdjustCredit(ctx, c.ID, -999999, "too much", nil)
	assert.Equal(t, apperr.CodeConflict, apperr.From(err).Code)

	// Missing client.
	_, err = repo.AdjustCredit(ctx, -1, 1000, "ghost", nil)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)

	// Ledger has exactly the two successful entries, newest first.
	entries, total, err := repo.ListByClient(ctx, c.ID, ports.ListParams{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, entries, 2)
	assert.Equal(t, int64(-30000), entries[0].Delta)
	assert.Equal(t, int64(70000), entries[0].BalanceAfter)
	assert.Equal(t, int64(100000), entries[1].Delta)

	// Balance persisted on the client row.
	got, err := repo.GetByID(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(70000), got.CreditBalance)
}

func TestRepoContacts(t *testing.T) {
	d := testDB(t)
	repo := clients.NewRepo(d)
	ctx := context.Background()
	a := newClientFixture(t, d, repo, uuid.NewString())
	b := newClientFixture(t, d, repo, uuid.NewString())

	ct := &clients.Contact{
		ClientContact: domain.ClientContact{ClientID: a.ID, FirstName: "Siti", Email: "siti@example.com"},
		Permissions:   json.RawMessage(`{"invoices":true,"domains":false}`),
	}
	require.NoError(t, repo.CreateContact(ctx, ct))
	require.NotZero(t, ct.ID)
	assert.JSONEq(t, `{"invoices":true,"domains":false}`, string(ct.Permissions))

	// Default permissions is `{}` when not supplied.
	ctNoPerm := &clients.Contact{ClientContact: domain.ClientContact{ClientID: a.ID, FirstName: "Budi"}}
	require.NoError(t, repo.CreateContact(ctx, ctNoPerm))
	assert.JSONEq(t, `{}`, string(ctNoPerm.Permissions))
	require.NoError(t, repo.DeleteContact(ctx, a.ID, ctNoPerm.ID))

	// Scoped get: other client sees NOT_FOUND.
	_, err := repo.GetContact(ctx, b.ID, ct.ID)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	got, err := repo.GetContact(ctx, a.ID, ct.ID)
	require.NoError(t, err)
	assert.Equal(t, "Siti", got.FirstName)
	assert.JSONEq(t, `{"invoices":true,"domains":false}`, string(got.Permissions))

	got.Phone = "0812"
	got.Permissions = json.RawMessage(`{"tickets":true}`)
	require.NoError(t, repo.UpdateContact(ctx, got))

	list, err := repo.ListContacts(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "0812", list[0].Phone)
	assert.JSONEq(t, `{"tickets":true}`, string(list[0].Permissions))

	// Scoped delete.
	assert.Equal(t, apperr.CodeNotFound, apperr.From(repo.DeleteContact(ctx, b.ID, ct.ID)).Code)
	require.NoError(t, repo.DeleteContact(ctx, a.ID, ct.ID))
	list, err = repo.ListContacts(ctx, a.ID)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestRepoSearchClients(t *testing.T) {
	d := testDB(t)
	repo := clients.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	c := newClientFixture(t, d, repo, tag)

	// Match by name fragment.
	rows, total, err := repo.SearchClients(ctx, clients.SearchInput{Search: tag})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, c.ID, rows[0].ID)
	assert.Equal(t, "clients-it-"+tag+"@example.com", rows[0].Email)

	// Match by email fragment.
	_, total, err = repo.SearchClients(ctx, clients.SearchInput{Search: "clients-it-" + tag})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// Status filter excludes.
	_, total, err = repo.SearchClients(ctx, clients.SearchInput{Search: tag, Status: "closed"})
	require.NoError(t, err)
	assert.Zero(t, total)

	// has_product excludes clients without services.
	_, total, err = repo.SearchClients(ctx, clients.SearchInput{Search: tag, HasProduct: true})
	require.NoError(t, err)
	assert.Zero(t, total)

	// ids narrows to exactly the requested clients (name resolution for list pages).
	rows, total, err = repo.SearchClients(ctx, clients.SearchInput{IDs: []int64{c.ID}})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, c.ID, rows[0].ID)

	_, total, err = repo.SearchClients(ctx, clients.SearchInput{IDs: []int64{c.ID + 1_000_000}})
	require.NoError(t, err)
	assert.Zero(t, total, "an id that is not a client matches nothing")

	// an empty/absent ids list means "no id filter", never "match nothing"
	_, total, err = repo.SearchClients(ctx, clients.SearchInput{Search: tag, IDs: []int64{}})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
}

func TestRepoCountsRecentAndExport(t *testing.T) {
	d := testDB(t)
	repo := clients.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()
	c := newClientFixture(t, d, repo, tag)

	// One unpaid invoice fixture (cleaned up by the client fixture cleanup).
	var invoiceID int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO invoices (invoice_number, client_id, status, subtotal, total, due_date)
		VALUES ($1, $2, 'unpaid', 50000, 50000, CURRENT_DATE + 3)
		RETURNING id`, "INV-IT-"+tag[:18], c.ID).Scan(&invoiceID))

	q := d.Querier(ctx)

	// Product + service fixture.
	var groupID, productID, serviceID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO product_groups (name, slug) VALUES ($1, $2) RETURNING id`,
		"IT Group "+tag[:8], "it-grp-"+tag).Scan(&groupID))
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO products (group_id, name, slug, type, module)
		VALUES ($1, $2, $3, 'shared_hosting', 'none') RETURNING id`,
		groupID, "IT Product "+tag[:8], "it-prod-"+tag).Scan(&productID))
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO services (client_id, product_id, domain, status, billing_cycle, recurring_amount, next_due_date)
		VALUES ($1, $2, $3, 'active', 'monthly', 25000, CURRENT_DATE + 30) RETURNING id`,
		c.ID, productID, "it-"+tag[:8]+".example.com").Scan(&serviceID))

	// Domain fixture (seeded rdash registrar).
	var registrarID, domainID int64
	require.NoError(t, q.QueryRow(ctx, `SELECT id FROM registrars WHERE name = 'rdash'`).Scan(&registrarID))
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO domains (client_id, registrar_id, name, status, billing_cycle, recurring_amount)
		VALUES ($1, $2, $3, 'active', 'annually', 150000) RETURNING id`,
		c.ID, registrarID, "it-"+tag+".com").Scan(&domainID))

	// Ticket fixture (seeded department).
	var deptID, ticketID int64
	require.NoError(t, q.QueryRow(ctx, `SELECT id FROM ticket_departments ORDER BY id LIMIT 1`).Scan(&deptID))
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO tickets (ticket_number, client_id, department_id, subject)
		VALUES ($1, $2, $3, 'IT test') RETURNING id`,
		"TKT-IT-"+tag[:12], c.ID, deptID).Scan(&ticketID))

	// Successful transaction on the invoice.
	var txID int64
	require.NoError(t, q.QueryRow(ctx, `
		INSERT INTO transactions (invoice_id, gateway, amount, status)
		VALUES ($1, 'manual', 50000, 'success') RETURNING id`, invoiceID).Scan(&txID))

	t.Cleanup(func() {
		_, _ = q.Exec(ctx, `DELETE FROM transactions WHERE id = $1`, txID)
		_, _ = q.Exec(ctx, `DELETE FROM tickets WHERE id = $1`, ticketID)
		_, _ = q.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)
		_, _ = q.Exec(ctx, `DELETE FROM services WHERE id = $1`, serviceID)
		_, _ = q.Exec(ctx, `DELETE FROM products WHERE id = $1`, productID)
		_, _ = q.Exec(ctx, `DELETE FROM product_groups WHERE id = $1`, groupID)
	})

	counts, err := repo.Counts(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), counts.Invoices)
	assert.Equal(t, int64(1), counts.InvoicesUnpaid)
	assert.Equal(t, int64(50000), counts.UnpaidTotal)
	assert.Equal(t, int64(1), counts.Services)
	assert.Equal(t, int64(1), counts.ServicesActive)
	assert.Equal(t, int64(1), counts.Domains)
	assert.Equal(t, int64(1), counts.Tickets)
	assert.Equal(t, int64(1), counts.TicketsOpen)
	assert.Equal(t, int64(1), counts.Transactions)

	recent, err := repo.Recent(ctx, c.ID, 5)
	require.NoError(t, err)
	require.Len(t, recent.Invoices, 1)
	assert.Equal(t, invoiceID, recent.Invoices[0].ID)
	require.Len(t, recent.Services, 1)
	assert.Equal(t, serviceID, recent.Services[0].ID)
	assert.Equal(t, "IT Product "+tag[:8], recent.Services[0].ProductName)
	assert.NotNil(t, recent.Services[0].NextDueDate)
	require.Len(t, recent.Domains, 1)
	assert.Equal(t, domainID, recent.Domains[0].ID)
	require.Len(t, recent.Tickets, 1)
	assert.Equal(t, ticketID, recent.Tickets[0].ID)
	require.Len(t, recent.Transactions, 1)
	assert.Equal(t, txID, recent.Transactions[0].ID)

	// Search has_product now includes this client.
	_, total, err := repo.SearchClients(ctx, clients.SearchInput{Search: tag, HasProduct: true})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// Export includes our row.
	found := false
	require.NoError(t, repo.ForEachExportRow(ctx, func(row clients.ExportRow) error {
		if row.ID == c.ID {
			found = true
			assert.Equal(t, "clients-it-"+tag+"@example.com", row.Email)
		}
		return nil
	}))
	assert.True(t, found)
}
