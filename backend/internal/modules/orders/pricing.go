package orders

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// itemPlan is one fully priced, validated order line before persistence.
type itemPlan struct {
	itemType   domain.OrderItemType
	product    *domain.Product
	cycle      domain.BillingCycle
	unit       int64 // recurring price incl. option deltas (domains: price * years)
	setup      int64
	desc       string
	domainName string
	opts       itemOptions
	years      int
}

// allAutoSetupOnOrder reports whether every item in the cart is a product
// configured for AutoSetup == on_order, so the whole order can activate
// right after creation instead of waiting for payment. Domain items never
// qualify - domain registration must never fire before payment, since it
// charges the registrar real money a chargeback can't claw back. A mixed
// cart (any on_payment/manual item, or any domain item) stays gated on
// payment as a whole, matching how ActivateOrder itself activates an order
// atomically rather than per item.
func allAutoSetupOnOrder(plans []itemPlan) bool {
	if len(plans) == 0 {
		return false
	}
	for _, p := range plans {
		if p.itemType != domain.ItemProduct || p.product.AutoSetup != domain.SetupOnOrder {
			return false
		}
	}
	return true
}

// requiresRegistrantContact reports whether any item needs a registrant
// contact built from the client profile at registration/transfer time
// (domains.Service.registrantContact) - checked at checkout so an incomplete
// profile is rejected before payment instead of failing opaquely, post-
// payment, in the async registrar job.
func requiresRegistrantContact(plans []itemPlan) bool {
	for _, p := range plans {
		if p.itemType == domain.ItemDomainRegister || p.itemType == domain.ItemDomainTransfer {
			return true
		}
	}
	return false
}

// buildPlans validates and prices every requested item. Per-item problems are
// collected into one VALIDATION error with items[i].field details.
func (s *Service) buildPlans(ctx context.Context, items []OrderItemRequest) ([]itemPlan, error) {
	plans := make([]itemPlan, 0, len(items))
	var details []apperr.FieldError
	fail := func(i int, field, msg string) {
		details = append(details, apperr.FieldError{
			Field:   fmt.Sprintf("items[%d].%s", i, field),
			Message: msg,
		})
	}

	for i, it := range items {
		switch domain.OrderItemType(it.ItemType) {
		case domain.ItemProduct:
			plan, errs := s.planProduct(ctx, it)
			if len(errs) > 0 {
				for _, fe := range errs {
					fail(i, fe.Field, fe.Message)
				}
				continue
			}
			plans = append(plans, plan)
		case domain.ItemDomainRegister, domain.ItemDomainTransfer:
			plan, errs, err := s.planDomain(ctx, it)
			if err != nil {
				return nil, err
			}
			if len(errs) > 0 {
				for _, fe := range errs {
					fail(i, fe.Field, fe.Message)
				}
				continue
			}
			plans = append(plans, plan)
		default:
			fail(i, "item_type", "must be one of: product, domain_register, domain_transfer")
		}
	}

	if len(details) > 0 {
		return nil, apperr.Validation("invalid order items", details...)
	}
	return plans, nil
}

// planProduct prices a hosting/other product line.
func (s *Service) planProduct(ctx context.Context, it OrderItemRequest) (itemPlan, []apperr.FieldError) {
	var errs []apperr.FieldError
	fieldErr := func(field, msg string) {
		errs = append(errs, apperr.FieldError{Field: field, Message: msg})
	}

	if it.ProductID == 0 {
		fieldErr("product_id", "is required for product items")
		return itemPlan{}, errs
	}
	product, err := s.d.Products.GetByID(ctx, it.ProductID)
	if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
		fieldErr("product_id", "could not load product")
		return itemPlan{}, errs
	}
	if product == nil || product.DeletedAt != nil {
		fieldErr("product_id", "product not found")
		return itemPlan{}, errs
	}
	if product.Hidden {
		fieldErr("product_id", "product is not available")
		return itemPlan{}, errs
	}
	if product.Type == domain.ProductDomain {
		fieldErr("item_type", "domain products must be ordered as domain_register")
		return itemPlan{}, errs
	}
	if product.StockEnabled && product.StockQty < 1 {
		fieldErr("product_id", "product is out of stock")
	}

	cycle := domain.BillingCycle(it.Cycle)
	if it.Cycle == "" || !cycle.Valid() {
		fieldErr("cycle", "a valid billing cycle is required")
		return itemPlan{}, errs
	}
	pricing, err := s.d.Products.GetPricing(ctx, product.ID, cycle)
	if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
		fieldErr("cycle", "could not load pricing")
		return itemPlan{}, errs
	}
	if pricing == nil {
		fieldErr("cycle", "product is not priced for this billing cycle")
		return itemPlan{}, errs
	}

	unit := pricing.Price
	var selections []OptionSelection
	for _, sel := range it.Options {
		values, err := s.d.Products.ListOptionValues(ctx, sel.OptionID)
		if err != nil {
			fieldErr("options", "could not load configurable options")
			continue
		}
		var matched *domain.ConfigurableOptionValue
		for v := range values {
			if values[v].ID == sel.ValueID {
				matched = &values[v]
				break
			}
		}
		if matched == nil {
			fieldErr("options", fmt.Sprintf("invalid value %d for option %d", sel.ValueID, sel.OptionID))
			continue
		}
		delta := optionDelta(matched.PriceDeltas, cycle)
		unit += delta
		selections = append(selections, OptionSelection{
			OptionID: sel.OptionID,
			ValueID:  sel.ValueID,
			Value:    matched.Name,
			Delta:    delta,
		})
	}
	// Dynamic/custom specs: price each chosen knob (qty × unit price above the
	// included quantity, or a flat add-on when unlimited). Server-authoritative
	// - the cart's estimate is never trusted.
	var specSels []SpecSelection
	if product.Configurable {
		specs, err := s.d.Products.ListSpecs(ctx, product.ID)
		if err != nil {
			fieldErr("specs", "could not load product specs")
		} else {
			chosen := make(map[string]SpecSelectionRequest, len(it.Specs))
			for _, sc := range it.Specs {
				chosen[sc.Key] = sc
			}
			valid := make(map[string]bool, len(specs))
			for _, sp := range specs {
				valid[sp.Key] = true
				qty, unlimited := sp.DefaultQty, false
				if sc, ok := chosen[sp.Key]; ok {
					qty, unlimited = sc.Qty, sc.Unlimited
				}
				sel, ferrs := s.priceSpec(ctx, sp, qty, unlimited, cycle)
				errs = append(errs, ferrs...)
				if len(ferrs) == 0 {
					unit += sel.Amount
					specSels = append(specSels, sel)
				}
			}
			for k := range chosen {
				if !valid[k] {
					fieldErr("specs", fmt.Sprintf("unknown spec %q", k))
				}
			}
		}
	} else if len(it.Specs) > 0 {
		fieldErr("specs", "product is not configurable")
	}

	if len(errs) > 0 {
		return itemPlan{}, errs
	}

	desc := fmt.Sprintf("%s (%s)", product.Name, cycle)
	if it.Domain != "" {
		desc += " - " + strings.ToLower(strings.TrimSpace(it.Domain))
	}
	if summary := specSummary(specSels); summary != "" {
		desc += " - " + summary
	}
	return itemPlan{
		itemType:   domain.ItemProduct,
		product:    product,
		cycle:      cycle,
		unit:       unit,
		setup:      pricing.SetupFee,
		desc:       desc,
		domainName: strings.ToLower(strings.TrimSpace(it.Domain)),
		opts:       itemOptions{Selections: selections, Specs: specSels},
	}, nil
}

// priceSpec validates a chosen spec quantity and computes the IDR amount it adds
// to the recurring price for the given cycle.
func (s *Service) priceSpec(ctx context.Context, sp domain.ProductSpec, qty int64, unlimited bool, cycle domain.BillingCycle) (SpecSelection, []apperr.FieldError) {
	var errs []apperr.FieldError
	fe := func(msg string) { errs = append(errs, apperr.FieldError{Field: "specs." + sp.Key, Message: msg}) }

	if unlimited {
		if !sp.AllowUnlimited {
			fe("unlimited is not allowed for this spec")
		}
	} else {
		if qty < sp.MinQty {
			fe(fmt.Sprintf("must be at least %d", sp.MinQty))
		}
		if sp.MaxQty != 0 && qty > sp.MaxQty {
			fe(fmt.Sprintf("must be at most %d", sp.MaxQty))
		}
		if sp.StepQty > 1 && (qty-sp.MinQty)%sp.StepQty != 0 {
			fe(fmt.Sprintf("must be in increments of %d", sp.StepQty))
		}
	}
	if len(errs) > 0 {
		return SpecSelection{}, errs
	}

	pricing, err := s.d.Products.GetSpecPricing(ctx, sp.ID, cycle)
	if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
		fe("could not load spec pricing")
		return SpecSelection{}, errs
	}

	sel := SpecSelection{
		Key: sp.Key, ProvisionKey: string(sp.ProvisionKey), Unit: string(sp.Unit),
		Qty: qty, Unlimited: unlimited,
	}
	if unlimited {
		sel.Qty = domain.UnlimitedQty
		if pricing != nil {
			sel.Amount = pricing.UnlimitedPrice
		}
	} else if pricing != nil {
		chargeable := qty - sp.IncludedQty
		if chargeable < 0 {
			chargeable = 0
		}
		sel.Amount = chargeable * pricing.UnitPrice
	}
	return sel, nil
}

// specSummary renders a human-readable spec list for the invoice/order line.
func specSummary(sels []SpecSelection) string {
	if len(sels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(sels))
	for _, s := range sels {
		qty := "unlimited"
		if !s.Unlimited {
			unit := ""
			switch domain.SpecUnit(s.Unit) {
			case domain.UnitGB:
				unit = "GB"
			case domain.UnitMB:
				unit = "MB"
			}
			qty = fmt.Sprintf("%d%s", s.Qty, unit)
		}
		parts = append(parts, fmt.Sprintf("%s %s", qty, s.Key))
	}
	return strings.Join(parts, ", ")
}

// domainExtensionOf returns the registry extension of a registrable domain
// name (no leading dot), e.g. "example.co.id" -> "co.id".
func domainExtensionOf(name string) string {
	i := strings.IndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return name[i+1:]
}

// planDomain prices a domain registration/transfer line. The third return is
// a hard error (e.g. registrar unreachable) that must abort the checkout.
//
// Price resolution order: an exact premium-domain override, else a premium
// length-tier match (TLD + registrable-label character count), else the
// active TLD pricing row for the extension (validating the requested years
// against its min/max and looking up the year-count in its register/renew
// matrix), else product pricing (type domain, annual cycle) when a product is
// referenced, else the registrar's live quote. Requested domain addons add
// their annual price on top of every path.
func (s *Service) planDomain(ctx context.Context, it OrderItemRequest) (itemPlan, []apperr.FieldError, error) {
	var errs []apperr.FieldError
	fieldErr := func(field, msg string) {
		errs = append(errs, apperr.FieldError{Field: field, Message: msg})
	}

	name := strings.ToLower(strings.TrimSpace(it.Domain))
	if name == "" {
		fieldErr("domain", "is required for domain items")
		return itemPlan{}, errs, nil
	}
	if !ValidDomainName(name) {
		fieldErr("domain", "is not a valid domain name")
		return itemPlan{}, errs, nil
	}
	years := it.DomainYears
	if years == 0 {
		years = defaultDomainYears
	}
	isTransfer := domain.OrderItemType(it.ItemType) == domain.ItemDomainTransfer

	var (
		unit         int64 // total charged for this term (register/transfer + years)
		renewPerYear int64 // base renewal rate for future years (excludes addons)
		priced       bool
		product      *domain.Product
	)

	// 1. Exact premium-domain override wins over everything else.
	if premium, err := s.d.PremiumPricing.GetByName(ctx, name); err == nil && premium != nil {
		if isTransfer {
			unit = premium.TransferPrice * int64(years)
		} else {
			unit = premium.RegisterPrice * int64(years)
		}
		renewPerYear = premium.RenewPrice
		priced = true
	}

	// 2. Premium length-tier match (TLD + registrable-label character count,
	// e.g. Dewabiz's "Limited Character" table) - one flat price covers
	// register/renew/transfer for the tier.
	if !priced {
		ext := domainExtensionOf(name)
		if tier, err := s.d.PremiumLengthPricing.GetByTLDAndLength(ctx, ext, len(strings.TrimSuffix(name, "."+ext))); err == nil && tier != nil {
			unit = tier.Price * int64(years)
			renewPerYear = tier.Price
			priced = true
		}
	}

	// 3. Active TLD pricing row for the extension. Once matched, this row
	// owns the price - a years count outside its bounds, or missing from its
	// register-price matrix, is a configuration gap to fix, not a silent
	// fallback trigger to a different price source.
	if !priced {
		tld, err := s.d.TLDPricing.GetByTLD(ctx, domainExtensionOf(name))
		if err == nil && tld != nil && tld.Active {
			if years < tld.MinYears || years > tld.MaxYears {
				fieldErr("domain_years", fmt.Sprintf("must be between %d and %d years for this TLD", tld.MinYears, tld.MaxYears))
				return itemPlan{}, errs, nil
			}
			if isTransfer {
				unit = tld.TransferPrice * int64(years)
			} else {
				price, ok := tld.RegisterPrices[strconv.Itoa(years)]
				if !ok {
					fieldErr("domain_years", "no price configured for this many years")
					return itemPlan{}, errs, nil
				}
				unit = price
			}
			renewPerYear = tld.RenewPrices["1"]
			priced = true
		}
	}

	// 4. Product pricing (type domain, annual cycle) when a product is referenced.
	if it.ProductID != 0 {
		p, err := s.d.Products.GetByID(ctx, it.ProductID)
		if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
			return itemPlan{}, nil, err
		}
		if p == nil || p.DeletedAt != nil || p.Type != domain.ProductDomain {
			fieldErr("product_id", "must reference a domain product")
			return itemPlan{}, errs, nil
		}
		product = p
		if !priced {
			pricing, err := s.d.Products.GetPricing(ctx, p.ID, domain.CycleAnnually)
			if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
				return itemPlan{}, nil, err
			}
			if pricing != nil {
				unit = pricing.Price * int64(years)
				renewPerYear = pricing.Price
				priced = true
			}
		}
	}

	// Availability check is mandatory for registrations; for transfers it is
	// only consulted as a price fallback.
	if !isTransfer || !priced {
		res, err := s.d.Registrar.CheckAvailability(ctx, []string{name})
		if err != nil {
			return itemPlan{}, nil, err
		}
		var avail *struct {
			available bool
			price     int64
		}
		for _, r := range res {
			if strings.EqualFold(r.Name, name) {
				avail = &struct {
					available bool
					price     int64
				}{r.Available, r.Price}
				break
			}
		}
		if !isTransfer {
			if avail == nil || !avail.available {
				fieldErr("domain", "domain is not available for registration")
				return itemPlan{}, errs, nil
			}
		}
		if !priced && avail != nil && avail.price > 0 {
			unit = avail.price * int64(years)
			renewPerYear = avail.price
			priced = true
		}
	}
	if !priced {
		fieldErr("domain", "no pricing available for this domain")
		return itemPlan{}, errs, nil
	}

	// Requested, currently-active addons add their annual price on top -
	// once for this term's total, once (undiscounted) into the renewal rate.
	addonTotal, err := s.domainAddonsTotal(ctx, it.DomainAddons, fieldErr)
	if err != nil {
		return itemPlan{}, nil, err
	}
	if len(errs) > 0 {
		return itemPlan{}, errs, nil
	}
	unit += addonTotal * int64(years)
	renewPerYear += addonTotal

	opts := itemOptions{DomainYears: years, DomainAddons: it.DomainAddons, DomainRenewPerYear: renewPerYear}
	if isTransfer && it.EPPCode != "" {
		enc, err := s.d.Encryptor.Encrypt(it.EPPCode)
		if err != nil {
			return itemPlan{}, nil, apperr.Internal(err)
		}
		opts.EPPCodeEnc = enc
	}

	verb := "Registration"
	itemType := domain.ItemDomainRegister
	if isTransfer {
		verb = "Transfer"
		itemType = domain.ItemDomainTransfer
	}
	return itemPlan{
		itemType:   itemType,
		product:    product,
		cycle:      domain.CycleAnnually,
		unit:       unit,
		desc:       fmt.Sprintf("Domain %s %s (%d year(s))", verb, name, years),
		domainName: name,
		opts:       opts,
		years:      years,
	}, nil, nil
}

// domainAddonsTotal sums the annual price of every requested addon key,
// validating each against the active addon catalog via fieldErr.
func (s *Service) domainAddonsTotal(ctx context.Context, keys []string, fieldErr func(field, msg string)) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	catalog, err := s.d.DomainAddons.ListActive(ctx)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	prices := make(map[string]int64, len(catalog))
	for _, a := range catalog {
		prices[a.Key] = a.Price
	}
	var total int64
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if seen[k] {
			continue
		}
		price, ok := prices[k]
		if !ok {
			fieldErr("domain_addons", "addon "+k+" is not available")
			continue
		}
		seen[k] = true
		total += price
	}
	return total, nil
}

// optionDelta reads the per-cycle price delta map ({"monthly":10000,...}).
func optionDelta(raw json.RawMessage, cycle domain.BillingCycle) int64 {
	if len(raw) == 0 {
		return 0
	}
	var m map[string]int64
	if json.Unmarshal(raw, &m) != nil {
		return 0
	}
	return m[string(cycle)]
}

// couponProductIDs parses coupons.applies_to; supported shapes are a bare
// array of product ids ([1,2]) and {"product_ids":[1,2]}. Empty means the
// coupon applies to every item.
func couponProductIDs(c *domain.Coupon) []int64 {
	if len(c.AppliesTo) == 0 {
		return nil
	}
	var ids []int64
	if json.Unmarshal(c.AppliesTo, &ids) == nil {
		return ids
	}
	var wrapped struct {
		ProductIDs []int64 `json:"product_ids"`
	}
	if json.Unmarshal(c.AppliesTo, &wrapped) == nil {
		return wrapped.ProductIDs
	}
	return nil
}

// couponAppliesToProduct reports whether the coupon covers the product.
func couponAppliesToProduct(c *domain.Coupon, productID int64) bool {
	ids := couponProductIDs(c)
	if len(ids) == 0 {
		return true
	}
	for _, id := range ids {
		if id == productID {
			return true
		}
	}
	return false
}

// couponDiscount computes the order discount over the eligible recurring
// base (unit prices; setup fees are never discounted). Percentage discounts
// round half-up; fixed discounts are clamped to the eligible base.
func couponDiscount(c *domain.Coupon, plans []itemPlan) int64 {
	var base int64
	for _, p := range plans {
		if planEligible(c, p) {
			base += p.unit
		}
	}
	if base <= 0 {
		return 0
	}
	switch c.Type {
	case domain.CouponPercentage:
		return (base*c.Value + 50) / 100
	case domain.CouponFixed:
		if c.Value > base {
			return base
		}
		return c.Value
	}
	return 0
}

func planEligible(c *domain.Coupon, p itemPlan) bool {
	ids := couponProductIDs(c)
	if len(ids) == 0 {
		return true
	}
	if p.product == nil {
		return false
	}
	return couponAppliesToProduct(c, p.product.ID)
}

// recurringWithCoupon returns the per-cycle recurring amount after applying a
// recurring coupon to one item (percentage rounds half-up; fixed clamps at 0).
func recurringWithCoupon(c *domain.Coupon, unit int64) int64 {
	switch c.Type {
	case domain.CouponPercentage:
		d := (unit*c.Value + 50) / 100
		return unit - d
	case domain.CouponFixed:
		if c.Value >= unit {
			return 0
		}
		return unit - c.Value
	}
	return unit
}

// ValidDomainName checks basic FQDN syntax: >=2 labels of [a-z0-9-] (no
// leading/trailing hyphen), each 1-63 chars, total <=253, TLD >=2 chars.
// Internationalized names must be punycode-encoded (xn--).
func ValidDomainName(name string) bool {
	if len(name) < 3 || len(name) > 253 {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for li, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
		// TLD: at least 2 chars, letters only (allows punycode elsewhere).
		if li == len(labels)-1 && len(label) < 2 {
			return false
		}
	}
	return true
}
