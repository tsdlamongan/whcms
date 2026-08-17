package queue_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/internal/platform/queue"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enqueueArchived enqueues payload under taskType and immediately archives
// it (skipping the real backoff wait), returning its task id.
func enqueueArchived(t *testing.T, opt asynq.RedisClientOpt, taskType string, payload any, lastErr string) (queueName, id string) {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	client := queue.NewClient(opt)
	defer client.Close()
	info, err := client.Enqueue(asynq.NewTask(taskType, raw))
	require.NoError(t, err)

	insp := asynq.NewInspector(opt)
	defer insp.Close()
	require.NoError(t, insp.ArchiveTask(info.Queue, info.ID))
	return info.Queue, info.ID
}

func cleanQueues(t *testing.T, opt asynq.RedisClientOpt) {
	t.Helper()
	insp := asynq.NewInspector(opt)
	defer insp.Close()
	for _, q := range []string{"critical", "default", "low"} {
		_, _ = insp.DeleteAllPendingTasks(q)
		_, _ = insp.DeleteAllArchivedTasks(q)
		_, _ = insp.DeleteAllRetryTasks(q)
	}
}

func TestInspectorListModuleActionsFiltersOutNonModuleTypes(t *testing.T) {
	opt := testOpt(t)
	cleanQueues(t, opt)
	defer cleanQueues(t, opt)

	enqueueArchived(t, opt, jobs.TypeDomainRegister, jobs.DomainRegisterPayload{DomainID: 40, Years: 1}, "boom")
	enqueueArchived(t, opt, jobs.TypeMailSend, map[string]string{"to": "x@example.test"}, "boom") // NOT a module action

	insp := queue.NewInspector(opt)
	rows, total, err := insp.ListModuleActions(context.Background(), ports.ModuleActionFilter{PerPage: 10, Page: 1})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, jobs.TypeDomainRegister, rows[0].Type)
	assert.Equal(t, "archived", rows[0].State)
	// NOTE: asynq's ArchiveTask (used by the enqueueArchived test helper to
	// avoid waiting through a real retry backoff) moves a task straight from
	// pending to archived administratively - it never runs the handler, so
	// LastErr/LastFailedAt stay at their zero values here. A REAL archived
	// task (exhausted retries after an actual handler failure) has both
	// populated; that mapping is covered directly by toModuleAction's nil
	// checks, exercised implicitly by every other row surviving round-trip.
}

// TestInspectorListModuleActionsPopulatesFailureInfoAfterRealFailure runs an
// actual failing handler through a real asynq.Server (unlike the other tests
// here, which use ArchiveTask to short-circuit straight to archived without
// ever recording a failure) so LastErr/LastFailedAt genuinely get set by
// asynq itself, exercising toModuleAction's non-zero-time branch.
func TestInspectorListModuleActionsPopulatesFailureInfoAfterRealFailure(t *testing.T) {
	opt := testOpt(t)
	cleanQueues(t, opt)
	defer cleanQueues(t, opt)

	client := queue.NewClient(opt)
	defer client.Close()
	raw, err := json.Marshal(jobs.DomainRegisterPayload{DomainID: 41, Years: 1})
	require.NoError(t, err)
	info, err := client.Enqueue(asynq.NewTask(jobs.TypeDomainRegister, raw), asynq.MaxRetry(0))
	require.NoError(t, err)

	mux := asynq.NewServeMux()
	mux.HandleFunc(jobs.TypeDomainRegister, func(context.Context, *asynq.Task) error {
		return errors.New("boom from handler")
	})
	srv := asynq.NewServer(opt, asynq.Config{Concurrency: 1, Queues: map[string]int{info.Queue: 1}})
	require.NoError(t, srv.Start(mux))
	defer srv.Shutdown()

	rawInsp := asynq.NewInspector(opt)
	defer rawInsp.Close()
	require.Eventually(t, func() bool {
		ti, err := rawInsp.GetTaskInfo(info.Queue, info.ID)
		return err == nil && ti.State == asynq.TaskStateArchived
	}, 5*time.Second, 50*time.Millisecond, "task must reach archived after its one allowed failure")

	insp := queue.NewInspector(opt)
	rows, _, err := insp.ListModuleActions(context.Background(), ports.ModuleActionFilter{PerPage: 10, Page: 1})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "boom from handler", rows[0].LastErr)
	require.NotNil(t, rows[0].LastFailedAt)
}

func TestInspectorListModuleActionsFilterByType(t *testing.T) {
	opt := testOpt(t)
	cleanQueues(t, opt)
	defer cleanQueues(t, opt)

	enqueueArchived(t, opt, jobs.TypeDomainRegister, jobs.DomainRegisterPayload{DomainID: 1, Years: 1}, "e1")
	enqueueArchived(t, opt, jobs.TypeProvisionCreate, jobs.ProvisionCreatePayload{ServiceID: 43}, "e2")

	insp := queue.NewInspector(opt)
	rows, total, err := insp.ListModuleActions(context.Background(), ports.ModuleActionFilter{
		Type: jobs.TypeProvisionCreate, PerPage: 10, Page: 1,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, jobs.TypeProvisionCreate, rows[0].Type)
}

func TestInspectorListModuleActionsPaginates(t *testing.T) {
	opt := testOpt(t)
	cleanQueues(t, opt)
	defer cleanQueues(t, opt)

	for i := 0; i < 3; i++ {
		enqueueArchived(t, opt, jobs.TypeDomainRegister, jobs.DomainRegisterPayload{DomainID: int64(i), Years: 1}, "e")
	}

	insp := queue.NewInspector(opt)
	page1, total, err := insp.ListModuleActions(context.Background(), ports.ModuleActionFilter{PerPage: 2, Page: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, page1, 2)

	page2, total, err := insp.ListModuleActions(context.Background(), ports.ModuleActionFilter{PerPage: 2, Page: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, page2, 1)

	page3, total, err := insp.ListModuleActions(context.Background(), ports.ModuleActionFilter{PerPage: 2, Page: 3})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Empty(t, page3, "page past the end returns no rows, not an error")
}

func TestInspectorRedactsSensitivePayloadFields(t *testing.T) {
	opt := testOpt(t)
	cleanQueues(t, opt)
	defer cleanQueues(t, opt)

	enqueueArchived(t, opt, jobs.TypeProvisionChangePassword,
		jobs.ProvisionChangePasswordPayload{ServiceID: 5, Password: "s3cr3t"}, "panel unreachable")
	enqueueArchived(t, opt, jobs.TypeDomainTransfer,
		jobs.DomainTransferPayload{DomainID: 9, EPPCode: "EPP-TOP-SECRET", Years: 1}, "registrar error")

	insp := queue.NewInspector(opt)
	rows, _, err := insp.ListModuleActions(context.Background(), ports.ModuleActionFilter{PerPage: 10, Page: 1})
	require.NoError(t, err)
	require.Len(t, rows, 2)

	for _, row := range rows {
		var m map[string]any
		require.NoError(t, json.Unmarshal(row.Payload, &m))
		switch row.Type {
		case jobs.TypeProvisionChangePassword:
			assert.Equal(t, "[redacted]", m["password"], "plaintext panel password must never reach the admin UI")
		case jobs.TypeDomainTransfer:
			assert.Equal(t, "[redacted]", m["epp_code"], "EPP code must never reach the admin UI")
		}
	}
}

func TestInspectorRetryModuleActionMovesTaskToPending(t *testing.T) {
	opt := testOpt(t)
	cleanQueues(t, opt)
	defer cleanQueues(t, opt)

	q, id := enqueueArchived(t, opt, jobs.TypeDomainRegister, jobs.DomainRegisterPayload{DomainID: 40, Years: 1}, "boom")

	insp := queue.NewInspector(opt)
	require.NoError(t, insp.RetryModuleAction(context.Background(), q, id))

	rawInsp := asynq.NewInspector(opt)
	defer rawInsp.Close()
	info, err := rawInsp.GetTaskInfo(q, id)
	require.NoError(t, err)
	assert.Equal(t, asynq.TaskStatePending, info.State)
}

func TestInspectorDeleteModuleActionRemovesTask(t *testing.T) {
	opt := testOpt(t)
	cleanQueues(t, opt)
	defer cleanQueues(t, opt)

	q, id := enqueueArchived(t, opt, jobs.TypeDomainRegister, jobs.DomainRegisterPayload{DomainID: 40, Years: 1}, "boom")

	insp := queue.NewInspector(opt)
	require.NoError(t, insp.DeleteModuleAction(context.Background(), q, id))

	rawInsp := asynq.NewInspector(opt)
	defer rawInsp.Close()
	_, err := rawInsp.GetTaskInfo(q, id)
	assert.Error(t, err, "task must actually be gone")
}

func TestInspectorRetryUnknownTaskNotFound(t *testing.T) {
	opt := testOpt(t)
	insp := queue.NewInspector(opt)
	err := insp.RetryModuleAction(context.Background(), "default", "no-such-task-id")
	require.Error(t, err)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
}

func TestInspectorDeleteUnknownTaskNotFound(t *testing.T) {
	opt := testOpt(t)
	insp := queue.NewInspector(opt)
	err := insp.DeleteModuleAction(context.Background(), "default", "no-such-task-id")
	require.Error(t, err)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
}

func TestInspectorDismissAllModuleActions(t *testing.T) {
	opt := testOpt(t)
	cleanQueues(t, opt)
	defer cleanQueues(t, opt)

	enqueueArchived(t, opt, jobs.TypeDomainRegister, jobs.DomainRegisterPayload{DomainID: 1, Years: 1}, "e1")
	enqueueArchived(t, opt, jobs.TypeDomainRegister, jobs.DomainRegisterPayload{DomainID: 2, Years: 1}, "e2")
	enqueueArchived(t, opt, jobs.TypeProvisionCreate, jobs.ProvisionCreatePayload{ServiceID: 3}, "e3")
	enqueueArchived(t, opt, jobs.TypeMailSend, map[string]string{"to": "x@example.test"}, "e4") // NOT a module action

	insp := queue.NewInspector(opt)

	// Filtered dismiss: only the matching type is removed.
	n, err := insp.DismissAllModuleActions(context.Background(), ports.ModuleActionFilter{Type: jobs.TypeProvisionCreate})
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	rows, total, err := insp.ListModuleActions(context.Background(), ports.ModuleActionFilter{PerPage: 10, Page: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "the two DomainRegister rows must survive the type-filtered dismiss")
	for _, r := range rows {
		assert.Equal(t, jobs.TypeDomainRegister, r.Type)
	}

	// Unfiltered dismiss: everything module-action-shaped goes.
	n, err = insp.DismissAllModuleActions(context.Background(), ports.ModuleActionFilter{})
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	_, total, err = insp.ListModuleActions(context.Background(), ports.ModuleActionFilter{PerPage: 10, Page: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)

	// Nothing left to dismiss - 0, not an error.
	n, err = insp.DismissAllModuleActions(context.Background(), ports.ModuleActionFilter{})
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}
