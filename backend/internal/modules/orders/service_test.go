package orders_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/orders"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test fakes for module-local interfaces

// fakeOrderStore adds the module-local extras to the shared OrderRepo mock.
type fakeOrderStore struct {
	*mocks.MockOrderRepo
	CountByClientSinceFn func(ctx context.Context, clientID int64, since time.Time) (int64, error)
	InvoiceIDForOrderFn  func(ctx context.Context, orderID int64) (int64, error)
}

func (f *fakeOrderStore) CountByClientSince(ctx context.Context, clientID int64, since time.Time) (int64, error) {
	if f.CountByClientSinceFn != nil {
		return f.CountByClientSinceFn(ctx, clientID, since)
	}
	return 0, nil
}

func (f *fakeOrderStore) InvoiceIDForOrder(ctx context.Context, orderID int64) (int64, error) {
	if f.InvoiceIDForOrderFn != nil {
		return f.InvoiceIDForOrderFn(ctx, orderID)
	}
	return 0, nil
}

// fakeStock records stock restores.
type fakeStock struct {
	Restored []int64
	Err      error
}

func (f *fakeStock) RestoreStock(_ context.Context, productID int64) error {
	if f.Err != nil {
		return f.Err
	}
	f.Restored = append(f.Restored, productID)
	return nil
}

// Fixture builder

var testNow = time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)

type testEnv struct {
	deps                 orders.Deps
	orderRepo            *mocks.MockOrderRepo
	store                *fakeOrderStore
	products             *mocks.MockProductRepo
	stock                *fakeStock
	coupons              *mocks.MockCouponRepo
	clients              *mocks.MockClientRepo
	users                *mocks.MockUserRepo
	services             *mocks.MockServiceRepo
	domains              *mocks.MockDomainRepo
	regRepo              *mocks.MockRegistrarRepo
	tldPricing           *mocks.MockTLDPricingRepo
	premiumPricing       *mocks.MockPremiumDomainPricingRepo
	premiumLengthPricing *mocks.MockPremiumLengthPricingRepo
	domainAddons         *mocks.MockDomainAddonRepo
	registrar            *mocks.MockRegistrarModule
	invoices             *mocks.MockInvoiceCreator
	invStore             *mocks.MockInvoiceRepo
	payments             *mocks.MockPaymentApplier
	settings             *mocks.MockSettingsRepo
	enqueuer             *mocks.MockEnqueuer
	notifier             *mocks.MockNotificationSender
	audit                *mocks.MockAuditLogger

	decremented []int64
	usageBumped []int64
	created     []*domain.Service
	domCreated  []*domain.Domain
	backrefs    map[int64][2]*int64 // itemID -> [serviceID, domainID]
	statusSet   []domain.OrderStatus
	invStatus   []domain.InvoiceStatus
}

func verifiedAt(t time.Time) *time.Time { return &t }

func newFixture() *testEnv {
	e := &testEnv{
		orderRepo:            &mocks.MockOrderRepo{},
		products:             &mocks.MockProductRepo{},
		stock:                &fakeStock{},
		coupons:              &mocks.MockCouponRepo{},
		clients:              &mocks.MockClientRepo{},
		users:                &mocks.MockUserRepo{},
		services:             &mocks.MockServiceRepo{},
		domains:              &mocks.MockDomainRepo{},
		regRepo:              &mocks.MockRegistrarRepo{},
		tldPricing:           &mocks.MockTLDPricingRepo{},
		premiumPricing:       &mocks.MockPremiumDomainPricingRepo{},
		premiumLengthPricing: &mocks.MockPremiumLengthPricingRepo{},
		domainAddons:         &mocks.MockDomainAddonRepo{},
		registrar:            &mocks.MockRegistrarModule{},
		invoices:             &mocks.MockInvoiceCreator{},
		invStore:             &mocks.MockInvoiceRepo{},
		payments:             &mocks.MockPaymentApplier{},
		settings:             &mocks.MockSettingsRepo{},
		enqueuer:             &mocks.MockEnqueuer{},
		notifier:             &mocks.MockNotificationSender{},
		audit:                &mocks.MockAuditLogger{},
		backrefs:             map[int64][2]*int64{},
	}
	e.store = &fakeOrderStore{MockOrderRepo: e.orderRepo}

	// Default fixtures ------------------------------------------------------
	e.clients.GetByIDFn = func(_ context.Context, id int64) (*domain.Client, error) {
		return &domain.Client{
			ID: id, UserID: 3, FirstName: "Budi", LastName: "Santoso",
			Address1: "Jl. Melati 1", City: "Lamongan", State: "Jawa Timur", Postcode: "62211",
			Phone: "081234567890",
		}, nil
	}
	e.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
		return &domain.User{ID: id, Email: "budi@example.com", EmailVerifiedAt: verifiedAt(testNow)}, nil
	}
	e.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		if id != 10 {
			return nil, apperr.NotFound("product")
		}
		return &domain.Product{
			ID: 10, Name: "Basic Hosting", Type: domain.ProductSharedHosting,
			Module: domain.ModuleCpanel, AutoSetup: domain.SetupOnPayment,
			StockEnabled: true, StockQty: 5,
		}, nil
	}
	e.products.GetPricingFn = func(_ context.Context, productID int64, cycle domain.BillingCycle) (*domain.ProductPricing, error) {
		if productID == 10 && cycle == domain.CycleMonthly {
			return &domain.ProductPricing{ProductID: productID, Cycle: cycle, Price: 50000, SetupFee: 10000}, nil
		}
		return nil, apperr.NotFound("pricing")
	}
	e.products.DecrementStockFn = func(_ context.Context, id int64) error {
		e.decremented = append(e.decremented, id)
		return nil
	}
	e.coupons.IncrementUsageFn = func(_ context.Context, id int64) error {
		e.usageBumped = append(e.usageBumped, id)
		return nil
	}
	e.orderRepo.NextNumberFn = func(_ context.Context, scope string) (int64, error) { return 42, nil }
	e.orderRepo.CreateFn = func(_ context.Context, o *domain.Order, items []domain.OrderItem) error {
		o.ID = 100
		for i := range items {
			items[i].OrderID = o.ID
			items[i].ID = int64(200 + i)
		}
		return nil
	}
	e.orderRepo.UpdateStatusFn = func(_ context.Context, _ int64, st domain.OrderStatus) error {
		e.statusSet = append(e.statusSet, st)
		return nil
	}
	e.orderRepo.UpdateItemBackRefsFn = func(_ context.Context, itemID int64, sid, did *int64) error {
		e.backrefs[itemID] = [2]*int64{sid, did}
		return nil
	}
	e.invoices.CreateInvoiceFn = func(_ context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
		var sub int64
		for _, it := range in.Items {
			sub += it.Amount
		}
		return &domain.Invoice{
			ID: 500, ClientID: in.ClientID, Status: domain.InvoiceUnpaid,
			Subtotal: sub, Discount: in.Discount, Total: sub - in.Discount,
			DueDate: in.DueDate,
		}, nil
	}
	e.invStore.UpdateStatusFn = func(_ context.Context, _ int64, st domain.InvoiceStatus, _ *time.Time) error {
		e.invStatus = append(e.invStatus, st)
		return nil
	}
	e.services.CreateFn = func(_ context.Context, s *domain.Service) error {
		s.ID = int64(700 + len(e.created))
		e.created = append(e.created, s)
		return nil
	}
	e.domains.CreateFn = func(_ context.Context, d *domain.Domain) error {
		d.ID = int64(800 + len(e.domCreated))
		e.domCreated = append(e.domCreated, d)
		return nil
	}
	e.regRepo.GetByNameFn = func(_ context.Context, name string) (*domain.Registrar, error) {
		return &domain.Registrar{ID: 1, Name: name, Active: true}, nil
	}
	e.registrar.CheckAvailabilityFn = func(_ context.Context, names []string) ([]ports.DomainAvailability, error) {
		out := make([]ports.DomainAvailability, 0, len(names))
		for _, n := range names {
			out = append(out, ports.DomainAvailability{Name: n, Available: true, Price: 150000})
		}
		return out, nil
	}

	e.deps = orders.Deps{
		Tx:                   &mocks.MockTxManager{},
		Orders:               e.store,
		Products:             e.products,
		Stock:                e.stock,
		Coupons:              e.coupons,
		Clients:              e.clients,
		Users:                e.users,
		Services:             e.services,
		Domains:              e.domains,
		Registrars:           e.regRepo,
		TLDPricing:           e.tldPricing,
		PremiumPricing:       e.premiumPricing,
		PremiumLengthPricing: e.premiumLengthPricing,
		DomainAddons:         e.domainAddons,
		Registrar:            e.registrar,
		Invoices:             e.invoices,
		InvoiceSt:            e.invStore,
		Payments:             e.payments,
		Settings:             e.settings,
		Enqueuer:             e.enqueuer,
		Notifier:             e.notifier,
		Encryptor:            &mocks.MockEncryptor{EncryptFn: func(p string) (string, error) { return "enc:" + p, nil }},
		Audit:                e.audit,
		Clock:                &mocks.MockClock{FixedTime: testNow},
	}
	return e
}

func productItem() orders.OrderItemRequest {
	return orders.OrderItemRequest{
		ItemType: "product", ProductID: 10, Cycle: "monthly", Domain: "budi.example.com",
	}
}

// CreateOrder

func TestCreateOrderHappyPath(t *testing.T) {
	e := newFixture()
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "10.0.0.1",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}, Notes: "hi"})
	require.NoError(t, err)

	assert.Equal(t, "ORD-202607-000042", res.Order.OrderNumber)
	assert.Equal(t, domain.OrderPending, res.Order.Status)
	assert.Equal(t, int64(60000), res.Order.Subtotal) // 50000 + 10000 setup
	assert.Equal(t, int64(0), res.Order.Discount)
	assert.Equal(t, int64(0), res.Order.TaxTotal) // tax disabled by default
	assert.Equal(t, int64(60000), res.Order.Total)
	assert.Equal(t, "10.0.0.1", res.Order.IP)
	assert.Equal(t, "hi", res.Order.Notes)

	require.Len(t, res.Items, 1)
	assert.Equal(t, int64(50000), res.Items[0].UnitPrice)
	assert.Equal(t, int64(10000), res.Items[0].SetupFee)
	assert.Equal(t, "budi.example.com", res.Items[0].Domain)

	require.NotNil(t, res.Invoice)
	assert.Equal(t, domain.InvoiceUnpaid, res.Invoice.Status)
	assert.Equal(t, testNow.AddDate(0, 0, 3), res.Invoice.DueDate) // billing.invoice_due_days default 3

	assert.Equal(t, []int64{10}, e.decremented, "stock decremented once")
	assert.Empty(t, e.usageBumped)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "order.create", e.audit.Entries[0].Action)
	assert.Equal(t, res.Order.ID, e.audit.Entries[0].EntityID)
}

func TestCreateOrderRequiresVerifiedEmail(t *testing.T) {
	e := newFixture()
	e.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
		return &domain.User{ID: id, Email: "budi@example.com"}, nil // not verified
	}
	svc := orders.New(e.deps)

	_, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeForbidden, apperr.From(err).Code)
	assert.Empty(t, e.decremented)
}

func TestCreateOrderSkipsVerificationWhenSettingDisabled(t *testing.T) {
	e := newFixture()
	e.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
		return &domain.User{ID: id, Email: "budi@example.com"}, nil // not verified
	}
	e.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
		if key == "security.require_email_verification" {
			return false, nil
		}
		return def, nil
	}
	svc := orders.New(e.deps)

	_, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err)
}

func TestCreateOrderValidatesDTO(t *testing.T) {
	e := newFixture()
	svc := orders.New(e.deps)

	_, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)

	_, err = svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items: []orders.OrderItemRequest{{ItemType: "bogus"}},
	})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)
}

func TestCreateOrderItemValidation(t *testing.T) {
	tests := []struct {
		name  string
		mut   func(e *testEnv, it *orders.OrderItemRequest)
		field string
	}{
		{"unknown product", func(e *testEnv, it *orders.OrderItemRequest) { it.ProductID = 99 }, "items[0].product_id"},
		{"missing product id", func(e *testEnv, it *orders.OrderItemRequest) { it.ProductID = 0 }, "items[0].product_id"},
		{"hidden product", func(e *testEnv, it *orders.OrderItemRequest) {
			e.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
				return &domain.Product{ID: id, Hidden: true}, nil
			}
		}, "items[0].product_id"},
		{"deleted product", func(e *testEnv, it *orders.OrderItemRequest) {
			e.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
				del := testNow
				return &domain.Product{ID: id, DeletedAt: &del}, nil
			}
		}, "items[0].product_id"},
		{"unpriced cycle", func(e *testEnv, it *orders.OrderItemRequest) { it.Cycle = "annually" }, "items[0].cycle"},
		{"missing cycle", func(e *testEnv, it *orders.OrderItemRequest) { it.Cycle = "" }, "items[0].cycle"},
		{"out of stock", func(e *testEnv, it *orders.OrderItemRequest) {
			e.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
				return &domain.Product{ID: id, Type: domain.ProductSharedHosting, StockEnabled: true, StockQty: 0}, nil
			}
		}, "items[0].product_id"},
		{"domain product as product item", func(e *testEnv, it *orders.OrderItemRequest) {
			e.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
				return &domain.Product{ID: id, Type: domain.ProductDomain}, nil
			}
		}, "items[0].item_type"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newFixture()
			it := productItem()
			tc.mut(e, &it)
			svc := orders.New(e.deps)
			_, err := svc.CreateOrder(context.Background(), 7, "ip",
				orders.CreateOrderRequest{Items: []orders.OrderItemRequest{it}})
			require.Error(t, err)
			ae := apperr.From(err)
			require.Equal(t, apperr.CodeValidation, ae.Code)
			require.NotEmpty(t, ae.Details)
			assert.Equal(t, tc.field, ae.Details[0].Field)
		})
	}
}

func TestCreateOrderConfigurableOptions(t *testing.T) {
	e := newFixture()
	deltas, _ := json.Marshal(map[string]int64{"monthly": 15000, "annually": 150000})
	e.products.ListOptionValuesFn = func(_ context.Context, optionID int64) ([]domain.ConfigurableOptionValue, error) {
		if optionID != 4 {
			return nil, nil
		}
		return []domain.ConfigurableOptionValue{
			{ID: 40, OptionID: 4, Name: "2 GB RAM", PriceDeltas: deltas},
		}, nil
	}
	svc := orders.New(e.deps)

	it := productItem()
	it.Options = []orders.OptionSelectionRequest{{OptionID: 4, ValueID: 40}}
	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{it}})
	require.NoError(t, err)
	assert.Equal(t, int64(65000), res.Items[0].UnitPrice) // 50000 + 15000 delta
	assert.Equal(t, int64(75000), res.Order.Subtotal)

	// Invalid value id -> validation.
	it.Options = []orders.OptionSelectionRequest{{OptionID: 4, ValueID: 99}}
	_, err = svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{it}})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)
}

func TestCreateOrderDomainRegister(t *testing.T) {
	e := newFixture()
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items: []orders.OrderItemRequest{{
			ItemType: "domain_register", Domain: "Example.COM ", DomainYears: 2,
		}},
	})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	assert.Equal(t, "example.com", res.Items[0].Domain)
	assert.Equal(t, int64(300000), res.Items[0].UnitPrice) // 150000 × 2 years
	assert.Equal(t, domain.CycleAnnually, res.Items[0].Cycle)
	assert.Equal(t, domain.ItemDomainRegister, res.Items[0].ItemType)
}

func TestCreateOrderDomainUnavailable(t *testing.T) {
	e := newFixture()
	e.registrar.CheckAvailabilityFn = func(_ context.Context, names []string) ([]ports.DomainAvailability, error) {
		return []ports.DomainAvailability{{Name: names[0], Available: false}}, nil
	}
	svc := orders.New(e.deps)

	_, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items: []orders.OrderItemRequest{{ItemType: "domain_register", Domain: "taken.com"}},
	})
	require.Error(t, err)
	ae := apperr.From(err)
	assert.Equal(t, apperr.CodeValidation, ae.Code)
	require.NotEmpty(t, ae.Details)
	assert.Equal(t, "items[0].domain", ae.Details[0].Field)
}

func TestCreateOrderDomainSyntax(t *testing.T) {
	for _, bad := range []string{"nodots", "-lead.com", "trail-.com", "a b.com", "x.c", "under_score.com"} {
		e := newFixture()
		svc := orders.New(e.deps)
		_, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
			Items: []orders.OrderItemRequest{{ItemType: "domain_register", Domain: bad}},
		})
		require.Error(t, err, "domain %q must be rejected", bad)
		assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)
	}
}

func TestCreateOrderDomainTransferSkipsAvailability(t *testing.T) {
	e := newFixture()
	checked := false
	e.registrar.CheckAvailabilityFn = func(_ context.Context, names []string) ([]ports.DomainAvailability, error) {
		checked = true
		return []ports.DomainAvailability{{Name: names[0], Available: false, Price: 120000}}, nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items: []orders.OrderItemRequest{{
			ItemType: "domain_transfer", Domain: "moving.com", EPPCode: "secret-epp",
		}},
	})
	require.NoError(t, err)
	assert.True(t, checked, "availability consulted only for price")
	assert.Equal(t, domain.ItemDomainTransfer, res.Items[0].ItemType)
	assert.Equal(t, int64(120000), res.Items[0].UnitPrice)

	var opts struct {
		EPPCodeEnc string `json:"epp_code_enc"`
	}
	require.NoError(t, json.Unmarshal(res.Items[0].Options, &opts))
	assert.Equal(t, "enc:secret-epp", opts.EPPCodeEnc, "EPP stored encrypted")
}

func TestCreateOrderDomainWithProductPricing(t *testing.T) {
	e := newFixture()
	e.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		return &domain.Product{ID: id, Type: domain.ProductDomain, Name: ".com"}, nil
	}
	e.products.GetPricingFn = func(_ context.Context, productID int64, cycle domain.BillingCycle) (*domain.ProductPricing, error) {
		require.Equal(t, domain.CycleAnnually, cycle)
		return &domain.ProductPricing{ProductID: productID, Cycle: cycle, Price: 99000}, nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items: []orders.OrderItemRequest{{
			ItemType: "domain_register", Domain: "cheap.com", ProductID: 33,
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(99000), res.Items[0].UnitPrice, "product pricing preferred over registrar quote")
}

func TestCreateOrderRegistrarDown(t *testing.T) {
	e := newFixture()
	e.registrar.CheckAvailabilityFn = func(_ context.Context, _ []string) ([]ports.DomainAvailability, error) {
		return nil, apperr.External("rdash", errors.New("boom"))
	}
	svc := orders.New(e.deps)

	_, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items: []orders.OrderItemRequest{{ItemType: "domain_register", Domain: "x.com"}},
	})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
}

func TestCreateOrderDomainRequiresCompleteProfile(t *testing.T) {
	cases := []struct {
		name     string
		itemType string
	}{
		{"domain register", "domain_register"},
		{"domain transfer", "domain_transfer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newFixture()
			e.clients.GetByIDFn = func(_ context.Context, id int64) (*domain.Client, error) {
				return &domain.Client{ID: id, UserID: 3, FirstName: "Budi", LastName: "Santoso"}, nil
			}
			svc := orders.New(e.deps)

			_, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
				Items: []orders.OrderItemRequest{{ItemType: tc.itemType, Domain: "x.com", EPPCode: "epp"}},
			})

			require.Error(t, err)
			ae := apperr.From(err)
			assert.Equal(t, apperr.CodeValidation, ae.Code)
			require.Len(t, ae.Details, 1)
			assert.Equal(t, "profile_address", ae.Details[0].Field)
		})
	}
}

// A registrant contact with a full address but a blank phone still fails:
// RDash rejects a customer/contact with a blank "voice" field.
func TestCreateOrderDomainRequiresPhone(t *testing.T) {
	e := newFixture()
	e.clients.GetByIDFn = func(_ context.Context, id int64) (*domain.Client, error) {
		return &domain.Client{
			ID: id, UserID: 3, FirstName: "Budi", LastName: "Santoso",
			Address1: "Jl. Melati 1", City: "Lamongan", State: "Jawa Timur", Postcode: "62211",
		}, nil
	}
	svc := orders.New(e.deps)

	_, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items: []orders.OrderItemRequest{{ItemType: "domain_register", Domain: "x.com"}},
	})

	require.Error(t, err)
	ae := apperr.From(err)
	assert.Equal(t, apperr.CodeValidation, ae.Code)
	require.Len(t, ae.Details, 1)
	assert.Equal(t, "profile_address", ae.Details[0].Field)
}

func TestCreateOrderNonDomainItemIgnoresIncompleteProfile(t *testing.T) {
	e := newFixture()
	e.clients.GetByIDFn = func(_ context.Context, id int64) (*domain.Client, error) {
		return &domain.Client{ID: id, UserID: 3, FirstName: "Budi", LastName: "Santoso"}, nil
	}
	svc := orders.New(e.deps)

	_, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err, "a plain product order must not require registrant address details")
}

// Coupons

func couponFixture(mut func(c *domain.Coupon)) *domain.Coupon {
	c := &domain.Coupon{
		ID: 5, Code: "SAVE10", Type: domain.CouponPercentage, Value: 10,
		Active: true, MaxUses: 100, UsedCount: 1,
	}
	if mut != nil {
		mut(c)
	}
	return c
}

func withCoupon(e *testEnv, c *domain.Coupon) {
	e.coupons.GetByCodeFn = func(_ context.Context, code string) (*domain.Coupon, error) {
		if c != nil && code == c.Code {
			return c, nil
		}
		return nil, apperr.NotFound("coupon")
	}
	e.coupons.GetByIDFn = func(_ context.Context, id int64) (*domain.Coupon, error) {
		if c != nil && id == c.ID {
			return c, nil
		}
		return nil, apperr.NotFound("coupon")
	}
}

func TestCreateOrderCouponPercentage(t *testing.T) {
	e := newFixture()
	withCoupon(e, couponFixture(nil))
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items:      []orders.OrderItemRequest{productItem()},
		CouponCode: "SAVE10",
	})
	require.NoError(t, err)
	// 10% of unit price 50000 (setup fees not discounted).
	assert.Equal(t, int64(5000), res.Order.Discount)
	assert.Equal(t, int64(55000), res.Order.Total)
	require.NotNil(t, res.Order.CouponID)
	assert.Equal(t, int64(5), *res.Order.CouponID)
	assert.Equal(t, []int64{5}, e.usageBumped, "coupon usage incremented")
}

func TestCreateOrderCouponFixedClamped(t *testing.T) {
	e := newFixture()
	withCoupon(e, couponFixture(func(c *domain.Coupon) {
		c.Type = domain.CouponFixed
		c.Value = 90000 // more than the 50000 eligible base
	}))
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items:      []orders.OrderItemRequest{productItem()},
		CouponCode: "SAVE10",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(50000), res.Order.Discount, "fixed discount clamped to eligible base")
}

func TestCreateOrderCouponAppliesToFilter(t *testing.T) {
	e := newFixture()
	withCoupon(e, couponFixture(func(c *domain.Coupon) {
		c.AppliesTo = json.RawMessage(`[999]`) // different product
	}))
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items:      []orders.OrderItemRequest{productItem()},
		CouponCode: "SAVE10",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), res.Order.Discount, "coupon does not cover the ordered product")
}

func TestCreateOrderCouponRejections(t *testing.T) {
	past := testNow.Add(-time.Hour)
	tests := []struct {
		name string
		mut  func(c *domain.Coupon)
	}{
		{"inactive", func(c *domain.Coupon) { c.Active = false }},
		{"expired", func(c *domain.Coupon) { c.ExpiresAt = &past }},
		{"exhausted", func(c *domain.Coupon) { c.MaxUses = 2; c.UsedCount = 2 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newFixture()
			withCoupon(e, couponFixture(tc.mut))
			svc := orders.New(e.deps)
			_, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
				Items:      []orders.OrderItemRequest{productItem()},
				CouponCode: "SAVE10",
			})
			require.Error(t, err)
			ae := apperr.From(err)
			assert.Equal(t, apperr.CodeValidation, ae.Code)
			require.NotEmpty(t, ae.Details)
			assert.Equal(t, "coupon_code", ae.Details[0].Field)
		})
	}

	t.Run("unknown code", func(t *testing.T) {
		e := newFixture()
		withCoupon(e, nil)
		svc := orders.New(e.deps)
		_, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
			Items:      []orders.OrderItemRequest{productItem()},
			CouponCode: "NOPE",
		})
		require.Error(t, err)
		assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)
	})
}

// Tax

func TestCreateOrderTaxExclusive(t *testing.T) {
	e := newFixture()
	e.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
		if key == "billing.tax_enabled" {
			return true, nil
		}
		return def, nil // tax_inclusive default false
	}
	e.settings.GetJSONFn = func(_ context.Context, key string, out any) error {
		if key == "billing.tax_rate" {
			*(out.(*float64)) = 11
		}
		return nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err)
	assert.Equal(t, int64(60000), res.Order.Subtotal)
	assert.Equal(t, int64(6600), res.Order.TaxTotal) // 11% of 60000
	assert.Equal(t, int64(66600), res.Order.Total)
}

func TestCreateOrderTaxInclusive(t *testing.T) {
	e := newFixture()
	e.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
		switch key {
		case "billing.tax_enabled", "billing.tax_inclusive":
			return true, nil
		}
		return def, nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err)
	// Default rate 11% extracted from 60000: tax = 60000*11/111 ≈ 5946.
	assert.Equal(t, int64(5946), res.Order.TaxTotal)
	assert.Equal(t, int64(60000), res.Order.Total, "inclusive: total unchanged")
}

// Fraud

func TestCreateOrderFraudEmailBlacklist(t *testing.T) {
	e := newFixture()
	withCoupon(e, couponFixture(nil))
	e.settings.GetJSONFn = func(_ context.Context, key string, out any) error {
		if key == "fraud.email_domain_blacklist" {
			*(out.(*[]string)) = []string{"spam.io", "Example.com"}
		}
		return nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items:      []orders.OrderItemRequest{productItem()},
		CouponCode: "SAVE10",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderFraud, res.Order.Status)
	require.NotNil(t, res.Invoice)
	assert.Equal(t, domain.InvoiceCancelled, res.Invoice.Status)
	assert.Contains(t, e.invStatus, domain.InvoiceCancelled)
	assert.Empty(t, e.decremented, "no stock decrement for fraud orders")
	assert.Empty(t, e.usageBumped, "no coupon usage for fraud orders")
	require.NotEmpty(t, e.audit.Entries)
	assert.Equal(t, "order.create", e.audit.Entries[0].Action)
	assert.Equal(t, "order.fraud_auto", e.audit.Entries[len(e.audit.Entries)-1].Action)
}

// zeroPricedItem points e.products at a free plan (price and setup fee both
// 0) so the resulting order/invoice total is exactly 0.
func zeroPricedItem(e *testEnv) {
	e.products.GetPricingFn = func(_ context.Context, productID int64, cycle domain.BillingCycle) (*domain.ProductPricing, error) {
		return &domain.ProductPricing{ProductID: productID, Cycle: cycle, Price: 0, SetupFee: 0}, nil
	}
}

func TestCreateOrderZeroTotalAutoPays(t *testing.T) {
	e := newFixture()
	zeroPricedItem(e)
	var gotInvoiceID int64
	var gotTx ports.ApplyTx
	e.payments.ApplyPaymentFn = func(_ context.Context, invoiceID int64, tx ports.ApplyTx) error {
		gotInvoiceID, gotTx = invoiceID, tx
		return nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err)
	require.NotNil(t, res.Invoice)
	assert.Equal(t, int64(0), res.Invoice.Total)
	assert.Equal(t, domain.InvoicePaid, res.Invoice.Status,
		"an invoice with nothing due settles immediately instead of waiting on a client payment")
	require.NotNil(t, res.Invoice.PaidAt)
	assert.Equal(t, res.Invoice.ID, gotInvoiceID)
	assert.Equal(t, domain.GatewayCredit, gotTx.Gateway, "settled the same way a manual credit payment would be")
}

func TestCreateOrderZeroTotalAutoPayFailureIsNonFatal(t *testing.T) {
	e := newFixture()
	zeroPricedItem(e)
	e.payments.ApplyPaymentFn = func(context.Context, int64, ports.ApplyTx) error {
		return apperr.Internal(errors.New("db hiccup"))
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err, "a failed best-effort auto-settle must not fail the whole checkout")
	require.NotNil(t, res.Invoice)
	assert.Equal(t, domain.InvoiceUnpaid, res.Invoice.Status,
		"invoice stays payable so the client's own Pay Now (credit) click is the fallback")
	require.NotEmpty(t, e.audit.Entries)
	assert.Equal(t, "order.autopay_zero_failed", e.audit.Entries[len(e.audit.Entries)-1].Action)
}

// TestCreateOrderFraudZeroTotalDoesNotAutoPay guards the exact hazard a naive
// implementation could hit: auto-settling before (or instead of) the fraud
// branch would activate/provision a flagged order. Fraud must always win.
func TestCreateOrderFraudZeroTotalDoesNotAutoPay(t *testing.T) {
	e := newFixture()
	zeroPricedItem(e)
	e.settings.GetJSONFn = func(_ context.Context, key string, out any) error {
		if key == "fraud.email_domain_blacklist" {
			*(out.(*[]string)) = []string{"example.com"}
		}
		return nil
	}
	e.payments.ApplyPaymentFn = func(context.Context, int64, ports.ApplyTx) error {
		t.Fatal("a fraud-flagged order must never auto-activate, even with a zero total")
		return nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderFraud, res.Order.Status)
	require.NotNil(t, res.Invoice)
	assert.Equal(t, domain.InvoiceCancelled, res.Invoice.Status)
}

// autoSetupOnOrderProduct is like cpanelProduct but configured to deliver
// immediately on order creation instead of waiting for payment.
func autoSetupOnOrderProduct() *domain.Product {
	return &domain.Product{
		ID: 10, Name: "Basic Hosting", Type: domain.ProductSharedHosting,
		Module: domain.ModuleCpanel, AutoSetup: domain.SetupOnOrder,
		StockEnabled: true, StockQty: 5,
	}
}

// onOrderEnv points e.products at an on_order product and stubs the
// order/item lookups ActivateOrder needs once CreateOrder calls it directly
// (mirrors activationEnv, but CreateOrder itself supplies the order id).
func onOrderEnv(e *testEnv, activate bool) {
	e.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		if id != 10 {
			return nil, apperr.NotFound("product")
		}
		return autoSetupOnOrderProduct(), nil
	}
	if activate {
		e.orderRepo.GetByIDFn = func(_ context.Context, id int64) (*domain.Order, error) {
			return &domain.Order{ID: id, OrderNumber: "ORD-202607-000042", ClientID: 7, Status: domain.OrderPending}, nil
		}
		e.orderRepo.GetItemsFn = func(_ context.Context, _ int64) ([]domain.OrderItem, error) {
			return []domain.OrderItem{hostingItem(200, 10)}, nil
		}
	}
}

func TestCreateOrderAutoSetupOnOrderActivatesImmediately(t *testing.T) {
	e := newFixture()
	onOrderEnv(e, true)
	e.payments.ApplyPaymentFn = func(context.Context, int64, ports.ApplyTx) error {
		t.Fatal("on_order activation must not go through the payment path")
		return nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err)

	assert.Equal(t, domain.OrderActive, res.Order.Status, "order activates right away, no payment needed")
	require.NotNil(t, res.Invoice)
	assert.Equal(t, domain.InvoiceUnpaid, res.Invoice.Status, "the invoice itself still awaits payment")

	require.Len(t, e.created, 1, "service row created")
	assert.Equal(t, domain.ServicePending, e.created[0].Status)
	require.Len(t, e.enqueuer.Tasks, 1, "provisioning job enqueued")
}

// TestCreateOrderAutoSetupOnOrderFailureIsNonFatal leaves the order lookup
// unstubbed so ActivateOrder's internal getOrder fails with NotFound,
// exercising the best-effort fallback path.
func TestCreateOrderAutoSetupOnOrderFailureIsNonFatal(t *testing.T) {
	e := newFixture()
	onOrderEnv(e, false)
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err, "a failed best-effort on_order activation must not fail checkout")
	assert.Equal(t, domain.OrderPending, res.Order.Status)
	require.NotEmpty(t, e.audit.Entries)
	assert.Equal(t, "order.autoactivate_on_order_failed", e.audit.Entries[len(e.audit.Entries)-1].Action)
}

// TestCreateOrderFraudDoesNotAutoSetupOnOrder guards the same hazard as
// TestCreateOrderFraudZeroTotalDoesNotAutoPay: fraud must win even when
// every item is configured on_order.
func TestCreateOrderFraudDoesNotAutoSetupOnOrder(t *testing.T) {
	e := newFixture()
	onOrderEnv(e, false)
	e.settings.GetJSONFn = func(_ context.Context, key string, out any) error {
		if key == "fraud.email_domain_blacklist" {
			*(out.(*[]string)) = []string{"example.com"}
		}
		return nil
	}
	e.orderRepo.GetByIDFn = func(_ context.Context, id int64) (*domain.Order, error) {
		t.Fatal("a fraud-flagged order must never reach ActivateOrder, even when on_order")
		return nil, nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderFraud, res.Order.Status)
}

// TestCreateOrderMixedAutoSetupDoesNotActivateEarly covers opsi (a): a cart
// mixing an on_order product with an on_payment one stays gated on payment
// as a whole, same as today, rather than partially activating.
func TestCreateOrderMixedAutoSetupDoesNotActivateEarly(t *testing.T) {
	e := newFixture()
	onPayment := cpanelProduct()
	onPayment.ID = 11
	e.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
		switch id {
		case 10:
			return autoSetupOnOrderProduct(), nil
		case 11:
			return onPayment, nil
		default:
			return nil, apperr.NotFound("product")
		}
	}
	e.products.GetPricingFn = func(_ context.Context, productID int64, cycle domain.BillingCycle) (*domain.ProductPricing, error) {
		if cycle != domain.CycleMonthly {
			return nil, apperr.NotFound("pricing")
		}
		switch productID {
		case 10:
			return &domain.ProductPricing{ProductID: productID, Cycle: cycle, Price: 50000, SetupFee: 10000}, nil
		case 11:
			return &domain.ProductPricing{ProductID: productID, Cycle: cycle, Price: 80000, SetupFee: 0}, nil
		default:
			return nil, apperr.NotFound("pricing")
		}
	}
	e.payments.ApplyPaymentFn = func(context.Context, int64, ports.ApplyTx) error {
		t.Fatal("total is non-zero, autopay must not run")
		return nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items: []orders.OrderItemRequest{
			productItem(),
			{ItemType: "product", ProductID: 11, Cycle: "monthly", Domain: "reseller.example.com"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderPending, res.Order.Status, "mixed auto-setup cart stays gated on payment")
}

// TestCreateOrderDomainItemBlocksAutoSetupOnOrder proves a domain item never
// qualifies for on_order, even alongside an eligible product - domain
// registration must never fire before payment.
func TestCreateOrderDomainItemBlocksAutoSetupOnOrder(t *testing.T) {
	e := newFixture()
	onOrderEnv(e, false)
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
		Items: []orders.OrderItemRequest{
			productItem(),
			{ItemType: "domain_register", Domain: "brandnew.com", DomainYears: 1},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderPending, res.Order.Status, "a domain item never auto-activates before payment")
}

func TestCreateOrderFraudMaxOrdersPerDay(t *testing.T) {
	e := newFixture()
	var gotSince time.Time
	e.store.CountByClientSinceFn = func(_ context.Context, clientID int64, since time.Time) (int64, error) {
		gotSince = since
		return 10, nil // default limit is 10
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderFraud, res.Order.Status)
	assert.Equal(t, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), gotSince, "counts today's orders")
}

func TestCreateOrderUnderDailyLimitIsClean(t *testing.T) {
	e := newFixture()
	e.store.CountByClientSinceFn = func(_ context.Context, _ int64, _ time.Time) (int64, error) {
		return 9, nil
	}
	svc := orders.New(e.deps)

	res, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderPending, res.Order.Status)
}

// Stock / tx failures

func TestCreateOrderStockConflictAborts(t *testing.T) {
	e := newFixture()
	e.products.DecrementStockFn = func(_ context.Context, _ int64) error {
		return apperr.Conflict("out of stock")
	}
	svc := orders.New(e.deps)

	_, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: []orders.OrderItemRequest{productItem()}})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeConflict, apperr.From(err).Code)
}

// Reads and scoping

func TestGetScopesToClient(t *testing.T) {
	e := newFixture()
	e.orderRepo.GetByIDFn = func(_ context.Context, id int64) (*domain.Order, error) {
		return &domain.Order{ID: id, ClientID: 7, Status: domain.OrderPending}, nil
	}
	svc := orders.New(e.deps)

	// Owner sees it.
	det, err := svc.Get(context.Background(), 7, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(100), det.Order.ID)

	// Another client gets 404 (never 403).
	_, err = svc.Get(context.Background(), 8, 100)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)

	// Admin (clientID 0) sees it.
	det, err = svc.Get(context.Background(), 0, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(7), det.Order.ClientID)
}

func TestGetIncludesInvoiceID(t *testing.T) {
	e := newFixture()
	e.orderRepo.GetByIDFn = func(_ context.Context, id int64) (*domain.Order, error) {
		return &domain.Order{ID: id, ClientID: 7}, nil
	}
	e.store.InvoiceIDForOrderFn = func(_ context.Context, _ int64) (int64, error) { return 500, nil }
	svc := orders.New(e.deps)

	det, err := svc.Get(context.Background(), 7, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(500), det.InvoiceID)
}

func TestListDelegates(t *testing.T) {
	e := newFixture()
	e.orderRepo.ListByClientFn = func(_ context.Context, clientID int64, _ ports.ListParams) ([]domain.Order, int64, error) {
		return []domain.Order{{ID: 1, ClientID: clientID}}, 1, nil
	}
	e.orderRepo.ListFn = func(_ context.Context, _ ports.ListParams) ([]domain.Order, int64, error) {
		return []domain.Order{{ID: 1}, {ID: 2}}, 2, nil
	}
	svc := orders.New(e.deps)

	mine, total, err := svc.ListByClient(context.Background(), 7, ports.ListParams{})
	require.NoError(t, err)
	assert.Len(t, mine, 1)
	assert.Equal(t, int64(1), total)

	all, total, err := svc.List(context.Background(), ports.ListParams{})
	require.NoError(t, err)
	assert.Len(t, all, 2)
	assert.Equal(t, int64(2), total)
}

// Admin lifecycle

func pendingOrderEnv(e *testEnv) {
	e.orderRepo.GetByIDFn = func(_ context.Context, id int64) (*domain.Order, error) {
		return &domain.Order{ID: id, OrderNumber: "ORD-202607-000042", ClientID: 7, Status: domain.OrderPending}, nil
	}
	pid := int64(10)
	e.orderRepo.GetItemsFn = func(_ context.Context, orderID int64) ([]domain.OrderItem, error) {
		return []domain.OrderItem{{
			ID: 200, OrderID: orderID, ItemType: domain.ItemProduct, ProductID: &pid,
			Domain: "budi.example.com", Cycle: domain.CycleMonthly, UnitPrice: 50000, SetupFee: 10000,
		}}, nil
	}
	e.store.InvoiceIDForOrderFn = func(_ context.Context, _ int64) (int64, error) { return 500, nil }
	e.invStore.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, Status: domain.InvoiceUnpaid}, nil
	}
}

func TestCancelOrder(t *testing.T) {
	e := newFixture()
	pendingOrderEnv(e)
	svc := orders.New(e.deps)

	order, err := svc.Cancel(context.Background(), 1, 100)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderCancelled, order.Status)
	assert.Contains(t, e.statusSet, domain.OrderCancelled)
	assert.Equal(t, []domain.InvoiceStatus{domain.InvoiceCancelled}, e.invStatus, "unpaid invoice cancelled")
	assert.Equal(t, []int64{10}, e.stock.Restored, "stock restored")
	require.NotEmpty(t, e.audit.Entries)
	assert.Equal(t, "order.cancel", e.audit.Entries[0].Action)
}

func TestCancelPaidInvoiceLeftAlone(t *testing.T) {
	e := newFixture()
	pendingOrderEnv(e)
	e.invStore.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, Status: domain.InvoicePaid}, nil
	}
	svc := orders.New(e.deps)

	_, err := svc.Cancel(context.Background(), 1, 100)
	require.NoError(t, err)
	assert.Empty(t, e.invStatus, "paid invoice must not be cancelled")
}

func TestCancelNonPendingConflicts(t *testing.T) {
	for _, st := range []domain.OrderStatus{domain.OrderActive, domain.OrderCancelled, domain.OrderFraud} {
		e := newFixture()
		e.orderRepo.GetByIDFn = func(_ context.Context, id int64) (*domain.Order, error) {
			return &domain.Order{ID: id, Status: st}, nil
		}
		svc := orders.New(e.deps)
		_, err := svc.Cancel(context.Background(), 1, 100)
		require.Error(t, err, "status %s", st)
		assert.Equal(t, apperr.CodeConflict, apperr.From(err).Code)
	}
}

func TestMarkFraud(t *testing.T) {
	e := newFixture()
	pendingOrderEnv(e)
	svc := orders.New(e.deps)

	order, err := svc.MarkFraud(context.Background(), 1, 100)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderFraud, order.Status)
	assert.Equal(t, []domain.InvoiceStatus{domain.InvoiceCancelled}, e.invStatus)
	assert.Equal(t, []int64{10}, e.stock.Restored)
	require.NotEmpty(t, e.audit.Entries)
	assert.Equal(t, "order.fraud", e.audit.Entries[0].Action)
}

func TestAcceptActivatesAndAudits(t *testing.T) {
	e := newFixture()
	pendingOrderEnv(e)
	svc := orders.New(e.deps)

	order, err := svc.Accept(context.Background(), 1, 100)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderActive, order.Status)
	require.Len(t, e.created, 1, "service row created")
	require.NotEmpty(t, e.audit.Entries)
	assert.Equal(t, "order.accept", e.audit.Entries[0].Action)
}

func TestOrderNotFound(t *testing.T) {
	e := newFixture()
	e.orderRepo.GetByIDFn = func(_ context.Context, _ int64) (*domain.Order, error) {
		return nil, apperr.NotFound("order")
	}
	svc := orders.New(e.deps)

	_, err := svc.Get(context.Background(), 7, 1)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	_, err = svc.Cancel(context.Background(), 1, 1)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	err = svc.ActivateOrder(context.Background(), 1)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
}
