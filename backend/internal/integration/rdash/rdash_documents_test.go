package rdash

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// detailsRoute answers GET /domains/details with the given domain object.
func detailsRoute(obj map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "ok", obj)
	}
}

func TestFlexBool(t *testing.T) {
	type wrap struct {
		V *flexBool `json:"v"`
	}
	cases := map[string]struct {
		in      string
		present bool
		want    bool
		wantErr bool
	}{
		"true":           {`{"v":true}`, true, true, false},
		"false":          {`{"v":false}`, true, false, false},
		"one":            {`{"v":1}`, true, true, false},
		"zero":           {`{"v":0}`, true, false, false},
		"quoted one":     {`{"v":"1"}`, true, true, false},
		"quoted zero":    {`{"v":"0"}`, true, false, false},
		"quoted true":    {`{"v":"true"}`, true, true, false},
		"empty string":   {`{"v":""}`, true, false, false},
		"other number":   {`{"v":2}`, true, true, false},
		"null":           {`{"v":null}`, false, false, false},
		"absent":         {`{}`, false, false, false},
		"garbage":        {`{"v":"maybe"}`, false, false, true},
		"fractional 0.0": {`{"v":0.0}`, true, false, false},
	}
	for name, tc := range cases {
		var w wrap
		err := json.Unmarshal([]byte(tc.in), &w)
		if tc.wantErr {
			assert.Error(t, err, name)
			continue
		}
		require.NoError(t, err, name)
		if !tc.present {
			assert.Nil(t, w.V, name)
			continue
		}
		require.NotNil(t, w.V, name)
		assert.Equal(t, tc.want, bool(*w.V), name)
	}
}

func TestVerificationFromLabel(t *testing.T) {
	assert.Equal(t, domain.VerificationWaiting, verificationFromLabel("Waiting"))
	assert.Equal(t, domain.VerificationVerifying, verificationFromLabel(" verifying "))
	assert.Equal(t, domain.VerificationDocumentValidating, verificationFromLabel("Document Validating"))
	assert.Equal(t, domain.VerificationActive, verificationFromLabel("ACTIVE"))
	assert.Equal(t, domain.VerificationNone, verificationFromLabel(""))
	assert.Equal(t, domain.VerificationNone, verificationFromLabel("Whatever"))
}

func TestDomainPayloadVerification(t *testing.T) {
	one, three := 0, 3
	yes, no := flexBool(true), flexBool(false)
	reason := "  Documents rejected: blurry ID  "

	cases := map[string]struct {
		in   domainPayload
		want ports.DomainVerificationInfo
	}{
		"nothing reported on a gTLD": {
			domainPayload{Name: "example.com"},
			ports.DomainVerificationInfo{},
		},
		"nothing reported on a document TLD falls back to the documented list": {
			domainPayload{Name: "sekolah.sch.id"},
			ports.DomainVerificationInfo{DocumentRequired: true},
		},
		"registrar says no documents, overriding the list": {
			domainPayload{Name: "a.co.id", IsRequiredDocument: &no},
			ports.DomainVerificationInfo{},
		},
		"numeric code wins over the label": {
			domainPayload{Name: "a.co.id", IsRequiredDocument: &yes, VerificationStatus: &three, VerificationStatusLabel: "Waiting"},
			ports.DomainVerificationInfo{DocumentRequired: true, Verification: domain.VerificationActive},
		},
		"null code falls back to the label (the register response)": {
			domainPayload{Name: "a.co.id", IsRequiredDocument: &yes, VerificationStatusLabel: "Waiting"},
			ports.DomainVerificationInfo{DocumentRequired: true, Verification: domain.VerificationWaiting},
		},
		"waiting code zero, reason trimmed, refundable": {
			domainPayload{Name: "a.co.id", VerificationStatus: &one, StatusReason: &reason, IsRefundable: &yes},
			ports.DomainVerificationInfo{
				DocumentRequired: true, Verification: domain.VerificationWaiting,
				StatusReason: "Documents rejected: blurry ID", Refundable: true,
			},
		},
	}
	for name, tc := range cases {
		assert.Equal(t, tc.want, tc.in.verification(), name)
	}

	for code, want := range map[int]domain.DomainVerification{
		1: domain.VerificationVerifying, 2: domain.VerificationDocumentValidating,
	} {
		got := domainPayload{Name: "a.id", VerificationStatus: &code}.verification()
		assert.Equal(t, want, got.Verification, "code %d", code)
	}
	unknown := 9
	assert.Equal(t, domain.VerificationNone,
		domainPayload{Name: "a.id", VerificationStatus: &unknown}.verification().Verification)
}

func TestRegisterAndSyncCarryVerificationInfo(t *testing.T) {
	obj := map[string]any{
		"id": 9, "name": "sekolah.sch.id", "status": 1, "status_label": "Active", "expired_at": "2027-01-01",
		"is_required_document": 1, "verification_status": 2, "verification_status_label": "Document Validating",
		"status_reason": nil, "is_refundable": true,
	}
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers":            func(w http.ResponseWriter, r *http.Request) { writeEnv(w, 200, "ok", map[string]any{"id": 5}) },
		"POST /customers/5/contacts": func(w http.ResponseWriter, r *http.Request) { writeEnv(w, 200, "ok", map[string]any{"id": 6}) },
		"POST /domains":              func(w http.ResponseWriter, r *http.Request) { writeEnv(w, 200, "ok", obj) },
		"GET /domains/details":       detailsRoute(obj),
	}))

	res, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{
		Name: "sekolah.sch.id", Years: 1, Contact: sampleContact(),
	})
	require.NoError(t, err)
	assert.Equal(t, ports.DomainVerificationInfo{
		DocumentRequired: true, Verification: domain.VerificationDocumentValidating, Refundable: true,
	}, res.DomainVerificationInfo)

	info, err := f.client.SyncDomain(context.Background(), "sekolah.sch.id")
	require.NoError(t, err)
	assert.Equal(t, res.DomainVerificationInfo, info.DomainVerificationInfo)
}

func TestSyncDomainSuspendedWithReason(t *testing.T) {
	f := newFixture(t, detailsRoute(map[string]any{
		"id": 9, "name": "a.co.id", "status": 7, "status_label": "Suspended", "expired_at": "2027-01-01",
		"is_required_document": 1, "verification_status": 0, "status_reason": "Document not uploaded",
	}))
	info, err := f.client.SyncDomain(context.Background(), "a.co.id")
	require.NoError(t, err)
	assert.Equal(t, "suspended", info.Status)
	assert.Equal(t, "Document not uploaded", info.StatusReason)
	assert.True(t, info.DocumentRequired)
	assert.Equal(t, domain.VerificationWaiting, info.Verification)
}

func TestDocumentUploadLink(t *testing.T) {
	const link = "https://dewabiz.example/documents/upload/va1JJC5Cd5V7EeRLzH4lNGiBBP7BrGHFX8jsjWSn8QYfP"
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": detailsRoute(map[string]any{"id": 42, "name": "a.co.id"}),
		"POST /domains/42/documents/link": func(w http.ResponseWriter, r *http.Request) {
			requireBasicAuth(t, r)
			writeEnv(w, http.StatusOK, "Success", link)
		},
	}))

	got, err := f.client.DocumentUploadLink(context.Background(), "A.co.id")
	require.NoError(t, err)
	assert.Equal(t, link, got)

	// the bearer link is never written to the integration log
	require.NotEmpty(t, f.logger.Calls)
	var saw bool
	for _, c := range f.logger.Calls {
		raw, _ := json.Marshal(c)
		assert.NotContains(t, string(raw), "va1JJC5Cd5V7", "upload token leaked into the integration log")
		if c.Endpoint == "/domains/42/documents/link" {
			saw = true
			assert.Equal(t, map[string]any{"data": redactedPlaceholder}, c.Response)
			assert.True(t, c.Success)
		}
	}
	assert.True(t, saw, "the link call must still be logged (redacted)")
}

func TestDocumentUploadLinkRejectsUnusableLinks(t *testing.T) {
	for name, data := range map[string]any{
		"empty":       "",
		"null":        nil,
		"javascript":  "javascript:alert(1)",
		"no host":     "https:///path",
		"relative":    "/documents/upload/abc",
		"unparseable": "http://[::1",
		"ftp":         "ftp://files.example/x",
	} {
		f := newFixture(t, routes(t, map[string]http.HandlerFunc{
			"GET /domains/details":            detailsRoute(map[string]any{"id": 42}),
			"POST /domains/42/documents/link": func(w http.ResponseWriter, r *http.Request) { writeEnv(w, 200, "ok", data) },
		}))
		_, err := f.client.DocumentUploadLink(context.Background(), "a.co.id")
		requireAppErr(t, err, apperr.CodeConflict)
		assert.NotNil(t, err, name)
	}
}

func TestDocumentUploadLinkErrors(t *testing.T) {
	// unknown domain
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusNotFound, "Domain not found", nil)
	})
	_, err := f.client.DocumentUploadLink(context.Background(), "a.co.id")
	requireAppErr(t, err, apperr.CodeNotFound)

	// registrar refuses (domain needs no documents)
	f = newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": detailsRoute(map[string]any{"id": 42}),
		"POST /domains/42/documents/link": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusUnprocessableEntity, "Domain does not require document", nil)
		},
	}))
	_, err = f.client.DocumentUploadLink(context.Background(), "a.com")
	requireAppErr(t, err, apperr.CodeValidation)

	// bad name never reaches the network
	_, err = f.client.DocumentUploadLink(context.Background(), "  ")
	requireAppErr(t, err, apperr.CodeValidation)
}

func TestResendVerification(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": detailsRoute(map[string]any{"id": 42}),
		"POST /domains/42/verification/resend": func(w http.ResponseWriter, r *http.Request) {
			requireBasicAuth(t, r)
			writeEnv(w, http.StatusOK, "Email verification sent to your email address!", "")
		},
	}))
	require.NoError(t, f.client.ResendVerification(context.Background(), "a.my.id"))

	f = newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusNotFound, "Domain not found", nil)
	})
	requireAppErr(t, f.client.ResendVerification(context.Background(), "a.my.id"), apperr.CodeNotFound)

	f = newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": detailsRoute(map[string]any{"id": 42}),
		"POST /domains/42/verification/resend": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusUnprocessableEntity, "Domain already verified", nil)
		},
	}))
	requireAppErr(t, f.client.ResendVerification(context.Background(), "a.my.id"), apperr.CodeValidation)
}

func TestRedactResponse(t *testing.T) {
	assert.Equal(t, map[string]any{"data": redactedPlaceholder},
		redactResponse("/domains/9/documents/link", []byte(`{"success":true,"data":"https://x/y/secret"}`)))

	got, ok := redactResponse("/domains/9", []byte(`{"data":{"token":"abc","name":"a.com"}}`)).(map[string]any)
	require.True(t, ok)
	raw, _ := json.Marshal(got)
	assert.NotContains(t, string(raw), "abc")
	assert.Contains(t, string(raw), "a.com")
}
