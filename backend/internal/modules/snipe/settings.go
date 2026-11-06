// Package snipe is the domain sniper: a background hunt that keeps trying to
// register a dropping/expired domain at the registrar until it succeeds, the
// window closes, or someone cancels it.
//
// A hunt is a domain_snipes row. The worker process runs one Supervisor that
// claims active rows under a short SQL lease (so a crashed or second worker
// never double-fires a domain) and drives each through two phases: "patient"
// (a cheap availability probe every few seconds) and "burst" (the register
// call every few hundred milliseconds, only inside the owner-supplied drop
// window or once the probe sees the name free). The HTTP side (Service +
// Handler) creates, lists and cancels hunts; it never talks to the registrar
// per attempt.
//
// Money: a client hunt is billed up front on one invoice (service fee + the
// domain's registration price); when the hunt ends without a win the
// refundable part is credited back in the same transaction that closes it.
// Admin hunts are free: no invoice, no refund.
package snipe

import (
	"context"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
)

// Settings keys (CONTRACTS §10). Everything except the pause markers is
// admin-editable through the generic settings endpoint (settings.Spec).
const (
	SettingEnabled            = "snipe.enabled"
	SettingMaxActiveGlobal    = "snipe.max_active_global"
	SettingMaxInFlight        = "snipe.max_in_flight"
	SettingBurstIntervalMS    = "snipe.burst_interval_ms"
	SettingBurstJitterMS      = "snipe.burst_jitter_ms"
	SettingPatientIntervalSec = "snipe.patient_interval_seconds"
	SettingBurstLeadSec       = "snipe.burst_lead_seconds"
	SettingRateLimitBackoffMS = "snipe.rate_limit_backoff_ms"
	SettingRequestTimeoutMS   = "snipe.request_timeout_ms"
	SettingMaxActivePerClient = "snipe.max_active_per_client"
	SettingInvoiceDueHours    = "snipe.invoice_due_hours"
	SettingMinResellerBalance = "snipe.min_reseller_balance"
	SettingPauseProbeSec      = "snipe.pause_probe_seconds"

	// SettingPauseReason is written by the supervisor (never by an admin
	// edit): non-empty means every worker stops hunting until a probe of the
	// reseller account is healthy again. SettingPausedAt records when.
	SettingPauseReason = "snipe.pause_reason"
	SettingPausedAt    = "snipe.paused_at"
	// SettingPausedBalance is the reseller balance seen when a pause was
	// caused by running out of money (-1 otherwise): such a pause only ends
	// once the balance has risen above it, instead of flapping on every probe.
	SettingPausedBalance = "snipe.paused_balance"

	settingDefaultNameservers = "domains.default_nameservers"
)

// Config is a point-in-time snapshot of the snipe.* settings, clamped to
// values that cannot hammer or starve the registrar by accident. The
// supervisor refreshes it every few seconds, so admin edits apply live.
type Config struct {
	Enabled            bool
	MaxActiveGlobal    int           // hunts running at once, across the whole system
	MaxInFlight        int           // concurrent registrar requests, across all hunts
	BurstInterval      time.Duration // pause between register attempts of one hunt
	BurstJitter        time.Duration // random extra on top of BurstInterval
	PatientInterval    time.Duration // pause between availability probes
	BurstLead          time.Duration // start bursting this long before hunt_from
	RateLimitBackoff   time.Duration // minimum wait after a 429
	RequestTimeout     time.Duration // per registrar request
	MaxActivePerClient int
	InvoiceDue         time.Duration
	MinResellerBalance int64
	PauseProbe         time.Duration // how often a paused supervisor re-checks the reseller account
	PauseReason        string
	PausedBalance      int64 // see SettingPausedBalance; -1 = not a balance pause
}

func defaultConfig() Config {
	return Config{
		Enabled:            true,
		MaxActiveGlobal:    50,
		MaxInFlight:        2,
		BurstInterval:      200 * time.Millisecond,
		BurstJitter:        100 * time.Millisecond,
		PatientInterval:    10 * time.Second,
		BurstLead:          60 * time.Second,
		RateLimitBackoff:   2 * time.Second,
		RequestTimeout:     3 * time.Second,
		MaxActivePerClient: 5,
		InvoiceDue:         24 * time.Hour,
		PauseProbe:         time.Minute,
		PausedBalance:      -1,
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// loadConfig reads the snipe.* settings. A missing key keeps its default; a
// read error returns the defaults-so-far together with the error so the
// caller can keep its previous snapshot instead.
func loadConfig(ctx context.Context, s ports.SettingsRepo) (Config, error) {
	cfg := defaultConfig()
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	getInt := func(key string, def int) int {
		v, err := s.GetInt(ctx, key, def)
		note(err)
		return v
	}

	enabled, err := s.GetBool(ctx, SettingEnabled, cfg.Enabled)
	note(err)
	cfg.Enabled = enabled
	cfg.MaxActiveGlobal = clampInt(getInt(SettingMaxActiveGlobal, cfg.MaxActiveGlobal), 1, 200)
	cfg.MaxInFlight = clampInt(getInt(SettingMaxInFlight, cfg.MaxInFlight), 1, 50)
	cfg.BurstInterval = time.Duration(clampInt(getInt(SettingBurstIntervalMS, 200), 10, 60_000)) * time.Millisecond
	cfg.BurstJitter = time.Duration(clampInt(getInt(SettingBurstJitterMS, 100), 0, 10_000)) * time.Millisecond
	cfg.PatientInterval = time.Duration(clampInt(getInt(SettingPatientIntervalSec, 10), 1, 3600)) * time.Second
	cfg.BurstLead = time.Duration(clampInt(getInt(SettingBurstLeadSec, 60), 0, 3600)) * time.Second
	cfg.RateLimitBackoff = time.Duration(clampInt(getInt(SettingRateLimitBackoffMS, 2000), 100, 60_000)) * time.Millisecond
	cfg.RequestTimeout = time.Duration(clampInt(getInt(SettingRequestTimeoutMS, 3000), 200, 30_000)) * time.Millisecond
	cfg.MaxActivePerClient = clampInt(getInt(SettingMaxActivePerClient, cfg.MaxActivePerClient), 1, 100)
	cfg.InvoiceDue = time.Duration(clampInt(getInt(SettingInvoiceDueHours, 24), 1, 720)) * time.Hour
	floor := getInt(SettingMinResellerBalance, 0)
	if floor < 0 {
		floor = 0
	}
	cfg.MinResellerBalance = int64(floor)
	cfg.PauseProbe = time.Duration(clampInt(getInt(SettingPauseProbeSec, 60), 1, 3600)) * time.Second
	cfg.PausedBalance = int64(getInt(SettingPausedBalance, -1))

	reason, err := s.GetString(ctx, SettingPauseReason, "")
	note(err)
	cfg.PauseReason = reason
	return cfg, firstErr
}
