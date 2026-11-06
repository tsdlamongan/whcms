package installer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doJSONSlow is like bootstrap_test.go's doJSON but with a longer app.Test
// timeout: go-redis retries a failed dial several times with backoff, which
// can exceed Fiber's 1-second default test timeout even though the endpoint
// itself responds well within a few seconds.
func doJSONSlow(t *testing.T, app *fiber.App, method, path, body string) (int, envelope) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second, FailOnTimeout: true})
	require.NoError(t, err)
	defer resp.Body.Close()
	var env envelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	return resp.StatusCode, env
}

// --- firstNonEmpty / randomToken --------------------------------------------

func TestFirstNonEmpty(t *testing.T) {
	assert.Equal(t, "a", firstNonEmpty("a", "b"))
	assert.Equal(t, "b", firstNonEmpty("", "b"))
	assert.Equal(t, "", firstNonEmpty("", ""))
	assert.Equal(t, "", firstNonEmpty())
}

func TestRandomToken(t *testing.T) {
	tok, err := randomToken(32)
	require.NoError(t, err)
	assert.NotEmpty(t, tok)

	tok2, err := randomToken(32)
	require.NoError(t, err)
	assert.NotEqual(t, tok, tok2)
}

type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) { return 0, fmt.Errorf("simulated rand failure") }

func TestRandomToken_ReadError(t *testing.T) {
	orig := randReader
	randReader = failingReader{}
	defer func() { randReader = orig }()

	_, err := randomToken(16)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "generate random token")
}

// --- bootstrap handler error branches that don't need live infra ----------

func TestTestRedisRejectsUnreachable(t *testing.T) {
	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), nil)
	status, env := doJSONSlow(t, app, "POST", "/api/v1/install/bootstrap/test-redis",
		`{"redis_addr":"127.0.0.1:1"}`)
	assert.Equal(t, 422, status)
	require.NotNil(t, env.Error)
	assert.Contains(t, env.Error.Message, "could not connect")
}

func TestTestS3RejectsUnreachable(t *testing.T) {
	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), nil)
	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/test-s3",
		`{"rustfs_endpoint":"http://127.0.0.1:1","rustfs_bucket":"whmcs"}`)
	assert.Equal(t, 422, status)
	require.NotNil(t, env.Error)
	assert.Contains(t, env.Error.Message, "could not connect")
}

func TestTestMailInvalidConfig(t *testing.T) {
	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), nil)
	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/test-mail",
		`{"mail_driver":"http","mail_http_url":"","test_recipient":"a@b.com"}`)
	assert.Equal(t, 422, status)
	require.NotNil(t, env.Error)
	assert.Contains(t, env.Error.Message, "invalid mail config")
}

func TestTestMailSendFails(t *testing.T) {
	app, _ := newBootstrapApp(discardLogger(), filepath.Join(t.TempDir(), "app.env"), nil)
	status, env := doJSON(t, app, "POST", "/api/v1/install/bootstrap/test-mail",
		`{"mail_driver":"http","mail_http_url":"http://127.0.0.1:1/mail/send","test_recipient":"a@b.com"}`)
	assert.Equal(t, 422, status)
	require.NotNil(t, env.Error)
	assert.Contains(t, env.Error.Message, "could not send test email")
}

// --- RunBootstrap: exercise the blocking Listen/select loop via a real
// self-signal, complementing the handler-level tests above which only drive
// routes through app.Test() without the real server loop. execSelf (the
// syscall.Exec restart path) is intentionally NOT exercised here - it
// replaces the running process image, which would kill the test binary
// itself; that path is only verified by the manual smoke test referenced in
// docs/CONTRACTS.md §15. -----------------------------------------------------

func TestRunBootstrap_StopsOnSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal-based shutdown test is unix-only")
	}

	t.Setenv("APP_PORT", "0")

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunBootstrap(discardLogger(), assert.AnError)
	}()

	// Give the server a moment to start listening before signalling.
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("RunBootstrap did not return after SIGTERM")
	}
}
