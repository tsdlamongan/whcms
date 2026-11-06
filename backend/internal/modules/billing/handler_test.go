package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMW satisfies Middlewares, injecting a fixed identity.
type fakeMW struct{ id httpx.AuthIdentity }

func (s fakeMW) RequireAuth() fiber.Handler {
	return func(c fiber.Ctx) error {
		httpx.SetIdentity(c, s.id)
		return c.Next()
	}
}
func (s fakeMW) RequireRole(...string) fiber.Handler {
	return func(c fiber.Ctx) error { return c.Next() }
}
func (s fakeMW) RequirePermission(string) fiber.Handler {
	return func(c fiber.Ctx) error { return c.Next() }
}
func (s fakeMW) RequireClient() fiber.Handler {
	return func(c fiber.Ctx) error { return c.Next() }
}

// fakeSvc is a function-field BillingService.
type fakeSvc struct {
	ListClientInvoicesFn              func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error)
	GetInvoiceFn                      func(ctx context.Context, clientID, invoiceID int64) (*InvoiceDetail, error)
	DownloadPDFFn                     func(ctx context.Context, clientID, invoiceID int64) (io.ReadCloser, string, error)
	AdminListInvoicesFn               func(ctx context.Context, p ports.ListParams) ([]domain.Invoice, int64, error)
	CreateManualInvoiceFn             func(ctx context.Context, actorUserID int64, in ManualInvoiceInput) (*domain.Invoice, error)
	GenerateSelectedRenewalInvoicesFn func(ctx context.Context, actorUserID int64, in GenerateSelectedInvoicesInput) (*GenerateSelectedInvoicesResult, error)
	AddManualPaymentFn                func(ctx context.Context, actorUserID, invoiceID int64, in ManualPaymentInput) (*domain.Invoice, error)
	CancelInvoiceFn                   func(ctx context.Context, actorUserID, invoiceID int64) (*domain.Invoice, error)
	RefundInvoiceFn                   func(ctx context.Context, actorUserID, invoiceID int64, in RefundInput) (*domain.Invoice, error)
	UpdateInvoiceFn                   func(ctx context.Context, actorUserID, invoiceID int64, in UpdateInvoiceInput) (*domain.Invoice, error)
}

func (f *fakeSvc) ListClientInvoices(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error) {
	return f.ListClientInvoicesFn(ctx, clientID, p)
}
func (f *fakeSvc) GetInvoice(ctx context.Context, clientID, invoiceID int64) (*InvoiceDetail, error) {
	return f.GetInvoiceFn(ctx, clientID, invoiceID)
}
func (f *fakeSvc) DownloadPDF(ctx context.Context, clientID, invoiceID int64) (io.ReadCloser, string, error) {
	return f.DownloadPDFFn(ctx, clientID, invoiceID)
}
func (f *fakeSvc) AdminListInvoices(ctx context.Context, p ports.ListParams) ([]domain.Invoice, int64, error) {
	return f.AdminListInvoicesFn(ctx, p)
}
func (f *fakeSvc) CreateManualInvoice(ctx context.Context, actorUserID int64, in ManualInvoiceInput) (*domain.Invoice, error) {
	return f.CreateManualInvoiceFn(ctx, actorUserID, in)
}
func (f *fakeSvc) GenerateSelectedRenewalInvoices(ctx context.Context, actorUserID int64, in GenerateSelectedInvoicesInput) (*GenerateSelectedInvoicesResult, error) {
	return f.GenerateSelectedRenewalInvoicesFn(ctx, actorUserID, in)
}
func (f *fakeSvc) AddManualPayment(ctx context.Context, actorUserID, invoiceID int64, in ManualPaymentInput) (*domain.Invoice, error) {
	return f.AddManualPaymentFn(ctx, actorUserID, invoiceID, in)
}
func (f *fakeSvc) CancelInvoice(ctx context.Context, actorUserID, invoiceID int64) (*domain.Invoice, error) {
	return f.CancelInvoiceFn(ctx, actorUserID, invoiceID)
}
func (f *fakeSvc) RefundInvoice(ctx context.Context, actorUserID, invoiceID int64, in RefundInput) (*domain.Invoice, error) {
	return f.RefundInvoiceFn(ctx, actorUserID, invoiceID, in)
}
func (f *fakeSvc) UpdateInvoice(ctx context.Context, actorUserID, invoiceID int64, in UpdateInvoiceInput) (*domain.Invoice, error) {
	return f.UpdateInvoiceFn(ctx, actorUserID, invoiceID, in)
}

func newApp(svc BillingService, id httpx.AuthIdentity) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: transporthttp.ErrorHandler(slog.New(slog.DiscardHandler)),
	})
	h := NewHandler(svc, fakeMW{id: id})
	h.RegisterRoutes(app.Group("/api/v1"))
	return app
}

func decodeEnvelope(t *testing.T, body io.Reader) httpx.Envelope {
	t.Helper()
	var env httpx.Envelope
	require.NoError(t, json.NewDecoder(body).Decode(&env))
	return env
}

func clientIdentity() httpx.AuthIdentity {
	return httpx.AuthIdentity{UserID: 103, Role: "client", ClientID: 3}
}

func adminIdentity() httpx.AuthIdentity {
	return httpx.AuthIdentity{UserID: 42, Role: "admin"}
}

func TestHandlerListInvoices(t *testing.T) {
	svc := &fakeSvc{
		ListClientInvoicesFn: func(_ context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error) {
			assert.Equal(t, int64(3), clientID)
			assert.Equal(t, "unpaid", p.Status)
			assert.Equal(t, 2, p.Page)
			return []domain.Invoice{{ID: 1, InvoiceNumber: "INV-1"}}, 26, nil
		},
	}
	app := newApp(svc, clientIdentity())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/invoices?status=unpaid&page=2", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := decodeEnvelope(t, resp.Body)
	require.NotNil(t, env.Meta)
	assert.Equal(t, int64(26), env.Meta.Total)
	assert.Equal(t, 2, env.Meta.Page)
}

func TestHandlerGetInvoice(t *testing.T) {
	svc := &fakeSvc{
		GetInvoiceFn: func(_ context.Context, clientID, invoiceID int64) (*InvoiceDetail, error) {
			assert.Equal(t, int64(3), clientID)
			assert.Equal(t, int64(9), invoiceID)
			return &InvoiceDetail{Invoice: domain.Invoice{ID: 9}}, nil
		},
	}
	app := newApp(svc, clientIdentity())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/invoices/9", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandlerGetInvoiceBadID(t *testing.T) {
	app := newApp(&fakeSvc{}, clientIdentity())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/invoices/abc", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlerGetInvoiceNotFound(t *testing.T) {
	svc := &fakeSvc{
		GetInvoiceFn: func(context.Context, int64, int64) (*InvoiceDetail, error) {
			return nil, apperr.NotFound("invoice")
		},
	}
	app := newApp(svc, clientIdentity())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/invoices/9", nil))
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)
	env := decodeEnvelope(t, resp.Body)
	require.NotNil(t, env.Error)
	assert.Equal(t, "NOT_FOUND", env.Error.Code)
}

func TestHandlerInvoicePDF(t *testing.T) {
	svc := &fakeSvc{
		DownloadPDFFn: func(_ context.Context, clientID, invoiceID int64) (io.ReadCloser, string, error) {
			assert.Equal(t, int64(3), clientID)
			return io.NopCloser(strings.NewReader("%PDF-1.4")), "INV-1.pdf", nil
		},
	}
	app := newApp(svc, clientIdentity())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/invoices/1/pdf", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "application/pdf", resp.Header.Get("Content-Type"))
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "INV-1.pdf")
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "%PDF-1.4", string(body))
}

func TestHandlerAdminInvoicePDF(t *testing.T) {
	svc := &fakeSvc{
		DownloadPDFFn: func(_ context.Context, clientID, invoiceID int64) (io.ReadCloser, string, error) {
			assert.Zero(t, clientID, "admin bypasses ownership")
			assert.Equal(t, int64(7), invoiceID)
			return io.NopCloser(strings.NewReader("%PDF-1.4")), "INV-7.pdf", nil
		},
	}
	app := newApp(svc, adminIdentity())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/invoices/7/pdf", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "application/pdf", resp.Header.Get("Content-Type"))
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "INV-7.pdf")
}

func TestHandlerAdminListInvoices(t *testing.T) {
	svc := &fakeSvc{
		AdminListInvoicesFn: func(_ context.Context, p ports.ListParams) ([]domain.Invoice, int64, error) {
			assert.Equal(t, "INV-2026", p.Search)
			return nil, 0, nil
		},
	}
	app := newApp(svc, adminIdentity())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/invoices?search=INV-2026", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandlerAdminListInvoicesFilters(t *testing.T) {
	var got ports.ListParams
	called := false
	svc := &fakeSvc{
		AdminListInvoicesFn: func(_ context.Context, p ports.ListParams) ([]domain.Invoice, int64, error) {
			called, got = true, p
			return nil, 0, nil
		},
	}
	app := newApp(svc, adminIdentity())

	resp, err := app.Test(httptest.NewRequest("GET",
		"/api/v1/admin/invoices?client_id=5&date_from=2026-01-10&date_to=2026-01-31&status=paid", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(5), got.ClientID)
	assert.Equal(t, "paid", got.Status)
	assert.Equal(t, "2026-01-10", got.From.Format("2006-01-02"))
	assert.Equal(t, "2026-01-31", got.To.Format("2006-01-02"))

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/invoices?ids=4,5,6", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, []int64{4, 5, 6}, got.IDs)

	for _, q := range []string{"client_id=abc", "date_from=10-01-2026", "date_to=soon", "ids=1,x"} {
		called = false
		resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/invoices?"+q, nil))
		require.NoError(t, err)
		assert.Equal(t, 422, resp.StatusCode, q)
		assert.False(t, called, q)
	}
}

func TestHandlerAdminCreateInvoice(t *testing.T) {
	svc := &fakeSvc{
		CreateManualInvoiceFn: func(_ context.Context, actor int64, in ManualInvoiceInput) (*domain.Invoice, error) {
			assert.Equal(t, int64(42), actor)
			assert.Equal(t, int64(3), in.ClientID)
			require.Len(t, in.Items, 1)
			return &domain.Invoice{ID: 5, InvoiceNumber: "INV-5"}, nil
		},
	}
	app := newApp(svc, adminIdentity())
	body := `{"client_id":3,"items":[{"description":"Setup","amount":75000,"taxed":true}]}`
	req := httptest.NewRequest("POST", "/api/v1/admin/invoices", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)
}

func TestHandlerAdminCreateInvoiceBadBody(t *testing.T) {
	app := newApp(&fakeSvc{}, adminIdentity())
	req := httptest.NewRequest("POST", "/api/v1/admin/invoices", bytes.NewBufferString("{bad"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlerAdminGetInvoice(t *testing.T) {
	svc := &fakeSvc{
		GetInvoiceFn: func(_ context.Context, clientID, invoiceID int64) (*InvoiceDetail, error) {
			assert.Zero(t, clientID, "admin bypasses ownership")
			return &InvoiceDetail{Invoice: domain.Invoice{ID: invoiceID}}, nil
		},
	}
	app := newApp(svc, adminIdentity())
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/invoices/7", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandlerAdminUpdateInvoice(t *testing.T) {
	svc := &fakeSvc{
		UpdateInvoiceFn: func(_ context.Context, actor, id int64, in UpdateInvoiceInput) (*domain.Invoice, error) {
			assert.Equal(t, int64(42), actor)
			require.NotNil(t, in.DueDate)
			assert.Equal(t, "2026-08-01", *in.DueDate)
			return &domain.Invoice{ID: id}, nil
		},
	}
	app := newApp(svc, adminIdentity())
	req := httptest.NewRequest("PATCH", "/api/v1/admin/invoices/7", bytes.NewBufferString(`{"due_date":"2026-08-01"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandlerAdminAddPayment(t *testing.T) {
	svc := &fakeSvc{
		AddManualPaymentFn: func(_ context.Context, actor, id int64, in ManualPaymentInput) (*domain.Invoice, error) {
			assert.Equal(t, int64(100000), in.Amount)
			assert.Equal(t, "bank_transfer", in.Method)
			return &domain.Invoice{ID: id, Status: domain.InvoicePaid}, nil
		},
	}
	app := newApp(svc, adminIdentity())
	req := httptest.NewRequest("POST", "/api/v1/admin/invoices/7/payment",
		bytes.NewBufferString(`{"amount":100000,"method":"bank_transfer"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandlerAdminCancelInvoice(t *testing.T) {
	svc := &fakeSvc{
		CancelInvoiceFn: func(_ context.Context, actor, id int64) (*domain.Invoice, error) {
			return nil, apperr.Conflict("invoice cannot transition from paid to cancelled")
		},
	}
	app := newApp(svc, adminIdentity())
	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/admin/invoices/7/cancel", nil))
	require.NoError(t, err)
	assert.Equal(t, 409, resp.StatusCode)
}

func TestHandlerAdminRefundInvoice(t *testing.T) {
	var got RefundInput
	svc := &fakeSvc{
		RefundInvoiceFn: func(_ context.Context, actor, id int64, in RefundInput) (*domain.Invoice, error) {
			got = in
			return &domain.Invoice{ID: id, Status: domain.InvoiceRefunded}, nil
		},
	}
	app := newApp(svc, adminIdentity())

	// with body
	req := httptest.NewRequest("POST", "/api/v1/admin/invoices/7/refund",
		bytes.NewBufferString(`{"to_credit":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.True(t, got.ToCredit)

	// without body defaults to no credit
	resp, err = app.Test(httptest.NewRequest("POST", "/api/v1/admin/invoices/7/refund", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.False(t, got.ToCredit)
}

func TestHandlerErrorPaths(t *testing.T) {
	svcErr := apperr.Internal(io.ErrUnexpectedEOF)
	svc := &fakeSvc{
		ListClientInvoicesFn: func(context.Context, int64, ports.ListParams) ([]domain.Invoice, int64, error) {
			return nil, 0, svcErr
		},
		DownloadPDFFn: func(context.Context, int64, int64) (io.ReadCloser, string, error) {
			return nil, "", apperr.NotFound("invoice")
		},
		AdminListInvoicesFn: func(context.Context, ports.ListParams) ([]domain.Invoice, int64, error) {
			return nil, 0, svcErr
		},
		CreateManualInvoiceFn: func(context.Context, int64, ManualInvoiceInput) (*domain.Invoice, error) {
			return nil, apperr.Validation("bad input")
		},
		AddManualPaymentFn: func(context.Context, int64, int64, ManualPaymentInput) (*domain.Invoice, error) {
			return nil, apperr.Conflict("already paid")
		},
		UpdateInvoiceFn: func(context.Context, int64, int64, UpdateInvoiceInput) (*domain.Invoice, error) {
			return nil, apperr.Conflict("not editable")
		},
		RefundInvoiceFn: func(context.Context, int64, int64, RefundInput) (*domain.Invoice, error) {
			return nil, apperr.Conflict("not paid")
		},
		GetInvoiceFn: func(context.Context, int64, int64) (*InvoiceDetail, error) {
			return nil, apperr.NotFound("invoice")
		},
	}
	app := newApp(svc, adminIdentity())

	cases := []struct {
		method, path string
		body         string
		want         int
	}{
		{"GET", "/api/v1/invoices", "", 500},
		{"GET", "/api/v1/invoices/1/pdf", "", 404},
		{"GET", "/api/v1/invoices/0/pdf", "", 422},
		{"GET", "/api/v1/admin/invoices/1/pdf", "", 404},
		{"GET", "/api/v1/admin/invoices/bad/pdf", "", 422},
		{"GET", "/api/v1/admin/invoices", "", 500},
		{"GET", "/api/v1/admin/invoices/9", "", 404},
		{"GET", "/api/v1/admin/invoices/bad", "", 422},
		{"POST", "/api/v1/admin/invoices", `{"client_id":1,"items":[]}`, 422},
		{"PATCH", "/api/v1/admin/invoices/bad", `{}`, 422},
		{"PATCH", "/api/v1/admin/invoices/9", `{bad`, 422},
		{"PATCH", "/api/v1/admin/invoices/9", `{"notes":"x"}`, 409},
		{"POST", "/api/v1/admin/invoices/bad/payment", `{}`, 422},
		{"POST", "/api/v1/admin/invoices/9/payment", `{bad`, 422},
		{"POST", "/api/v1/admin/invoices/9/payment", `{"amount":1,"method":"m"}`, 409},
		{"POST", "/api/v1/admin/invoices/bad/cancel", "", 422},
		{"POST", "/api/v1/admin/invoices/bad/refund", "", 422},
		{"POST", "/api/v1/admin/invoices/9/refund", `{bad`, 422},
		{"POST", "/api/v1/admin/invoices/9/refund", `{"to_credit":true}`, 409},
	}
	for _, tc := range cases {
		var req = httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := app.Test(req)
		require.NoError(t, err, tc.path)
		assert.Equal(t, tc.want, resp.StatusCode, "%s %s", tc.method, tc.path)
	}
}
