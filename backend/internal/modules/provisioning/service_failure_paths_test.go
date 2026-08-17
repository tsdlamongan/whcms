package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Failure paths of the provisioning service: dependency errors surface as
// apperrs, preconditions are enforced, and partial work is never applied.

func TestWrapPassesApperrThrough(t *testing.T) {
	orig := apperr.NotFound("x")
	assert.Same(t, orig, wrap(orig))

	wrapped := wrap(errors.New("db down"))
	var ae *apperr.Error
	require.ErrorAs(t, wrapped, &ae)
	assert.Equal(t, apperr.CodeInternal, ae.Code)
}

func TestPanelURL(t *testing.T) {
	assert.Equal(t, "https://h:2083", panelURL(domain.ModuleCpanel, "h"))
	assert.Equal(t, "https://h:2222", panelURL(domain.ModuleDirectAdmin, "h"))
	assert.Empty(t, panelURL(domain.ModuleNone, "h"))
}

func TestDefaultPort(t *testing.T) {
	assert.Equal(t, 2087, defaultPort(domain.ModuleCpanel))
	assert.Equal(t, 2222, defaultPort(domain.ModuleDirectAdmin))
	assert.Equal(t, 0, defaultPort(domain.ModuleNone))
}

func TestMetaHelpers(t *testing.T) {
	assert.Empty(t, metaMap(nil))
	assert.Empty(t, metaMap(json.RawMessage(`{broken`)))
	assert.False(t, metaBool(json.RawMessage(`{}`), "x"))
	assert.True(t, metaBool(json.RawMessage(`{"x":true}`), "x"))
	assert.Equal(t, json.RawMessage(`{}`), marshalMeta(map[string]any{"bad": func() {}}))
}

func TestProvisionChangePasswordWorkerVariant(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	called := false
	f.cpanel.ChangePasswordFn = func(context.Context, ports.ServerConfig, string, string) error {
		called = true
		return nil
	}
	require.NoError(t, f.svc.ProvisionChangePassword(context.Background(), 42, "Sup3rStrongPass"))
	assert.True(t, called)
}

func TestProvisionCreateUnknownService(t *testing.T) {
	f := newFixture()
	assertCode(t, f.svc.ProvisionCreate(context.Background(), 42), apperr.CodeNotFound)
}

func TestProvisionCreatePickServerFails(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServicePending)
	svc.ServerID = nil
	f.service = svc
	f.withProduct(cpanelProduct())
	f.servers.PickServerFn = func(context.Context, int64) (*domain.Server, error) {
		return nil, apperr.Conflict("no capacity")
	}
	assertCode(t, f.svc.ProvisionCreate(context.Background(), 42), apperr.CodeConflict)
}

func TestProvisionCreateDecryptErrors(t *testing.T) {
	// Server credential decrypt failure.
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.crypt.DecryptFn = func(string) (string, error) { return "", errors.New("bad key") }
	assertCode(t, f.svc.ProvisionCreate(context.Background(), 42), apperr.CodeInternal)
}

func TestProvisionCreateUpdateFailureAfterAdapter(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.store.UpdateFn = func(context.Context, *domain.Service) error { return errors.New("db down") }
	assertCode(t, f.svc.ProvisionCreate(context.Background(), 42), apperr.CodeInternal)
}

func TestProvisionCreateProductMissing(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(nil)
	assertCode(t, f.svc.ProvisionCreate(context.Background(), 42), apperr.CodeNotFound)
}

func TestProvisionSuspendWithoutPanelStillTransitions(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.ServerID = nil
	f.service = svc
	f.withProduct(&domain.Product{ID: 5, Module: domain.ModuleNone})

	require.NoError(t, f.svc.ProvisionSuspend(context.Background(), 42, "manual"))
	assert.Equal(t, domain.ServiceSuspended, f.service.Status)
}

func TestProvisionSuspendUnknownService(t *testing.T) {
	f := newFixture()
	assertCode(t, f.svc.ProvisionSuspend(context.Background(), 42, "x"), apperr.CodeNotFound)
}

func TestProvisionSuspendUpdateError(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.store.UpdateFn = func(context.Context, *domain.Service) error { return errors.New("db down") }
	assertCode(t, f.svc.ProvisionSuspend(context.Background(), 42, "x"), apperr.CodeInternal)
}

func TestProvisionUnsuspendErrors(t *testing.T) {
	f := newFixture()
	assertCode(t, f.svc.ProvisionUnsuspend(context.Background(), 42), apperr.CodeNotFound)

	// Panel context resolution failure (product gone).
	f = newFixture()
	f.service = baseService(domain.ServiceSuspended)
	f.withProduct(nil)
	assertCode(t, f.svc.ProvisionUnsuspend(context.Background(), 42), apperr.CodeNotFound)

	// Adapter failure.
	f = newFixture()
	f.service = baseService(domain.ServiceSuspended)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.UnsuspendFn = func(context.Context, ports.ServerConfig, string) error {
		return apperr.New(apperr.CodeExternal, "boom")
	}
	assertCode(t, f.svc.ProvisionUnsuspend(context.Background(), 42), apperr.CodeExternal)

	// Update failure.
	f = newFixture()
	f.service = baseService(domain.ServiceSuspended)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.store.UpdateFn = func(context.Context, *domain.Service) error { return errors.New("db down") }
	assertCode(t, f.svc.ProvisionUnsuspend(context.Background(), 42), apperr.CodeInternal)
}

func TestProvisionTerminateErrors(t *testing.T) {
	f := newFixture()
	assertCode(t, f.svc.ProvisionTerminate(context.Background(), 42), apperr.CodeNotFound)

	// Adapter failure keeps status.
	f = newFixture()
	f.service = baseService(domain.ServiceSuspended)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.TerminateFn = func(context.Context, ports.ServerConfig, string) error {
		return apperr.New(apperr.CodeExternal, "boom")
	}
	assertCode(t, f.svc.ProvisionTerminate(context.Background(), 42), apperr.CodeExternal)
	assert.Equal(t, domain.ServiceSuspended, f.service.Status)

	// Pending -> cancelled update failure.
	f = newFixture()
	f.service = baseService(domain.ServicePending)
	f.store.UpdateFn = func(context.Context, *domain.Service) error { return errors.New("db down") }
	assertCode(t, f.svc.ProvisionTerminate(context.Background(), 42), apperr.CodeInternal)

	// Update failure after adapter success.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.store.UpdateFn = func(context.Context, *domain.Service) error { return errors.New("db down") }
	assertCode(t, f.svc.ProvisionTerminate(context.Background(), 42), apperr.CodeInternal)
}

func TestProvisionChangePackageErrors(t *testing.T) {
	f := newFixture()
	assertCode(t, f.svc.ProvisionChangePackage(context.Background(), 42), apperr.CodeNotFound)

	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(nil)
	assertCode(t, f.svc.ProvisionChangePackage(context.Background(), 42), apperr.CodeNotFound)

	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.ChangePackageFn = func(context.Context, ports.ServerConfig, string, string) error {
		return apperr.New(apperr.CodeExternal, "boom")
	}
	assertCode(t, f.svc.ProvisionChangePackage(context.Background(), 42), apperr.CodeExternal)

	// No panel: no adapter call, still succeeds.
	f = newFixture()
	svc := baseService(domain.ServiceActive)
	svc.ServerID = nil
	f.service = svc
	f.withProduct(&domain.Product{ID: 5, Module: domain.ModuleNone, PackageName: "x"})
	require.NoError(t, f.svc.ProvisionChangePackage(context.Background(), 42))
}

func TestRenewServiceErrors(t *testing.T) {
	f := newFixture()
	assertCode(t, f.svc.RenewService(context.Background(), 42), apperr.CodeNotFound)

	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.store.UpdateFn = func(context.Context, *domain.Service) error { return errors.New("db down") }
	assertCode(t, f.svc.RenewService(context.Background(), 42), apperr.CodeInternal)

	f = newFixture()
	f.service = baseService(domain.ServiceSuspended)
	f.queue.EnqueueFn = func(context.Context, string, any, ...ports.JobOption) error {
		return errors.New("redis down")
	}
	assertCode(t, f.svc.RenewService(context.Background(), 42), apperr.CodeInternal)
}

func TestApplyUpgradeErrors(t *testing.T) {
	f := newFixture()
	assertCode(t, f.svc.ApplyUpgrade(context.Background(), 42), apperr.CodeNotFound)

	// Update failure inside tx.
	f = newFixture()
	svc := baseService(domain.ServiceActive)
	raw, _ := json.Marshal(domain.ServiceUpgrade{ProductID: 6, Cycle: domain.CycleMonthly, RecurringAmount: 1})
	svc.PendingUpgrade = raw
	f.service = svc
	f.store.UpdateFn = func(context.Context, *domain.Service) error { return errors.New("db down") }
	assertCode(t, f.svc.ApplyUpgrade(context.Background(), 42), apperr.CodeInternal)

	// Enqueue failure.
	f = newFixture()
	svc = baseService(domain.ServiceActive)
	svc.PendingUpgrade = raw
	f.service = svc
	f.queue.EnqueueFn = func(context.Context, string, any, ...ports.JobOption) error {
		return errors.New("redis down")
	}
	assertCode(t, f.svc.ApplyUpgrade(context.Background(), 42), apperr.CodeInternal)
}

func TestAutoSuspendErrors(t *testing.T) {
	f := newFixture()
	f.settings.GetIntFn = func(context.Context, string, int) (int, error) { return 0, errors.New("db down") }
	_, err := f.svc.AutoSuspend(context.Background())
	assertCode(t, err, apperr.CodeInternal)

	f = newFixture()
	f.store.ListOverdueSuspendableFn = func(context.Context, time.Time) ([]domain.Service, error) {
		return nil, errors.New("db down")
	}
	_, err = f.svc.AutoSuspend(context.Background())
	assertCode(t, err, apperr.CodeInternal)
}

func TestAutoTerminateErrors(t *testing.T) {
	f := newFixture()
	f.settings.GetIntFn = func(context.Context, string, int) (int, error) { return 0, errors.New("db down") }
	_, err := f.svc.AutoTerminate(context.Background())
	assertCode(t, err, apperr.CodeInternal)

	f = newFixture()
	f.store.ListTerminatableFn = func(context.Context, time.Time, time.Time) ([]domain.Service, error) {
		return nil, errors.New("db down")
	}
	_, err = f.svc.AutoTerminate(context.Background())
	assertCode(t, err, apperr.CodeInternal)

	f = newFixture()
	f.store.ListTerminatableFn = func(context.Context, time.Time, time.Time) ([]domain.Service, error) {
		return []domain.Service{{ID: 1}}, nil
	}
	f.queue.EnqueueFn = func(context.Context, string, any, ...ports.JobOption) error {
		return errors.New("redis down")
	}
	n, err := f.svc.AutoTerminate(context.Background())
	assert.Equal(t, 0, n)
	assertCode(t, err, apperr.CodeInternal)
}

func TestListServicesError(t *testing.T) {
	f := newFixture()
	f.store.ListFn = func(context.Context, ports.ListParams) ([]domain.Service, int64, error) {
		return nil, 0, errors.New("db down")
	}
	_, _, err := f.svc.ListServices(context.Background(), 0, ports.ListParams{})
	assertCode(t, err, apperr.CodeInternal)

	f.store.ListByClientFn = func(context.Context, int64, ports.ListParams) ([]domain.Service, int64, error) {
		return nil, 0, errors.New("db down")
	}
	_, _, err = f.svc.ListServices(context.Background(), 7, ports.ListParams{})
	assertCode(t, err, apperr.CodeInternal)
}

func TestUpgradeServiceMoreErrors(t *testing.T) {
	// Current product missing.
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(nil)
	_, err := f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeNotFound)

	// Settings failure while computing due date.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withUpgradeProducts(200_000)
	f.settings.GetIntFn = func(context.Context, string, int) (int, error) { return 0, errors.New("db down") }
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeInternal)

	// Update failure after invoice creation.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withUpgradeProducts(200_000)
	f.billing.CreateInvoiceFn = func(_ context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
		return &domain.Invoice{ID: 77}, nil
	}
	f.store.UpdateFn = func(context.Context, *domain.Service) error { return errors.New("db down") }
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeInternal)

	// Credit failure rolls back the downgrade tx.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withUpgradeProducts(50_000)
	f.credit.fn = func(context.Context, int64, int64, string, int64) error { return errors.New("credit down") }
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeInternal)

	// Enqueue failure after downgrade.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withUpgradeProducts(50_000)
	f.queue.EnqueueFn = func(context.Context, string, any, ...ports.JobOption) error {
		return errors.New("redis down")
	}
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeInternal)

	// NextDueDate nil -> rejected rather than silently prorating to zero
	// (TestUpgradeServiceRejectsNilNextDueDate exercises this in detail).
	f = newFixture()
	svc := baseService(domain.ServiceActive)
	svc.NextDueDate = nil
	f.service = svc
	f.withUpgradeProducts(200_000)
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeConflict)
}

func TestCancelServiceInvoiceErrors(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.invoices.ListByClientFn = func(context.Context, int64, ports.ListParams) ([]domain.Invoice, int64, error) {
		return nil, 0, errors.New("db down")
	}
	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	assertCode(t, err, apperr.CodeInternal)

	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.invoices.ListByClientFn = func(context.Context, int64, ports.ListParams) ([]domain.Invoice, int64, error) {
		return []domain.Invoice{{ID: 100, Status: domain.InvoiceUnpaid}}, 1, nil
	}
	f.invoices.GetItemsByInvoiceIDsFn = func(context.Context, []int64) (map[int64][]domain.InvoiceItem, error) {
		return nil, errors.New("db down")
	}
	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	assertCode(t, err, apperr.CodeInternal)

	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	rid := int64(42)
	f.invoices.ListByClientFn = func(context.Context, int64, ports.ListParams) ([]domain.Invoice, int64, error) {
		return []domain.Invoice{{ID: 100, Status: domain.InvoiceUnpaid}}, 1, nil
	}
	f.invoices.GetItemsByInvoiceIDsFn = func(context.Context, []int64) (map[int64][]domain.InvoiceItem, error) {
		return map[int64][]domain.InvoiceItem{100: {{RelatedType: domain.RelatedServiceRenewal, RelatedID: &rid}}}, nil
	}
	f.invoices.UpdateStatusFn = func(context.Context, int64, domain.InvoiceStatus, *time.Time) error {
		return errors.New("db down")
	}
	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	assertCode(t, err, apperr.CodeInternal)

	// Enqueue failure.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.queue.EnqueueFn = func(context.Context, string, any, ...ports.JobOption) error {
		return errors.New("redis down")
	}
	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	assertCode(t, err, apperr.CodeInternal)

	// GetPendingByService failure.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.cancellations.GetPendingByServiceFn = func(context.Context, int64) (*domain.CancellationRequest, error) {
		return nil, errors.New("db down")
	}
	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	assertCode(t, err, apperr.CodeInternal)

	// Cancellation request create failure (end_of_term).
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.cancellations.CreateFn = func(context.Context, *domain.CancellationRequest) error {
		return errors.New("db down")
	}
	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	assertCode(t, err, apperr.CodeInternal)
}

func TestChangePasswordEncryptError(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.crypt.EncryptFn = func(string) (string, error) { return "", errors.New("bad key") }
	assertCode(t, f.svc.ChangePassword(context.Background(), 10, 7, 42, "Sup3rStrongPass"), apperr.CodeInternal)
}

func TestSSOAdapterError(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.SSOURLFn = func(context.Context, ports.ServerConfig, string) (string, error) {
		return "", apperr.New(apperr.CodeExternal, "unsupported")
	}
	_, err := f.svc.SSO(context.Background(), 10, 7, 42)
	assertCode(t, err, apperr.CodeExternal)

	// No panel.
	f = newFixture()
	svc := baseService(domain.ServiceActive)
	svc.ServerID = nil
	f.service = svc
	f.withProduct(&domain.Product{ID: 5, Module: domain.ModuleNone})
	_, err = f.svc.SSO(context.Background(), 10, 7, 42)
	assertCode(t, err, apperr.CodeConflict)
}

func TestAdminChangePackageMoreErrors(t *testing.T) {
	f := newFixture()
	assertCode(t, f.svc.AdminChangePackage(context.Background(), 10, 42, AdminChangePackageInput{}),
		apperr.CodeNotFound)

	// Rebind update failure.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		if id == 5 {
			return cpanelProduct(), nil
		}
		return &domain.Product{ID: id, Module: domain.ModuleCpanel}, nil
	}
	f.store.UpdateFn = func(context.Context, *domain.Service) error { return errors.New("db down") }
	assertCode(t, f.svc.AdminChangePackage(context.Background(), 10, 42,
		AdminChangePackageInput{ProductID: ptr(int64(6))}), apperr.CodeInternal)

	// Async enqueue failure.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.queue.EnqueueFn = func(context.Context, string, any, ...ports.JobOption) error {
		return errors.New("redis down")
	}
	assertCode(t, f.svc.AdminChangePackage(context.Background(), 10, 42,
		AdminChangePackageInput{Async: true}), apperr.CodeInternal)
}

func TestAdminActionEnqueueVariants(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)

	require.NoError(t, f.svc.AdminAction(context.Background(), 10, 42, ActionCreate, "", true))
	require.NoError(t, f.svc.AdminAction(context.Background(), 10, 42, ActionUnsuspend, "", true))
	require.NoError(t, f.svc.AdminAction(context.Background(), 10, 42, ActionTerminate, "", true))
	assert.Len(t, f.queue.Tasks, 3)

	// Enqueue error.
	f = newFixture()
	f.service = baseService(domain.ServicePending)
	f.queue.EnqueueFn = func(context.Context, string, any, ...ports.JobOption) error {
		return errors.New("redis down")
	}
	assertCode(t, f.svc.AdminAction(context.Background(), 10, 42, ActionCreate, "", true), apperr.CodeInternal)

	// Sync dispatch for the other actions.
	f = newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(&domain.Product{ID: 5, Module: domain.ModuleNone})
	require.NoError(t, f.svc.AdminAction(context.Background(), 10, 42, ActionCreate, "", false))
	assert.Equal(t, domain.ServiceActive, f.service.Status)

	require.NoError(t, f.svc.AdminAction(context.Background(), 10, 42, ActionUnsuspend, "", false))
	require.NoError(t, f.svc.AdminAction(context.Background(), 10, 42, ActionTerminate, "", false))
	assert.Equal(t, domain.ServiceTerminated, f.service.Status)
}

func TestServerCRUDErrors(t *testing.T) {
	// GetServer passthrough.
	f := newFixture()
	f.withServer(cpanelServer())
	server, err := f.svc.GetServer(context.Background(), 3)
	require.NoError(t, err)
	assert.Equal(t, int64(3), server.ID)
	_, err = f.svc.GetServer(context.Background(), 99)
	assertCode(t, err, apperr.CodeNotFound)

	// ListServers passthrough + error.
	f.servers.ListServersFn = func(context.Context, ports.ListParams) ([]domain.Server, int64, error) {
		return []domain.Server{{ID: 3}}, 1, nil
	}
	list, total, err := f.svc.ListServers(context.Background(), ports.ListParams{})
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, int64(1), total)
	f.servers.ListServersFn = func(context.Context, ports.ListParams) ([]domain.Server, int64, error) {
		return nil, 0, errors.New("db down")
	}
	_, _, err = f.svc.ListServers(context.Background(), ports.ListParams{})
	assertCode(t, err, apperr.CodeInternal)

	// Create repo failure.
	f = newFixture()
	f.servers.CreateServerFn = func(context.Context, *domain.Server) error { return errors.New("db down") }
	_, err = f.svc.CreateServer(context.Background(), 10, ServerInput{
		Name: "s", Module: "cpanel", Hostname: "h.example.com"})
	assertCode(t, err, apperr.CodeInternal)

	// Create encrypt failure.
	f = newFixture()
	f.crypt.EncryptFn = func(string) (string, error) { return "", errors.New("bad key") }
	_, err = f.svc.CreateServer(context.Background(), 10, ServerInput{
		Name: "s", Module: "cpanel", Hostname: "h.example.com", Password: "pw"})
	assertCode(t, err, apperr.CodeInternal)

	// Update: unknown server, unknown group, repo failure, validation.
	f = newFixture()
	_, err = f.svc.UpdateServer(context.Background(), 10, 99, ServerInput{
		Name: "s", Module: "cpanel", Hostname: "h.example.com"})
	assertCode(t, err, apperr.CodeNotFound)

	f = newFixture()
	f.withServer(cpanelServer())
	_, err = f.svc.UpdateServer(context.Background(), 10, 3, ServerInput{
		GroupID: ptr(int64(77)), Name: "s", Module: "cpanel", Hostname: "h.example.com"})
	assertCode(t, err, apperr.CodeNotFound)

	f = newFixture()
	f.withServer(cpanelServer())
	f.servers.UpdateServerFn = func(context.Context, *domain.Server) error { return errors.New("db down") }
	_, err = f.svc.UpdateServer(context.Background(), 10, 3, ServerInput{
		Name: "s", Module: "cpanel", Hostname: "h.example.com"})
	assertCode(t, err, apperr.CodeInternal)

	f = newFixture()
	_, err = f.svc.UpdateServer(context.Background(), 10, 3, ServerInput{})
	assertCode(t, err, apperr.CodeValidation)

	// Delete: unknown server, count failure, repo failure.
	f = newFixture()
	assertCode(t, f.svc.DeleteServer(context.Background(), 10, 99), apperr.CodeNotFound)

	f = newFixture()
	f.withServer(cpanelServer())
	f.store.CountByServerFn = func(context.Context, int64) (int64, error) { return 0, errors.New("db down") }
	assertCode(t, f.svc.DeleteServer(context.Background(), 10, 3), apperr.CodeInternal)

	f = newFixture()
	f.withServer(cpanelServer())
	f.servers.DeleteServerFn = func(context.Context, int64) error { return errors.New("db down") }
	assertCode(t, f.svc.DeleteServer(context.Background(), 10, 3), apperr.CodeInternal)
}

func TestTestConnectionErrors(t *testing.T) {
	// Decrypt failure surfaces as INTERNAL.
	f := newFixture()
	f.withServer(cpanelServer())
	f.crypt.DecryptFn = func(string) (string, error) { return "", errors.New("bad key") }
	_, err := f.svc.TestConnection(context.Background(), TestConnectionInput{ID: 3})
	assertCode(t, err, apperr.CodeInternal)

	// Module not wired.
	f = newFixture()
	sv := cpanelServer()
	sv.Module = domain.ModuleDirectAdmin
	f.withServer(sv)
	_, err = f.svc.TestConnection(context.Background(), TestConnectionInput{ID: 3})
	assertCode(t, err, apperr.CodeInternal)
}

func TestGroupCRUDErrors(t *testing.T) {
	f := newFixture()
	f.servers.CreateGroupFn = func(context.Context, *domain.ServerGroup) error { return errors.New("db down") }
	_, err := f.svc.CreateGroup(context.Background(), 10, ServerGroupInput{Name: "g", Strategy: "round_robin"})
	assertCode(t, err, apperr.CodeInternal)

	f = newFixture()
	_, err = f.svc.UpdateGroup(context.Background(), 10, 9, ServerGroupInput{Name: "g", Strategy: "bogus"})
	assertCode(t, err, apperr.CodeValidation)

	f = newFixture()
	_, err = f.svc.UpdateGroup(context.Background(), 10, 9, ServerGroupInput{Name: "g", Strategy: "round_robin"})
	assertCode(t, err, apperr.CodeNotFound)

	f = newFixture()
	f.servers.GetGroupByIDFn = func(_ context.Context, id int64) (*domain.ServerGroup, error) {
		return &domain.ServerGroup{ID: id}, nil
	}
	f.servers.UpdateGroupFn = func(context.Context, *domain.ServerGroup) error { return errors.New("db down") }
	_, err = f.svc.UpdateGroup(context.Background(), 10, 9, ServerGroupInput{Name: "g", Strategy: "round_robin"})
	assertCode(t, err, apperr.CodeInternal)

	f = newFixture()
	f.servers.ListGroupsFn = func(context.Context) ([]domain.ServerGroup, error) { return nil, errors.New("db down") }
	_, err = f.svc.ListGroups(context.Background())
	assertCode(t, err, apperr.CodeInternal)

	f = newFixture()
	_, err = f.svc.GetGroup(context.Background(), 9)
	assertCode(t, err, apperr.CodeNotFound)

	f = newFixture()
	assertCode(t, f.svc.DeleteGroup(context.Background(), 10, 9), apperr.CodeNotFound)

	f = newFixture()
	f.servers.GetGroupByIDFn = func(_ context.Context, id int64) (*domain.ServerGroup, error) {
		return &domain.ServerGroup{ID: id}, nil
	}
	f.servers.DeleteGroupFn = func(context.Context, int64) error { return apperr.Conflict("referenced") }
	assertCode(t, f.svc.DeleteGroup(context.Background(), 10, 9), apperr.CodeConflict)
}

func TestNotifyServiceSwallowsClientLookupFailure(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(&domain.Product{ID: 5, Module: domain.ModuleNone})
	f.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) {
		return nil, errors.New("db down")
	}
	require.NoError(t, f.svc.ProvisionCreate(context.Background(), 42))
	assert.Empty(t, f.sent)
}
