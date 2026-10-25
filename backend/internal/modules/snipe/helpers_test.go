package snipe

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

var errBoom = errors.New("boom")

// t0 is the fake clock's starting instant.
var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// fakeClock is a goroutine-safe ports.Clock whose time only moves when a test
// (or the fake Sleep) advances it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type sentTemplate struct {
	UserID int64
	Key    string
	Data   map[string]any
}

type creditCall struct {
	ClientID int64
	Delta    int64
	Reason   string
	Invoice  *int64
}

// fx bundles every mock plus recorders for what the code under test did.
type fx struct {
	tx         *mocks.MockTxManager
	snipes     *mocks.MockSnipeRepo
	plans      *mocks.MockSnipePlanRepo
	users      *mocks.MockUserRepo
	invoices   *mocks.MockInvoiceCreator
	canceller  *mocks.MockInvoiceCanceller
	txRepo     *mocks.MockTransactionRepo
	domains    *mocks.MockSnipeDomainService
	domainRepo *mocks.MockDomainRepo
	clients    *mocks.MockClientRepo
	registrar  *mocks.MockDomainSniper
	settings   *mocks.MockSettingsRepo
	notifier   *mocks.MockNotificationSender
	audit      *mocks.MockAuditLogger
	clock      *fakeClock

	mu          sync.Mutex
	sleeps      []time.Duration
	events      []domain.SnipeEvent
	templates   []sentTemplate
	alerts      []string
	finishes    []ports.SnipeFinish
	credits     []creditCall
	refunded    []int64
	released    []int64
	heartbeats  []ports.SnipeHeartbeat
	settingsSet map[string]any
	cfgOverride map[string]int
}

// newFx wires recording defaults: every Finish succeeds, every credit works,
// the client exists, Sleep advances the fake clock.
func newFx() *fx {
	f := &fx{
		tx:          &mocks.MockTxManager{},
		snipes:      &mocks.MockSnipeRepo{},
		plans:       &mocks.MockSnipePlanRepo{},
		users:       &mocks.MockUserRepo{},
		invoices:    &mocks.MockInvoiceCreator{},
		canceller:   &mocks.MockInvoiceCanceller{},
		txRepo:      &mocks.MockTransactionRepo{},
		domains:     &mocks.MockSnipeDomainService{},
		domainRepo:  &mocks.MockDomainRepo{},
		clients:     &mocks.MockClientRepo{},
		registrar:   &mocks.MockDomainSniper{},
		settings:    &mocks.MockSettingsRepo{},
		notifier:    &mocks.MockNotificationSender{},
		audit:       &mocks.MockAuditLogger{},
		clock:       &fakeClock{t: t0},
		settingsSet: map[string]any{},
		cfgOverride: map[string]int{},
	}
	f.snipes.AddEventFn = func(_ context.Context, e *domain.SnipeEvent) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.events = append(f.events, *e)
		return nil
	}
	f.snipes.FinishFn = func(_ context.Context, fin ports.SnipeFinish) (bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.finishes = append(f.finishes, fin)
		return true, nil
	}
	f.snipes.MarkRefundedFn = func(_ context.Context, id int64, _ time.Time) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.refunded = append(f.refunded, id)
		return nil
	}
	f.snipes.ReleaseFn = func(_ context.Context, id int64, _ string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.released = append(f.released, id)
		return nil
	}
	f.snipes.HeartbeatFn = func(_ context.Context, h ports.SnipeHeartbeat) (bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.heartbeats = append(f.heartbeats, h)
		return true, nil
	}
	f.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) {
		return &domain.Client{ID: 7, UserID: 70}, nil
	}
	f.clients.AdjustCreditFn = func(_ context.Context, id, delta int64, reason string, inv *int64) (int64, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.credits = append(f.credits, creditCall{ClientID: id, Delta: delta, Reason: reason, Invoice: inv})
		return delta, nil
	}
	f.notifier.SendTemplateFn = func(_ context.Context, uid int64, key string, data map[string]any) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.templates = append(f.templates, sentTemplate{UserID: uid, Key: key, Data: data})
		return nil
	}
	f.notifier.AlertAdminFn = func(_ context.Context, subject, _ string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.alerts = append(f.alerts, subject)
		return nil
	}
	f.settings.SetFn = func(_ context.Context, key string, v any) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.settingsSet[key] = v
		return nil
	}
	f.settings.GetIntFn = func(_ context.Context, key string, def int) (int, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if v, ok := f.cfgOverride[key]; ok {
			return v, nil
		}
		return def, nil
	}
	// A registrar that has never heard of the domain and is healthy.
	f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
		return nil, apperr.NotFound("domain")
	}
	f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) {
		return &ports.RegistrarAccountInfo{Balance: 5_000_000}, nil
	}
	f.registrar.EnsureCustomerFn = func(context.Context, ports.RegistrantContact) (int64, error) { return 501, nil }
	f.domains.RegistrantContactFn = func(context.Context, int64) (ports.RegistrantContact, error) {
		return ports.RegistrantContact{Email: "a@b.c"}, nil
	}
	f.domains.RecordSnipedDomainFn = func(_ context.Context, in ports.RecordSnipedDomainInput) (*domain.Domain, error) {
		return &domain.Domain{ID: 900, Name: in.Name, ClientID: in.ClientID}, nil
	}
	return f
}

func (f *fx) sleep(ctx context.Context, d time.Duration) error {
	if d > 0 {
		f.clock.Advance(d)
	}
	f.mu.Lock()
	f.sleeps = append(f.sleeps, d)
	f.mu.Unlock()
	return ctx.Err()
}

func (f *fx) deps() Deps {
	return Deps{
		Tx:         f.tx,
		Snipes:     f.snipes,
		Plans:      f.plans,
		Domains:    f.domains,
		DomainRepo: f.domainRepo,
		Clients:    f.clients,
		Users:      f.users,
		Registrar:  f.registrar,
		Settings:   f.settings,
		Notifier:   f.notifier,
		Audit:      f.audit,
		Clock:      f.clock,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sleep:      f.sleep,
		Jitter:     func(time.Duration) time.Duration { return 0 },
		Owner:      "w1",

		Invoices:      f.invoices,
		InvoiceCancel: f.canceller,
		Transactions:  f.txRepo,
	}
}

func (f *fx) service() *Service       { return New(f.deps()) }
func (f *fx) supervisor() *Supervisor { return NewSupervisor(f.deps()) }
func (f *fx) templateKeys() []string  { return f.keys(func(t sentTemplate) string { return t.Key }) }
func (f *fx) lastFinish() ports.SnipeFinish {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finishes[len(f.finishes)-1]
}

func (f *fx) keys(pick func(sentTemplate) string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.templates))
	for _, t := range f.templates {
		out = append(out, pick(t))
	}
	return out
}

func (f *fx) eventKinds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.events))
	for _, e := range f.events {
		out = append(out, e.Kind)
	}
	return out
}

// testCtx is a context with a real-time safety net so a runaway hunt loop
// fails the test instead of hanging it.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// activeSnipe is a hunt as the supervisor claims it: an admin hunt that has
// an hour left.
func (f *fx) activeSnipe(over func(*domain.DomainSnipe)) domain.DomainSnipe {
	deadline := f.clock.Now().Add(time.Hour)
	sn := domain.DomainSnipe{
		ID: 1, ClientID: 7, Source: domain.SnipeSourceAdmin, DomainName: "hunt.com", Years: 1,
		Status: domain.SnipeActive, Phase: domain.SnipePhasePatient, RenewPerYear: 160000,
		DeadlineAt: &deadline, LeaseOwner: "w1",
	}
	if over != nil {
		over(&sn)
	}
	return sn
}

// paidSnipe is a client hunt with money to give back on a loss.
func (f *fx) paidSnipe(over func(*domain.DomainSnipe)) domain.DomainSnipe {
	inv := int64(55)
	return f.activeSnipe(func(s *domain.DomainSnipe) {
		s.Source = domain.SnipeSourceClient
		s.InvoiceID = &inv
		s.FeeAmount, s.DomainAmount = 50_000, 150_000
		s.RefundableDomain, s.RefundableFee = 166_500, 55_500
		if over != nil {
			over(s)
		}
	})
}

// Registrar replies, as the rdash adapter shapes them.

func errTaken() error {
	return apperr.New(apperr.CodeConflict, "rdash: Domain not available").
		WithCause(&ports.RegistrarCallError{HTTPStatus: 409, Message: "Domain not available"})
}

func errRateLimited(after time.Duration) error {
	return apperr.New(apperr.CodeRateLimited, "rdash: Too Many Requests").
		WithCause(&ports.RegistrarCallError{HTTPStatus: 429, RetryAfter: after, Message: "Too Many Requests"})
}

func errUnauthorized() error {
	return apperr.New(apperr.CodeExternal, "rdash: Unauthenticated (check RDash credentials)").
		WithCause(&ports.RegistrarCallError{HTTPStatus: 401, Message: "Unauthenticated"})
}

func errAmbiguous() error {
	return apperr.External("rdash", &ports.RegistrarCallError{Message: "timeout", Ambiguous: true})
}

func errNotSent() error {
	return apperr.New(apperr.CodeExternal, "rdash: circuit breaker open, failing fast").
		WithCause(&ports.RegistrarCallError{NotSent: true, Message: "circuit breaker open"})
}

func errRejected(msg string, fields ...apperr.FieldError) error {
	return apperr.Validation("rdash: "+msg, fields...).
		WithCause(&ports.RegistrarCallError{HTTPStatus: 422, Message: msg})
}

func registered(name string) *ports.DomainResult {
	return &ports.DomainResult{Name: name, Status: "pending", OrderID: "77", ExpiryDate: time.Date(2027, 10, 24, 0, 0, 0, 0, time.UTC)}
}
