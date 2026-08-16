package catalog

import (
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
)

// Product groups

// GroupInput creates a product group.
type GroupInput struct {
	Name   string `json:"name" validate:"required,min=2,max=100"`
	Slug   string `json:"slug" validate:"omitempty,min=2,max=100"`
	Sort   int    `json:"sort"`
	Hidden bool   `json:"hidden"`
}

// GroupUpdateInput patches a product group; nil fields are left unchanged.
type GroupUpdateInput struct {
	Name   *string `json:"name" validate:"omitempty,min=2,max=100"`
	Slug   *string `json:"slug" validate:"omitempty,min=2,max=100"`
	Sort   *int    `json:"sort"`
	Hidden *bool   `json:"hidden"`
}

// Products

// ProductInput creates a product.
type ProductInput struct {
	GroupID              int64  `json:"group_id" validate:"required,min=1"`
	Name                 string `json:"name" validate:"required,min=2,max=150"`
	Slug                 string `json:"slug" validate:"omitempty,min=2,max=150"`
	Description          string `json:"description"`
	Type                 string `json:"type" validate:"required,oneof=shared_hosting reseller_hosting domain other"`
	Module               string `json:"module" validate:"omitempty,oneof=cpanel directadmin none"`
	ServerGroupID        *int64 `json:"server_group_id" validate:"omitempty,min=1"`
	PackageName          string `json:"package_name" validate:"max=100"`
	AutoSetup            string `json:"auto_setup" validate:"omitempty,oneof=on_payment on_order manual"`
	Configurable         bool   `json:"configurable"`
	ShellAccess          bool   `json:"shell_access"`
	CGIAccess            bool   `json:"cgi_access"`
	FeatureList          string `json:"feature_list" validate:"max=100"`
	TemplatePackage      string `json:"template_package" validate:"max=100"`
	StockEnabled         bool   `json:"stock_enabled"`
	StockQty             int    `json:"stock_qty" validate:"min=0"`
	Hidden               bool   `json:"hidden"`
	Sort                 int    `json:"sort"`
	WelcomeEmailTemplate string `json:"welcome_email_template" validate:"max=100"`
}

// ProductUpdateInput patches a product; nil fields are left unchanged.
type ProductUpdateInput struct {
	GroupID              *int64  `json:"group_id" validate:"omitempty,min=1"`
	Name                 *string `json:"name" validate:"omitempty,min=2,max=150"`
	Slug                 *string `json:"slug" validate:"omitempty,min=2,max=150"`
	Description          *string `json:"description"`
	Type                 *string `json:"type" validate:"omitempty,oneof=shared_hosting reseller_hosting domain other"`
	Module               *string `json:"module" validate:"omitempty,oneof=cpanel directadmin none"`
	ServerGroupID        *int64  `json:"server_group_id" validate:"omitempty,min=0"` // 0 clears the binding
	PackageName          *string `json:"package_name" validate:"omitempty,max=100"`
	AutoSetup            *string `json:"auto_setup" validate:"omitempty,oneof=on_payment on_order manual"`
	Configurable         *bool   `json:"configurable"`
	ShellAccess          *bool   `json:"shell_access"`
	CGIAccess            *bool   `json:"cgi_access"`
	FeatureList          *string `json:"feature_list" validate:"omitempty,max=100"`
	TemplatePackage      *string `json:"template_package" validate:"omitempty,max=100"`
	StockEnabled         *bool   `json:"stock_enabled"`
	StockQty             *int    `json:"stock_qty" validate:"omitempty,min=0"`
	Hidden               *bool   `json:"hidden"`
	Sort                 *int    `json:"sort"`
	WelcomeEmailTemplate *string `json:"welcome_email_template" validate:"omitempty,max=100"`
}

// Pricing

// PricingInput upserts one billing-cycle price for a product. IDR only:
// any other currency is rejected with VALIDATION.
type PricingInput struct {
	Cycle    string `json:"cycle" validate:"required,oneof=one_time monthly quarterly semiannually annually biennially"`
	Price    int64  `json:"price" validate:"min=0"`
	SetupFee int64  `json:"setup_fee" validate:"min=0"`
	Currency string `json:"currency"` // "" or "IDR"
}

// Product specs (dynamic/custom-spec products)

// SpecInput creates or fully updates a product spec knob.
type SpecInput struct {
	Key            string `json:"key" validate:"required,min=1,max=50"`
	Label          string `json:"label" validate:"max=100"`
	ProvisionKey   string `json:"provision_key" validate:"required,oneof=disk bandwidth addon_domains subdomains parked_domains email_accounts databases ftp_accounts"`
	Unit           string `json:"unit" validate:"required,oneof=gb mb count"`
	IncludedQty    int64  `json:"included_qty" validate:"min=0"`
	MinQty         int64  `json:"min_qty" validate:"min=0"`
	MaxQty         int64  `json:"max_qty" validate:"min=0"` // 0 = unbounded
	StepQty        int64  `json:"step_qty" validate:"min=1"`
	DefaultQty     int64  `json:"default_qty" validate:"min=0"`
	AllowUnlimited bool   `json:"allow_unlimited"`
	Sort           int    `json:"sort"`
}

// SpecPricingInput upserts the per-cycle IDR unit pricing of a spec.
type SpecPricingInput struct {
	Cycle          string `json:"cycle" validate:"required,oneof=one_time monthly quarterly semiannually annually biennially"`
	UnitPrice      int64  `json:"unit_price" validate:"min=0"`
	UnlimitedPrice int64  `json:"unlimited_price" validate:"min=0"`
	Currency       string `json:"currency"` // "" or "IDR"
}

// SpecWithPricing is one spec plus its per-cycle pricing (admin listing).
type SpecWithPricing struct {
	Spec    domain.ProductSpec          `json:"spec"`
	Pricing []domain.ProductSpecPricing `json:"pricing"`
}

// Configurable options

// OptionGroupInput creates/updates a configurable option group.
type OptionGroupInput struct {
	Name        string `json:"name" validate:"required,min=2,max=100"`
	Description string `json:"description"`
}

// OptionInput creates/updates a configurable option.
type OptionInput struct {
	Name string `json:"name" validate:"required,min=1,max=100"`
	Sort int    `json:"sort"`
}

// OptionValueInput creates/updates a configurable option value.
// PriceDeltas maps billing cycle -> IDR delta added to the base price.
type OptionValueInput struct {
	Name        string           `json:"name" validate:"required,min=1,max=100"`
	PriceDeltas map[string]int64 `json:"price_deltas"`
	Sort        int              `json:"sort"`
}

// OptionGroupTree is one option group with its options and values (admin
// listing + public product detail).
type OptionGroupTree struct {
	Group   domain.ConfigurableOptionGroup `json:"group"`
	Options []OptionTreeEntry              `json:"options"`
}

// OptionTreeEntry is one option with its values.
type OptionTreeEntry struct {
	Option domain.ConfigurableOption        `json:"option"`
	Values []domain.ConfigurableOptionValue `json:"values"`
}

// Coupons

// CouponAppliesTo is the coupons.applies_to JSONB payload. An empty
// ProductIDs list means the coupon applies to every product.
type CouponAppliesTo struct {
	ProductIDs []int64 `json:"product_ids,omitempty"`
}

// CouponInput creates a coupon. Codes are stored uppercase.
type CouponInput struct {
	Code       string     `json:"code" validate:"required,min=2,max=50"`
	Type       string     `json:"type" validate:"required,oneof=percentage fixed"`
	Value      int64      `json:"value" validate:"required,min=1"`
	ProductIDs []int64    `json:"product_ids"`
	MaxUses    int        `json:"max_uses" validate:"min=0"` // 0 = unlimited
	Recurring  bool       `json:"recurring"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Active     *bool      `json:"active"` // default true
}

// CouponUpdateInput patches a coupon; nil fields are left unchanged.
type CouponUpdateInput struct {
	Type        *string    `json:"type" validate:"omitempty,oneof=percentage fixed"`
	Value       *int64     `json:"value" validate:"omitempty,min=1"`
	ProductIDs  []int64    `json:"product_ids"` // nil = unchanged, [] = clear
	MaxUses     *int       `json:"max_uses" validate:"omitempty,min=0"`
	Recurring   *bool      `json:"recurring"`
	ExpiresAt   *time.Time `json:"expires_at"`
	ClearExpiry bool       `json:"clear_expiry"`
	Active      *bool      `json:"active"`
}

// ValidateCouponRequest is the body of POST /coupons/validate.
type ValidateCouponRequest struct {
	Code       string  `json:"code" validate:"required,min=2,max=50"`
	ProductIDs []int64 `json:"product_ids"`
	Subtotal   int64   `json:"subtotal" validate:"min=0"`
}

// ValidateCouponResult is the response of POST /coupons/validate.
type ValidateCouponResult struct {
	Code      string            `json:"code"`
	Type      domain.CouponType `json:"type"`
	Value     int64             `json:"value"`
	Recurring bool              `json:"recurring"`
	Discount  int64             `json:"discount"`
}

// Public catalog payloads

// PublicPrice is one billing-cycle price on the public catalog.
type PublicPrice struct {
	Cycle    domain.BillingCycle `json:"cycle"`
	Price    int64               `json:"price"`
	SetupFee int64               `json:"setup_fee"`
	Currency string              `json:"currency"`
}

// PublicProduct is a visible product on the public catalog.
type PublicProduct struct {
	ID           int64              `json:"id"`
	GroupID      int64              `json:"group_id"`
	Name         string             `json:"name"`
	Slug         string             `json:"slug"`
	Description  string             `json:"description"`
	Type         domain.ProductType `json:"type"`
	Configurable bool               `json:"configurable"`
	StockEnabled bool               `json:"stock_enabled"`
	InStock      bool               `json:"in_stock"`
	Pricing      []PublicPrice      `json:"pricing"`
	// Specs carries the dynamic spec knobs (with per-cycle pricing) of a
	// configurable product so pickers that work off the grouped catalog -
	// e.g. the client-area service upgrade modal - can render the spec
	// configurator without a per-product detail fetch. Nil for flat products.
	Specs []PublicSpec `json:"specs,omitempty"`
}

// PublicSpecPrice is one billing-cycle unit price of a public spec.
type PublicSpecPrice struct {
	Cycle          domain.BillingCycle `json:"cycle"`
	UnitPrice      int64               `json:"unit_price"`
	UnlimitedPrice int64               `json:"unlimited_price"`
	Currency       string              `json:"currency"`
}

// PublicSpec is a configurable spec knob exposed on the public product detail
// so the client configurator can render sliders and compute a live estimate.
type PublicSpec struct {
	Key            string              `json:"key"`
	Label          string              `json:"label"`
	ProvisionKey   domain.ProvisionKey `json:"provision_key"`
	Unit           domain.SpecUnit     `json:"unit"`
	IncludedQty    int64               `json:"included_qty"`
	MinQty         int64               `json:"min_qty"`
	MaxQty         int64               `json:"max_qty"`
	StepQty        int64               `json:"step_qty"`
	DefaultQty     int64               `json:"default_qty"`
	AllowUnlimited bool                `json:"allow_unlimited"`
	Pricing        []PublicSpecPrice   `json:"pricing"`
}

// PublicGroup is a visible product group with its visible products.
type PublicGroup struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Slug     string          `json:"slug"`
	Sort     int             `json:"sort"`
	Products []PublicProduct `json:"products"`
}

// PublicProductDetail is the payload of GET /products/:slug: the product
// plus the configurable option catalog and dynamic spec knobs for the order
// configure page.
type PublicProductDetail struct {
	PublicProduct
	OptionGroups []OptionGroupTree `json:"option_groups"`
	Specs        []PublicSpec      `json:"specs"`
}
