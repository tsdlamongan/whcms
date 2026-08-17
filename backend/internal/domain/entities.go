package domain

import (
	"encoding/json"
	"time"
)

// User is an account able to log in (admin, staff or client owner).
type User struct {
	ID              int64           `json:"id"`
	Email           string          `json:"email"`
	PasswordHash    string          `json:"-"`
	Role            UserRole        `json:"role"`
	Status          UserStatus      `json:"status"`
	Permissions     json.RawMessage `json:"permissions"` // staff per-module map {"billing":true,...}
	TwoFASecretEnc  string          `json:"-"`
	TwoFAEnabled    bool            `json:"twofa_enabled"`
	Locale          string          `json:"locale"` // 'id' | 'en'
	EmailVerifiedAt *time.Time      `json:"email_verified_at"`
	LastLoginAt     *time.Time      `json:"last_login_at"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// Client is the billing profile attached to a user.
type Client struct {
	ID            int64        `json:"id"`
	UserID        int64        `json:"user_id"`
	FirstName     string       `json:"first_name"`
	LastName      string       `json:"last_name"`
	Company       string       `json:"company"`
	Address1      string       `json:"address1"`
	Address2      string       `json:"address2"`
	City          string       `json:"city"`
	State         string       `json:"state"`
	Postcode      string       `json:"postcode"`
	Country       string       `json:"country"`
	Phone         string       `json:"phone"`
	Currency      string       `json:"currency"`
	CreditBalance int64        `json:"credit_balance"`
	Status        ClientStatus `json:"status"`
	NotesAdmin    string       `json:"notes_admin"`
	DeletedAt     *time.Time   `json:"deleted_at"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// FullName returns "First Last" trimmed.
func (c Client) FullName() string {
	if c.FirstName == "" {
		return c.LastName
	}
	if c.LastName == "" {
		return c.FirstName
	}
	return c.FirstName + " " + c.LastName
}

// ClientContact is an additional contact person on a client account.
type ClientContact struct {
	ID        int64     `json:"id"`
	ClientID  int64     `json:"client_id"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Email     string    `json:"email"`
	Phone     string    `json:"phone"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProductGroup groups products in the catalog.
type ProductGroup struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Slug      string     `json:"slug"`
	Sort      int        `json:"sort"`
	Hidden    bool       `json:"hidden"`
	DeletedAt *time.Time `json:"deleted_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Product is a sellable item.
type Product struct {
	ID                   int64            `json:"id"`
	GroupID              int64            `json:"group_id"`
	Name                 string           `json:"name"`
	Slug                 string           `json:"slug"`
	Description          string           `json:"description"`
	Type                 ProductType      `json:"type"`
	Module               ServerModuleName `json:"module"`
	ServerGroupID        *int64           `json:"server_group_id"`
	PackageName          string           `json:"package_name"`
	AutoSetup            AutoSetup        `json:"auto_setup"`
	Configurable         bool             `json:"configurable"`     // customer configures specs (see product_specs)
	ShellAccess          bool             `json:"shell_access"`     // configurable products only: cPanel hasshell / DA ssh
	CGIAccess            bool             `json:"cgi_access"`       // configurable products only: cPanel cgi / DA cgi
	FeatureList          string           `json:"feature_list"`     // cPanel only: existing WHM Feature List name (live reference)
	TemplatePackage      string           `json:"template_package"` // DirectAdmin only: existing DA package cloned for long-tail fields
	StockEnabled         bool             `json:"stock_enabled"`
	StockQty             int              `json:"stock_qty"`
	Hidden               bool             `json:"hidden"`
	Sort                 int              `json:"sort"`
	WelcomeEmailTemplate string           `json:"welcome_email_template"`
	DeletedAt            *time.Time       `json:"deleted_at"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
}

// ProductPricing is the price of a product for one billing cycle.
type ProductPricing struct {
	ID        int64        `json:"id"`
	ProductID int64        `json:"product_id"`
	Cycle     BillingCycle `json:"cycle"`
	Price     int64        `json:"price"`
	SetupFee  int64        `json:"setup_fee"`
	Currency  string       `json:"currency"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// ConfigurableOptionGroup bundles options assignable to products.
type ConfigurableOptionGroup struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ConfigurableOption is one option within a group (e.g. "Extra RAM").
type ConfigurableOption struct {
	ID        int64     `json:"id"`
	GroupID   int64     `json:"group_id"`
	Name      string    `json:"name"`
	Sort      int       `json:"sort"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ConfigurableOptionValue is a selectable value with per-cycle price deltas.
type ConfigurableOptionValue struct {
	ID          int64           `json:"id"`
	OptionID    int64           `json:"option_id"`
	Name        string          `json:"name"`
	PriceDeltas json.RawMessage `json:"price_deltas"` // {"monthly": 10000, "annually": 100000}
	Sort        int             `json:"sort"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// Coupon is a discount code.
type Coupon struct {
	ID        int64           `json:"id"`
	Code      string          `json:"code"`
	Type      CouponType      `json:"type"`
	Value     int64           `json:"value"` // percentage (0-100) or fixed IDR
	AppliesTo json.RawMessage `json:"applies_to"`
	MaxUses   int             `json:"max_uses"`
	UsedCount int             `json:"used_count"`
	Recurring bool            `json:"recurring"`
	ExpiresAt *time.Time      `json:"expires_at"`
	Active    bool            `json:"active"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Order is a purchase request.
type Order struct {
	ID          int64       `json:"id"`
	OrderNumber string      `json:"order_number"`
	ClientID    int64       `json:"client_id"`
	Status      OrderStatus `json:"status"`
	Subtotal    int64       `json:"subtotal"`
	Discount    int64       `json:"discount"`
	TaxTotal    int64       `json:"tax_total"`
	Total       int64       `json:"total"`
	CouponID    *int64      `json:"coupon_id"`
	IP          string      `json:"ip"`
	Notes       string      `json:"notes"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// OrderItem is one line of an order.
type OrderItem struct {
	ID          int64           `json:"id"`
	OrderID     int64           `json:"order_id"`
	ItemType    OrderItemType   `json:"item_type"`
	ProductID   *int64          `json:"product_id"`
	Description string          `json:"description"`
	Domain      string          `json:"domain"`
	Cycle       BillingCycle    `json:"cycle"`
	UnitPrice   int64           `json:"unit_price"`
	SetupFee    int64           `json:"setup_fee"`
	Options     json.RawMessage `json:"options"`
	ServiceID   *int64          `json:"service_id"` // back-ref filled on activation
	DomainID    *int64          `json:"domain_id"`  // back-ref filled on activation
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// Invoice bills a client.
type Invoice struct {
	ID            int64         `json:"id"`
	InvoiceNumber string        `json:"invoice_number"`
	ClientID      int64         `json:"client_id"`
	Status        InvoiceStatus `json:"status"`
	Subtotal      int64         `json:"subtotal"`
	Discount      int64         `json:"discount"`
	TaxRate       float64       `json:"tax_rate"` // percent, e.g. 11.00
	TaxTotal      int64         `json:"tax_total"`
	CreditApplied int64         `json:"credit_applied"`
	Total         int64         `json:"total"`
	Currency      string        `json:"currency"`
	DueDate       time.Time     `json:"due_date"` // DATE column
	PaidAt        *time.Time    `json:"paid_at"`
	Notes         string        `json:"notes"`
	PDFObjectKey  string        `json:"pdf_object_key"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// InvoiceItem is one line of an invoice.
type InvoiceItem struct {
	ID          int64                  `json:"id"`
	InvoiceID   int64                  `json:"invoice_id"`
	Description string                 `json:"description"`
	Amount      int64                  `json:"amount"`
	Taxed       bool                   `json:"taxed"`
	RelatedType InvoiceItemRelatedType `json:"related_type"`
	RelatedID   *int64                 `json:"related_id"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// Transaction is a payment attempt/settlement against an invoice.
type Transaction struct {
	ID               int64             `json:"id"`
	InvoiceID        int64             `json:"invoice_id"`
	Gateway          Gateway           `json:"gateway"`
	MethodCode       string            `json:"method_code"`
	MerchantOrderID  *string           `json:"merchant_order_id"`
	GatewayReference string            `json:"gateway_reference"`
	Amount           int64             `json:"amount"`
	Fee              int64             `json:"fee"`
	Status           TransactionStatus `json:"status"`
	Raw              json.RawMessage   `json:"raw"`
	PaidAt           *time.Time        `json:"paid_at"`
	// ExpiresAt is when a pending gateway transaction's VA/QRIS/instructions
	// stop being valid (nil for non-expiring gateways/settled transactions).
	// Lets a page reload resume the SAME pending instructions instead of
	// silently starting a new gateway transaction on every visit.
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// CreditLedgerEntry records a change to a client's credit balance.
type CreditLedgerEntry struct {
	ID               int64     `json:"id"`
	ClientID         int64     `json:"client_id"`
	Delta            int64     `json:"delta"`
	BalanceAfter     int64     `json:"balance_after"`
	Reason           string    `json:"reason"`
	RelatedInvoiceID *int64    `json:"related_invoice_id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ServerGroup groups provisioning servers.
type ServerGroup struct {
	ID        int64               `json:"id"`
	Name      string              `json:"name"`
	Strategy  ServerGroupStrategy `json:"strategy"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

// Server is a provisioning target (WHM/DirectAdmin host).
type Server struct {
	ID          int64            `json:"id"`
	GroupID     *int64           `json:"group_id"`
	Name        string           `json:"name"`
	Module      ServerModuleName `json:"module"`
	Hostname    string           `json:"hostname"`
	Port        int              `json:"port"`
	Username    string           `json:"username"`
	PasswordEnc string           `json:"-"`
	APITokenEnc string           `json:"-"`
	UseSSL      bool             `json:"use_ssl"`
	Nameserver1 string           `json:"nameserver1"`
	Nameserver2 string           `json:"nameserver2"`
	Nameserver3 string           `json:"nameserver3"`
	Nameserver4 string           `json:"nameserver4"`
	MaxAccounts int              `json:"max_accounts"`
	// PackagePrefix is prepended to every dynamic control-panel package name
	// created on this server (DynamicPackageName) - required by some real WHM
	// reseller accounts, which silently rename/reject a package that doesn't
	// already carry their own prefix. Blank = no prefix (the default; matches
	// a dedicated/non-reseller server's behavior).
	PackagePrefix string `json:"package_prefix"`
	// IPAddress is the account IP passed to CreateAccountParams.IP on account
	// creation. Real DirectAdmin's CMD_API_ACCOUNT_USER requires a valid IP
	// from the reseller's own pool - unlike WHM's createacct, which silently
	// auto-assigns a shared IP when the ip param is omitted. Blank (the
	// default) is fine for cPanel/WHM servers, which don't need one.
	IPAddress string    `json:"ip_address"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Service is a provisioned (or to-be-provisioned) hosting service.
type Service struct {
	ID               int64           `json:"id"`
	ClientID         int64           `json:"client_id"`
	OrderItemID      *int64          `json:"order_item_id"`
	ProductID        int64           `json:"product_id"`
	ServerID         *int64          `json:"server_id"`
	Domain           string          `json:"domain"`
	Username         string          `json:"username"`
	PasswordEnc      string          `json:"-"`
	Status           ServiceStatus   `json:"status"`
	BillingCycle     BillingCycle    `json:"billing_cycle"`
	RecurringAmount  int64           `json:"recurring_amount"`
	SetupFee         int64           `json:"setup_fee"`
	NextDueDate      *time.Time      `json:"next_due_date"`     // DATE
	RegistrationDate *time.Time      `json:"registration_date"` // DATE
	TerminatedAt     *time.Time      `json:"terminated_at"`
	SuspendReason    string          `json:"suspend_reason"`
	PanelMeta        json.RawMessage `json:"panel_meta"`
	CouponID         *int64          `json:"coupon_id"`       // recurring coupon re-applied on renewals
	PendingUpgrade   json.RawMessage `json:"pending_upgrade"` // ServiceUpgrade JSON, nil when none
	Notes            string          `json:"notes"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// ServiceUpgrade is the JSON payload stored in services.pending_upgrade while
// an upgrade/downgrade awaits payment (invoice item related_type=service_upgrade).
// Specs carries the customer's dynamic-spec choices when the target product is
// configurable; ApplyUpgrade copies it into panel_meta[chosen_specs] so the
// panel package is rebuilt from the new configuration.
type ServiceUpgrade struct {
	ProductID       int64         `json:"product_id"`
	Cycle           BillingCycle  `json:"cycle"`
	RecurringAmount int64         `json:"recurring_amount"`
	InvoiceID       int64         `json:"invoice_id"`
	Specs           []UpgradeSpec `json:"specs,omitempty"`
}

// UpgradeSpec is one resolved, priced dynamic-spec choice inside a pending
// ServiceUpgrade. Its JSON shape mirrors the chosen_specs snapshot order
// activation writes to services.panel_meta (orders.SpecSelection), so
// provisioning reads both through the same decoder. Qty is in the spec unit;
// UnlimitedQty (-1) marks an unlimited choice. Amount is the per-cycle IDR the
// choice adds on top of the product's base price.
type UpgradeSpec struct {
	Key          string `json:"key"`
	ProvisionKey string `json:"provision_key"`
	Unit         string `json:"unit"`
	Qty          int64  `json:"qty"`
	Unlimited    bool   `json:"unlimited,omitempty"`
	Amount       int64  `json:"amount"`
}

// CancellationRequest is a client-submitted request to cancel a service.
// Immediate-mode requests are recorded already CancellationAutoProcessed (no
// admin action needed - the cancellation itself already ran synchronously);
// end_of_term requests start CancellationPending and only take effect
// (panel_meta.cancel_at_period_end set) once an admin accepts them.
type CancellationRequest struct {
	ID          int64                     `json:"id"`
	ServiceID   int64                     `json:"service_id"`
	ClientID    int64                     `json:"client_id"`
	Mode        string                    `json:"mode"` // "immediate" | "end_of_term"
	Reason      string                    `json:"reason"`
	Status      CancellationRequestStatus `json:"status"`
	RequestedAt time.Time                 `json:"requested_at"`
	DecidedAt   *time.Time                `json:"decided_at"`
	DecidedBy   *int64                    `json:"decided_by"`
	CreatedAt   time.Time                 `json:"created_at"`
	UpdatedAt   time.Time                 `json:"updated_at"`
}

// Registrar is a domain registrar configuration row. ResellerID/APIKeyEnc are
// admin-configurable dynamic credentials (env vars are the fallback for
// whichever of them is blank) - APIKeyEnc is AES-256-GCM ciphertext, never
// serialized, decrypted ad-hoc at the call site (see ports.Encryptor).
type Registrar struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Active     bool            `json:"active"`
	Config     json.RawMessage `json:"config"` // non-secret config only
	ResellerID string          `json:"reseller_id"`
	APIKeyEnc  string          `json:"-"`
	BaseURL    string          `json:"base_url"` // "custom endpoint"; blank = adapter falls back to RDASH_BASE_URL
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// Domain is a registered/managed domain name.
type Domain struct {
	ID                     int64           `json:"id"`
	ClientID               int64           `json:"client_id"`
	RegistrarID            int64           `json:"registrar_id"`
	Name                   string          `json:"name"`
	Status                 DomainStatus    `json:"status"`
	RegistrationDate       *time.Time      `json:"registration_date"` // DATE
	ExpiryDate             *time.Time      `json:"expiry_date"`       // DATE
	NextDueDate            *time.Time      `json:"next_due_date"`     // DATE
	RecurringAmount        int64           `json:"recurring_amount"`
	BillingCycle           BillingCycle    `json:"billing_cycle"`
	AutoRenew              bool            `json:"auto_renew"`
	Nameservers            json.RawMessage `json:"nameservers"`
	EPPCodeEnc             string          `json:"-"`
	IDProtection           bool            `json:"id_protection"`
	DNSManagementEnabled   bool            `json:"dns_management_enabled"`
	EmailForwardingEnabled bool            `json:"email_forwarding_enabled"`
	RegistrarMeta          json.RawMessage `json:"registrar_meta"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

// TLDPricing is the admin-configured selling price for one TLD/extension -
// a full 1-10 year register/renew matrix (RegisterPrices/RenewPrices keyed by
// year as a string, e.g. "1", "2") plus a flat transfer price.
type TLDPricing struct {
	ID             int64            `json:"id"`
	TLD            string           `json:"tld"` // no leading dot, e.g. "com", "co.id"
	RegistrarID    int64            `json:"registrar_id"`
	Active         bool             `json:"active"`
	MinYears       int              `json:"min_years"`
	MaxYears       int              `json:"max_years"`
	RegisterPrices map[string]int64 `json:"register_prices"`
	RenewPrices    map[string]int64 `json:"renew_prices"`
	TransferPrice  int64            `json:"transfer_price"`
	// RestorePrice is the flat redemption/restore fee for reactivating an
	// expired domain past its registry grace period (mirrors RDash/Dewabiz's
	// `redemption` field on GET /account/prices). Captured for display/
	// reference only - no functional restore workflow consumes it yet.
	RestorePrice int64     `json:"restore_price"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PremiumDomainPricing is a manually-curated price override for one exact
// domain name, taking precedence over its TLD's standard pricing.
type PremiumDomainPricing struct {
	ID            int64     `json:"id"`
	DomainName    string    `json:"domain_name"`
	RegisterPrice int64     `json:"register_price"`
	RenewPrice    int64     `json:"renew_price"`
	TransferPrice int64     `json:"transfer_price"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PremiumLengthPricing is a premium price tier keyed by TLD + registrable-
// label character length (e.g. Dewabiz's "Limited Character" table: .id
// 2-char/3-char/4-char each carry a distinct price). One flat Price covers
// register/renew/transfer for that tier. Takes precedence over standard TLD
// pricing but below an exact PremiumDomainPricing override.
type PremiumLengthPricing struct {
	ID         int64     `json:"id"`
	TLD        string    `json:"tld"` // no leading dot, e.g. "id", "co.id"
	CharLength int       `json:"char_length"`
	Price      int64     `json:"price"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// DomainAddonKey enumerates the fixed set of purchasable domain addons.
type DomainAddonKey string

const (
	DomainAddonIDProtection    DomainAddonKey = "id_protection"
	DomainAddonDNSManagement   DomainAddonKey = "dns_management"
	DomainAddonEmailForwarding DomainAddonKey = "email_forwarding"
)

// DomainAddon is one row of the fixed domain-addon catalog (ID Protection,
// DNS Management, Email Forwarding) - admin controls price/active only.
type DomainAddon struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	Price     int64     `json:"price"` // annual, whole IDR
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DNSRecordRow is an optionally cached DNS record.
type DNSRecordRow struct {
	ID        int64     `json:"id"`
	DomainID  int64     `json:"domain_id"`
	Type      string    `json:"type"`
	Host      string    `json:"host"`
	Value     string    `json:"value"`
	TTL       int       `json:"ttl"`
	Prio      int       `json:"prio"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TicketDepartment is a support department.
type TicketDepartment struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Active    bool      `json:"active"`
	Sort      int       `json:"sort"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Ticket is a support ticket.
type Ticket struct {
	ID             int64          `json:"id"`
	TicketNumber   string         `json:"ticket_number"`
	ClientID       *int64         `json:"client_id"`
	DepartmentID   int64          `json:"department_id"`
	Subject        string         `json:"subject"`
	Status         TicketStatus   `json:"status"`
	Priority       TicketPriority `json:"priority"`
	AssignedUserID *int64         `json:"assigned_user_id"`
	GuestName      string         `json:"guest_name,omitempty"`  // set only for guest (unauthenticated) tickets
	GuestEmail     string         `json:"guest_email,omitempty"` // set only for guest (unauthenticated) tickets
	LastReplyAt    *time.Time     `json:"last_reply_at"`
	ClosedAt       *time.Time     `json:"closed_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// TicketAttachment describes a stored ticket attachment.
type TicketAttachment struct {
	ObjectKey   string `json:"object_key"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// TicketReply is one message in a ticket thread.
type TicketReply struct {
	ID          int64           `json:"id"`
	TicketID    int64           `json:"ticket_id"`
	UserID      *int64          `json:"user_id"`
	AuthorName  string          `json:"author_name"`
	Message     string          `json:"message"`
	IsInternal  bool            `json:"is_internal"`
	Attachments json.RawMessage `json:"attachments"` // []TicketAttachment
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// EmailTemplate is a localized email template.
type EmailTemplate struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	Locale    string    `json:"locale"`
	Subject   string    `json:"subject"`
	BodyHTML  string    `json:"body_html"`
	BodyText  string    `json:"body_text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EmailLogEntry records an outbound email. UserID is the recipient account
// holder when known (set by SendTemplate; nil for AlertAdmin/SendTestEmail,
// which aren't addressed to a specific account) - it's what lets a user view
// their own delivery history.
type EmailLogEntry struct {
	ID          int64       `json:"id"`
	UserID      *int64      `json:"user_id"`
	ToEmail     string      `json:"to_email"`
	TemplateKey string      `json:"template_key"`
	Subject     string      `json:"subject"`
	Status      EmailStatus `json:"status"`
	Error       string      `json:"error"`
	SentAt      *time.Time  `json:"sent_at"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Setting is one settings row (JSONB value).
type Setting struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// AuditLog records a sensitive action.
type AuditLog struct {
	ID        int64           `json:"id"`
	UserID    *int64          `json:"user_id"`
	Action    string          `json:"action"`
	Entity    string          `json:"entity"`
	EntityID  int64           `json:"entity_id"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	IP        string          `json:"ip"`
	CreatedAt time.Time       `json:"created_at"`
}

// IntegrationLog records one external API call (redacted).
type IntegrationLog struct {
	ID         int64           `json:"id"`
	Provider   string          `json:"provider"`
	Endpoint   string          `json:"endpoint"`
	Method     string          `json:"method"`
	StatusCode int             `json:"status_code"`
	Success    bool            `json:"success"`
	LatencyMS  int64           `json:"latency_ms"`
	Request    json.RawMessage `json:"request"`
	Response   json.RawMessage `json:"response"`
	Error      string          `json:"error"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Announcement is a published (or draft) portal announcement / news post.
type Announcement struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Body        string     `json:"body"`
	Published   bool       `json:"published"`
	PublishedAt *time.Time `json:"published_at"`
	AuthorID    *int64     `json:"author_id"`
	DeletedAt   *time.Time `json:"deleted_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// KBCategory groups knowledgebase articles.
type KBCategory struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Description string     `json:"description"`
	Sort        int        `json:"sort"`
	Hidden      bool       `json:"hidden"`
	DeletedAt   *time.Time `json:"deleted_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// KBArticle is one knowledgebase article within a category.
type KBArticle struct {
	ID         int64      `json:"id"`
	CategoryID int64      `json:"category_id"`
	Title      string     `json:"title"`
	Slug       string     `json:"slug"`
	Body       string     `json:"body"`
	Published  bool       `json:"published"`
	Views      int64      `json:"views"`
	Sort       int        `json:"sort"`
	AuthorID   *int64     `json:"author_id"`
	DeletedAt  *time.Time `json:"deleted_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// NetworkIssue is one network status entry (scheduled maintenance, issue or
// outage) shown on the network status page.
type NetworkIssue struct {
	ID        int64                `json:"id"`
	Title     string               `json:"title"`
	Body      string               `json:"body"`
	Type      NetworkIssueType     `json:"type"`
	Severity  NetworkIssueSeverity `json:"severity"`
	Status    NetworkIssueStatus   `json:"status"`
	Affected  string               `json:"affected"`
	StartsAt  time.Time            `json:"starts_at"`
	EndsAt    *time.Time           `json:"ends_at"`
	DeletedAt *time.Time           `json:"deleted_at"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}
