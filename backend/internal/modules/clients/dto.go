package clients

import (
	"encoding/json"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
)

// Admin CRUD inputs

// CreateClientInput creates a user (role client) plus its client profile.
type CreateClientInput struct {
	Email     string `json:"email" validate:"required,email"`
	Password  string `json:"password" validate:"required,min=8"`
	FirstName string `json:"first_name" validate:"required,max=100"`
	LastName  string `json:"last_name" validate:"required,max=100"`
	Company   string `json:"company" validate:"max=200"`
	Address1  string `json:"address1" validate:"max=200"`
	Address2  string `json:"address2" validate:"max=200"`
	City      string `json:"city" validate:"max=100"`
	State     string `json:"state" validate:"max=100"`
	Postcode  string `json:"postcode" validate:"max=20"`
	Country   string `json:"country" validate:"omitempty,len=2"`
	Phone     string `json:"phone" validate:"max=32"`
}

// UpdateClientInput patches a client profile (admin side); nil = unchanged.
type UpdateClientInput struct {
	FirstName  *string `json:"first_name" validate:"omitempty,max=100"`
	LastName   *string `json:"last_name" validate:"omitempty,max=100"`
	Company    *string `json:"company" validate:"omitempty,max=200"`
	Address1   *string `json:"address1" validate:"omitempty,max=200"`
	Address2   *string `json:"address2" validate:"omitempty,max=200"`
	City       *string `json:"city" validate:"omitempty,max=100"`
	State      *string `json:"state" validate:"omitempty,max=100"`
	Postcode   *string `json:"postcode" validate:"omitempty,max=20"`
	Country    *string `json:"country" validate:"omitempty,len=2"`
	Phone      *string `json:"phone" validate:"omitempty,max=32"`
	NotesAdmin *string `json:"notes_admin" validate:"omitempty,max=10000"`
}

// UpdateProfileInput is the client-facing profile patch (address fields only;
// identity/email/password changes go through /auth/me).
type UpdateProfileInput struct {
	FirstName *string `json:"first_name" validate:"omitempty,max=100"`
	LastName  *string `json:"last_name" validate:"omitempty,max=100"`
	Company   *string `json:"company" validate:"omitempty,max=200"`
	Address1  *string `json:"address1" validate:"omitempty,max=200"`
	Address2  *string `json:"address2" validate:"omitempty,max=200"`
	City      *string `json:"city" validate:"omitempty,max=100"`
	State     *string `json:"state" validate:"omitempty,max=100"`
	Postcode  *string `json:"postcode" validate:"omitempty,max=20"`
	Country   *string `json:"country" validate:"omitempty,len=2"`
	Phone     *string `json:"phone" validate:"omitempty,max=32"`
}

// SetStatusInput changes a client's status.
type SetStatusInput struct {
	Status string `json:"status" validate:"required,oneof=active inactive closed"`
}

// ContactInput creates/updates a client contact (sub-account without login).
// Permissions is the client-area per-area access map the account UI already
// sends/renders (`invoices`, `services`, `domains`, `tickets` -
// docs/RECONCILE.md's client-area contract); unknown keys are rejected by
// the service (see KnownContactPermissions).
type ContactInput struct {
	FirstName   string          `json:"first_name" validate:"required,max=100"`
	LastName    string          `json:"last_name" validate:"max=100"`
	Email       string          `json:"email" validate:"omitempty,email"`
	Phone       string          `json:"phone" validate:"max=32"`
	Permissions map[string]bool `json:"permissions"`
}

// Contact is a client_contacts row plus its per-area permissions map. The
// permissions JSONB column intentionally isn't part of domain.ClientContact
// (kept a minimal CONTRACTS-owned entity shared across modules), so this
// module-local wrapper carries it end to end - same embedding pattern as
// ClientWithEmail below.
type Contact struct {
	domain.ClientContact
	Permissions json.RawMessage `json:"permissions"`
}

// AdjustCreditInput is the admin manual credit adjustment (delta may be
// negative to deduct).
type AdjustCreditInput struct {
	Delta  int64  `json:"delta" validate:"required"`
	Reason string `json:"reason" validate:"required,max=500"`
}

// MinDepositAmount is the minimum deposit in whole IDR (MODULES §3 M-CLIENTS).
const MinDepositAmount int64 = 10000

// DepositInput creates a credit deposit invoice.
type DepositInput struct {
	Amount int64 `json:"amount" validate:"required,gte=10000"`
}

// Search / listing

// SearchInput filters the admin client search.
type SearchInput struct {
	Search     string  // ILIKE across name/company (search_name) + users.email
	Status     string  // exact client status; empty = all
	HasProduct bool    // only clients with at least one service
	IDs        []int64 // restrict to these client ids (empty = all); lets list pages resolve names in one call
	Page       int
	PerPage    int
}

// limit returns the SQL limit with defaults/caps applied.
func (f SearchInput) limit() int {
	if f.PerPage < 1 {
		return 25
	}
	if f.PerPage > ports.MaxPerPage {
		return ports.MaxPerPage
	}
	return f.PerPage
}

// offset returns the SQL offset.
func (f SearchInput) offset() int {
	if f.Page < 1 {
		f.Page = 1
	}
	return (f.Page - 1) * f.limit()
}

// ClientListRow is one row of the admin client list (client + user email;
// only indexed/listed columns - no SELECT *).
type ClientListRow struct {
	ID            int64               `json:"id"`
	Email         string              `json:"email"`
	FirstName     string              `json:"first_name"`
	LastName      string              `json:"last_name"`
	Company       string              `json:"company"`
	Country       string              `json:"country"`
	Phone         string              `json:"phone"`
	Status        domain.ClientStatus `json:"status"`
	CreditBalance int64               `json:"credit_balance"`
	CreatedAt     time.Time           `json:"created_at"`
}

// ClientWithEmail is a client profile with the owning user's email attached
// (returned by admin create).
type ClientWithEmail struct {
	domain.Client
	Email string `json:"email"`
}

// Detail aggregate

// ClientCounts are the per-client entity counters on the detail page.
type ClientCounts struct {
	Services       int64 `json:"services"`
	ServicesActive int64 `json:"services_active"`
	Domains        int64 `json:"domains"`
	Invoices       int64 `json:"invoices"`
	InvoicesUnpaid int64 `json:"invoices_unpaid"`
	UnpaidTotal    int64 `json:"unpaid_total"`
	Tickets        int64 `json:"tickets"`
	TicketsOpen    int64 `json:"tickets_open"`
	Transactions   int64 `json:"transactions"`
}

// ServiceSummary is one recent service on the client detail page.
type ServiceSummary struct {
	ID              int64                `json:"id"`
	ProductName     string               `json:"product_name"`
	Domain          string               `json:"domain"`
	Status          domain.ServiceStatus `json:"status"`
	BillingCycle    domain.BillingCycle  `json:"billing_cycle"`
	RecurringAmount int64                `json:"recurring_amount"`
	NextDueDate     *time.Time           `json:"next_due_date"`
}

// DomainSummary is one recent domain on the client detail page.
type DomainSummary struct {
	ID              int64               `json:"id"`
	Name            string              `json:"name"`
	Status          domain.DomainStatus `json:"status"`
	ExpiryDate      *time.Time          `json:"expiry_date"`
	NextDueDate     *time.Time          `json:"next_due_date"`
	RecurringAmount int64               `json:"recurring_amount"`
}

// InvoiceSummary is one recent invoice on the client detail page.
type InvoiceSummary struct {
	ID            int64                `json:"id"`
	InvoiceNumber string               `json:"invoice_number"`
	Status        domain.InvoiceStatus `json:"status"`
	Total         int64                `json:"total"`
	DueDate       time.Time            `json:"due_date"`
	CreatedAt     time.Time            `json:"created_at"`
}

// TicketSummary is one recent ticket on the client detail page.
type TicketSummary struct {
	ID           int64                 `json:"id"`
	TicketNumber string                `json:"ticket_number"`
	Subject      string                `json:"subject"`
	Status       domain.TicketStatus   `json:"status"`
	Priority     domain.TicketPriority `json:"priority"`
	LastReplyAt  *time.Time            `json:"last_reply_at"`
}

// TransactionSummary is one recent transaction on the client detail page.
type TransactionSummary struct {
	ID        int64                    `json:"id"`
	InvoiceID int64                    `json:"invoice_id"`
	Gateway   domain.Gateway           `json:"gateway"`
	Amount    int64                    `json:"amount"`
	Status    domain.TransactionStatus `json:"status"`
	CreatedAt time.Time                `json:"created_at"`
}

// ClientRecent bundles the recent-entity lists of the detail aggregate.
type ClientRecent struct {
	Services     []ServiceSummary     `json:"services"`
	Domains      []DomainSummary      `json:"domains"`
	Invoices     []InvoiceSummary     `json:"invoices"`
	Tickets      []TicketSummary      `json:"tickets"`
	Transactions []TransactionSummary `json:"transactions"`
}

// ClientDetail is the admin client detail aggregate.
type ClientDetail struct {
	Client      domain.Client `json:"client"`
	Email       string        `json:"email"`
	LastLoginAt *time.Time    `json:"last_login_at"`
	Counts      ClientCounts  `json:"counts"`
	Recent      ClientRecent  `json:"recent"`
}

// CreditOverview is the client-facing credit payload (balance + ledger page).
type CreditOverview struct {
	Balance int64                      `json:"balance"`
	Entries []domain.CreditLedgerEntry `json:"entries"`
}

// CSV export

// ExportRow is one row of the streamed clients CSV export.
type ExportRow struct {
	ID            int64
	Email         string
	FirstName     string
	LastName      string
	Company       string
	City          string
	Country       string
	Phone         string
	Status        string
	CreditBalance int64
	CreatedAt     time.Time
}
