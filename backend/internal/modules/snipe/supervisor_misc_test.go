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

func TestSmallHelpers(t *testing.T) {
	assert.Equal(t, time.Second, minDuration(time.Second, time.Minute))
	assert.Equal(t, time.Second, minDuration(time.Minute, time.Second))
	assert.Empty(t, statusOf(nil))
	assert.Equal(t, "active", statusOf(&ports.DomainSyncInfo{Status: "active"}))
}

func TestPauseWithoutADeadlineSleepsTheFullInterval(t *testing.T) {
	f := newFx()
	sup := f.supervisor()
	h := newHunt(domain.DomainSnipe{ID: 1}, func() {})
	sup.pause(context.Background(), h, 5*time.Second)
	assert.Equal(t, 1, f.countSleeps(5*time.Second))
}

func TestTrailAndAlertFailuresAreOnlyLogged(t *testing.T) {
	f := newFx()
	f.snipes.AddEventFn = func(context.Context, *domain.SnipeEvent) error { return errBoom }
	f.notifier.AlertAdminFn = func(context.Context, string, string) error { return errBoom }
	sup := f.supervisor()
	sup.event(context.Background(), 1, "x", "y")
	sup.alert(context.Background(), "subject", "message")
}

func TestExpireAndFailSwallowRepositoryErrors(t *testing.T) {
	f := newFx()
	f.snipes.FinishFn = func(context.Context, ports.SnipeFinish) (bool, error) { return false, errBoom }
	sup := f.supervisor()
	h := newHunt(f.activeSnipe(nil), func() {})
	sup.expire(context.Background(), h)
	sup.fail(context.Background(), h, "boom")
	assert.Empty(t, f.templates)
}

func TestEnsureNotRegisteredAcceptsAnEmptyLookup(t *testing.T) {
	f := newFx()
	f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) { return nil, nil }
	require.NoError(t, f.service().ensureNotRegistered(context.Background(), "hunt.com"))
}

// A saturated limiter plus a cancelled context ends every registrar-facing
// step instead of waiting forever for a slot.
func TestRegistrarStepsGiveUpWhenCancelledWhileWaitingForASlot(t *testing.T) {
	f := newFx()
	sup := f.supervisor()
	sup.lim.n = defaultConfig().MaxInFlight // every slot taken
	h := newHunt(f.activeSnipe(nil), func() {})
	cfg := defaultConfig()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sup.attempt(ctx, h, cfg)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = sup.probe(ctx, h, cfg)
	assert.ErrorIs(t, err, context.Canceled)
	concluded, won := sup.verify(ctx, h, cfg)
	assert.False(t, concluded)
	assert.False(t, won)
}

// A second hunt of the same snipe (its lease lapsed and another worker took
// over) can detect the domain as ours after the first one already recorded the
// win. That is a no-op, not a failure to retry or alert about.
func TestDuplicateWinByAnotherHuntIsQuietlyIgnored(t *testing.T) {
	f := newFx()
	f.registerScript()
	f.domains.RecordSnipedDomainFn = func(context.Context, ports.RecordSnipedDomainInput) (*domain.Domain, error) {
		return nil, apperr.Conflict("domain hunt.com already exists")
	}
	f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
		return &domain.DomainSnipe{ID: 1, Status: domain.SnipeWon}, nil
	}

	runHunt(t, f, f.activeSnipe(f.inWindow))

	assert.Empty(t, f.alerts, "no alert for a win that is already on record")
	assert.Equal(t, 0, f.countSleeps(winRecordWait), "and no retries")
	assert.Empty(t, f.templates, "the winner already told the client")
}

func TestConflictWhileTheSnipeIsNotWonStillRetriesAndAlerts(t *testing.T) {
	f := newFx()
	f.registerScript()
	f.domains.RecordSnipedDomainFn = func(context.Context, ports.RecordSnipedDomainInput) (*domain.Domain, error) {
		return nil, apperr.Conflict("domain hunt.com already exists")
	}
	f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
		return &domain.DomainSnipe{ID: 1, Status: domain.SnipeActive}, nil
	}
	runHunt(t, f, f.activeSnipe(f.inWindow))
	require.Len(t, f.alerts, 1, "a genuine conflict (e.g. an admin imported the domain by hand) is reported")

	f = newFx()
	f.registerScript()
	f.domains.RecordSnipedDomainFn = func(context.Context, ports.RecordSnipedDomainInput) (*domain.Domain, error) {
		return nil, apperr.Conflict("exists")
	}
	f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return nil, errBoom }
	runHunt(t, f, f.activeSnipe(f.inWindow))
	require.Len(t, f.alerts, 1, "an unreadable snipe is not assumed won")
}

// Before any refund-bearing ending the registrar is asked whether the domain is
// in fact already ours: a register call that died with its worker may have
// succeeded, and refunding it would hand the client their money back AND leave
// an unrecorded paid domain in the reseller account.
func TestEndingsVerifyOwnershipBeforeRefunding(t *testing.T) {
	owned := func(context.Context, string) (*ports.DomainSyncInfo, error) {
		return &ports.DomainSyncInfo{Status: "active", ExpiryDate: t0.AddDate(1, 0, 0)}, nil
	}
	endings := map[string]func(*Supervisor, *hunt){
		"expire": func(s *Supervisor, h *hunt) { s.expire(context.Background(), h) },
		"fail":   func(s *Supervisor, h *hunt) { s.fail(context.Background(), h, "gave up") },
	}
	for name, end := range endings {
		t.Run(name+" records a win instead when the domain is ours", func(t *testing.T) {
			f := newFx()
			f.registrar.SyncDomainFn = owned
			end(f.supervisor(), newHunt(f.paidSnipe(nil), func() {}))

			require.Len(t, f.finishes, 1)
			assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
			assert.Zero(t, f.lastFinish().RefundAmount, "no money goes back for a domain we hold")
			assert.Empty(t, f.credits)
		})
		t.Run(name+" is postponed while ownership cannot be established", func(t *testing.T) {
			f := newFx()
			f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) { return nil, errAmbiguous() }
			end(f.supervisor(), newHunt(f.paidSnipe(nil), func() {}))
			assert.Empty(t, f.finishes, "better a late refund than a wrong one")
			assert.Empty(t, f.credits)
		})
		t.Run(name+" refunds once the registrar says the domain is not ours", func(t *testing.T) {
			f := newFx() // default SyncDomain: not found
			end(f.supervisor(), newHunt(f.paidSnipe(nil), func() {}))
			require.Len(t, f.finishes, 1)
			assert.NotZero(t, f.lastFinish().RefundAmount)
		})
	}
}

func TestExpireDueSettlesOwnedDomainsAndPostponesUnknownOnes(t *testing.T) {
	f := newFx()
	own := f.paidSnipe(nil)
	other := f.activeSnipe(func(s *domain.DomainSnipe) { s.ID, s.DomainName = 2, "other.com" })
	unknown := f.activeSnipe(func(s *domain.DomainSnipe) { s.ID, s.DomainName = 3, "unknown.com" })
	f.snipes.ListExpirableFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
		return []domain.DomainSnipe{own, other, unknown}, nil
	}
	f.registrar.SyncDomainFn = func(_ context.Context, name string) (*ports.DomainSyncInfo, error) {
		switch name {
		case own.DomainName:
			return &ports.DomainSyncInfo{Status: "active"}, nil
		case unknown.DomainName:
			return nil, errAmbiguous()
		}
		return nil, apperr.NotFound("domain")
	}

	f.supervisor().expireDue(context.Background(), t0)

	require.Len(t, f.finishes, 2)
	assert.Equal(t, domain.SnipeWon, f.finishes[0].To)
	assert.Equal(t, int64(2), f.finishes[1].ID)
	assert.Equal(t, domain.SnipeExpired, f.finishes[1].To)
}

// While the sniper is paused or switched off nobody was hunting: the window
// that passed is not the client's loss, so it ends as failed (full refund).
func TestExpireDueDuringAPauseFailsTheSnipeWithAFullRefund(t *testing.T) {
	for name, set := range map[string]func(*fx){
		"paused": func(f *fx) {
			f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
				if key == SettingPauseReason {
					return "credentials rejected", nil
				}
				return def, nil
			}
		},
		"disabled": func(f *fx) {
			f.settings.GetBoolFn = func(context.Context, string, bool) (bool, error) { return false, nil }
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFx()
			set(f)
			f.snipes.ListExpirableFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
				return []domain.DomainSnipe{f.paidSnipe(nil)}, nil
			}
			sup := f.supervisor()
			sup.refreshConfig(context.Background(), t0)
			sup.expireDue(context.Background(), t0)

			require.Len(t, f.finishes, 1)
			assert.Equal(t, domain.SnipeFailed, f.lastFinish().To)
			assert.EqualValues(t, 222_000, f.lastFinish().RefundAmount, "fee included (166.500 domain + 55.500 fee): our downtime, not their loss")
		})
	}
}

// A call cut off mid-flight (shutdown, lost lease) may still have been
// processed: the aborted hunt checks the account itself before giving up.
func TestAbortedRegisterCallIsVerifiedBeforeTheHuntStops(t *testing.T) {
	f := newFx()
	ctx, cancel := context.WithCancel(testCtx(t))
	f.registrar.RegisterForCustomerFn = func(context.Context, string, int, int64, []string) (*ports.DomainResult, error) {
		cancel()
		return nil, context.Canceled
	}
	f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
		return &ports.DomainSyncInfo{Status: "active"}, nil
	}
	f.supervisor().runHunt(ctx, newHunt(f.activeSnipe(f.inWindow), func() {}))

	require.Len(t, f.finishes, 1, "the registrar did register it before the connection dropped")
	assert.Equal(t, domain.SnipeWon, f.lastFinish().To)

	f = newFx() // not ours: nothing to record, the hunt simply stops
	ctx, cancel = context.WithCancel(testCtx(t))
	f.registrar.RegisterForCustomerFn = func(context.Context, string, int, int64, []string) (*ports.DomainResult, error) {
		cancel()
		return nil, context.Canceled
	}
	f.supervisor().runHunt(ctx, newHunt(f.activeSnipe(f.inWindow), func() {}))
	assert.Empty(t, f.finishes)
}

// One refused domain (a restricted TLD answering 403) must not stop every hunt
// of every client: only a confirmed unusable account pauses.
func TestVetPauseOnlyPausesWhenTheAccountIsReallyUnusable(t *testing.T) {
	cfg := defaultConfig()
	f := newFx()
	sup := f.supervisor()
	unauthorized := classify(errUnauthorized(), cfg, "")
	require.Equal(t, outPause, unauthorized.kind)

	got := sup.vetPause(context.Background(), cfg, unauthorized)
	assert.Equal(t, outRetry, got.kind, "the account answers fine: this was about one domain")
	assert.Contains(t, got.msg, "credentials")

	f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, errUnauthorized() }
	assert.Equal(t, outPause, sup.vetPause(context.Background(), cfg, unauthorized).kind)

	f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, errAmbiguous() }
	assert.Equal(t, outRetry, sup.vetPause(context.Background(), cfg, unauthorized).kind,
		"an unreachable account is a transient fault, not rejected credentials")

	balance := classify(errRejected("Insufficient balance"), cfg, "")
	f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { panic("not asked") }
	assert.Equal(t, balance, sup.vetPause(context.Background(), cfg, balance), "a balance verdict is kept as is")
	taken := classify(errTaken(), cfg, "")
	assert.Equal(t, taken, sup.vetPause(context.Background(), cfg, taken), "other verdicts pass through")
}

func TestClaimNeverStartsASecondGoroutineForAHuntThisProcessRuns(t *testing.T) {
	f := newFx()
	f.registerScript(errTaken())
	sn := f.activeSnipe(f.inWindow)
	var claims atomic.Int32
	f.snipes.ClaimFn = func(context.Context, string, int, time.Time, time.Time) ([]domain.DomainSnipe, error) {
		claims.Add(1)
		return []domain.DomainSnipe{sn}, nil
	}
	sup := f.supervisor()
	var cancelled atomic.Bool
	sup.hunts[sn.ID] = newHunt(sn, func() { cancelled.Store(true) })
	assert.True(t, sup.isRunning(sn.ID))
	assert.False(t, sup.isRunning(999))

	sup.claim(testCtx(t), t0, sup.config())
	sup.wg.Wait()

	assert.EqualValues(t, 1, claims.Load())
	assert.Empty(t, f.heartbeats)
	assert.False(t, cancelled.Load(), "the running hunt carries on untouched")
	assert.Len(t, sup.hunts, 1)
}
