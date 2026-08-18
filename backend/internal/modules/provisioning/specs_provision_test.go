package provisioning

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configurableProduct() *domain.Product {
	p := cpanelProduct()
	p.Configurable = true
	return p
}

func withChosenSpecs() json.RawMessage {
	return json.RawMessage(`{"chosen_specs":[` +
		`{"key":"disk","provision_key":"disk","unit":"gb","qty":20},` +
		`{"key":"bandwidth","provision_key":"bandwidth","unit":"gb","qty":-1,"unlimited":true}]}`)
}

func specDefs() []domain.ProductSpec {
	return []domain.ProductSpec{
		{ID: 1, ProductID: 5, Key: "disk", ProvisionKey: domain.SpecDisk, Unit: domain.UnitGB},
		{ID: 2, ProductID: 5, Key: "bandwidth", ProvisionKey: domain.SpecBandwidth, Unit: domain.UnitGB, AllowUnlimited: true},
	}
}

func TestProvisionCreate_ConfigurableEnsuresPackage(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServicePending)
	svc.PanelMeta = withChosenSpecs()
	f.service = svc
	f.withProduct(configurableProduct())
	f.withServer(cpanelServer())
	f.products.ListSpecsFn = func(_ context.Context, productID int64) ([]domain.ProductSpec, error) {
		return specDefs(), nil
	}

	var ensured ports.PackageSpec
	f.cpanel.EnsurePackageFn = func(_ context.Context, _ ports.ServerConfig, spec ports.PackageSpec) error {
		ensured = spec
		return nil
	}
	var created ports.CreateAccountParams
	f.cpanel.CreateFn = func(_ context.Context, _ ports.ServerConfig, a ports.CreateAccountParams) (*ports.AccountResult, error) {
		created = a
		return &ports.AccountResult{Username: "example1"}, nil
	}

	require.NoError(t, f.svc.ProvisionCreate(context.Background(), 42))

	// Package built from chosen specs; name is a deterministic hash of the
	// resolved limits, not the service ID.
	wantName := domain.DynamicPackageName("", map[domain.ProvisionKey]int64{
		domain.SpecDisk:      20 * 1024,
		domain.SpecBandwidth: ports.Unlimited,
	}, domain.PackageToggles{})
	assert.Equal(t, wantName, ensured.Name)
	assert.Equal(t, int64(20*1024), ensured.Limits[domain.SpecDisk])
	assert.Equal(t, ports.Unlimited, ensured.Limits[domain.SpecBandwidth])
	// Account created under that package.
	assert.Equal(t, wantName, created.Package)
	// Package name recorded in panel_meta for later terminate.
	assert.Contains(t, string(f.service.PanelMeta), wantName)
	assert.Contains(t, string(f.service.PanelMeta), domain.PanelMetaPackageName)
}

// TestBuildPackageSpec_SameSpecsShareName proves the reuse property: two
// distinct services resolving to identical limits land on the same package
// name, so EnsurePackage's create-or-update idempotency shares one package
// between them instead of creating a near-duplicate per service.
func TestBuildPackageSpec_SameSpecsShareName(t *testing.T) {
	f := newFixture()
	f.products.ListSpecsFn = func(_ context.Context, _ int64) ([]domain.ProductSpec, error) {
		return specDefs(), nil
	}
	product := configurableProduct()

	svcA := baseService(domain.ServicePending)
	svcA.ID = 42
	svcA.PanelMeta = withChosenSpecs()
	svcB := baseService(domain.ServicePending)
	svcB.ID = 99
	svcB.PanelMeta = withChosenSpecs()

	specA, err := f.svc.buildPackageSpec(context.Background(), svcA, product, "")
	require.NoError(t, err)
	specB, err := f.svc.buildPackageSpec(context.Background(), svcB, product, "")
	require.NoError(t, err)

	assert.NotEmpty(t, specA.Name)
	assert.Equal(t, specA.Name, specB.Name, "identical chosen specs on different services must share one package name")
}

// TestBuildPackageSpec_DifferentSpecsGetDifferentNames guards against the
// hash scheme accidentally collapsing genuinely different specs together.
func TestBuildPackageSpec_DifferentSpecsGetDifferentNames(t *testing.T) {
	f := newFixture()
	f.products.ListSpecsFn = func(_ context.Context, _ int64) ([]domain.ProductSpec, error) {
		return specDefs(), nil
	}
	product := configurableProduct()

	svcA := baseService(domain.ServicePending)
	svcA.PanelMeta = withChosenSpecs()
	svcB := baseService(domain.ServicePending)
	svcB.PanelMeta = json.RawMessage(`{"chosen_specs":[` +
		`{"key":"disk","provision_key":"disk","unit":"gb","qty":50},` +
		`{"key":"bandwidth","provision_key":"bandwidth","unit":"gb","qty":-1,"unlimited":true}]}`)

	specA, err := f.svc.buildPackageSpec(context.Background(), svcA, product, "")
	require.NoError(t, err)
	specB, err := f.svc.buildPackageSpec(context.Background(), svcB, product, "")
	require.NoError(t, err)

	assert.NotEqual(t, specA.Name, specB.Name)
}

// TestProvisionCreate_ConfigurablePackageUsesServerPrefix covers a real bug
// found 2026-07-27 against a live reseller WHM ("unable to use package
// whcms_s43... you have not exceeded your reseller restrictions"): the
// server's own PackagePrefix must be prepended to the dynamic package name
// used for EnsurePackage, Create, and the panel_meta record - not just the
// bare domain.DynamicPackageName("", ...).
func TestProvisionCreate_ConfigurablePackageUsesServerPrefix(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServicePending)
	svc.PanelMeta = withChosenSpecs()
	f.service = svc
	f.withProduct(configurableProduct())
	server := cpanelServer()
	server.PackagePrefix = "reseller_"
	f.withServer(server)
	f.products.ListSpecsFn = func(_ context.Context, productID int64) ([]domain.ProductSpec, error) {
		return specDefs(), nil
	}

	var ensured ports.PackageSpec
	f.cpanel.EnsurePackageFn = func(_ context.Context, _ ports.ServerConfig, spec ports.PackageSpec) error {
		ensured = spec
		return nil
	}
	var created ports.CreateAccountParams
	f.cpanel.CreateFn = func(_ context.Context, _ ports.ServerConfig, a ports.CreateAccountParams) (*ports.AccountResult, error) {
		created = a
		return &ports.AccountResult{Username: "example1"}, nil
	}

	require.NoError(t, f.svc.ProvisionCreate(context.Background(), 42))

	wantName := domain.DynamicPackageName("reseller_", map[domain.ProvisionKey]int64{
		domain.SpecDisk:      20 * 1024,
		domain.SpecBandwidth: ports.Unlimited,
	}, domain.PackageToggles{})
	assert.Equal(t, wantName, ensured.Name)
	assert.Equal(t, wantName, created.Package)
	assert.Contains(t, string(f.service.PanelMeta), wantName)
}

// TestProvisionCreate_ConfigurablePackageCarriesToggles proves buildPackageSpec
// copies Shell/CGI/FeatureList/TemplatePackage straight from the product onto
// the PackageSpec handed to EnsurePackage.
func TestProvisionCreate_ConfigurablePackageCarriesToggles(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServicePending)
	svc.PanelMeta = withChosenSpecs()
	f.service = svc
	product := configurableProduct()
	product.ShellAccess = true
	product.CGIAccess = true
	product.FeatureList = "custom_features"
	f.withProduct(product)
	f.withServer(cpanelServer())
	f.products.ListSpecsFn = func(_ context.Context, productID int64) ([]domain.ProductSpec, error) {
		return specDefs(), nil
	}

	var ensured ports.PackageSpec
	f.cpanel.EnsurePackageFn = func(_ context.Context, _ ports.ServerConfig, spec ports.PackageSpec) error {
		ensured = spec
		return nil
	}
	f.cpanel.CreateFn = func(_ context.Context, _ ports.ServerConfig, a ports.CreateAccountParams) (*ports.AccountResult, error) {
		return &ports.AccountResult{Username: "example1"}, nil
	}

	require.NoError(t, f.svc.ProvisionCreate(context.Background(), 42))

	assert.True(t, ensured.ShellAccess)
	assert.True(t, ensured.CGIAccess)
	assert.Equal(t, "custom_features", ensured.FeatureList)
}

// TestBuildPackageSpec_DifferentTogglesGetDifferentNames guards against two
// services with identical resolved limits but different toggles incorrectly
// sharing one physical panel package.
func TestBuildPackageSpec_DifferentTogglesGetDifferentNames(t *testing.T) {
	f := newFixture()
	f.products.ListSpecsFn = func(_ context.Context, _ int64) ([]domain.ProductSpec, error) {
		return specDefs(), nil
	}
	productA := configurableProduct()
	productB := configurableProduct()
	productB.ShellAccess = true

	svcA := baseService(domain.ServicePending)
	svcA.PanelMeta = withChosenSpecs()
	svcB := baseService(domain.ServicePending)
	svcB.PanelMeta = withChosenSpecs()

	specA, err := f.svc.buildPackageSpec(context.Background(), svcA, productA, "")
	require.NoError(t, err)
	specB, err := f.svc.buildPackageSpec(context.Background(), svcB, productB, "")
	require.NoError(t, err)

	assert.NotEqual(t, specA.Name, specB.Name, "identical limits with different ShellAccess must not share a package name")
}

func TestProvisionCreate_EnsurePackageFailureStaysPending(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServicePending)
	svc.PanelMeta = withChosenSpecs()
	f.service = svc
	f.withProduct(configurableProduct())
	f.withServer(cpanelServer())
	f.products.ListSpecsFn = func(_ context.Context, _ int64) ([]domain.ProductSpec, error) { return specDefs(), nil }
	f.cpanel.EnsurePackageFn = func(context.Context, ports.ServerConfig, ports.PackageSpec) error {
		return apperr.New(apperr.CodeExternal, "addpkg failed")
	}
	f.cpanel.CreateFn = func(context.Context, ports.ServerConfig, ports.CreateAccountParams) (*ports.AccountResult, error) {
		t.Fatal("Create must not run when EnsurePackage fails")
		return nil, nil
	}

	assertCode(t, f.svc.ProvisionCreate(context.Background(), 42), apperr.CodeExternal)
	assert.Equal(t, domain.ServicePending, f.service.Status)
}

func TestProvisionCreate_NonConfigurableUsesProductPackage(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(cpanelProduct()) // not configurable
	f.withServer(cpanelServer())
	f.cpanel.EnsurePackageFn = func(context.Context, ports.ServerConfig, ports.PackageSpec) error {
		t.Fatal("EnsurePackage must not run for non-configurable products")
		return nil
	}
	var created ports.CreateAccountParams
	f.cpanel.CreateFn = func(_ context.Context, _ ports.ServerConfig, a ports.CreateAccountParams) (*ports.AccountResult, error) {
		created = a
		return &ports.AccountResult{Username: "example1"}, nil
	}
	require.NoError(t, f.svc.ProvisionCreate(context.Background(), 42))
	assert.Equal(t, "basic", created.Package)
}

func TestProvisionTerminate_DeletesPackage(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.PanelMeta = json.RawMessage(`{"package_name":"whcms_s42"}`)
	f.service = svc
	f.withProduct(configurableProduct())
	f.withServer(cpanelServer())

	terminated := false
	f.cpanel.TerminateFn = func(context.Context, ports.ServerConfig, string) error { terminated = true; return nil }
	var deleted string
	f.cpanel.DeletePackageFn = func(_ context.Context, _ ports.ServerConfig, name string) error {
		deleted = name
		return nil
	}

	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
	assert.True(t, terminated)
	assert.Equal(t, "whcms_s42", deleted)
	assert.Equal(t, domain.ServiceTerminated, f.service.Status)
}

// TestProvisionTerminate_KeepsSharedPackageWhenSiblingStillUsesIt: when
// another non-terminal service on the same server still references the
// dynamic package, terminate must not delete it out from under that sibling
// - but the terminating service itself must still transition normally.
func TestProvisionTerminate_KeepsSharedPackageWhenSiblingStillUsesIt(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.PanelMeta = json.RawMessage(`{"package_name":"whcms_spec_abc123"}`)
	f.service = svc
	f.withProduct(configurableProduct())
	f.withServer(cpanelServer())

	f.cpanel.TerminateFn = func(context.Context, ports.ServerConfig, string) error { return nil }
	f.cpanel.DeletePackageFn = func(context.Context, ports.ServerConfig, string) error {
		t.Fatal("DeletePackage must not run while a sibling service still references the package")
		return nil
	}
	var countedServer int64
	var countedPkg string
	var countedExclude int64
	f.store.CountByServerAndPackageFn = func(_ context.Context, serverID int64, pkg string, excludeID int64) (int64, error) {
		countedServer, countedPkg, countedExclude = serverID, pkg, excludeID
		return 1, nil // one sibling still using it
	}

	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
	assert.Equal(t, domain.ServiceTerminated, f.service.Status)
	assert.Equal(t, int64(3), countedServer) // cpanelServer().ID
	assert.Equal(t, "whcms_spec_abc123", countedPkg)
	assert.Equal(t, int64(42), countedExclude)
}

// TestProvisionTerminate_SkipsDeleteWhenPanelStillUsesPackage: the DB-side
// sibling count can't see accounts created outside this app - the panel-side
// PackageInUse check must veto the delete, alert the operator, and still let
// the termination itself complete.
func TestProvisionTerminate_SkipsDeleteWhenPanelStillUsesPackage(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.PanelMeta = json.RawMessage(`{"package_name":"whcms_spec_abc123"}`)
	f.service = svc
	f.withProduct(configurableProduct())
	f.withServer(cpanelServer())

	f.cpanel.TerminateFn = func(context.Context, ports.ServerConfig, string) error { return nil }
	var checked string
	f.cpanel.PackageInUseFn = func(_ context.Context, _ ports.ServerConfig, name string) (bool, error) {
		checked = name
		return true, nil // an out-of-band panel account still uses it
	}
	f.cpanel.DeletePackageFn = func(context.Context, ports.ServerConfig, string) error {
		t.Fatal("DeletePackage must not run while the panel reports the package in use")
		return nil
	}
	var alerts []string
	f.notify.AlertAdminFn = func(_ context.Context, subject, _ string) error {
		alerts = append(alerts, subject)
		return nil
	}

	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
	assert.Equal(t, domain.ServiceTerminated, f.service.Status)
	assert.Equal(t, "whcms_spec_abc123", checked)
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0], "Package cleanup needs attention")
}

// TestProvisionTerminate_DeleteFailureStillTerminates: package cleanup is
// best-effort and runs AFTER the terminated status is persisted - a killpkg
// failure must alert the operator, never fail (and so retry) the job.
func TestProvisionTerminate_DeleteFailureStillTerminates(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.PanelMeta = json.RawMessage(`{"package_name":"whcms_spec_abc123"}`)
	f.service = svc
	f.withProduct(configurableProduct())
	f.withServer(cpanelServer())

	f.cpanel.TerminateFn = func(context.Context, ports.ServerConfig, string) error { return nil }
	f.cpanel.DeletePackageFn = func(context.Context, ports.ServerConfig, string) error {
		return apperr.New(apperr.CodeExternal, "killpkg failed")
	}
	var alerts []string
	f.notify.AlertAdminFn = func(_ context.Context, subject, _ string) error {
		alerts = append(alerts, subject)
		return nil
	}

	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
	assert.Equal(t, domain.ServiceTerminated, f.service.Status)
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0], "whcms_spec_abc123")
}

// TestProvisionTerminate_PackageInUseErrorFailsSafe: when the panel-side
// check itself errors, the package must be LEFT IN PLACE (fail safe - a
// stranded package is recoverable, deleting an in-use one is not) and the
// operator alerted, while the termination still completes.
func TestProvisionTerminate_PackageInUseErrorFailsSafe(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.PanelMeta = json.RawMessage(`{"package_name":"whcms_spec_abc123"}`)
	f.service = svc
	f.withProduct(configurableProduct())
	f.withServer(cpanelServer())

	f.cpanel.TerminateFn = func(context.Context, ports.ServerConfig, string) error { return nil }
	f.cpanel.PackageInUseFn = func(context.Context, ports.ServerConfig, string) (bool, error) {
		return false, apperr.New(apperr.CodeExternal, "listaccts failed")
	}
	f.cpanel.DeletePackageFn = func(context.Context, ports.ServerConfig, string) error {
		t.Fatal("DeletePackage must not run when the in-use check errored")
		return nil
	}
	alerted := false
	f.notify.AlertAdminFn = func(context.Context, string, string) error { alerted = true; return nil }

	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
	assert.Equal(t, domain.ServiceTerminated, f.service.Status)
	assert.True(t, alerted)
}

// TestProvisionTerminate_DirectAdminDeletesPackage drives the same
// terminate-with-dynamic-package flow through the DirectAdmin module wiring
// (the provisioning-service tests otherwise only exercise the cpanel mock).
func TestProvisionTerminate_DirectAdminDeletesPackage(t *testing.T) {
	f := newFixture()
	f.svc.d.Modules["directadmin"] = f.da // wired per-test: other tests rely on DA being absent
	svc := baseService(domain.ServiceActive)
	svc.PanelMeta = json.RawMessage(`{"package_name":"whcms_spec_da1"}`)
	f.service = svc
	product := configurableProduct()
	product.Module = domain.ModuleDirectAdmin
	f.withProduct(product)
	server := cpanelServer()
	server.Module = domain.ModuleDirectAdmin
	f.withServer(server)

	terminated := false
	f.da.TerminateFn = func(context.Context, ports.ServerConfig, string) error { terminated = true; return nil }
	var checked string
	f.da.PackageInUseFn = func(_ context.Context, _ ports.ServerConfig, name string) (bool, error) {
		checked = name
		return false, nil
	}
	var deleted string
	f.da.DeletePackageFn = func(_ context.Context, _ ports.ServerConfig, name string) error {
		deleted = name
		return nil
	}
	f.cpanel.TerminateFn = func(context.Context, ports.ServerConfig, string) error {
		t.Fatal("the cpanel module must not be called for a directadmin product")
		return nil
	}

	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
	assert.True(t, terminated)
	assert.Equal(t, "whcms_spec_da1", checked)
	assert.Equal(t, "whcms_spec_da1", deleted)
	assert.Equal(t, domain.ServiceTerminated, f.service.Status)
}

func TestProvisionTerminate_NoPackageNoDelete(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive) // panel_meta = {}
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.TerminateFn = func(context.Context, ports.ServerConfig, string) error { return nil }
	f.cpanel.DeletePackageFn = func(context.Context, ports.ServerConfig, string) error {
		t.Fatal("DeletePackage must not run without a package_name")
		return nil
	}
	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
}
