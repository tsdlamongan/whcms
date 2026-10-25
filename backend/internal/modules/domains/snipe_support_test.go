package domains_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func activeTLD(over func(*domain.TLDPricing)) *domain.TLDPricing {
	p := &domain.TLDPricing{
		ID: 1, TLD: "com", Active: true, MinYears: 1, MaxYears: 5,
		RegisterPrices: map[string]int64{"1": 150000, "2": 290000},
		RenewPrices:    map[string]int64{"1": 160000},
	}
	if over != nil {
		over(p)
	}
	return p
}

func TestQuoteRegistration(t *testing.T) {
	ctx := context.Background()

	t.Run("tld matrix prices the whole term and freezes the year-1 renewal", func(t *testing.T) {
		d := newFixture()
		d.tldPricing.GetByTLDFn = func(_ context.Context, tld string) (*domain.TLDPricing, error) {
			assert.Equal(t, "com", tld)
			return activeTLD(nil), nil
		}
		q, err := d.svc().QuoteRegistration(ctx, " Hunt.COM ", 2)
		require.NoError(t, err)
		assert.EqualValues(t, 290000, q.Total)
		assert.EqualValues(t, 160000, q.RenewPerYear)
	})

	t.Run("years default to one", func(t *testing.T) {
		d := newFixture()
		d.tldPricing.GetByTLDFn = func(context.Context, string) (*domain.TLDPricing, error) { return activeTLD(nil), nil }
		q, err := d.svc().QuoteRegistration(ctx, "hunt.com", 0)
		require.NoError(t, err)
		assert.EqualValues(t, 150000, q.Total)
	})

	t.Run("exact premium override wins and multiplies by years", func(t *testing.T) {
		d := newFixture()
		d.premiumPricing.GetByNameFn = func(context.Context, string) (*domain.PremiumDomainPricing, error) {
			return &domain.PremiumDomainPricing{RegisterPrice: 5_000_000, RenewPrice: 4_000_000}, nil
		}
		d.tldPricing.GetByTLDFn = func(context.Context, string) (*domain.TLDPricing, error) {
			t.Fatal("tld matrix must not be consulted once a premium override matched")
			return nil, nil
		}
		q, err := d.svc().QuoteRegistration(ctx, "hunt.com", 3)
		require.NoError(t, err)
		assert.EqualValues(t, 15_000_000, q.Total)
		assert.EqualValues(t, 4_000_000, q.RenewPerYear)
	})

	t.Run("length tier beats the tld matrix", func(t *testing.T) {
		d := newFixture()
		d.premiumLengthPricing.GetByTLDAndLengthFn = func(_ context.Context, tld string, n int) (*domain.PremiumLengthPricing, error) {
			assert.Equal(t, "id", tld)
			assert.Equal(t, 3, n)
			return &domain.PremiumLengthPricing{Price: 900000}, nil
		}
		q, err := d.svc().QuoteRegistration(ctx, "abc.id", 2)
		require.NoError(t, err)
		assert.EqualValues(t, 1_800_000, q.Total)
		assert.EqualValues(t, 900000, q.RenewPerYear)
	})

	t.Run("unconfigured or inactive tld is a validation error, never a guess", func(t *testing.T) {
		d := newFixture()
		_, err := d.svc().QuoteRegistration(ctx, "hunt.com", 1)
		assertCode(t, err, apperr.CodeValidation)

		d.tldPricing.GetByTLDFn = func(context.Context, string) (*domain.TLDPricing, error) {
			return activeTLD(func(p *domain.TLDPricing) { p.Active = false }), nil
		}
		_, err = d.svc().QuoteRegistration(ctx, "hunt.com", 1)
		assertCode(t, err, apperr.CodeValidation)

		d.tldPricing.GetByTLDFn = func(context.Context, string) (*domain.TLDPricing, error) {
			return nil, apperr.NotFound("tld pricing")
		}
		_, err = d.svc().QuoteRegistration(ctx, "hunt.com", 1)
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("bad input is rejected before any lookup", func(t *testing.T) {
		d := newFixture()
		_, err := d.svc().QuoteRegistration(ctx, "not a domain", 1)
		assertCode(t, err, apperr.CodeValidation)
		_, err = d.svc().QuoteRegistration(ctx, "hunt.com", 11)
		assertCode(t, err, apperr.CodeValidation)
		_, err = d.svc().QuoteRegistration(ctx, "hunt.com", -1)
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("years outside the tld bounds or missing from the matrix", func(t *testing.T) {
		d := newFixture()
		d.tldPricing.GetByTLDFn = func(context.Context, string) (*domain.TLDPricing, error) { return activeTLD(nil), nil }
		_, err := d.svc().QuoteRegistration(ctx, "hunt.com", 6)
		assertCode(t, err, apperr.CodeValidation) // > MaxYears
		_, err = d.svc().QuoteRegistration(ctx, "hunt.com", 3)
		assertCode(t, err, apperr.CodeValidation) // in bounds but no "3" price

		d.tldPricing.GetByTLDFn = func(context.Context, string) (*domain.TLDPricing, error) {
			return activeTLD(func(p *domain.TLDPricing) { p.MinYears = 2 }), nil
		}
		_, err = d.svc().QuoteRegistration(ctx, "hunt.com", 1)
		assertCode(t, err, apperr.CodeValidation) // < MinYears

		d.tldPricing.GetByTLDFn = func(context.Context, string) (*domain.TLDPricing, error) {
			return activeTLD(func(p *domain.TLDPricing) { p.RegisterPrices = map[string]int64{"1": 0} }), nil
		}
		_, err = d.svc().QuoteRegistration(ctx, "hunt.com", 1)
		assertCode(t, err, apperr.CodeValidation) // a zero price is never sellable
	})

	t.Run("a real lookup failure is returned, not swallowed", func(t *testing.T) {
		d := newFixture()
		d.premiumPricing.GetByNameFn = func(context.Context, string) (*domain.PremiumDomainPricing, error) { return nil, errBoom }
		_, err := d.svc().QuoteRegistration(ctx, "hunt.com", 1)
		assert.ErrorIs(t, err, errBoom)

		d = newFixture()
		d.premiumLengthPricing.GetByTLDAndLengthFn = func(context.Context, string, int) (*domain.PremiumLengthPricing, error) {
			return nil, errBoom
		}
		_, err = d.svc().QuoteRegistration(ctx, "hunt.com", 1)
		assert.ErrorIs(t, err, errBoom)

		d = newFixture()
		d.tldPricing.GetByTLDFn = func(context.Context, string) (*domain.TLDPricing, error) { return nil, errBoom }
		_, err = d.svc().QuoteRegistration(ctx, "hunt.com", 1)
		assert.ErrorIs(t, err, errBoom)
	})
}

func TestRegistrantContactFor(t *testing.T) {
	ctx := context.Background()
	full := &domain.Client{
		ID: 5, UserID: 8, FirstName: "Jane", LastName: "Doe", Company: "Acme",
		Phone: "+62.8123", Address1: "Jl. A 1", City: "Lamongan", State: "Jawa Timur", Postcode: "62211", Country: "ID",
	}

	t.Run("builds the contact and sends no email", func(t *testing.T) {
		d := newFixture()
		d.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return full, nil }
		d.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) {
			return &domain.User{ID: 8, Email: "jane@example.test"}, nil
		}
		d.notifier.SendTemplateFn = func(context.Context, int64, string, map[string]any) error {
			t.Fatal("RegistrantContactFor must not notify")
			return nil
		}
		c, err := d.svc().RegistrantContactFor(ctx, 5)
		require.NoError(t, err)
		assert.Equal(t, "jane@example.test", c.Email)
		assert.Equal(t, "Jl. A 1", c.Address1)
		assert.Equal(t, "ID", c.Country)
	})

	t.Run("incomplete profile is a validation error", func(t *testing.T) {
		d := newFixture()
		d.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return &domain.Client{ID: 5, UserID: 8}, nil }
		d.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) { return &domain.User{ID: 8, Email: "x@y.z"}, nil }
		_, err := d.svc().RegistrantContactFor(ctx, 5)
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("lookup failures propagate", func(t *testing.T) {
		d := newFixture()
		d.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return nil, errBoom }
		_, err := d.svc().RegistrantContactFor(ctx, 5)
		assert.ErrorIs(t, err, errBoom)

		d = newFixture()
		d.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) { return full, nil }
		d.users.GetByIDFn = func(context.Context, int64) (*domain.User, error) { return nil, errBoom }
		_, err = d.svc().RegistrantContactFor(ctx, 5)
		assert.ErrorIs(t, err, errBoom)
	})
}

func TestRecordSnipedDomain(t *testing.T) {
	ctx := context.Background()
	expiry := time.Date(2027, 10, 24, 0, 0, 0, 0, time.UTC)
	in := ports.RecordSnipedDomainInput{
		ClientID: 5, Name: " Hunt.COM ", RenewPerYear: 160000,
		Result:      ports.DomainResult{Status: "pending", ExpiryDate: expiry, OrderID: "77"},
		Nameservers: []string{"ns1.example.net", "ns2.example.net"},
	}

	t.Run("inserts an already-active domain with checkout defaults", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByNameFn = func(_ context.Context, name string) (*domain.Registrar, error) {
			assert.Equal(t, "rdash", name)
			return &domain.Registrar{ID: 3}, nil
		}
		var created *domain.Domain
		d.domains.CreateFn = func(_ context.Context, dom *domain.Domain) error {
			dom.ID = 99
			created = dom
			return nil
		}
		got, err := d.svc().RecordSnipedDomain(ctx, in)
		require.NoError(t, err)
		assert.EqualValues(t, 99, got.ID)
		require.NotNil(t, created)
		assert.Equal(t, "hunt.com", created.Name)
		assert.Equal(t, domain.DomainActive, created.Status)
		assert.EqualValues(t, 5, created.ClientID)
		assert.EqualValues(t, 3, created.RegistrarID)
		assert.EqualValues(t, 160000, created.RecurringAmount)
		assert.Equal(t, domain.CycleAnnually, created.BillingCycle)
		assert.True(t, created.AutoRenew)
		assert.Equal(t, fixedNow, *created.RegistrationDate)
		assert.Equal(t, expiry, *created.ExpiryDate)
		assert.Equal(t, expiry, *created.NextDueDate)
		assert.JSONEq(t, `["ns1.example.net","ns2.example.net"]`, string(created.Nameservers))
		var meta map[string]any
		require.NoError(t, json.Unmarshal(created.RegistrarMeta, &meta))
		assert.Equal(t, "snipe", meta["source"])
		assert.Equal(t, "77", meta["order_id"])
	})

	t.Run("no expiry and no nameservers leaves dates unset and ns empty", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByNameFn = func(context.Context, string) (*domain.Registrar, error) { return &domain.Registrar{ID: 3}, nil }
		var created *domain.Domain
		d.domains.CreateFn = func(_ context.Context, dom *domain.Domain) error { created = dom; return nil }
		_, err := d.svc().RecordSnipedDomain(ctx, ports.RecordSnipedDomainInput{ClientID: 5, Name: "hunt.com"})
		require.NoError(t, err)
		assert.Nil(t, created.ExpiryDate)
		assert.Nil(t, created.NextDueDate)
		assert.JSONEq(t, `[]`, string(created.Nameservers))
		var meta map[string]any
		require.NoError(t, json.Unmarshal(created.RegistrarMeta, &meta))
		assert.NotContains(t, meta, "order_id")
	})

	t.Run("failures propagate", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByNameFn = func(context.Context, string) (*domain.Registrar, error) { return nil, errBoom }
		_, err := d.svc().RecordSnipedDomain(ctx, in)
		assert.ErrorIs(t, err, errBoom)

		d = newFixture()
		d.registrars.GetByNameFn = func(context.Context, string) (*domain.Registrar, error) { return nil, nil }
		_, err = d.svc().RecordSnipedDomain(ctx, in)
		assertCode(t, err, apperr.CodeNotFound)

		d = newFixture()
		d.registrars.GetByNameFn = func(context.Context, string) (*domain.Registrar, error) { return &domain.Registrar{ID: 3}, nil }
		d.domains.CreateFn = func(context.Context, *domain.Domain) error { return apperr.Conflict("domain exists") }
		_, err = d.svc().RecordSnipedDomain(ctx, in)
		assertCode(t, err, apperr.CodeConflict)
	})
}
