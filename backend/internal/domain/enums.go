// Package domain contains entities, enums, state machines and pure business
// rules for WHCMS. It has no dependencies on transport, storage or any
// external service. Enum string values match the DB CHECK constraints exactly
// (CONTRACTS.md §4).
package domain

// UserRole is the role of a user account.
type UserRole string

const (
	RoleAdmin  UserRole = "admin"
	RoleStaff  UserRole = "staff"
	RoleClient UserRole = "client"
)

// Valid reports whether the role is a known value.
func (r UserRole) Valid() bool {
	switch r {
	case RoleAdmin, RoleStaff, RoleClient:
		return true
	}
	return false
}

// UserStatus is the status of a user account.
type UserStatus string

const (
	UserActive   UserStatus = "active"
	UserInactive UserStatus = "inactive"
)

// ClientStatus is the status of a client profile.
type ClientStatus string

const (
	ClientActive   ClientStatus = "active"
	ClientInactive ClientStatus = "inactive"
	ClientClosed   ClientStatus = "closed"
)

// ProductType classifies products.
type ProductType string

const (
	ProductSharedHosting   ProductType = "shared_hosting"
	ProductResellerHosting ProductType = "reseller_hosting"
	ProductDomain          ProductType = "domain"
	ProductOther           ProductType = "other"
)

// ServerModuleName is the provisioning module of a product/server.
type ServerModuleName string

const (
	ModuleCpanel      ServerModuleName = "cpanel"
	ModuleDirectAdmin ServerModuleName = "directadmin"
	ModuleNone        ServerModuleName = "none"
)

// AutoSetup controls when provisioning happens.
type AutoSetup string

const (
	SetupOnPayment AutoSetup = "on_payment"
	SetupOnOrder   AutoSetup = "on_order"
	SetupManual    AutoSetup = "manual"
)

// BillingCycle is the recurring billing period.
type BillingCycle string

const (
	CycleOneTime      BillingCycle = "one_time"
	CycleMonthly      BillingCycle = "monthly"
	CycleQuarterly    BillingCycle = "quarterly"
	CycleSemiannually BillingCycle = "semiannually"
	CycleAnnually     BillingCycle = "annually"
	CycleBiennially   BillingCycle = "biennially"
)

// Valid reports whether the cycle is a known value.
func (c BillingCycle) Valid() bool {
	switch c {
	case CycleOneTime, CycleMonthly, CycleQuarterly, CycleSemiannually, CycleAnnually, CycleBiennially:
		return true
	}
	return false
}

// OrderStatus is the status of an order.
type OrderStatus string

const (
	OrderPending   OrderStatus = "pending"
	OrderActive    OrderStatus = "active"
	OrderFraud     OrderStatus = "fraud"
	OrderCancelled OrderStatus = "cancelled"
)

// OrderItemType classifies order line items.
type OrderItemType string

const (
	ItemProduct        OrderItemType = "product"
	ItemDomainRegister OrderItemType = "domain_register"
	ItemDomainTransfer OrderItemType = "domain_transfer"
)

// InvoiceStatus is the status of an invoice.
type InvoiceStatus string

const (
	InvoiceDraft     InvoiceStatus = "draft"
	InvoiceUnpaid    InvoiceStatus = "unpaid"
	InvoicePaid      InvoiceStatus = "paid"
	InvoiceOverdue   InvoiceStatus = "overdue"
	InvoiceCancelled InvoiceStatus = "cancelled"
	InvoiceRefunded  InvoiceStatus = "refunded"
)

// InvoiceItemRelatedType classifies what an invoice item bills for.
type InvoiceItemRelatedType string

const (
	RelatedOrderItem      InvoiceItemRelatedType = "order_item"
	RelatedServiceRenewal InvoiceItemRelatedType = "service_renewal"
	RelatedDomainRenewal  InvoiceItemRelatedType = "domain_renewal"
	RelatedServiceUpgrade InvoiceItemRelatedType = "service_upgrade"
	RelatedLateFee        InvoiceItemRelatedType = "late_fee"
	RelatedDeposit        InvoiceItemRelatedType = "deposit"
	RelatedManual         InvoiceItemRelatedType = "manual"
)

// TransactionStatus is the status of a payment transaction.
type TransactionStatus string

const (
	TxPending  TransactionStatus = "pending"
	TxSuccess  TransactionStatus = "success"
	TxFailed   TransactionStatus = "failed"
	TxExpired  TransactionStatus = "expired"
	TxRefunded TransactionStatus = "refunded"
)

// Gateway identifies how an invoice was paid.
type Gateway string

const (
	GatewayDuitku Gateway = "duitku"
	GatewayCredit Gateway = "credit"
	GatewayManual Gateway = "manual"
)

// ServiceStatus is the status of a provisioned service.
type ServiceStatus string

const (
	ServicePending    ServiceStatus = "pending"
	ServiceActive     ServiceStatus = "active"
	ServiceSuspended  ServiceStatus = "suspended"
	ServiceTerminated ServiceStatus = "terminated"
	ServiceCancelled  ServiceStatus = "cancelled"
)

// CancellationRequestStatus is the status of a client cancellation request.
type CancellationRequestStatus string

const (
	CancellationPending       CancellationRequestStatus = "pending"
	CancellationAccepted      CancellationRequestStatus = "accepted"
	CancellationRejected      CancellationRequestStatus = "rejected"
	CancellationAutoProcessed CancellationRequestStatus = "auto_processed"
)

// DomainStatus is the status of a registered domain.
type DomainStatus string

const (
	DomainPending         DomainStatus = "pending"
	DomainActive          DomainStatus = "active"
	DomainPendingTransfer DomainStatus = "pending_transfer"
	DomainExpired         DomainStatus = "expired"
	DomainCancelled       DomainStatus = "cancelled"
)

// TicketStatus is the status of a support ticket.
type TicketStatus string

const (
	TicketOpen          TicketStatus = "open"
	TicketAnswered      TicketStatus = "answered"
	TicketCustomerReply TicketStatus = "customer_reply"
	TicketOnHold        TicketStatus = "on_hold"
	TicketClosed        TicketStatus = "closed"
)

// TicketPriority is the priority of a support ticket.
type TicketPriority string

const (
	PriorityLow    TicketPriority = "low"
	PriorityMedium TicketPriority = "medium"
	PriorityHigh   TicketPriority = "high"
)

// CouponType is the discount type of a coupon.
type CouponType string

const (
	CouponPercentage CouponType = "percentage"
	CouponFixed      CouponType = "fixed"
)

// EmailStatus is the delivery status of a logged email.
type EmailStatus string

const (
	EmailQueued EmailStatus = "queued"
	EmailSent   EmailStatus = "sent"
	EmailFailed EmailStatus = "failed"
)

// ServerGroupStrategy picks how servers are selected within a group.
type ServerGroupStrategy string

const (
	StrategyRoundRobin ServerGroupStrategy = "round_robin"
	StrategyLeastUsed  ServerGroupStrategy = "least_used"
)

// NetworkIssueType classifies a network status entry.
type NetworkIssueType string

const (
	NetworkTypeScheduled NetworkIssueType = "scheduled"
	NetworkTypeIssue     NetworkIssueType = "issue"
	NetworkTypeOutage    NetworkIssueType = "outage"
)

// Valid reports whether the network issue type is a known value.
func (t NetworkIssueType) Valid() bool {
	switch t {
	case NetworkTypeScheduled, NetworkTypeIssue, NetworkTypeOutage:
		return true
	}
	return false
}

// NetworkIssueSeverity is the severity of a network status entry.
type NetworkIssueSeverity string

const (
	NetworkSeverityMinor    NetworkIssueSeverity = "minor"
	NetworkSeverityMajor    NetworkIssueSeverity = "major"
	NetworkSeverityCritical NetworkIssueSeverity = "critical"
)

// Valid reports whether the network issue severity is a known value.
func (s NetworkIssueSeverity) Valid() bool {
	switch s {
	case NetworkSeverityMinor, NetworkSeverityMajor, NetworkSeverityCritical:
		return true
	}
	return false
}

// NetworkIssueStatus is the current status of a network status entry.
type NetworkIssueStatus string

const (
	NetworkStatusInvestigating NetworkIssueStatus = "investigating"
	NetworkStatusIdentified    NetworkIssueStatus = "identified"
	NetworkStatusMonitoring    NetworkIssueStatus = "monitoring"
	NetworkStatusResolved      NetworkIssueStatus = "resolved"
	NetworkStatusScheduled     NetworkIssueStatus = "scheduled"
)

// Valid reports whether the network issue status is a known value.
func (s NetworkIssueStatus) Valid() bool {
	switch s {
	case NetworkStatusInvestigating, NetworkStatusIdentified, NetworkStatusMonitoring,
		NetworkStatusResolved, NetworkStatusScheduled:
		return true
	}
	return false
}
