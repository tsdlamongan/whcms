// WHCMS API server. This is the composition root for the HTTP process:
// it builds every service via composition.Build and mounts all 15 feature
// modules on /api/v1, plus /healthz, /readyz, the settings reference module
// (docs/WIRING.md), and the installation wizard's app-level phase
// (docs/CONTRACTS.md §15). If required config is missing/invalid, run()
// serves the wizard's pre-boot bootstrap phase (internal/installer) instead.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/composition"
	"github.com/tsdlamongan/whcms/backend/internal/installer"
	"github.com/tsdlamongan/whcms/backend/internal/modules/adminops"
	"github.com/tsdlamongan/whcms/backend/internal/modules/announcements"
	"github.com/tsdlamongan/whcms/backend/internal/modules/auth"
	"github.com/tsdlamongan/whcms/backend/internal/modules/billing"
	"github.com/tsdlamongan/whcms/backend/internal/modules/catalog"
	"github.com/tsdlamongan/whcms/backend/internal/modules/clients"
	"github.com/tsdlamongan/whcms/backend/internal/modules/contact"
	"github.com/tsdlamongan/whcms/backend/internal/modules/domains"
	"github.com/tsdlamongan/whcms/backend/internal/modules/install"
	"github.com/tsdlamongan/whcms/backend/internal/modules/knowledgebase"
	"github.com/tsdlamongan/whcms/backend/internal/modules/networkstatus"
	"github.com/tsdlamongan/whcms/backend/internal/modules/notifications"
	"github.com/tsdlamongan/whcms/backend/internal/modules/orders"
	"github.com/tsdlamongan/whcms/backend/internal/modules/payments"
	"github.com/tsdlamongan/whcms/backend/internal/modules/provisioning"
	"github.com/tsdlamongan/whcms/backend/internal/modules/snipe"
	"github.com/tsdlamongan/whcms/backend/internal/modules/tickets"
	"github.com/tsdlamongan/whcms/backend/internal/platform/config"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/platform/logger"
	"github.com/tsdlamongan/whcms/backend/internal/platform/ratelimit"
	"github.com/tsdlamongan/whcms/backend/internal/platform/redisx"
	"github.com/tsdlamongan/whcms/backend/internal/platform/storage"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api: fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// Best-effort: pick up any env file the installation wizard has already
	// written (docs/CONTRACTS.md §15). A real env var always wins; a missing
	// file is not an error.
	_ = config.ApplyEnvFile(installer.EnvFilePath())

	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		// Required config still missing/invalid - serve the installation
		// wizard's bootstrap phase instead of exiting fatally. It writes a
		// complete config to the env file and self-restarts once the
		// operator finishes the form; the branch below then runs normally.
		bootstrapLog := logger.New("development")
		slog.SetDefault(bootstrapLog)
		return installer.RunBootstrap(bootstrapLog, cfgErr)
	}
	log := logger.New(cfg.AppEnv)
	slog.SetDefault(log)
	ctx := context.Background()

	// --- Infrastructure -------------------------------------------------
	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		return err
	}
	log.Info("migrations applied")

	database, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	rdb, err := redisx.Connect(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	s3, err := storage.New(ctx, storage.Options{
		Endpoint:  cfg.RustFSEndpoint,
		AccessKey: cfg.RustFSAccessKey,
		SecretKey: cfg.RustFSSecretKey,
		Bucket:    cfg.RustFSBucket,
		UseSSL:    cfg.RustFSUseSSL,
	})
	if err != nil {
		return err
	}
	// --- Composition root: build every repo/adapter/service ----------------
	app, err := composition.Build(ctx, cfg, database, rdb, s3, log)
	if err != nil {
		return err
	}

	mw := &transporthttp.Middleware{
		Tokens:  app.Tokens,
		Limiter: ratelimit.New(rdb),
		Log:     log,
		// Staff permissions live on the users row (permissions JSONB).
		Perms: auth.NewPermissionSource(app.AuthUsersRepo),
	}

	// --- HTTP app --------------------------------------------------------
	// The API is never reachable directly from the public internet - the
	// container only `expose`s :8080 (deploy/docker-compose*.yml) - so the
	// only path in is the frontend's internal-network hop (or the platform
	// edge proxy for the Duitku webhook route), both of which land on a
	// private/loopback peer address. Trust exactly that hop to read the real
	// client IP from X-Forwarded-For; otherwise c.IP() (used by RateLimit and
	// the login lockout key, see transport/http/middleware.go) always
	// resolves to that one constant internal IP for every end user, letting a
	// single client exhaust the shared auth rate limit for the whole site.
	fiberApp := fiber.New(fiber.Config{
		AppName:      "WHCMS API",
		ErrorHandler: transporthttp.ErrorHandler(log),
		TrustProxy:   true,
		ProxyHeader:  fiber.HeaderXForwardedFor,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Private:  true,
			Loopback: true,
		},
	})
	fiberApp.Use(transporthttp.RequestID())

	fiberApp.Get("/healthz", func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	fiberApp.Get("/readyz", func(c fiber.Ctx) error {
		checkCtx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancel()
		checks := map[string]string{"postgres": "ok", "redis": "ok", "s3": "ok"}
		healthy := true
		// Dependency errors are logged server-side only - this endpoint is
		// unauthenticated (reachable before RequireAuth runs), so the response
		// body must never carry raw driver error text (hostnames, ports, etc).
		if err := database.Ping(checkCtx); err != nil {
			log.Error("readyz: postgres unhealthy", "error", err)
			checks["postgres"], healthy = "down", false
		}
		if err := rdb.Ping(checkCtx).Err(); err != nil {
			log.Error("readyz: redis unhealthy", "error", err)
			checks["redis"], healthy = "down", false
		}
		if err := s3.Healthy(checkCtx); err != nil {
			log.Error("readyz: s3 unhealthy", "error", err)
			checks["s3"], healthy = "down", false
		}
		status := fiber.StatusOK
		if !healthy {
			status = fiber.StatusServiceUnavailable
		}
		return c.Status(status).JSON(httpx.Envelope{Data: checks})
	})

	api := fiberApp.Group("/api/v1")

	// --- Reference module (settings) - kept exactly as-is: pre-wrapped
	// /admin group, mounted alongside the 15 modules below. -----------------
	admin := api.Group("/admin", mw.RequireAuth(), mw.RequireRole("admin", "staff"))
	settingsHandler := transporthttp.NewSettingsHandler(app.Settings)
	settingsHandler.Register(admin.Group("/settings", mw.RequirePermission("settings")))

	// Public, unauthenticated runtime config (CAPTCHA toggle + site key, and
	// the effective billing tax settings) so public pages can render the
	// widget and price previews conditionally. No secrets exposed.
	transporthttp.NewPublicConfigHandler(app.Captcha, app.Settings, mw).Register(api)

	// --- Installation wizard, app-level phase (docs/CONTRACTS.md §15) -
	// unauthenticated by necessity (there's no admin yet); the service layer
	// re-checks ExistsAnyAdmin on every mutating call instead. -------------
	install.NewHandler(app.Install).RegisterRoutes(api)

	// --- 15 feature modules - each RegisterRoutes creates its own /admin
	// sub-group internally with its own auth/role/permission chain. ---------
	auth.NewHandler(app.Auth, mw).RegisterRoutes(api)
	clients.NewHandler(app.Clients, mw).RegisterRoutes(api)
	catalog.NewHandler(app.Catalog, mw).RegisterRoutes(api)
	orders.NewHandler(app.Orders, mw).RegisterRoutes(api)
	billing.NewHandler(app.Billing, mw).RegisterRoutes(api)
	payments.NewHandler(app.Payments, mw).RegisterRoutes(api)
	provisioning.NewHandler(app.Provisioning, mw).RegisterRoutes(api)
	domains.NewHandler(app.Domains, mw).RegisterRoutes(api)
	snipe.NewHandler(app.Snipe, mw).RegisterRoutes(api)
	tickets.NewHandler(app.Tickets, mw).RegisterRoutes(api)
	notifications.NewHandler(app.Notifications, mw).RegisterRoutes(api)
	adminops.NewHandler(app.AdminOps, mw).RegisterRoutes(api)
	announcements.NewHandler(app.Announcements, mw).RegisterRoutes(api)
	knowledgebase.NewHandler(app.Knowledgebase, mw).RegisterRoutes(api)
	networkstatus.NewHandler(app.NetworkStatus, mw).RegisterRoutes(api)
	contact.NewHandler(app.Contact, mw).RegisterRoutes(api)

	// --- Serve with graceful shutdown ------------------------------------
	errCh := make(chan error, 1)
	go func() {
		errCh <- fiberApp.Listen(fmt.Sprintf(":%d", cfg.AppPort), fiber.ListenConfig{
			DisableStartupMessage: true,
		})
	}()
	log.Info("api listening", "port", cfg.AppPort, "env", cfg.AppEnv)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-stop:
		log.Info("api shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return fiberApp.ShutdownWithContext(shutdownCtx)
	}
}
