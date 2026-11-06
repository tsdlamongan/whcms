package mocks_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errSnipeMock = errors.New("boom")

// Zero-value mocks return benign defaults; set Fn fields are called through.
func TestMockSnipeRepo(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	t.Run("defaults", func(t *testing.T) {
		m := &mocks.MockSnipeRepo{}
		require.NoError(t, m.Create(ctx, &domain.DomainSnipe{}))
		s, err := m.GetByID(ctx, 1)
		assert.Nil(t, s)
		assert.NoError(t, err)
		l, total, err := m.List(ctx, ports.ListParams{})
		assert.Nil(t, l)
		assert.Zero(t, total)
		assert.NoError(t, err)
		l, total, err = m.ListByClient(ctx, 1, ports.ListParams{})
		assert.Nil(t, l)
		assert.Zero(t, total)
		assert.NoError(t, err)
		n, err := m.CountOpenByClient(ctx, 1)
		assert.Zero(t, n)
		assert.NoError(t, err)
		counts, err := m.CountByStatus(ctx)
		assert.Nil(t, counts)
		assert.NoError(t, err)
		leased, err := m.CountLeased(ctx, now)
		assert.Zero(t, leased)
		assert.NoError(t, err)
		claimed, err := m.Claim(ctx, "w", 1, now, now)
		assert.Nil(t, claimed)
		assert.NoError(t, err)
		ok, err := m.Heartbeat(ctx, ports.SnipeHeartbeat{})
		assert.True(t, ok)
		assert.NoError(t, err)
		assert.NoError(t, m.Release(ctx, 1, "w"))
		assert.NoError(t, m.SetCustomerID(ctx, 1, 2))
		ok, err = m.Finish(ctx, ports.SnipeFinish{})
		assert.True(t, ok)
		assert.NoError(t, err)
		assert.NoError(t, m.MarkRefunded(ctx, 1, now))
		ok, err = m.RecordRefund(ctx, 1, 5, now)
		assert.True(t, ok)
		assert.NoError(t, err)
		assert.NoError(t, m.SetInvoice(ctx, 1, 2, 3, 4))
		ok, err = m.Activate(ctx, 1, now)
		assert.True(t, ok)
		assert.NoError(t, err)
		stale, err := m.ListStalePending(ctx, now, 1)
		assert.Nil(t, stale)
		assert.NoError(t, err)
		exp, err := m.ListExpirable(ctx, now, 1)
		assert.Nil(t, exp)
		assert.NoError(t, err)
		assert.NoError(t, m.AddEvent(ctx, &domain.SnipeEvent{}))
		evs, err := m.ListEvents(ctx, 1, 5)
		assert.Nil(t, evs)
		assert.NoError(t, err)
	})

	t.Run("fn overrides", func(t *testing.T) {
		m := &mocks.MockSnipeRepo{
			CreateFn:  func(context.Context, *domain.DomainSnipe) error { return errSnipeMock },
			GetByIDFn: func(context.Context, int64) (*domain.DomainSnipe, error) { return &domain.DomainSnipe{ID: 7}, nil },
			ListFn: func(context.Context, ports.ListParams) ([]domain.DomainSnipe, int64, error) {
				return []domain.DomainSnipe{{ID: 1}}, 1, nil
			},
			ListByClientFn: func(context.Context, int64, ports.ListParams) ([]domain.DomainSnipe, int64, error) {
				return []domain.DomainSnipe{{ID: 2}}, 1, nil
			},
			CountOpenByClientFn: func(context.Context, int64) (int, error) { return 3, nil },
			CountByStatusFn: func(context.Context) (map[domain.SnipeStatus]int, error) {
				return map[domain.SnipeStatus]int{domain.SnipeActive: 2}, nil
			},
			CountLeasedFn: func(context.Context, time.Time) (int, error) { return 4, nil },
			ClaimFn: func(context.Context, string, int, time.Time, time.Time) ([]domain.DomainSnipe, error) {
				return []domain.DomainSnipe{{ID: 4}}, nil
			},
			HeartbeatFn:     func(context.Context, ports.SnipeHeartbeat) (bool, error) { return false, nil },
			ReleaseFn:       func(context.Context, int64, string) error { return errSnipeMock },
			SetCustomerIDFn: func(context.Context, int64, int64) error { return errSnipeMock },
			FinishFn:        func(context.Context, ports.SnipeFinish) (bool, error) { return false, errSnipeMock },
			MarkRefundedFn:  func(context.Context, int64, time.Time) error { return errSnipeMock },
			RecordRefundFn:  func(context.Context, int64, int64, time.Time) (bool, error) { return false, errSnipeMock },
			SetInvoiceFn:    func(context.Context, int64, int64, int64, int64) error { return errSnipeMock },
			ActivateFn:      func(context.Context, int64, time.Time) (bool, error) { return false, errSnipeMock },
			ListStalePendingFn: func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
				return []domain.DomainSnipe{{ID: 8}}, nil
			},
			ListExpirableFn: func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
				return []domain.DomainSnipe{{ID: 5}}, nil
			},
			AddEventFn: func(context.Context, *domain.SnipeEvent) error { return errSnipeMock },
			ListEventsFn: func(context.Context, int64, int) ([]domain.SnipeEvent, error) {
				return []domain.SnipeEvent{{ID: 6}}, nil
			},
		}
		assert.ErrorIs(t, m.Create(ctx, nil), errSnipeMock)
		s, _ := m.GetByID(ctx, 1)
		assert.EqualValues(t, 7, s.ID)
		l, _, _ := m.List(ctx, ports.ListParams{})
		assert.EqualValues(t, 1, l[0].ID)
		l, _, _ = m.ListByClient(ctx, 1, ports.ListParams{})
		assert.EqualValues(t, 2, l[0].ID)
		n, _ := m.CountOpenByClient(ctx, 1)
		assert.Equal(t, 3, n)
		counts, _ := m.CountByStatus(ctx)
		assert.Equal(t, 2, counts[domain.SnipeActive])
		leased, _ := m.CountLeased(ctx, now)
		assert.Equal(t, 4, leased)
		claimed, _ := m.Claim(ctx, "w", 1, now, now)
		assert.EqualValues(t, 4, claimed[0].ID)
		ok, _ := m.Heartbeat(ctx, ports.SnipeHeartbeat{})
		assert.False(t, ok)
		assert.ErrorIs(t, m.Release(ctx, 1, "w"), errSnipeMock)
		assert.ErrorIs(t, m.SetCustomerID(ctx, 1, 2), errSnipeMock)
		_, err := m.Finish(ctx, ports.SnipeFinish{})
		assert.ErrorIs(t, err, errSnipeMock)
		assert.ErrorIs(t, m.MarkRefunded(ctx, 1, now), errSnipeMock)
		_, err = m.RecordRefund(ctx, 1, 5, now)
		assert.ErrorIs(t, err, errSnipeMock)
		assert.ErrorIs(t, m.SetInvoice(ctx, 1, 2, 3, 4), errSnipeMock)
		_, err = m.Activate(ctx, 1, now)
		assert.ErrorIs(t, err, errSnipeMock)
		stale, _ := m.ListStalePending(ctx, now, 1)
		assert.EqualValues(t, 8, stale[0].ID)
		exp, _ := m.ListExpirable(ctx, now, 1)
		assert.EqualValues(t, 5, exp[0].ID)
		assert.ErrorIs(t, m.AddEvent(ctx, nil), errSnipeMock)
		evs, _ := m.ListEvents(ctx, 1, 5)
		assert.EqualValues(t, 6, evs[0].ID)
	})
}

func TestMockDomainSniper(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults", func(t *testing.T) {
		m := &mocks.MockDomainSniper{}
		id, err := m.EnsureCustomer(ctx, ports.RegistrantContact{})
		assert.Zero(t, id)
		assert.NoError(t, err)
		ok, err := m.IsAvailable(ctx, "a.com")
		assert.False(t, ok)
		assert.NoError(t, err)
		res, err := m.RegisterForCustomer(ctx, "a.com", 1, 1, nil)
		assert.Nil(t, res)
		assert.NoError(t, err)
		info, err := m.SyncDomain(ctx, "a.com")
		assert.Nil(t, info)
		assert.NoError(t, err)
		acct, err := m.AccountInfo(ctx)
		assert.Nil(t, acct)
		assert.NoError(t, err)
	})

	t.Run("fn overrides", func(t *testing.T) {
		m := &mocks.MockDomainSniper{
			EnsureCustomerFn: func(context.Context, ports.RegistrantContact) (int64, error) { return 9, nil },
			IsAvailableFn:    func(context.Context, string) (bool, error) { return true, nil },
			RegisterForCustomerFn: func(context.Context, string, int, int64, []string) (*ports.DomainResult, error) {
				return &ports.DomainResult{Name: "a.com"}, nil
			},
			SyncDomainFn: func(context.Context, string) (*ports.DomainSyncInfo, error) {
				return &ports.DomainSyncInfo{Status: "ok"}, nil
			},
			AccountInfoFn: func(context.Context) (*ports.RegistrarAccountInfo, error) {
				return &ports.RegistrarAccountInfo{Balance: 5}, nil
			},
		}
		id, _ := m.EnsureCustomer(ctx, ports.RegistrantContact{})
		assert.EqualValues(t, 9, id)
		ok, _ := m.IsAvailable(ctx, "a.com")
		assert.True(t, ok)
		res, _ := m.RegisterForCustomer(ctx, "a.com", 1, 1, nil)
		assert.Equal(t, "a.com", res.Name)
		info, _ := m.SyncDomain(ctx, "a.com")
		assert.Equal(t, "ok", info.Status)
		acct, _ := m.AccountInfo(ctx)
		assert.EqualValues(t, 5, acct.Balance)
	})
}

func TestMockSnipeDomainService(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults", func(t *testing.T) {
		m := &mocks.MockSnipeDomainService{}
		q, err := m.QuoteRegistration(ctx, "a.com", 1)
		assert.Nil(t, q)
		assert.NoError(t, err)
		c, err := m.RegistrantContactFor(ctx, 1)
		assert.Equal(t, ports.RegistrantContact{}, c)
		assert.NoError(t, err)
		d, err := m.RecordSnipedDomain(ctx, ports.RecordSnipedDomainInput{})
		assert.Nil(t, d)
		assert.NoError(t, err)
	})

	t.Run("fn overrides", func(t *testing.T) {
		m := &mocks.MockSnipeDomainService{
			QuoteRegistrationFn: func(context.Context, string, int) (*ports.DomainQuote, error) {
				return &ports.DomainQuote{Total: 10}, nil
			},
			RegistrantContactFn: func(context.Context, int64) (ports.RegistrantContact, error) {
				return ports.RegistrantContact{Email: "a@b.c"}, nil
			},
			RecordSnipedDomainFn: func(context.Context, ports.RecordSnipedDomainInput) (*domain.Domain, error) {
				return &domain.Domain{ID: 3}, nil
			},
		}
		q, _ := m.QuoteRegistration(ctx, "a.com", 1)
		assert.EqualValues(t, 10, q.Total)
		c, _ := m.RegistrantContactFor(ctx, 1)
		assert.Equal(t, "a@b.c", c.Email)
		d, _ := m.RecordSnipedDomain(ctx, ports.RecordSnipedDomainInput{})
		assert.EqualValues(t, 3, d.ID)
	})
}

func TestMockSnipePlanRepo(t *testing.T) {
	ctx := context.Background()
	m := &mocks.MockSnipePlanRepo{}
	assert.NoError(t, m.Create(ctx, &domain.SnipePlan{}))
	p, err := m.GetByID(ctx, 1)
	assert.Nil(t, p)
	assert.NoError(t, err)
	assert.NoError(t, m.Update(ctx, &domain.SnipePlan{}))
	l, err := m.List(ctx, true)
	assert.Nil(t, l)
	assert.NoError(t, err)

	m = &mocks.MockSnipePlanRepo{
		CreateFn:  func(context.Context, *domain.SnipePlan) error { return errSnipeMock },
		GetByIDFn: func(context.Context, int64) (*domain.SnipePlan, error) { return &domain.SnipePlan{ID: 4}, nil },
		UpdateFn:  func(context.Context, *domain.SnipePlan) error { return errSnipeMock },
		ListFn: func(_ context.Context, activeOnly bool) ([]domain.SnipePlan, error) {
			assert.True(t, activeOnly)
			return []domain.SnipePlan{{ID: 5}}, nil
		},
	}
	assert.ErrorIs(t, m.Create(ctx, nil), errSnipeMock)
	p, _ = m.GetByID(ctx, 1)
	assert.EqualValues(t, 4, p.ID)
	assert.ErrorIs(t, m.Update(ctx, nil), errSnipeMock)
	l, _ = m.List(ctx, true)
	assert.EqualValues(t, 5, l[0].ID)
}

func TestMockSnipeActivatorAndInvoiceCanceller(t *testing.T) {
	ctx := context.Background()
	assert.NoError(t, (&mocks.MockSnipeActivator{}).ActivateSnipeAfterPayment(ctx, 1))
	a := &mocks.MockSnipeActivator{ActivateSnipeAfterPaymentFn: func(context.Context, int64) error { return errSnipeMock }}
	assert.ErrorIs(t, a.ActivateSnipeAfterPayment(ctx, 1), errSnipeMock)

	inv, err := (&mocks.MockInvoiceCanceller{}).CancelInvoice(ctx, 1, 2)
	assert.Nil(t, inv)
	assert.NoError(t, err)
	c := &mocks.MockInvoiceCanceller{CancelInvoiceFn: func(_ context.Context, actor, id int64) (*domain.Invoice, error) {
		return &domain.Invoice{ID: id}, nil
	}}
	inv, _ = c.CancelInvoice(ctx, 1, 9)
	assert.EqualValues(t, 9, inv.ID)
}
