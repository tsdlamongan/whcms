package mocks

import (
	"context"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
)

// MockPaymentGateway mocks ports.PaymentGateway.
type MockPaymentGateway struct {
	GetPaymentMethodsFn       func(ctx context.Context, amount int64) ([]ports.PaymentMethod, error)
	CreateTransactionFn       func(ctx context.Context, req ports.CreateTxRequest) (*ports.CreateTxResult, error)
	CheckTransactionFn        func(ctx context.Context, merchantOrderID string) (*ports.TxStatus, error)
	VerifyCallbackSignatureFn func(p ports.CallbackPayload) bool
}

func (m *MockPaymentGateway) GetPaymentMethods(ctx context.Context, amount int64) ([]ports.PaymentMethod, error) {
	if m.GetPaymentMethodsFn != nil {
		return m.GetPaymentMethodsFn(ctx, amount)
	}
	return nil, nil
}

func (m *MockPaymentGateway) CreateTransaction(ctx context.Context, req ports.CreateTxRequest) (*ports.CreateTxResult, error) {
	if m.CreateTransactionFn != nil {
		return m.CreateTransactionFn(ctx, req)
	}
	return nil, nil
}

func (m *MockPaymentGateway) CheckTransaction(ctx context.Context, merchantOrderID string) (*ports.TxStatus, error) {
	if m.CheckTransactionFn != nil {
		return m.CheckTransactionFn(ctx, merchantOrderID)
	}
	return nil, nil
}

func (m *MockPaymentGateway) VerifyCallbackSignature(p ports.CallbackPayload) bool {
	if m.VerifyCallbackSignatureFn != nil {
		return m.VerifyCallbackSignatureFn(p)
	}
	return false
}

// MockCaptchaVerifier mocks ports.CaptchaVerifier.
type MockCaptchaVerifier struct {
	VerifyFn func(ctx context.Context, token, remoteIP string) (bool, error)
}

func (m *MockCaptchaVerifier) Verify(ctx context.Context, token, remoteIP string) (bool, error) {
	if m.VerifyFn != nil {
		return m.VerifyFn(ctx, token, remoteIP)
	}
	return true, nil
}

// MockServerModule mocks ports.ServerModule.
type MockServerModule struct {
	NameFn           func() string
	CreateFn         func(ctx context.Context, s ports.ServerConfig, a ports.CreateAccountParams) (*ports.AccountResult, error)
	SuspendFn        func(ctx context.Context, s ports.ServerConfig, username, reason string) error
	UnsuspendFn      func(ctx context.Context, s ports.ServerConfig, username string) error
	TerminateFn      func(ctx context.Context, s ports.ServerConfig, username string) error
	ChangePackageFn  func(ctx context.Context, s ports.ServerConfig, username, pkg string) error
	ChangePasswordFn func(ctx context.Context, s ports.ServerConfig, username, password string) error
	EnsurePackageFn  func(ctx context.Context, s ports.ServerConfig, spec ports.PackageSpec) error
	DeletePackageFn  func(ctx context.Context, s ports.ServerConfig, name string) error
	PackageInUseFn   func(ctx context.Context, s ports.ServerConfig, name string) (bool, error)
	ListPackagesFn   func(ctx context.Context, s ports.ServerConfig) ([]string, error)
	AccountInfoFn    func(ctx context.Context, s ports.ServerConfig, username string) (*ports.AccountInfo, error)
	TestConnectionFn func(ctx context.Context, s ports.ServerConfig) (*ports.ServerInfo, error)
	SSOURLFn         func(ctx context.Context, s ports.ServerConfig, username string) (string, error)
}

func (m *MockServerModule) Name() string {
	if m.NameFn != nil {
		return m.NameFn()
	}
	return "mock"
}

func (m *MockServerModule) Create(ctx context.Context, s ports.ServerConfig, a ports.CreateAccountParams) (*ports.AccountResult, error) {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, s, a)
	}
	return nil, nil
}

func (m *MockServerModule) Suspend(ctx context.Context, s ports.ServerConfig, username, reason string) error {
	if m.SuspendFn != nil {
		return m.SuspendFn(ctx, s, username, reason)
	}
	return nil
}

func (m *MockServerModule) Unsuspend(ctx context.Context, s ports.ServerConfig, username string) error {
	if m.UnsuspendFn != nil {
		return m.UnsuspendFn(ctx, s, username)
	}
	return nil
}

func (m *MockServerModule) Terminate(ctx context.Context, s ports.ServerConfig, username string) error {
	if m.TerminateFn != nil {
		return m.TerminateFn(ctx, s, username)
	}
	return nil
}

func (m *MockServerModule) ChangePackage(ctx context.Context, s ports.ServerConfig, username, pkg string) error {
	if m.ChangePackageFn != nil {
		return m.ChangePackageFn(ctx, s, username, pkg)
	}
	return nil
}

func (m *MockServerModule) ChangePassword(ctx context.Context, s ports.ServerConfig, username, password string) error {
	if m.ChangePasswordFn != nil {
		return m.ChangePasswordFn(ctx, s, username, password)
	}
	return nil
}

func (m *MockServerModule) EnsurePackage(ctx context.Context, s ports.ServerConfig, spec ports.PackageSpec) error {
	if m.EnsurePackageFn != nil {
		return m.EnsurePackageFn(ctx, s, spec)
	}
	return nil
}

func (m *MockServerModule) DeletePackage(ctx context.Context, s ports.ServerConfig, name string) error {
	if m.DeletePackageFn != nil {
		return m.DeletePackageFn(ctx, s, name)
	}
	return nil
}

func (m *MockServerModule) PackageInUse(ctx context.Context, s ports.ServerConfig, name string) (bool, error) {
	if m.PackageInUseFn != nil {
		return m.PackageInUseFn(ctx, s, name)
	}
	return false, nil
}

func (m *MockServerModule) ListPackages(ctx context.Context, s ports.ServerConfig) ([]string, error) {
	if m.ListPackagesFn != nil {
		return m.ListPackagesFn(ctx, s)
	}
	return nil, nil
}

func (m *MockServerModule) AccountInfo(ctx context.Context, s ports.ServerConfig, username string) (*ports.AccountInfo, error) {
	if m.AccountInfoFn != nil {
		return m.AccountInfoFn(ctx, s, username)
	}
	return nil, nil
}

func (m *MockServerModule) TestConnection(ctx context.Context, s ports.ServerConfig) (*ports.ServerInfo, error) {
	if m.TestConnectionFn != nil {
		return m.TestConnectionFn(ctx, s)
	}
	return &ports.ServerInfo{}, nil
}

func (m *MockServerModule) SSOURL(ctx context.Context, s ports.ServerConfig, username string) (string, error) {
	if m.SSOURLFn != nil {
		return m.SSOURLFn(ctx, s, username)
	}
	return "", nil
}

// MockRegistrarModule mocks ports.RegistrarModule.
type MockRegistrarModule struct {
	CheckAvailabilityFn func(ctx context.Context, names []string) ([]ports.DomainAvailability, error)
	RegisterFn          func(ctx context.Context, req ports.RegisterDomainRequest) (*ports.DomainResult, error)
	TransferFn          func(ctx context.Context, req ports.TransferDomainRequest) (*ports.DomainResult, error)
	RenewFn             func(ctx context.Context, name string, years int) (*ports.DomainResult, error)
	GetNameserversFn    func(ctx context.Context, name string) ([]string, error)
	UpdateNameserversFn func(ctx context.Context, name string, ns []string) error
	GetContactFn        func(ctx context.Context, name string) (*ports.RegistrantContact, error)
	UpdateContactFn     func(ctx context.Context, name string, c ports.RegistrantContact) error
	GetEPPCodeFn        func(ctx context.Context, name string) (string, error)
	GetDNSRecordsFn     func(ctx context.Context, name string) ([]ports.DNSRecord, error)
	UpdateDNSRecordsFn  func(ctx context.Context, name string, recs []ports.DNSRecord) error
	SyncDomainFn        func(ctx context.Context, name string) (*ports.DomainSyncInfo, error)
	AccountInfoFn       func(ctx context.Context) (*ports.RegistrarAccountInfo, error)
	ListCatalogPricesFn func(ctx context.Context) ([]ports.RegistrarCatalogPrice, error)
}

func (m *MockRegistrarModule) CheckAvailability(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
	if m.CheckAvailabilityFn != nil {
		return m.CheckAvailabilityFn(ctx, names)
	}
	return nil, nil
}

func (m *MockRegistrarModule) Register(ctx context.Context, req ports.RegisterDomainRequest) (*ports.DomainResult, error) {
	if m.RegisterFn != nil {
		return m.RegisterFn(ctx, req)
	}
	return nil, nil
}

func (m *MockRegistrarModule) Transfer(ctx context.Context, req ports.TransferDomainRequest) (*ports.DomainResult, error) {
	if m.TransferFn != nil {
		return m.TransferFn(ctx, req)
	}
	return nil, nil
}

func (m *MockRegistrarModule) Renew(ctx context.Context, name string, years int) (*ports.DomainResult, error) {
	if m.RenewFn != nil {
		return m.RenewFn(ctx, name, years)
	}
	return nil, nil
}

func (m *MockRegistrarModule) GetNameservers(ctx context.Context, name string) ([]string, error) {
	if m.GetNameserversFn != nil {
		return m.GetNameserversFn(ctx, name)
	}
	return nil, nil
}

func (m *MockRegistrarModule) UpdateNameservers(ctx context.Context, name string, ns []string) error {
	if m.UpdateNameserversFn != nil {
		return m.UpdateNameserversFn(ctx, name, ns)
	}
	return nil
}

func (m *MockRegistrarModule) GetContact(ctx context.Context, name string) (*ports.RegistrantContact, error) {
	if m.GetContactFn != nil {
		return m.GetContactFn(ctx, name)
	}
	return nil, nil
}

func (m *MockRegistrarModule) UpdateContact(ctx context.Context, name string, c ports.RegistrantContact) error {
	if m.UpdateContactFn != nil {
		return m.UpdateContactFn(ctx, name, c)
	}
	return nil
}

func (m *MockRegistrarModule) GetEPPCode(ctx context.Context, name string) (string, error) {
	if m.GetEPPCodeFn != nil {
		return m.GetEPPCodeFn(ctx, name)
	}
	return "", nil
}

func (m *MockRegistrarModule) GetDNSRecords(ctx context.Context, name string) ([]ports.DNSRecord, error) {
	if m.GetDNSRecordsFn != nil {
		return m.GetDNSRecordsFn(ctx, name)
	}
	return nil, nil
}

func (m *MockRegistrarModule) UpdateDNSRecords(ctx context.Context, name string, recs []ports.DNSRecord) error {
	if m.UpdateDNSRecordsFn != nil {
		return m.UpdateDNSRecordsFn(ctx, name, recs)
	}
	return nil
}

func (m *MockRegistrarModule) SyncDomain(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
	if m.SyncDomainFn != nil {
		return m.SyncDomainFn(ctx, name)
	}
	return nil, nil
}

func (m *MockRegistrarModule) AccountInfo(ctx context.Context) (*ports.RegistrarAccountInfo, error) {
	if m.AccountInfoFn != nil {
		return m.AccountInfoFn(ctx)
	}
	return nil, nil
}

func (m *MockRegistrarModule) ListCatalogPrices(ctx context.Context) ([]ports.RegistrarCatalogPrice, error) {
	if m.ListCatalogPricesFn != nil {
		return m.ListCatalogPricesFn(ctx)
	}
	return nil, nil
}
