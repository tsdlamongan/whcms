// Package rdash implements ports.RegistrarModule against the real Dewabiz
// "RDash" domain reseller API (default base https://api.dewabiz.co.id/v1,
// OpenAPI: https://api.dewabiz.co.id/swagger/v1).
//
// Protocol:
//   - HTTP Basic auth `reseller_id:api_key`.
//   - Requests are application/x-www-form-urlencoded (form fields), except
//     GETs which pass query parameters.
//   - Every response uses the JSON envelope {success, data, message}; 422
//     validation failures additionally carry {errors: {field: [messages]}}.
//     Dates are YYYY-MM-DD.
//   - 404 -> unknown domain, 409 -> taken/duplicate, 400/422 -> invalid input.
//
// The provider's domain model is a resource graph (reseller -> customer ->
// contact -> domain) keyed by a numeric domain_id, not the domain name, for
// every per-domain mutation. This adapter hides that entirely behind the
// name-keyed ports.RegistrarModule surface:
//
//	GET  /domains/availability             CheckAvailability (one name/call - no bulk endpoint)
//	POST /domains                          Register (creates a registrar customer+contact first)
//	POST /domains/transfer                 Transfer (creates a registrar customer+contact first)
//	POST /domains/{id}/renew               Renew (id + current expiry resolved via /domains/details)
//	GET  /domains/details?domain_name=     GetNameservers / SyncDomain (nameservers embedded in the detail payload)
//	PUT  /domains/{id}/ns                  UpdateNameservers
//	GET  /domains/details?domain_name=     GetContact (registrant_contact embedded in the detail payload)
//	PUT  /domains/{id}/contacts            UpdateContact (creates a fresh contact, applied to all 4 roles)
//	GET  /domains/{id}/auth_code           GetEPPCode
//	GET  /domains/{id}/dns                 GetDNSRecords
//	POST /domains/{id}/dns                 UpdateDNSRecords (replaces the full record set)
//	GET  /account/profile, /account/balance AccountInfo (registrar test-connection)
//	GET  /account/prices                   ListCatalogPrices (paginated, no domainExtension filter - full pricelist)
//
// Behavior: 30s per-request timeout, GETs retried twice on 5xx/network
// errors (POST/PUT never auto-retried), circuit-breaker-lite (consecutive
// transport failures open the breaker for a cooldown window during which
// calls fail fast with an EXTERNAL apperr), every HTTP call logged through
// ports.IntegrationLogger with sensitive keys redacted.
package rdash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Provider is the integration-log provider name.
const Provider = "rdash"

const (
	defaultBaseURL          = "https://api.dewabiz.co.id/v1"
	defaultTimeout          = 30 * time.Second
	defaultRetryDelay       = 250 * time.Millisecond
	defaultBreakerThreshold = 5
	defaultBreakerCooldown  = 60 * time.Second

	dateFormat     = "2006-01-02"
	maxGetRetries  = 2
	maxBodyBytes   = 1 << 20 // 1 MiB response cap
	maxNameservers = 5
)

// Config holds the RDash connection settings. Zero fields fall back to the
// documented defaults in New. ResellerID/APIKey are the static fallback used
// when resolve (see New) is nil, errors, or is bypassed - e.g. the env vars.
type Config struct {
	BaseURL          string        // default https://api.dewabiz.co.id/v1 (RDASH_BASE_URL)
	ResellerID       string        // Basic auth user (RDASH_RESELLER_ID)
	APIKey           string        // Basic auth password (RDASH_API_KEY)
	Timeout          time.Duration // per-request timeout, default 30s
	RetryDelay       time.Duration // delay between GET retries, default 250ms
	BreakerThreshold int           // consecutive transport failures that open the breaker, default 5
	BreakerCooldown  time.Duration // fast-fail window once open, default 60s
}

// Credentials are the live base URL ("custom endpoint")/reseller_id/api_key
// resolved fresh per call - see New's resolve parameter.
type Credentials struct {
	BaseURL    string
	ResellerID string
	APIKey     string
}

// Client is the RDash adapter. It is safe for concurrent use.
type Client struct {
	cfg     Config
	resolve func(ctx context.Context) (Credentials, error)
	hc      *http.Client
	log     ports.IntegrationLogger
	clock   ports.Clock

	mu        sync.Mutex
	fails     int       // consecutive transport failures
	openUntil time.Time // breaker fast-fail deadline (zero = closed)
}

// Compile-time interface assertion.
var _ ports.RegistrarModule = (*Client)(nil)

// New builds a Client. hc, log and clock may be nil (a default 30s-timeout
// http.Client, a no-op logger and the system clock are used respectively).
//
// resolve, when non-nil, is consulted at the start of every HTTP round-trip
// to get the *effective* base URL ("custom endpoint")/reseller_id/api_key
// for that call - this is what lets an admin-configured value (encrypted at
// rest for the API key, e.g. via the domains module's registrar-update
// endpoint) take effect without restarting the process: no caching, just a
// fresh read each call, the same "cheap DB read in front of a much slower
// external call" tradeoff already accepted elsewhere in this codebase (e.g.
// settings reads). If resolve is nil or returns an error, Config's static
// BaseURL/ResellerID/APIKey (the env-var values) are used instead - a
// resolver error never fails the operation outright, it just falls back and
// logs a warning.
func New(cfg Config, resolve func(ctx context.Context) (Credentials, error), hc *http.Client, log ports.IntegrationLogger, clock ports.Clock) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = defaultRetryDelay
	}
	if cfg.BreakerThreshold <= 0 {
		cfg.BreakerThreshold = defaultBreakerThreshold
	}
	if cfg.BreakerCooldown <= 0 {
		cfg.BreakerCooldown = defaultBreakerCooldown
	}
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	if log == nil {
		log = nopLogger{}
	}
	if clock == nil {
		clock = sysClock{}
	}
	return &Client{cfg: cfg, resolve: resolve, hc: hc, log: log, clock: clock}
}

// Wire shapes

// envelope is the Dewabiz response wrapper. Single-resource/action endpoints
// use {success, data, message[, errors]}; paginated list endpoints (GET
// /customers, /domains, /account/prices, /account/transactions, contact
// lists) instead return a bare {data, links, meta} with no success/message
// field at all. Success is a pointer so "absent" (list endpoints - trust the
// HTTP status) can be told apart from "explicitly false" (action endpoints
// disagreeing with a 2xx status).
type envelope struct {
	Success *bool               `json:"success"`
	Message string              `json:"message"`
	Data    json.RawMessage     `json:"data"`
	Errors  map[string][]string `json:"errors"`
	Meta    *listMeta           `json:"meta"`
}

// listMeta is the pagination block a bare-list envelope (GET /account/prices
// and friends) carries alongside `data`.
type listMeta struct {
	CurrentPage int `json:"current_page"`
	LastPage    int `json:"last_page"`
}

// contactItem is a Dewabiz contact resource, embedded in domain details or
// returned by /customers/{id}/contacts.
type contactItem struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Organization string `json:"organization"`
	Street1      string `json:"street_1"`
	City         string `json:"city"`
	State        string `json:"state"`
	CountryCode  string `json:"country_code"`
	PostalCode   string `json:"postal_code"`
	Voice        string `json:"voice"`
}

// toRegistrantContact maps a Dewabiz contact onto ports.RegistrantContact.
// Name is split on the first space (Dewabiz has no first/last split); any
// remainder (street_2, fax) has no home in the port's flat contact shape and
// is dropped.
func (c contactItem) toRegistrantContact() ports.RegistrantContact {
	first, last := c.Name, ""
	if i := strings.IndexByte(c.Name, ' '); i >= 0 {
		first, last = c.Name[:i], strings.TrimSpace(c.Name[i+1:])
	}
	return ports.RegistrantContact{
		FirstName: first,
		LastName:  last,
		Company:   c.Organization,
		Email:     c.Email,
		Phone:     c.Voice,
		Address1:  c.Street1,
		City:      c.City,
		State:     c.State,
		Postcode:  c.PostalCode,
		Country:   strings.ToUpper(c.CountryCode),
	}
}

// domainPayload is the register/transfer/renew/details/show response body -
// every one of those endpoints returns the same domain object shape.
type domainPayload struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Nameserver1 string `json:"nameserver_1"`
	Nameserver2 string `json:"nameserver_2"`
	Nameserver3 string `json:"nameserver_3"`
	Nameserver4 string `json:"nameserver_4"`
	Nameserver5 string `json:"nameserver_5"`
	Customer    *struct {
		ID int64 `json:"id"`
	} `json:"customer"`
	RegistrantContact *contactItem `json:"registrant_contact"`
	Status            *int         `json:"status"`
	StatusLabel       string       `json:"status_label"`
	ExpiredAt         string       `json:"expired_at"`
}

// nameservers collects the non-empty nameserver_1..5 fields.
func (d domainPayload) nameservers() []string {
	out := make([]string, 0, maxNameservers)
	for _, ns := range []string{d.Nameserver1, d.Nameserver2, d.Nameserver3, d.Nameserver4, d.Nameserver5} {
		if ns != "" {
			out = append(out, ns)
		}
	}
	return out
}

// statusString prefers the human status_label (register/transfer responses
// have been observed with a null numeric `status` but a populated label);
// falls back to the numeric registry status code.
func (d domainPayload) statusString() string {
	if d.StatusLabel != "" {
		return strings.ToLower(d.StatusLabel)
	}
	if d.Status == nil {
		return ""
	}
	switch *d.Status {
	case 0:
		return "pending"
	case 1:
		return "active"
	case 2:
		return "expired"
	case 3:
		return "pending delete"
	case 4:
		return "deleted"
	case 5:
		return "pending transfer"
	case 6:
		return "transferred away"
	case 7:
		return "suspended"
	case 8:
		return "rejected"
	default:
		return ""
	}
}

func (d domainPayload) toResult() (*ports.DomainResult, error) {
	exp, err := parseDate(d.ExpiredAt)
	if err != nil {
		return nil, err
	}
	return &ports.DomainResult{
		Name:       d.Name,
		Status:     d.statusString(),
		ExpiryDate: exp,
		OrderID:    strconv.FormatInt(d.ID, 10),
	}, nil
}

// dnsRecordItem is one GET/POST /domains/{id}/dns record.
type dnsRecordItem struct {
	Prefix  string `json:"prefix"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

// toPort maps a wire DNS record onto ports.DNSRecord, splitting the
// "priority value" MX content back into Prio/Value.
func (it dnsRecordItem) toPort() ports.DNSRecord {
	rec := ports.DNSRecord{Type: strings.ToUpper(it.Type), Host: it.Prefix, Value: it.Content, TTL: it.TTL}
	if strings.EqualFold(it.Type, "MX") {
		if prio, val, ok := splitPriority(it.Content); ok {
			rec.Prio, rec.Value = prio, val
		}
	}
	return rec
}

// splitPriority parses a "<priority> <value>" MX content string.
func splitPriority(content string) (int, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(content), " ", 2)
	if len(parts) != 2 {
		return 0, content, false
	}
	p, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, content, false
	}
	return p, parts[1], true
}

// ports.RegistrarModule

// CheckAvailability checks each name individually (the real API has no bulk
// endpoint), concurrently - at up to 10 names per call (dto.go), this keeps
// wall-clock latency close to one round trip instead of stacking them up.
// Price is enriched from GET /account/prices (one lookup per distinct
// extension among names, cached for the call) since the availability
// endpoint itself carries no pricing.
func (c *Client) CheckAvailability(ctx context.Context, names []string) ([]ports.DomainAvailability, error) {
	clean := make([]string, 0, len(names))
	for _, n := range names {
		if n = normalizeName(n); n != "" {
			clean = append(clean, n)
		}
	}
	if len(clean) == 0 {
		return nil, apperr.Validation("at least one domain name is required")
	}

	var (
		mu        sync.Mutex
		priceMemo = map[string]*priceResult{}
		wg        sync.WaitGroup
	)
	out := make([]ports.DomainAvailability, len(clean))
	errs := make([]error, len(clean))

	for i, name := range clean {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()

			// This is a single-resource endpoint (one domain per call), so the
			// real API returns `data` as a single object, not an array -
			// unlike list endpoints such as GET /domains.
			var data struct {
				Name      string `json:"name"`
				Available int    `json:"available"`
			}
			q := url.Values{"domain": {name}}
			if err := c.do(ctx, http.MethodGet, "/domains/availability", q, nil, &data); err != nil {
				errs[i] = err
				return
			}
			avail := ports.DomainAvailability{Name: name}
			avail.Available = data.Available != 0
			if data.Name != "" {
				avail.Name = data.Name
			}

			ext := extensionOf(name)
			price, err := c.priceForExtensionMemoized(ctx, ext, &mu, priceMemo)
			if err != nil {
				errs[i] = err
				return
			}
			avail.Price = price
			out[i] = avail
		}(i, name)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// priceResult memoizes one extension's 1-year price lookup (or its error)
// across concurrent CheckAvailability goroutines.
type priceResult struct {
	once  sync.Once
	price int64
	err   error
}

// priceForExtensionMemoized runs priceForYear1 at most once per extension per
// CheckAvailability call, even when looked up from multiple goroutines
// concurrently.
func (c *Client) priceForExtensionMemoized(ctx context.Context, ext string, mu *sync.Mutex, memo map[string]*priceResult) (int64, error) {
	mu.Lock()
	r, ok := memo[ext]
	if !ok {
		r = &priceResult{}
		memo[ext] = r
	}
	mu.Unlock()

	r.once.Do(func() {
		r.price, r.err = c.priceForYear1(ctx, ext)
	})
	return r.price, r.err
}

// priceForYear1 fetches the 1-year registration price for a domain extension
// (e.g. ".my.id") via GET /account/prices. Returns 0 (no error) when the
// registrar has no price on file for it - the caller falls back to catalog
// pricing in that case.
func (c *Client) priceForYear1(ctx context.Context, ext string) (int64, error) {
	if ext == "" {
		return 0, nil
	}
	var items []struct {
		DomainExtension struct {
			Extension string `json:"extension"`
		} `json:"domain_extension"`
		Registration map[string]any `json:"registration"`
	}
	q := url.Values{"domainExtension[extension]": {ext}, "limit": {"1"}}
	if err := c.do(ctx, http.MethodGet, "/account/prices", q, nil, &items); err != nil {
		return 0, err
	}
	for _, it := range items {
		if !strings.EqualFold(it.DomainExtension.Extension, ext) {
			continue
		}
		if v, ok := it.Registration["1"]; ok {
			if n, ok := toInt64Lenient(v); ok {
				return n, nil
			}
		}
	}
	return 0, nil
}

// extensionOf returns the registry extension of a registrable domain name -
// everything after the first label (e.g. "example.co.id" -> ".co.id").
func extensionOf(name string) string {
	i := strings.IndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return name[i:]
}

// toInt64Lenient parses a JSON number that Dewabiz sometimes encodes as a
// string within the same map (observed: registration["1"] as a quoted
// string while other years are plain numbers).
func toInt64Lenient(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// Register registers a new domain: creates a registrar customer+contact from
// the given registrant contact, then registers the domain under it.
func (c *Client) Register(ctx context.Context, req ports.RegisterDomainRequest) (*ports.DomainResult, error) {
	name := normalizeName(req.Name)
	if name == "" {
		return nil, apperr.Validation("domain name is required")
	}
	customerID, err := c.ensureCustomer(ctx, req.Contact)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"name":        {name},
		"period":      {strconv.Itoa(atLeastOne(req.Years))},
		"customer_id": {strconv.FormatInt(customerID, 10)},
	}
	addNameservers(form, req.NS)

	var data domainPayload
	if err := c.do(ctx, http.MethodPost, "/domains", nil, form, &data); err != nil {
		return nil, err
	}
	return data.toResult()
}

// Transfer transfers a domain in using its EPP/auth code: creates a
// registrar customer+contact from the given registrant contact, then starts
// the transfer under it.
func (c *Client) Transfer(ctx context.Context, req ports.TransferDomainRequest) (*ports.DomainResult, error) {
	name := normalizeName(req.Name)
	if name == "" {
		return nil, apperr.Validation("domain name is required")
	}
	if strings.TrimSpace(req.EPPCode) == "" {
		return nil, apperr.Validation("EPP/auth code is required")
	}
	customerID, err := c.ensureCustomer(ctx, req.Contact)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"name":        {name},
		"auth_code":   {req.EPPCode},
		"period":      {strconv.Itoa(atLeastOne(req.Years))},
		"customer_id": {strconv.FormatInt(customerID, 10)},
	}
	addNameservers(form, req.NS)

	var data domainPayload
	if err := c.do(ctx, http.MethodPost, "/domains/transfer", nil, form, &data); err != nil {
		return nil, err
	}
	return data.toResult()
}

// Renew extends a domain registration by years (minimum 1). The registrar
// requires the domain's current expiry date, so this first resolves the
// domain's id+expiry via /domains/details.
func (c *Client) Renew(ctx context.Context, name string, years int) (*ports.DomainResult, error) {
	detail, err := c.detailsByName(ctx, name)
	if err != nil {
		return nil, err
	}
	currentDate := detail.ExpiredAt
	if currentDate == "" {
		currentDate = c.clock.Now().UTC().Format(dateFormat)
	}
	form := url.Values{
		"period":       {strconv.Itoa(atLeastOne(years))},
		"current_date": {currentDate},
	}
	var data domainPayload
	path := fmt.Sprintf("/domains/%d/renew", detail.ID)
	if err := c.do(ctx, http.MethodPost, path, nil, form, &data); err != nil {
		return nil, err
	}
	return data.toResult()
}

// GetNameservers returns the domain's current nameservers (embedded in the
// domain-details payload; there is no dedicated nameservers endpoint).
func (c *Client) GetNameservers(ctx context.Context, name string) ([]string, error) {
	detail, err := c.detailsByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return detail.nameservers(), nil
}

// UpdateNameservers replaces the domain's nameservers (2-4 hostnames).
func (c *Client) UpdateNameservers(ctx context.Context, name string, ns []string) error {
	if len(ns) < 2 {
		return apperr.Validation("at least two nameservers are required")
	}
	detail, err := c.detailsByName(ctx, name)
	if err != nil {
		return err
	}
	form := url.Values{}
	addNameservers(form, ns)
	path := fmt.Sprintf("/domains/%d/ns", detail.ID)
	return c.do(ctx, http.MethodPut, path, nil, form, nil)
}

// GetContact returns the registrant contact on file (embedded in the
// domain-details payload).
func (c *Client) GetContact(ctx context.Context, name string) (*ports.RegistrantContact, error) {
	detail, err := c.detailsByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if detail.RegistrantContact == nil {
		return nil, apperr.NotFound("registrant contact")
	}
	rc := detail.RegistrantContact.toRegistrantContact()
	return &rc, nil
}

// UpdateContact replaces the registrant contact: creates a fresh registrar
// contact from c and applies it to all four contact roles (admin, tech,
// billing, registrant) - the registrar has no bare "registrant only" update.
func (c *Client) UpdateContact(ctx context.Context, name string, contact ports.RegistrantContact) error {
	detail, err := c.detailsByName(ctx, name)
	if err != nil {
		return err
	}
	if detail.Customer == nil {
		return apperr.External(Provider, fmt.Errorf("domain %s has no linked registrar customer", name))
	}
	contactID, err := c.createContact(ctx, detail.Customer.ID, contact)
	if err != nil {
		return err
	}
	id := strconv.FormatInt(contactID, 10)
	form := url.Values{
		"admin_contact_id":      {id},
		"tech_contact_id":       {id},
		"billing_contact_id":    {id},
		"registrant_contact_id": {id},
	}
	path := fmt.Sprintf("/domains/%d/contacts", detail.ID)
	return c.do(ctx, http.MethodPut, path, nil, form, nil)
}

// GetEPPCode returns the domain's EPP/auth code.
func (c *Client) GetEPPCode(ctx context.Context, name string) (string, error) {
	detail, err := c.detailsByName(ctx, name)
	if err != nil {
		return "", err
	}
	var code string
	path := fmt.Sprintf("/domains/%d/auth_code", detail.ID)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &code); err != nil {
		return "", err
	}
	return code, nil
}

// GetDNSRecords returns the DNS records hosted at the registrar.
func (c *Client) GetDNSRecords(ctx context.Context, name string) ([]ports.DNSRecord, error) {
	detail, err := c.detailsByName(ctx, name)
	if err != nil {
		return nil, err
	}
	var items []dnsRecordItem
	path := fmt.Sprintf("/domains/%d/dns", detail.ID)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &items); err != nil {
		return nil, err
	}
	out := make([]ports.DNSRecord, 0, len(items))
	for _, it := range items {
		out = append(out, it.toPort())
	}
	return out, nil
}

// UpdateDNSRecords replaces the full DNS record set (POST /domains/{id}/dns
// is documented to replace all existing records).
func (c *Client) UpdateDNSRecords(ctx context.Context, name string, recs []ports.DNSRecord) error {
	detail, err := c.detailsByName(ctx, name)
	if err != nil {
		return err
	}
	form := url.Values{}
	for i, rec := range recs {
		content := rec.Value
		if strings.EqualFold(rec.Type, "MX") {
			content = fmt.Sprintf("%d %s", rec.Prio, rec.Value)
		}
		ttl := rec.TTL
		if ttl <= 0 {
			ttl = 3600
		}
		prefix := fmt.Sprintf("records[%d]", i)
		form.Set(prefix+"[name]", rec.Host)
		form.Set(prefix+"[type]", strings.ToUpper(rec.Type))
		form.Set(prefix+"[content]", content)
		form.Set(prefix+"[ttl]", strconv.Itoa(ttl))
	}
	path := fmt.Sprintf("/domains/%d/dns", detail.ID)
	return c.do(ctx, http.MethodPost, path, nil, form, nil)
}

// SyncDomain returns the registrar's current view of the domain (status,
// expiry, nameservers) via the same domain-details lookup used elsewhere.
func (c *Client) SyncDomain(ctx context.Context, name string) (*ports.DomainSyncInfo, error) {
	detail, err := c.detailsByName(ctx, name)
	if err != nil {
		return nil, err
	}
	exp, err := parseDate(detail.ExpiredAt)
	if err != nil {
		return nil, err
	}
	return &ports.DomainSyncInfo{Status: detail.statusString(), ExpiryDate: exp, NS: detail.nameservers()}, nil
}

// AccountInfo fetches the reseller account profile and balance
// (GET /account/profile, GET /account/balance) for the registrar
// test-connection endpoint.
func (c *Client) AccountInfo(ctx context.Context) (*ports.RegistrarAccountInfo, error) {
	var profile struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/account/profile", nil, nil, &profile); err != nil {
		return nil, err
	}
	var bal struct {
		Currency string `json:"currency"`
		Balance  string `json:"balance"`
	}
	if err := c.do(ctx, http.MethodGet, "/account/balance", nil, nil, &bal); err != nil {
		return nil, err
	}
	amount, err := parseRupiah(bal.Balance)
	if err != nil {
		return nil, err
	}
	return &ports.RegistrarAccountInfo{
		AccountID: strconv.FormatInt(profile.ID, 10),
		Name:      profile.Name,
		Currency:  bal.Currency,
		Balance:   amount,
	}, nil
}

const (
	catalogPageSize = 100 // items requested per page
	catalogMaxPages = 50  // defensive backstop only - a real catalog is a few hundred TLDs at most
)

// catalogPriceItem is one entry of GET /account/prices's data array when
// called without a domainExtension filter (confirmed optional-and-means-all
// against the live API spec - omitting it returns every extension the
// reseller account can sell, not an error).
type catalogPriceItem struct {
	DomainExtension struct {
		Extension string `json:"extension"`
	} `json:"domain_extension"`
	Currency     string         `json:"currency"`
	Registration map[string]any `json:"registration"`
	Renewal      map[string]any `json:"renewal"`
	Transfer     string         `json:"transfer"`
	Redemption   string         `json:"redemption"` // restore/redemption price
}

// toCatalogPrice converts one wire item into ports.RegistrarCatalogPrice.
func (it catalogPriceItem) toCatalogPrice() (ports.RegistrarCatalogPrice, error) {
	transfer, err := parseRupiah(it.Transfer)
	if err != nil {
		return ports.RegistrarCatalogPrice{}, err
	}
	restore, err := parseRupiah(it.Redemption)
	if err != nil {
		return ports.RegistrarCatalogPrice{}, err
	}
	return ports.RegistrarCatalogPrice{
		Extension:      strings.ToLower(it.DomainExtension.Extension),
		Currency:       it.Currency,
		RegisterPrices: yearPrices(it.Registration),
		RenewPrices:    yearPrices(it.Renewal),
		TransferPrice:  transfer,
		RestorePrice:   restore,
	}, nil
}

// yearPrices converts a Dewabiz year->price map (values sometimes numbers,
// sometimes quoted strings - same inconsistency toInt64Lenient already
// handles for the single-extension lookup) into map["1".."10"]int64,
// dropping any entry that doesn't parse as a number.
func yearPrices(m map[string]any) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		if n, ok := toInt64Lenient(v); ok {
			out[k] = n
		}
	}
	return out
}

// ListCatalogPrices fetches the registrar's full TLD pricelist (GET
// /account/prices with no domainExtension filter, paginating through every
// page) - feeds the admin TLD Pricing "Import from Registrar" flow.
func (c *Client) ListCatalogPrices(ctx context.Context) ([]ports.RegistrarCatalogPrice, error) {
	var out []ports.RegistrarCatalogPrice
	for page := 1; page <= catalogMaxPages; page++ {
		var items []catalogPriceItem
		q := url.Values{"page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(catalogPageSize)}}
		meta, err := c.doPaged(ctx, http.MethodGet, "/account/prices", q, nil, &items)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			cp, err := it.toCatalogPrice()
			if err != nil {
				return nil, err
			}
			out = append(out, cp)
		}
		if len(items) == 0 || meta == nil || page >= meta.LastPage {
			break
		}
	}
	return out, nil
}

// Customer/contact provisioning (Dewabiz requires a registrar-side
// customer+contact graph behind every domain; ports.RegistrantContact is
// flat, so the adapter creates these transparently).

// detailsByName resolves the full domain detail - including the numeric
// domain_id and linked customer_id every per-domain endpoint needs - from a
// domain name via GET /domains/details.
func (c *Client) detailsByName(ctx context.Context, name string) (*domainPayload, error) {
	name = normalizeName(name)
	if name == "" {
		return nil, apperr.Validation("domain name is required")
	}
	var data domainPayload
	q := url.Values{"domain_name": {name}}
	if err := c.do(ctx, http.MethodGet, "/domains/details", q, nil, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// ensureCustomer returns the registrar customer id for contact's email,
// reusing an existing one when the reseller already has a customer under
// that email (Dewabiz rejects POST /customers outright for a duplicate email
// - "Email has already on this reseller" - so a client's second, third, ...
// domain must reuse their first-registration customer rather than trying to
// create a duplicate) and otherwise creating a fresh one. The registrar
// customer login (password) is internal reseller-panel bookkeeping never
// surfaced to WHCMS end users, so a random one is generated per call.
func (c *Client) ensureCustomer(ctx context.Context, contact ports.RegistrantContact) (int64, error) {
	if id, err := c.findCustomerByEmail(ctx, contact.Email); err != nil {
		return 0, err
	} else if id != 0 {
		return id, nil
	}

	pw, err := domain.GeneratePassword(24)
	if err != nil {
		return 0, apperr.Internal(fmt.Errorf("rdash: generate customer credential: %w", err))
	}
	name := strings.TrimSpace(contact.FirstName + " " + contact.LastName)
	if name == "" {
		name = contact.Email
	}
	org := contact.Company
	if org == "" {
		org = name
	}
	form := url.Values{
		"name":                  {name},
		"email":                 {contact.Email},
		"password":              {pw},
		"password_confirmation": {pw},
		"organization":          {org},
		"street_1":              {contact.Address1},
		"city":                  {contact.City},
		"state":                 {contact.State},
		"country_code":          {strings.ToUpper(contact.Country)},
		"postal_code":           {contact.Postcode},
		"voice":                 {contact.Phone},
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/customers", nil, form, &created); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// findCustomerByEmail looks up an existing Dewabiz customer by email via GET
// /customers?email= (a paginated list endpoint - see envelope's doc comment).
// Returns 0 (no error) when no customer has that email yet.
func (c *Client) findCustomerByEmail(ctx context.Context, email string) (int64, error) {
	if email == "" {
		return 0, nil
	}
	var items []contactItem
	q := url.Values{"email": {email}}
	if _, err := c.doPaged(ctx, http.MethodGet, "/customers", q, nil, &items); err != nil {
		return 0, err
	}
	for _, it := range items {
		if strings.EqualFold(it.Email, email) {
			return it.ID, nil
		}
	}
	return 0, nil
}

// createContact creates a registrar contact under customerID from a
// registrant contact (used to apply a fresh contact to all four domain
// contact roles in UpdateContact).
func (c *Client) createContact(ctx context.Context, customerID int64, contact ports.RegistrantContact) (int64, error) {
	name := strings.TrimSpace(contact.FirstName + " " + contact.LastName)
	if name == "" {
		name = contact.Email
	}
	org := contact.Company
	if org == "" {
		org = name
	}
	form := url.Values{
		"label":        {"Default"},
		"name":         {name},
		"email":        {contact.Email},
		"organization": {org},
		"street_1":     {contact.Address1},
		"city":         {contact.City},
		"state":        {contact.State},
		"country_code": {strings.ToUpper(contact.Country)},
		"postal_code":  {contact.Postcode},
		"voice":        {contact.Phone},
	}
	path := fmt.Sprintf("/customers/%d/contacts", customerID)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, path, nil, form, &created); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// addNameservers sets nameserver[0..n-1] form fields, capped at 5 (the
// registrar's maximum).
func addNameservers(form url.Values, ns []string) {
	for i, host := range ns {
		if i >= maxNameservers {
			break
		}
		form.Set(fmt.Sprintf("nameserver[%d]", i), host)
	}
}

// HTTP plumbing

// do runs one logical API call: breaker check, query/form encode, attempt(s)
// with GET retry on transient failures, envelope decode into out.
func (c *Client) do(ctx context.Context, method, path string, query, form url.Values, out any) error {
	_, err := c.doPaged(ctx, method, path, query, form, out)
	return err
}

// doPaged is do, but also returns the envelope's pagination meta (nil when
// the endpoint isn't a paginated list) - used by ListCatalogPrices to walk
// every page of GET /account/prices.
func (c *Client) doPaged(ctx context.Context, method, path string, query, form url.Values, out any) (*listMeta, error) {
	if err := c.checkBreaker(); err != nil {
		return nil, err
	}

	fullPath := path
	if len(query) > 0 {
		fullPath += "?" + query.Encode()
	}

	attempts := 1
	if method == http.MethodGet {
		attempts += maxGetRetries
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, apperr.External(Provider, ctx.Err())
			case <-time.After(c.cfg.RetryDelay):
			}
		}
		transient, meta, err := c.attempt(ctx, method, fullPath, form, out)
		if err == nil {
			return meta, nil
		}
		lastErr = err
		if !transient {
			return nil, err
		}
	}
	return nil, lastErr
}

// attempt performs a single HTTP round trip. transient reports whether the
// failure is retry-worthy (network error or 5xx).
func (c *Client) attempt(ctx context.Context, method, fullPath string, form url.Values, out any) (transient bool, meta *listMeta, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	creds := c.resolveCredentials(reqCtx)

	var bodyReader io.Reader
	var payload []byte
	if form != nil {
		payload = []byte(form.Encode())
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, creds.BaseURL+fullPath, bodyReader)
	if err != nil {
		return false, nil, apperr.Internal(fmt.Errorf("rdash: build request: %w", err))
	}
	req.SetBasicAuth(creds.ResellerID, creds.APIKey)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	call := ports.IntegrationCall{Provider: Provider, Endpoint: fullPath, Method: method}
	if form != nil {
		call.Request = redactForm(form)
	}

	start := c.clock.Now()
	resp, err := c.hc.Do(req)
	call.LatencyMS = c.clock.Now().Sub(start).Milliseconds()
	if err != nil {
		c.recordFailure()
		call.Error = err.Error()
		c.logCall(ctx, call)
		return true, nil, apperr.External(Provider, err)
	}
	defer resp.Body.Close()

	call.StatusCode = resp.StatusCode
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		c.recordFailure()
		call.Error = err.Error()
		c.logCall(ctx, call)
		return true, nil, apperr.External(Provider, fmt.Errorf("read response: %w", err))
	}
	call.Response = redactJSON(raw)

	var env envelope
	parseErr := json.Unmarshal(raw, &env)

	// 5xx: provider fault - counts toward the breaker, retryable for GETs.
	if resp.StatusCode >= 500 {
		c.recordFailure()
		appErr := mapError(resp.StatusCode, env.Message, env.Errors)
		call.Error = appErr.Error()
		c.logCall(ctx, call)
		return true, nil, appErr
	}

	if parseErr != nil {
		if resp.StatusCode >= 400 {
			// The server answered with a clear HTTP error but a non-envelope
			// body; map by status.
			c.recordSuccess()
			appErr := mapError(resp.StatusCode, "", nil)
			call.Error = appErr.Error()
			c.logCall(ctx, call)
			return false, nil, appErr
		}
		// 2xx with an unparseable body: provider fault.
		c.recordFailure()
		appErr := apperr.External(Provider, fmt.Errorf("invalid response body: %w", parseErr))
		call.Error = appErr.Error()
		c.logCall(ctx, call)
		return false, nil, appErr
	}

	// Transport-level success from here on.
	c.recordSuccess()

	status := resp.StatusCode
	if status < 400 && env.Success != nil && !*env.Success && len(raw) > 0 {
		status = http.StatusBadRequest // defensive: envelope explicitly disagrees with HTTP status
	}
	if status >= 400 {
		appErr := mapError(status, env.Message, env.Errors)
		call.Error = appErr.Error()
		c.logCall(ctx, call)
		return false, nil, appErr
	}

	call.Success = true
	c.logCall(ctx, call)

	if out != nil && len(env.Data) > 0 && !bytes.Equal(env.Data, []byte("null")) {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return false, nil, apperr.External(Provider, fmt.Errorf("decode response data: %w", err))
		}
	}
	return false, env.Meta, nil
}

// logCall records the call without inheriting the request's cancellation (the
// integration log must be written even when the caller's context expired).
func (c *Client) logCall(ctx context.Context, call ports.IntegrationCall) {
	c.log.Log(context.WithoutCancel(ctx), call)
}

// mapError converts an RDash HTTP/envelope status into an apperr, attaching
// per-field validation details when the envelope carried an errors map.
func mapError(status int, message string, errs map[string][]string) *apperr.Error {
	if message == "" {
		message = http.StatusText(status)
	}
	msg := "rdash: " + message
	switch status {
	case http.StatusNotFound:
		return apperr.New(apperr.CodeNotFound, msg)
	case http.StatusConflict:
		return apperr.New(apperr.CodeConflict, msg)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return apperr.Validation(msg, fieldErrorsFrom(errs)...)
	case http.StatusUnauthorized, http.StatusForbidden:
		return apperr.New(apperr.CodeExternal, msg+" (check RDash credentials)")
	case http.StatusTooManyRequests:
		return apperr.New(apperr.CodeRateLimited, msg)
	default:
		return apperr.New(apperr.CodeExternal, msg)
	}
}

// fieldErrorsFrom converts a Dewabiz {field: [messages]} validation-error map
// into sorted (deterministic) apperr.FieldError details.
func fieldErrorsFrom(errs map[string][]string) []apperr.FieldError {
	if len(errs) == 0 {
		return nil
	}
	out := make([]apperr.FieldError, 0, len(errs))
	for field, msgs := range errs {
		if len(msgs) == 0 {
			continue
		}
		out = append(out, apperr.FieldError{Field: field, Message: msgs[0]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// Circuit breaker (lite)

// checkBreaker fails fast with an EXTERNAL apperr while the breaker is open.
func (c *Client) checkBreaker() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.openUntil.IsZero() && c.clock.Now().Before(c.openUntil) {
		return apperr.New(apperr.CodeExternal, "rdash: circuit breaker open, failing fast")
	}
	return nil
}

// recordFailure counts a transport-level failure; reaching the threshold
// opens the breaker for the cooldown window.
func (c *Client) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fails++
	if c.fails >= c.cfg.BreakerThreshold {
		c.openUntil = c.clock.Now().Add(c.cfg.BreakerCooldown)
	}
}

// recordSuccess resets the breaker after any healthy server response.
func (c *Client) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fails = 0
	c.openUntil = time.Time{}
}

// Helpers

// resolveCredentials returns the effective reseller_id/api_key for one HTTP
// call: c.resolve's result when it succeeds, otherwise the static
// (env-sourced) Config values - logged as a warning so a credential lookup
// failure is never silently indistinguishable from "everything is fine."
func (c *Client) resolveCredentials(ctx context.Context) Credentials {
	fallback := Credentials{BaseURL: c.cfg.BaseURL, ResellerID: c.cfg.ResellerID, APIKey: c.cfg.APIKey}
	if c.resolve == nil {
		return fallback
	}
	creds, err := c.resolve(ctx)
	if err != nil {
		slog.Default().WarnContext(ctx, "rdash: resolve live credentials failed, using static fallback", "error", err)
		return fallback
	}
	creds.BaseURL = strings.TrimRight(creds.BaseURL, "/")
	return creds
}

func normalizeName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// parseDate parses a YYYY-MM-DD registrar date; empty input yields zero time.
func parseDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(dateFormat, s)
	if err != nil {
		return time.Time{}, apperr.External(Provider, fmt.Errorf("invalid date %q: %w", s, err))
	}
	return t, nil
}

// parseRupiah parses a decimal-string IDR amount ("1013000.00") into whole
// rupiah, truncating any fractional part (CONTRACTS.md §0: money is int64
// whole rupiah, never floats).
func parseRupiah(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	whole := s
	if i := strings.IndexByte(s, '.'); i >= 0 {
		whole = s[:i]
	}
	v, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, apperr.External(Provider, fmt.Errorf("invalid balance %q: %w", s, err))
	}
	return v, nil
}

func atLeastOne(years int) int {
	if years < 1 {
		return 1
	}
	return years
}

// nopLogger discards integration calls (used when New receives a nil logger).
type nopLogger struct{}

func (nopLogger) Log(context.Context, ports.IntegrationCall) {}

// sysClock is the fallback ports.Clock.
type sysClock struct{}

func (sysClock) Now() time.Time { return time.Now() }
