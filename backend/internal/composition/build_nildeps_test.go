// This test exercises the full happy path of Build (every constructor +
// forwarder wiring) without any live Postgres/Redis/S3 connection. It relies
// on the same guarantee documented in build_invalid_config_test.go: every
// repository/adapter constructor here only stores the *db.DB / *redis.Client
// / *storage.S3 handle - none of them dial or query during construction - so
// passing nil handles through a fully successful Build is safe and needs no
// live infra. This complements build_test.go's //go:build integration
// variant (which asserts the same thing against a real Postgres/Redis) by
// giving the >90% unit coverage gate a fast, infra-free way to cover Build's
// long, mostly-linear happy path.
package composition

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/tsdlamongan/whcms/backend/internal/platform/config"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nilDepsValidConfig() config.Config {
	return config.Config{
		AppEnv:             "test",
		AppPort:            8080,
		AppBaseURL:         "http://localhost:8080",
		FrontendURL:        "http://localhost:5173",
		JWTSecret:          "test-jwt-secret",
		EncryptionKey:      make([]byte, 32),
		DatabaseURL:        "postgres://root:postgres@localhost:5432/whmcs_e2e?sslmode=disable",
		RedisAddr:          "localhost:6379",
		DuitkuMerchantCode: "MC1",
		DuitkuAPIKey:       "key",
		DuitkuEnv:          "sandbox",
		RDashResellerID:    "reseller",
		RDashAPIKey:        "key",
		RDashBaseURL:       "https://api.rdash.id/v1",
		MailDriver:         "log",
		WorkerConcurrency:  10,
		AdminAlertEmail:    "admin@example.com",
	}
}

// TestBuild_SuccessWithNilInfraHandles wires the full App with nil DB/Redis/S3
// handles (no live infra required) and asserts every field is populated -
// the same assertions as the integration-tagged TestBuild_Success.
func TestBuild_SuccessWithNilInfraHandles(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	app, err := Build(context.Background(), nilDepsValidConfig(), (*db.DB)(nil), (*redis.Client)(nil), nil, log)
	require.NoError(t, err)
	require.NotNil(t, app)

	assert.NotNil(t, app.Auth)
	assert.NotNil(t, app.Clients)
	assert.NotNil(t, app.Catalog)
	assert.NotNil(t, app.Orders)
	assert.NotNil(t, app.Billing)
	assert.NotNil(t, app.Payments)
	assert.NotNil(t, app.Provisioning)
	assert.NotNil(t, app.Domains)
	assert.NotNil(t, app.Tickets)
	assert.NotNil(t, app.Notifications)
	assert.NotNil(t, app.AdminOps)
	assert.NotNil(t, app.Announcements)
	assert.NotNil(t, app.Knowledgebase)
	assert.NotNil(t, app.NetworkStatus)
	assert.NotNil(t, app.Contact)
	assert.NotNil(t, app.Settings)
	assert.NotNil(t, app.Install)
	assert.NotNil(t, app.Captcha)
	assert.NotNil(t, app.AuthUsersRepo)
	assert.NotNil(t, app.Tokens)
	assert.NotNil(t, app.Enqueuer)
}

// TestBuild_SuccessWithRDashOverrideURL exercises the DuitkuBaseURLExplicit
// precedence branch (an operator-pinned DUITKU_BASE_URL beats the live mode
// derivation) that TestBuild_Success/TestBuild_SuccessWithNilInfraHandles
// don't otherwise touch.
func TestBuild_SuccessWithRDashOverrideURL(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := nilDepsValidConfig()
	cfg.DuitkuBaseURLExplicit = "http://localhost:9090/duitku"
	cfg.DuitkuEnv = "production"

	app, err := Build(context.Background(), cfg, (*db.DB)(nil), (*redis.Client)(nil), nil, log)
	require.NoError(t, err)
	require.NotNil(t, app)
}
