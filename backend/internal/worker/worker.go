// Package worker is the M-WORKER module: it binds every asynq task type to
// the owning module's job method (MODULES.md §3 M-WORKER) and exposes the
// periodic cron schedule consumed by cmd/worker.
//
// Task routing table (task type -> Deps method):
//
//	order:activate            -> Orders.ActivateOrder
//	provision:create          -> Provisioning.ProvisionCreate
//	provision:suspend         -> Provisioning.ProvisionSuspend
//	provision:unsuspend       -> Provisioning.ProvisionUnsuspend
//	provision:terminate       -> Provisioning.ProvisionTerminate
//	provision:change_package  -> Provisioning.ProvisionChangePackage
//	provision:change_password -> Provisioning.ProvisionChangePassword
//	domain:register           -> Domains.RegisterDomainJob
//	domain:transfer           -> Domains.TransferDomainJob
//	domain:renew              -> Domains.RenewDomainJob
//	domain:sync               -> Domains.SyncDomainJob
//	mail:send                 -> Notifications.DeliverEmail
//	invoice:generate_pdf      -> Billing.GenerateInvoicePDF
//	payment:reconcile_one     -> Payments.ReconcileOne
//	cron:invoices_generate    -> Billing.GenerateRenewalInvoices  (locked)
//	cron:mark_overdue         -> Billing.MarkOverdue              (locked)
//	cron:invoice_reminders    -> Billing.SendReminders            (locked)
//	cron:late_fees            -> Billing.ApplyLateFees            (locked)
//	cron:auto_suspend         -> Provisioning.AutoSuspend         (locked)
//	cron:auto_terminate       -> Provisioning.AutoTerminate       (locked)
//	cron:payment_reconcile    -> Payments.ReconcilePending        (locked)
//	cron:domain_sync          -> Domains.SyncAllDomains           (locked)
//	cron:domain_verification_sync -> Domains.SyncAwaitingVerification (locked)
//	cron:housekeeping         -> AdminOps.Housekeep               (locked)
//
// Every cron handler runs single-flight under Locker.WithLock("cron:<name>",
// 10m) and logs the processed count. Every handler is wrapped with logging
// plus a final-retry hook: when a task fails on its last attempt (asynq retry
// metadata) or returns asynq.SkipRetry, Notifier.AlertAdmin is called.
// Payload unmarshal errors and missing/invalid IDs return asynq.SkipRetry so
// permanently-broken tasks are archived instead of retried.
package worker

import (
	"context"
	"log/slog"

	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/internal/ports"

	"github.com/hibiken/asynq"
)

// Consumer-side interfaces (EXACT signatures from MODULES.md §2). They are
// implemented by the respective module services and satisfied implicitly at
// wiring time; the worker never imports module packages.

// BillingJobs are the billing cron/job methods (implemented by billing.*Service).
type BillingJobs interface {
	GenerateRenewalInvoices(ctx context.Context) (int, error)
	MarkOverdue(ctx context.Context) (int, error)
	SendReminders(ctx context.Context) (int, error)
	ApplyLateFees(ctx context.Context) (int, error)
	GenerateInvoicePDF(ctx context.Context, invoiceID int64) error
}

// PaymentJobs are the payments cron/job methods (implemented by payments.*Service).
type PaymentJobs interface {
	ReconcilePending(ctx context.Context) (int, error)
	ReconcileOne(ctx context.Context, transactionID int64) error
}

// ProvisioningJobs are the provisioning cron/job methods (implemented by
// provisioning.*Service). ProvisionChangePassword is not listed in MODULES §2;
// its signature is derived from jobs.ProvisionChangePasswordPayload
// {ServiceID, Password} - noted for the orchestrator.
type ProvisioningJobs interface {
	AutoSuspend(ctx context.Context) (int, error)
	AutoTerminate(ctx context.Context) (int, error)
	ProvisionCreate(ctx context.Context, serviceID int64) error
	ProvisionSuspend(ctx context.Context, serviceID int64, reason string) error
	ProvisionUnsuspend(ctx context.Context, serviceID int64) error
	ProvisionTerminate(ctx context.Context, serviceID int64) error
	ProvisionChangePackage(ctx context.Context, serviceID int64) error
	ProvisionChangePassword(ctx context.Context, serviceID int64, password string) error
}

// DomainJobs are the domains cron/job methods (implemented by domains.*Service).
type DomainJobs interface {
	RegisterDomainJob(ctx context.Context, domainID int64) error
	TransferDomainJob(ctx context.Context, domainID int64) error
	RenewDomainJob(ctx context.Context, domainID int64) error
	SyncDomainJob(ctx context.Context, domainID int64) error
	SyncAllDomains(ctx context.Context) (int, error)
	SyncAwaitingVerification(ctx context.Context) (int, error)
}

// MailDeliverer delivers one queued email_log row (implemented by
// notifications.*Service).
type MailDeliverer interface {
	DeliverEmail(ctx context.Context, emailLogID int64) error
}

// Housekeeper prunes old log rows (implemented by adminops.*Service).
type Housekeeper interface {
	Housekeep(ctx context.Context) (int, error)
}

// Deps are the narrow cross-module interfaces the worker dispatches to, plus
// the infrastructure it needs (cron lock + logger + admin alerting).
type Deps struct {
	Orders        ports.ServiceActivator   // orders module
	Provisioning  ProvisioningJobs         // provisioning module
	Domains       DomainJobs               // domains module
	Billing       BillingJobs              // billing module
	Payments      PaymentJobs              // payments module
	Notifications MailDeliverer            // notifications module
	AdminOps      Housekeeper              // adminops module
	Notifier      ports.NotificationSender // notifications module (AlertAdmin on final retry)
	Locker        ports.Locker             // platform/lock (cron single-flight)
	Log           *slog.Logger             // optional; slog.Default() when nil
}

// RegisterHandlers binds every jobs.Type* task type on mux to the matching
// Deps method.
func RegisterHandlers(mux *asynq.ServeMux, d Deps) {
	h := &handlers{d: d, log: d.Log}
	if h.log == nil {
		h.log = slog.Default()
	}

	// One-off jobs.
	mux.HandleFunc(jobs.TypeOrderActivate, h.wrap(h.orderActivate))
	mux.HandleFunc(jobs.TypeProvisionCreate, h.wrap(h.provisionCreate))
	mux.HandleFunc(jobs.TypeProvisionSuspend, h.wrap(h.provisionSuspend))
	mux.HandleFunc(jobs.TypeProvisionUnsuspend, h.wrap(h.provisionUnsuspend))
	mux.HandleFunc(jobs.TypeProvisionTerminate, h.wrap(h.provisionTerminate))
	mux.HandleFunc(jobs.TypeProvisionChangePackage, h.wrap(h.provisionChangePackage))
	mux.HandleFunc(jobs.TypeProvisionChangePassword, h.wrap(h.provisionChangePassword))
	mux.HandleFunc(jobs.TypeDomainRegister, h.wrap(h.domainRegister))
	mux.HandleFunc(jobs.TypeDomainTransfer, h.wrap(h.domainTransfer))
	mux.HandleFunc(jobs.TypeDomainRenew, h.wrap(h.domainRenew))
	mux.HandleFunc(jobs.TypeDomainSync, h.wrap(h.domainSync))
	mux.HandleFunc(jobs.TypeMailSend, h.wrap(h.mailSend))
	mux.HandleFunc(jobs.TypeInvoiceGeneratePDF, h.wrap(h.invoiceGeneratePDF))
	mux.HandleFunc(jobs.TypePaymentReconcileOne, h.wrap(h.paymentReconcileOne))

	// Periodic (cron) jobs - single-flight under the Redis lock.
	mux.HandleFunc(jobs.TypeCronInvoicesGenerate, h.wrap(h.cron(jobs.TypeCronInvoicesGenerate, d.Billing.GenerateRenewalInvoices)))
	mux.HandleFunc(jobs.TypeCronMarkOverdue, h.wrap(h.cron(jobs.TypeCronMarkOverdue, d.Billing.MarkOverdue)))
	mux.HandleFunc(jobs.TypeCronInvoiceReminders, h.wrap(h.cron(jobs.TypeCronInvoiceReminders, d.Billing.SendReminders)))
	mux.HandleFunc(jobs.TypeCronLateFees, h.wrap(h.cron(jobs.TypeCronLateFees, d.Billing.ApplyLateFees)))
	mux.HandleFunc(jobs.TypeCronAutoSuspend, h.wrap(h.cron(jobs.TypeCronAutoSuspend, d.Provisioning.AutoSuspend)))
	mux.HandleFunc(jobs.TypeCronAutoTerminate, h.wrap(h.cron(jobs.TypeCronAutoTerminate, d.Provisioning.AutoTerminate)))
	mux.HandleFunc(jobs.TypeCronPaymentReconcile, h.wrap(h.cron(jobs.TypeCronPaymentReconcile, d.Payments.ReconcilePending)))
	mux.HandleFunc(jobs.TypeCronDomainSync, h.wrap(h.cron(jobs.TypeCronDomainSync, d.Domains.SyncAllDomains)))
	mux.HandleFunc(jobs.TypeCronDomainVerificationSync,
		h.wrap(h.cron(jobs.TypeCronDomainVerificationSync, d.Domains.SyncAwaitingVerification)))
	mux.HandleFunc(jobs.TypeCronHousekeeping, h.wrap(h.cron(jobs.TypeCronHousekeeping, d.AdminOps.Housekeep)))
}
