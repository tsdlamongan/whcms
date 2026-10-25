package domains_test

import (
	"context"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Admin - TLD pricing

func tldRequest(over func(*domains.TLDPricingRequest)) domains.TLDPricingRequest {
	req := domains.TLDPricingRequest{
		TLD:            "com",
		RegistrarID:    1,
		Active:         true,
		MinYears:       1,
		MaxYears:       10,
		RegisterPrices: map[string]int64{"1": 150000, "2": 290000},
		RenewPrices:    map[string]int64{"1": 160000, "2": 310000},
		TransferPrice:  150000,
	}
	if over != nil {
		over(&req)
	}
	return req
}

func TestTLDPricingAdmin(t *testing.T) {
	t.Run("list and get", func(t *testing.T) {
		d := newFixture()
		d.tldPricing.ListFn = func(ctx context.Context) ([]domain.TLDPricing, error) {
			return []domain.TLDPricing{{ID: 1, TLD: "com"}}, nil
		}
		d.tldPricing.GetByIDFn = func(ctx context.Context, id int64) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{ID: id, TLD: "com"}, nil
		}
		list, err := d.svc().ListTLDPricing(context.Background())
		require.NoError(t, err)
		assert.Len(t, list, 1)
		got, err := d.svc().GetTLDPricing(context.Background(), 1)
		require.NoError(t, err)
		assert.Equal(t, "com", got.TLD)
	})

	t.Run("create validates and normalizes tld, then audits", func(t *testing.T) {
		d := newFixture()
		var stored *domain.TLDPricing
		d.tldPricing.CreateFn = func(ctx context.Context, p *domain.TLDPricing) error {
			p.ID = 5
			cp := *p
			stored = &cp
			return nil
		}
		p, err := d.svc().CreateTLDPricing(context.Background(), 9, tldRequest(func(r *domains.TLDPricingRequest) {
			r.TLD = " .CO.ID "
		}))
		require.NoError(t, err)
		assert.Equal(t, "co.id", p.TLD)
		require.NotNil(t, stored)
		assert.Equal(t, "co.id", stored.TLD)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "tld_pricing.create", d.audit.Entries[0].Action)
		assert.EqualValues(t, 9, d.audit.Entries[0].ActorUserID)
	})

	t.Run("create rejects blank tld", func(t *testing.T) {
		d := newFixture()
		_, err := d.svc().CreateTLDPricing(context.Background(), 1, tldRequest(func(r *domains.TLDPricingRequest) { r.TLD = "  " }))
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("create rejects min_years greater than max_years", func(t *testing.T) {
		d := newFixture()
		_, err := d.svc().CreateTLDPricing(context.Background(), 1, tldRequest(func(r *domains.TLDPricingRequest) {
			r.MinYears, r.MaxYears = 5, 2
		}))
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("create rejects out-of-range price year keys", func(t *testing.T) {
		d := newFixture()
		_, err := d.svc().CreateTLDPricing(context.Background(), 1, tldRequest(func(r *domains.TLDPricingRequest) {
			r.RegisterPrices = map[string]int64{"11": 100}
		}))
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("create rejects negative prices", func(t *testing.T) {
		d := newFixture()
		_, err := d.svc().CreateTLDPricing(context.Background(), 1, tldRequest(func(r *domains.TLDPricingRequest) {
			r.RenewPrices = map[string]int64{"1": -5}
		}))
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("update replaces the row and audits", func(t *testing.T) {
		d := newFixture()
		d.tldPricing.GetByIDFn = func(ctx context.Context, id int64) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{ID: id, TLD: "com", Active: true}, nil
		}
		var stored *domain.TLDPricing
		d.tldPricing.UpdateFn = func(ctx context.Context, p *domain.TLDPricing) error {
			cp := *p
			stored = &cp
			return nil
		}
		p, err := d.svc().UpdateTLDPricing(context.Background(), 9, 1, tldRequest(func(r *domains.TLDPricingRequest) {
			r.Active = false
		}))
		require.NoError(t, err)
		assert.False(t, p.Active)
		require.NotNil(t, stored)
		assert.False(t, stored.Active)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "tld_pricing.update", d.audit.Entries[0].Action)
	})

	t.Run("update propagates not-found", func(t *testing.T) {
		d := newFixture()
		d.tldPricing.GetByIDFn = func(ctx context.Context, id int64) (*domain.TLDPricing, error) {
			return nil, apperr.NotFound("tld pricing")
		}
		_, err := d.svc().UpdateTLDPricing(context.Background(), 1, 99, tldRequest(nil))
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("delete removes and audits", func(t *testing.T) {
		d := newFixture()
		d.tldPricing.GetByIDFn = func(ctx context.Context, id int64) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{ID: id, TLD: "com"}, nil
		}
		var deletedID int64
		d.tldPricing.DeleteFn = func(ctx context.Context, id int64) error {
			deletedID = id
			return nil
		}
		err := d.svc().DeleteTLDPricing(context.Background(), 9, 3)
		require.NoError(t, err)
		assert.EqualValues(t, 3, deletedID)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "tld_pricing.delete", d.audit.Entries[0].Action)
	})

	t.Run("delete propagates not-found", func(t *testing.T) {
		d := newFixture()
		d.tldPricing.GetByIDFn = func(ctx context.Context, id int64) (*domain.TLDPricing, error) {
			return nil, apperr.NotFound("tld pricing")
		}
		err := d.svc().DeleteTLDPricing(context.Background(), 1, 99)
		assertCode(t, err, apperr.CodeNotFound)
	})
}

// Admin - TLD pricing: import from registrar

func sampleCatalogPrices() []ports.RegistrarCatalogPrice {
	return []ports.RegistrarCatalogPrice{
		{
			Extension:      ".co.id",
			Currency:       "IDR",
			RegisterPrices: map[string]int64{"1": 100000, "2": 200000},
			RenewPrices:    map[string]int64{"1": 110000, "2": 220000},
			TransferPrice:  90000,
			RestorePrice:   500000,
		},
		{
			Extension:      ".my.id",
			Currency:       "IDR",
			RegisterPrices: map[string]int64{"1": 75000},
			RenewPrices:    map[string]int64{"1": 75000},
			TransferPrice:  70000,
			RestorePrice:   375000,
		},
	}
}

func TestListRegistrarCatalog(t *testing.T) {
	t.Run("flags already-configured TLDs", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			return &domain.Registrar{ID: id, Name: "rdash"}, nil
		}
		d.registrar.ListCatalogPricesFn = func(ctx context.Context) ([]ports.RegistrarCatalogPrice, error) {
			return sampleCatalogPrices(), nil
		}
		d.tldPricing.ListFn = func(ctx context.Context) ([]domain.TLDPricing, error) {
			return []domain.TLDPricing{{TLD: "co.id"}}, nil
		}

		list, err := d.svc().ListRegistrarCatalog(context.Background(), 1)
		require.NoError(t, err)
		require.Len(t, list, 2)
		assert.Equal(t, "co.id", list[0].TLD)
		assert.True(t, list[0].AlreadyConfigured)
		assert.Equal(t, int64(100000), list[0].RegisterPrices["1"])
		assert.Equal(t, "my.id", list[1].TLD)
		assert.False(t, list[1].AlreadyConfigured)
	})

	t.Run("unknown registrar propagates not-found", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			return nil, apperr.NotFound("registrar")
		}
		_, err := d.svc().ListRegistrarCatalog(context.Background(), 99)
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("registrar fetch error propagates", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			return &domain.Registrar{ID: id}, nil
		}
		d.registrar.ListCatalogPricesFn = func(ctx context.Context) ([]ports.RegistrarCatalogPrice, error) {
			return nil, errBoom
		}
		_, err := d.svc().ListRegistrarCatalog(context.Background(), 1)
		require.Error(t, err)
	})
}

func TestImportTLDPricing(t *testing.T) {
	t.Run("imports the selected unconfigured TLDs with markup applied, skips the rest", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			return &domain.Registrar{ID: id}, nil
		}
		d.registrar.ListCatalogPricesFn = func(ctx context.Context) ([]ports.RegistrarCatalogPrice, error) {
			return sampleCatalogPrices(), nil
		}
		d.tldPricing.ListFn = func(ctx context.Context) ([]domain.TLDPricing, error) {
			return []domain.TLDPricing{{TLD: "web.id"}}, nil // already configured, not in the catalog
		}
		var created []*domain.TLDPricing
		d.tldPricing.CreateFn = func(ctx context.Context, p *domain.TLDPricing) error {
			created = append(created, p)
			return nil
		}

		resp, err := d.svc().ImportTLDPricing(context.Background(), 9, domains.ImportTLDPricingRequest{
			RegistrarID:   1,
			TLDs:          []string{".co.id", ".web.id", ".unknown-tld"},
			MarkupPercent: 20,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"co.id"}, resp.Imported)
		assert.ElementsMatch(t, []string{"web.id", "unknown-tld"}, resp.Skipped)

		require.Len(t, created, 1)
		p := created[0]
		assert.Equal(t, "co.id", p.TLD)
		assert.EqualValues(t, 1, p.RegistrarID)
		assert.True(t, p.Active)
		assert.Equal(t, 1, p.MinYears)
		assert.Equal(t, 10, p.MaxYears)
		assert.Equal(t, int64(120000), p.RegisterPrices["1"]) // 100000 * 1.20
		assert.Equal(t, int64(240000), p.RegisterPrices["2"]) // 200000 * 1.20
		assert.Equal(t, int64(132000), p.RenewPrices["1"])    // 110000 * 1.20
		assert.Equal(t, int64(108000), p.TransferPrice)       // 90000 * 1.20
		assert.Equal(t, int64(600000), p.RestorePrice)        // 500000 * 1.20

		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "tld_pricing.import", d.audit.Entries[0].Action)
		assert.EqualValues(t, 9, d.audit.Entries[0].ActorUserID)
	})

	t.Run("zero markup imports at raw registrar cost", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			return &domain.Registrar{ID: id}, nil
		}
		d.registrar.ListCatalogPricesFn = func(ctx context.Context) ([]ports.RegistrarCatalogPrice, error) {
			return sampleCatalogPrices(), nil
		}
		var created []*domain.TLDPricing
		d.tldPricing.CreateFn = func(ctx context.Context, p *domain.TLDPricing) error {
			created = append(created, p)
			return nil
		}

		resp, err := d.svc().ImportTLDPricing(context.Background(), 1, domains.ImportTLDPricingRequest{
			RegistrarID: 1,
			TLDs:        []string{"my.id"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"my.id"}, resp.Imported)
		require.Len(t, created, 1)
		assert.Equal(t, int64(75000), created[0].RegisterPrices["1"])
	})

	t.Run("unknown registrar propagates not-found", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			return nil, apperr.NotFound("registrar")
		}
		_, err := d.svc().ImportTLDPricing(context.Background(), 1, domains.ImportTLDPricingRequest{
			RegistrarID: 99,
			TLDs:        []string{"com"},
		})
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("create failure propagates and stops the batch", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			return &domain.Registrar{ID: id}, nil
		}
		d.registrar.ListCatalogPricesFn = func(ctx context.Context) ([]ports.RegistrarCatalogPrice, error) {
			return sampleCatalogPrices(), nil
		}
		d.tldPricing.CreateFn = func(ctx context.Context, p *domain.TLDPricing) error {
			return errBoom
		}
		_, err := d.svc().ImportTLDPricing(context.Background(), 1, domains.ImportTLDPricingRequest{
			RegistrarID: 1,
			TLDs:        []string{"co.id"},
		})
		require.Error(t, err)
	})
}

// Admin - premium domain pricing

func premiumRequest(over func(*domains.PremiumDomainPricingRequest)) domains.PremiumDomainPricingRequest {
	req := domains.PremiumDomainPricingRequest{
		DomainName:    "Shop.ID",
		RegisterPrice: 5000000,
		RenewPrice:    5000000,
		TransferPrice: 3000000,
	}
	if over != nil {
		over(&req)
	}
	return req
}

func TestPremiumDomainPricingAdmin(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		d := newFixture()
		d.premiumPricing.ListFn = func(ctx context.Context) ([]domain.PremiumDomainPricing, error) {
			return []domain.PremiumDomainPricing{{ID: 1, DomainName: "shop.id"}}, nil
		}
		list, err := d.svc().ListPremiumPricing(context.Background())
		require.NoError(t, err)
		assert.Len(t, list, 1)
	})

	t.Run("create normalizes the domain name and audits", func(t *testing.T) {
		d := newFixture()
		var stored *domain.PremiumDomainPricing
		d.premiumPricing.CreateFn = func(ctx context.Context, p *domain.PremiumDomainPricing) error {
			p.ID = 4
			cp := *p
			stored = &cp
			return nil
		}
		p, err := d.svc().CreatePremiumPricing(context.Background(), 9, premiumRequest(nil))
		require.NoError(t, err)
		assert.Equal(t, "shop.id", p.DomainName)
		require.NotNil(t, stored)
		assert.Equal(t, "shop.id", stored.DomainName)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "premium_domain_pricing.create", d.audit.Entries[0].Action)
	})

	t.Run("create rejects an invalid domain name", func(t *testing.T) {
		d := newFixture()
		_, err := d.svc().CreatePremiumPricing(context.Background(), 1, premiumRequest(func(r *domains.PremiumDomainPricingRequest) {
			r.DomainName = "not a domain"
		}))
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("update persists and audits", func(t *testing.T) {
		d := newFixture()
		var stored *domain.PremiumDomainPricing
		d.premiumPricing.UpdateFn = func(ctx context.Context, p *domain.PremiumDomainPricing) error {
			cp := *p
			stored = &cp
			return nil
		}
		p, err := d.svc().UpdatePremiumPricing(context.Background(), 9, 4, premiumRequest(func(r *domains.PremiumDomainPricingRequest) {
			r.RegisterPrice = 6000000
		}))
		require.NoError(t, err)
		assert.EqualValues(t, 6000000, p.RegisterPrice)
		require.NotNil(t, stored)
		assert.EqualValues(t, 6000000, stored.RegisterPrice)
		require.Len(t, d.audit.Entries, 1)
	})

	t.Run("delete removes and audits", func(t *testing.T) {
		d := newFixture()
		var deletedID int64
		d.premiumPricing.DeleteFn = func(ctx context.Context, id int64) error {
			deletedID = id
			return nil
		}
		err := d.svc().DeletePremiumPricing(context.Background(), 9, 4)
		require.NoError(t, err)
		assert.EqualValues(t, 4, deletedID)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "premium_domain_pricing.delete", d.audit.Entries[0].Action)
	})
}

func lengthTierRequest(over func(*domains.PremiumLengthPricingRequest)) domains.PremiumLengthPricingRequest {
	req := domains.PremiumLengthPricingRequest{TLD: "id", CharLength: 2, Price: 485000000}
	if over != nil {
		over(&req)
	}
	return req
}

func TestPremiumLengthPricingAdmin(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		d := newFixture()
		d.premiumLengthPricing.ListFn = func(ctx context.Context) ([]domain.PremiumLengthPricing, error) {
			return []domain.PremiumLengthPricing{{ID: 1, TLD: "id", CharLength: 2, Price: 485000000}}, nil
		}
		list, err := d.svc().ListPremiumLengthPricing(context.Background())
		require.NoError(t, err)
		assert.Len(t, list, 1)
	})

	t.Run("create normalizes the tld and audits", func(t *testing.T) {
		d := newFixture()
		var stored *domain.PremiumLengthPricing
		d.premiumLengthPricing.CreateFn = func(ctx context.Context, p *domain.PremiumLengthPricing) error {
			p.ID = 7
			cp := *p
			stored = &cp
			return nil
		}
		p, err := d.svc().CreatePremiumLengthPricing(context.Background(), 9, lengthTierRequest(func(r *domains.PremiumLengthPricingRequest) {
			r.TLD = " .ID "
		}))
		require.NoError(t, err)
		assert.Equal(t, "id", p.TLD)
		assert.Equal(t, 2, p.CharLength)
		require.NotNil(t, stored)
		assert.EqualValues(t, 485000000, stored.Price)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "premium_length_pricing.create", d.audit.Entries[0].Action)
	})

	t.Run("create rejects a blank tld", func(t *testing.T) {
		d := newFixture()
		_, err := d.svc().CreatePremiumLengthPricing(context.Background(), 1, lengthTierRequest(func(r *domains.PremiumLengthPricingRequest) {
			r.TLD = "  "
		}))
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("update persists and audits", func(t *testing.T) {
		d := newFixture()
		var stored *domain.PremiumLengthPricing
		d.premiumLengthPricing.UpdateFn = func(ctx context.Context, p *domain.PremiumLengthPricing) error {
			cp := *p
			stored = &cp
			return nil
		}
		p, err := d.svc().UpdatePremiumLengthPricing(context.Background(), 9, 7, lengthTierRequest(func(r *domains.PremiumLengthPricingRequest) {
			r.Price = 500000000
		}))
		require.NoError(t, err)
		assert.EqualValues(t, 500000000, p.Price)
		require.NotNil(t, stored)
		assert.EqualValues(t, 500000000, stored.Price)
		require.Len(t, d.audit.Entries, 1)
	})

	t.Run("delete removes and audits", func(t *testing.T) {
		d := newFixture()
		var deletedID int64
		d.premiumLengthPricing.DeleteFn = func(ctx context.Context, id int64) error {
			deletedID = id
			return nil
		}
		err := d.svc().DeletePremiumLengthPricing(context.Background(), 9, 7)
		require.NoError(t, err)
		assert.EqualValues(t, 7, deletedID)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "premium_length_pricing.delete", d.audit.Entries[0].Action)
	})
}

// Admin - domain addons

func TestDomainAddonAdmin(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		d := newFixture()
		d.domainAddons.ListFn = func(ctx context.Context) ([]domain.DomainAddon, error) {
			return []domain.DomainAddon{{ID: 1, Key: "id_protection", Name: "ID Protection"}}, nil
		}
		list, err := d.svc().ListDomainAddons(context.Background())
		require.NoError(t, err)
		assert.Len(t, list, 1)
	})

	t.Run("update persists price/active and audits", func(t *testing.T) {
		d := newFixture()
		d.domainAddons.ListFn = func(ctx context.Context) ([]domain.DomainAddon, error) {
			return []domain.DomainAddon{
				{ID: 1, Key: "id_protection", Name: "ID Protection"},
				{ID: 2, Key: "dns_management", Name: "DNS Management"},
			}, nil
		}
		var stored *domain.DomainAddon
		d.domainAddons.UpdateFn = func(ctx context.Context, a *domain.DomainAddon) error {
			cp := *a
			stored = &cp
			return nil
		}
		a, err := d.svc().UpdateDomainAddon(context.Background(), 9, 2, domains.UpdateDomainAddonRequest{Price: 25000, Active: true})
		require.NoError(t, err)
		assert.EqualValues(t, 25000, a.Price)
		assert.True(t, a.Active)
		require.NotNil(t, stored)
		assert.EqualValues(t, 2, stored.ID)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain_addon.update", d.audit.Entries[0].Action)
	})

	t.Run("update unknown id is not found", func(t *testing.T) {
		d := newFixture()
		d.domainAddons.ListFn = func(ctx context.Context) ([]domain.DomainAddon, error) {
			return []domain.DomainAddon{{ID: 1, Key: "id_protection"}}, nil
		}
		_, err := d.svc().UpdateDomainAddon(context.Background(), 1, 99, domains.UpdateDomainAddonRequest{})
		assertCode(t, err, apperr.CodeNotFound)
	})
}

// Public - storefront TLD/addon lists

func TestListActiveTLDsAndAddons(t *testing.T) {
	d := newFixture()
	d.tldPricing.ListActiveFn = func(ctx context.Context) ([]domain.TLDPricing, error) {
		return []domain.TLDPricing{{ID: 1, TLD: "com", Active: true}}, nil
	}
	d.domainAddons.ListActiveFn = func(ctx context.Context) ([]domain.DomainAddon, error) {
		return []domain.DomainAddon{{ID: 1, Key: "id_protection", Active: true}}, nil
	}
	tlds, err := d.svc().ListActiveTLDs(context.Background())
	require.NoError(t, err)
	assert.Len(t, tlds, 1)
	addons, err := d.svc().ListActiveDomainAddons(context.Background())
	require.NoError(t, err)
	assert.Len(t, addons, 1)
}

// CheckAvailability pricing overlay

func TestCheckAvailabilityOverlay(t *testing.T) {
	t.Run("premium override wins and flags premium", func(t *testing.T) {
		d := newFixture()
		d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
			return []ports.DomainAvailability{{Name: "shop.id", Available: true, Price: 200000}}, nil
		}
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{TLD: "id", Active: true, RegisterPrices: map[string]int64{"1": 150000}}, nil
		}
		d.premiumPricing.GetByNameFn = func(ctx context.Context, name string) (*domain.PremiumDomainPricing, error) {
			assert.Equal(t, "shop.id", name)
			return &domain.PremiumDomainPricing{DomainName: name, RegisterPrice: 9000000}, nil
		}
		res, err := d.svc().CheckAvailability(context.Background(), []string{"shop.id"})
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.True(t, res[0].Premium)
		assert.True(t, res[0].Sellable)
		assert.EqualValues(t, 9000000, res[0].Price)
	})

	t.Run("premium length tier wins over standard TLD pricing (Dewabiz 2-char .id)", func(t *testing.T) {
		d := newFixture()
		d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
			return []ports.DomainAvailability{{Name: "ab.id", Available: true, Price: 150000}}, nil
		}
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{TLD: "id", Active: true, RegisterPrices: map[string]int64{"1": 150000}}, nil
		}
		d.premiumLengthPricing.GetByTLDAndLengthFn = func(ctx context.Context, tld string, charLength int) (*domain.PremiumLengthPricing, error) {
			assert.Equal(t, "id", tld)
			assert.Equal(t, 2, charLength)
			return &domain.PremiumLengthPricing{TLD: "id", CharLength: 2, Price: 485000000}, nil
		}
		res, err := d.svc().CheckAvailability(context.Background(), []string{"ab.id"})
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.True(t, res[0].Premium)
		assert.EqualValues(t, 485000000, res[0].Price)
	})

	t.Run("no matching length tier leaves standard TLD pricing untouched", func(t *testing.T) {
		d := newFixture()
		d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
			return []ports.DomainAvailability{{Name: "example.id", Available: true, Price: 200000}}, nil
		}
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{TLD: "id", Active: true, RegisterPrices: map[string]int64{"1": 150000}}, nil
		}
		res, err := d.svc().CheckAvailability(context.Background(), []string{"example.id"})
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.False(t, res[0].Premium)
		assert.EqualValues(t, 150000, res[0].Price)
	})

	t.Run("active TLD pricing overrides the registrar quote and exposes the full year matrix", func(t *testing.T) {
		d := newFixture()
		d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
			return []ports.DomainAvailability{{Name: "example.com", Available: true, Price: 200000}}, nil
		}
		d.premiumPricing.GetByNameFn = func(ctx context.Context, name string) (*domain.PremiumDomainPricing, error) {
			return nil, apperr.NotFound("premium domain pricing")
		}
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			assert.Equal(t, "com", tld)
			return &domain.TLDPricing{
				TLD: "com", Active: true,
				RegisterPrices: map[string]int64{"1": 150000, "2": 250000, "3": 300000},
			}, nil
		}
		res, err := d.svc().CheckAvailability(context.Background(), []string{"example.com"})
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.False(t, res[0].Premium)
		assert.EqualValues(t, 150000, res[0].Price)
		// The 3-year price (300000) is NOT 3x the 1-year price (150000) - the
		// caller must use this matrix instead of Price*years for a TLD-priced
		// registration, exactly the bug this field exists to prevent.
		assert.Equal(t, map[string]int64{"1": 150000, "2": 250000, "3": 300000}, res[0].RegisterPrices)
	})

	t.Run("premium override and length tier do not expose a per-year matrix (flat annual rate)", func(t *testing.T) {
		d := newFixture()
		d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
			return []ports.DomainAvailability{{Name: "shop.id", Available: true, Price: 200000}}, nil
		}
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{TLD: "id", Active: true, RegisterPrices: map[string]int64{"1": 150000}}, nil
		}
		d.premiumPricing.GetByNameFn = func(ctx context.Context, name string) (*domain.PremiumDomainPricing, error) {
			return &domain.PremiumDomainPricing{DomainName: name, RegisterPrice: 9000000}, nil
		}
		res, err := d.svc().CheckAvailability(context.Background(), []string{"shop.id"})
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Nil(t, res[0].RegisterPrices)
	})

	t.Run("an explicit zero year-1 register price (free-domain promo) overrides the registrar quote", func(t *testing.T) {
		d := newFixture()
		d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
			return []ports.DomainAvailability{{Name: "example.biz.id", Available: true, Price: 55000}}, nil
		}
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{TLD: "biz.id", Active: true, RegisterPrices: map[string]int64{"1": 0}}, nil
		}
		res, err := d.svc().CheckAvailability(context.Background(), []string{"example.biz.id"})
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.EqualValues(t, 0, res[0].Price)
	})

	t.Run("an extension without an ACTIVE pricing row is not sellable and shows no price", func(t *testing.T) {
		// The registrar's quote is the shop's cost: for an extension the shop
		// does not carry it must neither be shown as a price nor be bought.
		cases := map[string]func(context.Context, string) (*domain.TLDPricing, error){
			"inactive row": func(_ context.Context, _ string) (*domain.TLDPricing, error) {
				return &domain.TLDPricing{TLD: "net", Active: false, RegisterPrices: map[string]int64{"1": 150000}}, nil
			},
			"no row": func(_ context.Context, _ string) (*domain.TLDPricing, error) {
				return nil, apperr.NotFound("tld pricing")
			},
			"nil row": func(_ context.Context, _ string) (*domain.TLDPricing, error) { return nil, nil },
			"lookup error": func(_ context.Context, _ string) (*domain.TLDPricing, error) {
				return nil, errBoom
			},
		}
		for name, lookup := range cases {
			d := newFixture()
			d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
				return []ports.DomainAvailability{{Name: "example.net", Available: true, Price: 200000, Premium: true}}, nil
			}
			d.tldPricing.GetByTLDFn = lookup
			// premium rows must not make an unlisted extension sellable
			d.premiumPricing.GetByNameFn = func(ctx context.Context, name string) (*domain.PremiumDomainPricing, error) {
				return &domain.PremiumDomainPricing{DomainName: name, RegisterPrice: 9000000}, nil
			}
			res, err := d.svc().CheckAvailability(context.Background(), []string{"example.net"})
			require.NoError(t, err, name)
			require.Len(t, res, 1, name)
			assert.False(t, res[0].Sellable, name)
			assert.True(t, res[0].Available, "%s: availability is the registrar's truth", name)
			assert.Zero(t, res[0].Price, "%s: the registrar cost must not be exposed", name)
			assert.False(t, res[0].Premium, name)
			assert.Nil(t, res[0].RegisterPrices, name)
		}
	})

	t.Run("an active pricing row makes the extension sellable", func(t *testing.T) {
		d := newFixture()
		d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
			return []ports.DomainAvailability{{Name: "example.my.id", Available: true, Price: 1}}, nil
		}
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			assert.Equal(t, "my.id", tld)
			return &domain.TLDPricing{TLD: tld, Active: true, RegisterPrices: map[string]int64{"1": 25000}}, nil
		}
		res, err := d.svc().CheckAvailability(context.Background(), []string{"example.my.id"})
		require.NoError(t, err)
		assert.True(t, res[0].Sellable)
		assert.EqualValues(t, 25000, res[0].Price)
	})
}

// Client - UpdateDomainAddons

func TestUpdateDomainAddons(t *testing.T) {
	fullCatalog := []domain.DomainAddon{
		{ID: 1, Key: "id_protection", Price: 20000, Active: true},
		{ID: 2, Key: "dns_management", Price: 15000, Active: true},
		{ID: 3, Key: "email_forwarding", Price: 10000, Active: false},
	}

	t.Run("premium base plus selected addons", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Name = "shop.id" }))
		d.premiumPricing.GetByNameFn = func(ctx context.Context, name string) (*domain.PremiumDomainPricing, error) {
			return &domain.PremiumDomainPricing{DomainName: name, RenewPrice: 5000000}, nil
		}
		d.domainAddons.ListFn = func(ctx context.Context) ([]domain.DomainAddon, error) { return fullCatalog, nil }
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		got, err := d.svc().UpdateDomainAddons(context.Background(), 5, 10, []string{"id_protection", "dns_management"})
		require.NoError(t, err)
		assert.True(t, got.IDProtection)
		assert.True(t, got.DNSManagementEnabled)
		assert.False(t, got.EmailForwardingEnabled)
		assert.EqualValues(t, 5000000+20000+15000, got.RecurringAmount)
		require.NotNil(t, stored)
		assert.EqualValues(t, 5000000+20000+15000, stored.RecurringAmount)

		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain.addons_update", d.audit.Entries[0].Action)
		assert.EqualValues(t, 10, d.audit.Entries[0].EntityID)
	})

	t.Run("TLD base when no premium override", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Name = "example.com" }))
		d.premiumPricing.GetByNameFn = func(ctx context.Context, name string) (*domain.PremiumDomainPricing, error) {
			return nil, apperr.NotFound("premium domain pricing")
		}
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{TLD: "com", Active: true, RenewPrices: map[string]int64{"1": 160000}}, nil
		}
		d.domainAddons.ListFn = func(ctx context.Context) ([]domain.DomainAddon, error) { return fullCatalog, nil }
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error { return nil }
		got, err := d.svc().UpdateDomainAddons(context.Background(), 5, 10, []string{"dns_management"})
		require.NoError(t, err)
		assert.True(t, got.DNSManagementEnabled)
		assert.EqualValues(t, 160000+15000, got.RecurringAmount)
	})

	t.Run("premium length-tier base wins over standard TLD pricing when toggling addons", func(t *testing.T) {
		d := newFixture()
		// A 2-character .id domain - premium length-tier rule applies.
		stubGet(d, testDomain(func(x *domain.Domain) { x.Name = "ab.id" }))
		d.premiumPricing.GetByNameFn = func(ctx context.Context, name string) (*domain.PremiumDomainPricing, error) {
			return nil, apperr.NotFound("premium domain pricing")
		}
		d.premiumLengthPricing.GetByTLDAndLengthFn = func(ctx context.Context, tld string, charLength int) (*domain.PremiumLengthPricing, error) {
			assert.Equal(t, "id", tld)
			assert.Equal(t, 2, charLength)
			return &domain.PremiumLengthPricing{TLD: "id", CharLength: 2, Price: 485000000}, nil
		}
		// Even with a (much cheaper) standard TLD row configured, the
		// length-tier match must win - a client toggling an addon on this
		// premium domain must not have its price silently drop to the
		// standard .id rate.
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{TLD: "id", Active: true, RenewPrices: map[string]int64{"1": 160000}}, nil
		}
		d.domainAddons.ListFn = func(ctx context.Context) ([]domain.DomainAddon, error) { return fullCatalog, nil }
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		got, err := d.svc().UpdateDomainAddons(context.Background(), 5, 10, []string{"id_protection"})
		require.NoError(t, err)
		assert.True(t, got.IDProtection)
		assert.EqualValues(t, 485000000+20000, got.RecurringAmount)
		require.NotNil(t, stored)
		assert.EqualValues(t, 485000000+20000, stored.RecurringAmount)
	})

	t.Run("clearing all addons drops back to the base price", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) {
			x.Name, x.IDProtection, x.DNSManagementEnabled = "example.com", true, true
		}))
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{TLD: "com", Active: true, RenewPrices: map[string]int64{"1": 160000}}, nil
		}
		d.domainAddons.ListFn = func(ctx context.Context) ([]domain.DomainAddon, error) { return fullCatalog, nil }
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error { return nil }
		got, err := d.svc().UpdateDomainAddons(context.Background(), 5, 10, nil)
		require.NoError(t, err)
		assert.False(t, got.IDProtection)
		assert.False(t, got.DNSManagementEnabled)
		assert.EqualValues(t, 160000, got.RecurringAmount)
	})

	t.Run("rejects an unknown or inactive addon key", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Name = "example.com" }))
		d.tldPricing.GetByTLDFn = func(ctx context.Context, tld string) (*domain.TLDPricing, error) {
			return &domain.TLDPricing{TLD: "com", Active: true, RenewPrices: map[string]int64{"1": 160000}}, nil
		}
		d.domainAddons.ListFn = func(ctx context.Context) ([]domain.DomainAddon, error) { return fullCatalog, nil }
		_, err := d.svc().UpdateDomainAddons(context.Background(), 5, 10, []string{"free_lunch"})
		assertCode(t, err, apperr.CodeValidation)

		_, err = d.svc().UpdateDomainAddons(context.Background(), 5, 10, []string{"email_forwarding"})
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("falls back to the domain's current recurring amount when its TLD has no configured pricing", func(t *testing.T) {
		d := newFixture()
		// testDomain's default RecurringAmount is 150000; no addons active yet.
		stubGet(d, testDomain(func(x *domain.Domain) {
			x.Name, x.DNSManagementEnabled = "example.xyz", false
		}))
		d.domainAddons.ListFn = func(ctx context.Context) ([]domain.DomainAddon, error) { return fullCatalog, nil }
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		got, err := d.svc().UpdateDomainAddons(context.Background(), 5, 10, []string{"id_protection"})
		require.NoError(t, err)
		assert.True(t, got.IDProtection)
		assert.EqualValues(t, 150000+20000, got.RecurringAmount)
		require.NotNil(t, stored)
	})

	t.Run("fallback base excludes addons already active on the domain, so re-toggling doesn't double count", func(t *testing.T) {
		d := newFixture()
		// Recurring amount already includes id_protection's 20000.
		stubGet(d, testDomain(func(x *domain.Domain) {
			x.Name, x.IDProtection, x.DNSManagementEnabled, x.RecurringAmount = "example.xyz", true, false, 170000
		}))
		d.domainAddons.ListFn = func(ctx context.Context) ([]domain.DomainAddon, error) { return fullCatalog, nil }
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error { return nil }
		got, err := d.svc().UpdateDomainAddons(context.Background(), 5, 10, []string{"id_protection", "dns_management"})
		require.NoError(t, err)
		assert.EqualValues(t, 150000+20000+15000, got.RecurringAmount)
	})

	t.Run("other clients' domains are not found", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		_, err := d.svc().UpdateDomainAddons(context.Background(), 99, 10, nil)
		assertCode(t, err, apperr.CodeNotFound)
	})
}
