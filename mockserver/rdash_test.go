package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

const (
	rdashUser = "584"
	rdashPass = "apikey123"
)

// rdashGet issues an authenticated GET with query parameters.
func rdashGet(t *testing.T, ts *httptest.Server, path string, query url.Values) (*http.Response, map[string]any) {
	t.Helper()
	u := ts.URL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return jsonReq(t, http.MethodGet, u, nil, rdashUser, rdashPass)
}

// rdashForm issues an authenticated POST/PUT/DELETE with a form body.
func rdashForm(t *testing.T, ts *httptest.Server, method, path string, form url.Values) (*http.Response, map[string]any) {
	t.Helper()
	return formReq(t, method, ts.URL+path, form, rdashUser, rdashPass)
}

func rdashDataMap(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data missing or not an object: %v", m)
	}
	return data
}

func rdashDataArray(t *testing.T, m map[string]any) []any {
	t.Helper()
	data, ok := m["data"].([]any)
	if !ok {
		t.Fatalf("data missing or not an array: %v", m)
	}
	return data
}

// sampleCustomerForm is a fully valid /customers create form.
func sampleCustomerForm() url.Values {
	return url.Values{
		"name": {"Budi Santoso"}, "email": {"budi@example.id"},
		"password": {"S3cret!!"}, "password_confirmation": {"S3cret!!"},
		"organization": {"PT Budi"}, "street_1": {"Jl. Merdeka 1"},
		"city": {"Jakarta"}, "state": {"DKI Jakarta"},
		"country_code": {"ID"}, "postal_code": {"12345"}, "voice": {"081234567890"},
	}
}

// rdashCustomer creates a customer and returns its numeric id.
func rdashNewCustomer(t *testing.T, ts *httptest.Server) int64 {
	t.Helper()
	resp, m := rdashForm(t, ts, http.MethodPost, "/v1/customers", sampleCustomerForm())
	wantStatus(t, resp, http.StatusOK)
	id, ok := rdashDataMap(t, m)["id"].(float64)
	if !ok {
		t.Fatalf("customer id missing: %v", m)
	}
	return int64(id)
}

// rdashRegister creates a customer then registers name for `years`,
// returning the domain payload (including its numeric id).
func rdashRegister(t *testing.T, ts *httptest.Server, name string, years int) map[string]any {
	t.Helper()
	customerID := rdashNewCustomer(t, ts)
	resp, m := rdashForm(t, ts, http.MethodPost, "/v1/domains", url.Values{
		"name": {name}, "period": {strconv.Itoa(years)},
		"customer_id":   {strconv.FormatInt(customerID, 10)},
		"nameserver[0]": {"ns1.example.id"}, "nameserver[1]": {"ns2.example.id"},
	})
	wantStatus(t, resp, http.StatusOK)
	return rdashDataMap(t, m)
}

func domainID(t *testing.T, data map[string]any) int64 {
	t.Helper()
	id, ok := data["id"].(float64)
	if !ok {
		t.Fatalf("domain id missing: %v", data)
	}
	return int64(id)
}

func TestRDashAuth(t *testing.T) {
	_, ts := newTestServer(t)

	t.Run("missing auth 401", func(t *testing.T) {
		resp, m := jsonReq(t, http.MethodGet, ts.URL+"/v1/account/profile", nil, "", "")
		wantStatus(t, resp, http.StatusUnauthorized)
		wantField(t, m, "success", false)
	})

	t.Run("bad:bad rejected", func(t *testing.T) {
		resp, _ := jsonReq(t, http.MethodGet, ts.URL+"/v1/account/profile", nil, "bad", "bad")
		wantStatus(t, resp, http.StatusUnauthorized)
	})

	t.Run("any other pair accepted", func(t *testing.T) {
		resp, m := rdashGet(t, ts, "/v1/account/profile", nil)
		wantStatus(t, resp, http.StatusOK)
		data := rdashDataMap(t, m)
		wantField(t, data, "id", 584)
		wantField(t, data, "currency", "IDR")
	})
}

func TestRDashAccountBalance(t *testing.T) {
	_, ts := newTestServer(t)
	resp, m := rdashGet(t, ts, "/v1/account/balance", nil)
	wantStatus(t, resp, http.StatusOK)
	wantField(t, rdashDataMap(t, m), "balance", "10000000.00")
}

func TestRDashAccountPrices(t *testing.T) {
	_, ts := newTestServer(t)

	t.Run("quotes a flat per-year rate for any extension", func(t *testing.T) {
		resp, m := rdashGet(t, ts, "/v1/account/prices", url.Values{"domainExtension[extension]": {".my.id"}})
		wantStatus(t, resp, http.StatusOK)
		items := rdashDataArray(t, m)
		if len(items) != 1 {
			t.Fatalf("items len = %d, want 1", len(items))
		}
		item := items[0].(map[string]any)
		ext := item["domain_extension"].(map[string]any)["extension"]
		if ext != ".my.id" {
			t.Fatalf("extension = %v, want .my.id", ext)
		}
		registration := item["registration"].(map[string]any)
		wantField(t, registration, "1", 150000)
		wantField(t, registration, "2", 300000)
	})

	t.Run("empty extension paginates the full mock catalog", func(t *testing.T) {
		resp, m := rdashGet(t, ts, "/v1/account/prices", nil)
		wantStatus(t, resp, http.StatusOK)
		items := rdashDataArray(t, m)
		if len(items) != rdashCatalogPageCap {
			t.Fatalf("page 1 items len = %d, want %d (page size cap)", len(items), rdashCatalogPageCap)
		}
		meta := m["meta"].(map[string]any)
		wantField(t, meta, "current_page", 1)
		wantField(t, meta, "total", len(rdashCatalogTLDs))
		lastPage := (len(rdashCatalogTLDs) + rdashCatalogPageCap - 1) / rdashCatalogPageCap
		wantField(t, meta, "last_page", lastPage)

		first := items[0].(map[string]any)
		ext := first["domain_extension"].(map[string]any)["extension"]
		if ext != rdashCatalogTLDs[0].Extension {
			t.Fatalf("first item extension = %v, want %v", ext, rdashCatalogTLDs[0].Extension)
		}
		if _, ok := first["renewal"]; !ok {
			t.Fatal("catalog item missing renewal field")
		}
		if _, ok := first["transfer"]; !ok {
			t.Fatal("catalog item missing transfer field")
		}
	})

	t.Run("caller's limit is capped and the last page is short", func(t *testing.T) {
		_, m := rdashGet(t, ts, "/v1/account/prices", url.Values{"limit": {"100"}, "page": {"3"}})
		items := rdashDataArray(t, m)
		wantLen := len(rdashCatalogTLDs) - 2*rdashCatalogPageCap
		if len(items) != wantLen {
			t.Fatalf("page 3 items len = %d, want %d", len(items), wantLen)
		}
	})
}

func TestRDashAvailability(t *testing.T) {
	_, ts := newTestServer(t)

	resp, m := rdashGet(t, ts, "/v1/domains/availability", url.Values{"domain": {"fresh-name.id"}})
	wantStatus(t, resp, http.StatusOK)
	first := rdashDataMap(t, m)
	wantField(t, first, "name", "fresh-name.id")
	wantField(t, first, "available", 1)

	t.Run("taken substring unavailable", func(t *testing.T) {
		_, m := rdashGet(t, ts, "/v1/domains/availability", url.Values{"domain": {"alreadytaken.com"}})
		res := rdashDataMap(t, m)
		wantField(t, res, "available", 0)
	})

	t.Run("registered domain becomes unavailable", func(t *testing.T) {
		rdashRegister(t, ts, "now-mine.id", 1)
		_, m := rdashGet(t, ts, "/v1/domains/availability", url.Values{"domain": {"now-mine.id"}})
		res := rdashDataMap(t, m)
		wantField(t, res, "available", 0)
	})

	t.Run("missing domain param rejected", func(t *testing.T) {
		resp, _ := rdashGet(t, ts, "/v1/domains/availability", nil)
		wantStatus(t, resp, http.StatusUnprocessableEntity)
	})
}

func TestRDashCustomerCreate(t *testing.T) {
	_, ts := newTestServer(t)

	t.Run("success", func(t *testing.T) {
		resp, m := rdashForm(t, ts, http.MethodPost, "/v1/customers", sampleCustomerForm())
		wantStatus(t, resp, http.StatusOK)
		wantField(t, rdashDataMap(t, m), "email", "budi@example.id")
	})

	t.Run("missing required field rejected", func(t *testing.T) {
		form := sampleCustomerForm()
		form.Del("email")
		resp, m := rdashForm(t, ts, http.MethodPost, "/v1/customers", form)
		wantStatus(t, resp, http.StatusUnprocessableEntity)
		wantField(t, m, "success", false)
	})

	t.Run("password mismatch rejected", func(t *testing.T) {
		form := sampleCustomerForm()
		form.Set("password_confirmation", "different")
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/customers", form)
		wantStatus(t, resp, http.StatusUnprocessableEntity)
	})
}

func TestRDashCustomerList(t *testing.T) {
	_, ts := newTestServer(t)
	rdashNewCustomer(t, ts) // budi@example.id, per sampleCustomerForm

	t.Run("filters by exact email", func(t *testing.T) {
		resp, m := rdashGet(t, ts, "/v1/customers", url.Values{"email": {"budi@example.id"}})
		wantStatus(t, resp, http.StatusOK)
		items := rdashDataArray(t, m)
		if len(items) != 1 {
			t.Fatalf("want 1 matching customer, got %d: %v", len(items), items)
		}
		wantField(t, items[0].(map[string]any), "email", "budi@example.id")
	})

	t.Run("no match returns an empty list, not an error", func(t *testing.T) {
		resp, m := rdashGet(t, ts, "/v1/customers", url.Values{"email": {"nobody@example.id"}})
		wantStatus(t, resp, http.StatusOK)
		if items := rdashDataArray(t, m); len(items) != 0 {
			t.Fatalf("want no matches, got %v", items)
		}
	})

	t.Run("requires auth", func(t *testing.T) {
		resp, _ := jsonReq(t, http.MethodGet, ts.URL+"/v1/customers", nil, "bad", "bad")
		wantStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestRDashContactCreate(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)

	t.Run("success", func(t *testing.T) {
		resp, m := rdashForm(t, ts, http.MethodPost, "/v1/customers/"+strconv.FormatInt(customerID, 10)+"/contacts", url.Values{
			"label": {"Billing"}, "name": {"Siti Aminah"}, "email": {"siti@example.id"},
			"organization": {"PT Budi"}, "street_1": {"Jl. Sudirman 2"}, "city": {"Jakarta"},
			"state": {"DKI Jakarta"}, "country_code": {"ID"}, "postal_code": {"12345"}, "voice": {"0812"},
		})
		wantStatus(t, resp, http.StatusOK)
		wantField(t, rdashDataMap(t, m), "name", "Siti Aminah")
	})

	t.Run("unknown customer 404", func(t *testing.T) {
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/customers/999999/contacts", sampleCustomerForm())
		wantStatus(t, resp, http.StatusNotFound)
	})
}

func TestRDashRegister(t *testing.T) {
	_, ts := newTestServer(t)

	t.Run("success with expiry now+period, auto-created default contact", func(t *testing.T) {
		data := rdashRegister(t, ts, "webhost.id", 2)
		wantField(t, data, "name", "webhost.id")
		wantField(t, data, "status_label", "Active")
		wantExpiry := time.Now().AddDate(2, 0, 0).UTC().Format(rdashDateFormat)
		wantField(t, data, "expired_at", wantExpiry)
		wantField(t, data, "nameserver_1", "ns1.example.id")
		contact, ok := data["registrant_contact"].(map[string]any)
		if !ok {
			t.Fatalf("registrant_contact missing: %v", data)
		}
		wantField(t, contact, "name", "Budi Santoso")
	})

	t.Run("duplicate 409", func(t *testing.T) {
		customerID := rdashNewCustomer(t, ts)
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains", url.Values{
			"name": {"webhost.id"}, "period": {"1"}, "customer_id": {strconv.FormatInt(customerID, 10)},
		})
		wantStatus(t, resp, http.StatusConflict)
	})

	t.Run("taken name 409", func(t *testing.T) {
		customerID := rdashNewCustomer(t, ts)
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains", url.Values{
			"name": {"supertaken.id"}, "period": {"1"}, "customer_id": {strconv.FormatInt(customerID, 10)},
		})
		wantStatus(t, resp, http.StatusConflict)
	})

	t.Run("missing name rejected", func(t *testing.T) {
		customerID := rdashNewCustomer(t, ts)
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains", url.Values{
			"period": {"1"}, "customer_id": {strconv.FormatInt(customerID, 10)},
		})
		wantStatus(t, resp, http.StatusUnprocessableEntity)
	})

	t.Run("unknown customer_id rejected", func(t *testing.T) {
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains", url.Values{
			"name": {"orphan.id"}, "period": {"1"}, "customer_id": {"9999999"},
		})
		wantStatus(t, resp, http.StatusUnprocessableEntity)
	})

	t.Run("defaults applied for period and nameservers", func(t *testing.T) {
		customerID := rdashNewCustomer(t, ts)
		resp, m := rdashForm(t, ts, http.MethodPost, "/v1/domains", url.Values{
			"name": {"defaults.id"}, "customer_id": {strconv.FormatInt(customerID, 10)},
		})
		wantStatus(t, resp, http.StatusOK)
		data := rdashDataMap(t, m)
		wantExpiry := time.Now().AddDate(1, 0, 0).UTC().Format(rdashDateFormat)
		wantField(t, data, "expired_at", wantExpiry)
		wantField(t, data, "nameserver_1", "ns1.mock-rdash.id")
		wantField(t, data, "nameserver_2", "ns2.mock-rdash.id")
	})
}

func TestRDashTransfer(t *testing.T) {
	_, ts := newTestServer(t)

	t.Run("success", func(t *testing.T) {
		customerID := rdashNewCustomer(t, ts)
		resp, m := rdashForm(t, ts, http.MethodPost, "/v1/domains/transfer", url.Values{
			"name": {"moved.com"}, "auth_code": {"SECRET-EPP"}, "period": {"1"},
			"customer_id": {strconv.FormatInt(customerID, 10)},
		})
		wantStatus(t, resp, http.StatusOK)
		data := rdashDataMap(t, m)
		wantField(t, data, "name", "moved.com")
		wantField(t, data, "status_label", "Active")
	})

	t.Run("wrong auth_code 422", func(t *testing.T) {
		customerID := rdashNewCustomer(t, ts)
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains/transfer", url.Values{
			"name": {"other.com"}, "auth_code": {"WRONG"}, "period": {"1"},
			"customer_id": {strconv.FormatInt(customerID, 10)},
		})
		wantStatus(t, resp, http.StatusUnprocessableEntity)
	})

	t.Run("duplicate 409", func(t *testing.T) {
		customerID := rdashNewCustomer(t, ts)
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains/transfer", url.Values{
			"name": {"moved.com"}, "auth_code": {"SECRET-EPP"}, "period": {"1"},
			"customer_id": {strconv.FormatInt(customerID, 10)},
		})
		wantStatus(t, resp, http.StatusConflict)
	})
}

func TestRDashDetails(t *testing.T) {
	_, ts := newTestServer(t)
	rdashRegister(t, ts, "lookup.id", 1)

	t.Run("found by name", func(t *testing.T) {
		resp, m := rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"lookup.id"}})
		wantStatus(t, resp, http.StatusOK)
		wantField(t, rdashDataMap(t, m), "name", "lookup.id")
	})

	t.Run("unknown domain 404", func(t *testing.T) {
		resp, _ := rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"ghost.id"}})
		wantStatus(t, resp, http.StatusNotFound)
	})
}

func TestRDashRenew(t *testing.T) {
	_, ts := newTestServer(t)
	data := rdashRegister(t, ts, "renewme.id", 1)
	id := domainID(t, data)
	initialExpiry := data["expired_at"].(string)

	t.Run("extends expiry", func(t *testing.T) {
		resp, m := rdashForm(t, ts, http.MethodPost, "/v1/domains/"+strconv.FormatInt(id, 10)+"/renew", url.Values{
			"period": {"2"}, "current_date": {initialExpiry},
		})
		wantStatus(t, resp, http.StatusOK)
		renewed := rdashDataMap(t, m)
		base, _ := time.Parse(rdashDateFormat, initialExpiry)
		wantField(t, renewed, "expired_at", base.AddDate(2, 0, 0).Format(rdashDateFormat))
	})

	t.Run("mismatched current_date rejected", func(t *testing.T) {
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains/"+strconv.FormatInt(id, 10)+"/renew", url.Values{
			"period": {"1"}, "current_date": {"1999-01-01"},
		})
		wantStatus(t, resp, http.StatusUnprocessableEntity)
	})

	t.Run("unknown domain 404", func(t *testing.T) {
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains/999999/renew", url.Values{
			"period": {"1"}, "current_date": {"2026-01-01"},
		})
		wantStatus(t, resp, http.StatusNotFound)
	})
}

func TestRDashNameservers(t *testing.T) {
	_, ts := newTestServer(t)
	data := rdashRegister(t, ts, "nstest.id", 1)
	id := strconv.FormatInt(domainID(t, data), 10)

	t.Run("replaces nameservers", func(t *testing.T) {
		resp, m := rdashForm(t, ts, http.MethodPut, "/v1/domains/"+id+"/ns", url.Values{
			"nameserver[0]": {"dns1.new.id"}, "nameserver[1]": {"dns2.new.id"}, "nameserver[2]": {"dns3.new.id"},
		})
		wantStatus(t, resp, http.StatusOK)
		updated := rdashDataMap(t, m)
		wantField(t, updated, "nameserver_1", "dns1.new.id")
		wantField(t, updated, "nameserver_3", "dns3.new.id")
	})

	t.Run("fewer than two rejected", func(t *testing.T) {
		resp, _ := rdashForm(t, ts, http.MethodPut, "/v1/domains/"+id+"/ns", url.Values{"nameserver[0]": {"only-one.id"}})
		wantStatus(t, resp, http.StatusUnprocessableEntity)
	})

	t.Run("unknown domain 404", func(t *testing.T) {
		resp, _ := rdashForm(t, ts, http.MethodPut, "/v1/domains/999999/ns", url.Values{
			"nameserver[0]": {"a.id"}, "nameserver[1]": {"b.id"},
		})
		wantStatus(t, resp, http.StatusNotFound)
	})
}

func TestRDashContacts(t *testing.T) {
	_, ts := newTestServer(t)
	data := rdashRegister(t, ts, "contact.id", 1)
	id := strconv.FormatInt(domainID(t, data), 10)
	customerID := data["customer"].(map[string]any)["id"].(float64)

	t.Run("get returns auto-created registrant contact", func(t *testing.T) {
		_, m := rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"contact.id"}})
		contact := rdashDataMap(t, m)["registrant_contact"].(map[string]any)
		wantField(t, contact, "name", "Budi Santoso")
	})

	t.Run("update applies a new contact to all four roles", func(t *testing.T) {
		resp, m := rdashForm(t, ts, http.MethodPost,
			"/v1/customers/"+strconv.FormatInt(int64(customerID), 10)+"/contacts", url.Values{
				"label": {"Default"}, "name": {"Siti Aminah"}, "email": {"siti@example.id"},
				"organization": {"PT Budi"}, "street_1": {"Jl. Sudirman 2"}, "city": {"Jakarta"},
				"state": {"DKI Jakarta"}, "country_code": {"ID"}, "postal_code": {"12345"}, "voice": {"0812"},
			})
		wantStatus(t, resp, http.StatusOK)
		newContactID := strconv.FormatInt(int64(rdashDataMap(t, m)["id"].(float64)), 10)

		resp, m = rdashForm(t, ts, http.MethodPut, "/v1/domains/"+id+"/contacts", url.Values{
			"admin_contact_id": {newContactID}, "tech_contact_id": {newContactID},
			"billing_contact_id": {newContactID}, "registrant_contact_id": {newContactID},
		})
		wantStatus(t, resp, http.StatusOK)
		contact := rdashDataMap(t, m)["registrant_contact"].(map[string]any)
		wantField(t, contact, "name", "Siti Aminah")
	})

	t.Run("unknown contact id rejected", func(t *testing.T) {
		resp, _ := rdashForm(t, ts, http.MethodPut, "/v1/domains/"+id+"/contacts", url.Values{
			"admin_contact_id": {"1"}, "tech_contact_id": {"1"},
			"billing_contact_id": {"1"}, "registrant_contact_id": {"9999999"},
		})
		wantStatus(t, resp, http.StatusUnprocessableEntity)
	})
}

func TestRDashAuthCode(t *testing.T) {
	_, ts := newTestServer(t)
	data := rdashRegister(t, ts, "records.id", 1)
	id := strconv.FormatInt(domainID(t, data), 10)

	resp, m := rdashGet(t, ts, "/v1/domains/"+id+"/auth_code", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := m["data"]; got != "MOCK-EPP-records.id" {
		t.Fatalf("auth_code = %v, want MOCK-EPP-records.id", got)
	}

	t.Run("unknown domain 404", func(t *testing.T) {
		resp, _ := rdashGet(t, ts, "/v1/domains/999999/auth_code", nil)
		wantStatus(t, resp, http.StatusNotFound)
	})
}

func TestRDashDNS(t *testing.T) {
	_, ts := newTestServer(t)
	data := rdashRegister(t, ts, "records.id", 1)
	id := strconv.FormatInt(domainID(t, data), 10)

	t.Run("default records exist", func(t *testing.T) {
		_, m := rdashGet(t, ts, "/v1/domains/"+id+"/dns", nil)
		recs := rdashDataArray(t, m)
		if len(recs) == 0 {
			t.Fatal("expected default DNS records")
		}
		first := recs[0].(map[string]any)
		for _, k := range []string{"prefix", "type", "content", "ttl"} {
			if _, ok := first[k]; !ok {
				t.Fatalf("dns record missing key %q: %v", k, first)
			}
		}
	})

	t.Run("post replaces the full record set", func(t *testing.T) {
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains/"+id+"/dns", url.Values{
			"records[0][name]": {"@"}, "records[0][type]": {"A"}, "records[0][content]": {"10.0.0.1"}, "records[0][ttl]": {"600"},
			"records[1][name]": {"@"}, "records[1][type]": {"MX"}, "records[1][content]": {"10 mail.records.id"}, "records[1][ttl]": {"3600"},
		})
		wantStatus(t, resp, http.StatusOK)
		_, m := rdashGet(t, ts, "/v1/domains/"+id+"/dns", nil)
		recs := rdashDataArray(t, m)
		if len(recs) != 2 {
			t.Fatalf("records len = %d, want 2", len(recs))
		}
		mx := recs[1].(map[string]any)
		wantField(t, mx, "type", "MX")
		wantField(t, mx, "content", "10 mail.records.id")
	})

	t.Run("empty record set rejected (registrar requires at least one)", func(t *testing.T) {
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains/"+id+"/dns", url.Values{})
		wantStatus(t, resp, http.StatusUnprocessableEntity)
	})

	t.Run("unknown domain 404", func(t *testing.T) {
		resp, _ := rdashGet(t, ts, "/v1/domains/999999/dns", nil)
		wantStatus(t, resp, http.StatusNotFound)
	})
}
