package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/hibiken/asynq"
)

// cronLockTTL is the Redis lock TTL for periodic jobs (MODULES.md §3: 10m).
const cronLockTTL = 10 * time.Minute

// Retry metadata accessors, swappable in tests (asynq stores them in internal
// context keys that only the asynq server sets).
var (
	getRetryCount = asynq.GetRetryCount
	getMaxRetry   = asynq.GetMaxRetry
)

// handlers carries the deps + logger for every task handler.
type handlers struct {
	d   Deps
	log *slog.Logger
}

// wrap adds duration/outcome logging and the final-retry admin alert around a
// task handler. When the task fails on its final attempt (retry count has
// reached max retry, per asynq context metadata) or fails permanently with
// asynq.SkipRetry, Notifier.AlertAdmin is invoked; alert failures are logged
// and never mask the task error.
func (h *handlers) wrap(fn asynq.HandlerFunc) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		start := time.Now()
		err := fn(ctx, t)
		durationMS := time.Since(start).Milliseconds()
		if err == nil {
			h.log.InfoContext(ctx, "worker: job done", "type", t.Type(), "duration_ms", durationMS)
			return nil
		}

		retried, _ := getRetryCount(ctx)
		maxRetry, _ := getMaxRetry(ctx)
		final := isFinalAttempt(ctx, err)
		h.log.ErrorContext(ctx, "worker: job failed",
			"type", t.Type(), "retried", retried, "max_retry", maxRetry,
			"final", final, "duration_ms", durationMS, "error", err)

		if final {
			subject := fmt.Sprintf("WHCMS job failed permanently: %s", t.Type())
			message := fmt.Sprintf(
				"Task %q failed on its final attempt (retried %d of %d).\n\nError: %v",
				t.Type(), retried, maxRetry, err)
			if alertErr := h.d.Notifier.AlertAdmin(ctx, subject, message); alertErr != nil {
				h.log.ErrorContext(ctx, "worker: admin alert failed", "type", t.Type(), "error", alertErr)
			}
		}
		return err
	}
}

// isFinalAttempt reports whether err ends the task for good: either the
// handler asked to skip retries, or asynq metadata says this was the last try.
func isFinalAttempt(ctx context.Context, err error) bool {
	if errors.Is(err, asynq.SkipRetry) {
		return true
	}
	retried, okRetry := getRetryCount(ctx)
	maxRetry, okMax := getMaxRetry(ctx)
	return okRetry && okMax && retried >= maxRetry
}

// cron returns a handler that runs fn single-flight under
// Locker.WithLock("cron:<name>", 10m) and logs the processed count. When the
// lock is already held elsewhere the run is skipped without error (another
// worker instance is doing the work).
func (h *handlers) cron(taskType string, fn func(ctx context.Context) (int, error)) asynq.HandlerFunc {
	return func(ctx context.Context, _ *asynq.Task) error {
		var (
			processed int
			ran       bool
		)
		err := h.d.Locker.WithLock(ctx, taskType, cronLockTTL, func() error {
			ran = true
			var fnErr error
			processed, fnErr = fn(ctx)
			return fnErr
		})
		if err != nil {
			if !ran && apperr.From(err).Code == apperr.CodeConflict {
				h.log.InfoContext(ctx, "worker: cron skipped, lock held elsewhere", "cron", taskType)
				return nil
			}
			return fmt.Errorf("worker: %s: %w", taskType, err)
		}
		h.log.InfoContext(ctx, "worker: cron completed", "cron", taskType, "processed", processed)
		return nil
	}
}

// unmarshalPayload decodes the task payload; malformed payloads are permanent
// failures (asynq.SkipRetry).
func unmarshalPayload(t *asynq.Task, out any) error {
	if err := json.Unmarshal(t.Payload(), out); err != nil {
		return fmt.Errorf("worker: %s: unmarshal payload: %v: %w", t.Type(), err, asynq.SkipRetry)
	}
	return nil
}

// skipInvalidID is the permanent failure for a missing/invalid payload id.
func skipInvalidID(t *asynq.Task, field string, id int64) error {
	return fmt.Errorf("worker: %s: invalid %s %d: %w", t.Type(), field, id, asynq.SkipRetry)
}

// skipIfValidation treats a VALIDATION error (e.g. a client profile that is
// permanently missing required address details) as a final failure: retrying
// a fixed set of bad/missing data won't ever succeed, so asynq's exponential
// backoff would only delay the admin alert for no benefit.
func skipIfValidation(t *asynq.Task, err error) error {
	if err == nil {
		return nil
	}
	if apperr.From(err).Code == apperr.CodeValidation {
		return fmt.Errorf("worker: %s: %v: %w", t.Type(), err, asynq.SkipRetry)
	}
	return err
}

// --- one-off task handlers ---------------------------------------------------

func (h *handlers) orderActivate(ctx context.Context, t *asynq.Task) error {
	var p jobs.OrderActivatePayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.OrderID <= 0 {
		return skipInvalidID(t, "order_id", p.OrderID)
	}
	return h.d.Orders.ActivateOrder(ctx, p.OrderID)
}

func (h *handlers) provisionCreate(ctx context.Context, t *asynq.Task) error {
	var p jobs.ProvisionCreatePayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.ServiceID <= 0 {
		return skipInvalidID(t, "service_id", p.ServiceID)
	}
	return h.d.Provisioning.ProvisionCreate(ctx, p.ServiceID)
}

func (h *handlers) provisionSuspend(ctx context.Context, t *asynq.Task) error {
	var p jobs.ProvisionSuspendPayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.ServiceID <= 0 {
		return skipInvalidID(t, "service_id", p.ServiceID)
	}
	return h.d.Provisioning.ProvisionSuspend(ctx, p.ServiceID, p.Reason)
}

func (h *handlers) provisionUnsuspend(ctx context.Context, t *asynq.Task) error {
	var p jobs.ProvisionUnsuspendPayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.ServiceID <= 0 {
		return skipInvalidID(t, "service_id", p.ServiceID)
	}
	return h.d.Provisioning.ProvisionUnsuspend(ctx, p.ServiceID)
}

func (h *handlers) provisionTerminate(ctx context.Context, t *asynq.Task) error {
	var p jobs.ProvisionTerminatePayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.ServiceID <= 0 {
		return skipInvalidID(t, "service_id", p.ServiceID)
	}
	return h.d.Provisioning.ProvisionTerminate(ctx, p.ServiceID)
}

// provisionChangePackage ignores the payload Package field: the provisioning
// service reads the target package from services.pending_upgrade (MODULES §2
// signature takes only serviceID).
func (h *handlers) provisionChangePackage(ctx context.Context, t *asynq.Task) error {
	var p jobs.ProvisionChangePackagePayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.ServiceID <= 0 {
		return skipInvalidID(t, "service_id", p.ServiceID)
	}
	return h.d.Provisioning.ProvisionChangePackage(ctx, p.ServiceID)
}

func (h *handlers) provisionChangePassword(ctx context.Context, t *asynq.Task) error {
	var p jobs.ProvisionChangePasswordPayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.ServiceID <= 0 {
		return skipInvalidID(t, "service_id", p.ServiceID)
	}
	return h.d.Provisioning.ProvisionChangePassword(ctx, p.ServiceID, p.Password)
}

// Domain job payloads carry Years/EPPCode, but per MODULES §2 the domains
// service reads those from the domains row; only the id is dispatched.

func (h *handlers) domainRegister(ctx context.Context, t *asynq.Task) error {
	var p jobs.DomainRegisterPayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.DomainID <= 0 {
		return skipInvalidID(t, "domain_id", p.DomainID)
	}
	return skipIfValidation(t, h.d.Domains.RegisterDomainJob(ctx, p.DomainID))
}

func (h *handlers) domainTransfer(ctx context.Context, t *asynq.Task) error {
	var p jobs.DomainTransferPayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.DomainID <= 0 {
		return skipInvalidID(t, "domain_id", p.DomainID)
	}
	return skipIfValidation(t, h.d.Domains.TransferDomainJob(ctx, p.DomainID))
}

func (h *handlers) domainRenew(ctx context.Context, t *asynq.Task) error {
	var p jobs.DomainRenewPayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.DomainID <= 0 {
		return skipInvalidID(t, "domain_id", p.DomainID)
	}
	return h.d.Domains.RenewDomainJob(ctx, p.DomainID)
}

func (h *handlers) domainSync(ctx context.Context, t *asynq.Task) error {
	var p jobs.DomainSyncPayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.DomainID <= 0 {
		return skipInvalidID(t, "domain_id", p.DomainID)
	}
	return h.d.Domains.SyncDomainJob(ctx, p.DomainID)
}

// mailSend delivers a queued email_log row; notifications always insert the
// row before enqueueing (MODULES §3 M-NOTIFICATIONS).
func (h *handlers) mailSend(ctx context.Context, t *asynq.Task) error {
	var p jobs.MailSendPayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.EmailLogID <= 0 {
		return skipInvalidID(t, "email_log_id", p.EmailLogID)
	}
	return h.d.Notifications.DeliverEmail(ctx, p.EmailLogID)
}

func (h *handlers) invoiceGeneratePDF(ctx context.Context, t *asynq.Task) error {
	var p jobs.InvoiceGeneratePDFPayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.InvoiceID <= 0 {
		return skipInvalidID(t, "invoice_id", p.InvoiceID)
	}
	return h.d.Billing.GenerateInvoicePDF(ctx, p.InvoiceID)
}

func (h *handlers) paymentReconcileOne(ctx context.Context, t *asynq.Task) error {
	var p jobs.PaymentReconcileOnePayload
	if err := unmarshalPayload(t, &p); err != nil {
		return err
	}
	if p.TransactionID <= 0 {
		return skipInvalidID(t, "transaction_id", p.TransactionID)
	}
	return h.d.Payments.ReconcileOne(ctx, p.TransactionID)
}
