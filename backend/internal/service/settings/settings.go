// Package settings is the reference service implementation (foundation-owned)
// that later module agents copy: ports-only dependencies, validation via a
// key whitelist, audit logging, unit tests with internal/ports/mocks.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Kind is the expected JSON type of a settings value.
type Kind string

// Value kinds.
const (
	KindString  Kind = "string"
	KindInt     Kind = "int"
	KindNumber  Kind = "number"
	KindBool    Kind = "bool"
	KindIntList Kind = "int_list"
	KindStrList Kind = "string_list"
)

// Spec whitelists every editable settings key with its expected kind
// (CONTRACTS.md §10). Secrets are ENV-only and never appear here.
var Spec = map[string]Kind{
	"company.name":                        KindString,
	"company.logo_key":                    KindString,
	"company.address":                     KindString,
	"company.email":                       KindString,
	"billing.tax_enabled":                 KindBool,
	"billing.tax_rate":                    KindNumber,
	"billing.tax_inclusive":               KindBool,
	"billing.invoice_due_days":            KindInt,
	"billing.renewal_lead_days":           KindInt,
	"billing.late_fee_enabled":            KindBool,
	"billing.late_fee_amount":             KindInt,
	"billing.reminder_days":               KindIntList,
	"billing.overdue_reminder_days":       KindIntList,
	"billing.proforma_enabled":            KindBool,
	"automation.suspend_after_days":       KindInt,
	"automation.terminate_after_days":     KindInt,
	"mail.from_name":                      KindString,
	"mail.from_email":                     KindString,
	"tickets.allowed_extensions":          KindStrList,
	"tickets.max_attachment_mb":           KindInt,
	"fraud.max_orders_per_day":            KindInt,
	"fraud.email_domain_blacklist":        KindStrList,
	"domains.default_nameservers":         KindStrList,
	"security.captcha_enabled":            KindBool,
	"security.captcha_provider":           KindString,
	"security.captcha_site_key":           KindString,
	"security.require_email_verification": KindBool,
}

// gateway.duitku.merchant_code and gateway.duitku.mode are intentionally
// NOT in Spec: they are already editable (masked) via the dedicated
// GET/PUT /admin/gateways endpoint (internal/modules/adminops). Whitelisting
// them here too would open a second, unmasked write path for the same keys
// through the generic settings endpoint - a confusing duplicate. Keep
// gateway config edits on the adminops endpoint only.

// Service exposes grouped settings reads and validated, audited updates.
type Service struct {
	repo  ports.SettingsRepo
	tx    ports.TxManager
	audit ports.AuditLogger
}

// New builds a Service.
func New(repo ports.SettingsRepo, tx ports.TxManager, audit ports.AuditLogger) *Service {
	return &Service{repo: repo, tx: tx, audit: audit}
}

// Grouped returns all settings grouped by key prefix:
// {"billing": {"tax_rate": 11, ...}, "company": {...}}.
func (s *Service) Grouped(ctx context.Context) (map[string]map[string]any, error) {
	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make(map[string]map[string]any)
	for _, row := range rows {
		group, name, ok := strings.Cut(row.Key, ".")
		if !ok {
			group, name = "general", row.Key
		}
		if out[group] == nil {
			out[group] = make(map[string]any)
		}
		var v any
		if err := json.Unmarshal(row.Value, &v); err != nil {
			v = string(row.Value)
		}
		out[group][name] = v
	}
	return out, nil
}

// defaultTaxRate mirrors the fallback orders/billing services use when
// billing.tax_rate has never been explicitly set.
const defaultTaxRate = 11.0

// PublicTaxConfig is the browser-facing tax config (order/cart price
// previews) - never a secret, safe to expose unauthenticated.
type PublicTaxConfig struct {
	Enabled   bool    `json:"enabled"`
	Rate      float64 `json:"rate"`
	Inclusive bool    `json:"inclusive"`
}

// TaxConfig returns the effective, default-applied tax settings - the exact
// same billing.tax_enabled/tax_rate/tax_inclusive reads orders/billing
// services use when computing the real, authoritative tax at order/invoice
// time (CONTRACTS §10). Never errors: a settings read failure degrades to
// "tax disabled" rather than breaking the public config endpoint, matching
// captcha.Guard.PublicConfig's convention.
func (s *Service) TaxConfig(ctx context.Context) PublicTaxConfig {
	enabled, _ := s.repo.GetBool(ctx, "billing.tax_enabled", false)
	rate := defaultTaxRate
	_ = s.repo.GetJSON(ctx, "billing.tax_rate", &rate)
	inclusive, _ := s.repo.GetBool(ctx, "billing.tax_inclusive", false)
	return PublicTaxConfig{Enabled: enabled, Rate: rate, Inclusive: inclusive}
}

// RequireEmailVerification returns the effective
// security.require_email_verification setting - the exact same read
// orders.Checkout gates on - so public pages (cart, dashboard banner) can
// mirror the gate instead of guessing. Defaults to true (the server's own
// default, and the fail-closed choice: the UI hints at verification while the
// backend remains the enforcer either way).
func (s *Service) RequireEmailVerification(ctx context.Context) bool {
	required, _ := s.repo.GetBool(ctx, "security.require_email_verification", true)
	return required
}

// Update validates and persists updates (full key -> new value), atomically,
// and writes one audit entry with before/after snapshots of touched keys.
func (s *Service) Update(ctx context.Context, actorUserID int64, updates map[string]any) error {
	if len(updates) == 0 {
		return apperr.Validation("no settings provided")
	}

	var details []apperr.FieldError
	for key, val := range updates {
		kind, known := Spec[key]
		if !known {
			details = append(details, apperr.FieldError{Field: key, Message: "unknown settings key"})
			continue
		}
		if msg := checkKind(kind, val); msg != "" {
			details = append(details, apperr.FieldError{Field: key, Message: msg})
		}
	}
	if len(details) > 0 {
		return apperr.Validation("invalid settings", details...)
	}

	// Snapshot current values of the touched keys for the audit entry.
	before := map[string]any{}
	rows, err := s.repo.All(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	for _, row := range rows {
		if _, touched := updates[row.Key]; touched {
			var v any
			_ = json.Unmarshal(row.Value, &v)
			before[row.Key] = v
		}
	}

	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		for key, val := range updates {
			if err := s.repo.Set(txCtx, key, val); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return apperr.Internal(err)
	}

	s.audit.Log(ctx, actorUserID, "settings.update", "settings", 0, before, updates)
	return nil
}

func checkKind(kind Kind, val any) string {
	switch kind {
	case KindString:
		if _, ok := val.(string); !ok {
			return "must be a string"
		}
	case KindBool:
		if _, ok := val.(bool); !ok {
			return "must be a boolean"
		}
	case KindNumber:
		if !isNumber(val) {
			return "must be a number"
		}
	case KindInt:
		if !isInt(val) {
			return "must be an integer"
		}
	case KindIntList:
		items, ok := val.([]any)
		if !ok {
			if typed, tok := val.([]int); tok {
				_ = typed
				return ""
			}
			return "must be a list of integers"
		}
		for _, item := range items {
			if !isInt(item) {
				return "must be a list of integers"
			}
		}
	case KindStrList:
		items, ok := val.([]any)
		if !ok {
			if _, tok := val.([]string); tok {
				return ""
			}
			return "must be a list of strings"
		}
		for _, item := range items {
			if _, sok := item.(string); !sok {
				return "must be a list of strings"
			}
		}
	default:
		return fmt.Sprintf("unsupported kind %s", kind)
	}
	return ""
}

func isNumber(val any) bool {
	switch val.(type) {
	case float64, float32, int, int32, int64, json.Number:
		return true
	}
	return false
}

func isInt(val any) bool {
	switch v := val.(type) {
	case int, int32, int64:
		return true
	case float64:
		return v == float64(int64(v)) // JSON numbers arrive as float64
	case json.Number:
		_, err := v.Int64()
		return err == nil
	}
	return false
}
