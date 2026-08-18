package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func basicAuth(userpass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(userpass))
}

const whmGoodAuth = "whm root:goodtoken"

// whmCall hits /json-api/<fn>. For POST the params go in the form body; for
// GET they go in the query string.
func whmCall(t *testing.T, ts *httptest.Server, method, fn string, params url.Values, auth string) (*http.Response, map[string]any) {
	t.Helper()
	endpoint := ts.URL + "/json-api/" + fn
	var req *http.Request
	var err error
	if method == http.MethodGet {
		req, err = http.NewRequest(http.MethodGet, endpoint+"?"+params.Encode(), nil)
	} else {
		req, err = http.NewRequest(method, endpoint, strings.NewReader(params.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp, decodeBody(t, resp)
}

// whmMeta extracts result and reason from the WHM envelope.
func whmMeta(t *testing.T, m map[string]any) (result float64, reason string) {
	t.Helper()
	meta, ok := m["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing in %v", m)
	}
	result, _ = meta["result"].(float64)
	reason, _ = meta["reason"].(string)
	return result, reason
}

func whmCreate(t *testing.T, ts *httptest.Server, user, domain string) {
	t.Helper()
	resp, m := whmCall(t, ts, http.MethodPost, "createacct", url.Values{
		"username":     {user},
		"domain":       {domain},
		"plan":         {"starter"},
		"password":     {"S3cret!!"},
		"contactemail": {user + "@example.com"},
	}, whmGoodAuth)
	wantStatus(t, resp, http.StatusOK)
	if result, reason := whmMeta(t, m); result != 1 {
		t.Fatalf("createacct failed: result=%v reason=%q", result, reason)
	}
}

func TestWHMAuth(t *testing.T) {
	_, ts := newTestServer(t)
	tests := []struct {
		name       string
		auth       string
		wantStatus int
		wantResult float64
	}{
		{"missing header", "", http.StatusForbidden, 0},
		{"unsupported scheme", "Bearer xyz", http.StatusForbidden, 0},
		{"badtoken rejected", "whm root:badtoken", http.StatusForbidden, 0},
		{"empty token rejected", "whm root:", http.StatusForbidden, 0},
		{"missing colon rejected", "whm root", http.StatusForbidden, 0},
		{"any other token accepted", "whm root:whatever123", http.StatusOK, 1},
		{"scheme is case-insensitive", "WHM root:whatever123", http.StatusOK, 1},
		{"basic auth (password) accepted", basicAuth("root:s3cret"), http.StatusOK, 1},
		{"basic badpassword rejected", basicAuth("root:badpassword"), http.StatusForbidden, 0},
		{"basic empty password rejected", basicAuth("root:"), http.StatusForbidden, 0},
		{"basic malformed base64 rejected", "Basic !!!notb64", http.StatusForbidden, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, m := whmCall(t, ts, http.MethodGet, "listaccts", url.Values{}, tt.auth)
			wantStatus(t, resp, tt.wantStatus)
			if result, _ := whmMeta(t, m); result != tt.wantResult {
				t.Fatalf("result = %v, want %v", result, tt.wantResult)
			}
		})
	}
}

func TestWHMCreateAcct(t *testing.T) {
	_, ts := newTestServer(t)

	t.Run("success", func(t *testing.T) {
		resp, m := whmCall(t, ts, http.MethodPost, "createacct", url.Values{
			"username":     {"alice"},
			"domain":       {"alice.com"},
			"plan":         {"starter"},
			"password":     {"S3cret!!"},
			"contactemail": {"alice@example.com"},
		}, whmGoodAuth)
		wantStatus(t, resp, http.StatusOK)
		result, reason := whmMeta(t, m)
		if result != 1 || reason != "OK" {
			t.Fatalf("result=%v reason=%q, want 1/OK", result, reason)
		}
		data := m["data"].(map[string]any)
		wantField(t, data, "username", "alice")
		wantField(t, data, "domain", "alice.com")
		wantField(t, data, "package", "starter")
	})

	t.Run("duplicate username", func(t *testing.T) {
		_, m := whmCall(t, ts, http.MethodPost, "createacct", url.Values{
			"username": {"alice"}, "domain": {"other.com"},
		}, whmGoodAuth)
		result, reason := whmMeta(t, m)
		if result != 0 || reason != "account exists" {
			t.Fatalf("result=%v reason=%q, want 0/account exists", result, reason)
		}
	})

	t.Run("forced failure username failme", func(t *testing.T) {
		_, m := whmCall(t, ts, http.MethodPost, "createacct", url.Values{
			"username": {"failme"}, "domain": {"failme.com"},
		}, whmGoodAuth)
		result, reason := whmMeta(t, m)
		if result != 0 || reason != "forced failure" {
			t.Fatalf("result=%v reason=%q, want 0/forced failure", result, reason)
		}
	})

	t.Run("missing params", func(t *testing.T) {
		_, m := whmCall(t, ts, http.MethodPost, "createacct", url.Values{"username": {"nodomain"}}, whmGoodAuth)
		if result, _ := whmMeta(t, m); result != 0 {
			t.Fatal("expected failure without domain")
		}
	})

	t.Run("GET with query params also works", func(t *testing.T) {
		_, m := whmCall(t, ts, http.MethodGet, "createacct", url.Values{
			"username": {"getuser"}, "domain": {"getuser.com"},
		}, whmGoodAuth)
		if result, _ := whmMeta(t, m); result != 1 {
			t.Fatal("GET createacct should succeed")
		}
	})
}

func TestWHMAccountLifecycle(t *testing.T) {
	_, ts := newTestServer(t)
	whmCreate(t, ts, "bob", "bob.id")

	summary := func() map[string]any {
		t.Helper()
		_, m := whmCall(t, ts, http.MethodGet, "accountsummary", url.Values{"user": {"bob"}}, whmGoodAuth)
		if result, reason := whmMeta(t, m); result != 1 {
			t.Fatalf("accountsummary failed: %q", reason)
		}
		accts := m["data"].(map[string]any)["acct"].([]any)
		if len(accts) != 1 {
			t.Fatalf("acct len = %d, want 1", len(accts))
		}
		return accts[0].(map[string]any)
	}

	acct := summary()
	wantField(t, acct, "suspended", 0)
	wantField(t, acct, "plan", "starter")
	wantField(t, acct, "domain", "bob.id")

	// suspend with reason
	_, m := whmCall(t, ts, http.MethodPost, "suspendacct", url.Values{"user": {"bob"}, "reason": {"overdue invoice"}}, whmGoodAuth)
	if result, _ := whmMeta(t, m); result != 1 {
		t.Fatal("suspendacct failed")
	}
	acct = summary()
	wantField(t, acct, "suspended", 1)
	wantField(t, acct, "suspendreason", "overdue invoice")

	// unsuspend
	_, m = whmCall(t, ts, http.MethodPost, "unsuspendacct", url.Values{"user": {"bob"}}, whmGoodAuth)
	if result, _ := whmMeta(t, m); result != 1 {
		t.Fatal("unsuspendacct failed")
	}
	acct = summary()
	wantField(t, acct, "suspended", 0)
	wantField(t, acct, "suspendreason", "")

	// change package
	_, m = whmCall(t, ts, http.MethodPost, "changepackage", url.Values{"user": {"bob"}, "pkg": {"business"}}, whmGoodAuth)
	if result, _ := whmMeta(t, m); result != 1 {
		t.Fatal("changepackage failed")
	}
	acct = summary()
	wantField(t, acct, "plan", "business")

	// change password
	_, m = whmCall(t, ts, http.MethodPost, "passwd", url.Values{"user": {"bob"}, "password": {"NewP4ss!"}}, whmGoodAuth)
	if result, _ := whmMeta(t, m); result != 1 {
		t.Fatal("passwd failed")
	}

	// terminate
	_, m = whmCall(t, ts, http.MethodPost, "removeacct", url.Values{"user": {"bob"}}, whmGoodAuth)
	if result, _ := whmMeta(t, m); result != 1 {
		t.Fatal("removeacct failed")
	}
	_, m = whmCall(t, ts, http.MethodGet, "accountsummary", url.Values{"user": {"bob"}}, whmGoodAuth)
	if result, reason := whmMeta(t, m); result != 0 || reason != "account does not exist" {
		t.Fatalf("expected account does not exist, got result=%v reason=%q", result, reason)
	}
}

func TestWHMUnknownAccountAndFunction(t *testing.T) {
	_, ts := newTestServer(t)

	for _, fn := range []string{"suspendacct", "unsuspendacct", "removeacct", "passwd", "changepackage", "accountsummary", "create_user_session"} {
		t.Run(fn+" unknown user", func(t *testing.T) {
			params := url.Values{"user": {"ghost"}}
			if fn == "passwd" {
				params.Set("password", "x")
			}
			if fn == "changepackage" {
				params.Set("pkg", "x")
			}
			_, m := whmCall(t, ts, http.MethodPost, fn, params, whmGoodAuth)
			if result, _ := whmMeta(t, m); result != 0 {
				t.Fatalf("%s on unknown user should fail", fn)
			}
		})
	}

	t.Run("unknown function 404", func(t *testing.T) {
		resp, m := whmCall(t, ts, http.MethodGet, "nosuchfn", url.Values{}, whmGoodAuth)
		wantStatus(t, resp, http.StatusNotFound)
		if result, _ := whmMeta(t, m); result != 0 {
			t.Fatal("unknown function should have result 0")
		}
	})
}

func TestWHMListAccts(t *testing.T) {
	_, ts := newTestServer(t)
	whmCreate(t, ts, "zeta", "zeta.com")
	whmCreate(t, ts, "alpha", "alpha.com")

	_, m := whmCall(t, ts, http.MethodGet, "listaccts", url.Values{}, whmGoodAuth)
	if result, _ := whmMeta(t, m); result != 1 {
		t.Fatal("listaccts failed")
	}
	accts := m["data"].(map[string]any)["acct"].([]any)
	if len(accts) != 2 {
		t.Fatalf("acct len = %d, want 2", len(accts))
	}
	// deterministic sort by username
	first := accts[0].(map[string]any)
	wantField(t, first, "user", "alpha")
}

// TestWHMListAcctsPackageSearch covers the search filter the backend's
// package-in-use guard relies on: searchtype=package with an anchored regex
// must return only the accounts on that exact plan.
func TestWHMListAcctsPackageSearch(t *testing.T) {
	_, ts := newTestServer(t)
	whmCreate(t, ts, "alpha", "alpha.com") // plan "starter"
	whmCreate(t, ts, "zeta", "zeta.com")   // plan "starter"
	// One account on a different plan, moved via changepackage.
	whmCreate(t, ts, "beta", "beta.com")
	if _, m := whmCall(t, ts, http.MethodPost, "changepackage",
		url.Values{"user": {"beta"}, "pkg": {"starter_plus"}}, whmGoodAuth); m != nil {
		if result, _ := whmMeta(t, m); result != 1 {
			t.Fatal("changepackage failed")
		}
	}

	list := func(search string) []any {
		_, m := whmCall(t, ts, http.MethodGet, "listaccts",
			url.Values{"searchtype": {"package"}, "search": {search}}, whmGoodAuth)
		if result, _ := whmMeta(t, m); result != 1 {
			t.Fatalf("listaccts %q failed", search)
		}
		return m["data"].(map[string]any)["acct"].([]any)
	}

	// The anchored exact-name regex must NOT match the "starter_plus" account.
	if accts := list("^starter$"); len(accts) != 2 {
		t.Fatalf("anchored search matched %d accounts, want 2", len(accts))
	}
	if accts := list("^starter_plus$"); len(accts) != 1 {
		t.Fatalf("starter_plus search matched %d accounts, want 1", len(accts))
	}
	if accts := list("^nosuchpkg$"); len(accts) != 0 {
		t.Fatalf("nosuchpkg search matched %d accounts, want 0", len(accts))
	}

	// An invalid regex is an API-level error (result 0), not a crash.
	_, m := whmCall(t, ts, http.MethodGet, "listaccts",
		url.Values{"searchtype": {"package"}, "search": {"("}}, whmGoodAuth)
	if result, _ := whmMeta(t, m); result != 0 {
		t.Fatal("invalid regex should have result 0")
	}
}

func TestWHMTestConnectionProbes(t *testing.T) {
	_, ts := newTestServer(t)

	// version - the authoritative connectivity/credential probe.
	_, m := whmCall(t, ts, http.MethodGet, "version", url.Values{}, whmGoodAuth)
	if result, _ := whmMeta(t, m); result != 1 {
		t.Fatal("version failed")
	}
	if v, _ := m["data"].(map[string]any)["version"].(string); v == "" {
		t.Fatalf("version data missing: %v", m["data"])
	}

	// gethostname - auto-fill metadata.
	_, m = whmCall(t, ts, http.MethodGet, "gethostname", url.Values{}, whmGoodAuth)
	wantField(t, m["data"].(map[string]any), "hostname", "mock.whm.local")

	// get_nameserver_config - nameserver auto-population source.
	_, m = whmCall(t, ts, http.MethodGet, "get_nameserver_config", url.Values{}, whmGoodAuth)
	ns := m["data"].(map[string]any)["nameservers"].([]any)
	if len(ns) != 2 || ns[0] != "ns1.mock.local" {
		t.Fatalf("nameservers = %v", ns)
	}

	// A bad token is rejected before any probe runs.
	resp, _ := whmCall(t, ts, http.MethodGet, "version", url.Values{}, "whm user:badtoken")
	wantStatus(t, resp, http.StatusForbidden)
}

func TestWHMCreateUserSession(t *testing.T) {
	_, ts := newTestServer(t)
	whmCreate(t, ts, "ssouser", "sso.com")

	_, m := whmCall(t, ts, http.MethodPost, "create_user_session", url.Values{"user": {"ssouser"}}, whmGoodAuth)
	if result, _ := whmMeta(t, m); result != 1 {
		t.Fatal("create_user_session failed")
	}
	data := m["data"].(map[string]any)
	ssoURL, _ := data["url"].(string)
	if ssoURL != ts.URL+"/cpanel-sso/ssouser" {
		t.Fatalf("sso url = %q", ssoURL)
	}

	// The SSO URL actually resolves.
	resp, err := http.Get(ssoURL)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, resp, http.StatusOK)
	if body := bodyString(t, resp); !strings.Contains(body, "ssouser") {
		t.Fatalf("sso page missing user: %s", body)
	}
}
