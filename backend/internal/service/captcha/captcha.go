// Package captcha is a small cross-cutting guard that gates spam-prone public
// actions (login, register, order checkout, contact form) behind an optional CAPTCHA
// (Cloudflare Turnstile). Whether the CAPTCHA is enforced is a runtime setting
// (security.captcha_enabled) so operators can toggle it without a redeploy; the
// public site key is a setting too (security.captcha_site_key) while the secret
// stays ENV-only. When disabled, Verify is a no-op and no token is required.
package captcha

import (
	"context"
	"strings"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Settings keys (CONTRACTS.md §10). The secret key is ENV-only and never here.
const (
	SettingEnabled  = "security.captcha_enabled"
	SettingProvider = "security.captcha_provider"
	SettingSiteKey  = "security.captcha_site_key"

	defaultProvider = "turnstile"
)

// Guard reads the CAPTCHA settings and verifies tokens via the provider adapter.
type Guard struct {
	verifier ports.CaptchaVerifier
	settings ports.SettingsRepo
}

// New builds a Guard. verifier may be nil (only reached when enabled).
func New(verifier ports.CaptchaVerifier, settings ports.SettingsRepo) *Guard {
	return &Guard{verifier: verifier, settings: settings}
}

// Enabled reports whether CAPTCHA enforcement is currently on.
func (g *Guard) Enabled(ctx context.Context) bool {
	enabled, _ := g.settings.GetBool(ctx, SettingEnabled, false)
	return enabled
}

// Verify enforces the CAPTCHA when enabled: a missing token is a VALIDATION
// error, a token the provider rejects is a VALIDATION error, and a provider
// outage surfaces as the adapter's EXTERNAL error (fail-closed). When disabled
// it is a no-op (returns nil) regardless of token. remoteIP is optional.
func (g *Guard) Verify(ctx context.Context, token, remoteIP string) error {
	if !g.Enabled(ctx) {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return apperr.Validation("captcha verification required",
			apperr.FieldError{Field: "captcha_token", Message: "is required"})
	}
	if g.verifier == nil {
		// Enabled but no verifier wired (misconfiguration): fail closed.
		return apperr.External("turnstile", errNoVerifier)
	}
	ok, err := g.verifier.Verify(ctx, token, remoteIP)
	if err != nil {
		return apperr.From(err)
	}
	if !ok {
		return apperr.Validation("captcha verification failed",
			apperr.FieldError{Field: "captcha_token", Message: "invalid or expired, please retry"})
	}
	return nil
}

// PublicConfig is the browser-facing CAPTCHA configuration.
type PublicConfig struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	SiteKey  string `json:"site_key"`
}

// PublicConfig returns the safe, browser-facing CAPTCHA config (never the
// secret). A blank provider defaults to "turnstile".
func (g *Guard) PublicConfig(ctx context.Context) PublicConfig {
	enabled, _ := g.settings.GetBool(ctx, SettingEnabled, false)
	provider, _ := g.settings.GetString(ctx, SettingProvider, defaultProvider)
	if strings.TrimSpace(provider) == "" {
		provider = defaultProvider
	}
	siteKey, _ := g.settings.GetString(ctx, SettingSiteKey, "")
	return PublicConfig{Enabled: enabled, Provider: provider, SiteKey: siteKey}
}

// errNoVerifier is returned when CAPTCHA is enabled but no adapter is wired.
var errNoVerifier = errNoVerifierType("captcha enabled but no verifier configured")

type errNoVerifierType string

func (e errNoVerifierType) Error() string { return string(e) }
