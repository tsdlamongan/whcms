package orders

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/jackc/pgx/v5"
)

// Repo is the pgx implementation of ports.OrderRepo plus the module-local
// OrderStore/ProductStore extras. Every query goes through db.Querier(ctx) so
// it joins any transaction started by TxManager.WithinTx.
type Repo struct {
	db *db.DB
}

// NewRepo builds the orders Repo.
func NewRepo(d *db.DB) *Repo { return &Repo{db: d} }

// Compile-time interface checks.
var (
	_ ports.OrderRepo = (*Repo)(nil)
	_ OrderStore      = (*Repo)(nil)
	_ StockRestorer   = (*Repo)(nil)
)

const orderCols = `id, order_number, client_id, status, subtotal, discount, tax_total, total,
	coupon_id, ip, notes, created_at, updated_at`

const itemCols = `id, order_id, item_type, product_id, description, domain, cycle, unit_price,
	setup_fee, options, service_id, domain_id, created_at, updated_at`

func scanOrder(row pgx.Row, o *domain.Order) error {
	return row.Scan(&o.ID, &o.OrderNumber, &o.ClientID, &o.Status, &o.Subtotal, &o.Discount,
		&o.TaxTotal, &o.Total, &o.CouponID, &o.IP, &o.Notes, &o.CreatedAt, &o.UpdatedAt)
}

// Create inserts the order and its items, filling generated ids/timestamps.
func (r *Repo) Create(ctx context.Context, o *domain.Order, items []domain.OrderItem) error {
	q := r.db.Querier(ctx)
	err := q.QueryRow(ctx, `
		INSERT INTO orders (order_number, client_id, status, subtotal, discount, tax_total,
			total, coupon_id, ip, notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, created_at, updated_at`,
		o.OrderNumber, o.ClientID, o.Status, o.Subtotal, o.Discount, o.TaxTotal,
		o.Total, o.CouponID, o.IP, o.Notes,
	).Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("orders: create: %w", err)
	}
	for i := range items {
		items[i].OrderID = o.ID
		opts := items[i].Options
		if len(opts) == 0 {
			opts = []byte(`{}`)
		}
		err := q.QueryRow(ctx, `
			INSERT INTO order_items (order_id, item_type, product_id, description, domain,
				cycle, unit_price, setup_fee, options)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			RETURNING id, created_at, updated_at`,
			items[i].OrderID, items[i].ItemType, items[i].ProductID, items[i].Description,
			items[i].Domain, items[i].Cycle, items[i].UnitPrice, items[i].SetupFee, opts,
		).Scan(&items[i].ID, &items[i].CreatedAt, &items[i].UpdatedAt)
		if err != nil {
			return fmt.Errorf("orders: create item: %w", err)
		}
	}
	return nil
}

// GetByID returns one order or a NOT_FOUND apperr.
func (r *Repo) GetByID(ctx context.Context, id int64) (*domain.Order, error) {
	var o domain.Order
	err := scanOrder(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+orderCols+` FROM orders WHERE id = $1`, id), &o)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("order")
	}
	if err != nil {
		return nil, fmt.Errorf("orders: get %d: %w", id, err)
	}
	return &o, nil
}

// GetItems returns the order's items ordered by id.
func (r *Repo) GetItems(ctx context.Context, orderID int64) ([]domain.OrderItem, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+itemCols+` FROM order_items WHERE order_id = $1 ORDER BY id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("orders: items %d: %w", orderID, err)
	}
	defer rows.Close()

	var out []domain.OrderItem
	for rows.Next() {
		var it domain.OrderItem
		if err := rows.Scan(&it.ID, &it.OrderID, &it.ItemType, &it.ProductID, &it.Description,
			&it.Domain, &it.Cycle, &it.UnitPrice, &it.SetupFee, &it.Options,
			&it.ServiceID, &it.DomainID, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("orders: scan item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdateStatus sets the order status (state-machine checks happen in the
// service layer before calling this).
func (r *Repo) UpdateStatus(ctx context.Context, id int64, status domain.OrderStatus) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE orders SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("orders: update status %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("order")
	}
	return nil
}

// UpdateItemBackRefs fills the activation back-refs; nil arguments leave the
// existing values untouched.
func (r *Repo) UpdateItemBackRefs(ctx context.Context, itemID int64, serviceID, domainID *int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE order_items
		SET service_id = COALESCE($2, service_id), domain_id = COALESCE($3, domain_id)
		WHERE id = $1`, itemID, serviceID, domainID)
	if err != nil {
		return fmt.Errorf("orders: backrefs %d: %w", itemID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("order item")
	}
	return nil
}

// List returns orders filtered by ListParams (admin listing). Search matches
// the order number; Status filters exactly.
func (r *Repo) List(ctx context.Context, p ports.ListParams) ([]domain.Order, int64, error) {
	return r.list(ctx, p.ClientID, p)
}

// ListByClient returns one client's orders.
func (r *Repo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Order, int64, error) {
	return r.list(ctx, clientID, p)
}

func (r *Repo) list(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Order, int64, error) {
	where := `WHERE ($1 = 0 OR client_id = $1)
		AND ($2 = '' OR status = $2)
		AND ($3 = '' OR order_number ILIKE '%' || $3 || '%')`
	args := []any{clientID, p.Status, p.Search}

	var total int64
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM orders `+where, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("orders: count: %w", err)
	}

	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+orderCols+` FROM orders `+where+`
		 ORDER BY created_at DESC, id DESC LIMIT $4 OFFSET $5`,
		append(args, p.Limit(), p.Offset())...)
	if err != nil {
		return nil, 0, fmt.Errorf("orders: list: %w", err)
	}
	defer rows.Close()

	var out []domain.Order
	for rows.Next() {
		var o domain.Order
		if err := scanOrder(rows, &o); err != nil {
			return nil, 0, fmt.Errorf("orders: scan: %w", err)
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}

// NextNumber increments and returns the counter for scope (e.g.
// "order:202607"); call inside the order-creation transaction.
func (r *Repo) NextNumber(ctx context.Context, scope string) (int64, error) {
	var n int64
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO counters (scope, value) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET value = counters.value + 1
		RETURNING value`, scope).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("orders: next number %s: %w", scope, err)
	}
	return n, nil
}

// CountByClientSince counts the client's orders created at/after since
// (fraud.max_orders_per_day check).
func (r *Repo) CountByClientSince(ctx context.Context, clientID int64, since time.Time) (int64, error) {
	var n int64
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE client_id = $1 AND created_at >= $2`,
		clientID, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("orders: count since: %w", err)
	}
	return n, nil
}

// InvoiceIDForOrder resolves the invoice created for the order via
// invoice_items related_type=order_item back-references; 0 when none.
func (r *Repo) InvoiceIDForOrder(ctx context.Context, orderID int64) (int64, error) {
	var id int64
	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT ii.invoice_id
		FROM invoice_items ii
		JOIN order_items oi ON oi.id = ii.related_id
		WHERE ii.related_type = 'order_item' AND oi.order_id = $1
		ORDER BY ii.invoice_id
		LIMIT 1`, orderID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("orders: invoice for order %d: %w", orderID, err)
	}
	return id, nil
}

// RestoreStock increments stock_qty for stock-enabled products (stock is
// restored when an order is cancelled or marked fraud).
func (r *Repo) RestoreStock(ctx context.Context, productID int64) error {
	_, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE products SET stock_qty = stock_qty + 1 WHERE id = $1 AND stock_enabled`,
		productID)
	if err != nil {
		return fmt.Errorf("orders: restore stock %d: %w", productID, err)
	}
	return nil
}
