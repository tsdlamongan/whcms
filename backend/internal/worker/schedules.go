package worker

import "github.com/tsdlamongan/whcms/backend/internal/jobs"

// PeriodicTask is one scheduler entry: a cron spec (UTC, 5-field) plus the
// asynq task type to enqueue.
type PeriodicTask struct {
	Spec string
	Type string
}

// Schedules returns the periodic schedule (MODULES.md §3 M-WORKER). Every
// entry's task type has a handler registered by RegisterHandlers; each cron
// handler is idempotent and single-flight via the Redis lock.
func Schedules() []PeriodicTask {
	return []PeriodicTask{
		{Spec: "0 1 * * *", Type: jobs.TypeCronInvoicesGenerate},  // daily 01:00 UTC
		{Spec: "15 1 * * *", Type: jobs.TypeCronMarkOverdue},      // daily 01:15 UTC
		{Spec: "30 1 * * *", Type: jobs.TypeCronInvoiceReminders}, // daily 01:30 UTC
		{Spec: "0 2 * * *", Type: jobs.TypeCronLateFees},          // daily 02:00 UTC
		{Spec: "0 3 * * *", Type: jobs.TypeCronAutoSuspend},       // daily 03:00 UTC
		{Spec: "30 3 * * *", Type: jobs.TypeCronAutoTerminate},    // daily 03:30 UTC
		{Spec: "*/10 * * * *", Type: jobs.TypeCronPaymentReconcile},
		{Spec: "0 4 * * *", Type: jobs.TypeCronDomainSync}, // daily 04:00 UTC
		{Spec: "*/30 * * * *", Type: jobs.TypeCronDomainVerificationSync},
		{Spec: "0 5 * * 0", Type: jobs.TypeCronHousekeeping}, // weekly Sunday 05:00 UTC
	}
}
