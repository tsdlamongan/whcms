package snipe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Notification template keys (seeded in 000026_snipe_email_templates).
const (
	tplWon       = "snipe_won"
	tplExpired   = "snipe_expired"
	tplFailed    = "snipe_failed"
	tplCancelled = "snipe_cancelled"
	tplActive    = "snipe_active"
)

// Activity-trail event kinds. Terminal events reuse the new status string.
const (
	eventCreated   = "created"
	eventPaid      = "paid"
	eventPhase     = "phase"
	eventError     = "error"
	eventAvailable = "available"
	eventWon       = "won"
	eventLateWin   = "late_win"
)

const (
	// adminDefaultWindowDays is how long an admin hunt runs when the request
	// gives neither deadline_at nor window_days.
	adminDefaultWindowDays = 30
	// detailEventLimit caps the trail returned with one snipe.
	detailEventLimit = 100
	// maxEventMessage keeps one trail entry (and its row) small.
	maxEventMessage = 500
	// statusProbeTimeout bounds the reseller-balance lookup of the admin
	// overview so a slow registrar cannot hang the page.
	statusProbeTimeout = 5 * time.Second
	dateFormat         = "2006-01-02"
)

// Deps are the service/supervisor dependencies (small interfaces wired by the
// composition root; mocked in tests). The same Deps builds both the HTTP-side
// Service and the worker-side Supervisor.
type Deps struct {
	Tx         ports.TxManager
	Snipes     ports.SnipeRepo
	Plans      ports.SnipePlanRepo
	Domains    ports.SnipeDomainService
	DomainRepo ports.DomainRepo
	Clients    ports.ClientRepo
	Users      ports.UserRepo
	Registrar  ports.DomainSniper
	Settings   ports.SettingsRepo
	Notifier   ports.NotificationSender
	Audit      ports.AuditLogger
	Clock      ports.Clock
	Log        *slog.Logger

	// Invoices creates the up-front invoice of a client hunt; InvoiceCancel
	// voids it again when the hunt is cancelled or never paid (both billing).
	Invoices      ports.InvoiceCreator
	InvoiceCancel ports.InvoiceCanceller
	// Transactions lets voidInvoice see a gateway payment that is still in
	// flight: voiding the invoice under it would strand the client's money.
	Transactions ports.TransactionRepo

	// Test seams. Production leaves them nil and gets real sleeping, random
	// jitter and a unique per-process lease owner id.
	Sleep  func(ctx context.Context, d time.Duration) error
	Jitter func(max time.Duration) time.Duration
	Owner  string
}

func (d Deps) withDefaults() Deps {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Sleep == nil {
		d.Sleep = realSleep
	}
	if d.Jitter == nil {
		d.Jitter = randomJitter
	}
	if d.Owner == "" {
		d.Owner = newOwnerID()
	}
	return d
}

func realSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func randomJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(mrand.Int64N(int64(max)))
}

// newOwnerID is unique per process start, so a restarted worker never mistakes
// its predecessor's lease for its own.
func newOwnerID() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "worker"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b[:]))
}

// Service implements the snipe use-cases behind the HTTP handlers.
type Service struct {
	lifecycle
}

// New builds the snipe Service.
func New(d Deps) *Service {
	return &Service{lifecycle{d: d.withDefaults()}}
}

// AdminCreate starts a free hunt for a domain on behalf of a client: no
// invoice, no refund, active immediately. It fails fast on everything that
// would otherwise only surface as a dead hunt later - an unpriced TLD, an
// incomplete registrant profile, a domain already in the system.
func (s *Service) AdminCreate(ctx context.Context, actorUserID int64, in AdminCreateRequest) (*domain.DomainSnipe, error) {
	name := domains.NormalizeDomainName(in.Domain)
	if err := domains.ValidateDomainName(name); err != nil {
		return nil, err
	}
	years := in.Years
	if years == 0 {
		years = 1
	}
	var ns []string
	if len(in.Nameservers) > 0 {
		valid, err := domains.ValidateNameservers(in.Nameservers)
		if err != nil {
			return nil, err
		}
		ns = valid
	}

	now := s.d.Clock.Now()
	deadline, err := resolveDeadline(now, in)
	if err != nil {
		return nil, err
	}
	if err := validateWindow(in.HuntFrom, in.HuntUntil, deadline); err != nil {
		return nil, err
	}

	// The hunt registers under this client's profile; check it now rather than
	// letting the first attempt discover an incomplete one.
	if _, err := s.d.Domains.RegistrantContactFor(ctx, in.ClientID); err != nil {
		return nil, err
	}
	if err := s.ensureNotRegistered(ctx, name); err != nil {
		return nil, err
	}
	cfg, err := loadConfig(ctx, s.d.Settings)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err := s.ensureCapacity(ctx, cfg); err != nil {
		return nil, err
	}
	quote, err := s.d.Domains.QuoteRegistration(ctx, name, years)
	if err != nil {
		return nil, err
	}

	sn := &domain.DomainSnipe{
		ClientID:     in.ClientID,
		Source:       domain.SnipeSourceAdmin,
		DomainName:   name,
		Years:        years,
		Status:       domain.SnipeActive,
		Phase:        domain.SnipePhasePatient,
		HuntFrom:     in.HuntFrom,
		HuntUntil:    in.HuntUntil,
		Nameservers:  ns,
		RenewPerYear: quote.RenewPerYear,
		PaidAt:       &now,
		DeadlineAt:   &deadline,
	}
	if err := s.d.Snipes.Create(ctx, sn); err != nil {
		return nil, err
	}
	s.event(ctx, sn.ID, eventCreated, "hunt started by an administrator")
	s.d.Audit.Log(ctx, actorUserID, "snipe.create", "domain_snipe", sn.ID, nil, sn)
	return sn, nil
}

// ensureNotRegistered rejects a name the system already holds.
func (s *Service) ensureNotRegistered(ctx context.Context, name string) error {
	existing, err := s.d.DomainRepo.GetByName(ctx, name)
	if err != nil {
		if apperr.From(err).Code == apperr.CodeNotFound {
			return nil
		}
		return err
	}
	if existing != nil {
		return apperr.Conflict(name + " is already registered in the system")
	}
	return nil
}

// resolveDeadline picks when a hunt gives up: an explicit deadline_at, else
// window_days (default 30) from now. It must lie in the future.
func resolveDeadline(now time.Time, in AdminCreateRequest) (time.Time, error) {
	var deadline time.Time
	switch {
	case in.DeadlineAt != nil:
		deadline = *in.DeadlineAt
	default:
		days := in.WindowDays
		if days == 0 {
			days = adminDefaultWindowDays
		}
		deadline = now.AddDate(0, 0, days)
	}
	if !deadline.After(now) {
		return time.Time{}, apperr.Validation("deadline_at must be in the future")
	}
	return deadline, nil
}

// validateWindow checks the optional drop window: it needs a start, must be
// ordered, and must begin before the deadline.
func validateWindow(from, until *time.Time, deadline time.Time) error {
	if from == nil && until == nil {
		return nil
	}
	if from == nil {
		return apperr.Validation("hunt_until requires hunt_from")
	}
	if until != nil && !from.Before(*until) {
		return apperr.Validation("hunt_from must be before hunt_until")
	}
	if !from.Before(deadline) {
		return apperr.Validation("hunt_from must be before the deadline")
	}
	return nil
}

// AdminGet returns one snipe with its activity trail.
func (s *Service) AdminGet(ctx context.Context, id int64) (*Detail, error) {
	sn, err := s.d.Snipes.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	events, err := s.d.Snipes.ListEvents(ctx, id, detailEventLimit)
	if err != nil {
		return nil, err
	}
	if events == nil {
		events = []domain.SnipeEvent{}
	}
	return &Detail{Snipe: sn, Events: events}, nil
}

// AdminList is the admin list (status, domain-name search, client filter).
func (s *Service) AdminList(ctx context.Context, p ports.ListParams) ([]domain.DomainSnipe, int64, error) {
	return s.d.Snipes.List(ctx, p)
}

// AdminCancel stops a hunt. A paid hunt gets its domain price credited back
// (and the fee too when refundFee is set); a race with the hunt winning or
// expiring at the same instant is reported as CONFLICT, not silently dropped.
func (s *Service) AdminCancel(ctx context.Context, actorUserID, id int64, refundFee bool) (*domain.DomainSnipe, error) {
	sn, err := s.d.Snipes.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.ensureNotOwned(ctx, sn); err != nil {
		return nil, err
	}
	ok, err := s.finish(ctx, sn, domain.SnipeCancelled, finishOpts{
		RefundFee:   refundFee,
		Reason:      "cancelled by an administrator",
		ActorUserID: actorUserID,
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apperr.Conflict("the snipe changed state; reload and try again")
	}
	if sn.Status == domain.SnipePendingPayment {
		s.voidInvoice(ctx, sn, actorUserID)
	}
	return s.d.Snipes.GetByID(ctx, id)
}

// Status is the admin overview: supervisor switches, hunt counts and the
// reseller balance. A registrar failure is reported in the response instead of
// failing the whole page.
func (s *Service) Status(ctx context.Context) (*StatusResponse, error) {
	cfg, err := loadConfig(ctx, s.d.Settings)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	counts, err := s.d.Snipes.CountByStatus(ctx)
	if err != nil {
		return nil, err
	}
	hunting, err := s.d.Snipes.CountLeased(ctx, s.d.Clock.Now())
	if err != nil {
		return nil, err
	}
	out := &StatusResponse{
		Enabled:     cfg.Enabled,
		PauseReason: cfg.PauseReason,
		Counts:      make(map[string]int, len(counts)),
		Hunting:     hunting,
	}
	for st, n := range counts {
		out.Counts[string(st)] = n
	}
	if cfg.PauseReason != "" {
		out.PausedAt, _ = s.d.Settings.GetString(ctx, SettingPausedAt, "")
	}

	pctx, cancel := context.WithTimeout(ctx, statusProbeTimeout)
	defer cancel()
	info, err := s.d.Registrar.AccountInfo(pctx)
	switch {
	case err != nil:
		out.ResellerError = errText(err, apperr.From(err), ports.RegistrarCallErrorOf(err))
	case info != nil:
		balance := info.Balance
		out.ResellerBalance = &balance
	}
	return out, nil
}

// lifecycle holds the logic shared by the HTTP-side Service and the worker-side
// Supervisor: closing a snipe atomically (guarded status change + refund +
// trail entry in one transaction) and telling the client about it.
type lifecycle struct {
	d Deps
}

// lookupOwnership asks the registrar whether name is in our reseller account
// right now. A nil error means the answer is conclusive (owned or not);
// anything else (the call itself failed) leaves it unknown. A leftover record
// of a domain that is no longer live in our account (expired, pending delete)
// is "not owned". It does not take a limiter slot: the supervisor's hunts do
// that around it, the HTTP side makes one call per cancel.
func (l *lifecycle) lookupOwnership(ctx context.Context, name string, timeout time.Duration) (bool, *ports.DomainSyncInfo, error) {
	vctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	info, err := l.d.Registrar.SyncDomain(vctx, name)
	switch {
	case err == nil:
		if info != nil && !ownedStatus(info.Status) {
			return false, nil, nil
		}
		return true, info, nil
	case isNotFound(err):
		return false, nil, nil
	}
	return false, nil, err
}

// ensureNotOwned guards a cancellation of a running hunt: cancelling credits
// money back, which is only right if the registrar does not already hold the
// domain for us (a registration that was still settling when the hunt last
// looked). An unknown answer refuses too - better a retry in a moment than a
// refund for a domain we own. The hunt itself records the win shortly after.
func (l *lifecycle) ensureNotOwned(ctx context.Context, sn *domain.DomainSnipe) error {
	if sn.Status != domain.SnipeActive {
		return nil // never hunted: nothing can have been registered
	}
	timeout := defaultConfig().RequestTimeout
	if cfg, err := loadConfig(ctx, l.d.Settings); err == nil {
		timeout = cfg.RequestTimeout
	}
	owned, _, err := l.lookupOwnership(ctx, sn.DomainName, 2*timeout)
	switch {
	case err != nil:
		return apperr.New(apperr.CodeExternal,
			"cannot confirm the domain's registration status right now - please try again in a moment")
	case owned:
		return apperr.Conflict("this hunt has just secured the domain - refresh to see it")
	}
	return nil
}

// ensureCapacity refuses a new hunt once the supervisor has no room left for
// it: it only runs max_active_global hunts at once, so a snipe beyond that
// would be sold and then never hunted. Unpaid snipes count too - each becomes
// an active one the moment it is paid.
func (l *lifecycle) ensureCapacity(ctx context.Context, cfg Config) error {
	counts, err := l.d.Snipes.CountByStatus(ctx)
	if err != nil {
		return err
	}
	if committed := counts[domain.SnipeActive] + counts[domain.SnipePendingPayment]; committed >= cfg.MaxActiveGlobal {
		return apperr.Conflict("the domain sniper is fully booked right now - please try again later")
	}
	return nil
}

// voidInvoice cancels the invoice of a snipe that ended unpaid. Best effort:
// the invoice may already be cancelled, or may have been paid a moment ago (in
// which case the late-payment path refunds it) - neither is an error here.
//
// It leaves the invoice alone while a gateway payment on it is still pending
// (or when that cannot be checked): the client may be about to pay, and a paid
// invoice of an ended snipe is turned into account credit by
// ActivateSnipeAfterPayment, whereas paying a voided invoice has no such
// guarantee. An unpaid invoice that is left open simply lapses at its due date.
func (l *lifecycle) voidInvoice(ctx context.Context, sn *domain.DomainSnipe, actorUserID int64) {
	if sn.InvoiceID == nil {
		return
	}
	txs, err := l.d.Transactions.ListByInvoice(ctx, *sn.InvoiceID)
	if err != nil {
		l.d.Log.WarnContext(ctx, "snipe invoice kept: payments unreadable", "snipe", sn.ID, "invoice", *sn.InvoiceID, "error", err)
		return
	}
	for i := range txs {
		if txs[i].Status == domain.TxPending {
			l.d.Log.InfoContext(ctx, "snipe invoice kept: a payment is in flight", "snipe", sn.ID, "invoice", *sn.InvoiceID)
			return
		}
	}
	if _, err := l.d.InvoiceCancel.CancelInvoice(ctx, actorUserID, *sn.InvoiceID); err != nil &&
		apperr.From(err).Code != apperr.CodeConflict {
		l.d.Log.WarnContext(ctx, "snipe invoice not cancelled", "snipe", sn.ID, "invoice", *sn.InvoiceID, "error", err)
	}
}

// finishOpts tunes one terminal transition.
type finishOpts struct {
	Owner       string // hunt-side fencing: require this lease owner
	RequireIdle bool   // sweep: only when no live lease holds the row
	RefundFee   bool   // cancelled by an admin: also credit back the fee
	Reason      string // shown to the client and kept in last_error
	ActorUserID int64  // audit actor (0 = system)
	Attempts    int64  // the hunt's final attempt count to persist (0 = leave as stored)
}

// refundFor is the credit-back rule table. Nothing is refunded unless the
// snipe was paid, i.e. was active when it ended:
//
//	won                      nothing (the registration consumed the money)
//	expired                  domain price
//	cancelled (client)       domain price
//	cancelled (admin)        domain price, plus the fee when refundFee
//	failed (our/profile)     domain price + fee
func refundFor(s *domain.DomainSnipe, to domain.SnipeStatus, refundFee bool) int64 {
	if s.Status != domain.SnipeActive {
		return 0
	}
	switch to {
	case domain.SnipeExpired:
		return s.RefundableDomain
	case domain.SnipeCancelled:
		refund := s.RefundableDomain
		if refundFee {
			refund += s.RefundableFee
		}
		return refund
	case domain.SnipeFailed:
		return s.RefundableDomain + s.RefundableFee
	}
	return 0
}

// finish moves s to a terminal status. The guarded UPDATE, the credit-back and
// the trail entry commit together, so a crash can never leave money refunded
// for a still-active hunt or a closed hunt without its refund. It returns
// false (and changes nothing) when the row had already left s.Status or the
// lease guard failed - the caller lost a race and must not retry blindly.
func (l *lifecycle) finish(ctx context.Context, s *domain.DomainSnipe, to domain.SnipeStatus, o finishOpts) (bool, error) {
	if _, err := domain.TransitionSnipe(s.Status, to); err != nil {
		return false, err
	}
	refund := refundFor(s, to, o.RefundFee)
	now := l.d.Clock.Now()

	var applied, creditSkipped bool
	err := l.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		applied, creditSkipped = false, false
		ok, err := l.d.Snipes.Finish(ctx, ports.SnipeFinish{
			ID: s.ID, From: s.Status, To: to,
			Owner: o.Owner, RequireIdle: o.RequireIdle, Now: now,
			RefundAmount: refund, LastError: o.Reason, Attempts: o.Attempts,
		})
		if err != nil || !ok {
			return err
		}
		if refund > 0 {
			if _, err := l.d.Clients.AdjustCredit(ctx, s.ClientID, refund, "Domain snipe refund: "+s.DomainName, s.InvoiceID); err != nil {
				// A client deleted since paying has no account to credit. Failing
				// here would roll the closing back and leave the hunt running
				// (and the same refund failing) forever; close it and tell an
				// operator to settle the money by hand instead.
				var ae *apperr.Error
				if !errors.As(err, &ae) || ae.Code != apperr.CodeNotFound {
					return err
				}
				creditSkipped = true
			}
			if err := l.d.Snipes.MarkRefunded(ctx, s.ID, now); err != nil {
				return err
			}
		}
		if err := l.d.Snipes.AddEvent(ctx, &domain.SnipeEvent{
			SnipeID: s.ID, Kind: string(to), Message: truncate(trailMessage(to, o.Reason, refund), maxEventMessage),
		}); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil || !applied {
		return false, err
	}

	// Side effects after the commit, immune to the caller's cancellation (a
	// worker shutting down must still tell the client).
	bg := context.WithoutCancel(ctx)
	after := *s
	after.Status, after.RefundAmount = to, refund
	l.d.Audit.Log(bg, o.ActorUserID, "snipe."+string(to), "domain_snipe", s.ID, s, &after)
	if creditSkipped {
		if err := l.d.Notifier.AlertAdmin(bg, "Domain snipe refund could not be credited",
			fmt.Sprintf("Snipe #%d (%s) ended as %s with %s due back to client #%d, but that client account no longer exists. Settle the refund manually.",
				s.ID, s.DomainName, to, domain.FormatIDR(refund), s.ClientID)); err != nil {
			l.d.Log.WarnContext(bg, "admin alert failed", "snipe", s.ID, "error", err)
		}
	}
	l.notifyEnded(bg, s, to, refund)
	return true, nil
}

func trailMessage(to domain.SnipeStatus, reason string, refund int64) string {
	msg := string(to)
	if reason != "" {
		msg = reason
	}
	if refund > 0 {
		msg += fmt.Sprintf(" (refunded %s)", domain.FormatIDR(refund))
	}
	return msg
}

// templateFor maps a terminal status to its email template ("" = no email).
func templateFor(to domain.SnipeStatus) string {
	switch to {
	case domain.SnipeWon:
		return tplWon
	case domain.SnipeExpired:
		return tplExpired
	case domain.SnipeFailed:
		return tplFailed
	case domain.SnipeCancelled:
		return tplCancelled
	}
	return ""
}

// notifyEnded emails the owning client about a finished hunt. Failures are
// logged, never returned: the state change is already committed.
func (l *lifecycle) notifyEnded(ctx context.Context, s *domain.DomainSnipe, to domain.SnipeStatus, refund int64) {
	key := templateFor(to)
	if key == "" {
		return
	}
	// The reason is operator-facing (registrar wording, internals): it stays in
	// last_error and the trail, the email uses fixed text.
	data := map[string]any{"Domain": s.DomainName}
	if refund > 0 {
		data["RefundAmount"] = domain.FormatIDR(refund)
	}
	l.sendToClient(ctx, s, key, data)
}

// sendToClient resolves the owning user and sends a template.
func (l *lifecycle) sendToClient(ctx context.Context, s *domain.DomainSnipe, key string, data map[string]any) {
	cl, err := l.d.Clients.GetByID(ctx, s.ClientID)
	if err != nil {
		l.d.Log.WarnContext(ctx, "snipe notification skipped: client lookup failed",
			"snipe", s.ID, "template", key, "error", err)
		return
	}
	if err := l.d.Notifier.SendTemplate(ctx, cl.UserID, key, data); err != nil {
		l.d.Log.WarnContext(ctx, "snipe notification failed",
			"snipe", s.ID, "template", key, "error", err)
	}
}

// event appends a trail entry, logging (not returning) a failure: the trail is
// diagnostic, never worth failing the operation that produced it.
func (l *lifecycle) event(ctx context.Context, snipeID int64, kind, msg string) {
	if err := l.d.Snipes.AddEvent(ctx, &domain.SnipeEvent{
		SnipeID: snipeID, Kind: kind, Message: truncate(msg, maxEventMessage),
	}); err != nil {
		l.d.Log.WarnContext(ctx, "snipe event not recorded", "snipe", snipeID, "kind", kind, "error", err)
	}
}

// truncate shortens s to at most n runes (never splitting a UTF-8 sequence,
// which PostgreSQL would reject).
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
