package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/catalog"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fakes

// fakeProductStore is catalog.ProductStore: mocks.MockProductRepo plus the
// catalog-local queries.
type fakeProductStore struct {
	*mocks.MockProductRepo
	CountProductsInGroupFn  func(ctx context.Context, groupID int64) (int64, error)
	ListVisibleProductsFn   func(ctx context.Context) ([]domain.Product, error)
	ListPricingByProductsFn func(ctx context.Context, productIDs []int64) (map[int64][]domain.ProductPricing, error)
}

func (f *fakeProductStore) CountProductsInGroup(ctx context.Context, groupID int64) (int64, error) {
	if f.CountProductsInGroupFn != nil {
		return f.CountProductsInGroupFn(ctx, groupID)
	}
	return 0, nil
}

func (f *fakeProductStore) ListVisibleProducts(ctx context.Context) ([]domain.Product, error) {
	if f.ListVisibleProductsFn != nil {
		return f.ListVisibleProductsFn(ctx)
	}
	return nil, nil
}

func (f *fakeProductStore) ListPricingByProducts(ctx context.Context, productIDs []int64) (map[int64][]domain.ProductPricing, error) {
	if f.ListPricingByProductsFn != nil {
		return f.ListPricingByProductsFn(ctx, productIDs)
	}
	return map[int64][]domain.ProductPricing{}, nil
}

// fakeOptionStore is a nil-safe function-field catalog.OptionStore.
type fakeOptionStore struct {
	CreateOptionGroupFn  func(ctx context.Context, g *domain.ConfigurableOptionGroup) error
	GetOptionGroupByIDFn func(ctx context.Context, id int64) (*domain.ConfigurableOptionGroup, error)
	UpdateOptionGroupFn  func(ctx context.Context, g *domain.ConfigurableOptionGroup) error
	DeleteOptionGroupFn  func(ctx context.Context, id int64) error
	CreateOptionFn       func(ctx context.Context, o *domain.ConfigurableOption) error
	GetOptionByIDFn      func(ctx context.Context, id int64) (*domain.ConfigurableOption, error)
	UpdateOptionFn       func(ctx context.Context, o *domain.ConfigurableOption) error
	DeleteOptionFn       func(ctx context.Context, id int64) error
	CreateOptionValueFn  func(ctx context.Context, v *domain.ConfigurableOptionValue) error
	GetOptionValueByIDFn func(ctx context.Context, id int64) (*domain.ConfigurableOptionValue, error)
	UpdateOptionValueFn  func(ctx context.Context, v *domain.ConfigurableOptionValue) error
	DeleteOptionValueFn  func(ctx context.Context, id int64) error
}

func (f *fakeOptionStore) CreateOptionGroup(ctx context.Context, g *domain.ConfigurableOptionGroup) error {
	if f.CreateOptionGroupFn != nil {
		return f.CreateOptionGroupFn(ctx, g)
	}
	return nil
}

func (f *fakeOptionStore) GetOptionGroupByID(ctx context.Context, id int64) (*domain.ConfigurableOptionGroup, error) {
	if f.GetOptionGroupByIDFn != nil {
		return f.GetOptionGroupByIDFn(ctx, id)
	}
	return &domain.ConfigurableOptionGroup{ID: id}, nil
}

func (f *fakeOptionStore) UpdateOptionGroup(ctx context.Context, g *domain.ConfigurableOptionGroup) error {
	if f.UpdateOptionGroupFn != nil {
		return f.UpdateOptionGroupFn(ctx, g)
	}
	return nil
}

func (f *fakeOptionStore) DeleteOptionGroup(ctx context.Context, id int64) error {
	if f.DeleteOptionGroupFn != nil {
		return f.DeleteOptionGroupFn(ctx, id)
	}
	return nil
}

func (f *fakeOptionStore) CreateOption(ctx context.Context, o *domain.ConfigurableOption) error {
	if f.CreateOptionFn != nil {
		return f.CreateOptionFn(ctx, o)
	}
	return nil
}

func (f *fakeOptionStore) GetOptionByID(ctx context.Context, id int64) (*domain.ConfigurableOption, error) {
	if f.GetOptionByIDFn != nil {
		return f.GetOptionByIDFn(ctx, id)
	}
	return &domain.ConfigurableOption{ID: id}, nil
}

func (f *fakeOptionStore) UpdateOption(ctx context.Context, o *domain.ConfigurableOption) error {
	if f.UpdateOptionFn != nil {
		return f.UpdateOptionFn(ctx, o)
	}
	return nil
}

func (f *fakeOptionStore) DeleteOption(ctx context.Context, id int64) error {
	if f.DeleteOptionFn != nil {
		return f.DeleteOptionFn(ctx, id)
	}
	return nil
}

func (f *fakeOptionStore) CreateOptionValue(ctx context.Context, v *domain.ConfigurableOptionValue) error {
	if f.CreateOptionValueFn != nil {
		return f.CreateOptionValueFn(ctx, v)
	}
	return nil
}

func (f *fakeOptionStore) GetOptionValueByID(ctx context.Context, id int64) (*domain.ConfigurableOptionValue, error) {
	if f.GetOptionValueByIDFn != nil {
		return f.GetOptionValueByIDFn(ctx, id)
	}
	return &domain.ConfigurableOptionValue{ID: id}, nil
}

func (f *fakeOptionStore) UpdateOptionValue(ctx context.Context, v *domain.ConfigurableOptionValue) error {
	if f.UpdateOptionValueFn != nil {
		return f.UpdateOptionValueFn(ctx, v)
	}
	return nil
}

func (f *fakeOptionStore) DeleteOptionValue(ctx context.Context, id int64) error {
	if f.DeleteOptionValueFn != nil {
		return f.DeleteOptionValueFn(ctx, id)
	}
	return nil
}

// memCache is a functional in-memory ports.Cache used to assert caching and
// invalidation behavior.
type memCache struct {
	m map[string][]byte
}

func newMemCache() *memCache { return &memCache{m: map[string][]byte{}} }

func (c *memCache) GetJSON(ctx context.Context, key string, out any) (bool, error) {
	raw, ok := c.m[key]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, err
	}
	return true, nil
}

func (c *memCache) SetJSON(ctx context.Context, key string, val any, ttl time.Duration) error {
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	c.m[key] = raw
	return nil
}

func (c *memCache) Delete(ctx context.Context, key string) error {
	delete(c.m, key)
	return nil
}

var _ ports.Cache = (*memCache)(nil)

// fixtures bundles the fakes plus the service under test.
type fixtures struct {
	products *fakeProductStore
	options  *fakeOptionStore
	specs    *fakeSpecStore
	coupons  *mocks.MockCouponRepo
	cache    *memCache
	audit    *mocks.MockAuditLogger
	now      time.Time
	svc      *catalog.Service
}

func newFixture() *fixtures {
	f := &fixtures{
		products: &fakeProductStore{MockProductRepo: &mocks.MockProductRepo{}},
		options:  &fakeOptionStore{},
		specs:    &fakeSpecStore{},
		coupons:  &mocks.MockCouponRepo{},
		cache:    newMemCache(),
		audit:    &mocks.MockAuditLogger{},
		now:      time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
	}
	f.svc = catalog.New(catalog.Deps{
		Products: f.products,
		Options:  f.options,
		Specs:    f.specs,
		Coupons:  f.coupons,
		Tx:       &mocks.MockTxManager{},
		Cache:    f.cache,
		Audit:    f.audit,
		Clock:    &mocks.MockClock{FixedTime: f.now},
	})
	return f
}

func assertCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	var e *apperr.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, code, e.Code)
}

func ptr[T any](v T) *T { return &v }

// Coupon validation

func coupon(mut func(*domain.Coupon)) *domain.Coupon {
	c := &domain.Coupon{
		ID:        7,
		Code:      "SAVE10",
		Type:      domain.CouponPercentage,
		Value:     10,
		AppliesTo: json.RawMessage(`{}`),
		Active:    true,
	}
	if mut != nil {
		mut(c)
	}
	return c
}

func TestValidateCoupon(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		coupon     *domain.Coupon
		getErr     error
		code       string
		productIDs []int64
		subtotal   int64
		wantErr    apperr.Code
		wantDisc   int64
	}{
		{
			name: "percentage discount", coupon: coupon(nil),
			code: "SAVE10", subtotal: 100_000, wantDisc: 10_000,
		},
		{
			name: "percentage rounds down", coupon: coupon(nil),
			code: "SAVE10", subtotal: 99_999, wantDisc: 9_999,
		},
		{
			name: "fixed discount",
			coupon: coupon(func(c *domain.Coupon) {
				c.Type = domain.CouponFixed
				c.Value = 25_000
			}),
			code: "SAVE10", subtotal: 100_000, wantDisc: 25_000,
		},
		{
			name: "fixed discount clamped to subtotal",
			coupon: coupon(func(c *domain.Coupon) {
				c.Type = domain.CouponFixed
				c.Value = 250_000
			}),
			code: "SAVE10", subtotal: 100_000, wantDisc: 100_000,
		},
		{
			name: "unknown code", getErr: apperr.NotFound("coupon"),
			code: "NOPE", subtotal: 100_000, wantErr: apperr.CodeNotFound,
		},
		{
			name: "empty code",
			code: "   ", subtotal: 100_000, wantErr: apperr.CodeValidation,
		},
		{
			name:   "negative subtotal",
			coupon: coupon(nil),
			code:   "SAVE10", subtotal: -1, wantErr: apperr.CodeValidation,
		},
		{
			name:   "inactive coupon",
			coupon: coupon(func(c *domain.Coupon) { c.Active = false }),
			code:   "SAVE10", subtotal: 100_000, wantErr: apperr.CodeValidation,
		},
		{
			name: "expired coupon",
			coupon: coupon(func(c *domain.Coupon) {
				c.ExpiresAt = ptr(time.Date(2026, 7, 3, 11, 0, 0, 0, time.UTC))
			}),
			code: "SAVE10", subtotal: 100_000, wantErr: apperr.CodeValidation,
		},
		{
			name: "not yet expired coupon",
			coupon: coupon(func(c *domain.Coupon) {
				c.ExpiresAt = ptr(time.Date(2026, 7, 3, 13, 0, 0, 0, time.UTC))
			}),
			code: "SAVE10", subtotal: 100_000, wantDisc: 10_000,
		},
		{
			name: "max uses reached",
			coupon: coupon(func(c *domain.Coupon) {
				c.MaxUses = 5
				c.UsedCount = 5
			}),
			code: "SAVE10", subtotal: 100_000, wantErr: apperr.CodeConflict,
		},
		{
			name: "max uses remaining",
			coupon: coupon(func(c *domain.Coupon) {
				c.MaxUses = 5
				c.UsedCount = 4
			}),
			code: "SAVE10", subtotal: 100_000, wantDisc: 10_000,
		},
		{
			name: "applies_to mismatch",
			coupon: coupon(func(c *domain.Coupon) {
				c.AppliesTo = json.RawMessage(`{"product_ids":[1,2]}`)
			}),
			code: "SAVE10", productIDs: []int64{3, 4}, subtotal: 100_000,
			wantErr: apperr.CodeValidation,
		},
		{
			name: "applies_to with empty cart",
			coupon: coupon(func(c *domain.Coupon) {
				c.AppliesTo = json.RawMessage(`{"product_ids":[1,2]}`)
			}),
			code: "SAVE10", subtotal: 100_000, wantErr: apperr.CodeValidation,
		},
		{
			name: "applies_to match",
			coupon: coupon(func(c *domain.Coupon) {
				c.AppliesTo = json.RawMessage(`{"product_ids":[1,2]}`)
			}),
			code: "SAVE10", productIDs: []int64{2, 9}, subtotal: 100_000,
			wantDisc: 10_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.coupons.GetByCodeFn = func(ctx context.Context, code string) (*domain.Coupon, error) {
				if tt.getErr != nil {
					return nil, tt.getErr
				}
				return tt.coupon, nil
			}
			disc, c, err := f.svc.ValidateCoupon(ctx, tt.code, tt.productIDs, tt.subtotal)
			if tt.wantErr != "" {
				assertCode(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, c)
			assert.Equal(t, tt.wantDisc, disc)
		})
	}
}

func TestValidateCouponNormalizesCode(t *testing.T) {
	f := newFixture()
	var got string
	f.coupons.GetByCodeFn = func(ctx context.Context, code string) (*domain.Coupon, error) {
		got = code
		return coupon(nil), nil
	}
	_, _, err := f.svc.ValidateCoupon(context.Background(), "  save10 ", nil, 100_000)
	require.NoError(t, err)
	assert.Equal(t, "SAVE10", got)
}

// Public catalog + cache

func visibleFixture(f *fixtures) {
	f.products.ListGroupsFn = func(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
		return []domain.ProductGroup{
			{ID: 1, Name: "Hosting", Slug: "hosting", Sort: 1},
			{ID: 2, Name: "Addons", Slug: "addons", Sort: 2},
		}, nil
	}
	f.products.ListVisibleProductsFn = func(ctx context.Context) ([]domain.Product, error) {
		return []domain.Product{
			{ID: 10, GroupID: 1, Name: "Basic", Slug: "basic", Type: domain.ProductSharedHosting},
			{ID: 11, GroupID: 1, Name: "Pro", Slug: "pro", Type: domain.ProductSharedHosting, StockEnabled: true, StockQty: 0},
			{ID: 12, GroupID: 1, Name: "Custom", Slug: "custom", Type: domain.ProductSharedHosting, Configurable: true},
		}, nil
	}
	f.products.ListPricingByProductsFn = func(ctx context.Context, ids []int64) (map[int64][]domain.ProductPricing, error) {
		return map[int64][]domain.ProductPricing{
			10: {{ProductID: 10, Cycle: domain.CycleMonthly, Price: 50_000, Currency: "IDR"}},
		}, nil
	}
	f.specs.ListSpecsFn = func(ctx context.Context, productID int64) ([]domain.ProductSpec, error) {
		if productID != 12 {
			return nil, nil
		}
		return []domain.ProductSpec{{ID: 31, ProductID: 12, Key: "disk", Label: "Disk",
			ProvisionKey: domain.SpecDisk, Unit: domain.UnitGB, MinQty: 5, MaxQty: 100, StepQty: 5, DefaultQty: 10}}, nil
	}
	f.specs.ListSpecPricingFn = func(ctx context.Context, specID int64) ([]domain.ProductSpecPricing, error) {
		return []domain.ProductSpecPricing{{SpecID: specID, Cycle: domain.CycleMonthly, UnitPrice: 1_000, Currency: "IDR"}}, nil
	}
}

func TestPublicCatalog(t *testing.T) {
	f := newFixture()
	visibleFixture(f)

	groups, err := f.svc.PublicCatalog(context.Background())
	require.NoError(t, err)
	require.Len(t, groups, 2)
	require.Len(t, groups[0].Products, 3)
	assert.Empty(t, groups[1].Products)

	basic := groups[0].Products[0]
	assert.Equal(t, "basic", basic.Slug)
	assert.True(t, basic.InStock)
	require.Len(t, basic.Pricing, 1)
	assert.Equal(t, int64(50_000), basic.Pricing[0].Price)
	assert.Empty(t, basic.Specs, "flat products carry no specs")

	pro := groups[0].Products[1]
	assert.True(t, pro.StockEnabled)
	assert.False(t, pro.InStock, "stock-tracked product with qty 0 is out of stock")

	// Configurable products carry their spec knobs (with pricing) so catalog
	// consumers can configure without a per-product detail fetch.
	custom := groups[0].Products[2]
	require.Len(t, custom.Specs, 1)
	assert.Equal(t, "disk", custom.Specs[0].Key)
	require.Len(t, custom.Specs[0].Pricing, 1)
	assert.Equal(t, int64(1_000), custom.Specs[0].Pricing[0].UnitPrice)

	assert.Contains(t, f.cache.m, "catalog:products:v2", "result is cached")
}

func TestPublicCatalogCacheHit(t *testing.T) {
	f := newFixture()
	visibleFixture(f)
	_, err := f.svc.PublicCatalog(context.Background())
	require.NoError(t, err)

	// Second call must be served from cache: repo access now fails the test.
	f.products.ListGroupsFn = func(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
		t.Fatal("repo hit despite warm cache")
		return nil, nil
	}
	groups, err := f.svc.PublicCatalog(context.Background())
	require.NoError(t, err)
	assert.Len(t, groups, 2)
}

func TestPublicCatalogInvalidatedOnAdminWrite(t *testing.T) {
	f := newFixture()
	visibleFixture(f)
	_, err := f.svc.PublicCatalog(context.Background())
	require.NoError(t, err)
	require.Contains(t, f.cache.m, "catalog:products:v2")

	_, err = f.svc.CreateGroup(context.Background(), 1, catalog.GroupInput{Name: "New Group"})
	require.NoError(t, err)
	assert.NotContains(t, f.cache.m, "catalog:products:v2", "admin write invalidates the catalog cache")
	assert.NotContains(t, f.cache.m, "catalog:groups")
}

func TestPublicGroupsCached(t *testing.T) {
	f := newFixture()
	calls := 0
	f.products.ListGroupsFn = func(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
		calls++
		assert.False(t, includeHidden)
		return []domain.ProductGroup{{ID: 1, Name: "Hosting"}}, nil
	}
	for range 3 {
		groups, err := f.svc.PublicGroups(context.Background())
		require.NoError(t, err)
		assert.Len(t, groups, 1)
	}
	assert.Equal(t, 1, calls, "cached after the first read")
}

func TestPublicProductBySlug(t *testing.T) {
	f := newFixture()
	f.products.GetBySlugFn = func(ctx context.Context, slug string) (*domain.Product, error) {
		return &domain.Product{ID: 10, GroupID: 1, Name: "Basic", Slug: slug, StockEnabled: false}, nil
	}
	f.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id, Name: "Hosting"}, nil
	}
	f.products.ListPricingFn = func(ctx context.Context, productID int64) ([]domain.ProductPricing, error) {
		return []domain.ProductPricing{{ProductID: productID, Cycle: domain.CycleAnnually, Price: 500_000, Currency: "IDR"}}, nil
	}
	f.products.ListOptionGroupsFn = func(ctx context.Context) ([]domain.ConfigurableOptionGroup, error) {
		return []domain.ConfigurableOptionGroup{{ID: 1, Name: "Extras"}}, nil
	}
	f.products.ListOptionsByGroupIDsFn = func(ctx context.Context, groupIDs []int64) (map[int64][]domain.ConfigurableOption, error) {
		return map[int64][]domain.ConfigurableOption{1: {{ID: 2, GroupID: 1, Name: "RAM"}}}, nil
	}
	f.products.ListOptionValuesByOptionIDsFn = func(ctx context.Context, optionIDs []int64) (map[int64][]domain.ConfigurableOptionValue, error) {
		return map[int64][]domain.ConfigurableOptionValue{2: {{ID: 3, OptionID: 2, Name: "2GB"}}}, nil
	}

	detail, err := f.svc.PublicProductBySlug(context.Background(), "basic")
	require.NoError(t, err)
	assert.Equal(t, "basic", detail.Slug)
	assert.True(t, detail.InStock)
	require.Len(t, detail.Pricing, 1)
	require.Len(t, detail.OptionGroups, 1)
	require.Len(t, detail.OptionGroups[0].Options, 1)
	assert.Equal(t, "2GB", detail.OptionGroups[0].Options[0].Values[0].Name)
}

func TestPublicProductBySlugHiddenIsNotFound(t *testing.T) {
	f := newFixture()
	f.products.GetBySlugFn = func(ctx context.Context, slug string) (*domain.Product, error) {
		return &domain.Product{ID: 10, GroupID: 1, Slug: slug, Hidden: true}, nil
	}
	_, err := f.svc.PublicProductBySlug(context.Background(), "hidden")
	assertCode(t, err, apperr.CodeNotFound)
}

func TestPublicProductBySlugHiddenGroupIsNotFound(t *testing.T) {
	f := newFixture()
	f.products.GetBySlugFn = func(ctx context.Context, slug string) (*domain.Product, error) {
		return &domain.Product{ID: 10, GroupID: 1, Slug: slug}, nil
	}
	f.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id, Hidden: true}, nil
	}
	_, err := f.svc.PublicProductBySlug(context.Background(), "basic")
	assertCode(t, err, apperr.CodeNotFound)
}

// Product groups (admin)

func TestCreateGroupGeneratesSlugAndAudits(t *testing.T) {
	f := newFixture()
	var created *domain.ProductGroup
	f.products.CreateGroupFn = func(ctx context.Context, g *domain.ProductGroup) error {
		g.ID = 5
		created = g
		return nil
	}
	g, err := f.svc.CreateGroup(context.Background(), 42, catalog.GroupInput{Name: "Shared Hosting!"})
	require.NoError(t, err)
	assert.Equal(t, "shared-hosting", created.Slug)
	assert.Equal(t, int64(5), g.ID)
	require.Len(t, f.audit.Entries, 1)
	assert.Equal(t, "catalog.group.create", f.audit.Entries[0].Action)
	assert.Equal(t, int64(42), f.audit.Entries[0].ActorUserID)
}

func TestCreateGroupValidation(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateGroup(context.Background(), 1, catalog.GroupInput{Name: "x"})
	assertCode(t, err, apperr.CodeValidation)
}

func TestUpdateGroupPatchesFields(t *testing.T) {
	f := newFixture()
	f.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id, Name: "Old", Slug: "old", Sort: 1}, nil
	}
	var saved *domain.ProductGroup
	f.products.UpdateGroupFn = func(ctx context.Context, g *domain.ProductGroup) error {
		saved = g
		return nil
	}
	g, err := f.svc.UpdateGroup(context.Background(), 1, 3, catalog.GroupUpdateInput{
		Name: ptr("New"), Hidden: ptr(true),
	})
	require.NoError(t, err)
	assert.Equal(t, "New", saved.Name)
	assert.Equal(t, "old", saved.Slug, "unset fields unchanged")
	assert.True(t, g.Hidden)
}

func TestDeleteGroupBlockedWhileProductsExist(t *testing.T) {
	f := newFixture()
	f.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id}, nil
	}
	f.products.CountProductsInGroupFn = func(ctx context.Context, groupID int64) (int64, error) {
		return 3, nil
	}
	deleted := false
	f.products.SoftDeleteGroupFn = func(ctx context.Context, id int64) error {
		deleted = true
		return nil
	}
	err := f.svc.DeleteGroup(context.Background(), 1, 9)
	assertCode(t, err, apperr.CodeConflict)
	assert.False(t, deleted)
	assert.Empty(t, f.audit.Entries)
}

func TestDeleteGroupSoftDeletes(t *testing.T) {
	f := newFixture()
	f.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id}, nil
	}
	var deletedID int64
	f.products.SoftDeleteGroupFn = func(ctx context.Context, id int64) error {
		deletedID = id
		return nil
	}
	require.NoError(t, f.svc.DeleteGroup(context.Background(), 1, 9))
	assert.Equal(t, int64(9), deletedID)
	require.Len(t, f.audit.Entries, 1)
	assert.Equal(t, "catalog.group.delete", f.audit.Entries[0].Action)
}

// Products (admin)

func productInput(mut func(*catalog.ProductInput)) catalog.ProductInput {
	in := catalog.ProductInput{
		GroupID: 1,
		Name:    "Basic Hosting",
		Type:    "shared_hosting",
	}
	if mut != nil {
		mut(&in)
	}
	return in
}

func TestCreateProductDefaultsAndSlug(t *testing.T) {
	f := newFixture()
	f.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id}, nil
	}
	var created *domain.Product
	f.products.CreateFn = func(ctx context.Context, pr *domain.Product) error {
		pr.ID = 10
		created = pr
		return nil
	}
	p, err := f.svc.CreateProduct(context.Background(), 1, productInput(nil))
	require.NoError(t, err)
	assert.Equal(t, domain.ModuleNone, created.Module)
	assert.Equal(t, domain.SetupOnPayment, created.AutoSetup)
	assert.Equal(t, "basic-hosting", created.Slug)
	assert.Equal(t, int64(10), p.ID)
	require.Len(t, f.audit.Entries, 1)
}

func TestCreateProductModuleRequiresPackage(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateProduct(context.Background(), 1, productInput(func(in *catalog.ProductInput) {
		in.Module = "cpanel"
	}))
	assertCode(t, err, apperr.CodeValidation)
}

func TestCreateProductConfigurableAllowsEmptyPackage(t *testing.T) {
	f := newFixture()
	f.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id}, nil
	}
	var created *domain.Product
	f.products.CreateFn = func(ctx context.Context, pr *domain.Product) error {
		pr.ID = 11
		created = pr
		return nil
	}
	_, err := f.svc.CreateProduct(context.Background(), 1, productInput(func(in *catalog.ProductInput) {
		in.Module = "cpanel"
		in.Configurable = true
	}))
	require.NoError(t, err)
	assert.Empty(t, created.PackageName)
}

func TestCreateProductFeatureListRequiresCpanelModule(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateProduct(context.Background(), 1, productInput(func(in *catalog.ProductInput) {
		in.Module = "directadmin"
		in.Configurable = true
		in.FeatureList = "custom_features"
	}))
	assertCode(t, err, apperr.CodeValidation)
}

func TestCreateProductTemplatePackageRequiresDirectAdminModule(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateProduct(context.Background(), 1, productInput(func(in *catalog.ProductInput) {
		in.Module = "cpanel"
		in.Configurable = true
		in.TemplatePackage = "template_pkg"
	}))
	assertCode(t, err, apperr.CodeValidation)
}

func TestCreateProductAllowsShellCGIFeatureListForConfigurable(t *testing.T) {
	f := newFixture()
	f.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return &domain.ProductGroup{ID: id}, nil
	}
	var created *domain.Product
	f.products.CreateFn = func(ctx context.Context, pr *domain.Product) error {
		pr.ID = 12
		created = pr
		return nil
	}
	_, err := f.svc.CreateProduct(context.Background(), 1, productInput(func(in *catalog.ProductInput) {
		in.Module = "cpanel"
		in.Configurable = true
		in.ShellAccess = true
		in.CGIAccess = true
		in.FeatureList = "custom_features"
	}))
	require.NoError(t, err)
	assert.True(t, created.ShellAccess)
	assert.True(t, created.CGIAccess)
	assert.Equal(t, "custom_features", created.FeatureList)
}

func TestCreateProductUnknownGroup(t *testing.T) {
	f := newFixture()
	f.products.GetGroupByIDFn = func(ctx context.Context, id int64) (*domain.ProductGroup, error) {
		return nil, apperr.NotFound("product group")
	}
	_, err := f.svc.CreateProduct(context.Background(), 1, productInput(nil))
	assertCode(t, err, apperr.CodeValidation)
}

func TestCreateProductInvalidType(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateProduct(context.Background(), 1, productInput(func(in *catalog.ProductInput) {
		in.Type = "vps"
	}))
	assertCode(t, err, apperr.CodeValidation)
}

func TestUpdateProductPatchesAndClearsServerGroup(t *testing.T) {
	f := newFixture()
	sg := int64(4)
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{
			ID: id, GroupID: 1, Name: "Basic", Slug: "basic",
			Type: domain.ProductSharedHosting, Module: domain.ModuleCpanel,
			PackageName: "pkg", ServerGroupID: &sg, AutoSetup: domain.SetupOnPayment,
		}, nil
	}
	var saved *domain.Product
	f.products.UpdateFn = func(ctx context.Context, pr *domain.Product) error {
		saved = pr
		return nil
	}
	_, err := f.svc.UpdateProduct(context.Background(), 1, 10, catalog.ProductUpdateInput{
		Name:          ptr("Basic v2"),
		ServerGroupID: ptr(int64(0)),
		Hidden:        ptr(true),
	})
	require.NoError(t, err)
	assert.Equal(t, "Basic v2", saved.Name)
	assert.Nil(t, saved.ServerGroupID, "server_group_id=0 clears the binding")
	assert.True(t, saved.Hidden)
	assert.Equal(t, "basic", saved.Slug)
}

func TestUpdateProductModuleWithoutPackageRejected(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id, GroupID: 1, Name: "Basic", Slug: "basic",
			Type: domain.ProductOther, Module: domain.ModuleNone, AutoSetup: domain.SetupOnPayment}, nil
	}
	_, err := f.svc.UpdateProduct(context.Background(), 1, 10, catalog.ProductUpdateInput{
		Module: ptr("directadmin"),
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestUpdateProductConfigurableAllowsEmptyPackage(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id, GroupID: 1, Name: "Basic", Slug: "basic",
			Type: domain.ProductOther, Module: domain.ModuleCpanel, PackageName: "pkg",
			AutoSetup: domain.SetupOnPayment}, nil
	}
	var saved *domain.Product
	f.products.UpdateFn = func(ctx context.Context, pr *domain.Product) error {
		saved = pr
		return nil
	}
	_, err := f.svc.UpdateProduct(context.Background(), 1, 10, catalog.ProductUpdateInput{
		Configurable: ptr(true),
		PackageName:  ptr(""),
	})
	require.NoError(t, err)
	assert.True(t, saved.Configurable)
	assert.Empty(t, saved.PackageName)
}

func TestUpdateProductFeatureListRequiresCpanelModule(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id, GroupID: 1, Name: "Basic", Slug: "basic",
			Type: domain.ProductOther, Module: domain.ModuleDirectAdmin, PackageName: "pkg",
			AutoSetup: domain.SetupOnPayment}, nil
	}
	_, err := f.svc.UpdateProduct(context.Background(), 1, 10, catalog.ProductUpdateInput{
		FeatureList: ptr("custom_features"),
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestUpdateProductTemplatePackageRequiresDirectAdminModule(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id, GroupID: 1, Name: "Basic", Slug: "basic",
			Type: domain.ProductOther, Module: domain.ModuleCpanel, PackageName: "pkg",
			AutoSetup: domain.SetupOnPayment}, nil
	}
	_, err := f.svc.UpdateProduct(context.Background(), 1, 10, catalog.ProductUpdateInput{
		TemplatePackage: ptr("template_pkg"),
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestDeleteProductSoftDeletes(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id}, nil
	}
	var deletedID int64
	f.products.SoftDeleteFn = func(ctx context.Context, id int64) error {
		deletedID = id
		return nil
	}
	require.NoError(t, f.svc.DeleteProduct(context.Background(), 1, 10))
	assert.Equal(t, int64(10), deletedID)
	require.Len(t, f.audit.Entries, 1)
	assert.Equal(t, "catalog.product.delete", f.audit.Entries[0].Action)
}

// sourceProductForDuplicate is the product DuplicateProduct clones from in
// the tests below.
func sourceProductForDuplicate() *domain.Product {
	return &domain.Product{
		ID: 10, GroupID: 3, Name: "Basic Hosting", Slug: "basic-hosting",
		Description: "desc", Type: domain.ProductSharedHosting, Module: domain.ModuleCpanel,
		PackageName: "starter", AutoSetup: domain.SetupOnPayment, StockEnabled: true, StockQty: 5,
		Hidden: false, Sort: 2, WelcomeEmailTemplate: "welcome",
		ShellAccess: true, CGIAccess: true, FeatureList: "custom_features",
	}
}

func TestDuplicateProductHappyPath(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		if id != 10 {
			return nil, apperr.NotFound("product")
		}
		return sourceProductForDuplicate(), nil
	}
	f.products.ListPricingFn = func(ctx context.Context, productID int64) ([]domain.ProductPricing, error) {
		return []domain.ProductPricing{
			{ID: 1, ProductID: 10, Cycle: domain.CycleMonthly, Price: 50000, SetupFee: 10000, Currency: "IDR"},
		}, nil
	}
	f.specs.ListSpecsFn = func(ctx context.Context, productID int64) ([]domain.ProductSpec, error) {
		return []domain.ProductSpec{
			{ID: 20, ProductID: 10, Key: "disk", Label: "Disk", ProvisionKey: domain.SpecDisk,
				Unit: domain.UnitGB, IncludedQty: 5, MinQty: 1, MaxQty: 100, StepQty: 1,
				DefaultQty: 5, AllowUnlimited: true, Sort: 1},
		}, nil
	}
	f.specs.ListSpecPricingFn = func(ctx context.Context, specID int64) ([]domain.ProductSpecPricing, error) {
		return []domain.ProductSpecPricing{
			{ID: 30, SpecID: specID, Cycle: domain.CycleMonthly, UnitPrice: 1000, UnlimitedPrice: 20000, Currency: "IDR"},
		}, nil
	}
	var createdProduct *domain.Product
	f.products.CreateFn = func(ctx context.Context, pr *domain.Product) error {
		pr.ID = 99
		createdProduct = pr
		return nil
	}
	var createdPricing []*domain.ProductPricing
	f.products.UpsertPricingFn = func(ctx context.Context, pp *domain.ProductPricing) error {
		createdPricing = append(createdPricing, pp)
		return nil
	}
	var createdSpecs []*domain.ProductSpec
	f.specs.CreateSpecFn = func(ctx context.Context, s *domain.ProductSpec) error {
		s.ID = 40
		createdSpecs = append(createdSpecs, s)
		return nil
	}
	var createdSpecPricing []*domain.ProductSpecPricing
	f.specs.UpsertSpecPriceFn = func(ctx context.Context, p *domain.ProductSpecPricing) error {
		createdSpecPricing = append(createdSpecPricing, p)
		return nil
	}

	dup, err := f.svc.DuplicateProduct(context.Background(), 1, 10)
	require.NoError(t, err)

	require.NotNil(t, createdProduct)
	assert.Equal(t, int64(99), dup.ID)
	assert.Equal(t, "Basic Hosting (Copy)", dup.Name)
	assert.Equal(t, "basic-hosting-copy", dup.Slug)
	assert.True(t, dup.Hidden, "duplicate starts hidden until reviewed")
	assert.Equal(t, int64(3), dup.GroupID)
	assert.Equal(t, domain.ModuleCpanel, dup.Module)
	assert.Equal(t, "starter", dup.PackageName)
	assert.True(t, dup.ShellAccess, "package options must carry over to the copy")
	assert.True(t, dup.CGIAccess)
	assert.Equal(t, "custom_features", dup.FeatureList)

	require.Len(t, createdPricing, 1)
	assert.Equal(t, int64(99), createdPricing[0].ProductID)
	assert.Equal(t, int64(50000), createdPricing[0].Price)

	require.Len(t, createdSpecs, 1)
	assert.Equal(t, int64(99), createdSpecs[0].ProductID)
	assert.Equal(t, "disk", createdSpecs[0].Key)

	require.Len(t, createdSpecPricing, 1)
	assert.Equal(t, int64(40), createdSpecPricing[0].SpecID, "spec pricing points at the NEW spec, not the source")
	assert.Equal(t, int64(1000), createdSpecPricing[0].UnitPrice)

	require.Len(t, f.audit.Entries, 1)
	assert.Equal(t, "catalog.product.duplicate", f.audit.Entries[0].Action)
}

// TestDuplicateProductSlugFreeWhenGetBySlugErrorsNotFound matches the real
// repo's GetBySlug contract (repo.go): a missing slug comes back as a
// NotFound *error* with a nil product, not a nil product with a nil error.
// A naive `if err != nil { return err }` would misread "the copy slug is
// free" as a hard failure and 404 a DuplicateProduct call on a product that
// definitely exists (caught via E2E, not the other unit tests here, since
// the default fake's GetBySlugFn returns (nil, nil) instead).
func TestDuplicateProductSlugFreeWhenGetBySlugErrorsNotFound(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return sourceProductForDuplicate(), nil
	}
	f.products.GetBySlugFn = func(ctx context.Context, slug string) (*domain.Product, error) {
		return nil, apperr.NotFound("product")
	}
	var createdSlug string
	f.products.CreateFn = func(ctx context.Context, pr *domain.Product) error {
		pr.ID = 99
		createdSlug = pr.Slug
		return nil
	}

	_, err := f.svc.DuplicateProduct(context.Background(), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, "basic-hosting-copy", createdSlug)
}

func TestDuplicateProductSlugCollisionPicksNextSuffix(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return sourceProductForDuplicate(), nil
	}
	taken := map[string]bool{"basic-hosting-copy": true, "basic-hosting-copy-2": true}
	f.products.GetBySlugFn = func(ctx context.Context, slug string) (*domain.Product, error) {
		if taken[slug] {
			return &domain.Product{ID: 999, Slug: slug}, nil
		}
		return nil, apperr.NotFound("product") // matches the real repo: free slug -> NotFound error
	}
	var createdSlug string
	f.products.CreateFn = func(ctx context.Context, pr *domain.Product) error {
		pr.ID = 99
		createdSlug = pr.Slug
		return nil
	}

	_, err := f.svc.DuplicateProduct(context.Background(), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, "basic-hosting-copy-3", createdSlug)
}

func TestDuplicateProductSlugExhaustionConflicts(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return sourceProductForDuplicate(), nil
	}
	f.products.GetBySlugFn = func(ctx context.Context, slug string) (*domain.Product, error) {
		return &domain.Product{ID: 999, Slug: slug}, nil // every candidate is already taken
	}
	f.products.CreateFn = func(ctx context.Context, pr *domain.Product) error {
		t.Fatal("must not attempt to create once slug candidates are exhausted")
		return nil
	}

	_, err := f.svc.DuplicateProduct(context.Background(), 1, 10)
	assertCode(t, err, apperr.CodeConflict)
}

func TestDuplicateProductSourceNotFound(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return nil, nil
	}
	_, err := f.svc.DuplicateProduct(context.Background(), 1, 404)
	assertCode(t, err, apperr.CodeNotFound)
}

func TestGetProductNotFoundPassthrough(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return nil, apperr.NotFound("product")
	}
	_, err := f.svc.GetProduct(context.Background(), 99)
	assertCode(t, err, apperr.CodeNotFound)
}

func TestListProductsWrapsInternalErrors(t *testing.T) {
	f := newFixture()
	f.products.ListFn = func(ctx context.Context, p ports.ListParams) ([]domain.Product, int64, error) {
		return nil, 0, errors.New("boom")
	}
	_, _, err := f.svc.ListProducts(context.Background(), ports.ListParams{})
	assertCode(t, err, apperr.CodeInternal)
}

// Pricing (admin, IDR only)

func TestUpsertPricingRejectsNonIDR(t *testing.T) {
	f := newFixture()
	_, err := f.svc.UpsertPricing(context.Background(), 1, 10, catalog.PricingInput{
		Cycle: "monthly", Price: 50_000, Currency: "USD",
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestUpsertPricingDefaultsToIDR(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id}, nil
	}
	var saved *domain.ProductPricing
	f.products.UpsertPricingFn = func(ctx context.Context, pp *domain.ProductPricing) error {
		saved = pp
		return nil
	}
	pp, err := f.svc.UpsertPricing(context.Background(), 1, 10, catalog.PricingInput{
		Cycle: "monthly", Price: 50_000, SetupFee: 10_000,
	})
	require.NoError(t, err)
	assert.Equal(t, "IDR", saved.Currency)
	assert.Equal(t, domain.CycleMonthly, pp.Cycle)
	require.Len(t, f.audit.Entries, 1)
}

func TestUpsertPricingInvalidCycle(t *testing.T) {
	f := newFixture()
	_, err := f.svc.UpsertPricing(context.Background(), 1, 10, catalog.PricingInput{
		Cycle: "weekly", Price: 1000,
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestUpsertPricingNegativePrice(t *testing.T) {
	f := newFixture()
	_, err := f.svc.UpsertPricing(context.Background(), 1, 10, catalog.PricingInput{
		Cycle: "monthly", Price: -1,
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestDeletePricing(t *testing.T) {
	f := newFixture()
	var gotCycle domain.BillingCycle
	f.products.DeletePricingFn = func(ctx context.Context, productID int64, cycle domain.BillingCycle) error {
		gotCycle = cycle
		return nil
	}
	require.NoError(t, f.svc.DeletePricing(context.Background(), 1, 10, "annually"))
	assert.Equal(t, domain.CycleAnnually, gotCycle)

	err := f.svc.DeletePricing(context.Background(), 1, 10, "weekly")
	assertCode(t, err, apperr.CodeValidation)
}

func TestListPricingChecksProduct(t *testing.T) {
	f := newFixture()
	f.products.GetByIDFn = func(ctx context.Context, id int64) (*domain.Product, error) {
		return nil, apperr.NotFound("product")
	}
	_, err := f.svc.ListPricing(context.Background(), 99)
	assertCode(t, err, apperr.CodeNotFound)
}

// Configurable options (admin)

func TestOptionTreeBuildsNestedTreeFromBatchedLoads(t *testing.T) {
	f := newFixture()
	f.products.ListOptionGroupsFn = func(ctx context.Context) ([]domain.ConfigurableOptionGroup, error) {
		return []domain.ConfigurableOptionGroup{{ID: 1, Name: "Extras"}, {ID: 2, Name: "Empty"}}, nil
	}
	f.products.ListOptionsByGroupIDsFn = func(ctx context.Context, groupIDs []int64) (map[int64][]domain.ConfigurableOption, error) {
		assert.ElementsMatch(t, []int64{1, 2}, groupIDs)
		return map[int64][]domain.ConfigurableOption{
			1: {{ID: 10, GroupID: 1, Name: "RAM"}},
		}, nil
	}
	f.products.ListOptionValuesByOptionIDsFn = func(ctx context.Context, optionIDs []int64) (map[int64][]domain.ConfigurableOptionValue, error) {
		assert.Equal(t, []int64{10}, optionIDs)
		return map[int64][]domain.ConfigurableOptionValue{
			10: {{ID: 100, OptionID: 10, Name: "2GB"}},
		}, nil
	}

	tree, err := f.svc.OptionTree(context.Background())
	require.NoError(t, err)
	require.Len(t, tree, 2)
	require.Len(t, tree[0].Options, 1)
	assert.Equal(t, "2GB", tree[0].Options[0].Values[0].Name)
	assert.Empty(t, tree[1].Options)
}

func TestCreateOptionValueRejectsBadDeltas(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateOptionValue(context.Background(), 1, 2, catalog.OptionValueInput{
		Name: "2GB", PriceDeltas: map[string]int64{"weekly": 1000},
	})
	assertCode(t, err, apperr.CodeValidation)

	_, err = f.svc.CreateOptionValue(context.Background(), 1, 2, catalog.OptionValueInput{
		Name: "2GB", PriceDeltas: map[string]int64{"monthly": -1000},
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestCreateOptionValueMarshalsDeltas(t *testing.T) {
	f := newFixture()
	var created *domain.ConfigurableOptionValue
	f.options.CreateOptionValueFn = func(ctx context.Context, v *domain.ConfigurableOptionValue) error {
		v.ID = 3
		created = v
		return nil
	}
	v, err := f.svc.CreateOptionValue(context.Background(), 1, 2, catalog.OptionValueInput{
		Name: "2GB", PriceDeltas: map[string]int64{"monthly": 10_000},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"monthly":10000}`, string(created.PriceDeltas))
	assert.Equal(t, int64(2), v.OptionID)
}

func TestCreateOptionUnknownGroup(t *testing.T) {
	f := newFixture()
	f.options.GetOptionGroupByIDFn = func(ctx context.Context, id int64) (*domain.ConfigurableOptionGroup, error) {
		return nil, apperr.NotFound("option group")
	}
	_, err := f.svc.CreateOption(context.Background(), 1, 9, catalog.OptionInput{Name: "RAM"})
	assertCode(t, err, apperr.CodeNotFound)
}

func TestUpdateOptionGroup(t *testing.T) {
	f := newFixture()
	f.options.GetOptionGroupByIDFn = func(ctx context.Context, id int64) (*domain.ConfigurableOptionGroup, error) {
		return &domain.ConfigurableOptionGroup{ID: id, Name: "Old"}, nil
	}
	var saved *domain.ConfigurableOptionGroup
	f.options.UpdateOptionGroupFn = func(ctx context.Context, g *domain.ConfigurableOptionGroup) error {
		saved = g
		return nil
	}
	_, err := f.svc.UpdateOptionGroup(context.Background(), 1, 5, catalog.OptionGroupInput{Name: "Extras", Description: "d"})
	require.NoError(t, err)
	assert.Equal(t, "Extras", saved.Name)
}

func TestDeleteOptionEntitiesInvalidateCache(t *testing.T) {
	f := newFixture()
	require.NoError(t, f.cache.SetJSON(context.Background(), "catalog:products:v2", []string{"x"}, time.Minute))
	require.NoError(t, f.svc.DeleteOptionGroup(context.Background(), 1, 5))
	assert.NotContains(t, f.cache.m, "catalog:products:v2")

	require.NoError(t, f.svc.DeleteOption(context.Background(), 1, 5))
	require.NoError(t, f.svc.DeleteOptionValue(context.Background(), 1, 5))
	assert.Len(t, f.audit.Entries, 3)
}

func TestUpdateOptionValue(t *testing.T) {
	f := newFixture()
	f.options.GetOptionValueByIDFn = func(ctx context.Context, id int64) (*domain.ConfigurableOptionValue, error) {
		return &domain.ConfigurableOptionValue{ID: id, OptionID: 2, Name: "Old"}, nil
	}
	var saved *domain.ConfigurableOptionValue
	f.options.UpdateOptionValueFn = func(ctx context.Context, v *domain.ConfigurableOptionValue) error {
		saved = v
		return nil
	}
	_, err := f.svc.UpdateOptionValue(context.Background(), 1, 3, catalog.OptionValueInput{
		Name: "4GB", PriceDeltas: map[string]int64{"annually": 100_000}, Sort: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, "4GB", saved.Name)
	assert.JSONEq(t, `{"annually":100000}`, string(saved.PriceDeltas))
}

func TestUpdateOption(t *testing.T) {
	f := newFixture()
	f.options.GetOptionByIDFn = func(ctx context.Context, id int64) (*domain.ConfigurableOption, error) {
		return &domain.ConfigurableOption{ID: id, GroupID: 1, Name: "Old"}, nil
	}
	var saved *domain.ConfigurableOption
	f.options.UpdateOptionFn = func(ctx context.Context, o *domain.ConfigurableOption) error {
		saved = o
		return nil
	}
	_, err := f.svc.UpdateOption(context.Background(), 1, 2, catalog.OptionInput{Name: "CPU", Sort: 3})
	require.NoError(t, err)
	assert.Equal(t, "CPU", saved.Name)
	assert.Equal(t, 3, saved.Sort)
}

func TestCreateOptionGroupAndOption(t *testing.T) {
	f := newFixture()
	f.options.CreateOptionGroupFn = func(ctx context.Context, g *domain.ConfigurableOptionGroup) error {
		g.ID = 1
		return nil
	}
	g, err := f.svc.CreateOptionGroup(context.Background(), 7, catalog.OptionGroupInput{Name: "Extras"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), g.ID)

	f.options.CreateOptionFn = func(ctx context.Context, o *domain.ConfigurableOption) error {
		o.ID = 2
		return nil
	}
	o, err := f.svc.CreateOption(context.Background(), 7, 1, catalog.OptionInput{Name: "RAM"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), o.GroupID)
}

// Coupons (admin)

func TestCreateCouponUppercasesAndDefaultsActive(t *testing.T) {
	f := newFixture()
	var created *domain.Coupon
	f.coupons.CreateFn = func(ctx context.Context, c *domain.Coupon) error {
		c.ID = 7
		created = c
		return nil
	}
	c, err := f.svc.CreateCoupon(context.Background(), 1, catalog.CouponInput{
		Code: " save10 ", Type: "percentage", Value: 10, ProductIDs: []int64{1, 2},
	})
	require.NoError(t, err)
	assert.Equal(t, "SAVE10", created.Code)
	assert.True(t, created.Active)
	assert.JSONEq(t, `{"product_ids":[1,2]}`, string(created.AppliesTo))
	assert.Equal(t, int64(7), c.ID)
	require.Len(t, f.audit.Entries, 1)
}

func TestCreateCouponPercentageBounds(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateCoupon(context.Background(), 1, catalog.CouponInput{
		Code: "BIG", Type: "percentage", Value: 101,
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestCreateCouponPastExpiry(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateCoupon(context.Background(), 1, catalog.CouponInput{
		Code: "OLD", Type: "fixed", Value: 1000,
		ExpiresAt: ptr(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestUpdateCoupon(t *testing.T) {
	f := newFixture()
	f.coupons.GetByIDFn = func(ctx context.Context, id int64) (*domain.Coupon, error) {
		exp := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		return &domain.Coupon{
			ID: id, Code: "SAVE10", Type: domain.CouponPercentage, Value: 10,
			AppliesTo: json.RawMessage(`{}`), ExpiresAt: &exp, Active: true,
		}, nil
	}
	var saved *domain.Coupon
	f.coupons.UpdateFn = func(ctx context.Context, c *domain.Coupon) error {
		saved = c
		return nil
	}
	c, err := f.svc.UpdateCoupon(context.Background(), 1, 7, catalog.CouponUpdateInput{
		Value:       ptr(int64(20)),
		Active:      ptr(false),
		ClearExpiry: true,
		ProductIDs:  []int64{3},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(20), saved.Value)
	assert.False(t, saved.Active)
	assert.Nil(t, saved.ExpiresAt)
	assert.JSONEq(t, `{"product_ids":[3]}`, string(c.AppliesTo))
}

func TestUpdateCouponPercentageBounds(t *testing.T) {
	f := newFixture()
	f.coupons.GetByIDFn = func(ctx context.Context, id int64) (*domain.Coupon, error) {
		return &domain.Coupon{ID: id, Type: domain.CouponPercentage, Value: 10, Active: true}, nil
	}
	_, err := f.svc.UpdateCoupon(context.Background(), 1, 7, catalog.CouponUpdateInput{
		Value: ptr(int64(150)),
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestDeleteCoupon(t *testing.T) {
	f := newFixture()
	f.coupons.GetByIDFn = func(ctx context.Context, id int64) (*domain.Coupon, error) {
		return &domain.Coupon{ID: id, Code: "SAVE10"}, nil
	}
	var deletedID int64
	f.coupons.DeleteFn = func(ctx context.Context, id int64) error {
		deletedID = id
		return nil
	}
	require.NoError(t, f.svc.DeleteCoupon(context.Background(), 1, 7))
	assert.Equal(t, int64(7), deletedID)
	require.Len(t, f.audit.Entries, 1)
}

func TestDeleteCouponConflictPassthrough(t *testing.T) {
	f := newFixture()
	f.coupons.GetByIDFn = func(ctx context.Context, id int64) (*domain.Coupon, error) {
		return &domain.Coupon{ID: id}, nil
	}
	f.coupons.DeleteFn = func(ctx context.Context, id int64) error {
		return apperr.Conflict("coupon is referenced by or references other records")
	}
	err := f.svc.DeleteCoupon(context.Background(), 1, 7)
	assertCode(t, err, apperr.CodeConflict)
}

func TestGetCouponNotFound(t *testing.T) {
	f := newFixture()
	f.coupons.GetByIDFn = func(ctx context.Context, id int64) (*domain.Coupon, error) {
		return nil, apperr.NotFound("coupon")
	}
	_, err := f.svc.GetCoupon(context.Background(), 404)
	assertCode(t, err, apperr.CodeNotFound)
}

func TestListCoupons(t *testing.T) {
	f := newFixture()
	f.coupons.ListFn = func(ctx context.Context, p ports.ListParams) ([]domain.Coupon, int64, error) {
		return []domain.Coupon{{ID: 1}}, 1, nil
	}
	rows, total, err := f.svc.ListCoupons(context.Background(), ports.ListParams{})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, int64(1), total)
}

func TestListGroupsAdminIncludesHidden(t *testing.T) {
	f := newFixture()
	var gotInclude bool
	f.products.ListGroupsFn = func(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
		gotInclude = includeHidden
		return nil, nil
	}
	_, err := f.svc.ListGroups(context.Background(), true)
	require.NoError(t, err)
	assert.True(t, gotInclude)
}
