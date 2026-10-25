package ports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
)

// Domain sniper ports: the registrar surface a hunt needs, the repository the
// worker supervisor claims rows from, and the narrow domains-module seam it
// uses to price and record a won domain.

// RegistrarCallError is the wire-level detail of a failed registrar call that
// apperr's coarse code drops (the rdash adapter attaches it as the apperr
// cause, so errors.As finds it through the chain). A hunt needs it to tell
// "taken" from "rate limited", "credentials rejected", "never left this
// process" and "the order may have gone through".
type RegistrarCallError struct {
	HTTPStatus int           // 0 when no HTTP response arrived or the call was never sent
	RetryAfter time.Duration // parsed Retry-After header, 0 when absent
	Message    string        // upstream message, without the adapter prefix
	NotSent    bool          // circuit breaker open: nothing went on the wire
	Ambiguous  bool          // the registrar may have acted (transport failure after send, 5xx, unreadable 2xx)
	Err        error         // underlying transport/decode error, if any
}

// Error implements error with a short, log-friendly summary.
func (e *RegistrarCallError) Error() string {
	switch {
	case e.NotSent:
		return "not sent: " + e.Message
	case e.HTTPStatus == 0:
		return fmt.Sprintf("no response: %s", e.Message)
	}
	s := fmt.Sprintf("http %d", e.HTTPStatus)
	if e.RetryAfter > 0 {
		s += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// Unwrap exposes the underlying transport/decode error.
func (e *RegistrarCallError) Unwrap() error { return e.Err }

// RegistrarCallErrorOf extracts the wire detail from err, or nil when err
// carries none (a validation error raised before any call, a nil error, ...).
func RegistrarCallErrorOf(err error) *RegistrarCallError {
	var ce *RegistrarCallError
	if errors.As(err, &ce) {
		return ce
	}
	return nil
}

// DomainSniper is the registrar surface the sniper needs. It is satisfied by
// a dedicated *rdash.Client (own circuit breaker, sampled logging) so a hunt
// can never open the breaker that guards paid checkout registrations.
type DomainSniper interface {
	// EnsureCustomer finds or creates the registrar-side customer for the
	// contact and returns its id, resolved once per hunt instead of on every
	// attempt.
	EnsureCustomer(ctx context.Context, c RegistrantContact) (int64, error)
	// IsAvailable is a single cheap availability probe (no price lookup).
	IsAvailable(ctx context.Context, name string) (bool, error)
	// RegisterForCustomer registers name under an already-resolved customer.
	RegisterForCustomer(ctx context.Context, name string, years int, customerID int64, ns []string) (*DomainResult, error)
	// SyncDomain reads the domain from OUR registrar account; NOT_FOUND means
	// it is not ours (the hunt's way to verify an ambiguous outcome).
	SyncDomain(ctx context.Context, name string) (*DomainSyncInfo, error)
	// AccountInfo is the reseller credential/balance preflight.
	AccountInfo(ctx context.Context) (*RegistrarAccountInfo, error)
}

// DomainQuote is the admin-configured selling price of a registration.
type DomainQuote struct {
	Total        int64 // whole-rupiah price of the whole term
	RenewPerYear int64 // base renewal rate frozen onto the resulting domain
}

// RecordSnipedDomainInput describes a domain a hunt just won at the registrar.
type RecordSnipedDomainInput struct {
	ClientID     int64
	Name         string // the hunted name; the registrar's echo can be empty
	Result       DomainResult
	RenewPerYear int64
	Nameservers  []string
}

// SnipeDomainService is the part of the domains module the sniper relies on.
// Implemented by domains.Service; RecordSnipedDomain joins the caller's
// transaction (it only uses the ctx-aware repositories).
type SnipeDomainService interface {
	// QuoteRegistration prices years of registration from admin pricing only
	// (premium override, length tier, TLD matrix) - never the registrar's live
	// cost - and fails VALIDATION when the TLD has no configured price.
	QuoteRegistration(ctx context.Context, name string, years int) (*DomainQuote, error)
	// RegistrantContactFor builds the registrar contact for a client,
	// VALIDATION when the profile lacks the address the registrar requires.
	RegistrantContactFor(ctx context.Context, clientID int64) (RegistrantContact, error)
	// RecordSnipedDomain inserts the active domain row for a won hunt.
	RecordSnipedDomain(ctx context.Context, in RecordSnipedDomainInput) (*domain.Domain, error)
}

// SnipeHeartbeat is what the owning supervisor writes back for one active
// hunt: it renews the lease and flushes the in-memory progress counters.
type SnipeHeartbeat struct {
	ID            int64
	Owner         string
	LeaseUntil    time.Time
	Attempts      int64
	Phase         domain.SnipePhase
	LastAttemptAt *time.Time
	LastError     string
}

// SnipeFinish is a terminal (or pending->active) transition request. The
// UPDATE only applies when the row is still in From, so concurrent writers
// (the hunt, an API cancel, the expiry sweep) cannot both win.
type SnipeFinish struct {
	ID           int64
	From         domain.SnipeStatus
	To           domain.SnipeStatus
	Owner        string    // non-empty: also require lease_owner = Owner (hunt-side fencing)
	RequireIdle  bool      // sweep: only when the lease is free or expired at Now
	Now          time.Time // clock reading used for won_at and the idle check
	RefundAmount int64     // credit-back decided by the lifecycle rules (0 = none)
	LastError    string    // overwrites last_error when non-empty
	WonDomainID  *int64
	Attempts     int64 // the hunt's final attempt count (kept if the stored one is higher)
}

// SnipePlanRepo persists the admin-priced hunt packages.
type SnipePlanRepo interface {
	Create(ctx context.Context, p *domain.SnipePlan) error
	GetByID(ctx context.Context, id int64) (*domain.SnipePlan, error)
	Update(ctx context.Context, p *domain.SnipePlan) error
	// List returns plans ordered by sort then id; activeOnly hides deactivated ones.
	List(ctx context.Context, activeOnly bool) ([]domain.SnipePlan, error)
}

// SnipeActivator reacts to a paid snipe invoice (flips the hunt to active).
// Implemented by the snipe module; consumed by billing.ProcessPaid, which runs
// it inside the payment transaction - so it must be fast, idempotent and make
// no external call.
type SnipeActivator interface {
	ActivateSnipeAfterPayment(ctx context.Context, snipeID int64) error
}

// InvoiceCanceller cancels an unpaid/overdue invoice (state-machine checked;
// a paid invoice is CONFLICT). Implemented by billing.
type InvoiceCanceller interface {
	CancelInvoice(ctx context.Context, actorUserID, invoiceID int64) (*domain.Invoice, error)
}

// SnipeRepo persists domain snipes and their activity trail.
type SnipeRepo interface {
	// Create inserts a snipe; a second open hunt for the same name is CONFLICT.
	Create(ctx context.Context, s *domain.DomainSnipe) error
	GetByID(ctx context.Context, id int64) (*domain.DomainSnipe, error)
	// List is the admin list (Status, Search on the domain name, ClientID).
	List(ctx context.Context, p ListParams) ([]domain.DomainSnipe, int64, error)
	// ListByClient is the client-area list, always scoped to one client.
	ListByClient(ctx context.Context, clientID int64, p ListParams) ([]domain.DomainSnipe, int64, error)
	// CountOpenByClient counts the client's pending_payment + active snipes.
	CountOpenByClient(ctx context.Context, clientID int64) (int, error)
	// CountByStatus is the admin dashboard tally.
	CountByStatus(ctx context.Context) (map[domain.SnipeStatus]int, error)
	// CountLeased counts active snipes a supervisor currently holds a live
	// lease on - the hunts actually running right now.
	CountLeased(ctx context.Context, now time.Time) (int, error)

	// Claim leases up to limit active snipes whose lease is free or expired
	// (SELECT ... FOR UPDATE SKIP LOCKED) to owner until leaseUntil.
	Claim(ctx context.Context, owner string, limit int, now, leaseUntil time.Time) ([]domain.DomainSnipe, error)
	// Heartbeat renews the lease and flushes progress; false means the lease
	// (or the active status) was lost and the hunt must stop.
	Heartbeat(ctx context.Context, h SnipeHeartbeat) (bool, error)
	// Release frees the owner's lease (graceful shutdown).
	Release(ctx context.Context, id int64, owner string) error
	// SetCustomerID caches the resolved registrar customer id on the row.
	SetCustomerID(ctx context.Context, id int64, customerID int64) error
	// Finish applies a guarded status transition; false = the row had already
	// left From (or the lease guard failed) and nothing changed.
	Finish(ctx context.Context, f SnipeFinish) (bool, error)
	// MarkRefunded stamps refunded_at on a finished snipe with a refund.
	MarkRefunded(ctx context.Context, id int64, at time.Time) error
	// RecordRefund stores a credit-back decided after the snipe had already
	// ended (a payment that arrived for a cancelled hunt): amount + refunded_at
	// in one step, only while nothing has been refunded yet. false = no change.
	RecordRefund(ctx context.Context, id int64, amount int64, at time.Time) (bool, error)
	// SetInvoice links the freshly created invoice and freezes the refundable
	// split of its total.
	SetInvoice(ctx context.Context, id, invoiceID, refundableDomain, refundableFee int64) error
	// Activate moves a paid snipe pending_payment -> active (CAS): paid_at is
	// set and the hunting deadline restarts from the payment (it keeps the
	// plan's window length). false = the row was no longer pending_payment.
	Activate(ctx context.Context, id int64, paidAt time.Time) (bool, error)
	// ListStalePending returns pending_payment snipes whose invoice was
	// cancelled or has been past its due date for a full day.
	ListStalePending(ctx context.Context, now time.Time, limit int) ([]domain.DomainSnipe, error)
	// ListExpirable returns active snipes past deadline_at whose lease is idle.
	ListExpirable(ctx context.Context, now time.Time, limit int) ([]domain.DomainSnipe, error)

	AddEvent(ctx context.Context, e *domain.SnipeEvent) error
	ListEvents(ctx context.Context, snipeID int64, limit int) ([]domain.SnipeEvent, error)
}
