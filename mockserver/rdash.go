package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Dewabiz "RDash" v1 registrar mock. Basic auth `reseller_id:api_key` - any
// non-empty pair accepted except the literal pair bad:bad (-> 401). Requests
// are application/x-www-form-urlencoded (form fields); GETs pass query
// parameters. Every response uses the envelope {success, data, message},
// validation failures additionally carry {errors: {field: [messages]}}.
// Dates use format YYYY-MM-DD.
//
// The real API keys almost every per-domain mutation by a numeric domain_id
// behind a customer->contact->domain resource graph, not by domain name - this
// mock reproduces that shape so the adapter's name->id resolution
// (GET /domains/details) and customer/contact provisioning are exercised the
// same way they would be against production.

const rdashDateFormat = "2006-01-02"

type rdashCustomer struct {
	ID           int64
	Name         string
	Email        string
	Organization string
	Street1      string
	City         string
	State        string
	CountryCode  string
	PostalCode   string
	Voice        string
}

type rdashContact struct {
	ID           int64
	CustomerID   int64
	Label        string
	Name         string
	Email        string
	Organization string
	Street1      string
	City         string
	State        string
	CountryCode  string
	PostalCode   string
	Voice        string
}

type rdashDnsRecord struct {
	Prefix  string
	Type    string
	Content string
	TTL     int
}

type rdashDomain struct {
	ID                                                                   int64
	Name                                                                 string
	CustomerID                                                           int64
	Nameservers                                                          []string
	Status                                                               int
	StatusLabel                                                          string
	ExpiredAt                                                            time.Time
	AdminContactID, TechContactID, BillingContactID, RegistrantContactID int64
	DNS                                                                  []rdashDnsRecord
}

func defaultNameservers() []string {
	return []string{"ns1.mock-rdash.id", "ns2.mock-rdash.id"}
}

func defaultDNSRecords(name string) []rdashDnsRecord {
	return []rdashDnsRecord{
		{Prefix: "@", Type: "A", Content: "127.0.0.1", TTL: 3600},
		{Prefix: "www", Type: "CNAME", Content: name + ".", TTL: 3600},
	}
}

// writeRDash writes the {success, data, message} envelope.
func writeRDash(w http.ResponseWriter, status int, message string, data any) {
	writeJSON(w, status, map[string]any{
		"success": status < 400,
		"data":    data,
		"message": message,
	})
}

// writeRDashValidation writes a 422 {success:false, message, errors} envelope.
func writeRDashValidation(w http.ResponseWriter, message string, errs map[string][]string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"success": false,
		"message": message,
		"errors":  errs,
	})
}

// rdashAuth enforces Basic auth; returns the reseller_id and false after
// writing the 401 response when auth fails.
func rdashAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	user, pass, ok := r.BasicAuth()
	if !ok || user == "" || pass == "" || (user == "bad" && pass == "bad") {
		writeRDash(w, http.StatusUnauthorized, "unauthorized", nil)
		return "", false
	}
	return user, true
}

func (s *Server) domainPayload(d *rdashDomain) map[string]any {
	payload := map[string]any{
		"id":           d.ID,
		"name":         d.Name,
		"nameserver_1": nsAt(d.Nameservers, 0),
		"nameserver_2": nsAt(d.Nameservers, 1),
		"nameserver_3": nsAt(d.Nameservers, 2),
		"nameserver_4": nsAt(d.Nameservers, 3),
		"nameserver_5": nsAt(d.Nameservers, 4),
		"status":       d.Status,
		"status_label": d.StatusLabel,
		"expired_at":   d.ExpiredAt.UTC().Format(rdashDateFormat),
		"customer":     map[string]any{"id": d.CustomerID},
	}
	if c, ok := s.rdashContacts[d.RegistrantContactID]; ok {
		payload["registrant_contact"] = contactPayload(c)
	}
	return payload
}

func contactPayload(c *rdashContact) map[string]any {
	return map[string]any{
		"id":           c.ID,
		"label":        c.Label,
		"name":         c.Name,
		"email":        c.Email,
		"organization": c.Organization,
		"street_1":     c.Street1,
		"city":         c.City,
		"state":        c.State,
		"country_code": c.CountryCode,
		"postal_code":  c.PostalCode,
		"voice":        c.Voice,
	}
}

func nsAt(ns []string, i int) any {
	if i < len(ns) && ns[i] != "" {
		return ns[i]
	}
	return nil
}

// getDomainByID fetches a stored domain by numeric id; on miss it writes the
// 404 envelope.
func (s *Server) getDomainByID(w http.ResponseWriter, r *http.Request) (*rdashDomain, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeRDash(w, http.StatusNotFound, "domain not found", nil)
		return nil, false
	}
	s.mu.Lock()
	d, ok := s.rdashDomains[id]
	s.mu.Unlock()
	if !ok {
		writeRDash(w, http.StatusNotFound, "domain not found", nil)
		return nil, false
	}
	return d, true
}

// requireForm parses the request's application/x-www-form-urlencoded body.
func requireForm(r *http.Request) url.Values {
	_ = r.ParseForm()
	return r.PostForm
}

// GET /v1/domains/availability?domain=X
func (s *Server) handleRDashAvailability(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("domain")))
	if name == "" {
		writeRDashValidation(w, "The domain field is required.", map[string][]string{"domain": {"The domain field is required."}})
		return
	}
	s.mu.Lock()
	_, registered := s.rdashDomainsByName[name]
	s.mu.Unlock()
	available := 0
	message := "domain already registered"
	if !strings.Contains(name, "taken") && !registered {
		available = 1
		message = "available"
	}
	// Single-resource endpoint (one domain per call) - the real Dewabiz API
	// returns `data` as a plain object here, not wrapped in an array.
	writeRDash(w, http.StatusOK, "Success", map[string]any{
		"name":      name,
		"available": available,
		"message":   message,
	})
}

// GET /v1/domains/details?domain_name=X
func (s *Server) handleRDashDetails(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("domain_name")))
	s.mu.Lock()
	id, ok := s.rdashDomainsByName[name]
	var payload map[string]any
	if ok {
		payload = s.domainPayload(s.rdashDomains[id])
	}
	s.mu.Unlock()
	if !ok {
		writeRDash(w, http.StatusNotFound, "domain not found", nil)
		return
	}
	writeRDash(w, http.StatusOK, "Success", payload)
}

// GET /v1/customers?email= - a bare-list envelope like /account/prices, no
// "success" field (see rdash.go's envelope doc comment on the real adapter).
// The real API's ensureCustomer looks an email up here before creating a
// customer (Dewabiz rejects a second POST /customers for the same email), so
// the mock must support at least an exact-email filter for that path to be
// exercisable at all.
func (s *Server) handleRDashCustomerList(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
	s.mu.Lock()
	items := []map[string]any{}
	for _, c := range s.rdashCustomers {
		if email != "" && !strings.EqualFold(c.Email, email) {
			continue
		}
		items = append(items, map[string]any{
			"id": c.ID, "name": c.Name, "email": c.Email, "organization": c.Organization,
			"street_1": c.Street1, "street_2": nil, "city": c.City, "state": c.State,
			"country_code": c.CountryCode, "postal_code": c.PostalCode, "voice": c.Voice,
		})
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"data":  items,
		"links": map[string]any{"first": "/v1/customers?page=1", "last": "/v1/customers?page=1", "prev": nil, "next": nil},
		"meta":  map[string]any{"current_page": 1, "last_page": 1, "per_page": 10, "total": len(items)},
	})
}

// POST /v1/customers
func (s *Server) handleRDashCustomerCreate(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	form := requireForm(r)
	required := []string{"name", "email", "organization", "street_1", "city", "state", "country_code", "postal_code", "voice", "password", "password_confirmation"}
	if errs := missingFields(form, required); len(errs) > 0 {
		writeRDashValidation(w, "The given data was invalid", errs)
		return
	}
	if form.Get("password") != form.Get("password_confirmation") {
		writeRDashValidation(w, "The password confirmation and password must match.",
			map[string][]string{"password_confirmation": {"The password confirmation and password must match."}})
		return
	}
	s.mu.Lock()
	s.rdashCustomerSeq++
	c := &rdashCustomer{
		ID: s.rdashCustomerSeq, Name: form.Get("name"), Email: form.Get("email"),
		Organization: form.Get("organization"), Street1: form.Get("street_1"), City: form.Get("city"),
		State: form.Get("state"), CountryCode: strings.ToUpper(form.Get("country_code")),
		PostalCode: form.Get("postal_code"), Voice: form.Get("voice"),
	}
	s.rdashCustomers[c.ID] = c
	s.mu.Unlock()
	writeRDash(w, http.StatusOK, "Customer created successfully", map[string]any{"id": c.ID, "name": c.Name, "email": c.Email})
}

// POST /v1/customers/{customer_id}/contacts
func (s *Server) handleRDashContactCreate(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	customerID, err := strconv.ParseInt(r.PathValue("customer_id"), 10, 64)
	if err != nil {
		writeRDash(w, http.StatusNotFound, "customer not found", nil)
		return
	}
	s.mu.Lock()
	_, exists := s.rdashCustomers[customerID]
	s.mu.Unlock()
	if !exists {
		writeRDash(w, http.StatusNotFound, "customer not found", nil)
		return
	}
	form := requireForm(r)
	required := []string{"label", "name", "email", "organization", "street_1", "city", "state", "country_code", "postal_code", "voice"}
	if errs := missingFields(form, required); len(errs) > 0 {
		writeRDashValidation(w, "The given data was invalid", errs)
		return
	}
	s.mu.Lock()
	s.rdashContactSeq++
	c := &rdashContact{
		ID: s.rdashContactSeq, CustomerID: customerID, Label: form.Get("label"), Name: form.Get("name"),
		Email: form.Get("email"), Organization: form.Get("organization"), Street1: form.Get("street_1"),
		City: form.Get("city"), State: form.Get("state"), CountryCode: strings.ToUpper(form.Get("country_code")),
		PostalCode: form.Get("postal_code"), Voice: form.Get("voice"),
	}
	s.rdashContacts[c.ID] = c
	s.mu.Unlock()
	writeRDash(w, http.StatusOK, "Contact created successfully", contactPayload(c))
}

// POST /v1/domains - Register.
func (s *Server) handleRDashRegisterDomain(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	form := requireForm(r)
	name := strings.ToLower(strings.TrimSpace(form.Get("name")))
	if name == "" {
		writeRDashValidation(w, "The name field is required.", map[string][]string{"name": {"The name field is required."}})
		return
	}
	customerID, err := strconv.ParseInt(form.Get("customer_id"), 10, 64)
	if err != nil {
		writeRDashValidation(w, "The customer id is not exist.", map[string][]string{"customer_id": {"The customer id is not exist."}})
		return
	}
	s.mu.Lock()
	_, customerOK := s.rdashCustomers[customerID]
	s.mu.Unlock()
	if !customerOK {
		writeRDashValidation(w, "The customer id is not exist.", map[string][]string{"customer_id": {"The customer id is not exist."}})
		return
	}
	s.mu.Lock()
	_, taken := s.rdashDomainsByName[name]
	s.mu.Unlock()
	if strings.Contains(name, "taken") || taken {
		writeRDash(w, http.StatusConflict, "domain not available", nil)
		return
	}
	period, err := strconv.Atoi(form.Get("period"))
	if err != nil || period < 1 {
		period = 1
	}
	ns := formNameservers(form)
	if len(ns) == 0 {
		ns = defaultNameservers()
	}

	s.mu.Lock()
	registrantContactID := s.ensureDefaultContactLocked(customerID, form.Get("registrant_contact_id"))
	s.rdashDomainSeq++
	d := &rdashDomain{
		ID: s.rdashDomainSeq, Name: name, CustomerID: customerID, Nameservers: ns,
		Status: 1, StatusLabel: "Active", ExpiredAt: time.Now().AddDate(period, 0, 0),
		AdminContactID: registrantContactID, TechContactID: registrantContactID,
		BillingContactID: registrantContactID, RegistrantContactID: registrantContactID,
		DNS: defaultDNSRecords(name),
	}
	s.rdashDomains[d.ID] = d
	s.rdashDomainsByName[name] = d.ID
	payload := s.domainPayload(d)
	s.mu.Unlock()

	writeRDash(w, http.StatusOK, "Domain registered successfully", payload)
}

// POST /v1/domains/transfer - Transfer.
func (s *Server) handleRDashTransferDomain(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	form := requireForm(r)
	name := strings.ToLower(strings.TrimSpace(form.Get("name")))
	if name == "" {
		writeRDashValidation(w, "The name field is required.", map[string][]string{"name": {"The name field is required."}})
		return
	}
	if form.Get("auth_code") == "WRONG" {
		writeRDashValidation(w, "The auth code is invalid.", map[string][]string{"auth_code": {"The auth code is invalid."}})
		return
	}
	customerID, err := strconv.ParseInt(form.Get("customer_id"), 10, 64)
	if err != nil {
		writeRDashValidation(w, "The customer id is not exist.", map[string][]string{"customer_id": {"The customer id is not exist."}})
		return
	}
	s.mu.Lock()
	_, customerOK := s.rdashCustomers[customerID]
	_, exists := s.rdashDomainsByName[name]
	s.mu.Unlock()
	if !customerOK {
		writeRDashValidation(w, "The customer id is not exist.", map[string][]string{"customer_id": {"The customer id is not exist."}})
		return
	}
	if exists {
		writeRDash(w, http.StatusConflict, "domain already in account", nil)
		return
	}
	period, err := strconv.Atoi(form.Get("period"))
	if err != nil || period < 1 {
		period = 1
	}
	ns := formNameservers(form)
	if len(ns) == 0 {
		ns = defaultNameservers()
	}

	s.mu.Lock()
	registrantContactID := s.ensureDefaultContactLocked(customerID, "")
	s.rdashDomainSeq++
	d := &rdashDomain{
		ID: s.rdashDomainSeq, Name: name, CustomerID: customerID, Nameservers: ns,
		Status: 1, StatusLabel: "Active", ExpiredAt: time.Now().AddDate(period, 0, 0),
		AdminContactID: registrantContactID, TechContactID: registrantContactID,
		BillingContactID: registrantContactID, RegistrantContactID: registrantContactID,
		DNS: defaultDNSRecords(name),
	}
	s.rdashDomains[d.ID] = d
	s.rdashDomainsByName[name] = d.ID
	payload := s.domainPayload(d)
	s.mu.Unlock()

	writeRDash(w, http.StatusOK, "Domain registered successfully", payload)
}

// POST /v1/domains/{id}/renew
func (s *Server) handleRDashRenew(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	d, ok := s.getDomainByID(w, r)
	if !ok {
		return
	}
	form := requireForm(r)
	period, err := strconv.Atoi(form.Get("period"))
	if err != nil || period < 1 {
		period = 1
	}
	s.mu.Lock()
	currentDate := d.ExpiredAt.UTC().Format(rdashDateFormat)
	s.mu.Unlock()
	if got := form.Get("current_date"); got != currentDate {
		writeRDashValidation(w, "Expiry date is not correct.", map[string][]string{"current_date": {"Expiry date is not correct."}})
		return
	}

	s.mu.Lock()
	d.ExpiredAt = d.ExpiredAt.AddDate(period, 0, 0)
	d.Status, d.StatusLabel = 1, "Active"
	payload := s.domainPayload(d)
	s.mu.Unlock()
	writeRDash(w, http.StatusOK, "Renew domain successfully", payload)
}

// PUT /v1/domains/{id}/ns
func (s *Server) handleRDashUpdateNS(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	d, ok := s.getDomainByID(w, r)
	if !ok {
		return
	}
	form := requireForm(r)
	ns := formNameservers(form)
	if len(ns) < 2 {
		writeRDashValidation(w, "At least two nameservers are required.",
			map[string][]string{"nameserver": {"At least two nameservers are required."}})
		return
	}
	s.mu.Lock()
	d.Nameservers = ns
	payload := s.domainPayload(d)
	s.mu.Unlock()
	writeRDash(w, http.StatusOK, "Nameserver updated successfully", payload)
}

// PUT /v1/domains/{id}/contacts
func (s *Server) handleRDashUpdateContacts(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	d, ok := s.getDomainByID(w, r)
	if !ok {
		return
	}
	form := requireForm(r)
	ids := map[string]int64{}
	for _, field := range []string{"admin_contact_id", "tech_contact_id", "billing_contact_id", "registrant_contact_id"} {
		id, err := strconv.ParseInt(form.Get(field), 10, 64)
		if err != nil {
			writeRDashValidation(w, fmt.Sprintf("The %s field is required.", field),
				map[string][]string{field: {fmt.Sprintf("The %s field is required.", field)}})
			return
		}
		ids[field] = id
	}
	s.mu.Lock()
	for _, id := range ids {
		if _, exists := s.rdashContacts[id]; !exists {
			s.mu.Unlock()
			writeRDashValidation(w, "The contact id is not exist.", map[string][]string{"contact_id": {"The contact id is not exist."}})
			return
		}
	}
	d.AdminContactID = ids["admin_contact_id"]
	d.TechContactID = ids["tech_contact_id"]
	d.BillingContactID = ids["billing_contact_id"]
	d.RegistrantContactID = ids["registrant_contact_id"]
	payload := s.domainPayload(d)
	s.mu.Unlock()
	writeRDash(w, http.StatusOK, "Contact updated successfully", payload)
}

// GET /v1/domains/{id}/auth_code
func (s *Server) handleRDashAuthCode(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	d, ok := s.getDomainByID(w, r)
	if !ok {
		return
	}
	writeRDash(w, http.StatusOK, "Success", "MOCK-EPP-"+d.Name)
}

// GET /v1/domains/{id}/dns
func (s *Server) handleRDashDNSGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	d, ok := s.getDomainByID(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	items := make([]map[string]any, 0, len(d.DNS))
	for _, rec := range d.DNS {
		items = append(items, map[string]any{
			"prefix": rec.Prefix, "type": rec.Type, "content": rec.Content, "ttl": rec.TTL,
		})
	}
	s.mu.Unlock()
	writeRDash(w, http.StatusOK, "Success", items)
}

// POST /v1/domains/{id}/dns - replaces the full record set.
func (s *Server) handleRDashDNSReplace(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	d, ok := s.getDomainByID(w, r)
	if !ok {
		return
	}
	form := requireForm(r)
	recs, err := parseRecordsForm(form)
	if err != nil {
		writeRDashValidation(w, err.Error(), map[string][]string{"records.0.name": {err.Error()}})
		return
	}
	s.mu.Lock()
	d.DNS = recs
	s.mu.Unlock()
	writeRDash(w, http.StatusOK, "Records added successfully", "")
}

// GET /v1/account/profile
func (s *Server) handleRDashProfile(w http.ResponseWriter, r *http.Request) {
	resellerID, ok := rdashAuth(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(resellerID, 10, 64)
	if err != nil {
		id = 1
	}
	writeRDash(w, http.StatusOK, "Success", map[string]any{
		"id": id, "name": "Mock Reseller", "email": "reseller@example.test", "currency": "IDR",
	})
}

// GET /v1/account/balance
func (s *Server) handleRDashBalance(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	writeRDash(w, http.StatusOK, "Success", map[string]any{"currency": "IDR", "balance": "10000000.00"})
}

// GET /v1/account/prices?domainExtension[extension]=X - synthesizes a price
// for whatever extension is asked (real reseller accounts only price the
// extensions they've enabled; the mock has no such catalog to seed, so it
// quotes a flat per-year rate for anything, same shape as the real
// PricesResponse).
//
// Without a domainExtension filter (confirmed optional-and-means-all against
// the live Dewabiz swagger doc), it instead paginates through a fixed,
// realistic catalog (rdashCatalogTLDs below) - feeding the admin "Import
// from Registrar" flow. Page size is capped at rdashCatalogPageCap
// regardless of the caller's requested `limit`, so a small fixed catalog
// still exercises multi-page traversal the way a real, much larger registrar
// catalog would.
func (s *Server) handleRDashPrices(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	ext := r.URL.Query().Get("domainExtension[extension]")
	if ext != "" {
		const perYear = 150000
		registration := map[string]any{}
		for y := 1; y <= 10; y++ {
			registration[strconv.Itoa(y)] = perYear * y
		}
		writeRDash(w, http.StatusOK, "Success", []map[string]any{{
			"domain_extension": map[string]any{"extension": ext},
			"currency":         "IDR",
			"registration":     registration,
		}})
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > rdashCatalogPageCap {
		limit = rdashCatalogPageCap
	}
	total := len(rdashCatalogTLDs)
	lastPage := (total + limit - 1) / limit
	if lastPage < 1 {
		lastPage = 1
	}
	start := (page - 1) * limit
	items := []map[string]any{}
	if start < total {
		end := start + limit
		if end > total {
			end = total
		}
		for _, e := range rdashCatalogTLDs[start:end] {
			items = append(items, e.toJSON())
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": items,
		"links": map[string]any{
			"first": "/v1/account/prices?page=1", "last": fmt.Sprintf("/v1/account/prices?page=%d", lastPage),
			"prev": nil, "next": nil,
		},
		"meta": map[string]any{
			"current_page": page, "last_page": lastPage, "per_page": limit, "total": total,
		},
	})
}

// rdashCatalogPageCap is the mock's max page size for the unfiltered
// GET /account/prices catalog listing - deliberately smaller than the
// adapter's requested limit so a dozen fixture TLDs still force the
// ListCatalogPrices pagination loop through multiple pages.
const rdashCatalogPageCap = 5

// rdashCatalogEntry is one fixture row of the mock's registrar TLD
// pricelist, used only by the unfiltered (bulk) branch of
// handleRDashPrices.
type rdashCatalogEntry struct {
	Extension    string
	RegisterBase int64 // 1-year register price; year N = base * N
	RenewBase    int64 // 1-year renew price; year N = base * N
	Transfer     int64
	Redemption   int64 // restore price
}

// toJSON builds the wire shape one GET /account/prices item has - mixing
// numeric (registration) and quoted-string (renewal) year values on purpose,
// matching the real API's observed inconsistency (see rdash.toInt64Lenient).
func (e rdashCatalogEntry) toJSON() map[string]any {
	registration := map[string]any{}
	renewal := map[string]any{}
	for y := 1; y <= 10; y++ {
		registration[strconv.Itoa(y)] = e.RegisterBase * int64(y)
		renewal[strconv.Itoa(y)] = strconv.FormatInt(e.RenewBase*int64(y), 10)
	}
	return map[string]any{
		"domain_extension": map[string]any{"extension": e.Extension},
		"currency":         "IDR",
		"registration":     registration,
		"renewal":          renewal,
		"transfer":         fmt.Sprintf("%d.00", e.Transfer),
		"redemption":       fmt.Sprintf("%d.00", e.Redemption),
	}
}

// rdashCatalogTLDs is the mock's fixed registrar pricelist - a mix of the
// Indonesian ccTLD family and a couple gTLDs, mirroring what a real Dewabiz
// reseller account would see.
var rdashCatalogTLDs = []rdashCatalogEntry{
	{Extension: ".id", RegisterBase: 230000, RenewBase: 230000, Transfer: 230000, Redemption: 1150000},
	{Extension: ".co.id", RegisterBase: 150000, RenewBase: 150000, Transfer: 150000, Redemption: 750000},
	{Extension: ".my.id", RegisterBase: 75000, RenewBase: 75000, Transfer: 75000, Redemption: 375000},
	{Extension: ".web.id", RegisterBase: 75000, RenewBase: 75000, Transfer: 75000, Redemption: 375000},
	{Extension: ".or.id", RegisterBase: 75000, RenewBase: 75000, Transfer: 75000, Redemption: 375000},
	{Extension: ".ac.id", RegisterBase: 75000, RenewBase: 75000, Transfer: 75000, Redemption: 375000},
	{Extension: ".sch.id", RegisterBase: 75000, RenewBase: 75000, Transfer: 75000, Redemption: 375000},
	{Extension: ".biz.id", RegisterBase: 100000, RenewBase: 100000, Transfer: 100000, Redemption: 500000},
	{Extension: ".net.id", RegisterBase: 150000, RenewBase: 150000, Transfer: 150000, Redemption: 750000},
	{Extension: ".desa.id", RegisterBase: 75000, RenewBase: 75000, Transfer: 75000, Redemption: 375000},
	{Extension: ".com", RegisterBase: 180000, RenewBase: 195000, Transfer: 180000, Redemption: 900000},
	{Extension: ".net", RegisterBase: 195000, RenewBase: 210000, Transfer: 195000, Redemption: 975000},
}

// Helpers

// ensureDefaultContactLocked returns registrantContactID if it refers to an
// existing contact, otherwise auto-creates a "Default" contact from the
// customer's own profile (mirrors the real API's documented auto-create
// behavior when registrant_contact_id is omitted). Caller must hold s.mu.
func (s *Server) ensureDefaultContactLocked(customerID int64, registrantContactID string) int64 {
	if id, err := strconv.ParseInt(registrantContactID, 10, 64); err == nil {
		if _, ok := s.rdashContacts[id]; ok {
			return id
		}
	}
	cust := s.rdashCustomers[customerID]
	s.rdashContactSeq++
	c := &rdashContact{
		ID: s.rdashContactSeq, CustomerID: customerID, Label: "Default",
		Name: cust.Name, Email: cust.Email, Organization: cust.Organization, Street1: cust.Street1,
		City: cust.City, State: cust.State, CountryCode: cust.CountryCode,
		PostalCode: cust.PostalCode, Voice: cust.Voice,
	}
	s.rdashContacts[c.ID] = c
	return c.ID
}

// missingFields reports {field: [required message]} for every blank field.
func missingFields(form url.Values, fields []string) map[string][]string {
	errs := map[string][]string{}
	for _, f := range fields {
		if strings.TrimSpace(form.Get(f)) == "" {
			errs[f] = []string{fmt.Sprintf("The %s field is required.", f)}
		}
	}
	return errs
}

// formNameservers reads nameserver[0..4] from a form body.
func formNameservers(form url.Values) []string {
	var ns []string
	for i := 0; i < 5; i++ {
		v := strings.TrimSpace(form.Get(fmt.Sprintf("nameserver[%d]", i)))
		if v == "" {
			continue
		}
		ns = append(ns, v)
	}
	return ns
}

// parseRecordsForm reads records[0..][name|type|content|ttl] from a form
// body; the registrar requires at least one record.
func parseRecordsForm(form url.Values) ([]rdashDnsRecord, error) {
	var out []rdashDnsRecord
	for i := 0; ; i++ {
		nameKey := fmt.Sprintf("records[%d][name]", i)
		if _, ok := form[nameKey]; !ok {
			break
		}
		ttl, err := strconv.Atoi(form.Get(fmt.Sprintf("records[%d][ttl]", i)))
		if err != nil || ttl <= 0 {
			ttl = 3600
		}
		out = append(out, rdashDnsRecord{
			Prefix:  form.Get(nameKey),
			Type:    strings.ToUpper(form.Get(fmt.Sprintf("records[%d][type]", i))),
			Content: form.Get(fmt.Sprintf("records[%d][content]", i)),
			TTL:     ttl,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the records.0.name field is required")
	}
	return out, nil
}
