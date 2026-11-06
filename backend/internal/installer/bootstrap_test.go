package installer

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/platform/config"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// clearEnv unsets the given keys for the duration of the test, restoring
// their prior values (present or absent) on cleanup. Needed because
// requiredVars may already be set in the ambient environment a test runs
// under (e.g. `make cover` loads DATABASE_URL/JWT_SECRET/APP_ENCRYPTION_KEY
// from .env.testing), which would otherwise make these tests order/
// environment-dependent.
func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		prev, existed := os.LookupEnv(k)
		require.NoError(t, os.Unsetenv(k))
		t.Cleanup(func() {
			if existed {
				os.Setenv(k, prev)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func doJSON(t *testing.T, app *fiber.App, method, path, body string) (int, envelope) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var env envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	return resp.StatusCode, env
}

func TestEnvFilePath(t *testing.T) {
	clearEnv(t, "ENV_FILE")
	assert.Equal(t, ".env", EnvFilePath())

	t.Setenv("ENV_FILE", "/tmp/whcms-custom.env")
	assert.Equal(t, "/tmp/whcms-custom.env", EnvFilePath())
}

func TestStatusReportsMissingRequiredVars(t *testing.T) {
	clearEnv(t, "DATABASE_URL", "JWT_SECRET", "APP_ENCRYPTION_KEY")
	t.Setenv("JWT_SECRET", "already-set")

	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), assert.AnError)
	status, env := doJSON(t, app, "GET", "/api/v1/install/status", "")
	require.Equal(t, fiber.StatusOK, status)

	var got statusResponse
	require.NoError(t, json.Unmarshal(env.Data, &got))
	assert.False(t, got.ConfigReady)
	assert.ElementsMatch(t, []string{"DATABASE_URL", "APP_ENCRYPTION_KEY"}, got.Missing)
	assert.Equal(t, assert.AnError.Error(), got.Reason)
}

func TestTestDBRequiresURL(t *testing.T) {
	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), nil)
	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/test-db", `{}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestTestDBRejectsUnreachable(t *testing.T) {
	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), nil)
	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/test-db",
		`{"database_url":"postgres://root:wrongpass@localhost:1/nope?sslmode=disable&connect_timeout=1"}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)
	require.NotNil(t, env.Error)
	assert.Contains(t, env.Error.Message, "could not connect")
}

func TestTestRedisRequiresAddr(t *testing.T) {
	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), nil)
	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/test-redis", `{}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestTestS3RequiresFields(t *testing.T) {
	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), nil)
	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/test-s3", `{}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)
	require.NotNil(t, env.Error)
	assert.Equal(t, "VALIDATION", env.Error.Code)
}

func TestTestMailLogDriverAlwaysOK(t *testing.T) {
	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), nil)
	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/test-mail", `{"mail_driver":"log"}`)
	assert.Equal(t, fiber.StatusOK, status)
	assert.Nil(t, env.Error)

	status, env = doJSON(t, app, "POST", "/api/v1/install/bootstrap/test-mail", `{}`)
	assert.Equal(t, fiber.StatusOK, status, "mail_driver defaults to log")
	assert.Nil(t, env.Error)
}

func TestTestMailNonLogRequiresRecipient(t *testing.T) {
	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), nil)
	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/test-mail",
		`{"mail_driver":"http","mail_http_url":"http://localhost:9090/mail/send"}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)
	require.NotNil(t, env.Error)
	assert.Contains(t, env.Error.Message, "test_recipient")
}

func TestSaveRequiresDatabaseURL(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "app.env")
	app, _ := newBootstrapApp(discardLogger(), envPath, nil)

	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/save", `{}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)
	require.NotNil(t, env.Error)

	_, statErr := os.Stat(envPath)
	assert.True(t, os.IsNotExist(statErr), "no file should be written when validation fails")
}

func TestSaveRejectsInvalidMailDriver(t *testing.T) {
	// save() lets a real env var win over the submitted value (mirroring
	// ApplyEnvFile's precedence - see the comment on save()), so MAIL_DRIVER
	// must be cleared too: otherwise a MAIL_DRIVER from an env file loaded into
	// this test process's env (scripts/with-env.sh does that for `make cover`)
	// silently overrides "pigeon" with its own valid driver and this test
	// passes for the wrong reason (or fails, as it did here).
	clearEnv(t, "DATABASE_URL", "JWT_SECRET", "APP_ENCRYPTION_KEY", "MAIL_DRIVER")
	envPath := filepath.Join(t.TempDir(), "app.env")
	app, _ := newBootstrapApp(discardLogger(), envPath, nil)

	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/save",
		`{"database_url":"postgres://root:postgres@localhost:5432/whmcs_e2e?sslmode=disable","mail_driver":"pigeon"}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)
	require.NotNil(t, env.Error)
	assert.Contains(t, env.Error.Message, "MAIL_DRIVER")

	_, statErr := os.Stat(envPath)
	assert.True(t, os.IsNotExist(statErr), "no file should be written when validation fails")
}

func TestSaveWritesFileGeneratesSecretsAndSchedulesRestart(t *testing.T) {
	clearEnv(t, "DATABASE_URL", "JWT_SECRET", "APP_ENCRYPTION_KEY")
	envPath := filepath.Join(t.TempDir(), "nested", "app.env")
	app, h := newBootstrapApp(discardLogger(), envPath, nil)

	dsn := "postgres://root:postgres@localhost:5432/whmcs_e2e?sslmode=disable"
	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/save",
		`{"database_url":"`+dsn+`"}`)
	require.Equal(t, fiber.StatusOK, status)
	assert.Nil(t, env.Error)

	written, err := config.ReadEnvFile(envPath)
	require.NoError(t, err)
	assert.Equal(t, dsn, written["DATABASE_URL"])
	assert.Equal(t, "production", written["APP_ENV"], "defaults to production when unset")
	assert.Equal(t, "log", written["MAIL_DRIVER"])
	require.NotEmpty(t, written["JWT_SECRET"], "must auto-generate when missing")
	require.NotEmpty(t, written["APP_ENCRYPTION_KEY"], "must auto-generate when missing")

	select {
	case <-h.restart:
		// restart was scheduled, as expected - actually exec'ing the process
		// is exercised only by a manual smoke test (docs/CONTRACTS.md §15).
	case <-time.After(2 * time.Second):
		t.Fatal("expected a restart signal to be scheduled after a successful save")
	}
}
