package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test doubles for module-local interfaces

// serviceStoreMock extends MockServiceRepo with the ServiceStore extras.
type serviceStoreMock struct {
	mocks.MockServiceRepo
	ListOverdueSuspendableFn  func(ctx context.Context, before time.Time) ([]domain.Service, error)
	ListTerminatableFn        func(ctx context.Context, suspendedBefore, dueBefore time.Time) ([]domain.Service, error)
	CountByServerAndPackageFn func(ctx context.Context, serverID int64, packageName string, excludeServiceID int64) (int64, error)
}

func (m *serviceStoreMock) ListOverdueSuspendable(ctx context.Context, before time.Time) ([]domain.Service, error) {
	if m.ListOverdueSuspendableFn != nil {
		return m.ListOverdueSuspendableFn(ctx, before)
	}
	return nil, nil
}

func (m *serviceStoreMock) ListTerminatable(ctx context.Context, suspendedBefore, dueBefore time.Time) ([]domain.Service, error) {
	if m.ListTerminatableFn != nil {
		return m.ListTerminatableFn(ctx, suspendedBefore, dueBefore)
	}
	return nil, nil
}

func (m *serviceStoreMock) CountByServerAndPackage(ctx context.Context, serverID int64, packageName string, excludeServiceID int64) (int64, error) {
	if m.CountByServerAndPackageFn != nil {
		return m.CountByServerAndPackageFn(ctx, serverID, packageName, excludeServiceID)
	}
	return 0, nil
}

// serverStoreMock implements ServerStore with function fields.
type serverStoreMock struct {
	CreateServerFn  func(ctx context.Context, s *domain.Server) error
	GetServerByIDFn func(ctx context.Context, id int64) (*domain.Server, error)
	UpdateServerFn  func(ctx context.Context, s *domain.Server) error
	ListServersFn   func(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error)
	DeleteServerFn  func(ctx context.Context, id int64) error
	CreateGroupFn   func(ctx context.Context, g *domain.ServerGroup) error
	GetGroupByIDFn  func(ctx context.Context, id int64) (*domain.ServerGroup, error)
	UpdateGroupFn   func(ctx context.Context, g *domain.ServerGroup) error
	ListGroupsFn    func(ctx context.Context) ([]domain.ServerGroup, error)
	DeleteGroupFn   func(ctx context.Context, id int64) error
	PickServerFn    func(ctx context.Context, groupID int64) (*domain.Server, error)
}

func (m *serverStoreMock) CreateServer(ctx context.Context, s *domain.Server) error {
	if m.CreateServerFn != nil {
		return m.CreateServerFn(ctx, s)
	}
	return nil
}

func (m *serverStoreMock) GetServerByID(ctx context.Context, id int64) (*domain.Server, error) {
	if m.GetServerByIDFn != nil {
		return m.GetServerByIDFn(ctx, id)
	}
	return nil, apperr.NotFound("server")
}

func (m *serverStoreMock) UpdateServer(ctx context.Context, s *domain.Server) error {
	if m.UpdateServerFn != nil {
		return m.UpdateServerFn(ctx, s)
	}
	return nil
}

func (m *serverStoreMock) ListServers(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error) {
	if m.ListServersFn != nil {
		return m.ListServersFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *serverStoreMock) DeleteServer(ctx context.Context, id int64) error {
	if m.DeleteServerFn != nil {
		return m.DeleteServerFn(ctx, id)
	}
	return nil
}

func (m *serverStoreMock) CreateGroup(ctx context.Context, g *domain.ServerGroup) error {
	if m.CreateGroupFn != nil {
		return m.CreateGroupFn(ctx, g)
	}
	return nil
}

func (m *serverStoreMock) GetGroupByID(ctx context.Context, id int64) (*domain.ServerGroup, error) {
	if m.GetGroupByIDFn != nil {
		return m.GetGroupByIDFn(ctx, id)
	}
	return nil, apperr.NotFound("server group")
}

func (m *serverStoreMock) UpdateGroup(ctx context.Context, g *domain.ServerGroup) error {
	if m.UpdateGroupFn != nil {
		return m.UpdateGroupFn(ctx, g)
	}
	return nil
}

func (m *serverStoreMock) ListGroups(ctx context.Context) ([]domain.ServerGroup, error) {
	if m.ListGroupsFn != nil {
		return m.ListGroupsFn(ctx)
	}
	return nil, nil
}

func (m *serverStoreMock) DeleteGroup(ctx context.Context, id int64) error {
	if m.DeleteGroupFn != nil {
		return m.DeleteGroupFn(ctx, id)
	}
	return nil
}

func (m *serverStoreMock) PickServer(ctx context.Context, groupID int64) (*domain.Server, error) {
	if m.PickServerFn != nil {
		return m.PickServerFn(ctx, groupID)
	}
	return nil, apperr.Conflict("no server")
}

// creditAdderMock records AddCredit calls.
type creditAdderMock struct {
	fn    func(ctx context.Context, clientID, delta int64, reason string, relatedInvoiceID int64) error
	calls []struct {
		ClientID, Delta, InvoiceID int64
		Reason                     string
	}
}

func (m *creditAdderMock) AddCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID int64) error {
	m.calls = append(m.calls, struct {
		ClientID, Delta, InvoiceID int64
		Reason                     string
	}{clientID, delta, relatedInvoiceID, reason})
	if m.fn != nil {
		return m.fn(ctx, clientID, delta, reason, relatedInvoiceID)
	}
	return nil
}

// Fixture

var testNow = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

type fx struct {
	store    *serviceStoreMock
	servers  *serverStoreMock
	products *mocks.MockProductRepo
	clients  *mocks.MockClientRepo
	users    *mocks.MockUserRepo
	invoices *mocks.MockInvoiceRepo
	cpanel   *mocks.MockServerModule
	da       *mocks.MockServerModule
	crypt    *mocks.MockEncryptor
	queue    *mocks.MockEnqueuer
	notify   *mocks.MockNotificationSender
	billing  *mocks.MockInvoiceCreator
	credit   *creditAdderMock
	settings *mocks.MockSettingsRepo
	audit    *mocks.MockAuditLogger

	cancellations *mocks.MockCancellationRequestRepo
	crStore       map[int64]*domain.CancellationRequest
	crNextID      int64

	service *domain.Service // row returned by GetByID
	updated []domain.Service
	sent    []string // notification template keys

	svc *Service
}

func newFixture() *fx {
	f := &fx{
		store:    &serviceStoreMock{},
		servers:  &serverStoreMock{},
		products: &mocks.MockProductRepo{},
		clients:  &mocks.MockClientRepo{},
		users:    &mocks.MockUserRepo{},
		invoices: &mocks.MockInvoiceRepo{},
		cpanel:   &mocks.MockServerModule{},
		da:       &mocks.MockServerModule{},
		crypt:    &mocks.MockEncryptor{},
		queue:    &mocks.MockEnqueuer{},
		notify:   &mocks.MockNotificationSender{},
		billing:  &mocks.MockInvoiceCreator{},
		credit:   &creditAdderMock{},
		settings: &mocks.MockSettingsRepo{},
		audit:    &mocks.MockAuditLogger{},

		cancellations: &mocks.MockCancellationRequestRepo{},
		crStore:       map[int64]*domain.CancellationRequest{},
	}
	f.store.GetByIDFn = func(_ context.Context, id int64) (*domain.Service, error) {
		if f.service == nil || f.service.ID != id {
			return nil, apperr.NotFound("service")
		}
		cp := *f.service
		return &cp, nil
	}
	f.store.GetByIDsFn = func(_ context.Context, ids []int64) ([]domain.Service, error) {
		if f.service == nil {
			return nil, nil
		}
		for _, id := range ids {
			if id == f.service.ID {
				return []domain.Service{*f.service}, nil
			}
		}
		return nil, nil
	}
	f.store.UpdateFn = func(_ context.Context, s *domain.Service) error {
		f.updated = append(f.updated, *s)
		cp := *s
		f.service = &cp
		return nil
	}
	f.clients.GetByIDFn = func(_ context.Context, id int64) (*domain.Client, error) {
		return &domain.Client{ID: id, UserID: 900 + id, FirstName: "Test"}, nil
	}
	f.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
		return &domain.User{ID: id, Email: "client@example.com"}, nil
	}
	f.notify.SendTemplateFn = func(_ context.Context, _ int64, key string, _ map[string]any) error {
		f.sent = append(f.sent, key)
		return nil
	}
	f.cancellations.CreateFn = func(_ context.Context, cr *domain.CancellationRequest) error {
		f.crNextID++
		cr.ID = f.crNextID
		cr.RequestedAt = testNow
		cr.CreatedAt = testNow
		cr.UpdatedAt = testNow
		cp := *cr
		f.crStore[cr.ID] = &cp
		return nil
	}
	f.cancellations.GetByIDFn = func(_ context.Context, id int64) (*domain.CancellationRequest, error) {
		if r, ok := f.crStore[id]; ok {
			cp := *r
			return &cp, nil
		}
		return nil, apperr.NotFound("cancellation request")
	}
	f.cancellations.GetPendingByServiceFn = func(_ context.Context, serviceID int64) (*domain.CancellationRequest, error) {
		for _, r := range f.crStore {
			if r.ServiceID == serviceID && r.Status == domain.CancellationPending {
				cp := *r
				return &cp, nil
			}
		}
		return nil, nil
	}
	f.cancellations.UpdateFn = func(_ context.Context, cr *domain.CancellationRequest) error {
		if _, ok := f.crStore[cr.ID]; !ok {
			return apperr.NotFound("cancellation request")
		}
		cp := *cr
		f.crStore[cr.ID] = &cp
		return nil
	}
	f.cancellations.ListFn = func(_ context.Context, p ports.ListParams) ([]domain.CancellationRequest, int64, error) {
		var out []domain.CancellationRequest
		for _, r := range f.crStore {
			if p.Status != "" && string(r.Status) != p.Status {
				continue
			}
			if p.ServiceID != 0 && r.ServiceID != p.ServiceID {
				continue
			}
			out = append(out, *r)
		}
		return out, int64(len(out)), nil
	}

	f.svc = New(Deps{
		Services: f.store,
		Servers:  f.servers,
		Products: f.products,
		Clients:  f.clients,
		Users:    f.users,
		Invoices: f.invoices,
		Modules:  map[string]ports.ServerModule{"cpanel": f.cpanel},
		Crypt:    f.crypt,
		Queue:    f.queue,
		Notify:   f.notify,
		Billing:  f.billing,
		Credit:   f.credit,
		Settings: f.settings,
		Tx:       &mocks.MockTxManager{},
		Audit:    f.audit,
		Clock:    &mocks.MockClock{FixedTime: testNow},

		CancellationRequests: f.cancellations,
	})
	return f
}

func ptr[T any](v T) *T { return &v }

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// baseService returns a hosted cpanel service owned by client 7 on server 3.
func baseService(status domain.ServiceStatus) *domain.Service {
	return &domain.Service{
		ID: 42, ClientID: 7, ProductID: 5, ServerID: ptr(int64(3)),
		Domain: "example.com", Username: "example1", PasswordEnc: "secret-pw",
		Status: status, BillingCycle: domain.CycleMonthly, RecurringAmount: 100_000,
		NextDueDate: ptr(date(2026, 7, 16)),
		PanelMeta:   json.RawMessage(`{}`),
	}
}

func cpanelProduct() *domain.Product {
	return &domain.Product{
		ID: 5, Name: "Hosting Basic", Module: domain.ModuleCpanel,
		PackageName: "basic", ServerGroupID: ptr(int64(9)),
	}
}

func cpanelServer() *domain.Server {
	return &domain.Server{
		ID: 3, Name: "srv1", Module: domain.ModuleCpanel, Hostname: "srv1.host.id",
		Port: 2087, Username: "root", PasswordEnc: "srv-pass", APITokenEnc: "srv-token",
		UseSSL: true, Nameserver1: "ns1.host.id", Nameserver2: "ns2.host.id",
	}
}

func (f *fx) withProduct(p *domain.Product) {
	f.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		if p != nil && p.ID == id {
			cp := *p
			return &cp, nil
		}
		return nil, apperr.NotFound("product")
	}
}

func (f *fx) withServer(sv *domain.Server) {
	f.servers.GetServerByIDFn = func(_ context.Context, id int64) (*domain.Server, error) {
		if sv != nil && sv.ID == id {
			cp := *sv
			return &cp, nil
		}
		return nil, apperr.NotFound("server")
	}
}

func assertCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, code, ae.Code)
}

// ProvisionCreate

func TestProvisionCreateHappyPath(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServicePending)
	svc.ServerID = nil
	f.service = svc
	f.withProduct(cpanelProduct())
	server := cpanelServer()
	server.IPAddress = "203.0.113.10"
	f.withServer(server)
	f.servers.PickServerFn = func(_ context.Context, groupID int64) (*domain.Server, error) {
		assert.Equal(t, int64(9), groupID)
		return server, nil
	}

	var created ports.CreateAccountParams
	var gotCfg ports.ServerConfig
	f.cpanel.CreateFn = func(_ context.Context, cfg ports.ServerConfig, a ports.CreateAccountParams) (*ports.AccountResult, error) {
		gotCfg, created = cfg, a
		return &ports.AccountResult{Username: "example1", IP: "10.0.0.5"}, nil
	}

	require.NoError(t, f.svc.ProvisionCreate(context.Background(), 42))

	// Decrypted config (MockEncryptor passes through).
	assert.Equal(t, "srv-pass", gotCfg.Password)
	assert.Equal(t, "srv-token", gotCfg.APIToken)
	// Account params.
	assert.Equal(t, "example1", created.Username)
	assert.Equal(t, "example.com", created.Domain)
	assert.Equal(t, "secret-pw", created.Password)
	assert.Equal(t, "basic", created.Package)
	assert.Equal(t, "client@example.com", created.Email)
	assert.Equal(t, "203.0.113.10", created.IP)

	require.NotNil(t, f.service)
	assert.Equal(t, domain.ServiceActive, f.service.Status)
	assert.Equal(t, ptr(int64(3)), f.service.ServerID)
	require.NotNil(t, f.service.RegistrationDate)
	assert.Equal(t, date(2026, 7, 1), *f.service.RegistrationDate)
	assert.Contains(t, string(f.service.PanelMeta), "10.0.0.5")
	assert.Equal(t, []string{"service_activated"}, f.sent)
	require.NotEmpty(t, f.audit.Entries)
	assert.Equal(t, "service.provision_create", f.audit.Entries[0].Action)
}

func TestProvisionCreateIdempotentWhenNotPending(t *testing.T) {
	for _, status := range []domain.ServiceStatus{
		domain.ServiceActive, domain.ServiceSuspended,
		domain.ServiceTerminated, domain.ServiceCancelled,
	} {
		f := newFixture()
		f.service = baseService(status)
		f.cpanel.CreateFn = func(context.Context, ports.ServerConfig, ports.CreateAccountParams) (*ports.AccountResult, error) {
			t.Fatalf("adapter must not be called for status %s", status)
			return nil, nil
		}
		assert.NoError(t, f.svc.ProvisionCreate(context.Background(), 42), status)
		assert.Empty(t, f.updated, status)
	}
}

func TestProvisionCreateNoServerGroup(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServicePending)
	svc.ServerID = nil
	f.service = svc
	p := cpanelProduct()
	p.ServerGroupID = nil
	f.withProduct(p)

	assertCode(t, f.svc.ProvisionCreate(context.Background(), 42), apperr.CodeConflict)
}

func TestProvisionCreateAdapterFailureStaysPending(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.CreateFn = func(context.Context, ports.ServerConfig, ports.CreateAccountParams) (*ports.AccountResult, error) {
		return nil, apperr.New(apperr.CodeExternal, "whm down")
	}

	assertCode(t, f.svc.ProvisionCreate(context.Background(), 42), apperr.CodeExternal)
	assert.Equal(t, domain.ServicePending, f.service.Status)
	assert.Empty(t, f.sent)
}

// TestProvisionCreateConflictSameDomainIsIdempotent covers a retried job whose
// prior attempt created the panel account but crashed/failed before this
// service row was persisted as active: Create's CONFLICT is reconciled via
// AccountInfo instead of failing forever with an unchanged username.
func TestProvisionCreateConflictSameDomainIsIdempotent(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.CreateFn = func(context.Context, ports.ServerConfig, ports.CreateAccountParams) (*ports.AccountResult, error) {
		return nil, apperr.Conflict(`whm createacct: This system already has an account named "example1"`)
	}
	f.cpanel.AccountInfoFn = func(_ context.Context, _ ports.ServerConfig, username string) (*ports.AccountInfo, error) {
		assert.Equal(t, "example1", username)
		return &ports.AccountInfo{Username: "example1", Domain: "example.com"}, nil
	}

	require.NoError(t, f.svc.ProvisionCreate(context.Background(), 42))

	require.NotNil(t, f.service)
	assert.Equal(t, domain.ServiceActive, f.service.Status)
	assert.Equal(t, []string{"service_activated"}, f.sent)
}

// TestProvisionCreateConflictDifferentDomainDisambiguates covers a genuine
// username collision with an unrelated account (e.g. two services whose
// domains share the same UsernameFromDomain label): the attempt still fails
// so asynq retries, but the service is given a distinct candidate username
// instead of proposing the same taken name forever.
func TestProvisionCreateConflictDifferentDomainDisambiguates(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.CreateFn = func(context.Context, ports.ServerConfig, ports.CreateAccountParams) (*ports.AccountResult, error) {
		return nil, apperr.Conflict(`whm createacct: This system already has an account named "example1"`)
	}
	f.cpanel.AccountInfoFn = func(_ context.Context, _ ports.ServerConfig, username string) (*ports.AccountInfo, error) {
		return &ports.AccountInfo{Username: "example1", Domain: "someone-else.com"}, nil
	}

	assertCode(t, f.svc.ProvisionCreate(context.Background(), 42), apperr.CodeConflict)
	assert.Equal(t, domain.ServicePending, f.service.Status)
	assert.Equal(t, domain.DisambiguateUsername("example1", 42), f.service.Username,
		"should propose a distinct candidate, not retry the same taken name")
	assert.Empty(t, f.sent)
}

// TestProvisionCreateRetryAfterDisambiguationSucceeds covers the next asynq
// attempt following the above: with the disambiguated username already
// persisted, Create succeeds and the service activates normally.
func TestProvisionCreateRetryAfterDisambiguationSucceeds(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServicePending)
	svc.Username = "exampl42" // as left behind by a prior disambiguated attempt
	f.service = svc
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())

	var created ports.CreateAccountParams
	f.cpanel.CreateFn = func(_ context.Context, _ ports.ServerConfig, a ports.CreateAccountParams) (*ports.AccountResult, error) {
		created = a
		return &ports.AccountResult{Username: a.Username}, nil
	}

	require.NoError(t, f.svc.ProvisionCreate(context.Background(), 42))

	assert.Equal(t, "exampl42", created.Username)
	assert.Equal(t, domain.ServiceActive, f.service.Status)
	assert.Equal(t, []string{"service_activated"}, f.sent)
}

func TestProvisionCreateModuleNoneActivatesWithoutAdapter(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServicePending)
	svc.ServerID = nil
	f.service = svc
	f.withProduct(&domain.Product{ID: 5, Name: "Custom Work", Module: domain.ModuleNone,
		WelcomeEmailTemplate: "custom_welcome"})

	require.NoError(t, f.svc.ProvisionCreate(context.Background(), 42))
	assert.Equal(t, domain.ServiceActive, f.service.Status)
	assert.Equal(t, []string{"custom_welcome"}, f.sent) // product welcome template wins
}

func TestProvisionCreateNotifyFailureIgnored(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.notify.SendTemplateFn = func(context.Context, int64, string, map[string]any) error {
		return errors.New("smtp down")
	}
	require.NoError(t, f.svc.ProvisionCreate(context.Background(), 42))
	assert.Equal(t, domain.ServiceActive, f.service.Status)
}

func TestProvisionCreateUnknownModuleNotWired(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.withProduct(&domain.Product{ID: 5, Module: domain.ModuleDirectAdmin, ServerGroupID: ptr(int64(9))})
	assertCode(t, f.svc.ProvisionCreate(context.Background(), 42), apperr.CodeInternal)
}

// Suspend / Unsuspend / Terminate

func TestProvisionSuspendHappyPath(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	var gotReason string
	f.cpanel.SuspendFn = func(_ context.Context, _ ports.ServerConfig, username, reason string) error {
		assert.Equal(t, "example1", username)
		gotReason = reason
		return nil
	}

	require.NoError(t, f.svc.ProvisionSuspend(context.Background(), 42, "Overdue on payment"))
	assert.Equal(t, "Overdue on payment", gotReason)
	assert.Equal(t, domain.ServiceSuspended, f.service.Status)
	assert.Equal(t, "Overdue on payment", f.service.SuspendReason)
	assert.Contains(t, string(f.service.PanelMeta), "suspended_at")
	assert.Equal(t, []string{"service_suspended"}, f.sent)
}

func TestProvisionSuspendIdempotent(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceSuspended)
	require.NoError(t, f.svc.ProvisionSuspend(context.Background(), 42, "x"))
	assert.Empty(t, f.updated)
}

func TestProvisionSuspendInvalidTransition(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	assertCode(t, f.svc.ProvisionSuspend(context.Background(), 42, "x"), apperr.CodeConflict)
}

func TestProvisionSuspendAdapterError(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.SuspendFn = func(context.Context, ports.ServerConfig, string, string) error {
		return apperr.New(apperr.CodeExternal, "boom")
	}
	assertCode(t, f.svc.ProvisionSuspend(context.Background(), 42, "x"), apperr.CodeExternal)
	assert.Equal(t, domain.ServiceActive, f.service.Status)
}

func TestProvisionUnsuspendHappyPath(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceSuspended)
	svc.SuspendReason = "Overdue on payment"
	svc.PanelMeta = json.RawMessage(`{"suspended_at":"2026-06-20T00:00:00Z"}`)
	f.service = svc
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	called := false
	f.cpanel.UnsuspendFn = func(_ context.Context, _ ports.ServerConfig, username string) error {
		called = true
		return nil
	}

	require.NoError(t, f.svc.ProvisionUnsuspend(context.Background(), 42))
	assert.True(t, called)
	assert.Equal(t, domain.ServiceActive, f.service.Status)
	assert.Empty(t, f.service.SuspendReason)
	assert.NotContains(t, string(f.service.PanelMeta), "suspended_at")
	assert.Equal(t, []string{"service_unsuspended"}, f.sent)
}

func TestProvisionUnsuspendIdempotent(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	require.NoError(t, f.svc.ProvisionUnsuspend(context.Background(), 42))
	assert.Empty(t, f.updated)
}

func TestProvisionUnsuspendInvalidFromTerminated(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceTerminated)
	assertCode(t, f.svc.ProvisionUnsuspend(context.Background(), 42), apperr.CodeConflict)
}

func TestProvisionTerminateHappyPath(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	called := false
	f.cpanel.TerminateFn = func(context.Context, ports.ServerConfig, string) error {
		called = true
		return nil
	}

	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
	assert.True(t, called)
	assert.Equal(t, domain.ServiceTerminated, f.service.Status)
	require.NotNil(t, f.service.TerminatedAt)
	assert.Equal(t, testNow, *f.service.TerminatedAt)
	assert.Equal(t, []string{"service_terminated"}, f.sent)
}

func TestProvisionTerminateIdempotent(t *testing.T) {
	for _, status := range []domain.ServiceStatus{domain.ServiceTerminated, domain.ServiceCancelled} {
		f := newFixture()
		f.service = baseService(status)
		require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
		assert.Empty(t, f.updated)
	}
}

func TestProvisionTerminatePendingBecomesCancelled(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	f.cpanel.TerminateFn = func(context.Context, ports.ServerConfig, string) error {
		t.Fatal("adapter must not be called for pending service")
		return nil
	}
	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
	assert.Equal(t, domain.ServiceCancelled, f.service.Status)
	assert.NotNil(t, f.service.TerminatedAt)
}

// TestProvisionTerminateResolvesPendingCancellation is the other half of the
// duplicate-immediate-cancellation fix: once the worker actually completes
// termination, the service's pending cancellation request (created by
// CancelService, still pending up to this point) must flip to
// auto_processed - only then does the "one pending request per service"
// guard stop blocking a fresh request for this service.
func TestProvisionTerminateResolvesPendingCancellation(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())

	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	require.NoError(t, err)
	var reqID int64
	for id, cr := range f.crStore {
		reqID = id
		require.Equal(t, domain.CancellationPending, cr.Status)
	}

	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))

	got := f.crStore[reqID]
	assert.Equal(t, domain.CancellationAutoProcessed, got.Status)
	require.NotNil(t, got.DecidedAt)
	assert.Nil(t, got.DecidedBy, "system-resolved, not an admin decision")
}

// TestProvisionTerminatePendingBecomesCancelledResolvesPendingCancellation
// covers the OTHER ProvisionTerminate exit path (a still-pending, never-
// provisioned service) - an admin directly terminating a service that also
// happens to have its own pending immediate request must resolve that
// request too, not leave it dangling forever.
func TestProvisionTerminatePendingBecomesCancelledResolvesPendingCancellation(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)

	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	require.NoError(t, err)
	var reqID int64
	for id := range f.crStore {
		reqID = id
	}

	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))

	assert.Equal(t, domain.CancellationAutoProcessed, f.crStore[reqID].Status)
}

func TestProvisionTerminateNoPendingCancellationIsANoop(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())

	require.NoError(t, f.svc.ProvisionTerminate(context.Background(), 42))
	assert.Empty(t, f.crStore, "no cancellation request existed - nothing to resolve, no panic")
}

func TestProvisionChangePackagePushesCurrentProduct(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	var gotPkg string
	f.cpanel.ChangePackageFn = func(_ context.Context, _ ports.ServerConfig, _, pkg string) error {
		gotPkg = pkg
		return nil
	}
	require.NoError(t, f.svc.ProvisionChangePackage(context.Background(), 42))
	assert.Equal(t, "basic", gotPkg)
}

// List/Get views (name enrichment)

func TestListServicesAttachesProductAndServerNames(t *testing.T) {
	f := newFixture()
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.store.ListByClientFn = func(_ context.Context, clientID int64, _ ports.ListParams) ([]domain.Service, int64, error) {
		assert.EqualValues(t, 7, clientID)
		return []domain.Service{*baseService(domain.ServiceActive)}, 1, nil
	}

	views, total, err := f.svc.ListServices(context.Background(), 7, ports.ListParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, views, 1)
	assert.Equal(t, "Hosting Basic", views[0].ProductName)
	assert.Equal(t, "srv1", views[0].ServerName)
	assert.Equal(t, "srv1.host.id", views[0].ServerHostname)
}

func TestGetServiceViewNamesBestEffort(t *testing.T) {
	// A missing product/server must not fail the read - names stay empty and
	// the UI falls back to "#<id>".
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(nil)
	f.withServer(nil)

	view, err := f.svc.GetService(context.Background(), 7, 42)
	require.NoError(t, err)
	assert.EqualValues(t, 42, view.ID)
	assert.Empty(t, view.ProductName)
	assert.Empty(t, view.ServerHostname)

	// With both rows present the names are attached.
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	view, err = f.svc.GetService(context.Background(), 7, 42)
	require.NoError(t, err)
	assert.Equal(t, "Hosting Basic", view.ProductName)
	assert.Equal(t, "srv1.host.id", view.ServerHostname)
}

// Change password / SSO

func TestChangePasswordHappyPath(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.crypt.EncryptFn = func(p string) (string, error) { return "enc:" + p, nil }
	var pushed string
	f.cpanel.ChangePasswordFn = func(_ context.Context, _ ports.ServerConfig, _, password string) error {
		pushed = password
		return nil
	}

	require.NoError(t, f.svc.ChangePassword(context.Background(), 10, 7, 42, "Sup3rStrongPass"))
	assert.Equal(t, "Sup3rStrongPass", pushed)
	assert.Equal(t, "enc:Sup3rStrongPass", f.service.PasswordEnc)
	require.NotEmpty(t, f.audit.Entries)
	assert.Equal(t, "service.change_password", f.audit.Entries[0].Action)
}

func TestChangePasswordWeak(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	assertCode(t, f.svc.ChangePassword(context.Background(), 10, 7, 42, "short"), apperr.CodeValidation)
	assertCode(t, f.svc.ChangePassword(context.Background(), 10, 7, 42, "alllowercaseonly"), apperr.CodeValidation)
}

func TestChangePasswordOwnershipAndStatus(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	// Foreign client -> NOT_FOUND, never FORBIDDEN.
	assertCode(t, f.svc.ChangePassword(context.Background(), 10, 999, 42, "Sup3rStrongPass"), apperr.CodeNotFound)

	f.service = baseService(domain.ServiceSuspended)
	assertCode(t, f.svc.ChangePassword(context.Background(), 10, 7, 42, "Sup3rStrongPass"), apperr.CodeConflict)
}

func TestChangePasswordNoPanel(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.ServerID = nil
	f.service = svc
	f.withProduct(&domain.Product{ID: 5, Module: domain.ModuleNone})
	assertCode(t, f.svc.ChangePassword(context.Background(), 10, 7, 42, "Sup3rStrongPass"), apperr.CodeConflict)
}

func TestChangePasswordAdapterErrorDoesNotStore(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.ChangePasswordFn = func(context.Context, ports.ServerConfig, string, string) error {
		return apperr.New(apperr.CodeExternal, "boom")
	}
	assertCode(t, f.svc.ChangePassword(context.Background(), 10, 7, 42, "Sup3rStrongPass"), apperr.CodeExternal)
	assert.Equal(t, "secret-pw", f.service.PasswordEnc)
}

func TestSSO(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	f.cpanel.SSOURLFn = func(context.Context, ports.ServerConfig, string) (string, error) {
		return "https://srv1.host.id:2087/session", nil
	}

	url, err := f.svc.SSO(context.Background(), 10, 7, 42)
	require.NoError(t, err)
	assert.Equal(t, "https://srv1.host.id:2087/session", url)

	require.Len(t, f.audit.Entries, 1)
	assert.Equal(t, "service.sso", f.audit.Entries[0].Action)
	assert.EqualValues(t, 10, f.audit.Entries[0].ActorUserID)

	// Not active -> CONFLICT.
	f.service = baseService(domain.ServiceSuspended)
	_, err = f.svc.SSO(context.Background(), 10, 7, 42)
	assertCode(t, err, apperr.CodeConflict)

	// Foreign client -> NOT_FOUND.
	f.service = baseService(domain.ServiceActive)
	_, err = f.svc.SSO(context.Background(), 10, 8, 42)
	assertCode(t, err, apperr.CodeNotFound)
}

// RenewService / ApplyUpgrade (ports.ServiceRenewer)

func TestRenewServiceAdvancesFromCurrentDueDate(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive) // next due 2026-07-16, monthly
	require.NoError(t, f.svc.RenewService(context.Background(), 42))
	require.NotNil(t, f.service.NextDueDate)
	assert.Equal(t, date(2026, 8, 16), *f.service.NextDueDate)
	assert.Empty(t, f.queue.Tasks) // active service: no unsuspend job
	assert.Equal(t, []string{"service_renewed"}, f.sent)
}

func TestRenewServiceNilDueDateStartsToday(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.NextDueDate = nil
	f.service = svc
	require.NoError(t, f.svc.RenewService(context.Background(), 42))
	assert.Equal(t, date(2026, 8, 1), *f.service.NextDueDate)
	assert.Equal(t, []string{"service_renewed"}, f.sent)
}

func TestRenewServiceSuspendedEnqueuesUnsuspend(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceSuspended)
	require.NoError(t, f.svc.RenewService(context.Background(), 42))
	require.Len(t, f.queue.Tasks, 1)
	assert.Equal(t, jobs.TypeProvisionUnsuspend, f.queue.Tasks[0].TaskType)
	assert.Equal(t, jobs.ProvisionUnsuspendPayload{ServiceID: 42}, f.queue.Tasks[0].Payload)
	assert.Equal(t, []string{"service_renewed"}, f.sent)
}

func TestRenewServiceNotifiesWithServiceNameAndNextDueDate(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive) // next due 2026-07-16, monthly
	var gotKey string
	var gotData map[string]any
	var gotUserID int64
	f.notify.SendTemplateFn = func(_ context.Context, userID int64, key string, data map[string]any) error {
		gotUserID, gotKey, gotData = userID, key, data
		f.sent = append(f.sent, key)
		return nil
	}
	require.NoError(t, f.svc.RenewService(context.Background(), 42))
	assert.Equal(t, "service_renewed", gotKey)
	assert.EqualValues(t, 907, gotUserID) // clients.GetByID stub: UserID = 900 + ClientID(7)
	assert.Equal(t, "example.com", gotData["ServiceName"])
	assert.Equal(t, "example.com", gotData["Domain"])
	assert.Equal(t, "2026-08-16", gotData["NextDueDate"])
}

func TestApplyUpgradeNoPendingIsNoop(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	require.NoError(t, f.svc.ApplyUpgrade(context.Background(), 42))
	assert.Empty(t, f.updated)
	assert.Empty(t, f.queue.Tasks)
}

func TestApplyUpgradeAppliesAndEnqueuesChangePackage(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	up := domain.ServiceUpgrade{ProductID: 6, Cycle: domain.CycleAnnually, RecurringAmount: 1_000_000, InvoiceID: 77}
	raw, _ := json.Marshal(up)
	svc.PendingUpgrade = raw
	f.service = svc

	require.NoError(t, f.svc.ApplyUpgrade(context.Background(), 42))
	assert.Equal(t, int64(6), f.service.ProductID)
	assert.Equal(t, domain.CycleAnnually, f.service.BillingCycle)
	assert.Equal(t, int64(1_000_000), f.service.RecurringAmount)
	assert.Empty(t, f.service.PendingUpgrade)
	require.Len(t, f.queue.Tasks, 1)
	assert.Equal(t, jobs.TypeProvisionChangePackage, f.queue.Tasks[0].TaskType)
}

func TestApplyUpgradeCorruptJSON(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.PendingUpgrade = json.RawMessage(`{not json`)
	f.service = svc
	assertCode(t, f.svc.ApplyUpgrade(context.Background(), 42), apperr.CodeInternal)
}

// UpgradeService (client initiation, prorate)

func (f *fx) withUpgradeProducts(newPrice int64) {
	current := cpanelProduct()
	target := &domain.Product{ID: 6, Name: "Hosting Pro", Module: domain.ModuleCpanel,
		PackageName: "pro", ServerGroupID: ptr(int64(9))}
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
			return &domain.ProductPricing{ProductID: 6, Cycle: cycle, Price: newPrice}, nil
		}
		return nil, apperr.NotFound("pricing")
	}
}

func TestUpgradeServiceCreatesProratedInvoice(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive) // 100k monthly, due 2026-07-16, now 2026-07-01
	f.withUpgradeProducts(200_000)

	var gotInput ports.CreateInvoiceInput
	f.billing.CreateInvoiceFn = func(_ context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
		gotInput = in
		return &domain.Invoice{ID: 77, ClientID: in.ClientID, Status: domain.InvoiceUnpaid}, nil
	}

	res, err := f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	require.NoError(t, err)

	// 15 days left of a 31-day July cycle: unused = round(100000*15/31) = 48387,
	// charge = round(200000*15/31) = 96774, diff = 48387.
	assert.False(t, res.Applied)
	assert.Equal(t, int64(48_387), res.ProratedDiff)
	require.NotNil(t, res.Invoice)
	assert.Equal(t, int64(77), res.Invoice.ID)

	require.Len(t, gotInput.Items, 1)
	assert.Equal(t, int64(48_387), gotInput.Items[0].Amount)
	assert.Equal(t, domain.RelatedServiceUpgrade, gotInput.Items[0].RelatedType)
	assert.Equal(t, int64(42), gotInput.Items[0].RelatedID)
	assert.True(t, gotInput.Items[0].Taxed)
	assert.Equal(t, testNow.AddDate(0, 0, 3), gotInput.DueDate) // billing.invoice_due_days default 3

	var up domain.ServiceUpgrade
	require.NoError(t, json.Unmarshal(f.service.PendingUpgrade, &up))
	assert.Equal(t, domain.ServiceUpgrade{ProductID: 6, Cycle: domain.CycleMonthly,
		RecurringAmount: 200_000, InvoiceID: 77}, up)
	assert.Empty(t, f.credit.calls)
}

func TestUpgradeServiceDowngradeAppliesImmediatelyWithCredit(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withUpgradeProducts(50_000)

	res, err := f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	require.NoError(t, err)

	// charge = round(50000*15/31) = 24194, unused = 48387 -> diff = -24193.
	assert.True(t, res.Applied)
	assert.Equal(t, int64(-24_193), res.ProratedDiff)
	assert.Equal(t, int64(24_193), res.CreditIssued)
	assert.Nil(t, res.Invoice)

	assert.Equal(t, int64(6), f.service.ProductID)
	assert.Equal(t, int64(50_000), f.service.RecurringAmount)
	require.Len(t, f.credit.calls, 1)
	assert.Equal(t, int64(7), f.credit.calls[0].ClientID)
	assert.Equal(t, int64(24_193), f.credit.calls[0].Delta)

	require.Len(t, f.queue.Tasks, 1)
	assert.Equal(t, jobs.TypeProvisionChangePackage, f.queue.Tasks[0].TaskType)
}

func TestUpgradeServiceGuards(t *testing.T) {
	// Not active.
	f := newFixture()
	f.service = baseService(domain.ServiceSuspended)
	f.withUpgradeProducts(200_000)
	_, err := f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeConflict)

	// Pending upgrade already exists.
	f = newFixture()
	svc := baseService(domain.ServiceActive)
	svc.PendingUpgrade = json.RawMessage(`{"product_id":6}`)
	f.service = svc
	f.withUpgradeProducts(200_000)
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeConflict)

	// Same product + cycle.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withUpgradeProducts(200_000)
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 5, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeValidation)

	// Different module.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		if id == 5 {
			return cpanelProduct(), nil
		}
		return &domain.Product{ID: id, Module: domain.ModuleDirectAdmin}, nil
	}
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeValidation)

	// Unpriced cycle.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withUpgradeProducts(200_000)
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleAnnually})
	assertCode(t, err, apperr.CodeValidation)

	// Specs passed for a non-configurable (flat) target rejected.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withUpgradeProducts(200_000)
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly,
			Specs: []UpgradeSpecInput{{Key: "disk", Qty: 10}}})
	assertCode(t, err, apperr.CodeValidation)

	// one_time cycle rejected.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleOneTime})
	assertCode(t, err, apperr.CodeValidation)

	// Missing product id fails DTO validation.
	f = newFixture()
	_, err = f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeValidation)
}

func TestUpgradeServiceBillingErrorPropagates(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withUpgradeProducts(200_000)
	f.billing.CreateInvoiceFn = func(context.Context, ports.CreateInvoiceInput) (*domain.Invoice, error) {
		return nil, apperr.Internal(errors.New("db down"))
	}
	_, err := f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeInternal)
	assert.Empty(t, f.service.PendingUpgrade)
}

// TestUpgradeServiceRejectsNilNextDueDate guards against a silent free plan
// change: without a next_due_date there's no cycle end to prorate against,
// and the old fallback-to-now behavior zeroed out both unused and charge,
// applying the new plan for free with no invoice and no future correction.
func TestUpgradeServiceRejectsNilNextDueDate(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.NextDueDate = nil
	f.service = svc
	f.withUpgradeProducts(200_000)

	_, err := f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	assertCode(t, err, apperr.CodeConflict)
}

// TestUpgradeServiceZeroRemainingDaysAppliesFreeThisCycle documents the
// (correct) behavior when next_due_date has already arrived: with zero days
// left in the current cycle there is nothing left to prorate, so the plan
// change applies immediately at zero cost - the new price takes effect
// starting the very next (imminent) renewal invoice instead.
func TestUpgradeServiceZeroRemainingDaysAppliesFreeThisCycle(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.NextDueDate = ptr(testNow) // due right now: zero days remaining
	f.service = svc
	f.withUpgradeProducts(200_000)

	res, err := f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	require.NoError(t, err)

	assert.True(t, res.Applied)
	assert.Zero(t, res.ProratedDiff)
	assert.Zero(t, res.CreditIssued)
	assert.Nil(t, res.Invoice)
	assert.Empty(t, f.credit.calls, "zero diff must not issue a credit")
	assert.Equal(t, int64(6), f.service.ProductID)
	assert.Equal(t, int64(200_000), f.service.RecurringAmount)
	require.Len(t, f.queue.Tasks, 1)
	assert.Equal(t, jobs.TypeProvisionChangePackage, f.queue.Tasks[0].TaskType)
}

// TestUpgradeServiceBeyondCycleEndChargesFullPrice covers the "beyond cycle
// end" cap in domain.Prorate at the UpgradeService integration level: when
// next_due_date is a full cycle or more away, both the unused credit and the
// new charge are the FULL cycle amounts (not a day fraction), so the
// prorated diff is exactly the full price difference.
func TestUpgradeServiceBeyondCycleEndChargesFullPrice(t *testing.T) {
	f := newFixture()
	svc := baseService(domain.ServiceActive)
	svc.NextDueDate = ptr(date(2026, 9, 1)) // >1 full monthly cycle from testNow (2026-07-01)
	f.service = svc
	f.withUpgradeProducts(200_000)

	var gotInput ports.CreateInvoiceInput
	f.billing.CreateInvoiceFn = func(_ context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
		gotInput = in
		return &domain.Invoice{ID: 88, ClientID: in.ClientID, Status: domain.InvoiceUnpaid}, nil
	}

	res, err := f.svc.UpgradeService(context.Background(), 10, 7, 42,
		UpgradeServiceInput{ProductID: 6, Cycle: domain.CycleMonthly})
	require.NoError(t, err)

	assert.Equal(t, int64(100_000), res.ProratedDiff) // full 200_000 - full 100_000, uncapped by day fraction
	require.Len(t, gotInput.Items, 1)
	assert.Equal(t, int64(100_000), gotInput.Items[0].Amount)
}

// CancelService

func TestCancelServiceImmediate(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)

	renewalID := int64(42)
	f.invoices.ListByClientFn = func(_ context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error) {
		require.Equal(t, int64(7), clientID)
		if p.Status == string(domain.InvoiceUnpaid) {
			return []domain.Invoice{
				{ID: 100, Status: domain.InvoiceUnpaid}, // renewal for this service
				{ID: 101, Status: domain.InvoiceUnpaid}, // unrelated
			}, 2, nil
		}
		return []domain.Invoice{{ID: 102, Status: domain.InvoiceOverdue}}, 1, nil
	}
	f.invoices.GetItemsByInvoiceIDsFn = func(_ context.Context, invoiceIDs []int64) (map[int64][]domain.InvoiceItem, error) {
		out := make(map[int64][]domain.InvoiceItem)
		for _, invoiceID := range invoiceIDs {
			switch invoiceID {
			case 100:
				out[invoiceID] = []domain.InvoiceItem{{InvoiceID: 100, RelatedType: domain.RelatedServiceRenewal, RelatedID: &renewalID}}
			case 102:
				out[invoiceID] = []domain.InvoiceItem{{InvoiceID: 102, RelatedType: domain.RelatedServiceRenewal, RelatedID: &renewalID}}
			default:
				out[invoiceID] = []domain.InvoiceItem{{InvoiceID: invoiceID, RelatedType: domain.RelatedOrderItem}}
			}
		}
		return out, nil
	}
	var cancelled []int64
	f.invoices.UpdateStatusFn = func(_ context.Context, id int64, status domain.InvoiceStatus, paidAt *time.Time) error {
		assert.Equal(t, domain.InvoiceCancelled, status)
		assert.Nil(t, paidAt)
		cancelled = append(cancelled, id)
		return nil
	}

	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int64{100, 102}, cancelled)
	require.Len(t, f.queue.Tasks, 1)
	assert.Equal(t, jobs.TypeProvisionTerminate, f.queue.Tasks[0].TaskType)
	assert.Equal(t, jobs.ProvisionTerminatePayload{ServiceID: 42}, f.queue.Tasks[0].Payload)

	// Immediate mode needs no ADMIN approval, but the request still starts
	// pending until ProvisionTerminate actually completes (see
	// resolvePendingCancellation) - not auto_processed at submission time,
	// so a duplicate submission is blocked while the job sits in the queue.
	require.Len(t, f.crStore, 1)
	for _, cr := range f.crStore {
		assert.Equal(t, domain.CancellationPending, cr.Status)
		assert.Equal(t, CancelModeImmediate, cr.Mode)
		assert.Nil(t, cr.DecidedAt)
	}
}

func TestCancelServiceEndOfTermCreatesPendingRequest(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)

	svc, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	require.NoError(t, err)
	// The flag is only set once an admin accepts the request - not here.
	assert.False(t, metaBool(svc.PanelMeta, metaCancelAtPeriodEnd))
	assert.Empty(t, f.queue.Tasks)

	require.Len(t, f.crStore, 1)
	for _, cr := range f.crStore {
		assert.Equal(t, domain.CancellationPending, cr.Status)
		assert.Equal(t, CancelModeEndOfTerm, cr.Mode)
		assert.Nil(t, cr.DecidedAt)
	}
}

func TestCancelServiceRejectsDuplicatePending(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)

	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	require.NoError(t, err)

	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	assertCode(t, err, apperr.CodeConflict)
	require.Len(t, f.crStore, 1, "the duplicate request must not be persisted")
}

// TestCancelServiceRejectsDuplicateImmediate is the regression test for the
// gap the user found: submitting "immediate" twice while the first
// termination job still sits unprocessed in the queue (service status still
// active) used to be allowed, because the first request was recorded
// auto_processed at submission time rather than pending - so the "one
// pending request per service" guard never saw it. Immediate now starts
// pending too (see CancelService), so a second submission - of EITHER mode -
// while the job hasn't run yet must conflict, same as end_of_term.
func TestCancelServiceRejectsDuplicateImmediate(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)

	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	require.NoError(t, err)
	require.Len(t, f.queue.Tasks, 1, "first submission enqueues its termination job")

	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	assertCode(t, err, apperr.CodeConflict)
	assert.Len(t, f.queue.Tasks, 1, "a rejected duplicate must not enqueue a second termination job")
	require.Len(t, f.crStore, 1, "the duplicate request must not be persisted")

	// end_of_term is blocked too, not just a second immediate.
	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	assertCode(t, err, apperr.CodeConflict)
	require.Len(t, f.crStore, 1)
}

func TestCancelServiceGuards(t *testing.T) {
	// Invalid mode -> validation.
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: "later"})
	assertCode(t, err, apperr.CodeValidation)

	// Terminated -> conflict.
	f = newFixture()
	f.service = baseService(domain.ServiceTerminated)
	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	assertCode(t, err, apperr.CodeConflict)

	// Pending + end_of_term -> conflict; pending + immediate -> allowed.
	f = newFixture()
	f.service = baseService(domain.ServicePending)
	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	assertCode(t, err, apperr.CodeConflict)
	_, err = f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	require.NoError(t, err)

	// Foreign client -> NOT_FOUND.
	f = newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err = f.svc.CancelService(context.Background(), 10, 99, 42, CancelServiceInput{Mode: CancelModeImmediate})
	assertCode(t, err, apperr.CodeNotFound)
}

func TestAcceptCancellationRequest(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	require.NoError(t, err)
	var reqID int64
	for id := range f.crStore {
		reqID = id
	}

	require.NoError(t, f.svc.AcceptCancellationRequest(context.Background(), 1, reqID))

	assert.True(t, metaBool(f.service.PanelMeta, metaCancelAtPeriodEnd))
	got := f.crStore[reqID]
	assert.Equal(t, domain.CancellationAccepted, got.Status)
	require.NotNil(t, got.DecidedAt)
	require.NotNil(t, got.DecidedBy)
	assert.Equal(t, int64(1), *got.DecidedBy)

	// Already decided -> conflict.
	err = f.svc.AcceptCancellationRequest(context.Background(), 1, reqID)
	assertCode(t, err, apperr.CodeConflict)

	// Unknown id -> not found.
	err = f.svc.AcceptCancellationRequest(context.Background(), 1, -1)
	assertCode(t, err, apperr.CodeNotFound)
}

// TestAcceptRejectCancellationRequestRejectImmediateMode: an immediate
// request's pending row is only ever "in flight, awaiting the worker" - not
// awaiting an admin decision - so Accept/Reject must refuse it even though
// its status is technically pending (same status end_of_term uses).
func TestAcceptRejectCancellationRequestRejectImmediateMode(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeImmediate})
	require.NoError(t, err)
	var reqID int64
	for id := range f.crStore {
		reqID = id
	}

	err = f.svc.AcceptCancellationRequest(context.Background(), 1, reqID)
	assertCode(t, err, apperr.CodeConflict)
	assert.Equal(t, domain.CancellationPending, f.crStore[reqID].Status, "a refused accept must not touch the request")

	err = f.svc.RejectCancellationRequest(context.Background(), 1, reqID)
	assertCode(t, err, apperr.CodeConflict)
	assert.Equal(t, domain.CancellationPending, f.crStore[reqID].Status)
}

func TestAcceptCancellationRequestIneligibleService(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	require.NoError(t, err)
	var reqID int64
	for id := range f.crStore {
		reqID = id
	}

	// Service moved to terminated in the meantime (e.g. an admin terminated
	// it directly) - accepting a now-stale request must conflict, not
	// silently flag a dead service.
	terminated := *f.service
	terminated.Status = domain.ServiceTerminated
	f.service = &terminated

	err = f.svc.AcceptCancellationRequest(context.Background(), 1, reqID)
	assertCode(t, err, apperr.CodeConflict)
	assert.Equal(t, domain.CancellationPending, f.crStore[reqID].Status, "a conflicted accept must not mark the request decided")
}

func TestRejectCancellationRequest(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	require.NoError(t, err)
	var reqID int64
	for id := range f.crStore {
		reqID = id
	}

	require.NoError(t, f.svc.RejectCancellationRequest(context.Background(), 1, reqID))

	assert.False(t, metaBool(f.service.PanelMeta, metaCancelAtPeriodEnd), "rejecting must never touch the service")
	got := f.crStore[reqID]
	assert.Equal(t, domain.CancellationRejected, got.Status)
	require.NotNil(t, got.DecidedBy)
	assert.Equal(t, int64(1), *got.DecidedBy)

	// Already decided -> conflict.
	err = f.svc.RejectCancellationRequest(context.Background(), 1, reqID)
	assertCode(t, err, apperr.CodeConflict)
}

func TestListCancellationRequests(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.CancelService(context.Background(), 10, 7, 42, CancelServiceInput{Mode: CancelModeEndOfTerm})
	require.NoError(t, err)

	views, total, err := f.svc.ListCancellationRequests(context.Background(), ports.ListParams{Status: "pending"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, views, 1)
	assert.Equal(t, f.service.Domain, views[0].ServiceDomain)
	assert.Equal(t, "Test", views[0].ClientName)

	views, total, err = f.svc.ListCancellationRequests(context.Background(), ports.ListParams{Status: "rejected"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, views)
}

// AutoSuspend / AutoTerminate

func TestAutoSuspend(t *testing.T) {
	f := newFixture()
	var gotBefore time.Time
	f.store.ListOverdueSuspendableFn = func(_ context.Context, before time.Time) ([]domain.Service, error) {
		gotBefore = before
		return []domain.Service{{ID: 1}, {ID: 2}}, nil
	}

	n, err := f.svc.AutoSuspend(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, date(2026, 6, 24), gotBefore) // today - default 7 days

	require.Len(t, f.queue.Tasks, 2)
	assert.Equal(t, jobs.TypeProvisionSuspend, f.queue.Tasks[0].TaskType)
	assert.Equal(t, jobs.ProvisionSuspendPayload{ServiceID: 1, Reason: "Overdue on payment"},
		f.queue.Tasks[0].Payload)
}

func TestAutoSuspendEnqueueError(t *testing.T) {
	f := newFixture()
	f.store.ListOverdueSuspendableFn = func(context.Context, time.Time) ([]domain.Service, error) {
		return []domain.Service{{ID: 1}, {ID: 2}}, nil
	}
	f.queue.EnqueueFn = func(_ context.Context, taskType string, payload any, _ ...ports.JobOption) error {
		if payload.(jobs.ProvisionSuspendPayload).ServiceID == 2 {
			return errors.New("redis down")
		}
		return nil
	}
	n, err := f.svc.AutoSuspend(context.Background())
	assert.Equal(t, 1, n)
	assertCode(t, err, apperr.CodeInternal)
}

func TestAutoTerminate(t *testing.T) {
	f := newFixture()
	var gotSuspendedBefore, gotDueBefore time.Time
	f.store.ListTerminatableFn = func(_ context.Context, suspendedBefore, dueBefore time.Time) ([]domain.Service, error) {
		gotSuspendedBefore, gotDueBefore = suspendedBefore, dueBefore
		return []domain.Service{{ID: 9}}, nil
	}

	n, err := f.svc.AutoTerminate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, testNow.AddDate(0, 0, -21), gotSuspendedBefore)
	assert.Equal(t, date(2026, 7, 1), gotDueBefore)
	require.Len(t, f.queue.Tasks, 1)
	assert.Equal(t, jobs.TypeProvisionTerminate, f.queue.Tasks[0].TaskType)
}

// NotifyProvisionFailure

func TestNotifyProvisionFailure(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)
	var subject, detail string
	f.notify.AlertAdminFn = func(_ context.Context, s, m string) error {
		subject, detail = s, m
		return nil
	}

	require.NoError(t, f.svc.NotifyProvisionFailure(context.Background(), 42, jobs.TypeProvisionCreate, "whm timeout"))
	assert.Contains(t, subject, "provision:create")
	assert.Contains(t, subject, "#42")
	assert.Contains(t, detail, "example.com")
	assert.Contains(t, detail, "whm timeout")

	// Unknown service still alerts.
	f = newFixture()
	f.notify.AlertAdminFn = func(_ context.Context, s, m string) error { detail = m; return nil }
	require.NoError(t, f.svc.NotifyProvisionFailure(context.Background(), 999, jobs.TypeProvisionSuspend, "x"))
	assert.Contains(t, detail, "service_id=999")

	// Alert failure propagates.
	f = newFixture()
	f.notify.AlertAdminFn = func(context.Context, string, string) error { return errors.New("mail down") }
	assertCode(t, f.svc.NotifyProvisionFailure(context.Background(), 999, "t", "x"), apperr.CodeInternal)
}

// Admin actions

func TestAdminActionAsyncEnqueues(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)

	require.NoError(t, f.svc.AdminAction(context.Background(), 10, 42, ActionSuspend, "abuse", true))
	require.Len(t, f.queue.Tasks, 1)
	assert.Equal(t, jobs.TypeProvisionSuspend, f.queue.Tasks[0].TaskType)
	assert.Equal(t, jobs.ProvisionSuspendPayload{ServiceID: 42, Reason: "abuse"}, f.queue.Tasks[0].Payload)
	require.NotEmpty(t, f.audit.Entries)
	assert.Equal(t, "service.enqueue_suspend", f.audit.Entries[0].Action)
	assert.Equal(t, int64(10), f.audit.Entries[0].ActorUserID)
}

func TestAdminActionSyncDispatches(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withProduct(cpanelProduct())
	f.withServer(cpanelServer())
	called := false
	f.cpanel.SuspendFn = func(context.Context, ports.ServerConfig, string, string) error {
		called = true
		return nil
	}

	require.NoError(t, f.svc.AdminAction(context.Background(), 10, 42, ActionSuspend, "abuse", false))
	assert.True(t, called)
	assert.Equal(t, domain.ServiceSuspended, f.service.Status)
}

func TestAdminActionUnknownAndMissing(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	assertCode(t, f.svc.AdminAction(context.Background(), 10, 42, "reboot", "", false), apperr.CodeValidation)
	assertCode(t, f.svc.AdminAction(context.Background(), 10, 42, "reboot", "", true), apperr.CodeValidation)
	assertCode(t, f.svc.AdminAction(context.Background(), 10, 999, ActionSuspend, "", true), apperr.CodeNotFound)
}

func TestAdminChangePackageRebindAndSync(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.withServer(cpanelServer())
	target := &domain.Product{ID: 6, Name: "Pro", Module: domain.ModuleCpanel, PackageName: "pro"}
	f.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		if id == 5 {
			return cpanelProduct(), nil
		}
		if id == 6 {
			return target, nil
		}
		return nil, apperr.NotFound("product")
	}
	var gotPkg string
	f.cpanel.ChangePackageFn = func(_ context.Context, _ ports.ServerConfig, _, pkg string) error {
		gotPkg = pkg
		return nil
	}

	require.NoError(t, f.svc.AdminChangePackage(context.Background(), 10, 42,
		AdminChangePackageInput{ProductID: ptr(int64(6))}))
	assert.Equal(t, int64(6), f.service.ProductID)
	assert.Equal(t, "pro", gotPkg)
}

func TestAdminChangePackageIncompatibleModule(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		if id == 5 {
			return cpanelProduct(), nil
		}
		return &domain.Product{ID: id, Module: domain.ModuleDirectAdmin}, nil
	}
	assertCode(t, f.svc.AdminChangePackage(context.Background(), 10, 42,
		AdminChangePackageInput{ProductID: ptr(int64(6))}), apperr.CodeValidation)
}

func TestAdminChangePackageAsync(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	require.NoError(t, f.svc.AdminChangePackage(context.Background(), 10, 42,
		AdminChangePackageInput{Async: true}))
	require.Len(t, f.queue.Tasks, 1)
	assert.Equal(t, jobs.TypeProvisionChangePackage, f.queue.Tasks[0].TaskType)
}

func TestAdminUpdateServiceNextDueDateAndNotes(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)

	got, err := f.svc.AdminUpdateService(context.Background(), 10, 42,
		AdminUpdateServiceInput{NextDueDate: ptr("2026-09-01"), Notes: ptr("flagged for review")})
	require.NoError(t, err)
	require.NotNil(t, got.NextDueDate)
	assert.True(t, date(2026, 9, 1).Equal(*got.NextDueDate))
	assert.Equal(t, "flagged for review", got.Notes)
	require.Len(t, f.updated, 1)
}

// TestAdminUpdateServiceDomainAndUsername covers the field an admin needs to
// manually correct after a real control panel rejects a generated username
// (e.g. as a reserved system name) - no amount of automatic retry can fix
// that, so the admin must be able to type a replacement directly.
func TestAdminUpdateServiceDomainAndUsername(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServicePending)

	got, err := f.svc.AdminUpdateService(context.Background(), 10, 42,
		AdminUpdateServiceInput{Domain: ptr("corrected.example"), Username: ptr("corrected1")})
	require.NoError(t, err)
	assert.Equal(t, "corrected.example", got.Domain)
	assert.Equal(t, "corrected1", got.Username)
}

func TestAdminUpdateServiceServerID(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	f.servers.GetServerByIDFn = func(_ context.Context, id int64) (*domain.Server, error) {
		assert.Equal(t, int64(5), id)
		return &domain.Server{ID: 5, Name: "srv-5"}, nil
	}

	got, err := f.svc.AdminUpdateService(context.Background(), 10, 42,
		AdminUpdateServiceInput{ServerID: ptr(int64(5))})
	require.NoError(t, err)
	require.NotNil(t, got.ServerID)
	assert.Equal(t, int64(5), *got.ServerID)
}

func TestAdminUpdateServiceServerIDNotFound(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.AdminUpdateService(context.Background(), 10, 42,
		AdminUpdateServiceInput{ServerID: ptr(int64(999))})
	assertCode(t, err, apperr.CodeNotFound)
}

func TestAdminUpdateServiceBillingCycleAndRecurringAmount(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)

	got, err := f.svc.AdminUpdateService(context.Background(), 10, 42,
		AdminUpdateServiceInput{BillingCycle: ptr("annually"), RecurringAmount: ptr(int64(1_200_000))})
	require.NoError(t, err)
	assert.Equal(t, domain.CycleAnnually, got.BillingCycle)
	assert.Equal(t, int64(1_200_000), got.RecurringAmount)
}

func TestAdminUpdateServiceRegistrationAndTerminatedDates(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)

	got, err := f.svc.AdminUpdateService(context.Background(), 10, 42,
		AdminUpdateServiceInput{RegistrationDate: ptr("2026-01-15"), TerminatedAt: ptr("2026-10-01")})
	require.NoError(t, err)
	require.NotNil(t, got.RegistrationDate)
	assert.True(t, date(2026, 1, 15).Equal(*got.RegistrationDate))
	require.NotNil(t, got.TerminatedAt)
	assert.True(t, date(2026, 10, 1).Equal(*got.TerminatedAt))
}

func TestAdminUpdateServiceInvalidRegistrationDate(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.AdminUpdateService(context.Background(), 10, 42,
		AdminUpdateServiceInput{RegistrationDate: ptr("not-a-date")})
	assertCode(t, err, apperr.CodeValidation)
}

func TestAdminUpdateServiceInvalidTerminatedAt(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.AdminUpdateService(context.Background(), 10, 42,
		AdminUpdateServiceInput{TerminatedAt: ptr("not-a-date")})
	assertCode(t, err, apperr.CodeValidation)
}

func TestAdminUpdateServiceSuspendReason(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceSuspended)

	got, err := f.svc.AdminUpdateService(context.Background(), 10, 42,
		AdminUpdateServiceInput{SuspendReason: ptr("manually cleared")})
	require.NoError(t, err)
	assert.Equal(t, "manually cleared", got.SuspendReason)

	// Blanking it out (e.g. after manually verifying payment) must be allowed.
	got, err = f.svc.AdminUpdateService(context.Background(), 10, 42, AdminUpdateServiceInput{SuspendReason: ptr("")})
	require.NoError(t, err)
	assert.Empty(t, got.SuspendReason)
}

func TestAdminUpdateServiceInvalidDate(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.AdminUpdateService(context.Background(), 10, 42,
		AdminUpdateServiceInput{NextDueDate: ptr("not-a-date")})
	assertCode(t, err, apperr.CodeValidation)
}

func TestAdminUpdateServiceNothingToUpdate(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)
	_, err := f.svc.AdminUpdateService(context.Background(), 10, 42, AdminUpdateServiceInput{})
	assertCode(t, err, apperr.CodeValidation)
}

func TestAdminUpdateServiceNotFound(t *testing.T) {
	f := newFixture()
	_, err := f.svc.AdminUpdateService(context.Background(), 10, 999, AdminUpdateServiceInput{Notes: ptr("x")})
	assertCode(t, err, apperr.CodeNotFound)
}

// Listing / detail

func TestListServicesRouting(t *testing.T) {
	f := newFixture()
	f.store.ListFn = func(_ context.Context, p ports.ListParams) ([]domain.Service, int64, error) {
		return []domain.Service{{ID: 1}, {ID: 2}}, 2, nil
	}
	f.store.ListByClientFn = func(_ context.Context, clientID int64, p ports.ListParams) ([]domain.Service, int64, error) {
		assert.Equal(t, int64(7), clientID)
		return []domain.Service{{ID: 1}}, 1, nil
	}

	all, total, err := f.svc.ListServices(context.Background(), 0, ports.ListParams{})
	require.NoError(t, err)
	assert.Len(t, all, 2)
	assert.Equal(t, int64(2), total)

	mine, total, err := f.svc.ListServices(context.Background(), 7, ports.ListParams{})
	require.NoError(t, err)
	assert.Len(t, mine, 1)
	assert.Equal(t, int64(1), total)
}

func TestGetServiceOwnership(t *testing.T) {
	f := newFixture()
	f.service = baseService(domain.ServiceActive)

	svc, err := f.svc.GetService(context.Background(), 7, 42)
	require.NoError(t, err)
	assert.Equal(t, int64(42), svc.ID)

	svc, err = f.svc.GetService(context.Background(), 0, 42) // admin
	require.NoError(t, err)
	assert.Equal(t, int64(42), svc.ID)

	_, err = f.svc.GetService(context.Background(), 8, 42)
	assertCode(t, err, apperr.CodeNotFound)
}

// Servers

func TestCreateServerEncryptsAndDefaults(t *testing.T) {
	f := newFixture()
	f.crypt.EncryptFn = func(p string) (string, error) { return "enc:" + p, nil }
	f.servers.GetGroupByIDFn = func(_ context.Context, id int64) (*domain.ServerGroup, error) {
		return &domain.ServerGroup{ID: id}, nil
	}
	var created *domain.Server
	f.servers.CreateServerFn = func(_ context.Context, s *domain.Server) error {
		s.ID = 11
		created = s
		return nil
	}

	server, err := f.svc.CreateServer(context.Background(), 10, ServerInput{
		GroupID: ptr(int64(9)), Name: "srv1", Module: "cpanel", Hostname: "srv1.host.id",
		Username: "root", Password: "pw", APIToken: "tok", MaxAccounts: 100,
		PackagePrefix: "reseller_", IPAddress: "203.0.113.10",
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, "enc:pw", created.PasswordEnc)
	assert.Equal(t, "enc:tok", created.APITokenEnc)
	assert.Equal(t, 2087, created.Port) // cpanel default
	assert.True(t, created.UseSSL)
	assert.True(t, created.Active)
	assert.Equal(t, "reseller_", created.PackagePrefix)
	assert.Equal(t, "203.0.113.10", created.IPAddress)
	assert.Equal(t, int64(11), server.ID)
	require.NotEmpty(t, f.audit.Entries)
	assert.Equal(t, "server.create", f.audit.Entries[0].Action)
}

func TestCreateServerValidation(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateServer(context.Background(), 10, ServerInput{Module: "plesk", Hostname: "h"})
	assertCode(t, err, apperr.CodeValidation)
}

func TestCreateServerInvalidIPAddress(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateServer(context.Background(), 10, ServerInput{
		Name: "srv1", Module: "directadmin", Hostname: "da.host.id", IPAddress: "not-an-ip",
	})
	assertCode(t, err, apperr.CodeValidation)
}

func TestCreateServerUnknownGroup(t *testing.T) {
	f := newFixture()
	_, err := f.svc.CreateServer(context.Background(), 10, ServerInput{
		GroupID: ptr(int64(999)), Name: "srv", Module: "directadmin", Hostname: "da.host.id",
	})
	assertCode(t, err, apperr.CodeNotFound)
}

func TestUpdateServerKeepsSecretsWhenEmpty(t *testing.T) {
	f := newFixture()
	f.withServer(cpanelServer())
	f.crypt.EncryptFn = func(p string) (string, error) { return "enc:" + p, nil }
	var updated *domain.Server
	f.servers.UpdateServerFn = func(_ context.Context, s *domain.Server) error {
		updated = s
		return nil
	}

	_, err := f.svc.UpdateServer(context.Background(), 10, 3, ServerInput{
		Name: "srv1-renamed", Module: "cpanel", Hostname: "srv1.host.id",
		Username: "root", Active: ptr(false), PackagePrefix: "reseller_",
		IPAddress: "203.0.113.10",
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "srv1-renamed", updated.Name)
	assert.Equal(t, "srv-pass", updated.PasswordEnc)  // unchanged
	assert.Equal(t, "srv-token", updated.APITokenEnc) // unchanged
	assert.False(t, updated.Active)
	assert.Equal(t, 2087, updated.Port) // kept
	assert.Equal(t, "reseller_", updated.PackagePrefix)
	assert.Equal(t, "203.0.113.10", updated.IPAddress)

	// New secret gets re-encrypted.
	_, err = f.svc.UpdateServer(context.Background(), 10, 3, ServerInput{
		Name: "srv1", Module: "cpanel", Hostname: "srv1.host.id", APIToken: "newtok",
	})
	require.NoError(t, err)
	assert.Equal(t, "enc:newtok", updated.APITokenEnc)
}

func TestDeleteServerGuards(t *testing.T) {
	f := newFixture()
	f.withServer(cpanelServer())
	f.store.CountByServerFn = func(_ context.Context, serverID int64) (int64, error) { return 4, nil }
	assertCode(t, f.svc.DeleteServer(context.Background(), 10, 3), apperr.CodeConflict)

	f.store.CountByServerFn = func(context.Context, int64) (int64, error) { return 0, nil }
	deleted := false
	f.servers.DeleteServerFn = func(_ context.Context, id int64) error {
		deleted = true
		return nil
	}
	require.NoError(t, f.svc.DeleteServer(context.Background(), 10, 3))
	assert.True(t, deleted)
}

func TestTestConnection(t *testing.T) {
	f := newFixture()
	f.withServer(cpanelServer())
	f.cpanel.TestConnectionFn = func(_ context.Context, cfg ports.ServerConfig) (*ports.ServerInfo, error) {
		// Stored server: config is decrypted and complete.
		assert.Equal(t, "srv1.host.id", cfg.Hostname)
		assert.Equal(t, "srv-token", cfg.APIToken)
		return &ports.ServerInfo{Version: "11.134.0.45", Hostname: "srv1.host.id",
			Nameservers: []string{"ns1.host.id", "ns2.host.id"}}, nil
	}

	res, err := f.svc.TestConnection(context.Background(), TestConnectionInput{ID: 3})
	require.NoError(t, err)
	assert.True(t, res.OK)
	assert.Equal(t, "11.134.0.45", res.Version)
	assert.Equal(t, []string{"ns1.host.id", "ns2.host.id"}, res.Nameservers)
	assert.Contains(t, res.Message, "11.134.0.45")

	// Adapter failure -> OK=false, no HTTP error; the wrapped cause is surfaced.
	f.cpanel.TestConnectionFn = func(context.Context, ports.ServerConfig) (*ports.ServerInfo, error) {
		return nil, apperr.External("cpanel", errors.New("whm version: http 403: access denied"))
	}
	res, err = f.svc.TestConnection(context.Background(), TestConnectionInput{ID: 3})
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.Message, "access denied")

	// Plain (non-apperr) error is surfaced verbatim.
	f.cpanel.TestConnectionFn = func(context.Context, ports.ServerConfig) (*ports.ServerInfo, error) {
		return nil, errors.New("connection refused")
	}
	res, err = f.svc.TestConnection(context.Background(), TestConnectionInput{ID: 3})
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.Message, "connection refused")

	// Module none -> not testable.
	sv := cpanelServer()
	sv.Module = domain.ModuleNone
	f.withServer(sv)
	res, err = f.svc.TestConnection(context.Background(), TestConnectionInput{ID: 3})
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.Message, "no provisioning module")

	// Unknown server.
	_, err = f.svc.TestConnection(context.Background(), TestConnectionInput{ID: 999})
	assertCode(t, err, apperr.CodeNotFound)
}

func TestTestConnectionPreSave(t *testing.T) {
	f := newFixture()
	var gotCfg ports.ServerConfig
	f.cpanel.TestConnectionFn = func(_ context.Context, cfg ports.ServerConfig) (*ports.ServerInfo, error) {
		gotCfg = cfg
		return &ports.ServerInfo{Nameservers: []string{"ns1.new.id"}}, nil
	}

	// Pure pre-save (no id): defaults UseSSL true and port from module.
	res, err := f.svc.TestConnection(context.Background(), TestConnectionInput{
		Module: "cpanel", Hostname: "new.host.id", Username: "root", APIToken: "tok",
	})
	require.NoError(t, err)
	assert.True(t, res.OK)
	assert.Equal(t, []string{"ns1.new.id"}, res.Nameservers)
	assert.Equal(t, "connection successful", res.Message) // no version reported
	assert.True(t, gotCfg.UseSSL)
	assert.Equal(t, 2087, gotCfg.Port)
	assert.Equal(t, "tok", gotCfg.APIToken)

	// Missing hostname -> OK=false without hitting the adapter.
	res, err = f.svc.TestConnection(context.Background(), TestConnectionInput{Module: "cpanel"})
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.Message, "hostname is required")

	// Editing (id>0) with blank secret: stored token is decrypted and reused.
	f.withServer(cpanelServer())
	f.cpanel.TestConnectionFn = func(_ context.Context, cfg ports.ServerConfig) (*ports.ServerInfo, error) {
		gotCfg = cfg
		return &ports.ServerInfo{}, nil
	}
	res, err = f.svc.TestConnection(context.Background(), TestConnectionInput{
		ID: 3, Module: "cpanel", Hostname: "srv1.host.id", Username: "root", // no api_token
	})
	require.NoError(t, err)
	assert.True(t, res.OK)
	assert.Equal(t, "srv-token", gotCfg.APIToken) // filled from the stored row

	// Invalid module -> validation error (422 at the transport).
	_, err = f.svc.TestConnection(context.Background(), TestConnectionInput{Module: "bogus", Hostname: "x.host.id"})
	assertCode(t, err, apperr.CodeValidation)
}

// Server groups

func TestGroupCRUD(t *testing.T) {
	f := newFixture()

	// Create with bad strategy -> validation.
	_, err := f.svc.CreateGroup(context.Background(), 10, ServerGroupInput{Name: "g", Strategy: "random"})
	assertCode(t, err, apperr.CodeValidation)

	f.servers.CreateGroupFn = func(_ context.Context, g *domain.ServerGroup) error {
		g.ID = 9
		return nil
	}
	g, err := f.svc.CreateGroup(context.Background(), 10, ServerGroupInput{Name: "g", Strategy: "least_used"})
	require.NoError(t, err)
	assert.Equal(t, int64(9), g.ID)
	assert.Equal(t, domain.StrategyLeastUsed, g.Strategy)

	// Update.
	f.servers.GetGroupByIDFn = func(_ context.Context, id int64) (*domain.ServerGroup, error) {
		return &domain.ServerGroup{ID: id, Name: "g", Strategy: domain.StrategyLeastUsed}, nil
	}
	g, err = f.svc.UpdateGroup(context.Background(), 10, 9, ServerGroupInput{Name: "g2", Strategy: "round_robin"})
	require.NoError(t, err)
	assert.Equal(t, "g2", g.Name)
	assert.Equal(t, domain.StrategyRoundRobin, g.Strategy)

	// List / Get / Delete.
	f.servers.ListGroupsFn = func(context.Context) ([]domain.ServerGroup, error) {
		return []domain.ServerGroup{{ID: 9}}, nil
	}
	groups, err := f.svc.ListGroups(context.Background())
	require.NoError(t, err)
	assert.Len(t, groups, 1)

	got, err := f.svc.GetGroup(context.Background(), 9)
	require.NoError(t, err)
	assert.Equal(t, int64(9), got.ID)

	require.NoError(t, f.svc.DeleteGroup(context.Background(), 10, 9))
}

func TestListPackages(t *testing.T) {
	f := newFixture()
	f.servers.PickServerFn = func(_ context.Context, groupID int64) (*domain.Server, error) {
		assert.Equal(t, int64(9), groupID)
		return cpanelServer(), nil
	}
	f.cpanel.ListPackagesFn = func(_ context.Context, cfg ports.ServerConfig) ([]string, error) {
		assert.Equal(t, "srv1.host.id", cfg.Hostname)
		return []string{"business", "default"}, nil
	}

	res, err := f.svc.ListPackages(context.Background(), 9)
	require.NoError(t, err)
	assert.True(t, res.OK)
	assert.Equal(t, []string{"business", "default"}, res.Packages)

	// Adapter failure -> OK=false, no HTTP error.
	f.cpanel.ListPackagesFn = func(context.Context, ports.ServerConfig) ([]string, error) {
		return nil, apperr.External("cpanel", errors.New("whm listpkgs: http 403: access denied"))
	}
	res, err = f.svc.ListPackages(context.Background(), 9)
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.Message, "access denied")

	// Module none -> not previewable.
	sv := cpanelServer()
	sv.Module = domain.ModuleNone
	f.servers.PickServerFn = func(context.Context, int64) (*domain.Server, error) { return sv, nil }
	res, err = f.svc.ListPackages(context.Background(), 9)
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.Message, "no provisioning module")

	// No available server in the group -> hard error, not OK=false.
	f.servers.PickServerFn = func(context.Context, int64) (*domain.Server, error) {
		return nil, apperr.Conflict("no active server with available capacity in group")
	}
	_, err = f.svc.ListPackages(context.Background(), 9)
	assertCode(t, err, apperr.CodeConflict)
}

func TestValidatePasswordStrength(t *testing.T) {
	assert.NoError(t, validatePasswordStrength("Abcdef123456"))
	assertCode(t, validatePasswordStrength("Ab1"), apperr.CodeValidation)
	assertCode(t, validatePasswordStrength("abcdefghijkl"), apperr.CodeValidation)  // no upper/digit
	assertCode(t, validatePasswordStrength("ABCDEFGHIJKL1"), apperr.CodeValidation) // no lower
}
