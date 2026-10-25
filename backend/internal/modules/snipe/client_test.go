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

var verifiedAt = t0.Add(-24 * time.Hour)

// clientFx is a fixture ready for a successful client Create: a verified
// client with a complete profile, an active 30-day plan (fee 50,000), a priced
// TLD (150,000 / renew 160,000), nothing open, the name taken, and an invoice
// service that records what it was asked to bill.
type clientFx struct {
	*fx
	created     *domain.DomainSnipe
	invoiceIn   *ports.CreateInvoiceInput
	setInvoice  [4]int64
	invoiceCall int
}

func newClientFx() *clientFx {
	f := &clientFx{fx: newFx(), created: &domain.DomainSnipe{}, invoiceIn: &ports.CreateInvoiceInput{}}
	f.clients.GetByIDFn = func(_ context.Context, id int64) (*domain.Client, error) {
		return &domain.Client{
			ID: id, UserID: 70, Address1: "Jl. A 1", City: "Lamongan", State: "Jawa Timur", Postcode: "62211", Phone: "+62.81",
		}, nil
	}
	f.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) {
		return &domain.User{ID: 70, EmailVerifiedAt: &verifiedAt}, nil
	}
	f.plans.GetByIDFn = func(_ context.Context, id int64) (*domain.SnipePlan, error) {
		return &domain.SnipePlan{ID: id, Name: "Standard", WindowDays: 30, Fee: 50_000, Active: true}, nil
	}
	f.plans.ListFn = func(context.Context, bool) ([]domain.SnipePlan, error) {
		return []domain.SnipePlan{{ID: 3, Name: "Standard", WindowDays: 30, Fee: 50_000, Active: true}}, nil
	}
	f.domains.QuoteRegistrationFn = func(context.Context, string, int) (*ports.DomainQuote, error) {
		return &ports.DomainQuote{Total: 150_000, RenewPerYear: 160_000}, nil
	}
	f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) { return nil, apperr.NotFound("domain") }
	f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) { return false, nil }
	f.snipes.CountOpenByClientFn = func(context.Context, int64) (int, error) { return 0, nil }
	f.snipes.CreateFn = func(_ context.Context, s *domain.DomainSnipe) error {
		s.ID = 5
		s.CreatedAt = t0
		*f.created = *s
		return nil
	}
	f.invoices.CreateInvoiceFn = func(_ context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
		f.invoiceCall++
		*f.invoiceIn = in
		var sub int64
		for _, it := range in.Items {
			sub += it.Amount
		}
		return &domain.Invoice{ID: 900, Subtotal: sub, Total: sub}, nil
	}
	f.snipes.SetInvoiceFn = func(_ context.Context, id, inv, rd, rf int64) error {
		f.setInvoice = [4]int64{id, inv, rd, rf}
		return nil
	}
	return f
}

func (f *clientFx) who() ClientIdentity { return ClientIdentity{UserID: 70, ClientID: 7} }

func (f *clientFx) taxed(rate int, inclusive bool) {
	f.cfgOverride[settingTaxRate] = rate
	f.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
		switch key {
		case settingTaxEnabled:
			return true, nil
		case settingTaxInclusive:
			return inclusive, nil
		}
		return def, nil
	}
	// the invoice service applies the same tax rules billing does
	f.invoices.CreateInvoiceFn = func(_ context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
		*f.invoiceIn = in
		var sub int64
		for _, it := range in.Items {
			sub += it.Amount
		}
		res := domain.CalcTax(sub, float64(rate), inclusive)
		total := sub
		if !inclusive {
			total += res.Tax
		}
		return &domain.Invoice{ID: 900, Subtotal: sub, TaxTotal: res.Tax, Total: total}, nil
	}
}

func TestRefundSplit(t *testing.T) {
	tests := []struct {
		name                 string
		total, fee, subtotal int64
		wantDomain, wantFee  int64
	}{
		{"no tax", 200_000, 50_000, 200_000, 150_000, 50_000},
		{"tax exclusive 11%", 222_000, 50_000, 200_000, 166_500, 55_500},
		{"a free plan refunds everything", 150_000, 0, 150_000, 150_000, 0},
		{"rounding goes to the refundable side", 100, 1, 3, 67, 33},
		{"degenerate zero subtotal", 0, 0, 0, 0, 0},
		{"zero subtotal with a fee never divides by zero", 10, 5, 0, 10, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, f := refundSplit(tt.total, tt.fee, tt.subtotal)
			assert.Equal(t, tt.wantDomain, d)
			assert.Equal(t, tt.wantFee, f)
			assert.Equal(t, tt.total, d+f, "the two halves always add up to the total")
		})
	}
}

func TestQuote(t *testing.T) {
	ctx := context.Background()

	t.Run("a plain breakdown", func(t *testing.T) {
		f := newClientFx()
		q, err := f.service().Quote(ctx, QuoteRequest{Domain: " Hunt.COM ", PlanID: 3, Years: 2})
		require.NoError(t, err)
		assert.Equal(t, &Quote{
			Domain: "hunt.com", Years: 2, PlanID: 3, PlanName: "Standard", WindowDays: 30,
			Fee: 50_000, DomainPrice: 150_000, Subtotal: 200_000, Total: 200_000,
			RefundableIfLost: 150_000, RenewPerYear: 160_000,
		}, q)
	})

	t.Run("a success fee is part of the refundable side", func(t *testing.T) {
		f := newClientFx()
		f.plans.GetByIDFn = func(context.Context, int64) (*domain.SnipePlan, error) {
			return &domain.SnipePlan{ID: 3, Name: "Pro", WindowDays: 90, Fee: 50_000, SuccessFee: 25_000, Active: true}, nil
		}
		q, err := f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 3})
		require.NoError(t, err)
		assert.EqualValues(t, 225_000, q.Subtotal)
		assert.EqualValues(t, 25_000, q.SuccessFee)
		assert.EqualValues(t, 175_000, q.RefundableIfLost, "domain price + success fee")
		assert.Equal(t, 1, q.Years, "years default to one")
	})

	t.Run("tax exclusive and inclusive follow the billing settings", func(t *testing.T) {
		f := newClientFx()
		f.taxed(11, false)
		q, err := f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 3})
		require.NoError(t, err)
		assert.EqualValues(t, 22_000, q.Tax)
		assert.EqualValues(t, 222_000, q.Total)
		assert.EqualValues(t, 166_500, q.RefundableIfLost)
		assert.False(t, q.TaxInclusive)
		assert.EqualValues(t, 11, q.TaxRate)

		f = newClientFx()
		f.taxed(11, true)
		q, err = f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 3})
		require.NoError(t, err)
		assert.EqualValues(t, 200_000, q.Total, "inclusive tax does not change the total")
		assert.True(t, q.TaxInclusive)
		assert.Positive(t, q.Tax)
	})

	t.Run("a zero tax rate adds nothing", func(t *testing.T) {
		f := newClientFx()
		f.taxed(0, false)
		q, err := f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 3})
		require.NoError(t, err)
		assert.Zero(t, q.Tax)
		assert.EqualValues(t, 200_000, q.Total)
	})

	t.Run("everything that cannot be sold is rejected", func(t *testing.T) {
		f := newClientFx()
		_, err := f.service().Quote(ctx, QuoteRequest{Domain: "not a domain", PlanID: 3})
		assertCode(t, err, apperr.CodeValidation)

		f = newClientFx()
		f.plans.GetByIDFn = func(context.Context, int64) (*domain.SnipePlan, error) { return nil, apperr.NotFound("snipe plan") }
		_, err = f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 99})
		assertCode(t, err, apperr.CodeValidation)

		f = newClientFx()
		f.plans.GetByIDFn = func(context.Context, int64) (*domain.SnipePlan, error) {
			return &domain.SnipePlan{ID: 3, Active: false}, nil
		}
		_, err = f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 3})
		assertCode(t, err, apperr.CodeValidation)

		f = newClientFx()
		f.plans.GetByIDFn = func(context.Context, int64) (*domain.SnipePlan, error) { return nil, nil }
		_, err = f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 3})
		assertCode(t, err, apperr.CodeValidation)

		f = newClientFx()
		f.domains.QuoteRegistrationFn = func(context.Context, string, int) (*ports.DomainQuote, error) {
			return nil, apperr.Validation("no pricing is configured for .xyz")
		}
		_, err = f.service().Quote(ctx, QuoteRequest{Domain: "hunt.xyz", PlanID: 3})
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("infrastructure failures are not disguised as validation", func(t *testing.T) {
		f := newClientFx()
		f.plans.GetByIDFn = func(context.Context, int64) (*domain.SnipePlan, error) { return nil, errBoom }
		_, err := f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 3})
		assert.ErrorIs(t, err, errBoom)

		f = newClientFx()
		f.settings.GetBoolFn = func(context.Context, string, bool) (bool, error) { return false, errBoom }
		_, err = f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 3})
		assertCode(t, err, apperr.CodeInternal)

		f = newClientFx()
		f.taxed(11, false)
		f.settings.GetIntFn = func(context.Context, string, int) (int, error) { return 0, errBoom }
		_, err = f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 3})
		assertCode(t, err, apperr.CodeInternal)

		f = newClientFx()
		f.taxed(11, false)
		f.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
			if key == settingTaxInclusive {
				return false, errBoom
			}
			return true, nil
		}
		_, err = f.service().Quote(ctx, QuoteRequest{Domain: "hunt.com", PlanID: 3})
		assertCode(t, err, apperr.CodeInternal)
	})
}

func TestListActivePlans(t *testing.T) {
	f := newClientFx()
	f.plans.ListFn = func(_ context.Context, activeOnly bool) ([]domain.SnipePlan, error) {
		assert.True(t, activeOnly)
		return []domain.SnipePlan{{ID: 1, Name: "A", WindowDays: 7, Fee: 10_000, SuccessFee: 5_000}}, nil
	}
	got, err := f.service().ListActivePlans(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []PlanView{{ID: 1, Name: "A", WindowDays: 7, Fee: 10_000, SuccessFee: 5_000}}, got)

	f.plans.ListFn = func(context.Context, bool) ([]domain.SnipePlan, error) { return nil, errBoom }
	_, err = f.service().ListActivePlans(context.Background())
	assert.ErrorIs(t, err, errBoom)
}

func TestClientCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("creates the snipe and one invoice, the registration line carries the activation hook", func(t *testing.T) {
		f := newClientFx()
		got, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: " Hunt.COM ", PlanID: 3, Years: 2})
		require.NoError(t, err)

		sn := f.created
		assert.Equal(t, "hunt.com", sn.DomainName)
		assert.Equal(t, domain.SnipeSourceClient, sn.Source)
		assert.Equal(t, domain.SnipePendingPayment, sn.Status)
		assert.EqualValues(t, 7, sn.ClientID)
		assert.Equal(t, 2, sn.Years)
		require.NotNil(t, sn.PlanID)
		assert.EqualValues(t, 3, *sn.PlanID)
		assert.EqualValues(t, 50_000, sn.FeeAmount)
		assert.EqualValues(t, 150_000, sn.DomainAmount)
		assert.EqualValues(t, 160_000, sn.RenewPerYear)
		require.NotNil(t, sn.DeadlineAt)
		assert.Equal(t, t0.AddDate(0, 0, 30), *sn.DeadlineAt, "provisional: Activate restarts it from the payment")

		in := f.invoiceIn
		assert.EqualValues(t, 7, in.ClientID)
		assert.Equal(t, t0.Add(24*time.Hour), in.DueDate, "snipe.invoice_due_hours")
		require.Len(t, in.Items, 2)
		assert.EqualValues(t, 50_000, in.Items[0].Amount)
		assert.Equal(t, domain.RelatedManual, in.Items[0].RelatedType)
		assert.True(t, in.Items[0].Taxed)
		assert.EqualValues(t, 150_000, in.Items[1].Amount)
		assert.Equal(t, domain.RelatedDomainSnipe, in.Items[1].RelatedType)
		assert.EqualValues(t, 5, in.Items[1].RelatedID, "the hook line points at the snipe")

		assert.Equal(t, [4]int64{5, 900, 150_000, 50_000}, f.setInvoice)
		require.NotNil(t, got.InvoiceID)
		assert.EqualValues(t, 900, *got.InvoiceID)
		assert.EqualValues(t, 150_000, got.RefundableDomain)
		assert.EqualValues(t, 50_000, got.RefundableFee)
		assert.Equal(t, "Standard", got.PlanName)
		assert.Equal(t, domain.SnipePendingPayment, got.Status)
		assert.Empty(t, got.Phase, "a snipe that is not hunting has no phase")

		require.Len(t, f.audit.Entries, 1)
		assert.Equal(t, "snipe.create", f.audit.Entries[0].Action)
		assert.EqualValues(t, 70, f.audit.Entries[0].ActorUserID)
		assert.Equal(t, []string{eventCreated}, f.eventKinds())
	})

	t.Run("a free plan has no fee line, so the whole total is refundable", func(t *testing.T) {
		f := newClientFx()
		f.plans.GetByIDFn = func(context.Context, int64) (*domain.SnipePlan, error) {
			return &domain.SnipePlan{ID: 3, Name: "Free", WindowDays: 7, Active: true}, nil
		}
		_, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		require.NoError(t, err)
		require.Len(t, f.invoiceIn.Items, 1)
		assert.Equal(t, domain.RelatedDomainSnipe, f.invoiceIn.Items[0].RelatedType)
		assert.Equal(t, [4]int64{5, 900, 150_000, 0}, f.setInvoice)
		assert.Equal(t, t0.AddDate(0, 0, 7), *f.created.DeadlineAt)
	})

	t.Run("a success fee gets its own refundable line", func(t *testing.T) {
		f := newClientFx()
		f.plans.GetByIDFn = func(context.Context, int64) (*domain.SnipePlan, error) {
			return &domain.SnipePlan{ID: 3, Name: "Pro", WindowDays: 30, Fee: 50_000, SuccessFee: 25_000, Active: true}, nil
		}
		_, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		require.NoError(t, err)
		require.Len(t, f.invoiceIn.Items, 3)
		assert.EqualValues(t, 25_000, f.invoiceIn.Items[2].Amount)
		assert.Equal(t, domain.RelatedManual, f.invoiceIn.Items[2].RelatedType)
		assert.EqualValues(t, 175_000, f.created.DomainAmount, "registration price + success fee")
		assert.Equal(t, [4]int64{5, 900, 175_000, 50_000}, f.setInvoice)
	})

	t.Run("the refundable split follows the invoice's real total, tax included", func(t *testing.T) {
		f := newClientFx()
		f.taxed(11, false)
		got, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		require.NoError(t, err)
		assert.Equal(t, [4]int64{5, 900, 166_500, 55_500}, f.setInvoice)
		assert.EqualValues(t, 222_000, got.RefundableDomain+got.RefundableFee, "split adds up to invoice.total")
	})

	t.Run("an optional drop window and nameservers are kept", func(t *testing.T) {
		f := newClientFx()
		from, until := t0.Add(time.Hour), t0.Add(2*time.Hour)
		_, err := f.service().Create(ctx, f.who(), CreateRequest{
			Domain: "hunt.com", PlanID: 3, HuntFrom: &from, HuntUntil: &until,
			Nameservers: []string{"NS1.Example.net", "ns2.example.net"},
		})
		require.NoError(t, err)
		assert.Equal(t, from, *f.created.HuntFrom)
		assert.Equal(t, until, *f.created.HuntUntil)
		assert.Equal(t, []string{"ns1.example.net", "ns2.example.net"}, f.created.Nameservers)
	})

	t.Run("a drop window of exactly the cap is accepted", func(t *testing.T) {
		f := newClientFx()
		from := t0.Add(time.Hour)
		until := from.Add(maxClientBurstWindow)
		_, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3, HuntFrom: &from, HuntUntil: &until})
		require.NoError(t, err)
	})

	t.Run("an unverified email is refused unless verification is switched off", func(t *testing.T) {
		f := newClientFx()
		f.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) { return &domain.User{ID: 70}, nil }
		_, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		assertCode(t, err, apperr.CodeForbidden)
		assert.Zero(t, f.invoiceCall)

		f.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
			if key == settingRequireEmailVerify {
				return false, nil
			}
			return def, nil
		}
		_, err = f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		require.NoError(t, err)
	})

	t.Run("a disabled sniper sells nothing", func(t *testing.T) {
		f := newClientFx()
		f.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
			if key == SettingEnabled {
				return false, nil
			}
			return def, nil
		}
		_, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		assertCode(t, err, apperr.CodeForbidden)
		assert.Zero(t, f.invoiceCall)
	})

	t.Run("rejections happen before anything is written", func(t *testing.T) {
		from := t0.Add(time.Hour)
		beyond := t0.AddDate(0, 0, 60)
		past := t0.Add(-time.Minute)
		tooLong := from.Add(maxClientBurstWindow + time.Minute)
		tests := []struct {
			name string
			mut  func(*clientFx)
			req  CreateRequest
			code apperr.Code
		}{
			{"bad domain", nil, CreateRequest{Domain: "not a domain", PlanID: 3}, apperr.CodeValidation},
			{"one nameserver", nil, CreateRequest{Domain: "hunt.com", PlanID: 3, Nameservers: []string{"a.example.net"}}, apperr.CodeValidation},
			{"window already over", nil, CreateRequest{Domain: "hunt.com", PlanID: 3, HuntFrom: &past, HuntUntil: &past}, apperr.CodeValidation},
			{"window starts after the plan runs out", nil, CreateRequest{Domain: "hunt.com", PlanID: 3, HuntFrom: &beyond}, apperr.CodeValidation},
			{"until without from", nil, CreateRequest{Domain: "hunt.com", PlanID: 3, HuntUntil: &from}, apperr.CodeValidation},
			{"from without until would burst until the plan ends", nil, CreateRequest{Domain: "hunt.com", PlanID: 3, HuntFrom: &from}, apperr.CodeValidation},
			{"a drop window longer than the cap", nil, CreateRequest{Domain: "hunt.com", PlanID: 3, HuntFrom: &from, HuntUntil: &tooLong}, apperr.CodeValidation},
			{"incomplete profile", func(f *clientFx) {
				f.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return &domain.Client{ID: 7, UserID: 70}, nil }
			}, CreateRequest{Domain: "hunt.com", PlanID: 3}, apperr.CodeValidation},
			{"the sniper is fully booked", func(f *clientFx) {
				f.cfgOverride[SettingMaxActiveGlobal] = 3
				f.snipes.CountByStatusFn = func(context.Context) (map[domain.SnipeStatus]int, error) {
					return map[domain.SnipeStatus]int{domain.SnipeActive: 2, domain.SnipePendingPayment: 1}, nil
				}
			}, CreateRequest{Domain: "hunt.com", PlanID: 3}, apperr.CodeConflict},
			{"too many open snipes", func(f *clientFx) {
				f.snipes.CountOpenByClientFn = func(context.Context, int64) (int, error) { return 5, nil }
			}, CreateRequest{Domain: "hunt.com", PlanID: 3}, apperr.CodeConflict},
			{"already registered here", func(f *clientFx) {
				f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) { return &domain.Domain{ID: 1}, nil }
			}, CreateRequest{Domain: "hunt.com", PlanID: 3}, apperr.CodeConflict},
			{"registrable right now - no hunt needed", func(f *clientFx) {
				f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) { return true, nil }
			}, CreateRequest{Domain: "hunt.com", PlanID: 3}, apperr.CodeValidation},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := newClientFx()
				if tt.mut != nil {
					tt.mut(f)
				}
				f.snipes.CreateFn = func(context.Context, *domain.DomainSnipe) error {
					t.Fatal("nothing may be written")
					return nil
				}
				_, err := f.service().Create(ctx, f.who(), tt.req)
				assertCode(t, err, tt.code)
				assert.Zero(t, f.invoiceCall)
			})
		}
	})

	t.Run("infrastructure failures propagate", func(t *testing.T) {
		for name, mut := range map[string]func(*clientFx){
			"client lookup": func(f *clientFx) {
				f.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return nil, errBoom }
			},
			"user lookup": func(f *clientFx) {
				f.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) { return nil, errBoom }
			},
			"open count": func(f *clientFx) {
				f.snipes.CountOpenByClientFn = func(context.Context, int64) (int, error) { return 0, errBoom }
			},
			"registered look": func(f *clientFx) {
				f.domainRepo.GetByNameFn = func(context.Context, string) (*domain.Domain, error) { return nil, errBoom }
			},
			"availability": func(f *clientFx) {
				f.registrar.IsAvailableFn = func(context.Context, string) (bool, error) { return false, errBoom }
			},
		} {
			t.Run(name, func(t *testing.T) {
				f := newClientFx()
				mut(f)
				_, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
				assert.ErrorIs(t, err, errBoom)
				assert.Zero(t, f.invoiceCall)
			})
		}

		f := newClientFx()
		f.settings.GetBoolFn = func(_ context.Context, key string, def bool) (bool, error) {
			if key == settingRequireEmailVerify {
				return false, errBoom
			}
			return def, nil
		}
		_, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		assertCode(t, err, apperr.CodeInternal)

		f = newClientFx()
		f.cfgOverride[SettingMaxActiveGlobal] = 20
		f.settings.GetStringFn = func(context.Context, string, string) (string, error) { return "", errBoom }
		_, err = f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		assertCode(t, err, apperr.CodeInternal)
	})

	t.Run("an unreadable capacity tally refuses the sale", func(t *testing.T) {
		f := newClientFx()
		f.snipes.CountByStatusFn = func(context.Context) (map[domain.SnipeStatus]int, error) { return nil, errBoom }
		_, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		require.Error(t, err)
		assert.Zero(t, f.invoiceCall)
	})

	t.Run("a failure inside the transaction leaves nothing half-done", func(t *testing.T) {
		f := newClientFx()
		f.snipes.CreateFn = func(context.Context, *domain.DomainSnipe) error {
			return apperr.Conflict("a snipe for hunt.com is already open")
		}
		_, err := f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		assertCode(t, err, apperr.CodeConflict)
		assert.Zero(t, f.invoiceCall, "no invoice for a snipe that could not be created")
		assert.Empty(t, f.audit.Entries)

		f = newClientFx()
		f.invoices.CreateInvoiceFn = func(context.Context, ports.CreateInvoiceInput) (*domain.Invoice, error) { return nil, errBoom }
		f.snipes.SetInvoiceFn = func(context.Context, int64, int64, int64, int64) error {
			t.Fatal("no link without an invoice")
			return nil
		}
		_, err = f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		assert.ErrorIs(t, err, errBoom)
		assert.Empty(t, f.audit.Entries)

		f = newClientFx()
		f.snipes.SetInvoiceFn = func(context.Context, int64, int64, int64, int64) error { return errBoom }
		_, err = f.service().Create(ctx, f.who(), CreateRequest{Domain: "hunt.com", PlanID: 3})
		assert.ErrorIs(t, err, errBoom)
		assert.Empty(t, f.audit.Entries)
	})
}

func TestActivateSnipeAfterPayment(t *testing.T) {
	ctx := context.Background()
	deadline := t0.AddDate(0, 0, 30)

	t.Run("a paid snipe becomes active, the client is told", func(t *testing.T) {
		f := newClientFx()
		var paidAt time.Time
		f.snipes.ActivateFn = func(_ context.Context, id int64, at time.Time) (bool, error) {
			paidAt = at
			return true, nil
		}
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
			return &domain.DomainSnipe{ID: 5, ClientID: 7, DomainName: "hunt.com", Status: domain.SnipeActive, DeadlineAt: &deadline}, nil
		}
		require.NoError(t, f.service().ActivateSnipeAfterPayment(ctx, 5))

		assert.Equal(t, t0, paidAt)
		assert.Equal(t, []string{eventPaid}, f.eventKinds())
		require.Len(t, f.audit.Entries, 1)
		assert.Equal(t, "snipe.activate", f.audit.Entries[0].Action)
		assert.Equal(t, []string{tplActive}, f.templateKeys())
		assert.Equal(t, "hunt.com", f.templates[0].Data["Domain"])
		assert.Equal(t, deadline.Format(dateFormat), f.templates[0].Data["DeadlineAt"])
		assert.Empty(t, f.credits)
	})

	t.Run("a replay changes nothing", func(t *testing.T) {
		for _, st := range []domain.SnipeStatus{domain.SnipeActive, domain.SnipeWon, domain.SnipePendingPayment} {
			f := newClientFx()
			f.snipes.ActivateFn = func(context.Context, int64, time.Time) (bool, error) { return false, nil }
			f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
				return &domain.DomainSnipe{ID: 5, Status: st, RefundableDomain: 1, InvoiceID: ptr(900)}, nil
			}
			require.NoError(t, f.service().ActivateSnipeAfterPayment(ctx, 5), st)
			assert.Empty(t, f.templates, st)
			assert.Empty(t, f.credits, st)
			assert.Empty(t, f.events, st)
		}
	})

	t.Run("a payment that arrives after the hunt ended is credited straight back", func(t *testing.T) {
		for _, st := range []domain.SnipeStatus{domain.SnipeCancelled, domain.SnipeFailed, domain.SnipeExpired} {
			f := newClientFx()
			f.snipes.ActivateFn = func(context.Context, int64, time.Time) (bool, error) { return false, nil }
			f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
				return &domain.DomainSnipe{
					ID: 5, ClientID: 7, DomainName: "hunt.com", Status: st, InvoiceID: ptr(900),
					RefundableDomain: 166_500, RefundableFee: 55_500,
				}, nil
			}
			var recorded int64
			f.snipes.RecordRefundFn = func(_ context.Context, id, amount int64, at time.Time) (bool, error) {
				recorded = amount
				assert.Equal(t, t0, at)
				return true, nil
			}
			require.NoError(t, f.service().ActivateSnipeAfterPayment(ctx, 5), st)

			assert.EqualValues(t, 222_000, recorded, "everything that was paid, fee included")
			require.Len(t, f.credits, 1, st)
			assert.EqualValues(t, 222_000, f.credits[0].Delta)
			assert.EqualValues(t, 7, f.credits[0].ClientID)
			assert.EqualValues(t, 900, *f.credits[0].Invoice)
			assert.Equal(t, []string{"late_payment_refund"}, f.eventKinds())
			assert.Equal(t, []string{tplCancelled}, f.templateKeys())
			assert.Equal(t, domain.FormatIDR(222_000), f.templates[0].Data["RefundAmount"])
			require.Len(t, f.audit.Entries, 1)
			assert.Equal(t, "snipe.late_payment_refund", f.audit.Entries[0].Action)
		}
	})

	t.Run("a replayed late payment is not refunded twice", func(t *testing.T) {
		f := newClientFx()
		f.snipes.ActivateFn = func(context.Context, int64, time.Time) (bool, error) { return false, nil }
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
			return &domain.DomainSnipe{ID: 5, Status: domain.SnipeCancelled, InvoiceID: ptr(900), RefundableDomain: 100}, nil
		}
		f.snipes.RecordRefundFn = func(context.Context, int64, int64, time.Time) (bool, error) { return false, nil }
		require.NoError(t, f.service().ActivateSnipeAfterPayment(ctx, 5))
		assert.Empty(t, f.credits)
		assert.Empty(t, f.templates)
	})

	t.Run("nothing to give back, nothing recorded", func(t *testing.T) {
		for name, sn := range map[string]domain.DomainSnipe{
			"no amounts": {ID: 5, Status: domain.SnipeCancelled, InvoiceID: ptr(900)},
			"no invoice": {ID: 5, Status: domain.SnipeCancelled, RefundableDomain: 100},
		} {
			f := newClientFx()
			f.snipes.ActivateFn = func(context.Context, int64, time.Time) (bool, error) { return false, nil }
			f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { cp := sn; return &cp, nil }
			f.snipes.RecordRefundFn = func(context.Context, int64, int64, time.Time) (bool, error) {
				t.Fatal("must not record")
				return false, nil
			}
			require.NoError(t, f.service().ActivateSnipeAfterPayment(ctx, 5), name)
		}
	})

	t.Run("failures abort the payment transaction", func(t *testing.T) {
		f := newClientFx()
		f.snipes.ActivateFn = func(context.Context, int64, time.Time) (bool, error) { return false, errBoom }
		assert.ErrorIs(t, f.service().ActivateSnipeAfterPayment(ctx, 5), errBoom)

		f = newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return nil, errBoom }
		assert.ErrorIs(t, f.service().ActivateSnipeAfterPayment(ctx, 5), errBoom)

		late := func() *clientFx {
			f := newClientFx()
			f.snipes.ActivateFn = func(context.Context, int64, time.Time) (bool, error) { return false, nil }
			f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
				return &domain.DomainSnipe{ID: 5, Status: domain.SnipeCancelled, InvoiceID: ptr(900), RefundableDomain: 100}, nil
			}
			return f
		}
		f = late()
		f.snipes.RecordRefundFn = func(context.Context, int64, int64, time.Time) (bool, error) { return false, errBoom }
		assert.ErrorIs(t, f.service().ActivateSnipeAfterPayment(ctx, 5), errBoom)

		f = late()
		f.clients.AdjustCreditFn = func(context.Context, int64, int64, string, *int64) (int64, error) { return 0, errBoom }
		assert.ErrorIs(t, f.service().ActivateSnipeAfterPayment(ctx, 5), errBoom)
	})

	t.Run("an activation without a deadline still tells the client", func(t *testing.T) {
		f := newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
			return &domain.DomainSnipe{ID: 5, ClientID: 7, DomainName: "hunt.com", Status: domain.SnipeActive}, nil
		}
		require.NoError(t, f.service().ActivateSnipeAfterPayment(ctx, 5))
		assert.NotContains(t, f.templates[0].Data, "DeadlineAt")
	})
}

func ptr(v int64) *int64 { return &v }

func TestClientListAndGet(t *testing.T) {
	ctx := context.Background()
	inv := int64(900)
	owned := func() *domain.DomainSnipe {
		pid := int64(3)
		return &domain.DomainSnipe{
			ID: 4, ClientID: 7, PlanID: &pid, DomainName: "hunt.com", Status: domain.SnipeActive, Phase: domain.SnipePhaseBurst,
			InvoiceID: &inv, LastError: "registrar said something internal", Attempts: 12, LeaseOwner: "w1",
		}
	}

	t.Run("list names the plan and never leaks operator fields", func(t *testing.T) {
		f := newClientFx()
		f.snipes.ListByClientFn = func(_ context.Context, clientID int64, p ports.ListParams) ([]domain.DomainSnipe, int64, error) {
			assert.EqualValues(t, 7, clientID)
			assert.Equal(t, "active", p.Status)
			return []domain.DomainSnipe{*owned()}, 1, nil
		}
		items, total, err := f.service().ListForClient(ctx, 7, ports.ListParams{Status: "active"})
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		require.Len(t, items, 1)
		assert.Equal(t, "Standard", items[0].PlanName)
		assert.Equal(t, domain.SnipePhaseBurst, items[0].Phase)
		assert.EqualValues(t, 12, items[0].Attempts)
		assert.Equal(t, []string{}, items[0].Nameservers)
	})

	t.Run("a plan lookup failure only blanks the names", func(t *testing.T) {
		f := newClientFx()
		f.plans.ListFn = func(context.Context, bool) ([]domain.SnipePlan, error) { return nil, errBoom }
		f.snipes.ListByClientFn = func(context.Context, int64, ports.ListParams) ([]domain.DomainSnipe, int64, error) {
			return []domain.DomainSnipe{*owned()}, 1, nil
		}
		items, _, err := f.service().ListForClient(ctx, 7, ports.ListParams{})
		require.NoError(t, err)
		assert.Empty(t, items[0].PlanName)
	})

	t.Run("list errors propagate", func(t *testing.T) {
		f := newClientFx()
		f.snipes.ListByClientFn = func(context.Context, int64, ports.ListParams) ([]domain.DomainSnipe, int64, error) {
			return nil, 0, errBoom
		}
		_, _, err := f.service().ListForClient(ctx, 7, ports.ListParams{})
		assert.ErrorIs(t, err, errBoom)
	})

	t.Run("get returns only the events an owner may see", func(t *testing.T) {
		f := newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return owned(), nil }
		f.snipes.ListEventsFn = func(context.Context, int64, int) ([]domain.SnipeEvent, error) {
			return []domain.SnipeEvent{
				{Kind: eventError, Message: "rdash: Insufficient balance"},
				{Kind: eventPhase, Message: "bursting register calls"},
				{Kind: eventLateWin, Message: "internal"},
				{Kind: eventPaid, Message: "payment received"},
				{Kind: eventAvailable, Message: "available"},
				{Kind: string(domain.SnipeCancelled), Message: "cancelled"},
			}, nil
		}
		d, err := f.service().GetForClient(ctx, 7, 4)
		require.NoError(t, err)
		kinds := []string{}
		for _, e := range d.Events {
			kinds = append(kinds, e.Kind)
		}
		assert.Equal(t, []string{eventPaid, eventAvailable, "cancelled"}, kinds)
		assert.Equal(t, "Standard", d.Snipe.PlanName)
	})

	t.Run("another client's snipe does not exist", func(t *testing.T) {
		f := newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return owned(), nil }
		_, err := f.service().GetForClient(ctx, 8, 4)
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("errors propagate", func(t *testing.T) {
		f := newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return nil, errBoom }
		_, err := f.service().GetForClient(ctx, 7, 4)
		assert.ErrorIs(t, err, errBoom)

		f = newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return owned(), nil }
		f.snipes.ListEventsFn = func(context.Context, int64, int) ([]domain.SnipeEvent, error) { return nil, errBoom }
		_, err = f.service().GetForClient(ctx, 7, 4)
		assert.ErrorIs(t, err, errBoom)
	})

	t.Run("a snipe without a plan has no plan name", func(t *testing.T) {
		c := toClientSnipe(&domain.DomainSnipe{ID: 1, Status: domain.SnipeWon, Phase: domain.SnipePhaseBurst}, "")
		assert.Empty(t, c.PlanName)
		assert.Empty(t, c.Phase, "only an active hunt shows its phase")
		assert.EqualValues(t, 0, planKey(nil))
	})
}

func TestCancelForClient(t *testing.T) {
	ctx := context.Background()
	serve := func(f *clientFx, sn domain.DomainSnipe) {
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { cp := sn; return &cp, nil }
	}
	base := domain.DomainSnipe{
		ID: 4, ClientID: 7, DomainName: "hunt.com", InvoiceID: ptr(900),
		RefundableDomain: 166_500, RefundableFee: 55_500,
	}
	with := func(st domain.SnipeStatus) domain.DomainSnipe { s := base; s.Status = st; return s }

	t.Run("an unpaid snipe is closed first, then its invoice is voided; nothing to refund", func(t *testing.T) {
		f := newClientFx()
		serve(f, with(domain.SnipePendingPayment))
		var order []string
		prevFinish := f.snipes.FinishFn
		f.snipes.FinishFn = func(ctx context.Context, fin ports.SnipeFinish) (bool, error) {
			order = append(order, "finish")
			assert.Equal(t, domain.SnipePendingPayment, fin.From)
			assert.Zero(t, fin.RefundAmount)
			return prevFinish(ctx, fin)
		}
		f.canceller.CancelInvoiceFn = func(_ context.Context, actor, id int64) (*domain.Invoice, error) {
			order = append(order, "void")
			assert.EqualValues(t, 70, actor)
			assert.EqualValues(t, 900, id)
			return &domain.Invoice{ID: id}, nil
		}
		got, err := f.service().CancelForClient(ctx, f.who(), 4)
		require.NoError(t, err)
		assert.EqualValues(t, 4, got.ID)
		assert.Equal(t, []string{"finish", "void"}, order, "if a payment lands in between, the late-payment path refunds it")
		assert.Empty(t, f.credits)
		assert.Equal(t, []string{tplCancelled}, f.templateKeys())
	})

	t.Run("a paid active hunt returns the domain price and keeps the fee", func(t *testing.T) {
		f := newClientFx()
		serve(f, with(domain.SnipeActive))
		f.canceller.CancelInvoiceFn = func(context.Context, int64, int64) (*domain.Invoice, error) {
			t.Fatal("a paid invoice must not be touched")
			return nil, nil
		}
		_, err := f.service().CancelForClient(ctx, f.who(), 4)
		require.NoError(t, err)
		require.Len(t, f.credits, 1)
		assert.EqualValues(t, 166_500, f.credits[0].Delta)
		assert.EqualValues(t, 70, f.audit.Entries[0].ActorUserID)
		assert.Equal(t, "snipe.cancelled", f.audit.Entries[0].Action)
	})

	t.Run("voiding the invoice is best effort", func(t *testing.T) {
		for name, err := range map[string]error{
			"already cancelled or paid": apperr.Conflict("invoice cannot transition from cancelled to cancelled"),
			"a real failure":            errBoom,
		} {
			f := newClientFx()
			serve(f, with(domain.SnipePendingPayment))
			f.canceller.CancelInvoiceFn = func(context.Context, int64, int64) (*domain.Invoice, error) { return nil, err }
			_, cerr := f.service().CancelForClient(ctx, f.who(), 4)
			require.NoError(t, cerr, name)
		}
		// no invoice at all
		f := newClientFx()
		sn := with(domain.SnipePendingPayment)
		sn.InvoiceID = nil
		serve(f, sn)
		f.canceller.CancelInvoiceFn = func(context.Context, int64, int64) (*domain.Invoice, error) {
			t.Fatal("nothing to void")
			return nil, nil
		}
		_, err := f.service().CancelForClient(ctx, f.who(), 4)
		require.NoError(t, err)
	})

	t.Run("not yours, already ended, or lost a race", func(t *testing.T) {
		f := newClientFx()
		serve(f, with(domain.SnipeActive))
		_, err := f.service().CancelForClient(ctx, ClientIdentity{UserID: 71, ClientID: 8}, 4)
		assertCode(t, err, apperr.CodeNotFound)

		f = newClientFx()
		serve(f, with(domain.SnipeWon))
		_, err = f.service().CancelForClient(ctx, f.who(), 4)
		assertCode(t, err, apperr.CodeConflict)
		assert.Empty(t, f.finishes)

		f = newClientFx()
		serve(f, with(domain.SnipeActive))
		f.snipes.FinishFn = func(context.Context, ports.SnipeFinish) (bool, error) { return false, nil }
		_, err = f.service().CancelForClient(ctx, f.who(), 4)
		assertCode(t, err, apperr.CodeConflict)
		assert.Empty(t, f.credits)
	})

	t.Run("errors propagate", func(t *testing.T) {
		f := newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return nil, errBoom }
		_, err := f.service().CancelForClient(ctx, f.who(), 4)
		assert.ErrorIs(t, err, errBoom)

		f = newClientFx()
		serve(f, with(domain.SnipeActive))
		f.snipes.FinishFn = func(context.Context, ports.SnipeFinish) (bool, error) { return false, errBoom }
		_, err = f.service().CancelForClient(ctx, f.who(), 4)
		assert.ErrorIs(t, err, errBoom)

		f = newClientFx()
		var calls int
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
			calls++
			if calls > 1 {
				return nil, errBoom // the reload after the cancel
			}
			s := with(domain.SnipeActive)
			return &s, nil
		}
		_, err = f.service().CancelForClient(ctx, f.who(), 4)
		assert.ErrorIs(t, err, errBoom)
	})
}

func TestSweepStalePending(t *testing.T) {
	ctx := context.Background()
	stale := func(id, invoice int64) domain.DomainSnipe {
		return domain.DomainSnipe{ID: id, ClientID: 7, DomainName: "hunt.com", Status: domain.SnipePendingPayment, InvoiceID: ptr(invoice)}
	}

	t.Run("closes the snipe, then voids the invoice", func(t *testing.T) {
		f := newClientFx()
		f.snipes.ListStalePendingFn = func(_ context.Context, now time.Time, limit int) ([]domain.DomainSnipe, error) {
			assert.Equal(t, t0, now)
			assert.Equal(t, staleSweepBatch, limit)
			return []domain.DomainSnipe{stale(1, 901), stale(2, 902)}, nil
		}
		var voided []int64
		f.canceller.CancelInvoiceFn = func(_ context.Context, actor, id int64) (*domain.Invoice, error) {
			assert.Zero(t, actor, "the system voids it")
			voided = append(voided, id)
			return &domain.Invoice{ID: id}, nil
		}
		f.supervisor().sweepStale(ctx, t0)

		require.Len(t, f.finishes, 2)
		for _, fin := range f.finishes {
			assert.Equal(t, domain.SnipePendingPayment, fin.From)
			assert.Equal(t, domain.SnipeCancelled, fin.To)
			assert.Contains(t, fin.LastError, "not received")
			assert.Zero(t, fin.RefundAmount)
		}
		assert.Equal(t, []int64{901, 902}, voided)
	})

	t.Run("a conflict voiding the invoice is expected, a failure is only logged", func(t *testing.T) {
		f := newClientFx()
		f.snipes.ListStalePendingFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
			return []domain.DomainSnipe{stale(1, 901), stale(2, 902)}, nil
		}
		var n int
		f.canceller.CancelInvoiceFn = func(context.Context, int64, int64) (*domain.Invoice, error) {
			n++
			if n == 1 {
				return nil, apperr.Conflict("already cancelled")
			}
			return nil, errBoom
		}
		f.supervisor().sweepStale(ctx, t0)
		assert.Len(t, f.finishes, 2)
	})

	t.Run("a snipe that could not be closed keeps its invoice untouched", func(t *testing.T) {
		f := newClientFx()
		f.snipes.ListStalePendingFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
			return []domain.DomainSnipe{stale(1, 901), stale(2, 902), stale(3, 903)}, nil
		}
		var n int
		f.snipes.FinishFn = func(context.Context, ports.SnipeFinish) (bool, error) {
			n++
			switch n {
			case 1:
				return false, errBoom // storage error
			case 2:
				return false, nil // lost a race (e.g. it was just paid)
			}
			return true, nil
		}
		var voided []int64
		f.canceller.CancelInvoiceFn = func(_ context.Context, _, id int64) (*domain.Invoice, error) {
			voided = append(voided, id)
			return nil, nil
		}
		f.supervisor().sweepStale(ctx, t0)
		assert.Equal(t, []int64{903}, voided)
	})

	t.Run("a snipe without an invoice is simply closed; a list failure is contained", func(t *testing.T) {
		f := newClientFx()
		sn := stale(1, 0)
		sn.InvoiceID = nil
		f.snipes.ListStalePendingFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
			return []domain.DomainSnipe{sn}, nil
		}
		f.canceller.CancelInvoiceFn = func(context.Context, int64, int64) (*domain.Invoice, error) {
			t.Fatal("no invoice to void")
			return nil, nil
		}
		f.supervisor().sweepStale(ctx, t0)
		assert.Len(t, f.finishes, 1)

		f = newClientFx()
		f.snipes.ListStalePendingFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) { return nil, errBoom }
		f.supervisor().sweepStale(ctx, t0)
		assert.Empty(t, f.finishes)
	})

	t.Run("the supervisor sweeps at most once a minute", func(t *testing.T) {
		f := newClientFx()
		var lists int
		f.snipes.ListStalePendingFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
			lists++
			return nil, nil
		}
		sup := f.supervisor()
		sup.Tick(ctx)
		assert.Equal(t, 1, lists)
		f.clock.Advance(10 * time.Second)
		sup.Tick(ctx)
		assert.Equal(t, 1, lists)
		f.clock.Advance(staleSweepEvery)
		sup.Tick(ctx)
		assert.Equal(t, 2, lists)
	})
}

func TestPlansAdmin(t *testing.T) {
	ctx := context.Background()

	t.Run("list never returns null", func(t *testing.T) {
		f := newClientFx()
		f.plans.ListFn = func(_ context.Context, activeOnly bool) ([]domain.SnipePlan, error) {
			assert.False(t, activeOnly, "admins see deactivated plans too")
			return nil, nil
		}
		got, err := f.service().ListPlans(ctx)
		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)

		f.plans.ListFn = func(context.Context, bool) ([]domain.SnipePlan, error) { return nil, errBoom }
		_, err = f.service().ListPlans(ctx)
		assert.ErrorIs(t, err, errBoom)
	})

	t.Run("create defaults to active and audits", func(t *testing.T) {
		f := newClientFx()
		var stored domain.SnipePlan
		f.plans.CreateFn = func(_ context.Context, p *domain.SnipePlan) error {
			p.ID = 8
			stored = *p
			return nil
		}
		p, err := f.service().CreatePlan(ctx, 9, PlanRequest{Name: "Pro", WindowDays: 30, Fee: 50_000, SuccessFee: 25_000, Sort: 2})
		require.NoError(t, err)
		assert.EqualValues(t, 8, p.ID)
		assert.True(t, stored.Active)
		assert.Equal(t, "Pro", stored.Name)
		assert.EqualValues(t, 25_000, stored.SuccessFee)
		require.Len(t, f.audit.Entries, 1)
		assert.Equal(t, "snipe_plan.create", f.audit.Entries[0].Action)
		assert.EqualValues(t, 9, f.audit.Entries[0].ActorUserID)

		off := false
		p, err = f.service().CreatePlan(ctx, 9, PlanRequest{Name: "Hidden", WindowDays: 7, Active: &off})
		require.NoError(t, err)
		assert.False(t, p.Active)

		f.plans.CreateFn = func(context.Context, *domain.SnipePlan) error { return errBoom }
		_, err = f.service().CreatePlan(ctx, 9, PlanRequest{Name: "x", WindowDays: 7})
		assert.ErrorIs(t, err, errBoom)
	})

	t.Run("update replaces the fields, keeps active unless told, audits before and after", func(t *testing.T) {
		f := newClientFx()
		f.plans.GetByIDFn = func(context.Context, int64) (*domain.SnipePlan, error) {
			return &domain.SnipePlan{ID: 8, Name: "Old", WindowDays: 7, Fee: 1, Active: true}, nil
		}
		var stored domain.SnipePlan
		f.plans.UpdateFn = func(_ context.Context, p *domain.SnipePlan) error {
			stored = *p
			return nil
		}
		p, err := f.service().UpdatePlan(ctx, 9, 8, PlanRequest{Name: "New", WindowDays: 14, Fee: 70_000, SuccessFee: 5_000, Sort: 3})
		require.NoError(t, err)
		assert.Equal(t, "New", p.Name)
		assert.Equal(t, 14, stored.WindowDays)
		assert.EqualValues(t, 70_000, stored.Fee)
		assert.True(t, stored.Active, "active is untouched when the request does not say")
		before := f.audit.Entries[0].Before.(*domain.SnipePlan)
		assert.Equal(t, "Old", before.Name)

		off := false
		p, err = f.service().UpdatePlan(ctx, 9, 8, PlanRequest{Name: "New", WindowDays: 14, Active: &off})
		require.NoError(t, err)
		assert.False(t, p.Active)
	})

	t.Run("update errors propagate", func(t *testing.T) {
		f := newClientFx()
		f.plans.GetByIDFn = func(context.Context, int64) (*domain.SnipePlan, error) { return nil, apperr.NotFound("snipe plan") }
		_, err := f.service().UpdatePlan(ctx, 9, 8, PlanRequest{Name: "x", WindowDays: 7})
		assertCode(t, err, apperr.CodeNotFound)

		f = newClientFx()
		f.plans.GetByIDFn = func(context.Context, int64) (*domain.SnipePlan, error) { return &domain.SnipePlan{ID: 8}, nil }
		f.plans.UpdateFn = func(context.Context, *domain.SnipePlan) error { return errBoom }
		_, err = f.service().UpdatePlan(ctx, 9, 8, PlanRequest{Name: "x", WindowDays: 7})
		assert.ErrorIs(t, err, errBoom)
	})
}

func TestAdminCancelVoidsAnUnpaidSnipesInvoice(t *testing.T) {
	ctx := context.Background()
	f := newClientFx()
	f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
		return &domain.DomainSnipe{
			ID: 4, ClientID: 7, DomainName: "hunt.com", Status: domain.SnipePendingPayment, InvoiceID: ptr(900),
		}, nil
	}
	var voided, actor int64
	f.canceller.CancelInvoiceFn = func(_ context.Context, a, id int64) (*domain.Invoice, error) {
		voided, actor = id, a
		return &domain.Invoice{ID: id}, nil
	}
	_, err := f.service().AdminCancel(ctx, 9, 4, false)
	require.NoError(t, err)
	assert.EqualValues(t, 900, voided, "an unpaid snipe's invoice must not stay payable")
	assert.EqualValues(t, 9, actor)

	// A paid (active) snipe keeps its invoice: it is paid, there is nothing to void.
	f = newClientFx()
	f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) {
		return &domain.DomainSnipe{ID: 4, ClientID: 7, Status: domain.SnipeActive, InvoiceID: ptr(900)}, nil
	}
	f.canceller.CancelInvoiceFn = func(context.Context, int64, int64) (*domain.Invoice, error) {
		t.Fatal("a paid invoice must not be touched")
		return nil, nil
	}
	_, err = f.service().AdminCancel(ctx, 9, 4, false)
	require.NoError(t, err)
}

// A client may have a Duitku payment open when the hunt is cancelled. Voiding
// the invoice under it would strand that money (a paid, cancelled invoice has
// no refund path), so the invoice is left to be paid: ActivateSnipeAfterPayment
// then turns the whole payment into account credit.
func TestVoidInvoiceLeavesAnInvoiceWithAPaymentInFlight(t *testing.T) {
	ctx := context.Background()
	unpaid := func() *domain.DomainSnipe {
		return &domain.DomainSnipe{ID: 4, ClientID: 7, DomainName: "hunt.com", Status: domain.SnipePendingPayment, InvoiceID: ptr(900)}
	}

	t.Run("a pending gateway transaction keeps the invoice", func(t *testing.T) {
		f := newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return unpaid(), nil }
		f.txRepo.ListByInvoiceFn = func(_ context.Context, id int64) ([]domain.Transaction, error) {
			assert.EqualValues(t, 900, id)
			return []domain.Transaction{{Status: domain.TxFailed}, {Status: domain.TxPending}}, nil
		}
		f.canceller.CancelInvoiceFn = func(context.Context, int64, int64) (*domain.Invoice, error) {
			t.Fatal("a payment is in flight: the invoice must stay payable")
			return nil, nil
		}
		_, err := f.service().AdminCancel(ctx, 9, 4, false)
		require.NoError(t, err)
	})

	t.Run("only settled or failed transactions do not block the void", func(t *testing.T) {
		f := newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return unpaid(), nil }
		f.txRepo.ListByInvoiceFn = func(context.Context, int64) ([]domain.Transaction, error) {
			return []domain.Transaction{{Status: domain.TxFailed}, {Status: domain.TxExpired}}, nil
		}
		var voided bool
		f.canceller.CancelInvoiceFn = func(_ context.Context, _, id int64) (*domain.Invoice, error) {
			voided = true
			return &domain.Invoice{ID: id}, nil
		}
		_, err := f.service().AdminCancel(ctx, 9, 4, false)
		require.NoError(t, err)
		assert.True(t, voided)
	})

	t.Run("an unreadable payment list fails closed", func(t *testing.T) {
		f := newClientFx()
		f.snipes.GetByIDFn = func(context.Context, int64) (*domain.DomainSnipe, error) { return unpaid(), nil }
		f.txRepo.ListByInvoiceFn = func(context.Context, int64) ([]domain.Transaction, error) { return nil, errBoom }
		f.canceller.CancelInvoiceFn = func(context.Context, int64, int64) (*domain.Invoice, error) {
			t.Fatal("cannot tell whether a payment is in flight: do not void")
			return nil, nil
		}
		_, err := f.service().AdminCancel(ctx, 9, 4, false)
		require.NoError(t, err, "the snipe itself is cancelled regardless")
	})

	t.Run("the stale sweep applies the same guard", func(t *testing.T) {
		f := newClientFx()
		f.snipes.ListStalePendingFn = func(context.Context, time.Time, int) ([]domain.DomainSnipe, error) {
			return []domain.DomainSnipe{*unpaid()}, nil
		}
		f.txRepo.ListByInvoiceFn = func(context.Context, int64) ([]domain.Transaction, error) {
			return []domain.Transaction{{Status: domain.TxPending}}, nil
		}
		f.canceller.CancelInvoiceFn = func(context.Context, int64, int64) (*domain.Invoice, error) {
			t.Fatal("a payment is in flight: the sweep must not void")
			return nil, nil
		}
		f.supervisor().sweepStale(ctx, t0)
		require.Len(t, f.finishes, 1)
		assert.Equal(t, domain.SnipeCancelled, f.lastFinish().To, "the snipe is still closed first")
	})
}
