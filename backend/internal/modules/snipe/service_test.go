package snipe

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

func assertCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, code, apperr.From(err).Code, "unexpected apperr code: %v", err)
}

// createFx is a fixture ready for a successful AdminCreate.
func createFx() (*fx, *domain.DomainSnipe) {
	f := newFx()
	stored := &domain.DomainSnipe{}
	f.domains.QuoteRegistrationFn = func(context.Context, string, int) (*ports.DomainQuote, error) {
		return &ports.DomainQuote{Total: 150000, RenewPerYear: 160000}, nil
	}
	f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) {
		return nil, apperr.NotFound("domain")
	}
	f.snipes.CreateFn = func(_ context.Context, s *domain.DomainSnipe) error {
		s.ID = 5
		*stored = *s
		return nil
	}
	return f, stored
}

func TestAdminCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("starts a free, active hunt with a default 30 day deadline", func(t *testing.T) {
		f, stored := createFx()
		sn, err := f.service().AdminCreate(ctx, 9, AdminCreateRequest{ClientID: 7, Domain: " Hunt.COM "})
		require.NoError(t, err)

		assert.EqualValues(t, 5, sn.ID)
		assert.Equal(t, "hunt.com", stored.DomainName)
		assert.Equal(t, domain.SnipeSourceAdmin, stored.Source)
		assert.Equal(t, domain.SnipeActive, stored.Status)
		assert.Equal(t, domain.SnipePhasePatient, stored.Phase)
		assert.Equal(t, 1, stored.Years)
		assert.EqualValues(t, 160000, stored.RenewPerYear)
		assert.Zero(t, stored.FeeAmount+stored.DomainAmount+stored.RefundableDomain+stored.RefundableFee, "an admin hunt carries no money")
		require.NotNil(t, stored.DeadlineAt)
		assert.Equal(t, t0.AddDate(0, 0, 30), *stored.DeadlineAt)
		assert.Equal(t, &t0, stored.PaidAt)
		assert.Nil(t, stored.InvoiceID)

		require.Len(t, f.audit.Entries, 1)
		assert.Equal(t, "snipe.create", f.audit.Entries[0].Action)
		assert.EqualValues(t, 9, f.audit.Entries[0].ActorUserID)
		assert.Equal(t, []string{eventCreated}, f.eventKinds())
	})

	t.Run("honours an explicit deadline, window, years and nameservers", func(t *testing.T) {
		f, stored := createFx()
		deadline := t0.Add(72 * time.Hour)
		from := t0.Add(24 * time.Hour)
		until := t0.Add(26 * time.Hour)
		_, err := f.service().AdminCreate(ctx, 9, AdminCreateRequest{
			ClientID: 7, Domain: "hunt.com", Years: 2, DeadlineAt: &deadline, HuntFrom: &from, HuntUntil: &until,
			Nameservers: []string{"NS1.Example.net", "ns2.example.net"},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, stored.Years)
		assert.Equal(t, deadline, *stored.DeadlineAt)
		assert.Equal(t, from, *stored.HuntFrom)
		assert.Equal(t, until, *stored.HuntUntil)
		assert.Equal(t, []string{"ns1.example.net", "ns2.example.net"}, stored.Nameservers)
	})

	t.Run("window_days sets the deadline when there is no explicit one", func(t *testing.T) {
		f, stored := createFx()
		_, err := f.service().AdminCreate(ctx, 9, AdminCreateRequest{ClientID: 7, Domain: "hunt.com", WindowDays: 7})
		require.NoError(t, err)
		assert.Equal(t, t0.AddDate(0, 0, 7), *stored.DeadlineAt)
	})

	t.Run("validation failures never reach the repository", func(t *testing.T) {
		past := t0.Add(-time.Minute)
		soon := t0.Add(time.Hour)
		later := t0.Add(2 * time.Hour)
		beyond := t0.AddDate(0, 0, 60)
		tests := []struct {
			name string
			req  AdminCreateRequest
		}{
			{"bad domain", AdminCreateRequest{ClientID: 7, Domain: "not a domain"}},
			{"one nameserver", AdminCreateRequest{ClientID: 7, Domain: "hunt.com", Nameservers: []string{"ns1.example.net"}}},
			{"deadline in the past", AdminCreateRequest{ClientID: 7, Domain: "hunt.com", DeadlineAt: &past}},
			{"hunt_until without hunt_from", AdminCreateRequest{ClientID: 7, Domain: "hunt.com", HuntUntil: &soon}},
			{"hunt_from after hunt_until", AdminCreateRequest{ClientID: 7, Domain: "hunt.com", HuntFrom: &later, HuntUntil: &soon}},
			{"hunt_from after the deadline", AdminCreateRequest{ClientID: 7, Domain: "hunt.com", HuntFrom: &beyond}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f, _ := createFx()
				f.snipes.CreateFn = func(context.Context, *domain.DomainSnipe) error {
					t.Fatal("nothing may be created")
					return nil
				}
				_, err := f.service().AdminCreate(ctx, 9, tt.req)
				assertCode(t, err, apperr.CodeValidation)
			})
		}
	})

	t.Run("an incomplete registrant profile is reported up front", func(t *testing.T) {
		f, _ := createFx()
		f.domains.RegistrantContactFn = func(context.Context, int64) (ports.RegistrantContact, error) {
			return ports.RegistrantContact{}, apperr.Validation("client profile is missing registrant details")
		}
		_, err := f.service().AdminCreate(ctx, 9, AdminCreateRequest{ClientID: 7, Domain: "hunt.com"})
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("a domain already in the system is a conflict", func(t *testing.T) {
		f, _ := createFx()
		f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) {
			return &domain.Domain{ID: 3, Name: "hunt.com"}, nil
		}
		_, err := f.service().AdminCreate(ctx, 9, AdminCreateRequest{ClientID: 7, Domain: "hunt.com"})
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("an unpriced TLD and infrastructure failures propagate", func(t *testing.T) {
		f, _ := createFx()
		f.domains.QuoteRegistrationFn = func(context.Context, string, int) (*ports.DomainQuote, error) {
			return nil, apperr.Validation("no pricing is configured for .xyz")
		}
		_, err := f.service().AdminCreate(ctx, 9, AdminCreateRequest{ClientID: 7, Domain: "hunt.xyz"})
		assertCode(t, err, apperr.CodeValidation)

		f, _ = createFx()
		f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) { return nil, errBoom }
		_, err = f.service().AdminCreate(ctx, 9, AdminCreateRequest{ClientID: 7, Domain: "hunt.com"})
		assert.ErrorIs(t, err, errBoom)

		f, _ = createFx()
		f.snipes.CreateFn = func(context.Context, *domain.DomainSnipe) error {
			return apperr.Conflict("a snipe for hunt.com is already open")
		}
		_, err = f.service().AdminCreate(ctx, 9, AdminCreateRequest{ClientID: 7, Domain: "hunt.com"})
		assertCode(t, err, apperr.CodeConflict)
	})
}

func TestAdminGetAndList(t *testing.T) {
	ctx := context.Background()

	t.Run("get returns the snipe with an empty (not null) trail", func(t *testing.T) {
		f := newFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return &domain.DomainSnipe{ID: 4}, nil }
		d, err := f.service().AdminGet(ctx, 4)
		require.NoError(t, err)
		assert.EqualValues(t, 4, d.Snipe.ID)
		assert.NotNil(t, d.Events)
		assert.Empty(t, d.Events)
	})

	t.Run("get returns the trail and propagates errors", func(t *testing.T) {
		f := newFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return &domain.DomainSnipe{ID: 4}, nil }
		f.snipes.ListEventsFn = func(_ context.Context, id int64, limit int) ([]domain.SnipeEvent, error) {
			assert.EqualValues(t, 4, id)
			assert.Equal(t, detailEventLimit, limit)
			return []domain.SnipeEvent{{Kind: "won"}}, nil
		}
		d, err := f.service().AdminGet(ctx, 4)
		require.NoError(t, err)
		require.Len(t, d.Events, 1)

		f.snipes.ListEventsFn = func(context.Context, int64, int) ([]domain.SnipeEvent, error) { return nil, errBoom }
		_, err = f.service().AdminGet(ctx, 4)
		assert.ErrorIs(t, err, errBoom)

		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return nil, apperr.NotFound("snipe") }
		_, err = f.service().AdminGet(ctx, 4)
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("list passes the filters through", func(t *testing.T) {
		f := newFx()
		f.snipes.ListFn = func(_ context.Context, p ports.ListParams) ([]domain.DomainSnipe, int64, error) {
			assert.Equal(t, "active", p.Status)
			assert.Equal(t, "hunt", p.Search)
			return []domain.DomainSnipe{{ID: 1}}, 1, nil
		}
		items, total, err := f.service().AdminList(ctx, ports.ListParams{Status: "active", Search: "hunt"})
		require.NoError(t, err)
		assert.Len(t, items, 1)
		assert.EqualValues(t, 1, total)
	})
}

func TestRefundFor(t *testing.T) {
	paid := domain.DomainSnipe{Status: domain.SnipeActive, RefundableDomain: 166_500, RefundableFee: 55_500}
	tests := []struct {
		name      string
		snipe     domain.DomainSnipe
		to        domain.SnipeStatus
		refundFee bool
		want      int64
	}{
		{"won consumes the money", paid, domain.SnipeWon, false, 0},
		{"expired returns the domain price", paid, domain.SnipeExpired, false, 166_500},
		{"cancelled returns the domain price", paid, domain.SnipeCancelled, false, 166_500},
		{"cancelled by admin with refund_fee returns everything", paid, domain.SnipeCancelled, true, 222_000},
		{"failed returns everything", paid, domain.SnipeFailed, false, 222_000},
		{"nothing is owed before payment", domain.DomainSnipe{Status: domain.SnipePendingPayment, RefundableDomain: 166_500, RefundableFee: 55_500}, domain.SnipeCancelled, true, 0},
		{"an admin hunt has nothing to give back", domain.DomainSnipe{Status: domain.SnipeActive}, domain.SnipeExpired, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, refundFor(&tt.snipe, tt.to, tt.refundFee))
		})
	}
}

func TestAdminCancel(t *testing.T) {
	ctx := context.Background()
	serve := func(f *fx, sn domain.DomainSnipe) {
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
			cp := sn
			return &cp, nil
		}
	}

	t.Run("cancelling an admin hunt refunds nothing and tells the client", func(t *testing.T) {
		f := newFx()
		serve(f, f.activeSnipe(nil))
		got, err := f.service().AdminCancel(ctx, 9, 1, false)
		require.NoError(t, err)
		assert.EqualValues(t, 1, got.ID)

		fin := f.lastFinish()
		assert.Equal(t, domain.SnipeActive, fin.From)
		assert.Equal(t, domain.SnipeCancelled, fin.To)
		assert.Empty(t, fin.Owner, "an API cancel is not lease-fenced")
		assert.False(t, fin.RequireIdle)
		assert.Zero(t, fin.RefundAmount)
		assert.Empty(t, f.credits)
		assert.Equal(t, []string{tplCancelled}, f.templateKeys())
		assert.EqualValues(t, 70, f.templates[0].UserID)
		assert.Equal(t, "hunt.com", f.templates[0].Data["Domain"])
		assert.NotContains(t, f.templates[0].Data, "RefundAmount")
		require.Len(t, f.audit.Entries, 1)
		assert.Equal(t, "snipe.cancelled", f.audit.Entries[0].Action)
		assert.EqualValues(t, 9, f.audit.Entries[0].ActorUserID)
	})

	t.Run("a paid hunt gets the domain price back, atomically with the status change", func(t *testing.T) {
		f := newFx()
		serve(f, f.paidSnipe(nil))
		var order []string
		f.tx.WithinTxFn = func(ctx context.Context, fn func(context.Context) error) error {
			order = append(order, "begin")
			err := fn(ctx)
			order = append(order, "commit")
			return err
		}
		prevFinish := f.snipes.FinishFn
		f.snipes.FinishFn = func(ctx context.Context, fin ports.SnipeFinish) (bool, error) {
			order = append(order, "finish")
			return prevFinish(ctx, fin)
		}
		prevCredit := f.clients.AdjustCreditFn
		f.clients.AdjustCreditFn = func(ctx context.Context, id, d int64, r string, inv *int64) (int64, error) {
			order = append(order, "credit")
			return prevCredit(ctx, id, d, r, inv)
		}

		_, err := f.service().AdminCancel(ctx, 9, 1, false)
		require.NoError(t, err)

		assert.Equal(t, []string{"begin", "finish", "credit", "commit"}, order)
		assert.EqualValues(t, 166_500, f.lastFinish().RefundAmount)
		require.Len(t, f.credits, 1)
		assert.EqualValues(t, 166_500, f.credits[0].Delta)
		assert.EqualValues(t, 7, f.credits[0].ClientID)
		assert.Contains(t, f.credits[0].Reason, "hunt.com")
		require.NotNil(t, f.credits[0].Invoice)
		assert.EqualValues(t, 55, *f.credits[0].Invoice)
		assert.Equal(t, []int64{1}, f.refunded)
		assert.Equal(t, domain.FormatIDR(166_500), f.templates[0].Data["RefundAmount"])
	})

	t.Run("refund_fee also returns the service fee", func(t *testing.T) {
		f := newFx()
		serve(f, f.paidSnipe(nil))
		_, err := f.service().AdminCancel(ctx, 9, 1, true)
		require.NoError(t, err)
		assert.EqualValues(t, 222_000, f.credits[0].Delta)
	})

	t.Run("an unpaid snipe is cancelled without a refund", func(t *testing.T) {
		f := newFx()
		serve(f, f.paidSnipe(func(s *domain.DomainSnipe) { s.Status = domain.SnipePendingPayment }))
		_, err := f.service().AdminCancel(ctx, 9, 1, true)
		require.NoError(t, err)
		assert.Empty(t, f.credits)
		assert.Equal(t, domain.SnipePendingPayment, f.lastFinish().From)
	})

	t.Run("a finished snipe cannot be cancelled", func(t *testing.T) {
		f := newFx()
		serve(f, f.activeSnipe(func(s *domain.DomainSnipe) { s.Status = domain.SnipeWon }))
		_, err := f.service().AdminCancel(ctx, 9, 1, false)
		assertCode(t, err, apperr.CodeConflict)
		assert.Empty(t, f.finishes)
	})

	t.Run("losing the race to the hunt is a conflict, with no refund and no email", func(t *testing.T) {
		f := newFx()
		serve(f, f.paidSnipe(nil))
		f.snipes.FinishFn = func(context.Context, ports.SnipeFinish) (bool, error) { return false, nil }
		_, err := f.service().AdminCancel(ctx, 9, 1, false)
		assertCode(t, err, apperr.CodeConflict)
		assert.Empty(t, f.credits)
		assert.Empty(t, f.templates)
		assert.Empty(t, f.audit.Entries)
	})

	t.Run("a failed credit rolls the whole closing back", func(t *testing.T) {
		f := newFx()
		serve(f, f.paidSnipe(nil))
		f.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) { return 0, errBoom }
		_, err := f.service().AdminCancel(ctx, 9, 1, false)
		assert.ErrorIs(t, err, errBoom)
		assert.Empty(t, f.templates, "no email for a transition that did not commit")
		assert.Empty(t, f.audit.Entries)
	})

	t.Run("a refund owed to a deleted client closes the snipe and alerts an operator", func(t *testing.T) {
		f := newFx()
		serve(f, f.paidSnipe(nil))
		f.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) {
			return 0, apperr.NotFound("client")
		}
		_, err := f.service().AdminCancel(ctx, 9, 1, false)
		require.NoError(t, err, "the closing must not be rolled back forever")
		assert.Equal(t, domain.SnipeCancelled, f.lastFinish().To)
		assert.Equal(t, []int64{1}, f.refunded, "nothing further to retry")
		assert.Equal(t, []string{"Domain snipe refund could not be credited"}, f.alerts)

		f = newFx()
		serve(f, f.paidSnipe(nil))
		f.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) {
			return 0, apperr.NotFound("client")
		}
		f.notifier.AlertAdminFn = func(context.Context, string, string) error { return errBoom }
		_, err = f.service().AdminCancel(ctx, 9, 1, false)
		require.NoError(t, err, "a failed alert is only logged")
	})

	t.Run("repository failures propagate", func(t *testing.T) {
		f := newFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return nil, apperr.NotFound("snipe") }
		_, err := f.service().AdminCancel(ctx, 9, 1, false)
		assertCode(t, err, apperr.CodeNotFound)

		f = newFx()
		serve(f, f.activeSnipe(nil))
		f.snipes.FinishFn = func(context.Context, ports.SnipeFinish) (bool, error) { return false, errBoom }
		_, err = f.service().AdminCancel(ctx, 9, 1, false)
		assert.ErrorIs(t, err, errBoom)

		f = newFx()
		serve(f, f.paidSnipe(nil))
		f.snipes.MarkRefundedFn = func(context.Context, int64, time.Time) error { return errBoom }
		_, err = f.service().AdminCancel(ctx, 9, 1, false)
		assert.ErrorIs(t, err, errBoom)

		f = newFx()
		serve(f, f.activeSnipe(nil))
		f.snipes.AddEventFn = func(context.Context, *domain.SnipeEvent) error { return errBoom }
		_, err = f.service().AdminCancel(ctx, 9, 1, false)
		assert.ErrorIs(t, err, errBoom)
	})

	t.Run("a mail problem never undoes the cancellation", func(t *testing.T) {
		f := newFx()
		serve(f, f.activeSnipe(nil))
		f.notifier.SendTemplateFn = func(context.Context, int64, string, map[string]any) error { return errBoom }
		_, err := f.service().AdminCancel(ctx, 9, 1, false)
		require.NoError(t, err)

		f = newFx()
		serve(f, f.activeSnipe(nil))
		f.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return nil, errBoom }
		_, err = f.service().AdminCancel(ctx, 9, 1, false)
		require.NoError(t, err)
		assert.Empty(t, f.templates)
	})
}

func TestTemplateFor(t *testing.T) {
	assert.Equal(t, tplWon, templateFor(domain.SnipeWon))
	assert.Equal(t, tplExpired, templateFor(domain.SnipeExpired))
	assert.Equal(t, tplFailed, templateFor(domain.SnipeFailed))
	assert.Equal(t, tplCancelled, templateFor(domain.SnipeCancelled))
	assert.Empty(t, templateFor(domain.SnipeActive))

	f := newFx()
	f.service().notifyEnded(context.Background(), &domain.DomainSnipe{ClientID: 7}, domain.SnipeActive, 0)
	assert.Empty(t, f.templates, "a non-terminal status sends nothing")
}

func TestTrimAndTruncate(t *testing.T) {
	assert.Equal(t, "ab", truncate("  ab  ", 10))
	assert.Equal(t, "abc", truncate("abcdef", 3))
	assert.Equal(t, "héé", truncate("hééllo", 3), "never splits a multi-byte rune")
	assert.Equal(t, "short", truncate("short", 100))
	assert.Equal(t, "cancelled", trailMessage(domain.SnipeCancelled, "", 0))
	assert.Equal(t, "boom (refunded "+domain.FormatIDR(1000)+")", trailMessage(domain.SnipeFailed, "boom", 1000))
}

func TestStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("collects switches, counts and the reseller balance", func(t *testing.T) {
		f := newFx()
		f.settings.GetBoolFn = func(context.Context, string, bool) (bool, error) { return true, nil }
		f.settings.GetStringFn = func(_ context.Context, key, def string) (string, error) {
			switch key {
			case SettingPauseReason:
				return "balance below floor", nil
			case SettingPausedAt:
				return "2026-10-24T12:00:00Z", nil
			}
			return def, nil
		}
		f.snipes.CountByStatusFn = func(context.Context) (map[domain.SnipeStatus]int, error) {
			return map[domain.SnipeStatus]int{domain.SnipeActive: 3, domain.SnipeWon: 1}, nil
		}
		f.snipes.CountLeasedFn = func(_ context.Context, now time.Time) (int, error) {
			assert.Equal(t, t0, now)
			return 2, nil
		}
		st, err := f.service().Status(ctx)
		require.NoError(t, err)
		assert.True(t, st.Enabled)
		assert.Equal(t, "balance below floor", st.PauseReason)
		assert.Equal(t, "2026-10-24T12:00:00Z", st.PausedAt)
		assert.Equal(t, map[string]int{"active": 3, "won": 1}, st.Counts)
		assert.Equal(t, 2, st.Hunting)
		require.NotNil(t, st.ResellerBalance)
		assert.EqualValues(t, 5_000_000, *st.ResellerBalance)
		assert.Empty(t, st.ResellerError)
	})

	t.Run("a registrar failure is reported, not fatal", func(t *testing.T) {
		f := newFx()
		f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, errUnauthorized() }
		st, err := f.service().Status(ctx)
		require.NoError(t, err)
		assert.Nil(t, st.ResellerBalance)
		assert.Contains(t, st.ResellerError, "Unauthenticated")
		assert.Empty(t, st.PausedAt, "no pause, no timestamp")
	})

	t.Run("no account info and no error leaves the balance unknown", func(t *testing.T) {
		f := newFx()
		f.registrar.AccountInfoFn = func(context.Context) (*ports.RegistrarAccountInfo, error) { return nil, nil }
		st, err := f.service().Status(ctx)
		require.NoError(t, err)
		assert.Nil(t, st.ResellerBalance)
		assert.Empty(t, st.ResellerError)
	})

	t.Run("database failures fail the overview", func(t *testing.T) {
		f := newFx()
		f.settings.GetStringFn = func(context.Context, string, string) (string, error) { return "", errBoom }
		_, err := f.service().Status(ctx)
		assertCode(t, err, apperr.CodeInternal)

		f = newFx()
		f.snipes.CountByStatusFn = func(context.Context) (map[domain.SnipeStatus]int, error) { return nil, errBoom }
		_, err = f.service().Status(ctx)
		assert.ErrorIs(t, err, errBoom)

		f = newFx()
		f.snipes.CountLeasedFn = func(context.Context, time.Time) (int, error) { return 0, errBoom }
		_, err = f.service().Status(ctx)
		assert.ErrorIs(t, err, errBoom)
	})
}

func TestDepsDefaults(t *testing.T) {
	d := Deps{}.withDefaults()
	assert.NotNil(t, d.Log)
	assert.NotNil(t, d.Sleep)
	assert.NotNil(t, d.Jitter)
	assert.NotEmpty(t, d.Owner)
	assert.NotEqual(t, newOwnerID(), newOwnerID(), "owner ids are unique per call")

	assert.Zero(t, randomJitter(0))
	for i := 0; i < 50; i++ {
		assert.Less(t, randomJitter(100*time.Millisecond), 100*time.Millisecond)
	}
}

func TestRealSleep(t *testing.T) {
	assert.NoError(t, realSleep(context.Background(), 0))
	assert.NoError(t, realSleep(context.Background(), time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, realSleep(ctx, time.Hour), context.Canceled)
	assert.ErrorIs(t, realSleep(ctx, 0), context.Canceled)
}
