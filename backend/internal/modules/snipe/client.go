package snipe

import (
	"context"
	"fmt"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Settings read by the client-facing flows (shared with orders/billing).
const (
	settingTaxEnabled         = "billing.tax_enabled"
	settingTaxRate            = "billing.tax_rate"
	settingTaxInclusive       = "billing.tax_inclusive"
	settingRequireEmailVerify = "security.require_email_verification"
	defaultTaxRate            = 11

	// maxClientBurstWindow caps the intensive-attempt window a client may ask
	// for. Inside it a hunt fires a registration request every few hundred
	// milliseconds, so an open-ended or day-long window would turn one paid
	// plan into a standing flood against the shared reseller API key. Outside
	// the window the hunt only watches availability.
	maxClientBurstWindow = 6 * time.Hour

	// staleSweepEvery spaces the pending_payment clean-up; staleSweepBatch
	// bounds one pass.
	staleSweepEvery = time.Minute
	staleSweepBatch = 20
)

// clientEventKinds are the trail entries an owner may see. The free text of an
// event stays operator-only: it can carry registrar internals, and phase/error
// chatter is noise to a customer.
var clientEventKinds = map[string]bool{
	eventCreated:                       true,
	eventPaid:                          true,
	eventAvailable:                     true,
	eventWon:                           true,
	string(domain.SnipeExpired):        true,
	string(domain.SnipeCancelled):      true,
	string(domain.SnipeFailed):         true,
	string(domain.SnipePendingPayment): true,
}

// priced is a fully resolved hunt price: which plan, what the registration
// costs, the invoice math, and how the total splits into the part credited
// back on a loss and the part that is not.
type priced struct {
	name         string
	plan         *domain.SnipePlan
	years        int
	quote        *ports.DomainQuote
	fee          int64 // service fee, never refunded
	domainPart   int64 // registration price + plan success fee, refundable
	subtotal     int64
	tax          int64
	taxRate      float64
	taxInclusive bool
	total        int64
}

// refundSplit divides an invoice total pro-rata between the non-refundable
// service fee and everything else. The fee's share is rounded down, so any
// remainder goes to the refundable side and refundableFee+refundableDomain
// always equals the total.
func refundSplit(total, fee, subtotal int64) (refundableDomain, refundableFee int64) {
	if subtotal <= 0 || fee <= 0 {
		return total, 0
	}
	refundableFee = total * fee / subtotal
	return total - refundableFee, refundableFee
}

// price resolves the hunt cost for a domain on a plan. It validates the name,
// the plan (must exist and be active) and that the TLD has an admin price.
func (s *Service) price(ctx context.Context, rawName string, planID int64, years int) (*priced, error) {
	name := domains.NormalizeDomainName(rawName)
	if err := domains.ValidateDomainName(name); err != nil {
		return nil, err
	}
	if years == 0 {
		years = 1
	}
	plan, err := s.d.Plans.GetByID(ctx, planID)
	if err != nil {
		if apperr.From(err).Code == apperr.CodeNotFound {
			return nil, apperr.Validation("that plan does not exist", apperr.FieldError{Field: "plan_id", Message: "unknown plan"})
		}
		return nil, err
	}
	if plan == nil || !plan.Active {
		return nil, apperr.Validation("that plan is not available", apperr.FieldError{Field: "plan_id", Message: "inactive plan"})
	}
	quote, err := s.d.Domains.QuoteRegistration(ctx, name, years)
	if err != nil {
		return nil, err
	}

	p := &priced{
		name: name, plan: plan, years: years, quote: quote,
		fee: plan.Fee, domainPart: quote.Total + plan.SuccessFee,
	}
	p.subtotal = p.fee + p.domainPart
	p.total = p.subtotal

	// Same tax rules billing.CreateInvoice applies (every line is taxed).
	taxEnabled, err := s.d.Settings.GetBool(ctx, settingTaxEnabled, false)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if taxEnabled {
		rate, err := s.d.Settings.GetInt(ctx, settingTaxRate, defaultTaxRate)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		inclusive, err := s.d.Settings.GetBool(ctx, settingTaxInclusive, false)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		if rate > 0 {
			res := domain.CalcTax(p.subtotal, float64(rate), inclusive)
			p.tax, p.taxRate, p.taxInclusive = res.Tax, float64(rate), inclusive
			if !inclusive {
				p.total += res.Tax
			}
		}
	}
	return p, nil
}

func (p *priced) toQuote() *Quote {
	refundable, _ := refundSplit(p.total, p.fee, p.subtotal)
	return &Quote{
		Domain: p.name, Years: p.years, PlanID: p.plan.ID, PlanName: p.plan.Name, WindowDays: p.plan.WindowDays,
		Fee: p.fee, DomainPrice: p.quote.Total, SuccessFee: p.plan.SuccessFee, Subtotal: p.subtotal,
		TaxRate: p.taxRate, TaxInclusive: p.taxInclusive, Tax: p.tax, Total: p.total,
		RefundableIfLost: refundable, RenewPerYear: p.quote.RenewPerYear,
	}
}

// Quote prices a hunt without creating anything.
func (s *Service) Quote(ctx context.Context, in QuoteRequest) (*Quote, error) {
	p, err := s.price(ctx, in.Domain, in.PlanID, in.Years)
	if err != nil {
		return nil, err
	}
	return p.toQuote(), nil
}

// ListActivePlans returns the plans a client can buy.
func (s *Service) ListActivePlans(ctx context.Context) ([]PlanView, error) {
	plans, err := s.d.Plans.List(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]PlanView, 0, len(plans))
	for _, p := range plans {
		out = append(out, PlanView{ID: p.ID, Name: p.Name, WindowDays: p.WindowDays, Fee: p.Fee, SuccessFee: p.SuccessFee})
	}
	return out, nil
}

// Create starts a paid hunt: it validates everything that could make a hunt
// pointless, then in ONE transaction inserts the snipe (pending_payment) and
// its up-front invoice (service fee + the registration price, the latter
// carrying the activation hook). The client pays that invoice through the
// normal billing flow; ActivateSnipeAfterPayment then starts the hunt.
func (s *Service) Create(ctx context.Context, who ClientIdentity, in CreateRequest) (*ClientSnipe, error) {
	cfg, err := loadConfig(ctx, s.d.Settings)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if !cfg.Enabled {
		return nil, apperr.Forbidden("the domain sniper is not available right now")
	}
	if cfg.PauseReason != "" {
		// Hunts are standing still while the reseller account is unusable; a
		// hunt sold now would burn its window waiting.
		return nil, apperr.Forbidden("the domain sniper is paused right now - please try again later")
	}

	client, err := s.d.Clients.GetByID(ctx, who.ClientID)
	if err != nil {
		return nil, err
	}
	user, err := s.d.Users.GetByID(ctx, client.UserID)
	if err != nil {
		return nil, err
	}
	requireVerify, err := s.d.Settings.GetBool(ctx, settingRequireEmailVerify, true)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if requireVerify && user.EmailVerifiedAt == nil {
		return nil, apperr.Forbidden("email verification required before starting a snipe")
	}

	p, err := s.price(ctx, in.Domain, in.PlanID, in.Years)
	if err != nil {
		return nil, err
	}
	var ns []string
	if len(in.Nameservers) > 0 {
		if ns, err = domains.ValidateNameservers(in.Nameservers); err != nil {
			return nil, err
		}
	}
	if !client.HasRegistrantAddress() {
		return nil, apperr.Validation(
			"your profile is missing details (address, city, province, postal code, phone) required to "+
				"register a domain - please complete your profile before starting a snipe",
			apperr.FieldError{Field: "profile_address", Message: "incomplete"})
	}

	now := s.d.Clock.Now()
	deadline := now.AddDate(0, 0, p.plan.WindowDays)
	if in.HuntUntil != nil && !in.HuntUntil.After(now) {
		return nil, apperr.Validation("hunt_until must be in the future")
	}
	if err := validateWindow(in.HuntFrom, in.HuntUntil, deadline); err != nil {
		return nil, err
	}
	if err := validateClientWindow(in.HuntFrom, in.HuntUntil); err != nil {
		return nil, err
	}

	open, err := s.d.Snipes.CountOpenByClient(ctx, who.ClientID)
	if err != nil {
		return nil, err
	}
	if open >= cfg.MaxActivePerClient {
		return nil, apperr.Conflict(fmt.Sprintf("you already have %d open snipes; cancel or finish one first", open))
	}
	// Every paid hunt must actually be hunted (see ensureCapacity). The count
	// and the insert below are not one atomic step, so a burst of simultaneous
	// purchases can overshoot the cap by a few; the supervisor then claims the
	// oldest first and the rest wait for a free slot.
	if err := s.ensureCapacity(ctx, cfg); err != nil {
		return nil, err
	}
	if err := s.ensureNotRegistered(ctx, p.name); err != nil {
		return nil, err
	}

	// A name that is registrable right now needs no hunt: send the client to
	// the normal order flow instead of selling them a snipe.
	free, err := s.d.Registrar.IsAvailable(ctx, p.name)
	if err != nil {
		return nil, err
	}
	if free {
		return nil, apperr.Validation(
			"this domain is available right now - register it normally instead of sniping it",
			apperr.FieldError{Field: "domain", Message: "available"})
	}

	var sn *domain.DomainSnipe
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		planID := p.plan.ID
		sn = &domain.DomainSnipe{
			ClientID: who.ClientID, PlanID: &planID, Source: domain.SnipeSourceClient,
			DomainName: p.name, Years: p.years, Status: domain.SnipePendingPayment, Phase: domain.SnipePhasePatient,
			HuntFrom: in.HuntFrom, HuntUntil: in.HuntUntil, Nameservers: ns,
			FeeAmount: p.fee, DomainAmount: p.domainPart, RenewPerYear: p.quote.RenewPerYear,
			DeadlineAt: &deadline, // provisional: Activate restarts it from the payment
		}
		if err := s.d.Snipes.Create(ctx, sn); err != nil {
			return err
		}
		inv, err := s.d.Invoices.CreateInvoice(ctx, ports.CreateInvoiceInput{
			ClientID: who.ClientID,
			Items:    s.invoiceItems(sn.ID, p),
			DueDate:  now.Add(cfg.InvoiceDue),
			Notes:    "Domain snipe: " + p.name,
		})
		if err != nil {
			return err
		}
		refundableDomain, refundableFee := refundSplit(inv.Total, p.fee, inv.Subtotal)
		if err := s.d.Snipes.SetInvoice(ctx, sn.ID, inv.ID, refundableDomain, refundableFee); err != nil {
			return err
		}
		sn.InvoiceID, sn.RefundableDomain, sn.RefundableFee = &inv.ID, refundableDomain, refundableFee
		return nil
	})
	if err != nil {
		if apperr.From(err).Code == apperr.CodeConflict {
			// The unique index fired: somebody (maybe this client) already has an
			// open hunt for the name. Who else is hunting a name is not for a
			// client to learn, so the text stays vague.
			return nil, apperr.Conflict(p.name + " cannot be hunted right now - it may already be in your hunts list")
		}
		return nil, err
	}

	s.event(ctx, sn.ID, eventCreated, "snipe created, waiting for payment")
	s.d.Audit.Log(ctx, who.UserID, "snipe.create", "domain_snipe", sn.ID, nil, sn)
	return toClientSnipe(sn, p.plan.Name), nil
}

// validateClientWindow adds the limits that only apply to customers (an admin
// may hunt with an open window on purpose): a burst window needs both ends and
// may not exceed maxClientBurstWindow.
func validateClientWindow(from, until *time.Time) error {
	if from == nil {
		return nil
	}
	if until == nil {
		return apperr.Validation("hunt_until is required when hunt_from is set",
			apperr.FieldError{Field: "hunt_until", Message: "required"})
	}
	if until.Sub(*from) > maxClientBurstWindow {
		return apperr.Validation(
			fmt.Sprintf("the drop window may span at most %d hours", int(maxClientBurstWindow/time.Hour)),
			apperr.FieldError{Field: "hunt_until", Message: "too long"})
	}
	return nil
}

// invoiceItems builds the invoice lines of a client hunt. Only the domain
// registration line carries the activation hook (related_type=domain_snipe):
// it is always above zero, whereas a free plan has no fee line at all.
func (s *Service) invoiceItems(snipeID int64, p *priced) []ports.CreateInvoiceItem {
	var items []ports.CreateInvoiceItem
	if p.fee > 0 {
		items = append(items, ports.CreateInvoiceItem{
			Description: fmt.Sprintf("Domain snipe service fee - %s (%s)", p.name, p.plan.Name),
			Amount:      p.fee, Taxed: true, RelatedType: domain.RelatedManual,
		})
	}
	items = append(items, ports.CreateInvoiceItem{
		Description: fmt.Sprintf("Domain registration - %s, %d yr (credited back if the domain is not acquired)", p.name, p.years),
		Amount:      p.quote.Total, Taxed: true, RelatedType: domain.RelatedDomainSnipe, RelatedID: snipeID,
	})
	if p.plan.SuccessFee > 0 {
		items = append(items, ports.CreateInvoiceItem{
			Description: fmt.Sprintf("Success fee - %s (credited back if the domain is not acquired)", p.name),
			Amount:      p.plan.SuccessFee, Taxed: true, RelatedType: domain.RelatedManual,
		})
	}
	return items
}

// ActivateSnipeAfterPayment (ports.SnipeActivator) starts the hunt once its
// invoice is paid. It runs inside the payment transaction, so it only touches
// the database and must be idempotent: a replay finds the snipe already active
// and does nothing. A payment that arrives for a hunt that already ended (the
// snipe was cancelled while the client was still on the payment page) is
// credited straight back instead.
func (s *Service) ActivateSnipeAfterPayment(ctx context.Context, snipeID int64) error {
	now := s.d.Clock.Now()
	activated, err := s.d.Snipes.Activate(ctx, snipeID, now)
	if err != nil {
		return err
	}
	sn, err := s.d.Snipes.GetByID(ctx, snipeID)
	if err != nil {
		return err
	}
	if activated {
		s.event(ctx, snipeID, eventPaid, "payment received, the hunt is active")
		s.d.Audit.Log(ctx, 0, "snipe.activate", "domain_snipe", snipeID, nil, sn)
		data := map[string]any{"Domain": sn.DomainName}
		if sn.DeadlineAt != nil {
			data["DeadlineAt"] = sn.DeadlineAt.Format(dateFormat)
		}
		s.sendToClient(ctx, sn, tplActive, data)
		return nil
	}
	switch sn.Status {
	case domain.SnipeCancelled, domain.SnipeFailed, domain.SnipeExpired:
		return s.refundLatePayment(ctx, sn, now)
	}
	return nil
}

// refundLatePayment credits back everything paid for a hunt that had already
// ended. RecordRefund only applies while nothing has been refunded, so a
// replayed payment cannot pay the client twice.
func (s *Service) refundLatePayment(ctx context.Context, sn *domain.DomainSnipe, now time.Time) error {
	amount := sn.RefundableDomain + sn.RefundableFee
	if amount <= 0 || sn.InvoiceID == nil {
		return nil
	}
	recorded, err := s.d.Snipes.RecordRefund(ctx, sn.ID, amount, now)
	if err != nil || !recorded {
		return err
	}
	if _, err := s.d.Clients.AdjustCredit(ctx, sn.ClientID, amount,
		"Domain snipe refund (payment arrived after the hunt ended): "+sn.DomainName, sn.InvoiceID); err != nil {
		return err
	}
	s.event(ctx, sn.ID, "late_payment_refund", "payment arrived after the hunt ended; credited back "+domain.FormatIDR(amount))
	s.d.Audit.Log(ctx, 0, "snipe.late_payment_refund", "domain_snipe", sn.ID, nil, map[string]any{"amount": amount})
	s.notifyEnded(ctx, sn, domain.SnipeCancelled, amount)
	return nil
}

// ListForClient is the owner's list, newest first.
func (s *Service) ListForClient(ctx context.Context, clientID int64, p ports.ListParams) ([]ClientSnipe, int64, error) {
	rows, total, err := s.d.Snipes.ListByClient(ctx, clientID, p)
	if err != nil {
		return nil, 0, err
	}
	names := s.planNames(ctx)
	out := make([]ClientSnipe, 0, len(rows))
	for i := range rows {
		out = append(out, *toClientSnipe(&rows[i], names[planKey(rows[i].PlanID)]))
	}
	return out, total, nil
}

// GetForClient returns one of the client's snipes with its visible trail. A
// snipe of another client is NOT_FOUND, never FORBIDDEN (no existence oracle).
func (s *Service) GetForClient(ctx context.Context, clientID, id int64) (*ClientDetail, error) {
	sn, err := s.ownedSnipe(ctx, clientID, id)
	if err != nil {
		return nil, err
	}
	events, err := s.d.Snipes.ListEvents(ctx, id, detailEventLimit)
	if err != nil {
		return nil, err
	}
	visible := make([]ClientEvent, 0, len(events))
	for _, e := range events {
		if clientEventKinds[e.Kind] {
			visible = append(visible, ClientEvent{Kind: e.Kind, CreatedAt: e.CreatedAt})
		}
	}
	return &ClientDetail{Snipe: *toClientSnipe(sn, s.planNames(ctx)[planKey(sn.PlanID)]), Events: visible}, nil
}

// CancelForClient lets the owner stop a hunt. An active one is closed and gets
// its domain price credited back (the service fee is kept). An unpaid one is
// closed first and its invoice voided afterwards: if a payment lands in
// between, ActivateSnipeAfterPayment finds the snipe already ended and credits
// the whole payment back, so no ordering of the two can strand money.
func (s *Service) CancelForClient(ctx context.Context, who ClientIdentity, id int64) (*ClientSnipe, error) {
	sn, err := s.ownedSnipe(ctx, who.ClientID, id)
	if err != nil {
		return nil, err
	}
	if domain.SnipeTerminal(sn.Status) {
		return nil, apperr.Conflict("this snipe has already ended")
	}
	if err := s.ensureNotOwned(ctx, sn); err != nil {
		return nil, err
	}
	ok, err := s.finish(ctx, sn, domain.SnipeCancelled, finishOpts{Reason: "cancelled by the client", ActorUserID: who.UserID})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apperr.Conflict("the snipe changed state; reload and try again")
	}
	if sn.Status == domain.SnipePendingPayment {
		s.voidInvoice(ctx, sn, who.UserID)
	}
	cur, err := s.d.Snipes.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return toClientSnipe(cur, s.planNames(ctx)[planKey(cur.PlanID)]), nil
}

// ownedSnipe loads a snipe and checks it belongs to the client.
func (s *Service) ownedSnipe(ctx context.Context, clientID, id int64) (*domain.DomainSnipe, error) {
	sn, err := s.d.Snipes.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sn.ClientID != clientID {
		return nil, apperr.NotFound("snipe")
	}
	return sn, nil
}

func planKey(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// planNames maps plan id -> name for display; a lookup failure just leaves
// the names blank (the list is still correct).
func (s *Service) planNames(ctx context.Context) map[int64]string {
	plans, err := s.d.Plans.List(ctx, false)
	if err != nil {
		s.d.Log.WarnContext(ctx, "snipe plan names unavailable", "error", err)
		return nil
	}
	out := make(map[int64]string, len(plans))
	for _, p := range plans {
		out[p.ID] = p.Name
	}
	return out
}

func toClientSnipe(sn *domain.DomainSnipe, planName string) *ClientSnipe {
	c := &ClientSnipe{
		ID: sn.ID, DomainName: sn.DomainName, Years: sn.Years, Status: sn.Status,
		HuntFrom: sn.HuntFrom, HuntUntil: sn.HuntUntil, Nameservers: sn.Nameservers,
		InvoiceID: sn.InvoiceID, PlanID: sn.PlanID, PlanName: planName,
		FeeAmount: sn.FeeAmount, DomainAmount: sn.DomainAmount,
		RefundableDomain: sn.RefundableDomain, RefundableFee: sn.RefundableFee,
		RefundAmount: sn.RefundAmount, RefundedAt: sn.RefundedAt, Attempts: sn.Attempts,
		PaidAt: sn.PaidAt, DeadlineAt: sn.DeadlineAt, WonAt: sn.WonAt, WonDomainID: sn.WonDomainID,
		CreatedAt: sn.CreatedAt,
	}
	if c.Nameservers == nil {
		c.Nameservers = []string{}
	}
	if sn.Status == domain.SnipeActive {
		c.Phase = sn.Phase
	}
	return c
}

// Admin: plans

// ListPlans returns every plan, including deactivated ones.
func (s *Service) ListPlans(ctx context.Context) ([]domain.SnipePlan, error) {
	plans, err := s.d.Plans.List(ctx, false)
	if err != nil {
		return nil, err
	}
	if plans == nil {
		plans = []domain.SnipePlan{}
	}
	return plans, nil
}

// CreatePlan adds a plan (active unless the request says otherwise).
func (s *Service) CreatePlan(ctx context.Context, actorUserID int64, in PlanRequest) (*domain.SnipePlan, error) {
	p := &domain.SnipePlan{
		Name: in.Name, WindowDays: in.WindowDays, Fee: in.Fee, SuccessFee: in.SuccessFee,
		Active: in.Active == nil || *in.Active, Sort: in.Sort,
	}
	if err := s.d.Plans.Create(ctx, p); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "snipe_plan.create", "snipe_plan", p.ID, nil, p)
	return p, nil
}

// UpdatePlan replaces a plan's fields. Existing snipes keep the amounts they
// were sold at (they are frozen on the row), so repricing is always safe.
func (s *Service) UpdatePlan(ctx context.Context, actorUserID, id int64, in PlanRequest) (*domain.SnipePlan, error) {
	p, err := s.d.Plans.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	before := *p
	p.Name, p.WindowDays, p.Fee, p.SuccessFee, p.Sort = in.Name, in.WindowDays, in.Fee, in.SuccessFee, in.Sort
	if in.Active != nil {
		p.Active = *in.Active
	}
	if err := s.d.Plans.Update(ctx, p); err != nil {
		return nil, err
	}
	s.d.Audit.Log(ctx, actorUserID, "snipe_plan.update", "snipe_plan", p.ID, &before, p)
	return p, nil
}
