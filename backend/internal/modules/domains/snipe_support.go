package domains

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// maxQuoteYears is the registrar's longest registration term.
const maxQuoteYears = 10

var _ ports.SnipeDomainService = (*Service)(nil)

// QuoteRegistration prices years of registration for name from admin-
// configured pricing only, in the same precedence orders.planDomain uses (exact
// premium-domain override, then premium length tier, then the active TLD
// matrix). Unlike checkout it never falls back to the registrar's live cost: a
// TLD nobody priced cannot be sold, so it is a VALIDATION error rather than a
// silent zero or a guess. A real lookup failure is returned, never swallowed -
// this number becomes an invoice.
func (s *Service) QuoteRegistration(ctx context.Context, name string, years int) (*ports.DomainQuote, error) {
	name = NormalizeDomainName(name)
	if err := ValidateDomainName(name); err != nil {
		return nil, err
	}
	if years == 0 {
		years = 1
	}
	if years < 1 || years > maxQuoteYears {
		return nil, apperr.Validation(fmt.Sprintf("years must be between 1 and %d", maxQuoteYears))
	}

	premium, err := s.d.PremiumPricing.GetByName(ctx, name)
	if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
		return nil, err
	}
	if err == nil && premium != nil {
		return &ports.DomainQuote{Total: premium.RegisterPrice * int64(years), RenewPerYear: premium.RenewPrice}, nil
	}

	ext := domainExtension(name)
	tier, err := s.d.PremiumLengthPricing.GetByTLDAndLength(ctx, ext, domainLabelLength(name, ext))
	if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
		return nil, err
	}
	if err == nil && tier != nil {
		return &ports.DomainQuote{Total: tier.Price * int64(years), RenewPerYear: tier.Price}, nil
	}

	tld, err := s.d.TLDPricing.GetByTLD(ctx, ext)
	if err != nil && apperr.From(err).Code != apperr.CodeNotFound {
		return nil, err
	}
	if err != nil || tld == nil || !tld.Active {
		return nil, apperr.Validation("no pricing is configured for ." + ext)
	}
	if years < tld.MinYears || years > tld.MaxYears {
		return nil, apperr.Validation(fmt.Sprintf("years must be between %d and %d for .%s", tld.MinYears, tld.MaxYears, ext))
	}
	price, ok := tld.RegisterPrices[strconv.Itoa(years)]
	if !ok || price <= 0 {
		return nil, apperr.Validation(fmt.Sprintf("no %d-year price is configured for .%s", years, ext))
	}
	return &ports.DomainQuote{Total: price, RenewPerYear: tld.RenewPrices["1"]}, nil
}

// RegistrantContactFor builds the registrar contact for a client (profile plus
// the owning user's email); VALIDATION when the profile is missing the address
// details the registrar requires. Unlike the register job it sends no email -
// the sniper decides what to tell the client.
func (s *Service) RegistrantContactFor(ctx context.Context, clientID int64) (ports.RegistrantContact, error) {
	cl, err := s.d.Clients.GetByID(ctx, clientID)
	if err != nil {
		return ports.RegistrantContact{}, err
	}
	u, err := s.d.Users.GetByID(ctx, cl.UserID)
	if err != nil {
		return ports.RegistrantContact{}, err
	}
	return contactFromClient(cl, u.Email)
}

// RecordSnipedDomain inserts the active domain row for a domain a hunt just
// won: it is already registered at the registrar, so there is no pending
// state and no register job. It only uses the ctx-aware repositories, so
// called inside a transaction it commits (or rolls back) with the caller.
// Renewal rate, auto-renew and billing cycle follow the checkout defaults.
func (s *Service) RecordSnipedDomain(ctx context.Context, in ports.RecordSnipedDomainInput) (*domain.Domain, error) {
	reg, err := s.d.Registrars.GetByName(ctx, registrarName)
	if err != nil {
		return nil, err
	}
	if reg == nil {
		return nil, apperr.NotFound("registrar")
	}
	ns := in.Nameservers
	if ns == nil {
		ns = []string{}
	}
	nsJSON, err := json.Marshal(ns)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	meta := map[string]any{"source": "snipe"}
	if in.Result.OrderID != "" {
		meta["order_id"] = in.Result.OrderID
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	now := s.d.Clock.Now()
	d := &domain.Domain{
		ClientID:         in.ClientID,
		RegistrarID:      reg.ID,
		Name:             NormalizeDomainName(in.Name),
		Status:           domain.DomainActive,
		RegistrationDate: &now,
		RecurringAmount:  in.RenewPerYear,
		BillingCycle:     domain.CycleAnnually,
		AutoRenew:        true,
		Nameservers:      nsJSON,
		RegistrarMeta:    metaJSON,
	}
	if !in.Result.ExpiryDate.IsZero() {
		expiry := in.Result.ExpiryDate
		d.ExpiryDate = &expiry
		d.NextDueDate = &expiry
	}
	if err := s.d.Domains.Create(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}
