// Package billing implements the M-BILLING module: invoice creation (with
// numbering, tax and discounts), the paid-invoice dispatcher (ProcessPaid),
// renewal invoice generation, overdue marking, reminders, late fees, invoice
// PDF generation, and the client + admin invoice HTTP endpoints.
//
// Route table (mounted on the /api/v1 group by the composition root):
//
//	GET   /invoices                      client's invoices                 [auth+client]
//	GET   /invoices/:id                  invoice detail (items)            [auth+client]
//	GET   /invoices/:id/pdf              stream invoice PDF                [auth+client]
//	GET   /admin/invoices                ?search&status&page&per_page      [perm: billing]
//	POST  /admin/invoices                create manual invoice             [perm: billing]
//	GET   /admin/invoices/:id            invoice detail (items)            [perm: billing]
//	PATCH /admin/invoices/:id            edit due date / notes             [perm: billing]
//	POST  /admin/invoices/:id/payment    record manual payment             [perm: billing]
//	POST  /admin/invoices/:id/cancel     cancel unpaid/overdue invoice     [perm: billing]
//	POST  /admin/invoices/:id/refund     mark refunded (+optional credit)  [perm: billing]
//
// Cron/job entrypoints consumed by the worker: GenerateRenewalInvoices,
// MarkOverdue, SendReminders, ApplyLateFees (all (int, error)) and
// GenerateInvoicePDF(ctx, invoiceID) for invoice:generate_pdf.
//
// POST /invoices/:id/pay is owned by the payments module.
package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/jobs"
	"github.com/tsdlamongan/whcms/backend/internal/platform/validate"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Settings keys read by this module (CONTRACTS.md §10).
const (
	settingTaxEnabled          = "billing.tax_enabled"
	settingTaxRate             = "billing.tax_rate"
	settingTaxInclusive        = "billing.tax_inclusive"
	settingInvoiceDueDays      = "billing.invoice_due_days"
	settingRenewalLeadDays     = "billing.renewal_lead_days"
	settingLateFeeEnabled      = "billing.late_fee_enabled"
	settingLateFeeAmount       = "billing.late_fee_amount"
	settingReminderDays        = "billing.reminder_days"
	settingOverdueReminderDays = "billing.overdue_reminder_days"
	settingProformaEnabled     = "billing.proforma_enabled"
	settingCompanyName         = "company.name"
	settingCompanyAddress      = "company.address"
	settingCompanyEmail        = "company.email"
)

// Notification template keys used by this module (seeded in migrations).
const (
	tplInvoiceCreated  = "invoice_created"
	tplInvoiceReminder = "invoice_reminder"
	tplInvoiceOverdue  = "invoice_overdue"
	tplPaymentReceived = "payment_received"
)

// InvoiceStore is ports.InvoiceRepo plus the module-local queries
// (implemented by *Repo).
type InvoiceStore interface {
	ports.InvoiceRepo
	ports.RenewalInvoiceChecker
	// OrderIDForOrderItem resolves the order an order_item row belongs to.
	OrderIDForOrderItem(ctx context.Context, orderItemID int64) (int64, error)
	// HasEmailSince reports whether an email with templateKey referencing ref
	// was logged at/after since (reminder dedupe over email_log).
	HasEmailSince(ctx context.Context, templateKey, ref string, since time.Time) (bool, error)
}

// CreditService is the narrow clients-module surface billing consumes
// (MODULES.md §2 exact signatures; satisfied by the clients Service at wiring).
type CreditService interface {
	AddCredit(ctx context.Context, clientID int64, delta int64, reason string, relatedInvoiceID int64) error
	DeductCredit(ctx context.Context, clientID int64, amount int64, reason string, relatedInvoiceID int64) error
}

// Deps are the billing service dependencies (wired by the composition root).
type Deps struct {
	Tx            ports.TxManager
	Invoices      InvoiceStore           // *Repo
	Transactions  ports.TransactionRepo  // refund marking (payments owns the impl)
	Clients       ports.ClientRepo       // client lookups (user id, PDF address block)
	Users         ports.UserRepo         // client email for PDF
	Services      ports.ServiceRepo      // renewal generation (provisioning owns the impl)
	Domains       ports.DomainRepo       // renewal generation (domains owns the impl)
	Coupons       ports.CouponRepo       // recurring renewal coupons (catalog owns the impl)
	Settings      ports.SettingsRepo     // tax / billing settings
	Credit        CreditService          // clients module AddCredit/DeductCredit
	Payments      ports.PaymentApplier   // admin manual payment (payments module)
	Activator     ports.ServiceActivator // orders module
	Renewer       ports.ServiceRenewer   // provisioning module
	DomainRenewer ports.DomainRenewer    // domains module
	Snipes        ports.SnipeActivator   // snipe module
	Notifier      ports.NotificationSender
	PDF           ports.PDFGenerator
	Storage       ports.Storage
	Enqueuer      ports.Enqueuer
	Audit         ports.AuditLogger
	Clock         ports.Clock
	FrontendURL   string // for InvoiceURL template data, e.g. http://localhost:5173
}

// Service implements the billing use-cases. It implements
// ports.InvoiceCreator and ports.PaidInvoiceProcessor.
type Service struct {
	d   Deps
	val *validate.Validator
}

// Compile-time cross-service port checks.
var (
	_ ports.InvoiceCreator       = (*Service)(nil)
	_ ports.PaidInvoiceProcessor = (*Service)(nil)
)

// New builds the billing Service.
func New(d Deps) *Service {
	return &Service{d: d, val: validate.New()}
}

func dateOnly(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Service) invoiceURL(id int64) string {
	return strings.TrimSuffix(s.d.FrontendURL, "/") + fmt.Sprintf("/billing/invoices/%d", id)
}

// CreateInvoice (ports.InvoiceCreator)

// CreateInvoice builds an unpaid invoice for the client: assigns the number
// from the monthly counter inside the transaction, applies the discount
// before tax, and computes tax from settings on taxed items only.
func (s *Service) CreateInvoice(ctx context.Context, in ports.CreateInvoiceInput) (*domain.Invoice, error) {
	if in.ClientID <= 0 {
		return nil, apperr.Validation("client_id is required")
	}
	if len(in.Items) == 0 {
		return nil, apperr.Validation("at least one invoice item is required")
	}
	if in.Discount < 0 {
		return nil, apperr.Validation("discount must not be negative")
	}
	var subtotal, taxedSum int64
	for _, it := range in.Items {
		if it.Amount < 0 {
			return nil, apperr.Validation("item amount must not be negative")
		}
		subtotal += it.Amount
		if it.Taxed {
			taxedSum += it.Amount
		}
	}
	if _, err := s.d.Clients.GetByID(ctx, in.ClientID); err != nil {
		return nil, err
	}

	now := s.d.Clock.Now().UTC()
	due := in.DueDate
	if due.IsZero() {
		dueDays, err := s.d.Settings.GetInt(ctx, settingInvoiceDueDays, 3)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		due = dateOnly(now).AddDate(0, 0, dueDays)
	}

	discount := in.Discount
	if discount > subtotal {
		discount = subtotal
	}

	taxEnabled, err := s.d.Settings.GetBool(ctx, settingTaxEnabled, false)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	var taxRate float64
	var taxTotal int64
	total := subtotal - discount
	if taxEnabled && taxedSum > 0 {
		rateInt, err := s.d.Settings.GetInt(ctx, settingTaxRate, 11)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		inclusive, err := s.d.Settings.GetBool(ctx, settingTaxInclusive, false)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		if rateInt > 0 {
			taxRate = float64(rateInt)
			// Discount reduces the taxable base first.
			taxedBase := taxedSum - discount
			if taxedBase < 0 {
				taxedBase = 0
			}
			res := domain.CalcTax(taxedBase, taxRate, inclusive)
			taxTotal = res.Tax
			if !inclusive {
				total += taxTotal
			}
		}
	}

	inv := &domain.Invoice{
		ClientID: in.ClientID,
		Status:   domain.InvoiceUnpaid,
		Subtotal: subtotal,
		Discount: discount,
		TaxRate:  taxRate,
		TaxTotal: taxTotal,
		Total:    total,
		Currency: "IDR",
		DueDate:  dateOnly(due),
		Notes:    in.Notes,
	}
	items := make([]domain.InvoiceItem, 0, len(in.Items))
	for _, it := range in.Items {
		item := domain.InvoiceItem{
			Description: it.Description,
			Amount:      it.Amount,
			Taxed:       it.Taxed,
			RelatedType: it.RelatedType,
		}
		if it.RelatedID != 0 {
			id := it.RelatedID
			item.RelatedID = &id
		}
		items = append(items, item)
	}

	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		n, err := s.d.Invoices.NextNumber(txCtx, domain.InvoiceCounterScope(now))
		if err != nil {
			return err
		}
		inv.InvoiceNumber = domain.FormatInvoiceNumber(now, n)
		return s.d.Invoices.Create(txCtx, inv, items)
	})
	if err != nil {
		if e := apperr.From(err); e.Code != apperr.CodeInternal {
			return nil, e
		}
		return nil, apperr.Internal(err)
	}

	s.d.Audit.Log(ctx, 0, "invoice.create", "invoice", inv.ID, nil, map[string]any{
		"invoice_number": inv.InvoiceNumber,
		"client_id":      inv.ClientID,
		"total":          inv.Total,
		"due_date":       inv.DueDate.Format("2006-01-02"),
	})
	return inv, nil
}

// ProcessPaid (ports.PaidInvoiceProcessor)

// ProcessPaid dispatches the post-payment effects of a paid invoice per item
// related_type: order activation, service/domain renewals, pending upgrades
// and deposit credit; then sends the receipt and enqueues PDF generation.
// The caller (payments.ApplyPayment) guarantees a single unpaid->paid
// transition; consumed providers are idempotent so a re-run is tolerated.
func (s *Service) ProcessPaid(ctx context.Context, invoiceID int64) error {
	inv, err := s.d.Invoices.GetByID(ctx, invoiceID)
	if err != nil {
		return err
	}
	if inv.Status != domain.InvoicePaid {
		return apperr.Newf(apperr.CodeConflict, "invoice %s is not paid", inv.InvoiceNumber)
	}
	items, err := s.d.Invoices.GetItems(ctx, invoiceID)
	if err != nil {
		return err
	}

	activatedOrders := map[int64]bool{}
	for _, it := range items {
		relatedID := int64(0)
		if it.RelatedID != nil {
			relatedID = *it.RelatedID
		}
		switch it.RelatedType {
		case domain.RelatedOrderItem:
			if relatedID == 0 {
				continue
			}
			orderID, err := s.d.Invoices.OrderIDForOrderItem(ctx, relatedID)
			if err != nil {
				return err
			}
			if activatedOrders[orderID] {
				continue
			}
			if err := s.d.Activator.ActivateOrder(ctx, orderID); err != nil {
				return err
			}
			activatedOrders[orderID] = true
		case domain.RelatedServiceRenewal:
			if relatedID == 0 {
				continue
			}
			if err := s.d.Renewer.RenewService(ctx, relatedID); err != nil {
				return err
			}
		case domain.RelatedServiceUpgrade:
			if relatedID == 0 {
				continue
			}
			if err := s.d.Renewer.ApplyUpgrade(ctx, relatedID); err != nil {
				return err
			}
		case domain.RelatedDomainRenewal:
			if relatedID == 0 {
				continue
			}
			if err := s.d.DomainRenewer.RenewDomainAfterPayment(ctx, relatedID); err != nil {
				return err
			}
		case domain.RelatedDomainSnipe:
			if relatedID == 0 {
				continue
			}
			if err := s.d.Snipes.ActivateSnipeAfterPayment(ctx, relatedID); err != nil {
				return err
			}
		case domain.RelatedDeposit:
			if err := s.d.Credit.AddCredit(ctx, inv.ClientID, it.Amount,
				"Deposit "+inv.InvoiceNumber, inv.ID); err != nil {
				return err
			}
		}
	}

	// Receipt + PDF are best-effort: a paid invoice must never fail on them.
	s.notifyClient(ctx, inv.ClientID, tplPaymentReceived, map[string]any{
		"InvoiceNumber": inv.InvoiceNumber,
		"Amount":        domain.FormatIDR(inv.Total),
		"InvoiceURL":    s.invoiceURL(inv.ID),
	})
	_ = s.d.Enqueuer.Enqueue(ctx, jobs.TypeInvoiceGeneratePDF,
		jobs.InvoiceGeneratePDFPayload{InvoiceID: inv.ID})
	return nil
}

// notifyClient resolves the client's user and sends the template; failures
// are swallowed (notifications are never fatal to billing flows).
func (s *Service) notifyClient(ctx context.Context, clientID int64, templateKey string, data map[string]any) {
	client, err := s.d.Clients.GetByID(ctx, clientID)
	if err != nil || client == nil {
		return
	}
	if _, ok := data["Name"]; !ok {
		data["Name"] = client.FullName()
	}
	_ = s.d.Notifier.SendTemplate(ctx, client.UserID, templateKey, data)
}

// Cron: renewal invoice generation

// panelMetaCancelAtPeriodEnd reads the cancel_at_period_end flag stored by
// provisioning in services.panel_meta.
func panelMetaCancelAtPeriodEnd(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var meta struct {
		CancelAtPeriodEnd bool `json:"cancel_at_period_end"`
	}
	if json.Unmarshal(raw, &meta) != nil {
		return false
	}
	return meta.CancelAtPeriodEnd
}

// recurringDiscount computes the renewal discount for a recurring coupon.
func (s *Service) recurringDiscount(ctx context.Context, couponID *int64, amount int64) int64 {
	if couponID == nil || *couponID == 0 || amount <= 0 {
		return 0
	}
	coupon, err := s.d.Coupons.GetByID(ctx, *couponID)
	if err != nil || coupon == nil || !coupon.Active || !coupon.Recurring {
		return 0
	}
	if coupon.ExpiresAt != nil && coupon.ExpiresAt.Before(s.d.Clock.Now()) {
		return 0
	}
	var discount int64
	switch coupon.Type {
	case domain.CouponPercentage:
		discount = amount * coupon.Value / 100
	case domain.CouponFixed:
		discount = coupon.Value
	}
	if discount > amount {
		discount = amount
	}
	if discount < 0 {
		discount = 0
	}
	return discount
}

// Renewal-invoice skip reasons, surfaced to callers that need to report why
// an entity didn't get invoiced (e.g. the admin "invoice selected items"
// action).
const (
	skipAlreadyInvoiced   = "already invoiced"
	skipNotEligible       = "not eligible for renewal invoicing"
	skipCancelAtPeriodEnd = "cancellation scheduled at period end"
)

// generateServiceRenewalInvoice locks serviceID's row, re-validates
// eligibility and the open-invoice dedupe check inside that same locked
// transaction, and creates the renewal invoice if none is already open. The
// row lock (services.id, held for the duration of the transaction) is what
// makes this safe against a concurrent call for the same service - whether
// from the nightly cron, a manual "invoice selected items" request, or a
// retried job: the second caller blocks on the lock, then observes the open
// invoice the first caller just created and skips instead of duplicating.
// Returns the created invoice (nil if skipped) and, when skipped, a
// human-readable reason.
func (s *Service) generateServiceRenewalInvoice(ctx context.Context, serviceID int64) (*domain.Invoice, string, error) {
	var inv *domain.Invoice
	var skipReason string
	err := s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		svc, err := s.d.Services.GetByIDForUpdate(ctx, serviceID)
		if err != nil {
			return err
		}
		if svc.Status != domain.ServiceActive || svc.NextDueDate == nil || svc.RecurringAmount <= 0 {
			skipReason = skipNotEligible
			return nil
		}
		if panelMetaCancelAtPeriodEnd(svc.PanelMeta) {
			skipReason = skipCancelAtPeriodEnd
			return nil
		}
		open, err := s.d.Invoices.HasOpenRenewalInvoice(ctx, domain.RelatedServiceRenewal, svc.ID)
		if err != nil {
			return err
		}
		if open {
			skipReason = skipAlreadyInvoiced
			return nil
		}
		name := svc.Domain
		if name == "" {
			name = fmt.Sprintf("Service #%d", svc.ID)
		}
		desc := fmt.Sprintf("Renewal: %s (%s) - due %s",
			name, svc.BillingCycle, svc.NextDueDate.Format("2006-01-02"))
		created, err := s.CreateInvoice(ctx, ports.CreateInvoiceInput{
			ClientID: svc.ClientID,
			Items: []ports.CreateInvoiceItem{{
				Description: desc,
				Amount:      svc.RecurringAmount,
				Taxed:       true,
				RelatedType: domain.RelatedServiceRenewal,
				RelatedID:   svc.ID,
			}},
			Discount: s.recurringDiscount(ctx, svc.CouponID, svc.RecurringAmount),
			DueDate:  *svc.NextDueDate,
		})
		if err != nil {
			return err
		}
		inv = created
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if inv != nil {
		s.notifyInvoiceCreated(ctx, inv)
	}
	return inv, skipReason, nil
}

// generateDomainRenewalInvoice is generateServiceRenewalInvoice's domain
// counterpart - same row-lock + dedupe-inside-transaction guarantee, no
// coupon discount, no cancel_at_period_end concept.
func (s *Service) generateDomainRenewalInvoice(ctx context.Context, domainID int64) (*domain.Invoice, string, error) {
	var inv *domain.Invoice
	var skipReason string
	err := s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		dom, err := s.d.Domains.GetByIDForUpdate(ctx, domainID)
		if err != nil {
			return err
		}
		if dom.Status != domain.DomainActive || dom.NextDueDate == nil || dom.RecurringAmount <= 0 {
			skipReason = skipNotEligible
			return nil
		}
		open, err := s.d.Invoices.HasOpenRenewalInvoice(ctx, domain.RelatedDomainRenewal, dom.ID)
		if err != nil {
			return err
		}
		if open {
			skipReason = skipAlreadyInvoiced
			return nil
		}
		desc := fmt.Sprintf("Domain renewal: %s (1 year) - due %s",
			dom.Name, dom.NextDueDate.Format("2006-01-02"))
		created, err := s.CreateInvoice(ctx, ports.CreateInvoiceInput{
			ClientID: dom.ClientID,
			Items: []ports.CreateInvoiceItem{{
				Description: desc,
				Amount:      dom.RecurringAmount,
				Taxed:       true,
				RelatedType: domain.RelatedDomainRenewal,
				RelatedID:   dom.ID,
			}},
			DueDate: *dom.NextDueDate,
		})
		if err != nil {
			return err
		}
		inv = created
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if inv != nil {
		s.notifyInvoiceCreated(ctx, inv)
	}
	return inv, skipReason, nil
}

// GenerateRenewalInvoices creates renewal invoices for active services and
// domains whose next_due_date falls within billing.renewal_lead_days,
// skipping entities that already have an open (unpaid/overdue) renewal
// invoice and services flagged cancel_at_period_end. Returns the number of
// invoices created; per-entity failures are joined and do not stop the run.
func (s *Service) GenerateRenewalInvoices(ctx context.Context) (int, error) {
	lead, err := s.d.Settings.GetInt(ctx, settingRenewalLeadDays, 14)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	today := dateOnly(s.d.Clock.Now())
	before := today.AddDate(0, 0, lead)

	count := 0
	var errs []error

	services, err := s.d.Services.ListRenewalsDue(ctx, before)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	for _, svc := range services {
		inv, _, err := s.generateServiceRenewalInvoice(ctx, svc.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("service %d: %w", svc.ID, err))
			continue
		}
		if inv != nil {
			count++
		}
	}

	domains, err := s.d.Domains.ListRenewalsDue(ctx, before)
	if err != nil {
		return count, errors.Join(append(errs, apperr.Internal(err))...)
	}
	for _, dom := range domains {
		inv, _, err := s.generateDomainRenewalInvoice(ctx, dom.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("domain %d: %w", dom.ID, err))
			continue
		}
		if inv != nil {
			count++
		}
	}
	return count, errors.Join(errs...)
}

// GenerateSelectedRenewalInvoices force-generates renewal invoices for an
// explicit admin-picked set of services/domains - the "Invoice Selected
// Items" action, mirroring WHMCS exactly: no billing.renewal_lead_days
// window check, the only gate is eligibility + the open-invoice dedupe.
// It reuses generateServiceRenewalInvoice/generateDomainRenewalInvoice, the
// same row-locked helpers the nightly cron uses, so this can never race
// GenerateRenewalInvoices (or another concurrent call to this same method)
// into creating a duplicate invoice for the same service/domain - whichever
// caller's transaction commits first "wins" the dedupe check for the other.
func (s *Service) GenerateSelectedRenewalInvoices(ctx context.Context, actorUserID int64, in GenerateSelectedInvoicesInput) (*GenerateSelectedInvoicesResult, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	serviceIDs, domainIDs := in.ServiceIDs, in.DomainIDs
	if len(serviceIDs) == 0 && len(domainIDs) == 0 {
		return nil, apperr.Validation("select at least one service or domain")
	}
	// Non-nil so the JSON response always has "created"/"skipped" arrays
	// (never null) regardless of how many items land in each.
	res := &GenerateSelectedInvoicesResult{
		Created: []GeneratedInvoiceSummary{},
		Skipped: []SkippedItem{},
	}

	if len(serviceIDs) > 0 {
		services, err := s.d.Services.GetByIDs(ctx, serviceIDs)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		found := make(map[int64]bool, len(services))
		for _, svc := range services {
			found[svc.ID] = true
			inv, reason, err := s.generateServiceRenewalInvoice(ctx, svc.ID)
			if err != nil {
				res.Skipped = append(res.Skipped, SkippedItem{Type: "service", ID: svc.ID, Reason: err.Error()})
				continue
			}
			if inv != nil {
				res.Created = append(res.Created, GeneratedInvoiceSummary{
					Type: "service", ID: svc.ID, InvoiceID: inv.ID, InvoiceNumber: inv.InvoiceNumber,
				})
				continue
			}
			res.Skipped = append(res.Skipped, SkippedItem{Type: "service", ID: svc.ID, Reason: reason})
		}
		for _, id := range serviceIDs {
			if !found[id] {
				res.Skipped = append(res.Skipped, SkippedItem{Type: "service", ID: id, Reason: "not found"})
			}
		}
	}

	if len(domainIDs) > 0 {
		domainsList, err := s.d.Domains.GetByIDs(ctx, domainIDs)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		found := make(map[int64]bool, len(domainsList))
		for _, dom := range domainsList {
			found[dom.ID] = true
			inv, reason, err := s.generateDomainRenewalInvoice(ctx, dom.ID)
			if err != nil {
				res.Skipped = append(res.Skipped, SkippedItem{Type: "domain", ID: dom.ID, Reason: err.Error()})
				continue
			}
			if inv != nil {
				res.Created = append(res.Created, GeneratedInvoiceSummary{
					Type: "domain", ID: dom.ID, InvoiceID: inv.ID, InvoiceNumber: inv.InvoiceNumber,
				})
				continue
			}
			res.Skipped = append(res.Skipped, SkippedItem{Type: "domain", ID: dom.ID, Reason: reason})
		}
		for _, id := range domainIDs {
			if !found[id] {
				res.Skipped = append(res.Skipped, SkippedItem{Type: "domain", ID: id, Reason: "not found"})
			}
		}
	}

	s.d.Audit.Log(ctx, actorUserID, "invoice.generate_selected", "invoice", 0, nil, map[string]any{
		"service_ids": serviceIDs,
		"domain_ids":  domainIDs,
		"created":     len(res.Created),
		"skipped":     len(res.Skipped),
	})
	return res, nil
}

func (s *Service) notifyInvoiceCreated(ctx context.Context, inv *domain.Invoice) {
	s.notifyClient(ctx, inv.ClientID, tplInvoiceCreated, map[string]any{
		"InvoiceNumber": inv.InvoiceNumber,
		"Total":         domain.FormatIDR(inv.Total),
		"DueDate":       inv.DueDate.Format("2006-01-02"),
		"InvoiceURL":    s.invoiceURL(inv.ID),
	})
}

// Cron: overdue marking, reminders, late fees

// MarkOverdue transitions unpaid invoices past their due date to overdue and
// notifies the client. Returns the number of invoices marked.
func (s *Service) MarkOverdue(ctx context.Context) (int, error) {
	today := dateOnly(s.d.Clock.Now())
	list, err := s.d.Invoices.ListDueForStatus(ctx, domain.InvoiceUnpaid, today.AddDate(0, 0, -1))
	if err != nil {
		return 0, apperr.Internal(err)
	}
	count := 0
	var errs []error
	for _, inv := range list {
		next, err := domain.TransitionInvoice(inv.Status, domain.InvoiceOverdue)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.d.Invoices.UpdateStatus(ctx, inv.ID, next, nil); err != nil {
			errs = append(errs, err)
			continue
		}
		s.notifyClient(ctx, inv.ClientID, tplInvoiceOverdue, map[string]any{
			"InvoiceNumber": inv.InvoiceNumber,
			"Total":         domain.FormatIDR(inv.Total),
			"DueDate":       inv.DueDate.Format("2006-01-02"),
			"InvoiceURL":    s.invoiceURL(inv.ID),
		})
		count++
	}
	return count, errors.Join(errs...)
}

func daysBetween(from, to time.Time) int {
	return int(dateOnly(to).Sub(dateOnly(from)).Hours() / 24)
}

func containsInt(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

// SendReminders sends pre-due reminders (billing.reminder_days before due)
// for unpaid invoices and overdue reminders (billing.overdue_reminder_days
// after due) for overdue invoices, deduplicated against email_log per
// invoice/template/day. Returns the number of reminders sent.
func (s *Service) SendReminders(ctx context.Context) (int, error) {
	reminderDays := []int{7, 3, 1}
	if err := s.d.Settings.GetJSON(ctx, settingReminderDays, &reminderDays); err != nil {
		return 0, apperr.Internal(err)
	}
	overdueDays := []int{1, 3, 7}
	if err := s.d.Settings.GetJSON(ctx, settingOverdueReminderDays, &overdueDays); err != nil {
		return 0, apperr.Internal(err)
	}
	today := dateOnly(s.d.Clock.Now())

	maxLead := 0
	for _, d := range reminderDays {
		if d > maxLead {
			maxLead = d
		}
	}

	count := 0
	var errs []error

	unpaid, err := s.d.Invoices.ListDueForStatus(ctx, domain.InvoiceUnpaid, today.AddDate(0, 0, maxLead))
	if err != nil {
		return 0, apperr.Internal(err)
	}
	for _, inv := range unpaid {
		daysUntil := daysBetween(today, inv.DueDate)
		if daysUntil < 0 || !containsInt(reminderDays, daysUntil) {
			continue
		}
		sent, err := s.sendReminderOnce(ctx, inv, tplInvoiceReminder, today)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if sent {
			count++
		}
	}

	overdue, err := s.d.Invoices.ListDueForStatus(ctx, domain.InvoiceOverdue, today)
	if err != nil {
		return count, errors.Join(append(errs, apperr.Internal(err))...)
	}
	for _, inv := range overdue {
		daysOver := daysBetween(inv.DueDate, today)
		if daysOver <= 0 || !containsInt(overdueDays, daysOver) {
			continue
		}
		sent, err := s.sendReminderOnce(ctx, inv, tplInvoiceOverdue, today)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if sent {
			count++
		}
	}
	return count, errors.Join(errs...)
}

// sendReminderOnce sends the template unless an identical reminder (same
// template, same invoice number) was already logged today.
func (s *Service) sendReminderOnce(ctx context.Context, inv domain.Invoice, templateKey string, today time.Time) (bool, error) {
	already, err := s.d.Invoices.HasEmailSince(ctx, templateKey, inv.InvoiceNumber, today)
	if err != nil {
		return false, err
	}
	if already {
		return false, nil
	}
	client, err := s.d.Clients.GetByID(ctx, inv.ClientID)
	if err != nil {
		return false, err
	}
	err = s.d.Notifier.SendTemplate(ctx, client.UserID, templateKey, map[string]any{
		"Name":          client.FullName(),
		"InvoiceNumber": inv.InvoiceNumber,
		"Total":         domain.FormatIDR(inv.Total),
		"DueDate":       inv.DueDate.Format("2006-01-02"),
		"InvoiceURL":    s.invoiceURL(inv.ID),
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// ApplyLateFees adds the configured fixed late fee (billing.late_fee_amount)
// once per overdue invoice (guarded by an existing late_fee item), recalcs
// totals and notifies the client. Returns the number of fees applied.
func (s *Service) ApplyLateFees(ctx context.Context) (int, error) {
	enabled, err := s.d.Settings.GetBool(ctx, settingLateFeeEnabled, false)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	fee, err := s.d.Settings.GetInt(ctx, settingLateFeeAmount, 0)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	if !enabled || fee <= 0 {
		return 0, nil
	}

	today := dateOnly(s.d.Clock.Now())
	overdue, err := s.d.Invoices.ListDueForStatus(ctx, domain.InvoiceOverdue, today)
	if err != nil {
		return 0, apperr.Internal(err)
	}

	count := 0
	var errs []error
	for _, inv := range overdue {
		items, err := s.d.Invoices.GetItems(ctx, inv.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		hasFee := false
		for _, it := range items {
			if it.RelatedType == domain.RelatedLateFee {
				hasFee = true
				break
			}
		}
		if hasFee {
			continue
		}
		invoiceID := inv.ID
		applied := false
		err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
			locked, err := s.d.Invoices.GetByIDForUpdate(txCtx, invoiceID)
			if err != nil {
				return err
			}
			if locked.Status != domain.InvoiceOverdue {
				return nil // paid/cancelled meanwhile
			}
			if err := s.d.Invoices.AddItem(txCtx, &domain.InvoiceItem{
				InvoiceID:   locked.ID,
				Description: "Late fee",
				Amount:      int64(fee),
				Taxed:       false,
				RelatedType: domain.RelatedLateFee,
			}); err != nil {
				return err
			}
			locked.Subtotal += int64(fee)
			locked.Total += int64(fee)
			if err := s.d.Invoices.Update(txCtx, locked); err != nil {
				return err
			}
			applied = true
			return nil
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !applied {
			continue // status changed under lock: no fee, no notification
		}
		s.notifyClient(ctx, inv.ClientID, tplInvoiceOverdue, map[string]any{
			"InvoiceNumber": inv.InvoiceNumber,
			"Total":         domain.FormatIDR(inv.Total + int64(fee)),
			"DueDate":       inv.DueDate.Format("2006-01-02"),
			"InvoiceURL":    s.invoiceURL(inv.ID),
		})
		count++
	}
	return count, errors.Join(errs...)
}

// Invoice PDF (invoice:generate_pdf job)

// GenerateInvoicePDF renders the invoice PDF, stores it at
// invoices/<number>.pdf and saves the object key. Unpaid invoices are titled
// PROFORMA when billing.proforma_enabled (numbering unchanged).
func (s *Service) GenerateInvoicePDF(ctx context.Context, invoiceID int64) error {
	inv, err := s.d.Invoices.GetByID(ctx, invoiceID)
	if err != nil {
		return err
	}
	items, err := s.d.Invoices.GetItems(ctx, invoiceID)
	if err != nil {
		return err
	}
	client, err := s.d.Clients.GetByID(ctx, inv.ClientID)
	if err != nil {
		return err
	}
	user, err := s.d.Users.GetByID(ctx, client.UserID)
	if err != nil {
		return err
	}

	companyName, _ := s.d.Settings.GetString(ctx, settingCompanyName, "WHCMS")
	companyAddr, _ := s.d.Settings.GetString(ctx, settingCompanyAddress, "")
	companyEmail, _ := s.d.Settings.GetString(ctx, settingCompanyEmail, "")

	displayNumber := inv.InvoiceNumber
	if inv.Status == domain.InvoiceUnpaid {
		proforma, err := s.d.Settings.GetBool(ctx, settingProformaEnabled, false)
		if err != nil {
			return apperr.Internal(err)
		}
		if proforma {
			// ports.InvoicePDFData has no title field; the PROFORMA flag is
			// carried on the rendered number (stored numbering unchanged).
			displayNumber = "PROFORMA " + inv.InvoiceNumber
		}
	}

	var addr []string
	for _, part := range []string{client.Address1, client.Address2, client.City,
		client.State, client.Postcode, client.Country} {
		if part != "" {
			addr = append(addr, part)
		}
	}
	pdfItems := make([]ports.InvoicePDFItem, 0, len(items))
	for _, it := range items {
		pdfItems = append(pdfItems, ports.InvoicePDFItem{Description: it.Description, Amount: it.Amount})
	}

	data := ports.InvoicePDFData{
		InvoiceNumber: displayNumber,
		Status:        inv.Status,
		IssuedAt:      inv.CreatedAt,
		DueDate:       inv.DueDate,
		PaidAt:        inv.PaidAt,
		CompanyName:   companyName,
		CompanyAddr:   companyAddr,
		CompanyEmail:  companyEmail,
		ClientName:    client.FullName(),
		ClientCompany: client.Company,
		ClientAddr:    strings.Join(addr, "\n"),
		ClientEmail:   user.Email,
		Items:         pdfItems,
		Subtotal:      inv.Subtotal,
		Discount:      inv.Discount,
		TaxRate:       inv.TaxRate,
		TaxTotal:      inv.TaxTotal,
		CreditApplied: inv.CreditApplied,
		Total:         inv.Total,
		Notes:         inv.Notes,
	}
	raw, err := s.d.PDF.InvoicePDF(ctx, data)
	if err != nil {
		return apperr.Internal(err)
	}
	key := "invoices/" + inv.InvoiceNumber + ".pdf"
	if err := s.d.Storage.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), "application/pdf"); err != nil {
		return apperr.Internal(err)
	}
	return s.d.Invoices.SetPDFObjectKey(ctx, inv.ID, key)
}

// Client queries

// ListClientInvoices lists a client's own invoices.
func (s *Service) ListClientInvoices(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Invoice, int64, error) {
	list, total, err := s.d.Invoices.ListByClient(ctx, clientID, p)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	return list, total, nil
}

// GetInvoice returns an invoice with items. clientID != 0 enforces ownership
// and yields NOT_FOUND for other clients' invoices; 0 means admin access.
func (s *Service) GetInvoice(ctx context.Context, clientID, invoiceID int64) (*InvoiceDetail, error) {
	inv, err := s.d.Invoices.GetByID(ctx, invoiceID)
	if err != nil {
		return nil, err
	}
	if clientID != 0 && inv.ClientID != clientID {
		return nil, apperr.NotFound("invoice")
	}
	items, err := s.d.Invoices.GetItems(ctx, invoiceID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	txs, err := s.d.Transactions.ListByInvoice(ctx, invoiceID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &InvoiceDetail{Invoice: *inv, Items: items, Transactions: txs}, nil
}

// DownloadPDF streams the stored invoice PDF (generating it on first
// access), returning the reader and the download filename.
func (s *Service) DownloadPDF(ctx context.Context, clientID, invoiceID int64) (io.ReadCloser, string, error) {
	detail, err := s.GetInvoice(ctx, clientID, invoiceID)
	if err != nil {
		return nil, "", err
	}
	inv := detail.Invoice
	if inv.PDFObjectKey == "" {
		if err := s.GenerateInvoicePDF(ctx, inv.ID); err != nil {
			return nil, "", err
		}
		inv.PDFObjectKey = "invoices/" + inv.InvoiceNumber + ".pdf"
	}
	rc, err := s.d.Storage.Get(ctx, inv.PDFObjectKey)
	if err != nil {
		return nil, "", apperr.Internal(err)
	}
	return rc, inv.InvoiceNumber + ".pdf", nil
}

// Admin operations

// AdminListInvoices lists all invoices (Search matches number, Status exact).
func (s *Service) AdminListInvoices(ctx context.Context, p ports.ListParams) ([]domain.Invoice, int64, error) {
	list, total, err := s.d.Invoices.List(ctx, p)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	return list, total, nil
}

// CreateManualInvoice creates a manual invoice for a client (admin).
func (s *Service) CreateManualInvoice(ctx context.Context, actorUserID int64, in ManualInvoiceInput) (*domain.Invoice, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	input := ports.CreateInvoiceInput{
		ClientID: in.ClientID,
		Discount: in.Discount,
		Notes:    in.Notes,
	}
	if in.DueDate != "" {
		due, err := parseDate(in.DueDate)
		if err != nil {
			return nil, apperr.Validation("invalid due_date",
				apperr.FieldError{Field: "due_date", Message: "must be YYYY-MM-DD"})
		}
		input.DueDate = due
	}
	for _, it := range in.Items {
		input.Items = append(input.Items, ports.CreateInvoiceItem{
			Description: it.Description,
			Amount:      it.Amount,
			Taxed:       it.Taxed,
			RelatedType: domain.RelatedManual,
		})
	}
	inv, err := s.CreateInvoice(ctx, input)
	if err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "invoice.create_manual", "invoice", inv.ID, nil, map[string]any{
		"invoice_number": inv.InvoiceNumber,
		"client_id":      inv.ClientID,
		"total":          inv.Total,
	})
	s.notifyInvoiceCreated(ctx, inv)
	return inv, nil
}

// AddManualPayment records an admin manual payment against an invoice via
// the payments module (gateway "manual"); ApplyPayment enforces status and
// amount rules idempotently.
func (s *Service) AddManualPayment(ctx context.Context, actorUserID, invoiceID int64, in ManualPaymentInput) (*domain.Invoice, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	inv, err := s.d.Invoices.GetByID(ctx, invoiceID)
	if err != nil {
		return nil, err
	}
	err = s.d.Payments.ApplyPayment(ctx, invoiceID, ports.ApplyTx{
		Gateway:    domain.GatewayManual,
		MethodCode: in.Method,
		Amount:     in.Amount,
		PaidAt:     s.d.Clock.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "invoice.manual_payment", "invoice", invoiceID,
		map[string]any{"status": inv.Status},
		map[string]any{"amount": in.Amount, "method": in.Method})
	return s.d.Invoices.GetByID(ctx, invoiceID)
}

// CancelInvoice cancels an unpaid/overdue invoice (state-machine checked).
func (s *Service) CancelInvoice(ctx context.Context, actorUserID, invoiceID int64) (*domain.Invoice, error) {
	var out *domain.Invoice
	err := s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		inv, err := s.d.Invoices.GetByIDForUpdate(txCtx, invoiceID)
		if err != nil {
			return err
		}
		next, err := domain.TransitionInvoice(inv.Status, domain.InvoiceCancelled)
		if err != nil {
			return err
		}
		if err := s.d.Invoices.UpdateStatus(txCtx, inv.ID, next, nil); err != nil {
			return err
		}
		inv.Status = next
		out = inv
		return nil
	})
	if err != nil {
		if e := apperr.From(err); e.Code != apperr.CodeInternal {
			return nil, e
		}
		return nil, apperr.Internal(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "invoice.cancel", "invoice", invoiceID,
		nil, map[string]any{"status": domain.InvoiceCancelled})
	return out, nil
}

// RefundInvoice marks a paid invoice refunded, marks its success transaction
// refunded, and optionally returns the invoice total to client credit.
// Manual mark-as-refunded only - no gateway call (scope decision).
func (s *Service) RefundInvoice(ctx context.Context, actorUserID, invoiceID int64, in RefundInput) (*domain.Invoice, error) {
	var out *domain.Invoice
	err := s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		inv, err := s.d.Invoices.GetByIDForUpdate(txCtx, invoiceID)
		if err != nil {
			return err
		}
		next, err := domain.TransitionInvoice(inv.Status, domain.InvoiceRefunded)
		if err != nil {
			return err
		}
		// A domain hunt's money has its own rules (the registration price goes
		// back, the service fee does not, and only if the hunt did not win): a
		// blanket invoice refund would hand back a domain that was registered.
		items, err := s.d.Invoices.GetItems(txCtx, inv.ID)
		if err != nil {
			return err
		}
		for i := range items {
			if items[i].RelatedType == domain.RelatedDomainSnipe {
				return apperr.Conflict("this invoice pays for a domain hunt - cancel the hunt instead of refunding the invoice")
			}
		}
		if err := s.d.Invoices.UpdateStatus(txCtx, inv.ID, next, nil); err != nil {
			return err
		}
		txs, err := s.d.Transactions.ListByInvoice(txCtx, inv.ID)
		if err != nil {
			return err
		}
		for i := range txs {
			if txs[i].Status == domain.TxSuccess {
				txs[i].Status = domain.TxRefunded
				if err := s.d.Transactions.Update(txCtx, &txs[i]); err != nil {
					return err
				}
			}
		}
		if in.ToCredit && inv.Total > 0 {
			if err := s.d.Credit.AddCredit(txCtx, inv.ClientID, inv.Total,
				"Refund "+inv.InvoiceNumber, inv.ID); err != nil {
				return err
			}
		}
		inv.Status = next
		out = inv
		return nil
	})
	if err != nil {
		if e := apperr.From(err); e.Code != apperr.CodeInternal {
			return nil, e
		}
		return nil, apperr.Internal(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "invoice.refund", "invoice", invoiceID,
		map[string]any{"status": domain.InvoicePaid},
		map[string]any{"status": domain.InvoiceRefunded, "to_credit": in.ToCredit})
	return out, nil
}

// UpdateInvoice edits the due date and/or notes of an open (draft/unpaid/
// overdue) invoice.
func (s *Service) UpdateInvoice(ctx context.Context, actorUserID, invoiceID int64, in UpdateInvoiceInput) (*domain.Invoice, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if in.DueDate == nil && in.Notes == nil {
		return nil, apperr.Validation("nothing to update")
	}
	var out *domain.Invoice
	err := s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		inv, err := s.d.Invoices.GetByIDForUpdate(txCtx, invoiceID)
		if err != nil {
			return err
		}
		switch inv.Status {
		case domain.InvoiceDraft, domain.InvoiceUnpaid, domain.InvoiceOverdue:
		default:
			return apperr.Newf(apperr.CodeConflict, "cannot edit a %s invoice", inv.Status)
		}
		if in.DueDate != nil {
			due, err := parseDate(*in.DueDate)
			if err != nil {
				return apperr.Validation("invalid due_date",
					apperr.FieldError{Field: "due_date", Message: "must be YYYY-MM-DD"})
			}
			inv.DueDate = due
		}
		if in.Notes != nil {
			inv.Notes = *in.Notes
		}
		if err := s.d.Invoices.Update(txCtx, inv); err != nil {
			return err
		}
		out = inv
		return nil
	})
	if err != nil {
		if e := apperr.From(err); e.Code != apperr.CodeInternal {
			return nil, e
		}
		return nil, apperr.Internal(err)
	}
	s.d.Audit.Log(ctx, actorUserID, "invoice.update", "invoice", invoiceID, nil, map[string]any{
		"due_date": out.DueDate.Format("2006-01-02"),
		"notes":    out.Notes,
	})
	return out, nil
}
