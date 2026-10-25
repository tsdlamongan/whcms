package domain_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dayPtr(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestDocumentTLDs(t *testing.T) {
	for _, tld := range []string{"co.id", ".co.id", "OR.ID", " ac.id ", "sch.id", "ponpes.id"} {
		assert.True(t, domain.TLDRequiresDocuments(tld), tld)
	}
	for _, tld := range []string{"id", "my.id", "biz.id", "web.id", "com", "", "go.id"} {
		assert.False(t, domain.TLDRequiresDocuments(tld), tld)
	}

	assert.True(t, domain.NameRequiresDocuments("sekolah.sch.id"))
	assert.True(t, domain.NameRequiresDocuments("Toko.CO.ID."))
	assert.False(t, domain.NameRequiresDocuments("toko.my.id"))
	assert.False(t, domain.NameRequiresDocuments("example.com"))
	assert.False(t, domain.NameRequiresDocuments("localhost"))

	for _, n := range []string{"a.id", "a.my.id", "a.biz.id", "a.web.id"} {
		assert.True(t, domain.NameRequiresEmailVerification(n), n)
	}
	for _, n := range []string{"a.co.id", "a.com", "a"} {
		assert.False(t, domain.NameRequiresEmailVerification(n), n)
	}
}

func TestDomainVerificationValid(t *testing.T) {
	for _, v := range []domain.DomainVerification{
		domain.VerificationNone, domain.VerificationWaiting, domain.VerificationVerifying,
		domain.VerificationDocumentValidating, domain.VerificationActive,
	} {
		assert.True(t, v.Valid(), string(v))
	}
	assert.False(t, domain.DomainVerification("approved").Valid())
}

func TestDomainDeadlines_documentTimeline(t *testing.T) {
	// Day 1 is the activation day: upload through day 3, suspended on day 5,
	// deleted on day 30.
	d := domain.Domain{
		Name: "sekolah.sch.id", Status: domain.DomainActive, RegistrationDate: dayPtr(2026, 10, 1),
		DocumentRequired: true, Verification: domain.VerificationWaiting,
	}
	dl := d.Deadlines()
	require.NotNil(t, dl)
	assert.Equal(t, domain.VerificationKindDocuments, dl.Kind)
	assert.Equal(t, *dayPtr(2026, 10, 3), *dl.UploadBy)
	assert.Equal(t, *dayPtr(2026, 10, 5), dl.SuspendOn)
	assert.Equal(t, *dayPtr(2026, 10, 30), *dl.DeleteOn)

	d.Status = domain.DomainSuspended
	assert.NotNil(t, d.Deadlines(), "a suspended domain still counts down to deletion")
}

func TestDomainDeadlines_emailTimeline(t *testing.T) {
	d := domain.Domain{
		Name: "toko.my.id", Status: domain.DomainActive, RegistrationDate: dayPtr(2026, 10, 1),
		Verification: domain.VerificationWaiting,
	}
	dl := d.Deadlines()
	require.NotNil(t, dl)
	assert.Equal(t, domain.VerificationKindEmail, dl.Kind)
	assert.Nil(t, dl.UploadBy)
	assert.Nil(t, dl.DeleteOn)
	assert.Equal(t, *dayPtr(2026, 10, 31), dl.SuspendOn) // day 31: "more than 30 days"
}

func TestDomainDeadlines_none(t *testing.T) {
	tests := map[string]domain.Domain{
		"verified": {
			Name: "a.co.id", Status: domain.DomainActive, RegistrationDate: dayPtr(2026, 10, 1),
			DocumentRequired: true, Verification: domain.VerificationActive,
		},
		"no registration date": {
			Name: "a.co.id", Status: domain.DomainActive, DocumentRequired: true, Verification: domain.VerificationWaiting,
		},
		"terminal status": {
			Name: "a.co.id", Status: domain.DomainRejected, RegistrationDate: dayPtr(2026, 10, 1),
			DocumentRequired: true, Verification: domain.VerificationWaiting,
		},
		"expired": {
			Name: "a.com", Status: domain.DomainExpired, RegistrationDate: dayPtr(2026, 10, 1),
			Verification: domain.VerificationWaiting,
		},
		"registrar never reported a verification on an email tld": {
			Name: "a.my.id", Status: domain.DomainActive, RegistrationDate: dayPtr(2026, 10, 1),
		},
		"verification is not a thing for a gTLD": {
			Name: "a.com", Status: domain.DomainActive, RegistrationDate: dayPtr(2026, 10, 1),
			Verification: domain.VerificationWaiting,
		},
	}
	for name, d := range tests {
		assert.Nil(t, d.Deadlines(), name)
	}

	// still open (so the poller keeps watching it) even though there is no
	// registration date to count from yet
	noDate := tests["no registration date"]
	assert.True(t, noDate.VerificationOpen())
	delete(tests, "no registration date")
	for name, d := range tests {
		assert.False(t, d.VerificationOpen(), name)
	}
}

func TestDomainMarshalJSON_includesDeadlinesAndHidesEPP(t *testing.T) {
	d := domain.Domain{
		ID: 7, Name: "a.co.id", Status: domain.DomainActive, RegistrationDate: dayPtr(2026, 10, 1),
		DocumentRequired: true, Verification: domain.VerificationWaiting, EPPCodeEnc: "secret-ciphertext",
	}
	raw, err := json.Marshal(d)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.EqualValues(t, 7, got["id"])
	assert.Equal(t, true, got["document_required"])
	assert.Equal(t, "waiting", got["verification_status"])
	dl, ok := got["verification_deadlines"].(map[string]any)
	require.True(t, ok, "verification_deadlines must be an object: %s", raw)
	assert.Equal(t, "documents", dl["kind"])
	assert.NotContains(t, string(raw), "secret-ciphertext")
	assert.NotContains(t, string(raw), "epp_code")

	// a pointer marshals identically
	rawPtr, err := json.Marshal(&d)
	require.NoError(t, err)
	assert.JSONEq(t, string(raw), string(rawPtr))

	// no open verification -> explicit null, never an omitted key
	plain, err := json.Marshal(domain.Domain{Name: "a.com", Status: domain.DomainActive})
	require.NoError(t, err)
	var got2 map[string]any
	require.NoError(t, json.Unmarshal(plain, &got2))
	v, present := got2["verification_deadlines"]
	assert.True(t, present)
	assert.Nil(t, v)
}

func TestDomainDocumentFailure(t *testing.T) {
	assert.True(t, domain.Domain{DocumentRequired: true, Verification: domain.VerificationWaiting}.DocumentFailure())
	assert.True(t, domain.Domain{DocumentRequired: true, Verification: domain.VerificationDocumentValidating}.DocumentFailure())
	assert.True(t, domain.Domain{DocumentRequired: true}.DocumentFailure())
	assert.False(t, domain.Domain{DocumentRequired: true, Verification: domain.VerificationActive}.DocumentFailure())
	assert.False(t, domain.Domain{Verification: domain.VerificationWaiting}.DocumentFailure())
}

func TestDomainRefundDecision(t *testing.T) {
	paid := time.Now()
	tests := []struct {
		name   string
		d      domain.Domain
		want   bool
		reason domain.RefundReason
	}{
		{
			"registrar refundable, no documents involved",
			domain.Domain{RegistrarRefundable: true}, true, domain.RefundRegistrarRefundable,
		},
		{
			"registrar says non-refundable",
			domain.Domain{RegistrarRefundable: false}, false, domain.RefundRegistrarNonRefundable,
		},
		{
			// docs: day-30 deletion after missing/rejected documents -> no refund,
			// whatever the is_refundable flag says
			"documents never verified overrides the refundable flag",
			domain.Domain{DocumentRequired: true, Verification: domain.VerificationWaiting, RegistrarRefundable: true},
			false, domain.RefundDocumentsNotVerified,
		},
		{
			"documents in review when rejected is still a document failure",
			domain.Domain{DocumentRequired: true, Verification: domain.VerificationDocumentValidating, RegistrarRefundable: true},
			false, domain.RefundDocumentsNotVerified,
		},
		{
			"documents verified, registry still rejected, registrar refundable",
			domain.Domain{DocumentRequired: true, Verification: domain.VerificationActive, RegistrarRefundable: true},
			true, domain.RefundRegistrarRefundable,
		},
		{
			"already refunded once",
			domain.Domain{RegistrarRefundable: true, RefundedAt: &paid}, false, domain.RefundAlreadyRefunded,
		},
	}
	for _, tt := range tests {
		got := tt.d.RefundDecision()
		assert.Equal(t, tt.want, got.Eligible, tt.name)
		assert.Equal(t, tt.reason, got.Reason, tt.name)
	}
}
