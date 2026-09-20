package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fakes

type call struct {
	method string
	args   []any
}

type alert struct {
	subject string
	message string
}

// fakeDeps implements every consumer interface of Deps and records each call.
type fakeDeps struct {
	calls  []call
	errs   map[string]error // method name -> injected error
	counts map[string]int   // method name -> processed count to return
	alerts []alert
}

func (f *fakeDeps) record(method string, args ...any) error {
	f.calls = append(f.calls, call{method: method, args: args})
	return f.errs[method]
}

func (f *fakeDeps) recordCount(method string, args ...any) (int, error) {
	err := f.record(method, args...)
	return f.counts[method], err
}

// methodCalls returns the recorded calls excluding AlertAdmin/SendTemplate
// (which the wrap hook may add on failures).
func (f *fakeDeps) methodCalls() []call {
	var out []call
	for _, c := range f.calls {
		if c.method == "AlertAdmin" || c.method == "SendTemplate" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ports.ServiceActivator
func (f *fakeDeps) ActivateOrder(_ context.Context, orderID int64) error {
	return f.record("ActivateOrder", orderID)
}

// ProvisioningJobs
func (f *fakeDeps) AutoSuspend(_ context.Context) (int, error) { return f.recordCount("AutoSuspend") }
func (f *fakeDeps) AutoTerminate(_ context.Context) (int, error) {
	return f.recordCount("AutoTerminate")
}
func (f *fakeDeps) ProvisionCreate(_ context.Context, serviceID int64) error {
	return f.record("ProvisionCreate", serviceID)
}
func (f *fakeDeps) ProvisionSuspend(_ context.Context, serviceID int64, reason string) error {
	return f.record("ProvisionSuspend", serviceID, reason)
}
func (f *fakeDeps) ProvisionUnsuspend(_ context.Context, serviceID int64) error {
	return f.record("ProvisionUnsuspend", serviceID)
}
func (f *fakeDeps) ProvisionTerminate(_ context.Context, serviceID int64) error {
	return f.record("ProvisionTerminate", serviceID)
}
func (f *fakeDeps) ProvisionChangePackage(_ context.Context, serviceID int64) error {
	return f.record("ProvisionChangePackage", serviceID)
}
func (f *fakeDeps) ProvisionChangePassword(_ context.Context, serviceID int64, password string) error {
	return f.record("ProvisionChangePassword", serviceID, password)
}

// DomainJobs
func (f *fakeDeps) RegisterDomainJob(_ context.Context, domainID int64) error {
	return f.record("RegisterDomainJob", domainID)
}
func (f *fakeDeps) TransferDomainJob(_ context.Context, domainID int64) error {
	return f.record("TransferDomainJob", domainID)
}
func (f *fakeDeps) RenewDomainJob(_ context.Context, domainID int64) error {
	return f.record("RenewDomainJob", domainID)
}
func (f *fakeDeps) SyncDomainJob(_ context.Context, domainID int64) error {
	return f.record("SyncDomainJob", domainID)
}
func (f *fakeDeps) SyncAllDomains(_ context.Context) (int, error) {
	return f.recordCount("SyncAllDomains")
}

// BillingJobs
func (f *fakeDeps) GenerateRenewalInvoices(_ context.Context) (int, error) {
	return f.recordCount("GenerateRenewalInvoices")
}
func (f *fakeDeps) MarkOverdue(_ context.Context) (int, error) { return f.recordCount("MarkOverdue") }
func (f *fakeDeps) SendReminders(_ context.Context) (int, error) {
	return f.recordCount("SendReminders")
}
func (f *fakeDeps) ApplyLateFees(_ context.Context) (int, error) {
	return f.recordCount("ApplyLateFees")
}
func (f *fakeDeps) GenerateInvoicePDF(_ context.Context, invoiceID int64) error {
	return f.record("GenerateInvoicePDF", invoiceID)
}

// PaymentJobs
func (f *fakeDeps) ReconcilePending(_ context.Context) (int, error) {
	return f.recordCount("ReconcilePending")
}
func (f *fakeDeps) ReconcileOne(_ context.Context, transactionID int64) error {
	return f.record("ReconcileOne", transactionID)
}

// MailDeliverer
func (f *fakeDeps) DeliverEmail(_ context.Context, emailLogID int64) error {
	return f.record("DeliverEmail", emailLogID)
}

// Housekeeper
func (f *fakeDeps) Housekeep(_ context.Context) (int, error) { return f.recordCount("Housekeep") }

// ports.NotificationSender
func (f *fakeDeps) SendTemplate(_ context.Context, userID int64, templateKey string, _ map[string]any) error {
	return f.record("SendTemplate", userID, templateKey)
}
func (f *fakeDeps) AlertAdmin(_ context.Context, subject, message string) error {
	f.alerts = append(f.alerts, alert{subject: subject, message: message})
	return f.record("AlertAdmin", subject, message)
}

// fakeLocker records lock usage; held simulates the lock being taken elsewhere.
type fakeLocker struct {
	keys []string
	ttls []time.Duration
	held bool
	err  error
}

func (l *fakeLocker) WithLock(_ context.Context, key string, ttl time.Duration, fn func() error) error {
	l.keys = append(l.keys, key)
	l.ttls = append(l.ttls, ttl)
	if l.held {
		return apperr.Newf(apperr.CodeConflict, "lock %s already held", key)
	}
	if l.err != nil {
		return l.err
	}
	return fn()
}

// Harness

type env struct {
	mux    *asynq.ServeMux
	deps   *fakeDeps
	locker *fakeLocker
	logBuf *bytes.Buffer
}

func newFixture() *env {
	f := &fakeDeps{errs: map[string]error{}, counts: map[string]int{}}
	l := &fakeLocker{}
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mux := asynq.NewServeMux()
	RegisterHandlers(mux, Deps{
		Orders:        f,
		Provisioning:  f,
		Domains:       f,
		Billing:       f,
		Payments:      f,
		Notifications: f,
		AdminOps:      f,
		Notifier:      f,
		Locker:        l,
		Log:           log,
	})
	return &env{mux: mux, deps: f, locker: l, logBuf: buf}
}

func (e *env) process(t *testing.T, taskType string, payload any) error {
	t.Helper()
	var raw []byte
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	return e.processRaw(taskType, raw)
}

func (e *env) processRaw(taskType string, raw []byte) error {
	return e.mux.ProcessTask(context.Background(), asynq.NewTask(taskType, raw))
}

// setRetryMeta overrides the asynq retry metadata accessors for one test.
func setRetryMeta(t *testing.T, retried, maxRetry int, ok bool) {
	t.Helper()
	origRetry, origMax := getRetryCount, getMaxRetry
	getRetryCount = func(context.Context) (int, bool) { return retried, ok }
	getMaxRetry = func(context.Context) (int, bool) { return maxRetry, ok }
	t.Cleanup(func() { getRetryCount = origRetry; getMaxRetry = origMax })
}

// Dispatch tables

var oneOffDispatch = []struct {
	taskType string
	payload  any
	method   string
	args     []any
}{
	{jobs.TypeOrderActivate, jobs.OrderActivatePayload{OrderID: 41}, "ActivateOrder", []any{int64(41)}},
	{jobs.TypeProvisionCreate, jobs.ProvisionCreatePayload{ServiceID: 7}, "ProvisionCreate", []any{int64(7)}},
	{jobs.TypeProvisionSuspend, jobs.ProvisionSuspendPayload{ServiceID: 8, Reason: "Overdue on payment"}, "ProvisionSuspend", []any{int64(8), "Overdue on payment"}},
	{jobs.TypeProvisionUnsuspend, jobs.ProvisionUnsuspendPayload{ServiceID: 9}, "ProvisionUnsuspend", []any{int64(9)}},
	{jobs.TypeProvisionTerminate, jobs.ProvisionTerminatePayload{ServiceID: 10}, "ProvisionTerminate", []any{int64(10)}},
	{jobs.TypeProvisionChangePackage, jobs.ProvisionChangePackagePayload{ServiceID: 11, Package: "gold"}, "ProvisionChangePackage", []any{int64(11)}},
	{jobs.TypeProvisionChangePassword, jobs.ProvisionChangePasswordPayload{ServiceID: 12, Password: "S3cret!pass"}, "ProvisionChangePassword", []any{int64(12), "S3cret!pass"}},
	{jobs.TypeDomainRegister, jobs.DomainRegisterPayload{DomainID: 13, Years: 2}, "RegisterDomainJob", []any{int64(13)}},
	{jobs.TypeDomainTransfer, jobs.DomainTransferPayload{DomainID: 14, EPPCode: "epp-x", Years: 1}, "TransferDomainJob", []any{int64(14)}},
	{jobs.TypeDomainRenew, jobs.DomainRenewPayload{DomainID: 15, Years: 1}, "RenewDomainJob", []any{int64(15)}},
	{jobs.TypeDomainSync, jobs.DomainSyncPayload{DomainID: 16}, "SyncDomainJob", []any{int64(16)}},
	{jobs.TypeMailSend, jobs.MailSendPayload{EmailLogID: 17}, "DeliverEmail", []any{int64(17)}},
	{jobs.TypeInvoiceGeneratePDF, jobs.InvoiceGeneratePDFPayload{InvoiceID: 18}, "GenerateInvoicePDF", []any{int64(18)}},
	{jobs.TypePaymentReconcileOne, jobs.PaymentReconcileOnePayload{TransactionID: 19}, "ReconcileOne", []any{int64(19)}},
}

var cronDispatch = []struct {
	taskType string
	method   string
}{
	{jobs.TypeCronInvoicesGenerate, "GenerateRenewalInvoices"},
	{jobs.TypeCronMarkOverdue, "MarkOverdue"},
	{jobs.TypeCronInvoiceReminders, "SendReminders"},
	{jobs.TypeCronLateFees, "ApplyLateFees"},
	{jobs.TypeCronAutoSuspend, "AutoSuspend"},
	{jobs.TypeCronAutoTerminate, "AutoTerminate"},
	{jobs.TypeCronPaymentReconcile, "ReconcilePending"},
	{jobs.TypeCronDomainSync, "SyncAllDomains"},
	{jobs.TypeCronHousekeeping, "Housekeep"},
}

// One-off task routing

func TestRegisterHandlers_RoutesEveryOneOffTask(t *testing.T) {
	for _, tc := range oneOffDispatch {
		t.Run(tc.taskType, func(t *testing.T) {
			e := newFixture()
			require.NoError(t, e.process(t, tc.taskType, tc.payload))
			require.Equal(t, []call{{method: tc.method, args: tc.args}}, e.deps.calls)
			assert.Contains(t, e.logBuf.String(), "job done")
		})
	}
}

func TestOneOffTask_ServiceErrorPropagatesForRetry(t *testing.T) {
	e := newFixture()
	boom := errors.New("adapter down")
	e.deps.errs["ProvisionCreate"] = boom

	err := e.process(t, jobs.TypeProvisionCreate, jobs.ProvisionCreatePayload{ServiceID: 5})

	require.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, asynq.SkipRetry, "transient service errors must stay retryable")
	assert.Empty(t, e.deps.alerts, "no admin alert before the final retry")
}

func TestOneOffTask_MalformedPayloadSkipsRetry(t *testing.T) {
	for _, tc := range oneOffDispatch {
		t.Run(tc.taskType, func(t *testing.T) {
			e := newFixture()
			err := e.processRaw(tc.taskType, []byte(`{"broken`))
			require.Error(t, err)
			assert.ErrorIs(t, err, asynq.SkipRetry)
			assert.Empty(t, e.deps.methodCalls(), "service method must not run on bad payload")
		})
	}
}

func TestOneOffTask_MissingIDSkipsRetry(t *testing.T) {
	for _, tc := range oneOffDispatch {
		t.Run(tc.taskType, func(t *testing.T) {
			e := newFixture()
			err := e.processRaw(tc.taskType, []byte(`{}`))
			require.Error(t, err)
			assert.ErrorIs(t, err, asynq.SkipRetry)
			assert.Empty(t, e.deps.methodCalls(), "service method must not run without an id")
		})
	}
}

// A VALIDATION error (e.g. a client profile permanently missing address
// details) means retrying can never succeed - domainRegister/domainTransfer
// skip retry on it instead of burning asynq's exponential backoff.
func TestDomainJobs_ValidationErrorSkipsRetry(t *testing.T) {
	cases := []struct {
		taskType string
		payload  any
		method   string
	}{
		{jobs.TypeDomainRegister, jobs.DomainRegisterPayload{DomainID: 13}, "RegisterDomainJob"},
		{jobs.TypeDomainTransfer, jobs.DomainTransferPayload{DomainID: 14}, "TransferDomainJob"},
	}
	for _, tc := range cases {
		t.Run(tc.taskType, func(t *testing.T) {
			e := newFixture()
			e.deps.errs[tc.method] = apperr.Validation("client profile is missing address details")

			err := e.process(t, tc.taskType, tc.payload)

			require.Error(t, err)
			assert.ErrorIs(t, err, asynq.SkipRetry)
			assert.Len(t, e.deps.alerts, 1, "a skipped-retry validation failure still alerts the admin immediately")
		})
	}
}

// Other one-off tasks must not get this treatment: a VALIDATION error from,
// say, ProvisionCreate is not necessarily permanent, so it stays retryable.
func TestOneOffTask_ValidationErrorStaysRetryableOutsideDomainJobs(t *testing.T) {
	e := newFixture()
	e.deps.errs["ProvisionCreate"] = apperr.Validation("bad input")

	err := e.process(t, jobs.TypeProvisionCreate, jobs.ProvisionCreatePayload{ServiceID: 5})

	require.Error(t, err)
	assert.NotErrorIs(t, err, asynq.SkipRetry)
}

// Cron routing + locking

func TestRegisterHandlers_RoutesEveryCronUnderLock(t *testing.T) {
	for _, tc := range cronDispatch {
		t.Run(tc.taskType, func(t *testing.T) {
			e := newFixture()
			e.deps.counts[tc.method] = 3

			require.NoError(t, e.processRaw(tc.taskType, nil))

			require.Equal(t, []call{{method: tc.method, args: nil}}, e.deps.calls)
			require.Equal(t, []string{tc.taskType}, e.locker.keys, "lock key must be cron:<name>")
			require.Equal(t, []time.Duration{10 * time.Minute}, e.locker.ttls)
			assert.Contains(t, e.logBuf.String(), "processed=3", "processed count must be logged")
			assert.Contains(t, e.logBuf.String(), tc.taskType)
		})
	}
}

func TestCron_LockHeldElsewhereSkipsWithoutError(t *testing.T) {
	e := newFixture()
	e.locker.held = true

	require.NoError(t, e.processRaw(jobs.TypeCronInvoicesGenerate, nil))

	assert.Empty(t, e.deps.calls, "cron body must not run when the lock is held")
	assert.Contains(t, e.logBuf.String(), "lock held elsewhere")
}

func TestCron_LockInfraErrorPropagates(t *testing.T) {
	e := newFixture()
	e.locker.err = errors.New("redis down")

	err := e.processRaw(jobs.TypeCronDomainSync, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), jobs.TypeCronDomainSync)
	assert.Contains(t, err.Error(), "redis down")
	assert.Empty(t, e.deps.calls)
}

func TestCron_HandlerErrorPropagatesForRetry(t *testing.T) {
	e := newFixture()
	boom := errors.New("db unavailable")
	e.deps.errs["MarkOverdue"] = boom

	err := e.processRaw(jobs.TypeCronMarkOverdue, nil)

	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), jobs.TypeCronMarkOverdue)
}

func TestCron_BusinessConflictInsideLockIsNotSwallowed(t *testing.T) {
	e := newFixture()
	e.deps.errs["Housekeep"] = apperr.Conflict("state machine says no")

	err := e.processRaw(jobs.TypeCronHousekeeping, nil)

	require.Error(t, err, "CONFLICT from the cron body (lock acquired) must propagate")
	assert.Contains(t, err.Error(), "state machine says no")
	require.Len(t, e.deps.calls, 1, "cron body ran once")
}

// Final-retry admin alert

func TestWrap_FinalRetryAlertsAdmin(t *testing.T) {
	setRetryMeta(t, 5, 5, true)
	e := newFixture()
	e.deps.errs["ActivateOrder"] = errors.New("permanent boom")

	err := e.process(t, jobs.TypeOrderActivate, jobs.OrderActivatePayload{OrderID: 1})

	require.Error(t, err)
	require.Len(t, e.deps.alerts, 1)
	assert.Contains(t, e.deps.alerts[0].subject, jobs.TypeOrderActivate)
	assert.Contains(t, e.deps.alerts[0].message, "permanent boom")
	assert.Contains(t, e.deps.alerts[0].message, "5 of 5")
}

func TestWrap_NonFinalRetryDoesNotAlert(t *testing.T) {
	setRetryMeta(t, 1, 5, true)
	e := newFixture()
	e.deps.errs["ActivateOrder"] = errors.New("transient boom")

	err := e.process(t, jobs.TypeOrderActivate, jobs.OrderActivatePayload{OrderID: 1})

	require.Error(t, err)
	assert.Empty(t, e.deps.alerts)
}

func TestWrap_MissingRetryMetadataDoesNotAlert(t *testing.T) {
	// Plain context: asynq.GetRetryCount/GetMaxRetry report ok=false.
	e := newFixture()
	e.deps.errs["ActivateOrder"] = errors.New("boom")

	err := e.process(t, jobs.TypeOrderActivate, jobs.OrderActivatePayload{OrderID: 1})

	require.Error(t, err)
	assert.Empty(t, e.deps.alerts)
}

func TestWrap_SkipRetryAlertsImmediately(t *testing.T) {
	e := newFixture()

	err := e.processRaw(jobs.TypeInvoiceGeneratePDF, []byte(`not json`))

	require.ErrorIs(t, err, asynq.SkipRetry)
	require.Len(t, e.deps.alerts, 1, "SkipRetry is a permanent failure: alert the admin")
	assert.Contains(t, e.deps.alerts[0].subject, jobs.TypeInvoiceGeneratePDF)
}

func TestWrap_AlertFailureDoesNotMaskTaskError(t *testing.T) {
	setRetryMeta(t, 5, 5, true)
	e := newFixture()
	boom := errors.New("job boom")
	e.deps.errs["ActivateOrder"] = boom
	e.deps.errs["AlertAdmin"] = errors.New("smtp down")

	err := e.process(t, jobs.TypeOrderActivate, jobs.OrderActivatePayload{OrderID: 1})

	require.ErrorIs(t, err, boom)
	assert.Contains(t, e.logBuf.String(), "admin alert failed")
}

func TestIsFinalAttempt(t *testing.T) {
	tests := []struct {
		name    string
		retried int
		max     int
		ok      bool
		err     error
		want    bool
	}{
		{"skip retry is always final", 0, 5, false, asynq.SkipRetry, true},
		{"last attempt", 5, 5, true, errors.New("x"), true},
		{"beyond max", 6, 5, true, errors.New("x"), true},
		{"attempts left", 2, 5, true, errors.New("x"), false},
		{"no metadata", 9, 5, false, errors.New("x"), false},
		{"zero max retry fails final on first attempt", 0, 0, true, errors.New("x"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setRetryMeta(t, tc.retried, tc.max, tc.ok)
			assert.Equal(t, tc.want, isFinalAttempt(context.Background(), tc.err))
		})
	}
}

// Schedules

func TestSchedules_ExactSpecTable(t *testing.T) {
	want := []PeriodicTask{
		{Spec: "0 1 * * *", Type: jobs.TypeCronInvoicesGenerate},
		{Spec: "15 1 * * *", Type: jobs.TypeCronMarkOverdue},
		{Spec: "30 1 * * *", Type: jobs.TypeCronInvoiceReminders},
		{Spec: "0 2 * * *", Type: jobs.TypeCronLateFees},
		{Spec: "0 3 * * *", Type: jobs.TypeCronAutoSuspend},
		{Spec: "30 3 * * *", Type: jobs.TypeCronAutoTerminate},
		{Spec: "*/10 * * * *", Type: jobs.TypeCronPaymentReconcile},
		{Spec: "0 4 * * *", Type: jobs.TypeCronDomainSync},
		{Spec: "0 5 * * 0", Type: jobs.TypeCronHousekeeping},
	}
	require.Equal(t, want, Schedules())
}

func TestSchedules_EveryTypeHasARegisteredHandler(t *testing.T) {
	e := newFixture()
	for _, pt := range Schedules() {
		require.NoError(t, e.processRaw(pt.Type, nil),
			"scheduled type %s must have a handler", pt.Type)
	}
	require.Len(t, e.deps.calls, len(Schedules()))
}

// Misc

func TestRegisterHandlers_NilLoggerFallsBackToDefault(t *testing.T) {
	f := &fakeDeps{}
	mux := asynq.NewServeMux()
	RegisterHandlers(mux, Deps{
		Orders:        f,
		Provisioning:  f,
		Domains:       f,
		Billing:       f,
		Payments:      f,
		Notifications: f,
		AdminOps:      f,
		Notifier:      f,
		Locker:        &fakeLocker{},
		Log:           nil,
	})

	raw, err := json.Marshal(jobs.OrderActivatePayload{OrderID: 3})
	require.NoError(t, err)
	require.NoError(t, mux.ProcessTask(context.Background(), asynq.NewTask(jobs.TypeOrderActivate, raw)))
	require.Equal(t, []call{{method: "ActivateOrder", args: []any{int64(3)}}}, f.calls)
}

func TestRegisterHandlers_UnknownTypeIsNotHandled(t *testing.T) {
	e := newFixture()
	err := e.processRaw("nope:unknown", nil)
	require.Error(t, err, "unregistered task types must not be silently handled")
}
