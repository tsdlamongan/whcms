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

// accountCalls counts AccountInfo calls while keeping the healthy default.
func (f *fx) accountCalls() *atomic.Int32 {
	var n atomic.Int32
	prev := f.registrar.AccountInfoFn
	f.registrar.AccountInfoFn = func(ctx context.Context) (*ports.RegistrarAccountInfo, error) {
		n.Add(1)
		return prev(ctx)
	}
	return &n
}

// claimCalls records every Claim and serves the given snipes the first time.
func (f *fx) claimCalls(serve ...domain.DomainSnipe) (*atomic.Int32, *struct {
	owner      string
	limit      int
	now, lease time.Time
}) {
	var n atomic.Int32
	got := &struct {
		owner      string
		limit      int
		now, lease time.Time
	}{}
	f.snipes.ClaimFn = func(_ context.Context, owner string, limit int, now, lease time.Time) ([]domain.DomainSnipe, error) {
		if n.Add(1) == 1 {
			got.owner, got.limit, got.now, got.lease = owner, limit, now, lease
			return serve, nil
		}
		return nil, nil
	}
	return &n, got
}

func TestTickClaimsAndRunsHunts(t *testing.T) {
	f := newFx()
	f.registerScript(errTaken())
	claims, got := f.claimCalls(f.activeSnipe(f.inWindow))
	accounts := f.accountCalls()
	sup := f.supervisor()
	ctx := testCtx(t)

	sup.Tick(ctx)
	sup.wg.Wait()

	assert.EqualValues(t, 1, claims.Load())
	assert.Equal(t, "w1", got.owner)
	assert.Equal(t, 50, got.limit, "capacity is max_active_global minus what already runs")
	assert.Equal(t, t0, got.now)
	assert.Equal(t, t0.Add(leaseTTL), got.lease)
	assert.EqualValues(t, 1, accounts.Load(), "preflight runs once before claiming")
	assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
	assert.Zero(t, sup.running(), "a finished hunt removes itself")

	// Within the cache window neither the config, the preflight nor the
	// heartbeat repeat; claiming does (new work can appear any time).
	sup.Tick(ctx)
	assert.EqualValues(t, 1, accounts.Load(), "the healthy preflight is cached")
	assert.EqualValues(t, 2, claims.Load())
	assert.Len(t, f.heartbeats, 0, "nothing was running to heartbeat")
}

func TestTickHeartbeat(t *testing.T) {
	ctx := context.Background()

	t.Run("renews the lease and flushes progress", func(t *testing.T) {
		f := newFx()
		sup := f.supervisor()
		h := newHunt(f.activeSnipe(nil), func() {})
		h.attempts.Add(7)
		h.setLastError("still taken")
		sup.hunts[1] = h

		sup.heartbeat(ctx, t0)

		require.Len(t, f.heartbeats, 1)
		hb := f.heartbeats[0]
		assert.EqualValues(t, 1, hb.ID)
		assert.Equal(t, "w1", hb.Owner)
		assert.Equal(t, t0.Add(leaseTTL), hb.LeaseUntil)
		assert.EqualValues(t, 7, hb.Attempts)
		assert.Equal(t, "still taken", hb.LastError)
	})

	t.Run("a refused heartbeat stops the hunt", func(t *testing.T) {
		f := newFx()
		f.snipes.HeartbeatFn = func(context.Context, ports.SnipeHeartbeat) (bool, error) { return false, nil }
		var cancelled atomic.Bool
		sup := f.supervisor()
		sup.hunts[1] = newHunt(f.activeSnipe(nil), func() { cancelled.Store(true) })
		sup.heartbeat(ctx, t0)
		assert.True(t, cancelled.Load(), "cancelled by an admin or taken over by another worker")
	})

	t.Run("a heartbeat error keeps the hunt running", func(t *testing.T) {
		f := newFx()
		f.snipes.HeartbeatFn = func(context.Context, ports.SnipeHeartbeat) (bool, error) { return false, errBoom }
		var cancelled atomic.Bool
		sup := f.supervisor()
		sup.hunts[1] = newHunt(f.activeSnipe(nil), func() { cancelled.Store(true) })
		sup.heartbeat(ctx, t0)
		assert.False(t, cancelled.Load())
	})

	t.Run("runs at most every heartbeatEvery", func(t *testing.T) {
		f := newFx()
		sup := f.supervisor()
		sup.hunts[1] = newHunt(f.activeSnipe(nil), func() {})
		sup.Tick(ctx) // first tick always beats
		assert.Len(t, f.heartbeats, 1)
		f.clock.Advance(heartbeatEvery - time.Millisecond)
		sup.Tick(ctx)
		assert.Len(t, f.heartbeats, 1)
		f.clock.Advance(time.Millisecond)
		sup.Tick(ctx)
		assert.Len(t, f.heartbeats, 2)
	})
}

func TestTickExpiresIdleHuntsPastTheirDeadline(t *testing.T) {
	f := newFx()
	due := []domain.DomainSnipe{f.paidSnipe(nil), f.activeSnipe(func(s *domain.DomainSnipe) { s.ID = 2 })}
	f.snipes.ListExpirableFn = func(_ context.Context, now time.Time, limit int) ([]domain.DomainSnipe, error) {
		assert.Equal(t, t0, now)
		assert.Equal(t, expireBatch, limit)
		return due, nil
	}
	f.supervisor().expireDue(context.Background(), t0)

	require.Len(t, f.finishes, 2)
	for _, fin := range f.finishes {
		assert.Equal(t, domain.SnipeExpired, fin.To)
		assert.True(t, fin.RequireIdle, "a live hunt expires itself")
		assert.Empty(t, fin.Owner)
	}
	assert.EqualValues(t, 166_500, f.finishes[0].RefundAmount)
	assert.Zero(t, f.finishes[1].RefundAmount)
	assert.Equal(t, []string{tplExpired, tplExpired}, f.templateKeys())
}

func TestTickExpiryFailuresAreContained(t *testing.T) {
	f := newFx()
	f.snipes.ListExpirableFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) { return nil, errBoom }
	f.supervisor().expireDue(context.Background(), t0)
	assert.Empty(t, f.finishes)

	f = newFx()
	f.snipes.ListExpirableFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
		return []domain.DomainSnipe{f.activeSnipe(nil)}, nil
	}
	f.snipes.FinishFn = func(context.Context, ports.SnipeFinish) (bool, error) { return false, errBoom }
	f.supervisor().expireDue(context.Background(), t0)
	assert.Empty(t, f.templates)
}

func TestTickDisabledStopsHuntsAndClaimsNothing(t *testing.T) {
	f := newFx()
	f.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
		assert.Equal(t, SettingEnabled, key)
		return false, nil
	}
	claims, _ := f.claimCalls()
	var cancelled atomic.Bool
	sup := f.supervisor()
	sup.hunts[1] = newHunt(f.activeSnipe(nil), func() { cancelled.Store(true) })

	sup.Tick(context.Background())

	assert.True(t, cancelled.Load())
	assert.Zero(t, claims.Load())
}

func TestTickDoesNotClaimBeyondCapacity(t *testing.T) {
	f := newFx()
	f.cfgOverride[SettingMaxActiveGlobal] = 1
	claims, _ := f.claimCalls()
	sup := f.supervisor()
	sup.hunts[1] = newHunt(f.activeSnipe(nil), func() {})
	sup.Tick(context.Background())
	assert.Zero(t, claims.Load())
}

func TestTickClaimFailureIsContained(t *testing.T) {
	f := newFx()
	f.snipes.ClaimFn = func(context.Context, string, int, time.Time, time.Time) ([]domain.DomainSnipe, error) {
		return nil, errBoom
	}
	f.supervisor().Tick(context.Background())
	assert.Zero(t, f.supervisor().running())
}

// forbidRegistrarTraffic fails the test if a hunt reaches the registrar.
func (f *fx) forbidRegistrarTraffic(t *testing.T) {
	t.Helper()
	f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) {
		t.Fatal("no hunt may run while the preflight is failing")
		return false, nil
	}
	f.registrar.RegisterForCustomerFn = func(context.Context, string, int, int64, []string) (*ports.DomainResult, error) {
		t.Fatal("no hunt may run while the preflight is failing")
		return nil, nil
	}
}

// An idle system - the normal state - must not call the registrar at all: the
// account preflight costs two requests and only matters once a hunt exists.
func TestTickWithNothingToHuntNeverCallsTheRegistrar(t *testing.T) {
	f := newFx()
	accounts := f.accountCalls()
	f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) {
		t.Fatal("nothing to probe")
		return false, nil
	}
	sup := f.supervisor()
	for i := 0; i < 5; i++ {
		sup.Tick(context.Background())
		f.clock.Advance(tickInterval)
	}
	assert.Zero(t, accounts.Load(), "no snipes, no preflight")
}

func TestTickPreflight(t *testing.T) {
	ctx := context.Background()

	t.Run("rejected credentials pause instead of hunting, and hand the claimed rows back", func(t *testing.T) {
		f := newFx()
		f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, errUnauthorized() }
		f.forbidRegistrarTraffic(t)
		claims, _ := f.claimCalls(f.activeSnipe(f.inWindow))
		sup := f.supervisor()

		sup.Tick(ctx)

		assert.EqualValues(t, 1, claims.Load())
		assert.Zero(t, sup.running(), "no hunt starts")
		assert.Equal(t, []int64{1}, f.released, "the lease taken for the check is given straight back")
		reason, ok := f.paused()
		require.True(t, ok)
		assert.Contains(t, reason, "credentials")
		assert.Equal(t, []string{"Domain sniper paused"}, f.alerts)
	})

	t.Run("a transient failure only skips the tick and is retried soon, without claiming meanwhile", func(t *testing.T) {
		f := newFx()
		accounts := f.accountCalls()
		var fail atomic.Bool
		fail.Store(true)
		inner := f.registrar.AccountInfoFn
		f.registrar.AccountInfoFn = func(c context.Context) (*ports.RegistrarAccountInfo, error) {
			if fail.Load() {
				return nil, errAmbiguous()
			}
			return inner(c)
		}
		var claims atomic.Int32
		sn := f.activeSnipe(f.inWindow)
		f.snipes.ClaimFn = func(context.Context, string, int, time.Time, time.Time) ([]domain.DomainSnipe, error) {
			claims.Add(1)
			return []domain.DomainSnipe{sn}, nil
		}
		f.registerScript()
		sup := f.supervisor()

		sup.Tick(ctx)
		_, paused := f.paused()
		assert.False(t, paused, "a hiccup is not a pause")
		assert.EqualValues(t, 1, claims.Load())
		assert.Equal(t, []int64{1}, f.released)
		callsAfterFirst := accounts.Load()

		f.clock.Advance(time.Second)
		sup.Tick(ctx)
		assert.EqualValues(t, 1, claims.Load(), "a cached failure skips claiming: no churn")
		assert.Equal(t, callsAfterFirst, accounts.Load(), "and no hammering of the account endpoint")

		fail.Store(false)
		f.clock.Advance(preflightRetry)
		sup.Tick(ctx)
		sup.wg.Wait()
		assert.EqualValues(t, 2, claims.Load(), "recovered: claiming resumes")
		assert.Equal(t, domain.SnipeWon, f.lastFinish().To, "and the hunt ran")
	})

	t.Run("a balance under the floor pauses", func(t *testing.T) {
		f := newFx()
		f.cfgOverride[SettingMinResellerBalance] = 10_000_000 // balance is 5,000,000
		f.forbidRegistrarTraffic(t)
		f.claimCalls(f.activeSnipe(f.inWindow))
		f.supervisor().Tick(ctx)
		reason, ok := f.paused()
		require.True(t, ok)
		assert.Contains(t, reason, "below the configured floor")
		assert.Equal(t, []int64{1}, f.released)
	})

	t.Run("a missing account answer is treated as healthy", func(t *testing.T) {
		f := newFx()
		f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, nil }
		f.registerScript()
		claims, _ := f.claimCalls(f.activeSnipe(f.inWindow))
		sup := f.supervisor()
		sup.Tick(ctx)
		sup.wg.Wait()
		assert.EqualValues(t, 1, claims.Load())
		assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
	})

	t.Run("a failure to hand a row back is only logged", func(t *testing.T) {
		f := newFx()
		f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, errUnauthorized() }
		f.snipes.ReleaseFn = func(context.Context, int64, string) error { return errBoom }
		f.claimCalls(f.activeSnipe(nil))
		f.supervisor().Tick(ctx)
		_, ok := f.paused()
		assert.True(t, ok)
	})
}

func TestTickPauseProbeResumes(t *testing.T) {
	ctx := context.Background()

	t.Run("an unhealthy account stays paused and is probed once a minute", func(t *testing.T) {
		f := newFx()
		f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
			if key == SettingPauseReason {
				return "credentials rejected", nil
			}
			return def, nil
		}
		calls := f.accountCalls()
		inner := f.registrar.AccountInfoFn
		var healthy atomic.Bool
		f.registrar.AccountInfoFn = func(c context.Context) (*ports.RegistrarAccountInfo, error) {
			if !healthy.Load() {
				calls.Add(0)
				return nil, errUnauthorized()
			}
			return inner(c)
		}
		claims, _ := f.claimCalls()
		sup := f.supervisor()

		sup.Tick(ctx)
		assert.Zero(t, claims.Load(), "paused: no claiming")
		assert.Empty(t, f.settingsSet, "still broken: the markers stay")

		f.clock.Advance(20 * time.Second)
		sup.Tick(ctx)
		assert.Empty(t, f.settingsSet)

		healthy.Store(true)
		f.clock.Advance(defaultConfig().PauseProbe)
		sup.Tick(ctx)
		assert.Equal(t, "", f.settingsSet[SettingPauseReason], "the pause marker is cleared")
		assert.Equal(t, "", f.settingsSet[SettingPausedAt])
		assert.Equal(t, []string{"Domain sniper resumed"}, f.alerts)
		assert.Empty(t, sup.config().PauseReason)
	})

	t.Run("a balance still under the floor does not resume", func(t *testing.T) {
		f := newFx()
		f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
			if key == SettingPauseReason {
				return "balance below floor", nil
			}
			return def, nil
		}
		f.cfgOverride[SettingMinResellerBalance] = 99_000_000
		f.supervisor().Tick(ctx)
		assert.Empty(t, f.settingsSet)
		assert.Empty(t, f.alerts)
	})

	t.Run("a resume that cannot be persisted stays paused", func(t *testing.T) {
		f := newFx()
		f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
			if key == SettingPauseReason {
				return "paused", nil
			}
			return def, nil
		}
		f.settings.SetFn = func(context.Context, string, any) error { return errBoom }
		sup := f.supervisor()
		sup.Tick(ctx)
		assert.Equal(t, "paused", sup.config().PauseReason)
		assert.Empty(t, f.alerts)
	})
}

func TestSetPausePersistenceFailuresAreContained(t *testing.T) {
	f := newFx()
	f.settings.SetFn = func(context.Context, string, any) error { return errBoom }
	f.notifier.AlertAdminFn = func(context.Context, string, string) error { return errBoom }
	sup := f.supervisor()
	sup.setPause(context.Background(), "credentials rejected", false)
	assert.Equal(t, "credentials rejected", sup.config().PauseReason, "the in-memory pause still applies")

	// resume: alert failure is only logged.
	sup2 := newFx()
	sup2.notifier.AlertAdminFn = func(context.Context, string, string) error { return errBoom }
	s := sup2.supervisor()
	s.cfg.PauseReason = "x"
	s.probePause(context.Background(), t0, s.config())
	assert.Empty(t, s.config().PauseReason)
}

func TestRefreshConfigKeepsThePreviousSnapshotOnError(t *testing.T) {
	f := newFx()
	sup := f.supervisor()
	sup.cfg.MaxActiveGlobal = 3
	f.settings.GetIntFn = func(context.Context, string, int) (int, error) { return 0, errBoom }

	sup.refreshConfig(context.Background(), t0)
	assert.Equal(t, 3, sup.config().MaxActiveGlobal, "a database hiccup never changes the snapshot")

	// And the failed attempt is not retried until the TTL passes.
	var reads atomic.Int32
	f.settings.GetIntFn = func(_ context.Context, _ string, def int) (int, error) { reads.Add(1); return def, nil }
	sup.refreshConfig(context.Background(), t0.Add(time.Second))
	assert.Zero(t, reads.Load())
	sup.refreshConfig(context.Background(), t0.Add(configTTL))
	assert.NotZero(t, reads.Load())
	assert.Equal(t, 50, sup.config().MaxActiveGlobal)
}

func TestRunStopsHuntsOnShutdown(t *testing.T) {
	f := newFx()
	started := make(chan struct{})
	f.registrar.IsAvailableFn = func(ctx context.Context, _ string) (bool, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-ctx.Done()
		return false, ctx.Err()
	}
	f.claimCalls(f.activeSnipe(nil))

	d := f.deps()
	ctx, cancel := context.WithCancel(testCtx(t))
	base := d.Sleep
	var ticks atomic.Int32
	d.Sleep = func(c context.Context, dur time.Duration) error {
		// Let the supervisor tick once, wait for the hunt to be probing, then stop.
		if ticks.Add(1) == 1 {
			<-started
			cancel()
		}
		return base(c, dur)
	}
	sup := NewSupervisor(d)

	done := make(chan struct{})
	go func() {
		sup.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	assert.Zero(t, sup.running())
	assert.Equal(t, []int64{1}, f.released, "the lease is handed back on shutdown")
	assert.Empty(t, f.finishes, "stopping is not an ending")
}

func TestSnipeClassifyStatusOfAccountErrors(t *testing.T) {
	// A bare apperr (no wire detail) from AccountInfo during preflight is
	// treated as a transient failure rather than a pause.
	f := newFx()
	f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) {
		return nil, apperr.External("rdash", errBoom)
	}
	f.forbidRegistrarTraffic(t)
	claims, _ := f.claimCalls(f.activeSnipe(f.inWindow))
	sup := f.supervisor()
	sup.Tick(context.Background())
	assert.EqualValues(t, 1, claims.Load())
	assert.Zero(t, sup.running())
	_, paused := f.paused()
	assert.False(t, paused)
}
