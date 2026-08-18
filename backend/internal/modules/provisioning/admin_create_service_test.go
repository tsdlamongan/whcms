package provisioning

// AdminCreateService tests: the "add existing hosting" path - an admin
// records a pre-existing service on a client with no order, no provisioning
// job and no control-panel call.

import (
	"context"
	"errors"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func adminCreateInput() AdminCreateServiceInput {
	return AdminCreateServiceInput{
		ClientID:        7,
		ProductID:       5,
		ServerID:        ptr(int64(3)),
		Domain:          "imported.example.com",
		Username:        "imported1",
		BillingCycle:    "monthly",
		RecurringAmount: 150_000,
		NextDueDate:     "2026-08-01",
	}
}

func TestAdminCreateService(t *testing.T) {
	f := newFixture()
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	var created *domain.Service
	f.store.CreateFn = func(_ context.Context, s *domain.Service) error {
		s.ID = 77
		cp := *s
		created = &cp
		return nil
	}
	f.crypt.EncryptFn = func(plaintext string) (string, error) { return "enc:" + plaintext, nil }
	f.cpanel.CreateFn = func(context.Context, ports.ServerConfig, ports.CreateAccountParams) (*ports.AccountResult, error) {
		t.Fatal("adding an existing service must never call the panel")
		return nil, nil
	}
	f.queue.EnqueueFn = func(_ context.Context, taskType string, _ any, _ ...ports.JobOption) error {
		t.Fatalf("adding an existing service must not enqueue jobs (got %s)", taskType)
		return nil
	}
	sent := false
	f.notify.SendTemplateFn = func(context.Context, int64, string, map[string]any) error { sent = true; return nil }

	in := adminCreateInput()
	in.Password = "SuperSecret99"
	svc, err := f.svc.AdminCreateService(context.Background(), 1, in)
	require.NoError(t, err)

	require.NotNil(t, created)
	assert.Equal(t, int64(77), svc.ID)
	assert.Equal(t, domain.ServiceActive, created.Status)
	assert.Equal(t, int64(7), created.ClientID)
	assert.Nil(t, created.OrderItemID, "no order backs a manually-added service")
	assert.Equal(t, "enc:SuperSecret99", created.PasswordEnc)
	assert.Equal(t, domain.CycleMonthly, created.BillingCycle)
	require.NotNil(t, created.NextDueDate)
	assert.Equal(t, "2026-08-01", created.NextDueDate.Format("2006-01-02"))
	// registration_date defaults to today when not given.
	require.NotNil(t, created.RegistrationDate)
	assert.Equal(t, testNow.Format("2006-01-02"), created.RegistrationDate.Format("2006-01-02"))
	assert.False(t, sent, "the client must not be notified - nothing was provisioned")
}

func TestAdminCreateServiceValidation(t *testing.T) {
	f := newFixture()
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())

	// Missing required fields.
	_, err := f.svc.AdminCreateService(context.Background(), 1, AdminCreateServiceInput{})
	assertCode(t, err, apperr.CodeValidation)

	// Recurring cycle requires a next_due_date so renewal billing works.
	in := adminCreateInput()
	in.NextDueDate = ""
	_, err = f.svc.AdminCreateService(context.Background(), 1, in)
	assertCode(t, err, apperr.CodeValidation)

	// one_time is fine without one.
	in = adminCreateInput()
	in.BillingCycle = "one_time"
	in.NextDueDate = ""
	svc, err := f.svc.AdminCreateService(context.Background(), 1, in)
	require.NoError(t, err)
	assert.Nil(t, svc.NextDueDate)

	// Bad date format.
	in = adminCreateInput()
	in.RegistrationDate = "01-08-2026"
	_, err = f.svc.AdminCreateService(context.Background(), 1, in)
	assertCode(t, err, apperr.CodeValidation)
}

func TestAdminCreateServiceReferenceChecks(t *testing.T) {
	// Unknown product.
	f := newFixture()
	f.withProduct(nil)
	f.withServer(cpanelServer())
	_, err := f.svc.AdminCreateService(context.Background(), 1, adminCreateInput())
	assertCode(t, err, apperr.CodeNotFound)

	// Unknown client.
	f = newFixture()
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) {
		return nil, apperr.NotFound("client")
	}
	_, err = f.svc.AdminCreateService(context.Background(), 1, adminCreateInput())
	assertCode(t, err, apperr.CodeNotFound)

	// Unknown server.
	f = newFixture()
	f.withProduct(cpanelProduct())
	f.withServer(nil)
	_, err = f.svc.AdminCreateService(context.Background(), 1, adminCreateInput())
	assertCode(t, err, apperr.CodeNotFound)

	// Server module mismatch: a cpanel product cannot sit on a DA server.
	f = newFixture()
	f.withProduct(cpanelProduct())
	server := cpanelServer()
	server.Module = domain.ModuleDirectAdmin
	f.withServer(server)
	_, err = f.svc.AdminCreateService(context.Background(), 1, adminCreateInput())
	assertCode(t, err, apperr.CodeValidation)

	// module=none product on any server is fine (record-only binding).
	f = newFixture()
	f.withProduct(&domain.Product{ID: 5, Name: "Record Only", Module: domain.ModuleNone})
	server = cpanelServer()
	server.Module = domain.ModuleDirectAdmin
	f.withServer(server)
	_, err = f.svc.AdminCreateService(context.Background(), 1, adminCreateInput())
	require.NoError(t, err)
}

func TestAdminCreateServiceInfraErrors(t *testing.T) {
	// Encrypt failure.
	f := newFixture()
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.crypt.EncryptFn = func(string) (string, error) { return "", errors.New("no key") }
	in := adminCreateInput()
	in.Password = "SuperSecret99"
	_, err := f.svc.AdminCreateService(context.Background(), 1, in)
	assertCode(t, err, apperr.CodeInternal)

	// Insert failure.
	f = newFixture()
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.store.CreateFn = func(context.Context, *domain.Service) error { return errors.New("db down") }
	_, err = f.svc.AdminCreateService(context.Background(), 1, adminCreateInput())
	assertCode(t, err, apperr.CodeInternal)
}
