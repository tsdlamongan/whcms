// Package catalog implements the M-CATALOG module: product groups, products,
// per-cycle pricing (IDR only), configurable option groups/options/values,
// coupons (CRUD + validation) and the cached public catalog.
//
// Route table (mounted on the /api/v1 group by the composition root):
//
//	GET    /product-groups                          public, visible groups (cached)
//	GET    /products                                public catalog, grouped visible products (cached 10m)
//	GET    /products/:slug                          public product detail + option catalog
//	POST   /coupons/validate                        public coupon validation {code, product_ids, subtotal}
//	GET    /admin/product-groups                    ?include_hidden                    [perm: products]
//	POST   /admin/product-groups                                                       [perm: products]
//	GET    /admin/product-groups/:id                                                   [perm: products]
//	PATCH  /admin/product-groups/:id                                                   [perm: products]
//	DELETE /admin/product-groups/:id                blocked while products exist       [perm: products]
//	GET    /admin/products                          ?search&status=visible|hidden      [perm: products]
//	POST   /admin/products                                                             [perm: products]
//	GET    /admin/products/:id                                                         [perm: products]
//	PATCH  /admin/products/:id                                                         [perm: products]
//	DELETE /admin/products/:id                      soft delete                        [perm: products]
//	POST   /admin/products/:id/duplicate            clone incl. pricing+specs          [perm: products]
//	GET    /admin/products/:id/pricing                                                 [perm: products]
//	PUT    /admin/products/:id/pricing              upsert one cycle (IDR only)        [perm: products]
//	DELETE /admin/products/:id/pricing/:cycle                                          [perm: products]
//	GET    /admin/products/:id/specs                dynamic-spec knobs + pricing        [perm: products]
//	POST   /admin/products/:id/specs                                                    [perm: products]
//	PATCH  /admin/product-specs/:id                                                     [perm: products]
//	DELETE /admin/product-specs/:id                                                     [perm: products]
//	PUT    /admin/product-specs/:id/pricing         upsert one cycle (IDR only)         [perm: products]
//	DELETE /admin/product-specs/:id/pricing/:cycle                                      [perm: products]
//	GET    /admin/config-options                    full group->option->value tree       [perm: products]
//	POST   /admin/config-options/groups                                                [perm: products]
//	PATCH  /admin/config-options/groups/:id                                            [perm: products]
//	DELETE /admin/config-options/groups/:id                                            [perm: products]
//	POST   /admin/config-options/groups/:id/options                                    [perm: products]
//	PATCH  /admin/config-options/options/:id                                           [perm: products]
//	DELETE /admin/config-options/options/:id                                           [perm: products]
//	POST   /admin/config-options/options/:id/values                                    [perm: products]
//	PATCH  /admin/config-options/values/:id                                            [perm: products]
//	DELETE /admin/config-options/values/:id                                            [perm: products]
//	GET    /admin/coupons                           ?search&status=active|inactive     [perm: products]
//	POST   /admin/coupons                                                              [perm: products]
//	GET    /admin/coupons/:id                                                          [perm: products]
//	PATCH  /admin/coupons/:id                                                          [perm: products]
//	DELETE /admin/coupons/:id                       blocked while referenced (FK)      [perm: products]
//
// Cross-module surface: *Repo implements ports.ProductRepo + ports.CouponRepo;
// *Service exposes ValidateCoupon(ctx, code, productIDs, subtotal) for M-ORDERS.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/validate"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Public catalog cache parameters (invalidated on every admin write).
const (
	// v2: cached payload gained per-product specs for configurable products;
	// the version suffix keeps a pre-upgrade cached blob (which would lack
	// specs) from being served until its TTL expires.
	cacheKeyCatalog = "catalog:products:v2"
	cacheKeyGroups  = "catalog:groups"
	catalogCacheTTL = 10 * time.Minute
)

// ProductStore is ports.ProductRepo plus the catalog-local queries the
// service needs (implemented by *Repo).
type ProductStore interface {
	ports.ProductRepo
	// CountProductsInGroup counts non-deleted products in a group (used to
	// block group deletion).
	CountProductsInGroup(ctx context.Context, groupID int64) (int64, error)
	// ListVisibleProducts returns all non-deleted, non-hidden products
	// ordered by sort, id.
	ListVisibleProducts(ctx context.Context) ([]domain.Product, error)
	// ListPricingByProducts returns IDR pricing rows grouped by product id.
	ListPricingByProducts(ctx context.Context, productIDs []int64) (map[int64][]domain.ProductPricing, error)
}

// OptionStore persists configurable option groups/options/values writes
// (reads live on ports.ProductRepo). Implemented by *Repo.
type OptionStore interface {
	CreateOptionGroup(ctx context.Context, g *domain.ConfigurableOptionGroup) error
	GetOptionGroupByID(ctx context.Context, id int64) (*domain.ConfigurableOptionGroup, error)
	UpdateOptionGroup(ctx context.Context, g *domain.ConfigurableOptionGroup) error
	DeleteOptionGroup(ctx context.Context, id int64) error
	CreateOption(ctx context.Context, o *domain.ConfigurableOption) error
	GetOptionByID(ctx context.Context, id int64) (*domain.ConfigurableOption, error)
	UpdateOption(ctx context.Context, o *domain.ConfigurableOption) error
	DeleteOption(ctx context.Context, id int64) error
	CreateOptionValue(ctx context.Context, v *domain.ConfigurableOptionValue) error
	GetOptionValueByID(ctx context.Context, id int64) (*domain.ConfigurableOptionValue, error)
	UpdateOptionValue(ctx context.Context, v *domain.ConfigurableOptionValue) error
	DeleteOptionValue(ctx context.Context, id int64) error
}

// SpecStore persists dynamic-product spec definitions and their per-cycle
// pricing (implemented by *Repo).
type SpecStore interface {
	ListSpecs(ctx context.Context, productID int64) ([]domain.ProductSpec, error)
	GetSpecByID(ctx context.Context, id int64) (*domain.ProductSpec, error)
	CreateSpec(ctx context.Context, s *domain.ProductSpec) error
	UpdateSpec(ctx context.Context, s *domain.ProductSpec) error
	DeleteSpec(ctx context.Context, id int64) error
	ListSpecPricing(ctx context.Context, specID int64) ([]domain.ProductSpecPricing, error)
	// ListSpecPricingBySpecIDs batch-loads pricing across many specs at once
	// (keyed by spec_id), to avoid an N+1 query per spec.
	ListSpecPricingBySpecIDs(ctx context.Context, specIDs []int64) (map[int64][]domain.ProductSpecPricing, error)
	GetSpecPricing(ctx context.Context, specID int64, cycle domain.BillingCycle) (*domain.ProductSpecPricing, error)
	UpsertSpecPricing(ctx context.Context, p *domain.ProductSpecPricing) error
	DeleteSpecPricing(ctx context.Context, specID int64, cycle domain.BillingCycle) error
}

// Deps are the service dependencies (wired by the composition root).
type Deps struct {
	Products ProductStore
	Options  OptionStore
	Specs    SpecStore
	Coupons  ports.CouponRepo
	Tx       ports.TxManager
	Cache    ports.Cache
	Audit    ports.AuditLogger
	Clock    ports.Clock
}

// Service implements the catalog use-cases.
type Service struct {
	d   Deps
	val *validate.Validator
}

// New builds the catalog Service.
func New(d Deps) *Service {
	return &Service{d: d, val: validate.New()}
}

// Helpers

// wrap passes *apperr.Error through and wraps anything else as INTERNAL.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	var e *apperr.Error
	if errors.As(err, &e) {
		return e
	}
	return apperr.Internal(err)
}

func isNotFound(err error) bool {
	var e *apperr.Error
	return errors.As(err, &e) && e.Code == apperr.CodeNotFound
}

// invalidateCatalogCache drops the public catalog cache (best effort; a
// cache hiccup must not fail the admin write).
func (s *Service) invalidateCatalogCache(ctx context.Context) {
	_ = s.d.Cache.Delete(ctx, cacheKeyCatalog)
	_ = s.d.Cache.Delete(ctx, cacheKeyGroups)
}

// slugify lowercases s and replaces every non-alphanumeric run with '-'.
func slugify(s string) string {
	var b strings.Builder
	prevDash := true // trims leading dashes
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// checkPriceDeltas validates option-value price delta keys/amounts.
func checkPriceDeltas(deltas map[string]int64) error {
	var details []apperr.FieldError
	for cycle, amount := range deltas {
		if !domain.BillingCycle(cycle).Valid() {
			details = append(details, apperr.FieldError{Field: "price_deltas." + cycle, Message: "unknown billing cycle"})
		}
		if amount < 0 {
			details = append(details, apperr.FieldError{Field: "price_deltas." + cycle, Message: "must be at least 0"})
		}
	}
	if len(details) > 0 {
		return apperr.Validation("invalid price deltas", details...)
	}
	return nil
}

// Public catalog

// PublicCatalog returns the visible catalog grouped by product group,
// cached for 10 minutes and invalidated on every admin write.
func (s *Service) PublicCatalog(ctx context.Context) ([]PublicGroup, error) {
	var cached []PublicGroup
	if hit, err := s.d.Cache.GetJSON(ctx, cacheKeyCatalog, &cached); err == nil && hit {
		return cached, nil
	}

	groups, err := s.d.Products.ListGroups(ctx, false)
	if err != nil {
		return nil, wrap(err)
	}
	products, err := s.d.Products.ListVisibleProducts(ctx)
	if err != nil {
		return nil, wrap(err)
	}
	ids := make([]int64, 0, len(products))
	for _, p := range products {
		ids = append(ids, p.ID)
	}
	pricing, err := s.d.Products.ListPricingByProducts(ctx, ids)
	if err != nil {
		return nil, wrap(err)
	}

	byGroup := make(map[int64][]PublicProduct, len(groups))
	for _, p := range products {
		pub := toPublicProduct(p, pricing[p.ID])
		if p.Configurable {
			// Attach the spec knobs so catalog consumers (e.g. the client
			// upgrade modal) can configure without a detail fetch; the result
			// is cached below, so this per-product cost is amortized.
			specs, err := s.publicSpecs(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			pub.Specs = specs
		}
		byGroup[p.GroupID] = append(byGroup[p.GroupID], pub)
	}
	out := make([]PublicGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, PublicGroup{
			ID: g.ID, Name: g.Name, Slug: g.Slug, Sort: g.Sort,
			Products: byGroup[g.ID],
		})
	}

	_ = s.d.Cache.SetJSON(ctx, cacheKeyCatalog, out, catalogCacheTTL) // best effort
	return out, nil
}

// PublicGroups returns the visible product groups (cached 10m).
func (s *Service) PublicGroups(ctx context.Context) ([]domain.ProductGroup, error) {
	var cached []domain.ProductGroup
	if hit, err := s.d.Cache.GetJSON(ctx, cacheKeyGroups, &cached); err == nil && hit {
		return cached, nil
	}
	groups, err := s.d.Products.ListGroups(ctx, false)
	if err != nil {
		return nil, wrap(err)
	}
	_ = s.d.Cache.SetJSON(ctx, cacheKeyGroups, groups, catalogCacheTTL)
	return groups, nil
}

// PublicProductBySlug returns one visible product with pricing and the
// configurable option catalog. Hidden/deleted products (or products in a
// hidden group) are NOT_FOUND.
func (s *Service) PublicProductBySlug(ctx context.Context, slug string) (*PublicProductDetail, error) {
	p, err := s.d.Products.GetBySlug(ctx, slug)
	if err != nil {
		return nil, wrap(err)
	}
	if p == nil || p.Hidden || p.DeletedAt != nil {
		return nil, apperr.NotFound("product")
	}
	group, err := s.d.Products.GetGroupByID(ctx, p.GroupID)
	if err != nil && !isNotFound(err) {
		return nil, wrap(err)
	}
	if group == nil || group.Hidden || group.DeletedAt != nil {
		return nil, apperr.NotFound("product")
	}
	pricing, err := s.d.Products.ListPricing(ctx, p.ID)
	if err != nil {
		return nil, wrap(err)
	}
	options, err := s.OptionTree(ctx)
	if err != nil {
		return nil, err
	}
	var specs []PublicSpec
	if p.Configurable {
		specs, err = s.publicSpecs(ctx, p.ID)
		if err != nil {
			return nil, err
		}
	}
	return &PublicProductDetail{
		PublicProduct: toPublicProduct(*p, pricing),
		OptionGroups:  options,
		Specs:         specs,
	}, nil
}

// publicSpecs builds the customer-facing spec knobs (with per-cycle pricing)
// for a configurable product.
func (s *Service) publicSpecs(ctx context.Context, productID int64) ([]PublicSpec, error) {
	specs, err := s.d.Specs.ListSpecs(ctx, productID)
	if err != nil {
		return nil, wrap(err)
	}
	specIDs := make([]int64, len(specs))
	for i, sp := range specs {
		specIDs[i] = sp.ID
	}
	pricingBySpec, err := s.d.Specs.ListSpecPricingBySpecIDs(ctx, specIDs)
	if err != nil {
		return nil, wrap(err)
	}
	out := make([]PublicSpec, 0, len(specs))
	for _, sp := range specs {
		pricing := pricingBySpec[sp.ID]
		prices := make([]PublicSpecPrice, 0, len(pricing))
		for _, pp := range pricing {
			prices = append(prices, PublicSpecPrice{
				Cycle: pp.Cycle, UnitPrice: pp.UnitPrice,
				UnlimitedPrice: pp.UnlimitedPrice, Currency: pp.Currency,
			})
		}
		out = append(out, PublicSpec{
			Key: sp.Key, Label: sp.Label, ProvisionKey: sp.ProvisionKey, Unit: sp.Unit,
			IncludedQty: sp.IncludedQty, MinQty: sp.MinQty, MaxQty: sp.MaxQty,
			StepQty: sp.StepQty, DefaultQty: sp.DefaultQty, AllowUnlimited: sp.AllowUnlimited,
			Pricing: prices,
		})
	}
	return out, nil
}

func toPublicProduct(p domain.Product, pricing []domain.ProductPricing) PublicProduct {
	prices := make([]PublicPrice, 0, len(pricing))
	for _, pp := range pricing {
		prices = append(prices, PublicPrice{
			Cycle: pp.Cycle, Price: pp.Price, SetupFee: pp.SetupFee, Currency: pp.Currency,
		})
	}
	return PublicProduct{
		ID: p.ID, GroupID: p.GroupID, Name: p.Name, Slug: p.Slug,
		Description: p.Description, Type: p.Type,
		Configurable: p.Configurable,
		StockEnabled: p.StockEnabled,
		InStock:      !p.StockEnabled || p.StockQty > 0,
		Pricing:      prices,
	}
}

// Coupon validation (also consumed by M-ORDERS)

// ValidateCoupon checks a coupon code against the order contents and returns
// the discount amount (whole IDR, clamped to subtotal) plus the coupon.
// Enforces active, expiry, max_uses and applies_to.
func (s *Service) ValidateCoupon(ctx context.Context, code string, productIDs []int64, subtotal int64) (int64, *domain.Coupon, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return 0, nil, apperr.Validation("coupon code is required")
	}
	if subtotal < 0 {
		return 0, nil, apperr.Validation("subtotal must be at least 0")
	}

	coupon, err := s.d.Coupons.GetByCode(ctx, code)
	if err != nil {
		return 0, nil, wrap(err)
	}
	if coupon == nil {
		return 0, nil, apperr.NotFound("coupon")
	}
	if !coupon.Active {
		return 0, nil, apperr.Validation("coupon is not active")
	}
	if coupon.ExpiresAt != nil && !coupon.ExpiresAt.After(s.d.Clock.Now()) {
		return 0, nil, apperr.Validation("coupon has expired")
	}
	if coupon.MaxUses > 0 && coupon.UsedCount >= coupon.MaxUses {
		return 0, nil, apperr.Conflict("coupon usage limit reached")
	}

	var applies CouponAppliesTo
	if len(coupon.AppliesTo) > 0 {
		if err := json.Unmarshal(coupon.AppliesTo, &applies); err != nil {
			return 0, nil, apperr.Internal(err)
		}
	}
	if len(applies.ProductIDs) > 0 {
		allowed := make(map[int64]bool, len(applies.ProductIDs))
		for _, id := range applies.ProductIDs {
			allowed[id] = true
		}
		match := false
		for _, id := range productIDs {
			if allowed[id] {
				match = true
				break
			}
		}
		if !match {
			return 0, nil, apperr.Validation("coupon does not apply to the selected products")
		}
	}

	var discount int64
	switch coupon.Type {
	case domain.CouponPercentage:
		discount = subtotal * coupon.Value / 100
	case domain.CouponFixed:
		discount = coupon.Value
	default:
		return 0, nil, apperr.Internal(errors.New("catalog: unknown coupon type " + string(coupon.Type)))
	}
	if discount > subtotal {
		discount = subtotal
	}
	if discount < 0 {
		discount = 0
	}
	return discount, coupon, nil
}

// Product groups (admin)

// ListGroups lists product groups (admin; includeHidden toggles hidden rows).
func (s *Service) ListGroups(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
	groups, err := s.d.Products.ListGroups(ctx, includeHidden)
	return groups, wrap(err)
}

// GetGroup returns one product group.
func (s *Service) GetGroup(ctx context.Context, id int64) (*domain.ProductGroup, error) {
	g, err := s.d.Products.GetGroupByID(ctx, id)
	if err != nil {
		return nil, wrap(err)
	}
	if g == nil {
		return nil, apperr.NotFound("product group")
	}
	return g, nil
}

// CreateGroup creates a product group.
func (s *Service) CreateGroup(ctx context.Context, actorUserID int64, in GroupInput) (*domain.ProductGroup, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	slug := in.Slug
	if slug == "" {
		slug = slugify(in.Name)
	}
	if slug == "" {
		return nil, apperr.Validation("invalid slug", apperr.FieldError{Field: "slug", Message: "cannot be derived from name"})
	}
	g := &domain.ProductGroup{Name: in.Name, Slug: slug, Sort: in.Sort, Hidden: in.Hidden}
	if err := s.d.Products.CreateGroup(ctx, g); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.group.create", "product_group", g.ID, nil, g)
	s.invalidateCatalogCache(ctx)
	return g, nil
}

// UpdateGroup patches a product group.
func (s *Service) UpdateGroup(ctx context.Context, actorUserID, id int64, in GroupUpdateInput) (*domain.ProductGroup, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	g, err := s.GetGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	before := *g
	if in.Name != nil {
		g.Name = *in.Name
	}
	if in.Slug != nil {
		g.Slug = *in.Slug
	}
	if in.Sort != nil {
		g.Sort = *in.Sort
	}
	if in.Hidden != nil {
		g.Hidden = *in.Hidden
	}
	if err := s.d.Products.UpdateGroup(ctx, g); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.group.update", "product_group", g.ID, before, g)
	s.invalidateCatalogCache(ctx)
	return g, nil
}

// DeleteGroup soft-deletes a product group. Groups that still contain
// non-deleted products cannot be deleted (CONFLICT).
func (s *Service) DeleteGroup(ctx context.Context, actorUserID, id int64) error {
	g, err := s.GetGroup(ctx, id)
	if err != nil {
		return err
	}
	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		n, err := s.d.Products.CountProductsInGroup(txCtx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("product group still has products")
		}
		return s.d.Products.SoftDeleteGroup(txCtx, id)
	})
	if err != nil {
		return wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.group.delete", "product_group", id, g, nil)
	s.invalidateCatalogCache(ctx)
	return nil
}

// Products (admin)

// ListProducts lists products (admin; search on name/slug, status
// visible|hidden).
func (s *Service) ListProducts(ctx context.Context, p ports.ListParams) ([]domain.Product, int64, error) {
	rows, total, err := s.d.Products.List(ctx, p)
	return rows, total, wrap(err)
}

// GetProduct returns one product.
func (s *Service) GetProduct(ctx context.Context, id int64) (*domain.Product, error) {
	p, err := s.d.Products.GetByID(ctx, id)
	if err != nil {
		return nil, wrap(err)
	}
	if p == nil {
		return nil, apperr.NotFound("product")
	}
	return p, nil
}

// CreateProduct creates a product.
func (s *Service) CreateProduct(ctx context.Context, actorUserID int64, in ProductInput) (*domain.Product, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if in.Module == "" {
		in.Module = string(domain.ModuleNone)
	}
	if in.AutoSetup == "" {
		in.AutoSetup = string(domain.SetupOnPayment)
	}
	// Configurable products ignore PackageName entirely - provisioning builds and
	// manages a per-service package from the chosen specs instead (see
	// provisioning.Service.buildPackageSpec) - so it's never required here.
	if in.Module != string(domain.ModuleNone) && !in.Configurable && in.PackageName == "" {
		return nil, apperr.Validation("invalid product",
			apperr.FieldError{Field: "package_name", Message: "is required when a provisioning module is set"})
	}
	if in.Configurable && in.Module == string(domain.ModuleNone) {
		return nil, apperr.Validation("invalid product",
			apperr.FieldError{Field: "configurable", Message: "requires a provisioning module (cpanel/directadmin)"})
	}
	if in.FeatureList != "" && in.Module != string(domain.ModuleCpanel) {
		return nil, apperr.Validation("invalid product",
			apperr.FieldError{Field: "feature_list", Message: "only applies to cpanel products"})
	}
	if in.TemplatePackage != "" && in.Module != string(domain.ModuleDirectAdmin) {
		return nil, apperr.Validation("invalid product",
			apperr.FieldError{Field: "template_package", Message: "only applies to directadmin products"})
	}
	if g, err := s.GetGroup(ctx, in.GroupID); err != nil || g == nil {
		if err != nil && !isNotFound(err) {
			return nil, err
		}
		return nil, apperr.Validation("invalid product",
			apperr.FieldError{Field: "group_id", Message: "unknown product group"})
	}
	slug := in.Slug
	if slug == "" {
		slug = slugify(in.Name)
	}
	if slug == "" {
		return nil, apperr.Validation("invalid slug", apperr.FieldError{Field: "slug", Message: "cannot be derived from name"})
	}
	p := &domain.Product{
		GroupID:              in.GroupID,
		Name:                 in.Name,
		Slug:                 slug,
		Description:          in.Description,
		Type:                 domain.ProductType(in.Type),
		Module:               domain.ServerModuleName(in.Module),
		ServerGroupID:        in.ServerGroupID,
		PackageName:          in.PackageName,
		AutoSetup:            domain.AutoSetup(in.AutoSetup),
		Configurable:         in.Configurable,
		ShellAccess:          in.ShellAccess,
		CGIAccess:            in.CGIAccess,
		FeatureList:          in.FeatureList,
		TemplatePackage:      in.TemplatePackage,
		StockEnabled:         in.StockEnabled,
		StockQty:             in.StockQty,
		Hidden:               in.Hidden,
		Sort:                 in.Sort,
		WelcomeEmailTemplate: in.WelcomeEmailTemplate,
	}
	if err := s.d.Products.Create(ctx, p); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.product.create", "product", p.ID, nil, p)
	s.invalidateCatalogCache(ctx)
	return p, nil
}

// UpdateProduct patches a product.
func (s *Service) UpdateProduct(ctx context.Context, actorUserID, id int64, in ProductUpdateInput) (*domain.Product, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	p, err := s.GetProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	before := *p
	if in.GroupID != nil {
		if g, err := s.GetGroup(ctx, *in.GroupID); err != nil || g == nil {
			if err != nil && !isNotFound(err) {
				return nil, err
			}
			return nil, apperr.Validation("invalid product",
				apperr.FieldError{Field: "group_id", Message: "unknown product group"})
		}
		p.GroupID = *in.GroupID
	}
	if in.Name != nil {
		p.Name = *in.Name
	}
	if in.Slug != nil {
		p.Slug = *in.Slug
	}
	if in.Description != nil {
		p.Description = *in.Description
	}
	if in.Type != nil {
		p.Type = domain.ProductType(*in.Type)
	}
	if in.Module != nil {
		p.Module = domain.ServerModuleName(*in.Module)
	}
	if in.ServerGroupID != nil {
		if *in.ServerGroupID == 0 {
			p.ServerGroupID = nil
		} else {
			p.ServerGroupID = in.ServerGroupID
		}
	}
	if in.PackageName != nil {
		p.PackageName = *in.PackageName
	}
	if in.AutoSetup != nil {
		p.AutoSetup = domain.AutoSetup(*in.AutoSetup)
	}
	if in.Configurable != nil {
		p.Configurable = *in.Configurable
	}
	if in.ShellAccess != nil {
		p.ShellAccess = *in.ShellAccess
	}
	if in.CGIAccess != nil {
		p.CGIAccess = *in.CGIAccess
	}
	if in.FeatureList != nil {
		p.FeatureList = *in.FeatureList
	}
	if in.TemplatePackage != nil {
		p.TemplatePackage = *in.TemplatePackage
	}
	if in.StockEnabled != nil {
		p.StockEnabled = *in.StockEnabled
	}
	if in.StockQty != nil {
		p.StockQty = *in.StockQty
	}
	if in.Hidden != nil {
		p.Hidden = *in.Hidden
	}
	if in.Sort != nil {
		p.Sort = *in.Sort
	}
	if in.WelcomeEmailTemplate != nil {
		p.WelcomeEmailTemplate = *in.WelcomeEmailTemplate
	}
	if p.Module != domain.ModuleNone && !p.Configurable && p.PackageName == "" {
		return nil, apperr.Validation("invalid product",
			apperr.FieldError{Field: "package_name", Message: "is required when a provisioning module is set"})
	}
	if p.Configurable && p.Module == domain.ModuleNone {
		return nil, apperr.Validation("invalid product",
			apperr.FieldError{Field: "configurable", Message: "requires a provisioning module (cpanel/directadmin)"})
	}
	if p.FeatureList != "" && p.Module != domain.ModuleCpanel {
		return nil, apperr.Validation("invalid product",
			apperr.FieldError{Field: "feature_list", Message: "only applies to cpanel products"})
	}
	if p.TemplatePackage != "" && p.Module != domain.ModuleDirectAdmin {
		return nil, apperr.Validation("invalid product",
			apperr.FieldError{Field: "template_package", Message: "only applies to directadmin products"})
	}
	if err := s.d.Products.Update(ctx, p); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.product.update", "product", p.ID, before, p)
	s.invalidateCatalogCache(ctx)
	return p, nil
}

// DeleteProduct soft-deletes a product.
func (s *Service) DeleteProduct(ctx context.Context, actorUserID, id int64) error {
	p, err := s.GetProduct(ctx, id)
	if err != nil {
		return err
	}
	if err := s.d.Products.SoftDelete(ctx, id); err != nil {
		return wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.product.delete", "product", id, p, nil)
	s.invalidateCatalogCache(ctx)
	return nil
}

// maxCopySlugAttempts bounds uniqueCopySlug's search for a free slug.
const maxCopySlugAttempts = 50

// DuplicateProduct clones a product - including its pricing rows and every
// spec (with that spec's own per-cycle pricing) - into a new hidden product
// named "<Name> (Copy)". Hidden so an admin must explicitly review/rename
// and re-publish the clone rather than it becoming sellable as-is.
// Configurable options are catalog-wide reference data (see OptionTree), not
// per-product, so there is nothing product-specific to clone there.
func (s *Service) DuplicateProduct(ctx context.Context, actorUserID, id int64) (*domain.Product, error) {
	src, err := s.GetProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	slug, err := s.uniqueCopySlug(ctx, src.Slug)
	if err != nil {
		return nil, err
	}
	pricing, err := s.d.Products.ListPricing(ctx, src.ID)
	if err != nil {
		return nil, wrap(err)
	}
	specs, err := s.d.Specs.ListSpecs(ctx, src.ID)
	if err != nil {
		return nil, wrap(err)
	}
	specIDs := make([]int64, len(specs))
	for i, sp := range specs {
		specIDs[i] = sp.ID
	}
	pricingBySpec, err := s.d.Specs.ListSpecPricingBySpecIDs(ctx, specIDs)
	if err != nil {
		return nil, wrap(err)
	}
	specPricing := make([][]domain.ProductSpecPricing, len(specs))
	for i, sp := range specs {
		specPricing[i] = pricingBySpec[sp.ID]
	}

	dst := &domain.Product{
		GroupID:              src.GroupID,
		Name:                 src.Name + " (Copy)",
		Slug:                 slug,
		Description:          src.Description,
		Type:                 src.Type,
		Module:               src.Module,
		ServerGroupID:        src.ServerGroupID,
		PackageName:          src.PackageName,
		AutoSetup:            src.AutoSetup,
		Configurable:         src.Configurable,
		ShellAccess:          src.ShellAccess,
		CGIAccess:            src.CGIAccess,
		FeatureList:          src.FeatureList,
		TemplatePackage:      src.TemplatePackage,
		StockEnabled:         src.StockEnabled,
		StockQty:             src.StockQty,
		Hidden:               true,
		Sort:                 src.Sort,
		WelcomeEmailTemplate: src.WelcomeEmailTemplate,
	}
	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.d.Products.Create(txCtx, dst); err != nil {
			return err
		}
		for _, pp := range pricing {
			if err := s.d.Products.UpsertPricing(txCtx, &domain.ProductPricing{
				ProductID: dst.ID, Cycle: pp.Cycle, Price: pp.Price,
				SetupFee: pp.SetupFee, Currency: pp.Currency,
			}); err != nil {
				return err
			}
		}
		for i, sp := range specs {
			newSpec := &domain.ProductSpec{
				ProductID:      dst.ID,
				Key:            sp.Key,
				Label:          sp.Label,
				ProvisionKey:   sp.ProvisionKey,
				Unit:           sp.Unit,
				IncludedQty:    sp.IncludedQty,
				MinQty:         sp.MinQty,
				MaxQty:         sp.MaxQty,
				StepQty:        sp.StepQty,
				DefaultQty:     sp.DefaultQty,
				AllowUnlimited: sp.AllowUnlimited,
				Sort:           sp.Sort,
			}
			if err := s.d.Specs.CreateSpec(txCtx, newSpec); err != nil {
				return err
			}
			for _, pr := range specPricing[i] {
				if err := s.d.Specs.UpsertSpecPricing(txCtx, &domain.ProductSpecPricing{
					SpecID: newSpec.ID, Cycle: pr.Cycle, UnitPrice: pr.UnitPrice,
					UnlimitedPrice: pr.UnlimitedPrice, Currency: pr.Currency,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.product.duplicate", "product", dst.ID,
		map[string]any{"source_product_id": src.ID}, dst)
	s.invalidateCatalogCache(ctx)
	return dst, nil
}

// uniqueCopySlug finds a free slug for a duplicate: "<base>-copy",
// "<base>-copy-2", "<base>-copy-3", etc. Checked ahead of the insert (rather
// than retrying the insert itself on a unique-violation) because a retry
// loop inside DuplicateProduct's single transaction would hit an aborted
// transaction after the first conflict.
//
// GetBySlug reports "no such product" as a NotFound *error* (repo.go), not a
// nil product with a nil error - a candidate is free when either the lookup
// returns a nil product OR errors with NotFound; any other error is a real
// failure.
func (s *Service) uniqueCopySlug(ctx context.Context, base string) (string, error) {
	root := base + "-copy"
	for i := 0; i < maxCopySlugAttempts; i++ {
		candidate := root
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", root, i+1)
		}
		existing, err := s.d.Products.GetBySlug(ctx, candidate)
		if err != nil && !isNotFound(err) {
			return "", wrap(err)
		}
		if existing == nil {
			return candidate, nil
		}
	}
	return "", apperr.Conflict("could not find a free slug for the duplicate; rename the source product first")
}

// Pricing (admin, IDR only)

// ListPricing lists the pricing rows of a product.
func (s *Service) ListPricing(ctx context.Context, productID int64) ([]domain.ProductPricing, error) {
	if _, err := s.GetProduct(ctx, productID); err != nil {
		return nil, err
	}
	rows, err := s.d.Products.ListPricing(ctx, productID)
	return rows, wrap(err)
}

// UpsertPricing creates or updates one billing-cycle price. Only IDR is
// accepted (CONTRACTS: IDR-only MVP).
func (s *Service) UpsertPricing(ctx context.Context, actorUserID, productID int64, in PricingInput) (*domain.ProductPricing, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	currency := in.Currency
	if currency == "" {
		currency = "IDR"
	}
	if currency != "IDR" {
		return nil, apperr.Validation("only IDR pricing is supported",
			apperr.FieldError{Field: "currency", Message: "must be IDR"})
	}
	if _, err := s.GetProduct(ctx, productID); err != nil {
		return nil, err
	}
	pp := &domain.ProductPricing{
		ProductID: productID,
		Cycle:     domain.BillingCycle(in.Cycle),
		Price:     in.Price,
		SetupFee:  in.SetupFee,
		Currency:  currency,
	}
	if err := s.d.Products.UpsertPricing(ctx, pp); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.pricing.upsert", "product", productID, nil, pp)
	s.invalidateCatalogCache(ctx)
	return pp, nil
}

// DeletePricing removes one billing-cycle price from a product.
func (s *Service) DeletePricing(ctx context.Context, actorUserID, productID int64, cycle string) error {
	c := domain.BillingCycle(cycle)
	if !c.Valid() {
		return apperr.Validation("invalid billing cycle",
			apperr.FieldError{Field: "cycle", Message: "unknown billing cycle"})
	}
	if err := s.d.Products.DeletePricing(ctx, productID, c); err != nil {
		return wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.pricing.delete", "product", productID, map[string]string{"cycle": cycle}, nil)
	s.invalidateCatalogCache(ctx)
	return nil
}

// Configurable options (admin)

// OptionTree returns every option group with its options and values.
func (s *Service) OptionTree(ctx context.Context) ([]OptionGroupTree, error) {
	groups, err := s.d.Products.ListOptionGroups(ctx)
	if err != nil {
		return nil, wrap(err)
	}
	groupIDs := make([]int64, len(groups))
	for i, g := range groups {
		groupIDs[i] = g.ID
	}
	optionsByGroup, err := s.d.Products.ListOptionsByGroupIDs(ctx, groupIDs)
	if err != nil {
		return nil, wrap(err)
	}
	var optionIDs []int64
	for _, options := range optionsByGroup {
		for _, o := range options {
			optionIDs = append(optionIDs, o.ID)
		}
	}
	valuesByOption, err := s.d.Products.ListOptionValuesByOptionIDs(ctx, optionIDs)
	if err != nil {
		return nil, wrap(err)
	}

	out := make([]OptionGroupTree, 0, len(groups))
	for _, g := range groups {
		options := optionsByGroup[g.ID]
		entries := make([]OptionTreeEntry, 0, len(options))
		for _, o := range options {
			entries = append(entries, OptionTreeEntry{Option: o, Values: valuesByOption[o.ID]})
		}
		out = append(out, OptionGroupTree{Group: g, Options: entries})
	}
	return out, nil
}

// CreateOptionGroup creates a configurable option group.
func (s *Service) CreateOptionGroup(ctx context.Context, actorUserID int64, in OptionGroupInput) (*domain.ConfigurableOptionGroup, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	g := &domain.ConfigurableOptionGroup{Name: in.Name, Description: in.Description}
	if err := s.d.Options.CreateOptionGroup(ctx, g); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.option_group.create", "configurable_option_group", g.ID, nil, g)
	s.invalidateCatalogCache(ctx)
	return g, nil
}

// UpdateOptionGroup updates a configurable option group.
func (s *Service) UpdateOptionGroup(ctx context.Context, actorUserID, id int64, in OptionGroupInput) (*domain.ConfigurableOptionGroup, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	g, err := s.d.Options.GetOptionGroupByID(ctx, id)
	if err != nil {
		return nil, wrap(err)
	}
	if g == nil {
		return nil, apperr.NotFound("option group")
	}
	before := *g
	g.Name, g.Description = in.Name, in.Description
	if err := s.d.Options.UpdateOptionGroup(ctx, g); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.option_group.update", "configurable_option_group", g.ID, before, g)
	s.invalidateCatalogCache(ctx)
	return g, nil
}

// DeleteOptionGroup deletes a configurable option group (cascades to
// options/values via FK).
func (s *Service) DeleteOptionGroup(ctx context.Context, actorUserID, id int64) error {
	if err := s.d.Options.DeleteOptionGroup(ctx, id); err != nil {
		return wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.option_group.delete", "configurable_option_group", id, nil, nil)
	s.invalidateCatalogCache(ctx)
	return nil
}

// CreateOption creates an option inside a group.
func (s *Service) CreateOption(ctx context.Context, actorUserID, groupID int64, in OptionInput) (*domain.ConfigurableOption, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	g, err := s.d.Options.GetOptionGroupByID(ctx, groupID)
	if err != nil {
		return nil, wrap(err)
	}
	if g == nil {
		return nil, apperr.NotFound("option group")
	}
	o := &domain.ConfigurableOption{GroupID: groupID, Name: in.Name, Sort: in.Sort}
	if err := s.d.Options.CreateOption(ctx, o); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.option.create", "configurable_option", o.ID, nil, o)
	s.invalidateCatalogCache(ctx)
	return o, nil
}

// UpdateOption updates an option.
func (s *Service) UpdateOption(ctx context.Context, actorUserID, id int64, in OptionInput) (*domain.ConfigurableOption, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	o, err := s.d.Options.GetOptionByID(ctx, id)
	if err != nil {
		return nil, wrap(err)
	}
	if o == nil {
		return nil, apperr.NotFound("option")
	}
	before := *o
	o.Name, o.Sort = in.Name, in.Sort
	if err := s.d.Options.UpdateOption(ctx, o); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.option.update", "configurable_option", o.ID, before, o)
	s.invalidateCatalogCache(ctx)
	return o, nil
}

// DeleteOption deletes an option (cascades to values).
func (s *Service) DeleteOption(ctx context.Context, actorUserID, id int64) error {
	if err := s.d.Options.DeleteOption(ctx, id); err != nil {
		return wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.option.delete", "configurable_option", id, nil, nil)
	s.invalidateCatalogCache(ctx)
	return nil
}

// CreateOptionValue creates a value under an option.
func (s *Service) CreateOptionValue(ctx context.Context, actorUserID, optionID int64, in OptionValueInput) (*domain.ConfigurableOptionValue, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if err := checkPriceDeltas(in.PriceDeltas); err != nil {
		return nil, err
	}
	o, err := s.d.Options.GetOptionByID(ctx, optionID)
	if err != nil {
		return nil, wrap(err)
	}
	if o == nil {
		return nil, apperr.NotFound("option")
	}
	deltas, err := json.Marshal(in.PriceDeltas)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	v := &domain.ConfigurableOptionValue{OptionID: optionID, Name: in.Name, PriceDeltas: deltas, Sort: in.Sort}
	if err := s.d.Options.CreateOptionValue(ctx, v); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.option_value.create", "configurable_option_value", v.ID, nil, v)
	s.invalidateCatalogCache(ctx)
	return v, nil
}

// UpdateOptionValue updates a value.
func (s *Service) UpdateOptionValue(ctx context.Context, actorUserID, id int64, in OptionValueInput) (*domain.ConfigurableOptionValue, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if err := checkPriceDeltas(in.PriceDeltas); err != nil {
		return nil, err
	}
	v, err := s.d.Options.GetOptionValueByID(ctx, id)
	if err != nil {
		return nil, wrap(err)
	}
	if v == nil {
		return nil, apperr.NotFound("option value")
	}
	before := *v
	deltas, err := json.Marshal(in.PriceDeltas)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	v.Name, v.PriceDeltas, v.Sort = in.Name, deltas, in.Sort
	if err := s.d.Options.UpdateOptionValue(ctx, v); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.option_value.update", "configurable_option_value", v.ID, before, v)
	s.invalidateCatalogCache(ctx)
	return v, nil
}

// DeleteOptionValue deletes a value.
func (s *Service) DeleteOptionValue(ctx context.Context, actorUserID, id int64) error {
	if err := s.d.Options.DeleteOptionValue(ctx, id); err != nil {
		return wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.option_value.delete", "configurable_option_value", id, nil, nil)
	s.invalidateCatalogCache(ctx)
	return nil
}

// Coupons (admin)

// ListCoupons lists coupons (admin; search on code, status active|inactive).
func (s *Service) ListCoupons(ctx context.Context, p ports.ListParams) ([]domain.Coupon, int64, error) {
	rows, total, err := s.d.Coupons.List(ctx, p)
	return rows, total, wrap(err)
}

// GetCoupon returns one coupon.
func (s *Service) GetCoupon(ctx context.Context, id int64) (*domain.Coupon, error) {
	c, err := s.d.Coupons.GetByID(ctx, id)
	if err != nil {
		return nil, wrap(err)
	}
	if c == nil {
		return nil, apperr.NotFound("coupon")
	}
	return c, nil
}

// CreateCoupon creates a coupon (code stored uppercase; percentage value
// must be 1..100).
func (s *Service) CreateCoupon(ctx context.Context, actorUserID int64, in CouponInput) (*domain.Coupon, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if in.Type == string(domain.CouponPercentage) && in.Value > 100 {
		return nil, apperr.Validation("invalid coupon",
			apperr.FieldError{Field: "value", Message: "percentage must be between 1 and 100"})
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(s.d.Clock.Now()) {
		return nil, apperr.Validation("invalid coupon",
			apperr.FieldError{Field: "expires_at", Message: "must be in the future"})
	}
	applies, err := json.Marshal(CouponAppliesTo{ProductIDs: in.ProductIDs})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	c := &domain.Coupon{
		Code:      strings.ToUpper(strings.TrimSpace(in.Code)),
		Type:      domain.CouponType(in.Type),
		Value:     in.Value,
		AppliesTo: applies,
		MaxUses:   in.MaxUses,
		Recurring: in.Recurring,
		ExpiresAt: in.ExpiresAt,
		Active:    active,
	}
	if err := s.d.Coupons.Create(ctx, c); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.coupon.create", "coupon", c.ID, nil, c)
	s.invalidateCatalogCache(ctx)
	return c, nil
}

// UpdateCoupon patches a coupon (the code itself is immutable).
func (s *Service) UpdateCoupon(ctx context.Context, actorUserID, id int64, in CouponUpdateInput) (*domain.Coupon, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	c, err := s.GetCoupon(ctx, id)
	if err != nil {
		return nil, err
	}
	before := *c
	if in.Type != nil {
		c.Type = domain.CouponType(*in.Type)
	}
	if in.Value != nil {
		c.Value = *in.Value
	}
	if c.Type == domain.CouponPercentage && c.Value > 100 {
		return nil, apperr.Validation("invalid coupon",
			apperr.FieldError{Field: "value", Message: "percentage must be between 1 and 100"})
	}
	if in.ProductIDs != nil {
		applies, err := json.Marshal(CouponAppliesTo{ProductIDs: in.ProductIDs})
		if err != nil {
			return nil, apperr.Internal(err)
		}
		c.AppliesTo = applies
	}
	if in.MaxUses != nil {
		c.MaxUses = *in.MaxUses
	}
	if in.Recurring != nil {
		c.Recurring = *in.Recurring
	}
	if in.ClearExpiry {
		c.ExpiresAt = nil
	} else if in.ExpiresAt != nil {
		c.ExpiresAt = in.ExpiresAt
	}
	if in.Active != nil {
		c.Active = *in.Active
	}
	if err := s.d.Coupons.Update(ctx, c); err != nil {
		return nil, wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.coupon.update", "coupon", c.ID, before, c)
	s.invalidateCatalogCache(ctx)
	return c, nil
}

// DeleteCoupon hard-deletes a coupon. Coupons referenced by orders/services
// cannot be deleted (repo maps the FK violation to CONFLICT).
func (s *Service) DeleteCoupon(ctx context.Context, actorUserID, id int64) error {
	c, err := s.GetCoupon(ctx, id)
	if err != nil {
		return err
	}
	if err := s.d.Coupons.Delete(ctx, id); err != nil {
		return wrap(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "catalog.coupon.delete", "coupon", id, c, nil)
	s.invalidateCatalogCache(ctx)
	return nil
}
