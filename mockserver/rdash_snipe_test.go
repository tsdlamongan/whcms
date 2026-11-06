package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- helpers (shared by the scripted-drop / fault / balance tests) ----

// snipeRegister POSTs /v1/domains for name and returns the raw response.
func snipeRegister(t *testing.T, ts *httptest.Server, customerID int64, name string, years int) (*http.Response, map[string]any) {
	t.Helper()
	return rdashForm(t, ts, http.MethodPost, "/v1/domains", url.Values{
		"name": {name}, "period": {strconv.Itoa(years)},
		"customer_id": {strconv.FormatInt(customerID, 10)},
	})
}

// snipeScript installs a drop script and asserts the control endpoint said ok.
func snipeScript(t *testing.T, ts *httptest.Server, req map[string]any) {
	t.Helper()
	resp, m := postJSON(t, ts.URL+"/mock/rdash/script", req)
	wantStatus(t, resp, http.StatusOK)
	wantField(t, m, "ok", true)
}

// snipeFault arms a fault and asserts the control endpoint said ok.
func snipeFault(t *testing.T, ts *httptest.Server, req map[string]any) {
	t.Helper()
	resp, m := postJSON(t, ts.URL+"/mock/rdash/fault", req)
	wantStatus(t, resp, http.StatusOK)
	wantField(t, m, "ok", true)
}

func snipeAttempts(t *testing.T, ts *httptest.Server, domain string) int {
	t.Helper()
	resp, m := jsonReq(t, http.MethodGet, ts.URL+"/mock/rdash/attempts?domain="+url.QueryEscape(domain), nil, "", "")
	wantStatus(t, resp, http.StatusOK)
	n, ok := m["attempts"].(float64)
	if !ok {
		t.Fatalf("attempts missing in %v", m)
	}
	return int(n)
}

// snipeAvailable returns the availability flag (0/1) the mock reports for name.
func snipeAvailable(t *testing.T, ts *httptest.Server, name string) int {
	t.Helper()
	resp, m := rdashGet(t, ts, "/v1/domains/availability", url.Values{"domain": {name}})
	wantStatus(t, resp, http.StatusOK)
	return int(rdashDataMap(t, m)["available"].(float64))
}

func snipeBalance(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp, m := rdashGet(t, ts, "/v1/account/balance", nil)
	wantStatus(t, resp, http.StatusOK)
	data := rdashDataMap(t, m)
	wantField(t, data, "currency", "IDR")
	return data["balance"].(string)
}

func snipeSetBalance(t *testing.T, ts *httptest.Server, amount int) {
	t.Helper()
	resp, m := postJSON(t, ts.URL+"/mock/rdash/balance", map[string]any{"amount": amount})
	wantStatus(t, resp, http.StatusOK)
	wantField(t, m, "ok", true)
	wantField(t, m, "balance", amount)
}

// wantRDashError asserts the standard {success:false, data:null, message}
// error envelope (no `errors` map - the shape the 409 "domain not available"
// has always had).
func wantRDashError(t *testing.T, resp *http.Response, m map[string]any, status int, message string) {
	t.Helper()
	wantStatus(t, resp, status)
	wantField(t, m, "success", false)
	wantField(t, m, "message", message)
	if m["data"] != nil {
		t.Fatalf("error envelope data = %v, want null", m["data"])
	}
	if _, has := m["errors"]; has {
		t.Fatalf("error envelope unexpectedly has errors: %v", m)
	}
}

// recordSleeps swaps the server's sleep for a recorder so delay_ms is
// observable without waiting. Returns an accessor for the recorded delays.
func recordSleeps(s *Server) func() []time.Duration {
	var mu sync.Mutex
	var got []time.Duration
	s.mu.Lock()
	s.sleep = func(d time.Duration) {
		mu.Lock()
		got = append(got, d)
		mu.Unlock()
	}
	s.mu.Unlock()
	return func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), got...)
	}
}

func rawPost(t *testing.T, url, body string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp, decodeBody(t, resp)
}

// ---- scripted drop ----

func TestRDashScriptedDrop(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)
	snipeScript(t, ts, map[string]any{"domain": "Drop-Me.com", "fail_times": 3})

	// While the script holds the name it is unavailable and absent.
	if got := snipeAvailable(t, ts, "drop-me.com"); got != 0 {
		t.Fatalf("available before any attempt = %d, want 0", got)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		resp, m := snipeRegister(t, ts, customerID, "drop-me.com", 1)
		wantRDashError(t, resp, m, http.StatusConflict, "Domain not available")
		if got := snipeAttempts(t, ts, "drop-me.com"); got != attempt {
			t.Fatalf("attempts after failure %d = %d", attempt, got)
		}
		if attempt < 3 {
			if got := snipeAvailable(t, ts, "drop-me.com"); got != 0 {
				t.Fatalf("available after %d failures = %d, want 0", attempt, got)
			}
		}
		respD, _ := rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"drop-me.com"}})
		wantStatus(t, respD, http.StatusNotFound)
	}

	// Script exhausted: the name now reads as available, nothing registered yet.
	if got := snipeAvailable(t, ts, "drop-me.com"); got != 1 {
		t.Fatalf("available after the scripted failures = %d, want 1", got)
	}
	respD, _ := rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"drop-me.com"}})
	wantStatus(t, respD, http.StatusNotFound)

	// The next attempt registers exactly like an unscripted domain.
	resp, m := snipeRegister(t, ts, customerID, "DROP-ME.com", 2)
	wantStatus(t, resp, http.StatusOK)
	data := rdashDataMap(t, m)
	wantField(t, data, "name", "drop-me.com")
	wantField(t, data, "status", 1)
	wantField(t, data, "status_label", "Active")
	wantField(t, data, "expired_at", time.Now().AddDate(2, 0, 0).UTC().Format(rdashDateFormat))

	respD, details := rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"drop-me.com"}})
	wantStatus(t, respD, http.StatusOK)
	wantField(t, rdashDataMap(t, details), "name", "drop-me.com")
	if got := snipeAvailable(t, ts, "drop-me.com"); got != 0 {
		t.Fatalf("available once registered = %d, want 0", got)
	}
	if got := snipeAttempts(t, ts, "drop-me.com"); got != 4 {
		t.Fatalf("attempts = %d, want 4 (3 failures + the winner)", got)
	}

	// Registered now: further attempts hit the regular duplicate 409.
	resp, m = snipeRegister(t, ts, customerID, "drop-me.com", 1)
	wantRDashError(t, resp, m, http.StatusConflict, "domain not available")
	if got := snipeAttempts(t, ts, "drop-me.com"); got != 5 {
		t.Fatalf("attempts = %d, want 5", got)
	}
}

func TestRDashScriptFailureResponse(t *testing.T) {
	cases := []struct {
		name        string
		script      map[string]any
		wantStatus  int
		wantMessage string
	}{
		{"defaults are 409 + Domain not available", map[string]any{"domain": "a.com", "fail_times": 1}, 409, "Domain not available"},
		{"explicit defaults via zero/blank values", map[string]any{"domain": "a.com", "fail_times": 1, "fail_status": 0, "fail_message": "  "}, 409, "Domain not available"},
		{"custom status and message", map[string]any{"domain": "a.com", "fail_times": 1, "fail_status": 503, "fail_message": "registry busy"}, 503, "registry busy"},
		{"custom message only", map[string]any{"domain": "a.com", "fail_times": 1, "fail_message": "pending delete"}, 409, "pending delete"},
		{"rate limited", map[string]any{"domain": "a.com", "fail_times": 1, "fail_status": 429, "fail_message": "slow down"}, 429, "slow down"},
		{"validation-style status", map[string]any{"domain": "a.com", "fail_times": 1, "fail_status": 422, "fail_message": "nope"}, 422, "nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ts := newTestServer(t)
			customerID := rdashNewCustomer(t, ts)
			snipeScript(t, ts, tc.script)

			resp, m := snipeRegister(t, ts, customerID, "a.com", 1)
			wantRDashError(t, resp, m, tc.wantStatus, tc.wantMessage)

			resp, m = snipeRegister(t, ts, customerID, "a.com", 1)
			wantStatus(t, resp, http.StatusOK)
			wantField(t, rdashDataMap(t, m), "status_label", "Active")
		})
	}
}

func TestRDashScriptEdgeCases(t *testing.T) {
	t.Run("fail_times 0 is available and registers on the first attempt", func(t *testing.T) {
		_, ts := newTestServer(t)
		customerID := rdashNewCustomer(t, ts)
		snipeScript(t, ts, map[string]any{"domain": "instant.com", "fail_times": 0})
		if got := snipeAvailable(t, ts, "instant.com"); got != 1 {
			t.Fatalf("available = %d, want 1", got)
		}
		resp, _ := snipeRegister(t, ts, customerID, "instant.com", 1)
		wantStatus(t, resp, http.StatusOK)
		if got := snipeAttempts(t, ts, "instant.com"); got != 1 {
			t.Fatalf("attempts = %d, want 1", got)
		}
	})

	t.Run("script overrides the taken substring rule", func(t *testing.T) {
		_, ts := newTestServer(t)
		customerID := rdashNewCustomer(t, ts)
		if got := snipeAvailable(t, ts, "taken-drop.com"); got != 0 {
			t.Fatalf("unscripted taken name available = %d, want 0", got)
		}
		snipeScript(t, ts, map[string]any{"domain": "taken-drop.com", "fail_times": 1, "fail_message": "held"})
		if got := snipeAvailable(t, ts, "taken-drop.com"); got != 0 {
			t.Fatalf("held name available = %d, want 0", got)
		}
		resp, m := snipeRegister(t, ts, customerID, "taken-drop.com", 1)
		wantRDashError(t, resp, m, http.StatusConflict, "held")
		if got := snipeAvailable(t, ts, "taken-drop.com"); got != 1 {
			t.Fatalf("dropped name available = %d, want 1", got)
		}
		resp, _ = snipeRegister(t, ts, customerID, "taken-drop.com", 1)
		wantStatus(t, resp, http.StatusOK)
	})

	t.Run("re-scripting a name restarts its sequence", func(t *testing.T) {
		_, ts := newTestServer(t)
		customerID := rdashNewCustomer(t, ts)
		snipeScript(t, ts, map[string]any{"domain": "again.com", "fail_times": 1})
		resp, _ := snipeRegister(t, ts, customerID, "again.com", 1)
		wantStatus(t, resp, http.StatusConflict)

		snipeScript(t, ts, map[string]any{"domain": "again.com", "fail_times": 2})
		if got := snipeAttempts(t, ts, "again.com"); got != 0 {
			t.Fatalf("attempts after re-script = %d, want 0", got)
		}
		for i := 0; i < 2; i++ {
			resp, _ = snipeRegister(t, ts, customerID, "again.com", 1)
			wantStatus(t, resp, http.StatusConflict)
		}
		resp, _ = snipeRegister(t, ts, customerID, "again.com", 1)
		wantStatus(t, resp, http.StatusOK)
	})

	t.Run("scripts are per name", func(t *testing.T) {
		_, ts := newTestServer(t)
		customerID := rdashNewCustomer(t, ts)
		snipeScript(t, ts, map[string]any{"domain": "held.com", "fail_times": 5})
		resp, _ := snipeRegister(t, ts, customerID, "other.com", 1)
		wantStatus(t, resp, http.StatusOK)
		if got := snipeAttempts(t, ts, "held.com"); got != 0 {
			t.Fatalf("held.com attempts = %d, want 0", got)
		}
	})
}

func TestRDashScriptValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"invalid json", `{not json`},
		{"missing domain", `{"fail_times":2}`},
		{"blank domain", `{"domain":"  ","fail_times":2}`},
		{"negative fail_times", `{"domain":"x.com","fail_times":-1}`},
		{"non-error fail_status", `{"domain":"x.com","fail_times":1,"fail_status":200}`},
		{"out of range fail_status", `{"domain":"x.com","fail_times":1,"fail_status":700}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ts := newTestServer(t)
			resp, m := rawPost(t, ts.URL+"/mock/rdash/script", tc.body)
			wantStatus(t, resp, http.StatusBadRequest)
			if msg, _ := m["error"].(string); msg == "" {
				t.Fatalf("missing error message: %v", m)
			}
		})
	}
}

// ---- attempts ----

func TestRDashAttempts(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)

	t.Run("never seen is zero", func(t *testing.T) {
		resp, m := jsonReq(t, http.MethodGet, ts.URL+"/mock/rdash/attempts?domain=Ghost.com", nil, "", "")
		wantStatus(t, resp, http.StatusOK)
		wantField(t, m, "domain", "ghost.com")
		wantField(t, m, "attempts", 0)
	})

	t.Run("counts every attempt, failed or not, case-insensitively", func(t *testing.T) {
		for _, name := range []string{"counted.com", "COUNTED.com", "counted.com"} { // ok, dup 409, dup 409
			snipeRegister(t, ts, customerID, name, 1)
		}
		snipeRegister(t, ts, customerID, "alreadytaken.com", 1) // unscripted 409
		if got := snipeAttempts(t, ts, "Counted.COM"); got != 3 {
			t.Fatalf("counted.com attempts = %d, want 3", got)
		}
		if got := snipeAttempts(t, ts, "alreadytaken.com"); got != 1 {
			t.Fatalf("alreadytaken.com attempts = %d, want 1", got)
		}
	})

	t.Run("missing domain rejected", func(t *testing.T) {
		resp, m := jsonReq(t, http.MethodGet, ts.URL+"/mock/rdash/attempts", nil, "", "")
		wantStatus(t, resp, http.StatusBadRequest)
		if msg, _ := m["error"].(string); msg == "" {
			t.Fatalf("missing error message: %v", m)
		}
	})

}

// ---- fault injection ----

func TestRDashFaultConsumption(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)
	snipeFault(t, ts, map[string]any{
		"path": "/v1/domains", "status": 429, "count": 3, "retry_after": 2, "message": "Too Many Requests",
	})

	for i := 1; i <= 3; i++ {
		resp, m := snipeRegister(t, ts, customerID, "faulty.com", 1)
		wantRDashError(t, resp, m, http.StatusTooManyRequests, "Too Many Requests")
		if got := resp.Header.Get("Retry-After"); got != "2" {
			t.Fatalf("request %d Retry-After = %q, want 2", i, got)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("Content-Type = %q, want JSON", got)
		}
		// A faulted attempt counts but registers nothing.
		if got := snipeAttempts(t, ts, "faulty.com"); got != i {
			t.Fatalf("attempts after fault %d = %d", i, got)
		}
		respD, _ := rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"faulty.com"}})
		wantStatus(t, respD, http.StatusNotFound)
	}

	// Fourth request: the fault is spent, normal handling resumes.
	resp, m := snipeRegister(t, ts, customerID, "faulty.com", 1)
	wantStatus(t, resp, http.StatusOK)
	wantField(t, rdashDataMap(t, m), "name", "faulty.com")
	if got := resp.Header.Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After on a normal response = %q", got)
	}
	if got := snipeAttempts(t, ts, "faulty.com"); got != 4 {
		t.Fatalf("attempts = %d, want 4", got)
	}
}

func TestRDashFaultShapes(t *testing.T) {
	cases := []struct {
		name           string
		fault          map[string]any
		wantStatus     int
		wantMessage    string
		wantRetryAfter string
	}{
		{"500 without retry-after", map[string]any{"path": "/v1/domains/availability", "status": 500, "message": "boom"}, 500, "boom", ""},
		{"502", map[string]any{"path": "/v1/domains/availability", "status": 502, "message": "bad gateway", "count": 1}, 502, "bad gateway", ""},
		{"503 with retry-after", map[string]any{"path": "/v1/domains/availability", "status": 503, "retry_after": 7, "message": "maintenance"}, 503, "maintenance", "7"},
		{"504", map[string]any{"path": "/v1/domains/availability", "status": 504, "message": "gateway timeout"}, 504, "gateway timeout", ""},
		{"default message is the status text", map[string]any{"path": "/v1/domains/availability", "status": 429}, 429, "Too Many Requests", ""},
		{"omitted count means one request", map[string]any{"path": "/v1/domains/availability", "status": 500, "message": "once"}, 500, "once", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ts := newTestServer(t)
			snipeFault(t, ts, tc.fault)

			resp, m := rdashGet(t, ts, "/v1/domains/availability", url.Values{"domain": {"fresh.com"}})
			wantRDashError(t, resp, m, tc.wantStatus, tc.wantMessage)
			if got := resp.Header.Get("Retry-After"); got != tc.wantRetryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, tc.wantRetryAfter)
			}

			// The fault matches the exact path only and spends itself per request.
			if tc.fault["count"] == nil || tc.fault["count"] == 1 {
				if got := snipeAvailable(t, ts, "fresh.com"); got != 1 {
					t.Fatalf("available after the single fault = %d, want 1", got)
				}
			}
		})
	}
}

func TestRDashFaultPathMatching(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)
	snipeFault(t, ts, map[string]any{"path": "/v1/domains", "status": 503, "count": 1})

	t.Run("other paths are untouched, even with the same prefix", func(t *testing.T) {
		if got := snipeAvailable(t, ts, "fresh.com"); got != 1 {
			t.Fatalf("availability = %d, want 1", got)
		}
		resp, _ := rdashGet(t, ts, "/v1/account/balance", nil)
		wantStatus(t, resp, http.StatusOK)
		resp, _ = rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"ghost.com"}})
		wantStatus(t, resp, http.StatusNotFound) // the regular not-found, not the fault
	})

	t.Run("fault outranks the handler's own validation", func(t *testing.T) {
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains", url.Values{"period": {"1"}})
		wantStatus(t, resp, http.StatusServiceUnavailable) // would be 422 (missing name)
		resp, _ = rdashForm(t, ts, http.MethodPost, "/v1/domains", url.Values{"period": {"1"}})
		wantStatus(t, resp, http.StatusUnprocessableEntity) // spent
	})

	t.Run("any method on the path is faulted", func(t *testing.T) {
		snipeFault(t, ts, map[string]any{"path": "/v1/domains", "status": 500, "count": 1})
		resp, _ := rdashGet(t, ts, "/v1/domains", nil)
		wantStatus(t, resp, http.StatusInternalServerError)
	})

	t.Run("faults on one path queue in order", func(t *testing.T) {
		snipeFault(t, ts, map[string]any{"path": "/v1/domains", "status": 500, "message": "first"})
		snipeFault(t, ts, map[string]any{"path": "/v1/domains", "status": 502, "count": 2, "message": "second"})
		for _, want := range []int{500, 502, 502} {
			resp, _ := snipeRegister(t, ts, customerID, "queued.com", 1)
			wantStatus(t, resp, want)
		}
		resp, _ := snipeRegister(t, ts, customerID, "queued.com", 1)
		wantStatus(t, resp, http.StatusOK)
	})

	t.Run("a fault on one path leaves another path's fault alone", func(t *testing.T) {
		snipeFault(t, ts, map[string]any{"path": "/v1/domains/availability", "status": 504, "count": 1})
		snipeFault(t, ts, map[string]any{"path": "/v1/account/profile", "status": 500, "count": 1})
		resp, _ := rdashGet(t, ts, "/v1/account/profile", nil)
		wantStatus(t, resp, http.StatusInternalServerError)
		resp, _ = rdashGet(t, ts, "/v1/domains/availability", url.Values{"domain": {"x.com"}})
		wantStatus(t, resp, http.StatusGatewayTimeout)
	})
}

func TestRDashFaultCountsTowardScript(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)
	snipeScript(t, ts, map[string]any{"domain": "race.com", "fail_times": 2})
	snipeFault(t, ts, map[string]any{"path": "/v1/domains", "status": 429, "count": 1})

	// attempt 1: 429 fault; attempt 2: scripted 409; attempt 3: wins.
	for _, want := range []int{429, 409, 200} {
		resp, _ := snipeRegister(t, ts, customerID, "race.com", 1)
		wantStatus(t, resp, want)
	}
	if got := snipeAttempts(t, ts, "race.com"); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestRDashFaultDelay(t *testing.T) {
	cases := []struct {
		name       string
		fault      map[string]any
		wantStatus int
		wantSleep  time.Duration
	}{
		{"delay only continues normal handling", map[string]any{"path": "/v1/domains/availability", "delay_ms": 250}, 200, 250 * time.Millisecond},
		{"delay then error", map[string]any{"path": "/v1/domains/availability", "status": 503, "delay_ms": 40, "message": "slow"}, 503, 40 * time.Millisecond},
		{"delay is capped at 10s", map[string]any{"path": "/v1/domains/availability", "delay_ms": 60000}, 200, 10 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ts := newTestServer(t)
			slept := recordSleeps(s)
			snipeFault(t, ts, tc.fault)

			resp, m := rdashGet(t, ts, "/v1/domains/availability", url.Values{"domain": {"fresh.com"}})
			wantStatus(t, resp, tc.wantStatus)
			if tc.wantStatus == 200 {
				wantField(t, rdashDataMap(t, m), "available", 1)
			}
			got := slept()
			if len(got) != 1 || got[0] != tc.wantSleep {
				t.Fatalf("sleeps = %v, want [%v]", got, tc.wantSleep)
			}

			// Spent after one request: no further delay.
			rdashGet(t, ts, "/v1/domains/availability", url.Values{"domain": {"fresh.com"}})
			if got := slept(); len(got) != 1 {
				t.Fatalf("sleeps after second request = %v, want still 1", got)
			}
		})
	}

	t.Run("delay-only registration is still counted by the handler once", func(t *testing.T) {
		s, ts := newTestServer(t)
		slept := recordSleeps(s)
		customerID := rdashNewCustomer(t, ts)
		snipeFault(t, ts, map[string]any{"path": "/v1/domains", "delay_ms": 100})
		resp, _ := snipeRegister(t, ts, customerID, "slowpoke.com", 1)
		wantStatus(t, resp, http.StatusOK)
		if got := snipeAttempts(t, ts, "slowpoke.com"); got != 1 {
			t.Fatalf("attempts = %d, want 1", got)
		}
		if got := slept(); len(got) != 1 || got[0] != 100*time.Millisecond {
			t.Fatalf("sleeps = %v", got)
		}
	})
}

func TestRDashFaultAuth(t *testing.T) {
	_, ts := newTestServer(t)
	snipeFault(t, ts, map[string]any{"path": "/v1/domains/availability", "status": 503, "count": 1})

	t.Run("bad credentials get 401 and leave the fault armed", func(t *testing.T) {
		resp, m := jsonReq(t, http.MethodGet, ts.URL+"/v1/domains/availability?domain=x.com", nil, "bad", "bad")
		wantStatus(t, resp, http.StatusUnauthorized)
		wantField(t, m, "message", "unauthorized")
		resp, _ = jsonReq(t, http.MethodGet, ts.URL+"/v1/domains/availability?domain=x.com", nil, "", "")
		wantStatus(t, resp, http.StatusUnauthorized)

		resp, _ = rdashGet(t, ts, "/v1/domains/availability", url.Values{"domain": {"x.com"}})
		wantStatus(t, resp, http.StatusServiceUnavailable)
	})

	t.Run("new /v1 paths still require auth", func(t *testing.T) {
		resp, _ := formReq(t, http.MethodPost, ts.URL+"/v1/domains", url.Values{"name": {"auth.com"}}, "bad", "bad")
		wantStatus(t, resp, http.StatusUnauthorized)
		if got := snipeAttempts(t, ts, "auth.com"); got != 0 {
			t.Fatalf("unauthenticated attempt was counted: %d", got)
		}
		resp, _ = jsonReq(t, http.MethodGet, ts.URL+"/v1/account/balance", nil, "bad", "bad")
		wantStatus(t, resp, http.StatusUnauthorized)
	})
}

func TestRDashFaultValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"invalid json", `{nope`},
		{"missing path", `{"status":429}`},
		{"blank path", `{"path":" ","status":429}`},
		{"path outside /v1", `{"path":"/webapi/x","status":429}`},
		{"path without leading slash", `{"path":"v1/domains","status":429}`},
		{"negative count", `{"path":"/v1/domains","status":429,"count":-1}`},
		{"negative retry_after", `{"path":"/v1/domains","status":429,"retry_after":-1}`},
		{"negative delay_ms", `{"path":"/v1/domains","status":429,"delay_ms":-5}`},
		{"success status", `{"path":"/v1/domains","status":200}`},
		{"redirect status", `{"path":"/v1/domains","status":302,"delay_ms":5}`},
		{"status above 599", `{"path":"/v1/domains","status":600}`},
		{"no status and no delay", `{"path":"/v1/domains"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ts := newTestServer(t)
			resp, m := rawPost(t, ts.URL+"/mock/rdash/fault", tc.body)
			wantStatus(t, resp, http.StatusBadRequest)
			if msg, _ := m["error"].(string); msg == "" {
				t.Fatalf("missing error message: %v", m)
			}
			// A rejected fault must not arm anything.
			if got := snipeAvailable(t, ts, "fresh.com"); got != 1 {
				t.Fatalf("availability = %d, want 1 (no fault armed)", got)
			}
		})
	}
}

// ---- balance ----

func TestRDashBalanceDebit(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)

	if got := snipeBalance(t, ts); got != "10000000.00" {
		t.Fatalf("default balance = %q, want 10000000.00", got)
	}

	cases := []struct {
		name        string
		years       int
		wantBalance string
	}{
		{"one year costs 10000", 1, "9990000.00"},
		{"three years cost 30000", 3, "9960000.00"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := snipeRegister(t, ts, customerID, "debit"+strconv.Itoa(i)+".com", tc.years)
			wantStatus(t, resp, http.StatusOK)
			if got := snipeBalance(t, ts); got != tc.wantBalance {
				t.Fatalf("balance = %q, want %q", got, tc.wantBalance)
			}
		})
	}

	t.Run("a refused registration does not debit", func(t *testing.T) {
		resp, _ := snipeRegister(t, ts, customerID, "debit0.com", 1) // duplicate
		wantStatus(t, resp, http.StatusConflict)
		resp, _ = snipeRegister(t, ts, customerID, "stilltaken.com", 1) // `taken` substring
		wantStatus(t, resp, http.StatusConflict)
		if got := snipeBalance(t, ts); got != "9960000.00" {
			t.Fatalf("balance = %q, want unchanged 9960000.00", got)
		}
	})

	t.Run("renew and transfer leave the balance alone", func(t *testing.T) {
		data := rdashRegister(t, ts, "renewable.com", 1) // helper registers (and debits) 10000
		before := snipeBalance(t, ts)
		id := strconv.FormatInt(domainID(t, data), 10)
		resp, _ := rdashForm(t, ts, http.MethodPost, "/v1/domains/"+id+"/renew", url.Values{
			"period": {"2"}, "current_date": {data["expired_at"].(string)},
		})
		wantStatus(t, resp, http.StatusOK)
		resp, _ = rdashForm(t, ts, http.MethodPost, "/v1/domains/transfer", url.Values{
			"name": {"moved.com"}, "auth_code": {"EPP"}, "period": {"2"},
			"customer_id": {strconv.FormatInt(customerID, 10)},
		})
		wantStatus(t, resp, http.StatusOK)
		if got := snipeBalance(t, ts); got != before {
			t.Fatalf("balance = %q, want %q", got, before)
		}
	})
}

func TestRDashBalanceInsufficient(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)
	snipeSetBalance(t, ts, 15000)
	if got := snipeBalance(t, ts); got != "15000.00" {
		t.Fatalf("balance = %q, want 15000.00", got)
	}

	// 2 years = 20000 > 15000.
	resp, m := snipeRegister(t, ts, customerID, "poor.com", 2)
	wantRDashError(t, resp, m, http.StatusUnprocessableEntity, "Insufficient balance")
	respD, _ := rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"poor.com"}})
	wantStatus(t, respD, http.StatusNotFound)
	if got := snipeBalance(t, ts); got != "15000.00" {
		t.Fatalf("balance after refusal = %q, want unchanged", got)
	}
	if got := snipeAttempts(t, ts, "poor.com"); got != 1 {
		t.Fatalf("refused attempt not counted: attempts = %d", got)
	}
	if got := snipeAvailable(t, ts, "poor.com"); got != 1 {
		t.Fatalf("poor.com should still be available, got %d", got)
	}

	// 1 year fits.
	resp, _ = snipeRegister(t, ts, customerID, "poor.com", 1)
	wantStatus(t, resp, http.StatusOK)
	if got := snipeBalance(t, ts); got != "5000.00" {
		t.Fatalf("balance = %q, want 5000.00", got)
	}

	// 5000 < 10000: refused again; topping up exactly to the price allows it.
	resp, m = snipeRegister(t, ts, customerID, "poor2.com", 1)
	wantRDashError(t, resp, m, http.StatusUnprocessableEntity, "Insufficient balance")
	snipeSetBalance(t, ts, 10000)
	resp, _ = snipeRegister(t, ts, customerID, "poor2.com", 1)
	wantStatus(t, resp, http.StatusOK)
	if got := snipeBalance(t, ts); got != "0.00" {
		t.Fatalf("balance = %q, want 0.00", got)
	}

	t.Run("a huge period is refused, not overflowed", func(t *testing.T) {
		snipeSetBalance(t, ts, 10000000)
		resp, m := rdashForm(t, ts, http.MethodPost, "/v1/domains", url.Values{
			"name": {"forever.com"}, "period": {"9223372036854775807"},
			"customer_id": {strconv.FormatInt(customerID, 10)},
		})
		wantRDashError(t, resp, m, http.StatusUnprocessableEntity, "Insufficient balance")
	})
}

func TestRDashInsufficientBalanceDuringScript(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)
	snipeSetBalance(t, ts, 0)
	snipeScript(t, ts, map[string]any{"domain": "broke.com", "fail_times": 1})

	resp, m := snipeRegister(t, ts, customerID, "broke.com", 1)
	wantRDashError(t, resp, m, http.StatusConflict, "Domain not available")
	// Script spent but the reseller cannot pay: 422, domain still free.
	resp, m = snipeRegister(t, ts, customerID, "broke.com", 1)
	wantRDashError(t, resp, m, http.StatusUnprocessableEntity, "Insufficient balance")
	if got := snipeAvailable(t, ts, "broke.com"); got != 1 {
		t.Fatalf("available = %d, want 1", got)
	}
	snipeSetBalance(t, ts, 10000)
	resp, _ = snipeRegister(t, ts, customerID, "broke.com", 1)
	wantStatus(t, resp, http.StatusOK)
	if got := snipeAttempts(t, ts, "broke.com"); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestRDashSetBalanceValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"invalid json", `{amount`},
		{"missing amount", `{}`},
		{"null amount", `{"amount":null}`},
		{"non-numeric amount", `{"amount":"abc"}`},
		{"negative amount", `{"amount":-1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ts := newTestServer(t)
			resp, m := rawPost(t, ts.URL+"/mock/rdash/balance", tc.body)
			wantStatus(t, resp, http.StatusBadRequest)
			if msg, _ := m["error"].(string); msg == "" {
				t.Fatalf("missing error message: %v", m)
			}
			if got := snipeBalance(t, ts); got != "10000000.00" {
				t.Fatalf("balance changed by a rejected request: %q", got)
			}
		})
	}

	t.Run("numeric string is accepted", func(t *testing.T) {
		_, ts := newTestServer(t)
		resp, m := rawPost(t, ts.URL+"/mock/rdash/balance", `{"amount":"75000"}`)
		wantStatus(t, resp, http.StatusOK)
		wantField(t, m, "balance", 75000)
		if got := snipeBalance(t, ts); got != "75000.00" {
			t.Fatalf("balance = %q", got)
		}
	})
}

// ---- atomicity ----

// TestRDashConcurrentSnipers fires many registrations of one name at once:
// exactly one wins, everyone else sees the duplicate 409, and the balance is
// debited exactly once.
func TestRDashConcurrentSnipers(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)

	const workers = 24
	statuses := make(chan int, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/domains", strings.NewReader(url.Values{
				"name": {"contested.com"}, "period": {"1"}, "customer_id": {strconv.FormatInt(customerID, 10)},
			}.Encode()))
			if err != nil {
				statuses <- -1
				return
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetBasicAuth(rdashUser, rdashPass)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				statuses <- -1
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)

	won, lost := 0, 0
	for st := range statuses {
		switch st {
		case http.StatusOK:
			won++
		case http.StatusConflict:
			lost++
		default:
			t.Fatalf("unexpected status %d", st)
		}
	}
	if won != 1 || lost != workers-1 {
		t.Fatalf("won=%d lost=%d, want 1 and %d", won, lost, workers-1)
	}
	if got := snipeBalance(t, ts); got != "9990000.00" {
		t.Fatalf("balance = %q, want a single 10000 debit", got)
	}
	if got := snipeAttempts(t, ts, "contested.com"); got != workers {
		t.Fatalf("attempts = %d, want %d", got, workers)
	}
}

// ---- reset ----

func TestRDashSnipeStateReset(t *testing.T) {
	_, ts := newTestServer(t)
	customerID := rdashNewCustomer(t, ts)

	snipeScript(t, ts, map[string]any{"domain": "reset-me.com", "fail_times": 5})
	snipeRegister(t, ts, customerID, "reset-me.com", 1) // attempt 1 (scripted 409)
	snipeFault(t, ts, map[string]any{"path": "/v1/domains/availability", "status": 503, "count": 5})
	snipeSetBalance(t, ts, 42)

	resp, m := postJSON(t, ts.URL+"/mock/reset", map[string]any{})
	wantStatus(t, resp, http.StatusOK)
	wantField(t, m, "status", "reset")

	// Fault queue cleared: the availability call is answered normally.
	respA, mA := rdashGet(t, ts, "/v1/domains/availability", url.Values{"domain": {"reset-me.com"}})
	wantStatus(t, respA, http.StatusOK)
	// Script cleared: the formerly held name reads as plainly available.
	wantField(t, rdashDataMap(t, mA), "available", 1)
	if got := snipeAttempts(t, ts, "reset-me.com"); got != 0 {
		t.Fatalf("attempts after reset = %d, want 0", got)
	}
	if got := snipeBalance(t, ts); got != "10000000.00" {
		t.Fatalf("balance after reset = %q, want 10000000.00", got)
	}

	// Customers are gone too, so make a new one; the name now registers first try.
	customerID = rdashNewCustomer(t, ts)
	resp, _ = snipeRegister(t, ts, customerID, "reset-me.com", 1)
	wantStatus(t, resp, http.StatusOK)
	if got := snipeAttempts(t, ts, "reset-me.com"); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}
