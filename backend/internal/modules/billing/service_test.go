package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
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

var testNow = time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// fakeInvoiceStore extends the shared MockInvoiceRepo with the module-local
// InvoiceStore methods.
type fakeInvoiceStore struct {
	mocks.MockInvoiceRepo
	OrderIDForOrderItemFn   func(ctx context.Context, orderItemID int64) (int64, error)
	HasOpenRenewalInvoiceFn func(ctx context.Context, rt domain.InvoiceItemRelatedType, relatedID int64) (bool, error)
	HasEmailSinceFn         func(ctx context.Context, templateKey, ref string, since time.Time) (bool, error)
}

func (f *fakeInvoiceStore) OrderIDForOrderItem(ctx context.Context, orderItemID int64) (int64, error) {
	if f.OrderIDForOrderItemFn != nil {
		return f.OrderIDForOrderItemFn(ctx, orderItemID)
	}
	return 0, nil
}

func (f *fakeInvoiceStore) HasOpenRenewalInvoice(ctx context.Context, rt domain.InvoiceItemRelatedType, relatedID int64) (bool, error) {
	if f.HasOpenRenewalInvoiceFn != nil {
		return f.HasOpenRenewalInvoiceFn(ctx, rt, relatedID)
	}
	return false, nil
}

func (f *fakeInvoiceStore) HasEmailSince(ctx context.Context, templateKey, ref string, since time.Time) (bool, error) {
	if f.HasEmailSinceFn != nil {
		return f.HasEmailSinceFn(ctx, templateKey, ref, since)
	}
	return false, nil
}

type creditCall struct {
	ClientID  int64
	Amount    int64
	Reason    string
	InvoiceID int64
}

// fakeCredit implements CreditService recording calls.
type fakeCredit struct {
	AddCreditFn    func(ctx context.Context, clientID, delta int64, reason string, relatedInvoiceID int64) error
	Added          []creditCall
	Deducted       []creditCall
	DeductCreditFn func(ctx context.Context, clientID, amount int64, reason string, relatedInvoiceID int64) error
}

func (f *fakeCredit) AddCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID int64) error {
	if f.AddCreditFn != nil {
		return f.AddCreditFn(ctx, clientID, delta, reason, relatedInvoiceID)
	}
	f.Added = append(f.Added, creditCall{clientID, delta, reason, relatedInvoiceID})
	return nil
}

func (f *fakeCredit) DeductCredit(ctx context.Context, clientID int64, amount int64, reason string, relatedInvoiceID int64) error {
	if f.DeductCreditFn != nil {
		return f.DeductCreditFn(ctx, clientID, amount, reason, relatedInvoiceID)
	}
	f.Deducted = append(f.Deducted, creditCall{clientID, amount, reason, relatedInvoiceID})
	return nil
}

func settingsWith(vals map[string]any) *mocks.MockSettingsRepo {
	return &mocks.MockSettingsRepo{
		GetBoolFn: func(_ context.Context, key string, def bool) (bool, error) {
			if v, ok := vals[key]; ok {
				return v.(bool), nil
			}
			return def, nil
		},
		GetIntFn: func(_ context.Context, key string, def int) (int, error) {
			if v, ok := vals[key]; ok {
				return v.(int), nil
			}
			return def, nil
		},
		GetStringFn: func(_ context.Context, key, def string) (string, error) {
			if v, ok := vals[key]; ok {
				return v.(string), nil
			}
			return def, nil
		},
		GetJSONFn: func(_ context.Context, key string, out any) error {
			if v, ok := vals[key]; ok {
				b, err := json.Marshal(v)
				if err != nil {
					return err
				}
				return json.Unmarshal(b, out)
			}
			return nil
		},
	}
}

type testEnv struct {
	invoices   *fakeInvoiceStore
	txRepo     *mocks.MockTransactionRepo
	clients    *mocks.MockClientRepo
	users      *mocks.MockUserRepo
	services   *mocks.MockServiceRepo
	domains    *mocks.MockDomainRepo
	coupons    *mocks.MockCouponRepo
	settings   *mocks.MockSettingsRepo
	credit     *fakeCredit
	payments   *mocks.MockPaymentApplier
	activator  *mocks.MockServiceActivator
	renewer    *mocks.MockServiceRenewer
	domRenewer *mocks.MockDomainRenewer
	snipes     *mocks.MockSnipeActivator
	notifier   *mocks.MockNotificationSender
	pdf        *mocks.MockPDFGenerator
	storage    *mocks.MockStorage
	enq        *mocks.MockEnqueuer
	audit      *mocks.MockAuditLogger
}

func newFixture() *testEnv {
	return &testEnv{
		invoices: &fakeInvoiceStore{},
		txRepo:   &mocks.MockTransactionRepo{},
		clients: &mocks.MockClientRepo{
			GetByIDFn: func(_ context.Context, id int64) (*domain.Client, error) {
				return &domain.Client{ID: id, UserID: 100 + id, FirstName: "Budi", LastName: "Santoso",
					Address1: "Jl. Merdeka 1", City: "Lamongan", Country: "ID"}, nil
			},
		},
		users: &mocks.MockUserRepo{
			GetByIDFn: func(_ context.Context, id int64) (*domain.User, error) {
				return &domain.User{ID: id, Email: fmt.Sprintf("user%d@example.com", id)}, nil
			},
		},
		services:   &mocks.MockServiceRepo{},
		domains:    &mocks.MockDomainRepo{},
		coupons:    &mocks.MockCouponRepo{},
		settings:   settingsWith(nil),
		credit:     &fakeCredit{},
		payments:   &mocks.MockPaymentApplier{},
		activator:  &mocks.MockServiceActivator{},
		renewer:    &mocks.MockServiceRenewer{},
		domRenewer: &mocks.MockDomainRenewer{},
		snipes:     &mocks.MockSnipeActivator{},
		notifier:   &mocks.MockNotificationSender{},
		pdf:        &mocks.MockPDFGenerator{},
		storage:    &mocks.MockStorage{},
		enq:        &mocks.MockEnqueuer{},
		audit:      &mocks.MockAuditLogger{},
	}
}

func (e *testEnv) service() *Service {
	return New(Deps{
		Tx:            &mocks.MockTxManager{},
		Invoices:      e.invoices,
		Transactions:  e.txRepo,
		Clients:       e.clients,
		Users:         e.users,
		Services:      e.services,
		Domains:       e.domains,
		Coupons:       e.coupons,
		Settings:      e.settings,
		Credit:        e.credit,
		Payments:      e.payments,
		Activator:     e.activator,
		Renewer:       e.renewer,
		DomainRenewer: e.domRenewer,
		Snipes:        e.snipes,
		Notifier:      e.notifier,
		PDF:           e.pdf,
		Storage:       e.storage,
		Enqueuer:      e.enq,
		Audit:         e.audit,
		Clock:         &mocks.MockClock{FixedTime: testNow},
		FrontendURL:   "http://localhost:5173",
	})
}

// captureCreate wires NextNumber + Create to capture the created invoice.
func (e *testEnv) captureCreate(seq int64) (**domain.Invoice, *[]domain.InvoiceItem) {
	var inv *domain.Invoice
	var items []domain.InvoiceItem
	invp, itemsp := &inv, &items
	e.invoices.NextNumberFn = func(_ context.Context, scope string) (int64, error) {
		return seq, nil
	}
	e.invoices.CreateFn = func(_ context.Context, i *domain.Invoice, its []domain.InvoiceItem) error {
		i.ID = 999
		*invp = i
		*itemsp = its
		return nil
	}
	return invp, itemsp
}

func codeOf(t *testing.T, err error) apperr.Code {
	t.Helper()
	require.Error(t, err)
	return apperr.From(err).Code
}

// CreateInvoice

func TestCreateInvoiceValidation(t *testing.T) {
	item := ports.CreateInvoiceItem{Description: "x", Amount: 1000, Taxed: true}
	tests := []struct {
		name string
		in   ports.CreateInvoiceInput
	}{
		{"missing client", ports.CreateInvoiceInput{Items: []ports.CreateInvoiceItem{item}}},
		{"no items", ports.CreateInvoiceInput{ClientID: 1}},
		{"negative discount", ports.CreateInvoiceInput{ClientID: 1, Items: []ports.CreateInvoiceItem{item}, Discount: -1}},
		{"negative amount", ports.CreateInvoiceInput{ClientID: 1, Items: []ports.CreateInvoiceItem{{Amount: -5}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFixture().service()
			_, err := svc.CreateInvoice(context.Background(), tc.in)
			assert.Equal(t, apperr.CodeValidation, codeOf(t, err))
		})
	}
}

func TestCreateInvoiceClientNotFound(t *testing.T) {
	env := newFixture()
	env.clients.GetByIDFn = func(_ context.Context, id int64) (*domain.Client, error) {
		return nil, apperr.NotFound("client")
	}
	_, err := env.service().CreateInvoice(context.Background(), ports.CreateInvoiceInput{
		ClientID: 5,
		Items:    []ports.CreateInvoiceItem{{Description: "x", Amount: 1000}},
	})
	assert.Equal(t, apperr.CodeNotFound, codeOf(t, err))
}

func TestCreateInvoiceTaxExclusive(t *testing.T) {
	env := newFixture()
	env.settings = settingsWith(map[string]any{
		"billing.tax_enabled": true, "billing.tax_rate": 11, "billing.tax_inclusive": false,
	})
	created, items := env.captureCreate(42)

	inv, err := env.service().CreateInvoice(context.Background(), ports.CreateInvoiceInput{
		ClientID: 1,
		Items: []ports.CreateInvoiceItem{
			{Description: "Hosting", Amount: 100_000, Taxed: true, RelatedType: domain.RelatedOrderItem, RelatedID: 77},
			{Description: "Deposit", Amount: 50_000, Taxed: false, RelatedType: domain.RelatedDeposit},
		},
		Discount: 20_000,
	})
	require.NoError(t, err)
	require.NotNil(t, *created)

	assert.Equal(t, "INV-202607-000042", inv.InvoiceNumber)
	assert.Equal(t, domain.InvoiceUnpaid, inv.Status)
	assert.Equal(t, int64(150_000), inv.Subtotal)
	assert.Equal(t, int64(20_000), inv.Discount)
	assert.Equal(t, 11.0, inv.TaxRate)
	// taxed base = 100000 - 20000 = 80000; 11% = 8800
	assert.Equal(t, int64(8_800), inv.TaxTotal)
	assert.Equal(t, int64(150_000-20_000+8_800), inv.Total)
	assert.Equal(t, "IDR", inv.Currency)
	// default due days = 3
	assert.Equal(t, date(2026, 7, 6), inv.DueDate)

	require.Len(t, *items, 2)
	require.NotNil(t, (*items)[0].RelatedID)
	assert.Equal(t, int64(77), *(*items)[0].RelatedID)
	assert.Nil(t, (*items)[1].RelatedID)
	// audit written
	require.Len(t, env.audit.Entries, 1)
	assert.Equal(t, "invoice.create", env.audit.Entries[0].Action)
}

func TestCreateInvoiceTaxInclusive(t *testing.T) {
	env := newFixture()
	env.settings = settingsWith(map[string]any{
		"billing.tax_enabled": true, "billing.tax_rate": 11, "billing.tax_inclusive": true,
	})
	env.captureCreate(1)

	inv, err := env.service().CreateInvoice(context.Background(), ports.CreateInvoiceInput{
		ClientID: 1,
		Items:    []ports.CreateInvoiceItem{{Description: "Hosting", Amount: 111_000, Taxed: true}},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(11_000), inv.TaxTotal)
	// inclusive: total not increased by tax
	assert.Equal(t, int64(111_000), inv.Total)
}

func TestCreateInvoiceTaxDisabled(t *testing.T) {
	env := newFixture()
	env.captureCreate(1)
	inv, err := env.service().CreateInvoice(context.Background(), ports.CreateInvoiceInput{
		ClientID: 1,
		Items:    []ports.CreateInvoiceItem{{Description: "Hosting", Amount: 90_000, Taxed: true}},
		DueDate:  date(2026, 8, 1),
	})
	require.NoError(t, err)
	assert.Zero(t, inv.TaxTotal)
	assert.Zero(t, inv.TaxRate)
	assert.Equal(t, int64(90_000), inv.Total)
	assert.Equal(t, date(2026, 8, 1), inv.DueDate)
}

func TestCreateInvoiceDiscountClamped(t *testing.T) {
	env := newFixture()
	env.settings = settingsWith(map[string]any{
		"billing.tax_enabled": true, "billing.tax_rate": 11,
	})
	env.captureCreate(1)
	inv, err := env.service().CreateInvoice(context.Background(), ports.CreateInvoiceInput{
		ClientID: 1,
		Items:    []ports.CreateInvoiceItem{{Description: "x", Amount: 100_000, Taxed: true}},
		Discount: 500_000,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(100_000), inv.Discount)
	assert.Zero(t, inv.TaxTotal) // taxed base fully discounted
	assert.Zero(t, inv.Total)
}

// TestCreateInvoiceDiscountAndTaxInteraction locks in the exact total when a
// partial discount and tax both apply to a mixed taxed/untaxed-item invoice:
// the discount reduces the taxable base first, then tax is computed on what
// remains (CONTRACTS.md money rules - never floats, exact IDR arithmetic).
func TestCreateInvoiceDiscountAndTaxInteraction(t *testing.T) {
	env := newFixture()
	env.settings = settingsWith(map[string]any{
		"billing.tax_enabled": true, "billing.tax_rate": 11, "billing.tax_inclusive": false,
	})
	env.captureCreate(1)
	inv, err := env.service().CreateInvoice(context.Background(), ports.CreateInvoiceInput{
		ClientID: 1,
		Items: []ports.CreateInvoiceItem{
			{Description: "hosting", Amount: 100_000, Taxed: true},
			{Description: "one-time setup", Amount: 50_000, Taxed: false},
		},
		Discount: 30_000,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(150_000), inv.Subtotal)
	assert.Equal(t, int64(30_000), inv.Discount)
	// taxed base after discount: 100_000 - 30_000 = 70_000; tax = 70_000*11% = 7_700.
	assert.Equal(t, int64(7_700), inv.TaxTotal)
	// total = subtotal - discount + tax (exclusive) = 150_000 - 30_000 + 7_700.
	assert.Equal(t, int64(127_700), inv.Total)
}

func TestCreateInvoiceRepoError(t *testing.T) {
	env := newFixture()
	env.invoices.CreateFn = func(context.Context, *domain.Invoice, []domain.InvoiceItem) error {
		return errors.New("boom")
	}
	_, err := env.service().CreateInvoice(context.Background(), ports.CreateInvoiceInput{
		ClientID: 1,
		Items:    []ports.CreateInvoiceItem{{Description: "x", Amount: 1}},
	})
	assert.Equal(t, apperr.CodeInternal, codeOf(t, err))
}

// ProcessPaid

func paidInvoice(id int64) *domain.Invoice {
	return &domain.Invoice{ID: id, InvoiceNumber: "INV-202607-000001", ClientID: 3,
		Status: domain.InvoicePaid, Total: 250_000, DueDate: date(2026, 7, 6)}
}

func rid(v int64) *int64 { return &v }

func TestProcessPaidNotPaid(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		inv := paidInvoice(id)
		inv.Status = domain.InvoiceUnpaid
		return inv, nil
	}
	err := env.service().ProcessPaid(context.Background(), 1)
	assert.Equal(t, apperr.CodeConflict, codeOf(t, err))
}

func TestProcessPaidDispatch(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return paidInvoice(id), nil
	}
	env.invoices.GetItemsFn = func(context.Context, int64) ([]domain.InvoiceItem, error) {
		return []domain.InvoiceItem{
			{RelatedType: domain.RelatedOrderItem, RelatedID: rid(21)},
			{RelatedType: domain.RelatedOrderItem, RelatedID: rid(22)}, // same order
			{RelatedType: domain.RelatedServiceRenewal, RelatedID: rid(11)},
			{RelatedType: domain.RelatedServiceUpgrade, RelatedID: rid(12)},
			{RelatedType: domain.RelatedDomainRenewal, RelatedID: rid(13)},
			{RelatedType: domain.RelatedDomainSnipe, RelatedID: rid(14)},
			{RelatedType: domain.RelatedDomainSnipe}, // no id: skipped, never a call with 0
			{RelatedType: domain.RelatedDeposit, Amount: 50_000},
			{RelatedType: domain.RelatedLateFee, Amount: 5_000},
			{RelatedType: domain.RelatedManual, Amount: 1_000},
		}, nil
	}
	env.invoices.OrderIDForOrderItemFn = func(_ context.Context, itemID int64) (int64, error) {
		return 7, nil // both items belong to order 7
	}
	var activated []int64
	env.activator.ActivateOrderFn = func(_ context.Context, orderID int64) error {
		activated = append(activated, orderID)
		return nil
	}
	var renewed, upgraded, domainsRenewed, snipesActivated []int64
	env.snipes.ActivateSnipeAfterPaymentFn = func(_ context.Context, id int64) error {
		snipesActivated = append(snipesActivated, id)
		return nil
	}
	env.renewer.RenewServiceFn = func(_ context.Context, id int64) error {
		renewed = append(renewed, id)
		return nil
	}
	env.renewer.ApplyUpgradeFn = func(_ context.Context, id int64) error {
		upgraded = append(upgraded, id)
		return nil
	}
	env.domRenewer.RenewDomainAfterPaymentFn = func(_ context.Context, id int64) error {
		domainsRenewed = append(domainsRenewed, id)
		return nil
	}
	var sentTpl string
	var sentUser int64
	env.notifier.SendTemplateFn = func(_ context.Context, userID int64, key string, data map[string]any) error {
		sentTpl, sentUser = key, userID
		assert.Equal(t, "INV-202607-000001", data["InvoiceNumber"])
		return nil
	}

	require.NoError(t, env.service().ProcessPaid(context.Background(), 1))

	assert.Equal(t, []int64{7}, activated, "same order activated once")
	assert.Equal(t, []int64{11}, renewed)
	assert.Equal(t, []int64{12}, upgraded)
	assert.Equal(t, []int64{13}, domainsRenewed)
	assert.Equal(t, []int64{14}, snipesActivated, "the paid snipe line starts its hunt")
	require.Len(t, env.credit.Added, 1)
	assert.Equal(t, creditCall{ClientID: 3, Amount: 50_000, Reason: "Deposit INV-202607-000001", InvoiceID: 1}, env.credit.Added[0])
	assert.Equal(t, "payment_received", sentTpl)
	assert.Equal(t, int64(103), sentUser) // client 3 -> user 103
	require.Len(t, env.enq.Tasks, 1)
	assert.Equal(t, jobs.TypeInvoiceGeneratePDF, env.enq.Tasks[0].TaskType)
	assert.Equal(t, jobs.InvoiceGeneratePDFPayload{InvoiceID: 1}, env.enq.Tasks[0].Payload)
}

func TestProcessPaidActivationErrorPropagates(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return paidInvoice(id), nil
	}
	env.invoices.GetItemsFn = func(context.Context, int64) ([]domain.InvoiceItem, error) {
		return []domain.InvoiceItem{{RelatedType: domain.RelatedOrderItem, RelatedID: rid(21)}}, nil
	}
	env.invoices.OrderIDForOrderItemFn = func(context.Context, int64) (int64, error) { return 7, nil }
	env.activator.ActivateOrderFn = func(context.Context, int64) error { return errors.New("adapter down") }

	err := env.service().ProcessPaid(context.Background(), 1)
	require.Error(t, err)
	assert.Empty(t, env.enq.Tasks, "no pdf job on failure")
}

func TestProcessPaidNotificationFailureNotFatal(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return paidInvoice(id), nil
	}
	env.notifier.SendTemplateFn = func(context.Context, int64, string, map[string]any) error {
		return errors.New("mail down")
	}
	require.NoError(t, env.service().ProcessPaid(context.Background(), 1))
	assert.Len(t, env.enq.Tasks, 1)
}

// GenerateRenewalInvoices

func dueService(id int64, due time.Time) domain.Service {
	return domain.Service{ID: id, ClientID: 3, Status: domain.ServiceActive, Domain: "example.com",
		BillingCycle: domain.CycleMonthly, RecurringAmount: 120_000, NextDueDate: &due}
}

// byServiceID builds a GetByIDForUpdateFn that looks services up by ID from
// the given set (mirrors what a locked SELECT would return for a row already
// produced by ListRenewalsDue).
func byServiceID(svcs ...domain.Service) func(context.Context, int64) (*domain.Service, error) {
	m := make(map[int64]domain.Service, len(svcs))
	for _, s := range svcs {
		m[s.ID] = s
	}
	return func(_ context.Context, id int64) (*domain.Service, error) {
		if s, ok := m[id]; ok {
			return &s, nil
		}
		return nil, apperr.NotFound("service")
	}
}

// byDomainID is byServiceID's domain counterpart.
func byDomainID(doms ...domain.Domain) func(context.Context, int64) (*domain.Domain, error) {
	m := make(map[int64]domain.Domain, len(doms))
	for _, d := range doms {
		m[d.ID] = d
	}
	return func(_ context.Context, id int64) (*domain.Domain, error) {
		if d, ok := m[id]; ok {
			return &d, nil
		}
		return nil, apperr.NotFound("domain")
	}
}

func TestGenerateRenewalInvoicesServices(t *testing.T) {
	env := newFixture()
	due := date(2026, 7, 10)
	couponID := int64(9)
	svc1 := dueService(1, due)
	svc1.CouponID = &couponID
	env.coupons.GetByIDFn = func(_ context.Context, id int64) (*domain.Coupon, error) {
		return &domain.Coupon{ID: id, Type: domain.CouponPercentage, Value: 10, Recurring: true, Active: true}, nil
	}
	var listedBefore time.Time
	env.services.ListRenewalsDueFn = func(_ context.Context, before time.Time) ([]domain.Service, error) {
		listedBefore = before
		return []domain.Service{svc1}, nil
	}
	env.services.GetByIDForUpdateFn = byServiceID(svc1)
	created, items := env.captureCreate(5)
	var notified []string
	env.notifier.SendTemplateFn = func(_ context.Context, _ int64, key string, _ map[string]any) error {
		notified = append(notified, key)
		return nil
	}

	n, err := env.service().GenerateRenewalInvoices(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	// lead days default 14: today 2026-07-03 -> before 2026-07-17
	assert.Equal(t, date(2026, 7, 17), listedBefore)

	require.NotNil(t, *created)
	inv := *created
	assert.Equal(t, int64(120_000), inv.Subtotal)
	assert.Equal(t, int64(12_000), inv.Discount, "10% recurring coupon")
	assert.Equal(t, due, inv.DueDate)
	require.Len(t, *items, 1)
	assert.Equal(t, domain.RelatedServiceRenewal, (*items)[0].RelatedType)
	assert.Equal(t, int64(1), *(*items)[0].RelatedID)
	assert.Contains(t, (*items)[0].Description, "example.com")
	assert.Equal(t, []string{"invoice_created"}, notified)
}

func TestGenerateRenewalInvoicesSkips(t *testing.T) {
	env := newFixture()
	due := date(2026, 7, 10)

	withMeta := dueService(2, due)
	withMeta.PanelMeta = json.RawMessage(`{"cancel_at_period_end": true}`)
	noDue := dueService(3, due)
	noDue.NextDueDate = nil
	zeroAmount := dueService(4, due)
	zeroAmount.RecurringAmount = 0
	openInvoice := dueService(5, due)

	env.services.ListRenewalsDueFn = func(context.Context, time.Time) ([]domain.Service, error) {
		return []domain.Service{withMeta, noDue, zeroAmount, openInvoice}, nil
	}
	env.services.GetByIDForUpdateFn = byServiceID(withMeta, noDue, zeroAmount, openInvoice)
	env.invoices.HasOpenRenewalInvoiceFn = func(_ context.Context, rt domain.InvoiceItemRelatedType, id int64) (bool, error) {
		return id == 5, nil
	}
	createdCount := 0
	env.invoices.CreateFn = func(context.Context, *domain.Invoice, []domain.InvoiceItem) error {
		createdCount++
		return nil
	}

	n, err := env.service().GenerateRenewalInvoices(context.Background())
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Zero(t, createdCount)
}

func TestGenerateRenewalInvoicesDomains(t *testing.T) {
	env := newFixture()
	due := date(2026, 7, 12)
	dom8 := domain.Domain{ID: 8, ClientID: 3, Name: "contoh.id", Status: domain.DomainActive,
		RecurringAmount: 150_000, NextDueDate: &due, AutoRenew: true}
	env.domains.ListRenewalsDueFn = func(context.Context, time.Time) ([]domain.Domain, error) {
		return []domain.Domain{dom8}, nil
	}
	env.domains.GetByIDForUpdateFn = byDomainID(dom8)
	created, items := env.captureCreate(6)

	n, err := env.service().GenerateRenewalInvoices(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NotNil(t, *created)
	assert.Equal(t, due, (*created).DueDate)
	require.Len(t, *items, 1)
	assert.Equal(t, domain.RelatedDomainRenewal, (*items)[0].RelatedType)
	assert.Equal(t, int64(8), *(*items)[0].RelatedID)
	assert.Contains(t, (*items)[0].Description, "contoh.id")
}

func TestRecurringDiscountRules(t *testing.T) {
	env := newFixture()
	svc := env.service()
	expired := testNow.Add(-time.Hour)
	tests := []struct {
		name   string
		coupon *domain.Coupon
		want   int64
	}{
		{"percentage", &domain.Coupon{Type: domain.CouponPercentage, Value: 25, Recurring: true, Active: true}, 25_000},
		{"fixed", &domain.Coupon{Type: domain.CouponFixed, Value: 30_000, Recurring: true, Active: true}, 30_000},
		{"fixed clamped", &domain.Coupon{Type: domain.CouponFixed, Value: 500_000, Recurring: true, Active: true}, 100_000},
		{"not recurring", &domain.Coupon{Type: domain.CouponPercentage, Value: 25, Recurring: false, Active: true}, 0},
		{"inactive", &domain.Coupon{Type: domain.CouponPercentage, Value: 25, Recurring: true, Active: false}, 0},
		{"expired", &domain.Coupon{Type: domain.CouponPercentage, Value: 25, Recurring: true, Active: true, ExpiresAt: &expired}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env.coupons.GetByIDFn = func(context.Context, int64) (*domain.Coupon, error) {
				return tc.coupon, nil
			}
			id := int64(1)
			assert.Equal(t, tc.want, svc.recurringDiscount(context.Background(), &id, 100_000))
		})
	}
	assert.Zero(t, svc.recurringDiscount(context.Background(), nil, 100_000), "nil coupon id")
}

func TestGenerateRenewalInvoicesPartialFailure(t *testing.T) {
	env := newFixture()
	due := date(2026, 7, 10)
	svc1, svc2 := dueService(1, due), dueService(2, due)
	env.services.ListRenewalsDueFn = func(context.Context, time.Time) ([]domain.Service, error) {
		return []domain.Service{svc1, svc2}, nil
	}
	env.services.GetByIDForUpdateFn = byServiceID(svc1, svc2)
	env.invoices.CreateFn = func(_ context.Context, inv *domain.Invoice, its []domain.InvoiceItem) error {
		if *its[0].RelatedID == 1 {
			return errors.New("db hiccup")
		}
		return nil
	}
	n, err := env.service().GenerateRenewalInvoices(context.Background())
	assert.Equal(t, 1, n, "second service still invoiced")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service 1")
}

// statefulOpenCheck returns a HasOpenRenewalInvoiceFn backed by a live set
// that CreateFn (below) populates - unlike a pre-stubbed true/false, this
// actually reflects "an invoice now exists" after a real Create call, so a
// second call for the same (type, id) genuinely observes the first's effect
// instead of a canned answer. That's what makes these tests a real proof of
// idempotency rather than a tautology.
func statefulOpenCheck(open map[string]bool) func(context.Context, domain.InvoiceItemRelatedType, int64) (bool, error) {
	return func(_ context.Context, rt domain.InvoiceItemRelatedType, id int64) (bool, error) {
		return open[fmt.Sprintf("%s:%d", rt, id)], nil
	}
}

// trackingCreate returns a CreateFn that marks its item's (type, id) as open
// in the given set, for use with statefulOpenCheck.
func trackingCreate(open map[string]bool, nextID *int64) func(context.Context, *domain.Invoice, []domain.InvoiceItem) error {
	return func(_ context.Context, inv *domain.Invoice, its []domain.InvoiceItem) error {
		*nextID++
		inv.ID = *nextID
		for _, it := range its {
			if it.RelatedID != nil {
				open[fmt.Sprintf("%s:%d", it.RelatedType, *it.RelatedID)] = true
			}
		}
		return nil
	}
}

// TestGenerateServiceRenewalInvoiceIsIdempotent proves the row-locked helper
// never double-invoices the same service: calling it twice in a row only
// creates one invoice, the second call observing the first's effect via the
// (stateful) open-invoice check and skipping instead of duplicating.
func TestGenerateServiceRenewalInvoiceIsIdempotent(t *testing.T) {
	env := newFixture()
	svc1 := dueService(1, date(2026, 7, 10))
	env.services.GetByIDForUpdateFn = byServiceID(svc1)
	open := map[string]bool{}
	var seq int64
	env.invoices.HasOpenRenewalInvoiceFn = statefulOpenCheck(open)
	env.invoices.CreateFn = trackingCreate(open, &seq)
	env.invoices.NextNumberFn = func(context.Context, string) (int64, error) { return 1, nil }

	svc := env.service()
	inv1, reason1, err1 := svc.generateServiceRenewalInvoice(context.Background(), 1)
	require.NoError(t, err1)
	require.NotNil(t, inv1)
	assert.Empty(t, reason1)

	inv2, reason2, err2 := svc.generateServiceRenewalInvoice(context.Background(), 1)
	require.NoError(t, err2)
	assert.Nil(t, inv2, "second call must not create a duplicate invoice")
	assert.Equal(t, skipAlreadyInvoiced, reason2)
	assert.EqualValues(t, 1, seq, "exactly one invoice created across both calls")
}

// TestGenerateDomainRenewalInvoiceIsIdempotent is
// TestGenerateServiceRenewalInvoiceIsIdempotent's domain counterpart.
func TestGenerateDomainRenewalInvoiceIsIdempotent(t *testing.T) {
	env := newFixture()
	due := date(2026, 7, 12)
	dom8 := domain.Domain{ID: 8, ClientID: 3, Name: "contoh.id", Status: domain.DomainActive,
		RecurringAmount: 150_000, NextDueDate: &due, AutoRenew: true}
	env.domains.GetByIDForUpdateFn = byDomainID(dom8)
	open := map[string]bool{}
	var seq int64
	env.invoices.HasOpenRenewalInvoiceFn = statefulOpenCheck(open)
	env.invoices.CreateFn = trackingCreate(open, &seq)
	env.invoices.NextNumberFn = func(context.Context, string) (int64, error) { return 1, nil }

	svc := env.service()
	inv1, _, err1 := svc.generateDomainRenewalInvoice(context.Background(), 8)
	require.NoError(t, err1)
	require.NotNil(t, inv1)

	inv2, reason2, err2 := svc.generateDomainRenewalInvoice(context.Background(), 8)
	require.NoError(t, err2)
	assert.Nil(t, inv2, "second call must not create a duplicate invoice")
	assert.Equal(t, skipAlreadyInvoiced, reason2)
	assert.EqualValues(t, 1, seq)
}

// TestGenerateSelectedRenewalInvoicesSkipsAlreadyInvoiced proves the admin
// "Invoice Selected Items" action, called twice for the same service, only
// creates one invoice - reported as "created" the first time and "skipped:
// already invoiced" the second.
func TestGenerateSelectedRenewalInvoicesSkipsAlreadyInvoiced(t *testing.T) {
	env := newFixture()
	svc1 := dueService(1, date(2026, 7, 10))
	env.services.GetByIDsFn = func(_ context.Context, ids []int64) ([]domain.Service, error) {
		return []domain.Service{svc1}, nil
	}
	env.services.GetByIDForUpdateFn = byServiceID(svc1)
	open := map[string]bool{}
	var seq int64
	env.invoices.HasOpenRenewalInvoiceFn = statefulOpenCheck(open)
	env.invoices.CreateFn = trackingCreate(open, &seq)
	env.invoices.NextNumberFn = func(context.Context, string) (int64, error) { return 1, nil }

	svc := env.service()
	res1, err1 := svc.GenerateSelectedRenewalInvoices(context.Background(), 42,
		GenerateSelectedInvoicesInput{ServiceIDs: []int64{1}})
	require.NoError(t, err1)
	require.Len(t, res1.Created, 1)
	assert.Equal(t, "service", res1.Created[0].Type)
	assert.NotNil(t, res1.Skipped, "must be [] not null in the JSON response")
	assert.Empty(t, res1.Skipped)

	res2, err2 := svc.GenerateSelectedRenewalInvoices(context.Background(), 42,
		GenerateSelectedInvoicesInput{ServiceIDs: []int64{1}})
	require.NoError(t, err2)
	assert.Empty(t, res2.Created)
	require.Len(t, res2.Skipped, 1)
	assert.Equal(t, skipAlreadyInvoiced, res2.Skipped[0].Reason)
	assert.EqualValues(t, 1, seq, "exactly one invoice created across both calls")
}

// TestGenerateRenewalInvoicesThenSelectedNeverDuplicates is the mixed
// scenario the user explicitly asked to be guaranteed against: the nightly
// cron creates the renewal invoice, then an admin's "Invoice Selected Items"
// runs for the same service - it must skip, not create a second invoice.
func TestGenerateRenewalInvoicesThenSelectedNeverDuplicates(t *testing.T) {
	env := newFixture()
	svc1 := dueService(1, date(2026, 7, 10))
	env.services.ListRenewalsDueFn = func(context.Context, time.Time) ([]domain.Service, error) {
		return []domain.Service{svc1}, nil
	}
	env.services.GetByIDForUpdateFn = byServiceID(svc1)
	env.services.GetByIDsFn = func(_ context.Context, ids []int64) ([]domain.Service, error) {
		return []domain.Service{svc1}, nil
	}
	open := map[string]bool{}
	var seq int64
	env.invoices.HasOpenRenewalInvoiceFn = statefulOpenCheck(open)
	env.invoices.CreateFn = trackingCreate(open, &seq)
	env.invoices.NextNumberFn = func(context.Context, string) (int64, error) { return 1, nil }

	svc := env.service()
	n, err := svc.GenerateRenewalInvoices(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	res, err := svc.GenerateSelectedRenewalInvoices(context.Background(), 42,
		GenerateSelectedInvoicesInput{ServiceIDs: []int64{1}})
	require.NoError(t, err)
	assert.Empty(t, res.Created)
	require.Len(t, res.Skipped, 1)
	assert.Equal(t, skipAlreadyInvoiced, res.Skipped[0].Reason)
	assert.EqualValues(t, 1, seq, "GenerateSelectedRenewalInvoices must not duplicate the cron's invoice")
}

func TestGenerateSelectedRenewalInvoicesValidation(t *testing.T) {
	env := newFixture()
	_, err := env.service().GenerateSelectedRenewalInvoices(context.Background(), 42, GenerateSelectedInvoicesInput{})
	assert.Equal(t, apperr.CodeValidation, codeOf(t, err))
}

func TestGenerateSelectedRenewalInvoicesNotFound(t *testing.T) {
	env := newFixture()
	env.services.GetByIDsFn = func(context.Context, []int64) ([]domain.Service, error) {
		return nil, nil // id 99 doesn't exist
	}
	res, err := env.service().GenerateSelectedRenewalInvoices(context.Background(), 42,
		GenerateSelectedInvoicesInput{ServiceIDs: []int64{99}})
	require.NoError(t, err)
	// Regression: Created must be a non-nil empty slice (JSON "[]"), never a
	// nil slice (JSON "null") - a null "created" field crashed the admin UI's
	// result-summary rendering.
	assert.NotNil(t, res.Created)
	assert.Empty(t, res.Created)
	require.Len(t, res.Skipped, 1)
	assert.Equal(t, "not found", res.Skipped[0].Reason)
}

// MarkOverdue

func TestMarkOverdue(t *testing.T) {
	env := newFixture()
	var before time.Time
	env.invoices.ListDueForStatusFn = func(_ context.Context, status domain.InvoiceStatus, b time.Time) ([]domain.Invoice, error) {
		assert.Equal(t, domain.InvoiceUnpaid, status)
		before = b
		return []domain.Invoice{
			{ID: 1, InvoiceNumber: "INV-A", ClientID: 3, Status: domain.InvoiceUnpaid, DueDate: date(2026, 7, 1)},
			{ID: 2, InvoiceNumber: "INV-B", ClientID: 3, Status: domain.InvoiceUnpaid, DueDate: date(2026, 7, 2)},
		}, nil
	}
	var marked []int64
	env.invoices.UpdateStatusFn = func(_ context.Context, id int64, status domain.InvoiceStatus, paidAt *time.Time) error {
		assert.Equal(t, domain.InvoiceOverdue, status)
		assert.Nil(t, paidAt)
		marked = append(marked, id)
		return nil
	}
	var tpls []string
	env.notifier.SendTemplateFn = func(_ context.Context, _ int64, key string, _ map[string]any) error {
		tpls = append(tpls, key)
		return nil
	}

	n, err := env.service().MarkOverdue(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	// invoices due today are NOT overdue: bound is yesterday
	assert.Equal(t, date(2026, 7, 2), before)
	assert.Equal(t, []int64{1, 2}, marked)
	assert.Equal(t, []string{"invoice_overdue", "invoice_overdue"}, tpls)
}

// SendReminders

func TestSendReminders(t *testing.T) {
	env := newFixture()
	env.invoices.ListDueForStatusFn = func(_ context.Context, status domain.InvoiceStatus, b time.Time) ([]domain.Invoice, error) {
		switch status {
		case domain.InvoiceUnpaid:
			assert.Equal(t, date(2026, 7, 10), b, "today + max(reminder_days)=7")
			return []domain.Invoice{
				{ID: 1, InvoiceNumber: "INV-3D", ClientID: 3, DueDate: date(2026, 7, 6)},  // 3 days -> remind
				{ID: 2, InvoiceNumber: "INV-2D", ClientID: 3, DueDate: date(2026, 7, 5)},  // 2 days -> no
				{ID: 3, InvoiceNumber: "INV-DUP", ClientID: 3, DueDate: date(2026, 7, 4)}, // 1 day but deduped
			}, nil
		case domain.InvoiceOverdue:
			return []domain.Invoice{
				{ID: 4, InvoiceNumber: "INV-OD3", ClientID: 3, DueDate: date(2026, 6, 30)}, // 3 days over -> remind
				{ID: 5, InvoiceNumber: "INV-OD2", ClientID: 3, DueDate: date(2026, 7, 1)},  // 2 days over -> no
			}, nil
		}
		return nil, nil
	}
	env.invoices.HasEmailSinceFn = func(_ context.Context, tpl, ref string, since time.Time) (bool, error) {
		assert.Equal(t, date(2026, 7, 3), since)
		return ref == "INV-DUP", nil
	}
	var sent []string
	env.notifier.SendTemplateFn = func(_ context.Context, _ int64, key string, data map[string]any) error {
		sent = append(sent, key+":"+data["InvoiceNumber"].(string))
		return nil
	}

	n, err := env.service().SendReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, []string{"invoice_reminder:INV-3D", "invoice_overdue:INV-OD3"}, sent)
}

// ApplyLateFees

func TestApplyLateFeesDisabled(t *testing.T) {
	env := newFixture()
	called := false
	env.invoices.ListDueForStatusFn = func(context.Context, domain.InvoiceStatus, time.Time) ([]domain.Invoice, error) {
		called = true
		return nil, nil
	}
	n, err := env.service().ApplyLateFees(context.Background())
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.False(t, called)
}

func TestApplyLateFees(t *testing.T) {
	env := newFixture()
	env.settings = settingsWith(map[string]any{
		"billing.late_fee_enabled": true, "billing.late_fee_amount": 5_000,
	})
	inv1 := domain.Invoice{ID: 1, InvoiceNumber: "INV-1", ClientID: 3, Status: domain.InvoiceOverdue,
		Subtotal: 100_000, Total: 100_000, DueDate: date(2026, 6, 30)}
	inv2 := domain.Invoice{ID: 2, InvoiceNumber: "INV-2", ClientID: 3, Status: domain.InvoiceOverdue,
		Subtotal: 60_000, Total: 60_000, DueDate: date(2026, 6, 30)}
	env.invoices.ListDueForStatusFn = func(context.Context, domain.InvoiceStatus, time.Time) ([]domain.Invoice, error) {
		return []domain.Invoice{inv1, inv2}, nil
	}
	env.invoices.GetItemsFn = func(_ context.Context, id int64) ([]domain.InvoiceItem, error) {
		if id == 2 { // already has a late fee -> skipped
			return []domain.InvoiceItem{{RelatedType: domain.RelatedLateFee, Amount: 5_000}}, nil
		}
		return []domain.InvoiceItem{{RelatedType: domain.RelatedServiceRenewal, Amount: 100_000}}, nil
	}
	env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		cp := inv1
		return &cp, nil
	}
	var added []domain.InvoiceItem
	env.invoices.AddItemFn = func(_ context.Context, it *domain.InvoiceItem) error {
		added = append(added, *it)
		return nil
	}
	var updated *domain.Invoice
	env.invoices.UpdateFn = func(_ context.Context, inv *domain.Invoice) error {
		updated = inv
		return nil
	}

	n, err := env.service().ApplyLateFees(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.Len(t, added, 1)
	assert.Equal(t, domain.RelatedLateFee, added[0].RelatedType)
	assert.Equal(t, int64(5_000), added[0].Amount)
	assert.False(t, added[0].Taxed)
	require.NotNil(t, updated)
	assert.Equal(t, int64(105_000), updated.Subtotal)
	assert.Equal(t, int64(105_000), updated.Total)
}

func TestApplyLateFeesSkipsWhenStatusChangedUnderLock(t *testing.T) {
	env := newFixture()
	env.settings = settingsWith(map[string]any{
		"billing.late_fee_enabled": true, "billing.late_fee_amount": 5_000,
	})
	env.invoices.ListDueForStatusFn = func(context.Context, domain.InvoiceStatus, time.Time) ([]domain.Invoice, error) {
		return []domain.Invoice{{ID: 1, ClientID: 3, Status: domain.InvoiceOverdue, DueDate: date(2026, 6, 30)}}, nil
	}
	env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, Status: domain.InvoicePaid}, nil // paid meanwhile
	}
	addItemCalled := false
	env.invoices.AddItemFn = func(context.Context, *domain.InvoiceItem) error {
		addItemCalled = true
		return nil
	}
	notified := false
	env.notifier.SendTemplateFn = func(context.Context, int64, string, map[string]any) error {
		notified = true
		return nil
	}
	n, err := env.service().ApplyLateFees(context.Background())
	require.NoError(t, err)
	assert.Zero(t, n, "skipped invoice must not be counted")
	assert.False(t, addItemCalled)
	assert.False(t, notified, "no overdue notification for an invoice paid meanwhile")
}

// GenerateInvoicePDF

func TestGenerateInvoicePDF(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		inv := paidInvoice(id)
		return inv, nil
	}
	env.invoices.GetItemsFn = func(context.Context, int64) ([]domain.InvoiceItem, error) {
		return []domain.InvoiceItem{{Description: "Hosting", Amount: 250_000}}, nil
	}
	env.settings = settingsWith(map[string]any{"company.name": "TSD Hosting"})
	var gotData ports.InvoicePDFData
	env.pdf.InvoicePDFFn = func(_ context.Context, d ports.InvoicePDFData) ([]byte, error) {
		gotData = d
		return []byte("pdf-bytes"), nil
	}
	var putKey, putType string
	var putSize int64
	env.storage.PutFn = func(_ context.Context, key string, r io.Reader, size int64, ct string) error {
		putKey, putSize, putType = key, size, ct
		return nil
	}
	var savedKey string
	env.invoices.SetPDFObjectKeyFn = func(_ context.Context, id int64, key string) error {
		savedKey = key
		return nil
	}

	require.NoError(t, env.service().GenerateInvoicePDF(context.Background(), 1))
	assert.Equal(t, "INV-202607-000001", gotData.InvoiceNumber, "paid -> no PROFORMA prefix")
	assert.Equal(t, "TSD Hosting", gotData.CompanyName)
	assert.Equal(t, "Budi Santoso", gotData.ClientName)
	assert.Equal(t, "user103@example.com", gotData.ClientEmail)
	assert.Contains(t, gotData.ClientAddr, "Jl. Merdeka 1")
	require.Len(t, gotData.Items, 1)
	assert.Equal(t, "invoices/INV-202607-000001.pdf", putKey)
	assert.Equal(t, int64(len("pdf-bytes")), putSize)
	assert.Equal(t, "application/pdf", putType)
	assert.Equal(t, putKey, savedKey)
}

func TestGenerateInvoicePDFProforma(t *testing.T) {
	env := newFixture()
	env.settings = settingsWith(map[string]any{"billing.proforma_enabled": true})
	env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		inv := paidInvoice(id)
		inv.Status = domain.InvoiceUnpaid
		return inv, nil
	}
	var gotData ports.InvoicePDFData
	env.pdf.InvoicePDFFn = func(_ context.Context, d ports.InvoicePDFData) ([]byte, error) {
		gotData = d
		return []byte("x"), nil
	}
	var putKey string
	env.storage.PutFn = func(_ context.Context, key string, _ io.Reader, _ int64, _ string) error {
		putKey = key
		return nil
	}
	require.NoError(t, env.service().GenerateInvoicePDF(context.Background(), 1))
	assert.Equal(t, "PROFORMA INV-202607-000001", gotData.InvoiceNumber)
	assert.Equal(t, "invoices/INV-202607-000001.pdf", putKey, "object key keeps the real number")
}

// Client queries

func TestGetInvoiceOwnership(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, ClientID: 3, Status: domain.InvoiceUnpaid}, nil
	}
	svc := env.service()

	_, err := svc.GetInvoice(context.Background(), 99, 1)
	assert.Equal(t, apperr.CodeNotFound, codeOf(t, err), "other client's invoice is a 404")

	detail, err := svc.GetInvoice(context.Background(), 3, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(3), detail.Invoice.ClientID)

	_, err = svc.GetInvoice(context.Background(), 0, 1) // admin access
	require.NoError(t, err)
}

// TestGetInvoiceIncludesTransactions covers a real bug found 2026-07-27: the
// client invoice detail page's "previous transactions" table (and the
// payments-resume feature that reads the same list to find a pending
// transaction to resume) both depend on GET /invoices/:id embedding
// transaction history - which InvoiceDetail never actually carried, so the
// table silently rendered empty forever (no test ever caught it, since none
// asserted on it).
func TestGetInvoiceIncludesTransactions(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, ClientID: 3, Status: domain.InvoiceUnpaid}, nil
	}
	env.txRepo.ListByInvoiceFn = func(_ context.Context, invoiceID int64) ([]domain.Transaction, error) {
		return []domain.Transaction{{ID: 41, InvoiceID: invoiceID, Status: domain.TxPending}}, nil
	}
	svc := env.service()

	detail, err := svc.GetInvoice(context.Background(), 3, 1)
	require.NoError(t, err)
	require.Len(t, detail.Transactions, 1)
	assert.Equal(t, int64(41), detail.Transactions[0].ID)
}

func TestListClientInvoices(t *testing.T) {
	env := newFixture()
	env.invoices.ListByClientFn = func(_ context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error) {
		assert.Equal(t, int64(3), clientID)
		return []domain.Invoice{{ID: 1}}, 1, nil
	}
	list, total, err := env.service().ListClientInvoices(context.Background(), 3, ports.ListParams{})
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, int64(1), total)
}

func TestDownloadPDFGeneratesOnFirstAccess(t *testing.T) {
	env := newFixture()
	generated := false
	key := ""
	env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		inv := paidInvoice(id)
		inv.PDFObjectKey = key
		return inv, nil
	}
	env.invoices.SetPDFObjectKeyFn = func(_ context.Context, id int64, k string) error {
		generated = true
		key = k
		return nil
	}
	env.storage.GetFn = func(_ context.Context, k string) (io.ReadCloser, error) {
		assert.Equal(t, "invoices/INV-202607-000001.pdf", k)
		return io.NopCloser(strings.NewReader("pdf")), nil
	}
	rc, filename, err := env.service().DownloadPDF(context.Background(), 3, 1)
	require.NoError(t, err)
	defer rc.Close()
	assert.True(t, generated)
	assert.Equal(t, "INV-202607-000001.pdf", filename)
}

// Admin operations

func TestCreateManualInvoice(t *testing.T) {
	env := newFixture()
	_, items := env.captureCreate(7)
	var notified string
	env.notifier.SendTemplateFn = func(_ context.Context, _ int64, key string, _ map[string]any) error {
		notified = key
		return nil
	}
	inv, err := env.service().CreateManualInvoice(context.Background(), 42, ManualInvoiceInput{
		ClientID: 3,
		Items:    []ManualInvoiceItemInput{{Description: "Setup assistance", Amount: 75_000, Taxed: true}},
		DueDate:  "2026-07-20",
		Notes:    "manual",
	})
	require.NoError(t, err)
	assert.Equal(t, date(2026, 7, 20), inv.DueDate)
	require.Len(t, *items, 1)
	assert.Equal(t, domain.RelatedManual, (*items)[0].RelatedType)
	assert.Equal(t, "invoice_created", notified)
	// two audits: invoice.create + invoice.create_manual
	require.Len(t, env.audit.Entries, 2)
	assert.Equal(t, "invoice.create_manual", env.audit.Entries[1].Action)
	assert.Equal(t, int64(42), env.audit.Entries[1].ActorUserID)
}

func TestCreateManualInvoiceValidation(t *testing.T) {
	env := newFixture()
	_, err := env.service().CreateManualInvoice(context.Background(), 42, ManualInvoiceInput{ClientID: 3})
	assert.Equal(t, apperr.CodeValidation, codeOf(t, err))

	_, err = env.service().CreateManualInvoice(context.Background(), 42, ManualInvoiceInput{
		ClientID: 3,
		Items:    []ManualInvoiceItemInput{{Description: "x", Amount: 100}},
		DueDate:  "20-07-2026",
	})
	assert.Equal(t, apperr.CodeValidation, codeOf(t, err))
}

func TestAddManualPayment(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, ClientID: 3, Status: domain.InvoiceUnpaid, Total: 100_000}, nil
	}
	var applied ports.ApplyTx
	env.payments.ApplyPaymentFn = func(_ context.Context, invoiceID int64, tx ports.ApplyTx) error {
		assert.Equal(t, int64(1), invoiceID)
		applied = tx
		return nil
	}
	_, err := env.service().AddManualPayment(context.Background(), 42, 1, ManualPaymentInput{
		Amount: 100_000, Method: "bank_transfer",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.GatewayManual, applied.Gateway)
	assert.Equal(t, "bank_transfer", applied.MethodCode)
	assert.Equal(t, int64(100_000), applied.Amount)
	assert.Equal(t, testNow, applied.PaidAt)
	require.Len(t, env.audit.Entries, 1)
	assert.Equal(t, "invoice.manual_payment", env.audit.Entries[0].Action)
}

func TestAddManualPaymentValidation(t *testing.T) {
	env := newFixture()
	_, err := env.service().AddManualPayment(context.Background(), 42, 1, ManualPaymentInput{Amount: 0, Method: ""})
	assert.Equal(t, apperr.CodeValidation, codeOf(t, err))
}

func TestCancelInvoice(t *testing.T) {
	env := newFixture()
	status := domain.InvoiceUnpaid
	env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, ClientID: 3, Status: status}, nil
	}
	var newStatus domain.InvoiceStatus
	env.invoices.UpdateStatusFn = func(_ context.Context, id int64, st domain.InvoiceStatus, paidAt *time.Time) error {
		newStatus = st
		return nil
	}
	inv, err := env.service().CancelInvoice(context.Background(), 42, 1)
	require.NoError(t, err)
	assert.Equal(t, domain.InvoiceCancelled, inv.Status)
	assert.Equal(t, domain.InvoiceCancelled, newStatus)

	status = domain.InvoicePaid
	_, err = env.service().CancelInvoice(context.Background(), 42, 1)
	assert.Equal(t, apperr.CodeConflict, codeOf(t, err), "paid invoice cannot be cancelled")
}

func TestRefundInvoice(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, InvoiceNumber: "INV-R", ClientID: 3,
			Status: domain.InvoicePaid, Total: 200_000}, nil
	}
	env.txRepo.ListByInvoiceFn = func(context.Context, int64) ([]domain.Transaction, error) {
		return []domain.Transaction{
			{ID: 10, Status: domain.TxSuccess},
			{ID: 11, Status: domain.TxFailed},
		}, nil
	}
	var updatedTx []domain.Transaction
	env.txRepo.UpdateFn = func(_ context.Context, tx *domain.Transaction) error {
		updatedTx = append(updatedTx, *tx)
		return nil
	}
	var invStatus domain.InvoiceStatus
	env.invoices.UpdateStatusFn = func(_ context.Context, _ int64, st domain.InvoiceStatus, _ *time.Time) error {
		invStatus = st
		return nil
	}

	inv, err := env.service().RefundInvoice(context.Background(), 42, 1, RefundInput{ToCredit: true})
	require.NoError(t, err)
	assert.Equal(t, domain.InvoiceRefunded, inv.Status)
	assert.Equal(t, domain.InvoiceRefunded, invStatus)
	require.Len(t, updatedTx, 1, "only the success tx is refunded")
	assert.Equal(t, int64(10), updatedTx[0].ID)
	assert.Equal(t, domain.TxRefunded, updatedTx[0].Status)
	require.Len(t, env.credit.Added, 1)
	assert.Equal(t, creditCall{ClientID: 3, Amount: 200_000, Reason: "Refund INV-R", InvoiceID: 1}, env.credit.Added[0])
}

func TestRefundInvoiceRefusesADomainHuntInvoice(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, InvoiceNumber: "INV-S", ClientID: 3, Status: domain.InvoicePaid, Total: 222_000}, nil
	}
	snipeID := int64(5)
	env.invoices.GetItemsFn = func(context.Context, int64) ([]domain.InvoiceItem, error) {
		return []domain.InvoiceItem{
			{Description: "Hunting service", RelatedType: domain.RelatedManual},
			{Description: "hunt.com registration", RelatedType: domain.RelatedDomainSnipe, RelatedID: &snipeID},
		}, nil
	}
	env.invoices.UpdateStatusFn = func(context.Context, int64, domain.InvoiceStatus, *time.Time) error {
		t.Fatal("the invoice must stay paid")
		return nil
	}
	_, err := env.service().RefundInvoice(context.Background(), 42, 1, RefundInput{ToCredit: true})
	assert.Equal(t, apperr.CodeConflict, codeOf(t, err))
	assert.Contains(t, err.Error(), "cancel the hunt")
	assert.Empty(t, env.credit.Added)
}

func TestRefundInvoiceNotPaid(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, Status: domain.InvoiceUnpaid}, nil
	}
	_, err := env.service().RefundInvoice(context.Background(), 42, 1, RefundInput{})
	assert.Equal(t, apperr.CodeConflict, codeOf(t, err))
	assert.Empty(t, env.credit.Added)
}

func TestRefundInvoiceNoCreditFlag(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, InvoiceNumber: "INV-R", ClientID: 3,
			Status: domain.InvoicePaid, Total: 200_000}, nil
	}
	_, err := env.service().RefundInvoice(context.Background(), 42, 1, RefundInput{ToCredit: false})
	require.NoError(t, err)
	assert.Empty(t, env.credit.Added)
}

func TestUpdateInvoice(t *testing.T) {
	env := newFixture()
	env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, Status: domain.InvoiceUnpaid, DueDate: date(2026, 7, 6)}, nil
	}
	var updated *domain.Invoice
	env.invoices.UpdateFn = func(_ context.Context, inv *domain.Invoice) error {
		updated = inv
		return nil
	}
	due := "2026-08-01"
	notes := "extended per client request"
	inv, err := env.service().UpdateInvoice(context.Background(), 42, 1, UpdateInvoiceInput{DueDate: &due, Notes: &notes})
	require.NoError(t, err)
	assert.Equal(t, date(2026, 8, 1), inv.DueDate)
	assert.Equal(t, notes, inv.Notes)
	require.NotNil(t, updated)
}

func TestUpdateInvoiceGuards(t *testing.T) {
	env := newFixture()
	_, err := env.service().UpdateInvoice(context.Background(), 42, 1, UpdateInvoiceInput{})
	assert.Equal(t, apperr.CodeValidation, codeOf(t, err), "nothing to update")

	env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, Status: domain.InvoicePaid}, nil
	}
	notes := "x"
	_, err = env.service().UpdateInvoice(context.Background(), 42, 1, UpdateInvoiceInput{Notes: &notes})
	assert.Equal(t, apperr.CodeConflict, codeOf(t, err), "paid invoice not editable")

	badDue := "01/08/2026"
	env.invoices.GetByIDForUpdateFn = func(_ context.Context, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id, Status: domain.InvoiceUnpaid}, nil
	}
	_, err = env.service().UpdateInvoice(context.Background(), 42, 1, UpdateInvoiceInput{DueDate: &badDue})
	assert.Equal(t, apperr.CodeValidation, codeOf(t, err))
}

// Misc helpers

func TestPanelMetaCancelAtPeriodEnd(t *testing.T) {
	assert.False(t, panelMetaCancelAtPeriodEnd(nil))
	assert.False(t, panelMetaCancelAtPeriodEnd(json.RawMessage(`not-json`)))
	assert.False(t, panelMetaCancelAtPeriodEnd(json.RawMessage(`{}`)))
	assert.True(t, panelMetaCancelAtPeriodEnd(json.RawMessage(`{"cancel_at_period_end":true}`)))
}

func TestDaysBetween(t *testing.T) {
	assert.Equal(t, 3, daysBetween(date(2026, 7, 3), date(2026, 7, 6)))
	assert.Equal(t, -2, daysBetween(date(2026, 7, 3), date(2026, 7, 1)))
	assert.Equal(t, 0, daysBetween(testNow, date(2026, 7, 3)))
}
