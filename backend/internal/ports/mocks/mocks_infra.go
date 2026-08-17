package mocks

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
)

// MockMailer mocks ports.Mailer. Sent collects messages when SendFn is nil.
type MockMailer struct {
	SendFn func(ctx context.Context, msg ports.MailMessage) error
	Sent   []ports.MailMessage
}

func (m *MockMailer) Send(ctx context.Context, msg ports.MailMessage) error {
	if m.SendFn != nil {
		return m.SendFn(ctx, msg)
	}
	m.Sent = append(m.Sent, msg)
	return nil
}

// MockStorage mocks ports.Storage.
type MockStorage struct {
	PutFn        func(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	GetFn        func(ctx context.Context, key string) (io.ReadCloser, error)
	DeleteFn     func(ctx context.Context, key string) error
	PresignGetFn func(ctx context.Context, key string, ttl time.Duration, opts ...ports.PresignOption) (string, error)
}

func (m *MockStorage) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if m.PutFn != nil {
		return m.PutFn(ctx, key, r, size, contentType)
	}
	return nil
}

func (m *MockStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if m.GetFn != nil {
		return m.GetFn(ctx, key)
	}
	return nil, nil
}

func (m *MockStorage) Delete(ctx context.Context, key string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, key)
	}
	return nil
}

func (m *MockStorage) PresignGet(ctx context.Context, key string, ttl time.Duration, opts ...ports.PresignOption) (string, error) {
	if m.PresignGetFn != nil {
		return m.PresignGetFn(ctx, key, ttl, opts...)
	}
	return "", nil
}

// MockEncryptor mocks ports.Encryptor. Nil fields pass values through
// unchanged, which is convenient for service tests.
type MockEncryptor struct {
	EncryptFn func(plaintext string) (string, error)
	DecryptFn func(ciphertext string) (string, error)
}

func (m *MockEncryptor) Encrypt(plaintext string) (string, error) {
	if m.EncryptFn != nil {
		return m.EncryptFn(plaintext)
	}
	return plaintext, nil
}

func (m *MockEncryptor) Decrypt(ciphertext string) (string, error) {
	if m.DecryptFn != nil {
		return m.DecryptFn(ciphertext)
	}
	return ciphertext, nil
}

// MockPasswordHasher mocks ports.PasswordHasher. Nil fields use a trivial
// reversible scheme ("hashed:" prefix).
type MockPasswordHasher struct {
	HashFn   func(password string) (string, error)
	VerifyFn func(password, phc string) (bool, error)
}

func (m *MockPasswordHasher) Hash(password string) (string, error) {
	if m.HashFn != nil {
		return m.HashFn(password)
	}
	return "hashed:" + password, nil
}

func (m *MockPasswordHasher) Verify(password, phc string) (bool, error) {
	if m.VerifyFn != nil {
		return m.VerifyFn(password, phc)
	}
	return phc == "hashed:"+password, nil
}

// MockTokenStore mocks ports.TokenStore.
type MockTokenStore struct {
	CreateFn  func(ctx context.Context, kind string, userID int64, ttl time.Duration) (string, error)
	ConsumeFn func(ctx context.Context, kind, token string) (int64, error)
}

func (m *MockTokenStore) Create(ctx context.Context, kind string, userID int64, ttl time.Duration) (string, error) {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, kind, userID, ttl)
	}
	return "", nil
}

func (m *MockTokenStore) Consume(ctx context.Context, kind, token string) (int64, error) {
	if m.ConsumeFn != nil {
		return m.ConsumeFn(ctx, kind, token)
	}
	return 0, nil
}

// EnqueuedTask records one call to MockEnqueuer.Enqueue.
type EnqueuedTask struct {
	TaskType string
	Payload  any
	Opts     []ports.JobOption
}

// MockEnqueuer mocks ports.Enqueuer. Tasks collects calls when EnqueueFn is nil.
type MockEnqueuer struct {
	EnqueueFn func(ctx context.Context, taskType string, payload any, opts ...ports.JobOption) error
	Tasks     []EnqueuedTask
}

func (m *MockEnqueuer) Enqueue(ctx context.Context, taskType string, payload any, opts ...ports.JobOption) error {
	if m.EnqueueFn != nil {
		return m.EnqueueFn(ctx, taskType, payload, opts...)
	}
	m.Tasks = append(m.Tasks, EnqueuedTask{TaskType: taskType, Payload: payload, Opts: opts})
	return nil
}

// MockLocker mocks ports.Locker. When WithLockFn is nil it simply runs fn.
type MockLocker struct {
	WithLockFn func(ctx context.Context, key string, ttl time.Duration, fn func() error) error
}

func (m *MockLocker) WithLock(ctx context.Context, key string, ttl time.Duration, fn func() error) error {
	if m.WithLockFn != nil {
		return m.WithLockFn(ctx, key, ttl, fn)
	}
	return fn()
}

// MockRateLimiter mocks ports.RateLimiter. Nil Allow permits everything.
type MockRateLimiter struct {
	AllowFn func(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
	ResetFn func(ctx context.Context, key string) error
}

func (m *MockRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	if m.AllowFn != nil {
		return m.AllowFn(ctx, key, limit, window)
	}
	return true, nil
}

func (m *MockRateLimiter) Reset(ctx context.Context, key string) error {
	if m.ResetFn != nil {
		return m.ResetFn(ctx, key)
	}
	return nil
}

// MockTOTPReplayGuard mocks ports.TOTPReplayGuard. Nil Accept accepts every
// step (no replay tracking), matching MockRateLimiter's fail-open default.
type MockTOTPReplayGuard struct {
	AcceptFn func(ctx context.Context, userID int64, step int64, ttl time.Duration) (bool, error)
}

func (m *MockTOTPReplayGuard) Accept(ctx context.Context, userID int64, step int64, ttl time.Duration) (bool, error) {
	if m.AcceptFn != nil {
		return m.AcceptFn(ctx, userID, step, ttl)
	}
	return true, nil
}

// MockCache mocks ports.Cache. Nil GetJSON reports a miss.
type MockCache struct {
	GetJSONFn func(ctx context.Context, key string, out any) (bool, error)
	SetJSONFn func(ctx context.Context, key string, val any, ttl time.Duration) error
	DeleteFn  func(ctx context.Context, key string) error
}

func (m *MockCache) GetJSON(ctx context.Context, key string, out any) (bool, error) {
	if m.GetJSONFn != nil {
		return m.GetJSONFn(ctx, key, out)
	}
	return false, nil
}

func (m *MockCache) SetJSON(ctx context.Context, key string, val any, ttl time.Duration) error {
	if m.SetJSONFn != nil {
		return m.SetJSONFn(ctx, key, val, ttl)
	}
	return nil
}

func (m *MockCache) Delete(ctx context.Context, key string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, key)
	}
	return nil
}

// MockPresenceTracker mocks ports.PresenceTracker. Nil Touch/Untouch/List no-op.
type MockPresenceTracker struct {
	TouchFn   func(ctx context.Context, userID int64, email, role string) error
	UntouchFn func(ctx context.Context, userID int64) error
	ListFn    func(ctx context.Context) ([]ports.PresenceEntry, error)
}

func (m *MockPresenceTracker) Touch(ctx context.Context, userID int64, email, role string) error {
	if m.TouchFn != nil {
		return m.TouchFn(ctx, userID, email, role)
	}
	return nil
}

func (m *MockPresenceTracker) Untouch(ctx context.Context, userID int64) error {
	if m.UntouchFn != nil {
		return m.UntouchFn(ctx, userID)
	}
	return nil
}

func (m *MockPresenceTracker) List(ctx context.Context) ([]ports.PresenceEntry, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx)
	}
	return nil, nil
}

// AuditEntry records one call to MockAuditLogger.Log.
type AuditEntry struct {
	ActorUserID int64
	Action      string
	Entity      string
	EntityID    int64
	Before      any
	After       any
}

// MockAuditLogger mocks ports.AuditLogger. Entries collects calls when LogFn is nil.
type MockAuditLogger struct {
	LogFn   func(ctx context.Context, actorUserID int64, action, entity string, entityID int64, before, after any)
	Entries []AuditEntry
}

func (m *MockAuditLogger) Log(ctx context.Context, actorUserID int64, action, entity string, entityID int64, before, after any) {
	if m.LogFn != nil {
		m.LogFn(ctx, actorUserID, action, entity, entityID, before, after)
		return
	}
	m.Entries = append(m.Entries, AuditEntry{
		ActorUserID: actorUserID, Action: action, Entity: entity,
		EntityID: entityID, Before: before, After: after,
	})
}

// MockIntegrationLogger mocks ports.IntegrationLogger. Calls collects entries
// when LogFn is nil. Log is safe for concurrent use (adapters may log from
// multiple in-flight requests at once); read Calls only after every logging
// goroutine has joined (e.g. via sync.WaitGroup.Wait or the call under test
// has returned).
type MockIntegrationLogger struct {
	LogFn func(ctx context.Context, call ports.IntegrationCall)

	mu    sync.Mutex
	Calls []ports.IntegrationCall
}

func (m *MockIntegrationLogger) Log(ctx context.Context, call ports.IntegrationCall) {
	if m.LogFn != nil {
		m.LogFn(ctx, call)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, call)
}

// MockClock mocks ports.Clock. Zero FixedTime + nil NowFn falls back to
// time.Now().
type MockClock struct {
	NowFn     func() time.Time
	FixedTime time.Time
}

func (m *MockClock) Now() time.Time {
	if m.NowFn != nil {
		return m.NowFn()
	}
	if !m.FixedTime.IsZero() {
		return m.FixedTime
	}
	return time.Now()
}

// MockPDFGenerator mocks ports.PDFGenerator.
type MockPDFGenerator struct {
	InvoicePDFFn func(ctx context.Context, inv ports.InvoicePDFData) ([]byte, error)
}

func (m *MockPDFGenerator) InvoicePDF(ctx context.Context, inv ports.InvoicePDFData) ([]byte, error) {
	if m.InvoicePDFFn != nil {
		return m.InvoicePDFFn(ctx, inv)
	}
	return []byte("%PDF-1.4 mock"), nil
}

// MockInvoiceCreator mocks ports.InvoiceCreator.
type MockInvoiceCreator struct {
	CreateInvoiceFn func(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error)
}

func (m *MockInvoiceCreator) CreateInvoice(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
	if m.CreateInvoiceFn != nil {
		return m.CreateInvoiceFn(ctx, in)
	}
	return nil, nil
}

// MockRenewalInvoiceChecker mocks ports.RenewalInvoiceChecker.
type MockRenewalInvoiceChecker struct {
	HasOpenRenewalInvoiceFn func(ctx context.Context, relatedType domain.InvoiceItemRelatedType, relatedID int64) (bool, error)
}

func (m *MockRenewalInvoiceChecker) HasOpenRenewalInvoice(ctx context.Context, relatedType domain.InvoiceItemRelatedType, relatedID int64) (bool, error) {
	if m.HasOpenRenewalInvoiceFn != nil {
		return m.HasOpenRenewalInvoiceFn(ctx, relatedType, relatedID)
	}
	return false, nil
}

// MockPaidInvoiceProcessor mocks ports.PaidInvoiceProcessor.
type MockPaidInvoiceProcessor struct {
	ProcessPaidFn func(ctx context.Context, invoiceID int64) error
}

func (m *MockPaidInvoiceProcessor) ProcessPaid(ctx context.Context, invoiceID int64) error {
	if m.ProcessPaidFn != nil {
		return m.ProcessPaidFn(ctx, invoiceID)
	}
	return nil
}

// MockServiceRenewer mocks ports.ServiceRenewer.
type MockServiceRenewer struct {
	RenewServiceFn func(ctx context.Context, serviceID int64) error
	ApplyUpgradeFn func(ctx context.Context, serviceID int64) error
}

func (m *MockServiceRenewer) RenewService(ctx context.Context, serviceID int64) error {
	if m.RenewServiceFn != nil {
		return m.RenewServiceFn(ctx, serviceID)
	}
	return nil
}

func (m *MockServiceRenewer) ApplyUpgrade(ctx context.Context, serviceID int64) error {
	if m.ApplyUpgradeFn != nil {
		return m.ApplyUpgradeFn(ctx, serviceID)
	}
	return nil
}

// MockDomainRenewer mocks ports.DomainRenewer.
type MockDomainRenewer struct {
	RenewDomainAfterPaymentFn func(ctx context.Context, domainID int64) error
}

func (m *MockDomainRenewer) RenewDomainAfterPayment(ctx context.Context, domainID int64) error {
	if m.RenewDomainAfterPaymentFn != nil {
		return m.RenewDomainAfterPaymentFn(ctx, domainID)
	}
	return nil
}

// MockPaymentApplier mocks ports.PaymentApplier.
type MockPaymentApplier struct {
	ApplyPaymentFn func(ctx context.Context, invoiceID int64, tx ports.ApplyTx) error
}

func (m *MockPaymentApplier) ApplyPayment(ctx context.Context, invoiceID int64, tx ports.ApplyTx) error {
	if m.ApplyPaymentFn != nil {
		return m.ApplyPaymentFn(ctx, invoiceID, tx)
	}
	return nil
}

// MockServiceActivator mocks ports.ServiceActivator.
type MockServiceActivator struct {
	ActivateOrderFn func(ctx context.Context, orderID int64) error
}

func (m *MockServiceActivator) ActivateOrder(ctx context.Context, orderID int64) error {
	if m.ActivateOrderFn != nil {
		return m.ActivateOrderFn(ctx, orderID)
	}
	return nil
}

// MockNotificationSender mocks ports.NotificationSender.
type MockNotificationSender struct {
	SendTemplateFn func(ctx context.Context, userID int64, templateKey string, data map[string]any) error
	AlertAdminFn   func(ctx context.Context, subject, message string) error
}

func (m *MockNotificationSender) SendTemplate(ctx context.Context, userID int64, templateKey string, data map[string]any) error {
	if m.SendTemplateFn != nil {
		return m.SendTemplateFn(ctx, userID, templateKey, data)
	}
	return nil
}

func (m *MockNotificationSender) AlertAdmin(ctx context.Context, subject, message string) error {
	if m.AlertAdminFn != nil {
		return m.AlertAdminFn(ctx, subject, message)
	}
	return nil
}

// MockJobInspector mocks ports.JobInspector.
type MockJobInspector struct {
	ListModuleActionsFn       func(ctx context.Context, filter ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error)
	RetryModuleActionFn       func(ctx context.Context, queue, id string) error
	DeleteModuleActionFn      func(ctx context.Context, queue, id string) error
	DismissAllModuleActionsFn func(ctx context.Context, filter ports.ModuleActionFilter) (int, error)
}

func (m *MockJobInspector) ListModuleActions(ctx context.Context, filter ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error) {
	if m.ListModuleActionsFn != nil {
		return m.ListModuleActionsFn(ctx, filter)
	}
	return nil, 0, nil
}

func (m *MockJobInspector) RetryModuleAction(ctx context.Context, queue, id string) error {
	if m.RetryModuleActionFn != nil {
		return m.RetryModuleActionFn(ctx, queue, id)
	}
	return nil
}

func (m *MockJobInspector) DeleteModuleAction(ctx context.Context, queue, id string) error {
	if m.DeleteModuleActionFn != nil {
		return m.DeleteModuleActionFn(ctx, queue, id)
	}
	return nil
}

func (m *MockJobInspector) DismissAllModuleActions(ctx context.Context, filter ports.ModuleActionFilter) (int, error) {
	if m.DismissAllModuleActionsFn != nil {
		return m.DismissAllModuleActionsFn(ctx, filter)
	}
	return 0, nil
}
