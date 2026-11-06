package payments_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/payments"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeMW satisfies payments.Middlewares: RequireAuth injects the configured
// identity; everything else passes through.
type fakeMW struct{ id httpx.AuthIdentity }

func pass(c fiber.Ctx) error { return c.Next() }

func (s fakeMW) RequireAuth() fiber.Handler {
	return func(c fiber.Ctx) error {
		httpx.SetIdentity(c, s.id)
		return c.Next()
	}
}
func (s fakeMW) RequireRole(...string) fiber.Handler                { return pass }
func (s fakeMW) RequirePermission(string) fiber.Handler             { return pass }
func (s fakeMW) RequireClient() fiber.Handler                       { return pass }
func (s fakeMW) RateLimit(string, int, time.Duration) fiber.Handler { return pass }

// fakeService mocks payments.PaymentsService with function fields.
type fakeService struct {
	GetPaymentMethodsFn        func(ctx context.Context, clientID, invoiceID int64) ([]ports.PaymentMethod, error)
	PayInvoiceFn               func(ctx context.Context, actorUserID, clientID, invoiceID int64, method string) (*payments.PaymentInstruction, error)
	HandleCallbackFn           func(ctx context.Context, p ports.CallbackPayload) error
	ResolveReturnFn            func(ctx context.Context, merchantOrderID string) string
	ResolveReturnInfoFn        func(ctx context.Context, merchantOrderID string, clientID int64) (*payments.ReturnInfo, error)
	ListTransactionsFn         func(ctx context.Context, p ports.ListParams) ([]domain.Transaction, int64, error)
	ListMyTransactionsFn       func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Transaction, int64, error)
	ConfirmManualTransactionFn func(ctx context.Context, actorUserID, transactionID int64) error
}

func (f *fakeService) GetPaymentMethods(ctx context.Context, clientID, invoiceID int64) ([]ports.PaymentMethod, error) {
	if f.GetPaymentMethodsFn != nil {
		return f.GetPaymentMethodsFn(ctx, clientID, invoiceID)
	}
	return nil, nil
}

func (f *fakeService) PayInvoice(ctx context.Context, actorUserID, clientID, invoiceID int64, method string) (*payments.PaymentInstruction, error) {
	if f.PayInvoiceFn != nil {
		return f.PayInvoiceFn(ctx, actorUserID, clientID, invoiceID, method)
	}
	return &payments.PaymentInstruction{Status: "pending"}, nil
}

func (f *fakeService) HandleCallback(ctx context.Context, p ports.CallbackPayload) error {
	if f.HandleCallbackFn != nil {
		return f.HandleCallbackFn(ctx, p)
	}
	return nil
}

func (f *fakeService) ResolveReturn(ctx context.Context, merchantOrderID string) string {
	if f.ResolveReturnFn != nil {
		return f.ResolveReturnFn(ctx, merchantOrderID)
	}
	return "http://front.test/billing"
}

func (f *fakeService) ResolveReturnInfo(ctx context.Context, merchantOrderID string, clientID int64) (*payments.ReturnInfo, error) {
	if f.ResolveReturnInfoFn != nil {
		return f.ResolveReturnInfoFn(ctx, merchantOrderID, clientID)
	}
	return &payments.ReturnInfo{}, nil
}

func (f *fakeService) ListTransactions(ctx context.Context, p ports.ListParams) ([]domain.Transaction, int64, error) {
	if f.ListTransactionsFn != nil {
		return f.ListTransactionsFn(ctx, p)
	}
	return nil, 0, nil
}

func (f *fakeService) ListMyTransactions(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Transaction, int64, error) {
	if f.ListMyTransactionsFn != nil {
		return f.ListMyTransactionsFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (f *fakeService) ConfirmManualTransaction(ctx context.Context, actorUserID, transactionID int64) error {
	if f.ConfirmManualTransactionFn != nil {
		return f.ConfirmManualTransactionFn(ctx, actorUserID, transactionID)
	}
	return nil
}

func newApp(svc payments.PaymentsService, id httpx.AuthIdentity) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: transporthttp.ErrorHandler(discard)})
	payments.NewHandler(svc, fakeMW{id: id}).RegisterRoutes(app.Group("/api/v1"))
	return app
}

type envelope struct {
	Data any `json:"data"`
	Meta *struct {
		Page    int   `json:"page"`
		PerPage int   `json:"per_page"`
		Total   int64 `json:"total"`
	} `json:"meta"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func readEnvelope(t *testing.T, r io.Reader) envelope {
	t.Helper()
	var env envelope
	require.NoError(t, json.NewDecoder(r).Decode(&env))
	return env
}

func clientIdentity() httpx.AuthIdentity {
	return httpx.AuthIdentity{UserID: 7, Role: "client", ClientID: 5}
}

// GET /payments/methods

func TestMethodsEndpoint(t *testing.T) {
	var gotClientID, gotInvoiceID int64
	svc := &fakeService{
		GetPaymentMethodsFn: func(ctx context.Context, clientID, invoiceID int64) ([]ports.PaymentMethod, error) {
			gotClientID, gotInvoiceID = clientID, invoiceID
			return []ports.PaymentMethod{{Code: "VA", Name: "Virtual Account", Fee: 4000}}, nil
		},
	}
	app := newApp(svc, clientIdentity())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/payments/methods?invoice_id=10", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(5), gotClientID, "client id from identity")
	assert.Equal(t, int64(10), gotInvoiceID)

	env := readEnvelope(t, resp.Body)
	require.Nil(t, env.Error)
	methods := env.Data.([]any)
	require.Len(t, methods, 1)
	assert.Equal(t, "VA", methods[0].(map[string]any)["code"])
}

func TestMethodsEndpointInvalidInvoiceID(t *testing.T) {
	app := newApp(&fakeService{}, clientIdentity())
	for _, q := range []string{"", "?invoice_id=abc", "?invoice_id=0"} {
		resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/payments/methods"+q, nil))
		require.NoError(t, err)
		assert.Equal(t, 422, resp.StatusCode, "query %q", q)
	}
}

// POST /invoices/:id/pay

func TestPayEndpoint(t *testing.T) {
	var gotActor, gotClient, gotInvoice int64
	var gotMethod string
	svc := &fakeService{
		PayInvoiceFn: func(ctx context.Context, actorUserID, clientID, invoiceID int64, method string) (*payments.PaymentInstruction, error) {
			gotActor, gotClient, gotInvoice, gotMethod = actorUserID, clientID, invoiceID, method
			return &payments.PaymentInstruction{
				Status: "pending", MerchantOrderID: "INV-202607-000001-01",
				PaymentURL: "https://pay.example/x", Amount: 150000,
			}, nil
		},
	}
	app := newApp(svc, clientIdentity())

	req := httptest.NewRequest("POST", "/api/v1/invoices/10/pay", strings.NewReader(`{"method":"VA"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(7), gotActor)
	assert.Equal(t, int64(5), gotClient)
	assert.Equal(t, int64(10), gotInvoice)
	assert.Equal(t, "VA", gotMethod)

	env := readEnvelope(t, resp.Body)
	require.Nil(t, env.Error)
	data := env.Data.(map[string]any)
	assert.Equal(t, "pending", data["status"])
	assert.Equal(t, "https://pay.example/x", data["payment_url"])
}

func TestPayEndpointInvalidID(t *testing.T) {
	app := newApp(&fakeService{}, clientIdentity())
	req := httptest.NewRequest("POST", "/api/v1/invoices/abc/pay", strings.NewReader(`{"method":"VA"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestPayEndpointMissingMethod(t *testing.T) {
	app := newApp(&fakeService{}, clientIdentity())
	req := httptest.NewRequest("POST", "/api/v1/invoices/10/pay", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
	env := readEnvelope(t, resp.Body)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestPayEndpointServiceConflict(t *testing.T) {
	svc := &fakeService{
		PayInvoiceFn: func(ctx context.Context, a, c, i int64, m string) (*payments.PaymentInstruction, error) {
			return nil, apperr.Conflict("invoice is not payable")
		},
	}
	app := newApp(svc, clientIdentity())
	req := httptest.NewRequest("POST", "/api/v1/invoices/10/pay", strings.NewReader(`{"method":"VA"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 409, resp.StatusCode)
}

// POST /webhooks/duitku

func callbackForm() url.Values {
	return url.Values{
		"merchantCode":    {"DEMO"},
		"amount":          {"150000"},
		"merchantOrderId": {"INV-202607-000001-01"},
		"resultCode":      {"00"},
		"reference":       {"DREF-1"},
		"signature":       {"862f4f3c58f4f6e676038789450a4ffd"},
	}
}

func postForm(t *testing.T, app *fiber.App, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/webhooks/duitku", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := app.Test(req)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	rec.Code = resp.StatusCode
	body, _ := io.ReadAll(resp.Body)
	_, _ = rec.Body.Write(body)
	return rec
}

func TestWebhookParsesFormAndAnswersOK(t *testing.T) {
	var got ports.CallbackPayload
	svc := &fakeService{
		HandleCallbackFn: func(ctx context.Context, p ports.CallbackPayload) error {
			got = p
			return nil
		},
	}
	app := newApp(svc, httpx.AuthIdentity{})

	rec := postForm(t, app, callbackForm())
	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "OK", rec.Body.String())
	assert.Equal(t, "DEMO", got.MerchantCode)
	assert.Equal(t, "150000", got.Amount)
	assert.Equal(t, "INV-202607-000001-01", got.MerchantOrderID)
	assert.Equal(t, "00", got.ResultCode)
	assert.Equal(t, "862f4f3c58f4f6e676038789450a4ffd", got.Signature)
}

func TestWebhookBadSignature400(t *testing.T) {
	svc := &fakeService{
		HandleCallbackFn: func(ctx context.Context, p ports.CallbackPayload) error {
			return payments.ErrBadSignature
		},
	}
	app := newApp(svc, httpx.AuthIdentity{})
	rec := postForm(t, app, callbackForm())
	assert.Equal(t, 400, rec.Code)
	assert.NotEqual(t, "OK", rec.Body.String())
}

func TestWebhookUnknownOrder404(t *testing.T) {
	svc := &fakeService{
		HandleCallbackFn: func(ctx context.Context, p ports.CallbackPayload) error {
			return apperr.NotFound("transaction")
		},
	}
	app := newApp(svc, httpx.AuthIdentity{})
	rec := postForm(t, app, callbackForm())
	assert.Equal(t, 404, rec.Code)
}

func TestWebhookMissingMerchantOrderID400(t *testing.T) {
	app := newApp(&fakeService{}, httpx.AuthIdentity{})
	form := callbackForm()
	form.Del("merchantOrderId")
	rec := postForm(t, app, form)
	assert.Equal(t, 400, rec.Code)
}

func TestWebhookOversizedBody413(t *testing.T) {
	app := newApp(&fakeService{
		HandleCallbackFn: func(ctx context.Context, p ports.CallbackPayload) error {
			t.Fatal("HandleCallback must not be reached for an oversized body")
			return nil
		},
	}, httpx.AuthIdentity{})

	form := callbackForm()
	// Pad well past the 16KB cap with an oversized (otherwise-irrelevant) field.
	form.Set("padding", strings.Repeat("x", 17*1024))
	rec := postForm(t, app, form)
	assert.Equal(t, 413, rec.Code)
}

// rateLimitRecordingMW is fakeMW plus a record of every RateLimit(prefix,
// limit, window) call made while registering routes, proving the webhook
// route is actually wired with a limiter (as opposed to merely having one
// available on the interface).
type rateLimitRecordingMW struct {
	fakeMW
	calls []struct {
		prefix string
		limit  int
		window time.Duration
	}
}

func (r *rateLimitRecordingMW) RateLimit(prefix string, limit int, window time.Duration) fiber.Handler {
	r.calls = append(r.calls, struct {
		prefix string
		limit  int
		window time.Duration
	}{prefix, limit, window})
	return pass
}

func TestWebhookIsRateLimited(t *testing.T) {
	mw := &rateLimitRecordingMW{}
	app := fiber.New()
	payments.NewHandler(&fakeService{}, mw).RegisterRoutes(app.Group("/api/v1"))

	require.Len(t, mw.calls, 2, "webhooks:duitku and payments:return each register one RateLimit call")
	var webhookCall *struct {
		prefix string
		limit  int
		window time.Duration
	}
	for i := range mw.calls {
		if mw.calls[i].prefix == "webhooks:duitku" {
			webhookCall = &mw.calls[i]
		}
	}
	require.NotNil(t, webhookCall, "POST /webhooks/duitku must be rate-limited")
	assert.Positive(t, webhookCall.limit)
	assert.Positive(t, webhookCall.window)

	rec := postForm(t, app, callbackForm())
	assert.Equal(t, 200, rec.Code, "the (passthrough) rate limiter must not block a normal request")
}

// GET /payments/return

func TestReturnResolvesJSON(t *testing.T) {
	var gotClientID int64
	svc := &fakeService{
		ResolveReturnInfoFn: func(ctx context.Context, merchantOrderID string, clientID int64) (*payments.ReturnInfo, error) {
			assert.Equal(t, "INV-202607-000001-01", merchantOrderID)
			gotClientID = clientID
			return &payments.ReturnInfo{InvoiceID: 10, Status: "paid"}, nil
		},
	}
	app := newApp(svc, clientIdentity())

	resp, err := app.Test(httptest.NewRequest("GET",
		"/api/v1/payments/return?merchantOrderId=INV-202607-000001-01", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	var body struct {
		Data payments.ReturnInfo `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, int64(10), body.Data.InvoiceID)
	assert.Equal(t, "paid", body.Data.Status)
	// Regression: the caller's own clientID must reach the service so it can
	// enforce ownership - the endpoint used to be reachable with zero auth
	// context at all, letting anyone enumerate any invoice's status.
	assert.Equal(t, int64(5), gotClientID)
}

// TestReturnEndpointRequiresAuth is a regression test for the leak: it
// asserts RequireAuth is wired directly in front of GET /payments/return (the
// only thing that actually rejects an unauthenticated caller with 401 - the
// handler/service layer alone treats a missing identity as ClientID 0, i.e.
// "unrestricted", so auth must be enforced at the route).
func TestReturnEndpointRequiresAuth(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: transporthttp.ErrorHandler(discard)})
	rec := &authRecordingMW{}
	payments.NewHandler(&fakeService{}, rec).RegisterRoutes(app.Group("/api/v1"))

	resp, err := app.Test(httptest.NewRequest("GET",
		"/api/v1/payments/return?merchantOrderId=INV-202607-000001-01", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.True(t, rec.authCalled, "RequireAuth must be wired in front of GET /payments/return")
}

// authRecordingMW is fakeMW plus a flag recording whether RequireAuth() was
// installed on the request path (as opposed to merely being available on the
// interface).
type authRecordingMW struct {
	fakeMW
	authCalled bool
}

func (r *authRecordingMW) RequireAuth() fiber.Handler {
	inner := r.fakeMW.RequireAuth()
	return func(c fiber.Ctx) error {
		r.authCalled = true
		return inner(c)
	}
}

// GET /transactions (client)

func TestListMyTransactions(t *testing.T) {
	var gotClientID int64
	svc := &fakeService{
		ListMyTransactionsFn: func(_ context.Context, clientID int64, p ports.ListParams) ([]domain.Transaction, int64, error) {
			gotClientID = clientID
			return []domain.Transaction{{ID: 1, InvoiceID: 10, Gateway: domain.GatewayDuitku}}, 1, nil
		},
	}
	app := newApp(svc, clientIdentity())

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/transactions?page=1&per_page=10", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(5), gotClientID)
}

// GET /admin/transactions

func TestAdminListTransactions(t *testing.T) {
	var gotParams ports.ListParams
	svc := &fakeService{
		ListTransactionsFn: func(ctx context.Context, p ports.ListParams) ([]domain.Transaction, int64, error) {
			gotParams = p
			return []domain.Transaction{{ID: 1, InvoiceID: 10, Gateway: domain.GatewayDuitku}}, 1, nil
		},
	}
	app := newApp(svc, httpx.AuthIdentity{UserID: 1, Role: "admin"})

	resp, err := app.Test(httptest.NewRequest("GET",
		"/api/v1/admin/transactions?page=2&per_page=10&status=pending&search=INV&gateway=manual", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 2, gotParams.Page)
	assert.Equal(t, 10, gotParams.PerPage)
	assert.Equal(t, "pending", gotParams.Status)
	assert.Equal(t, "INV", gotParams.Search)
	assert.Equal(t, "manual", gotParams.Gateway)

	env := readEnvelope(t, resp.Body)
	require.Nil(t, env.Error)
	require.NotNil(t, env.Meta)
	assert.Equal(t, int64(1), env.Meta.Total)
	assert.Equal(t, 2, env.Meta.Page)
}

func TestAdminListTransactionsInvoiceAndDateFilters(t *testing.T) {
	var gotParams ports.ListParams
	called := false
	svc := &fakeService{
		ListTransactionsFn: func(ctx context.Context, p ports.ListParams) ([]domain.Transaction, int64, error) {
			called, gotParams = true, p
			return nil, 0, nil
		},
	}
	app := newApp(svc, httpx.AuthIdentity{UserID: 1, Role: "admin"})

	resp, err := app.Test(httptest.NewRequest("GET",
		"/api/v1/admin/transactions?invoice_id=33&date_from=2026-03-01&date_to=2026-03-31", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(33), gotParams.InvoiceID)
	assert.Equal(t, "2026-03-01", gotParams.From.Format("2006-01-02"))
	assert.Equal(t, "2026-03-31", gotParams.To.Format("2006-01-02"))

	for _, q := range []string{"invoice_id=zero", "date_from=1-3-2026", "date_to=later"} {
		called = false
		resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/transactions?"+q, nil))
		require.NoError(t, err)
		assert.Equal(t, 422, resp.StatusCode, q)
		assert.False(t, called, q)
	}
}

// Error branches

func TestMethodsEndpointServiceError(t *testing.T) {
	svc := &fakeService{
		GetPaymentMethodsFn: func(ctx context.Context, clientID, invoiceID int64) ([]ports.PaymentMethod, error) {
			return nil, apperr.NotFound("invoice")
		},
	}
	app := newApp(svc, clientIdentity())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/payments/methods?invoice_id=10", nil))
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)
}

func TestPayEndpointMalformedBody(t *testing.T) {
	app := newApp(&fakeService{}, clientIdentity())
	req := httptest.NewRequest("POST", "/api/v1/invoices/10/pay", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestAdminListTransactionsServiceError(t *testing.T) {
	svc := &fakeService{
		ListTransactionsFn: func(ctx context.Context, p ports.ListParams) ([]domain.Transaction, int64, error) {
			return nil, 0, apperr.Internal(errIntegration)
		},
	}
	app := newApp(svc, httpx.AuthIdentity{UserID: 1, Role: "admin"})
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/transactions", nil))
	require.NoError(t, err)
	assert.Equal(t, 500, resp.StatusCode)
}

var errIntegration = errors.New("db down")

// POST /admin/transactions/:id/confirm

func TestConfirmManualTransactionEndpoint(t *testing.T) {
	var gotActor, gotTxID int64
	svc := &fakeService{
		ConfirmManualTransactionFn: func(ctx context.Context, actorUserID, transactionID int64) error {
			gotActor, gotTxID = actorUserID, transactionID
			return nil
		},
	}
	app := newApp(svc, httpx.AuthIdentity{UserID: 9, Role: "admin"})

	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/transactions/42/confirm", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(9), gotActor)
	assert.Equal(t, int64(42), gotTxID)
}

func TestConfirmManualTransactionInvalidID(t *testing.T) {
	app := newApp(&fakeService{}, httpx.AuthIdentity{UserID: 9, Role: "admin"})
	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/transactions/abc/confirm", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestConfirmManualTransactionServiceError(t *testing.T) {
	svc := &fakeService{
		ConfirmManualTransactionFn: func(ctx context.Context, actorUserID, transactionID int64) error {
			return apperr.Conflict("transaction is not a pending manual bank-transfer payment")
		},
	}
	app := newApp(svc, httpx.AuthIdentity{UserID: 9, Role: "admin"})
	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/transactions/42/confirm", nil))
	require.NoError(t, err)
	assert.Equal(t, 409, resp.StatusCode)
}
