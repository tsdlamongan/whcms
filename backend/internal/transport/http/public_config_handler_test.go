package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/service/captcha"
	settingssvc "github.com/tsdlamongan/whcms/backend/internal/service/settings"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubCaptchaConfig implements transporthttp.CaptchaConfigProvider.
type stubCaptchaConfig struct{ cfg captcha.PublicConfig }

func (s stubCaptchaConfig) PublicConfig(context.Context) captcha.PublicConfig { return s.cfg }

// stubTaxConfig implements transporthttp.TaxConfigProvider.
type stubTaxConfig struct {
	cfg           settingssvc.PublicTaxConfig
	requireVerify bool
}

func (s stubTaxConfig) TaxConfig(context.Context) settingssvc.PublicTaxConfig { return s.cfg }

func (s stubTaxConfig) RequireEmailVerification(context.Context) bool { return s.requireVerify }

// passMW implements transporthttp.PublicConfigMiddlewares with a no-op limiter.
type passMW struct{}

func (passMW) RateLimit(string, int, time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error { return c.Next() }
}

func TestPublicConfigGet(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: transporthttp.ErrorHandler(discard)})
	captchaProvider := stubCaptchaConfig{cfg: captcha.PublicConfig{
		Enabled:  true,
		Provider: "turnstile",
		SiteKey:  "1x00000000000000000000AA",
	}}
	taxProvider := stubTaxConfig{cfg: settingssvc.PublicTaxConfig{Enabled: true, Rate: 11, Inclusive: false}, requireVerify: true}
	transporthttp.NewPublicConfigHandler(captchaProvider, taxProvider, passMW{}).Register(app.Group("/api/v1"))

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/public/config", nil))
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Data struct {
			Captcha captcha.PublicConfig `json:"captcha"`
			Billing struct {
				TaxEnabled   bool    `json:"tax_enabled"`
				TaxRate      float64 `json:"tax_rate"`
				TaxInclusive bool    `json:"tax_inclusive"`
			} `json:"billing"`
			Security struct {
				RequireEmailVerification bool `json:"require_email_verification"`
			} `json:"security"`
		} `json:"data"`
		Error any `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &env))
	assert.Nil(t, env.Error)
	assert.True(t, env.Data.Captcha.Enabled)
	assert.Equal(t, "turnstile", env.Data.Captcha.Provider)
	assert.Equal(t, "1x00000000000000000000AA", env.Data.Captcha.SiteKey)
	assert.True(t, env.Data.Billing.TaxEnabled)
	assert.Equal(t, 11.0, env.Data.Billing.TaxRate)
	assert.False(t, env.Data.Billing.TaxInclusive)
	assert.True(t, env.Data.Security.RequireEmailVerification)
}

func TestPublicConfigGetDisabled(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: transporthttp.ErrorHandler(discard)})
	captchaProvider := stubCaptchaConfig{cfg: captcha.PublicConfig{Enabled: false, Provider: "turnstile"}}
	taxProvider := stubTaxConfig{cfg: settingssvc.PublicTaxConfig{Enabled: false, Rate: 11}}
	transporthttp.NewPublicConfigHandler(captchaProvider, taxProvider, passMW{}).Register(app.Group("/api/v1"))

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/public/config", nil))
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Data struct {
			Captcha captcha.PublicConfig `json:"captcha"`
			Billing struct {
				TaxEnabled bool `json:"tax_enabled"`
			} `json:"billing"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &env))
	assert.False(t, env.Data.Captcha.Enabled)
	assert.Empty(t, env.Data.Captcha.SiteKey)
	assert.False(t, env.Data.Billing.TaxEnabled)
}
