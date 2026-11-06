package composition

import (
	"context"
	"errors"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise each forwarder's late-bound pass-through (see
// forwarders.go): a forwarder is constructed with a zero inner value during
// Build, then bound to the real service. Once bound, every method must
// forward both the arguments and the (result, error) pair unchanged.

func TestInvoiceCreatorFwd(t *testing.T) {
	ctx := context.Background()
	wantIn := ports.CreateInvoiceInput{ClientID: 42, Notes: "test"}
	wantInv := &domain.Invoice{ID: 7}
	var gotIn ports.CreateInvoiceInput
	inner := &mocks.MockInvoiceCreator{
		CreateInvoiceFn: func(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
			gotIn = in
			return wantInv, nil
		},
	}
	f := &invoiceCreatorFwd{inner: inner}

	inv, err := f.CreateInvoice(ctx, wantIn)
	require.NoError(t, err)
	assert.Same(t, wantInv, inv)
	assert.Equal(t, wantIn, gotIn)

	// Error propagation.
	wantErr := errors.New("boom")
	inner.CreateInvoiceFn = func(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
		return nil, wantErr
	}
	inv, err = f.CreateInvoice(ctx, wantIn)
	assert.Nil(t, inv)
	assert.Same(t, wantErr, err)
}

func TestServiceActivatorFwd(t *testing.T) {
	ctx := context.Background()
	var gotOrderID int64
	inner := &mocks.MockServiceActivator{
		ActivateOrderFn: func(ctx context.Context, orderID int64) error {
			gotOrderID = orderID
			return nil
		},
	}
	f := &serviceActivatorFwd{inner: inner}

	require.NoError(t, f.ActivateOrder(ctx, 99))
	assert.Equal(t, int64(99), gotOrderID)

	wantErr := errors.New("activate failed")
	inner.ActivateOrderFn = func(ctx context.Context, orderID int64) error { return wantErr }
	assert.Same(t, wantErr, f.ActivateOrder(ctx, 99))
}

func TestServiceRenewerFwd(t *testing.T) {
	ctx := context.Background()
	var gotRenewID, gotUpgradeID int64
	renewErr := errors.New("renew failed")
	upgradeErr := errors.New("upgrade failed")
	inner := &mocks.MockServiceRenewer{
		RenewServiceFn: func(ctx context.Context, serviceID int64) error {
			gotRenewID = serviceID
			return renewErr
		},
		ApplyUpgradeFn: func(ctx context.Context, serviceID int64) error {
			gotUpgradeID = serviceID
			return upgradeErr
		},
	}
	f := &serviceRenewerFwd{inner: inner}

	assert.Same(t, renewErr, f.RenewService(ctx, 1))
	assert.Equal(t, int64(1), gotRenewID)

	assert.Same(t, upgradeErr, f.ApplyUpgrade(ctx, 2))
	assert.Equal(t, int64(2), gotUpgradeID)
}

func TestDomainRenewerFwd(t *testing.T) {
	ctx := context.Background()
	var gotDomainID int64
	inner := &mocks.MockDomainRenewer{
		RenewDomainAfterPaymentFn: func(ctx context.Context, domainID int64) error {
			gotDomainID = domainID
			return nil
		},
	}
	f := &domainRenewerFwd{inner: inner}

	require.NoError(t, f.RenewDomainAfterPayment(ctx, 5))
	assert.Equal(t, int64(5), gotDomainID)

	wantErr := errors.New("domain renew failed")
	inner.RenewDomainAfterPaymentFn = func(ctx context.Context, domainID int64) error { return wantErr }
	assert.Same(t, wantErr, f.RenewDomainAfterPayment(ctx, 5))
}

func TestPaymentApplierFwd(t *testing.T) {
	ctx := context.Background()
	wantTx := ports.ApplyTx{Gateway: domain.GatewayDuitku, MerchantOrderID: "ORD-1"}
	var gotInvoiceID int64
	var gotTx ports.ApplyTx
	inner := &mocks.MockPaymentApplier{
		ApplyPaymentFn: func(ctx context.Context, invoiceID int64, tx ports.ApplyTx) error {
			gotInvoiceID, gotTx = invoiceID, tx
			return nil
		},
	}
	f := &paymentApplierFwd{inner: inner}

	require.NoError(t, f.ApplyPayment(ctx, 11, wantTx))
	assert.Equal(t, int64(11), gotInvoiceID)
	assert.Equal(t, wantTx, gotTx)

	wantErr := errors.New("apply payment failed")
	inner.ApplyPaymentFn = func(ctx context.Context, invoiceID int64, tx ports.ApplyTx) error { return wantErr }
	assert.Same(t, wantErr, f.ApplyPayment(ctx, 11, wantTx))
}

func TestPaidInvoiceProcessorFwd(t *testing.T) {
	ctx := context.Background()
	var gotInvoiceID int64
	inner := &mocks.MockPaidInvoiceProcessor{
		ProcessPaidFn: func(ctx context.Context, invoiceID int64) error {
			gotInvoiceID = invoiceID
			return nil
		},
	}
	f := &paidInvoiceProcessorFwd{inner: inner}

	require.NoError(t, f.ProcessPaid(ctx, 21))
	assert.Equal(t, int64(21), gotInvoiceID)

	wantErr := errors.New("process paid failed")
	inner.ProcessPaidFn = func(ctx context.Context, invoiceID int64) error { return wantErr }
	assert.Same(t, wantErr, f.ProcessPaid(ctx, 21))
}

// fakeCreditService is a narrow local fake for creditServiceFwd's anonymous
// inner interface (AddCredit/DeductCredit), mirroring the fakeCredit pattern
// used in internal/modules/billing/service_test.go.
type fakeCreditService struct {
	AddCreditFn    func(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID int64) error
	DeductCreditFn func(ctx context.Context, clientID int64, amount int64, reason string, relatedInvoiceID int64) error
}

func (f *fakeCreditService) AddCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID int64) error {
	if f.AddCreditFn != nil {
		return f.AddCreditFn(ctx, clientID, delta, reason, relatedInvoiceID)
	}
	return nil
}

func (f *fakeCreditService) DeductCredit(ctx context.Context, clientID int64, amount int64, reason string, relatedInvoiceID int64) error {
	if f.DeductCreditFn != nil {
		return f.DeductCreditFn(ctx, clientID, amount, reason, relatedInvoiceID)
	}
	return nil
}

func TestCreditServiceFwd(t *testing.T) {
	ctx := context.Background()
	type call struct {
		clientID, amount, relatedInvoiceID int64
		reason                             string
	}
	var gotAdd, gotDeduct call
	addErr := errors.New("add credit failed")
	deductErr := errors.New("deduct credit failed")
	inner := &fakeCreditService{
		AddCreditFn: func(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID int64) error {
			gotAdd = call{clientID, delta, relatedInvoiceID, reason}
			return addErr
		},
		DeductCreditFn: func(ctx context.Context, clientID int64, amount int64, reason string, relatedInvoiceID int64) error {
			gotDeduct = call{clientID, amount, relatedInvoiceID, reason}
			return deductErr
		},
	}
	f := &creditServiceFwd{inner: inner}

	assert.Same(t, addErr, f.AddCredit(ctx, 1, 100, "topup", 55))
	assert.Equal(t, call{1, 100, 55, "topup"}, gotAdd)

	assert.Same(t, deductErr, f.DeductCredit(ctx, 2, 200, "spend", 66))
	assert.Equal(t, call{2, 200, 66, "spend"}, gotDeduct)
}

func TestNotificationSenderFwd(t *testing.T) {
	ctx := context.Background()
	var gotUserID int64
	var gotKey string
	var gotData map[string]any
	var gotSubject, gotMessage string
	sendErr := errors.New("send template failed")
	alertErr := errors.New("alert admin failed")
	inner := &mocks.MockNotificationSender{
		SendTemplateFn: func(ctx context.Context, userID int64, templateKey string, data map[string]any) error {
			gotUserID, gotKey, gotData = userID, templateKey, data
			return sendErr
		},
		AlertAdminFn: func(ctx context.Context, subject, message string) error {
			gotSubject, gotMessage = subject, message
			return alertErr
		},
	}
	f := &notificationSenderFwd{inner: inner}

	data := map[string]any{"a": 1}
	assert.Same(t, sendErr, f.SendTemplate(ctx, 3, "welcome", data))
	assert.Equal(t, int64(3), gotUserID)
	assert.Equal(t, "welcome", gotKey)
	assert.Equal(t, data, gotData)

	assert.Same(t, alertErr, f.AlertAdmin(ctx, "subj", "msg"))
	assert.Equal(t, "subj", gotSubject)
	assert.Equal(t, "msg", gotMessage)
}

func TestSnipeActivatorFwd(t *testing.T) {
	ctx := context.Background()
	var got int64
	inner := &mocks.MockSnipeActivator{ActivateSnipeAfterPaymentFn: func(_ context.Context, id int64) error {
		got = id
		return nil
	}}
	f := &snipeActivatorFwd{inner: inner}
	require.NoError(t, f.ActivateSnipeAfterPayment(ctx, 77))
	assert.EqualValues(t, 77, got)

	wantErr := errors.New("boom")
	inner.ActivateSnipeAfterPaymentFn = func(context.Context, int64) error { return wantErr }
	assert.Same(t, wantErr, f.ActivateSnipeAfterPayment(ctx, 1))
}

func TestInvoiceCancellerFwd(t *testing.T) {
	ctx := context.Background()
	want := &domain.Invoice{ID: 9}
	var gotActor, gotID int64
	inner := &mocks.MockInvoiceCanceller{CancelInvoiceFn: func(_ context.Context, actor, id int64) (*domain.Invoice, error) {
		gotActor, gotID = actor, id
		return want, nil
	}}
	f := &invoiceCancellerFwd{inner: inner}
	inv, err := f.CancelInvoice(ctx, 3, 9)
	require.NoError(t, err)
	assert.Same(t, want, inv)
	assert.EqualValues(t, 3, gotActor)
	assert.EqualValues(t, 9, gotID)

	wantErr := errors.New("boom")
	inner.CancelInvoiceFn = func(context.Context, int64, int64) (*domain.Invoice, error) { return nil, wantErr }
	inv, err = f.CancelInvoice(ctx, 3, 9)
	assert.Nil(t, inv)
	assert.Same(t, wantErr, err)
}
