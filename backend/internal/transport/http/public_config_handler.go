package http

import (
	"context"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/service/captcha"
	settingssvc "github.com/tsdlamongan/whcms/backend/internal/service/settings"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
)

// CaptchaConfigProvider exposes the browser-facing CAPTCHA config (satisfied by
// *service/captcha.Guard).
type CaptchaConfigProvider interface {
	PublicConfig(ctx context.Context) captcha.PublicConfig
}

// TaxConfigProvider exposes the browser-facing settings-derived config
// (satisfied by *service/settings.Service): the tax settings so order/cart
// price previews reflect the real billing.tax_* values, and the
// email-verification requirement so the cart/dashboard can mirror the
// checkout gate instead of guessing.
type TaxConfigProvider interface {
	TaxConfig(ctx context.Context) settingssvc.PublicTaxConfig
	RequireEmailVerification(ctx context.Context) bool
}

// PublicConfigMiddlewares is the narrow middleware surface the handler needs.
type PublicConfigMiddlewares interface {
	RateLimit(prefix string, limit int, window time.Duration) fiber.Handler
}

// PublicConfigHandler serves unauthenticated, browser-safe runtime config
// (the optional CAPTCHA toggle + public site key, and the effective billing
// tax settings) so public pages like login/register/checkout can render
// conditionally. It never exposes secrets.
type PublicConfigHandler struct {
	captcha CaptchaConfigProvider
	tax     TaxConfigProvider
	mw      PublicConfigMiddlewares
}

// NewPublicConfigHandler builds a PublicConfigHandler.
func NewPublicConfigHandler(captchaCfg CaptchaConfigProvider, taxCfg TaxConfigProvider, mw PublicConfigMiddlewares) *PublicConfigHandler {
	return &PublicConfigHandler{captcha: captchaCfg, tax: taxCfg, mw: mw}
}

// publicBillingConfig is the browser-facing tax config (part of the
// GET /public/config payload).
type publicBillingConfig struct {
	TaxEnabled   bool    `json:"tax_enabled"`
	TaxRate      float64 `json:"tax_rate"`
	TaxInclusive bool    `json:"tax_inclusive"`
}

// publicSecurityConfig is the browser-facing security config (part of the
// GET /public/config payload). RequireEmailVerification only mirrors the
// checkout gate for UI hints - the backend still enforces it at POST /orders.
type publicSecurityConfig struct {
	RequireEmailVerification bool `json:"require_email_verification"`
}

// publicConfigResponse is the GET /public/config payload.
type publicConfigResponse struct {
	Captcha  captcha.PublicConfig `json:"captcha"`
	Billing  publicBillingConfig  `json:"billing"`
	Security publicSecurityConfig `json:"security"`
}

// Register mounts GET /public/config on r (the /api/v1 group), rate limited.
func (h *PublicConfigHandler) Register(r fiber.Router) {
	r.Get("/public/config", h.mw.RateLimit("public_config", 120, time.Minute), h.Get)
}

// Get returns the browser-facing runtime config.
func (h *PublicConfigHandler) Get(c fiber.Ctx) error {
	tax := h.tax.TaxConfig(c.Context())
	return httpx.OK(c, publicConfigResponse{
		Captcha: h.captcha.PublicConfig(c.Context()),
		Billing: publicBillingConfig{
			TaxEnabled:   tax.Enabled,
			TaxRate:      tax.Rate,
			TaxInclusive: tax.Inclusive,
		},
		Security: publicSecurityConfig{
			RequireEmailVerification: h.tax.RequireEmailVerification(c.Context()),
		},
	})
}
