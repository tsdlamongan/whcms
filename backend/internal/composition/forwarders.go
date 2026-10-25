// Package composition is the shared composition root: it builds every repo,
// adapter and service exactly once and wires the cross-module dependency
// cycle described in docs/WIRING.md §0 via late-bound forwarder handles.
//
// The forwarders below are constructed with a zero inner value FIRST, handed
// out to every Deps struct that needs the interface, and only bound to the
// real concrete service AFTER all services are constructed. Nothing calls
// these methods during wiring - only during real request/job handling, long
// after Build returns - so a temporarily empty forwarder is safe.
package composition

import (
	"context"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
)

// invoiceCreatorFwd forwards ports.InvoiceCreator to the billing service.
type invoiceCreatorFwd struct{ inner ports.InvoiceCreator }

func (f *invoiceCreatorFwd) CreateInvoice(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
	return f.inner.CreateInvoice(ctx, in)
}

// serviceActivatorFwd forwards ports.ServiceActivator to the orders service.
type serviceActivatorFwd struct{ inner ports.ServiceActivator }

func (f *serviceActivatorFwd) ActivateOrder(ctx context.Context, orderID int64) error {
	return f.inner.ActivateOrder(ctx, orderID)
}

// serviceRenewerFwd forwards ports.ServiceRenewer to the provisioning service.
type serviceRenewerFwd struct{ inner ports.ServiceRenewer }

func (f *serviceRenewerFwd) RenewService(ctx context.Context, serviceID int64) error {
	return f.inner.RenewService(ctx, serviceID)
}

func (f *serviceRenewerFwd) ApplyUpgrade(ctx context.Context, serviceID int64) error {
	return f.inner.ApplyUpgrade(ctx, serviceID)
}

// domainRenewerFwd forwards ports.DomainRenewer to the domains service.
type domainRenewerFwd struct{ inner ports.DomainRenewer }

func (f *domainRenewerFwd) RenewDomainAfterPayment(ctx context.Context, domainID int64) error {
	return f.inner.RenewDomainAfterPayment(ctx, domainID)
}

// snipeActivatorFwd forwards ports.SnipeActivator to the snipe service.
type snipeActivatorFwd struct{ inner ports.SnipeActivator }

func (f *snipeActivatorFwd) ActivateSnipeAfterPayment(ctx context.Context, snipeID int64) error {
	return f.inner.ActivateSnipeAfterPayment(ctx, snipeID)
}

// invoiceCancellerFwd forwards ports.InvoiceCanceller to the billing service.
type invoiceCancellerFwd struct{ inner ports.InvoiceCanceller }

func (f *invoiceCancellerFwd) CancelInvoice(ctx context.Context, actorUserID, invoiceID int64) (*domain.Invoice, error) {
	return f.inner.CancelInvoice(ctx, actorUserID, invoiceID)
}

// paymentApplierFwd forwards ports.PaymentApplier to the payments service.
type paymentApplierFwd struct{ inner ports.PaymentApplier }

func (f *paymentApplierFwd) ApplyPayment(ctx context.Context, invoiceID int64, tx ports.ApplyTx) error {
	return f.inner.ApplyPayment(ctx, invoiceID, tx)
}

// paidInvoiceProcessorFwd forwards ports.PaidInvoiceProcessor to the billing service.
type paidInvoiceProcessorFwd struct{ inner ports.PaidInvoiceProcessor }

func (f *paidInvoiceProcessorFwd) ProcessPaid(ctx context.Context, invoiceID int64) error {
	return f.inner.ProcessPaid(ctx, invoiceID)
}

// creditServiceFwd forwards AddCredit/DeductCredit to the clients service. It
// satisfies every narrower consumer interface (billing.CreditService,
// payments.CreditDeducter, provisioning.CreditAdder) via structural typing.
type creditServiceFwd struct {
	inner interface {
		AddCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID int64) error
		DeductCredit(ctx context.Context, clientID int64, amount int64, reason string, relatedInvoiceID int64) error
	}
}

func (f *creditServiceFwd) AddCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID int64) error {
	return f.inner.AddCredit(ctx, clientID, delta, reason, relatedInvoiceID)
}

func (f *creditServiceFwd) DeductCredit(ctx context.Context, clientID int64, amount int64, reason string, relatedInvoiceID int64) error {
	return f.inner.DeductCredit(ctx, clientID, amount, reason, relatedInvoiceID)
}

// notificationSenderFwd forwards ports.NotificationSender to the notifications service.
type notificationSenderFwd struct{ inner ports.NotificationSender }

func (f *notificationSenderFwd) SendTemplate(ctx context.Context, userID int64, templateKey string, data map[string]any) error {
	return f.inner.SendTemplate(ctx, userID, templateKey, data)
}

func (f *notificationSenderFwd) AlertAdmin(ctx context.Context, subject, message string) error {
	return f.inner.AlertAdmin(ctx, subject, message)
}
