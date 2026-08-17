// Package ports declares every interface used across WHCMS: repositories,
// external integrations, infrastructure and cross-service ports, plus their
// request/result structs (CONTRACTS.md §6). Module agents import this package
// and depend only on interfaces; implementations live in internal/repository,
// internal/integration/* and internal/platform/*.
package ports

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
)

// Transactions

// TxManager runs fn inside a database transaction. The transaction is stored
// in the returned context; repositories pick it up via platform/db.Querier(ctx)
// so nested repo calls automatically join the transaction.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repositories

// ListParams is the common pagination/filter input for list queries.
type ListParams struct {
	Page    int
	PerPage int
	Search  string
	Status  string
	Sort    string // column name, prefix '-' for DESC
	// Gateway filters TransactionRepo.List by domain.Gateway code (e.g.
	// "manual") - unused by every other List caller, same convention as
	// Status being reused across unrelated domains' status enums.
	Gateway string
	// UserID filters EmailLogRepo.List to one recipient account (0 = unset);
	// unused by every other List caller, same convention as Gateway.
	UserID int64
	// ServiceID filters CancellationRequestRepo.List to one service (0 =
	// unset); unused by every other List caller, same convention as Gateway.
	ServiceID int64
}

// Offset returns the SQL offset.
func (p ListParams) Offset() int {
	if p.Page < 1 {
		p.Page = 1
	}
	return (p.Page - 1) * p.Limit()
}

// Limit returns the SQL limit with defaults applied.
func (p ListParams) Limit() int {
	if p.PerPage < 1 {
		return 25
	}
	if p.PerPage > 100 {
		return 100
	}
	return p.PerPage
}

// UserRepo persists users.
type UserRepo interface {
	Create(ctx context.Context, u *domain.User) error
	GetByID(ctx context.Context, id int64) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	Update(ctx context.Context, u *domain.User) error
	UpdatePassword(ctx context.Context, id int64, passwordHash string) error
	SetEmailVerified(ctx context.Context, id int64, at time.Time) error
	SetLastLogin(ctx context.Context, id int64, at time.Time) error
	List(ctx context.Context, p ListParams) ([]domain.User, int64, error)
	Delete(ctx context.Context, id int64) error
	// ExistsAnyAdmin reports whether any role='admin' user exists - the
	// installation wizard's definition of "installed" (docs/CONTRACTS.md §15).
	ExistsAnyAdmin(ctx context.Context) (bool, error)
}

// ClientRepo persists client profiles.
type ClientRepo interface {
	Create(ctx context.Context, c *domain.Client) error
	GetByID(ctx context.Context, id int64) (*domain.Client, error)
	GetByUserID(ctx context.Context, userID int64) (*domain.Client, error)
	Update(ctx context.Context, c *domain.Client) error
	List(ctx context.Context, p ListParams) ([]domain.Client, int64, error)
	SoftDelete(ctx context.Context, id int64) error
	// AdjustCredit atomically applies delta to credit_balance and writes a
	// ledger row; returns the new balance. Fails if result would be negative.
	AdjustCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID *int64) (int64, error)
}

// ProductRepo persists products, groups, pricing and configurable options.
type ProductRepo interface {
	// Products
	Create(ctx context.Context, pr *domain.Product) error
	GetByID(ctx context.Context, id int64) (*domain.Product, error)
	GetBySlug(ctx context.Context, slug string) (*domain.Product, error)
	Update(ctx context.Context, pr *domain.Product) error
	List(ctx context.Context, p ListParams) ([]domain.Product, int64, error)
	SoftDelete(ctx context.Context, id int64) error
	// Groups
	CreateGroup(ctx context.Context, g *domain.ProductGroup) error
	GetGroupByID(ctx context.Context, id int64) (*domain.ProductGroup, error)
	UpdateGroup(ctx context.Context, g *domain.ProductGroup) error
	ListGroups(ctx context.Context, includeHidden bool) ([]domain.ProductGroup, error)
	SoftDeleteGroup(ctx context.Context, id int64) error
	// Pricing
	UpsertPricing(ctx context.Context, pp *domain.ProductPricing) error
	GetPricing(ctx context.Context, productID int64, cycle domain.BillingCycle) (*domain.ProductPricing, error)
	ListPricing(ctx context.Context, productID int64) ([]domain.ProductPricing, error)
	DeletePricing(ctx context.Context, productID int64, cycle domain.BillingCycle) error
	// Configurable options
	ListOptionGroups(ctx context.Context) ([]domain.ConfigurableOptionGroup, error)
	ListOptions(ctx context.Context, groupID int64) ([]domain.ConfigurableOption, error)
	ListOptionValues(ctx context.Context, optionID int64) ([]domain.ConfigurableOptionValue, error)
	// ListOptionsByGroupIDs and ListOptionValuesByOptionIDs batch-load across
	// many groups/options at once (keyed by group_id/option_id), to avoid an
	// N+1 query per group/option in tree-building callers like OptionTree.
	ListOptionsByGroupIDs(ctx context.Context, groupIDs []int64) (map[int64][]domain.ConfigurableOption, error)
	ListOptionValuesByOptionIDs(ctx context.Context, optionIDs []int64) (map[int64][]domain.ConfigurableOptionValue, error)
	// Dynamic-product specs (read surface used by orders pricing + provisioning)
	ListSpecs(ctx context.Context, productID int64) ([]domain.ProductSpec, error)
	GetSpecPricing(ctx context.Context, specID int64, cycle domain.BillingCycle) (*domain.ProductSpecPricing, error)
	// DecrementStock decrements stock_qty if stock_enabled; returns CONFLICT
	// apperr when out of stock.
	DecrementStock(ctx context.Context, productID int64) error
}

// CouponRepo persists coupons.
type CouponRepo interface {
	Create(ctx context.Context, c *domain.Coupon) error
	GetByID(ctx context.Context, id int64) (*domain.Coupon, error)
	GetByCode(ctx context.Context, code string) (*domain.Coupon, error)
	Update(ctx context.Context, c *domain.Coupon) error
	List(ctx context.Context, p ListParams) ([]domain.Coupon, int64, error)
	Delete(ctx context.Context, id int64) error
	// IncrementUsage bumps used_count; returns CONFLICT when max_uses reached.
	IncrementUsage(ctx context.Context, id int64) error
}

// OrderRepo persists orders and their items.
type OrderRepo interface {
	Create(ctx context.Context, o *domain.Order, items []domain.OrderItem) error
	GetByID(ctx context.Context, id int64) (*domain.Order, error)
	GetItems(ctx context.Context, orderID int64) ([]domain.OrderItem, error)
	UpdateStatus(ctx context.Context, id int64, status domain.OrderStatus) error
	UpdateItemBackRefs(ctx context.Context, itemID int64, serviceID, domainID *int64) error
	List(ctx context.Context, p ListParams) ([]domain.Order, int64, error)
	ListByClient(ctx context.Context, clientID int64, p ListParams) ([]domain.Order, int64, error)
	NextNumber(ctx context.Context, scope string) (int64, error)
}

// InvoiceRepo persists invoices and their items.
type InvoiceRepo interface {
	Create(ctx context.Context, inv *domain.Invoice, items []domain.InvoiceItem) error
	GetByID(ctx context.Context, id int64) (*domain.Invoice, error)
	GetByNumber(ctx context.Context, number string) (*domain.Invoice, error)
	// GetByIDForUpdate row-locks the invoice (SELECT ... FOR UPDATE); must be
	// called inside a TxManager.WithinTx.
	GetByIDForUpdate(ctx context.Context, id int64) (*domain.Invoice, error)
	GetItems(ctx context.Context, invoiceID int64) ([]domain.InvoiceItem, error)
	// GetItemsByInvoiceIDs batch-loads items for many invoices at once (keyed
	// by invoice_id), to avoid an N+1 query per invoice in callers that already
	// hold a list of invoices.
	GetItemsByInvoiceIDs(ctx context.Context, invoiceIDs []int64) (map[int64][]domain.InvoiceItem, error)
	AddItem(ctx context.Context, item *domain.InvoiceItem) error
	Update(ctx context.Context, inv *domain.Invoice) error
	UpdateStatus(ctx context.Context, id int64, status domain.InvoiceStatus, paidAt *time.Time) error
	SetPDFObjectKey(ctx context.Context, id int64, key string) error
	List(ctx context.Context, p ListParams) ([]domain.Invoice, int64, error)
	ListByClient(ctx context.Context, clientID int64, p ListParams) ([]domain.Invoice, int64, error)
	// ListDueForStatus returns invoices in `status` with due_date <= before.
	ListDueForStatus(ctx context.Context, status domain.InvoiceStatus, before time.Time) ([]domain.Invoice, error)
	// NextNumber increments and returns the counter for scope (e.g.
	// "invoice:202607") using UPDATE ... RETURNING inside the current tx.
	NextNumber(ctx context.Context, scope string) (int64, error)
}

// RenewalInvoiceChecker reports whether an unpaid/overdue renewal invoice
// already exists for a service/domain (renewal-generation dedupe). Owned by
// billing (implemented by billing.Repo); consumed by any module that forces
// a renewal invoice outside billing's own cron path (e.g. domains.RenewNow).
type RenewalInvoiceChecker interface {
	HasOpenRenewalInvoice(ctx context.Context, relatedType domain.InvoiceItemRelatedType, relatedID int64) (bool, error)
}

// TransactionRepo persists payment transactions.
type TransactionRepo interface {
	Create(ctx context.Context, t *domain.Transaction) error
	GetByID(ctx context.Context, id int64) (*domain.Transaction, error)
	GetByMerchantOrderID(ctx context.Context, merchantOrderID string) (*domain.Transaction, error)
	Update(ctx context.Context, t *domain.Transaction) error
	ListByInvoice(ctx context.Context, invoiceID int64) ([]domain.Transaction, error)
	ListPending(ctx context.Context, gateway domain.Gateway, olderThan time.Time) ([]domain.Transaction, error)
	List(ctx context.Context, p ListParams) ([]domain.Transaction, int64, error)
	// ListForClient lists transactions for invoices owned by clientID (client
	// billing history), newest first.
	ListForClient(ctx context.Context, clientID int64, p ListParams) ([]domain.Transaction, int64, error)
	// NextAttempt atomically reserves and returns the next gateway-payment
	// attempt number for invoiceID (counters table, scope
	// "payment_attempt:<invoiceID>", same UPDATE ... RETURNING pattern as
	// NextNumber above). Unlike counting existing transactions, this never
	// races: two concurrent callers for the same invoice always get distinct
	// numbers, even though neither has inserted its transaction row yet.
	NextAttempt(ctx context.Context, invoiceID int64) (int64, error)
}

// CreditRepo reads the client credit ledger (writes go through
// ClientRepo.AdjustCredit to keep balance+ledger atomic).
type CreditRepo interface {
	ListByClient(ctx context.Context, clientID int64, p ListParams) ([]domain.CreditLedgerEntry, int64, error)
}

// ServiceRepo persists provisioned services.
type ServiceRepo interface {
	Create(ctx context.Context, s *domain.Service) error
	GetByID(ctx context.Context, id int64) (*domain.Service, error)
	// GetByIDForUpdate row-locks the service (SELECT ... FOR UPDATE); must be
	// called inside a TxManager.WithinTx.
	GetByIDForUpdate(ctx context.Context, id int64) (*domain.Service, error)
	// GetByIDs returns the services matching ids, in no particular order;
	// ids not found are simply omitted from the result.
	GetByIDs(ctx context.Context, ids []int64) ([]domain.Service, error)
	Update(ctx context.Context, s *domain.Service) error
	UpdateStatus(ctx context.Context, id int64, status domain.ServiceStatus) error
	List(ctx context.Context, p ListParams) ([]domain.Service, int64, error)
	ListByClient(ctx context.Context, clientID int64, p ListParams) ([]domain.Service, int64, error)
	// ListRenewalsDue returns active services with next_due_date <= before.
	ListRenewalsDue(ctx context.Context, before time.Time) ([]domain.Service, error)
	CountByServer(ctx context.Context, serverID int64) (int64, error)
}

// ServerRepo persists servers and server groups.
type ServerRepo interface {
	Create(ctx context.Context, s *domain.Server) error
	GetByID(ctx context.Context, id int64) (*domain.Server, error)
	Update(ctx context.Context, s *domain.Server) error
	List(ctx context.Context, p ListParams) ([]domain.Server, int64, error)
	Delete(ctx context.Context, id int64) error
	CreateGroup(ctx context.Context, g *domain.ServerGroup) error
	GetGroupByID(ctx context.Context, id int64) (*domain.ServerGroup, error)
	UpdateGroup(ctx context.Context, g *domain.ServerGroup) error
	ListGroups(ctx context.Context) ([]domain.ServerGroup, error)
	DeleteGroup(ctx context.Context, id int64) error
	// PickServer selects an active server in the group per the group strategy
	// (round_robin | least_used), capacity-aware (max_accounts).
	PickServer(ctx context.Context, groupID int64) (*domain.Server, error)
}

// DomainRepo persists domains.
type DomainRepo interface {
	Create(ctx context.Context, d *domain.Domain) error
	GetByID(ctx context.Context, id int64) (*domain.Domain, error)
	GetByName(ctx context.Context, name string) (*domain.Domain, error)
	// GetByIDForUpdate row-locks the domain (SELECT ... FOR UPDATE); must be
	// called inside a TxManager.WithinTx.
	GetByIDForUpdate(ctx context.Context, id int64) (*domain.Domain, error)
	// GetByIDs returns the domains matching ids, in no particular order; ids
	// not found are simply omitted from the result.
	GetByIDs(ctx context.Context, ids []int64) ([]domain.Domain, error)
	Update(ctx context.Context, d *domain.Domain) error
	UpdateStatus(ctx context.Context, id int64, status domain.DomainStatus) error
	List(ctx context.Context, p ListParams) ([]domain.Domain, int64, error)
	ListByClient(ctx context.Context, clientID int64, p ListParams) ([]domain.Domain, int64, error)
	// ListRenewalsDue returns active auto_renew domains with next_due_date <= before.
	ListRenewalsDue(ctx context.Context, before time.Time) ([]domain.Domain, error)
	ListForSync(ctx context.Context, limit int) ([]domain.Domain, error)
}

// RegistrarRepo persists registrar configuration rows.
type RegistrarRepo interface {
	GetByID(ctx context.Context, id int64) (*domain.Registrar, error)
	GetByName(ctx context.Context, name string) (*domain.Registrar, error)
	Update(ctx context.Context, r *domain.Registrar) error
	List(ctx context.Context) ([]domain.Registrar, error)
}

// TLDPricingRepo persists per-TLD admin pricing/config rows.
type TLDPricingRepo interface {
	List(ctx context.Context) ([]domain.TLDPricing, error)
	ListActive(ctx context.Context) ([]domain.TLDPricing, error)
	GetByID(ctx context.Context, id int64) (*domain.TLDPricing, error)
	GetByTLD(ctx context.Context, tld string) (*domain.TLDPricing, error)
	Create(ctx context.Context, p *domain.TLDPricing) error
	Update(ctx context.Context, p *domain.TLDPricing) error
	Delete(ctx context.Context, id int64) error
}

// PremiumDomainPricingRepo persists manually-curated exact-domain price overrides.
type PremiumDomainPricingRepo interface {
	List(ctx context.Context) ([]domain.PremiumDomainPricing, error)
	GetByName(ctx context.Context, name string) (*domain.PremiumDomainPricing, error)
	Create(ctx context.Context, p *domain.PremiumDomainPricing) error
	Update(ctx context.Context, p *domain.PremiumDomainPricing) error
	Delete(ctx context.Context, id int64) error
}

// PremiumLengthPricingRepo persists premium price tiers keyed by TLD +
// registrable-label character length.
type PremiumLengthPricingRepo interface {
	List(ctx context.Context) ([]domain.PremiumLengthPricing, error)
	GetByTLDAndLength(ctx context.Context, tld string, charLength int) (*domain.PremiumLengthPricing, error)
	Create(ctx context.Context, p *domain.PremiumLengthPricing) error
	Update(ctx context.Context, p *domain.PremiumLengthPricing) error
	Delete(ctx context.Context, id int64) error
}

// DomainAddonRepo persists the fixed domain-addon catalog (id_protection,
// dns_management, email_forwarding) - rows are seeded by migration; only
// price/active are ever updated, never created/deleted.
type DomainAddonRepo interface {
	List(ctx context.Context) ([]domain.DomainAddon, error)
	ListActive(ctx context.Context) ([]domain.DomainAddon, error)
	GetByKey(ctx context.Context, key string) (*domain.DomainAddon, error)
	Update(ctx context.Context, a *domain.DomainAddon) error
}

// TicketRepo persists tickets, replies and departments.
type TicketRepo interface {
	Create(ctx context.Context, t *domain.Ticket) error
	GetByID(ctx context.Context, id int64) (*domain.Ticket, error)
	Update(ctx context.Context, t *domain.Ticket) error
	List(ctx context.Context, p ListParams) ([]domain.Ticket, int64, error)
	ListByClient(ctx context.Context, clientID int64, p ListParams) ([]domain.Ticket, int64, error)
	NextNumber(ctx context.Context, scope string) (int64, error)
	// Replies
	AddReply(ctx context.Context, r *domain.TicketReply) error
	ListReplies(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.TicketReply, error)
	// Departments
	CreateDepartment(ctx context.Context, d *domain.TicketDepartment) error
	GetDepartmentByID(ctx context.Context, id int64) (*domain.TicketDepartment, error)
	UpdateDepartment(ctx context.Context, d *domain.TicketDepartment) error
	ListDepartments(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error)
	DeleteDepartment(ctx context.Context, id int64) error
}

// EmailTemplateRepo persists email templates.
type EmailTemplateRepo interface {
	Get(ctx context.Context, key, locale string) (*domain.EmailTemplate, error)
	Upsert(ctx context.Context, t *domain.EmailTemplate) error
	List(ctx context.Context) ([]domain.EmailTemplate, error)
	Delete(ctx context.Context, key, locale string) error
}

// EmailLogRepo persists the outbound email log.
type EmailLogRepo interface {
	Create(ctx context.Context, e *domain.EmailLogEntry) error
	GetByID(ctx context.Context, id int64) (*domain.EmailLogEntry, error)
	MarkSent(ctx context.Context, id int64, at time.Time) error
	MarkFailed(ctx context.Context, id int64, errMsg string) error
	List(ctx context.Context, p ListParams) ([]domain.EmailLogEntry, int64, error)
}

// SettingsRepo reads/writes the settings table with typed getters
// (CONTRACTS.md §10). Getters return def when the key is missing or the value
// cannot be converted.
type SettingsRepo interface {
	GetString(ctx context.Context, key, def string) (string, error)
	GetInt(ctx context.Context, key string, def int) (int, error)
	GetBool(ctx context.Context, key string, def bool) (bool, error)
	GetJSON(ctx context.Context, key string, out any) error
	Set(ctx context.Context, key string, value any) error
	All(ctx context.Context) ([]domain.Setting, error)
}

// AuditRepo persists audit log rows.
type AuditRepo interface {
	Create(ctx context.Context, a *domain.AuditLog) error
	List(ctx context.Context, p ListParams) ([]domain.AuditLog, int64, error)
}

// IntegrationLogRepo persists integration log rows.
type IntegrationLogRepo interface {
	Create(ctx context.Context, l *domain.IntegrationLog) error
	List(ctx context.Context, p ListParams) ([]domain.IntegrationLog, int64, error)
}

// RevenuePoint is one bucket of the revenue report.
type RevenuePoint struct {
	Period string `json:"period"` // e.g. "2026-07" or "2026-07-03"
	Amount int64  `json:"amount"`
	Count  int64  `json:"count"`
}

// DashboardStats are the admin dashboard KPIs.
type DashboardStats struct {
	ClientsActive     int64 `json:"clients_active"`
	ServicesActive    int64 `json:"services_active"`
	InvoicesUnpaid    int64 `json:"invoices_unpaid"`
	InvoicesOverdue   int64 `json:"invoices_overdue"`
	TicketsOpen       int64 `json:"tickets_open"`
	OrdersPending     int64 `json:"orders_pending"`
	RevenueToday      int64 `json:"revenue_today"`
	RevenueThisMonth  int64 `json:"revenue_this_month"`
	DomainsActive     int64 `json:"domains_active"`
	ServicesSuspended int64 `json:"services_suspended"`
}

// DashboardRepo runs aggregate queries for the admin dashboard and reports.
type DashboardRepo interface {
	Stats(ctx context.Context) (*DashboardStats, error)
	Revenue(ctx context.Context, from, to time.Time, groupBy string) ([]RevenuePoint, error)
	OrdersReport(ctx context.Context, from, to time.Time) ([]RevenuePoint, error)
	ServicesReport(ctx context.Context) (map[string]int64, error)
}

// AnnouncementRepo persists portal announcements (soft-deleted).
type AnnouncementRepo interface {
	Create(ctx context.Context, a *domain.Announcement) error
	GetByID(ctx context.Context, id int64) (*domain.Announcement, error)
	GetBySlug(ctx context.Context, slug string) (*domain.Announcement, error)
	Update(ctx context.Context, a *domain.Announcement) error
	List(ctx context.Context, p ListParams) ([]domain.Announcement, int64, error)
	// ListPublished lists only published, non-deleted announcements (public view).
	ListPublished(ctx context.Context, p ListParams) ([]domain.Announcement, int64, error)
	SoftDelete(ctx context.Context, id int64) error
}

// KnowledgebaseRepo persists knowledgebase categories and articles
// (soft-deleted).
type KnowledgebaseRepo interface {
	// Categories
	CreateCategory(ctx context.Context, c *domain.KBCategory) error
	GetCategoryByID(ctx context.Context, id int64) (*domain.KBCategory, error)
	GetCategoryBySlug(ctx context.Context, slug string) (*domain.KBCategory, error)
	UpdateCategory(ctx context.Context, c *domain.KBCategory) error
	ListCategories(ctx context.Context, includeHidden bool) ([]domain.KBCategory, error)
	SoftDeleteCategory(ctx context.Context, id int64) error
	CountArticlesInCategory(ctx context.Context, categoryID int64) (int64, error)
	// Articles
	CreateArticle(ctx context.Context, a *domain.KBArticle) error
	GetArticleByID(ctx context.Context, id int64) (*domain.KBArticle, error)
	GetArticleBySlug(ctx context.Context, slug string) (*domain.KBArticle, error)
	UpdateArticle(ctx context.Context, a *domain.KBArticle) error
	List(ctx context.Context, p ListParams) ([]domain.KBArticle, int64, error)
	// ListPublished lists published, non-deleted articles, optionally scoped to
	// categoryID (0 = all categories) - the public view.
	ListPublished(ctx context.Context, categoryID int64, p ListParams) ([]domain.KBArticle, int64, error)
	SoftDeleteArticle(ctx context.Context, id int64) error
	IncrementViews(ctx context.Context, id int64) error
}

// NetworkStatusRepo persists network status entries (soft-deleted).
type NetworkStatusRepo interface {
	Create(ctx context.Context, n *domain.NetworkIssue) error
	GetByID(ctx context.Context, id int64) (*domain.NetworkIssue, error)
	Update(ctx context.Context, n *domain.NetworkIssue) error
	List(ctx context.Context, p ListParams) ([]domain.NetworkIssue, int64, error)
	// ListActive lists non-deleted, unresolved entries (public status view).
	ListActive(ctx context.Context) ([]domain.NetworkIssue, error)
	SoftDelete(ctx context.Context, id int64) error
}

// Integration ports

// PaymentMethod is one gateway payment channel.
type PaymentMethod struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Image string `json:"image"`
	Fee   int64  `json:"fee"`
	// Gateway identifies which registered ports.PaymentGateway this method
	// belongs to (e.g. "duitku", "manual") - filled in by payments.Service
	// when aggregating across the registry, not by the adapter itself.
	Gateway string `json:"gateway"`
}

// CreateTxRequest asks the gateway to open a payment.
type CreateTxRequest struct {
	MerchantOrderID string
	Amount          int64
	Method          string
	ProductDetails  string
	Email           string
	Phone           string
	CustomerName    string
	ReturnURL       string
	CallbackURL     string
	ExpiryMinutes   int
}

// BankAccount is one manual-transfer destination account (non-secret -
// account numbers here are display instructions, not credentials).
type BankAccount struct {
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
	AccountHolder string `json:"account_holder"`
}

// CreateTxResult is the gateway response to CreateTransaction. BankAccounts/
// Note are only ever populated by the manual gateway (empty for Duitku).
type CreateTxResult struct {
	Reference    string
	PaymentURL   string
	VANumber     string
	QRString     string
	Amount       int64
	BankAccounts []BankAccount
	Note         string
}

// TxStatus is the gateway's view of a transaction.
type TxStatus struct {
	Reference string
	// Amount is the gross amount the gateway settled - for a channel whose
	// fee is passed on to the customer, this already includes Fee (Duitku
	// reports both directly; we never recompute the fee ourselves).
	Amount        int64
	Fee           int64
	StatusCode    string // "00" success, "01" pending, "02" cancelled/failed
	StatusMessage string
}

// Duitku transaction status codes.
const (
	TxStatusSuccess   = "00"
	TxStatusPending   = "01"
	TxStatusCancelled = "02"
)

// CallbackPayload is the form-urlencoded webhook body from the gateway.
type CallbackPayload struct {
	MerchantCode    string `form:"merchantCode" json:"merchantCode"`
	Amount          string `form:"amount" json:"amount"`
	MerchantOrderID string `form:"merchantOrderId" json:"merchantOrderId"`
	ProductDetail   string `form:"productDetail" json:"productDetail"`
	AdditionalParam string `form:"additionalParam" json:"additionalParam"`
	PaymentCode     string `form:"paymentCode" json:"paymentCode"`
	ResultCode      string `form:"resultCode" json:"resultCode"`
	Reference       string `form:"reference" json:"reference"`
	Signature       string `form:"signature" json:"signature"`
}

// PaymentGateway is implemented by the duitku adapter.
type PaymentGateway interface {
	GetPaymentMethods(ctx context.Context, amount int64) ([]PaymentMethod, error)
	CreateTransaction(ctx context.Context, req CreateTxRequest) (*CreateTxResult, error)
	CheckTransaction(ctx context.Context, merchantOrderID string) (*TxStatus, error)
	// VerifyCallbackSignature checks MD5(merchantCode+amount+merchantOrderId+apiKey).
	VerifyCallbackSignature(p CallbackPayload) bool
}

// CaptchaVerifier verifies a client-side CAPTCHA token with the provider
// (Cloudflare Turnstile). Implemented by the turnstile adapter. remoteIP is the
// end-user IP (optional; may be empty). It returns whether the token is valid;
// a non-nil error signals an EXTERNAL failure (provider unreachable / bad
// response) as opposed to a plain "token invalid" (ok=false, err=nil).
type CaptchaVerifier interface {
	Verify(ctx context.Context, token, remoteIP string) (ok bool, err error)
}

// ServerConfig is the decrypted connection info for one provisioning server.
type ServerConfig struct {
	ID          int64
	Name        string
	Module      domain.ServerModuleName
	Hostname    string
	Port        int
	Username    string
	Password    string // decrypted
	APIToken    string // decrypted
	UseSSL      bool
	Nameserver1 string
	Nameserver2 string
}

// CreateAccountParams are the inputs for creating a hosting account.
type CreateAccountParams struct {
	Username string
	Domain   string
	Password string
	Package  string
	Email    string
	IP       string
}

// Unlimited is the sentinel value for a PackageSpec limit meaning "no cap". Each
// adapter renders it in its own dialect (cPanel "unlimited", DirectAdmin u<field>=ON).
const Unlimited = domain.UnlimitedQty

// PackageSpec is the set of resource limits for a control-panel package. Limits
// is keyed by canonical knob; size values are in MB (already converted from the
// spec unit), count values are plain counts, and a value equal to Unlimited (-1)
// means no cap. Each adapter resolves the concrete panel parameter name via
// domain.PanelParam.
type PackageSpec struct {
	Name            string                        // deterministic package name, e.g. "whcms_s123"
	FeatureList     string                        // cPanel only; "" defaults to "default"
	ShellAccess     bool                          // cPanel hasshell / DA ssh
	CGIAccess       bool                          // cPanel cgi / DA cgi
	TemplatePackage string                        // DirectAdmin only; existing DA package cloned for unmodeled long-tail fields (Git, WordPress, ClamAV, etc.) - cPanel needs no equivalent since FeatureList is a live WHM reference
	Limits          map[domain.ProvisionKey]int64 // MB for size keys, count otherwise
}

// AccountResult is returned by ServerModule.Create.
type AccountResult struct {
	Username string
	Domain   string
	IP       string
	Meta     map[string]any
}

// AccountInfo is returned by ServerModule.AccountInfo.
type AccountInfo struct {
	Username  string
	Domain    string
	Package   string
	Suspended bool
	DiskUsed  int64 // MB
	DiskLimit int64 // MB
	Meta      map[string]any
}

// ServerInfo is the read-only server metadata returned by
// ServerModule.TestConnection. It validates connectivity and credentials
// WITHOUT touching a specific hosting account, and carries values the admin UI
// can auto-populate (nameservers, and - informationally - the panel version and
// server-reported hostname). Any field may be empty when the panel does not
// report it or the credentials lack the privilege to read it.
type ServerInfo struct {
	Version     string   // control-panel software version (e.g. WHM "11.134.0.45")
	Hostname    string   // server-reported hostname
	Nameservers []string // configured nameservers, in order (may be empty)
	Meta        map[string]any
}

// ServerModule is implemented by the cpanel and directadmin adapters.
type ServerModule interface {
	Name() string
	// Create provisions a hosting account. Callers retrying a failed
	// provisioning job MUST treat a CONFLICT (account already exists) as
	// possibly the already-completed step of a prior attempt - call
	// AccountInfo and compare Domain before deciding whether it's safe to
	// proceed, since Create itself is not required to de-duplicate by domain.
	Create(ctx context.Context, s ServerConfig, a CreateAccountParams) (*AccountResult, error)
	Suspend(ctx context.Context, s ServerConfig, username, reason string) error
	Unsuspend(ctx context.Context, s ServerConfig, username string) error
	Terminate(ctx context.Context, s ServerConfig, username string) error
	ChangePackage(ctx context.Context, s ServerConfig, username, pkg string) error
	ChangePassword(ctx context.Context, s ServerConfig, username, password string) error
	// EnsurePackage creates the package if absent or updates it to match spec.
	// It MUST be idempotent so provisioning jobs are safe to retry.
	EnsurePackage(ctx context.Context, s ServerConfig, spec PackageSpec) error
	// DeletePackage removes a package by name. A missing package is not an error.
	DeletePackage(ctx context.Context, s ServerConfig, name string) error
	// ListPackages returns the hosting packages/plans currently defined on the
	// panel (cpanel `listpkgs`; DA `CMD_API_MANAGE_USER_PACKAGES` GET), so the
	// admin product form can pick an existing name instead of free-typing one.
	// Read-only, no side effects.
	ListPackages(ctx context.Context, s ServerConfig) ([]string, error)
	AccountInfo(ctx context.Context, s ServerConfig, username string) (*AccountInfo, error)
	// TestConnection verifies connectivity and credentials with a read-only
	// call that does NOT require an existing hosting account (WHM `version`;
	// DirectAdmin user listing) and returns server metadata for
	// auto-population. A non-nil error means the connection/credentials failed.
	TestConnection(ctx context.Context, s ServerConfig) (*ServerInfo, error)
	// SSOURL returns a one-time login URL (cPanel create_user_session; DA
	// login key URL) or an Unsupported error.
	SSOURL(ctx context.Context, s ServerConfig, username string) (string, error)
}

// DomainAvailability is one result of a bulk availability check.
type DomainAvailability struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Premium   bool   `json:"premium"`
	Price     int64  `json:"price"` // register price for a 1-year term
	// RegisterPrices is the full year-1..10 register price matrix, set only
	// when Price was sourced from an active TLDPricing row (the one pricing
	// source that isn't a flat annual rate - see domains.overlayPricing).
	// Premium-domain/length-tier overrides and the live-registrar-quote
	// fallback ARE flat annual rates, so callers can safely assume
	// Price * years for those and only need this map otherwise - nil when
	// unavailable.
	RegisterPrices map[string]int64 `json:"register_prices,omitempty"`
}

// RegistrantContact is a domain registrant/admin contact.
type RegistrantContact struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Company   string `json:"company"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Address1  string `json:"address1"`
	City      string `json:"city"`
	State     string `json:"state"`
	Postcode  string `json:"postcode"`
	Country   string `json:"country"`
}

// RegisterDomainRequest asks the registrar to register a domain.
type RegisterDomainRequest struct {
	Name    string
	Years   int
	NS      []string
	Contact RegistrantContact
}

// TransferDomainRequest asks the registrar to transfer a domain in.
type TransferDomainRequest struct {
	Name    string
	Years   int
	NS      []string
	Contact RegistrantContact
	EPPCode string
}

// DomainResult is returned by register/transfer/renew calls.
type DomainResult struct {
	Name       string
	Status     string
	ExpiryDate time.Time
	OrderID    string // registrar-side order/reference id
}

// DNSRecord is one DNS record at the registrar.
type DNSRecord struct {
	Type  string `json:"type"`
	Host  string `json:"host"`
	Value string `json:"value"`
	TTL   int    `json:"ttl"`
	Prio  int    `json:"prio"`
}

// DomainSyncInfo is the registrar's current view of a domain.
type DomainSyncInfo struct {
	Status     string
	ExpiryDate time.Time
	NS         []string
}

// RegistrarAccountInfo is the reseller account profile/balance used for a
// registrar connectivity/health check (admin registrar "Test" action).
type RegistrarAccountInfo struct {
	AccountID string
	Name      string
	Currency  string
	Balance   int64 // whole IDR
}

// RegistrarCatalogPrice is one TLD's full registrar-side cost pricing, as
// returned by ListCatalogPrices - the registrar's source-of-truth pricelist
// used to seed/import TLDPricing rows instead of hand-typing every TLD.
type RegistrarCatalogPrice struct {
	Extension      string // e.g. ".co.id" (leading dot, lowercase)
	Currency       string
	RegisterPrices map[string]int64 // year "1".."10" -> registrar cost
	RenewPrices    map[string]int64 // year "1".."10" -> registrar cost
	TransferPrice  int64
	RestorePrice   int64
}

// RegistrarModule is implemented by the rdash adapter.
type RegistrarModule interface {
	CheckAvailability(ctx context.Context, names []string) ([]DomainAvailability, error)
	Register(ctx context.Context, req RegisterDomainRequest) (*DomainResult, error)
	Transfer(ctx context.Context, req TransferDomainRequest) (*DomainResult, error)
	Renew(ctx context.Context, name string, years int) (*DomainResult, error)
	GetNameservers(ctx context.Context, name string) ([]string, error)
	UpdateNameservers(ctx context.Context, name string, ns []string) error
	GetContact(ctx context.Context, name string) (*RegistrantContact, error)
	UpdateContact(ctx context.Context, name string, c RegistrantContact) error
	GetEPPCode(ctx context.Context, name string) (string, error)
	GetDNSRecords(ctx context.Context, name string) ([]DNSRecord, error)
	UpdateDNSRecords(ctx context.Context, name string, recs []DNSRecord) error
	SyncDomain(ctx context.Context, name string) (*DomainSyncInfo, error)
	AccountInfo(ctx context.Context) (*RegistrarAccountInfo, error)
	// ListCatalogPrices returns the registrar's full TLD pricelist
	// (paginating through every page). Read-only, no side effects - feeds
	// the admin TLD Pricing "Import from Registrar" flow.
	ListCatalogPrices(ctx context.Context) ([]RegistrarCatalogPrice, error)
}

// Infra ports

// MailMessage is one outbound email.
type MailMessage struct {
	To       string `json:"to"`
	ToName   string `json:"to_name"`
	From     string `json:"from"`
	FromName string `json:"from_name"`
	Subject  string `json:"subject"`
	HTML     string `json:"html"`
	Text     string `json:"text"`
}

// Mailer sends email. Drivers: smtp | http | log (picked by MAIL_DRIVER).
type Mailer interface {
	Send(ctx context.Context, msg MailMessage) error
}

// PresignOptions are resolved response-header overrides for a presigned GET
// URL (translated to the S3 "response-content-type" / "response-content-
// disposition" query overrides). Used to force safe download behavior
// regardless of what content-type the object was stored with.
type PresignOptions struct {
	ResponseContentType        string
	ResponseContentDisposition string
}

// PresignOption configures PresignOptions.
type PresignOption func(*PresignOptions)

// WithResponseContentType overrides the Content-Type the browser sees when
// following the presigned URL.
func WithResponseContentType(ct string) PresignOption {
	return func(o *PresignOptions) { o.ResponseContentType = ct }
}

// WithResponseContentDisposition overrides the Content-Disposition the
// browser sees when following the presigned URL (e.g. "attachment;
// filename=\"...\"" to force a download instead of inline rendering).
func WithResponseContentDisposition(cd string) PresignOption {
	return func(o *PresignOptions) { o.ResponseContentDisposition = cd }
}

// Storage is S3-compatible object storage (RustFS).
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	// PresignGet returns a presigned GET URL valid for ttl. opts may force
	// response Content-Type/Content-Disposition overrides (defense-in-depth
	// against stored objects with an unsafe/spoofed content type).
	PresignGet(ctx context.Context, key string, ttl time.Duration, opts ...PresignOption) (string, error)
}

// Encryptor encrypts/decrypts secrets (AES-256-GCM, base64 output).
type Encryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// PasswordHasher hashes and verifies passwords (Argon2id PHC strings).
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, phc string) (bool, error)
}

// Token kinds for TokenStore.
const (
	TokenKindVerifyEmail   = "verify_email"
	TokenKindResetPassword = "reset_password"
)

// TokenStore issues and consumes single-use tokens (Redis-backed).
type TokenStore interface {
	Create(ctx context.Context, kind string, userID int64, ttl time.Duration) (string, error)
	// Consume deletes the token and returns the owning user id; NOT_FOUND
	// apperr when missing/expired/already used.
	Consume(ctx context.Context, kind, token string) (int64, error)
}

// JobOption configures an enqueued job.
type JobOption func(*JobOptions)

// JobOptions are resolved enqueue options.
type JobOptions struct {
	Queue     string
	MaxRetry  int
	ProcessIn time.Duration
	UniqueTTL time.Duration
}

// WithQueue routes the task to a named queue.
func WithQueue(q string) JobOption { return func(o *JobOptions) { o.Queue = q } }

// WithMaxRetry overrides the default retry count.
func WithMaxRetry(n int) JobOption { return func(o *JobOptions) { o.MaxRetry = n } }

// WithProcessIn delays task execution.
func WithProcessIn(d time.Duration) JobOption { return func(o *JobOptions) { o.ProcessIn = d } }

// WithUniqueTTL deduplicates identical tasks for the given window.
func WithUniqueTTL(d time.Duration) JobOption { return func(o *JobOptions) { o.UniqueTTL = d } }

// Enqueuer enqueues background jobs (asynq-backed). payload is JSON-marshaled.
type Enqueuer interface {
	Enqueue(ctx context.Context, taskType string, payload any, opts ...JobOption) error
}

// Locker runs fn under a distributed Redis lock (SET NX). If the lock is held
// elsewhere it returns a CONFLICT apperr without running fn.
type Locker interface {
	WithLock(ctx context.Context, key string, ttl time.Duration, fn func() error) error
}

// TOTPReplayGuard prevents replay of an already-accepted TOTP code: it tracks,
// per user, the last time-step that was accepted (Redis-backed, short TTL
// since steps are 30s wide). Used on the login path, which is the actual
// attacker-exploitable surface for a captured/leaked code.
type TOTPReplayGuard interface {
	// Accept reports whether step is newer than the last-accepted step
	// recorded for userID (step <= last-accepted is treated as a replay and
	// rejected). On acceptance it atomically records step (with ttl) so a
	// later call with the same or an older step is rejected.
	Accept(ctx context.Context, userID int64, step int64, ttl time.Duration) (bool, error)
}

// RateLimiter is a Redis fixed-window rate limiter.
type RateLimiter interface {
	// Allow increments the counter for key in the current window and reports
	// whether the call is within limit.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
	// Reset clears the counter for key.
	Reset(ctx context.Context, key string) error
}

// Cache is a JSON cache (product catalog, dashboard, payment methods).
type Cache interface {
	GetJSON(ctx context.Context, key string, out any) (bool, error)
	SetJSON(ctx context.Context, key string, val any, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// PresenceEntry is one currently-active admin/staff user (there is no "name"
// column for staff/admin in this schema - email is the display identifier).
type PresenceEntry struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

// PresenceTracker backs the HostPanel "Staff Online" widget: Touch is called
// on every authenticated /auth/me request (heartbeat), List returns everyone
// still within the freshness window, and Untouch removes a user immediately
// on explicit logout rather than waiting out the freshness window.
type PresenceTracker interface {
	Touch(ctx context.Context, userID int64, email, role string) error
	Untouch(ctx context.Context, userID int64) error
	List(ctx context.Context) ([]PresenceEntry, error)
}

// AuditLogger records sensitive actions (implemented over AuditRepo).
type AuditLogger interface {
	Log(ctx context.Context, actorUserID int64, action, entity string, entityID int64, before, after any)
}

// IntegrationCall describes one external API call for logging.
type IntegrationCall struct {
	Provider   string
	Endpoint   string
	Method     string
	StatusCode int
	Success    bool
	LatencyMS  int64
	Request    any // redacted before persist
	Response   any // redacted before persist
	Error      string
}

// IntegrationLogger records external API calls (implemented over IntegrationLogRepo).
type IntegrationLogger interface {
	Log(ctx context.Context, call IntegrationCall)
}

// Clock abstracts time.Now for testability. Inject everywhere time matters.
type Clock interface {
	Now() time.Time
}

// ModuleAction is one queued/failed provisioning or domain-registrar job
// shown in the admin "Pending Module Actions" queue (WHMCS's "Module Queue"
// equivalent) - jobs.ModuleActionTypes lists which task types qualify.
// Payload is redacted for any type that carries a secret (plaintext
// password, EPP code) before it ever reaches this struct.
type ModuleAction struct {
	ID            string          `json:"id"`
	Queue         string          `json:"queue"`
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
	State         string          `json:"state"` // "archived" | "retry"
	MaxRetry      int             `json:"max_retry"`
	Retried       int             `json:"retried"`
	LastErr       string          `json:"last_err"`
	LastFailedAt  *time.Time      `json:"last_failed_at,omitempty"`
	NextProcessAt *time.Time      `json:"next_process_at,omitempty"`
}

// ModuleActionFilter narrows ListModuleActions. Type/State blank = no filter.
type ModuleActionFilter struct {
	Type    string
	State   string
	Page    int
	PerPage int
}

// JobInspector exposes the subset of the job queue an admin needs to see and
// manually retry/dismiss a stuck module action (implemented over asynq's
// Inspector - internal/platform/queue).
type JobInspector interface {
	ListModuleActions(ctx context.Context, filter ModuleActionFilter) ([]ModuleAction, int64, error)
	RetryModuleAction(ctx context.Context, queue, id string) error
	DeleteModuleAction(ctx context.Context, queue, id string) error
	// DismissAllModuleActions dismisses every module action matching filter
	// (Type/State only - Page/PerPage are ignored, the full matching set is
	// dismissed), returning how many were dismissed.
	DismissAllModuleActions(ctx context.Context, filter ModuleActionFilter) (int, error)
}

// CancellationRequestRepo is the persistence surface for client service
// cancellation requests (provisioning module). List is filtered by
// ListParams.Status/ServiceID (both optional).
type CancellationRequestRepo interface {
	Create(ctx context.Context, r *domain.CancellationRequest) error
	GetByID(ctx context.Context, id int64) (*domain.CancellationRequest, error)
	// GetPendingByService returns the service's pending request, or nil (no
	// error) when none exists.
	GetPendingByService(ctx context.Context, serviceID int64) (*domain.CancellationRequest, error)
	Update(ctx context.Context, r *domain.CancellationRequest) error
	List(ctx context.Context, p ListParams) ([]domain.CancellationRequest, int64, error)
}

// InvoicePDFItem is one invoice line for PDF rendering.
type InvoicePDFItem struct {
	Description string
	Amount      int64
}

// InvoicePDFData is everything needed to render an invoice PDF.
type InvoicePDFData struct {
	InvoiceNumber string
	Status        domain.InvoiceStatus
	IssuedAt      time.Time
	DueDate       time.Time
	PaidAt        *time.Time
	CompanyName   string
	CompanyAddr   string
	CompanyEmail  string
	ClientName    string
	ClientCompany string
	ClientAddr    string
	ClientEmail   string
	Items         []InvoicePDFItem
	Subtotal      int64
	Discount      int64
	TaxRate       float64
	TaxTotal      int64
	CreditApplied int64
	Total         int64
	Notes         string
}

// PDFGenerator renders documents.
type PDFGenerator interface {
	InvoicePDF(ctx context.Context, inv InvoicePDFData) ([]byte, error)
}

// Cross-service ports (implemented by services, consumed by other services)

// CreateInvoiceItem is one line of a to-be-created invoice.
type CreateInvoiceItem struct {
	Description string
	Amount      int64
	Taxed       bool
	RelatedType domain.InvoiceItemRelatedType
	RelatedID   int64
}

// CreateInvoiceInput describes a new invoice for InvoiceCreator.
type CreateInvoiceInput struct {
	ClientID int64
	Items    []CreateInvoiceItem
	Discount int64 // coupon/order discount, subtracted before tax
	DueDate  time.Time
	Notes    string
}

// InvoiceCreator creates invoices (used by orders, deposits, renewals, upgrades).
type InvoiceCreator interface {
	// CreateInvoice builds an invoice (status unpaid) for the client with the
	// given items, applying tax settings; returns the stored invoice.
	CreateInvoice(ctx context.Context, in CreateInvoiceInput) (*domain.Invoice, error)
}

// PaidInvoiceProcessor dispatches post-payment effects of a paid invoice
// (activation, renewals, upgrades, deposits, receipt + PDF). Implemented by
// billing; called by payments inside the ApplyPayment transaction. Must
// tolerate re-runs.
type PaidInvoiceProcessor interface {
	ProcessPaid(ctx context.Context, invoiceID int64) error
}

// ServiceRenewer advances service billing dates after a paid renewal and
// applies pending upgrades after a paid upgrade invoice. Implemented by
// provisioning; consumed by billing.ProcessPaid.
type ServiceRenewer interface {
	RenewService(ctx context.Context, serviceID int64) error
	ApplyUpgrade(ctx context.Context, serviceID int64) error
}

// DomainRenewer reacts to a paid domain renewal invoice (enqueues the
// registrar renew job). Implemented by domains; consumed by billing.
type DomainRenewer interface {
	RenewDomainAfterPayment(ctx context.Context, domainID int64) error
}

// ApplyTx describes the settling payment for PaymentApplier.
type ApplyTx struct {
	Gateway          domain.Gateway
	MethodCode       string
	MerchantOrderID  string
	GatewayReference string
	Amount           int64
	Fee              int64
	Raw              []byte // raw gateway payload JSON
	PaidAt           time.Time
}

// PaymentApplier is the ONE idempotent entrypoint that marks an invoice paid
// and enqueues follow-up work (activation, PDF, receipt email). Safe to call
// multiple times for the same invoice/payment.
type PaymentApplier interface {
	ApplyPayment(ctx context.Context, invoiceID int64, tx ApplyTx) error
}

// ServiceActivator activates all items of a paid (or on_order) order.
type ServiceActivator interface {
	ActivateOrder(ctx context.Context, orderID int64) error
}

// NotificationSender renders an email template for a user and enqueues the
// mail job. AlertAdmin notifies the operator address (ADMIN_ALERT_EMAIL).
type NotificationSender interface {
	SendTemplate(ctx context.Context, userID int64, templateKey string, data map[string]any) error
	AlertAdmin(ctx context.Context, subject, message string) error
}

// GuestTicketInput describes a ticket opened by an unauthenticated visitor
// (the public "contact us" form). There is no client/user account behind it.
type GuestTicketInput struct {
	DepartmentID int64
	GuestName    string
	GuestEmail   string
	Subject      string
	Message      string
	IP           string
}

// GuestTicketCreator opens a support ticket for an unauthenticated visitor.
// Implemented by the tickets service; consumed by the public contact module.
type GuestTicketCreator interface {
	CreateGuestTicket(ctx context.Context, in GuestTicketInput) (*domain.Ticket, error)
}
