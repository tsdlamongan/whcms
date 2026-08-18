// Package domains implements the M-DOMAINS module: public availability
// checks, client domain management (nameservers, DNS, EPP, renewals,
// auto-renew/contact, addons), registrar job methods consumed by the worker
// (register/transfer/renew/sync) and admin domain/registrar/pricing/addon
// endpoints.
//
// Route table (RegisterRoutes):
//
//	POST   /domains/check                       public, rate-limited 20/min/IP
//	GET    /domains/tlds                        public - sellable TLDs + years bounds
//	GET    /domains/addons                      public - sellable domain addons
//	GET    /domains                             client
//	GET    /domains/:id                         client
//	PATCH  /domains/:id                         client (auto_renew, contact)
//	PATCH  /domains/:id/nameservers             client
//	GET    /domains/:id/dns                     client (requires the DNS Management add-on)
//	PUT    /domains/:id/dns                     client (requires the DNS Management add-on)
//	GET    /domains/:id/epp                     client
//	POST   /domains/:id/renew                   client (creates renewal invoice)
//	POST   /domains/:id/addons                  client (replace active addons, adjusts recurring amount)
//	GET    /admin/domains                       admin/staff (permission: domains)
//	POST   /admin/domains                       admin/staff (permission: domains) - add existing domain (no registrar call)
//	GET    /admin/domains/:id                   admin/staff (permission: domains)
//	PATCH  /admin/domains/:id                   admin/staff (permission: domains) (status, auto_renew, nameservers, billing fields)
//	POST   /admin/domains/:id/sync              admin/staff (permission: domains)
//	POST   /admin/domains/:id/renew             admin/staff (permission: domains)
//	GET    /admin/registrars                    admin
//	GET    /admin/registrars/:id                admin
//	PUT    /admin/registrars/:id                admin
//	POST   /admin/registrars/:id/test           admin
//	GET    /admin/registrars/:id/catalog        admin - registrar TLD pricelist preview
//	GET    /admin/tld-pricing                   admin
//	POST   /admin/tld-pricing                   admin
//	POST   /admin/tld-pricing/import            admin - bulk-create from the registrar catalog
//	GET    /admin/tld-pricing/:id                admin
//	PUT    /admin/tld-pricing/:id                admin
//	DELETE /admin/tld-pricing/:id                admin
//	GET    /admin/premium-domain-pricing        admin
//	POST   /admin/premium-domain-pricing        admin
//	PUT    /admin/premium-domain-pricing/:id     admin
//	DELETE /admin/premium-domain-pricing/:id     admin
//	GET    /admin/premium-length-pricing        admin
//	POST   /admin/premium-length-pricing        admin
//	PUT    /admin/premium-length-pricing/:id     admin
//	DELETE /admin/premium-length-pricing/:id     admin
//	GET    /admin/domain-addons                 admin
//	PUT    /admin/domain-addons/:id              admin
package domains

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Notification template keys (seeded in 000002_seed_core).
const (
	tplDomainRegistered = "domain_registered"
	tplDomainRenewed    = "domain_renewed"
)

// syncBatchLimit bounds one SyncAllDomains run.
const syncBatchLimit = 200

// registrarName is the registrars.name row every domain is attributed to
// (single-registrar setup, same constant the orders module resolves).
const registrarName = "rdash"

// dateFormat renders DATE values in notifications.
const dateFormat = "2006-01-02"

// Deps are the service dependencies (small interfaces, wired by composition
// root; mocked in tests).
type Deps struct {
	Tx                   ports.TxManager
	Domains              ports.DomainRepo
	Registrars           ports.RegistrarRepo
	TLDPricing           ports.TLDPricingRepo
	PremiumPricing       ports.PremiumDomainPricingRepo
	PremiumLengthPricing ports.PremiumLengthPricingRepo
	DomainAddons         ports.DomainAddonRepo
	Clients              ports.ClientRepo
	Users                ports.UserRepo
	Settings             ports.SettingsRepo
	Registrar            ports.RegistrarModule
	Enqueuer             ports.Enqueuer
	Invoices             ports.InvoiceCreator
	// RenewalCheck reports whether an open renewal invoice already exists
	// for a domain - the dedupe guard RenewNow uses, under a row lock, to
	// avoid creating a second invoice (owned by billing; see
	// ports.RenewalInvoiceChecker).
	RenewalCheck ports.RenewalInvoiceChecker
	Notifier     ports.NotificationSender
	Encryptor    ports.Encryptor
	Audit        ports.AuditLogger
	Clock        ports.Clock
	Log          *slog.Logger

	// RegistrarAPIKeyPresent reports whether RDASH_API_KEY is set in the
	// environment (never the key itself) - surfaced to the admin registrars
	// UI so it can show "configured" instead of guessing.
	RegistrarAPIKeyPresent bool

	// AllowPrivateBaseURL permits the registrar base_url override (CLAUDE.md
	// §0.3) to point at a loopback/private/link-local host - true only
	// outside production (cfg.IsProduction()), so local dev/test can still
	// target the mockserver at localhost:9090, while a compromised or
	// careless production admin account can't be used to make the server
	// send authenticated requests to internal-network/cloud-metadata hosts.
	AllowPrivateBaseURL bool
}

// Service implements the domains use-cases. It satisfies ports.DomainRenewer.
type Service struct {
	d Deps
}

var _ ports.DomainRenewer = (*Service)(nil)

// New builds the domains Service.
func New(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Service{d: d}
}

// Public

// CheckAvailability validates syntax and queries the registrar for up to 10
// names. Rate limiting (20/min/IP) is applied at the transport layer.
func (s *Service) CheckAvailability(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
	if len(names) == 0 {
		return nil, apperr.Validation("at least one domain name is required")
	}
	if len(names) > 10 {
		return nil, apperr.Validation("at most 10 domain names per check")
	}
	normalized := make([]string, 0, len(names))
	for _, n := range names {
		name := NormalizeDomainName(n)
		if err := ValidateDomainName(name); err != nil {
			return nil, err
		}
		normalized = append(normalized, name)
	}
	res, err := s.d.Registrar.CheckAvailability(ctx, normalized)
	if err != nil {
		return nil, err
	}
	for i := range res {
		s.overlayPricing(ctx, &res[i])
	}
	return res, nil
}

// overlayPricing replaces a live registrar quote with admin-configured
// pricing when available, in priority order: an exact premium-domain
// override, else a premium length-tier match (TLD + registrable-label
// character count, e.g. Dewabiz's "Limited Character" table), else the
// active TLD pricing row's year-1 register price (plus its full per-year
// matrix, via RegisterPrices - see orders/pricing.go's planDomain, which
// prices a >1-year registration from that same matrix rather than a flat
// Price*years multiple, since the matrix isn't guaranteed linear, e.g. a
// free-first-year promo). Every lookup miss (unconfigured TLD, no premium
// override/tier) leaves the registrar's own quote untouched - never a hard
// failure.
func (s *Service) overlayPricing(ctx context.Context, avail *ports.DomainAvailability) {
	if premium, err := s.d.PremiumPricing.GetByName(ctx, avail.Name); err == nil && premium != nil {
		avail.Premium = true
		avail.Price = premium.RegisterPrice
		return
	}
	ext := domainExtension(avail.Name)
	if tier, err := s.d.PremiumLengthPricing.GetByTLDAndLength(ctx, ext, domainLabelLength(avail.Name, ext)); err == nil && tier != nil {
		avail.Premium = true
		avail.Price = tier.Price
		return
	}
	tld, err := s.d.TLDPricing.GetByTLD(ctx, ext)
	if err != nil || tld == nil || !tld.Active {
		return
	}
	if price, ok := tld.RegisterPrices["1"]; ok {
		avail.Price = price
	}
	avail.RegisterPrices = tld.RegisterPrices
}

// domainExtension returns the registry extension of a registrable domain
// name (no leading dot) - everything after the first label, e.g.
// "example.co.id" -> "co.id".
func domainExtension(name string) string {
	i := 0
	for ; i < len(name); i++ {
		if name[i] == '.' {
			break
		}
	}
	if i >= len(name) {
		return ""
	}
	return name[i+1:]
}

// domainLabelLength returns the character count of a domain's registrable
// label (the part before its extension), e.g. "ab.id" with ext "id" -> 2.
func domainLabelLength(name, ext string) int {
	label := strings.TrimSuffix(name, "."+ext)
	return len(label)
}

// ListActiveTLDs returns the TLDs currently offered for sale, for the
// storefront's domain search grid (replaces a hardcoded TLD list).
func (s *Service) ListActiveTLDs(ctx context.Context) ([]domain.TLDPricing, error) {
	return s.d.TLDPricing.ListActive(ctx)
}

// ListActiveDomainAddons returns the domain addons currently offered for
// sale, for the storefront and client-area addon pickers.
func (s *Service) ListActiveDomainAddons(ctx context.Context) ([]domain.DomainAddon, error) {
	return s.d.DomainAddons.ListActive(ctx)
}

// Client

// ListForClient lists the client's own domains.
func (s *Service) ListForClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Domain, int64, error) {
	return s.d.Domains.ListByClient(ctx, clientID, p)
}

// GetForClient returns one domain owned by clientID; other clients' domains
// yield NOT_FOUND (never FORBIDDEN).
func (s *Service) GetForClient(ctx context.Context, clientID, domainID int64) (*domain.Domain, error) {
	dom, err := s.d.Domains.GetByID(ctx, domainID)
	if err != nil {
		return nil, err
	}
	if dom.ClientID != clientID {
		return nil, apperr.NotFound("domain")
	}
	return dom, nil
}

// UpdateNameservers validates 2-4 hostnames, pushes them to the registrar and
// stores them on the domain row.
func (s *Service) UpdateNameservers(ctx context.Context, clientID, domainID int64, ns []string) (*domain.Domain, error) {
	dom, err := s.GetForClient(ctx, clientID, domainID)
	if err != nil {
		return nil, err
	}
	if dom.Status != domain.DomainActive {
		return nil, apperr.Conflict("nameservers can only be changed on an active domain")
	}
	hosts, err := ValidateNameservers(ns)
	if err != nil {
		return nil, err
	}
	if err := s.d.Registrar.UpdateNameservers(ctx, dom.Name, hosts); err != nil {
		return nil, err
	}
	before := dom.Nameservers
	raw, err := json.Marshal(hosts)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	dom.Nameservers = raw
	if err := s.d.Domains.Update(ctx, dom); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, 0, "domain.nameservers_update", "domain", dom.ID,
		map[string]any{"nameservers": before}, map[string]any{"nameservers": hosts})
	return dom, nil
}

// errDNSAddonRequired is returned by GetDNS/UpdateDNS when the domain hasn't
// purchased the DNS Management add-on.
func errDNSAddonRequired() error {
	return apperr.New(apperr.CodePaymentRequired, "DNS management requires the DNS Management add-on")
}

// GetDNS fetches the zone records from the registrar.
func (s *Service) GetDNS(ctx context.Context, clientID, domainID int64) ([]ports.DNSRecord, error) {
	dom, err := s.GetForClient(ctx, clientID, domainID)
	if err != nil {
		return nil, err
	}
	if dom.Status != domain.DomainActive {
		return nil, apperr.Conflict("DNS is only available for an active domain")
	}
	if !dom.DNSManagementEnabled {
		return nil, errDNSAddonRequired()
	}
	recs, err := s.d.Registrar.GetDNSRecords(ctx, dom.Name)
	if err != nil {
		return nil, err
	}
	return recs, nil
}

// UpdateDNS replaces the zone records at the registrar.
func (s *Service) UpdateDNS(ctx context.Context, clientID, domainID int64, recs []ports.DNSRecord) ([]ports.DNSRecord, error) {
	dom, err := s.GetForClient(ctx, clientID, domainID)
	if err != nil {
		return nil, err
	}
	if dom.Status != domain.DomainActive {
		return nil, apperr.Conflict("DNS is only available for an active domain")
	}
	if !dom.DNSManagementEnabled {
		return nil, errDNSAddonRequired()
	}
	if err := s.d.Registrar.UpdateDNSRecords(ctx, dom.Name, recs); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, 0, "domain.dns_update", "domain", dom.ID, nil,
		map[string]any{"record_count": len(recs)})
	return recs, nil
}

// GetEPP returns the transfer auth code: the stored encrypted code when
// present, otherwise fetched live from the registrar.
func (s *Service) GetEPP(ctx context.Context, clientID, domainID int64) (string, error) {
	dom, err := s.GetForClient(ctx, clientID, domainID)
	if err != nil {
		return "", err
	}
	if dom.Status != domain.DomainActive {
		return "", apperr.Conflict("EPP code is only available for an active domain")
	}
	if dom.EPPCodeEnc != "" {
		code, err := s.d.Encryptor.Decrypt(dom.EPPCodeEnc)
		if err != nil {
			return "", apperr.Internal(err)
		}
		return code, nil
	}
	code, err := s.d.Registrar.GetEPPCode(ctx, dom.Name)
	if err != nil {
		return "", err
	}
	return code, nil
}

// GetContact returns the domain's current registrant contact from the
// registrar (there is no local copy to read back - UpdateDomain only pushes
// contact changes, it never stores them).
func (s *Service) GetContact(ctx context.Context, clientID, domainID int64) (*ports.RegistrantContact, error) {
	dom, err := s.GetForClient(ctx, clientID, domainID)
	if err != nil {
		return nil, err
	}
	if dom.Status != domain.DomainActive {
		return nil, apperr.Conflict("registrant contact is only available for an active domain")
	}
	return s.d.Registrar.GetContact(ctx, dom.Name)
}

// RenewNow creates a renewal invoice for one billing cycle (1 year for
// annual/shorter cycles, matching what RenewDomainJob will renew) immediately.
// The registrar renewal itself runs after payment (billing.ProcessPaid ->
// RenewDomainAfterPayment -> domain:renew job).
//
// The open-invoice dedupe check runs under a row lock on the domain inside a
// transaction (same idiom as billing's renewal-invoice generation), so two
// concurrent calls - a client double-clicking "Renew Now", or this racing
// the nightly renewal-invoice cron - can never both create an invoice for
// the same domain: the second caller blocks on the lock, then observes the
// first's invoice and is rejected instead of duplicating it.
func (s *Service) RenewNow(ctx context.Context, clientID, domainID int64) (*domain.Invoice, error) {
	dom, err := s.GetForClient(ctx, clientID, domainID)
	if err != nil {
		return nil, err
	}
	if dom.Status != domain.DomainActive && dom.Status != domain.DomainExpired {
		return nil, apperr.Conflict("only active or expired domains can be renewed")
	}
	if dom.RecurringAmount <= 0 {
		return nil, apperr.Conflict("domain has no renewal price configured")
	}
	dueDays, err := s.d.Settings.GetInt(ctx, "billing.invoice_due_days", 3)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	now := s.d.Clock.Now()
	years := cycleYears(dom.BillingCycle)
	unit := "year"
	if years > 1 {
		unit = "years"
	}

	var inv *domain.Invoice
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		locked, lerr := s.d.Domains.GetByIDForUpdate(ctx, dom.ID)
		if lerr != nil {
			return lerr
		}
		open, oerr := s.d.RenewalCheck.HasOpenRenewalInvoice(ctx, domain.RelatedDomainRenewal, locked.ID)
		if oerr != nil {
			return oerr
		}
		if open {
			return apperr.Conflict("a renewal invoice is already open for this domain")
		}
		created, cerr := s.d.Invoices.CreateInvoice(ctx, ports.CreateInvoiceInput{
			ClientID: locked.ClientID,
			Items: []ports.CreateInvoiceItem{{
				Description: "Domain Renewal: " + locked.Name + " (" + strconv.Itoa(years) + " " + unit + ")",
				Amount:      locked.RecurringAmount,
				Taxed:       true,
				RelatedType: domain.RelatedDomainRenewal,
				RelatedID:   locked.ID,
			}},
			DueDate: now.AddDate(0, 0, dueDays),
		})
		if cerr != nil {
			return cerr
		}
		inv = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, 0, "domain.renew_now", "domain", dom.ID, nil,
		map[string]any{"invoice_id": inv.ID, "invoice_number": inv.InvoiceNumber})
	return inv, nil
}

// UpdateDomain patches auto_renew and/or pushes a registrant contact update.
func (s *Service) UpdateDomain(ctx context.Context, clientID, domainID int64, in UpdateDomainRequest) (*domain.Domain, error) {
	dom, err := s.GetForClient(ctx, clientID, domainID)
	if err != nil {
		return nil, err
	}
	if in.AutoRenew == nil && in.Contact == nil {
		return nil, apperr.Validation("nothing to update")
	}
	before := dom.AutoRenew
	contactUpdated := false
	if in.Contact != nil {
		if dom.Status != domain.DomainActive {
			return nil, apperr.Conflict("contact can only be changed on an active domain")
		}
		if err := s.d.Registrar.UpdateContact(ctx, dom.Name, in.Contact.Contact()); err != nil {
			return nil, err
		}
		contactUpdated = true
	}
	if in.AutoRenew != nil {
		dom.AutoRenew = *in.AutoRenew
		if err := s.d.Domains.Update(ctx, dom); err != nil {
			return nil, err
		}
	}
	s.d.Audit.Log(ctx, 0, "domain.client_update", "domain", dom.ID,
		map[string]any{"auto_renew": before},
		map[string]any{"auto_renew": dom.AutoRenew, "contact_updated": contactUpdated})
	return dom, nil
}

// UpdateDomainAddons replaces which domain addons are active on one domain
// (replace-set semantics) and recomputes the recurring renewal amount to
// match: the domain's current base TLD/premium renew price plus the sum of
// the newly-selected addons' prices.
func (s *Service) UpdateDomainAddons(ctx context.Context, clientID, domainID int64, keys []string) (*domain.Domain, error) {
	dom, err := s.GetForClient(ctx, clientID, domainID)
	if err != nil {
		return nil, err
	}
	catalog, err := s.d.DomainAddons.List(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	price := make(map[string]int64, len(catalog))
	active := make(map[string]bool, len(catalog))
	for _, a := range catalog {
		price[a.Key] = a.Price
		active[a.Key] = a.Active
	}

	base := s.baseRenewPrice(ctx, dom, price)

	selected := make(map[string]bool, len(keys))
	total := base
	for _, k := range keys {
		if !active[k] {
			return nil, apperr.Validation("addon " + k + " is not available")
		}
		if selected[k] {
			continue
		}
		selected[k] = true
		total += price[k]
	}
	beforeAmount := dom.RecurringAmount
	dom.IDProtection = selected[string(domain.DomainAddonIDProtection)]
	dom.DNSManagementEnabled = selected[string(domain.DomainAddonDNSManagement)]
	dom.EmailForwardingEnabled = selected[string(domain.DomainAddonEmailForwarding)]
	dom.RecurringAmount = total
	if err := s.d.Domains.Update(ctx, dom); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, 0, "domain.addons_update", "domain", dom.ID,
		map[string]any{"recurring_amount": beforeAmount},
		map[string]any{"recurring_amount": total, "addons": keys})
	return dom, nil
}

// baseRenewPrice resolves a domain's current TLD/premium-based renew price
// (excluding addon costs) - the starting point when addons are toggled after
// registration. Priority matches planDomain/overlayPricing: an exact premium-
// domain override, else a premium length-tier match, else the active TLD's
// year-1 renew price. When none of those are configured (bought via the
// live-registrar-quote fallback, or before pricing was configured), it falls
// back to the domain's current recurring amount minus whichever addons are
// already active on it, so toggling addons still works.
func (s *Service) baseRenewPrice(ctx context.Context, dom *domain.Domain, addonPrice map[string]int64) int64 {
	if premium, err := s.d.PremiumPricing.GetByName(ctx, dom.Name); err == nil && premium != nil {
		return premium.RenewPrice
	}
	ext := domainExtension(dom.Name)
	if tier, err := s.d.PremiumLengthPricing.GetByTLDAndLength(ctx, ext, domainLabelLength(dom.Name, ext)); err == nil && tier != nil {
		return tier.Price
	}
	if tld, err := s.d.TLDPricing.GetByTLD(ctx, ext); err == nil && tld != nil && tld.Active {
		if price, ok := tld.RenewPrices["1"]; ok {
			return price
		}
	}
	current := dom.RecurringAmount
	if dom.IDProtection {
		current -= addonPrice[string(domain.DomainAddonIDProtection)]
	}
	if dom.DNSManagementEnabled {
		current -= addonPrice[string(domain.DomainAddonDNSManagement)]
	}
	if dom.EmailForwardingEnabled {
		current -= addonPrice[string(domain.DomainAddonEmailForwarding)]
	}
	if current < 0 {
		return 0
	}
	return current
}

// Cross-module (ports.DomainRenewer)

// RenewDomainAfterPayment enqueues the registrar renew job for a paid domain
// renewal invoice (called by billing.ProcessPaid, must tolerate re-runs; the
// job itself is guarded by registrar-side idempotency of Renew per expiry).
func (s *Service) RenewDomainAfterPayment(ctx context.Context, domainID int64) error {
	dom, err := s.d.Domains.GetByID(ctx, domainID)
	if err != nil {
		return err
	}
	return s.d.Enqueuer.Enqueue(ctx, jobs.TypeDomainRenew, jobs.DomainRenewPayload{
		DomainID: dom.ID,
		Years:    cycleYears(dom.BillingCycle),
	})
}

// Worker job methods (MODULES.md §2 signatures)

// RegisterDomainJob registers a pending domain at the registrar: builds the
// registrant contact from the client profile, uses stored (or registrar
// default) nameservers, then activates the row and notifies the client.
// Already-active domains return nil (idempotent retry).
func (s *Service) RegisterDomainJob(ctx context.Context, domainID int64) error {
	dom, err := s.d.Domains.GetByID(ctx, domainID)
	if err != nil {
		return err
	}
	if dom.Status == domain.DomainActive {
		return nil // idempotent re-run
	}
	if dom.Status != domain.DomainPending {
		return apperr.Newf(apperr.CodeConflict, "domain %s is %s, cannot register", dom.Name, dom.Status)
	}
	contact, userID, err := s.registrantContact(ctx, dom.ClientID)
	if err != nil {
		return err
	}
	ns, err := s.nameserversFor(ctx, dom)
	if err != nil {
		return err
	}
	res, err := s.d.Registrar.Register(ctx, ports.RegisterDomainRequest{
		Name:    dom.Name,
		Years:   cycleYears(dom.BillingCycle),
		NS:      ns,
		Contact: contact,
	})
	if err != nil {
		return err // asynq retries; final failure alerts admin in the worker wrapper
	}
	if err := s.activateFromResult(ctx, dom, res, ns); err != nil {
		return err
	}
	s.notify(ctx, userID, tplDomainRegistered, dom)
	return nil
}

// TransferDomainJob transfers a domain in using the stored (encrypted) EPP
// code. Already-active domains return nil.
func (s *Service) TransferDomainJob(ctx context.Context, domainID int64) error {
	dom, err := s.d.Domains.GetByID(ctx, domainID)
	if err != nil {
		return err
	}
	if dom.Status == domain.DomainActive {
		return nil // idempotent re-run
	}
	if dom.Status != domain.DomainPending && dom.Status != domain.DomainPendingTransfer {
		return apperr.Newf(apperr.CodeConflict, "domain %s is %s, cannot transfer", dom.Name, dom.Status)
	}
	if dom.EPPCodeEnc == "" {
		return apperr.Conflict("domain " + dom.Name + " has no EPP code for transfer")
	}
	epp, err := s.d.Encryptor.Decrypt(dom.EPPCodeEnc)
	if err != nil {
		return apperr.Internal(err)
	}
	contact, userID, err := s.registrantContact(ctx, dom.ClientID)
	if err != nil {
		return err
	}
	ns, err := s.nameserversFor(ctx, dom)
	if err != nil {
		return err
	}
	res, err := s.d.Registrar.Transfer(ctx, ports.TransferDomainRequest{
		Name:    dom.Name,
		Years:   cycleYears(dom.BillingCycle),
		NS:      ns,
		Contact: contact,
		EPPCode: epp,
	})
	if err != nil {
		return err
	}
	if err := s.activateFromResult(ctx, dom, res, ns); err != nil {
		return err
	}
	s.notify(ctx, userID, tplDomainRegistered, dom)
	return nil
}

// RenewDomainJob renews the domain at the registrar and advances
// expiry_date/next_due_date; expired domains transition back to active.
func (s *Service) RenewDomainJob(ctx context.Context, domainID int64) error {
	dom, err := s.d.Domains.GetByID(ctx, domainID)
	if err != nil {
		return err
	}
	years := cycleYears(dom.BillingCycle)
	res, err := s.d.Registrar.Renew(ctx, dom.Name, years)
	if err != nil {
		return err
	}
	expiry := res.ExpiryDate
	if expiry.IsZero() {
		base := s.d.Clock.Now()
		if dom.ExpiryDate != nil && dom.ExpiryDate.After(base) {
			base = *dom.ExpiryDate
		}
		expiry = base.AddDate(years, 0, 0)
	}
	dom.ExpiryDate = &expiry
	dom.NextDueDate = &expiry
	if dom.Status == domain.DomainExpired {
		next, err := domain.TransitionDomain(dom.Status, domain.DomainActive)
		if err != nil {
			return err
		}
		dom.Status = next
	}
	if err := s.d.Domains.Update(ctx, dom); err != nil {
		return err
	}
	if userID, err := s.clientUserID(ctx, dom.ClientID); err == nil {
		s.notify(ctx, userID, tplDomainRenewed, dom)
	}
	return nil
}

// SyncDomainJob pulls status/expiry/nameservers from the registrar and
// applies them (status only through the domain state machine).
func (s *Service) SyncDomainJob(ctx context.Context, domainID int64) error {
	dom, err := s.d.Domains.GetByID(ctx, domainID)
	if err != nil {
		return err
	}
	info, err := s.d.Registrar.SyncDomain(ctx, dom.Name)
	if err != nil {
		return err
	}
	if next, ok := mapRegistrarStatus(info.Status); ok && next != dom.Status {
		if domain.DomainCanTransition(dom.Status, next) {
			dom.Status = next
		} else {
			s.d.Log.WarnContext(ctx, "domain sync: illegal status transition skipped",
				"domain", dom.Name, "from", dom.Status, "to", next)
		}
	}
	if !info.ExpiryDate.IsZero() {
		expiry := info.ExpiryDate
		dom.ExpiryDate = &expiry
		if dom.NextDueDate == nil || dom.NextDueDate.Before(expiry) {
			dom.NextDueDate = &expiry
		}
	}
	if len(info.NS) > 0 {
		raw, err := json.Marshal(info.NS)
		if err != nil {
			return apperr.Internal(err)
		}
		dom.Nameservers = raw
	}
	return s.d.Domains.Update(ctx, dom)
}

// SyncAllDomains syncs a bounded batch of domains; failures are logged and
// skipped. Returns the number of successfully synced domains.
func (s *Service) SyncAllDomains(ctx context.Context) (int, error) {
	list, err := s.d.Domains.ListForSync(ctx, syncBatchLimit)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, dom := range list {
		if err := s.SyncDomainJob(ctx, dom.ID); err != nil {
			s.d.Log.WarnContext(ctx, "domain sync failed", "domain", dom.Name, "error", err)
			continue
		}
		count++
	}
	return count, nil
}

// Admin

// AdminList lists all domains with search/status filters.
func (s *Service) AdminList(ctx context.Context, p ports.ListParams) ([]domain.Domain, int64, error) {
	return s.d.Domains.List(ctx, p)
}

// AdminGet returns any domain by id.
func (s *Service) AdminGet(ctx context.Context, id int64) (*domain.Domain, error) {
	return s.d.Domains.GetByID(ctx, id)
}

// AdminSync runs a synchronous registrar sync (fast admin feedback) and
// returns the refreshed domain.
func (s *Service) AdminSync(ctx context.Context, actorUserID, id int64) (*domain.Domain, error) {
	if err := s.SyncDomainJob(ctx, id); err != nil {
		return nil, err
	}
	dom, err := s.d.Domains.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "domain.sync", "domain", id, nil, dom)
	return dom, nil
}

// AdminForceRenew enqueues a registrar renew for the domain regardless of
// invoice state (audited).
func (s *Service) AdminForceRenew(ctx context.Context, actorUserID, id int64) error {
	dom, err := s.d.Domains.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.d.Enqueuer.Enqueue(ctx, jobs.TypeDomainRenew, jobs.DomainRenewPayload{
		DomainID: dom.ID,
		Years:    cycleYears(dom.BillingCycle),
	}); err != nil {
		return err
	}
	s.d.Audit.Log(ctx, actorUserID, "domain.force_renew", "domain", id, nil, map[string]any{"name": dom.Name})
	return nil
}

// AdminCreate records an already-registered domain directly on a client
// (WHMCS-style "add existing domain"): no order, no invoice, no payment and
// no registrar call - the row is attributed to the configured registrar and
// created active, so renewal invoicing works immediately and AdminSync can
// pull the real registrar-side status/expiry/nameservers afterwards. Audited.
func (s *Service) AdminCreate(ctx context.Context, actorUserID int64, in AdminCreateDomainRequest) (*domain.Domain, error) {
	name := NormalizeDomainName(in.Name)
	if err := ValidateDomainName(name); err != nil {
		return nil, err
	}
	client, err := s.d.Clients.GetByID(ctx, in.ClientID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, apperr.NotFound("client")
	}
	registrar, err := s.d.Registrars.GetByName(ctx, registrarName)
	if err != nil {
		return nil, err
	}
	if registrar == nil {
		return nil, apperr.NotFound("registrar")
	}

	cycle := domain.CycleAnnually
	if in.BillingCycle != "" {
		cycle = domain.BillingCycle(in.BillingCycle)
	}
	autoRenew := true
	if in.AutoRenew != nil {
		autoRenew = *in.AutoRenew
	}
	var nsJSON json.RawMessage
	if in.Nameservers != nil {
		hosts, err := ValidateNameservers(in.Nameservers)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(hosts)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		nsJSON = raw
	}

	dom := &domain.Domain{
		ClientID:         in.ClientID,
		RegistrarID:      registrar.ID,
		Name:             name,
		Status:           domain.DomainActive,
		RegistrationDate: dateOrNil(in.RegistrationDate),
		ExpiryDate:       dateOrNil(in.ExpiryDate),
		NextDueDate:      dateOrNil(in.NextDueDate),
		RecurringAmount:  in.RecurringAmount,
		BillingCycle:     cycle,
		AutoRenew:        autoRenew,
		Nameservers:      nsJSON,
	}
	if err := s.d.Domains.Create(ctx, dom); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "domain.admin_create", "domain", dom.ID, nil, dom)
	return dom, nil
}

// dateOrNil parses an already-format-validated YYYY-MM-DD string; empty (or,
// defensively, unparseable) becomes nil.
func dateOrNil(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(dateFormat, s)
	if err != nil {
		return nil
	}
	return &t
}

// AdminUpdate patches admin-editable domain fields: status (moved through
// the domain state machine - a status equal to the current one is a no-op,
// since the admin settings form always resubmits the current value),
// auto_renew and/or nameservers (same 2-4 hostname validation as the
// client-facing nameserver update, persisted directly without pushing to the
// registrar). Audits the change.
func (s *Service) AdminUpdate(ctx context.Context, actorUserID, domainID int64, in AdminUpdateDomainRequest) (*domain.Domain, error) {
	dom, err := s.d.Domains.GetByID(ctx, domainID)
	if err != nil {
		return nil, err
	}
	if in.Status == nil && in.AutoRenew == nil && in.Nameservers == nil &&
		in.RegistrationDate == nil && in.ExpiryDate == nil && in.NextDueDate == nil &&
		in.RecurringAmount == nil && in.BillingCycle == nil {
		return nil, apperr.Validation("nothing to update")
	}
	before := *dom

	if in.Nameservers != nil {
		hosts, err := ValidateNameservers(in.Nameservers)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(hosts)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		dom.Nameservers = raw
	}
	if in.AutoRenew != nil {
		dom.AutoRenew = *in.AutoRenew
	}
	// Billing fields: a nil pointer leaves the field untouched; an explicit
	// empty date string clears it.
	if in.RegistrationDate != nil {
		dom.RegistrationDate = dateOrNil(*in.RegistrationDate)
	}
	if in.ExpiryDate != nil {
		dom.ExpiryDate = dateOrNil(*in.ExpiryDate)
	}
	if in.NextDueDate != nil {
		dom.NextDueDate = dateOrNil(*in.NextDueDate)
	}
	if in.RecurringAmount != nil {
		dom.RecurringAmount = *in.RecurringAmount
	}
	if in.BillingCycle != nil && *in.BillingCycle != "" {
		dom.BillingCycle = domain.BillingCycle(*in.BillingCycle)
	}
	if in.Status != nil {
		target := domain.DomainStatus(*in.Status)
		if target != dom.Status {
			next, err := domain.TransitionDomain(dom.Status, target)
			if err != nil {
				return nil, err
			}
			dom.Status = next
		}
	}

	if err := s.d.Domains.Update(ctx, dom); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "domain.update", "domain", dom.ID, before, dom)
	return dom, nil
}

// ListRegistrars lists registrar configuration rows.
func (s *Service) ListRegistrars(ctx context.Context) ([]domain.Registrar, error) {
	return s.d.Registrars.List(ctx)
}

// GetRegistrar returns one registrar row.
func (s *Service) GetRegistrar(ctx context.Context, id int64) (*domain.Registrar, error) {
	return s.d.Registrars.GetByID(ctx, id)
}

// UpdateRegistrar patches active/config/reseller_id/api_key/base_url and
// audits the change. The API key is encrypted at rest (ports.Encryptor) and
// env vars remain the fallback for whichever of reseller_id/api_key/base_url
// is left unset - see rdash.Client's credential resolver
// (internal/composition/build.go).
func (s *Service) UpdateRegistrar(ctx context.Context, actorUserID, id int64, in UpdateRegistrarRequest) (*domain.Registrar, error) {
	if in.Active == nil && in.Config == nil && in.ResellerID == nil && in.APIKey == nil && in.BaseURL == nil {
		return nil, apperr.Validation("nothing to update")
	}
	reg, err := s.d.Registrars.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	before := *reg
	if in.Active != nil {
		reg.Active = *in.Active
	}
	if in.Config != nil {
		raw, err := json.Marshal(in.Config)
		if err != nil {
			return nil, apperr.Validation("config must be a JSON object")
		}
		reg.Config = raw
	}
	if in.ResellerID != nil {
		reg.ResellerID = *in.ResellerID
	}
	if in.APIKey != nil {
		if *in.APIKey == "" {
			reg.APIKeyEnc = ""
		} else {
			enc, err := s.d.Encryptor.Encrypt(*in.APIKey)
			if err != nil {
				return nil, apperr.Internal(err)
			}
			reg.APIKeyEnc = enc
		}
	}
	if in.BaseURL != nil {
		if err := ValidateRegistrarBaseURL(*in.BaseURL, s.d.AllowPrivateBaseURL); err != nil {
			return nil, err
		}
		reg.BaseURL = *in.BaseURL
	}
	if err := s.d.Registrars.Update(ctx, reg); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "registrar.update", "registrar", id, before, reg)
	return reg, nil
}

// TestRegistrar probes registrar connectivity by fetching the reseller
// account profile/balance (validates credentials without touching any
// domain).
func (s *Service) TestRegistrar(ctx context.Context, id int64) error {
	if _, err := s.d.Registrars.GetByID(ctx, id); err != nil {
		return err
	}
	if _, err := s.d.Registrar.AccountInfo(ctx); err != nil {
		return err
	}
	return nil
}

// RegistrarAPIKeyPresent reports whether RDASH_API_KEY is set in the
// environment (used to build the admin registrar response DTO).
func (s *Service) RegistrarAPIKeyPresent() bool {
	return s.d.RegistrarAPIKeyPresent
}

// Admin - TLD pricing

// ListTLDPricing lists all TLD pricing rows.
func (s *Service) ListTLDPricing(ctx context.Context) ([]domain.TLDPricing, error) {
	return s.d.TLDPricing.List(ctx)
}

// GetTLDPricing returns one TLD pricing row.
func (s *Service) GetTLDPricing(ctx context.Context, id int64) (*domain.TLDPricing, error) {
	return s.d.TLDPricing.GetByID(ctx, id)
}

// CreateTLDPricing validates and creates a new TLD pricing row, and audits
// the change.
func (s *Service) CreateTLDPricing(ctx context.Context, actorUserID int64, in TLDPricingRequest) (*domain.TLDPricing, error) {
	p, err := in.toTLDPricing(0)
	if err != nil {
		return nil, err
	}
	if err := s.d.TLDPricing.Create(ctx, p); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "tld_pricing.create", "tld_pricing", p.ID, nil, p)
	return p, nil
}

// UpdateTLDPricing validates and replaces a TLD pricing row, and audits the
// change.
func (s *Service) UpdateTLDPricing(ctx context.Context, actorUserID, id int64, in TLDPricingRequest) (*domain.TLDPricing, error) {
	before, err := s.d.TLDPricing.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err := in.toTLDPricing(id)
	if err != nil {
		return nil, err
	}
	if err := s.d.TLDPricing.Update(ctx, p); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "tld_pricing.update", "tld_pricing", id, before, p)
	return p, nil
}

// DeleteTLDPricing removes a TLD pricing row and audits the change.
func (s *Service) DeleteTLDPricing(ctx context.Context, actorUserID, id int64) error {
	before, err := s.d.TLDPricing.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.d.TLDPricing.Delete(ctx, id); err != nil {
		return err
	}
	s.d.Audit.Log(ctx, actorUserID, "tld_pricing.delete", "tld_pricing", id, before, nil)
	return nil
}

// ListRegistrarCatalog fetches the registrar's full TLD pricelist for the
// admin "Import from Registrar" preview, flagging TLDs that already have a
// tld_pricing row - import always skips those (see ImportTLDPricing), so the
// UI can grey them out up front instead of surprising the admin at submit.
func (s *Service) ListRegistrarCatalog(ctx context.Context, registrarID int64) ([]RegistrarCatalogResponse, error) {
	if _, err := s.d.Registrars.GetByID(ctx, registrarID); err != nil {
		return nil, err
	}
	prices, err := s.d.Registrar.ListCatalogPrices(ctx)
	if err != nil {
		return nil, err
	}
	existing, err := s.d.TLDPricing.List(ctx)
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(existing))
	for _, p := range existing {
		have[p.TLD] = true
	}
	out := make([]RegistrarCatalogResponse, 0, len(prices))
	for _, p := range prices {
		tld := normalizeTLD(p.Extension)
		out = append(out, RegistrarCatalogResponse{
			TLD:               tld,
			Currency:          p.Currency,
			RegisterPrices:    p.RegisterPrices,
			RenewPrices:       p.RenewPrices,
			TransferPrice:     p.TransferPrice,
			RestorePrice:      p.RestorePrice,
			AlreadyConfigured: have[tld],
		})
	}
	return out, nil
}

// applyMarkup adds pct percent on top of a registrar cost, rounded to the
// nearest whole rupiah (CONTRACTS.md §0: money is int64 whole IDR, never
// floats - the float64 markup math is confined to this one conversion).
func applyMarkup(cost int64, pct float64) int64 {
	return int64(math.Round(float64(cost) * (1 + pct/100)))
}

// ImportTLDPricing bulk-creates tld_pricing rows for the selected TLDs from
// the registrar's live catalog (re-fetched fresh here - server-authoritative
// pricing, never trusting client-submitted prices), applying a uniform
// markup over the registrar's raw cost to every price field. A TLD that
// already has a tld_pricing row, or that the registrar's catalog no longer
// lists, is skipped rather than erroring the whole batch. Imported rows are
// created active with the full 1-10 year range (the registrar-provided price
// matrix covers all of it); audited as one summary entry, not one per TLD.
func (s *Service) ImportTLDPricing(ctx context.Context, actorUserID int64, in ImportTLDPricingRequest) (*ImportTLDPricingResponse, error) {
	if _, err := s.d.Registrars.GetByID(ctx, in.RegistrarID); err != nil {
		return nil, err
	}
	prices, err := s.d.Registrar.ListCatalogPrices(ctx)
	if err != nil {
		return nil, err
	}
	byTLD := make(map[string]ports.RegistrarCatalogPrice, len(prices))
	for _, p := range prices {
		byTLD[normalizeTLD(p.Extension)] = p
	}
	existing, err := s.d.TLDPricing.List(ctx)
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(existing))
	for _, p := range existing {
		have[p.TLD] = true
	}

	resp := ImportTLDPricingResponse{Imported: []string{}, Skipped: []string{}}
	for _, raw := range in.TLDs {
		tld := normalizeTLD(raw)
		cp, ok := byTLD[tld]
		if tld == "" || have[tld] || !ok {
			resp.Skipped = append(resp.Skipped, tld)
			continue
		}
		p := &domain.TLDPricing{
			TLD:            tld,
			RegistrarID:    in.RegistrarID,
			Active:         true,
			MinYears:       1,
			MaxYears:       10,
			RegisterPrices: markupYearPrices(cp.RegisterPrices, in.MarkupPercent),
			RenewPrices:    markupYearPrices(cp.RenewPrices, in.MarkupPercent),
			TransferPrice:  applyMarkup(cp.TransferPrice, in.MarkupPercent),
			RestorePrice:   applyMarkup(cp.RestorePrice, in.MarkupPercent),
		}
		if err := s.d.TLDPricing.Create(ctx, p); err != nil {
			return nil, err
		}
		have[tld] = true
		resp.Imported = append(resp.Imported, tld)
	}
	s.d.Audit.Log(ctx, actorUserID, "tld_pricing.import", "tld_pricing", 0, nil, resp)
	return &resp, nil
}

// markupYearPrices applies applyMarkup to every value of a year->price map.
func markupYearPrices(m map[string]int64, pct float64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for y, cost := range m {
		out[y] = applyMarkup(cost, pct)
	}
	return out
}

// Admin - premium domain pricing

// ListPremiumPricing lists all premium domain pricing rows.
func (s *Service) ListPremiumPricing(ctx context.Context) ([]domain.PremiumDomainPricing, error) {
	return s.d.PremiumPricing.List(ctx)
}

// CreatePremiumPricing validates and creates a new premium pricing row, and
// audits the change.
func (s *Service) CreatePremiumPricing(ctx context.Context, actorUserID int64, in PremiumDomainPricingRequest) (*domain.PremiumDomainPricing, error) {
	p, err := in.toPremiumDomainPricing(0)
	if err != nil {
		return nil, err
	}
	if err := s.d.PremiumPricing.Create(ctx, p); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "premium_domain_pricing.create", "premium_domain_pricing", p.ID, nil, p)
	return p, nil
}

// UpdatePremiumPricing validates and replaces a premium pricing row, and
// audits the change.
func (s *Service) UpdatePremiumPricing(ctx context.Context, actorUserID, id int64, in PremiumDomainPricingRequest) (*domain.PremiumDomainPricing, error) {
	p, err := in.toPremiumDomainPricing(id)
	if err != nil {
		return nil, err
	}
	if err := s.d.PremiumPricing.Update(ctx, p); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "premium_domain_pricing.update", "premium_domain_pricing", id, nil, p)
	return p, nil
}

// DeletePremiumPricing removes a premium pricing row and audits the change.
func (s *Service) DeletePremiumPricing(ctx context.Context, actorUserID, id int64) error {
	if err := s.d.PremiumPricing.Delete(ctx, id); err != nil {
		return err
	}
	s.d.Audit.Log(ctx, actorUserID, "premium_domain_pricing.delete", "premium_domain_pricing", id, nil, nil)
	return nil
}

// Admin - premium length-tier pricing

// ListPremiumLengthPricing lists all premium length-tier pricing rows.
func (s *Service) ListPremiumLengthPricing(ctx context.Context) ([]domain.PremiumLengthPricing, error) {
	return s.d.PremiumLengthPricing.List(ctx)
}

// CreatePremiumLengthPricing validates and creates a new premium length-tier
// row, and audits the change.
func (s *Service) CreatePremiumLengthPricing(ctx context.Context, actorUserID int64, in PremiumLengthPricingRequest) (*domain.PremiumLengthPricing, error) {
	p, err := in.toPremiumLengthPricing(0)
	if err != nil {
		return nil, err
	}
	if err := s.d.PremiumLengthPricing.Create(ctx, p); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "premium_length_pricing.create", "premium_length_pricing", p.ID, nil, p)
	return p, nil
}

// UpdatePremiumLengthPricing validates and replaces a premium length-tier
// row, and audits the change.
func (s *Service) UpdatePremiumLengthPricing(ctx context.Context, actorUserID, id int64, in PremiumLengthPricingRequest) (*domain.PremiumLengthPricing, error) {
	p, err := in.toPremiumLengthPricing(id)
	if err != nil {
		return nil, err
	}
	if err := s.d.PremiumLengthPricing.Update(ctx, p); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "premium_length_pricing.update", "premium_length_pricing", id, nil, p)
	return p, nil
}

// DeletePremiumLengthPricing removes a premium length-tier row and audits
// the change.
func (s *Service) DeletePremiumLengthPricing(ctx context.Context, actorUserID, id int64) error {
	if err := s.d.PremiumLengthPricing.Delete(ctx, id); err != nil {
		return err
	}
	s.d.Audit.Log(ctx, actorUserID, "premium_length_pricing.delete", "premium_length_pricing", id, nil, nil)
	return nil
}

// Admin - domain addons

// ListDomainAddons lists the fixed domain-addon catalog.
func (s *Service) ListDomainAddons(ctx context.Context) ([]domain.DomainAddon, error) {
	return s.d.DomainAddons.List(ctx)
}

// UpdateDomainAddon patches one addon's price/active and audits the change.
func (s *Service) UpdateDomainAddon(ctx context.Context, actorUserID, id int64, in UpdateDomainAddonRequest) (*domain.DomainAddon, error) {
	addons, err := s.d.DomainAddons.List(ctx)
	if err != nil {
		return nil, err
	}
	var existing *domain.DomainAddon
	for i := range addons {
		if addons[i].ID == id {
			existing = &addons[i]
			break
		}
	}
	if existing == nil {
		return nil, apperr.NotFound("domain addon")
	}
	before := *existing
	existing.Price = in.Price
	existing.Active = in.Active
	if err := s.d.DomainAddons.Update(ctx, existing); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "domain_addon.update", "domain_addon", id, before, existing)
	return existing, nil
}

// Helpers

// cycleYears maps a billing cycle to registrar years (domains bill annually
// or biennially; anything else defaults to 1 year).
func cycleYears(c domain.BillingCycle) int {
	if months := domain.CycleMonths(c); months >= 12 {
		return months / 12
	}
	return 1
}

// registrantContact builds the registrar contact from the client profile and
// the owning user's email; also returns the user id for notifications.
func (s *Service) registrantContact(ctx context.Context, clientID int64) (ports.RegistrantContact, int64, error) {
	cl, err := s.d.Clients.GetByID(ctx, clientID)
	if err != nil {
		return ports.RegistrantContact{}, 0, err
	}
	u, err := s.d.Users.GetByID(ctx, cl.UserID)
	if err != nil {
		return ports.RegistrantContact{}, 0, err
	}
	contact := ports.RegistrantContact{
		FirstName: cl.FirstName,
		LastName:  cl.LastName,
		Company:   cl.Company,
		Email:     u.Email,
		Phone:     cl.Phone,
		Address1:  cl.Address1,
		City:      cl.City,
		State:     cl.State,
		Postcode:  cl.Postcode,
		Country:   cl.Country,
	}
	// Address/city/state/postcode are optional on the client profile, but the
	// registrar requires all four for the registrant contact - fail fast with
	// an actionable message rather than a raw registrar validation error.
	if contact.Address1 == "" || contact.City == "" || contact.State == "" || contact.Postcode == "" {
		return ports.RegistrantContact{}, 0, apperr.Validation(
			"client profile is missing address details (address, city, state, postal code) required for domain registration")
	}
	return contact, cl.UserID, nil
}

// clientUserID resolves the owning user id for notifications.
func (s *Service) clientUserID(ctx context.Context, clientID int64) (int64, error) {
	cl, err := s.d.Clients.GetByID(ctx, clientID)
	if err != nil {
		return 0, err
	}
	return cl.UserID, nil
}

// nameserversFor returns the domain's stored nameservers, falling back to the
// registrar config default ("default_ns" array in registrars.config JSONB).
func (s *Service) nameserversFor(ctx context.Context, dom *domain.Domain) ([]string, error) {
	var ns []string
	if len(dom.Nameservers) > 0 {
		if err := json.Unmarshal(dom.Nameservers, &ns); err != nil {
			return nil, apperr.Internal(err)
		}
	}
	if len(ns) > 0 {
		return ns, nil
	}
	reg, err := s.d.Registrars.GetByID(ctx, dom.RegistrarID)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		DefaultNS []string `json:"default_ns"`
	}
	if len(reg.Config) > 0 {
		_ = json.Unmarshal(reg.Config, &cfg) // malformed config -> no defaults
	}
	return cfg.DefaultNS, nil
}

// activateFromResult applies a successful register/transfer result: status ->
// active via the state machine, dates set, nameservers + registrar meta stored.
func (s *Service) activateFromResult(ctx context.Context, dom *domain.Domain, res *ports.DomainResult, ns []string) error {
	next, err := domain.TransitionDomain(dom.Status, domain.DomainActive)
	if err != nil {
		return err
	}
	dom.Status = next
	now := s.d.Clock.Now()
	dom.RegistrationDate = &now
	if !res.ExpiryDate.IsZero() {
		expiry := res.ExpiryDate
		dom.ExpiryDate = &expiry
		dom.NextDueDate = &expiry
	}
	if len(ns) > 0 {
		if raw, err := json.Marshal(ns); err == nil {
			dom.Nameservers = raw
		}
	}
	if res.OrderID != "" {
		meta := map[string]any{}
		if len(dom.RegistrarMeta) > 0 {
			_ = json.Unmarshal(dom.RegistrarMeta, &meta)
		}
		meta["order_id"] = res.OrderID
		if raw, err := json.Marshal(meta); err == nil {
			dom.RegistrarMeta = raw
		}
	}
	return s.d.Domains.Update(ctx, dom)
}

// notify sends a domain template email; failures are logged, never returned
// (the domain state is already persisted - retrying would re-run the job as a
// no-op and lose the mail anyway).
func (s *Service) notify(ctx context.Context, userID int64, key string, dom *domain.Domain) {
	data := map[string]any{
		"Domain": dom.Name,
	}
	if dom.ExpiryDate != nil {
		data["ExpiryDate"] = dom.ExpiryDate.Format(dateFormat)
	}
	if err := s.d.Notifier.SendTemplate(ctx, userID, key, data); err != nil {
		s.d.Log.WarnContext(ctx, "domain notification failed",
			"template", key, "domain", dom.Name, "error", err)
	}
}

// mapRegistrarStatus maps a registrar-side status string to our enum.
func mapRegistrarStatus(s string) (domain.DomainStatus, bool) {
	switch normalizeStatus(s) {
	case "active", "ok", "registered":
		return domain.DomainActive, true
	case "expired":
		return domain.DomainExpired, true
	case "pending":
		return domain.DomainPending, true
	case "pending_transfer", "transfer_in_progress", "transferring":
		return domain.DomainPendingTransfer, true
	case "cancelled", "canceled", "deleted", "pending_delete", "transferred_away", "rejected":
		return domain.DomainCancelled, true
	}
	return "", false
}

func normalizeStatus(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		case r == ' ' || r == '-':
			out = append(out, '_')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
