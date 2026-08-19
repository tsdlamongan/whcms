// Package installer serves the pre-boot "bootstrap" phase of the
// installation wizard (docs/CONTRACTS.md §15): a minimal, dependency-free
// HTTP app that runs in place of the full API when config.Load() itself
// fails (required env vars missing/invalid), letting a fresh install collect
// DB/Redis/S3/mail connection details through a web form, test them, and
// persist them. It has no ports/domain/composition dependency - only
// platform-level connectors - since none of that can exist yet without a
// working DATABASE_URL.
package installer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/platform/config"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/platform/mailer"
	"github.com/tsdlamongan/whcms/backend/internal/platform/redisx"
	"github.com/tsdlamongan/whcms/backend/internal/platform/storage"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
)

// requiredVars mirrors exactly the fields config.LoadFrom treats as required
// (backend/internal/platform/config/config.go) - kept as a small, stable,
// literal list rather than parsed out of the error text.
var requiredVars = []string{"DATABASE_URL", "JWT_SECRET", "APP_ENCRYPTION_KEY"}

// EnvFilePath resolves the KEY=VALUE file the wizard reads/writes: the
// ENV_FILE env var if set, else ./.env relative to the current working
// directory (matching backend/Makefile's own `include .env` convention for
// native/local dev - CONTRACTS.md §11).
func EnvFilePath() string {
	if p := os.Getenv("ENV_FILE"); p != "" {
		return p
	}
	return ".env"
}

// RunBootstrap serves the bootstrap installer app on APP_PORT (default 8080)
// until an operator submits a complete, validated config, at which point it
// writes it to the env file and self-restarts (syscall.Exec) so the next
// boot picks up the finished config fresh via the normal path. cfgErr is
// config.Load's error, surfaced by the status endpoint so the wizard can
// explain what's missing.
func RunBootstrap(log *slog.Logger, cfgErr error) error {
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	app, h := newBootstrapApp(log, EnvFilePath(), cfgErr)

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Listen(":"+port, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	log.Warn("api: required config missing/invalid - serving the installation wizard instead of the full API",
		"reason", cfgErr, "install_url", fmt.Sprintf("http://localhost:%s/install", port))

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-stop:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return app.ShutdownWithContext(shutdownCtx)
	case <-h.restart:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = app.ShutdownWithContext(shutdownCtx)
		return execSelf()
	}
}

func execSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("installer: resolve executable path: %w", err)
	}
	return syscall.Exec(exe, os.Args, os.Environ()) //nolint:gosec // re-executing our own already-running binary
}

// newBootstrapApp builds the bootstrap Fiber app and its handler, split out
// from RunBootstrap so tests can drive routes via app.Test() without going
// through the blocking Listen/signal/restart select loop.
func newBootstrapApp(log *slog.Logger, envPath string, cfgErr error) (*fiber.App, *bootstrapHandler) {
	app := fiber.New(fiber.Config{
		AppName:      "WHCMS API (installer)",
		ErrorHandler: transporthttp.ErrorHandler(log),
	})
	app.Use(transporthttp.RequestID())
	app.Get("/healthz", func(c fiber.Ctx) error { return c.SendString("ok") })

	h := &bootstrapHandler{log: log, envPath: envPath, cfgErr: cfgErr, restart: make(chan struct{}, 1)}
	// /status lives at the SAME path the app-level install module
	// (internal/modules/install) uses once config/DB are up, so the
	// frontend wizard can always probe one URL regardless of which phase is
	// currently running - only this bootstrap phase ever answers with
	// config_ready:false.
	api := app.Group("/api/v1/install")
	api.Get("/status", h.status)
	grp := api.Group("/bootstrap")
	grp.Post("/test-db", h.testDB)
	grp.Post("/test-redis", h.testRedis)
	grp.Post("/test-s3", h.testS3)
	grp.Post("/test-mail", h.testMail)
	grp.Post("/save", h.save)
	return app, h
}

type bootstrapHandler struct {
	log     *slog.Logger
	envPath string
	cfgErr  error
	restart chan struct{}
}

// statusResponse's shape is a superset of install.StatusResponse (config_ready
// + installed), with bootstrap-only fields appended - same field names,
// same JSON path, so the frontend wizard can decode either phase's response
// with one type.
type statusResponse struct {
	ConfigReady bool     `json:"config_ready"`
	Installed   bool     `json:"installed"`
	Missing     []string `json:"missing,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// status never echoes current values back - some of the required fields
// (DATABASE_URL, mail credentials) can embed secrets, and golden rule §0.3
// forbids secrets ever appearing in an API response, even a partial one.
func (h *bootstrapHandler) status(c fiber.Ctx) error {
	missing := make([]string, 0, len(requiredVars))
	for _, key := range requiredVars {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}
	reason := ""
	if h.cfgErr != nil {
		reason = h.cfgErr.Error()
	}
	return httpx.OK(c, statusResponse{ConfigReady: false, Missing: missing, Reason: reason})
}

type testDBRequest struct {
	DatabaseURL string `json:"database_url"`
}

func (h *bootstrapHandler) testDB(c fiber.Ctx) error {
	var req testDBRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if req.DatabaseURL == "" {
		return apperr.Validation("database_url is required")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	conn, err := db.Connect(ctx, req.DatabaseURL)
	if err != nil {
		return apperr.Validation(fmt.Sprintf("could not connect: %v", err))
	}
	conn.Close()
	return httpx.OK(c, fiber.Map{"ok": true})
}

type testRedisRequest struct {
	RedisAddr     string `json:"redis_addr"`
	RedisPassword string `json:"redis_password"`
	RedisDB       int    `json:"redis_db"`
}

func (h *bootstrapHandler) testRedis(c fiber.Ctx) error {
	var req testRedisRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if req.RedisAddr == "" {
		return apperr.Validation("redis_addr is required")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	client, err := redisx.Connect(ctx, req.RedisAddr, req.RedisPassword, req.RedisDB)
	if err != nil {
		return apperr.Validation(fmt.Sprintf("could not connect: %v", err))
	}
	_ = client.Close()
	return httpx.OK(c, fiber.Map{"ok": true})
}

type testS3Request struct {
	RustFSEndpoint  string `json:"rustfs_endpoint"`
	RustFSAccessKey string `json:"rustfs_access_key"`
	RustFSSecretKey string `json:"rustfs_secret_key"`
	RustFSBucket    string `json:"rustfs_bucket"`
	RustFSUseSSL    bool   `json:"rustfs_use_ssl"`
}

func (h *bootstrapHandler) testS3(c fiber.Ctx) error {
	var req testS3Request
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if req.RustFSEndpoint == "" || req.RustFSBucket == "" {
		return apperr.Validation("rustfs_endpoint and rustfs_bucket are required")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	_, err := storage.New(ctx, storage.Options{
		Endpoint:  req.RustFSEndpoint,
		AccessKey: req.RustFSAccessKey,
		SecretKey: req.RustFSSecretKey,
		Bucket:    req.RustFSBucket,
		UseSSL:    req.RustFSUseSSL,
	})
	if err != nil {
		return apperr.Validation(fmt.Sprintf("could not connect: %v", err))
	}
	return httpx.OK(c, fiber.Map{"ok": true})
}

type testMailRequest struct {
	MailDriver    string `json:"mail_driver"`
	SMTPHost      string `json:"smtp_host"`
	SMTPPort      int    `json:"smtp_port"`
	SMTPUser      string `json:"smtp_user"`
	SMTPPass      string `json:"smtp_pass"`
	MailHTTPURL   string `json:"mail_http_url"`
	TestRecipient string `json:"test_recipient"`
}

func (h *bootstrapHandler) testMail(c fiber.Ctx) error {
	var req testMailRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	driver := req.MailDriver
	if driver == "" {
		driver = "log"
	}
	if driver == "log" {
		return httpx.OK(c, fiber.Map{"ok": true})
	}
	if req.TestRecipient == "" {
		return apperr.Validation("test_recipient is required to send a test email")
	}
	m, err := mailer.New(config.Config{
		MailDriver:  driver,
		SMTPHost:    req.SMTPHost,
		SMTPPort:    req.SMTPPort,
		SMTPUser:    req.SMTPUser,
		SMTPPass:    req.SMTPPass,
		MailHTTPURL: req.MailHTTPURL,
	}, h.log)
	if err != nil {
		return apperr.Validation(fmt.Sprintf("invalid mail config: %v", err))
	}
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	msg := ports.MailMessage{
		To:      req.TestRecipient,
		From:    "no-reply@example.com",
		Subject: "WHCMS installation wizard: test email",
		Text:    "If you're reading this, your mail configuration works.",
		HTML:    "<p>If you're reading this, your mail configuration works.</p>",
	}
	if err := m.Send(ctx, msg); err != nil {
		return apperr.Validation(fmt.Sprintf("could not send test email: %v", err))
	}
	return httpx.OK(c, fiber.Map{"ok": true})
}

type saveRequest struct {
	AppEnv          string `json:"app_env"`
	AppBaseURL      string `json:"app_base_url"`
	FrontendURL     string `json:"frontend_url"`
	DatabaseURL     string `json:"database_url"`
	RedisAddr       string `json:"redis_addr"`
	RedisPassword   string `json:"redis_password"`
	RedisDB         int    `json:"redis_db"`
	RustFSEndpoint  string `json:"rustfs_endpoint"`
	RustFSAccessKey string `json:"rustfs_access_key"`
	RustFSSecretKey string `json:"rustfs_secret_key"`
	RustFSBucket    string `json:"rustfs_bucket"`
	RustFSUseSSL    bool   `json:"rustfs_use_ssl"`
	MailDriver      string `json:"mail_driver"`
	SMTPHost        string `json:"smtp_host"`
	SMTPPort        int    `json:"smtp_port"`
	SMTPUser        string `json:"smtp_user"`
	SMTPPass        string `json:"smtp_pass"`
	MailHTTPURL     string `json:"mail_http_url"`
}

// save validates the submitted config (reusing config.LoadFrom itself, so
// there is exactly one place that decides what "valid" means), auto-generates
// JWT_SECRET/APP_ENCRYPTION_KEY when left blank, persists everything to the
// env file, and schedules a self-restart shortly after responding.
func (h *bootstrapHandler) save(c fiber.Ctx) error {
	var req saveRequest
	if err := c.Bind().Body(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	if req.DatabaseURL == "" {
		return apperr.Validation("database_url is required")
	}

	values := map[string]string{
		"APP_ENV":           firstNonEmpty(req.AppEnv, "production"),
		"APP_BASE_URL":      req.AppBaseURL,
		"FRONTEND_URL":      req.FrontendURL,
		"DATABASE_URL":      req.DatabaseURL,
		"REDIS_ADDR":        req.RedisAddr,
		"REDIS_PASSWORD":    req.RedisPassword,
		"REDIS_DB":          fmt.Sprintf("%d", req.RedisDB),
		"RUSTFS_ENDPOINT":   req.RustFSEndpoint,
		"RUSTFS_ACCESS_KEY": req.RustFSAccessKey,
		"RUSTFS_SECRET_KEY": req.RustFSSecretKey,
		"RUSTFS_BUCKET":     req.RustFSBucket,
		"RUSTFS_USE_SSL":    fmt.Sprintf("%t", req.RustFSUseSSL),
		"MAIL_DRIVER":       firstNonEmpty(req.MailDriver, "log"),
		"SMTP_HOST":         req.SMTPHost,
		"SMTP_PORT":         fmt.Sprintf("%d", req.SMTPPort),
		"SMTP_USER":         req.SMTPUser,
		"SMTP_PASS":         req.SMTPPass,
		"MAIL_HTTP_URL":     req.MailHTTPURL,
	}
	for k, v := range values {
		if v == "" || v == "0" {
			delete(values, k)
		}
	}

	jwtSecret, err := randomToken(32)
	if err != nil {
		return apperr.Internal(err)
	}
	encKey, err := randomToken(32)
	if err != nil {
		return apperr.Internal(err)
	}
	if os.Getenv("JWT_SECRET") == "" {
		values["JWT_SECRET"] = jwtSecret
	}
	if os.Getenv("APP_ENCRYPTION_KEY") == "" {
		values["APP_ENCRYPTION_KEY"] = encKey
	}

	// Validate through the same rules the real boot path uses, against the
	// real environment overlaid with these new values (a real env var still
	// wins, mirroring ApplyEnvFile's own precedence).
	merged := map[string]string{}
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				merged[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	for k, v := range values {
		if _, exists := os.LookupEnv(k); !exists {
			merged[k] = v
		}
	}
	if _, err := config.LoadFrom(func(key string) (string, bool) { v, ok := merged[key]; return v, ok }); err != nil {
		return apperr.Validation(err.Error())
	}

	if err := config.WriteEnvFile(h.envPath, values); err != nil {
		return apperr.Internal(err)
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		h.restart <- struct{}{}
	}()
	return httpx.OK(c, fiber.Map{"restarting": true})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// randReader defaults to crypto/rand's Reader; overridable in tests to force
// randomToken's error branch deterministically.
var randReader io.Reader = rand.Reader

func randomToken(numBytes int) (string, error) {
	b := make([]byte, numBytes)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", fmt.Errorf("installer: generate random token: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
