package snipe

import (
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
)

// AdminCreateRequest is the body of POST /admin/snipes: start a free hunt for
// a domain on behalf of a client (the operator's own client account for
// personal use). No invoice is created and nothing is refunded.
//
// HuntFrom/HuntUntil are the optional drop window: the hunt only bursts the
// register call inside it (starting BurstLead earlier), and probes cheaply
// otherwise. DeadlineAt is when the hunt gives up; when omitted it is
// WindowDays (default 30) from now.
type AdminCreateRequest struct {
	ClientID    int64      `json:"client_id" validate:"required,min=1"`
	Domain      string     `json:"domain" validate:"required,max=253"`
	Years       int        `json:"years" validate:"omitempty,min=1,max=10"`
	HuntFrom    *time.Time `json:"hunt_from"`
	HuntUntil   *time.Time `json:"hunt_until"`
	DeadlineAt  *time.Time `json:"deadline_at"`
	WindowDays  int        `json:"window_days" validate:"omitempty,min=1,max=365"`
	Nameservers []string   `json:"nameservers" validate:"omitempty,max=4,dive,required"`
}

// AdminCancelRequest is the body of POST /admin/snipes/:id/cancel.
type AdminCancelRequest struct {
	// RefundFee also credits back the non-refundable service fee (the domain
	// price is always credited back for a paid hunt).
	RefundFee bool `json:"refund_fee"`
}

// Detail is one snipe plus its activity trail (newest event first).
type Detail struct {
	Snipe  *domain.DomainSnipe `json:"snipe"`
	Events []domain.SnipeEvent `json:"events"`
}

// StatusResponse is the admin overview: what the supervisor is doing and how
// the reseller account looks.
type StatusResponse struct {
	Enabled         bool           `json:"enabled"`
	PauseReason     string         `json:"pause_reason"`
	PausedAt        string         `json:"paused_at,omitempty"`
	Counts          map[string]int `json:"counts"`
	Hunting         int            `json:"hunting"`
	ResellerBalance *int64         `json:"reseller_balance"`
	ResellerError   string         `json:"reseller_error,omitempty"`
}

// QuoteRequest is the body of POST /snipe/quote: what a hunt for this domain
// on this plan would cost, without creating anything.
type QuoteRequest struct {
	Domain string `json:"domain" validate:"required,max=253"`
	PlanID int64  `json:"plan_id" validate:"required,min=1"`
	Years  int    `json:"years" validate:"omitempty,min=1,max=10"`
}

// Quote is the price breakdown of a hunt (whole rupiah). Everything except
// Fee and its tax share is credited back if the domain is not acquired.
type Quote struct {
	Domain           string  `json:"domain"`
	Years            int     `json:"years"`
	PlanID           int64   `json:"plan_id"`
	PlanName         string  `json:"plan_name"`
	WindowDays       int     `json:"window_days"`
	Fee              int64   `json:"fee"`
	DomainPrice      int64   `json:"domain_price"`
	SuccessFee       int64   `json:"success_fee"`
	Subtotal         int64   `json:"subtotal"`
	TaxRate          float64 `json:"tax_rate"`
	TaxInclusive     bool    `json:"tax_inclusive"`
	Tax              int64   `json:"tax"`
	Total            int64   `json:"total"`
	RefundableIfLost int64   `json:"refundable_if_lost"`
	RenewPerYear     int64   `json:"renew_per_year"`
}

// CreateRequest is the body of POST /snipes: start a paid hunt. HuntFrom /
// HuntUntil are the optional drop window (see AdminCreateRequest).
type CreateRequest struct {
	Domain      string     `json:"domain" validate:"required,max=253"`
	PlanID      int64      `json:"plan_id" validate:"required,min=1"`
	Years       int        `json:"years" validate:"omitempty,min=1,max=10"`
	HuntFrom    *time.Time `json:"hunt_from"`
	HuntUntil   *time.Time `json:"hunt_until"`
	Nameservers []string   `json:"nameservers" validate:"omitempty,max=4,dive,required"`
}

// ClientIdentity is who is acting from the client area: the user (audit,
// invoice cancellation) and the client profile every query is scoped to.
type ClientIdentity struct {
	UserID   int64
	ClientID int64
}

// ClientSnipe is a snipe as its owner sees it. The registrar's raw replies,
// the lease and other operator internals are deliberately left out.
type ClientSnipe struct {
	ID               int64              `json:"id"`
	DomainName       string             `json:"domain_name"`
	Years            int                `json:"years"`
	Status           domain.SnipeStatus `json:"status"`
	Phase            domain.SnipePhase  `json:"phase,omitempty"`
	HuntFrom         *time.Time         `json:"hunt_from"`
	HuntUntil        *time.Time         `json:"hunt_until"`
	Nameservers      []string           `json:"nameservers"`
	InvoiceID        *int64             `json:"invoice_id"`
	PlanID           *int64             `json:"plan_id"`
	PlanName         string             `json:"plan_name"`
	FeeAmount        int64              `json:"fee_amount"`
	DomainAmount     int64              `json:"domain_amount"`
	RefundableDomain int64              `json:"refundable_domain"`
	RefundableFee    int64              `json:"refundable_fee"`
	RefundAmount     int64              `json:"refund_amount"`
	RefundedAt       *time.Time         `json:"refunded_at"`
	Attempts         int64              `json:"attempts"`
	PaidAt           *time.Time         `json:"paid_at"`
	DeadlineAt       *time.Time         `json:"deadline_at"`
	WonAt            *time.Time         `json:"won_at"`
	WonDomainID      *int64             `json:"won_domain_id"`
	CreatedAt        time.Time          `json:"created_at"`
}

// ClientEvent is one trail entry shown to the owner: the kind and when. The
// free-text message stays operator-only (it can carry registrar internals).
type ClientEvent struct {
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
}

// ClientDetail is one snipe plus its owner-visible trail (newest first).
type ClientDetail struct {
	Snipe  ClientSnipe   `json:"snipe"`
	Events []ClientEvent `json:"events"`
}

// PlanView is a purchasable plan as clients see it.
type PlanView struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	WindowDays int    `json:"window_days"`
	Fee        int64  `json:"fee"`
	SuccessFee int64  `json:"success_fee"`
}

// PlanRequest is the body of POST/PUT /admin/snipe-plans.
type PlanRequest struct {
	Name       string `json:"name" validate:"required,max=100"`
	WindowDays int    `json:"window_days" validate:"required,min=1,max=365"`
	Fee        int64  `json:"fee" validate:"min=0,max=1000000000"`
	SuccessFee int64  `json:"success_fee" validate:"min=0,max=1000000000"`
	Active     *bool  `json:"active"`
	Sort       int    `json:"sort" validate:"min=0,max=100000"`
}
