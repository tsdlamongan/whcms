//go:build integration

// Integration tests against the real local Postgres (whmcs DB, migrations
// applied). Run: go test -tags integration ./internal/modules/orders/
//
// Fixtures use uuid-suffixed identifiers and are never cleaned up wholesale;
// other agents share this database.
package orders_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/orders"
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
		t.Skipf("integration tests need the local whmcs database: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

type fixtures struct {
	clientID  int64
	productID int64
}

func createFixtures(t *testing.T, d *db.DB) fixtures {
	t.Helper()
	ctx := context.Background()
	uid := uuid.NewString()[:8]

	var userID int64
	require.NoError(t, d.Pool().QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, status)
		VALUES ('orders-it-`+uid+`@example.com', 'x', 'client', 'active')
		RETURNING id`).Scan(&userID))

	var clientID int64
	require.NoError(t, d.Pool().QueryRow(ctx, `
		INSERT INTO clients (user_id, first_name, last_name)
		VALUES ($1, 'Orders', 'IT-`+uid+`')
		RETURNING id`, userID).Scan(&clientID))

	var groupID int64
	require.NoError(t, d.Pool().QueryRow(ctx, `
		INSERT INTO product_groups (name, slug)
		VALUES ('orders-it-`+uid+`', 'orders-it-`+uid+`')
		RETURNING id`).Scan(&groupID))

	var productID int64
	require.NoError(t, d.Pool().QueryRow(ctx, `
		INSERT INTO products (group_id, name, slug, type, module, stock_enabled, stock_qty)
		VALUES ($1, 'Orders IT', 'orders-it-p-`+uid+`', 'shared_hosting', 'cpanel', TRUE, 5)
		RETURNING id`, groupID).Scan(&productID))

	return fixtures{clientID: clientID, productID: productID}
}

func TestRepoOrderLifecycle(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	fx := createFixtures(t, d)
	repo := orders.NewRepo(d)

	scope := "order:it-" + uuid.NewString()[:8]
	n1, err := repo.NextNumber(ctx, scope)
	require.NoError(t, err)
	n2, err := repo.NextNumber(ctx, scope)
	require.NoError(t, err)
	assert.Equal(t, n1+1, n2, "counter increments")

	order := &domain.Order{
		OrderNumber: "ORD-IT-" + uuid.NewString()[:12],
		ClientID:    fx.clientID,
		Status:      domain.OrderPending,
		Subtotal:    60000,
		Total:       60000,
		IP:          "127.0.0.1",
		Notes:       "integration",
	}
	pid := fx.productID
	items := []domain.OrderItem{{
		ItemType:    domain.ItemProduct,
		ProductID:   &pid,
		Description: "Orders IT (monthly)",
		Domain:      "it.example.com",
		Cycle:       domain.CycleMonthly,
		UnitPrice:   50000,
		SetupFee:    10000,
	}}
	require.NoError(t, repo.Create(ctx, order, items))
	require.NotZero(t, order.ID)
	require.NotZero(t, items[0].ID)

	got, err := repo.GetByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, order.OrderNumber, got.OrderNumber)
	assert.Equal(t, domain.OrderPending, got.Status)
	assert.Equal(t, int64(60000), got.Subtotal)

	gotItems, err := repo.GetItems(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, gotItems, 1)
	assert.Equal(t, "it.example.com", gotItems[0].Domain)
	assert.Nil(t, gotItems[0].ServiceID)

	// Status update + not-found.
	require.NoError(t, repo.UpdateStatus(ctx, order.ID, domain.OrderActive))
	got, err = repo.GetByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderActive, got.Status)
	assert.Error(t, repo.UpdateStatus(ctx, -1, domain.OrderActive))

	_, err = repo.GetByID(ctx, -1)
	assert.Error(t, err)

	// Back-refs: create a service row, then link it.
	var serviceID int64
	require.NoError(t, d.Pool().QueryRow(ctx, `
		INSERT INTO services (client_id, product_id, order_item_id, billing_cycle, status)
		VALUES ($1, $2, $3, 'monthly', 'pending')
		RETURNING id`, fx.clientID, fx.productID, items[0].ID).Scan(&serviceID))
	require.NoError(t, repo.UpdateItemBackRefs(ctx, items[0].ID, &serviceID, nil))
	gotItems, err = repo.GetItems(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, gotItems[0].ServiceID)
	assert.Equal(t, serviceID, *gotItems[0].ServiceID)
	assert.Nil(t, gotItems[0].DomainID, "nil argument leaves domain_id untouched")

	// Listing.
	all, total, err := repo.List(ctx, ports.ListParams{Search: order.OrderNumber})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, all, 1)
	assert.Equal(t, order.ID, all[0].ID)

	// Admin List narrows by ListParams.ClientID.
	byClient, total, err := repo.List(ctx, ports.ListParams{ClientID: fx.clientID, Search: order.OrderNumber})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, byClient, 1)
	_, total, err = repo.List(ctx, ports.ListParams{ClientID: fx.clientID + 1_000_000, Search: order.OrderNumber})
	require.NoError(t, err)
	assert.Zero(t, total, "another client's id matches nothing")

	mine, total, err := repo.ListByClient(ctx, fx.clientID, ports.ListParams{Status: "active"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, mine, 1)

	none, total, err := repo.ListByClient(ctx, fx.clientID, ports.ListParams{Status: "fraud"})
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, none)

	// CountByClientSince.
	count, err := repo.CountByClientSince(ctx, fx.clientID, time.Now().UTC().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	count, err = repo.CountByClientSince(ctx, fx.clientID, time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestRepoInvoiceIDForOrderAndStock(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	fx := createFixtures(t, d)
	repo := orders.NewRepo(d)

	order := &domain.Order{
		OrderNumber: "ORD-IT-" + uuid.NewString()[:12],
		ClientID:    fx.clientID,
		Status:      domain.OrderPending,
	}
	pid := fx.productID
	items := []domain.OrderItem{{
		ItemType: domain.ItemProduct, ProductID: &pid,
		Description: "x", Cycle: domain.CycleMonthly, UnitPrice: 50000,
	}}
	require.NoError(t, repo.Create(ctx, order, items))

	// No invoice yet.
	invID, err := repo.InvoiceIDForOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Zero(t, invID)

	// Raw invoice + item referencing the order item.
	var invoiceID int64
	require.NoError(t, d.Pool().QueryRow(ctx, `
		INSERT INTO invoices (invoice_number, client_id, status, subtotal, total, due_date)
		VALUES ($1, $2, 'unpaid', 50000, 50000, CURRENT_DATE + 3)
		RETURNING id`, "INV-IT-"+uuid.NewString()[:12], fx.clientID).Scan(&invoiceID))
	_, err = d.Pool().Exec(ctx, `
		INSERT INTO invoice_items (invoice_id, description, amount, taxed, related_type, related_id)
		VALUES ($1, 'x', 50000, TRUE, 'order_item', $2)`, invoiceID, items[0].ID)
	require.NoError(t, err)

	invID, err = repo.InvoiceIDForOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, invoiceID, invID)

	// RestoreStock bumps stock-enabled products only.
	var before int
	require.NoError(t, d.Pool().QueryRow(ctx,
		`SELECT stock_qty FROM products WHERE id = $1`, fx.productID).Scan(&before))
	require.NoError(t, repo.RestoreStock(ctx, fx.productID))
	var after int
	require.NoError(t, d.Pool().QueryRow(ctx,
		`SELECT stock_qty FROM products WHERE id = $1`, fx.productID).Scan(&after))
	assert.Equal(t, before+1, after)

	// Disable stock: RestoreStock becomes a no-op.
	_, err = d.Pool().Exec(ctx,
		`UPDATE products SET stock_enabled = FALSE WHERE id = $1`, fx.productID)
	require.NoError(t, err)
	require.NoError(t, repo.RestoreStock(ctx, fx.productID))
	var unchanged int
	require.NoError(t, d.Pool().QueryRow(ctx,
		`SELECT stock_qty FROM products WHERE id = $1`, fx.productID).Scan(&unchanged))
	assert.Equal(t, after, unchanged)
}
