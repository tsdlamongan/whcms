// Package mocks provides hand-written, nil-safe mocks for every interface in
// internal/ports using the function-field pattern:
//
//	repo := &mocks.MockUserRepo{
//	    GetByIDFn: func(ctx context.Context, id int64) (*domain.User, error) {
//	        return &domain.User{ID: id}, nil
//	    },
//	}
//
// Methods whose Fn field is nil return zero values (nil error), so partially
// configured mocks never panic. Module agents add missing mocks HERE ONLY,
// named Mock<Interface>.
package mocks

import (
	"context"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
)

// MockTxManager mocks ports.TxManager. When WithinTxFn is nil it simply runs
// fn(ctx), which is what service tests almost always want.
type MockTxManager struct {
	WithinTxFn func(ctx context.Context, fn func(ctx context.Context) error) error
}

func (m *MockTxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if m.WithinTxFn != nil {
		return m.WithinTxFn(ctx, fn)
	}
	return fn(ctx)
}

// MockUserRepo mocks ports.UserRepo.
type MockUserRepo struct {
	CreateFn           func(ctx context.Context, u *domain.User) error
	GetByIDFn          func(ctx context.Context, id int64) (*domain.User, error)
	GetByEmailFn       func(ctx context.Context, email string) (*domain.User, error)
	UpdateFn           func(ctx context.Context, u *domain.User) error
	UpdatePasswordFn   func(ctx context.Context, id int64, passwordHash string) error
	SetEmailVerifiedFn func(ctx context.Context, id int64, at time.Time) error
	SetLastLoginFn     func(ctx context.Context, id int64, at time.Time) error
	ListFn             func(ctx context.Context, p ports.ListParams) ([]domain.User, int64, error)
	DeleteFn           func(ctx context.Context, id int64) error
	ExistsAnyAdminFn   func(ctx context.Context) (bool, error)
}

func (m *MockUserRepo) Create(ctx context.Context, u *domain.User) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, u)
	}
	return nil
}

func (m *MockUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	if m.GetByEmailFn != nil {
		return m.GetByEmailFn(ctx, email)
	}
	return nil, nil
}

func (m *MockUserRepo) Update(ctx context.Context, u *domain.User) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, u)
	}
	return nil
}

func (m *MockUserRepo) UpdatePassword(ctx context.Context, id int64, passwordHash string) error {
	if m.UpdatePasswordFn != nil {
		return m.UpdatePasswordFn(ctx, id, passwordHash)
	}
	return nil
}

func (m *MockUserRepo) SetEmailVerified(ctx context.Context, id int64, at time.Time) error {
	if m.SetEmailVerifiedFn != nil {
		return m.SetEmailVerifiedFn(ctx, id, at)
	}
	return nil
}

func (m *MockUserRepo) SetLastLogin(ctx context.Context, id int64, at time.Time) error {
	if m.SetLastLoginFn != nil {
		return m.SetLastLoginFn(ctx, id, at)
	}
	return nil
}

func (m *MockUserRepo) List(ctx context.Context, p ports.ListParams) ([]domain.User, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockUserRepo) Delete(ctx context.Context, id int64) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, id)
	}
	return nil
}

func (m *MockUserRepo) ExistsAnyAdmin(ctx context.Context) (bool, error) {
	if m.ExistsAnyAdminFn != nil {
		return m.ExistsAnyAdminFn(ctx)
	}
	return false, nil
}

// MockClientRepo mocks ports.ClientRepo.
type MockClientRepo struct {
	CreateFn       func(ctx context.Context, c *domain.Client) error
	GetByIDFn      func(ctx context.Context, id int64) (*domain.Client, error)
	GetByUserIDFn  func(ctx context.Context, userID int64) (*domain.Client, error)
	UpdateFn       func(ctx context.Context, c *domain.Client) error
	ListFn         func(ctx context.Context, p ports.ListParams) ([]domain.Client, int64, error)
	SoftDeleteFn   func(ctx context.Context, id int64) error
	AdjustCreditFn func(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID *int64) (int64, error)
}

func (m *MockClientRepo) Create(ctx context.Context, c *domain.Client) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, c)
	}
	return nil
}

func (m *MockClientRepo) GetByID(ctx context.Context, id int64) (*domain.Client, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockClientRepo) GetByUserID(ctx context.Context, userID int64) (*domain.Client, error) {
	if m.GetByUserIDFn != nil {
		return m.GetByUserIDFn(ctx, userID)
	}
	return nil, nil
}

func (m *MockClientRepo) Update(ctx context.Context, c *domain.Client) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, c)
	}
	return nil
}

func (m *MockClientRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Client, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockClientRepo) SoftDelete(ctx context.Context, id int64) error {
	if m.SoftDeleteFn != nil {
		return m.SoftDeleteFn(ctx, id)
	}
	return nil
}

func (m *MockClientRepo) AdjustCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID *int64) (int64, error) {
	if m.AdjustCreditFn != nil {
		return m.AdjustCreditFn(ctx, clientID, delta, reason, relatedInvoiceID)
	}
	return 0, nil
}

// MockProductRepo mocks ports.ProductRepo.
type MockProductRepo struct {
	CreateFn                      func(ctx context.Context, pr *domain.Product) error
	GetByIDFn                     func(ctx context.Context, id int64) (*domain.Product, error)
	GetBySlugFn                   func(ctx context.Context, slug string) (*domain.Product, error)
	UpdateFn                      func(ctx context.Context, pr *domain.Product) error
	ListFn                        func(ctx context.Context, p ports.ListParams) ([]domain.Product, int64, error)
	SoftDeleteFn                  func(ctx context.Context, id int64) error
	CreateGroupFn                 func(ctx context.Context, g *domain.ProductGroup) error
	GetGroupByIDFn                func(ctx context.Context, id int64) (*domain.ProductGroup, error)
	UpdateGroupFn                 func(ctx context.Context, g *domain.ProductGroup) error
	ListGroupsFn                  func(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error)
	SoftDeleteGroupFn             func(ctx context.Context, id int64) error
	UpsertPricingFn               func(ctx context.Context, pp *domain.ProductPricing) error
	GetPricingFn                  func(ctx context.Context, productID int64, cycle domain.BillingCycle) (*domain.ProductPricing, error)
	ListPricingFn                 func(ctx context.Context, productID int64) ([]domain.ProductPricing, error)
	DeletePricingFn               func(ctx context.Context, productID int64, cycle domain.BillingCycle) error
	ListOptionGroupsFn            func(ctx context.Context) ([]domain.ConfigurableOptionGroup, error)
	ListOptionsFn                 func(ctx context.Context, groupID int64) ([]domain.ConfigurableOption, error)
	ListOptionValuesFn            func(ctx context.Context, optionID int64) ([]domain.ConfigurableOptionValue, error)
	ListOptionsByGroupIDsFn       func(ctx context.Context, groupIDs []int64) (map[int64][]domain.ConfigurableOption, error)
	ListOptionValuesByOptionIDsFn func(ctx context.Context, optionIDs []int64) (map[int64][]domain.ConfigurableOptionValue, error)
	ListSpecsFn                   func(ctx context.Context, productID int64) ([]domain.ProductSpec, error)
	GetSpecPricingFn              func(ctx context.Context, specID int64, cycle domain.BillingCycle) (*domain.ProductSpecPricing, error)
	DecrementStockFn              func(ctx context.Context, productID int64) error
}

func (m *MockProductRepo) Create(ctx context.Context, pr *domain.Product) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, pr)
	}
	return nil
}

func (m *MockProductRepo) GetByID(ctx context.Context, id int64) (*domain.Product, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockProductRepo) GetBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	if m.GetBySlugFn != nil {
		return m.GetBySlugFn(ctx, slug)
	}
	return nil, nil
}

func (m *MockProductRepo) Update(ctx context.Context, pr *domain.Product) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, pr)
	}
	return nil
}

func (m *MockProductRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Product, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockProductRepo) SoftDelete(ctx context.Context, id int64) error {
	if m.SoftDeleteFn != nil {
		return m.SoftDeleteFn(ctx, id)
	}
	return nil
}

func (m *MockProductRepo) CreateGroup(ctx context.Context, g *domain.ProductGroup) error {
	if m.CreateGroupFn != nil {
		return m.CreateGroupFn(ctx, g)
	}
	return nil
}

func (m *MockProductRepo) GetGroupByID(ctx context.Context, id int64) (*domain.ProductGroup, error) {
	if m.GetGroupByIDFn != nil {
		return m.GetGroupByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockProductRepo) UpdateGroup(ctx context.Context, g *domain.ProductGroup) error {
	if m.UpdateGroupFn != nil {
		return m.UpdateGroupFn(ctx, g)
	}
	return nil
}

func (m *MockProductRepo) ListGroups(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error) {
	if m.ListGroupsFn != nil {
		return m.ListGroupsFn(ctx, includeHidden)
	}
	return nil, nil
}

func (m *MockProductRepo) SoftDeleteGroup(ctx context.Context, id int64) error {
	if m.SoftDeleteGroupFn != nil {
		return m.SoftDeleteGroupFn(ctx, id)
	}
	return nil
}

func (m *MockProductRepo) UpsertPricing(ctx context.Context, pp *domain.ProductPricing) error {
	if m.UpsertPricingFn != nil {
		return m.UpsertPricingFn(ctx, pp)
	}
	return nil
}

func (m *MockProductRepo) GetPricing(ctx context.Context, productID int64, cycle domain.BillingCycle) (*domain.ProductPricing, error) {
	if m.GetPricingFn != nil {
		return m.GetPricingFn(ctx, productID, cycle)
	}
	return nil, nil
}

func (m *MockProductRepo) ListPricing(ctx context.Context, productID int64) ([]domain.ProductPricing, error) {
	if m.ListPricingFn != nil {
		return m.ListPricingFn(ctx, productID)
	}
	return nil, nil
}

func (m *MockProductRepo) DeletePricing(ctx context.Context, productID int64, cycle domain.BillingCycle) error {
	if m.DeletePricingFn != nil {
		return m.DeletePricingFn(ctx, productID, cycle)
	}
	return nil
}

func (m *MockProductRepo) ListOptionGroups(ctx context.Context) ([]domain.ConfigurableOptionGroup, error) {
	if m.ListOptionGroupsFn != nil {
		return m.ListOptionGroupsFn(ctx)
	}
	return nil, nil
}

func (m *MockProductRepo) ListOptions(ctx context.Context, groupID int64) ([]domain.ConfigurableOption, error) {
	if m.ListOptionsFn != nil {
		return m.ListOptionsFn(ctx, groupID)
	}
	return nil, nil
}

func (m *MockProductRepo) ListOptionValues(ctx context.Context, optionID int64) ([]domain.ConfigurableOptionValue, error) {
	if m.ListOptionValuesFn != nil {
		return m.ListOptionValuesFn(ctx, optionID)
	}
	return nil, nil
}

func (m *MockProductRepo) ListOptionsByGroupIDs(ctx context.Context, groupIDs []int64) (map[int64][]domain.ConfigurableOption, error) {
	if m.ListOptionsByGroupIDsFn != nil {
		return m.ListOptionsByGroupIDsFn(ctx, groupIDs)
	}
	return nil, nil
}

func (m *MockProductRepo) ListOptionValuesByOptionIDs(ctx context.Context, optionIDs []int64) (map[int64][]domain.ConfigurableOptionValue, error) {
	if m.ListOptionValuesByOptionIDsFn != nil {
		return m.ListOptionValuesByOptionIDsFn(ctx, optionIDs)
	}
	return nil, nil
}

func (m *MockProductRepo) ListSpecs(ctx context.Context, productID int64) ([]domain.ProductSpec, error) {
	if m.ListSpecsFn != nil {
		return m.ListSpecsFn(ctx, productID)
	}
	return nil, nil
}

func (m *MockProductRepo) GetSpecPricing(ctx context.Context, specID int64, cycle domain.BillingCycle) (*domain.ProductSpecPricing, error) {
	if m.GetSpecPricingFn != nil {
		return m.GetSpecPricingFn(ctx, specID, cycle)
	}
	return nil, nil
}

func (m *MockProductRepo) DecrementStock(ctx context.Context, productID int64) error {
	if m.DecrementStockFn != nil {
		return m.DecrementStockFn(ctx, productID)
	}
	return nil
}

// MockCouponRepo mocks ports.CouponRepo.
type MockCouponRepo struct {
	CreateFn         func(ctx context.Context, c *domain.Coupon) error
	GetByIDFn        func(ctx context.Context, id int64) (*domain.Coupon, error)
	GetByCodeFn      func(ctx context.Context, code string) (*domain.Coupon, error)
	UpdateFn         func(ctx context.Context, c *domain.Coupon) error
	ListFn           func(ctx context.Context, p ports.ListParams) ([]domain.Coupon, int64, error)
	DeleteFn         func(ctx context.Context, id int64) error
	IncrementUsageFn func(ctx context.Context, id int64) error
}

func (m *MockCouponRepo) Create(ctx context.Context, c *domain.Coupon) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, c)
	}
	return nil
}

func (m *MockCouponRepo) GetByID(ctx context.Context, id int64) (*domain.Coupon, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockCouponRepo) GetByCode(ctx context.Context, code string) (*domain.Coupon, error) {
	if m.GetByCodeFn != nil {
		return m.GetByCodeFn(ctx, code)
	}
	return nil, nil
}

func (m *MockCouponRepo) Update(ctx context.Context, c *domain.Coupon) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, c)
	}
	return nil
}

func (m *MockCouponRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Coupon, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockCouponRepo) Delete(ctx context.Context, id int64) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, id)
	}
	return nil
}

func (m *MockCouponRepo) IncrementUsage(ctx context.Context, id int64) error {
	if m.IncrementUsageFn != nil {
		return m.IncrementUsageFn(ctx, id)
	}
	return nil
}

// MockOrderRepo mocks ports.OrderRepo.
type MockOrderRepo struct {
	CreateFn             func(ctx context.Context, o *domain.Order, items []domain.OrderItem) error
	GetByIDFn            func(ctx context.Context, id int64) (*domain.Order, error)
	GetItemsFn           func(ctx context.Context, orderID int64) ([]domain.OrderItem, error)
	UpdateStatusFn       func(ctx context.Context, id int64, status domain.OrderStatus) error
	UpdateItemBackRefsFn func(ctx context.Context, itemID int64, serviceID, domainID *int64) error
	ListFn               func(ctx context.Context, p ports.ListParams) ([]domain.Order, int64, error)
	ListByClientFn       func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Order, int64, error)
	NextNumberFn         func(ctx context.Context, scope string) (int64, error)
}

func (m *MockOrderRepo) Create(ctx context.Context, o *domain.Order, items []domain.OrderItem) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, o, items)
	}
	return nil
}

func (m *MockOrderRepo) GetByID(ctx context.Context, id int64) (*domain.Order, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockOrderRepo) GetItems(ctx context.Context, orderID int64) ([]domain.OrderItem, error) {
	if m.GetItemsFn != nil {
		return m.GetItemsFn(ctx, orderID)
	}
	return nil, nil
}

func (m *MockOrderRepo) UpdateStatus(ctx context.Context, id int64, status domain.OrderStatus) error {
	if m.UpdateStatusFn != nil {
		return m.UpdateStatusFn(ctx, id, status)
	}
	return nil
}

func (m *MockOrderRepo) UpdateItemBackRefs(ctx context.Context, itemID int64, serviceID, domainID *int64) error {
	if m.UpdateItemBackRefsFn != nil {
		return m.UpdateItemBackRefsFn(ctx, itemID, serviceID, domainID)
	}
	return nil
}

func (m *MockOrderRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Order, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockOrderRepo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Order, int64, error) {
	if m.ListByClientFn != nil {
		return m.ListByClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (m *MockOrderRepo) NextNumber(ctx context.Context, scope string) (int64, error) {
	if m.NextNumberFn != nil {
		return m.NextNumberFn(ctx, scope)
	}
	return 0, nil
}

// MockInvoiceRepo mocks ports.InvoiceRepo.
type MockInvoiceRepo struct {
	CreateFn               func(ctx context.Context, inv *domain.Invoice, items []domain.InvoiceItem) error
	GetByIDFn              func(ctx context.Context, id int64) (*domain.Invoice, error)
	GetByNumberFn          func(ctx context.Context, number string) (*domain.Invoice, error)
	GetByIDForUpdateFn     func(ctx context.Context, id int64) (*domain.Invoice, error)
	GetItemsFn             func(ctx context.Context, invoiceID int64) ([]domain.InvoiceItem, error)
	GetItemsByInvoiceIDsFn func(ctx context.Context, invoiceIDs []int64) (map[int64][]domain.InvoiceItem, error)
	AddItemFn              func(ctx context.Context, item *domain.InvoiceItem) error
	UpdateFn               func(ctx context.Context, inv *domain.Invoice) error
	UpdateStatusFn         func(ctx context.Context, id int64, status domain.InvoiceStatus, paidAt *time.Time) error
	SetPDFObjectKeyFn      func(ctx context.Context, id int64, key string) error
	ListFn                 func(ctx context.Context, p ports.ListParams) ([]domain.Invoice, int64, error)
	ListByClientFn         func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error)
	ListDueForStatusFn     func(ctx context.Context, status domain.InvoiceStatus, before time.Time) ([]domain.Invoice, error)
	NextNumberFn           func(ctx context.Context, scope string) (int64, error)
}

func (m *MockInvoiceRepo) Create(ctx context.Context, inv *domain.Invoice, items []domain.InvoiceItem) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, inv, items)
	}
	return nil
}

func (m *MockInvoiceRepo) GetByID(ctx context.Context, id int64) (*domain.Invoice, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockInvoiceRepo) GetByNumber(ctx context.Context, number string) (*domain.Invoice, error) {
	if m.GetByNumberFn != nil {
		return m.GetByNumberFn(ctx, number)
	}
	return nil, nil
}

func (m *MockInvoiceRepo) GetByIDForUpdate(ctx context.Context, id int64) (*domain.Invoice, error) {
	if m.GetByIDForUpdateFn != nil {
		return m.GetByIDForUpdateFn(ctx, id)
	}
	return nil, nil
}

func (m *MockInvoiceRepo) GetItems(ctx context.Context, invoiceID int64) ([]domain.InvoiceItem, error) {
	if m.GetItemsFn != nil {
		return m.GetItemsFn(ctx, invoiceID)
	}
	return nil, nil
}

func (m *MockInvoiceRepo) GetItemsByInvoiceIDs(ctx context.Context, invoiceIDs []int64) (map[int64][]domain.InvoiceItem, error) {
	if m.GetItemsByInvoiceIDsFn != nil {
		return m.GetItemsByInvoiceIDsFn(ctx, invoiceIDs)
	}
	return nil, nil
}

func (m *MockInvoiceRepo) AddItem(ctx context.Context, item *domain.InvoiceItem) error {
	if m.AddItemFn != nil {
		return m.AddItemFn(ctx, item)
	}
	return nil
}

func (m *MockInvoiceRepo) Update(ctx context.Context, inv *domain.Invoice) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, inv)
	}
	return nil
}

func (m *MockInvoiceRepo) UpdateStatus(ctx context.Context, id int64, status domain.InvoiceStatus, paidAt *time.Time) error {
	if m.UpdateStatusFn != nil {
		return m.UpdateStatusFn(ctx, id, status, paidAt)
	}
	return nil
}

func (m *MockInvoiceRepo) SetPDFObjectKey(ctx context.Context, id int64, key string) error {
	if m.SetPDFObjectKeyFn != nil {
		return m.SetPDFObjectKeyFn(ctx, id, key)
	}
	return nil
}

func (m *MockInvoiceRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Invoice, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockInvoiceRepo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error) {
	if m.ListByClientFn != nil {
		return m.ListByClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (m *MockInvoiceRepo) ListDueForStatus(ctx context.Context, status domain.InvoiceStatus, before time.Time) ([]domain.Invoice, error) {
	if m.ListDueForStatusFn != nil {
		return m.ListDueForStatusFn(ctx, status, before)
	}
	return nil, nil
}

func (m *MockInvoiceRepo) NextNumber(ctx context.Context, scope string) (int64, error) {
	if m.NextNumberFn != nil {
		return m.NextNumberFn(ctx, scope)
	}
	return 0, nil
}

// MockTransactionRepo mocks ports.TransactionRepo.
type MockTransactionRepo struct {
	CreateFn               func(ctx context.Context, t *domain.Transaction) error
	GetByIDFn              func(ctx context.Context, id int64) (*domain.Transaction, error)
	GetByMerchantOrderIDFn func(ctx context.Context, merchantOrderID string) (*domain.Transaction, error)
	UpdateFn               func(ctx context.Context, t *domain.Transaction) error
	ListByInvoiceFn        func(ctx context.Context, invoiceID int64) ([]domain.Transaction, error)
	ListPendingFn          func(ctx context.Context, gateway domain.Gateway, olderThan time.Time) ([]domain.Transaction, error)
	ListFn                 func(ctx context.Context, p ports.ListParams) ([]domain.Transaction, int64, error)
	ListForClientFn        func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Transaction, int64, error)
	NextAttemptFn          func(ctx context.Context, invoiceID int64) (int64, error)
}

func (m *MockTransactionRepo) Create(ctx context.Context, t *domain.Transaction) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, t)
	}
	return nil
}

func (m *MockTransactionRepo) GetByID(ctx context.Context, id int64) (*domain.Transaction, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockTransactionRepo) GetByMerchantOrderID(ctx context.Context, merchantOrderID string) (*domain.Transaction, error) {
	if m.GetByMerchantOrderIDFn != nil {
		return m.GetByMerchantOrderIDFn(ctx, merchantOrderID)
	}
	return nil, nil
}

func (m *MockTransactionRepo) Update(ctx context.Context, t *domain.Transaction) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, t)
	}
	return nil
}

func (m *MockTransactionRepo) ListByInvoice(ctx context.Context, invoiceID int64) ([]domain.Transaction, error) {
	if m.ListByInvoiceFn != nil {
		return m.ListByInvoiceFn(ctx, invoiceID)
	}
	return nil, nil
}

func (m *MockTransactionRepo) ListPending(ctx context.Context, gateway domain.Gateway, olderThan time.Time) ([]domain.Transaction, error) {
	if m.ListPendingFn != nil {
		return m.ListPendingFn(ctx, gateway, olderThan)
	}
	return nil, nil
}

func (m *MockTransactionRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Transaction, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockTransactionRepo) ListForClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Transaction, int64, error) {
	if m.ListForClientFn != nil {
		return m.ListForClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (m *MockTransactionRepo) NextAttempt(ctx context.Context, invoiceID int64) (int64, error) {
	if m.NextAttemptFn != nil {
		return m.NextAttemptFn(ctx, invoiceID)
	}
	return 0, nil
}

// MockCreditRepo mocks ports.CreditRepo.
type MockCreditRepo struct {
	ListByClientFn func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.CreditLedgerEntry, int64, error)
}

func (m *MockCreditRepo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.CreditLedgerEntry, int64, error) {
	if m.ListByClientFn != nil {
		return m.ListByClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

// MockServiceRepo mocks ports.ServiceRepo.
type MockServiceRepo struct {
	CreateFn           func(ctx context.Context, s *domain.Service) error
	GetByIDFn          func(ctx context.Context, id int64) (*domain.Service, error)
	GetByIDForUpdateFn func(ctx context.Context, id int64) (*domain.Service, error)
	GetByIDsFn         func(ctx context.Context, ids []int64) ([]domain.Service, error)
	UpdateFn           func(ctx context.Context, s *domain.Service) error
	UpdateStatusFn     func(ctx context.Context, id int64, status domain.ServiceStatus) error
	ListFn             func(ctx context.Context, p ports.ListParams) ([]domain.Service, int64, error)
	ListByClientFn     func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Service, int64, error)
	ListRenewalsDueFn  func(ctx context.Context, before time.Time) ([]domain.Service, error)
	CountByServerFn    func(ctx context.Context, serverID int64) (int64, error)
}

func (m *MockServiceRepo) Create(ctx context.Context, s *domain.Service) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, s)
	}
	return nil
}

func (m *MockServiceRepo) GetByID(ctx context.Context, id int64) (*domain.Service, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockServiceRepo) GetByIDForUpdate(ctx context.Context, id int64) (*domain.Service, error) {
	if m.GetByIDForUpdateFn != nil {
		return m.GetByIDForUpdateFn(ctx, id)
	}
	return nil, nil
}

func (m *MockServiceRepo) GetByIDs(ctx context.Context, ids []int64) ([]domain.Service, error) {
	if m.GetByIDsFn != nil {
		return m.GetByIDsFn(ctx, ids)
	}
	return nil, nil
}

func (m *MockServiceRepo) Update(ctx context.Context, s *domain.Service) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, s)
	}
	return nil
}

func (m *MockServiceRepo) UpdateStatus(ctx context.Context, id int64, status domain.ServiceStatus) error {
	if m.UpdateStatusFn != nil {
		return m.UpdateStatusFn(ctx, id, status)
	}
	return nil
}

func (m *MockServiceRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Service, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockServiceRepo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Service, int64, error) {
	if m.ListByClientFn != nil {
		return m.ListByClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (m *MockServiceRepo) ListRenewalsDue(ctx context.Context, before time.Time) ([]domain.Service, error) {
	if m.ListRenewalsDueFn != nil {
		return m.ListRenewalsDueFn(ctx, before)
	}
	return nil, nil
}

func (m *MockServiceRepo) CountByServer(ctx context.Context, serverID int64) (int64, error) {
	if m.CountByServerFn != nil {
		return m.CountByServerFn(ctx, serverID)
	}
	return 0, nil
}

// MockCancellationRequestRepo mocks ports.CancellationRequestRepo.
type MockCancellationRequestRepo struct {
	CreateFn              func(ctx context.Context, r *domain.CancellationRequest) error
	GetByIDFn             func(ctx context.Context, id int64) (*domain.CancellationRequest, error)
	GetPendingByServiceFn func(ctx context.Context, serviceID int64) (*domain.CancellationRequest, error)
	UpdateFn              func(ctx context.Context, r *domain.CancellationRequest) error
	ListFn                func(ctx context.Context, p ports.ListParams) ([]domain.CancellationRequest, int64, error)
}

func (m *MockCancellationRequestRepo) Create(ctx context.Context, r *domain.CancellationRequest) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, r)
	}
	return nil
}

func (m *MockCancellationRequestRepo) GetByID(ctx context.Context, id int64) (*domain.CancellationRequest, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockCancellationRequestRepo) GetPendingByService(ctx context.Context, serviceID int64) (*domain.CancellationRequest, error) {
	if m.GetPendingByServiceFn != nil {
		return m.GetPendingByServiceFn(ctx, serviceID)
	}
	return nil, nil
}

func (m *MockCancellationRequestRepo) Update(ctx context.Context, r *domain.CancellationRequest) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, r)
	}
	return nil
}

func (m *MockCancellationRequestRepo) List(ctx context.Context, p ports.ListParams) ([]domain.CancellationRequest, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

// MockServerRepo mocks ports.ServerRepo.
type MockServerRepo struct {
	CreateFn       func(ctx context.Context, s *domain.Server) error
	GetByIDFn      func(ctx context.Context, id int64) (*domain.Server, error)
	UpdateFn       func(ctx context.Context, s *domain.Server) error
	ListFn         func(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error)
	DeleteFn       func(ctx context.Context, id int64) error
	CreateGroupFn  func(ctx context.Context, g *domain.ServerGroup) error
	GetGroupByIDFn func(ctx context.Context, id int64) (*domain.ServerGroup, error)
	UpdateGroupFn  func(ctx context.Context, g *domain.ServerGroup) error
	ListGroupsFn   func(ctx context.Context) ([]domain.ServerGroup, error)
	DeleteGroupFn  func(ctx context.Context, id int64) error
	PickServerFn   func(ctx context.Context, groupID int64) (*domain.Server, error)
}

func (m *MockServerRepo) Create(ctx context.Context, s *domain.Server) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, s)
	}
	return nil
}

func (m *MockServerRepo) GetByID(ctx context.Context, id int64) (*domain.Server, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockServerRepo) Update(ctx context.Context, s *domain.Server) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, s)
	}
	return nil
}

func (m *MockServerRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Server, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockServerRepo) Delete(ctx context.Context, id int64) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, id)
	}
	return nil
}

func (m *MockServerRepo) CreateGroup(ctx context.Context, g *domain.ServerGroup) error {
	if m.CreateGroupFn != nil {
		return m.CreateGroupFn(ctx, g)
	}
	return nil
}

func (m *MockServerRepo) GetGroupByID(ctx context.Context, id int64) (*domain.ServerGroup, error) {
	if m.GetGroupByIDFn != nil {
		return m.GetGroupByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockServerRepo) UpdateGroup(ctx context.Context, g *domain.ServerGroup) error {
	if m.UpdateGroupFn != nil {
		return m.UpdateGroupFn(ctx, g)
	}
	return nil
}

func (m *MockServerRepo) ListGroups(ctx context.Context) ([]domain.ServerGroup, error) {
	if m.ListGroupsFn != nil {
		return m.ListGroupsFn(ctx)
	}
	return nil, nil
}

func (m *MockServerRepo) DeleteGroup(ctx context.Context, id int64) error {
	if m.DeleteGroupFn != nil {
		return m.DeleteGroupFn(ctx, id)
	}
	return nil
}

func (m *MockServerRepo) PickServer(ctx context.Context, groupID int64) (*domain.Server, error) {
	if m.PickServerFn != nil {
		return m.PickServerFn(ctx, groupID)
	}
	return nil, nil
}

// MockDomainRepo mocks ports.DomainRepo.
type MockDomainRepo struct {
	CreateFn           func(ctx context.Context, d *domain.Domain) error
	GetByIDFn          func(ctx context.Context, id int64) (*domain.Domain, error)
	GetByNameFn        func(ctx context.Context, name string) (*domain.Domain, error)
	GetByIDForUpdateFn func(ctx context.Context, id int64) (*domain.Domain, error)
	GetByIDsFn         func(ctx context.Context, ids []int64) ([]domain.Domain, error)
	UpdateFn           func(ctx context.Context, d *domain.Domain) error
	UpdateStatusFn     func(ctx context.Context, id int64, status domain.DomainStatus) error
	ListFn             func(ctx context.Context, p ports.ListParams) ([]domain.Domain, int64, error)
	ListByClientFn     func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Domain, int64, error)
	ListRenewalsDueFn  func(ctx context.Context, before time.Time) ([]domain.Domain, error)
	ListForSyncFn      func(ctx context.Context, limit int) ([]domain.Domain, error)
}

func (m *MockDomainRepo) Create(ctx context.Context, d *domain.Domain) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, d)
	}
	return nil
}

func (m *MockDomainRepo) GetByID(ctx context.Context, id int64) (*domain.Domain, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockDomainRepo) GetByName(ctx context.Context, name string) (*domain.Domain, error) {
	if m.GetByNameFn != nil {
		return m.GetByNameFn(ctx, name)
	}
	return nil, nil
}

func (m *MockDomainRepo) GetByIDForUpdate(ctx context.Context, id int64) (*domain.Domain, error) {
	if m.GetByIDForUpdateFn != nil {
		return m.GetByIDForUpdateFn(ctx, id)
	}
	return nil, nil
}

func (m *MockDomainRepo) GetByIDs(ctx context.Context, ids []int64) ([]domain.Domain, error) {
	if m.GetByIDsFn != nil {
		return m.GetByIDsFn(ctx, ids)
	}
	return nil, nil
}

func (m *MockDomainRepo) Update(ctx context.Context, d *domain.Domain) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, d)
	}
	return nil
}

func (m *MockDomainRepo) UpdateStatus(ctx context.Context, id int64, status domain.DomainStatus) error {
	if m.UpdateStatusFn != nil {
		return m.UpdateStatusFn(ctx, id, status)
	}
	return nil
}

func (m *MockDomainRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Domain, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockDomainRepo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Domain, int64, error) {
	if m.ListByClientFn != nil {
		return m.ListByClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (m *MockDomainRepo) ListRenewalsDue(ctx context.Context, before time.Time) ([]domain.Domain, error) {
	if m.ListRenewalsDueFn != nil {
		return m.ListRenewalsDueFn(ctx, before)
	}
	return nil, nil
}

func (m *MockDomainRepo) ListForSync(ctx context.Context, limit int) ([]domain.Domain, error) {
	if m.ListForSyncFn != nil {
		return m.ListForSyncFn(ctx, limit)
	}
	return nil, nil
}

// MockRegistrarRepo mocks ports.RegistrarRepo.
type MockRegistrarRepo struct {
	GetByIDFn   func(ctx context.Context, id int64) (*domain.Registrar, error)
	GetByNameFn func(ctx context.Context, name string) (*domain.Registrar, error)
	UpdateFn    func(ctx context.Context, r *domain.Registrar) error
	ListFn      func(ctx context.Context) ([]domain.Registrar, error)
}

func (m *MockRegistrarRepo) GetByID(ctx context.Context, id int64) (*domain.Registrar, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockRegistrarRepo) GetByName(ctx context.Context, name string) (*domain.Registrar, error) {
	if m.GetByNameFn != nil {
		return m.GetByNameFn(ctx, name)
	}
	return nil, nil
}

func (m *MockRegistrarRepo) Update(ctx context.Context, r *domain.Registrar) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, r)
	}
	return nil
}

func (m *MockRegistrarRepo) List(ctx context.Context) ([]domain.Registrar, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx)
	}
	return nil, nil
}

// MockTLDPricingRepo mocks ports.TLDPricingRepo.
type MockTLDPricingRepo struct {
	ListFn       func(ctx context.Context) ([]domain.TLDPricing, error)
	ListActiveFn func(ctx context.Context) ([]domain.TLDPricing, error)
	GetByIDFn    func(ctx context.Context, id int64) (*domain.TLDPricing, error)
	GetByTLDFn   func(ctx context.Context, tld string) (*domain.TLDPricing, error)
	CreateFn     func(ctx context.Context, p *domain.TLDPricing) error
	UpdateFn     func(ctx context.Context, p *domain.TLDPricing) error
	DeleteFn     func(ctx context.Context, id int64) error
}

func (m *MockTLDPricingRepo) List(ctx context.Context) ([]domain.TLDPricing, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx)
	}
	return nil, nil
}

func (m *MockTLDPricingRepo) ListActive(ctx context.Context) ([]domain.TLDPricing, error) {
	if m.ListActiveFn != nil {
		return m.ListActiveFn(ctx)
	}
	return nil, nil
}

func (m *MockTLDPricingRepo) GetByID(ctx context.Context, id int64) (*domain.TLDPricing, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockTLDPricingRepo) GetByTLD(ctx context.Context, tld string) (*domain.TLDPricing, error) {
	if m.GetByTLDFn != nil {
		return m.GetByTLDFn(ctx, tld)
	}
	return nil, nil
}

func (m *MockTLDPricingRepo) Create(ctx context.Context, p *domain.TLDPricing) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, p)
	}
	return nil
}

func (m *MockTLDPricingRepo) Update(ctx context.Context, p *domain.TLDPricing) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, p)
	}
	return nil
}

func (m *MockTLDPricingRepo) Delete(ctx context.Context, id int64) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, id)
	}
	return nil
}

// MockPremiumDomainPricingRepo mocks ports.PremiumDomainPricingRepo.
type MockPremiumDomainPricingRepo struct {
	ListFn      func(ctx context.Context) ([]domain.PremiumDomainPricing, error)
	GetByNameFn func(ctx context.Context, name string) (*domain.PremiumDomainPricing, error)
	CreateFn    func(ctx context.Context, p *domain.PremiumDomainPricing) error
	UpdateFn    func(ctx context.Context, p *domain.PremiumDomainPricing) error
	DeleteFn    func(ctx context.Context, id int64) error
}

func (m *MockPremiumDomainPricingRepo) List(ctx context.Context) ([]domain.PremiumDomainPricing, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx)
	}
	return nil, nil
}

func (m *MockPremiumDomainPricingRepo) GetByName(ctx context.Context, name string) (*domain.PremiumDomainPricing, error) {
	if m.GetByNameFn != nil {
		return m.GetByNameFn(ctx, name)
	}
	return nil, nil
}

func (m *MockPremiumDomainPricingRepo) Create(ctx context.Context, p *domain.PremiumDomainPricing) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, p)
	}
	return nil
}

func (m *MockPremiumDomainPricingRepo) Update(ctx context.Context, p *domain.PremiumDomainPricing) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, p)
	}
	return nil
}

func (m *MockPremiumDomainPricingRepo) Delete(ctx context.Context, id int64) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, id)
	}
	return nil
}

// MockPremiumLengthPricingRepo mocks ports.PremiumLengthPricingRepo.
type MockPremiumLengthPricingRepo struct {
	ListFn              func(ctx context.Context) ([]domain.PremiumLengthPricing, error)
	GetByTLDAndLengthFn func(ctx context.Context, tld string, charLength int) (*domain.PremiumLengthPricing, error)
	CreateFn            func(ctx context.Context, p *domain.PremiumLengthPricing) error
	UpdateFn            func(ctx context.Context, p *domain.PremiumLengthPricing) error
	DeleteFn            func(ctx context.Context, id int64) error
}

func (m *MockPremiumLengthPricingRepo) List(ctx context.Context) ([]domain.PremiumLengthPricing, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx)
	}
	return nil, nil
}

func (m *MockPremiumLengthPricingRepo) GetByTLDAndLength(ctx context.Context, tld string, charLength int) (*domain.PremiumLengthPricing, error) {
	if m.GetByTLDAndLengthFn != nil {
		return m.GetByTLDAndLengthFn(ctx, tld, charLength)
	}
	return nil, nil
}

func (m *MockPremiumLengthPricingRepo) Create(ctx context.Context, p *domain.PremiumLengthPricing) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, p)
	}
	return nil
}

func (m *MockPremiumLengthPricingRepo) Update(ctx context.Context, p *domain.PremiumLengthPricing) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, p)
	}
	return nil
}

func (m *MockPremiumLengthPricingRepo) Delete(ctx context.Context, id int64) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, id)
	}
	return nil
}

// MockDomainAddonRepo mocks ports.DomainAddonRepo.
type MockDomainAddonRepo struct {
	ListFn       func(ctx context.Context) ([]domain.DomainAddon, error)
	ListActiveFn func(ctx context.Context) ([]domain.DomainAddon, error)
	GetByKeyFn   func(ctx context.Context, key string) (*domain.DomainAddon, error)
	UpdateFn     func(ctx context.Context, a *domain.DomainAddon) error
}

func (m *MockDomainAddonRepo) List(ctx context.Context) ([]domain.DomainAddon, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx)
	}
	return nil, nil
}

func (m *MockDomainAddonRepo) ListActive(ctx context.Context) ([]domain.DomainAddon, error) {
	if m.ListActiveFn != nil {
		return m.ListActiveFn(ctx)
	}
	return nil, nil
}

func (m *MockDomainAddonRepo) GetByKey(ctx context.Context, key string) (*domain.DomainAddon, error) {
	if m.GetByKeyFn != nil {
		return m.GetByKeyFn(ctx, key)
	}
	return nil, nil
}

func (m *MockDomainAddonRepo) Update(ctx context.Context, a *domain.DomainAddon) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, a)
	}
	return nil
}

// MockTicketRepo mocks ports.TicketRepo.
type MockTicketRepo struct {
	CreateFn            func(ctx context.Context, t *domain.Ticket) error
	GetByIDFn           func(ctx context.Context, id int64) (*domain.Ticket, error)
	UpdateFn            func(ctx context.Context, t *domain.Ticket) error
	ListFn              func(ctx context.Context, p ports.ListParams) ([]domain.Ticket, int64, error)
	ListByClientFn      func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Ticket, int64, error)
	NextNumberFn        func(ctx context.Context, scope string) (int64, error)
	AddReplyFn          func(ctx context.Context, r *domain.TicketReply) error
	ListRepliesFn       func(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.TicketReply, error)
	CreateDepartmentFn  func(ctx context.Context, d *domain.TicketDepartment) error
	GetDepartmentByIDFn func(ctx context.Context, id int64) (*domain.TicketDepartment, error)
	UpdateDepartmentFn  func(ctx context.Context, d *domain.TicketDepartment) error
	ListDepartmentsFn   func(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error)
	DeleteDepartmentFn  func(ctx context.Context, id int64) error
}

func (m *MockTicketRepo) Create(ctx context.Context, t *domain.Ticket) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, t)
	}
	return nil
}

func (m *MockTicketRepo) GetByID(ctx context.Context, id int64) (*domain.Ticket, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockTicketRepo) Update(ctx context.Context, t *domain.Ticket) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, t)
	}
	return nil
}

func (m *MockTicketRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Ticket, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockTicketRepo) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Ticket, int64, error) {
	if m.ListByClientFn != nil {
		return m.ListByClientFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (m *MockTicketRepo) NextNumber(ctx context.Context, scope string) (int64, error) {
	if m.NextNumberFn != nil {
		return m.NextNumberFn(ctx, scope)
	}
	return 0, nil
}

func (m *MockTicketRepo) AddReply(ctx context.Context, r *domain.TicketReply) error {
	if m.AddReplyFn != nil {
		return m.AddReplyFn(ctx, r)
	}
	return nil
}

func (m *MockTicketRepo) ListReplies(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.TicketReply, error) {
	if m.ListRepliesFn != nil {
		return m.ListRepliesFn(ctx, ticketID, includeInternal)
	}
	return nil, nil
}

func (m *MockTicketRepo) CreateDepartment(ctx context.Context, d *domain.TicketDepartment) error {
	if m.CreateDepartmentFn != nil {
		return m.CreateDepartmentFn(ctx, d)
	}
	return nil
}

func (m *MockTicketRepo) GetDepartmentByID(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
	if m.GetDepartmentByIDFn != nil {
		return m.GetDepartmentByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockTicketRepo) UpdateDepartment(ctx context.Context, d *domain.TicketDepartment) error {
	if m.UpdateDepartmentFn != nil {
		return m.UpdateDepartmentFn(ctx, d)
	}
	return nil
}

func (m *MockTicketRepo) ListDepartments(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error) {
	if m.ListDepartmentsFn != nil {
		return m.ListDepartmentsFn(ctx, activeOnly)
	}
	return nil, nil
}

func (m *MockTicketRepo) DeleteDepartment(ctx context.Context, id int64) error {
	if m.DeleteDepartmentFn != nil {
		return m.DeleteDepartmentFn(ctx, id)
	}
	return nil
}

// MockEmailTemplateRepo mocks ports.EmailTemplateRepo.
type MockEmailTemplateRepo struct {
	GetFn    func(ctx context.Context, key, locale string) (*domain.EmailTemplate, error)
	UpsertFn func(ctx context.Context, t *domain.EmailTemplate) error
	ListFn   func(ctx context.Context) ([]domain.EmailTemplate, error)
	DeleteFn func(ctx context.Context, key, locale string) error
}

func (m *MockEmailTemplateRepo) Get(ctx context.Context, key, locale string) (*domain.EmailTemplate, error) {
	if m.GetFn != nil {
		return m.GetFn(ctx, key, locale)
	}
	return nil, nil
}

func (m *MockEmailTemplateRepo) Upsert(ctx context.Context, t *domain.EmailTemplate) error {
	if m.UpsertFn != nil {
		return m.UpsertFn(ctx, t)
	}
	return nil
}

func (m *MockEmailTemplateRepo) List(ctx context.Context) ([]domain.EmailTemplate, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx)
	}
	return nil, nil
}

func (m *MockEmailTemplateRepo) Delete(ctx context.Context, key, locale string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, key, locale)
	}
	return nil
}

// MockEmailLogRepo mocks ports.EmailLogRepo.
type MockEmailLogRepo struct {
	CreateFn     func(ctx context.Context, e *domain.EmailLogEntry) error
	GetByIDFn    func(ctx context.Context, id int64) (*domain.EmailLogEntry, error)
	MarkSentFn   func(ctx context.Context, id int64, at time.Time) error
	MarkFailedFn func(ctx context.Context, id int64, errMsg string) error
	ListFn       func(ctx context.Context, p ports.ListParams) ([]domain.EmailLogEntry, int64, error)
}

func (m *MockEmailLogRepo) Create(ctx context.Context, e *domain.EmailLogEntry) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, e)
	}
	return nil
}

func (m *MockEmailLogRepo) GetByID(ctx context.Context, id int64) (*domain.EmailLogEntry, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockEmailLogRepo) MarkSent(ctx context.Context, id int64, at time.Time) error {
	if m.MarkSentFn != nil {
		return m.MarkSentFn(ctx, id, at)
	}
	return nil
}

func (m *MockEmailLogRepo) MarkFailed(ctx context.Context, id int64, errMsg string) error {
	if m.MarkFailedFn != nil {
		return m.MarkFailedFn(ctx, id, errMsg)
	}
	return nil
}

func (m *MockEmailLogRepo) List(ctx context.Context, p ports.ListParams) ([]domain.EmailLogEntry, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

// MockSettingsRepo mocks ports.SettingsRepo. Nil getters return the default.
type MockSettingsRepo struct {
	GetStringFn func(ctx context.Context, key, def string) (string, error)
	GetIntFn    func(ctx context.Context, key string, def int) (int, error)
	GetBoolFn   func(ctx context.Context, key string, def bool) (bool, error)
	GetJSONFn   func(ctx context.Context, key string, out any) error
	SetFn       func(ctx context.Context, key string, value any) error
	AllFn       func(ctx context.Context) ([]domain.Setting, error)
}

func (m *MockSettingsRepo) GetString(ctx context.Context, key, def string) (string, error) {
	if m.GetStringFn != nil {
		return m.GetStringFn(ctx, key, def)
	}
	return def, nil
}

func (m *MockSettingsRepo) GetInt(ctx context.Context, key string, def int) (int, error) {
	if m.GetIntFn != nil {
		return m.GetIntFn(ctx, key, def)
	}
	return def, nil
}

func (m *MockSettingsRepo) GetBool(ctx context.Context, key string, def bool) (bool, error) {
	if m.GetBoolFn != nil {
		return m.GetBoolFn(ctx, key, def)
	}
	return def, nil
}

func (m *MockSettingsRepo) GetJSON(ctx context.Context, key string, out any) error {
	if m.GetJSONFn != nil {
		return m.GetJSONFn(ctx, key, out)
	}
	return nil
}

func (m *MockSettingsRepo) Set(ctx context.Context, key string, value any) error {
	if m.SetFn != nil {
		return m.SetFn(ctx, key, value)
	}
	return nil
}

func (m *MockSettingsRepo) All(ctx context.Context) ([]domain.Setting, error) {
	if m.AllFn != nil {
		return m.AllFn(ctx)
	}
	return nil, nil
}

// MockAuditRepo mocks ports.AuditRepo.
type MockAuditRepo struct {
	CreateFn func(ctx context.Context, a *domain.AuditLog) error
	ListFn   func(ctx context.Context, p ports.ListParams) ([]domain.AuditLog, int64, error)
}

func (m *MockAuditRepo) Create(ctx context.Context, a *domain.AuditLog) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, a)
	}
	return nil
}

func (m *MockAuditRepo) List(ctx context.Context, p ports.ListParams) ([]domain.AuditLog, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

// MockIntegrationLogRepo mocks ports.IntegrationLogRepo.
type MockIntegrationLogRepo struct {
	CreateFn func(ctx context.Context, l *domain.IntegrationLog) error
	ListFn   func(ctx context.Context, p ports.ListParams) ([]domain.IntegrationLog, int64, error)
}

func (m *MockIntegrationLogRepo) Create(ctx context.Context, l *domain.IntegrationLog) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, l)
	}
	return nil
}

func (m *MockIntegrationLogRepo) List(ctx context.Context, p ports.ListParams) ([]domain.IntegrationLog, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

// MockDashboardRepo mocks ports.DashboardRepo.
type MockDashboardRepo struct {
	StatsFn          func(ctx context.Context) (*ports.DashboardStats, error)
	RevenueFn        func(ctx context.Context, from, to time.Time, groupBy string) ([]ports.RevenuePoint, error)
	OrdersReportFn   func(ctx context.Context, from, to time.Time) ([]ports.RevenuePoint, error)
	ServicesReportFn func(ctx context.Context) (map[string]int64, error)
}

func (m *MockDashboardRepo) Stats(ctx context.Context) (*ports.DashboardStats, error) {
	if m.StatsFn != nil {
		return m.StatsFn(ctx)
	}
	return nil, nil
}

func (m *MockDashboardRepo) Revenue(ctx context.Context, from, to time.Time, groupBy string) ([]ports.RevenuePoint, error) {
	if m.RevenueFn != nil {
		return m.RevenueFn(ctx, from, to, groupBy)
	}
	return nil, nil
}

func (m *MockDashboardRepo) OrdersReport(ctx context.Context, from, to time.Time) ([]ports.RevenuePoint, error) {
	if m.OrdersReportFn != nil {
		return m.OrdersReportFn(ctx, from, to)
	}
	return nil, nil
}

func (m *MockDashboardRepo) ServicesReport(ctx context.Context) (map[string]int64, error) {
	if m.ServicesReportFn != nil {
		return m.ServicesReportFn(ctx)
	}
	return nil, nil
}

// MockAnnouncementRepo mocks ports.AnnouncementRepo.
type MockAnnouncementRepo struct {
	CreateFn        func(ctx context.Context, a *domain.Announcement) error
	GetByIDFn       func(ctx context.Context, id int64) (*domain.Announcement, error)
	GetBySlugFn     func(ctx context.Context, slug string) (*domain.Announcement, error)
	UpdateFn        func(ctx context.Context, a *domain.Announcement) error
	ListFn          func(ctx context.Context, p ports.ListParams) ([]domain.Announcement, int64, error)
	ListPublishedFn func(ctx context.Context, p ports.ListParams) ([]domain.Announcement, int64, error)
	SoftDeleteFn    func(ctx context.Context, id int64) error
}

func (m *MockAnnouncementRepo) Create(ctx context.Context, a *domain.Announcement) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, a)
	}
	return nil
}

func (m *MockAnnouncementRepo) GetByID(ctx context.Context, id int64) (*domain.Announcement, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockAnnouncementRepo) GetBySlug(ctx context.Context, slug string) (*domain.Announcement, error) {
	if m.GetBySlugFn != nil {
		return m.GetBySlugFn(ctx, slug)
	}
	return nil, nil
}

func (m *MockAnnouncementRepo) Update(ctx context.Context, a *domain.Announcement) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, a)
	}
	return nil
}

func (m *MockAnnouncementRepo) List(ctx context.Context, p ports.ListParams) ([]domain.Announcement, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockAnnouncementRepo) ListPublished(ctx context.Context, p ports.ListParams) ([]domain.Announcement, int64, error) {
	if m.ListPublishedFn != nil {
		return m.ListPublishedFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockAnnouncementRepo) SoftDelete(ctx context.Context, id int64) error {
	if m.SoftDeleteFn != nil {
		return m.SoftDeleteFn(ctx, id)
	}
	return nil
}

// MockKnowledgebaseRepo mocks ports.KnowledgebaseRepo.
type MockKnowledgebaseRepo struct {
	CreateCategoryFn          func(ctx context.Context, c *domain.KBCategory) error
	GetCategoryByIDFn         func(ctx context.Context, id int64) (*domain.KBCategory, error)
	GetCategoryBySlugFn       func(ctx context.Context, slug string) (*domain.KBCategory, error)
	UpdateCategoryFn          func(ctx context.Context, c *domain.KBCategory) error
	ListCategoriesFn          func(ctx context.Context, includeHidden bool) ([]domain.KBCategory, error)
	SoftDeleteCategoryFn      func(ctx context.Context, id int64) error
	CountArticlesInCategoryFn func(ctx context.Context, categoryID int64) (int64, error)
	CreateArticleFn           func(ctx context.Context, a *domain.KBArticle) error
	GetArticleByIDFn          func(ctx context.Context, id int64) (*domain.KBArticle, error)
	GetArticleBySlugFn        func(ctx context.Context, slug string) (*domain.KBArticle, error)
	UpdateArticleFn           func(ctx context.Context, a *domain.KBArticle) error
	ListFn                    func(ctx context.Context, p ports.ListParams) ([]domain.KBArticle, int64, error)
	ListPublishedFn           func(ctx context.Context, categoryID int64, p ports.ListParams) ([]domain.KBArticle, int64, error)
	SoftDeleteArticleFn       func(ctx context.Context, id int64) error
	IncrementViewsFn          func(ctx context.Context, id int64) error
}

func (m *MockKnowledgebaseRepo) CreateCategory(ctx context.Context, c *domain.KBCategory) error {
	if m.CreateCategoryFn != nil {
		return m.CreateCategoryFn(ctx, c)
	}
	return nil
}

func (m *MockKnowledgebaseRepo) GetCategoryByID(ctx context.Context, id int64) (*domain.KBCategory, error) {
	if m.GetCategoryByIDFn != nil {
		return m.GetCategoryByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockKnowledgebaseRepo) GetCategoryBySlug(ctx context.Context, slug string) (*domain.KBCategory, error) {
	if m.GetCategoryBySlugFn != nil {
		return m.GetCategoryBySlugFn(ctx, slug)
	}
	return nil, nil
}

func (m *MockKnowledgebaseRepo) UpdateCategory(ctx context.Context, c *domain.KBCategory) error {
	if m.UpdateCategoryFn != nil {
		return m.UpdateCategoryFn(ctx, c)
	}
	return nil
}

func (m *MockKnowledgebaseRepo) ListCategories(ctx context.Context, includeHidden bool) ([]domain.KBCategory, error) {
	if m.ListCategoriesFn != nil {
		return m.ListCategoriesFn(ctx, includeHidden)
	}
	return nil, nil
}

func (m *MockKnowledgebaseRepo) SoftDeleteCategory(ctx context.Context, id int64) error {
	if m.SoftDeleteCategoryFn != nil {
		return m.SoftDeleteCategoryFn(ctx, id)
	}
	return nil
}

func (m *MockKnowledgebaseRepo) CountArticlesInCategory(ctx context.Context, categoryID int64) (int64, error) {
	if m.CountArticlesInCategoryFn != nil {
		return m.CountArticlesInCategoryFn(ctx, categoryID)
	}
	return 0, nil
}

func (m *MockKnowledgebaseRepo) CreateArticle(ctx context.Context, a *domain.KBArticle) error {
	if m.CreateArticleFn != nil {
		return m.CreateArticleFn(ctx, a)
	}
	return nil
}

func (m *MockKnowledgebaseRepo) GetArticleByID(ctx context.Context, id int64) (*domain.KBArticle, error) {
	if m.GetArticleByIDFn != nil {
		return m.GetArticleByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockKnowledgebaseRepo) GetArticleBySlug(ctx context.Context, slug string) (*domain.KBArticle, error) {
	if m.GetArticleBySlugFn != nil {
		return m.GetArticleBySlugFn(ctx, slug)
	}
	return nil, nil
}

func (m *MockKnowledgebaseRepo) UpdateArticle(ctx context.Context, a *domain.KBArticle) error {
	if m.UpdateArticleFn != nil {
		return m.UpdateArticleFn(ctx, a)
	}
	return nil
}

func (m *MockKnowledgebaseRepo) List(ctx context.Context, p ports.ListParams) ([]domain.KBArticle, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockKnowledgebaseRepo) ListPublished(ctx context.Context, categoryID int64, p ports.ListParams) ([]domain.KBArticle, int64, error) {
	if m.ListPublishedFn != nil {
		return m.ListPublishedFn(ctx, categoryID, p)
	}
	return nil, 0, nil
}

func (m *MockKnowledgebaseRepo) SoftDeleteArticle(ctx context.Context, id int64) error {
	if m.SoftDeleteArticleFn != nil {
		return m.SoftDeleteArticleFn(ctx, id)
	}
	return nil
}

func (m *MockKnowledgebaseRepo) IncrementViews(ctx context.Context, id int64) error {
	if m.IncrementViewsFn != nil {
		return m.IncrementViewsFn(ctx, id)
	}
	return nil
}

// MockNetworkStatusRepo mocks ports.NetworkStatusRepo.
type MockNetworkStatusRepo struct {
	CreateFn     func(ctx context.Context, n *domain.NetworkIssue) error
	GetByIDFn    func(ctx context.Context, id int64) (*domain.NetworkIssue, error)
	UpdateFn     func(ctx context.Context, n *domain.NetworkIssue) error
	ListFn       func(ctx context.Context, p ports.ListParams) ([]domain.NetworkIssue, int64, error)
	ListActiveFn func(ctx context.Context) ([]domain.NetworkIssue, error)
	SoftDeleteFn func(ctx context.Context, id int64) error
}

func (m *MockNetworkStatusRepo) Create(ctx context.Context, n *domain.NetworkIssue) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, n)
	}
	return nil
}

func (m *MockNetworkStatusRepo) GetByID(ctx context.Context, id int64) (*domain.NetworkIssue, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *MockNetworkStatusRepo) Update(ctx context.Context, n *domain.NetworkIssue) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, n)
	}
	return nil
}

func (m *MockNetworkStatusRepo) List(ctx context.Context, p ports.ListParams) ([]domain.NetworkIssue, int64, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, p)
	}
	return nil, 0, nil
}

func (m *MockNetworkStatusRepo) ListActive(ctx context.Context) ([]domain.NetworkIssue, error) {
	if m.ListActiveFn != nil {
		return m.ListActiveFn(ctx)
	}
	return nil, nil
}

func (m *MockNetworkStatusRepo) SoftDelete(ctx context.Context, id int64) error {
	if m.SoftDeleteFn != nil {
		return m.SoftDeleteFn(ctx, id)
	}
	return nil
}

// MockGuestTicketCreator mocks ports.GuestTicketCreator.
type MockGuestTicketCreator struct {
	CreateGuestTicketFn func(ctx context.Context, in ports.GuestTicketInput) (*domain.Ticket, error)
}

func (m *MockGuestTicketCreator) CreateGuestTicket(ctx context.Context, in ports.GuestTicketInput) (*domain.Ticket, error) {
	if m.CreateGuestTicketFn != nil {
		return m.CreateGuestTicketFn(ctx, in)
	}
	return nil, nil
}
