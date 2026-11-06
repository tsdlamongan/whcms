//go:build integration

// Integration tests against the real local Postgres (whmcs DB, migrations
// applied). Every fixture uses uuid-suffixed identifiers and is cleaned up
// afterwards - shared/seeded data is never touched.
// Run: go test -tags integration ./internal/modules/catalog/
package catalog_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/catalog"
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

func tag() string { return uuid.NewString()[:8] }

// newGroupFixture inserts a product group and schedules a hard cleanup.
func newGroupFixture(t *testing.T, repo *catalog.Repo, d *db.DB, suffix string) *domain.ProductGroup {
	t.Helper()
	ctx := context.Background()
	g := &domain.ProductGroup{Name: "IT Group " + suffix, Slug: "it-group-" + suffix, Sort: 1}
	require.NoError(t, repo.CreateGroup(ctx, g))
	t.Cleanup(func() {
		_, _ = d.Pool().Exec(ctx, `DELETE FROM product_groups WHERE id = $1`, g.ID)
	})
	return g
}

func newProductFixture(t *testing.T, repo *catalog.Repo, d *db.DB, groupID int64, suffix string, mut func(*domain.Product)) *domain.Product {
	t.Helper()
	ctx := context.Background()
	p := &domain.Product{
		GroupID:   groupID,
		Name:      "IT Product " + suffix,
		Slug:      "it-product-" + suffix,
		Type:      domain.ProductSharedHosting,
		Module:    domain.ModuleNone,
		AutoSetup: domain.SetupOnPayment,
	}
	if mut != nil {
		mut(p)
	}
	require.NoError(t, repo.Create(ctx, p))
	t.Cleanup(func() {
		_, _ = d.Pool().Exec(ctx, `DELETE FROM product_pricing WHERE product_id = $1`, p.ID)
		_, _ = d.Pool().Exec(ctx, `DELETE FROM products WHERE id = $1`, p.ID)
	})
	return p
}

func TestRepoProductLifecycle(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	repo := catalog.NewRepo(d)
	suffix := tag()

	g := newGroupFixture(t, repo, d, suffix)
	p := newProductFixture(t, repo, d, g.ID, suffix, func(pr *domain.Product) {
		pr.ShellAccess = true
		pr.CGIAccess = true
		pr.FeatureList = "custom_features"
		pr.TemplatePackage = "template_pkg"
	})
	require.NotZero(t, p.ID)

	got, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.Slug, got.Slug)
	assert.True(t, got.ShellAccess)
	assert.True(t, got.CGIAccess)
	assert.Equal(t, "custom_features", got.FeatureList)
	assert.Equal(t, "template_pkg", got.TemplatePackage)

	bySlug, err := repo.GetBySlug(ctx, p.Slug)
	require.NoError(t, err)
	assert.Equal(t, p.ID, bySlug.ID)

	// Duplicate slug -> CONFLICT.
	dup := *p
	dup.ID = 0
	err = repo.Create(ctx, &dup)
	assertCode(t, err, apperr.CodeConflict)

	// Update.
	got.Name = "IT Product Updated " + suffix
	got.Hidden = true
	got.ShellAccess = false
	got.CGIAccess = false
	got.FeatureList = ""
	got.TemplatePackage = ""
	require.NoError(t, repo.Update(ctx, got))
	again, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	assert.True(t, again.Hidden)
	assert.False(t, again.ShellAccess)
	assert.False(t, again.CGIAccess)
	assert.Empty(t, again.FeatureList)
	assert.Empty(t, again.TemplatePackage)

	// List with search finds it.
	rows, total, err := repo.List(ctx, ports.ListParams{Search: "it-product-" + suffix})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(1))
	require.NotEmpty(t, rows)

	// Status filter.
	rows, _, err = repo.List(ctx, ports.ListParams{Search: "it-product-" + suffix, Status: "hidden"})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	rows, _, err = repo.List(ctx, ports.ListParams{Search: "it-product-" + suffix, Status: "visible"})
	require.NoError(t, err)
	assert.Empty(t, rows)

	// group_id / type filters (the Products page filters).
	rows, _, err = repo.List(ctx, ports.ListParams{Search: "it-product-" + suffix, GroupID: g.ID})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	rows, _, err = repo.List(ctx, ports.ListParams{Search: "it-product-" + suffix, GroupID: g.ID + 1_000_000})
	require.NoError(t, err)
	assert.Empty(t, rows, "a different group excludes the product")
	rows, _, err = repo.List(ctx, ports.ListParams{Search: "it-product-" + suffix, Type: string(p.Type)})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	rows, _, err = repo.List(ctx, ports.ListParams{Search: "it-product-" + suffix, Type: "domain"})
	require.NoError(t, err)
	assert.Empty(t, rows, "a different type excludes the product")

	// Count in group.
	n, err := repo.CountProductsInGroup(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	// Soft delete hides it everywhere.
	require.NoError(t, repo.SoftDelete(ctx, p.ID))
	_, err = repo.GetByID(ctx, p.ID)
	assertCode(t, err, apperr.CodeNotFound)
	n, err = repo.CountProductsInGroup(ctx, g.ID)
	require.NoError(t, err)
	assert.Zero(t, n)
	err = repo.SoftDelete(ctx, p.ID)
	assertCode(t, err, apperr.CodeNotFound)
}

func TestRepoGroupLifecycle(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	repo := catalog.NewRepo(d)
	suffix := tag()

	g := newGroupFixture(t, repo, d, suffix)

	got, err := repo.GetGroupByID(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, g.Slug, got.Slug)

	got.Hidden = true
	require.NoError(t, repo.UpdateGroup(ctx, got))

	all, err := repo.ListGroups(ctx, true)
	require.NoError(t, err)
	found := false
	for _, row := range all {
		if row.ID == g.ID {
			found = true
		}
	}
	assert.True(t, found)

	visible, err := repo.ListGroups(ctx, false)
	require.NoError(t, err)
	for _, row := range visible {
		assert.NotEqual(t, g.ID, row.ID, "hidden group excluded from visible listing")
	}

	require.NoError(t, repo.SoftDeleteGroup(ctx, g.ID))
	_, err = repo.GetGroupByID(ctx, g.ID)
	assertCode(t, err, apperr.CodeNotFound)
}

func TestRepoPricingIDROnly(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	repo := catalog.NewRepo(d)
	suffix := tag()

	g := newGroupFixture(t, repo, d, suffix)
	p := newProductFixture(t, repo, d, g.ID, suffix, nil)

	// Non-IDR rejected.
	err := repo.UpsertPricing(ctx, &domain.ProductPricing{
		ProductID: p.ID, Cycle: domain.CycleMonthly, Price: 1, Currency: "USD",
	})
	assertCode(t, err, apperr.CodeValidation)

	// Insert + update via upsert.
	pp := &domain.ProductPricing{ProductID: p.ID, Cycle: domain.CycleMonthly, Price: 50_000, SetupFee: 10_000}
	require.NoError(t, repo.UpsertPricing(ctx, pp))
	assert.Equal(t, "IDR", pp.Currency)
	firstID := pp.ID

	pp2 := &domain.ProductPricing{ProductID: p.ID, Cycle: domain.CycleMonthly, Price: 60_000}
	require.NoError(t, repo.UpsertPricing(ctx, pp2))
	assert.Equal(t, firstID, pp2.ID, "upsert updates the same row")

	got, err := repo.GetPricing(ctx, p.ID, domain.CycleMonthly)
	require.NoError(t, err)
	assert.Equal(t, int64(60_000), got.Price)

	list, err := repo.ListPricing(ctx, p.ID)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	byProducts, err := repo.ListPricingByProducts(ctx, []int64{p.ID})
	require.NoError(t, err)
	assert.Len(t, byProducts[p.ID], 1)

	empty, err := repo.ListPricingByProducts(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	require.NoError(t, repo.DeletePricing(ctx, p.ID, domain.CycleMonthly))
	_, err = repo.GetPricing(ctx, p.ID, domain.CycleMonthly)
	assertCode(t, err, apperr.CodeNotFound)
	err = repo.DeletePricing(ctx, p.ID, domain.CycleMonthly)
	assertCode(t, err, apperr.CodeNotFound)
}

func TestRepoDecrementStock(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	repo := catalog.NewRepo(d)
	suffix := tag()

	g := newGroupFixture(t, repo, d, suffix)
	stocked := newProductFixture(t, repo, d, g.ID, suffix+"-a", func(p *domain.Product) {
		p.StockEnabled = true
		p.StockQty = 1
	})
	unstocked := newProductFixture(t, repo, d, g.ID, suffix+"-b", nil)

	// Stock-tracked: 1 -> 0 -> CONFLICT.
	require.NoError(t, repo.DecrementStock(ctx, stocked.ID))
	err := repo.DecrementStock(ctx, stocked.ID)
	assertCode(t, err, apperr.CodeConflict)

	// Untracked: no-op success.
	require.NoError(t, repo.DecrementStock(ctx, unstocked.ID))

	// Missing product.
	err = repo.DecrementStock(ctx, 99_999_999)
	assertCode(t, err, apperr.CodeNotFound)
}

func TestRepoOptionsCRUD(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	repo := catalog.NewRepo(d)
	suffix := tag()

	g := &domain.ConfigurableOptionGroup{Name: "IT Options " + suffix, Description: "d"}
	require.NoError(t, repo.CreateOptionGroup(ctx, g))
	t.Cleanup(func() {
		_, _ = d.Pool().Exec(ctx, `DELETE FROM configurable_option_groups WHERE id = $1`, g.ID)
	})

	got, err := repo.GetOptionGroupByID(ctx, g.ID)
	require.NoError(t, err)
	got.Name = "IT Options v2 " + suffix
	require.NoError(t, repo.UpdateOptionGroup(ctx, got))

	o := &domain.ConfigurableOption{GroupID: g.ID, Name: "RAM", Sort: 1}
	require.NoError(t, repo.CreateOption(ctx, o))
	v := &domain.ConfigurableOptionValue{OptionID: o.ID, Name: "2GB", PriceDeltas: json.RawMessage(`{"monthly":10000}`)}
	require.NoError(t, repo.CreateOptionValue(ctx, v))

	options, err := repo.ListOptions(ctx, g.ID)
	require.NoError(t, err)
	require.Len(t, options, 1)
	values, err := repo.ListOptionValues(ctx, o.ID)
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.JSONEq(t, `{"monthly":10000}`, string(values[0].PriceDeltas))

	// ListOptionGroups returns every group, including this fixture.
	allGroups, err := repo.ListOptionGroups(ctx)
	require.NoError(t, err)
	found := false
	for _, row := range allGroups {
		if row.ID == g.ID {
			found = true
		}
	}
	assert.True(t, found)

	gotO, err := repo.GetOptionByID(ctx, o.ID)
	require.NoError(t, err)
	gotO.Name = "CPU"
	require.NoError(t, repo.UpdateOption(ctx, gotO))

	gotV, err := repo.GetOptionValueByID(ctx, v.ID)
	require.NoError(t, err)
	gotV.Name = "4GB"
	require.NoError(t, repo.UpdateOptionValue(ctx, gotV))

	// Direct deletes (not via cascade): value then option.
	require.NoError(t, repo.DeleteOptionValue(ctx, v.ID))
	_, err = repo.GetOptionValueByID(ctx, v.ID)
	assertCode(t, err, apperr.CodeNotFound)
	err = repo.DeleteOptionValue(ctx, v.ID)
	assertCode(t, err, apperr.CodeNotFound)

	require.NoError(t, repo.DeleteOption(ctx, o.ID))
	_, err = repo.GetOptionByID(ctx, o.ID)
	assertCode(t, err, apperr.CodeNotFound)
	err = repo.DeleteOption(ctx, o.ID)
	assertCode(t, err, apperr.CodeNotFound)

	// Deleting the group cascades (no options/values left, but exercises the
	// same path when children do exist too).
	require.NoError(t, repo.DeleteOptionGroup(ctx, g.ID))
	err = repo.DeleteOptionGroup(ctx, g.ID)
	assertCode(t, err, apperr.CodeNotFound)
}

func TestRepoCouponLifecycle(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	repo := catalog.NewRepo(d)
	coupons := repo.CouponRepo()
	suffix := tag()
	code := "IT-" + suffix

	exp := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	c := &domain.Coupon{
		Code: code, Type: domain.CouponPercentage, Value: 10,
		AppliesTo: json.RawMessage(`{"product_ids":[1]}`),
		MaxUses:   2, Recurring: true, ExpiresAt: &exp, Active: true,
	}
	require.NoError(t, coupons.Create(ctx, c))
	t.Cleanup(func() {
		_, _ = d.Pool().Exec(ctx, `DELETE FROM coupons WHERE id = $1`, c.ID)
	})

	// Duplicate code -> CONFLICT.
	dup := *c
	dup.ID = 0
	err := coupons.Create(ctx, &dup)
	assertCode(t, err, apperr.CodeConflict)

	got, err := coupons.GetByCode(ctx, code)
	require.NoError(t, err)
	assert.Equal(t, c.ID, got.ID)
	assert.True(t, got.Recurring)
	require.NotNil(t, got.ExpiresAt)

	byID, err := coupons.GetByID(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, code, byID.Code)

	byID.Value = 20
	byID.Active = false
	require.NoError(t, coupons.Update(ctx, byID))

	rows, total, err := coupons.List(ctx, ports.ListParams{Search: code, Status: "inactive"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(20), rows[0].Value)

	// max_uses=2: two increments succeed, third conflicts.
	require.NoError(t, coupons.IncrementUsage(ctx, c.ID))
	require.NoError(t, coupons.IncrementUsage(ctx, c.ID))
	err = coupons.IncrementUsage(ctx, c.ID)
	assertCode(t, err, apperr.CodeConflict)
	err = coupons.IncrementUsage(ctx, 99_999_999)
	assertCode(t, err, apperr.CodeNotFound)

	require.NoError(t, coupons.Delete(ctx, c.ID))
	_, err = coupons.GetByID(ctx, c.ID)
	assertCode(t, err, apperr.CodeNotFound)
	err = coupons.Delete(ctx, c.ID)
	assertCode(t, err, apperr.CodeNotFound)
}

func TestRepoVisibleProducts(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	repo := catalog.NewRepo(d)
	suffix := tag()

	g := newGroupFixture(t, repo, d, suffix)
	visible := newProductFixture(t, repo, d, g.ID, suffix+"-v", nil)
	hidden := newProductFixture(t, repo, d, g.ID, suffix+"-h", func(p *domain.Product) { p.Hidden = true })

	rows, err := repo.ListVisibleProducts(ctx)
	require.NoError(t, err)
	ids := map[int64]bool{}
	for _, r := range rows {
		ids[r.ID] = true
	}
	assert.True(t, ids[visible.ID])
	assert.False(t, ids[hidden.ID])
}

// TestRepoCreateCouponWrapper exercises (*Repo).CreateCoupon directly (the
// exported wrapper around createCoupon; ports.CouponRepo goes through
// couponRepoView.Create instead, which is covered by TestRepoCouponLifecycle).
func TestRepoCreateCouponWrapper(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	repo := catalog.NewRepo(d)
	suffix := tag()

	c := &domain.Coupon{Code: "IT-WRAP-" + suffix, Type: domain.CouponFixed, Value: 1000, Active: true}
	require.NoError(t, repo.CreateCoupon(ctx, c))
	t.Cleanup(func() {
		_, _ = d.Pool().Exec(ctx, `DELETE FROM coupons WHERE id = $1`, c.ID)
	})
	require.NotZero(t, c.ID)

	got, err := repo.GetCouponByID(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, c.Code, got.Code)
}

// TestRepoConstraintViolations drives mapPgErr's foreign_key_violation and
// check_violation branches (unique_violation is already exercised by the
// duplicate-slug/duplicate-code cases in the lifecycle tests above).
func TestRepoConstraintViolations(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	repo := catalog.NewRepo(d)
	suffix := tag()

	// Foreign key violation: group_id references a non-existent group.
	err := repo.Create(ctx, &domain.Product{
		GroupID: 999_999_999, Name: "IT FK " + suffix, Slug: "it-fk-" + suffix,
		Type: domain.ProductOther, Module: domain.ModuleNone, AutoSetup: domain.SetupOnPayment,
	})
	assertCode(t, err, apperr.CodeConflict)

	// Check violation: type is outside the allowed enum.
	g := newGroupFixture(t, repo, d, suffix)
	err = repo.Create(ctx, &domain.Product{
		GroupID: g.ID, Name: "IT Check " + suffix, Slug: "it-check-" + suffix,
		Type: domain.ProductType("bogus"), Module: domain.ModuleNone, AutoSetup: domain.SetupOnPayment,
	})
	assertCode(t, err, apperr.CodeValidation)
}

// TestRepoCanceledContextPropagatesErrors drives the generic
// "if err != nil" wrapping branch of every repo method (and mapPgErr's
// final passthrough) by forcing pgx to fail fast on an already-canceled
// context, without ever reaching Postgres.
func TestRepoCanceledContextPropagatesErrors(t *testing.T) {
	d := testDB(t)
	repo := catalog.NewRepo(d)

	cctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.Error(t, repo.Create(cctx, &domain.Product{Type: domain.ProductOther}))
	_, err := repo.GetByID(cctx, 1)
	assert.Error(t, err)
	_, err = repo.GetBySlug(cctx, "x")
	assert.Error(t, err)
	assert.Error(t, repo.Update(cctx, &domain.Product{ID: 1, Type: domain.ProductOther}))
	_, _, err = repo.List(cctx, ports.ListParams{})
	assert.Error(t, err)
	assert.Error(t, repo.SoftDelete(cctx, 1))
	assert.Error(t, repo.DecrementStock(cctx, 1))
	_, err = repo.CountProductsInGroup(cctx, 1)
	assert.Error(t, err)
	_, err = repo.ListVisibleProducts(cctx)
	assert.Error(t, err)

	assert.Error(t, repo.CreateGroup(cctx, &domain.ProductGroup{}))
	_, err = repo.GetGroupByID(cctx, 1)
	assert.Error(t, err)
	assert.Error(t, repo.UpdateGroup(cctx, &domain.ProductGroup{ID: 1}))
	_, err = repo.ListGroups(cctx, true)
	assert.Error(t, err)
	assert.Error(t, repo.SoftDeleteGroup(cctx, 1))

	assert.Error(t, repo.UpsertPricing(cctx, &domain.ProductPricing{Currency: "IDR"}))
	_, err = repo.GetPricing(cctx, 1, domain.CycleMonthly)
	assert.Error(t, err)
	_, err = repo.ListPricing(cctx, 1)
	assert.Error(t, err)
	assert.Error(t, repo.DeletePricing(cctx, 1, domain.CycleMonthly))
	_, err = repo.ListPricingByProducts(cctx, []int64{1})
	assert.Error(t, err)

	_, err = repo.ListOptionGroups(cctx)
	assert.Error(t, err)
	_, err = repo.ListOptions(cctx, 1)
	assert.Error(t, err)
	_, err = repo.ListOptionValues(cctx, 1)
	assert.Error(t, err)
	assert.Error(t, repo.CreateOptionGroup(cctx, &domain.ConfigurableOptionGroup{}))
	_, err = repo.GetOptionGroupByID(cctx, 1)
	assert.Error(t, err)
	assert.Error(t, repo.UpdateOptionGroup(cctx, &domain.ConfigurableOptionGroup{ID: 1}))
	assert.Error(t, repo.DeleteOptionGroup(cctx, 1))
	assert.Error(t, repo.CreateOption(cctx, &domain.ConfigurableOption{}))
	_, err = repo.GetOptionByID(cctx, 1)
	assert.Error(t, err)
	assert.Error(t, repo.UpdateOption(cctx, &domain.ConfigurableOption{ID: 1}))
	assert.Error(t, repo.DeleteOption(cctx, 1))
	assert.Error(t, repo.CreateOptionValue(cctx, &domain.ConfigurableOptionValue{}))
	_, err = repo.GetOptionValueByID(cctx, 1)
	assert.Error(t, err)
	assert.Error(t, repo.UpdateOptionValue(cctx, &domain.ConfigurableOptionValue{ID: 1}))
	assert.Error(t, repo.DeleteOptionValue(cctx, 1))

	assert.Error(t, repo.CreateCoupon(cctx, &domain.Coupon{}))
	_, err = repo.GetCouponByID(cctx, 1)
	assert.Error(t, err)
	_, err = repo.GetByCode(cctx, "x")
	assert.Error(t, err)
	assert.Error(t, repo.UpdateCoupon(cctx, &domain.Coupon{ID: 1}))
	_, _, err = repo.ListCoupons(cctx, ports.ListParams{})
	assert.Error(t, err)
	assert.Error(t, repo.DeleteCoupon(cctx, 1))
	assert.Error(t, repo.IncrementUsage(cctx, 1))
}
