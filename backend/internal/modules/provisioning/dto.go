package provisioning

import (
	"github.com/tsdlamongan/whcms/backend/internal/domain"
)

// Client-facing DTOs

// CancelModeImmediate / CancelModeEndOfTerm are the two cancellation modes.
const (
	CancelModeImmediate = "immediate"
	CancelModeEndOfTerm = "end_of_term"
)

// CancelServiceInput is the body of POST /services/:id/cancel.
type CancelServiceInput struct {
	Mode   string `json:"mode" validate:"required,oneof=immediate end_of_term"`
	Reason string `json:"reason" validate:"max=500"`
}

// ChangePasswordInput is the body of POST /services/:id/change-password.
type ChangePasswordInput struct {
	Password string `json:"password" validate:"required,min=12,max=64"`
}

// UpgradeServiceInput is the body of POST /services/:id/upgrade. Specs carries
// the customer's dynamic-spec choices when the target product is configurable
// (same shape as the order checkout's spec selections); knobs left out fall
// back to their default quantity.
type UpgradeServiceInput struct {
	ProductID int64               `json:"product_id" validate:"required,min=1"`
	Cycle     domain.BillingCycle `json:"cycle" validate:"required"`
	Specs     []UpgradeSpecInput  `json:"specs" validate:"omitempty,max=20,dive"`
}

// UpgradeSpecInput is a customer-chosen quantity for one dynamic spec knob of
// a configurable upgrade target (mirrors orders.SpecSelectionRequest).
type UpgradeSpecInput struct {
	Key       string `json:"key" validate:"required,min=1,max=50"`
	Qty       int64  `json:"qty" validate:"gte=0"`
	Unlimited bool   `json:"unlimited"`
}

// UpgradeResult is returned by UpgradeService. When the change requires
// payment, Invoice is the prorated diff invoice and Applied is false; a
// downgrade (or zero diff) is applied immediately and CreditIssued carries
// the excess returned as account credit.
type UpgradeResult struct {
	Applied      bool            `json:"applied"`
	ProratedDiff int64           `json:"prorated_diff"`
	CreditIssued int64           `json:"credit_issued"`
	Invoice      *domain.Invoice `json:"invoice,omitempty"`
	Service      *domain.Service `json:"service"`
}

// SSOResult is returned by GET /services/:id/sso.
type SSOResult struct {
	URL string `json:"url"`
}

// ServiceView is a domain.Service plus the display names of its referenced
// product and server, joined for the client/admin UIs (GET /services and
// /admin/services list + detail). Names are best-effort: an unresolvable
// reference (e.g. a soft-deleted product) leaves them empty and the UI falls
// back to "#<id>".
type ServiceView struct {
	domain.Service
	ProductName    string `json:"product_name,omitempty"`
	ServerName     string `json:"server_name,omitempty"`
	ServerHostname string `json:"server_hostname,omitempty"`
	// PendingCancellation is the service's pending cancellation request, if
	// any (set by GetService only - the bulk ListServices path leaves it nil
	// to avoid an extra query per row).
	PendingCancellation *domain.CancellationRequest `json:"pending_cancellation,omitempty"`
}

// CancellationRequestView is a domain.CancellationRequest plus the display
// names of its service/client, joined for the admin cancellation-requests
// list (GET /admin/services/cancellation-requests).
type CancellationRequestView struct {
	domain.CancellationRequest
	ServiceDomain string `json:"service_domain,omitempty"`
	ClientName    string `json:"client_name,omitempty"`
}

// Admin DTOs - services

// AdminActionInput is the shared body for admin service actions. When Async
// is true the action is enqueued as a background job instead of calling the
// server adapter synchronously.
type AdminActionInput struct {
	Async  bool   `json:"async"`
	Reason string `json:"reason" validate:"max=500"` // suspend only
}

// AdminChangePackageInput is the body of POST /admin/services/:id/change-package.
// ProductID optionally re-binds the service to another product (same module);
// the adapter is then told the (possibly new) product's package name.
type AdminChangePackageInput struct {
	Async     bool   `json:"async"`
	ProductID *int64 `json:"product_id" validate:"omitempty,min=1"`
}

// AdminChangePasswordInput is the body of POST /admin/services/:id/change-password.
type AdminChangePasswordInput struct {
	Password string `json:"password" validate:"required,min=12,max=64"`
}

// AdminUpdateServiceInput is the body of PATCH /admin/services/:id - a plain
// field editor for an admin to manually correct a service's record (e.g. a
// username a real control panel rejected as reserved). Product, password,
// and status changes deliberately go through their own dedicated endpoints
// (change-package, change-password, and the create/suspend/unsuspend/
// terminate module actions respectively) instead, since those also
// synchronize the control panel and, for status, validate a state-machine
// transition - this endpoint only ever performs a plain DB field update.
type AdminUpdateServiceInput struct {
	Domain           *string `json:"domain" validate:"omitempty,min=1,max=255"`
	Username         *string `json:"username" validate:"omitempty,min=1,max=64"`
	ServerID         *int64  `json:"server_id" validate:"omitempty,min=1"`
	BillingCycle     *string `json:"billing_cycle" validate:"omitempty,oneof=one_time monthly quarterly semiannually annually biennially"`
	RecurringAmount  *int64  `json:"recurring_amount" validate:"omitempty,min=0"`
	NextDueDate      *string `json:"next_due_date" validate:"omitempty,datetime=2006-01-02"`
	RegistrationDate *string `json:"registration_date" validate:"omitempty,datetime=2006-01-02"`
	TerminatedAt     *string `json:"terminated_at" validate:"omitempty,datetime=2006-01-02"`
	SuspendReason    *string `json:"suspend_reason" validate:"omitempty,max=500"`
	Notes            *string `json:"notes" validate:"omitempty,max=2000"`
}

// AdminCreateServiceInput is the body of POST /admin/services - the "add
// existing hosting" path: an admin records a service that already exists (a
// hosting account provisioned before this app, or one managed entirely by
// hand) directly on a client, with no order, no invoice, no payment, no
// provisioning job and no control-panel call. The row is created active;
// panel-bound actions (suspend/terminate/SSO/change-password) work as soon as
// server_id + username point at the real account. NextDueDate is required for
// recurring cycles so the imported service is immediately billable by renewal
// invoicing.
type AdminCreateServiceInput struct {
	ClientID         int64  `json:"client_id" validate:"required,min=1"`
	ProductID        int64  `json:"product_id" validate:"required,min=1"`
	ServerID         *int64 `json:"server_id" validate:"omitempty,min=1"`
	Domain           string `json:"domain" validate:"omitempty,max=255"`
	Username         string `json:"username" validate:"omitempty,max=64"`
	Password         string `json:"password" validate:"omitempty,min=8,max=64"`
	BillingCycle     string `json:"billing_cycle" validate:"required,oneof=one_time monthly quarterly semiannually annually biennially"`
	RecurringAmount  int64  `json:"recurring_amount" validate:"min=0"`
	NextDueDate      string `json:"next_due_date" validate:"omitempty,datetime=2006-01-02"`
	RegistrationDate string `json:"registration_date" validate:"omitempty,datetime=2006-01-02"`
	Notes            string `json:"notes" validate:"omitempty,max=2000"`
}

// Admin DTOs - servers & groups

// ServerInput creates or updates a server. Password/APIToken are plaintext
// secrets, encrypted before storage; empty values on update keep the stored
// secret unchanged.
type ServerInput struct {
	GroupID     *int64 `json:"group_id" validate:"omitempty,min=1"`
	Name        string `json:"name" validate:"required,max=100"`
	Module      string `json:"module" validate:"required,oneof=cpanel directadmin none"`
	Hostname    string `json:"hostname" validate:"required,hostname|ip,max=255"`
	Port        int    `json:"port" validate:"min=0,max=65535"`
	Username    string `json:"username" validate:"max=100"`
	Password    string `json:"password" validate:"max=200"`
	APIToken    string `json:"api_token" validate:"max=500"`
	UseSSL      *bool  `json:"use_ssl"`
	Nameserver1 string `json:"nameserver1" validate:"max=255"`
	Nameserver2 string `json:"nameserver2" validate:"max=255"`
	Nameserver3 string `json:"nameserver3" validate:"max=255"`
	Nameserver4 string `json:"nameserver4" validate:"max=255"`
	MaxAccounts int    `json:"max_accounts" validate:"min=0"`
	// PackagePrefix: some real WHM reseller accounts require every package
	// name to carry the reseller's own prefix (e.g. "reseller_"). Blank (the
	// default) means no prefix - a dedicated/non-reseller server's normal
	// behavior.
	PackagePrefix string `json:"package_prefix" validate:"max=50"`
	// IPAddress: DirectAdmin's CMD_API_ACCOUNT_USER requires a valid IP from
	// the reseller's own pool on every account create (unlike WHM's
	// createacct, which auto-assigns a shared IP when omitted). Blank is fine
	// for cPanel/WHM servers.
	IPAddress string `json:"ip_address" validate:"omitempty,ip,max=45"`
	Active    *bool  `json:"active"`
}

// ServerGroupInput creates or updates a server group.
type ServerGroupInput struct {
	Name     string `json:"name" validate:"required,max=100"`
	Strategy string `json:"strategy" validate:"required,oneof=round_robin least_used"`
}

// TestConnectionInput is the body of POST /admin/servers/test-connection - a
// pre-save connectivity probe driven by the current form values. When ID > 0
// (editing an existing server) any blank secret is filled from the stored
// (encrypted) value, so the admin can re-test without re-entering the token.
type TestConnectionInput struct {
	ID       int64  `json:"id" validate:"omitempty,min=1"`
	Module   string `json:"module" validate:"omitempty,oneof=cpanel directadmin none"`
	Hostname string `json:"hostname" validate:"omitempty,hostname|ip,max=255"`
	Port     int    `json:"port" validate:"min=0,max=65535"`
	Username string `json:"username" validate:"max=100"`
	Password string `json:"password" validate:"max=200"`
	APIToken string `json:"api_token" validate:"max=500"`
	UseSSL   *bool  `json:"use_ssl"`
}

// TestConnectionResult is returned by the server test-connection endpoints.
// Nameservers/Version/Hostname are best-effort metadata read from the panel for
// the admin UI to auto-populate; they are empty when the panel does not report
// them.
type TestConnectionResult struct {
	OK          bool     `json:"ok"`
	Message     string   `json:"message"`
	Version     string   `json:"version,omitempty"`
	Hostname    string   `json:"hostname,omitempty"`
	Nameservers []string `json:"nameservers,omitempty"`
}

// PackageListResult previews the packages/plans found on a representative
// server in a group, for the admin product form's package-name picker.
// OK=false (not an HTTP error) on a panel probe failure, so the UI can fall
// back to manual entry instead of showing a form error.
type PackageListResult struct {
	OK       bool     `json:"ok"`
	Message  string   `json:"message,omitempty"`
	Packages []string `json:"packages,omitempty"`
}
