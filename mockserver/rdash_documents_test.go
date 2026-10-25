package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestRDashTLDRequirements(t *testing.T) {
	for _, n := range []string{"a.co.id", "A.OR.ID", "a.ac.id", "a.sch.id", "a.ponpes.id"} {
		if !rdashRequiresDocument(n) {
			t.Errorf("%s must require documents", n)
		}
	}
	for _, n := range []string{"a.id", "a.my.id", "a.biz.id", "a.web.id", "a.com", "a"} {
		if rdashRequiresDocument(n) {
			t.Errorf("%s must not require documents", n)
		}
	}
	for _, n := range []string{"a.id", "a.my.id", "a.biz.id", "a.web.id"} {
		if !rdashNeedsEmailVerification(n) {
			t.Errorf("%s needs email verification", n)
		}
	}
	for _, n := range []string{"a.co.id", "a.com"} {
		if rdashNeedsEmailVerification(n) {
			t.Errorf("%s needs no email verification", n)
		}
	}
}

// rdashPost issues a JSON POST to a mock control endpoint.
func rdashControl(t *testing.T, ts *httptest.Server, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	return postJSON(t, ts.URL+path, body)
}

func TestRDashRegisterReportsVerificationState(t *testing.T) {
	_, ts := newTestServer(t)

	doc := rdashRegister(t, ts, "toko.co.id", 1)
	wantField(t, doc, "is_required_document", float64(1))
	wantField(t, doc, "verification_status", float64(0))
	wantField(t, doc, "verification_status_label", "Waiting")
	wantField(t, doc, "status_label", "Active") // Auto Activation: active first, documents afterwards
	wantField(t, doc, "is_refundable", true)
	if doc["status_reason"] != nil {
		t.Errorf("status_reason = %v, want null", doc["status_reason"])
	}
	if tok, _ := doc["token"].(string); len(tok) < 16 {
		t.Errorf("token = %q, want a document token", tok)
	}

	email := rdashRegister(t, ts, "toko.my.id", 1)
	wantField(t, email, "is_required_document", float64(0))
	wantField(t, email, "verification_status", float64(0))

	plain := rdashRegister(t, ts, "toko.com", 1)
	wantField(t, plain, "is_required_document", float64(0))
	wantField(t, plain, "verification_status", float64(3))
	wantField(t, plain, "verification_status_label", "Active")

	// details echoes the same fields
	resp, m := rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"toko.co.id"}})
	wantStatus(t, resp, http.StatusOK)
	wantField(t, rdashDataMap(t, m), "is_required_document", float64(1))
}

func TestRDashAutoActivationOffHoldsDocumentDomains(t *testing.T) {
	_, ts := newTestServer(t)
	resp, _ := rdashControl(t, ts, "/mock/rdash/auto-activation", map[string]any{"enabled": false})
	wantStatus(t, resp, http.StatusOK)

	held := rdashRegister(t, ts, "toko.co.id", 1)
	wantField(t, held, "status", float64(0))
	wantField(t, held, "status_label", "Pending")

	// the switch only concerns document domains
	wantField(t, rdashRegister(t, ts, "toko.com", 1), "status_label", "Active")
	wantField(t, rdashRegister(t, ts, "toko.my.id", 1), "status_label", "Active")

	resp, _ = rdashControl(t, ts, "/mock/rdash/auto-activation", map[string]any{})
	wantStatus(t, resp, http.StatusBadRequest)
}

func TestRDashTransferOfDocumentDomainWaitsForDocuments(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)
	resp, m := rdashForm(t, ts, http.MethodPost, "/v1/domains/transfer", url.Values{
		"name": {"pindah.co.id"}, "auth_code": {"EPP"}, "period": {"1"},
		"customer_id": {strconv.FormatInt(customerID, 10)},
	})
	wantStatus(t, resp, http.StatusOK)
	data := rdashDataMap(t, m)
	wantField(t, data, "status_label", "Pending Transfer")
	wantField(t, data, "is_required_document", float64(1))
}

func TestRDashDocumentLinkAndUploadPage(t *testing.T) {
	_, ts := newTestServer(t)
	doc := rdashRegister(t, ts, "toko.co.id", 1)
	id := domainID(t, doc)

	// the link points at the hosted page on the mock
	resp, m := rdashForm(t, ts, http.MethodPost, "/v1/domains/"+strconv.FormatInt(id, 10)+"/documents/link", nil)
	wantStatus(t, resp, http.StatusOK)
	link, _ := m["data"].(string)
	if !strings.HasPrefix(link, ts.URL+"/documents/upload/") {
		t.Fatalf("link = %q", link)
	}

	// GET renders the page for the domain
	page, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(body), "toko.co.id") || !strings.Contains(string(body), "Waiting") {
		t.Fatalf("upload page: %d %s", page.StatusCode, body)
	}

	// submitting moves Waiting -> Document Validating
	post, err := http.Post(link, "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(post.Body)
	post.Body.Close()
	if post.StatusCode != http.StatusOK || !strings.Contains(string(body), "Documents received") {
		t.Fatalf("upload submit: %d %s", post.StatusCode, body)
	}
	resp, m = rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"toko.co.id"}})
	wantStatus(t, resp, http.StatusOK)
	data := rdashDataMap(t, m)
	wantField(t, data, "verification_status", float64(2))
	wantField(t, data, "verification_status_label", "Document Validating")

	// bookkeeping for assertions
	resp, m = jsonReq(t, http.MethodGet, ts.URL+"/mock/rdash/state?domain=toko.co.id", nil, "", "")
	wantStatus(t, resp, http.StatusOK)
	wantField(t, m, "uploads", float64(1))
	wantField(t, m, "link_requests", float64(1))

	// a reviewer approves: a further submit is refused
	rdashControl(t, ts, "/mock/rdash/domain-state", map[string]any{"domain": "toko.co.id", "verification_status": 3})
	again, err := http.Post(link, "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	again.Body.Close()
	if again.StatusCode != http.StatusGone {
		t.Errorf("submit after verification = %d, want 410", again.StatusCode)
	}
}

func TestRDashDocumentLinkRefusals(t *testing.T) {
	_, ts := newTestServer(t)
	plain := rdashRegister(t, ts, "toko.com", 1)
	path := "/v1/domains/" + strconv.FormatInt(domainID(t, plain), 10) + "/documents/link"

	resp, _ := rdashForm(t, ts, http.MethodPost, path, nil)
	wantStatus(t, resp, http.StatusUnprocessableEntity)

	resp, _ = formReq(t, http.MethodPost, ts.URL+path, nil, "", "")
	wantStatus(t, resp, http.StatusUnauthorized)

	resp, _ = rdashForm(t, ts, http.MethodPost, "/v1/domains/99999/documents/link", nil)
	wantStatus(t, resp, http.StatusNotFound)

	// unknown / foreign tokens
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req, _ := http.NewRequest(method, ts.URL+"/documents/upload/not-a-token", nil)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusGone {
			t.Errorf("%s unknown token = %d, want 410", method, r.StatusCode)
		}
	}
	// a gTLD has a token but needs no documents
	tok, _ := plain["token"].(string)
	r, err := http.Get(ts.URL + "/documents/upload/" + tok)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusGone {
		t.Errorf("page for a domain without documents = %d, want 410", r.StatusCode)
	}
}

func TestRDashVerificationResend(t *testing.T) {
	_, ts := newTestServer(t)
	dom := rdashRegister(t, ts, "toko.my.id", 1)
	path := "/v1/domains/" + strconv.FormatInt(domainID(t, dom), 10) + "/verification/resend"

	resp, m := rdashForm(t, ts, http.MethodPost, path, nil)
	wantStatus(t, resp, http.StatusOK)
	wantField(t, m, "message", "Email verification sent to your email address!")

	resp, m = jsonReq(t, http.MethodGet, ts.URL+"/mock/rdash/state?domain=toko.my.id", nil, "", "")
	wantStatus(t, resp, http.StatusOK)
	wantField(t, m, "resend_count", float64(1))

	// once verified there is nothing to resend
	rdashControl(t, ts, "/mock/rdash/domain-state", map[string]any{"domain": "toko.my.id", "verification_status": 3})
	resp, _ = rdashForm(t, ts, http.MethodPost, path, nil)
	wantStatus(t, resp, http.StatusUnprocessableEntity)

	resp, _ = formReq(t, http.MethodPost, ts.URL+path, nil, "", "")
	wantStatus(t, resp, http.StatusUnauthorized)
	resp, _ = rdashForm(t, ts, http.MethodPost, "/v1/domains/99999/verification/resend", nil)
	wantStatus(t, resp, http.StatusNotFound)
}

func TestRDashDomainStateControl(t *testing.T) {
	_, ts := newTestServer(t)
	rdashRegister(t, ts, "toko.co.id", 1)

	// bounce a review: back to waiting with a reason
	resp, _ := rdashControl(t, ts, "/mock/rdash/domain-state", map[string]any{
		"domain": "toko.co.id", "verification_status": 2,
	})
	wantStatus(t, resp, http.StatusOK)
	resp, m := rdashControl(t, ts, "/mock/rdash/domain-state", map[string]any{
		"domain": "TOKO.co.id", "verification_status": 0, "status_reason": "KTP tidak terbaca",
	})
	wantStatus(t, resp, http.StatusOK)
	dom, _ := m["domain"].(map[string]any)
	wantField(t, dom, "verification_status", float64(0))
	wantField(t, dom, "status_reason", "KTP tidak terbaca")

	// suspend, then reject as non-refundable
	_, m = rdashControl(t, ts, "/mock/rdash/domain-state", map[string]any{"domain": "toko.co.id", "status": 7})
	wantField(t, m["domain"].(map[string]any), "status_label", "Suspended")
	_, m = rdashControl(t, ts, "/mock/rdash/domain-state", map[string]any{
		"domain": "toko.co.id", "status": 8, "is_refundable": false, "required_document": true,
	})
	d := m["domain"].(map[string]any)
	wantField(t, d, "status_label", "Rejected")
	wantField(t, d, "is_refundable", false)

	// the adapter-facing details reflect it
	resp, m = rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"toko.co.id"}})
	wantStatus(t, resp, http.StatusOK)
	wantField(t, rdashDataMap(t, m), "status", float64(8))

	for name, body := range map[string]any{
		"missing domain":   map[string]any{"status": 1},
		"bad status":       map[string]any{"domain": "toko.co.id", "status": 42},
		"bad verification": map[string]any{"domain": "toko.co.id", "verification_status": 9},
	} {
		resp, _ := rdashControl(t, ts, "/mock/rdash/domain-state", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, resp.StatusCode)
		}
	}
	resp, _ = rdashControl(t, ts, "/mock/rdash/domain-state", map[string]any{"domain": "ghost.co.id", "status": 1})
	wantStatus(t, resp, http.StatusNotFound)

	resp, _ = jsonReq(t, http.MethodGet, ts.URL+"/mock/rdash/state?domain=ghost.co.id", nil, "", "")
	wantStatus(t, resp, http.StatusNotFound)
}

func TestMockResetClearsDocumentState(t *testing.T) {
	_, ts := newTestServer(t)
	rdashControl(t, ts, "/mock/rdash/auto-activation", map[string]any{"enabled": false})
	rdashRegister(t, ts, "toko.co.id", 1)

	resp, _ := postJSON(t, ts.URL+"/mock/reset", map[string]any{})
	wantStatus(t, resp, http.StatusOK)

	// Auto Activation is back on and the old domain (and its token) is gone
	wantField(t, rdashRegister(t, ts, "toko.co.id", 1), "status_label", "Active")
}
