package snipe

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// runHunt drives one hunt synchronously to its end.
func runHunt(t *testing.T, f *fx, sn domain.DomainSnipe) (*Supervisor, *hunt) {
	t.Helper()
	sup := f.supervisor()
	h := newHunt(sn, func() {})
	sup.runHunt(testCtx(t), h)
	return sup, h
}

// inWindow puts a hunt inside its drop window so it bursts straight away.
func (f *fx) inWindow(s *domain.DomainSnipe) {
	from, until := f.clock.Now().Add(-time.Minute), f.clock.Now().Add(time.Hour)
	s.HuntFrom, s.HuntUntil = &from, &until
}

// registerScript plays the register replies in order, then succeeds.
func (f *fx) registerScript(replies ...error) *atomic.Int32 {
	var calls atomic.Int32
	f.registrar.RegisterForCustomerFn = func(_ context.Context, name string, years int, cust int64, ns []string) (*ports.DomainResult, error) {
		n := int(calls.Add(1))
		if n <= len(replies) {
			if replies[n-1] != nil {
				return nil, replies[n-1]
			}
		}
		return registered(name), nil
	}
	return &calls
}

func (f *fx) countSleeps(d time.Duration) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sleeps {
		if s == d {
			n++
		}
	}
	return n
}

func (f *fx) paused() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.settingsSet[SettingPauseReason]
	if !ok {
		return "", false
	}
	return v.(string), true
}

func TestHuntBurstWindowWinsAfterTakenReplies(t *testing.T) {
	f := newFx()
	calls := f.registerScript(errTaken(), errTaken(), errTaken())
	var customerCached int64
	f.snipes.SetCustomerIDFn = func(_ context.Context, id, cust int64) error { customerCached = cust; return nil }
	var recorded ports.RecordSnipedDomainInput
	f.domains.RecordSnipedDomainFn = func(_ context.Context, in ports.RecordSnipedDomainInput) (*domain.Domain, error) {
		recorded = in
		return &domain.Domain{ID: 900}, nil
	}
	var regArgs struct {
		cust  int64
		years int
		ns    []string
	}
	prev := f.registrar.RegisterForCustomerFn
	f.registrar.RegisterForCustomerFn = func(ctx context.Context, name string, years int, cust int64, ns []string) (*ports.DomainResult, error) {
		regArgs.cust, regArgs.years, regArgs.ns = cust, years, ns
		return prev(ctx, name, years, cust, ns)
	}
	f.settings.GetJSONFn = func(_ context.Context, key string, out any) error {
		assert.Equal(t, settingDefaultNameservers, key)
		*(out.(*[]string)) = []string{"ns1.default.net", "ns2.default.net"}
		return nil
	}

	_, h := runHunt(t, f, f.activeSnipe(func(s *domain.DomainSnipe) { f.inWindow(s); s.Years = 2 }))

	assert.EqualValues(t, 4, calls.Load())
	assert.EqualValues(t, 4, h.attempts.Load())
	assert.EqualValues(t, 501, customerCached, "the resolved registrar customer is cached on the row")
	assert.EqualValues(t, 501, regArgs.cust)
	assert.Equal(t, 2, regArgs.years)
	assert.Equal(t, []string{"ns1.default.net", "ns2.default.net"}, regArgs.ns, "falls back to the system default nameservers")
	assert.Equal(t, 3, f.countSleeps(200*time.Millisecond), "one burst interval after each taken reply")

	require.Len(t, f.finishes, 1)
	fin := f.finishes[0]
	assert.Equal(t, domain.SnipeActive, fin.From)
	assert.Equal(t, domain.SnipeWon, fin.To)
	assert.Empty(t, fin.Owner, "a win is recorded even if the lease lapsed meanwhile")
	require.NotNil(t, fin.WonDomainID)
	assert.EqualValues(t, 900, *fin.WonDomainID)
	assert.Zero(t, fin.RefundAmount)

	assert.Equal(t, "hunt.com", recorded.Name)
	assert.EqualValues(t, 7, recorded.ClientID)
	assert.EqualValues(t, 160000, recorded.RenewPerYear)
	assert.Equal(t, "77", recorded.Result.OrderID)

	assert.Equal(t, []string{tplWon}, f.templateKeys())
	assert.Equal(t, "2027-10-24", f.templates[0].Data["ExpiryDate"])
	assert.Equal(t, []string{eventPhase, eventWon}, f.eventKinds())
	require.Len(t, f.audit.Entries, 1)
	assert.Equal(t, "snipe.won", f.audit.Entries[0].Action)
	assert.Equal(t, []int64{1}, f.released, "the lease is released when the hunt ends")
}

func TestHuntPatientProbeThenBurstOnceAvailable(t *testing.T) {
	f := newFx()
	var probes atomic.Int32
	f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) { return probes.Add(1) >= 3, nil }
	calls := f.registerScript()

	runHunt(t, f, f.activeSnipe(nil))

	assert.EqualValues(t, 3, probes.Load())
	assert.EqualValues(t, 1, calls.Load(), "registers only once the name looks free")
	assert.Equal(t, 2, f.countSleeps(10*time.Second), "one patient interval after each unavailable probe")
	assert.Equal(t, []string{eventAvailable, eventPhase, eventWon}, f.eventKinds())
	assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
}

func TestHuntWaitsSilentlyUntilTheWindowOpens(t *testing.T) {
	f := newFx()
	f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) {
		t.Fatal("no probes while waiting for the drop window")
		return false, nil
	}
	calls := f.registerScript()
	from := f.clock.Now().Add(10 * time.Minute)

	runHunt(t, f, f.activeSnipe(func(s *domain.DomainSnipe) { s.HuntFrom = &from }))

	// Bursting starts burstLead (60s) before hunt_from: 9 minutes of 30s waits.
	assert.Equal(t, 18, f.countSleeps(waitCap))
	assert.EqualValues(t, 1, calls.Load())
	assert.False(t, f.clock.Now().Before(from.Add(-time.Minute)), "the first call was not before the lead time")
	assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
}

func TestHuntFallsBackToProbingAfterTheWindowCloses(t *testing.T) {
	f := newFx()
	from, until := f.clock.Now().Add(-2*time.Hour), f.clock.Now().Add(-time.Hour)
	var probes atomic.Int32
	f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) { return probes.Add(1) >= 2, nil }
	calls := f.registrar.RegisterForCustomerFn
	_ = calls
	n := f.registerScript()

	runHunt(t, f, f.activeSnipe(func(s *domain.DomainSnipe) { s.HuntFrom, s.HuntUntil = &from, &until }))

	assert.EqualValues(t, 2, probes.Load(), "an expired window is patient probing again, not bursting")
	assert.EqualValues(t, 1, n.Load())
}

func TestHuntBacksOffOnRateLimit(t *testing.T) {
	f := newFx()
	calls := f.registerScript(errRateLimited(5 * time.Second))

	_, h := runHunt(t, f, f.activeSnipe(f.inWindow))

	assert.EqualValues(t, 2, calls.Load())
	assert.EqualValues(t, 2, h.attempts.Load())
	assert.Equal(t, 1, f.countSleeps(5*time.Second), "waits the registrar's Retry-After")
	assert.Contains(t, f.eventKinds(), eventError)
	assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
}

func TestHuntVerifiesAnAmbiguousReplyBeforeRetrying(t *testing.T) {
	t.Run("the order had gone through", func(t *testing.T) {
		f := newFx()
		calls := f.registerScript(errAmbiguous())
		var syncs atomic.Int32
		f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
			if syncs.Add(1) == 1 {
				return nil, apperr.NotFound("domain") // the startup check
			}
			return &ports.DomainSyncInfo{Status: "pending", ExpiryDate: time.Date(2027, 10, 24, 0, 0, 0, 0, time.UTC)}, nil
		}

		runHunt(t, f, f.activeSnipe(f.inWindow))

		assert.EqualValues(t, 1, calls.Load(), "never re-registers a domain that is already ours")
		assert.EqualValues(t, 2, syncs.Load())
		assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
		assert.Equal(t, "2027-10-24", f.templates[0].Data["ExpiryDate"])
	})

	t.Run("it had not, so the hunt carries on and a later 409 is believed", func(t *testing.T) {
		f := newFx()
		calls := f.registerScript(errAmbiguous(), errTaken())
		var syncs atomic.Int32
		f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
			syncs.Add(1)
			return nil, apperr.NotFound("domain")
		}

		runHunt(t, f, f.activeSnipe(f.inWindow))

		assert.EqualValues(t, 3, calls.Load())
		assert.EqualValues(t, 3, syncs.Load(),
			"at start, after the ambiguous reply, and once more for the 409 that follows it (the registration may have been settling)")
	})

	t.Run("a 409 right after an unclear reply that turns out to be our own registration is a win", func(t *testing.T) {
		f := newFx()
		calls := f.registerScript(errAmbiguous(), errTaken(), errTaken())
		var syncs atomic.Int32
		f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
			// start: nothing; right after the timeout: still nothing (the order is
			// settling); after the next 409 (the name is now ours): registered.
			if syncs.Add(1) <= 2 {
				return nil, apperr.NotFound("domain")
			}
			return &ports.DomainSyncInfo{Status: "active"}, nil
		}

		runHunt(t, f, f.activeSnipe(f.inWindow))

		assert.EqualValues(t, 2, calls.Load(), "stops hammering a name we already hold")
		assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
	})

	t.Run("409s long after an unclear reply are not re-verified every time", func(t *testing.T) {
		f := newFx()
		f.registerScript(append([]error{errAmbiguous()}, repeatErr(errTaken(), 60)...)...)
		var syncs atomic.Int32
		f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
			syncs.Add(1)
			return nil, apperr.NotFound("domain")
		}
		sn := f.activeSnipe(f.inWindow)
		dl := f.clock.Now().Add(10 * time.Minute)
		sn.DeadlineAt = &dl
		runHunt(t, f, sn)
		// start + after the ambiguous reply + the periodic re-checks inside the
		// 30s suspicion window (<= one per 2s), then none; the final expiry check.
		assert.Less(t, int(syncs.Load()), 25, "re-verification is bounded by the suspicion window")
	})

	t.Run("an empty registration result counts as unclear too", func(t *testing.T) {
		f := newFx()
		var calls atomic.Int32
		f.registrar.RegisterForCustomerFn = func(_ context.Context, name string, _ int, _ int64, _ []string) (*ports.DomainResult, error) {
			if calls.Add(1) == 1 {
				return nil, nil
			}
			return registered(name), nil
		}
		var syncs atomic.Int32
		f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
			syncs.Add(1)
			return nil, apperr.NotFound("domain")
		}
		runHunt(t, f, f.activeSnipe(f.inWindow))
		assert.EqualValues(t, 2, syncs.Load())
		assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
	})
}

func TestHuntResumeDetectsAnEarlierWin(t *testing.T) {
	f := newFx()
	f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
		return &ports.DomainSyncInfo{Status: "active", ExpiryDate: time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)}, nil
	}
	f.registrar.RegisterForCustomerFn = func(context.Context, string, int, int64, []string) (*ports.DomainResult, error) {
		t.Fatal("must not register a domain that is already in our account")
		return nil, nil
	}

	runHunt(t, f, f.activeSnipe(f.inWindow))

	assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
	assert.Equal(t, "2027-01-02", f.templates[0].Data["ExpiryDate"])
}

func TestHuntIgnoresALapsedRecordOfAnOldDomain(t *testing.T) {
	f := newFx()
	var syncs atomic.Int32
	f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
		syncs.Add(1)
		return &ports.DomainSyncInfo{Status: "Pending Delete"}, nil
	}
	calls := f.registerScript()

	runHunt(t, f, f.activeSnipe(f.inWindow))

	assert.EqualValues(t, 1, syncs.Load(), "a lapsed record is not a win, and verification concluded")
	assert.EqualValues(t, 1, calls.Load(), "so the hunt registers it for real")
}

func TestOwnedStatus(t *testing.T) {
	for _, s := range []string{"", "active", "Active", " pending ", "ok", "registered"} {
		assert.True(t, ownedStatus(s), s)
	}
	for _, s := range []string{"pending delete", "expired", "deleted", "transferred away", "rejected", "suspended"} {
		assert.False(t, ownedStatus(s), s)
	}
}

func TestHuntPausesEverythingOnAccountProblems(t *testing.T) {
	for _, tc := range []struct {
		name          string
		reply         error
		pausedBalance int64 // what is remembered so the pause can tell a top-up from mere readability
	}{
		{"credentials rejected", errUnauthorized(), -1},
		{"out of balance", errRejected("Insufficient balance"), 5_000_000}, // the fx reseller holds 5,000,000
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx()
			if tc.pausedBalance < 0 { // the account itself confirms it is unusable
				f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, errUnauthorized() }
			}
			f.registerScript(tc.reply)

			sup, _ := runHunt(t, f, f.activeSnipe(f.inWindow))

			reason, ok := f.paused()
			require.True(t, ok, "the pause is persisted for every worker and the admin UI")
			assert.NotEmpty(t, reason)
			assert.Contains(t, f.settingsSet, SettingPausedAt)
			assert.EqualValues(t, tc.pausedBalance, f.settingsSet[SettingPausedBalance])
			assert.Equal(t, tc.pausedBalance, sup.config().PausedBalance)
			assert.Equal(t, []string{"Domain sniper paused"}, f.alerts)
			assert.Equal(t, reason, sup.config().PauseReason, "and applied in memory immediately")
			assert.Empty(t, f.finishes, "the snipe stays active - the reseller account is what is broken")
			assert.Equal(t, []int64{1}, f.released)

			// A second hunt hitting the same wall must not alert again.
			sup.setPause(context.Background(), reason, false)
			assert.Len(t, f.alerts, 1)
		})
	}
}

func TestHuntFailsAndRefundsOnAPermanentProblem(t *testing.T) {
	f := newFx()
	f.registerScript(errRejected("The selected customer id is invalid.", apperr.FieldError{Field: "customer_id", Message: "invalid"}))

	runHunt(t, f, f.paidSnipe(f.inWindow))

	fin := f.lastFinish()
	assert.Equal(t, domain.SnipeFailed, fin.To)
	assert.Equal(t, "w1", fin.Owner)
	assert.EqualValues(t, 222_000, fin.RefundAmount, "our problem: the client gets everything back")
	require.Len(t, f.credits, 1)
	assert.EqualValues(t, 222_000, f.credits[0].Delta)
	assert.Equal(t, []string{tplFailed}, f.templateKeys())
	assert.NotContains(t, f.templates[0].Data, "Reason", "registrar wording never reaches the client's email")
	assert.Contains(t, f.finishes[0].LastError, "customer", "it stays on the record for the operator")
}

func TestHuntGivesUpAfterTooManyUnexplainedRejections(t *testing.T) {
	f := newFx()
	var calls atomic.Int32
	f.registrar.RegisterForCustomerFn = func(context.Context, string, int, int64, []string) (*ports.DomainResult, error) {
		calls.Add(1)
		return nil, errRejected("The name is reserved.")
	}

	_, h := runHunt(t, f, f.paidSnipe(f.inWindow))

	assert.EqualValues(t, rejectCap, calls.Load())
	assert.EqualValues(t, rejectCap, h.attempts.Load())
	assert.Equal(t, domain.SnipeFailed, f.lastFinish().To)
	assert.Contains(t, f.lastFinish().LastError, "reserved")
	assert.Equal(t, 1, len(f.credits))
}

func TestHuntExpiresAtTheDeadlineAndRefundsTheDomainPrice(t *testing.T) {
	f := newFx()
	var calls atomic.Int32
	f.registrar.RegisterForCustomerFn = func(context.Context, string, int, int64, []string) (*ports.DomainResult, error) {
		calls.Add(1)
		return nil, errTaken()
	}
	dl := f.clock.Now().Add(3 * time.Second)

	runHunt(t, f, f.paidSnipe(func(s *domain.DomainSnipe) { f.inWindow(s); s.DeadlineAt = &dl }))

	assert.EqualValues(t, 15, calls.Load(), "3s / 200ms")
	fin := f.lastFinish()
	assert.Equal(t, domain.SnipeExpired, fin.To)
	assert.Equal(t, "w1", fin.Owner, "the hunt's own expiry is lease-fenced")
	assert.EqualValues(t, 166_500, fin.RefundAmount, "the fee is not refunded on a plain expiry")
	assert.Equal(t, []string{tplExpired}, f.templateKeys())
	assert.Equal(t, domain.FormatIDR(166_500), f.templates[0].Data["RefundAmount"])
	assert.False(t, f.clock.Now().After(dl), "sleeps never run past the deadline")
}

func TestHuntFallsBackToProbingAfterTooManyLostBursts(t *testing.T) {
	f := newFx()
	ctx, cancel := context.WithCancel(testCtx(t))
	var probes, regs atomic.Int32
	f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) {
		if probes.Add(1) == 1 {
			return true, nil
		}
		cancel() // second probe: we are back to patient probing - done with the test
		return false, nil
	}
	f.registrar.RegisterForCustomerFn = func(context.Context, string, int, int64, []string) (*ports.DomainResult, error) {
		regs.Add(1)
		return nil, errTaken()
	}

	sup := f.supervisor()
	sup.runHunt(ctx, newHunt(f.activeSnipe(nil), func() {}))

	assert.EqualValues(t, burstGiveUp, regs.Load())
	assert.EqualValues(t, 2, probes.Load())
	assert.Contains(t, f.eventKinds(), eventPhase)
	assert.Empty(t, f.finishes, "shutting down is not an ending")
}

func TestHuntDoesNotCountUnsentCallsAsAttempts(t *testing.T) {
	f := newFx()
	calls := f.registerScript(errNotSent())

	_, h := runHunt(t, f, f.activeSnipe(f.inWindow))

	assert.EqualValues(t, 2, calls.Load())
	assert.EqualValues(t, 1, h.attempts.Load(), "the breaker-open call never left the process")
	assert.Equal(t, 1, f.countSleeps(2*time.Second))
}

func TestHuntRecordsAWinEvenWhenShuttingDownAtThatMoment(t *testing.T) {
	f := newFx()
	ctx, cancel := context.WithCancel(testCtx(t))
	f.registrar.RegisterForCustomerFn = func(_ context.Context, name string, _ int, _ int64, _ []string) (*ports.DomainResult, error) {
		cancel() // the worker is told to stop just as the registrar says yes
		return registered(name), nil
	}

	sup := f.supervisor()
	sup.runHunt(ctx, newHunt(f.activeSnipe(f.inWindow), func() {}))

	assert.Equal(t, domain.SnipeWon, f.lastFinish().To, "the money is spent: the win must be recorded regardless")
	assert.Equal(t, []string{tplWon}, f.templateKeys())
}

func TestHuntLateWinWhenCancelledMidFlight(t *testing.T) {
	f := newFx()
	f.registerScript()
	f.snipes.FinishFn = func(context.Context, ports.SnipeFinish) (bool, error) { return false, nil }
	f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
		return &domain.DomainSnipe{ID: 1, Status: domain.SnipeCancelled}, nil
	}
	var recordCalls atomic.Int32
	f.domains.RecordSnipedDomainFn = func(_ context.Context, in ports.RecordSnipedDomainInput) (*domain.Domain, error) {
		recordCalls.Add(1)
		return &domain.Domain{ID: 900}, nil
	}

	runHunt(t, f, f.activeSnipe(f.inWindow))

	assert.EqualValues(t, 2, recordCalls.Load(), "once inside the rolled-back tx, once for the late hand-over")
	assert.Contains(t, f.eventKinds(), eventLateWin)
	require.Len(t, f.alerts, 1)
	assert.Contains(t, f.alerts[0], "after its snipe ended")
	assert.Empty(t, f.templates, "no victory email for a snipe the client already saw cancelled")
	assert.Empty(t, f.audit.Entries)
}

func TestHuntLateWinReportsWhenEvenTheHandOverFails(t *testing.T) {
	f := newFx()
	f.registerScript()
	f.snipes.FinishFn = func(context.Context, ports.SnipeFinish) (bool, error) { return false, nil }
	f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return nil, errBoom }
	var n atomic.Int32
	f.domains.RecordSnipedDomainFn = func(context.Context, ports.RecordSnipedDomainInput) (*domain.Domain, error) {
		if n.Add(1) == 1 {
			return &domain.Domain{ID: 900}, nil // inside the (mock, non-rolling-back) tx
		}
		return nil, errBoom
	}

	runHunt(t, f, f.activeSnipe(f.inWindow))

	require.Len(t, f.alerts, 1)
	assert.Contains(t, f.alerts[0], "after its snipe ended")
	assert.NotContains(t, f.eventKinds(), eventLateWin)
}

func TestHuntRetriesRecordingAWin(t *testing.T) {
	t.Run("a transient failure is retried", func(t *testing.T) {
		f := newFx()
		f.registerScript()
		var n atomic.Int32
		f.domains.RecordSnipedDomainFn = func(context.Context, ports.RecordSnipedDomainInput) (*domain.Domain, error) {
			if n.Add(1) <= 2 {
				return nil, errBoom
			}
			return &domain.Domain{ID: 900}, nil
		}
		runHunt(t, f, f.activeSnipe(f.inWindow))
		assert.EqualValues(t, 3, n.Load())
		assert.Equal(t, 2, f.countSleeps(winRecordWait))
		assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
	})

	t.Run("giving up alerts the operator and leaves the snipe for the resume path", func(t *testing.T) {
		f := newFx()
		f.registerScript()
		f.domains.RecordSnipedDomainFn = func(context.Context, ports.RecordSnipedDomainInput) (*domain.Domain, error) {
			return nil, errBoom
		}
		var registered atomic.Bool
		inner := f.registrar.RegisterForCustomerFn
		f.registrar.RegisterForCustomerFn = func(ctx context.Context, n string, y int, c int64, ns []string) (*ports.DomainResult, error) {
			registered.Store(true)
			return inner(ctx, n, y, c, ns)
		}
		f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
			if registered.Load() {
				return &ports.DomainSyncInfo{Status: "active"}, nil
			}
			return nil, apperr.NotFound("domain")
		}
		sn := f.activeSnipe(f.inWindow)
		soon := f.clock.Now().Add(30 * time.Minute) // well inside one alert interval
		sn.DeadlineAt = &soon
		runHunt(t, f, sn)
		require.Len(t, f.alerts, 1, "the hunt keeps retrying but alerts only once an hour")
		assert.Contains(t, f.alerts[0], "could not be recorded")
		assert.Empty(t, f.finishes, "never refunded or ended: the domain is ours, a human must look")
	})

	t.Run("shutting down mid-retry stops without alerting", func(t *testing.T) {
		f := newFx()
		ctx, cancel := context.WithCancel(testCtx(t))
		f.registerScript()
		f.domains.RecordSnipedDomainFn = func(context.Context, ports.RecordSnipedDomainInput) (*domain.Domain, error) {
			cancel()
			return nil, errBoom
		}
		sup := f.supervisor()
		sup.runHunt(ctx, newHunt(f.activeSnipe(f.inWindow), func() {}))
		assert.Empty(t, f.alerts)
	})
}

func TestHuntPrepare(t *testing.T) {
	t.Run("an incomplete profile fails the snipe and gives the money back", func(t *testing.T) {
		f := newFx()
		f.domains.RegistrantContactFn = func(context.Context, int64) (ports.RegistrantContact, error) {
			return ports.RegistrantContact{}, apperr.Validation("client profile is missing registrant details")
		}
		f.registrar.EnsureCustomerFn = func(context.Context, ports.RegistrantContact) (int64, error) {
			t.Fatal("no registrar call for an unusable profile")
			return 0, nil
		}
		runHunt(t, f, f.paidSnipe(nil))
		assert.Equal(t, domain.SnipeFailed, f.lastFinish().To)
		assert.EqualValues(t, 222_000, f.lastFinish().RefundAmount)
	})

	t.Run("a cached registrar customer skips the lookup", func(t *testing.T) {
		f := newFx()
		cust := int64(600)
		f.registrar.EnsureCustomerFn = func(context.Context, ports.RegistrantContact) (int64, error) {
			t.Fatal("the customer id is already on the row")
			return 0, nil
		}
		var used int64
		f.registrar.RegisterForCustomerFn = func(_ context.Context, name string, _ int, c int64, _ []string) (*ports.DomainResult, error) {
			used = c
			return registered(name), nil
		}
		runHunt(t, f, f.activeSnipe(func(s *domain.DomainSnipe) { f.inWindow(s); s.RegistrarCustomerID = &cust }))
		assert.EqualValues(t, 600, used)
	})

	t.Run("transient profile and customer failures are retried", func(t *testing.T) {
		f := newFx()
		var contacts, customers atomic.Int32
		f.domains.RegistrantContactFn = func(context.Context, int64) (ports.RegistrantContact, error) {
			if contacts.Add(1) == 1 {
				return ports.RegistrantContact{}, errBoom
			}
			return ports.RegistrantContact{Email: "a@b.c"}, nil
		}
		f.registrar.EnsureCustomerFn = func(context.Context, ports.RegistrantContact) (int64, error) {
			if customers.Add(1) == 1 {
				return 0, errAmbiguous()
			}
			return 501, nil
		}
		f.snipes.SetCustomerIDFn = func(context.Context, int64, int64) error { return errBoom } // only logged
		f.registerScript()
		runHunt(t, f, f.activeSnipe(f.inWindow))
		assert.EqualValues(t, 3, contacts.Load(), "the profile is re-read on every retry round")
		assert.EqualValues(t, 2, customers.Load())
		assert.Equal(t, 2, f.countSleeps(prepareRetry))
		assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
	})

	t.Run("rejected credentials pause instead of failing the snipe", func(t *testing.T) {
		f := newFx()
		f.registrar.EnsureCustomerFn = func(context.Context, ports.RegistrantContact) (int64, error) { return 0, errUnauthorized() }
		f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, errUnauthorized() }
		runHunt(t, f, f.activeSnipe(nil))
		_, ok := f.paused()
		assert.True(t, ok)
		assert.Empty(t, f.finishes)
	})

	t.Run("a customer the registrar rejects fails the snipe", func(t *testing.T) {
		f := newFx()
		f.registrar.EnsureCustomerFn = func(context.Context, ports.RegistrantContact) (int64, error) {
			return 0, errRejected("customer invalid", apperr.FieldError{Field: "customer_id", Message: "bad"})
		}
		runHunt(t, f, f.activeSnipe(nil))
		assert.Equal(t, domain.SnipeFailed, f.lastFinish().To)
	})

	t.Run("shutdown while preparing ends quietly", func(t *testing.T) {
		f := newFx()
		ctx, cancel := context.WithCancel(testCtx(t))
		f.registrar.EnsureCustomerFn = func(context.Context, ports.RegistrantContact) (int64, error) {
			cancel()
			return 0, errAmbiguous()
		}
		sup := f.supervisor()
		sup.runHunt(ctx, newHunt(f.activeSnipe(nil), func() {}))
		assert.Empty(t, f.finishes)

		f2 := newFx()
		ctx2, cancel2 := context.WithCancel(testCtx(t))
		f2.domains.RegistrantContactFn = func(context.Context, int64) (ports.RegistrantContact, error) {
			cancel2()
			return ports.RegistrantContact{}, errBoom
		}
		f2.supervisor().runHunt(ctx2, newHunt(f2.activeSnipe(nil), func() {}))
		assert.Empty(t, f2.finishes)
	})

	t.Run("unreadable default nameservers register without", func(t *testing.T) {
		f := newFx()
		f.settings.GetJSONFn = func(context.Context, string, any) error { return errBoom }
		ns := []string{"unset"}
		f.registrar.RegisterForCustomerFn = func(_ context.Context, name string, _ int, _ int64, got []string) (*ports.DomainResult, error) {
			ns = got
			return registered(name), nil
		}
		runHunt(t, f, f.activeSnipe(f.inWindow))
		assert.Nil(t, ns)
	})

	t.Run("a snipe's own nameservers win over the default", func(t *testing.T) {
		f := newFx()
		f.settings.GetJSONFn = func(context.Context, string, any) error {
			t.Fatal("the default must not even be read")
			return nil
		}
		var ns []string
		f.registrar.RegisterForCustomerFn = func(_ context.Context, name string, _ int, _ int64, got []string) (*ports.DomainResult, error) {
			ns = got
			return registered(name), nil
		}
		runHunt(t, f, f.activeSnipe(func(s *domain.DomainSnipe) { f.inWindow(s); s.Nameservers = []string{"a.net", "b.net"} }))
		assert.Equal(t, []string{"a.net", "b.net"}, ns)
	})
}

func TestHuntVerificationAndProbeFailures(t *testing.T) {
	t.Run("an unverifiable start is retried until it concludes", func(t *testing.T) {
		f := newFx()
		var syncs atomic.Int32
		f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
			if syncs.Add(1) == 1 {
				return nil, errAmbiguous()
			}
			return nil, apperr.NotFound("domain")
		}
		f.registerScript()
		runHunt(t, f, f.activeSnipe(f.inWindow))
		assert.EqualValues(t, 2, syncs.Load())
		assert.Equal(t, 1, f.countSleeps(10*time.Second))
		assert.Contains(t, f.eventKinds(), eventError)
	})

	t.Run("rejected credentials while verifying pause everything", func(t *testing.T) {
		f := newFx()
		f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) { return nil, errUnauthorized() }
		f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, errUnauthorized() }
		runHunt(t, f, f.activeSnipe(f.inWindow))
		_, ok := f.paused()
		assert.True(t, ok)
	})

	t.Run("a cancelled context during verification just stops", func(t *testing.T) {
		f := newFx()
		ctx, cancel := context.WithCancel(testCtx(t))
		f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
			cancel()
			return nil, context.Canceled
		}
		f.supervisor().runHunt(ctx, newHunt(f.activeSnipe(f.inWindow), func() {}))
		assert.Empty(t, f.finishes)
		assert.Empty(t, f.eventKinds())
	})

	t.Run("probe errors back off, pause or end the hunt", func(t *testing.T) {
		f := newFx()
		var probes atomic.Int32
		f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) {
			switch probes.Add(1) {
			case 1:
				return false, errRateLimited(4 * time.Second)
			case 2:
				return false, errNotSent()
			case 3:
				return false, errAmbiguous()
			}
			return true, nil
		}
		f.registerScript()
		runHunt(t, f, f.activeSnipe(nil))
		assert.EqualValues(t, 4, probes.Load())
		assert.Equal(t, 1, f.countSleeps(4*time.Second))
		assert.Equal(t, 1, f.countSleeps(2*time.Second), "breaker-open waits the rate-limit backoff")
		assert.Equal(t, 1, f.countSleeps(10*time.Second), "an unclear probe waits one patient interval")

		f = newFx()
		f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) { return false, errUnauthorized() }
		f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, errUnauthorized() }
		runHunt(t, f, f.activeSnipe(nil))
		_, ok := f.paused()
		assert.True(t, ok)

		f = newFx()
		ctx, cancel := context.WithCancel(testCtx(t))
		f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) {
			cancel()
			return false, context.Canceled
		}
		f.supervisor().runHunt(ctx, newHunt(f.activeSnipe(nil), func() {}))
		assert.Empty(t, f.finishes)
	})

	t.Run("a hunt stops when the supervisor is disabled or paused", func(t *testing.T) {
		f := newFx()
		f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) {
			t.Fatal("a paused supervisor must not probe")
			return false, nil
		}
		sup := f.supervisor()
		sup.cfg.PauseReason = "paused elsewhere"
		sup.runHunt(testCtx(t), newHunt(f.activeSnipe(nil), func() {}))
		assert.Empty(t, f.finishes)

		sup = f.supervisor()
		sup.cfg.Enabled = false
		sup.runHunt(testCtx(t), newHunt(f.activeSnipe(nil), func() {}))
		assert.Empty(t, f.finishes)
	})
}

func TestLimiter(t *testing.T) {
	f := newFx()
	l := &limiter{sleep: f.sleep}
	ctx := context.Background()

	release, err := l.acquire(ctx, 1)
	require.NoError(t, err)

	got := make(chan func(), 1)
	go func() {
		r, err := l.acquire(ctx, 1)
		assert.NoError(t, err)
		got <- r
	}()
	select {
	case <-got:
		t.Fatal("the second request must wait for a free slot")
	case <-time.After(30 * time.Millisecond):
	}
	release()
	release() // idempotent: a double release must not free a slot it does not hold
	select {
	case r := <-got:
		r()
	case <-time.After(5 * time.Second):
		t.Fatal("the second request never got the slot")
	}

	l2 := &limiter{sleep: f.sleep}
	hold, err := l2.acquire(ctx, 1)
	require.NoError(t, err)
	defer hold()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = l2.acquire(cancelled, 1)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestHuntModeTable(t *testing.T) {
	cfg := defaultConfig()
	now := t0
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	tests := []struct {
		name       string
		from, till *time.Time
		availBurst bool
		want       mode
		wait       time.Duration
	}{
		{"no window, quiet", nil, nil, false, modePatient, 0},
		{"no window, name looks free", nil, nil, true, modeBurst, 0},
		{"before the lead time", at(10 * time.Minute), nil, false, modeWait, 9 * time.Minute},
		{"inside the lead time", at(30 * time.Second), nil, false, modeBurst, 0},
		{"open-ended window", at(-time.Hour), nil, false, modeBurst, 0},
		{"inside a closed window", at(-time.Hour), at(time.Hour), false, modeBurst, 0},
		{"after the window", at(-2 * time.Hour), at(-time.Hour), false, modePatient, 0},
		{"after the window but the name looks free", at(-2 * time.Hour), at(-time.Hour), true, modeBurst, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHunt(domain.DomainSnipe{HuntFrom: tt.from, HuntUntil: tt.till}, func() {})
			got, wait := h.mode(cfg, now, tt.availBurst)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wait, wait)
		})
	}
}

func TestHuntHeartbeatSnapshotAndDedupe(t *testing.T) {
	h := newHunt(domain.DomainSnipe{ID: 3, Attempts: 4, Phase: ""}, func() {})
	hb := h.heartbeat("w1", t0.Add(30*time.Second))
	assert.EqualValues(t, 3, hb.ID)
	assert.EqualValues(t, 4, hb.Attempts)
	assert.Equal(t, domain.SnipePhasePatient, hb.Phase, "an unset phase reads as patient")
	assert.Nil(t, hb.LastAttemptAt)

	h.attempts.Add(1)
	h.lastAttempt.Store(t0.UnixNano())
	h.setLastError("x")
	assert.True(t, h.setPhase(domain.SnipePhaseBurst))
	assert.False(t, h.setPhase(domain.SnipePhaseBurst))
	hb = h.heartbeat("w1", t0)
	assert.EqualValues(t, 5, hb.Attempts)
	assert.Equal(t, domain.SnipePhaseBurst, hb.Phase)
	assert.Equal(t, "x", hb.LastError)
	require.NotNil(t, hb.LastAttemptAt)
	assert.True(t, hb.LastAttemptAt.Equal(t0))

	assert.True(t, h.dedupe("k", t0))
	assert.False(t, h.dedupe("k", t0.Add(time.Minute)), "the same trail entry within five minutes is dropped")
	assert.True(t, h.dedupe("k", t0.Add(eventDedupe)))
	assert.True(t, h.dedupe("other", t0))
}

// repeatErr is n copies of err, for scripting long runs of identical replies.
func repeatErr(err error, n int) []error {
	out := make([]error, n)
	for i := range out {
		out[i] = err
	}
	return out
}
