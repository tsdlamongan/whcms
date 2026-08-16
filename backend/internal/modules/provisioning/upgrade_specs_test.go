package provisioning

// Spec-aware upgrade tests: UpgradeService pricing/validation for custom-spec
// (configurable) targets, ApplyUpgrade's chosen_specs rebinding and
// ProvisionChangePackage's dynamic-package rebuild. The flat-product upgrade
// paths are covered in service_test.go.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upgradeSpecDefs are the target product's knobs: disk 5-100GB step 5
// (5GB included, 10k/GB above), bandwidth 10GB+ step 10 (1k/GB, unlimited
// allowed at a flat 50k).
func upgradeSpecDefs() []domain.ProductSpec {
	return []domain.ProductSpec{
		{ID: 11, ProductID: 6, Key: "disk", ProvisionKey: domain.SpecDisk, Unit: domain.UnitGB,
			IncludedQty: 5, MinQty: 5, MaxQty: 100, StepQty: 5, DefaultQty: 5},
		{ID: 12, ProductID: 6, Key: "bandwidth", ProvisionKey: domain.SpecBandwidth, Unit: domain.UnitGB,
			MinQty: 10, StepQty: 10, DefaultQty: 10, AllowUnlimited: true},
	}
}

// withConfigurableUpgradeTarget wires product 6 as a configurable cpanel
// target priced basePrice/monthly with upgradeSpecDefs knobs.
func (f *fx) withConfigurableUpgradeTarget(basePrice int64) {
	current := cpanelProduct()
	target := &domain.Product{ID: 6, Name: "Custom Cloud", Module: domain.ModuleCpanel,
		Configurable: true, ServerGroupID: ptr(int64(9))}
	f.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		switch id {
		case 5:
			return current, nil
		case 6:
			return target, nil
		}
		return nil, apperr.NotFound("product")
	}
	f.products.GetPricingFn = func(_ context.Context, productID int64, cycle domain.BillingCycle) (*domain.ProductPricing, error) {
		if productID == 6 && cycle == domain.CycleMonthly {
			return &domain.ProductPricing{ProductID: 6, Cycle: cycle, Price: basePrice}, nil
		}
		return nil, apperr.NotFound("pricing")
	}
	f.products.ListSpecsFn = func(_ context.Context, productID int64) ([]domain.ProductSpec, error) {
		if productID == 6 {
			return upgradeSpecDefs(), nil
		}
		return nil, nil
	}
	f.products.GetSpecPricingFn = func(_ context.Context, specID int64, cycle domain.BillingCycle) (*domain.ProductSpecPricing, error) {
		switch specID {
		case 11:
			return &domain.ProductSpecPricing{SpecID: 11, Cycle: cycle, UnitPrice: 10_000}, nil
		case 12:
			return &domain.ProductSpecPricing{SpecID: 12, Cycle: cycle, UnitPrice: 1_000, UnlimitedPrice: 50_000}, nil
		}
		return nil, apperr.NotFound("spec pricing")
	}
}

// decodeChosenSpecs reads the chosen_specs snapshot from a panel_meta blob.
func decodeChosenSpecs(t *testing.T, panelMeta json.RawMessage) []domain.UpgradeSpec {
	t.Helper()
	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(panelMeta, &meta))
	raw, ok := meta[domain.PanelMetaChosenSpecs]
	if !ok {
		return nil
	}
	var specs []domain.UpgradeSpec
	require.NoError(t, json.Unmarshal(raw, &specs))
	return specs
}

func TestUpgradeServiceConfigurableCreatesSpecPricedInvoice(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive) // monthly 100k, next due 2026-07-16
	f.withConfigurableUpgradeTarget(150_000)

	var gotInput ports.CreateInvoiceInput
	f.billing.CreateInvoiceFn = func(_ context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
		gotInput = in
		return &domain.Invoice{ID: 77, ClientID: in.ClientID, Status: domain.InvoiceUnpaid}, nil
	}

	// disk 20GB -> (20-5)*10k = 150k; bandwidth unlimited -> flat 50k.
	res, err := f.svc.UpgradeService(context.Background(), 10, 7, 42, UpgradeServiceInput{
		ProductID: 6, Cycle: domain.CycleMonthly,
		Specs: []UpgradeSpecInput{
			{Key: "disk", Qty: 20},
			{Key: "bandwidth", Unlimited: true},
		},
	})
	require.NoError(t, err)
	require.False(t, res.Applied)
	require.NotNil(t, res.Invoice)

	// The prorated diff must be computed over base + spec charges (350k), not
	// the flat base price alone.
	until := date(2026, 7, 16)
	wantDiff := domain.Prorate(350_000, domain.CycleMonthly, testNow, until) -
		domain.Prorate(100_000, domain.CycleMonthly, testNow, until)
	assert.Equal(t, wantDiff, res.ProratedDiff)
	require.Len(t, gotInput.Items, 1)
	assert.Equal(t, wantDiff, gotInput.Items[0].Amount)
	assert.Contains(t, gotInput.Items[0].Description, "20GB disk")
	assert.Contains(t, gotInput.Items[0].Description, "unlimited bandwidth")

	// Pending upgrade carries the resolved specs so ApplyUpgrade can rebuild
	// the panel package after payment.
	var up domain.ServiceUpgrade
	require.NoError(t, json.Unmarshal(f.service.PendingUpgrade, &up))
	assert.Equal(t, int64(350_000), up.RecurringAmount)
	require.Len(t, up.Specs, 2)
	assert.Equal(t, domain.UpgradeSpec{Key: "disk", ProvisionKey: "disk", Unit: "gb", Qty: 20, Amount: 150_000}, up.Specs[0])
	assert.Equal(t, domain.UpgradeSpec{Key: "bandwidth", ProvisionKey: "bandwidth", Unit: "gb",
		Qty: domain.UnlimitedQty, Unlimited: true, Amount: 50_000}, up.Specs[1])
	assert.Empty(t, f.queue.Tasks) // nothing provisioned until the invoice is paid
}

func TestUpgradeServiceConfigurableUsesDefaultsForUnchosenKnobs(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withConfigurableUpgradeTarget(150_000)
	f.billing.CreateInvoiceFn = func(_ context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
		return &domain.Invoice{ID: 78, ClientID: in.ClientID, Status: domain.InvoiceUnpaid}, nil
	}

	// No specs chosen: disk default 5 (0 above included), bandwidth default
	// 10GB -> 10k. Target total 160k.
	_, err := f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	require.NoError(t, err)

	var up domain.ServiceUpgrade
	require.NoError(t, json.Unmarshal(f.service.PendingUpgrade, &up))
	assert.Equal(t, int64(160_000), up.RecurringAmount)
	require.Len(t, up.Specs, 2)
	assert.Equal(t, int64(5), up.Specs[0].Qty)
	assert.Equal(t, int64(0), up.Specs[0].Amount)
	assert.Equal(t, int64(10), up.Specs[1].Qty)
	assert.Equal(t, int64(10_000), up.Specs[1].Amount)
}

func TestUpgradeServiceConfigurableSpecValidation(t *testing.T) {
	cases := []struct {
		name  string
		specs []UpgradeSpecInput
	}{
		{"below min", []UpgradeSpecInput{{Key: "disk", Qty: 2}}},
		{"above max", []UpgradeSpecInput{{Key: "disk", Qty: 200}}},
		{"off step", []UpgradeSpecInput{{Key: "disk", Qty: 12}}},
		{"unlimited not allowed", []UpgradeSpecInput{{Key: "disk", Unlimited: true}}},
		{"unknown key", []UpgradeSpecInput{{Key: "gpu", Qty: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			f.service = baseService(domain.ServiceActive)
			f.withConfigurableUpgradeTarget(150_000)
			_, err := f.svc.UpgradeService(context.Background(), 10, 7, 42,
				UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly, Specs: tc.specs})
			assertCode(t, err, apperr.CodeValidation)
			assert.Empty(t, f.service.PendingUpgrade)
		})
	}
}

func TestUpgradeServiceConfigurableSameProductResize(t *testing.T) {
	// The service already runs the configurable product with disk 20 +
	// bandwidth unlimited; changing only the disk is a valid resize.
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.ProductID = 6
	svc.PanelMeta = json.RawMessage(`{"chosen_specs":[` +
		`{"key":"disk","provision_key":"disk","unit":"gb","qty":20,"amount":150000},` +
		`{"key":"bandwidth","provision_key":"bandwidth","unit":"gb","qty":-1,"unlimited":true,"amount":50000}]}`)
	f.service = svc
	f.withConfigurableUpgradeTarget(150_000)
	f.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		if id == 6 {
			return &domain.Product{ID: 6, Name: "Custom Cloud", Module: domain.ModuleCpanel,
				Configurable: true, ServerGroupID: ptr(int64(9))}, nil
		}
		return nil, apperr.NotFound("product")
	}
	f.billing.CreateInvoiceFn = func(_ context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
		return &domain.Invoice{ID: 79, ClientID: in.ClientID, Status: domain.InvoiceUnpaid}, nil
	}

	res, err := f.svc.UpgradeService(context.Background(), 10, 7, 42, UpgradeServiceInput{
		ProductID: 6, Cycle: domain.CycleMonthly,
		Specs: []UpgradeSpecInput{{Key: "disk", Qty: 40}, {Key: "bandwidth", Unlimited: true}},
	})
	require.NoError(t, err)
	assert.False(t, res.Applied)

	// Identical choices are a no-op, not an upgrade.
	f2 := newFixture()
	svc2 := baseService(domain.ServiceActive)
	svc2.ProductID = 6
	svc2.PanelMeta = svc.PanelMeta
	f2.service = svc2
	f2.withConfigurableUpgradeTarget(150_000)
	f2.products.GetByIDFn = f.products.GetByIDFn
	_, err = f2.svc.UpgradeService(context.Background(), 10, 7, 42, UpgradeServiceInput{
		ProductID: 6, Cycle: domain.CycleMonthly,
		Specs: []UpgradeSpecInput{{Key: "disk", Qty: 20}, {Key: "bandwidth", Unlimited: true}},
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestUpgradeServiceConfigurableDowngradeAppliesSpecsImmediately(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive) // monthly 100k
	f.withConfigurableUpgradeTarget(50_000)

	// Minimal knobs: disk 5 (included) + bandwidth 10 -> 10k. Total 60k < 100k.
	res, err := f.svc.UpgradeService(context.Background(), 10, 7, 42, UpgradeServiceInput{
		ProductID: 6, Cycle: domain.CycleMonthly,
		Specs: []UpgradeSpecInput{{Key: "disk", Qty: 5}, {Key: "bandwidth", Qty: 10}},
	})
	require.NoError(t, err)
	assert.True(t, res.Applied)
	assert.Positive(t, res.CreditIssued)
	require.Len(t, f.credit.calls, 1)

	assert.Equal(t, int64(6), f.service.ProductID)
	assert.Equal(t, int64(60_000), f.service.RecurringAmount)
	specs := decodeChosenSpecs(t, f.service.PanelMeta)
	require.Len(t, specs, 2)
	assert.Equal(t, int64(5), specs[0].Qty)
	assert.Equal(t, int64(10), specs[1].Qty)

	require.Len(t, f.queue.Tasks, 1)
	assert.Equal(t, jobs.TypeProvisionChangePackage, f.queue.Tasks[0].TaskType)
}

func TestApplyUpgradeWritesChosenSpecs(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	up := domain.ServiceUpgrade{
		ProductID: 6, Cycle: domain.CycleMonthly, RecurringAmount: 350_000, InvoiceID: 77,
		Specs: []domain.UpgradeSpec{
			{Key: "disk", ProvisionKey: "disk", Unit: "gb", Qty: 20, Amount: 150_000},
			{Key: "bandwidth", ProvisionKey: "bandwidth", Unit: "gb", Qty: domain.UnlimitedQty, Unlimited: true, Amount: 50_000},
		},
	}
	raw, _ := json.Marshal(up)
	svc.PendingUpgrade = raw
	f.service = svc

	require.NoError(t, f.svc.ApplyUpgrade(context.Background(), 42))
	assert.Equal(t, int64(6), f.service.ProductID)
	assert.Empty(t, f.service.PendingUpgrade)
	specs := decodeChosenSpecs(t, f.service.PanelMeta)
	require.Len(t, specs, 2)
	assert.Equal(t, int64(20), specs[0].Qty)
	assert.True(t, specs[1].Unlimited)
	require.Len(t, f.queue.Tasks, 1)
	assert.Equal(t, jobs.TypeProvisionChangePackage, f.queue.Tasks[0].TaskType)
}

func TestApplyUpgradeToFlatProductClearsChosenSpecs(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.PanelMeta = withChosenSpecs() // moving away from a configurable product
	up := domain.ServiceUpgrade{ProductID: 6, Cycle: domain.CycleMonthly, RecurringAmount: 200_000, InvoiceID: 77}
	raw, _ := json.Marshal(up)
	svc.PendingUpgrade = raw
	f.service = svc

	require.NoError(t, f.svc.ApplyUpgrade(context.Background(), 42))
	assert.Empty(t, decodeChosenSpecs(t, f.service.PanelMeta),
		"stale chosen_specs must not survive a move to a flat product")
}

func TestProvisionChangePackageConfigurableEnsuresDynamicPackage(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	meta := map[string]any{
		domain.PanelMetaChosenSpecs: []domain.UpgradeSpec{
			{Key: "disk", ProvisionKey: "disk", Unit: "gb", Qty: 20},
			{Key: "bandwidth", ProvisionKey: "bandwidth", Unit: "gb", Qty: domain.UnlimitedQty, Unlimited: true},
		},
		domain.PanelMetaPackageName: "whcms_spec_old",
	}
	rawMeta, _ := json.Marshal(meta)
	svc.PanelMeta = rawMeta
	f.service = svc
	f.withProduct(configurableProduct())
	f.withServer(cpanelServer())
	f.products.ListSpecsFn = func(_ context.Context, _ int64) ([]domain.ProductSpec, error) {
		return specDefs(), nil
	}

	var ensured ports.PackageSpec
	f.cpanel.EnsurePackageFn = func(_ context.Context, _ ports.ServerConfig, spec ports.PackageSpec) error {
		ensured = spec
		return nil
	}
	var changedTo string
	f.cpanel.ChangePackageFn = func(_ context.Context, _ ports.ServerConfig, _, pkg string) error {
		changedTo = pkg
		return nil
	}
	var deleted []string
	f.cpanel.DeletePackageFn = func(_ context.Context, _ ports.ServerConfig, name string) error {
		deleted = append(deleted, name)
		return nil
	}

	require.NoError(t, f.svc.ProvisionChangePackage(context.Background(), 42))

	// Package rebuilt from chosen_specs: disk 20GB -> 20480 MB, bandwidth
	// unlimited sentinel; the account is moved onto the new dynamic name.
	assert.Equal(t, int64(20480), ensured.Limits[domain.SpecDisk])
	assert.Equal(t, ports.Unlimited, ensured.Limits[domain.SpecBandwidth])
	assert.Equal(t, ensured.Name, changedTo)
	assert.NotEqual(t, "whcms_spec_old", changedTo)

	// Bookkeeping updated and the orphaned old dynamic package removed.
	var newMeta map[string]any
	require.NoError(t, json.Unmarshal(f.service.PanelMeta, &newMeta))
	assert.Equal(t, ensured.Name, newMeta[domain.PanelMetaPackageName])
	assert.Contains(t, newMeta, domain.PanelMetaLimits)
	assert.Equal(t, []string{"whcms_spec_old"}, deleted)
}

func TestProvisionChangePackageKeepsSharedOldPackage(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.PanelMeta = json.RawMessage(`{"chosen_specs":[{"key":"disk","provision_key":"disk","unit":"gb","qty":20}],"package_name":"whcms_spec_old"}`)
	f.service = svc
	f.withProduct(configurableProduct())
	f.withServer(cpanelServer())
	f.products.ListSpecsFn = func(_ context.Context, _ int64) ([]domain.ProductSpec, error) {
		return specDefs(), nil
	}
	f.store.CountByServerAndPackageFn = func(_ context.Context, _ int64, _ string, _ int64) (int64, error) {
		return 1, nil // a sibling service still uses the old package
	}
	deleteCalled := false
	f.cpanel.DeletePackageFn = func(context.Context, ports.ServerConfig, string) error {
		deleteCalled = true
		return nil
	}

	require.NoError(t, f.svc.ProvisionChangePackage(context.Background(), 42))
	assert.False(t, deleteCalled)
}

func TestProvisionChangePackageFlatClearsDynamicMeta(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.PanelMeta = json.RawMessage(`{"package_name":"whcms_spec_old","limits":{"disk":10240}}`)
	f.service = svc
	f.withProduct(cpanelProduct()) // flat product, package "basic"
	f.withServer(cpanelServer())

	var changedTo string
	f.cpanel.ChangePackageFn = func(_ context.Context, _ ports.ServerConfig, _, pkg string) error {
		changedTo = pkg
		return nil
	}
	var deleted []string
	f.cpanel.DeletePackageFn = func(_ context.Context, _ ports.ServerConfig, name string) error {
		deleted = append(deleted, name)
		return nil
	}

	require.NoError(t, f.svc.ProvisionChangePackage(context.Background(), 42))
	assert.Equal(t, "basic", changedTo)

	var newMeta map[string]any
	require.NoError(t, json.Unmarshal(f.service.PanelMeta, &newMeta))
	assert.NotContains(t, newMeta, domain.PanelMetaPackageName)
	assert.NotContains(t, newMeta, domain.PanelMetaLimits)
	assert.Equal(t, []string{"whcms_spec_old"}, deleted)
}
