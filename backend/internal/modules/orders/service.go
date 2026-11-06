// Package orders implements the M-ORDERS module (FR-ORD-001..007): client
// checkout with pricing/coupon/fraud validation, order activation
// (ports.ServiceActivator, called by billing.ProcessPaid) and the admin order
// lifecycle.
//
// Route table (mounted on the /api/v1 group by the composition root):
//
//	POST /orders                    create order + invoice          [auth, client, verified email]
//	GET  /orders                    list own orders                 [auth, client]
//	GET  /orders/:id                own order detail                [auth, client]
//	GET  /admin/orders              list all orders                 [admin/staff, perm: orders]
//	GET  /admin/orders/:id          order detail                    [admin/staff, perm: orders]
//	POST /admin/orders/:id/accept   activate manually               [admin/staff, perm: orders]
//	POST /admin/orders/:id/cancel   cancel + cancel invoice + stock [admin/staff, perm: orders]
//	POST /admin/orders/:id/fraud    mark fraud + cancel invoice     [admin/staff, perm: orders]
package orders

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/platform/validate"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Settings keys consumed by this module.
const (
	settingMaxOrdersPerDay      = "fraud.max_orders_per_day"
	settingEmailDomainBlacklist = "fraud.email_domain_blacklist"
	settingInvoiceDueDays       = "billing.invoice_due_days"
	settingTaxEnabled           = "billing.tax_enabled"
	settingTaxRate              = "billing.tax_rate"
	settingTaxInclusive         = "billing.tax_inclusive"
	settingDefaultNameservers   = "domains.default_nameservers"
	settingRequireEmailVerify   = "security.require_email_verification"
)

// Defaults (CONTRACTS.md §10 / MODULES.md §1).
const (
	defaultMaxOrdersPerDay = 10
	defaultInvoiceDueDays  = 3
	defaultTaxRate         = 11.0
	defaultDomainYears     = 1
	registrarName          = "rdash"
	templateOrderActivated = "order_activated"
)

// OrderStore is ports.OrderRepo plus the module-specific queries the service
// needs (implemented by *Repo).
type OrderStore interface {
	ports.OrderRepo
	// CountByClientSince counts the client's orders created at/after since.
	CountByClientSince(ctx context.Context, clientID int64, since time.Time) (int64, error)
	// InvoiceIDForOrder returns the id of the invoice whose items reference
	// this order's items (related_type=order_item), or 0 when none exists.
	InvoiceIDForOrder(ctx context.Context, orderID int64) (int64, error)
}

// StockRestorer restores product stock on cancel/fraud (implemented by *Repo
// because ports.ProductRepo only exposes DecrementStock).
type StockRestorer interface {
	// RestoreStock increments stock_qty by one for stock-enabled products.
	RestoreStock(ctx context.Context, productID int64) error
}

// CaptchaGuard verifies a CAPTCHA token on checkout when CAPTCHA is enabled (a
// no-op returning nil when disabled). Satisfied by *service/captcha.Guard.
// Optional: a nil guard disables the check entirely.
type CaptchaGuard interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

// InvoiceStore is the narrow invoice surface needed to cancel an order's
// unpaid invoice; satisfied by ports.InvoiceRepo implementations.
type InvoiceStore interface {
	GetByID(ctx context.Context, id int64) (*domain.Invoice, error)
	UpdateStatus(ctx context.Context, id int64, status domain.InvoiceStatus, paidAt *time.Time) error
}

// Deps are the service dependencies (wired by the composition root).
type Deps struct {
	Tx                   ports.TxManager
	Orders               OrderStore
	Products             ports.ProductRepo
	Stock                StockRestorer
	Coupons              ports.CouponRepo
	Clients              ports.ClientRepo
	Users                ports.UserRepo
	Services             ports.ServiceRepo
	Domains              ports.DomainRepo
	Registrars           ports.RegistrarRepo
	TLDPricing           ports.TLDPricingRepo
	PremiumPricing       ports.PremiumDomainPricingRepo
	PremiumLengthPricing ports.PremiumLengthPricingRepo
	DomainAddons         ports.DomainAddonRepo
	Registrar            ports.RegistrarModule // rdash adapter (availability checks)
	Invoices             ports.InvoiceCreator  // billing service
	InvoiceSt            InvoiceStore          // invoice repo (cancel unpaid invoice)
	Payments             ports.PaymentApplier  // payments service (settle Rp0 invoices)
	Settings             ports.SettingsRepo
	Enqueuer             ports.Enqueuer
	Notifier             ports.NotificationSender
	Encryptor            ports.Encryptor
	Audit                ports.AuditLogger
	Clock                ports.Clock
	Captcha              CaptchaGuard        // optional CAPTCHA on checkout; nil disables it
	Validator            *validate.Validator // optional; defaulted by New
}

// Service implements the orders use-cases and ports.ServiceActivator.
type Service struct {
	d   Deps
	val *validate.Validator
}

// New builds a Service.
func New(d Deps) *Service {
	v := d.Validator
	if v == nil {
		v = validate.New()
	}
	return &Service{d: d, val: v}
}

// Compile-time cross-service port check.
var _ ports.ServiceActivator = (*Service)(nil)

// Checkout

// CreateOrder validates the checkout payload, prices every item, applies the
// coupon, runs fraud checks and creates order + order_items + invoice in one
// transaction. Fraud violations still create the order (status fraud) with a
// cancelled invoice; stock and coupon usage are untouched in that case.
func (s *Service) CreateOrder(ctx context.Context, clientID int64, ip string, in CreateOrderRequest) (*CheckoutResponse, error) {
	if err := s.val.Struct(in); err != nil {
		return nil, err
	}
	if s.d.Captcha != nil {
		if err := s.d.Captcha.Verify(ctx, in.CaptchaToken, ip); err != nil {
			return nil, err
		}
	}

	client, err := s.d.Clients.GetByID(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, apperr.NotFound("client")
	}
	user, err := s.d.Users.GetByID(ctx, client.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, apperr.NotFound("user")
	}
	requireVerify, err := s.d.Settings.GetBool(ctx, settingRequireEmailVerify, true)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if requireVerify && user.EmailVerifiedAt == nil {
		return nil, apperr.Forbidden("email verification required before checkout")
	}

	now := s.d.Clock.Now().UTC()

	plans, err := s.buildPlans(ctx, in.Items)
	if err != nil {
		return nil, err
	}
	if requiresRegistrantContact(plans) && !client.HasRegistrantAddress() {
		return nil, apperr.Validation(
			"your profile is missing details (address, city, province, postal code, phone) required to "+
				"register or transfer a domain - please complete your profile before checking out",
			apperr.FieldError{Field: "profile_address", Message: "incomplete"},
		)
	}

	var subtotal int64
	for _, p := range plans {
		subtotal += p.unit + p.setup
	}

	// Coupon.
	var coupon *domain.Coupon
	var discount int64
	if in.CouponCode != "" {
		coupon, err = s.loadCoupon(ctx, in.CouponCode, now)
		if err != nil {
			return nil, err
		}
		discount = couponDiscount(coupon, plans)
	}

	// Fraud checks.
	fraudReason, err := s.fraudReason(ctx, clientID, user.Email, now)
	if err != nil {
		return nil, err
	}
	isFraud := fraudReason != ""

	// Tax (order-level snapshot; billing recomputes identically from the same
	// settings when it builds the invoice).
	taxEnabled, err := s.d.Settings.GetBool(ctx, settingTaxEnabled, false)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	var taxTotal int64
	total := subtotal - discount
	if taxEnabled {
		rate := defaultTaxRate
		if err := s.d.Settings.GetJSON(ctx, settingTaxRate, &rate); err != nil {
			return nil, apperr.Internal(err)
		}
		inclusive, err := s.d.Settings.GetBool(ctx, settingTaxInclusive, false)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		res := domain.CalcTax(subtotal-discount, rate, inclusive)
		taxTotal = res.Tax
		total = res.Gross
	}

	dueDays, err := s.d.Settings.GetInt(ctx, settingInvoiceDueDays, defaultInvoiceDueDays)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	dueDate := now.AddDate(0, 0, dueDays)

	order := &domain.Order{
		ClientID: clientID,
		Status:   domain.OrderPending,
		Subtotal: subtotal,
		Discount: discount,
		TaxTotal: taxTotal,
		Total:    total,
		IP:       ip,
		Notes:    in.Notes,
	}
	if coupon != nil {
		id := coupon.ID
		order.CouponID = &id
	}

	items := make([]domain.OrderItem, 0, len(plans))
	for _, p := range plans {
		optsJSON, mErr := json.Marshal(p.opts)
		if mErr != nil {
			return nil, apperr.Internal(mErr)
		}
		oi := domain.OrderItem{
			ItemType:    p.itemType,
			Description: p.desc,
			Domain:      p.domainName,
			Cycle:       p.cycle,
			UnitPrice:   p.unit,
			SetupFee:    p.setup,
			Options:     optsJSON,
		}
		if p.product != nil {
			pid := p.product.ID
			oi.ProductID = &pid
		}
		items = append(items, oi)
	}

	var invoice *domain.Invoice
	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		seq, err := s.d.Orders.NextNumber(txCtx, domain.OrderCounterScope(now))
		if err != nil {
			return err
		}
		order.OrderNumber = domain.FormatOrderNumber(now, seq)

		if err := s.d.Orders.Create(txCtx, order, items); err != nil {
			return err
		}

		if !isFraud {
			// Stock decrement (CONFLICT apperr when out of stock aborts the tx).
			for i := range plans {
				p := plans[i]
				if p.product != nil && p.product.StockEnabled {
					if err := s.d.Products.DecrementStock(txCtx, p.product.ID); err != nil {
						return err
					}
				}
			}
			if coupon != nil {
				if err := s.d.Coupons.IncrementUsage(txCtx, coupon.ID); err != nil {
					return err
				}
			}
		}

		invItems := make([]ports.CreateInvoiceItem, 0, len(items))
		for i := range items {
			invItems = append(invItems, ports.CreateInvoiceItem{
				Description: items[i].Description,
				Amount:      items[i].UnitPrice + items[i].SetupFee,
				Taxed:       true,
				RelatedType: domain.RelatedOrderItem,
				RelatedID:   items[i].ID,
			})
		}
		invoice, err = s.d.Invoices.CreateInvoice(txCtx, ports.CreateInvoiceInput{
			ClientID: clientID,
			Items:    invItems,
			Discount: discount,
			DueDate:  dueDate,
			Notes:    "Order " + order.OrderNumber,
		})
		if err != nil {
			return err
		}

		if isFraud {
			newStatus, tErr := domain.TransitionOrder(order.Status, domain.OrderFraud)
			if tErr != nil {
				return tErr
			}
			if err := s.d.Orders.UpdateStatus(txCtx, order.ID, newStatus); err != nil {
				return err
			}
			order.Status = newStatus
			invStatus, tErr := domain.TransitionInvoice(invoice.Status, domain.InvoiceCancelled)
			if tErr != nil {
				return tErr
			}
			if err := s.d.InvoiceSt.UpdateStatus(txCtx, invoice.ID, invStatus, nil); err != nil {
				return err
			}
			invoice.Status = invStatus
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.d.Audit.Log(ctx, client.UserID, "order.create", "order", order.ID, nil,
		map[string]any{"order_number": order.OrderNumber, "total": order.Total, "invoice_id": invoice.ID})

	switch {
	case isFraud:
		s.d.Audit.Log(ctx, client.UserID, "order.fraud_auto", "order", order.ID,
			nil, map[string]any{"status": string(domain.OrderFraud), "reason": fraudReason})
	case invoice.Total <= 0:
		// Nothing to collect (fully-discounted or Rp0-priced items, e.g. a
		// promo domain): settle through the same idempotent entrypoint a
		// manual "Pay Now with credit" click would use, so the order
		// activates without asking the client to pay for nothing. Checked
		// only in the non-fraud branch, and only after the transaction above
		// (which may itself cancel the invoice for fraud) has committed - a
		// fraud-flagged order must never auto-activate. Best-effort: on
		// failure the invoice just stays payable and the client's own credit
		// "Pay Now" click is the fallback, exactly as for any other Rp0
		// invoice today.
		if err := s.d.Payments.ApplyPayment(ctx, invoice.ID, ports.ApplyTx{
			Gateway: domain.GatewayCredit, MethodCode: "credit", PaidAt: now,
		}); err != nil {
			s.d.Audit.Log(ctx, client.UserID, "order.autopay_zero_failed", "order", order.ID,
				nil, map[string]any{"invoice_id": invoice.ID, "error": err.Error()})
		} else {
			invoice.Status = domain.InvoicePaid
			paidAt := now
			invoice.PaidAt = &paidAt
		}
	case allAutoSetupOnOrder(plans):
		// Every item is configured for AutoSetup == on_order: deliver right
		// away instead of waiting for the invoice to be paid (free/trial or
		// invoice-later products). Best-effort like the Rp0 branch above -
		// on failure the order just stays pending and the existing
		// fallbacks (payment success, or an admin's manual Accept) still
		// activate it later.
		if err := s.ActivateOrder(ctx, order.ID); err != nil {
			s.d.Audit.Log(ctx, client.UserID, "order.autoactivate_on_order_failed", "order", order.ID,
				nil, map[string]any{"error": err.Error()})
		} else {
			order.Status = domain.OrderActive
		}
	}

	return &CheckoutResponse{Order: *order, Items: items, Invoice: invoice}, nil
}

// loadCoupon fetches and validates a coupon code for use at time now.
func (s *Service) loadCoupon(ctx context.Context, code string, now time.Time) (*domain.Coupon, error) {
	invalid := func(msg string) error {
		return apperr.Validation("invalid coupon", apperr.FieldError{Field: "coupon_code", Message: msg})
	}
	c, err := s.d.Coupons.GetByCode(ctx, code)
	if err != nil {
		if apperr.From(err).Code == apperr.CodeNotFound {
			return nil, invalid("unknown coupon code")
		}
		return nil, err
	}
	if c == nil {
		return nil, invalid("unknown coupon code")
	}
	if !c.Active {
		return nil, invalid("coupon is not active")
	}
	if c.ExpiresAt != nil && !now.Before(*c.ExpiresAt) {
		return nil, invalid("coupon has expired")
	}
	if c.MaxUses > 0 && c.UsedCount >= c.MaxUses {
		return nil, invalid("coupon usage limit reached")
	}
	return c, nil
}

// fraudReason evaluates the fraud settings; empty string means clean.
func (s *Service) fraudReason(ctx context.Context, clientID int64, email string, now time.Time) (string, error) {
	var blacklist []string
	if err := s.d.Settings.GetJSON(ctx, settingEmailDomainBlacklist, &blacklist); err != nil {
		return "", apperr.Internal(err)
	}
	if dom := emailDomain(email); dom != "" {
		for _, b := range blacklist {
			if strings.EqualFold(strings.TrimSpace(b), dom) {
				return "email domain blacklisted: " + dom, nil
			}
		}
	}

	maxPerDay, err := s.d.Settings.GetInt(ctx, settingMaxOrdersPerDay, defaultMaxOrdersPerDay)
	if err != nil {
		return "", apperr.Internal(err)
	}
	if maxPerDay > 0 {
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		count, err := s.d.Orders.CountByClientSince(ctx, clientID, dayStart)
		if err != nil {
			return "", err
		}
		if count >= int64(maxPerDay) {
			return fmt.Sprintf("max orders per day exceeded (%d)", maxPerDay), nil
		}
	}
	return "", nil
}

func emailDomain(email string) string {
	i := strings.LastIndexByte(email, '@')
	if i < 0 || i == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[i+1:])
}

// Reads

// ListByClient lists the client's own orders.
func (s *Service) ListByClient(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Order, int64, error) {
	return s.d.Orders.ListByClient(ctx, clientID, p)
}

// List lists all orders (admin).
func (s *Service) List(ctx context.Context, p ports.ListParams) ([]domain.Order, int64, error) {
	return s.d.Orders.List(ctx, p)
}

// Get returns one order with items. clientID != 0 scopes the lookup to that
// client; other clients' orders return NOT_FOUND (never 403).
func (s *Service) Get(ctx context.Context, clientID, orderID int64) (*OrderDetail, error) {
	order, err := s.getOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if clientID != 0 && order.ClientID != clientID {
		return nil, apperr.NotFound("order")
	}
	items, err := s.d.Orders.GetItems(ctx, orderID)
	if err != nil {
		return nil, err
	}
	invoiceID, err := s.d.Orders.InvoiceIDForOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	return &OrderDetail{Order: *order, Items: items, InvoiceID: invoiceID}, nil
}

func (s *Service) getOrder(ctx context.Context, orderID int64) (*domain.Order, error) {
	order, err := s.d.Orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, apperr.NotFound("order")
	}
	return order, nil
}

// Admin lifecycle

// Accept manually activates a pending order (admin action).
func (s *Service) Accept(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error) {
	order, err := s.getOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if err := s.ActivateOrder(ctx, orderID); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "order.accept", "order", orderID,
		map[string]any{"status": string(order.Status)},
		map[string]any{"status": string(domain.OrderActive)})
	order.Status = domain.OrderActive
	return order, nil
}

// Cancel cancels a pending order: order -> cancelled, its unpaid invoice ->
// cancelled, and stock restored.
func (s *Service) Cancel(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error) {
	return s.setTerminalStatus(ctx, actorUserID, orderID, domain.OrderCancelled, "order.cancel")
}

// MarkFraud flags a pending order as fraud: same effects as cancel plus the
// fraud status, audit-logged.
func (s *Service) MarkFraud(ctx context.Context, actorUserID, orderID int64) (*domain.Order, error) {
	return s.setTerminalStatus(ctx, actorUserID, orderID, domain.OrderFraud, "order.fraud")
}

func (s *Service) setTerminalStatus(ctx context.Context, actorUserID, orderID int64, to domain.OrderStatus, action string) (*domain.Order, error) {
	order, err := s.getOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	newStatus, err := domain.TransitionOrder(order.Status, to)
	if err != nil {
		return nil, err
	}
	items, err := s.d.Orders.GetItems(ctx, orderID)
	if err != nil {
		return nil, err
	}

	err = s.d.Tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := s.d.Orders.UpdateStatus(txCtx, orderID, newStatus); err != nil {
			return err
		}
		// Cancel the order's invoice while it is still cancellable.
		invoiceID, err := s.d.Orders.InvoiceIDForOrder(txCtx, orderID)
		if err != nil {
			return err
		}
		if invoiceID != 0 {
			inv, err := s.d.InvoiceSt.GetByID(txCtx, invoiceID)
			if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
				return err
			}
			if inv != nil && domain.InvoiceCanTransition(inv.Status, domain.InvoiceCancelled) {
				if err := s.d.InvoiceSt.UpdateStatus(txCtx, invoiceID, domain.InvoiceCancelled, nil); err != nil {
					return err
				}
			}
		}
		// Restore stock for product items (RestoreStock no-ops when the
		// product is not stock-enabled).
		for i := range items {
			if items[i].ItemType == domain.ItemProduct && items[i].ProductID != nil {
				if err := s.d.Stock.RestoreStock(txCtx, *items[i].ProductID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.d.Audit.Log(ctx, actorUserID, action, "order", orderID,
		map[string]any{"status": string(order.Status)},
		map[string]any{"status": string(newStatus)})
	order.Status = newStatus
	return order, nil
}
