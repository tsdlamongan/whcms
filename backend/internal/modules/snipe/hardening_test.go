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

// Cancelling a running hunt refunds money, so it first asks the registrar
// whether the domain is in fact already ours (a registration that was still
// settling when the hunt last looked).
func TestCancellingARunningHuntChecksOwnershipFirst(t *testing.T) {
	ctx := context.Background()
	running := func() *domain.DomainSnipe {
		return &domain.DomainSnipe{
			ID: 4, ClientID: 7, DomainName: "hunt.com", Status: domain.SnipeActive, InvoiceID: ptr(900),
			RefundableDomain: 166_500, RefundableFee: 55_500,
		}
	}
	cancels := map[string]func(f *clientFx) error{
		"client": func(f *clientFx) error { _, err := f.service().CancelForClient(ctx, f.who(), 4); return err },
		"admin":  func(f *clientFx) error { _, err := f.service().AdminCancel(ctx, 9, 4, false); return err },
	}
	for who, cancel := range cancels {
		t.Run(who+": an owned domain refuses the cancel (the hunt records the win instead)", func(t *testing.T) {
			f := newClientFx()
			f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return running(), nil }
			f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
				return &ports.DomainSyncInfo{Status: "active"}, nil
			}
			assertCode(t, cancel(f), apperr.CodeConflict)
			assert.Empty(t, f.finishes, "no state change")
			assert.Empty(t, f.credits, "and no money moved")
		})
		t.Run(who+": an unknown answer refuses too", func(t *testing.T) {
			f := newClientFx()
			f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return running(), nil }
			f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) { return nil, errAmbiguous() }
			assertCode(t, cancel(f), apperr.CodeExternal)
			assert.Empty(t, f.finishes)
			assert.Empty(t, f.credits)
		})
		t.Run(who+": not ours goes ahead and refunds", func(t *testing.T) {
			f := newClientFx() // default SyncDomain: not found
			f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return running(), nil }
			require.NoError(t, cancel(f))
			require.Len(t, f.finishes, 1)
			assert.Equal(t, domain.SnipeCancelled, f.lastFinish().To)
		})
		t.Run(who+": an unpaid snipe was never hunted, so the registrar is not asked", func(t *testing.T) {
			f := newClientFx()
			f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
				sn := running()
				sn.Status = domain.SnipePendingPayment
				return sn, nil
			}
			f.registrar.SyncDomainFn = func(context.Context, string) (*ports.DomainSyncInfo, error) {
				t.Fatal("nothing can have been registered for an unpaid snipe")
				return nil, nil
			}
			require.NoError(t, cancel(f))
		})
	}

	t.Run("an unreadable settings store falls back to the default timeout", func(t *testing.T) {
		f := newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return running(), nil }
		f.settings.GetStringFn = func(context.Context, string, string) (string, error) { return "", errBoom }
		require.NoError(t, cancels["admin"](f))
	})
}

func TestAdminCreateRespectsTheGlobalCapacity(t *testing.T) {
	ctx := context.Background()
	f := newClientFx()
	f.cfgOverride[SettingMaxActiveGlobal] = 2
	f.snipes.CountByStatusFn = func(context.Context) (map[domain.SnipeStatus]int, error) {
		return map[domain.SnipeStatus]int{domain.SnipeActive: 1, domain.SnipePendingPayment: 1}, nil
	}
	f.snipes.CreateFn = func(context.Context, *domain.DomainSnipe) error {
		t.Fatal("nothing may be written when the sniper is full")
		return nil
	}
	_, err := f.service().AdminCreate(ctx, 9, AdminCreateRequest{ClientID: 7, Domain: "hunt.com"})
	assertCode(t, err, apperr.CodeConflict)

	f = newClientFx()
	f.snipes.CountByStatusFn = func(context.Context) (map[domain.SnipeStatus]int, error) { return nil, errBoom }
	_, err = f.service().AdminCreate(ctx, 9, AdminCreateRequest{ClientID: 7, Domain: "hunt.com"})
	assert.ErrorIs(t, err, errBoom)
}

func TestClientCreateRefusesWhileThePauseIsOn(t *testing.T) {
	f := newClientFx()
	f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
		if key == SettingPauseReason {
			return "reseller balance too low", nil
		}
		return def, nil
	}
	_, err := f.service().Create(context.Background(), f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
	assertCode(t, err, apperr.CodeForbidden)
	assert.Zero(t, f.invoiceCall, "no invoice for a hunt that cannot run")
}

func TestClientCreateHidesWhoElseIsHuntingTheName(t *testing.T) {
	f := newClientFx()
	f.snipes.CreateFn = func(context.Context, *domain.DomainSnipe) error {
		return apperr.Conflict("a snipe for hunt.com is already open")
	}
	_, err := f.service().Create(context.Background(), f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
	assertCode(t, err, apperr.CodeConflict)
	assert.NotContains(t, err.Error(), "already open")
	assert.Contains(t, err.Error(), "hunt.com")
}

// A paid hunt must not retry the registrar customer for ever when the
// registrar definitively refuses the client's details: it ends as failed with
// a full refund instead of keeping the fee for a hunt that never ran.
func TestPrepareGivesUpOnADefinitiveRefusalOfTheAccountDetails(t *testing.T) {
	f := newFx()
	var calls atomic.Int32
	f.registrar.EnsureCustomerFn = func(context.Context, ports.RegistrantContact) (int64, error) {
		calls.Add(1)
		return 0, errRejected("The phone number format is invalid.", apperr.FieldError{Field: "voice", Message: "invalid"})
	}
	runHunt(t, f, f.paidSnipe(nil))

	assert.EqualValues(t, prepareRejectCap, calls.Load())
	fin := f.lastFinish()
	assert.Equal(t, domain.SnipeFailed, fin.To)
	assert.EqualValues(t, 222_000, fin.RefundAmount, "everything goes back: this hunt never ran")
	assert.Contains(t, fin.LastError, "phone number")
}

func TestPrepareKeepsTryingThroughTransientFaults(t *testing.T) {
	f := newFx()
	var calls atomic.Int32
	f.registrar.EnsureCustomerFn = func(context.Context, ports.RegistrantContact) (int64, error) {
		if calls.Add(1) <= 3*prepareRejectCap { // far more than the refusal cap
			return 0, errAmbiguous()
		}
		return 501, nil
	}
	f.registerScript()
	runHunt(t, f, f.activeSnipe(f.inWindow))
	assert.Greater(t, int(calls.Load()), prepareRejectCap, "timeouts never count as a refusal")
	assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
}

// The domains row for a win may already exist for the same client (an admin
// added it by hand while the hunt ran): the snipe adopts it instead of
// retrying a conflict for ever.
func TestWinAdoptsADomainRowThatAlreadyBelongsToTheClient(t *testing.T) {
	newConflict := func() *fx {
		f := newFx()
		f.registerScript()
		f.domains.RecordSnipedDomainFn = func(context.Context, ports.RecordSnipedDomainInput) (*domain.Domain, error) {
			return nil, apperr.Conflict("domain hunt.com already exists")
		}
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
			return &domain.DomainSnipe{ID: 1, Status: domain.SnipeActive}, nil
		}
		return f
	}

	t.Run("same client: adopted", func(t *testing.T) {
		f := newConflict()
		f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) {
			return &domain.Domain{ID: 77, ClientID: 7}, nil
		}
		runHunt(t, f, f.activeSnipe(f.inWindow))
		require.Len(t, f.finishes, 1)
		assert.Equal(t, domain.SnipeWon, f.lastFinish().To)
		require.NotNil(t, f.lastFinish().WonDomainID)
		assert.EqualValues(t, 77, *f.lastFinish().WonDomainID)
		assert.Contains(t, f.eventKinds(), eventWon)
		assert.Empty(t, f.alerts)
		assert.Equal(t, []string{tplWon}, f.templateKeys(), "the client still hears they won")
	})

	t.Run("another client's row is a human's problem, not adopted", func(t *testing.T) {
		f := newConflict()
		f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) {
			return &domain.Domain{ID: 77, ClientID: 999}, nil
		}
		sn := f.activeSnipe(f.inWindow)
		soon := f.clock.Now().Add(20 * time.Minute)
		sn.DeadlineAt = &soon
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
		runHunt(t, f, sn)
		assert.Empty(t, f.finishes, "not won, and certainly not refunded")
		assert.Len(t, f.alerts, 1)
	})

	t.Run("lookup failure or a failing adoption falls back to retrying", func(t *testing.T) {
		f := newConflict()
		f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) { return nil, errBoom }
		sup := f.supervisor()
		h := newHunt(f.activeSnipe(nil), func() {})
		assert.False(t, sup.adoptExisting(context.Background(), h, ports.DomainResult{}, apperr.Conflict("x")))
		assert.False(t, sup.adoptExisting(context.Background(), h, ports.DomainResult{}, errBoom), "only a conflict is adoptable")

		f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) {
			return &domain.Domain{ID: 77, ClientID: 7}, nil
		}
		f.snipes.FinishFn = func(context.Context, ports.SnipeFinish) (bool, error) { return false, errBoom }
		assert.False(t, sup.adoptExisting(context.Background(), h, ports.DomainResult{}, apperr.Conflict("x")))
	})
}

func TestShouldAlertIsRateLimitedPerKey(t *testing.T) {
	f := newFx()
	sup := f.supervisor()
	assert.True(t, sup.shouldAlert("k"))
	assert.False(t, sup.shouldAlert("k"), "same key within the interval")
	assert.True(t, sup.shouldAlert("other"), "another key is independent")
	f.clock.Advance(time.Hour)
	assert.True(t, sup.shouldAlert("k"), "and it speaks again after the interval")
}

// The expiry sweep runs beside the Tick, never twice at once, so a slow
// registrar cannot hold up the lease heartbeats.
func TestExpirySweepRunsInTheBackgroundOneAtATime(t *testing.T) {
	f := newFx()
	release := make(chan struct{})
	var passes atomic.Int32
	f.snipes.ListExpirableFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
		passes.Add(1)
		<-release
		return nil, nil
	}
	sup := f.supervisor()
	ctx := testCtx(t)

	done := make(chan struct{})
	go func() { sup.goExpireDue(ctx, t0); close(done) }()
	<-done // goExpireDue itself returns at once even though the pass is blocked
	require.Eventually(t, func() bool { return passes.Load() == 1 }, 2*time.Second, 5*time.Millisecond)

	sup.goExpireDue(ctx, t0) // a second one while the first runs is skipped
	time.Sleep(50 * time.Millisecond)
	assert.EqualValues(t, 1, passes.Load())

	close(release)
	sup.wg.Wait()
	sup.goExpireDue(ctx, t0) // idle again: a new pass may start
	sup.wg.Wait()
	assert.EqualValues(t, 2, passes.Load())
}
