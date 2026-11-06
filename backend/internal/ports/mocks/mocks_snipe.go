package mocks

import (
	"context"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
)

// Compile-time checks for the domain sniper mocks.
var (
	_ ports.SnipeRepo          = (*MockSnipeRepo)(nil)
	_ ports.SnipePlanRepo      = (*MockSnipePlanRepo)(nil)
	_ ports.SnipeActivator     = (*MockSnipeActivator)(nil)
	_ ports.InvoiceCanceller   = (*MockInvoiceCanceller)(nil)
	_ ports.DomainSniper       = (*MockDomainSniper)(nil)
	_ ports.SnipeDomainService = (*MockSnipeDomainService)(nil)
)

// MockSnipeRepo mocks ports.SnipeRepo.
type MockSnipeRepo struct {
	CreateFn            func(ctx context.Context, s *domain.DomainSnipe) error
	GetByIDFn           func(ctx context.Context, id int64) (*domain.DomainSnipe, error)
	ListFn              func(ctx context.Context, p ports.ListParams) ([]domain.DomainSnipe, int64, error)
	ListByClientFn      func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.DomainSnipe, int64, error)
	CountOpenByClientFn func(ctx context.Context, clientID int64) (int, error)
	CountByStatusFn     func(ctx context.Context) (map[domain.SnipeStatus]int, error)
	CountLeasedFn       func(ctx context.Context, now time.Time) (int, error)
	ClaimFn             func(ctx context.Context, owner string, limit int, now, leaseUntil time.Time) ([]domain.DomainSnipe, error)
	HeartbeatFn         func(ctx context.Context, h ports.SnipeHeartbeat) (bool, error)
	ReleaseFn           func(ctx context.Context, id int64, owner string) error
	SetCustomerIDFn     func(ctx context.Context, id int64, customerID int64) error
	FinishFn            func(ctx context.Context, f ports.SnipeFinish) (bool, error)
	MarkRefundedFn      func(ctx context.Context, id int64, at time.Time) error
	RecordRefundFn      func(ctx context.Context, id int64, amount int64, at time.Time) (bool, error)
	SetInvoiceFn        func(ctx context.Context, id, invoiceID, refundableDomain, refundableFee int64) error
	ActivateFn          func(ctx context.Context, id int64, paidAt time.Time) (bool, error)
	ListStalePendingFn  func(ctx context.Context, now time.Time, limit int) ([]domain.DomainSnipe, error)
	ListExpirableFn     func(ctx context.Context, now time.Time, limit int) ([]domain.DomainSnipe, error)
	AddEventFn          func(ctx context.Context, e *domain.SnipeEvent) error
	ListEventsFn        func(ctx context.Context, snipeID int64, limit int) ([]domain.SnipeEvent, error)
}

func (m *MockSnipeRepo) Create(ctx context.Context, s *domain.DomainSnipe) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, s)
	}
	return nil
}

func (m *MockSnipeRepo) GetByID(ctx context.Context, id int64) (*domain.DomainSnipe, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockSnipeRepo) List(ctx context.Context, p ports.ListParams) ([]domain.DomainSnipe, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockSnipeRepo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.DomainSnipe, int64, error) {
	if m.ListByClientFn != nil {
		return m.ListByClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (m *MockSnipeRepo) CountOpenByClient(ctx context.Context, clientID int64) (int, error) {
	if m.CountOpenByClientFn != nil {
		return m.CountOpenByClientFn(ctx, clientID)
	}
	return 0, nil
}

func (m *MockSnipeRepo) CountByStatus(ctx context.Context) (map[domain.SnipeStatus]int, error) {
	if m.CountByStatusFn != nil {
		return m.CountByStatusFn(ctx)
	}
	return nil, nil
}

func (m *MockSnipeRepo) CountLeased(ctx context.Context, now time.Time) (int, error) {
	if m.CountLeasedFn != nil {
		return m.CountLeasedFn(ctx, now)
	}
	return 0, nil
}

func (m *MockSnipeRepo) Claim(ctx context.Context, owner string, limit int, now, leaseUntil time.Time) ([]domain.DomainSnipe, error) {
	if m.ClaimFn != nil {
		return m.ClaimFn(ctx, owner, limit, now, leaseUntil)
	}
	return nil, nil
}

func (m *MockSnipeRepo) Heartbeat(ctx context.Context, h ports.SnipeHeartbeat) (bool, error) {
	if m.HeartbeatFn != nil {
		return m.HeartbeatFn(ctx, h)
	}
	return true, nil
}

func (m *MockSnipeRepo) Release(ctx context.Context, id int64, owner string) error {
	if m.ReleaseFn != nil {
		return m.ReleaseFn(ctx, id, owner)
	}
	return nil
}

func (m *MockSnipeRepo) SetCustomerID(ctx context.Context, id int64, customerID int64) error {
	if m.SetCustomerIDFn != nil {
		return m.SetCustomerIDFn(ctx, id, customerID)
	}
	return nil
}

func (m *MockSnipeRepo) Finish(ctx context.Context, f ports.SnipeFinish) (bool, error) {
	if m.FinishFn != nil {
		return m.FinishFn(ctx, f)
	}
	return true, nil
}

func (m *MockSnipeRepo) MarkRefunded(ctx context.Context, id int64, at time.Time) error {
	if m.MarkRefundedFn != nil {
		return m.MarkRefundedFn(ctx, id, at)
	}
	return nil
}

func (m *MockSnipeRepo) RecordRefund(ctx context.Context, id int64, amount int64, at time.Time) (bool, error) {
	if m.RecordRefundFn != nil {
		return m.RecordRefundFn(ctx, id, amount, at)
	}
	return true, nil
}

func (m *MockSnipeRepo) SetInvoice(ctx context.Context, id, invoiceID, refundableDomain, refundableFee int64) error {
	if m.SetInvoiceFn != nil {
		return m.SetInvoiceFn(ctx, id, invoiceID, refundableDomain, refundableFee)
	}
	return nil
}

func (m *MockSnipeRepo) Activate(ctx context.Context, id int64, paidAt time.Time) (bool, error) {
	if m.ActivateFn != nil {
		return m.ActivateFn(ctx, id, paidAt)
	}
	return true, nil
}

func (m *MockSnipeRepo) ListStalePending(ctx context.Context, now time.Time, limit int) ([]domain.DomainSnipe, error) {
	if m.ListStalePendingFn != nil {
		return m.ListStalePendingFn(ctx, now, limit)
	}
	return nil, nil
}

func (m *MockSnipeRepo) ListExpirable(ctx context.Context, now time.Time, limit int) ([]domain.DomainSnipe, error) {
	if m.ListExpirableFn != nil {
		return m.ListExpirableFn(ctx, now, limit)
	}
	return nil, nil
}

func (m *MockSnipeRepo) AddEvent(ctx context.Context, e *domain.SnipeEvent) error {
	if m.AddEventFn != nil {
		return m.AddEventFn(ctx, e)
	}
	return nil
}

func (m *MockSnipeRepo) ListEvents(ctx context.Context, snipeID int64, limit int) ([]domain.SnipeEvent, error) {
	if m.ListEventsFn != nil {
		return m.ListEventsFn(ctx, snipeID, limit)
	}
	return nil, nil
}

// MockDomainSniper mocks ports.DomainSniper.
type MockDomainSniper struct {
	EnsureCustomerFn      func(ctx context.Context, c ports.RegistrantContact) (int64, error)
	IsAvailableFn         func(ctx context.Context, name string) (bool, error)
	RegisterForCustomerFn func(ctx context.Context, name string, years int, customerID int64, ns []string) (*ports.DomainResult, error)
	SyncDomainFn          func(ctx context.Context, name string) (*ports.DomainSyncInfo, error)
	AccountInfoFn         func(ctx context.Context) (*ports.RegistrarAccountInfo, error)
}

func (m *MockDomainSniper) EnsureCustomer(ctx context.Context, c ports.RegistrantContact) (int64, error) {
	if m.EnsureCustomerFn != nil {
		return m.EnsureCustomerFn(ctx, c)
	}
	return 0, nil
}

func (m *MockDomainSniper) IsAvailable(ctx context.Context, name string) (bool, error) {
	if m.IsAvailableFn != nil {
		return m.IsAvailableFn(ctx, name)
	}
	return false, nil
}

func (m *MockDomainSniper) RegisterForCustomer(ctx context.Context, name string, years int, customerID int64, ns []string) (*ports.DomainResult, error) {
	if m.RegisterForCustomerFn != nil {
		return m.RegisterForCustomerFn(ctx, name, years, customerID, ns)
	}
	return nil, nil
}

func (m *MockDomainSniper) SyncDomain(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
	if m.SyncDomainFn != nil {
		return m.SyncDomainFn(ctx, name)
	}
	return nil, nil
}

func (m *MockDomainSniper) AccountInfo(ctx context.Context) (*ports.RegistrarAccountInfo, error) {
	if m.AccountInfoFn != nil {
		return m.AccountInfoFn(ctx)
	}
	return nil, nil
}

// MockSnipeDomainService mocks ports.SnipeDomainService.
type MockSnipeDomainService struct {
	QuoteRegistrationFn  func(ctx context.Context, name string, years int) (*ports.DomainQuote, error)
	RegistrantContactFn  func(ctx context.Context, clientID int64) (ports.RegistrantContact, error)
	RecordSnipedDomainFn func(ctx context.Context, in ports.RecordSnipedDomainInput) (*domain.Domain, error)
}

func (m *MockSnipeDomainService) QuoteRegistration(ctx context.Context, name string, years int) (*ports.DomainQuote, error) {
	if m.QuoteRegistrationFn != nil {
		return m.QuoteRegistrationFn(ctx, name, years)
	}
	return nil, nil
}

func (m *MockSnipeDomainService) RegistrantContactFor(ctx context.Context, clientID int64) (ports.RegistrantContact, error) {
	if m.RegistrantContactFn != nil {
		return m.RegistrantContactFn(ctx, clientID)
	}
	return ports.RegistrantContact{}, nil
}

func (m *MockSnipeDomainService) RecordSnipedDomain(ctx context.Context, in ports.RecordSnipedDomainInput) (*domain.Domain, error) {
	if m.RecordSnipedDomainFn != nil {
		return m.RecordSnipedDomainFn(ctx, in)
	}
	return nil, nil
}

// MockSnipePlanRepo mocks ports.SnipePlanRepo.
type MockSnipePlanRepo struct {
	CreateFn  func(ctx context.Context, p *domain.SnipePlan) error
	GetByIDFn func(ctx context.Context, id int64) (*domain.SnipePlan, error)
	UpdateFn  func(ctx context.Context, p *domain.SnipePlan) error
	ListFn    func(ctx context.Context, activeOnly bool) ([]domain.SnipePlan, error)
}

func (m *MockSnipePlanRepo) Create(ctx context.Context, p *domain.SnipePlan) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, p)
	}
	return nil
}

func (m *MockSnipePlanRepo) GetByID(ctx context.Context, id int64) (*domain.SnipePlan, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockSnipePlanRepo) Update(ctx context.Context, p *domain.SnipePlan) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, p)
	}
	return nil
}

func (m *MockSnipePlanRepo) List(ctx context.Context, activeOnly bool) ([]domain.SnipePlan, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, activeOnly)
	}
	return nil, nil
}

// MockSnipeActivator mocks ports.SnipeActivator.
type MockSnipeActivator struct {
	ActivateSnipeAfterPaymentFn func(ctx context.Context, snipeID int64) error
}

func (m *MockSnipeActivator) ActivateSnipeAfterPayment(ctx context.Context, snipeID int64) error {
	if m.ActivateSnipeAfterPaymentFn != nil {
		return m.ActivateSnipeAfterPaymentFn(ctx, snipeID)
	}
	return nil
}

// MockInvoiceCanceller mocks ports.InvoiceCanceller.
type MockInvoiceCanceller struct {
	CancelInvoiceFn func(ctx context.Context, actorUserID, invoiceID int64) (*domain.Invoice, error)
}

func (m *MockInvoiceCanceller) CancelInvoice(ctx context.Context, actorUserID, invoiceID int64) (*domain.Invoice, error) {
	if m.CancelInvoiceFn != nil {
		return m.CancelInvoiceFn(ctx, actorUserID, invoiceID)
	}
	return nil, nil
}
