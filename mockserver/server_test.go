package main

import (
	"net/http"
	"net/url"
	"os"
	"testing"
)

func TestHealthz(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, resp, http.StatusOK)
	if body := bodyString(t, resp); body != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		for _, k := range []string{"MOCK_PORT", "DUITKU_MERCHANT_CODE", "DUITKU_API_KEY"} {
			if v, ok := os.LookupEnv(k); ok {
				t.Setenv(k, v) // register for restore
				os.Unsetenv(k)
			}
		}
		cfg := configFromEnv()
		if cfg.Port != "9090" || cfg.BaseURL != "http://localhost:9090" {
			t.Fatalf("port defaults wrong: %+v", cfg)
		}
		if cfg.DuitkuMerchantCode != "DEMO" || cfg.DuitkuAPIKey != "secretkey" {
			t.Fatalf("duitku defaults wrong: %+v", cfg)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		t.Setenv("MOCK_PORT", "9999")
		t.Setenv("DUITKU_MERCHANT_CODE", "D1234")
		t.Setenv("DUITKU_API_KEY", "otherkey")
		cfg := configFromEnv()
		if cfg.Port != "9999" || cfg.BaseURL != "http://localhost:9999" {
			t.Fatalf("port override wrong: %+v", cfg)
		}
		if cfg.DuitkuMerchantCode != "D1234" || cfg.DuitkuAPIKey != "otherkey" {
			t.Fatalf("duitku override wrong: %+v", cfg)
		}
	})
}

// TestMockReset seeds every store, resets, and verifies everything is gone.
func TestMockReset(t *testing.T) {
	_, ts := newTestServer(t)

	// Seed all four stores + mail.
	doInquiry(t, ts, "ORD-RESET", 15000, "", "")
	whmCreate(t, ts, "resetuser", "reset.com")
	daCreate(t, ts, "resetda")
	rdashRegister(t, ts, "resetdomain.id", 1)
	sendMail(t, ts, "reset@example.com", "before reset")

	resp, m := postJSON(t, ts.URL+"/mock/reset", map[string]any{})
	wantStatus(t, resp, http.StatusOK)
	wantField(t, m, "status", "reset")

	// Duitku transactions gone.
	respStatus, _ := checkStatus(t, ts, "ORD-RESET")
	wantStatus(t, respStatus, http.StatusNotFound)

	// WHM accounts gone.
	_, wm := whmCall(t, ts, http.MethodGet, "accountsummary", url.Values{"user": {"resetuser"}}, whmGoodAuth)
	if result, _ := whmMeta(t, wm); result != 0 {
		t.Fatal("WHM account should be gone after reset")
	}

	// DirectAdmin accounts gone.
	_, dv := daCall(t, ts, http.MethodGet, "/CMD_API_SHOW_USER_CONFIG", url.Values{"user": {"resetda"}}, "admin", "pass")
	if dv.Get("error") != "1" {
		t.Fatalf("DA account should be gone after reset: %v", dv)
	}

	// RDash domains gone.
	respDetails, _ := rdashGet(t, ts, "/v1/domains/details", url.Values{"domain_name": {"resetdomain.id"}})
	wantStatus(t, respDetails, http.StatusNotFound)

	// Mail gone.
	if msgs := listMail(t, ts, ""); len(msgs) != 0 {
		t.Fatalf("mail should be gone after reset: %v", msgs)
	}

	// Sequence restarts: a new inquiry gets MOCKREF-1 again.
	m2 := doInquiry(t, ts, "ORD-AFTER", 15000, "", "")
	wantField(t, m2, "reference", "MOCKREF-1")
}
