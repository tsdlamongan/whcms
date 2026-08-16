// Package duitku implements ports.PaymentGateway against the Duitku V2 API
// (mirrored by the local mockserver).
//
// Endpoints:
//
//	POST /webapi/api/merchant/paymentmethod/getpaymentmethod  -> GetPaymentMethods
//	POST /webapi/api/merchant/v2/inquiry                      -> CreateTransaction
//	POST /webapi/api/merchant/transactionStatus               -> CheckTransaction
//
// Signature formulas (PRD §8.1):
//
//	getpaymentmethod:  SHA256(merchantCode + amount + datetime + apiKey), datetime "2006-01-02 15:04:05" (WIB)
//	inquiry:           MD5(merchantCode + merchantOrderId + paymentAmount + apiKey)
//	callback (verify): MD5(merchantCode + amount + merchantOrderId + apiKey)
//	transactionStatus: MD5(merchantCode + merchantOrderId + apiKey)
//
// Amounts are whole-IDR integers; Duitku renders response amounts/fees as JSON
// strings, so decoding accepts both numbers and numeric strings. Every HTTP
// attempt is recorded through ports.IntegrationLogger with signatures and keys
// redacted. Read-only calls (getpaymentmethod, transactionStatus) retry up to
// 2 extra times on 5xx/network errors; inquiry is never auto-retried. A lite
// circuit breaker fast-fails for 60s after 3 consecutive transport/5xx
// failures.
package duitku

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

const providerName = "duitku"

// Duitku V2 endpoint paths.
const (
	pathGetPaymentMethod  = "/webapi/api/merchant/paymentmethod/getpaymentmethod"
	pathInquiry           = "/webapi/api/merchant/v2/inquiry"
	pathTransactionStatus = "/webapi/api/merchant/transactionStatus"
)

const (
	defaultTimeout   = 30 * time.Second
	maxRetries       = 2 // extra attempts for read-only calls on 5xx/network errors
	retryBackoff     = 50 * time.Millisecond
	maxResponseBytes = 1 << 20

	// datetimeLayout is the getpaymentmethod datetime format.
	datetimeLayout = "2006-01-02 15:04:05"

	responseCodeOK = "00"
)

// wib is Duitku's server timezone (UTC+7); the signed datetime uses it.
var wib = time.FixedZone("WIB", 7*60*60)

// Config carries merchant credentials and URLs; wiring fills it from env
// (DUITKU_MERCHANT_CODE, DUITKU_API_KEY, DUITKU_BASE_URL/DUITKU_ENV, and the
// app callback/return URLs).
type Config struct {
	MerchantCode string
	APIKey       string
	BaseURL      string // e.g. https://sandbox.duitku.com, https://passport.duitku.com, or mockserver override
	CallbackURL  string // default callbackUrl when CreateTxRequest has none
	ReturnURL    string // default returnUrl when CreateTxRequest has none
}

// Credentials is the live merchant code, api_key and base URL resolved
// fresh per call - see New's resolve parameter.
type Credentials struct {
	MerchantCode string
	APIKey       string
	BaseURL      string
}

// Client is the Duitku V2 payment gateway adapter.
type Client struct {
	cfg     Config
	resolve func(ctx context.Context) (Credentials, error)
	http    *http.Client
	ilog    ports.IntegrationLogger
	clock   ports.Clock
	brk     *breaker
}

// Compile-time interface assertion.
var _ ports.PaymentGateway = (*Client)(nil)

// New builds a Client. A nil httpClient gets a default 30s-timeout client; a
// caller-provided client without a timeout is shallow-copied and given one.
//
// resolve, when non-nil, is consulted on every call to get the *effective*
// merchant code, API key and base URL for that call - this is what lets
// admin-configured values (merchant code/mode/base-url settings, API key
// encrypted at rest) take effect without restarting the process: no
// caching, just a fresh read each call. If resolve is nil or errors, the
// static Config values (env-var-sourced) are used instead and the error is
// logged as a warning, never as a hard failure.
func New(cfg Config, resolve func(ctx context.Context) (Credentials, error), httpClient *http.Client, ilog ports.IntegrationLogger, clock ports.Clock) *Client {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	hc := &http.Client{Timeout: defaultTimeout}
	if httpClient != nil {
		cp := *httpClient
		if cp.Timeout == 0 {
			cp.Timeout = defaultTimeout
		}
		hc = &cp
	}
	return &Client{cfg: cfg, resolve: resolve, http: hc, ilog: ilog, clock: clock, brk: newBreaker()}
}

// resolveCreds returns the effective credentials for one call: resolve's
// result when it succeeds, otherwise the static (env-sourced) Config values
// as a whole (per-field env-fallback, e.g. "use the env base URL only if the
// merchant_code setting itself couldn't be read", is the resolve closure's
// responsibility, not this method's).
func (c *Client) resolveCreds(ctx context.Context) Credentials {
	fallback := Credentials{MerchantCode: c.cfg.MerchantCode, APIKey: c.cfg.APIKey, BaseURL: c.cfg.BaseURL}
	if c.resolve == nil {
		return fallback
	}
	creds, err := c.resolve(ctx)
	if err != nil {
		slog.Default().WarnContext(ctx, "duitku: resolve live credentials failed, using static fallback", "error", err)
		return fallback
	}
	creds.BaseURL = strings.TrimRight(creds.BaseURL, "/")
	return creds
}

// Wire types

// flexInt64 decodes Duitku amount/fee fields, which arrive as JSON numbers or
// numeric strings ("150000", "150000.00").
type flexInt64 struct {
	Value int64
}

// UnmarshalJSON implements json.Unmarshaler.
func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		f.Value = 0
		return nil
	}
	s = strings.Trim(s, `"`)
	if s == "" {
		f.Value = 0
		return nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		f.Value = i
		return nil
	}
	fl, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("invalid amount %q", s)
	}
	f.Value = int64(fl)
	return nil
}

type getMethodsResponse struct {
	PaymentFee []struct {
		PaymentMethod string    `json:"paymentMethod"`
		PaymentName   string    `json:"paymentName"`
		PaymentImage  string    `json:"paymentImage"`
		TotalFee      flexInt64 `json:"totalFee"`
	} `json:"paymentFee"`
	ResponseCode    string `json:"responseCode"`
	ResponseMessage string `json:"responseMessage"`
}

type inquiryResponse struct {
	MerchantCode  string    `json:"merchantCode"`
	Reference     string    `json:"reference"`
	PaymentURL    string    `json:"paymentUrl"`
	VANumber      string    `json:"vaNumber"`
	QRString      string    `json:"qrString"`
	Amount        flexInt64 `json:"amount"`
	StatusCode    string    `json:"statusCode"`
	StatusMessage string    `json:"statusMessage"`
}

type statusResponse struct {
	MerchantOrderID string    `json:"merchantOrderId"`
	Reference       string    `json:"reference"`
	Amount          flexInt64 `json:"amount"`
	Fee             flexInt64 `json:"fee"`
	StatusCode      string    `json:"statusCode"`
	StatusMessage   string    `json:"statusMessage"`
}

// ports.PaymentGateway

// GetPaymentMethods lists the payment channels available for the given amount
// (whole IDR), including per-channel fees.
func (c *Client) GetPaymentMethods(ctx context.Context, amount int64) ([]ports.PaymentMethod, error) {
	if amount <= 0 {
		return nil, apperr.Validation("amount must be positive",
			apperr.FieldError{Field: "amount", Message: "must be positive"})
	}
	creds := c.resolveCreds(ctx)
	amountStr := strconv.FormatInt(amount, 10)
	datetime := c.clock.Now().In(wib).Format(datetimeLayout)
	payload := map[string]any{
		"merchantcode": creds.MerchantCode,
		"amount":       amount,
		"datetime":     datetime,
		"signature":    signGetPaymentMethod(creds.MerchantCode, amountStr, datetime, creds.APIKey),
	}

	res, err := c.do(ctx, creds.BaseURL, pathGetPaymentMethod, payload, true)
	if err != nil {
		return nil, err
	}
	if err := checkHTTPStatus(res); err != nil {
		return nil, err
	}
	var out getMethodsResponse
	if err := json.Unmarshal(res.body, &out); err != nil {
		return nil, apperr.External(providerName, fmt.Errorf("decode getpaymentmethod response: %w", err))
	}
	if out.ResponseCode != responseCodeOK {
		return nil, apperr.Newf(apperr.CodeExternal, "%s error: %s", providerName, out.ResponseMessage).
			WithCause(fmt.Errorf("getpaymentmethod response code %s: %s", out.ResponseCode, out.ResponseMessage))
	}
	methods := make([]ports.PaymentMethod, 0, len(out.PaymentFee))
	for _, m := range out.PaymentFee {
		// Duitku rejects inquiries below a per-channel floor ("Minimum
		// Payment 10000 IDR") but getpaymentmethod still lists the channel -
		// drop those channels here so a small invoice (e.g. a prorated
		// upgrade diff) only offers methods that can actually complete. QRIS
		// is the one family that accepts micro-payments.
		if amount < minNonQRISAmount && !isQRISMethod(m.PaymentMethod) {
			continue
		}
		methods = append(methods, ports.PaymentMethod{
			Code:  m.PaymentMethod,
			Name:  m.PaymentName,
			Image: m.PaymentImage,
			Fee:   m.TotalFee.Value,
		})
	}
	return methods, nil
}

// minNonQRISAmount is Duitku's per-transaction floor for every non-QRIS
// channel - the upstream inquiry rejects smaller amounts with HTTP 400
// "Minimum Payment 10000 IDR" (observed against the real sandbox).
const minNonQRISAmount = 10_000

// isQRISMethod reports whether a Duitku channel code is in the QRIS family
// (PRD §8.1: SP/LQ/NQ) - the only family accepting payments below
// minNonQRISAmount.
func isQRISMethod(code string) bool {
	switch code {
	case "SP", "LQ", "NQ":
		return true
	}
	return false
}

// CreateTransaction opens a payment via the V2 inquiry endpoint. Empty
// CallbackURL/ReturnURL fall back to the configured defaults. Never
// auto-retried (it creates a transaction upstream).
func (c *Client) CreateTransaction(ctx context.Context, req ports.CreateTxRequest) (*ports.CreateTxResult, error) {
	var details []apperr.FieldError
	if req.MerchantOrderID == "" {
		details = append(details, apperr.FieldError{Field: "merchant_order_id", Message: "required"})
	}
	if req.Amount <= 0 {
		details = append(details, apperr.FieldError{Field: "amount", Message: "must be positive"})
	}
	if req.Method == "" {
		details = append(details, apperr.FieldError{Field: "method", Message: "required"})
	}
	if len(details) > 0 {
		return nil, apperr.Validation("invalid transaction request", details...)
	}

	creds := c.resolveCreds(ctx)
	callbackURL := req.CallbackURL
	if callbackURL == "" {
		callbackURL = c.cfg.CallbackURL
	}
	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = c.cfg.ReturnURL
	}
	amountStr := strconv.FormatInt(req.Amount, 10)
	payload := map[string]any{
		"merchantCode":    creds.MerchantCode,
		"paymentAmount":   req.Amount,
		"paymentMethod":   req.Method,
		"merchantOrderId": req.MerchantOrderID,
		"productDetails":  req.ProductDetails,
		"additionalParam": "",
		"email":           req.Email,
		"phoneNumber":     req.Phone,
		"customerVaName":  req.CustomerName,
		"callbackUrl":     callbackURL,
		"returnUrl":       returnURL,
		"signature":       signInquiry(creds.MerchantCode, req.MerchantOrderID, amountStr, creds.APIKey),
	}
	if req.ExpiryMinutes > 0 {
		payload["expiryPeriod"] = req.ExpiryMinutes
	}

	res, err := c.do(ctx, creds.BaseURL, pathInquiry, payload, false)
	if err != nil {
		return nil, err
	}
	if err := checkHTTPStatus(res); err != nil {
		return nil, err
	}
	var out inquiryResponse
	if err := json.Unmarshal(res.body, &out); err != nil {
		return nil, apperr.External(providerName, fmt.Errorf("decode inquiry response: %w", err))
	}
	if out.StatusCode != responseCodeOK {
		return nil, apperr.Newf(apperr.CodeExternal, "%s error: %s", providerName, out.StatusMessage).
			WithCause(fmt.Errorf("inquiry status code %s: %s", out.StatusCode, out.StatusMessage))
	}
	return &ports.CreateTxResult{
		Reference:  out.Reference,
		PaymentURL: out.PaymentURL,
		VANumber:   out.VANumber,
		QRString:   out.QRString,
		Amount:     out.Amount.Value,
	}, nil
}

// CheckTransaction queries the latest status of a merchant order. An unknown
// merchantOrderId (HTTP 404 upstream) maps to a NOT_FOUND apperr. StatusCode
// passes through: "00" paid, "01" pending, "02" cancelled/expired.
func (c *Client) CheckTransaction(ctx context.Context, merchantOrderID string) (*ports.TxStatus, error) {
	if merchantOrderID == "" {
		return nil, apperr.Validation("merchant order id is required",
			apperr.FieldError{Field: "merchant_order_id", Message: "required"})
	}
	creds := c.resolveCreds(ctx)
	payload := map[string]any{
		"merchantCode":    creds.MerchantCode,
		"merchantOrderId": merchantOrderID,
		"signature":       signCheckTransaction(creds.MerchantCode, merchantOrderID, creds.APIKey),
	}

	res, err := c.do(ctx, creds.BaseURL, pathTransactionStatus, payload, true)
	if err != nil {
		return nil, err
	}
	if res.status == http.StatusNotFound {
		return nil, apperr.NotFound("transaction")
	}
	if err := checkHTTPStatus(res); err != nil {
		return nil, err
	}
	var out statusResponse
	if err := json.Unmarshal(res.body, &out); err != nil {
		return nil, apperr.External(providerName, fmt.Errorf("decode transactionStatus response: %w", err))
	}
	return &ports.TxStatus{
		Reference:     out.Reference,
		Amount:        out.Amount.Value,
		Fee:           out.Fee.Value,
		StatusCode:    out.StatusCode,
		StatusMessage: out.StatusMessage,
	}, nil
}

// VerifyCallbackSignature checks the webhook payload signature:
// MD5(merchantCode + amount + merchantOrderId + apiKey), hex compared
// case-insensitively. The payload merchantCode must match the live-resolved
// one (not just the static config) so a merchant code rotated through the
// admin UI takes effect for callbacks without a restart, same as outbound
// calls. No HTTP call, no logging - but the interface (ports.PaymentGateway)
// takes no context, so the live-credential resolver (if any) runs with
// context.Background() rather than a caller-scoped context.
func (c *Client) VerifyCallbackSignature(p ports.CallbackPayload) bool {
	creds := c.resolveCreds(context.Background())
	if p.MerchantCode != creds.MerchantCode {
		return false
	}
	expected := signCallback(creds.MerchantCode, p.Amount, p.MerchantOrderID, creds.APIKey)
	got := strings.ToLower(strings.TrimSpace(p.Signature))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(got)) == 1
}

// HTTP plumbing

type httpResult struct {
	status int
	body   []byte
}

// do POSTs payload as JSON to baseURL+path (baseURL is resolved once by the
// caller so every attempt/retry within this call targets the same host).
// When retriable, 5xx/network failures are retried up to maxRetries extra
// times with linear backoff. Transport-level failures (network error or 5xx
// after all attempts) trip the circuit breaker and return an EXTERNAL
// apperr; 2xx-4xx responses are returned for the caller to map.
func (c *Client) do(ctx context.Context, baseURL, path string, payload map[string]any, retriable bool) (*httpResult, error) {
	if !c.brk.allow(c.clock.Now()) {
		return nil, apperr.External(providerName,
			errors.New("circuit open: too many consecutive failures, fast-failing"))
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, apperr.Internal(fmt.Errorf("marshal %s request: %w", path, err))
	}

	attempts := 1
	if retriable {
		attempts += maxRetries
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				c.brk.failure(c.clock.Now())
				return nil, apperr.External(providerName, ctx.Err())
			case <-time.After(retryBackoff * time.Duration(attempt)):
			}
		}
		res, err := c.doOnce(ctx, baseURL, path, body, payload)
		if err != nil {
			lastErr = err
			continue
		}
		if res.status >= http.StatusInternalServerError {
			lastErr = fmt.Errorf("%s returned HTTP %d", path, res.status)
			continue
		}
		c.brk.success()
		return res, nil
	}
	c.brk.failure(c.clock.Now())
	return nil, apperr.External(providerName, lastErr)
}

// doOnce performs one HTTP attempt and records it via the integration logger
// (request and response redacted).
func (c *Client) doOnce(ctx context.Context, baseURL, path string, body []byte, payload map[string]any) (*httpResult, error) {
	call := ports.IntegrationCall{
		Provider: providerName,
		Endpoint: path,
		Method:   http.MethodPost,
		Request:  redactValue(payload),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		call.Error = err.Error()
		c.ilog.Log(ctx, call)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	start := c.clock.Now()
	resp, err := c.http.Do(req)
	call.LatencyMS = c.clock.Now().Sub(start).Milliseconds()
	if err != nil {
		call.Error = err.Error()
		c.ilog.Log(ctx, call)
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	call.StatusCode = resp.StatusCode
	call.Response = redactBody(respBody)
	if readErr != nil {
		call.Error = readErr.Error()
		c.ilog.Log(ctx, call)
		return nil, readErr
	}
	call.Success = resp.StatusCode >= 200 && resp.StatusCode < 400
	c.ilog.Log(ctx, call)
	return &httpResult{status: resp.StatusCode, body: respBody}, nil
}

// checkHTTPStatus maps a non-2xx gateway response to an EXTERNAL apperr with
// the upstream message attached.
func checkHTTPStatus(res *httpResult) error {
	if res.status >= 200 && res.status < 300 {
		return nil
	}
	// Surface Duitku's own message ("Minimum Payment 10000 IDR", ...) in the
	// client-visible error instead of a bare "duitku error" - the message is
	// the part the payer can act on.
	msg := gatewayMessage(res.body)
	return apperr.Newf(apperr.CodeExternal, "%s error: %s", providerName, msg).
		WithCause(fmt.Errorf("HTTP %d: %s", res.status, msg))
}

// gatewayMessage extracts a human-readable message from a Duitku error body.
func gatewayMessage(body []byte) string {
	var e struct {
		StatusMessage   string `json:"statusMessage"`
		ResponseMessage string `json:"responseMessage"`
		Message         string `json:"Message"`
	}
	if err := json.Unmarshal(body, &e); err == nil {
		switch {
		case e.StatusMessage != "":
			return e.StatusMessage
		case e.ResponseMessage != "":
			return e.ResponseMessage
		case e.Message != "":
			return e.Message
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		return "no response body"
	}
	return s
}
