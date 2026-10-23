package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Repo is the pgx persistence for the catalog module. It implements
// ports.ProductRepo and ports.CouponRepo plus the module-local ProductStore
// and OptionStore interfaces. Every query goes through db.Querier(ctx) so it
// joins any transaction started by TxManager.WithinTx.
type Repo struct {
	db *db.DB
}

// NewRepo builds the catalog Repo.
func NewRepo(d *db.DB) *Repo { return &Repo{db: d} }

// Compile-time interface checks. ports.CouponRepo is implemented by the
// couponRepoView returned from (*Repo).CouponRepo() because the Create/GetByID
// method names collide with the product methods on *Repo.
var (
	_ ports.ProductRepo = (*Repo)(nil)
	_ ports.CouponRepo  = couponRepoView{}
	_ ProductStore      = (*Repo)(nil)
	_ OptionStore       = (*Repo)(nil)
	_ SpecStore         = (*Repo)(nil)
)

// mapPgErr converts common Postgres constraint violations into apperr codes.
func mapPgErr(entity string, err error) error {
	var pge *pgconn.PgError
	if errors.As(err, &pge) {
		switch pge.Code {
		case "23505": // unique_violation
			return apperr.Conflict(entity + " already exists")
		case "23503": // foreign_key_violation
			return apperr.Conflict(entity + " is referenced by or references other records")
		case "23514": // check_violation
			return apperr.Validation("invalid " + entity)
		}
	}
	return err
}

// Products (ports.ProductRepo)

const productCols = `id, group_id, name, slug, description, type, module,
	server_group_id, package_name, auto_setup, configurable, stock_enabled, stock_qty,
	hidden, sort, welcome_email_template, shell_access, cgi_access, feature_list,
	template_package, deleted_at, created_at, updated_at`

func scanProduct(row pgx.Row) (*domain.Product, error) {
	var p domain.Product
	err := row.Scan(&p.ID, &p.GroupID, &p.Name, &p.Slug, &p.Description, &p.Type,
		&p.Module, &p.ServerGroupID, &p.PackageName, &p.AutoSetup, &p.Configurable,
		&p.StockEnabled, &p.StockQty, &p.Hidden, &p.Sort, &p.WelcomeEmailTemplate,
		&p.ShellAccess, &p.CGIAccess, &p.FeatureList, &p.TemplatePackage,
		&p.DeletedAt, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Create inserts a product.
func (r *Repo) Create(ctx context.Context, pr *domain.Product) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO products (group_id, name, slug, description, type, module,
			server_group_id, package_name, auto_setup, configurable, stock_enabled, stock_qty,
			hidden, sort, welcome_email_template, shell_access, cgi_access, feature_list,
			template_package)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING id, created_at, updated_at`,
		pr.GroupID, pr.Name, pr.Slug, pr.Description, pr.Type, pr.Module,
		pr.ServerGroupID, pr.PackageName, pr.AutoSetup, pr.Configurable, pr.StockEnabled, pr.StockQty,
		pr.Hidden, pr.Sort, pr.WelcomeEmailTemplate, pr.ShellAccess, pr.CGIAccess, pr.FeatureList,
		pr.TemplatePackage,
	).Scan(&pr.ID, &pr.CreatedAt, &pr.UpdatedAt)
	if err != nil {
		return mapPgErr("product", fmt.Errorf("catalog: create product: %w", err))
	}
	return nil
}

// GetByID returns one non-deleted product.
func (r *Repo) GetByID(ctx context.Context, id int64) (*domain.Product, error) {
	p, err := scanProduct(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+productCols+` FROM products WHERE id = $1 AND deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("product")
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get product %d: %w", id, err)
	}
	return p, nil
}

// GetBySlug returns one non-deleted product by slug.
func (r *Repo) GetBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	p, err := scanProduct(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+productCols+` FROM products WHERE slug = $1 AND deleted_at IS NULL`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("product")
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get product %q: %w", slug, err)
	}
	return p, nil
}

// Update saves all mutable product columns.
func (r *Repo) Update(ctx context.Context, pr *domain.Product) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE products SET group_id=$2, name=$3, slug=$4, description=$5,
			type=$6, module=$7, server_group_id=$8, package_name=$9,
			auto_setup=$10, configurable=$11, stock_enabled=$12, stock_qty=$13, hidden=$14,
			sort=$15, welcome_email_template=$16, shell_access=$17, cgi_access=$18,
			feature_list=$19, template_package=$20
		WHERE id = $1 AND deleted_at IS NULL`,
		pr.ID, pr.GroupID, pr.Name, pr.Slug, pr.Description, pr.Type, pr.Module,
		pr.ServerGroupID, pr.PackageName, pr.AutoSetup, pr.Configurable, pr.StockEnabled, pr.StockQty,
		pr.Hidden, pr.Sort, pr.WelcomeEmailTemplate, pr.ShellAccess, pr.CGIAccess, pr.FeatureList,
		pr.TemplatePackage)
	if err != nil {
		return mapPgErr("product", fmt.Errorf("catalog: update product %d: %w", pr.ID, err))
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("product")
	}
	return nil
}

// listSortColumns whitelists ORDER BY columns for products/coupons.
var listSortColumns = map[string]string{
	"id": "id", "name": "name", "slug": "slug", "sort": "sort",
	"created_at": "created_at", "code": "code",
}

func orderBy(sort, def string) string {
	col, dir := sort, "ASC"
	if strings.HasPrefix(col, "-") {
		col, dir = col[1:], "DESC"
	}
	if c, ok := listSortColumns[col]; ok {
		return c + " " + dir
	}
	return def
}

// List returns products page + total. Search matches name/slug (ILIKE);
// Status filters visibility: "visible" | "hidden"; GroupID / Type (0 / "" =
// unset) narrow to one product group / product type.
func (r *Repo) List(ctx context.Context, p ports.ListParams) ([]domain.Product, int64, error) {
	where := `deleted_at IS NULL`
	args := []any{}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		where += fmt.Sprintf(` AND (name ILIKE $%d OR slug ILIKE $%d)`, len(args), len(args))
	}
	switch p.Status {
	case "visible":
		where += ` AND NOT hidden`
	case "hidden":
		where += ` AND hidden`
	}
	if p.GroupID > 0 {
		args = append(args, p.GroupID)
		where += fmt.Sprintf(` AND group_id = $%d`, len(args))
	}
	if p.Type != "" {
		args = append(args, p.Type)
		where += fmt.Sprintf(` AND type = $%d`, len(args))
	}

	var total int64
	if err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM products WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("catalog: count products: %w", err)
	}

	args = append(args, p.Limit(), p.Offset())
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+productCols+` FROM products WHERE `+where+
			` ORDER BY `+orderBy(p.Sort, "sort ASC, id ASC")+
			fmt.Sprintf(` LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("catalog: list products: %w", err)
	}
	defer rows.Close()

	var out []domain.Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("catalog: scan product: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("catalog: list products rows: %w", err)
	}
	return out, total, nil
}

// SoftDelete marks a product deleted.
func (r *Repo) SoftDelete(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE products SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("catalog: soft delete product %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("product")
	}
	return nil
}

// DecrementStock decrements stock_qty when stock tracking is enabled;
// CONFLICT when out of stock, no-op when stock is not tracked.
func (r *Repo) DecrementStock(ctx context.Context, productID int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE products SET stock_qty = stock_qty - 1
		WHERE id = $1 AND deleted_at IS NULL AND stock_enabled AND stock_qty > 0`,
		productID)
	if err != nil {
		return fmt.Errorf("catalog: decrement stock %d: %w", productID, err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var enabled bool
	err = r.db.Querier(ctx).QueryRow(ctx,
		`SELECT stock_enabled FROM products WHERE id = $1 AND deleted_at IS NULL`,
		productID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("product")
	}
	if err != nil {
		return fmt.Errorf("catalog: decrement stock check %d: %w", productID, err)
	}
	if !enabled {
		return nil // stock not tracked
	}
	return apperr.Conflict("product out of stock")
}

// CountProductsInGroup counts non-deleted products in a group.
func (r *Repo) CountProductsInGroup(ctx context.Context, groupID int64) (int64, error) {
	var n int64
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM products WHERE group_id = $1 AND deleted_at IS NULL`,
		groupID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("catalog: count products in group %d: %w", groupID, err)
	}
	return n, nil
}

// ListVisibleProducts returns all non-deleted, non-hidden products.
func (r *Repo) ListVisibleProducts(ctx context.Context) ([]domain.Product, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+productCols+` FROM products
		 WHERE deleted_at IS NULL AND NOT hidden ORDER BY sort, id`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list visible products: %w", err)
	}
	defer rows.Close()

	var out []domain.Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog: scan product: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: visible products rows: %w", err)
	}
	return out, nil
}

// Product groups (ports.ProductRepo)

const groupCols = `id, name, slug, sort, hidden, deleted_at, created_at, updated_at`

func scanGroup(row pgx.Row) (*domain.ProductGroup, error) {
	var g domain.ProductGroup
	err := row.Scan(&g.ID, &g.Name, &g.Slug, &g.Sort, &g.Hidden, &g.DeletedAt,
		&g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// CreateGroup inserts a product group.
func (r *Repo) CreateGroup(ctx context.Context, g *domain.ProductGroup) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO product_groups (name, slug, sort, hidden)
		VALUES ($1,$2,$3,$4) RETURNING id, created_at, updated_at`,
		g.Name, g.Slug, g.Sort, g.Hidden,
	).Scan(&g.ID, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return mapPgErr("product group", fmt.Errorf("catalog: create group: %w", err))
	}
	return nil
}

// GetGroupByID returns one non-deleted product group.
func (r *Repo) GetGroupByID(ctx context.Context, id int64) (*domain.ProductGroup, error) {
	g, err := scanGroup(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+groupCols+` FROM product_groups WHERE id = $1 AND deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("product group")
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get group %d: %w", id, err)
	}
	return g, nil
}

// UpdateGroup saves all mutable group columns.
func (r *Repo) UpdateGroup(ctx context.Context, g *domain.ProductGroup) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE product_groups SET name=$2, slug=$3, sort=$4, hidden=$5
		WHERE id = $1 AND deleted_at IS NULL`,
		g.ID, g.Name, g.Slug, g.Sort, g.Hidden)
	if err != nil {
		return mapPgErr("product group", fmt.Errorf("catalog: update group %d: %w", g.ID, err))
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("product group")
	}
	return nil
}

// ListGroups returns non-deleted groups ordered by sort, id.
func (r *Repo) ListGroups(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+groupCols+` FROM product_groups
		 WHERE deleted_at IS NULL AND ($1 OR NOT hidden) ORDER BY sort, id`,
		includeHidden)
	if err != nil {
		return nil, fmt.Errorf("catalog: list groups: %w", err)
	}
	defer rows.Close()

	var out []domain.ProductGroup
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog: scan group: %w", err)
		}
		out = append(out, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: groups rows: %w", err)
	}
	return out, nil
}

// SoftDeleteGroup marks a group deleted.
func (r *Repo) SoftDeleteGroup(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE product_groups SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("catalog: soft delete group %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("product group")
	}
	return nil
}

// Pricing (ports.ProductRepo, IDR only)

const pricingCols = `id, product_id, cycle, price, setup_fee, currency, created_at, updated_at`

func scanPricing(row pgx.Row) (*domain.ProductPricing, error) {
	var pp domain.ProductPricing
	err := row.Scan(&pp.ID, &pp.ProductID, &pp.Cycle, &pp.Price, &pp.SetupFee,
		&pp.Currency, &pp.CreatedAt, &pp.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &pp, nil
}

// UpsertPricing inserts or updates the price for (product, cycle). Only IDR
// rows are accepted.
func (r *Repo) UpsertPricing(ctx context.Context, pp *domain.ProductPricing) error {
	if pp.Currency == "" {
		pp.Currency = "IDR"
	}
	if pp.Currency != "IDR" {
		return apperr.Validation("only IDR pricing is supported",
			apperr.FieldError{Field: "currency", Message: "must be IDR"})
	}
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO product_pricing (product_id, cycle, price, setup_fee, currency)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (product_id, cycle, currency)
		DO UPDATE SET price = EXCLUDED.price, setup_fee = EXCLUDED.setup_fee
		RETURNING id, created_at, updated_at`,
		pp.ProductID, pp.Cycle, pp.Price, pp.SetupFee, pp.Currency,
	).Scan(&pp.ID, &pp.CreatedAt, &pp.UpdatedAt)
	if err != nil {
		return mapPgErr("pricing", fmt.Errorf("catalog: upsert pricing: %w", err))
	}
	return nil
}

// GetPricing returns the IDR price row for (product, cycle).
func (r *Repo) GetPricing(ctx context.Context, productID int64, cycle domain.BillingCycle) (*domain.ProductPricing, error) {
	pp, err := scanPricing(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+pricingCols+` FROM product_pricing
		 WHERE product_id = $1 AND cycle = $2 AND currency = 'IDR'`,
		productID, cycle))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("pricing")
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get pricing %d/%s: %w", productID, cycle, err)
	}
	return pp, nil
}

// ListPricing returns all IDR price rows of a product.
func (r *Repo) ListPricing(ctx context.Context, productID int64) ([]domain.ProductPricing, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+pricingCols+` FROM product_pricing
		 WHERE product_id = $1 AND currency = 'IDR' ORDER BY id`, productID)
	if err != nil {
		return nil, fmt.Errorf("catalog: list pricing %d: %w", productID, err)
	}
	defer rows.Close()

	var out []domain.ProductPricing
	for rows.Next() {
		pp, err := scanPricing(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog: scan pricing: %w", err)
		}
		out = append(out, *pp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: pricing rows: %w", err)
	}
	return out, nil
}

// DeletePricing removes the IDR price row for (product, cycle).
func (r *Repo) DeletePricing(ctx context.Context, productID int64, cycle domain.BillingCycle) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`DELETE FROM product_pricing
		 WHERE product_id = $1 AND cycle = $2 AND currency = 'IDR'`,
		productID, cycle)
	if err != nil {
		return fmt.Errorf("catalog: delete pricing %d/%s: %w", productID, cycle, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("pricing")
	}
	return nil
}

// ListPricingByProducts returns IDR pricing rows grouped by product id.
func (r *Repo) ListPricingByProducts(ctx context.Context, productIDs []int64) (map[int64][]domain.ProductPricing, error) {
	out := make(map[int64][]domain.ProductPricing, len(productIDs))
	if len(productIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+pricingCols+` FROM product_pricing
		 WHERE product_id = ANY($1) AND currency = 'IDR' ORDER BY product_id, id`,
		productIDs)
	if err != nil {
		return nil, fmt.Errorf("catalog: pricing by products: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		pp, err := scanPricing(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog: scan pricing: %w", err)
		}
		out[pp.ProductID] = append(out[pp.ProductID], *pp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: pricing by products rows: %w", err)
	}
	return out, nil
}

// Configurable options (reads on ports.ProductRepo, writes on OptionStore)

// ListOptionGroups returns every configurable option group.
func (r *Repo) ListOptionGroups(ctx context.Context) ([]domain.ConfigurableOptionGroup, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT id, name, description, created_at, updated_at
		 FROM configurable_option_groups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list option groups: %w", err)
	}
	defer rows.Close()

	var out []domain.ConfigurableOptionGroup
	for rows.Next() {
		var g domain.ConfigurableOptionGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog: scan option group: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: option groups rows: %w", err)
	}
	return out, nil
}

// ListOptions returns the options of a group ordered by sort.
func (r *Repo) ListOptions(ctx context.Context, groupID int64) ([]domain.ConfigurableOption, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT id, group_id, name, sort, created_at, updated_at
		 FROM configurable_options WHERE group_id = $1 ORDER BY sort, id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("catalog: list options: %w", err)
	}
	defer rows.Close()

	var out []domain.ConfigurableOption
	for rows.Next() {
		var o domain.ConfigurableOption
		if err := rows.Scan(&o.ID, &o.GroupID, &o.Name, &o.Sort, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog: scan option: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: options rows: %w", err)
	}
	return out, nil
}

// ListOptionsByGroupIDs batch-loads options for many groups in one query,
// keyed by group_id, to avoid an N+1 query per group.
func (r *Repo) ListOptionsByGroupIDs(ctx context.Context, groupIDs []int64) (map[int64][]domain.ConfigurableOption, error) {
	out := make(map[int64][]domain.ConfigurableOption)
	if len(groupIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT id, group_id, name, sort, created_at, updated_at
		 FROM configurable_options WHERE group_id = ANY($1) ORDER BY group_id, sort, id`, groupIDs)
	if err != nil {
		return nil, fmt.Errorf("catalog: list options by group ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var o domain.ConfigurableOption
		if err := rows.Scan(&o.ID, &o.GroupID, &o.Name, &o.Sort, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog: scan option: %w", err)
		}
		out[o.GroupID] = append(out[o.GroupID], o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: options rows: %w", err)
	}
	return out, nil
}

// ListOptionValues returns the values of an option ordered by sort.
func (r *Repo) ListOptionValues(ctx context.Context, optionID int64) ([]domain.ConfigurableOptionValue, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT id, option_id, name, price_deltas, sort, created_at, updated_at
		 FROM configurable_option_values WHERE option_id = $1 ORDER BY sort, id`, optionID)
	if err != nil {
		return nil, fmt.Errorf("catalog: list option values: %w", err)
	}
	defer rows.Close()

	var out []domain.ConfigurableOptionValue
	for rows.Next() {
		var v domain.ConfigurableOptionValue
		if err := rows.Scan(&v.ID, &v.OptionID, &v.Name, &v.PriceDeltas, &v.Sort, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog: scan option value: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: option values rows: %w", err)
	}
	return out, nil
}

// ListOptionValuesByOptionIDs batch-loads values for many options in one
// query, keyed by option_id, to avoid an N+1 query per option.
func (r *Repo) ListOptionValuesByOptionIDs(ctx context.Context, optionIDs []int64) (map[int64][]domain.ConfigurableOptionValue, error) {
	out := make(map[int64][]domain.ConfigurableOptionValue)
	if len(optionIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT id, option_id, name, price_deltas, sort, created_at, updated_at
		 FROM configurable_option_values WHERE option_id = ANY($1) ORDER BY option_id, sort, id`, optionIDs)
	if err != nil {
		return nil, fmt.Errorf("catalog: list option values by option ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var v domain.ConfigurableOptionValue
		if err := rows.Scan(&v.ID, &v.OptionID, &v.Name, &v.PriceDeltas, &v.Sort, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog: scan option value: %w", err)
		}
		out[v.OptionID] = append(out[v.OptionID], v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: option values rows: %w", err)
	}
	return out, nil
}

// CreateOptionGroup inserts a configurable option group.
func (r *Repo) CreateOptionGroup(ctx context.Context, g *domain.ConfigurableOptionGroup) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO configurable_option_groups (name, description)
		VALUES ($1,$2) RETURNING id, created_at, updated_at`,
		g.Name, g.Description,
	).Scan(&g.ID, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return mapPgErr("option group", fmt.Errorf("catalog: create option group: %w", err))
	}
	return nil
}

// GetOptionGroupByID returns one option group.
func (r *Repo) GetOptionGroupByID(ctx context.Context, id int64) (*domain.ConfigurableOptionGroup, error) {
	var g domain.ConfigurableOptionGroup
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT id, name, description, created_at, updated_at
		 FROM configurable_option_groups WHERE id = $1`, id,
	).Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("option group")
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get option group %d: %w", id, err)
	}
	return &g, nil
}

// UpdateOptionGroup saves an option group.
func (r *Repo) UpdateOptionGroup(ctx context.Context, g *domain.ConfigurableOptionGroup) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE configurable_option_groups SET name=$2, description=$3 WHERE id = $1`,
		g.ID, g.Name, g.Description)
	if err != nil {
		return fmt.Errorf("catalog: update option group %d: %w", g.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("option group")
	}
	return nil
}

// DeleteOptionGroup deletes an option group (options/values cascade).
func (r *Repo) DeleteOptionGroup(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`DELETE FROM configurable_option_groups WHERE id = $1`, id)
	if err != nil {
		return mapPgErr("option group", fmt.Errorf("catalog: delete option group %d: %w", id, err))
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("option group")
	}
	return nil
}

// CreateOption inserts an option.
func (r *Repo) CreateOption(ctx context.Context, o *domain.ConfigurableOption) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO configurable_options (group_id, name, sort)
		VALUES ($1,$2,$3) RETURNING id, created_at, updated_at`,
		o.GroupID, o.Name, o.Sort,
	).Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return mapPgErr("option", fmt.Errorf("catalog: create option: %w", err))
	}
	return nil
}

// GetOptionByID returns one option.
func (r *Repo) GetOptionByID(ctx context.Context, id int64) (*domain.ConfigurableOption, error) {
	var o domain.ConfigurableOption
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT id, group_id, name, sort, created_at, updated_at
		 FROM configurable_options WHERE id = $1`, id,
	).Scan(&o.ID, &o.GroupID, &o.Name, &o.Sort, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("option")
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get option %d: %w", id, err)
	}
	return &o, nil
}

// UpdateOption saves an option.
func (r *Repo) UpdateOption(ctx context.Context, o *domain.ConfigurableOption) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE configurable_options SET name=$2, sort=$3 WHERE id = $1`,
		o.ID, o.Name, o.Sort)
	if err != nil {
		return fmt.Errorf("catalog: update option %d: %w", o.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("option")
	}
	return nil
}

// DeleteOption deletes an option (values cascade).
func (r *Repo) DeleteOption(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`DELETE FROM configurable_options WHERE id = $1`, id)
	if err != nil {
		return mapPgErr("option", fmt.Errorf("catalog: delete option %d: %w", id, err))
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("option")
	}
	return nil
}

// CreateOptionValue inserts an option value.
func (r *Repo) CreateOptionValue(ctx context.Context, v *domain.ConfigurableOptionValue) error {
	deltas := v.PriceDeltas
	if len(deltas) == 0 {
		deltas = []byte(`{}`)
	}
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO configurable_option_values (option_id, name, price_deltas, sort)
		VALUES ($1,$2,$3,$4) RETURNING id, created_at, updated_at`,
		v.OptionID, v.Name, deltas, v.Sort,
	).Scan(&v.ID, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return mapPgErr("option value", fmt.Errorf("catalog: create option value: %w", err))
	}
	return nil
}

// GetOptionValueByID returns one option value.
func (r *Repo) GetOptionValueByID(ctx context.Context, id int64) (*domain.ConfigurableOptionValue, error) {
	var v domain.ConfigurableOptionValue
	err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT id, option_id, name, price_deltas, sort, created_at, updated_at
		 FROM configurable_option_values WHERE id = $1`, id,
	).Scan(&v.ID, &v.OptionID, &v.Name, &v.PriceDeltas, &v.Sort, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("option value")
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get option value %d: %w", id, err)
	}
	return &v, nil
}

// UpdateOptionValue saves an option value.
func (r *Repo) UpdateOptionValue(ctx context.Context, v *domain.ConfigurableOptionValue) error {
	deltas := v.PriceDeltas
	if len(deltas) == 0 {
		deltas = []byte(`{}`)
	}
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE configurable_option_values SET name=$2, price_deltas=$3, sort=$4 WHERE id = $1`,
		v.ID, v.Name, deltas, v.Sort)
	if err != nil {
		return fmt.Errorf("catalog: update option value %d: %w", v.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("option value")
	}
	return nil
}

// DeleteOptionValue deletes an option value.
func (r *Repo) DeleteOptionValue(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx,
		`DELETE FROM configurable_option_values WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("catalog: delete option value %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("option value")
	}
	return nil
}

// Coupons (ports.CouponRepo)

const couponCols = `id, code, type, value, applies_to, max_uses, used_count,
	recurring, expires_at, active, created_at, updated_at`

func scanCoupon(row pgx.Row) (*domain.Coupon, error) {
	var c domain.Coupon
	err := row.Scan(&c.ID, &c.Code, &c.Type, &c.Value, &c.AppliesTo, &c.MaxUses,
		&c.UsedCount, &c.Recurring, &c.ExpiresAt, &c.Active, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// CreateCoupon inserts a coupon.
func (r *Repo) CreateCoupon(ctx context.Context, c *domain.Coupon) error {
	return r.createCoupon(ctx, c)
}

// Create implements ports.CouponRepo.
func (r *Repo) createCoupon(ctx context.Context, c *domain.Coupon) error {
	applies := c.AppliesTo
	if len(applies) == 0 {
		applies = []byte(`{}`)
	}
	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO coupons (code, type, value, applies_to, max_uses, recurring, expires_at, active)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, used_count, created_at, updated_at`,
		c.Code, c.Type, c.Value, applies, c.MaxUses, c.Recurring, c.ExpiresAt, c.Active,
	).Scan(&c.ID, &c.UsedCount, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return mapPgErr("coupon", fmt.Errorf("catalog: create coupon: %w", err))
	}
	return nil
}

// GetCouponByID returns one coupon by id (ports.CouponRepo.GetByID lives on
// couponRepoView; see CouponRepo()).
func (r *Repo) GetCouponByID(ctx context.Context, id int64) (*domain.Coupon, error) {
	c, err := scanCoupon(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+couponCols+` FROM coupons WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("coupon")
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get coupon %d: %w", id, err)
	}
	return c, nil
}

// GetByCode returns one coupon by exact code (codes are stored uppercase).
func (r *Repo) GetByCode(ctx context.Context, code string) (*domain.Coupon, error) {
	c, err := scanCoupon(r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+couponCols+` FROM coupons WHERE code = $1`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("coupon")
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get coupon %q: %w", code, err)
	}
	return c, nil
}

// UpdateCoupon saves all mutable coupon columns (code immutable).
func (r *Repo) UpdateCoupon(ctx context.Context, c *domain.Coupon) error {
	applies := c.AppliesTo
	if len(applies) == 0 {
		applies = []byte(`{}`)
	}
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE coupons SET type=$2, value=$3, applies_to=$4, max_uses=$5,
			recurring=$6, expires_at=$7, active=$8
		WHERE id = $1`,
		c.ID, c.Type, c.Value, applies, c.MaxUses, c.Recurring, c.ExpiresAt, c.Active)
	if err != nil {
		return mapPgErr("coupon", fmt.Errorf("catalog: update coupon %d: %w", c.ID, err))
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("coupon")
	}
	return nil
}

// ListCoupons returns coupons page + total. Search matches code (ILIKE);
// Status filters "active" | "inactive".
func (r *Repo) ListCoupons(ctx context.Context, p ports.ListParams) ([]domain.Coupon, int64, error) {
	where := `TRUE`
	args := []any{}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		where += fmt.Sprintf(` AND code ILIKE $%d`, len(args))
	}
	switch p.Status {
	case "active":
		where += ` AND active`
	case "inactive":
		where += ` AND NOT active`
	}

	var total int64
	if err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT count(*) FROM coupons WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("catalog: count coupons: %w", err)
	}

	args = append(args, p.Limit(), p.Offset())
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+couponCols+` FROM coupons WHERE `+where+
			` ORDER BY `+orderBy(p.Sort, "id DESC")+
			fmt.Sprintf(` LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("catalog: list coupons: %w", err)
	}
	defer rows.Close()

	var out []domain.Coupon
	for rows.Next() {
		c, err := scanCoupon(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("catalog: scan coupon: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("catalog: coupons rows: %w", err)
	}
	return out, total, nil
}

// DeleteCoupon hard-deletes a coupon; FK references map to CONFLICT.
func (r *Repo) DeleteCoupon(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM coupons WHERE id = $1`, id)
	if err != nil {
		return mapPgErr("coupon", fmt.Errorf("catalog: delete coupon %d: %w", id, err))
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("coupon")
	}
	return nil
}

// IncrementUsage bumps used_count; CONFLICT when max_uses is exhausted.
func (r *Repo) IncrementUsage(ctx context.Context, id int64) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE coupons SET used_count = used_count + 1
		WHERE id = $1 AND (max_uses = 0 OR used_count < max_uses)`, id)
	if err != nil {
		return fmt.Errorf("catalog: increment coupon usage %d: %w", id, err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM coupons WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("catalog: increment coupon usage check %d: %w", id, err)
	}
	if !exists {
		return apperr.NotFound("coupon")
	}
	return apperr.Conflict("coupon usage limit reached")
}

// CouponRepo adapts *Repo to ports.CouponRepo (method names on Repo carry a
// Coupon prefix to avoid clashing with the product methods).
func (r *Repo) CouponRepo() ports.CouponRepo { return couponRepoView{r} }

// couponRepoView renames the coupon methods onto the ports.CouponRepo shape.
type couponRepoView struct{ r *Repo }

func (v couponRepoView) Create(ctx context.Context, c *domain.Coupon) error {
	return v.r.createCoupon(ctx, c)
}
func (v couponRepoView) GetByID(ctx context.Context, id int64) (*domain.Coupon, error) {
	return v.r.GetCouponByID(ctx, id)
}
func (v couponRepoView) GetByCode(ctx context.Context, code string) (*domain.Coupon, error) {
	return v.r.GetByCode(ctx, code)
}
func (v couponRepoView) Update(ctx context.Context, c *domain.Coupon) error {
	return v.r.UpdateCoupon(ctx, c)
}
func (v couponRepoView) List(ctx context.Context, p ports.ListParams) ([]domain.Coupon, int64, error) {
	return v.r.ListCoupons(ctx, p)
}
func (v couponRepoView) Delete(ctx context.Context, id int64) error {
	return v.r.DeleteCoupon(ctx, id)
}
func (v couponRepoView) IncrementUsage(ctx context.Context, id int64) error {
	return v.r.IncrementUsage(ctx, id)
}
