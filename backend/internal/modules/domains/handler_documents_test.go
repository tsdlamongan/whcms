package domains_test

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerDocumentLink(t *testing.T) {
	const link = "https://registrar.example/documents/upload/secret-token"
	svc := &fakeService{
		DocumentUploadLinkFn: func(ctx context.Context, clientID, domainID int64) (string, error) {
			assert.EqualValues(t, 5, clientID, "the client comes from the token, never the URL")
			assert.EqualValues(t, 10, domainID)
			return link, nil
		},
	}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})

	req := httptest.NewRequest("POST", "/api/v1/domains/10/documents/link", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"), "the link is a bearer secret")
	raw, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(raw), link)

	t.Run("service errors map through the envelope", func(t *testing.T) {
		svc := &fakeService{DocumentUploadLinkFn: func(context.Context, int64, int64) (string, error) {
			return "", apperr.Conflict("this domain does not require documents")
		}}
		status, env := doJSON(t, newApp(svc, &fakeMW{identity: clientIdentity()}), "POST", "/api/v1/domains/10/documents/link", "")
		assert.Equal(t, 409, status)
		assert.Equal(t, "CONFLICT", env["error"].(map[string]any)["code"])
		assert.Nil(t, env["data"])
	})

	t.Run("bad id", func(t *testing.T) {
		status, _ := doJSON(t, newApp(svc, &fakeMW{identity: clientIdentity()}), "POST", "/api/v1/domains/abc/documents/link", "")
		assert.Equal(t, 422, status)
	})

	t.Run("rate limited", func(t *testing.T) {
		status, _ := doJSON(t, newApp(svc, &fakeMW{identity: clientIdentity(), rateLimited: true}), "POST", "/api/v1/domains/10/documents/link", "")
		assert.Equal(t, 429, status)
	})

	t.Run("client only", func(t *testing.T) {
		status, _ := doJSON(t, newApp(svc, &fakeMW{identity: httpx.AuthIdentity{UserID: 2, Role: "staff"}}), "POST", "/api/v1/domains/10/documents/link", "")
		assert.Equal(t, 403, status)
		status, _ = doJSON(t, newApp(svc, &fakeMW{authFail: true}), "POST", "/api/v1/domains/10/documents/link", "")
		assert.Equal(t, 401, status)
	})
}

func TestHandlerResendVerification(t *testing.T) {
	var called bool
	svc := &fakeService{ResendVerificationFn: func(ctx context.Context, clientID, domainID int64) error {
		called = true
		assert.EqualValues(t, 5, clientID)
		assert.EqualValues(t, 10, domainID)
		return nil
	}}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})
	status, env := doJSON(t, app, "POST", "/api/v1/domains/10/verification/resend", "")
	assert.Equal(t, 200, status)
	assert.True(t, called)
	assert.Equal(t, true, env["data"].(map[string]any)["sent"])

	svc.ResendVerificationFn = func(context.Context, int64, int64) error { return apperr.Conflict("already verified") }
	status, _ = doJSON(t, app, "POST", "/api/v1/domains/10/verification/resend", "")
	assert.Equal(t, 409, status)

	status, _ = doJSON(t, app, "POST", "/api/v1/domains/x/verification/resend", "")
	assert.Equal(t, 422, status)
}

func TestHandlerRefresh(t *testing.T) {
	svc := &fakeService{RefreshForClientFn: func(ctx context.Context, clientID, domainID int64) (*domain.Domain, error) {
		assert.EqualValues(t, 5, clientID)
		return &domain.Domain{ID: domainID, Name: "a.co.id", Status: domain.DomainActive, Verification: domain.VerificationDocumentValidating}, nil
	}}
	app := newApp(svc, &fakeMW{identity: clientIdentity()})
	status, env := doJSON(t, app, "POST", "/api/v1/domains/10/refresh", "")
	assert.Equal(t, 200, status)
	data := env["data"].(map[string]any)
	assert.EqualValues(t, 10, data["id"])
	assert.Equal(t, "document_validating", data["verification_status"])

	svc.RefreshForClientFn = func(context.Context, int64, int64) (*domain.Domain, error) {
		return nil, apperr.NotFound("domain")
	}
	status, _ = doJSON(t, app, "POST", "/api/v1/domains/10/refresh", "")
	assert.Equal(t, 404, status)

	status, _ = doJSON(t, app, "POST", "/api/v1/domains/x/refresh", "")
	assert.Equal(t, 422, status)
}

func TestHandlerAdminRefund(t *testing.T) {
	var got domains.AdminRefundRequest
	svc := &fakeService{AdminRefundFn: func(ctx context.Context, actorUserID, id int64, in domains.AdminRefundRequest) (*domain.Domain, error) {
		got = in
		assert.EqualValues(t, 1, actorUserID)
		assert.EqualValues(t, 10, id)
		return &domain.Domain{ID: id, Status: domain.DomainRejected, RefundAmount: 150000}, nil
	}}
	app := newApp(svc, &fakeMW{identity: adminIdentity()})

	status, env := doJSON(t, app, "POST", "/api/v1/admin/domains/10/refund", `{"amount":150000,"reason":"goodwill"}`)
	assert.Equal(t, 200, status)
	require.NotNil(t, got.Amount)
	assert.EqualValues(t, 150000, *got.Amount)
	assert.Equal(t, "goodwill", got.Reason)
	assert.EqualValues(t, 150000, env["data"].(map[string]any)["refund_amount"])

	t.Run("body is optional: full refund", func(t *testing.T) {
		got = domains.AdminRefundRequest{}
		status, _ := doJSON(t, app, "POST", "/api/v1/admin/domains/10/refund", "")
		assert.Equal(t, 200, status)
		assert.Nil(t, got.Amount)
	})

	t.Run("validation", func(t *testing.T) {
		status, env := doJSON(t, app, "POST", "/api/v1/admin/domains/10/refund", `{"amount":0}`)
		assert.Equal(t, 422, status)
		assert.Equal(t, "VALIDATION", env["error"].(map[string]any)["code"])

		status, _ = doJSON(t, app, "POST", "/api/v1/admin/domains/10/refund", `{"reason":"`+strings.Repeat("x", 501)+`"}`)
		assert.Equal(t, 422, status)

		status, _ = doJSON(t, app, "POST", "/api/v1/admin/domains/10/refund", `{not json`)
		assert.Equal(t, 422, status)
	})

	t.Run("service error and bad id", func(t *testing.T) {
		svc.AdminRefundFn = func(context.Context, int64, int64, domains.AdminRefundRequest) (*domain.Domain, error) {
			return nil, apperr.Conflict("this domain was already refunded")
		}
		status, _ := doJSON(t, app, "POST", "/api/v1/admin/domains/10/refund", "")
		assert.Equal(t, 409, status)
		status, _ = doJSON(t, app, "POST", "/api/v1/admin/domains/x/refund", "")
		assert.Equal(t, 422, status)
	})

	t.Run("admin/staff with the domains permission only", func(t *testing.T) {
		status, _ := doJSON(t, newApp(svc, &fakeMW{identity: clientIdentity()}), "POST", "/api/v1/admin/domains/10/refund", "")
		assert.Equal(t, 403, status)
		status, _ = doJSON(t, newApp(svc, &fakeMW{identity: httpx.AuthIdentity{UserID: 2, Role: "staff"}, permissions: map[string]bool{}}),
			"POST", "/api/v1/admin/domains/10/refund", "")
		assert.Equal(t, 403, status)
	})
}
