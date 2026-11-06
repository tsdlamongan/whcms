package orders_test

// Error-propagation paths: every repo/port failure must surface (or be
// tolerated where the spec says so).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/orders"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errBoom = errors.New("boom")

func mustFailCreate(t *testing.T, e *testEnv, items ...orders.OrderItemRequest) error {
	t.Helper()
	if len(items) == 0 {
		items = []orders.OrderItemRequest{productItem()}
	}
	svc := orders.New(e.deps)
	_, err := svc.CreateOrder(context.Background(), 7, "ip",
		orders.CreateOrderRequest{Items: items})
	require.Error(t, err)
	return err
}

func TestCreateOrderDependencyFailures(t *testing.T) {
	t.Run("client repo error", func(t *testing.T) {
		e := newFixture()
		e.clients.GetByIDFn = func(_ context.Context, _ int64) (*domain.Client, error) { return nil, errBoom }
		mustFailCreate(t, e)
	})
	t.Run("client missing", func(t *testing.T) {
		e := newFixture()
		e.clients.GetByIDFn = func(_ context.Context, _ int64) (*domain.Client, error) { return nil, nil }
		err := mustFailCreate(t, e)
		assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	})
	t.Run("user repo error", func(t *testing.T) {
		e := newFixture()
		e.users.GetByIDFn = func(_ context.Context, _ int64) (*domain.User, error) { return nil, errBoom }
		mustFailCreate(t, e)
	})
	t.Run("user missing", func(t *testing.T) {
		e := newFixture()
		e.users.GetByIDFn = func(_ context.Context, _ int64) (*domain.User, error) { return nil, nil }
		err := mustFailCreate(t, e)
		assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	})
	t.Run("tax settings error", func(t *testing.T) {
		e := newFixture()
		e.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
			return def, errBoom
		}
		mustFailCreate(t, e)
	})
	t.Run("email verification setting error", func(t *testing.T) {
		e := newFixture()
		e.users.GetByIDFn = func(_ context.Context, id int64) (*domain.User, error) {
			return &domain.User{ID: id, Email: "budi@example.com"}, nil // not verified
		}
		e.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
			if key == "security.require_email_verification" {
				return def, errBoom
			}
			return def, nil
		}
		err := mustFailCreate(t, e)
		assert.Equal(t, apperr.CodeInternal, apperr.From(err).Code)
	})
	t.Run("due days setting error", func(t *testing.T) {
		e := newFixture()
		e.settings.GetIntFn = func(_ context.Context, key string, def int) (int, error) {
			if key == "billing.invoice_due_days" {
				return 0, errBoom
			}
			return def, nil
		}
		mustFailCreate(t, e)
	})
	t.Run("blacklist setting error", func(t *testing.T) {
		e := newFixture()
		e.settings.GetJSONFn = func(_ context.Context, key string, _ any) error {
			if key == "fraud.email_domain_blacklist" {
				return errBoom
			}
			return nil
		}
		mustFailCreate(t, e)
	})
	t.Run("daily count error", func(t *testing.T) {
		e := newFixture()
		e.store.CountByClientSinceFn = func(_ context.Context, _ int64, _ time.Time) (int64, error) {
			return 0, errBoom
		}
		mustFailCreate(t, e)
	})
	t.Run("counter error", func(t *testing.T) {
		e := newFixture()
		e.orderRepo.NextNumberFn = func(_ context.Context, _ string) (int64, error) { return 0, errBoom }
		mustFailCreate(t, e)
	})
	t.Run("order insert error", func(t *testing.T) {
		e := newFixture()
		e.orderRepo.CreateFn = func(_ context.Context, _ *domain.Order, _ []domain.OrderItem) error { return errBoom }
		mustFailCreate(t, e)
	})
	t.Run("invoice creation error", func(t *testing.T) {
		e := newFixture()
		e.invoices.CreateInvoiceFn = func(_ context.Context, _ ports.CreateInvoiceInput) (*domain.Invoice, error) {
			return nil, errBoom
		}
		mustFailCreate(t, e)
	})
	t.Run("coupon usage conflict aborts", func(t *testing.T) {
		e := newFixture()
		withCoupon(e, couponFixture(nil))
		e.coupons.IncrementUsageFn = func(_ context.Context, _ int64) error {
			return apperr.Conflict("max uses reached")
		}
		svc := orders.New(e.deps)
		_, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
			Items: []orders.OrderItemRequest{productItem()}, CouponCode: "SAVE10",
		})
		require.Error(t, err)
		assert.Equal(t, apperr.CodeConflict, apperr.From(err).Code)
	})
	t.Run("coupon repo hard error", func(t *testing.T) {
		e := newFixture()
		e.coupons.GetByCodeFn = func(_ context.Context, _ string) (*domain.Coupon, error) { return nil, errBoom }
		svc := orders.New(e.deps)
		_, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
			Items: []orders.OrderItemRequest{productItem()}, CouponCode: "SAVE10",
		})
		require.Error(t, err)
	})
	t.Run("fraud invoice cancel error", func(t *testing.T) {
		e := newFixture()
		e.store.CountByClientSinceFn = func(_ context.Context, _ int64, _ time.Time) (int64, error) {
			return 10, nil // fraud
		}
		e.invStore.UpdateStatusFn = func(_ context.Context, _ int64, _ domain.InvoiceStatus, _ *time.Time) error {
			return errBoom
		}
		mustFailCreate(t, e)
	})
	t.Run("product repo hard error", func(t *testing.T) {
		e := newFixture()
		e.products.GetByIDFn = func(_ context.Context, _ int64) (*domain.Product, error) { return nil, errBoom }
		err := mustFailCreate(t, e)
		assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)
	})
	t.Run("option lookup error", func(t *testing.T) {
		e := newFixture()
		e.products.ListOptionValuesFn = func(_ context.Context, _ int64) ([]domain.ConfigurableOptionValue, error) {
			return nil, errBoom
		}
		it := productItem()
		it.Options = []orders.OptionSelectionRequest{{OptionID: 4, ValueID: 40}}
		err := mustFailCreate(t, e, it)
		assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)
	})
}

func TestPlanDomainEdgeCases(t *testing.T) {
	t.Run("transfer of an extension that is not offered", func(t *testing.T) {
		e := newFixture()
		e.tldPricing.GetByTLDFn = func(_ context.Context, _ string) (*domain.TLDPricing, error) {
			return nil, apperr.NotFound("tld pricing")
		}
		err := mustFailCreate(t, e, orders.OrderItemRequest{ItemType: "domain_transfer", Domain: "x.com"})
		assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)
	})
	t.Run("register with non-domain product ref", func(t *testing.T) {
		e := newFixture()
		err := mustFailCreate(t, e, orders.OrderItemRequest{
			ItemType: "domain_register", Domain: "x.com", ProductID: 10, // shared_hosting fixture
		})
		ae := apperr.From(err)
		assert.Equal(t, apperr.CodeValidation, ae.Code)
		require.NotEmpty(t, ae.Details)
		assert.Equal(t, "items[0].product_id", ae.Details[0].Field)
	})
	t.Run("registrar quote is never the selling price", func(t *testing.T) {
		// Even with a domain product that has no price, the TLD row prices the
		// line - and the registrar's (cost) quote is not consulted for price.
		e := newFixture()
		e.products.GetByIDFn = func(_ context.Context, id int64) (*domain.Product, error) {
			return &domain.Product{ID: id, Type: domain.ProductDomain, Name: ".com"}, nil
		}
		e.registrar.CheckAvailabilityFn = func(_ context.Context, names []string) ([]ports.DomainAvailability, error) {
			return []ports.DomainAvailability{{Name: names[0], Available: true, Price: 1}}, nil
		}
		svc := orders.New(e.deps)
		res, err := svc.CreateOrder(context.Background(), 7, "ip", orders.CreateOrderRequest{
			Items: []orders.OrderItemRequest{{ItemType: "domain_register", Domain: "x.com", ProductID: 33}},
		})
		require.NoError(t, err)
		assert.Equal(t, int64(150000), res.Items[0].UnitPrice)
	})
	t.Run("epp encryption error", func(t *testing.T) {
		e := newFixture()
		e.deps.Encryptor = failingEncryptor{}
		err := mustFailCreate(t, e, orders.OrderItemRequest{
			ItemType: "domain_transfer", Domain: "x.com", EPPCode: "epp",
		})
		assert.Equal(t, apperr.CodeInternal, apperr.From(err).Code)
	})
	t.Run("missing domain", func(t *testing.T) {
		e := newFixture()
		err := mustFailCreate(t, e, orders.OrderItemRequest{ItemType: "domain_register"})
		assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)
	})
}

type failingEncryptor struct{}

func (failingEncryptor) Encrypt(string) (string, error) { return "", errBoom }
func (failingEncryptor) Decrypt(string) (string, error) { return "", errBoom }

func TestActivateOrderDependencyFailures(t *testing.T) {
	base := func() *testEnv {
		e := newFixture()
		activationEnv(e, []domain.OrderItem{hostingItem(200, 10)},
			map[int64]*domain.Product{10: cpanelProduct()})
		return e
	}
	run := func(e *testEnv) error {
		return orders.New(e.deps).ActivateOrder(context.Background(), 100)
	}

	t.Run("client load error", func(t *testing.T) {
		e := base()
		e.clients.GetByIDFn = func(_ context.Context, _ int64) (*domain.Client, error) { return nil, errBoom }
		require.Error(t, run(e))
	})
	t.Run("client missing", func(t *testing.T) {
		e := base()
		e.clients.GetByIDFn = func(_ context.Context, _ int64) (*domain.Client, error) { return nil, nil }
		assert.Equal(t, apperr.CodeNotFound, apperr.From(run(e)).Code)
	})
	t.Run("items load error", func(t *testing.T) {
		e := base()
		e.orderRepo.GetItemsFn = func(_ context.Context, _ int64) ([]domain.OrderItem, error) { return nil, errBoom }
		require.Error(t, run(e))
	})
	t.Run("coupon hard error", func(t *testing.T) {
		e := base()
		cid := int64(5)
		e.orderRepo.GetByIDFn = func(_ context.Context, id int64) (*domain.Order, error) {
			return &domain.Order{ID: id, ClientID: 7, Status: domain.OrderPending, CouponID: &cid}, nil
		}
		e.coupons.GetByIDFn = func(_ context.Context, _ int64) (*domain.Coupon, error) { return nil, errBoom }
		require.Error(t, run(e))
	})
	t.Run("product gone at activation", func(t *testing.T) {
		e := base()
		e.products.GetByIDFn = func(_ context.Context, _ int64) (*domain.Product, error) {
			return nil, apperr.NotFound("product")
		}
		assert.Equal(t, apperr.CodeNotFound, apperr.From(run(e)).Code)
	})
	t.Run("encrypt error", func(t *testing.T) {
		e := base()
		e.deps.Encryptor = failingEncryptor{}
		assert.Equal(t, apperr.CodeInternal, apperr.From(run(e)).Code)
	})
	t.Run("service create error", func(t *testing.T) {
		e := base()
		e.services.CreateFn = func(_ context.Context, _ *domain.Service) error { return errBoom }
		require.Error(t, run(e))
	})
	t.Run("backref error", func(t *testing.T) {
		e := base()
		e.orderRepo.UpdateItemBackRefsFn = func(_ context.Context, _ int64, _, _ *int64) error { return errBoom }
		require.Error(t, run(e))
	})
	t.Run("enqueue error rolls back", func(t *testing.T) {
		e := base()
		e.enqueuer.EnqueueFn = func(_ context.Context, _ string, _ any, _ ...ports.JobOption) error { return errBoom }
		require.Error(t, run(e))
	})
	t.Run("final status update error", func(t *testing.T) {
		e := base()
		e.orderRepo.UpdateStatusFn = func(_ context.Context, _ int64, _ domain.OrderStatus) error { return errBoom }
		require.Error(t, run(e))
	})
	t.Run("item without product id", func(t *testing.T) {
		e := base()
		e.orderRepo.GetItemsFn = func(_ context.Context, _ int64) ([]domain.OrderItem, error) {
			return []domain.OrderItem{{ID: 200, ItemType: domain.ItemProduct}}, nil
		}
		assert.Equal(t, apperr.CodeInternal, apperr.From(run(e)).Code)
	})
}

func TestActivateDomainDependencyFailures(t *testing.T) {
	base := func() *testEnv {
		e := newFixture()
		item := domain.OrderItem{
			ID: 201, OrderID: 100, ItemType: domain.ItemDomainRegister,
			Domain: "example.com", Cycle: domain.CycleAnnually, UnitPrice: 150000,
		}
		activationEnv(e, []domain.OrderItem{item}, nil)
		return e
	}
	run := func(e *testEnv) error {
		return orders.New(e.deps).ActivateOrder(context.Background(), 100)
	}

	t.Run("nameserver setting error", func(t *testing.T) {
		e := base()
		e.settings.GetJSONFn = func(_ context.Context, key string, _ any) error {
			if key == "domains.default_nameservers" {
				return errBoom
			}
			return nil
		}
		require.Error(t, run(e))
	})
	t.Run("domain create error", func(t *testing.T) {
		e := base()
		e.domains.CreateFn = func(_ context.Context, _ *domain.Domain) error { return errBoom }
		require.Error(t, run(e))
	})
	t.Run("backref error", func(t *testing.T) {
		e := base()
		e.orderRepo.UpdateItemBackRefsFn = func(_ context.Context, _ int64, _, _ *int64) error { return errBoom }
		require.Error(t, run(e))
	})
	t.Run("enqueue error", func(t *testing.T) {
		e := base()
		e.enqueuer.EnqueueFn = func(_ context.Context, _ string, _ any, _ ...ports.JobOption) error { return errBoom }
		require.Error(t, run(e))
	})
	t.Run("malformed options json", func(t *testing.T) {
		e := base()
		e.orderRepo.GetItemsFn = func(_ context.Context, _ int64) ([]domain.OrderItem, error) {
			return []domain.OrderItem{{
				ID: 201, ItemType: domain.ItemDomainRegister, Domain: "example.com",
				Cycle: domain.CycleAnnually, UnitPrice: 150000, Options: []byte("not-json"),
			}}, nil
		}
		assert.Equal(t, apperr.CodeInternal, apperr.From(run(e)).Code)
	})
}

func TestTerminalStatusDependencyFailures(t *testing.T) {
	run := func(e *testEnv) error {
		_, err := orders.New(e.deps).Cancel(context.Background(), 1, 100)
		return err
	}

	t.Run("items error", func(t *testing.T) {
		e := newFixture()
		pendingOrderEnv(e)
		e.orderRepo.GetItemsFn = func(_ context.Context, _ int64) ([]domain.OrderItem, error) { return nil, errBoom }
		require.Error(t, run(e))
	})
	t.Run("status update error", func(t *testing.T) {
		e := newFixture()
		pendingOrderEnv(e)
		e.orderRepo.UpdateStatusFn = func(_ context.Context, _ int64, _ domain.OrderStatus) error { return errBoom }
		require.Error(t, run(e))
	})
	t.Run("invoice lookup error", func(t *testing.T) {
		e := newFixture()
		pendingOrderEnv(e)
		e.store.InvoiceIDForOrderFn = func(_ context.Context, _ int64) (int64, error) { return 0, errBoom }
		require.Error(t, run(e))
	})
	t.Run("invoice vanished is tolerated", func(t *testing.T) {
		e := newFixture()
		pendingOrderEnv(e)
		e.invStore.GetByIDFn = func(_ context.Context, _ int64) (*domain.Invoice, error) {
			return nil, apperr.NotFound("invoice")
		}
		require.NoError(t, run(e))
		assert.Empty(t, e.invStatus)
	})
	t.Run("stock restore error", func(t *testing.T) {
		e := newFixture()
		pendingOrderEnv(e)
		e.stock.Err = errBoom
		require.Error(t, run(e))
	})
}

func TestGetDependencyFailures(t *testing.T) {
	e := newFixture()
	e.orderRepo.GetByIDFn = func(_ context.Context, id int64) (*domain.Order, error) {
		return &domain.Order{ID: id, ClientID: 7}, nil
	}
	e.orderRepo.GetItemsFn = func(_ context.Context, _ int64) ([]domain.OrderItem, error) { return nil, errBoom }
	svc := orders.New(e.deps)
	_, err := svc.Get(context.Background(), 7, 100)
	require.Error(t, err)

	e.orderRepo.GetItemsFn = nil
	e.store.InvoiceIDForOrderFn = func(_ context.Context, _ int64) (int64, error) { return 0, errBoom }
	_, err = svc.Get(context.Background(), 7, 100)
	require.Error(t, err)

	// nil order without error -> NOT_FOUND.
	e.orderRepo.GetByIDFn = func(_ context.Context, _ int64) (*domain.Order, error) { return nil, nil }
	_, err = svc.Get(context.Background(), 7, 100)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
}
