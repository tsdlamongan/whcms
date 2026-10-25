package snipe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

const (
	// tickInterval is how often the supervisor looks for work.
	tickInterval = 2 * time.Second
	// heartbeatEvery is how often a hunt's lease is renewed and its progress
	// flushed; leaseTTL is how long a lease survives without one. A crashed
	// worker's hunts are re-claimed within leaseTTL. The heartbeat is also how
	// a hunt learns that its snipe was cancelled elsewhere (the API process
	// cannot signal this one), so its period is how long a cancelled hunt may
	// keep firing: one tick, not the better part of a drop window.
	heartbeatEvery = tickInterval
	leaseTTL       = 30 * time.Second
	// configTTL is how stale the settings snapshot may get.
	configTTL = 10 * time.Second
	// preflightTTL caches a healthy reseller-account check; preflightRetry is
	// how soon a failed one is tried again. (How often a paused supervisor
	// re-probes is the snipe.pause_probe_seconds setting.)
	preflightTTL   = 60 * time.Second
	preflightRetry = 5 * time.Second
	// eventDedupe is the minimum gap between two identical trail entries.
	eventDedupe = 5 * time.Minute
	// waitCap bounds one sleep while waiting for the drop window, so deadline
	// and cancellation are noticed.
	waitCap = 30 * time.Second
	// burstGiveUp is how many consecutive "taken" replies end a burst that
	// began because the name looked free (someone else won the race).
	burstGiveUp = 50
	// rejectCap is how many consecutive unexplained rejections a hunt
	// tolerates before giving up on the snipe.
	rejectCap    = 200
	expireBatch  = 20
	prepareRetry = 5 * time.Second
	// suspectWindow / suspectRecheck: after an unclear registrar reply the
	// registration may still be settling, so for a while every "taken" answer
	// is double-checked against our own account (at most once per recheck):
	// the name stops being free because WE now hold it.
	suspectWindow  = 30 * time.Second
	suspectRecheck = 2 * time.Second
	// prepareRejectCap is how many consecutive definitive refusals of the
	// client's details (not transient faults) end a hunt that has not started.
	prepareRejectCap = 6
	// alertEvery spaces a repeated operator alert (per key).
	alertEvery    = time.Hour
	winRecordWait = 2 * time.Second
	winRecordMax  = 5
	limiterPoll   = 5 * time.Millisecond
)

// errLostRace aborts the win transaction when the snipe left 'active' (an
// admin cancelled it) while the registrar call was in flight.
var errLostRace = errors.New("snipe left the active state")

// Supervisor is the worker-side runtime: it claims active snipes under an SQL
// lease and runs one hunt goroutine per claimed snipe. State lives in the
// database, so a restarted or second worker simply resumes (each resumed hunt
// first verifies against the registrar whether its previous incarnation
// already won).
type Supervisor struct {
	lifecycle

	mu        sync.Mutex
	hunts     map[int64]*hunt
	cfg       Config
	cfgAt     time.Time
	lastBeat  time.Time
	lastSweep time.Time
	pre       preflight
	probeAt   time.Time
	lim       *limiter
	wg        sync.WaitGroup
	expiring  atomic.Bool          // an expiry pass is already running
	alerted   map[string]time.Time // alert key -> last sent (guarded by mu)
}

// preflight caches the outcome of the reseller-account health check.
type preflight struct {
	ok    bool
	until time.Time
}

// NewSupervisor builds the supervisor from the shared Deps.
func NewSupervisor(d Deps) *Supervisor {
	d = d.withDefaults()
	return &Supervisor{
		lifecycle: lifecycle{d: d},
		hunts:     make(map[int64]*hunt),
		alerted:   make(map[string]time.Time),
		cfg:       defaultConfig(),
		lim:       &limiter{sleep: d.Sleep},
	}
}

// Run drives the supervisor until ctx is cancelled, then stops every hunt.
func (s *Supervisor) Run(ctx context.Context) {
	s.d.Log.InfoContext(ctx, "snipe supervisor started", "owner", s.d.Owner)
	for {
		s.Tick(ctx)
		if s.d.Sleep(ctx, tickInterval) != nil {
			break
		}
	}
	s.Shutdown()
}

// Shutdown cancels every running hunt and waits for them to release their
// leases.
func (s *Supervisor) Shutdown() {
	s.stopAll()
	s.wg.Wait()
	s.d.Log.Info("snipe supervisor stopped", "owner", s.d.Owner)
}

// Tick is one supervision pass: refresh settings, renew leases, expire idle
// hunts past their deadline, then (unless disabled or paused) claim new work.
func (s *Supervisor) Tick(ctx context.Context) {
	now := s.d.Clock.Now()
	s.refreshConfig(ctx, now)
	cfg := s.config()

	if !cfg.Enabled || cfg.PauseReason != "" {
		s.stopAll()
	}
	if s.lastBeat.IsZero() || now.Sub(s.lastBeat) >= heartbeatEvery {
		s.heartbeat(ctx, now)
		s.lastBeat = now
	}
	s.goExpireDue(ctx, now)
	if s.lastSweep.IsZero() || now.Sub(s.lastSweep) >= staleSweepEvery {
		s.sweepStale(ctx, now)
		s.lastSweep = now
	}

	if cfg.PauseReason != "" {
		s.probePause(ctx, now, cfg)
		return
	}
	if !cfg.Enabled {
		return
	}
	s.claim(ctx, now, cfg)
}

func (s *Supervisor) config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// refreshConfig reloads the settings snapshot when it is stale. A failed read
// keeps the previous snapshot (an admin edit lands a few seconds later, a
// database hiccup never stops hunting).
func (s *Supervisor) refreshConfig(ctx context.Context, now time.Time) {
	s.mu.Lock()
	fresh := !s.cfgAt.IsZero() && now.Sub(s.cfgAt) < configTTL
	s.mu.Unlock()
	if fresh {
		return
	}
	cfg, err := loadConfig(ctx, s.d.Settings)
	if err != nil {
		s.d.Log.WarnContext(ctx, "snipe settings unreadable, keeping previous snapshot", "error", err)
		s.mu.Lock()
		s.cfgAt = now
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.cfg, s.cfgAt = cfg, now
	s.mu.Unlock()
}

func (s *Supervisor) running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.hunts)
}

func (s *Supervisor) snapshotHunts() []*hunt {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*hunt, 0, len(s.hunts))
	for _, h := range s.hunts {
		out = append(out, h)
	}
	return out
}

func (s *Supervisor) stopAll() {
	for _, h := range s.snapshotHunts() {
		h.cancel()
	}
}

// heartbeat renews every running hunt's lease and flushes its counters. A
// refused heartbeat means the snipe was cancelled or its lease taken: stop.
func (s *Supervisor) heartbeat(ctx context.Context, now time.Time) {
	for _, h := range s.snapshotHunts() {
		ok, err := s.d.Snipes.Heartbeat(ctx, h.heartbeat(s.d.Owner, now.Add(leaseTTL)))
		if err != nil {
			s.d.Log.WarnContext(ctx, "snipe heartbeat failed", "snipe", h.sn.ID, "error", err)
			continue
		}
		if !ok {
			s.d.Log.InfoContext(ctx, "snipe lease lost or snipe no longer active, stopping hunt", "snipe", h.sn.ID)
			h.cancel()
		}
	}
}

// goExpireDue runs an expiry pass in the background, never two at once. A pass
// can make up to expireBatch sequential registrar lookups (each bounded by the
// request timeout, and each waiting for a limiter slot first); run inline they
// could hold the Tick - and with it every lease heartbeat - for longer than a
// lease lives.
func (s *Supervisor) goExpireDue(ctx context.Context, now time.Time) {
	if !s.expiring.CompareAndSwap(false, true) {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.expiring.Store(false)
		s.expireDue(ctx, now)
	}()
}

// expireDue closes active snipes past their deadline that no live hunt holds.
// A running hunt expires itself in its own loop.
//
// A refund can only follow a registrar answer of "not ours": the previous
// owner of this snipe may have had a register call in flight when it died, and
// refunding a domain the reseller account already holds would give the client
// their money back AND leave an unrecorded paid domain behind. So each snipe is
// checked first (a win is recorded as one; an inconclusive check postpones the
// closing to the next pass). While the sniper is paused or switched off the
// hunt could not run, so that downtime is not the client's loss: such a snipe
// ends as failed, with everything credited back.
func (s *Supervisor) expireDue(ctx context.Context, now time.Time) {
	due, err := s.d.Snipes.ListExpirable(ctx, now, expireBatch)
	if err != nil {
		s.d.Log.WarnContext(ctx, "snipe expiry sweep failed", "error", err)
		return
	}
	cfg := s.config()
	for i := range due {
		if s.settleIfOwned(ctx, newHunt(due[i], func() {}), cfg) {
			continue
		}
		to, reason := domain.SnipeExpired, "the hunting window closed without winning the domain"
		if !cfg.Enabled || cfg.PauseReason != "" {
			to, reason = domain.SnipeFailed, "the domain sniper was paused while this hunt was running"
		}
		if _, err := s.finish(ctx, &due[i], to, finishOpts{RequireIdle: true, Reason: reason}); err != nil {
			s.d.Log.WarnContext(ctx, "snipe expiry failed", "snipe", due[i].ID, "error", err)
		}
	}
}

// sweepStale closes snipes that were never paid: their invoice was cancelled,
// or has been overdue for a full day. The snipe is closed first and the
// invoice voided afterwards (best effort) - see Service.CancelForClient for why
// that order cannot strand a payment that lands in between.
func (s *Supervisor) sweepStale(ctx context.Context, now time.Time) {
	rows, err := s.d.Snipes.ListStalePending(ctx, now, staleSweepBatch)
	if err != nil {
		s.d.Log.WarnContext(ctx, "snipe stale-payment sweep failed", "error", err)
		return
	}
	for i := range rows {
		sn := &rows[i]
		ok, err := s.finish(ctx, sn, domain.SnipeCancelled, finishOpts{Reason: "payment was not received in time"})
		if err != nil {
			s.d.Log.WarnContext(ctx, "snipe stale-payment cancel failed", "snipe", sn.ID, "error", err)
			continue
		}
		if ok {
			s.voidInvoice(ctx, sn, 0)
		}
	}
}

// claim leases as many active snipes as there is capacity for and starts a
// hunt for each - but only after a healthy reseller-account preflight, so a
// dead credential or empty balance never fires a single register call.
//
// The preflight costs two registrar requests, so it only runs once there is
// actually something to hunt: an idle system (the usual case) never calls the
// registrar. Rows claimed just before a failed preflight are handed straight
// back, and a cached failure skips claiming altogether until it is retried.
func (s *Supervisor) claim(ctx context.Context, now time.Time, cfg Config) {
	capacity := cfg.MaxActiveGlobal - s.running()
	if capacity <= 0 {
		return
	}
	if now.Before(s.pre.until) && !s.pre.ok {
		return
	}
	rows, err := s.d.Snipes.Claim(ctx, s.d.Owner, capacity, now, now.Add(leaseTTL))
	if err != nil {
		s.d.Log.WarnContext(ctx, "snipe claim failed", "error", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	if !s.preflightOK(ctx, now, cfg) {
		bg := context.WithoutCancel(ctx)
		for _, sn := range rows {
			if err := s.d.Snipes.Release(bg, sn.ID, s.d.Owner); err != nil {
				s.d.Log.WarnContext(ctx, "snipe lease release failed", "snipe", sn.ID, "error", err)
			}
		}
		return
	}
	for _, sn := range rows {
		// A stalled tick can let our own lease lapse and re-claim a hunt this
		// process is still running: it just carries on (its next heartbeat
		// renews the lease); never start a second goroutine for it.
		if s.isRunning(sn.ID) {
			continue
		}
		s.start(ctx, sn)
	}
}

func (s *Supervisor) isRunning(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.hunts[id]
	return ok
}

// preflightOK checks (cached) that the reseller credentials work and the
// balance clears the configured floor. Credential or balance trouble pauses
// every hunt; a transient registrar error just skips this tick.
func (s *Supervisor) preflightOK(ctx context.Context, now time.Time, cfg Config) bool {
	if now.Before(s.pre.until) {
		return s.pre.ok
	}
	_, healthy, err := s.checkAccount(ctx, cfg)
	switch {
	case err != nil:
		oc := classify(err, cfg, "")
		if oc.kind == outPause {
			s.setPause(ctx, oc.msg, oc.balance)
		} else {
			s.d.Log.WarnContext(ctx, "snipe preflight failed, will retry", "error", err)
		}
		s.pre = preflight{ok: false, until: now.Add(preflightRetry)}
		return false
	case healthy != "":
		s.setPause(ctx, healthy, false)
		s.pre = preflight{ok: false, until: now.Add(preflightRetry)}
		return false
	}
	s.pre = preflight{ok: true, until: now.Add(preflightTTL)}
	return true
}

// checkAccount reads the reseller account. A non-empty string is a pause
// reason (balance below the floor); an error is a registrar failure.
func (s *Supervisor) checkAccount(ctx context.Context, cfg Config) (*ports.RegistrarAccountInfo, string, error) {
	actx, cancel := context.WithTimeout(ctx, 4*cfg.RequestTimeout)
	defer cancel()
	info, err := s.d.Registrar.AccountInfo(actx)
	if err != nil {
		return nil, "", err
	}
	if info != nil && info.Balance < cfg.MinResellerBalance {
		return info, fmt.Sprintf("reseller balance %s is below the configured floor %s",
			domain.FormatIDR(info.Balance), domain.FormatIDR(cfg.MinResellerBalance)), nil
	}
	return info, "", nil
}

// setPause records a global pause (visible to every worker and the admin UI),
// alerts the operator once, and stops this worker's hunts. The snipes
// themselves stay active: it is the reseller account that is broken, not them.
//
// balanceKind marks a pause caused by running out of money: the balance seen
// right now is remembered, and the pause then only ends once a probe sees it
// rise (a top-up). Without that, an empty-but-readable account would look
// "healthy" to the probe and the supervisor would resume, hit "insufficient
// balance" again and flap, alerting the operator every cycle.
func (s *Supervisor) setPause(ctx context.Context, reason string, balanceKind bool) {
	s.mu.Lock()
	already := s.cfg.PauseReason != ""
	s.cfg.PauseReason = reason
	s.mu.Unlock()
	s.stopAll()
	if already {
		return
	}
	bg := context.WithoutCancel(ctx)
	paused := int64(-1)
	if balanceKind {
		paused = s.balanceNow(bg)
	}
	s.mu.Lock()
	s.cfg.PausedBalance = paused
	s.mu.Unlock()
	if err := s.d.Settings.Set(bg, SettingPausedBalance, paused); err != nil {
		s.d.Log.WarnContext(ctx, "snipe paused balance not persisted", "error", err)
	}
	if err := s.d.Settings.Set(bg, SettingPauseReason, reason); err != nil {
		s.d.Log.WarnContext(ctx, "snipe pause not persisted", "error", err)
	}
	if err := s.d.Settings.Set(bg, SettingPausedAt, s.d.Clock.Now().UTC().Format(time.RFC3339)); err != nil {
		s.d.Log.WarnContext(ctx, "snipe pause time not persisted", "error", err)
	}
	s.d.Log.ErrorContext(ctx, "snipe supervisor paused", "reason", reason)
	if err := s.d.Notifier.AlertAdmin(bg, "Domain sniper paused",
		"All domain hunts are paused: "+reason+". They resume automatically once the registrar account is healthy again."); err != nil {
		s.d.Log.WarnContext(ctx, "snipe pause alert failed", "error", err)
	}
}

// balanceNow reads the reseller balance for a pause snapshot (-1 when it
// cannot be read, which makes the pause end on any healthy probe).
func (s *Supervisor) balanceNow(ctx context.Context) int64 {
	cfg := s.config()
	actx, cancel := context.WithTimeout(ctx, 4*cfg.RequestTimeout)
	defer cancel()
	info, err := s.d.Registrar.AccountInfo(actx)
	if err != nil || info == nil {
		return -1
	}
	return info.Balance
}

// probePause tries to end a pause: once the reseller account answers, the
// balance clears the floor and - for a pause caused by an empty balance - has
// risen above where it stood, clear the markers and let claiming resume.
func (s *Supervisor) probePause(ctx context.Context, now time.Time, cfg Config) {
	if !s.probeAt.IsZero() && now.Sub(s.probeAt) < cfg.PauseProbe {
		return
	}
	s.probeAt = now
	info, belowFloor, err := s.checkAccount(ctx, cfg)
	if err != nil || belowFloor != "" {
		return
	}
	if cfg.PausedBalance >= 0 && (info == nil || info.Balance <= cfg.PausedBalance) {
		return // out of money and not topped up yet
	}
	bg := context.WithoutCancel(ctx)
	if err := s.d.Settings.Set(bg, SettingPauseReason, ""); err != nil {
		s.d.Log.WarnContext(ctx, "snipe resume not persisted", "error", err)
		return
	}
	_ = s.d.Settings.Set(bg, SettingPausedAt, "")
	_ = s.d.Settings.Set(bg, SettingPausedBalance, -1)
	s.mu.Lock()
	s.cfg.PauseReason = ""
	s.cfg.PausedBalance = -1
	s.mu.Unlock()
	s.pre = preflight{ok: true, until: now.Add(preflightTTL)}
	s.d.Log.InfoContext(ctx, "snipe supervisor resumed")
	if err := s.d.Notifier.AlertAdmin(bg, "Domain sniper resumed",
		"The registrar account is healthy again; domain hunts have resumed."); err != nil {
		s.d.Log.WarnContext(ctx, "snipe resume alert failed", "error", err)
	}
}

// start launches the hunt goroutine for a freshly claimed snipe.
func (s *Supervisor) start(ctx context.Context, sn domain.DomainSnipe) {
	hctx, cancel := context.WithCancel(ctx)
	h := newHunt(sn, cancel)
	s.mu.Lock()
	s.hunts[sn.ID] = h
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.hunts, sn.ID)
			s.mu.Unlock()
		}()
		s.runHunt(hctx, h)
	}()
}

// hunt is the in-memory state of one running snipe. Only its own goroutine
// mutates sn; the counters are read by the heartbeat concurrently.
type hunt struct {
	sn     domain.DomainSnipe
	cancel context.CancelFunc

	customerID int64
	ns         []string

	attempts    atomic.Int64
	lastAttempt atomic.Int64 // unix nanos, 0 = none yet

	mu      sync.Mutex
	phase   domain.SnipePhase
	lastErr string
	events  map[string]time.Time
}

func newHunt(sn domain.DomainSnipe, cancel context.CancelFunc) *hunt {
	h := &hunt{sn: sn, cancel: cancel, phase: sn.Phase, lastErr: sn.LastError, events: map[string]time.Time{}}
	if h.phase == "" {
		h.phase = domain.SnipePhasePatient
	}
	h.attempts.Store(sn.Attempts)
	return h
}

func (h *hunt) heartbeat(owner string, until time.Time) ports.SnipeHeartbeat {
	h.mu.Lock()
	phase, lastErr := h.phase, h.lastErr
	h.mu.Unlock()
	hb := ports.SnipeHeartbeat{
		ID: h.sn.ID, Owner: owner, LeaseUntil: until,
		Attempts: h.attempts.Load(), Phase: phase, LastError: lastErr,
	}
	if ns := h.lastAttempt.Load(); ns != 0 {
		t := time.Unix(0, ns)
		hb.LastAttemptAt = &t
	}
	return hb
}

// setPhase records the phase and reports whether it changed.
func (h *hunt) setPhase(p domain.SnipePhase) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.phase == p {
		return false
	}
	h.phase = p
	return true
}

func (h *hunt) setLastError(msg string) {
	h.mu.Lock()
	h.lastErr = truncate(msg, maxEventMessage)
	h.mu.Unlock()
}

// dedupe reports whether an event with this key should be written now (at
// most once per eventDedupe).
func (h *hunt) dedupe(key string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if last, ok := h.events[key]; ok && now.Sub(last) < eventDedupe {
		return false
	}
	h.events[key] = now
	return true
}

type mode int

const (
	modeWait mode = iota
	modePatient
	modeBurst
)

// mode decides what the hunt does at now. With a drop window it stays silent
// until burstLead before hunt_from, bursts through the window, and probes
// patiently afterwards; without one (or once the probe sees the name free) it
// probes and only bursts when availability flips.
func (h *hunt) mode(cfg Config, now time.Time, availBurst bool) (mode, time.Duration) {
	if from := h.sn.HuntFrom; from != nil {
		start := from.Add(-cfg.BurstLead)
		if now.Before(start) {
			return modeWait, start.Sub(now)
		}
		if h.sn.HuntUntil == nil || !now.After(*h.sn.HuntUntil) {
			return modeBurst, 0
		}
	}
	if availBurst {
		return modeBurst, 0
	}
	return modePatient, 0
}

// runHunt is the life of one claimed snipe. It returns when the snipe ends
// (won, expired, failed), the supervisor pauses or stops, or the lease is
// lost; the deferred release frees the lease for anyone else to resume.
func (s *Supervisor) runHunt(ctx context.Context, h *hunt) {
	defer func() {
		if err := s.d.Snipes.Release(context.WithoutCancel(ctx), h.sn.ID, s.d.Owner); err != nil {
			s.d.Log.Warn("snipe lease release failed", "snipe", h.sn.ID, "error", err)
		}
	}()
	if !s.prepare(ctx, h) {
		return
	}

	var (
		needsVerify  = true // always check our own account first: we may have won before a crash
		availBurst   bool
		takenStreak  int
		rejectStreak int
		suspectUntil time.Time // see suspectWindow
		lastSuspect  time.Time
	)
	for ctx.Err() == nil {
		cfg := s.config()
		if !cfg.Enabled || cfg.PauseReason != "" {
			return
		}
		now := s.d.Clock.Now()
		if dl := h.sn.DeadlineAt; dl != nil && !now.Before(*dl) {
			s.expire(ctx, h)
			return
		}

		if needsVerify {
			concluded, won := s.verify(ctx, h, cfg)
			if won {
				return
			}
			if !concluded {
				s.pause(ctx, h, cfg.PatientInterval)
				continue
			}
			needsVerify = false
		}

		m, wait := h.mode(cfg, now, availBurst)
		switch m {
		case modeWait:
			s.setPhase(ctx, h, domain.SnipePhasePatient, "waiting for the drop window")
			s.pause(ctx, h, minDuration(wait, waitCap))

		case modePatient:
			s.setPhase(ctx, h, domain.SnipePhasePatient, "probing availability")
			avail, err := s.probe(ctx, h, cfg)
			switch {
			case err == nil && avail:
				availBurst, takenStreak = true, 0
				s.recordEvent(ctx, h, eventAvailable, "the domain looks available, bursting register calls")
			case err == nil:
				h.setLastError("")
				s.pause(ctx, h, cfg.PatientInterval)
			case isCtxErr(ctx, err):
				return
			default:
				if stop := s.handleProbeError(ctx, h, cfg, err); stop {
					return
				}
			}

		case modeBurst:
			s.setPhase(ctx, h, domain.SnipePhaseBurst, "bursting register calls")
			res, err := s.attempt(ctx, h, cfg)
			if err == nil {
				if s.win(ctx, h, *res) {
					return
				}
				// Registered but not recorded yet: keep the hunt (and its lease)
				// alive and settle it through the ownership check, which
				// retries the recording, instead of dropping it.
				needsVerify = true
				s.pause(ctx, h, cfg.PatientInterval)
				continue
			}
			if isCtxErr(ctx, err) {
				// The call was cut off (shutdown, cancelled, lease lost): the
				// registrar may still have processed it.
				s.afterAbort(ctx, h, cfg)
				return
			}
			oc := s.vetPause(ctx, cfg, classify(err, cfg, h.sn.DomainName))
			switch oc.kind {
			case outTaken:
				rejectStreak = 0
				takenStreak++
				h.setLastError(oc.msg)
				if now.Before(suspectUntil) && now.Sub(lastSuspect) >= suspectRecheck {
					needsVerify, lastSuspect = true, now
				}
				if availBurst && takenStreak >= burstGiveUp {
					availBurst, takenStreak = false, 0
					s.recordEvent(ctx, h, eventPhase, "someone else got the domain first, back to probing")
				}
				s.pause(ctx, h, cfg.BurstInterval+s.d.Jitter(cfg.BurstJitter))
			case outBackoff:
				s.recordError(ctx, h, "rate limited: "+oc.msg)
				s.pause(ctx, h, oc.wait+s.d.Jitter(cfg.BurstJitter))
			case outAmbiguous:
				needsVerify = true
				suspectUntil = now.Add(suspectWindow)
				s.recordError(ctx, h, "unclear registrar reply, verifying: "+oc.msg)
				s.pause(ctx, h, cfg.BurstInterval)
			case outNotSent:
				s.pause(ctx, h, oc.wait)
			case outPause:
				s.setPause(ctx, oc.msg, oc.balance)
				return
			case outFail:
				s.fail(ctx, h, oc.msg)
				return
			default: // outRetry
				rejectStreak++
				s.recordError(ctx, h, oc.msg)
				if rejectStreak >= rejectCap {
					s.fail(ctx, h, "the registrar keeps rejecting the registration: "+oc.msg)
					return
				}
				s.pause(ctx, h, cfg.BurstInterval+s.d.Jitter(cfg.BurstJitter))
			}
		}
	}
}

// handleProbeError reacts to a failed availability probe; true = end the hunt.
func (s *Supervisor) handleProbeError(ctx context.Context, h *hunt, cfg Config, err error) bool {
	oc := s.vetPause(ctx, cfg, classify(err, cfg, h.sn.DomainName))
	switch oc.kind {
	case outPause:
		s.setPause(ctx, oc.msg, oc.balance)
		return true
	case outBackoff, outNotSent:
		s.recordError(ctx, h, oc.msg)
		s.pause(ctx, h, oc.wait)
	default:
		s.recordError(ctx, h, oc.msg)
		s.pause(ctx, h, cfg.PatientInterval)
	}
	return false
}

// prepare resolves what a hunt needs before its first call: the registrar
// customer (resolved once, cached on the row) and the nameservers. A profile
// the registrar would reject ends the snipe; transient trouble is retried.
func (s *Supervisor) prepare(ctx context.Context, h *hunt) bool {
	h.ns = s.nameservers(ctx, h)
	if h.sn.RegistrarCustomerID != nil {
		h.customerID = *h.sn.RegistrarCustomerID
		return true
	}
	rejections := 0
	for ctx.Err() == nil {
		cfg := s.config()
		contact, err := s.d.Domains.RegistrantContactFor(ctx, h.sn.ClientID)
		if err != nil {
			if apperr.From(err).Code == apperr.CodeValidation {
				s.fail(ctx, h, "the account profile is missing the address details the registrar requires")
				return false
			}
			s.recordError(ctx, h, "cannot read the client profile: "+err.Error())
			if s.d.Sleep(ctx, prepareRetry) != nil {
				return false
			}
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 4*cfg.RequestTimeout)
		id, err := s.d.Registrar.EnsureCustomer(rctx, contact)
		cancel()
		if err == nil {
			h.customerID = id
			if err := s.d.Snipes.SetCustomerID(ctx, h.sn.ID, id); err != nil {
				s.d.Log.WarnContext(ctx, "snipe customer id not cached", "snipe", h.sn.ID, "error", err)
			}
			return true
		}
		if isCtxErr(ctx, err) {
			return false
		}
		oc := s.vetPause(ctx, cfg, classify(err, cfg, h.sn.DomainName))
		switch oc.kind {
		case outPause:
			s.setPause(ctx, oc.msg, oc.balance)
			return false
		case outFail:
			s.fail(ctx, h, oc.msg)
			return false
		case outRetry:
			// A definitive refusal of the account details (say a malformed phone
			// number) will not fix itself: end the hunt with a full refund
			// instead of retrying until the deadline and keeping the fee.
			rejections++
			if rejections >= prepareRejectCap {
				s.fail(ctx, h, "the registrar rejects the account details: "+oc.msg)
				return false
			}
		default:
			rejections = 0 // transient (timeout, breaker, rate limit): keep trying
		}
		s.recordError(ctx, h, "registrar customer lookup failed: "+oc.msg)
		if s.d.Sleep(ctx, prepareRetry) != nil {
			return false
		}
	}
	return false
}

// nameservers picks the hunt's nameservers: its own, else the system default.
func (s *Supervisor) nameservers(ctx context.Context, h *hunt) []string {
	if len(h.sn.Nameservers) > 0 {
		return h.sn.Nameservers
	}
	var ns []string
	if err := s.d.Settings.GetJSON(ctx, settingDefaultNameservers, &ns); err != nil {
		s.d.Log.WarnContext(ctx, "default nameservers unreadable, registering without", "error", err)
		return nil
	}
	return ns
}

// ownership asks our own registrar account whether name is ours right now: the
// ground truth after a crash, an ambiguous register reply, or before refunding.
// A nil error means the answer is conclusive (owned or not); anything else
// (the call itself failed) leaves it unknown. A leftover record of a domain
// that is no longer live in our account (expired, pending delete) is "not
// owned".
func (s *Supervisor) ownership(ctx context.Context, name string, cfg Config) (bool, *ports.DomainSyncInfo, error) {
	release, err := s.lim.acquire(ctx, cfg.MaxInFlight)
	if err != nil {
		return false, nil, err
	}
	defer release()
	return s.lookupOwnership(ctx, name, cfg.RequestTimeout)
}

// resultOf builds the registration result a hunt records from an ownership
// answer (the registrar's own view of the domain).
func resultOf(name string, info *ports.DomainSyncInfo) ports.DomainResult {
	res := ports.DomainResult{Name: name, Status: statusOf(info)}
	if info != nil {
		res.ExpiryDate = info.ExpiryDate
	}
	return res
}

// verify asks our own registrar account whether the domain is already ours -
// the answer after a crash or an ambiguous register reply. concluded=false
// means it could not be established (try again); won=true means the win has
// been recorded.
func (s *Supervisor) verify(ctx context.Context, h *hunt, cfg Config) (concluded, won bool) {
	owned, info, err := s.ownership(ctx, h.sn.DomainName, cfg)
	switch {
	case err == nil && owned:
		recorded := s.win(ctx, h, resultOf(h.sn.DomainName, info))
		return recorded, recorded
	case err == nil:
		return true, false
	case isCtxErr(ctx, err):
		return false, false
	}
	oc := s.vetPause(ctx, cfg, classify(err, cfg, h.sn.DomainName))
	if oc.kind == outPause {
		s.setPause(ctx, oc.msg, oc.balance)
		return false, false
	}
	s.recordError(ctx, h, "cannot verify ownership at the registrar: "+oc.msg)
	return false, false
}

// settleIfOwned is the guard in front of every refund-bearing ending (expiry,
// failure): if the domain is in fact already ours, record the win instead; if
// that cannot be established right now, postpone. It returns true when the
// caller must NOT go on to refund.
func (s *Supervisor) settleIfOwned(ctx context.Context, h *hunt, cfg Config) bool {
	owned, info, err := s.ownership(ctx, h.sn.DomainName, cfg)
	if err != nil {
		s.d.Log.WarnContext(ctx, "snipe ownership unknown, postponing the ending", "snipe", h.sn.ID, "error", err)
		return true
	}
	if owned {
		if h.ns == nil {
			h.ns = s.nameservers(ctx, h)
		}
		s.win(ctx, h, resultOf(h.sn.DomainName, info))
		return true
	}
	return false
}

// afterAbort runs when a register call was cut off mid-flight: the registrar
// may well have registered the domain before the connection dropped. A short,
// detached check records such a win (a snipe cancelled in the meantime is
// handed over as a late win with an admin alert).
func (s *Supervisor) afterAbort(ctx context.Context, h *hunt, cfg Config) {
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*cfg.RequestTimeout)
	defer cancel()
	owned, info, err := s.ownership(bg, h.sn.DomainName, cfg)
	if err == nil && owned {
		if h.ns == nil {
			h.ns = s.nameservers(bg, h)
		}
		s.win(bg, h, resultOf(h.sn.DomainName, info))
	}
}

// vetPause double-checks a "pause everything" verdict against the account
// itself. Rejected credentials (401/403) can also be a reply about ONE domain
// (a restricted TLD, say), and one such reply must not stop every hunt of every
// client: only when the reseller account is really unusable does it pause. A
// balance verdict is kept as is (the registrar said so about the account).
func (s *Supervisor) vetPause(ctx context.Context, cfg Config, oc outcome) outcome {
	if oc.kind != outPause || oc.balance {
		return oc
	}
	_, _, err := s.checkAccount(ctx, cfg)
	if err == nil || classify(err, cfg, "").kind != outPause {
		return outcome{kind: outRetry, msg: oc.msg}
	}
	return oc
}

func statusOf(info *ports.DomainSyncInfo) string {
	if info == nil {
		return ""
	}
	return info.Status
}

// ownedStatus reports whether a registrar status describes a live domain of
// ours (as opposed to a lapsed record we merely still see).
func ownedStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active", "pending", "ok", "registered":
		return true
	}
	return false
}

// probe is one cheap availability check, counted against the in-flight cap.
func (s *Supervisor) probe(ctx context.Context, h *hunt, cfg Config) (bool, error) {
	release, err := s.lim.acquire(ctx, cfg.MaxInFlight)
	if err != nil {
		return false, err
	}
	defer release()
	pctx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()
	return s.d.Registrar.IsAvailable(pctx, h.sn.DomainName)
}

// attempt is one register call, counted against the in-flight cap.
func (s *Supervisor) attempt(ctx context.Context, h *hunt, cfg Config) (*ports.DomainResult, error) {
	release, err := s.lim.acquire(ctx, cfg.MaxInFlight)
	if err != nil {
		return nil, err
	}
	defer release()
	rctx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()
	res, err := s.d.Registrar.RegisterForCustomer(rctx, h.sn.DomainName, h.sn.Years, h.customerID, h.ns)
	// A call that never left this process (breaker open) is not an attempt.
	if ce := ports.RegistrarCallErrorOf(err); ce == nil || !ce.NotSent {
		h.attempts.Add(1)
		h.lastAttempt.Store(s.d.Clock.Now().UnixNano())
	}
	if err == nil && res == nil {
		return nil, apperr.External("registrar", errors.New("empty registration result"))
	}
	return res, err
}

// win records a successful registration exactly once: the domain row and the
// won status commit together. Cancellation of the hunt must not abort it (the
// money is already spent at the registrar), so it runs detached from ctx.
func (s *Supervisor) win(ctx context.Context, h *hunt, res ports.DomainResult) bool {
	bg := context.WithoutCancel(ctx)
	in := ports.RecordSnipedDomainInput{
		ClientID: h.sn.ClientID, Name: h.sn.DomainName, Result: res,
		RenewPerYear: h.sn.RenewPerYear, Nameservers: h.ns,
	}
	for attempt := 1; ; attempt++ {
		err := s.recordWin(bg, h, in)
		if err == nil {
			s.afterWin(bg, h, res)
			return true
		}
		if errors.Is(err, errLostRace) {
			s.lateWin(bg, h, in)
			return true
		}
		if s.alreadyWon(bg, h, err) || s.adoptExisting(bg, h, res, err) {
			return true
		}
		s.d.Log.ErrorContext(ctx, "snipe win not recorded", "snipe", h.sn.ID, "attempt", attempt, "error", err)
		if attempt >= winRecordMax {
			// The caller keeps the hunt alive and tries again; one alert per
			// hour is enough to get a human to look at the registrar panel.
			if s.shouldAlert(fmt.Sprintf("win:%d", h.sn.ID)) {
				s.alert(bg, fmt.Sprintf("Domain %s was registered at the registrar but could not be recorded", h.sn.DomainName),
					fmt.Sprintf("Snipe #%d won %s but saving it failed %d times (%v). The hunt keeps verifying and retrying; check the domain in the registrar panel.",
						h.sn.ID, h.sn.DomainName, attempt, err))
			}
			return false
		}
		if s.d.Sleep(ctx, winRecordWait) != nil {
			return false // shutting down: the resumed hunt verifies ownership and finishes the job
		}
	}
}

// shouldAlert rate-limits an operator alert per key: true at most once per alertEvery.
func (s *Supervisor) shouldAlert(key string) bool {
	now := s.d.Clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.alerted[key]; ok && now.Sub(last) < alertEvery {
		return false
	}
	s.alerted[key] = now
	return true
}

// adoptExisting settles a win whose domains row cannot be created because the
// name is already in `domains` for this very client (an administrator added
// it by hand while the hunt was still running): the snipe adopts that row
// instead of looping on a conflict forever. A row of another client is a
// genuine problem for a human and is left alone.
func (s *Supervisor) adoptExisting(ctx context.Context, h *hunt, res ports.DomainResult, cause error) bool {
	if apperr.From(cause).Code != apperr.CodeConflict {
		return false
	}
	existing, err := s.d.DomainRepo.GetByName(ctx, h.sn.DomainName)
	if err != nil || existing == nil || existing.ClientID != h.sn.ClientID {
		return false
	}
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		ok, err := s.d.Snipes.Finish(ctx, ports.SnipeFinish{
			ID: h.sn.ID, From: domain.SnipeActive, To: domain.SnipeWon,
			Now: s.d.Clock.Now(), WonDomainID: &existing.ID, Attempts: h.attempts.Load(),
		})
		if err != nil || !ok {
			return err
		}
		return s.d.Snipes.AddEvent(ctx, &domain.SnipeEvent{
			SnipeID: h.sn.ID, Kind: eventWon, Message: "the domain was registered (it was already on the client's account)",
		})
	})
	if err != nil {
		return false
	}
	s.afterWin(ctx, h, res)
	return true
}

func (s *Supervisor) alreadyWon(ctx context.Context, h *hunt, err error) bool {
	if apperr.From(err).Code != apperr.CodeConflict {
		return false
	}
	cur, getErr := s.d.Snipes.GetByID(ctx, h.sn.ID)
	if getErr != nil || cur == nil || cur.Status != domain.SnipeWon {
		return false
	}
	s.d.Log.InfoContext(ctx, "snipe win was already recorded by another hunt", "snipe", h.sn.ID)
	return true
}

func (s *Supervisor) recordWin(ctx context.Context, h *hunt, in ports.RecordSnipedDomainInput) error {
	return s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		dom, err := s.d.Domains.RecordSnipedDomain(ctx, in)
		if err != nil {
			return err
		}
		// No lease fencing here: the registration is already paid for at the
		// registrar, so the win must be recorded even if the lease lapsed while
		// the call was in flight. The only legitimate reason to lose this
		// compare-and-swap is the snipe having left 'active' (cancelled).
		ok, err := s.d.Snipes.Finish(ctx, ports.SnipeFinish{
			ID: h.sn.ID, From: domain.SnipeActive, To: domain.SnipeWon,
			Now: s.d.Clock.Now(), WonDomainID: &dom.ID, Attempts: h.attempts.Load(),
		})
		if err != nil {
			return err
		}
		if !ok {
			return errLostRace // rolls the domain row back
		}
		return s.d.Snipes.AddEvent(ctx, &domain.SnipeEvent{
			SnipeID: h.sn.ID, Kind: eventWon, Message: "the domain was registered",
		})
	})
}

// afterWin runs the post-commit side effects of a recorded win.
func (s *Supervisor) afterWin(ctx context.Context, h *hunt, res ports.DomainResult) {
	won := h.sn
	won.Status = domain.SnipeWon
	s.d.Audit.Log(ctx, 0, "snipe.won", "domain_snipe", h.sn.ID, &h.sn, &won)
	data := map[string]any{"Domain": h.sn.DomainName}
	if !res.ExpiryDate.IsZero() {
		data["ExpiryDate"] = res.ExpiryDate.Format(dateFormat)
	}
	s.sendToClient(ctx, &h.sn, tplWon, data)
}

// lateWin handles the rare race where the snipe was cancelled while its
// register call was already in flight and then succeeded: the domain exists at
// the registrar (and was paid for), so it is still handed to the client and
// the operator is told to square up any refund that was already issued.
func (s *Supervisor) lateWin(ctx context.Context, h *hunt, in ports.RecordSnipedDomainInput) {
	status := "no longer active"
	if cur, err := s.d.Snipes.GetByID(ctx, h.sn.ID); err == nil && cur != nil {
		status = string(cur.Status)
	}
	if _, err := s.d.Domains.RecordSnipedDomain(ctx, in); err != nil {
		s.alert(ctx, fmt.Sprintf("Domain %s registered after its snipe ended", h.sn.DomainName),
			fmt.Sprintf("Snipe #%d is %s but %s was registered at the registrar and recording it failed (%v). Add the domain to the client manually.",
				h.sn.ID, status, h.sn.DomainName, err))
		return
	}
	s.recordEvent(ctx, h, eventLateWin, "the domain was registered after the snipe was already "+status)
	s.alert(ctx, fmt.Sprintf("Domain %s registered after its snipe ended", h.sn.DomainName),
		fmt.Sprintf("Snipe #%d was %s when %s was registered at the registrar. The domain was added to client #%d; review any refund that was already credited.",
			h.sn.ID, status, h.sn.DomainName, h.sn.ClientID))
}

func (s *Supervisor) alert(ctx context.Context, subject, message string) {
	if err := s.d.Notifier.AlertAdmin(ctx, subject, message); err != nil {
		s.d.Log.WarnContext(ctx, "admin alert failed", "subject", subject, "error", err)
	}
}

// expire closes the snipe at its deadline (the hunt's own fencing applies).
func (s *Supervisor) expire(ctx context.Context, h *hunt) {
	if s.settleIfOwned(ctx, h, s.config()) {
		return
	}
	if _, err := s.finish(ctx, &h.sn, domain.SnipeExpired, finishOpts{
		Owner:    s.d.Owner,
		Reason:   "the hunting window closed without winning the domain",
		Attempts: h.attempts.Load(),
	}); err != nil {
		s.d.Log.WarnContext(ctx, "snipe expiry failed", "snipe", h.sn.ID, "error", err)
	}
}

// fail closes the snipe as failed (the client gets everything back).
func (s *Supervisor) fail(ctx context.Context, h *hunt, reason string) {
	if s.settleIfOwned(ctx, h, s.config()) {
		return
	}
	if _, err := s.finish(ctx, &h.sn, domain.SnipeFailed, finishOpts{Owner: s.d.Owner, Reason: reason, Attempts: h.attempts.Load()}); err != nil {
		s.d.Log.WarnContext(ctx, "snipe fail transition failed", "snipe", h.sn.ID, "error", err)
	}
}

// setPhase records a phase change and, when it really changed, a trail entry.
func (s *Supervisor) setPhase(ctx context.Context, h *hunt, p domain.SnipePhase, msg string) {
	if h.setPhase(p) {
		s.recordEvent(ctx, h, eventPhase, msg)
	}
}

// recordEvent writes a trail entry unless the same one was written recently.
func (s *Supervisor) recordEvent(ctx context.Context, h *hunt, kind, msg string) {
	if h.dedupe(kind+"|"+msg, s.d.Clock.Now()) {
		s.event(context.WithoutCancel(ctx), h.sn.ID, kind, msg)
	}
}

// recordError remembers the latest problem (shown on the snipe) and logs it to
// the trail at most once per eventDedupe.
func (s *Supervisor) recordError(ctx context.Context, h *hunt, msg string) {
	h.setLastError(msg)
	s.recordEvent(ctx, h, eventError, msg)
}

// pause sleeps d, but never past the snipe's deadline.
func (s *Supervisor) pause(ctx context.Context, h *hunt, d time.Duration) {
	if dl := h.sn.DeadlineAt; dl != nil {
		if left := dl.Sub(s.d.Clock.Now()); left < d {
			d = left
		}
	}
	_ = s.d.Sleep(ctx, d)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// limiter caps concurrent registrar requests across every hunt of this
// worker. The cap is read per acquire, so an admin edit applies live.
type limiter struct {
	mu    sync.Mutex
	n     int
	sleep func(ctx context.Context, d time.Duration) error
}

func (l *limiter) acquire(ctx context.Context, limit int) (func(), error) {
	for {
		l.mu.Lock()
		if l.n < limit {
			l.n++
			l.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					l.mu.Lock()
					l.n--
					l.mu.Unlock()
				})
			}, nil
		}
		l.mu.Unlock()
		if err := l.sleep(ctx, limiterPoll); err != nil {
			return nil, err
		}
	}
}
