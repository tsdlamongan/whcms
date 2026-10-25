// Package jobs declares asynq task type constants and payload structs
// (CONTRACTS.md §7). Payloads are JSON-marshaled by the Enqueuer.
package jobs

// One-off task types.
const (
	TypeProvisionCreate         = "provision:create"
	TypeProvisionSuspend        = "provision:suspend"
	TypeProvisionUnsuspend      = "provision:unsuspend"
	TypeProvisionTerminate      = "provision:terminate"
	TypeProvisionChangePackage  = "provision:change_package"
	TypeProvisionChangePassword = "provision:change_password"

	TypeDomainRegister = "domain:register"
	TypeDomainTransfer = "domain:transfer"
	TypeDomainRenew    = "domain:renew"
	TypeDomainSync     = "domain:sync"

	TypeMailSend            = "mail:send"
	TypeInvoiceGeneratePDF  = "invoice:generate_pdf"
	TypePaymentReconcileOne = "payment:reconcile_one"
)

// Periodic (cron) task types - run by the worker scheduler under a Redis
// lock; handlers must be idempotent.
const (
	TypeCronInvoicesGenerate = "cron:invoices_generate"
	TypeCronMarkOverdue      = "cron:mark_overdue"
	TypeCronInvoiceReminders = "cron:invoice_reminders"
	TypeCronLateFees         = "cron:late_fees"
	TypeCronAutoSuspend      = "cron:auto_suspend"
	TypeCronAutoTerminate    = "cron:auto_terminate"
	TypeCronPaymentReconcile = "cron:payment_reconcile"
	TypeCronDomainSync       = "cron:domain_sync"
	// TypeCronDomainVerificationSync polls only the domains whose document /
	// email verification is still open, so an approved, bounced or lapsed
	// upload surfaces within minutes instead of at the daily sync.
	TypeCronDomainVerificationSync = "cron:domain_verification_sync"
	TypeCronHousekeeping           = "cron:housekeeping"
)

// DefaultMaxRetry is the standard retry policy (exponential backoff). On final
// failure the handler writes an integration/audit log and emails the admin.
const DefaultMaxRetry = 5

// ModuleActionTypes are the WHMCS-style "module actions" surfaced in the
// admin Pending Module Actions queue: provisioning + domain-registrar
// operations that can fail and need a manual admin retry. Deliberately
// excludes cron triggers, mail, PDF generation, payment reconciliation
// (noise, not module actions) and domain:sync (WHMCS treats sync as a
// background cron, not a queued module action).
var ModuleActionTypes = []string{
	TypeProvisionCreate,
	TypeProvisionSuspend,
	TypeProvisionUnsuspend,
	TypeProvisionTerminate,
	TypeProvisionChangePackage,
	TypeProvisionChangePassword,
	TypeDomainRegister,
	TypeDomainTransfer,
	TypeDomainRenew,
}

// ProvisionCreatePayload creates the hosting account for a pending service.
type ProvisionCreatePayload struct {
	ServiceID int64 `json:"service_id"`
}

// ProvisionSuspendPayload suspends a service's hosting account.
type ProvisionSuspendPayload struct {
	ServiceID int64  `json:"service_id"`
	Reason    string `json:"reason"`
}

// ProvisionUnsuspendPayload unsuspends a service's hosting account.
type ProvisionUnsuspendPayload struct {
	ServiceID int64 `json:"service_id"`
}

// ProvisionTerminatePayload terminates a service's hosting account.
type ProvisionTerminatePayload struct {
	ServiceID int64 `json:"service_id"`
}

// ProvisionChangePackagePayload changes the hosting package of a service.
type ProvisionChangePackagePayload struct {
	ServiceID int64  `json:"service_id"`
	Package   string `json:"package"`
}

// ProvisionChangePasswordPayload changes the panel password of a service.
// Password is the NEW plaintext password; the handler encrypts before storing.
type ProvisionChangePasswordPayload struct {
	ServiceID int64  `json:"service_id"`
	Password  string `json:"password"`
}

// DomainRegisterPayload registers a pending domain at the registrar.
type DomainRegisterPayload struct {
	DomainID int64 `json:"domain_id"`
	Years    int   `json:"years"`
}

// DomainTransferPayload transfers a domain in.
type DomainTransferPayload struct {
	DomainID int64  `json:"domain_id"`
	EPPCode  string `json:"epp_code"`
	Years    int    `json:"years"`
}

// DomainRenewPayload renews a domain.
type DomainRenewPayload struct {
	DomainID int64 `json:"domain_id"`
	Years    int   `json:"years"`
}

// DomainSyncPayload syncs status/expiry from the registrar.
type DomainSyncPayload struct {
	DomainID int64 `json:"domain_id"`
}

// MailSendPayload sends one email. EmailLogID references a queued email_log
// row created by the notifications module before enqueueing.
type MailSendPayload struct {
	EmailLogID int64 `json:"email_log_id,omitempty"`
}

// InvoiceGeneratePDFPayload renders and stores the invoice PDF.
type InvoiceGeneratePDFPayload struct {
	InvoiceID int64 `json:"invoice_id"`
}

// PaymentReconcileOnePayload re-checks one pending gateway transaction.
type PaymentReconcileOnePayload struct {
	TransactionID int64 `json:"transaction_id"`
}

// OrderActivatePayload activates all items of a paid order.
type OrderActivatePayload struct {
	OrderID int64 `json:"order_id"`
}

// TypeOrderActivate activates a paid order (enqueued by PaymentApplier).
const TypeOrderActivate = "order:activate"
