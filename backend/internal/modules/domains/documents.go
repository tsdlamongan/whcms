package domains

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Domain document / verification lifecycle - the registrar side of RDash's
// "Auto Activation" (docs.dewabiz.co.id/domain/auto-activation):
//
//	day 1       domain active (provisionally); the registrant is asked for documents
//	day 1-3     the client uploads the documents on the registrar-hosted page
//	day 4       reminder + suspend warning if nothing (valid) was uploaded
//	day 5       domain suspended
//	day 14/21   further reminders
//	day 30      domain deleted - NO refund when documents were missing or rejected
//
// The registrar has no upload API (only a link to its hosted page), so this
// module's job is to hand the client that link, mirror the registrar's
// verification state onto the domain row, tell the client when a review
// bounced the upload or the domain was suspended, and settle the money when
// the registrar finally rejects the domain (domain.Domain.RefundDecision).

// Notification template keys for the lifecycle (seeded in 000028).
const (
	tplDomainDocumentsRequired = "domain_documents_required"
	tplDomainDocumentsRejected = "domain_documents_rejected"
	tplDomainSuspended         = "domain_suspended"
	tplDomainRejected          = "domain_rejected"
)

// verificationPollLimit bounds one SyncAwaitingVerification run.
const verificationPollLimit = 200

// syncEvents are the lifecycle moments one registrar sync revealed; each one
// triggers exactly one client email, after the new state is persisted.
type syncEvents struct {
	docsRejected bool // a document review bounced the upload (validating -> waiting)
	suspended    bool // the registrar suspended the domain
	rejected     bool // the registrar refused/deleted the registration
}

// applyVerification mirrors the registrar's verification view onto dom and
// reports whether it reveals a bounced document review. A registrar that did
// not report a verification status (empty) never erases a known one.
func (s *Service) applyVerification(dom *domain.Domain, info ports.DomainVerificationInfo) (docsRejected bool) {
	prev := dom.Verification
	dom.DocumentRequired = info.DocumentRequired
	if info.Verification != domain.VerificationNone {
		dom.Verification = info.Verification
	}
	dom.StatusReason = info.StatusReason
	dom.RegistrarRefundable = info.Refundable

	switch {
	case dom.DocumentRequired && prev == domain.VerificationDocumentValidating &&
		dom.Verification == domain.VerificationWaiting:
		// Under review, then back to "waiting": the review rejected the
		// documents and the client has to upload them again.
		now := s.d.Clock.Now()
		dom.DocumentRejectedAt = &now
		return true
	case dom.Verification == domain.VerificationDocumentValidating, dom.Verification == domain.VerificationActive:
		dom.DocumentRejectedAt = nil // re-submitted, or verified
	}
	return false
}

// mapSyncStatus maps the registrar's status onto ours. A domain that needed
// documents that were never verified and that the registrar then deleted (day
// 30) or rejected counts as rejected - the case the refund policy is about -
// rather than a plain cancellation.
func mapSyncStatus(info *ports.DomainSyncInfo) (domain.DomainStatus, bool) {
	next, ok := mapRegistrarStatus(info.Status)
	if ok && next == domain.DomainCancelled &&
		info.DocumentRequired && info.Verification != domain.VerificationActive {
		return domain.DomainRejected, true
	}
	return next, ok
}

// mutateFromSync applies one registrar sync to dom in memory (status only
// through the state machine) and reports the lifecycle events it caused.
func (s *Service) mutateFromSync(ctx context.Context, dom *domain.Domain, info *ports.DomainSyncInfo) (syncEvents, error) {
	var ev syncEvents
	ev.docsRejected = s.applyVerification(dom, info.DomainVerificationInfo)

	if next, ok := mapSyncStatus(info); ok && next != dom.Status {
		if domain.DomainCanTransition(dom.Status, next) {
			if next == domain.DomainActive && dom.RegistrationDate == nil {
				// A domain the registrar held back until its documents were
				// verified starts its life (and its renewal clock) now.
				now := s.d.Clock.Now()
				dom.RegistrationDate = &now
			}
			dom.Status = next
			ev.suspended = next == domain.DomainSuspended
			ev.rejected = next == domain.DomainRejected
		} else {
			s.d.Log.WarnContext(ctx, "domain sync: illegal status transition skipped",
				"domain", dom.Name, "from", dom.Status, "to", next)
		}
	}
	if !info.ExpiryDate.IsZero() {
		expiry := info.ExpiryDate
		dom.ExpiryDate = &expiry
		if dom.NextDueDate == nil || dom.NextDueDate.Before(expiry) {
			dom.NextDueDate = &expiry
		}
	}
	if len(info.NS) > 0 {
		raw, err := json.Marshal(info.NS)
		if err != nil {
			return ev, apperr.Internal(err)
		}
		dom.Nameservers = raw
	}
	return ev, nil
}

// settleRejected is SyncDomainJob's path for a domain the registrar just
// rejected. The status change, the refund (client credit) and the refund
// record commit together under the domain's row lock, so concurrent syncs
// (cron, admin click, client refresh) can neither refund twice nor refund a
// domain that stayed alive. Emails go out after the commit.
func (s *Service) settleRejected(ctx context.Context, domainID int64, info *ports.DomainSyncInfo) error {
	var (
		settled  *domain.Domain
		decision domain.RefundDecision
		refunded int64
	)
	err := s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		locked, err := s.d.Domains.GetByIDForUpdate(txCtx, domainID)
		if err != nil {
			return err
		}
		if locked.Status == domain.DomainRejected {
			return nil // a concurrent sync already settled it
		}
		ev, err := s.mutateFromSync(txCtx, locked, info)
		if err != nil {
			return err
		}
		if !ev.rejected { // the row moved under us; keep the plain sync result
			return s.d.Domains.Update(txCtx, locked)
		}
		decision = locked.RefundDecision()
		if decision.Eligible {
			if refunded, err = s.creditRegistration(txCtx, locked, "Domain registration refund: "+locked.Name); err != nil {
				return err
			}
		}
		if err := s.d.Domains.Update(txCtx, locked); err != nil {
			return err
		}
		settled = locked
		return nil
	})
	if err != nil || settled == nil {
		return err
	}
	s.afterRejected(ctx, settled, decision, refunded)
	return nil
}

// creditRegistration credits the client what they paid for the domain's
// registration and records it on dom (RefundAmount/RefundedAt - the caller
// persists). A domain with no paid order behind it (admin-added, won by a
// hunt) has nothing to give back: 0 and no error.
func (s *Service) creditRegistration(ctx context.Context, dom *domain.Domain, reason string) (int64, error) {
	pay, err := s.d.Payments.RegistrationPayment(ctx, dom.ID)
	if err != nil {
		if apperr.From(err).Code == apperr.CodeNotFound {
			return 0, nil
		}
		return 0, err
	}
	if pay == nil || pay.Amount <= 0 {
		return 0, nil
	}
	invoiceID := pay.InvoiceID
	if _, err := s.d.Clients.AdjustCredit(ctx, dom.ClientID, pay.Amount, reason, &invoiceID); err != nil {
		return 0, err
	}
	now := s.d.Clock.Now()
	dom.RefundAmount = pay.Amount
	dom.RefundedAt = &now
	return pay.Amount, nil
}

// afterRejected tells the client what happened and, when no refund was
// issued, the admins - nobody is left guessing why money did not move.
func (s *Service) afterRejected(ctx context.Context, dom *domain.Domain, decision domain.RefundDecision, refunded int64) {
	if userID, err := s.clientUserID(ctx, dom.ClientID); err == nil {
		s.notifyWith(ctx, userID, tplDomainRejected, dom, map[string]any{
			"Refunded":        refunded > 0,
			"RefundAmount":    domain.FormatIDR(refunded),
			"DocumentFailure": decision.Reason == domain.RefundDocumentsNotVerified,
		})
	} else {
		s.d.Log.WarnContext(ctx, "domain rejected: client user not found", "domain", dom.Name, "error", err)
	}
	if refunded > 0 {
		return
	}
	var why string
	switch {
	case decision.Eligible:
		why = "the registrar reports it refundable, but no paid order was found for it - settle the refund manually"
	case decision.Reason == domain.RefundDocumentsNotVerified:
		why = "its required documents were never verified, and the registrar's policy is no refund in that case"
	default:
		why = "the registrar reports the registration as non-refundable"
	}
	if err := s.d.Notifier.AlertAdmin(ctx, "Domain rejected by the registrar: "+dom.Name,
		fmt.Sprintf("%s (domain #%d, client #%d) was rejected by the registrar and was NOT refunded automatically: %s. "+
			"Registrar reason: %q. An admin can still credit the client from the domain page.",
			dom.Name, dom.ID, dom.ClientID, why, dom.StatusReason)); err != nil {
		s.d.Log.WarnContext(ctx, "domain rejected: admin alert failed", "domain", dom.Name, "error", err)
	}
}

// dispatchSyncEvents sends the client emails for the lifecycle moments a
// non-final sync revealed.
func (s *Service) dispatchSyncEvents(ctx context.Context, dom *domain.Domain, ev syncEvents) {
	if !ev.docsRejected && !ev.suspended {
		return
	}
	userID, err := s.clientUserID(ctx, dom.ClientID)
	if err != nil {
		s.d.Log.WarnContext(ctx, "domain sync: client user not found", "domain", dom.Name, "error", err)
		return
	}
	if ev.docsRejected {
		s.notify(ctx, userID, tplDomainDocumentsRejected, dom)
	}
	if ev.suspended {
		s.notify(ctx, userID, tplDomainSuspended, dom)
	}
}

// SyncAwaitingVerification polls the registrar for the domains whose
// document/email verification is still open (cron:domain_verification_sync) so
// an upload that was approved, bounced or left to lapse shows up within
// minutes instead of at the next daily sync. Failures are logged and skipped;
// returns the number synced.
func (s *Service) SyncAwaitingVerification(ctx context.Context) (int, error) {
	list, err := s.d.Domains.ListAwaitingVerification(ctx, verificationPollLimit)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, dom := range list {
		if err := s.SyncDomainJob(ctx, dom.ID); err != nil {
			s.d.Log.WarnContext(ctx, "domain verification sync failed", "domain", dom.Name, "error", err)
			continue
		}
		count++
	}
	return count, nil
}

// Client

// uploadableStatuses are the states in which a client may still upload
// documents: provisionally active, suspended for lacking them, or held back
// by the registrar (pending / pending transfer) until they are verified.
func uploadable(dom *domain.Domain) bool {
	switch dom.Status {
	case domain.DomainActive, domain.DomainSuspended, domain.DomainPendingTransfer:
		return true
	case domain.DomainPending:
		return registrarOrderID(dom) != "" // already placed at the registrar
	}
	return false
}

// DocumentUploadLink returns the registrar-hosted page where the client
// uploads the documents the domain requires. The link is a bearer secret:
// it is fetched fresh per request, handed only to the owner, and never
// stored or audited with its value.
func (s *Service) DocumentUploadLink(ctx context.Context, clientID, domainID int64) (string, error) {
	dom, err := s.GetForClient(ctx, clientID, domainID)
	if err != nil {
		return "", err
	}
	switch {
	case !dom.DocumentRequired:
		return "", apperr.Conflict("this domain does not require documents")
	case dom.Verification == domain.VerificationActive:
		return "", apperr.Conflict("the documents for this domain are already verified")
	case !uploadable(dom):
		return "", apperr.Newf(apperr.CodeConflict, "documents cannot be uploaded for a %s domain", dom.Status)
	}
	link, err := s.d.Registrar.DocumentUploadLink(ctx, dom.Name)
	if err != nil {
		return "", err
	}
	s.d.Audit.Log(ctx, 0, "domain.document_link", "domain", dom.ID, nil, map[string]any{"name": dom.Name})
	return link, nil
}

// ResendVerification re-sends the registrant's verification email for the
// Indonesian TLDs that are verified by email (.id, .my.id, .biz.id, .web.id).
func (s *Service) ResendVerification(ctx context.Context, clientID, domainID int64) error {
	dom, err := s.GetForClient(ctx, clientID, domainID)
	if err != nil {
		return err
	}
	switch {
	case dom.DocumentRequired || !domain.NameRequiresEmailVerification(dom.Name):
		return apperr.Conflict("this domain is not verified by email")
	case dom.Verification == domain.VerificationActive:
		return apperr.Conflict("this domain is already verified")
	case dom.Status != domain.DomainActive && dom.Status != domain.DomainSuspended:
		return apperr.Newf(apperr.CodeConflict, "verification cannot be re-sent for a %s domain", dom.Status)
	}
	if err := s.d.Registrar.ResendVerification(ctx, dom.Name); err != nil {
		return err
	}
	s.d.Audit.Log(ctx, 0, "domain.verification_resend", "domain", dom.ID, nil, map[string]any{"name": dom.Name})
	return nil
}

// RefreshForClient pulls the registrar's current state of one of the client's
// own domains right now - the "I uploaded my documents, check again" button.
func (s *Service) RefreshForClient(ctx context.Context, clientID, domainID int64) (*domain.Domain, error) {
	if _, err := s.GetForClient(ctx, clientID, domainID); err != nil {
		return nil, err
	}
	if err := s.SyncDomainJob(ctx, domainID); err != nil {
		return nil, err
	}
	return s.d.Domains.GetByID(ctx, domainID)
}

// Admin

// AdminRefund credits a client what they paid for a domain the registrar
// rejected (or that was cancelled), when the automatic refund policy did not
// - typically a document failure, where the registrar itself refunds nothing
// but the operator may still choose to make the client whole. The amount
// defaults to what the client paid for the domain and can never exceed it; a
// domain is refunded at most once. Audited.
func (s *Service) AdminRefund(ctx context.Context, actorUserID, domainID int64, in AdminRefundRequest) (*domain.Domain, error) {
	var (
		dom    *domain.Domain
		amount int64
	)
	err := s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		locked, err := s.d.Domains.GetByIDForUpdate(txCtx, domainID)
		if err != nil {
			return err
		}
		if locked.Status != domain.DomainRejected && locked.Status != domain.DomainCancelled {
			return apperr.Conflict("only a rejected or cancelled domain can be refunded")
		}
		if locked.RefundedAt != nil {
			return apperr.Conflict("this domain was already refunded")
		}
		pay, err := s.d.Payments.RegistrationPayment(txCtx, domainID)
		if err != nil {
			if apperr.From(err).Code == apperr.CodeNotFound {
				return apperr.Conflict("no paid order was found for this domain - refund the invoice directly")
			}
			return err
		}
		if pay == nil || pay.Amount <= 0 {
			return apperr.Conflict("nothing was paid for this domain")
		}
		amount = pay.Amount
		if in.Amount != nil {
			if *in.Amount > pay.Amount {
				return apperr.Validation("amount exceeds what the client paid for this domain",
					apperr.FieldError{Field: "amount", Message: "at most " + domain.FormatIDR(pay.Amount)})
			}
			amount = *in.Amount
		}
		reason := "Domain refund: " + locked.Name
		if in.Reason != "" {
			reason += " (" + in.Reason + ")"
		}
		invoiceID := pay.InvoiceID
		if _, err := s.d.Clients.AdjustCredit(txCtx, locked.ClientID, amount, reason, &invoiceID); err != nil {
			return err
		}
		now := s.d.Clock.Now()
		locked.RefundAmount, locked.RefundedAt = amount, &now
		if err := s.d.Domains.Update(txCtx, locked); err != nil {
			return err
		}
		dom = locked
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "domain.refund", "domain", domainID, nil,
		map[string]any{"name": dom.Name, "amount": amount, "reason": in.Reason})
	if userID, err := s.clientUserID(ctx, dom.ClientID); err == nil {
		s.notifyWith(ctx, userID, tplDomainRejected, dom, map[string]any{
			"Refunded": true, "RefundAmount": domain.FormatIDR(amount), "DocumentFailure": false,
		})
	}
	return dom, nil
}
