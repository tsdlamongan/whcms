package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"strings"
)

// RDash document / verification lifecycle mock (docs.dewabiz.co.id "Auto
// Activation" + "Syarat Domain Indonesia"; swagger operations
// LinkUploadDocument and ResendVerification).
//
// - .co.id/.or.id/.ac.id/.sch.id/.ponpes.id need documents; .id/.my.id/
//   .biz.id/.web.id need an email verification; everything else is verified.
// - Registering a document domain answers Active with `is_required_document`
//   1 and verification_status 0 (Waiting) - Auto Activation - unless
//   POST /mock/rdash/auto-activation {"enabled":false} turned it off, in which
//   case the domain is held Pending until a reviewer verifies it.
// - POST /v1/domains/{id}/documents/link returns the URL of a hosted upload
//   page (GET/POST /documents/upload/{token}); submitting it moves the domain
//   to Document Validating (2). There is no real file handling: the page only
//   mimics the registrar's, so a browser can complete the flow end to end.
// - POST /mock/rdash/domain-state plays the registrar's reviewer / lifecycle
//   (approve = verification 3, bounce = back to 0 with a reason, suspend =
//   status 7, reject/delete = status 8/4) and GET /mock/rdash/state reads
//   the mock-side bookkeeping back for assertions.

var rdashStatusLabels = map[int]string{
	0: "Pending", 1: "Active", 2: "Expired", 3: "Pending Delete", 4: "Deleted",
	5: "Pending Transfer", 6: "Transferred Away", 7: "Suspended", 8: "Rejected", 9: "Moved",
}

var rdashVerificationLabels = map[int]string{
	0: "Waiting", 1: "Verifying", 2: "Document Validating", 3: "Active",
}

// rdashDocumentTLDs / rdashEmailVerificationTLDs mirror the registrar's
// published requirements.
var (
	rdashDocumentTLDs          = []string{"co.id", "or.id", "ac.id", "sch.id", "ponpes.id"}
	rdashEmailVerificationTLDs = []string{"id", "my.id", "biz.id", "web.id"}
)

func rdashTLD(name string) string {
	_, rest, _ := strings.Cut(strings.ToLower(name), ".")
	return rest
}

func rdashRequiresDocument(name string) bool {
	tld := rdashTLD(name)
	for _, t := range rdashDocumentTLDs {
		if tld == t {
			return true
		}
	}
	return false
}

func rdashNeedsEmailVerification(name string) bool {
	tld := rdashTLD(name)
	for _, t := range rdashEmailVerificationTLDs {
		if tld == t {
			return true
		}
	}
	return false
}

func newDocToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// initVerificationLocked sets a freshly registered/transferred domain's
// document + verification state from its TLD and the Auto Activation switch.
// Caller must hold s.mu.
func (s *Server) initVerificationLocked(d *rdashDomain, transfer bool) {
	d.RequiredDocument = rdashRequiresDocument(d.Name)
	d.Refundable = true
	d.DocToken = newDocToken()
	s.rdashDocTokens[d.DocToken] = d.ID
	switch {
	case d.RequiredDocument, rdashNeedsEmailVerification(d.Name):
		d.Verification = 0 // Waiting
	default:
		d.Verification = 3 // nothing to verify
	}
	if d.RequiredDocument && !s.rdashAutoActivation && !transfer {
		d.Status, d.StatusLabel = 0, rdashStatusLabels[0] // held until a reviewer verifies it
	}
	if transfer && d.RequiredDocument {
		// "Untuk nama domain ID yang membutuhkan dokumen, maka proses transfer
		// akan berjalan ketika dokumen sudah lengkap dan sudah lolos verifikasi."
		d.Status, d.StatusLabel = 5, rdashStatusLabels[5]
	}
}

// POST /v1/domains/{id}/documents/link - LinkUploadDocument.
func (s *Server) handleRDashDocumentLink(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	d, ok := s.getDomainByID(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	required := d.RequiredDocument
	token := d.DocToken
	if required {
		d.LinkRequests++
	}
	s.mu.Unlock()
	if !required {
		writeRDashValidation(w, "Domain does not require document", map[string][]string{
			"domain": {"Domain does not require document"},
		})
		return
	}
	writeRDash(w, http.StatusOK, "Success", s.cfg.BaseURL+"/documents/upload/"+token)
}

// POST /v1/domains/{id}/verification/resend - ResendVerification.
func (s *Server) handleRDashVerificationResend(w http.ResponseWriter, r *http.Request) {
	if _, ok := rdashAuth(w, r); !ok {
		return
	}
	d, ok := s.getDomainByID(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	verified := d.Verification == 3
	if !verified {
		d.ResendCount++
	}
	s.mu.Unlock()
	if verified {
		writeRDashValidation(w, "Domain already verified", map[string][]string{"domain": {"Domain already verified"}})
		return
	}
	writeRDash(w, http.StatusOK, "Email verification sent to your email address!", "")
}

// domainByToken resolves an upload token to its domain (nil when unknown).
// Caller must hold s.mu.
func (s *Server) domainByTokenLocked(token string) *rdashDomain {
	id, ok := s.rdashDocTokens[token]
	if !ok {
		return nil
	}
	return s.rdashDomains[id]
}

const uploadPageTpl = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Document upload</title></head>
<body style="font-family:sans-serif;max-width:520px;margin:48px auto">
<h1 data-testid="mock-upload-title">Upload documents for %s</h1>
<p data-testid="mock-upload-state">Verification status: %s</p>
<form method="POST" action="">
<p><input type="file" name="document" data-testid="mock-upload-file"></p>
<button type="submit" data-testid="mock-upload-submit">Upload documents</button>
</form></body></html>`

const uploadDonePageTpl = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Document upload</title></head>
<body style="font-family:sans-serif;max-width:520px;margin:48px auto">
<h1 data-testid="mock-upload-done">Documents received for %s</h1>
<p>Your documents are being validated.</p></body></html>`

// GET /documents/upload/{token} - the registrar-hosted upload page.
func (s *Server) handleDocumentUploadPage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	d := s.domainByTokenLocked(r.PathValue("token"))
	var name, state string
	if d != nil && d.RequiredDocument {
		name, state = d.Name, rdashVerificationLabels[d.Verification]
	}
	s.mu.Unlock()
	if name == "" {
		http.Error(w, "upload link is invalid or no longer needed", http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, uploadPageTpl, html.EscapeString(name), html.EscapeString(state))
}

// POST /documents/upload/{token} - submitting moves Waiting -> Document Validating.
func (s *Server) handleDocumentUploadSubmit(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	d := s.domainByTokenLocked(r.PathValue("token"))
	var name string
	if d != nil && d.RequiredDocument && d.Verification != 3 {
		name = d.Name
		d.Verification = 2
		d.StatusReason = ""
		d.Uploads++
	}
	s.mu.Unlock()
	if name == "" {
		http.Error(w, "upload link is invalid or no longer needed", http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, uploadDonePageTpl, html.EscapeString(name))
}

// POST /mock/rdash/domain-state - play the registrar's reviewer/lifecycle.
// {"domain":"x.co.id","status":7,"verification_status":0,"status_reason":"...",
//
//	"is_refundable":false,"required_document":true}
//
// Every field but domain is optional.
func (s *Server) handleRDashDomainState(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain             string  `json:"domain"`
		Status             *int    `json:"status"`
		VerificationStatus *int    `json:"verification_status"`
		StatusReason       *string `json:"status_reason"`
		IsRefundable       *bool   `json:"is_refundable"`
		RequiredDocument   *bool   `json:"required_document"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeMockError(w, "invalid request body")
		return
	}
	name := normalizeDomain(req.Domain)
	if name == "" {
		writeMockError(w, "missing required field: domain")
		return
	}
	if req.Status != nil {
		if _, ok := rdashStatusLabels[*req.Status]; !ok {
			writeMockError(w, "status must be 0..9")
			return
		}
	}
	if req.VerificationStatus != nil {
		if _, ok := rdashVerificationLabels[*req.VerificationStatus]; !ok {
			writeMockError(w, "verification_status must be 0..3")
			return
		}
	}

	s.mu.Lock()
	id, ok := s.rdashDomainsByName[name]
	if !ok {
		s.mu.Unlock()
		writeRDash(w, http.StatusNotFound, "domain not found", nil)
		return
	}
	d := s.rdashDomains[id]
	if req.Status != nil {
		d.Status, d.StatusLabel = *req.Status, rdashStatusLabels[*req.Status]
	}
	if req.VerificationStatus != nil {
		d.Verification = *req.VerificationStatus
	}
	if req.StatusReason != nil {
		d.StatusReason = *req.StatusReason
	}
	if req.IsRefundable != nil {
		d.Refundable = *req.IsRefundable
	}
	if req.RequiredDocument != nil {
		d.RequiredDocument = *req.RequiredDocument
	}
	payload := s.domainPayload(d)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "domain": payload})
}

// GET /mock/rdash/state?domain= - mock-side bookkeeping for assertions.
func (s *Server) handleRDashGetState(w http.ResponseWriter, r *http.Request) {
	name := normalizeDomain(r.URL.Query().Get("domain"))
	s.mu.Lock()
	id, ok := s.rdashDomainsByName[name]
	var out map[string]any
	if ok {
		d := s.rdashDomains[id]
		out = map[string]any{
			"id": d.ID, "name": d.Name, "status": d.Status, "verification_status": d.Verification,
			"required_document": d.RequiredDocument, "status_reason": d.StatusReason,
			"is_refundable": d.Refundable, "link_requests": d.LinkRequests,
			"uploads": d.Uploads, "resend_count": d.ResendCount,
		}
	}
	s.mu.Unlock()
	if !ok {
		writeRDash(w, http.StatusNotFound, "domain not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /mock/rdash/auto-activation {"enabled":false} - the reseller's Auto
// Activation switch (default on): off holds new document domains Pending.
func (s *Server) handleRDashAutoActivation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		writeMockError(w, "missing required field: enabled")
		return
	}
	s.mu.Lock()
	s.rdashAutoActivation = *req.Enabled
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": *req.Enabled})
}
