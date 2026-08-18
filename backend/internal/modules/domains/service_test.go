package domains_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	fixedNow = time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	errBoom  = errors.New("boom")
)

// deps bundles all mocks for one test.
type deps struct {
	tx                   *mocks.MockTxManager
	domains              *mocks.MockDomainRepo
	registrars           *mocks.MockRegistrarRepo
	tldPricing           *mocks.MockTLDPricingRepo
	premiumPricing       *mocks.MockPremiumDomainPricingRepo
	premiumLengthPricing *mocks.MockPremiumLengthPricingRepo
	domainAddons         *mocks.MockDomainAddonRepo
	clients              *mocks.MockClientRepo
	users                *mocks.MockUserRepo
	settings             *mocks.MockSettingsRepo
	registrar            *mocks.MockRegistrarModule
	enqueuer             *mocks.MockEnqueuer
	invoices             *mocks.MockInvoiceCreator
	renewalCheck         *mocks.MockRenewalInvoiceChecker
	notifier             *mocks.MockNotificationSender
	encryptor            *mocks.MockEncryptor
	audit                *mocks.MockAuditLogger
	clock                *mocks.MockClock
}

func newFixture() *deps {
	return &deps{
		tx:                   &mocks.MockTxManager{},
		domains:              &mocks.MockDomainRepo{},
		registrars:           &mocks.MockRegistrarRepo{},
		tldPricing:           &mocks.MockTLDPricingRepo{},
		premiumPricing:       &mocks.MockPremiumDomainPricingRepo{},
		premiumLengthPricing: &mocks.MockPremiumLengthPricingRepo{},
		domainAddons:         &mocks.MockDomainAddonRepo{},
		clients:              &mocks.MockClientRepo{},
		users:                &mocks.MockUserRepo{},
		settings:             &mocks.MockSettingsRepo{},
		registrar:            &mocks.MockRegistrarModule{},
		enqueuer:             &mocks.MockEnqueuer{},
		invoices:             &mocks.MockInvoiceCreator{},
		renewalCheck:         &mocks.MockRenewalInvoiceChecker{},
		notifier:             &mocks.MockNotificationSender{},
		encryptor:            &mocks.MockEncryptor{},
		audit:                &mocks.MockAuditLogger{},
		clock:                &mocks.MockClock{FixedTime: fixedNow},
	}
}

func (d *deps) svc() *domains.Service {
	return domains.New(domains.Deps{
		Tx:                   d.tx,
		Domains:              d.domains,
		Registrars:           d.registrars,
		TLDPricing:           d.tldPricing,
		PremiumPricing:       d.premiumPricing,
		PremiumLengthPricing: d.premiumLengthPricing,
		DomainAddons:         d.domainAddons,
		Clients:              d.clients,
		Users:                d.users,
		Settings:             d.settings,
		Registrar:            d.registrar,
		Enqueuer:             d.enqueuer,
		Invoices:             d.invoices,
		RenewalCheck:         d.renewalCheck,
		Notifier:             d.notifier,
		Encryptor:            d.encryptor,
		Audit:                d.audit,
		Clock:                d.clock,
		AllowPrivateBaseURL:  true,
	})
}

func testDomain(over func(*domain.Domain)) *domain.Domain {
	expiry := time.Date(2027, 7, 3, 0, 0, 0, 0, time.UTC)
	d := &domain.Domain{
		ID:                   10,
		ClientID:             5,
		RegistrarID:          1,
		Name:                 "example.com",
		Status:               domain.DomainActive,
		ExpiryDate:           &expiry,
		NextDueDate:          &expiry,
		RecurringAmount:      150000,
		BillingCycle:         domain.CycleAnnually,
		AutoRenew:            true,
		Nameservers:          json.RawMessage(`["ns1.example.net","ns2.example.net"]`),
		DNSManagementEnabled: true,
	}
	if over != nil {
		over(d)
	}
	return d
}

func stubGet(d *deps, dom *domain.Domain) {
	lookup := func(ctx context.Context, id int64) (*domain.Domain, error) {
		if dom != nil && id == dom.ID {
			return dom, nil
		}
		return nil, apperr.NotFound("domain")
	}
	d.domains.GetByIDFn = lookup
	d.domains.GetByIDForUpdateFn = lookup
}

func stubClientUser(d *deps) {
	d.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) {
		return &domain.Client{
			ID: id, UserID: 77, FirstName: "Budi", LastName: "Santoso",
			Company: "PT Test", Phone: "+628123456", Address1: "Jl. Melati 1",
			City: "Lamongan", State: "Jawa Timur", Postcode: "62211", Country: "ID",
		}, nil
	}
	d.users.GetByIDFn = func(ctx context.Context, id int64) (*domain.User, error) {
		return &domain.User{ID: id, Email: "budi@example.com"}, nil
	}
}

func assertCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, code, apperr.From(err).Code, "unexpected apperr code: %v", err)
}

// Syntax validation

func TestValidateDomainName(t *testing.T) {
	valid := []string{
		"example.com", "sub.example.co.id", "EXAMPLE.COM", "xn--nxasmq6b.com",
		"a-b.example.id", "example.com.", "bücher.de", "1域名.中国",
	}
	for _, name := range valid {
		assert.NoError(t, domains.ValidateDomainName(name), name)
	}

	invalid := []string{
		"", "example", "-bad.com", "bad-.com", "ex ample.com", "exa_mple.com",
		"example.c", "example..com", ".example.com",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.com", // 64-char label
	}
	for _, name := range invalid {
		assert.Error(t, domains.ValidateDomainName(name), name)
	}

	// Total length > 253.
	long := ""
	for range 70 {
		long += "abc."
	}
	assert.Error(t, domains.ValidateDomainName(long+"com"))
}

func TestValidateNameservers(t *testing.T) {
	_, err := domains.ValidateNameservers([]string{"ns1.host.com"})
	assertCode(t, err, apperr.CodeValidation)

	_, err = domains.ValidateNameservers([]string{"a.b.c", "d.e.f", "g.h.i", "j.k.l", "m.n.o"})
	assertCode(t, err, apperr.CodeValidation)

	_, err = domains.ValidateNameservers([]string{"ns1.host.com", "bad_host"})
	assertCode(t, err, apperr.CodeValidation)

	_, err = domains.ValidateNameservers([]string{"ns1.host.com", "NS1.host.com"})
	assertCode(t, err, apperr.CodeValidation) // duplicates after normalization

	got, err := domains.ValidateNameservers([]string{" NS1.Host.com ", "ns2.host.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ns1.host.com", "ns2.host.com"}, got)
}

func TestValidateRegistrarBaseURL(t *testing.T) {
	assert.NoError(t, domains.ValidateRegistrarBaseURL("", false))
	assert.NoError(t, domains.ValidateRegistrarBaseURL("https://api.dewabiz.co.id/v1", false))
	// A dev/test-only override may point at the mockserver on localhost.
	assert.NoError(t, domains.ValidateRegistrarBaseURL("http://localhost:9090/v1", true))
	assert.NoError(t, domains.ValidateRegistrarBaseURL("http://127.0.0.1:9090/v1", true))

	assertCode(t, domains.ValidateRegistrarBaseURL("not-a-url", true), apperr.CodeValidation)
	assertCode(t, domains.ValidateRegistrarBaseURL("ftp://example.com", true), apperr.CodeValidation)
	assertCode(t, domains.ValidateRegistrarBaseURL("http://", true), apperr.CodeValidation)

	// In production (allowPrivate=false), a loopback/private/link-local
	// override host is rejected - it would otherwise let a compromised admin
	// point authenticated outbound RDash calls at an internal service.
	assertCode(t, domains.ValidateRegistrarBaseURL("http://localhost:9090/v1", false), apperr.CodeValidation)
	assertCode(t, domains.ValidateRegistrarBaseURL("http://127.0.0.1:9090/v1", false), apperr.CodeValidation)
	assertCode(t, domains.ValidateRegistrarBaseURL("http://10.0.0.5/v1", false), apperr.CodeValidation)
	assertCode(t, domains.ValidateRegistrarBaseURL("http://169.254.169.254/latest/meta-data", false), apperr.CodeValidation)
}

// CheckAvailability

func TestCheckAvailability(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, err := newFixture().svc().CheckAvailability(context.Background(), nil)
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("too many", func(t *testing.T) {
		names := make([]string, 11)
		for i := range names {
			names[i] = "example.com"
		}
		_, err := newFixture().svc().CheckAvailability(context.Background(), names)
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("invalid syntax", func(t *testing.T) {
		_, err := newFixture().svc().CheckAvailability(context.Background(), []string{"not_a_domain"})
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("registrar error propagates", func(t *testing.T) {
		d := newFixture()
		d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
			return nil, apperr.External("rdash", errBoom)
		}
		_, err := d.svc().CheckAvailability(context.Background(), []string{"example.com"})
		assertCode(t, err, apperr.CodeExternal)
	})

	t.Run("ok normalizes names", func(t *testing.T) {
		d := newFixture()
		var got []string
		d.registrar.CheckAvailabilityFn = func(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
			got = names
			return []ports.DomainAvailability{{Name: names[0], Available: true, Price: 150000}}, nil
		}
		res, err := d.svc().CheckAvailability(context.Background(), []string{" Example.COM ", "toko.id"})
		require.NoError(t, err)
		assert.Equal(t, []string{"example.com", "toko.id"}, got)
		require.Len(t, res, 1)
		assert.True(t, res[0].Available)
	})
}

// Client reads / ownership

func TestGetForClient(t *testing.T) {
	d := newFixture()
	stubGet(d, testDomain(nil))
	svc := d.svc()

	_, err := svc.GetForClient(context.Background(), 5, 999)
	assertCode(t, err, apperr.CodeNotFound)

	// Other client's domain -> NOT_FOUND, never FORBIDDEN.
	_, err = svc.GetForClient(context.Background(), 6, 10)
	assertCode(t, err, apperr.CodeNotFound)

	dom, err := svc.GetForClient(context.Background(), 5, 10)
	require.NoError(t, err)
	assert.Equal(t, "example.com", dom.Name)
}

func TestListForClient(t *testing.T) {
	d := newFixture()
	d.domains.ListByClientFn = func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Domain, int64, error) {
		assert.EqualValues(t, 5, clientID)
		return []domain.Domain{*testDomain(nil)}, 1, nil
	}
	items, total, err := d.svc().ListForClient(context.Background(), 5, ports.ListParams{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Len(t, items, 1)
}

// Nameservers / DNS / EPP

func TestUpdateNameservers(t *testing.T) {
	ns := []string{"ns1.new.net", "ns2.new.net"}

	t.Run("not active", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainPending }))
		_, err := d.svc().UpdateNameservers(context.Background(), 5, 10, ns)
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("invalid count", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		_, err := d.svc().UpdateNameservers(context.Background(), 5, 10, []string{"ns1.new.net"})
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("registrar failure does not store", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.registrar.UpdateNameserversFn = func(ctx context.Context, name string, ns []string) error {
			return apperr.External("rdash", errBoom)
		}
		updated := false
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			updated = true
			return nil
		}
		_, err := d.svc().UpdateNameservers(context.Background(), 5, 10, ns)
		assertCode(t, err, apperr.CodeExternal)
		assert.False(t, updated)
	})

	t.Run("ok stores normalized hosts", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		var sentName string
		var sentNS []string
		d.registrar.UpdateNameserversFn = func(ctx context.Context, name string, ns []string) error {
			sentName, sentNS = name, ns
			return nil
		}
		var stored json.RawMessage
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			stored = dm.Nameservers
			return nil
		}
		dom, err := d.svc().UpdateNameservers(context.Background(), 5, 10, []string{"NS1.new.net", "ns2.new.net"})
		require.NoError(t, err)
		assert.Equal(t, "example.com", sentName)
		assert.Equal(t, []string{"ns1.new.net", "ns2.new.net"}, sentNS)
		assert.JSONEq(t, `["ns1.new.net","ns2.new.net"]`, string(stored))
		assert.NotNil(t, dom)

		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain.nameservers_update", d.audit.Entries[0].Action)
		assert.EqualValues(t, 10, d.audit.Entries[0].EntityID)
	})
}

func TestDNS(t *testing.T) {
	recs := []ports.DNSRecord{{Type: "A", Host: "@", Value: "1.2.3.4", TTL: 3600}}

	t.Run("get requires active", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainExpired }))
		_, err := d.svc().GetDNS(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("get ok", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.registrar.GetDNSRecordsFn = func(ctx context.Context, name string) ([]ports.DNSRecord, error) {
			assert.Equal(t, "example.com", name)
			return recs, nil
		}
		got, err := d.svc().GetDNS(context.Background(), 5, 10)
		require.NoError(t, err)
		assert.Equal(t, recs, got)
	})

	t.Run("update ok", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		var sent []ports.DNSRecord
		d.registrar.UpdateDNSRecordsFn = func(ctx context.Context, name string, r []ports.DNSRecord) error {
			sent = r
			return nil
		}
		got, err := d.svc().UpdateDNS(context.Background(), 5, 10, recs)
		require.NoError(t, err)
		assert.Equal(t, recs, sent)
		assert.Equal(t, recs, got)

		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain.dns_update", d.audit.Entries[0].Action)
	})

	t.Run("update registrar error", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.registrar.UpdateDNSRecordsFn = func(ctx context.Context, name string, r []ports.DNSRecord) error {
			return apperr.External("rdash", errBoom)
		}
		_, err := d.svc().UpdateDNS(context.Background(), 5, 10, recs)
		assertCode(t, err, apperr.CodeExternal)
	})

	t.Run("get requires the DNS Management add-on", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.DNSManagementEnabled = false }))
		_, err := d.svc().GetDNS(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodePaymentRequired)
	})

	t.Run("update requires the DNS Management add-on", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.DNSManagementEnabled = false }))
		_, err := d.svc().UpdateDNS(context.Background(), 5, 10, recs)
		assertCode(t, err, apperr.CodePaymentRequired)
	})
}

func TestGetEPP(t *testing.T) {
	t.Run("stored code decrypted", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.EPPCodeEnc = "enc:secret" }))
		d.encryptor.DecryptFn = func(ct string) (string, error) {
			assert.Equal(t, "enc:secret", ct)
			return "secret", nil
		}
		code, err := d.svc().GetEPP(context.Background(), 5, 10)
		require.NoError(t, err)
		assert.Equal(t, "secret", code)
	})

	t.Run("fetched from registrar when absent", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.registrar.GetEPPCodeFn = func(ctx context.Context, name string) (string, error) {
			return "live-code", nil
		}
		code, err := d.svc().GetEPP(context.Background(), 5, 10)
		require.NoError(t, err)
		assert.Equal(t, "live-code", code)
	})

	t.Run("requires active", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainPending }))
		_, err := d.svc().GetEPP(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("decrypt failure", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.EPPCodeEnc = "enc:bad" }))
		d.encryptor.DecryptFn = func(ct string) (string, error) { return "", errBoom }
		_, err := d.svc().GetEPP(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeInternal)
	})
}

func TestGetContact(t *testing.T) {
	t.Run("fetched live from registrar", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.registrar.GetContactFn = func(_ context.Context, name string) (*ports.RegistrantContact, error) {
			assert.Equal(t, "example.com", name)
			return &ports.RegistrantContact{FirstName: "Budi", Email: "budi@example.com"}, nil
		}
		contact, err := d.svc().GetContact(context.Background(), 5, 10)
		require.NoError(t, err)
		assert.Equal(t, "budi@example.com", contact.Email)
	})

	t.Run("requires active", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainPending }))
		_, err := d.svc().GetContact(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeConflict)
	})
}

// RenewNow (client-initiated renewal invoice)

func TestRenewNow(t *testing.T) {
	t.Run("wrong owner", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		_, err := d.svc().RenewNow(context.Background(), 99, 10)
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("pending not renewable", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainPending }))
		_, err := d.svc().RenewNow(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("no price", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.RecurringAmount = 0 }))
		_, err := d.svc().RenewNow(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("expired renewable, invoice built correctly", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainExpired }))
		d.settings.GetIntFn = func(ctx context.Context, key string, def int) (int, error) {
			assert.Equal(t, "billing.invoice_due_days", key)
			return 7, nil
		}
		var got ports.CreateInvoiceInput
		d.invoices.CreateInvoiceFn = func(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
			got = in
			return &domain.Invoice{ID: 42, Status: domain.InvoiceUnpaid, Total: in.Items[0].Amount}, nil
		}
		inv, err := d.svc().RenewNow(context.Background(), 5, 10)
		require.NoError(t, err)
		assert.EqualValues(t, 42, inv.ID)
		assert.EqualValues(t, 5, got.ClientID)

		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain.renew_now", d.audit.Entries[0].Action)
		assert.EqualValues(t, 10, d.audit.Entries[0].EntityID)
		require.Len(t, got.Items, 1)
		item := got.Items[0]
		assert.EqualValues(t, 150000, item.Amount)
		assert.True(t, item.Taxed)
		assert.Equal(t, domain.RelatedDomainRenewal, item.RelatedType)
		assert.EqualValues(t, 10, item.RelatedID)
		assert.Contains(t, item.Description, "example.com")
		assert.Equal(t, fixedNow.AddDate(0, 0, 7), got.DueDate)
	})

	t.Run("invoice creator failure propagates", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.invoices.CreateInvoiceFn = func(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
			return nil, apperr.Internal(errBoom)
		}
		_, err := d.svc().RenewNow(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeInternal)
	})

	// Regression: RenewNow used to have no dedupe guard at all - a second
	// call (double-click, or racing the nightly renewal-invoice cron) would
	// create a second invoice for the same domain. It must now reject
	// instead of duplicating.
	t.Run("rejects when an open renewal invoice already exists", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.renewalCheck.HasOpenRenewalInvoiceFn = func(_ context.Context, rt domain.InvoiceItemRelatedType, id int64) (bool, error) {
			assert.Equal(t, domain.RelatedDomainRenewal, rt)
			assert.EqualValues(t, 10, id)
			return true, nil
		}
		created := false
		d.invoices.CreateInvoiceFn = func(context.Context, ports.CreateInvoiceInput) (*domain.Invoice, error) {
			created = true
			return &domain.Invoice{ID: 1}, nil
		}
		_, err := d.svc().RenewNow(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeConflict)
		assert.False(t, created, "must not create a second invoice")
	})

	t.Run("open-invoice check failure propagates", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.renewalCheck.HasOpenRenewalInvoiceFn = func(context.Context, domain.InvoiceItemRelatedType, int64) (bool, error) {
			return false, errBoom
		}
		_, err := d.svc().RenewNow(context.Background(), 5, 10)
		assertCode(t, err, apperr.CodeInternal)
	})

	// Regression: the invoice description must reflect the billing cycle -
	// recurring_amount is a per-cycle price and RenewDomainJob renews
	// cycleYears(cycle) years, so a biennial domain must not say "1 year".
	t.Run("description matches cycle years", func(t *testing.T) {
		for _, tc := range []struct {
			cycle domain.BillingCycle
			want  string
		}{
			{domain.CycleAnnually, "(1 year)"},
			{domain.CycleBiennially, "(2 years)"},
			{domain.CycleMonthly, "(1 year)"}, // shorter cycles renew 1 year minimum
		} {
			d := newFixture()
			stubGet(d, testDomain(func(x *domain.Domain) { x.BillingCycle = tc.cycle }))
			var got ports.CreateInvoiceInput
			d.invoices.CreateInvoiceFn = func(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
				got = in
				return &domain.Invoice{ID: 1, Status: domain.InvoiceUnpaid}, nil
			}
			_, err := d.svc().RenewNow(context.Background(), 5, 10)
			require.NoError(t, err, tc.cycle)
			require.Len(t, got.Items, 1, tc.cycle)
			assert.Contains(t, got.Items[0].Description, tc.want, tc.cycle)
		}
	})
}

// UpdateDomain (auto_renew, contact)

func TestUpdateDomain(t *testing.T) {
	t.Run("nothing to update", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		_, err := d.svc().UpdateDomain(context.Background(), 5, 10, domains.UpdateDomainRequest{})
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("auto_renew toggle persists", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		var stored bool
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			stored = dm.AutoRenew
			return nil
		}
		off := false
		dom, err := d.svc().UpdateDomain(context.Background(), 5, 10, domains.UpdateDomainRequest{AutoRenew: &off})
		require.NoError(t, err)
		assert.False(t, stored)
		assert.False(t, dom.AutoRenew)

		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain.client_update", d.audit.Entries[0].Action)
	})

	t.Run("contact on non-active conflicts", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainExpired }))
		_, err := d.svc().UpdateDomain(context.Background(), 5, 10, domains.UpdateDomainRequest{
			Contact: &domains.ContactInput{FirstName: "A", LastName: "B", Email: "a@b.co", Country: "id"},
		})
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("contact pushed to registrar", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		var got ports.RegistrantContact
		d.registrar.UpdateContactFn = func(ctx context.Context, name string, c ports.RegistrantContact) error {
			got = c
			return nil
		}
		_, err := d.svc().UpdateDomain(context.Background(), 5, 10, domains.UpdateDomainRequest{
			Contact: &domains.ContactInput{FirstName: "Budi", LastName: "S", Email: "b@s.id", Country: "id"},
		})
		require.NoError(t, err)
		assert.Equal(t, "Budi", got.FirstName)
		assert.Equal(t, "ID", got.Country) // uppercased
	})
}

// ports.DomainRenewer

func TestRenewDomainAfterPayment(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		d := newFixture()
		stubGet(d, nil)
		err := d.svc().RenewDomainAfterPayment(context.Background(), 10)
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("enqueues renew job with cycle years", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.BillingCycle = domain.CycleBiennially }))
		err := d.svc().RenewDomainAfterPayment(context.Background(), 10)
		require.NoError(t, err)
		require.Len(t, d.enqueuer.Tasks, 1)
		assert.Equal(t, jobs.TypeDomainRenew, d.enqueuer.Tasks[0].TaskType)
		payload, ok := d.enqueuer.Tasks[0].Payload.(jobs.DomainRenewPayload)
		require.True(t, ok)
		assert.EqualValues(t, 10, payload.DomainID)
		assert.Equal(t, 2, payload.Years)
	})
}

// RegisterDomainJob

func TestRegisterDomainJob(t *testing.T) {
	t.Run("already active is idempotent", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		called := false
		d.registrar.RegisterFn = func(ctx context.Context, req ports.RegisterDomainRequest) (*ports.DomainResult, error) {
			called = true
			return nil, nil
		}
		require.NoError(t, d.svc().RegisterDomainJob(context.Background(), 10))
		assert.False(t, called)
	})

	t.Run("cancelled cannot register", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainCancelled }))
		err := d.svc().RegisterDomainJob(context.Background(), 10)
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("registrar failure leaves domain pending", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainPending }))
		stubClientUser(d)
		d.registrar.RegisterFn = func(ctx context.Context, req ports.RegisterDomainRequest) (*ports.DomainResult, error) {
			return nil, apperr.External("rdash", errBoom)
		}
		updated := false
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			updated = true
			return nil
		}
		err := d.svc().RegisterDomainJob(context.Background(), 10)
		assertCode(t, err, apperr.CodeExternal)
		assert.False(t, updated)
	})

	t.Run("success activates, stores dates and notifies", func(t *testing.T) {
		d := newFixture()
		dom := testDomain(func(x *domain.Domain) {
			x.Status = domain.DomainPending
			x.ExpiryDate, x.NextDueDate = nil, nil
		})
		stubGet(d, dom)
		stubClientUser(d)
		expiry := time.Date(2027, 7, 3, 0, 0, 0, 0, time.UTC)
		var req ports.RegisterDomainRequest
		d.registrar.RegisterFn = func(ctx context.Context, r ports.RegisterDomainRequest) (*ports.DomainResult, error) {
			req = r
			return &ports.DomainResult{Name: r.Name, Status: "active", ExpiryDate: expiry, OrderID: "RD-1"}, nil
		}
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		var notifiedUser int64
		var notifiedTpl string
		d.notifier.SendTemplateFn = func(ctx context.Context, userID int64, key string, data map[string]any) error {
			notifiedUser, notifiedTpl = userID, key
			assert.Equal(t, "example.com", data["Domain"])
			assert.Equal(t, "2027-07-03", data["ExpiryDate"])
			return nil
		}

		require.NoError(t, d.svc().RegisterDomainJob(context.Background(), 10))

		// Registrar request built from client profile + stored nameservers.
		assert.Equal(t, "example.com", req.Name)
		assert.Equal(t, 1, req.Years)
		assert.Equal(t, []string{"ns1.example.net", "ns2.example.net"}, req.NS)
		assert.Equal(t, "Budi", req.Contact.FirstName)
		assert.Equal(t, "budi@example.com", req.Contact.Email)
		assert.Equal(t, "ID", req.Contact.Country)

		require.NotNil(t, stored)
		assert.Equal(t, domain.DomainActive, stored.Status)
		require.NotNil(t, stored.RegistrationDate)
		assert.Equal(t, fixedNow, *stored.RegistrationDate)
		require.NotNil(t, stored.ExpiryDate)
		assert.Equal(t, expiry, *stored.ExpiryDate)
		assert.Equal(t, expiry, *stored.NextDueDate)
		assert.Contains(t, string(stored.RegistrarMeta), "RD-1")

		assert.EqualValues(t, 77, notifiedUser)
		assert.Equal(t, "domain_registered", notifiedTpl)
	})

	t.Run("empty nameservers fall back to registrar default_ns", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) {
			x.Status = domain.DomainPending
			x.Nameservers = nil
		}))
		stubClientUser(d)
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			return &domain.Registrar{ID: id, Name: "rdash",
				Config: json.RawMessage(`{"default_ns":["ns1.rdash.id","ns2.rdash.id"]}`)}, nil
		}
		var req ports.RegisterDomainRequest
		d.registrar.RegisterFn = func(ctx context.Context, r ports.RegisterDomainRequest) (*ports.DomainResult, error) {
			req = r
			return &ports.DomainResult{ExpiryDate: fixedNow.AddDate(1, 0, 0)}, nil
		}
		require.NoError(t, d.svc().RegisterDomainJob(context.Background(), 10))
		assert.Equal(t, []string{"ns1.rdash.id", "ns2.rdash.id"}, req.NS)
	})

	t.Run("notification failure does not fail the job", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainPending }))
		stubClientUser(d)
		d.registrar.RegisterFn = func(ctx context.Context, r ports.RegisterDomainRequest) (*ports.DomainResult, error) {
			return &ports.DomainResult{ExpiryDate: fixedNow.AddDate(1, 0, 0)}, nil
		}
		d.notifier.SendTemplateFn = func(ctx context.Context, userID int64, key string, data map[string]any) error {
			return errBoom
		}
		assert.NoError(t, d.svc().RegisterDomainJob(context.Background(), 10))
	})
}

// TransferDomainJob

func TestTransferDomainJob(t *testing.T) {
	t.Run("already active is idempotent", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		require.NoError(t, d.svc().TransferDomainJob(context.Background(), 10))
	})

	t.Run("missing epp code conflicts", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainPendingTransfer }))
		err := d.svc().TransferDomainJob(context.Background(), 10)
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("expired cannot transfer", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainExpired }))
		err := d.svc().TransferDomainJob(context.Background(), 10)
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("success decrypts epp and activates", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) {
			x.Status = domain.DomainPendingTransfer
			x.EPPCodeEnc = "enc:epp-secret"
		}))
		stubClientUser(d)
		d.encryptor.DecryptFn = func(ct string) (string, error) { return "epp-secret", nil }
		var req ports.TransferDomainRequest
		d.registrar.TransferFn = func(ctx context.Context, r ports.TransferDomainRequest) (*ports.DomainResult, error) {
			req = r
			return &ports.DomainResult{ExpiryDate: fixedNow.AddDate(1, 0, 0), OrderID: "RD-2"}, nil
		}
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		require.NoError(t, d.svc().TransferDomainJob(context.Background(), 10))
		assert.Equal(t, "epp-secret", req.EPPCode)
		require.NotNil(t, stored)
		assert.Equal(t, domain.DomainActive, stored.Status)
	})

	t.Run("registrar failure propagates", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) {
			x.Status = domain.DomainPending
			x.EPPCodeEnc = "enc:x"
		}))
		stubClientUser(d)
		d.registrar.TransferFn = func(ctx context.Context, r ports.TransferDomainRequest) (*ports.DomainResult, error) {
			return nil, apperr.External("rdash", errBoom)
		}
		err := d.svc().TransferDomainJob(context.Background(), 10)
		assertCode(t, err, apperr.CodeExternal)
	})
}

// RenewDomainJob

func TestRenewDomainJob(t *testing.T) {
	t.Run("registrar failure propagates", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.registrar.RenewFn = func(ctx context.Context, name string, years int) (*ports.DomainResult, error) {
			return nil, apperr.External("rdash", errBoom)
		}
		err := d.svc().RenewDomainJob(context.Background(), 10)
		assertCode(t, err, apperr.CodeExternal)
	})

	t.Run("advances expiry and next due, notifies", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		stubClientUser(d)
		newExpiry := time.Date(2028, 7, 3, 0, 0, 0, 0, time.UTC)
		d.registrar.RenewFn = func(ctx context.Context, name string, years int) (*ports.DomainResult, error) {
			assert.Equal(t, "example.com", name)
			assert.Equal(t, 1, years)
			return &ports.DomainResult{ExpiryDate: newExpiry}, nil
		}
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		var tpl string
		d.notifier.SendTemplateFn = func(ctx context.Context, userID int64, key string, data map[string]any) error {
			tpl = key
			return nil
		}
		require.NoError(t, d.svc().RenewDomainJob(context.Background(), 10))
		require.NotNil(t, stored)
		assert.Equal(t, newExpiry, *stored.ExpiryDate)
		assert.Equal(t, newExpiry, *stored.NextDueDate)
		assert.Equal(t, domain.DomainActive, stored.Status)
		assert.Equal(t, "domain_renewed", tpl)
	})

	t.Run("expired domain reactivates", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainExpired }))
		stubClientUser(d)
		d.registrar.RenewFn = func(ctx context.Context, name string, years int) (*ports.DomainResult, error) {
			return &ports.DomainResult{ExpiryDate: fixedNow.AddDate(1, 0, 0)}, nil
		}
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		require.NoError(t, d.svc().RenewDomainJob(context.Background(), 10))
		assert.Equal(t, domain.DomainActive, stored.Status)
	})

	t.Run("zero result expiry falls back to old expiry + years", func(t *testing.T) {
		d := newFixture()
		oldExpiry := time.Date(2027, 7, 3, 0, 0, 0, 0, time.UTC)
		stubGet(d, testDomain(func(x *domain.Domain) { x.ExpiryDate = &oldExpiry }))
		stubClientUser(d)
		d.registrar.RenewFn = func(ctx context.Context, name string, years int) (*ports.DomainResult, error) {
			return &ports.DomainResult{}, nil
		}
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		require.NoError(t, d.svc().RenewDomainJob(context.Background(), 10))
		assert.Equal(t, oldExpiry.AddDate(1, 0, 0), *stored.ExpiryDate)
	})
}

// SyncDomainJob / SyncAllDomains

func TestSyncDomainJob(t *testing.T) {
	t.Run("applies expired status, expiry and ns", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		expiry := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		d.registrar.SyncDomainFn = func(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
			return &ports.DomainSyncInfo{Status: "Expired", ExpiryDate: expiry,
				NS: []string{"ns9.x.id", "ns10.x.id"}}, nil
		}
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
		assert.Equal(t, domain.DomainExpired, stored.Status)
		assert.Equal(t, expiry, *stored.ExpiryDate)
		assert.JSONEq(t, `["ns9.x.id","ns10.x.id"]`, string(stored.Nameservers))
	})

	t.Run("illegal transition skipped", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil)) // active
		d.registrar.SyncDomainFn = func(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
			return &ports.DomainSyncInfo{Status: "pending"}, nil
		}
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
		assert.Equal(t, domain.DomainActive, stored.Status) // unchanged
	})

	t.Run("pending transfer completes to active", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(func(x *domain.Domain) { x.Status = domain.DomainPendingTransfer }))
		d.registrar.SyncDomainFn = func(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
			return &ports.DomainSyncInfo{Status: "active"}, nil
		}
		var stored *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, dm *domain.Domain) error {
			cp := *dm
			stored = &cp
			return nil
		}
		require.NoError(t, d.svc().SyncDomainJob(context.Background(), 10))
		assert.Equal(t, domain.DomainActive, stored.Status)
	})

	t.Run("registrar failure propagates", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.registrar.SyncDomainFn = func(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
			return nil, apperr.External("rdash", errBoom)
		}
		err := d.svc().SyncDomainJob(context.Background(), 10)
		assertCode(t, err, apperr.CodeExternal)
	})
}

func TestSyncAllDomains(t *testing.T) {
	d := newFixture()
	a := testDomain(func(x *domain.Domain) { x.ID = 1; x.Name = "a.com" })
	b := testDomain(func(x *domain.Domain) { x.ID = 2; x.Name = "b.com" })
	d.domains.ListForSyncFn = func(ctx context.Context, limit int) ([]domain.Domain, error) {
		assert.Positive(t, limit)
		return []domain.Domain{*a, *b}, nil
	}
	d.domains.GetByIDFn = func(ctx context.Context, id int64) (*domain.Domain, error) {
		if id == 1 {
			return a, nil
		}
		return b, nil
	}
	d.registrar.SyncDomainFn = func(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
		if name == "a.com" {
			return nil, apperr.External("rdash", errBoom) // one failure is skipped
		}
		return &ports.DomainSyncInfo{Status: "active"}, nil
	}
	count, err := d.svc().SyncAllDomains(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	t.Run("list failure propagates", func(t *testing.T) {
		d := newFixture()
		d.domains.ListForSyncFn = func(ctx context.Context, limit int) ([]domain.Domain, error) {
			return nil, errBoom
		}
		_, err := d.svc().SyncAllDomains(context.Background())
		assert.Error(t, err)
	})
}

// Admin

func TestAdminSync(t *testing.T) {
	d := newFixture()
	stubGet(d, testDomain(nil))
	d.registrar.SyncDomainFn = func(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
		return &ports.DomainSyncInfo{Status: "active"}, nil
	}
	dom, err := d.svc().AdminSync(context.Background(), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, "example.com", dom.Name)
	require.Len(t, d.audit.Entries, 1)
	assert.Equal(t, "domain.sync", d.audit.Entries[0].Action)
	assert.EqualValues(t, 10, d.audit.Entries[0].EntityID)
}

func TestAdminForceRenew(t *testing.T) {
	d := newFixture()
	stubGet(d, testDomain(nil))
	require.NoError(t, d.svc().AdminForceRenew(context.Background(), 1, 10))
	require.Len(t, d.enqueuer.Tasks, 1)
	assert.Equal(t, jobs.TypeDomainRenew, d.enqueuer.Tasks[0].TaskType)
	require.Len(t, d.audit.Entries, 1)
	assert.Equal(t, "domain.force_renew", d.audit.Entries[0].Action)

	t.Run("not found", func(t *testing.T) {
		d := newFixture()
		stubGet(d, nil)
		err := d.svc().AdminForceRenew(context.Background(), 1, 10)
		assertCode(t, err, apperr.CodeNotFound)
	})
}

func TestAdminUpdate(t *testing.T) {
	t.Run("partial update: auto_renew + no-op status", func(t *testing.T) {
		d := newFixture()
		dom := testDomain(nil) // status active
		stubGet(d, dom)
		var updated *domain.Domain
		d.domains.UpdateFn = func(ctx context.Context, x *domain.Domain) error {
			updated = x
			return nil
		}
		autoRenew := false
		status := "active" // same as current: no-op, not a transition error
		got, err := d.svc().AdminUpdate(context.Background(), 1, 10, domains.AdminUpdateDomainRequest{
			AutoRenew: &autoRenew,
			Status:    &status,
		})
		require.NoError(t, err)
		assert.False(t, got.AutoRenew)
		assert.Equal(t, domain.DomainActive, got.Status)
		require.NotNil(t, updated)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain.update", d.audit.Entries[0].Action)
		assert.EqualValues(t, 10, d.audit.Entries[0].EntityID)
	})

	t.Run("nameservers replaced", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		d.domains.UpdateFn = func(ctx context.Context, x *domain.Domain) error { return nil }
		got, err := d.svc().AdminUpdate(context.Background(), 1, 10, domains.AdminUpdateDomainRequest{
			Nameservers: []string{"ns1.new.com", "ns2.new.com"},
		})
		require.NoError(t, err)
		var ns []string
		require.NoError(t, json.Unmarshal(got.Nameservers, &ns))
		assert.Equal(t, []string{"ns1.new.com", "ns2.new.com"}, ns)
	})

	t.Run("invalid nameserver count", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		_, err := d.svc().AdminUpdate(context.Background(), 1, 10, domains.AdminUpdateDomainRequest{
			Nameservers: []string{"ns1.new.com"},
		})
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("invalid state transition", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil)) // active
		status := "pending"
		_, err := d.svc().AdminUpdate(context.Background(), 1, 10, domains.AdminUpdateDomainRequest{
			Status: &status,
		})
		assertCode(t, err, apperr.CodeConflict)
	})

	t.Run("not found", func(t *testing.T) {
		d := newFixture()
		stubGet(d, nil)
		autoRenew := true
		_, err := d.svc().AdminUpdate(context.Background(), 1, 10, domains.AdminUpdateDomainRequest{AutoRenew: &autoRenew})
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("nothing to update", func(t *testing.T) {
		d := newFixture()
		stubGet(d, testDomain(nil))
		_, err := d.svc().AdminUpdate(context.Background(), 1, 10, domains.AdminUpdateDomainRequest{})
		assertCode(t, err, apperr.CodeValidation)
	})
}

func TestAdminCreate(t *testing.T) {
	stubRegistrar := func(d *deps) {
		d.registrars.GetByNameFn = func(_ context.Context, name string) (*domain.Registrar, error) {
			assert.Equal(t, "rdash", name)
			return &domain.Registrar{ID: 1, Name: "rdash"}, nil
		}
	}
	baseReq := func() domains.AdminCreateDomainRequest {
		return domains.AdminCreateDomainRequest{
			ClientID:         5,
			Name:             "Imported-Example.COM",
			RegistrationDate: "2024-03-01",
			ExpiryDate:       "2027-03-01",
			NextDueDate:      "2027-03-01",
			RecurringAmount:  180_000,
			Nameservers:      []string{"ns1.example.net", "ns2.example.net"},
		}
	}

	t.Run("happy path: active row, no registrar call, audited", func(t *testing.T) {
		d := newFixture()
		stubClientUser(d)
		stubRegistrar(d)
		var created *domain.Domain
		d.domains.CreateFn = func(_ context.Context, x *domain.Domain) error {
			x.ID = 33
			created = x
			return nil
		}
		d.registrar.RegisterFn = func(context.Context, ports.RegisterDomainRequest) (*ports.DomainResult, error) {
			t.Fatal("adding an existing domain must never call the registrar")
			return nil, nil
		}
		d.enqueuer.EnqueueFn = func(_ context.Context, taskType string, _ any, _ ...ports.JobOption) error {
			t.Fatalf("adding an existing domain must not enqueue jobs (got %s)", taskType)
			return nil
		}

		got, err := d.svc().AdminCreate(context.Background(), 1, baseReq())
		require.NoError(t, err)
		require.NotNil(t, created)
		assert.Equal(t, int64(33), got.ID)
		assert.Equal(t, "imported-example.com", created.Name, "name is normalized")
		assert.Equal(t, domain.DomainActive, created.Status)
		assert.Equal(t, int64(1), created.RegistrarID)
		assert.Equal(t, domain.CycleAnnually, created.BillingCycle, "defaults to annually")
		assert.True(t, created.AutoRenew, "defaults to auto-renew on")
		require.NotNil(t, created.NextDueDate)
		assert.Equal(t, "2027-03-01", created.NextDueDate.Format("2006-01-02"))
		var ns []string
		require.NoError(t, json.Unmarshal(created.Nameservers, &ns))
		assert.Equal(t, []string{"ns1.example.net", "ns2.example.net"}, ns)
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "domain.admin_create", d.audit.Entries[0].Action)
	})

	t.Run("invalid name", func(t *testing.T) {
		d := newFixture()
		req := baseReq()
		req.Name = "not_a_domain"
		_, err := d.svc().AdminCreate(context.Background(), 1, req)
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("invalid nameservers", func(t *testing.T) {
		d := newFixture()
		stubClientUser(d)
		stubRegistrar(d)
		req := baseReq()
		req.Nameservers = []string{"only-one.example.net"}
		_, err := d.svc().AdminCreate(context.Background(), 1, req)
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("unknown client", func(t *testing.T) {
		d := newFixture()
		d.clients.GetByIDFn = func(context.Context, int64) (*domain.Client, error) {
			return nil, apperr.NotFound("client")
		}
		_, err := d.svc().AdminCreate(context.Background(), 1, baseReq())
		assertCode(t, err, apperr.CodeNotFound)
	})

	t.Run("duplicate name surfaces the repo CONFLICT", func(t *testing.T) {
		d := newFixture()
		stubClientUser(d)
		stubRegistrar(d)
		d.domains.CreateFn = func(context.Context, *domain.Domain) error {
			return apperr.Conflict("domain imported-example.com already exists")
		}
		_, err := d.svc().AdminCreate(context.Background(), 1, baseReq())
		assertCode(t, err, apperr.CodeConflict)
	})
}

func TestAdminUpdateBillingFields(t *testing.T) {
	d := newFixture()
	stubGet(d, testDomain(nil))
	d.domains.UpdateFn = func(ctx context.Context, x *domain.Domain) error { return nil }

	amount := int64(275_000)
	cycle := "annually"
	regDate := "2024-03-01"
	expiry := "2028-03-01"
	nextDue := "2028-03-01"
	got, err := d.svc().AdminUpdate(context.Background(), 1, 10, domains.AdminUpdateDomainRequest{
		RecurringAmount:  &amount,
		BillingCycle:     &cycle,
		RegistrationDate: &regDate,
		ExpiryDate:       &expiry,
		NextDueDate:      &nextDue,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(275_000), got.RecurringAmount)
	assert.Equal(t, domain.CycleAnnually, got.BillingCycle)
	require.NotNil(t, got.ExpiryDate)
	assert.Equal(t, "2028-03-01", got.ExpiryDate.Format("2006-01-02"))
	require.NotNil(t, got.NextDueDate)
	assert.Equal(t, "2028-03-01", got.NextDueDate.Format("2006-01-02"))
	require.NotNil(t, got.RegistrationDate)
	assert.Equal(t, "2024-03-01", got.RegistrationDate.Format("2006-01-02"))

	// An explicit empty date clears the stored value.
	clear := ""
	got, err = d.svc().AdminUpdate(context.Background(), 1, 10, domains.AdminUpdateDomainRequest{
		ExpiryDate: &clear,
	})
	require.NoError(t, err)
	assert.Nil(t, got.ExpiryDate)
}

func TestAdminListAndGet(t *testing.T) {
	d := newFixture()
	d.domains.ListFn = func(ctx context.Context, p ports.ListParams) ([]domain.Domain, int64, error) {
		return []domain.Domain{*testDomain(nil)}, 1, nil
	}
	items, total, err := d.svc().AdminList(context.Background(), ports.ListParams{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Len(t, items, 1)

	stubGet(d, testDomain(nil))
	dom, err := d.svc().AdminGet(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, "example.com", dom.Name)
}

func TestRegistrarAPIKeyPresent(t *testing.T) {
	for _, present := range []bool{true, false} {
		svc := domains.New(domains.Deps{RegistrarAPIKeyPresent: present})
		assert.Equal(t, present, svc.RegistrarAPIKeyPresent())
	}
}

func TestRegistrarAdmin(t *testing.T) {
	rdash := &domain.Registrar{ID: 1, Name: "rdash", Active: true, Config: json.RawMessage(`{}`)}

	t.Run("list and get", func(t *testing.T) {
		d := newFixture()
		d.registrars.ListFn = func(ctx context.Context) ([]domain.Registrar, error) {
			return []domain.Registrar{*rdash}, nil
		}
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			cp := *rdash
			return &cp, nil
		}
		regs, err := d.svc().ListRegistrars(context.Background())
		require.NoError(t, err)
		assert.Len(t, regs, 1)
		reg, err := d.svc().GetRegistrar(context.Background(), 1)
		require.NoError(t, err)
		assert.Equal(t, "rdash", reg.Name)
	})

	t.Run("update nothing", func(t *testing.T) {
		d := newFixture()
		_, err := d.svc().UpdateRegistrar(context.Background(), 1, 1, domains.UpdateRegistrarRequest{})
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("update nothing rejects even with only credential fields absent", func(t *testing.T) {
		// Guard must consider Active/Config/ResellerID/APIKey/BaseURL together,
		// not just the original Active/Config pair.
		d := newFixture()
		_, err := d.svc().UpdateRegistrar(context.Background(), 1, 1, domains.UpdateRegistrarRequest{
			Active: nil, Config: nil, ResellerID: nil, APIKey: nil, BaseURL: nil,
		})
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("update persists and audits", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			cp := *rdash
			return &cp, nil
		}
		var stored *domain.Registrar
		d.registrars.UpdateFn = func(ctx context.Context, r *domain.Registrar) error {
			cp := *r
			stored = &cp
			return nil
		}
		off := false
		reg, err := d.svc().UpdateRegistrar(context.Background(), 9, 1, domains.UpdateRegistrarRequest{
			Active: &off,
			Config: map[string]any{"default_ns": []string{"ns1.rdash.id", "ns2.rdash.id"}},
		})
		require.NoError(t, err)
		assert.False(t, reg.Active)
		require.NotNil(t, stored)
		assert.False(t, stored.Active)
		assert.Contains(t, string(stored.Config), "ns1.rdash.id")
		require.Len(t, d.audit.Entries, 1)
		assert.Equal(t, "registrar.update", d.audit.Entries[0].Action)
		assert.EqualValues(t, 9, d.audit.Entries[0].ActorUserID)
	})

	t.Run("reseller id updates in place", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			cp := *rdash
			return &cp, nil
		}
		var stored *domain.Registrar
		d.registrars.UpdateFn = func(ctx context.Context, r *domain.Registrar) error {
			cp := *r
			stored = &cp
			return nil
		}
		resellerID := "584"
		reg, err := d.svc().UpdateRegistrar(context.Background(), 1, 1, domains.UpdateRegistrarRequest{ResellerID: &resellerID})
		require.NoError(t, err)
		assert.Equal(t, "584", reg.ResellerID)
		assert.Equal(t, "584", stored.ResellerID)
	})

	t.Run("api key encrypted on save, never touched when nil, cleared by explicit empty string", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			cp := *rdash
			cp.APIKeyEnc = "existing-ciphertext"
			return &cp, nil
		}
		var stored *domain.Registrar
		d.registrars.UpdateFn = func(ctx context.Context, r *domain.Registrar) error {
			cp := *r
			stored = &cp
			return nil
		}
		d.encryptor.EncryptFn = func(pt string) (string, error) { return "enc:" + pt, nil }

		// nil APIKey: existing ciphertext untouched.
		active := true
		_, err := d.svc().UpdateRegistrar(context.Background(), 1, 1, domains.UpdateRegistrarRequest{Active: &active})
		require.NoError(t, err)
		assert.Equal(t, "existing-ciphertext", stored.APIKeyEnc)

		// Non-empty APIKey: encrypted and stored.
		newKey := "UBJZusY7HHxaEdaUyTFhykGUEGEHwaXN"
		_, err = d.svc().UpdateRegistrar(context.Background(), 1, 1, domains.UpdateRegistrarRequest{APIKey: &newKey})
		require.NoError(t, err)
		assert.Equal(t, "enc:"+newKey, stored.APIKeyEnc)

		// Explicit empty string: clears the stored key.
		empty := ""
		_, err = d.svc().UpdateRegistrar(context.Background(), 1, 1, domains.UpdateRegistrarRequest{APIKey: &empty})
		require.NoError(t, err)
		assert.Equal(t, "", stored.APIKeyEnc)
	})

	t.Run("base url (custom endpoint) updates in place, blank reverts to env fallback", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			cp := *rdash
			return &cp, nil
		}
		var stored *domain.Registrar
		d.registrars.UpdateFn = func(ctx context.Context, r *domain.Registrar) error {
			cp := *r
			stored = &cp
			return nil
		}

		custom := "http://localhost:9090/v1"
		reg, err := d.svc().UpdateRegistrar(context.Background(), 1, 1, domains.UpdateRegistrarRequest{BaseURL: &custom})
		require.NoError(t, err)
		assert.Equal(t, custom, reg.BaseURL)
		assert.Equal(t, custom, stored.BaseURL)

		blank := ""
		reg, err = d.svc().UpdateRegistrar(context.Background(), 1, 1, domains.UpdateRegistrarRequest{BaseURL: &blank})
		require.NoError(t, err)
		assert.Equal(t, "", reg.BaseURL)
		assert.Equal(t, "", stored.BaseURL)
	})

	t.Run("base url rejects a malformed override", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			cp := *rdash
			return &cp, nil
		}
		bad := "not-a-url"
		_, err := d.svc().UpdateRegistrar(context.Background(), 1, 1, domains.UpdateRegistrarRequest{BaseURL: &bad})
		assertCode(t, err, apperr.CodeValidation)
	})

	t.Run("api key encrypt failure surfaces as internal error", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			cp := *rdash
			return &cp, nil
		}
		d.encryptor.EncryptFn = func(string) (string, error) { return "", errBoom }
		newKey := "some-key"
		_, err := d.svc().UpdateRegistrar(context.Background(), 1, 1, domains.UpdateRegistrarRequest{APIKey: &newKey})
		assertCode(t, err, apperr.CodeInternal)
	})

	t.Run("test ok and failing", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			return rdash, nil
		}
		require.NoError(t, d.svc().TestRegistrar(context.Background(), 1))

		d.registrar.AccountInfoFn = func(ctx context.Context) (*ports.RegistrarAccountInfo, error) {
			return nil, apperr.External("rdash", errBoom)
		}
		assertCode(t, d.svc().TestRegistrar(context.Background(), 1), apperr.CodeExternal)
	})

	t.Run("test unknown registrar", func(t *testing.T) {
		d := newFixture()
		d.registrars.GetByIDFn = func(ctx context.Context, id int64) (*domain.Registrar, error) {
			return nil, apperr.NotFound("registrar")
		}
		assertCode(t, d.svc().TestRegistrar(context.Background(), 99), apperr.CodeNotFound)
	})
}
