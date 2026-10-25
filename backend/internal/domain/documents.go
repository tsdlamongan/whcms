package domain

import (
	"encoding/json"
	"strings"
	"time"
)

// Domain document / verification rules, transcribed from the registrar's
// documentation (docs.dewabiz.co.id: "Syarat Domain Indonesia", "Auto
// Activation"). Change them only together with that documentation.

// documentTLDs are the Indonesian extensions that need identity/legal
// documents before the registry accepts the registrant.
var documentTLDs = map[string]struct{}{
	"co.id": {}, "or.id": {}, "ac.id": {}, "sch.id": {}, "ponpes.id": {},
}

// emailVerificationTLDs need no documents - only the registrant confirming an
// email; unconfirmed after 30 days the domain is suspended.
var emailVerificationTLDs = map[string]struct{}{
	"id": {}, "my.id": {}, "biz.id": {}, "web.id": {},
}

// Day numbers of the auto-activation timeline. Day 1 is the day the domain is
// activated; the client may upload on days 1-3, gets a reminder plus suspend
// warning on day 4, the domain is suspended on day 5 and deleted on day 30.
const (
	DocumentUploadLastDay = 3
	DocumentSuspendDay    = 5
	DocumentDeleteDay     = 30
	// EmailVerificationSuspendDays: an unverified .id/.my.id/.biz.id/.web.id
	// domain is suspended more than 30 days after registration.
	EmailVerificationSuspendDays = 30
)

// tldOf returns the part of name after its first label ("shop.co.id" ->
// "co.id"), or "" for a single label.
func tldOf(name string) string {
	name = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, ".")))
	_, rest, ok := strings.Cut(name, ".")
	if !ok {
		return ""
	}
	return rest
}

// TLDRequiresDocuments reports whether the extension ("co.id" or ".co.id")
// needs documents.
func TLDRequiresDocuments(tld string) bool {
	_, ok := documentTLDs[strings.TrimPrefix(strings.ToLower(strings.TrimSpace(tld)), ".")]
	return ok
}

// NameRequiresDocuments reports whether the registered name's extension
// needs documents (e.g. "sekolah.sch.id").
func NameRequiresDocuments(name string) bool {
	return TLDRequiresDocuments(tldOf(name))
}

// NameRequiresEmailVerification reports whether the name's extension is one
// of the Indonesian TLDs verified through the registrant's email.
func NameRequiresEmailVerification(name string) bool {
	_, ok := emailVerificationTLDs[tldOf(name)]
	return ok
}

// VerificationDeadlines is the documented timeline for a domain whose
// verification is still open, derived from its registration date (day 1).
// UploadBy/DeleteOn are set only for the document flow; SuspendOn always.
type VerificationDeadlines struct {
	Kind      string     `json:"kind"` // "documents" | "email"
	UploadBy  *time.Time `json:"upload_by,omitempty"`
	SuspendOn time.Time  `json:"suspend_on"`
	DeleteOn  *time.Time `json:"delete_on,omitempty"`
}

// Verification kinds reported in VerificationDeadlines.Kind.
const (
	VerificationKindDocuments = "documents"
	VerificationKindEmail     = "email"
)

// dayN returns the calendar date of day n counting the registration date as day 1.
func dayN(reg time.Time, n int) time.Time {
	y, m, d := reg.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n-1)
}

// VerificationOpen reports whether the domain is still waiting on the
// registrant (documents or email) and so still subject to the suspend/delete
// timeline.
func (d Domain) VerificationOpen() bool {
	switch d.Status {
	case DomainActive, DomainSuspended, DomainPendingTransfer, DomainPending:
	default:
		return false
	}
	if d.Verification == VerificationActive {
		return false
	}
	return d.DocumentRequired || (NameRequiresEmailVerification(d.Name) && d.Verification != VerificationNone)
}

// DocumentFailure reports whether the domain needs documents that were never
// verified - the situation in which the registrar's documented policy is
// "no refund".
func (d Domain) DocumentFailure() bool {
	return d.DocumentRequired && d.Verification != VerificationActive
}

// Deadlines returns the documented timeline while verification is open and
// the registration date is known, else nil.
func (d Domain) Deadlines() *VerificationDeadlines {
	if !d.VerificationOpen() || d.RegistrationDate == nil {
		return nil
	}
	reg := *d.RegistrationDate
	if d.DocumentRequired {
		upload, suspend, del := dayN(reg, DocumentUploadLastDay), dayN(reg, DocumentSuspendDay), dayN(reg, DocumentDeleteDay)
		return &VerificationDeadlines{
			Kind: VerificationKindDocuments, UploadBy: &upload, SuspendOn: suspend, DeleteOn: &del,
		}
	}
	return &VerificationDeadlines{
		Kind: VerificationKindEmail, SuspendOn: dayN(reg, EmailVerificationSuspendDays+1),
	}
}

// MarshalJSON adds the computed `verification_deadlines` to the entity's
// JSON (client and admin responses serialize the entity directly).
func (d Domain) MarshalJSON() ([]byte, error) {
	type plain Domain // no methods: avoids recursing into MarshalJSON
	return json.Marshal(struct {
		plain
		VerificationDeadlines *VerificationDeadlines `json:"verification_deadlines"`
	}{plain(d), d.Deadlines()})
}

// RefundReason says why a rejected domain is, or is not, refundable.
type RefundReason string

const (
	RefundRegistrarRefundable    RefundReason = "registrar_refundable"
	RefundDocumentsNotVerified   RefundReason = "documents_not_verified"
	RefundRegistrarNonRefundable RefundReason = "registrar_non_refundable"
	RefundAlreadyRefunded        RefundReason = "already_refunded"
)

// RefundDecision is the outcome of DomainRefundDecision.
type RefundDecision struct {
	Eligible bool
	Reason   RefundReason
}

// RefundDecision applies the registrar's refund policy to a rejected domain.
// The documentation is explicit that no refund applies when the required
// documents were never uploaded or were rejected (they are deleted on day
// 30); otherwise the registrar's own `is_refundable` flag decides. A refund
// is issued at most once.
func (d Domain) RefundDecision() RefundDecision {
	switch {
	case d.RefundedAt != nil:
		return RefundDecision{Reason: RefundAlreadyRefunded}
	case d.DocumentFailure():
		return RefundDecision{Reason: RefundDocumentsNotVerified}
	case !d.RegistrarRefundable:
		return RefundDecision{Reason: RefundRegistrarNonRefundable}
	}
	return RefundDecision{Eligible: true, Reason: RefundRegistrarRefundable}
}
