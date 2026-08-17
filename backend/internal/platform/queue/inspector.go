package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/hibiken/asynq"
)

// moduleActionQueues are every asynq queue this app enqueues to (see
// NewServer's Queues config) - Inspector has no "list across all queues"
// call, so ListModuleActions scans each of these explicitly.
var moduleActionQueues = []string{"critical", "default", "low"}

// moduleActionListPageSize is generously larger than any realistic module-
// action backlog. Inspector's per-queue/per-state pagination has no
// server-side type filter, so ListModuleActions fetches this many per
// queue/state, filters+sorts in Go, then paginates the filtered result.
const moduleActionListPageSize = 1000

var moduleActionTypeSet = func() map[string]bool {
	m := make(map[string]bool, len(jobs.ModuleActionTypes))
	for _, t := range jobs.ModuleActionTypes {
		m[t] = true
	}
	return m
}()

// sensitivePayloadFields lists JSON keys redacted out of a task's payload
// before it ever reaches the admin UI (plaintext panel password, EPP code).
var sensitivePayloadFields = map[string][]string{
	jobs.TypeProvisionChangePassword: {"password"},
	jobs.TypeDomainTransfer:          {"epp_code"},
}

// Inspector implements ports.JobInspector over asynq's own Inspector.
type Inspector struct {
	insp *asynq.Inspector
}

var _ ports.JobInspector = (*Inspector)(nil)

// NewInspector builds an Inspector using the same Redis connection options
// as NewClient/NewServer.
func NewInspector(opt asynq.RedisClientOpt) *Inspector {
	return &Inspector{insp: asynq.NewInspector(opt)}
}

type taskWithState struct {
	task  *asynq.TaskInfo
	state string
}

// collectModuleActions gathers every archived+retry (or just filter.State,
// if set) module-action task across every queue matching filter.Type,
// newest-failure-first. Shared by ListModuleActions (paginates the result)
// and DismissAllModuleActions (dismisses the whole thing).
func (i *Inspector) collectModuleActions(filter ports.ModuleActionFilter) ([]taskWithState, error) {
	states := []string{"archived", "retry"}
	if filter.State != "" {
		states = []string{filter.State}
	}

	var all []taskWithState
	for _, q := range moduleActionQueues {
		for _, state := range states {
			tasks, err := i.listByState(q, state)
			if err != nil {
				if errors.Is(err, asynq.ErrQueueNotFound) {
					continue // queue never had a task enqueued to it yet
				}
				return nil, fmt.Errorf("queue: list %s tasks in %s: %w", state, q, err)
			}
			for _, t := range tasks {
				if !moduleActionTypeSet[t.Type] {
					continue
				}
				if filter.Type != "" && t.Type != filter.Type {
					continue
				}
				all = append(all, taskWithState{t, state})
			}
		}
	}

	sort.Slice(all, func(a, b int) bool {
		return all[a].task.LastFailedAt.After(all[b].task.LastFailedAt)
	})
	return all, nil
}

// ListModuleActions merges archived+retry (or just filter.State, if set)
// module-action tasks across every queue, newest-failure-first.
func (i *Inspector) ListModuleActions(_ context.Context, filter ports.ModuleActionFilter) ([]ports.ModuleAction, int64, error) {
	all, err := i.collectModuleActions(filter)
	if err != nil {
		return nil, 0, err
	}

	total := int64(len(all))
	page, perPage := filter.Page, filter.PerPage
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 10
	}
	start := (page - 1) * perPage
	if start >= len(all) {
		return []ports.ModuleAction{}, total, nil
	}
	end := start + perPage
	if end > len(all) {
		end = len(all)
	}

	out := make([]ports.ModuleAction, 0, end-start)
	for _, tw := range all[start:end] {
		out = append(out, toModuleAction(tw.task, tw.state))
	}
	return out, total, nil
}

func (i *Inspector) listByState(queue, state string) ([]*asynq.TaskInfo, error) {
	switch state {
	case "archived":
		return i.insp.ListArchivedTasks(queue, asynq.PageSize(moduleActionListPageSize))
	case "retry":
		return i.insp.ListRetryTasks(queue, asynq.PageSize(moduleActionListPageSize))
	default:
		return nil, fmt.Errorf("queue: unknown module action state %q", state)
	}
}

func toModuleAction(t *asynq.TaskInfo, state string) ports.ModuleAction {
	ma := ports.ModuleAction{
		ID:       t.ID,
		Queue:    t.Queue,
		Type:     t.Type,
		Payload:  redactPayload(t.Type, t.Payload),
		State:    state,
		MaxRetry: t.MaxRetry,
		Retried:  t.Retried,
		LastErr:  t.LastErr,
	}
	if !t.LastFailedAt.IsZero() {
		lf := t.LastFailedAt
		ma.LastFailedAt = &lf
	}
	if !t.NextProcessAt.IsZero() {
		np := t.NextProcessAt
		ma.NextProcessAt = &np
	}
	return ma
}

func redactPayload(taskType string, raw []byte) json.RawMessage {
	fields, ok := sensitivePayloadFields[taskType]
	if !ok || len(raw) == 0 {
		return json.RawMessage(raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return json.RawMessage(raw) // not valid JSON - leave untouched
	}
	for _, f := range fields {
		if _, exists := m[f]; exists {
			m[f] = "[redacted]"
		}
	}
	redacted, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(raw)
	}
	return json.RawMessage(redacted)
}

// RetryModuleAction moves an archived/retry task back to pending so it runs
// immediately, instead of waiting for its next scheduled backoff attempt (or,
// for an archived task, never running again at all without this).
func (i *Inspector) RetryModuleAction(_ context.Context, queue, id string) error {
	if err := i.insp.RunTask(queue, id); err != nil {
		if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
			return apperr.NotFound("module action")
		}
		return fmt.Errorf("queue: retry task %s/%s: %w", queue, id, err)
	}
	return nil
}

// DeleteModuleAction dismisses a module action without retrying it (e.g. the
// admin resolved it manually outside the system).
func (i *Inspector) DeleteModuleAction(_ context.Context, queue, id string) error {
	if err := i.insp.DeleteTask(queue, id); err != nil {
		if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
			return apperr.NotFound("module action")
		}
		return fmt.Errorf("queue: delete task %s/%s: %w", queue, id, err)
	}
	return nil
}

// DismissAllModuleActions dismisses every module action matching filter
// (ignoring Page/PerPage - the full matching set, across every queue). A
// task deleted by a concurrent admin action between collection and delete
// (already gone) is not an error for a bulk operation - it's simply not
// counted.
func (i *Inspector) DismissAllModuleActions(_ context.Context, filter ports.ModuleActionFilter) (int, error) {
	all, err := i.collectModuleActions(filter)
	if err != nil {
		return 0, err
	}

	dismissed := 0
	for _, tw := range all {
		if err := i.insp.DeleteTask(tw.task.Queue, tw.task.ID); err != nil {
			if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
				continue
			}
			return dismissed, fmt.Errorf("queue: delete task %s/%s: %w", tw.task.Queue, tw.task.ID, err)
		}
		dismissed++
	}
	return dismissed, nil
}
