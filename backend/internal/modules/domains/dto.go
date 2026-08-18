package domains

import (
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// CheckRequest is the public availability-check body.
type CheckRequest struct {
	Names []string `json:"names" validate:"required,min=1,max=10,dive,required"`
}

// UpdateNameserversRequest replaces a domain's nameservers (2-4 hostnames).
type UpdateNameserversRequest struct {
	Nameservers []string `json:"nameservers" validate:"required,min=2,max=4,dive,required"`
}

// ContactInput updates the registrant contact at the registrar.
type ContactInput struct {
	FirstName string `json:"first_name" validate:"required,max=100"`
	LastName  string `json:"last_name" validate:"required,max=100"`
	Company   string `json:"company" validate:"max=150"`
	Email     string `json:"email" validate:"required,email"`
	Phone     string `json:"phone" validate:"required,max=32"`
	Address1  string `json:"address1" validate:"required,max=200"`
	City      string `json:"city" validate:"required,max=100"`
	State     string `json:"state" validate:"required,max=100"`
	Postcode  string `json:"postcode" validate:"required,max=16"`
	Country   string `json:"country" validate:"required,len=2"`
}

// Contact converts the input to the ports contact struct.
func (c ContactInput) Contact() ports.RegistrantContact {
	return ports.RegistrantContact{
		FirstName: c.FirstName,
		LastName:  c.LastName,
		Company:   c.Company,
		Email:     c.Email,
		Phone:     c.Phone,
		Address1:  c.Address1,
		City:      c.City,
		State:     c.State,
		Postcode:  c.Postcode,
		Country:   strings.ToUpper(c.Country),
	}
}

// UpdateDomainRequest patches client-editable domain fields.
type UpdateDomainRequest struct {
	AutoRenew *bool         `json:"auto_renew"`
	Contact   *ContactInput `json:"contact"`
}

// DNSRecordInput is one DNS record in a PUT /dns body. The type set matches
// what the registrar actually supports (no TXT/NS/SRV/CAA - SPF is its own
// legacy record type there, not folded into TXT).
type DNSRecordInput struct {
	Type  string `json:"type" validate:"required,oneof=A AAAA CNAME MX SPF"`
	Host  string `json:"host" validate:"max=253"`
	Value string `json:"value" validate:"required,max=4096"`
	TTL   int    `json:"ttl" validate:"omitempty,min=60,max=604800"`
	Prio  int    `json:"prio" validate:"min=0,max=65535"`
}

// UpdateDNSRequest replaces the DNS zone records at the registrar.
type UpdateDNSRequest struct {
	Records []DNSRecordInput `json:"records" validate:"required,max=200,dive"`
}

// Ports converts inputs to ports.DNSRecord, defaulting TTL to 3600.
func (r UpdateDNSRequest) Ports() []ports.DNSRecord {
	out := make([]ports.DNSRecord, 0, len(r.Records))
	for _, rec := range r.Records {
		ttl := rec.TTL
		if ttl == 0 {
			ttl = 3600
		}
		out = append(out, ports.DNSRecord{
			Type:  strings.ToUpper(rec.Type),
			Host:  rec.Host,
			Value: rec.Value,
			TTL:   ttl,
			Prio:  rec.Prio,
		})
	}
	return out
}

// UpdateRegistrarRequest patches a registrar row (admin). ResellerID/APIKey/
// BaseURL are dynamic, admin-configurable settings (env vars are the
// fallback for whichever is left unset) - nil means "don't touch" for all
// three, matching Active/Config's existing convention. APIKey is
// additionally tri-state: nil = unchanged, "" = clear the stored key (only
// ever sent by the UI when the admin explicitly checks "Clear stored key" -
// never just from leaving the field blank), non-empty = encrypt and store.
type UpdateRegistrarRequest struct {
	Active     *bool          `json:"active"`
	Config     map[string]any `json:"config"`
	ResellerID *string        `json:"reseller_id"`
	APIKey     *string        `json:"api_key"`
	BaseURL    *string        `json:"base_url"`
}

// RegistrarResponse is the admin registrar list/get/update response: the
// registrar row plus whether an API key is actually set - from the DB
// (encrypted) or the environment - never the key itself, which never
// leaves the server.
type RegistrarResponse struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	Active        bool            `json:"active"`
	Config        json.RawMessage `json:"config"`
	ResellerID    string          `json:"reseller_id"`
	APIKeyPresent bool            `json:"api_key_present"`
	BaseURL       string          `json:"base_url"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// toRegistrarResponse maps a registrar entity onto its API response.
// envAPIKeyPresent reports whether the RDASH_API_KEY env var is set - the
// response's api_key_present is true if *either* source has one.
func toRegistrarResponse(reg *domain.Registrar, envAPIKeyPresent bool) RegistrarResponse {
	return RegistrarResponse{
		ID: reg.ID, Name: reg.Name, Active: reg.Active, Config: reg.Config,
		ResellerID:    reg.ResellerID,
		APIKeyPresent: reg.APIKeyEnc != "" || envAPIKeyPresent,
		BaseURL:       reg.BaseURL,
		CreatedAt:     reg.CreatedAt, UpdatedAt: reg.UpdatedAt,
	}
}

// toRegistrarResponses maps a slice of registrar entities.
func toRegistrarResponses(regs []domain.Registrar, envAPIKeyPresent bool) []RegistrarResponse {
	out := make([]RegistrarResponse, 0, len(regs))
	for i := range regs {
		out = append(out, toRegistrarResponse(&regs[i], envAPIKeyPresent))
	}
	return out
}

// AdminCreateDomainRequest is the body of POST /admin/domains - the "add
// existing domain" path: an admin records a domain that is already registered
// (at the registrar or elsewhere) directly on a client, with no order, no
// payment and no registrar call. The row is created active; POST
// /admin/domains/:id/sync can then pull the real status/expiry/nameservers
// from the registrar. Billing fields are taken as given so the imported
// domain is immediately billable by renewal invoicing.
type AdminCreateDomainRequest struct {
	ClientID         int64    `json:"client_id" validate:"required,min=1"`
	Name             string   `json:"name" validate:"required,max=253"`
	RegistrationDate string   `json:"registration_date" validate:"omitempty,datetime=2006-01-02"`
	ExpiryDate       string   `json:"expiry_date" validate:"omitempty,datetime=2006-01-02"`
	NextDueDate      string   `json:"next_due_date" validate:"required,datetime=2006-01-02"`
	RecurringAmount  int64    `json:"recurring_amount" validate:"min=0"`
	BillingCycle     string   `json:"billing_cycle" validate:"omitempty,oneof=one_time monthly quarterly semiannually annually biennially"`
	AutoRenew        *bool    `json:"auto_renew"`
	Nameservers      []string `json:"nameservers" validate:"omitempty,min=2,max=4,dive,required"`
}

// AdminUpdateDomainRequest patches admin-editable domain fields: status
// (moved through the domain state machine), auto_renew, nameservers
// (validated the same way as the client-facing nameserver update), and/or
// the billing fields (dates, recurring amount, cycle) - the latter so a
// manually-added existing domain can have its renewal billing corrected
// without a registrar round-trip.
type AdminUpdateDomainRequest struct {
	Status           *string  `json:"status" validate:"omitempty,oneof=pending active pending_transfer expired cancelled"`
	AutoRenew        *bool    `json:"auto_renew"`
	Nameservers      []string `json:"nameservers" validate:"omitempty,min=2,max=4,dive,required"`
	RegistrationDate *string  `json:"registration_date" validate:"omitempty,datetime=2006-01-02"`
	ExpiryDate       *string  `json:"expiry_date" validate:"omitempty,datetime=2006-01-02"`
	NextDueDate      *string  `json:"next_due_date" validate:"omitempty,datetime=2006-01-02"`
	RecurringAmount  *int64   `json:"recurring_amount" validate:"omitempty,min=0"`
	BillingCycle     *string  `json:"billing_cycle" validate:"omitempty,oneof=one_time monthly quarterly semiannually annually biennially"`
}

// EPPResponse carries a domain's transfer auth code.
type EPPResponse struct {
	EPPCode string `json:"epp_code"`
}

// TestRegistrarResponse reports a registrar connectivity test result.
type TestRegistrarResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// TLDPricingRequest is the admin create/update payload for one TLD's
// pricing: a full 1-10 year register/renew matrix plus a flat transfer
// price and the registrable-years range.
type TLDPricingRequest struct {
	TLD            string           `json:"tld" validate:"required,max=63"`
	RegistrarID    int64            `json:"registrar_id" validate:"required,gt=0"`
	Active         bool             `json:"active"`
	MinYears       int              `json:"min_years" validate:"required,min=1,max=10"`
	MaxYears       int              `json:"max_years" validate:"required,min=1,max=10"`
	RegisterPrices map[string]int64 `json:"register_prices"`
	RenewPrices    map[string]int64 `json:"renew_prices"`
	TransferPrice  int64            `json:"transfer_price" validate:"gte=0"`
	RestorePrice   int64            `json:"restore_price" validate:"gte=0"`
}

// yearPriceKeys validates a register/renew price map's keys are "1".."10"
// and values are non-negative whole IDR.
func yearPriceKeys(m map[string]int64) error {
	for k, v := range m {
		y, err := strconv.Atoi(k)
		if err != nil || y < 1 || y > 10 {
			return apperr.Validation("price years must be keyed \"1\" through \"10\"")
		}
		if v < 0 {
			return apperr.Validation("prices must not be negative")
		}
	}
	return nil
}

// normalizeTLD lowercases and strips a leading dot, so ".Co.ID", "co.id" and
// "CO.ID" all key the same tld_pricing row.
func normalizeTLD(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "."))
}

// toTLDPricing validates the request and builds/patches a domain.TLDPricing.
// id is 0 for a new row (Create fills it in via RETURNING).
func (in TLDPricingRequest) toTLDPricing(id int64) (*domain.TLDPricing, error) {
	tld := normalizeTLD(in.TLD)
	if tld == "" {
		return nil, apperr.Validation("tld is required")
	}
	if in.MinYears > in.MaxYears {
		return nil, apperr.Validation("min_years must be <= max_years")
	}
	if err := yearPriceKeys(in.RegisterPrices); err != nil {
		return nil, err
	}
	if err := yearPriceKeys(in.RenewPrices); err != nil {
		return nil, err
	}
	registerPrices := in.RegisterPrices
	if registerPrices == nil {
		registerPrices = map[string]int64{}
	}
	renewPrices := in.RenewPrices
	if renewPrices == nil {
		renewPrices = map[string]int64{}
	}
	return &domain.TLDPricing{
		ID:             id,
		TLD:            tld,
		RegistrarID:    in.RegistrarID,
		Active:         in.Active,
		MinYears:       in.MinYears,
		MaxYears:       in.MaxYears,
		RegisterPrices: registerPrices,
		RenewPrices:    renewPrices,
		TransferPrice:  in.TransferPrice,
		RestorePrice:   in.RestorePrice,
	}, nil
}

// RegistrarCatalogResponse is one row of the "Import from Registrar" preview
// table - the registrar's raw TLD + cost pricing, plus whether a tld_pricing
// row already exists for it (so the UI can grey out / block re-selecting
// already-configured TLDs).
type RegistrarCatalogResponse struct {
	TLD               string           `json:"tld"`
	Currency          string           `json:"currency"`
	RegisterPrices    map[string]int64 `json:"register_prices"`
	RenewPrices       map[string]int64 `json:"renew_prices"`
	TransferPrice     int64            `json:"transfer_price"`
	RestorePrice      int64            `json:"restore_price"`
	AlreadyConfigured bool             `json:"already_configured"`
}

// ImportTLDPricingRequest bulk-imports selected TLDs from the registrar's
// live catalog into tld_pricing, applying a uniform markup over the
// registrar's raw cost. TLDs that already have a tld_pricing row are always
// skipped, never overwritten.
type ImportTLDPricingRequest struct {
	RegistrarID   int64    `json:"registrar_id" validate:"required,gt=0"`
	TLDs          []string `json:"tlds" validate:"required,min=1,dive,required"`
	MarkupPercent float64  `json:"markup_percent" validate:"gte=0"`
}

// ImportTLDPricingResponse summarizes a bulk import.
type ImportTLDPricingResponse struct {
	Imported []string `json:"imported"`
	Skipped  []string `json:"skipped"`
}

// PremiumDomainPricingRequest is the admin create/update payload for one
// manually-curated exact-domain price override.
type PremiumDomainPricingRequest struct {
	DomainName    string `json:"domain_name" validate:"required,max=253"`
	RegisterPrice int64  `json:"register_price" validate:"gte=0"`
	RenewPrice    int64  `json:"renew_price" validate:"gte=0"`
	TransferPrice int64  `json:"transfer_price" validate:"gte=0"`
}

func (in PremiumDomainPricingRequest) toPremiumDomainPricing(id int64) (*domain.PremiumDomainPricing, error) {
	name := NormalizeDomainName(in.DomainName)
	if err := ValidateDomainName(name); err != nil {
		return nil, err
	}
	return &domain.PremiumDomainPricing{
		ID:            id,
		DomainName:    name,
		RegisterPrice: in.RegisterPrice,
		RenewPrice:    in.RenewPrice,
		TransferPrice: in.TransferPrice,
	}, nil
}

// PremiumLengthPricingRequest is the admin create/update payload for one
// premium price tier keyed by TLD + registrable-label character length
// (e.g. Dewabiz's "Limited Character" table).
type PremiumLengthPricingRequest struct {
	TLD        string `json:"tld" validate:"required,max=63"`
	CharLength int    `json:"char_length" validate:"required,min=1,max=63"`
	Price      int64  `json:"price" validate:"gte=0"`
}

func (in PremiumLengthPricingRequest) toPremiumLengthPricing(id int64) (*domain.PremiumLengthPricing, error) {
	tld := normalizeTLD(in.TLD)
	if tld == "" {
		return nil, apperr.Validation("tld is required")
	}
	return &domain.PremiumLengthPricing{
		ID:         id,
		TLD:        tld,
		CharLength: in.CharLength,
		Price:      in.Price,
	}, nil
}

// UpdateDomainAddonRequest patches one domain addon catalog row's price/active.
type UpdateDomainAddonRequest struct {
	Price  int64 `json:"price" validate:"gte=0"`
	Active bool  `json:"active"`
}

// UpdateDomainAddonsRequest replaces the set of addons active on one domain
// (client self-service). Valid keys: id_protection, dns_management,
// email_forwarding.
type UpdateDomainAddonsRequest struct {
	Addons []string `json:"addons" validate:"omitempty,max=3,dive,oneof=id_protection dns_management email_forwarding"`
}

// Domain-name / hostname syntax validation (pure helpers, unit-tested)

// NormalizeDomainName lowercases and trims a domain name (trailing dot removed).
func NormalizeDomainName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// ValidateDomainName checks registrable-domain syntax. Unicode letters are
// accepted (IDN ok); each label must be 1-63 chars, no leading/trailing
// hyphen; at least two labels; TLD at least 2 chars; total length <= 253.
func ValidateDomainName(name string) error {
	name = NormalizeDomainName(name)
	if name == "" {
		return apperr.Validation("domain name is required")
	}
	if len(name) > 253 {
		return apperr.Validation("domain name too long: " + name)
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return apperr.Validation("invalid domain name: " + name)
	}
	for i, label := range labels {
		if err := validateLabel(label, name); err != nil {
			return err
		}
		// TLD: at least 2 chars, no digits-only.
		if i == len(labels)-1 && len([]rune(label)) < 2 {
			return apperr.Validation("invalid domain name: " + name)
		}
	}
	return nil
}

func validateLabel(label, name string) error {
	runes := []rune(label)
	if len(runes) == 0 || len(label) > 63 {
		return apperr.Validation("invalid domain name: " + name)
	}
	if runes[0] == '-' || runes[len(runes)-1] == '-' {
		return apperr.Validation("invalid domain name: " + name)
	}
	for _, r := range runes {
		if r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return apperr.Validation("invalid domain name: " + name)
	}
	return nil
}

// ValidateNameservers checks 2-4 distinct, syntactically valid hostnames.
func ValidateNameservers(ns []string) ([]string, error) {
	if len(ns) < 2 || len(ns) > 4 {
		return nil, apperr.Validation("nameservers must be 2 to 4 hostnames")
	}
	seen := make(map[string]bool, len(ns))
	out := make([]string, 0, len(ns))
	for _, h := range ns {
		host := NormalizeDomainName(h)
		if err := ValidateDomainName(host); err != nil {
			return nil, apperr.Validation("invalid nameserver hostname: " + h)
		}
		if seen[host] {
			return nil, apperr.Validation("duplicate nameserver: " + host)
		}
		seen[host] = true
		out = append(out, host)
	}
	return out, nil
}

// ValidateRegistrarBaseURL checks a registrar "custom endpoint" override:
// blank is always valid (falls back to RDASH_BASE_URL), otherwise it must be
// a well-formed absolute http(s) URL - catching an obvious typo before it
// silently breaks every registrar call far from this settings page.
//
// allowPrivate additionally lets it point at a loopback/private/link-local
// host (needed in dev/test to target the mockserver at localhost:9090); when
// false (production), such hosts are rejected so the override can't be used
// to make the server send authenticated RDash requests to an internal
// service or the cloud metadata endpoint (169.254.169.254).
func ValidateRegistrarBaseURL(raw string, allowPrivate bool) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return apperr.Validation("base_url must be a valid absolute http(s) URL")
	}
	if !allowPrivate && isPrivateOrLoopbackHost(u.Hostname()) {
		return apperr.Validation("base_url must not point to a private/loopback host")
	}
	return nil
}

// isPrivateOrLoopbackHost reports whether host (a URL hostname, possibly a
// literal IP) resolves to a loopback, private, or link-local address. A
// non-IP hostname (real DNS name) is not flagged here - the SSRF risk this
// guards against is an admin pointing the override straight at a literal
// internal IP; blocking every non-numeric hostname would also break using a
// real domain that only later resolves internally, which is out of scope for
// a synchronous input check.
func isPrivateOrLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
