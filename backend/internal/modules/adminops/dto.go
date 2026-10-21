package adminops

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
)

// Dashboard

// ExtraStats are the dashboard KPIs beyond ports.DashboardStats.
type ExtraStats struct {
	OrdersToday     int64 `json:"orders_today"`
	UnpaidTotal     int64 `json:"unpaid_total"`
	OverdueTotal    int64 `json:"overdue_total"`
	ServicesPending int64 `json:"services_pending"`
}

// RecentOrderRow is one row of the dashboard's "Recent Orders" widget.
type RecentOrderRow struct {
	ID          int64     `json:"id"`
	OrderNumber string    `json:"order_number"`
	ClientID    int64     `json:"client_id"`
	ClientName  string    `json:"client_name"`
	Status      string    `json:"status"`
	Total       int64     `json:"total"`
	CreatedAt   time.Time `json:"created_at"`
}

// RecentTicketRow is one row of the dashboard's "Recent Tickets" widget.
type RecentTicketRow struct {
	ID           int64      `json:"id"`
	TicketNumber string     `json:"ticket_number"`
	ClientID     *int64     `json:"client_id,omitempty"`
	Subject      string     `json:"subject"`
	Status       string     `json:"status"`
	Priority     string     `json:"priority"`
	LastReplyAt  *time.Time `json:"last_reply_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// recentRowsLimit bounds the dashboard's Recent Orders/Tickets widgets.
const recentRowsLimit = 5

// DashboardData is the full admin dashboard payload (cached 60s).
type DashboardData struct {
	Stats          ports.DashboardStats `json:"stats"`
	Extra          ExtraStats           `json:"extra"`
	RecentActivity []domain.AuditLog    `json:"recent_activity"`
	RecentOrders   []RecentOrderRow     `json:"recent_orders"`
	RecentTickets  []RecentTicketRow    `json:"recent_tickets"`
	// ModuleActionsPending is the total row count of the Pending Module
	// Actions queue (archived + retry, across every module-action type) -
	// sourced from Redis (ports.JobInspector), not the DB-backed repo query
	// the rest of this struct comes from. Best-effort: 0 if Jobs is unset
	// or the lookup fails, never blocks the rest of the dashboard.
	ModuleActionsPending int64     `json:"module_actions_pending"`
	GeneratedAt          time.Time `json:"generated_at"`
}

// Reports

// GatewayRevenue is the revenue split for one payment gateway.
type GatewayRevenue struct {
	Gateway string `json:"gateway"`
	Amount  int64  `json:"amount"`
	Count   int64  `json:"count"`
}

// ProductStatusCount is one row of the services-by-product report.
type ProductStatusCount struct {
	Product string `json:"product"`
	Status  string `json:"status"`
	Count   int64  `json:"count"`
}

// RevenueReportData is the revenue report payload.
type RevenueReportData struct {
	From        string               `json:"from"`
	To          string               `json:"to"`
	GroupBy     string               `json:"group_by"`
	Points      []ports.RevenuePoint `json:"points"`
	TotalAmount int64                `json:"total_amount"`
	TotalCount  int64                `json:"total_count"`
	ByGateway   []GatewayRevenue     `json:"by_gateway"`
}

// CSV renders the revenue report as CSV (period,amount,count + totals row).
func (d *RevenueReportData) CSV() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"period", "amount", "count"})
	for _, p := range d.Points {
		_ = w.Write([]string{p.Period, strconv.FormatInt(p.Amount, 10), strconv.FormatInt(p.Count, 10)})
	}
	_ = w.Write([]string{"TOTAL", strconv.FormatInt(d.TotalAmount, 10), strconv.FormatInt(d.TotalCount, 10)})
	w.Flush()
	return buf.Bytes()
}

// OrderStatusCount is one row of the orders-by-status breakdown.
type OrderStatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
	Amount int64  `json:"amount"`
}

// OrdersReportData is the orders report payload (grouped per day, plus a
// breakdown by order status over the same range).
type OrdersReportData struct {
	From        string               `json:"from"`
	To          string               `json:"to"`
	Points      []ports.RevenuePoint `json:"points"`
	TotalAmount int64                `json:"total_amount"`
	TotalCount  int64                `json:"total_count"`
	ByStatus    []OrderStatusCount   `json:"by_status"`
}

// CSV renders the orders report as CSV (period,amount,count + totals row),
// followed by the by-status breakdown (status,amount,count) after a blank line.
func (d *OrdersReportData) CSV() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"period", "amount", "count"})
	for _, p := range d.Points {
		_ = w.Write([]string{p.Period, strconv.FormatInt(p.Amount, 10), strconv.FormatInt(p.Count, 10)})
	}
	_ = w.Write([]string{"TOTAL", strconv.FormatInt(d.TotalAmount, 10), strconv.FormatInt(d.TotalCount, 10)})
	if len(d.ByStatus) > 0 {
		_ = w.Write(nil)
		_ = w.Write([]string{"status", "amount", "count"})
		for _, s := range d.ByStatus {
			_ = w.Write([]string{s.Status, strconv.FormatInt(s.Amount, 10), strconv.FormatInt(s.Count, 10)})
		}
	}
	w.Flush()
	return buf.Bytes()
}

// ServicesReportData is the services report payload.
type ServicesReportData struct {
	ByStatus  map[string]int64     `json:"by_status"`
	ByProduct []ProductStatusCount `json:"by_product"`
}

// CSV renders the services report as CSV (product,status,count).
func (d *ServicesReportData) CSV() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"product", "status", "count"})
	for _, row := range d.ByProduct {
		_ = w.Write([]string{row.Product, row.Status, strconv.FormatInt(row.Count, 10)})
	}
	w.Flush()
	return buf.Bytes()
}

// Staff management

// CreateStaffInput creates a staff user (role is always staff).
type CreateStaffInput struct {
	Email       string          `json:"email" validate:"required,email"`
	Password    string          `json:"password" validate:"required,min=8"`
	Permissions map[string]bool `json:"permissions"`
	Locale      string          `json:"locale" validate:"omitempty,oneof=id en"`
}

// UpdateStaffInput patches a staff user; nil fields are left unchanged.
type UpdateStaffInput struct {
	Permissions map[string]bool `json:"permissions"` // nil = unchanged
	Status      *string         `json:"status" validate:"omitempty,oneof=active inactive"`
	Password    *string         `json:"password" validate:"omitempty,min=8"`
	Locale      *string         `json:"locale" validate:"omitempty,oneof=id en"`
}

// Logs

// AuditLogFilter filters the audit log listing. From/To are whole dates:
// From is inclusive, To is inclusive (the service converts it to an
// exclusive next-day bound before querying).
type AuditLogFilter struct {
	UserID  *int64
	Entity  string
	Action  string // prefix match
	From    *time.Time
	To      *time.Time
	Page    int
	PerPage int
}

// IntegrationLogFilter filters the integration log listing.
type IntegrationLogFilter struct {
	Provider string
	Success  *bool
	Page     int
	PerPage  int
}

// Gateways

// UpdateGatewaysInput updates the duitku gateway config. APIKey is an
// admin-configurable dynamic credential (encrypted at rest, env var as
// fallback when unset) - nil means "don't touch", tri-state like
// domains.UpdateRegistrarRequest.APIKey: nil = unchanged, "" = clear the
// stored key (only ever sent when the admin explicitly checks "Clear stored
// key"), non-empty = encrypt and store. BaseURL is a non-secret "custom
// endpoint" override (blank = derive from Mode), same idea as
// domains.UpdateRegistrarRequest.BaseURL.
type UpdateGatewaysInput struct {
	MerchantCode string  `json:"merchant_code" validate:"required"`
	Mode         string  `json:"mode" validate:"required,oneof=sandbox production"`
	BaseURL      string  `json:"base_url"`
	APIKey       *string `json:"api_key"`
}

// DuitkuGatewayConfig is the duitku gateway config exposed to admins.
// The API key itself never leaves the server: only whether one is set
// (from the DB or the environment) is exposed (APIKeySet).
type DuitkuGatewayConfig struct {
	MerchantCode string `json:"merchant_code"`
	Mode         string `json:"mode"`
	BaseURL      string `json:"base_url"`
	APIKeySet    bool   `json:"api_key_set"`
}

// GatewaysConfig is the payload of GET /admin/gateways.
type GatewaysConfig struct {
	Duitku DuitkuGatewayConfig `json:"duitku"`
	Manual ManualGatewayConfig `json:"manual"`
}

// GatewaySecrets carries ENV-derived gateway values into the service:
// non-secret fallbacks plus presence booleans for the secrets themselves.
type GatewaySecrets struct {
	DuitkuMerchantCode string // ENV fallback for the merchant code (non-secret)
	DuitkuMode         string // ENV fallback for the mode (sandbox|production)
	DuitkuBaseURL      string // ENV fallback for the base URL override (non-secret)
	DuitkuAPIKeySet    bool   // whether DUITKU_API_KEY is set (never the value)
}

// BankAccountInput is one bank-transfer destination in an admin request -
// kept separate from ports.BankAccount (which carries no validate: tags -
// ports types are foundation-owned) so each row can be validated.
type BankAccountInput struct {
	BankName      string `json:"bank_name" validate:"required"`
	AccountNumber string `json:"account_number" validate:"required"`
	AccountHolder string `json:"account_holder" validate:"required"`
}

// UpdateManualGatewayInput updates the manual bank-transfer gateway config.
// Unlike Duitku's API key, nothing here is secret - bank account numbers are
// display instructions, not credentials - so the full config round-trips.
type UpdateManualGatewayInput struct {
	Enabled      bool               `json:"enabled"`
	Accounts     []BankAccountInput `json:"accounts" validate:"dive"`
	Instructions string             `json:"instructions"`
}

// ManualGatewayConfig is the manual bank-transfer gateway config exposed to
// admins (and read live by the manual adapter - internal/integration/manual).
type ManualGatewayConfig struct {
	Enabled      bool                `json:"enabled"`
	Accounts     []ports.BankAccount `json:"accounts"`
	Instructions string              `json:"instructions"`
}
