package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// --- signature vectors ---

func TestSignatureHelpers_Vectors(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"inquiry MD5(code+orderId+amount+key)", md5Hex(testMerchantCode + "ORD-1" + "150000" + testAPIKey), vecInquirySig},
		{"getpaymentmethod SHA256(code+amount+datetime+key)", sha256Hex(testMerchantCode + "150000" + vecDatetime + testAPIKey), vecGetMethodSig},
		{"callback MD5(code+amount+orderId+key)", md5Hex(testMerchantCode + "150000" + "ORD-1" + testAPIKey), vecCallbackSig},
		{"check MD5(code+orderId+key)", md5Hex(testMerchantCode + "ORD-1" + testAPIKey), vecStatusSig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("signature = %s, want %s", tt.got, tt.want)
			}
		})
	}
}

// --- get payment method ---

func TestDuitkuGetPaymentMethods(t *testing.T) {
	_, ts := newTestServer(t)
	endpoint := ts.URL + "/webapi/api/merchant/paymentmethod/getpaymentmethod"

	t.Run("valid signature returns 8 methods", func(t *testing.T) {
		resp, m := postJSON(t, endpoint, map[string]any{
			"merchantcode": testMerchantCode,
			"amount":       150000,
			"datetime":     vecDatetime,
			"signature":    vecGetMethodSig,
		})
		wantStatus(t, resp, http.StatusOK)
		wantField(t, m, "responseCode", "00")
		wantField(t, m, "responseMessage", "SUCCESS")

		fees, ok := m["paymentFee"].([]any)
		if !ok {
			t.Fatalf("paymentFee missing or wrong type: %v", m["paymentFee"])
		}
		if len(fees) != 8 {
			t.Fatalf("len(paymentFee) = %d, want 8", len(fees))
		}
		wantCodes := map[string]bool{"VC": false, "BC": false, "M2": false, "OV": false, "SA": false, "SP": false, "FT": false, "DN": false}
		for _, f := range fees {
			method := f.(map[string]any)
			code, _ := method["paymentMethod"].(string)
			if _, expected := wantCodes[code]; !expected {
				t.Fatalf("unexpected method code %q", code)
			}
			wantCodes[code] = true
			for _, k := range []string{"paymentName", "paymentImage", "totalFee"} {
				if v, _ := method[k].(string); v == "" {
					t.Fatalf("method %s: field %q empty", code, k)
				}
			}
		}
		for code, seen := range wantCodes {
			if !seen {
				t.Fatalf("method %s missing from response", code)
			}
		}
	})

	t.Run("amount as JSON string also validates", func(t *testing.T) {
		resp, m := postJSON(t, endpoint, map[string]any{
			"merchantcode": testMerchantCode,
			"amount":       "150000",
			"datetime":     vecDatetime,
			"signature":    vecGetMethodSig,
		})
		wantStatus(t, resp, http.StatusOK)
		wantField(t, m, "responseCode", "00")
	})

	t.Run("uppercase signature hex accepted", func(t *testing.T) {
		resp, _ := postJSON(t, endpoint, map[string]any{
			"merchantcode": testMerchantCode,
			"amount":       150000,
			"datetime":     vecDatetime,
			"signature":    strings.ToUpper(vecGetMethodSig),
		})
		wantStatus(t, resp, http.StatusOK)
	})

	t.Run("bad signature rejected", func(t *testing.T) {
		resp, m := postJSON(t, endpoint, map[string]any{
			"merchantcode": testMerchantCode,
			"amount":       150000,
			"datetime":     vecDatetime,
			"signature":    "deadbeef",
		})
		wantDuitkuBadSignature(t, resp, m)
	})

	t.Run("wrong merchant code rejected even with self-consistent signature", func(t *testing.T) {
		resp, m := postJSON(t, endpoint, map[string]any{
			"merchantcode": "OTHER",
			"amount":       150000,
			"datetime":     vecDatetime,
			"signature":    sha256Hex("OTHER" + "150000" + vecDatetime + testAPIKey),
		})
		wantDuitkuBadSignature(t, resp, m)
	})

	t.Run("malformed body rejected", func(t *testing.T) {
		resp, err := http.Post(endpoint, "application/json", strings.NewReader("{not json"))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		wantStatus(t, resp, http.StatusBadRequest)
	})
}

// --- inquiry ---

// doInquiry creates a pending mock transaction and returns the response body.
func doInquiry(t *testing.T, ts *httptest.Server, orderID string, amount int64, callbackURL, returnURL string) map[string]any {
	t.Helper()
	amountStr := strconv.FormatInt(amount, 10)
	resp, m := postJSON(t, ts.URL+"/webapi/api/merchant/v2/inquiry", map[string]any{
		"merchantCode":    testMerchantCode,
		"paymentAmount":   amount,
		"paymentMethod":   "BC",
		"merchantOrderId": orderID,
		"productDetails":  "Invoice " + orderID,
		"additionalParam": "extra",
		"callbackUrl":     callbackURL,
		"returnUrl":       returnURL,
		"signature":       md5Hex(testMerchantCode + orderID + amountStr + testAPIKey),
		"expiryPeriod":    60,
	})
	wantStatus(t, resp, http.StatusOK)
	return m
}

func TestDuitkuInquiry(t *testing.T) {
	_, ts := newTestServer(t)
	endpoint := ts.URL + "/webapi/api/merchant/v2/inquiry"

	t.Run("valid inquiry with vector signature", func(t *testing.T) {
		resp, m := postJSON(t, endpoint, map[string]any{
			"merchantCode":    testMerchantCode,
			"paymentAmount":   150000,
			"paymentMethod":   "BC",
			"merchantOrderId": "ORD-1",
			"productDetails":  "Invoice ORD-1",
			"callbackUrl":     "http://localhost:1/cb",
			"signature":       vecInquirySig,
		})
		wantStatus(t, resp, http.StatusOK)
		wantField(t, m, "statusCode", "00")
		wantField(t, m, "statusMessage", "SUCCESS")
		wantField(t, m, "merchantCode", testMerchantCode)
		wantField(t, m, "reference", "MOCKREF-1")
		wantField(t, m, "paymentUrl", ts.URL+"/payment/MOCKREF-1")
		wantField(t, m, "amount", "150000")
		// "BC" (BCA Virtual Account) is a VA-family code: vaNumber present,
		// qrString absent - real Duitku never returns both.
		if v, _ := m["vaNumber"].(string); v == "" {
			t.Fatal("vaNumber empty")
		}
		if _, ok := m["qrString"]; ok {
			t.Fatal("qrString must be absent for a VA-family method")
		}
	})

	t.Run("references are sequential", func(t *testing.T) {
		m := doInquiry(t, ts, "ORD-2", 25000, "", "")
		wantField(t, m, "reference", "MOCKREF-2")
	})

	t.Run("sub-minimum amount rejected for non-QRIS, accepted for QRIS", func(t *testing.T) {
		// Mirrors the real gateway: every non-QRIS channel rejects < 10000 IDR.
		orderID := "ORD-MIN"
		amountStr := "5000"
		resp, m := postJSON(t, endpoint, map[string]any{
			"merchantCode":    testMerchantCode,
			"paymentAmount":   5000,
			"paymentMethod":   "BC",
			"merchantOrderId": orderID,
			"signature":       md5Hex(testMerchantCode + orderID + amountStr + testAPIKey),
		})
		wantStatus(t, resp, http.StatusBadRequest)
		wantField(t, m, "statusMessage", "Minimum Payment 10000 IDR")

		resp, m = postJSON(t, endpoint, map[string]any{
			"merchantCode":    testMerchantCode,
			"paymentAmount":   5000,
			"paymentMethod":   "SP",
			"merchantOrderId": orderID,
			"signature":       md5Hex(testMerchantCode + orderID + amountStr + testAPIKey),
		})
		wantStatus(t, resp, http.StatusOK)
		if v, _ := m["qrString"].(string); v == "" {
			t.Fatal("qrString empty for sub-minimum QRIS payment")
		}
	})

	t.Run("QRIS method returns qrString only", func(t *testing.T) {
		orderID := "ORD-QRIS"
		amountStr := "75000"
		resp, m := postJSON(t, endpoint, map[string]any{
			"merchantCode":    testMerchantCode,
			"paymentAmount":   75000,
			"paymentMethod":   "SP",
			"merchantOrderId": orderID,
			"signature":       md5Hex(testMerchantCode + orderID + amountStr + testAPIKey),
		})
		wantStatus(t, resp, http.StatusOK)
		if v, _ := m["qrString"].(string); v == "" {
			t.Fatal("qrString empty")
		}
		if _, ok := m["vaNumber"]; ok {
			t.Fatal("vaNumber must be absent for a QRIS-family method")
		}
	})

	t.Run("e-wallet method returns neither vaNumber nor qrString", func(t *testing.T) {
		orderID := "ORD-OV"
		amountStr := "50000"
		resp, m := postJSON(t, endpoint, map[string]any{
			"merchantCode":    testMerchantCode,
			"paymentAmount":   50000,
			"paymentMethod":   "OV",
			"merchantOrderId": orderID,
			"signature":       md5Hex(testMerchantCode + orderID + amountStr + testAPIKey),
		})
		wantStatus(t, resp, http.StatusOK)
		if _, ok := m["vaNumber"]; ok {
			t.Fatal("vaNumber must be absent for a non-VA method")
		}
		if _, ok := m["qrString"]; ok {
			t.Fatal("qrString must be absent for a non-QRIS method")
		}
	})

	t.Run("amount as string accepted", func(t *testing.T) {
		resp, m := postJSON(t, endpoint, map[string]any{
			"merchantCode":    testMerchantCode,
			"paymentAmount":   "99000",
			"merchantOrderId": "ORD-STR",
			"signature":       md5Hex(testMerchantCode + "ORD-STR" + "99000" + testAPIKey),
		})
		wantStatus(t, resp, http.StatusOK)
		wantField(t, m, "amount", "99000")
	})

	t.Run("bad signature rejected", func(t *testing.T) {
		resp, m := postJSON(t, endpoint, map[string]any{
			"merchantCode":    testMerchantCode,
			"paymentAmount":   150000,
			"merchantOrderId": "ORD-1",
			"signature":       "deadbeef",
		})
		wantDuitkuBadSignature(t, resp, m)
	})

	t.Run("missing merchantOrderId rejected", func(t *testing.T) {
		resp, _ := postJSON(t, endpoint, map[string]any{
			"merchantCode":  testMerchantCode,
			"paymentAmount": 150000,
			"signature":     "irrelevant",
		})
		wantStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("missing amount rejected", func(t *testing.T) {
		resp, _ := postJSON(t, endpoint, map[string]any{
			"merchantCode":    testMerchantCode,
			"merchantOrderId": "ORD-NOAMOUNT",
			"signature":       "irrelevant",
		})
		wantStatus(t, resp, http.StatusBadRequest)
	})
}

// --- check transaction ---

// checkStatus calls transactionStatus with a valid signature.
func checkStatus(t *testing.T, ts *httptest.Server, orderID string) (*http.Response, map[string]any) {
	t.Helper()
	return postJSON(t, ts.URL+"/webapi/api/merchant/transactionStatus", map[string]any{
		"merchantCode":    testMerchantCode,
		"merchantOrderId": orderID,
		"signature":       md5Hex(testMerchantCode + orderID + testAPIKey),
	})
}

func TestDuitkuTransactionStatus(t *testing.T) {
	_, ts := newTestServer(t)

	t.Run("pending transaction returns 01", func(t *testing.T) {
		doInquiry(t, ts, "ORD-1", 150000, "", "")
		resp, m := postJSON(t, ts.URL+"/webapi/api/merchant/transactionStatus", map[string]any{
			"merchantCode":    testMerchantCode,
			"merchantOrderId": "ORD-1",
			"signature":       vecStatusSig, // hardcoded vector
		})
		wantStatus(t, resp, http.StatusOK)
		wantField(t, m, "statusCode", "01")
		wantField(t, m, "statusMessage", "PENDING")
		wantField(t, m, "merchantOrderId", "ORD-1")
		wantField(t, m, "reference", "MOCKREF-1")
		wantField(t, m, "amount", "150000")
	})

	t.Run("bad signature rejected", func(t *testing.T) {
		resp, m := postJSON(t, ts.URL+"/webapi/api/merchant/transactionStatus", map[string]any{
			"merchantCode":    testMerchantCode,
			"merchantOrderId": "ORD-1",
			"signature":       "deadbeef",
		})
		wantDuitkuBadSignature(t, resp, m)
	})

	t.Run("unknown order returns 404", func(t *testing.T) {
		resp, m := checkStatus(t, ts, "ORD-UNKNOWN")
		wantStatus(t, resp, http.StatusNotFound)
		wantField(t, m, "statusCode", "XX")
	})
}

// --- payment page ---

func TestDuitkuPaymentPage(t *testing.T) {
	_, ts := newTestServer(t)
	doInquiry(t, ts, "ORD-PAGE", 75000, "", "")

	resp, err := http.Get(ts.URL + "/payment/MOCKREF-1")
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, resp, http.StatusOK)
	body := bodyString(t, resp)

	for _, want := range []string{
		"<title>Duitku Mock Payment</title>",
		"ORD-PAGE",
		"Rp75000",
		`id="pay-now"`,
		"Bayar Sekarang",
		`action="/mock/duitku/pay/MOCKREF-1"`,
		`method="POST"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("payment page missing %q:\n%s", want, body)
		}
	}

	resp2, err := http.Get(ts.URL + "/payment/MOCKREF-DOES-NOT-EXIST")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	wantStatus(t, resp2, http.StatusNotFound)
}

// --- pay: callback delivery + redirect ---

// callbackReceiver records Duitku callbacks like the real backend webhook.
type callbackReceiver struct {
	mu       sync.Mutex
	forms    []url.Values
	failures int // respond 500 to this many requests before succeeding
}

func (c *callbackReceiver) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		c.mu.Lock()
		c.forms = append(c.forms, r.PostForm)
		fail := len(c.forms) <= c.failures
		c.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("OK"))
	}
}

func (c *callbackReceiver) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.forms)
}

func (c *callbackReceiver) last() url.Values {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.forms) == 0 {
		return nil
	}
	return c.forms[len(c.forms)-1]
}

func TestDuitkuPay_CallbackAndRedirect(t *testing.T) {
	_, ts := newTestServer(t)

	receiver := &callbackReceiver{}
	cbServer := httptest.NewServer(receiver.handler())
	defer cbServer.Close()

	returnURL := "http://localhost:5173/payments/return?src=duitku"
	doInquiry(t, ts, "ORD-1", 150000, cbServer.URL+"/webhook", returnURL)

	resp, err := noRedirectClient().Post(ts.URL+"/mock/duitku/pay/MOCKREF-1", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	wantStatus(t, resp, http.StatusSeeOther)

	// Redirect target carries the return parameters (existing query preserved).
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Host != "localhost:5173" || loc.Path != "/payments/return" {
		t.Fatalf("unexpected redirect target %s", loc)
	}
	q := loc.Query()
	for k, want := range map[string]string{
		"merchantOrderId": "ORD-1",
		"resultCode":      "00",
		"reference":       "MOCKREF-1",
		"src":             "duitku",
	} {
		if got := q.Get(k); got != want {
			t.Fatalf("redirect query %s = %q, want %q", k, got, want)
		}
	}

	// Exactly one callback was delivered, with correct fields and signature.
	if receiver.count() != 1 {
		t.Fatalf("callback count = %d, want 1", receiver.count())
	}
	form := receiver.last()
	for k, want := range map[string]string{
		"merchantCode":    testMerchantCode,
		"amount":          "150000",
		"merchantOrderId": "ORD-1",
		"productDetail":   "Invoice ORD-1",
		"additionalParam": "extra",
		"paymentCode":     "BC",
		"resultCode":      "00",
		"reference":       "MOCKREF-1",
		"signature":       vecCallbackSig, // hardcoded MD5 vector
	} {
		if got := form.Get(k); got != want {
			t.Fatalf("callback field %s = %q, want %q", k, got, want)
		}
	}

	// Check Transaction now reports success.
	respStatus, m := checkStatus(t, ts, "ORD-1")
	wantStatus(t, respStatus, http.StatusOK)
	wantField(t, m, "statusCode", "00")
	wantField(t, m, "statusMessage", "SUCCESS")

	// Paying an unknown reference is a 404.
	resp404, m404 := postJSON(t, ts.URL+"/mock/duitku/pay/MOCKREF-999", map[string]any{})
	wantStatus(t, resp404, http.StatusNotFound)
	wantField(t, m404, "statusCode", "XX")
}

func TestDuitkuPay_NoReturnURLRespondsJSON(t *testing.T) {
	_, ts := newTestServer(t)

	receiver := &callbackReceiver{}
	cbServer := httptest.NewServer(receiver.handler())
	defer cbServer.Close()

	doInquiry(t, ts, "ORD-NORET", 10000, cbServer.URL, "")

	resp, m := postJSON(t, ts.URL+"/mock/duitku/pay/MOCKREF-1", map[string]any{})
	wantStatus(t, resp, http.StatusOK)
	wantField(t, m, "status", "paid")
	wantField(t, m, "reference", "MOCKREF-1")
	wantField(t, m, "merchantOrderId", "ORD-NORET")
	if receiver.count() != 1 {
		t.Fatalf("callback count = %d, want 1", receiver.count())
	}
}

func TestDuitkuPay_CallbackRetries(t *testing.T) {
	t.Run("recovers after transient failures", func(t *testing.T) {
		_, ts := newTestServer(t)
		receiver := &callbackReceiver{failures: 2}
		cbServer := httptest.NewServer(receiver.handler())
		defer cbServer.Close()

		doInquiry(t, ts, "ORD-RETRY", 10000, cbServer.URL, "")
		resp, _ := postJSON(t, ts.URL+"/mock/duitku/pay/MOCKREF-1", map[string]any{})
		wantStatus(t, resp, http.StatusOK)

		if receiver.count() != 3 {
			t.Fatalf("callback attempts = %d, want 3 (2 failures + 1 success)", receiver.count())
		}
	})

	t.Run("gives up after 4 attempts but payment still succeeds", func(t *testing.T) {
		_, ts := newTestServer(t)
		receiver := &callbackReceiver{failures: 1000}
		cbServer := httptest.NewServer(receiver.handler())
		defer cbServer.Close()

		doInquiry(t, ts, "ORD-DEAD", 10000, cbServer.URL, "")
		resp, m := postJSON(t, ts.URL+"/mock/duitku/pay/MOCKREF-1", map[string]any{})
		wantStatus(t, resp, http.StatusOK)
		wantField(t, m, "status", "paid")

		if receiver.count() != 4 {
			t.Fatalf("callback attempts = %d, want 4 (1 initial + 3 retries)", receiver.count())
		}
		// The transaction is still marked paid - reconciliation would find it.
		respStatus, sm := checkStatus(t, ts, "ORD-DEAD")
		wantStatus(t, respStatus, http.StatusOK)
		wantField(t, sm, "statusCode", "00")
	})
}

func TestDuitkuExpire(t *testing.T) {
	_, ts := newTestServer(t)

	t.Run("expire pending then status 02 and pay conflicts", func(t *testing.T) {
		doInquiry(t, ts, "ORD-EXP", 15000, "", "")
		resp, m := postJSON(t, ts.URL+"/mock/duitku/expire/MOCKREF-1", map[string]any{})
		wantStatus(t, resp, http.StatusOK)
		wantField(t, m, "status", "expired")

		respStatus, sm := checkStatus(t, ts, "ORD-EXP")
		wantStatus(t, respStatus, http.StatusOK)
		wantField(t, sm, "statusCode", "02")
		wantField(t, sm, "statusMessage", "CANCELED")

		respPay, pm := postJSON(t, ts.URL+"/mock/duitku/pay/MOCKREF-1", map[string]any{})
		wantStatus(t, respPay, http.StatusConflict)
		wantField(t, pm, "statusCode", "XX")
	})

	t.Run("expiring a paid transaction conflicts", func(t *testing.T) {
		doInquiry(t, ts, "ORD-EXP2", 15000, "", "")
		respPay, _ := postJSON(t, ts.URL+"/mock/duitku/pay/MOCKREF-2", map[string]any{})
		wantStatus(t, respPay, http.StatusOK)

		resp, _ := postJSON(t, ts.URL+"/mock/duitku/expire/MOCKREF-2", map[string]any{})
		wantStatus(t, resp, http.StatusConflict)
	})

	t.Run("unknown reference 404", func(t *testing.T) {
		resp, _ := postJSON(t, ts.URL+"/mock/duitku/expire/MOCKREF-999", map[string]any{})
		wantStatus(t, resp, http.StatusNotFound)
	})
}

func TestBuildReturnURL_InvalidURLFallsBack(t *testing.T) {
	tx := duitkuTx{ReturnURL: "http://bad url\x7f", MerchantOrderID: "X", Reference: "R"}
	if got := buildReturnURL(tx); got != tx.ReturnURL {
		t.Fatalf("buildReturnURL = %q, want passthrough %q", got, tx.ReturnURL)
	}
}

func TestFlexInt64(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    int64
		wantSet bool
		wantErr bool
	}{
		{"number", `{"v":150000}`, 150000, true, false},
		{"string", `{"v":"150000"}`, 150000, true, false},
		{"float", `{"v":150000.0}`, 150000, true, false},
		{"null", `{"v":null}`, 0, false, false},
		{"empty string", `{"v":""}`, 0, false, false},
		{"garbage", `{"v":"abc"}`, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dst struct {
				V flexInt64 `json:"v"`
			}
			err := json.Unmarshal([]byte(tt.json), &dst)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if dst.V.Value != tt.want || dst.V.Set != tt.wantSet {
				t.Fatalf("flexInt64 = {%d %v}, want {%d %v}", dst.V.Value, dst.V.Set, tt.want, tt.wantSet)
			}
		})
	}
}
