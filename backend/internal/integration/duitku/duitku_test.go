package duitku

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// fixedUTC is 2026-07-03 03:00:00 UTC == 2026-07-03 10:00:00 WIB, matching
// the signature vectors in signature_test.go (proves the WIB conversion).
var fixedUTC = time.Date(2026, 7, 3, 3, 0, 0, 0, time.UTC)

var errBoom = errors.New("boom")

func testConfig(baseURL string) Config {
	return Config{
		MerchantCode: vecMerchantCode,
		APIKey:       vecAPIKey,
		BaseURL:      baseURL,
		CallbackURL:  "http://localhost:8080/api/v1/webhooks/duitku",
		ReturnURL:    "http://localhost:5173/payments/return",
	}
}

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *mocks.MockIntegrationLogger) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	ilog := &mocks.MockIntegrationLogger{}
	clock := &mocks.MockClock{FixedTime: fixedUTC}
	return New(testConfig(srv.URL), nil, srv.Client(), ilog, clock), ilog
}

func decodeReq(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m))
	return m
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

// GetPaymentMethods

func TestGetPaymentMethodsSuccess(t *testing.T) {
	var gotReq map[string]any
	c, ilog := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, pathGetPaymentMethod, r.URL.Path)
		gotReq = decodeReq(t, r)
		// Mirror of the real/mock wire shape: totalFee is a JSON STRING.
		writeJSON(t, w, http.StatusOK, map[string]any{
			"paymentFee": []map[string]any{
				{"paymentMethod": "VC", "paymentName": "Credit Card (Visa / Master)", "paymentImage": "https://images.duitku.com/hotlink-ok/VC.PNG", "totalFee": "5000"},
				{"paymentMethod": "BC", "paymentName": "BCA Virtual Account", "paymentImage": "https://images.duitku.com/hotlink-ok/BC.PNG", "totalFee": "4000"},
				{"paymentMethod": "OV", "paymentName": "OVO", "paymentImage": "https://images.duitku.com/hotlink-ok/OV.PNG", "totalFee": "0"},
			},
			"responseCode":    "00",
			"responseMessage": "SUCCESS",
		})
	})

	methods, err := c.GetPaymentMethods(context.Background(), 150000)
	require.NoError(t, err)

	// Request wire shape + signed with the fixed vector (datetime in WIB).
	assert.Equal(t, vecMerchantCode, gotReq["merchantcode"])
	assert.Equal(t, float64(150000), gotReq["amount"], "amount is a JSON number")
	assert.Equal(t, vecDatetime, gotReq["datetime"], "datetime formatted 2006-01-02 15:04:05 in WIB")
	assert.Equal(t, vecSigGetMethod, gotReq["signature"])

	require.Len(t, methods, 3)
	assert.Equal(t, ports.PaymentMethod{Code: "VC", Name: "Credit Card (Visa / Master)", Image: "https://images.duitku.com/hotlink-ok/VC.PNG", Fee: 5000}, methods[0])
	assert.Equal(t, int64(4000), methods[1].Fee)
	assert.Equal(t, int64(0), methods[2].Fee)

	// Integration log: one call, success, signature redacted.
	require.Len(t, ilog.Calls, 1)
	call := ilog.Calls[0]
	assert.Equal(t, "duitku", call.Provider)
	assert.Equal(t, pathGetPaymentMethod, call.Endpoint)
	assert.Equal(t, http.MethodPost, call.Method)
	assert.Equal(t, http.StatusOK, call.StatusCode)
	assert.True(t, call.Success)
	reqMap, ok := call.Request.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, redactedPlaceholder, reqMap["signature"])
	assert.Equal(t, vecMerchantCode, reqMap["merchantcode"], "non-secret fields kept")
}

func TestGetPaymentMethodsFiltersSubMinimumChannels(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"paymentFee": []map[string]any{
				{"paymentMethod": "VC", "paymentName": "Credit Card", "paymentImage": "", "totalFee": "5000"},
				{"paymentMethod": "BC", "paymentName": "BCA Virtual Account", "paymentImage": "", "totalFee": "4000"},
				{"paymentMethod": "SP", "paymentName": "QRIS ShopeePay", "paymentImage": "", "totalFee": "700"},
				{"paymentMethod": "NQ", "paymentName": "QRIS Nobu", "paymentImage": "", "totalFee": "700"},
			},
			"responseCode":    "00",
			"responseMessage": "SUCCESS",
		})
	}

	// Below the non-QRIS floor: only the QRIS family survives (Duitku would
	// reject an inquiry on any other channel with "Minimum Payment 10000 IDR").
	c, _ := newTestClient(t, handler)
	methods, err := c.GetPaymentMethods(context.Background(), 5000)
	require.NoError(t, err)
	require.Len(t, methods, 2)
	assert.Equal(t, "SP", methods[0].Code)
	assert.Equal(t, "NQ", methods[1].Code)

	// At the floor every channel is offered.
	c2, _ := newTestClient(t, handler)
	methods, err = c2.GetPaymentMethods(context.Background(), 10000)
	require.NoError(t, err)
	assert.Len(t, methods, 4)
}

func TestGetPaymentMethodsRejectsNonPositiveAmount(t *testing.T) {
	c := New(testConfig("http://unused.invalid"), nil, nil, &mocks.MockIntegrationLogger{}, &mocks.MockClock{FixedTime: fixedUTC})
	for _, amount := range []int64{0, -1} {
		_, err := c.GetPaymentMethods(context.Background(), amount)
		require.Error(t, err)
		assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)
	}
}

func TestGetPaymentMethodsGatewayResponseCodeError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"paymentFee":      []any{},
			"responseCode":    "01",
			"responseMessage": "amount out of range",
		})
	})
	_, err := c.GetPaymentMethods(context.Background(), 150000)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	assert.Contains(t, err.Error(), "amount out of range")
}

func TestGetPaymentMethodsBadSignatureHTTP400(t *testing.T) {
	c, ilog := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]any{
			"statusCode": "XX", "statusMessage": "invalid signature",
		})
	})
	_, err := c.GetPaymentMethods(context.Background(), 150000)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	assert.Contains(t, err.Error(), "invalid signature")
	require.Len(t, ilog.Calls, 1, "4xx is not retried")
	assert.False(t, ilog.Calls[0].Success)
}

func TestGetPaymentMethodsMalformedJSON(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>gateway timeout</html>"))
	})
	_, err := c.GetPaymentMethods(context.Background(), 150000)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
}

func TestGetPaymentMethodsUnparseableFee(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"paymentFee":   []map[string]any{{"paymentMethod": "VC", "totalFee": "abc"}},
			"responseCode": "00",
		})
	})
	_, err := c.GetPaymentMethods(context.Background(), 150000)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
}

func TestGetPaymentMethodsRetriesOn5xx(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	c, ilog := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		if n <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"paymentFee":   []map[string]any{{"paymentMethod": "VC", "paymentName": "CC", "totalFee": "5000"}},
			"responseCode": "00",
		})
	})

	methods, err := c.GetPaymentMethods(context.Background(), 150000)
	require.NoError(t, err)
	require.Len(t, methods, 1)
	assert.Equal(t, 3, hits, "two 5xx retries then success")
	assert.Len(t, ilog.Calls, 3, "every attempt logged")
}

// CreateTransaction

func inquiryOKResponse() map[string]any {
	// Mirror of the real/mock wire shape: amount is a JSON STRING.
	return map[string]any{
		"merchantCode":  vecMerchantCode,
		"reference":     "MOCKREF-1",
		"paymentUrl":    "http://localhost:9090/payment/MOCKREF-1",
		"vaNumber":      "7007000000000001",
		"qrString":      "MOCK-QR-MOCKREF-1",
		"amount":        "150000",
		"statusCode":    "00",
		"statusMessage": "SUCCESS",
	}
}

func TestCreateTransactionSuccess(t *testing.T) {
	var gotReq map[string]any
	c, ilog := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, pathInquiry, r.URL.Path)
		gotReq = decodeReq(t, r)
		writeJSON(t, w, http.StatusOK, inquiryOKResponse())
	})

	res, err := c.CreateTransaction(context.Background(), ports.CreateTxRequest{
		MerchantOrderID: vecOrderID,
		Amount:          150000,
		Method:          "BC",
		ProductDetails:  "Invoice INV-202607-000001",
		Email:           "client@example.com",
		Phone:           "081234567890",
		CustomerName:    "Budi Santoso",
		ExpiryMinutes:   1440,
	})
	require.NoError(t, err)

	assert.Equal(t, vecMerchantCode, gotReq["merchantCode"])
	assert.Equal(t, float64(150000), gotReq["paymentAmount"], "paymentAmount is a JSON number")
	assert.Equal(t, "BC", gotReq["paymentMethod"])
	assert.Equal(t, vecOrderID, gotReq["merchantOrderId"])
	assert.Equal(t, "Invoice INV-202607-000001", gotReq["productDetails"])
	assert.Equal(t, "", gotReq["additionalParam"])
	assert.Equal(t, "client@example.com", gotReq["email"])
	assert.Equal(t, "081234567890", gotReq["phoneNumber"])
	assert.Equal(t, "Budi Santoso", gotReq["customerVaName"])
	assert.Equal(t, "http://localhost:8080/api/v1/webhooks/duitku", gotReq["callbackUrl"], "falls back to Config.CallbackURL")
	assert.Equal(t, "http://localhost:5173/payments/return", gotReq["returnUrl"], "falls back to Config.ReturnURL")
	assert.Equal(t, float64(1440), gotReq["expiryPeriod"])
	assert.Equal(t, vecSigInquiry, gotReq["signature"], "MD5(merchantCode+merchantOrderId+paymentAmount+apiKey)")

	assert.Equal(t, &ports.CreateTxResult{
		Reference:  "MOCKREF-1",
		PaymentURL: "http://localhost:9090/payment/MOCKREF-1",
		VANumber:   "7007000000000001",
		QRString:   "MOCK-QR-MOCKREF-1",
		Amount:     150000,
	}, res)

	require.Len(t, ilog.Calls, 1)
	reqMap := ilog.Calls[0].Request.(map[string]any)
	assert.Equal(t, redactedPlaceholder, reqMap["signature"])
}

func TestCreateTransactionExplicitURLsAndNoExpiry(t *testing.T) {
	var gotReq map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotReq = decodeReq(t, r)
		writeJSON(t, w, http.StatusOK, inquiryOKResponse())
	})

	_, err := c.CreateTransaction(context.Background(), ports.CreateTxRequest{
		MerchantOrderID: vecOrderID,
		Amount:          150000,
		Method:          "OV",
		CallbackURL:     "https://api.example.com/hook",
		ReturnURL:       "https://app.example.com/return",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://api.example.com/hook", gotReq["callbackUrl"], "request URL wins over config default")
	assert.Equal(t, "https://app.example.com/return", gotReq["returnUrl"])
	_, hasExpiry := gotReq["expiryPeriod"]
	assert.False(t, hasExpiry, "expiryPeriod omitted when zero")
}

func TestCreateTransactionSurfacesGatewayMessage(t *testing.T) {
	// The client-visible apperr message must carry Duitku's own reason (e.g.
	// the minimum-payment rejection) - a bare "duitku error" gives the payer
	// nothing to act on.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]any{"Message": "Minimum Payment 10000 IDR"})
	})
	_, err := c.CreateTransaction(context.Background(), ports.CreateTxRequest{
		MerchantOrderID: "INV-1-01", Amount: 5000, Method: "BC",
		CustomerName: "Tester", Email: "t@example.test",
	})
	require.Error(t, err)
	ae := apperr.From(err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Message, "Minimum Payment 10000 IDR")
}

func TestCreateTransactionValidation(t *testing.T) {
	c := New(testConfig("http://unused.invalid"), nil, nil, &mocks.MockIntegrationLogger{}, &mocks.MockClock{FixedTime: fixedUTC})
	_, err := c.CreateTransaction(context.Background(), ports.CreateTxRequest{})
	require.Error(t, err)
	e := apperr.From(err)
	assert.Equal(t, apperr.CodeValidation, e.Code)
	require.Len(t, e.Details, 3)
	fields := []string{e.Details[0].Field, e.Details[1].Field, e.Details[2].Field}
	assert.ElementsMatch(t, []string{"merchant_order_id", "amount", "method"}, fields)
}

func TestCreateTransactionGatewayStatusNotOK(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		resp := inquiryOKResponse()
		resp["statusCode"] = "02"
		resp["statusMessage"] = "payment channel unavailable"
		writeJSON(t, w, http.StatusOK, resp)
	})
	_, err := c.CreateTransaction(context.Background(), ports.CreateTxRequest{
		MerchantOrderID: vecOrderID, Amount: 150000, Method: "BC",
	})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	assert.Contains(t, err.Error(), "payment channel unavailable")
}

func TestCreateTransactionHTTP400(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]any{"statusCode": "XX", "statusMessage": "invalid signature"})
	})
	_, err := c.CreateTransaction(context.Background(), ports.CreateTxRequest{
		MerchantOrderID: vecOrderID, Amount: 150000, Method: "BC",
	})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	assert.Contains(t, err.Error(), "invalid signature")
}

func TestCreateTransactionNeverRetries(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.CreateTransaction(context.Background(), ports.CreateTxRequest{
		MerchantOrderID: vecOrderID, Amount: 150000, Method: "BC",
	})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	assert.Equal(t, 1, hits, "POST inquiry must not auto-retry")
}

func TestCreateTransactionMalformedJSON(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{not json"))
	})
	_, err := c.CreateTransaction(context.Background(), ports.CreateTxRequest{
		MerchantOrderID: vecOrderID, Amount: 150000, Method: "BC",
	})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
}

// CheckTransaction

func TestCheckTransactionStatuses(t *testing.T) {
	for _, tc := range []struct {
		statusCode    string
		statusMessage string
	}{
		{"00", "SUCCESS"},
		{"01", "PENDING"},
		{"02", "CANCELED"},
	} {
		t.Run(tc.statusCode, func(t *testing.T) {
			var gotReq map[string]any
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, pathTransactionStatus, r.URL.Path)
				gotReq = decodeReq(t, r)
				// Mirror of the real/mock wire shape: amount + fee are STRINGS.
				writeJSON(t, w, http.StatusOK, map[string]any{
					"merchantOrderId": vecOrderID,
					"reference":       "MOCKREF-1",
					"amount":          "150000",
					"fee":             "0",
					"statusCode":      tc.statusCode,
					"statusMessage":   tc.statusMessage,
				})
			})

			st, err := c.CheckTransaction(context.Background(), vecOrderID)
			require.NoError(t, err)
			assert.Equal(t, vecMerchantCode, gotReq["merchantCode"])
			assert.Equal(t, vecOrderID, gotReq["merchantOrderId"])
			assert.Equal(t, vecSigCheck, gotReq["signature"], "MD5(merchantCode+merchantOrderId+apiKey)")
			assert.Equal(t, &ports.TxStatus{
				Reference:     "MOCKREF-1",
				Amount:        150000,
				StatusCode:    tc.statusCode,
				StatusMessage: tc.statusMessage,
			}, st)
		})
	}
}

func TestCheckTransactionUnknownOrderMapsTo404(t *testing.T) {
	c, ilog := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusNotFound, map[string]any{"statusCode": "XX", "statusMessage": "transaction not found"})
	})
	_, err := c.CheckTransaction(context.Background(), "UNKNOWN-1")
	require.Error(t, err)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code)
	require.Len(t, ilog.Calls, 1, "404 is not retried")
}

func TestCheckTransactionEmptyOrderID(t *testing.T) {
	c := New(testConfig("http://unused.invalid"), nil, nil, &mocks.MockIntegrationLogger{}, &mocks.MockClock{FixedTime: fixedUTC})
	_, err := c.CheckTransaction(context.Background(), "")
	require.Error(t, err)
	assert.Equal(t, apperr.CodeValidation, apperr.From(err).Code)
}

func TestCheckTransactionRetriesThenFails(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := c.CheckTransaction(context.Background(), vecOrderID)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	assert.Equal(t, 3, hits, "1 attempt + 2 retries")
}

func TestCheckTransactionHTTP400(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]any{"statusCode": "XX", "statusMessage": "invalid signature"})
	})
	_, err := c.CheckTransaction(context.Background(), vecOrderID)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	assert.Contains(t, err.Error(), "invalid signature")
}

func TestResponseBodyReadError(t *testing.T) {
	c, ilog := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short")) // fewer bytes than advertised -> client read error
	})
	_, err := c.CreateTransaction(context.Background(), ports.CreateTxRequest{
		MerchantOrderID: vecOrderID, Amount: 150000, Method: "BC",
	})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	require.Len(t, ilog.Calls, 1)
	assert.NotEmpty(t, ilog.Calls[0].Error)
	assert.False(t, ilog.Calls[0].Success)
}

func TestCheckTransactionMalformedJSON(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]{"))
	})
	_, err := c.CheckTransaction(context.Background(), vecOrderID)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
}

func TestNetworkErrorMapsToExternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listening anymore
	ilog := &mocks.MockIntegrationLogger{}
	c := New(testConfig(url), nil, nil, ilog, &mocks.MockClock{FixedTime: fixedUTC})

	_, err := c.CheckTransaction(context.Background(), vecOrderID)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	require.Len(t, ilog.Calls, 3, "network errors are retried and each attempt logged")
	assert.NotEmpty(t, ilog.Calls[0].Error)
	assert.False(t, ilog.Calls[0].Success)
}

func TestContextCancelledDuringRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		cancel() // fail the first attempt, then the retry loop sees ctx.Done
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.CheckTransaction(ctx, vecOrderID)
	require.Error(t, err)
	e := apperr.From(err)
	assert.Equal(t, apperr.CodeExternal, e.Code)
	assert.True(t, errors.Is(e.Unwrap(), context.Canceled))
}

func TestBadBaseURLRequestCreationError(t *testing.T) {
	ilog := &mocks.MockIntegrationLogger{}
	c := New(Config{MerchantCode: vecMerchantCode, APIKey: vecAPIKey, BaseURL: "http://\x7f"},
		nil, nil, ilog, &mocks.MockClock{FixedTime: fixedUTC})
	_, err := c.CreateTransaction(context.Background(), ports.CreateTxRequest{
		MerchantOrderID: vecOrderID, Amount: 150000, Method: "BC",
	})
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	require.Len(t, ilog.Calls, 1, "failed request creation is still logged")
}

// VerifyCallbackSignature

func TestVerifyCallbackSignature(t *testing.T) {
	c := New(testConfig("http://unused.invalid"), nil, nil, &mocks.MockIntegrationLogger{}, &mocks.MockClock{FixedTime: fixedUTC})

	valid := ports.CallbackPayload{
		MerchantCode:    vecMerchantCode,
		Amount:          vecAmount,
		MerchantOrderID: vecOrderID,
		ResultCode:      "00",
		Reference:       "MOCKREF-1",
		Signature:       vecSigCallback,
	}

	t.Run("valid", func(t *testing.T) {
		assert.True(t, c.VerifyCallbackSignature(valid))
	})
	t.Run("uppercase hex accepted", func(t *testing.T) {
		p := valid
		p.Signature = "862F4F3C58F4F6E676038789450A4FFD"
		assert.True(t, c.VerifyCallbackSignature(p))
	})
	t.Run("surrounding whitespace tolerated", func(t *testing.T) {
		p := valid
		p.Signature = " " + vecSigCallback + " "
		assert.True(t, c.VerifyCallbackSignature(p))
	})
	t.Run("tampered amount", func(t *testing.T) {
		p := valid
		p.Amount = "999999"
		assert.False(t, c.VerifyCallbackSignature(p))
	})
	t.Run("tampered order id", func(t *testing.T) {
		p := valid
		p.MerchantOrderID = "INV-202607-000002-01"
		assert.False(t, c.VerifyCallbackSignature(p))
	})
	t.Run("wrong merchant code", func(t *testing.T) {
		p := valid
		p.MerchantCode = "OTHER"
		assert.False(t, c.VerifyCallbackSignature(p))
	})
	t.Run("wrong signature", func(t *testing.T) {
		p := valid
		p.Signature = "deadbeefdeadbeefdeadbeefdeadbeef"
		assert.False(t, c.VerifyCallbackSignature(p))
	})
	t.Run("empty signature", func(t *testing.T) {
		p := valid
		p.Signature = ""
		assert.False(t, c.VerifyCallbackSignature(p))
	})
}

// Circuit breaker

func TestCircuitBreakerOpensAndRecovers(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	failing := true
	handler := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		fail := failing
		mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		writeJSON(t, w, http.StatusOK, inquiryOKResponse())
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	now := fixedUTC
	clock := &mocks.MockClock{NowFn: func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}}
	c := New(testConfig(srv.URL), nil, srv.Client(), &mocks.MockIntegrationLogger{}, clock)

	req := ports.CreateTxRequest{MerchantOrderID: vecOrderID, Amount: 150000, Method: "BC"}

	// Three consecutive failures (inquiry is non-retriable: 1 hit each) trip
	// the breaker.
	for i := 0; i < breakerThreshold; i++ {
		_, err := c.CreateTransaction(context.Background(), req)
		require.Error(t, err)
	}
	mu.Lock()
	assert.Equal(t, 3, hits)
	mu.Unlock()

	// Circuit open: fast-fail without touching the server.
	_, err := c.CreateTransaction(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code)
	assert.Contains(t, err.Error(), "circuit open")
	mu.Lock()
	assert.Equal(t, 3, hits, "no HTTP call while the circuit is open")
	mu.Unlock()

	// After the cooldown the next call goes through and closes the circuit.
	mu.Lock()
	now = now.Add(breakerCooldown + time.Second)
	failing = false
	mu.Unlock()
	res, err := c.CreateTransaction(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "MOCKREF-1", res.Reference)
	mu.Lock()
	assert.Equal(t, 4, hits)
	mu.Unlock()
}

func TestBreakerUnit(t *testing.T) {
	t.Parallel()
	b := newBreaker()
	now := fixedUTC

	assert.True(t, b.allow(now), "closed by default")
	b.failure(now)
	b.failure(now)
	assert.True(t, b.allow(now), "below threshold stays closed")
	b.failure(now)
	assert.False(t, b.allow(now), "threshold reached opens the circuit")
	assert.False(t, b.allow(now.Add(breakerCooldown-time.Second)))
	assert.True(t, b.allow(now.Add(breakerCooldown)), "cooldown elapsed allows a probe")

	b.success()
	assert.True(t, b.allow(now))
	b.failure(now)
	b.failure(now)
	assert.True(t, b.allow(now), "success reset the consecutive counter")
}

func Test4xxDoesNotTripBreaker(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]any{"statusCode": "XX", "statusMessage": "invalid signature"})
	})
	req := ports.CreateTxRequest{MerchantOrderID: vecOrderID, Amount: 150000, Method: "BC"}
	for i := 0; i < breakerThreshold+1; i++ {
		_, err := c.CreateTransaction(context.Background(), req)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "circuit open")
	}
}

// Helpers

func TestFlexInt64(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{`150000`, 150000, false},
		{`"150000"`, 150000, false},
		{`"150000.00"`, 150000, false},
		{`0`, 0, false},
		{`"0"`, 0, false},
		{`null`, 0, false},
		{`""`, 0, false},
		{`"abc"`, 0, true},
	} {
		var f flexInt64
		err := json.Unmarshal([]byte(tc.in), &f)
		if tc.wantErr {
			assert.Error(t, err, tc.in)
			continue
		}
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, f.Value, tc.in)
	}
}

func TestRedactValue(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"merchantCode": "DEMO",
		"Signature":    "abc",
		"apiKey":       "secret",
		"API_KEY":      "secret",
		"nested": map[string]any{
			"password":      "hunter2",
			"passwd":        "hunter2",
			"token":         "tok",
			"Authorization": "Bearer x",
			"keep":          "me",
		},
		"list": []any{map[string]any{"signature": "abc"}, "plain"},
	}
	out := redactValue(in).(map[string]any)
	assert.Equal(t, "DEMO", out["merchantCode"])
	assert.Equal(t, redactedPlaceholder, out["Signature"], "case-insensitive key match")
	assert.Equal(t, redactedPlaceholder, out["apiKey"])
	assert.Equal(t, redactedPlaceholder, out["API_KEY"])
	nested := out["nested"].(map[string]any)
	for _, k := range []string{"password", "passwd", "token", "Authorization"} {
		assert.Equal(t, redactedPlaceholder, nested[k], k)
	}
	assert.Equal(t, "me", nested["keep"])
	list := out["list"].([]any)
	assert.Equal(t, redactedPlaceholder, list[0].(map[string]any)["signature"])
	assert.Equal(t, "plain", list[1])
	// Original untouched.
	assert.Equal(t, "abc", in["Signature"])
}

func TestRedactBody(t *testing.T) {
	t.Parallel()
	assert.Nil(t, redactBody(nil))
	assert.Equal(t, "not json", redactBody([]byte("not json")))
	m := redactBody([]byte(`{"signature":"abc","x":1}`)).(map[string]any)
	assert.Equal(t, redactedPlaceholder, m["signature"])
	assert.Equal(t, float64(1), m["x"])
	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'a'
	}
	assert.Len(t, redactBody(long).(string), 2048, "non-JSON bodies truncated")
}

func TestGatewayMessage(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "invalid signature", gatewayMessage([]byte(`{"statusCode":"XX","statusMessage":"invalid signature"}`)))
	assert.Equal(t, "bad amount", gatewayMessage([]byte(`{"responseMessage":"bad amount"}`)))
	assert.Equal(t, "denied", gatewayMessage([]byte(`{"Message":"denied"}`)))
	assert.Equal(t, "plain text error", gatewayMessage([]byte("plain text error")))
	assert.Equal(t, "no response body", gatewayMessage(nil))
	long := "{" + string(make([]byte, 300))
	assert.Len(t, gatewayMessage([]byte(long)), 200)
}

func TestNewClientDefaults(t *testing.T) {
	t.Parallel()
	ilog := &mocks.MockIntegrationLogger{}
	clock := &mocks.MockClock{FixedTime: fixedUTC}

	c := New(Config{BaseURL: "http://x/"}, nil, nil, ilog, clock)
	assert.Equal(t, defaultTimeout, c.http.Timeout, "nil client gets a 30s default")
	assert.Equal(t, "http://x", c.cfg.BaseURL, "trailing slash trimmed")

	noTimeout := &http.Client{}
	c = New(Config{}, nil, noTimeout, ilog, clock)
	assert.Equal(t, defaultTimeout, c.http.Timeout, "zero timeout replaced")
	assert.Equal(t, time.Duration(0), noTimeout.Timeout, "caller's client not mutated")

	custom := &http.Client{Timeout: 5 * time.Second}
	c = New(Config{}, nil, custom, ilog, clock)
	assert.Equal(t, 5*time.Second, c.http.Timeout, "explicit timeout kept")
}

// Live credential resolver (dynamic, admin-configurable, no-restart-needed)

func TestResolveCredsUsesResolverWhenSet(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = decodeReq(t, r)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"merchantOrderId": vecOrderID, "reference": "MOCKREF-1", "amount": "150000", "fee": "0",
			"statusCode": "00", "statusMessage": "SUCCESS",
		})
	}))
	t.Cleanup(srv.Close)
	// The static Config points at an unreachable host - if the resolved
	// BaseURL were merely resolved-and-discarded (rather than actually
	// threaded into the HTTP call), this request would fail to dial it.
	c := New(testConfig("http://static.invalid"), func(ctx context.Context) (Credentials, error) {
		return Credentials{MerchantCode: "LIVE001", APIKey: "live-key", BaseURL: srv.URL}, nil
	}, srv.Client(), &mocks.MockIntegrationLogger{}, &mocks.MockClock{FixedTime: fixedUTC})

	_, err := c.CheckTransaction(context.Background(), vecOrderID)
	require.NoError(t, err, "request must actually reach the resolver's BaseURL, not the static one")
	assert.Equal(t, "LIVE001", gotReq["merchantCode"])
	assert.Equal(t, signCheckTransaction("LIVE001", vecOrderID, "live-key"), gotReq["signature"])
	assert.NotEqual(t, vecSigCheck, gotReq["signature"], "must differ from the static-key vector")
}

func TestResolveCredsFallsBackToStaticConfigOnError(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = decodeReq(t, r)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"merchantOrderId": vecOrderID, "reference": "MOCKREF-1", "amount": "150000", "fee": "0",
			"statusCode": "00", "statusMessage": "SUCCESS",
		})
	}))
	t.Cleanup(srv.Close)
	c := New(testConfig(srv.URL), func(ctx context.Context) (Credentials, error) {
		return Credentials{}, errBoom
	}, srv.Client(), &mocks.MockIntegrationLogger{}, &mocks.MockClock{FixedTime: fixedUTC})

	_, err := c.CheckTransaction(context.Background(), vecOrderID)
	require.NoError(t, err)
	assert.Equal(t, vecMerchantCode, gotReq["merchantCode"])
	assert.Equal(t, vecSigCheck, gotReq["signature"], "resolver error falls back to the static/env config values")
}

func TestResolveCredsNilResolverUsesStaticConfig(t *testing.T) {
	c := &Client{cfg: Config{MerchantCode: "STATIC1", APIKey: "static-key", BaseURL: "http://static.example"}}
	assert.Equal(t,
		Credentials{MerchantCode: "STATIC1", APIKey: "static-key", BaseURL: "http://static.example"},
		c.resolveCreds(context.Background()))
}

func TestVerifyCallbackSignatureUsesResolver(t *testing.T) {
	c := New(testConfig("http://unused.invalid"), func(ctx context.Context) (Credentials, error) {
		return Credentials{MerchantCode: "LIVE001", APIKey: "live-key", BaseURL: "http://unused.invalid"}, nil
	}, nil, &mocks.MockIntegrationLogger{}, &mocks.MockClock{FixedTime: fixedUTC})

	p := ports.CallbackPayload{
		MerchantCode: "LIVE001", Amount: "150000", MerchantOrderID: vecOrderID,
		Signature: signCallback("LIVE001", "150000", vecOrderID, "live-key"),
	}
	assert.True(t, c.VerifyCallbackSignature(p))
	// The static-key vector signature must NOT verify once a resolver is set.
	p.Signature = vecSigCallback
	assert.False(t, c.VerifyCallbackSignature(p))
	// A callback claiming the STATIC/stale merchant code (rather than the
	// live-resolved one) must also be rejected - this is the regression test
	// for the bug where a rotated merchant code still 400'd every webhook
	// until restart.
	p2 := ports.CallbackPayload{
		MerchantCode: vecMerchantCode, Amount: "150000", MerchantOrderID: vecOrderID, Signature: vecSigCallback,
	}
	assert.False(t, c.VerifyCallbackSignature(p2))
}
