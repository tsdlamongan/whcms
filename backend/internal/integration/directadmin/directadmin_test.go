package directadmin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// --- helpers ---------------------------------------------------------------

func testConfig() Config {
	return Config{
		MaxRetries:       2,
		RetryBackoff:     time.Millisecond,
		BreakerThreshold: 3,
		BreakerCooldown:  time.Minute,
	}
}

func newTestClient(t *testing.T, cfg Config) (*Client, *mocks.MockIntegrationLogger) {
	t.Helper()
	il := &mocks.MockIntegrationLogger{}
	return New(cfg, &http.Client{Timeout: 5 * time.Second}, il, &mocks.MockClock{}), il
}

func serverConfig(t *testing.T, ts *httptest.Server) ports.ServerConfig {
	t.Helper()
	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return ports.ServerConfig{
		ID:       1,
		Name:     "da01",
		Hostname: u.Hostname(),
		Port:     port,
		Username: "admin",
		Password: "adminpass",
		UseSSL:   false,
	}
}

// legacyHandler answers with a legacy url-encoded body and captures the request.
func legacyHandler(t *testing.T, status int, body string, gotForm *url.Values, gotReq *http.Request) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		if gotForm != nil {
			*gotForm = r.Form
		}
		if gotReq != nil {
			*gotReq = *r
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func requireAppErr(t *testing.T, err error, code apperr.Code) *apperr.Error {
	t.Helper()
	require.Error(t, err)
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, code, ae.Code)
	return ae
}

// --- Name / defaults --------------------------------------------------------

func TestName(t *testing.T) {
	c, _ := newTestClient(t, testConfig())
	assert.Equal(t, "directadmin", c.Name())
}

func TestNewDefaults(t *testing.T) {
	c := New(Config{}, nil, nil, nil)
	assert.Equal(t, defaultTimeout, c.cfg.Timeout)
	assert.Equal(t, defaultMaxRetries, c.cfg.MaxRetries)
	assert.Equal(t, defaultRetryBackoff, c.cfg.RetryBackoff)
	assert.Equal(t, defaultBreakerThreshold, c.cfg.BreakerThreshold)
	assert.Equal(t, defaultBreakerCooldown, c.cfg.BreakerCooldown)
	require.NotNil(t, c.hc)
	assert.Equal(t, defaultTimeout, c.hc.Timeout)

	// nil logger and clock are usable end-to-end.
	ts := httptest.NewServer(legacyHandler(t, 200, "error=0&text=Success&details=ok", nil, nil))
	defer ts.Close()
	err := c.Unsuspend(context.Background(), serverConfig(t, ts), "alice")
	require.NoError(t, err)
}

// --- Create ------------------------------------------------------------------

func TestCreateSuccess(t *testing.T) {
	var form url.Values
	var req http.Request
	ts := httptest.NewServer(legacyHandler(t, 200,
		"error=0&text=Success&details=User+alice+created", &form, &req))
	defer ts.Close()

	c, il := newTestClient(t, testConfig())
	res, err := c.Create(context.Background(), serverConfig(t, ts), ports.CreateAccountParams{
		Username: "alice",
		Domain:   "alice.example.com",
		Password: "S3cretPass!",
		Package:  "gold",
		Email:    "alice@example.com",
		IP:       "10.0.0.5",
	})
	require.NoError(t, err)

	// Request shape.
	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "/CMD_API_ACCOUNT_USER", req.URL.Path)
	user, pass, ok := req.BasicAuth()
	require.True(t, ok)
	assert.Equal(t, "admin", user)
	assert.Equal(t, "adminpass", pass)
	assert.Equal(t, "create", form.Get("action"))
	assert.Equal(t, "alice", form.Get("username"))
	assert.Equal(t, "alice.example.com", form.Get("domain"))
	assert.Equal(t, "S3cretPass!", form.Get("passwd"))
	assert.Equal(t, "S3cretPass!", form.Get("passwd2"))
	assert.Equal(t, "gold", form.Get("package"))
	assert.Equal(t, "alice@example.com", form.Get("email"))
	assert.Equal(t, "10.0.0.5", form.Get("ip"))
	assert.Equal(t, "no", form.Get("notify"))

	// Result mapping.
	assert.Equal(t, "alice", res.Username)
	assert.Equal(t, "alice.example.com", res.Domain)
	assert.Equal(t, "10.0.0.5", res.IP)
	assert.Equal(t, "Success", res.Meta["text"])
	assert.Equal(t, "User alice created", res.Meta["details"])

	// Integration log with redacted password.
	require.Len(t, il.Calls, 1)
	call := il.Calls[0]
	assert.Equal(t, "directadmin", call.Provider)
	assert.Equal(t, "/CMD_API_ACCOUNT_USER", call.Endpoint)
	assert.Equal(t, http.MethodPost, call.Method)
	assert.Equal(t, 200, call.StatusCode)
	assert.True(t, call.Success)
	reqLog, ok := call.Request.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "[REDACTED]", reqLog["passwd"])
	assert.Equal(t, "[REDACTED]", reqLog["passwd2"])
	assert.Equal(t, "alice", reqLog["username"])
	assert.NotEmpty(t, reqLog["host"])
}

// Unlike WHM, DirectAdmin has no auto-assign-shared-IP behavior when ip is
// omitted, so a blank IP is rejected locally rather than round-tripped to the
// panel's confusing "A valid IP was not provided".
func TestCreateMissingIPMapsValidation(t *testing.T) {
	c, _ := newTestClient(t, testConfig())
	_, err := c.Create(context.Background(), ports.ServerConfig{Hostname: "x"}, ports.CreateAccountParams{
		Username: "alice", Domain: "alice.example.com",
	})
	requireAppErr(t, err, apperr.CodeValidation)
}

func TestCreateDuplicateMapsConflict(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200,
		"error=1&text=Error+Creating+User&details=That+username+already+exists", nil, nil))
	defer ts.Close()

	c, il := newTestClient(t, testConfig())
	_, err := c.Create(context.Background(), serverConfig(t, ts), ports.CreateAccountParams{Username: "alice", IP: "10.0.0.5"})
	ae := requireAppErr(t, err, apperr.CodeConflict)
	assert.Contains(t, ae.Message, "already exists")

	require.Len(t, il.Calls, 1)
	assert.False(t, il.Calls[0].Success)
	assert.NotEmpty(t, il.Calls[0].Error)
}

func TestCreateReservedUsernameMapsConflict(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200,
		"error=1&text=Error+Creating+User&details=That+username+is+reserved", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	_, err := c.Create(context.Background(), serverConfig(t, ts), ports.CreateAccountParams{Username: "test", IP: "10.0.0.5"})
	ae := requireAppErr(t, err, apperr.CodeConflict)
	assert.Contains(t, ae.Message, "reserved")
}

func TestCreateForcedFailureMapsExternal(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200,
		"error=1&text=Error+Creating+User&details=forced+failure", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	_, err := c.Create(context.Background(), serverConfig(t, ts), ports.CreateAccountParams{Username: "failme", IP: "10.0.0.5"})
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "forced failure")
}

// CMD_API_ACCOUNT_USER is an action command: unlike the read-only dump
// endpoints, DirectAdmin always marks its outcome with an error=0/1 key, so a
// body missing it entirely is genuinely malformed and must not be treated as
// success.
func TestCreateMissingErrorKeyMapsUnexpectedFormat(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "text=nonsense", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	_, err := c.Create(context.Background(), serverConfig(t, ts), ports.CreateAccountParams{Username: "nomarker", IP: "10.0.0.5"})
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "unexpected response format")
}

// --- Suspend / Unsuspend / Terminate ----------------------------------------

func TestSuspendSuccess(t *testing.T) {
	var form url.Values
	var req http.Request
	ts := httptest.NewServer(legacyHandler(t, 200, "error=0&text=Success&details=1+user(s)+suspended", &form, &req))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Suspend(context.Background(), serverConfig(t, ts), "alice", "Overdue on payment")
	require.NoError(t, err)
	assert.Equal(t, "/CMD_API_SELECT_USERS", req.URL.Path)
	assert.Equal(t, "alice", form.Get("select0"))
	assert.Equal(t, "Suspend", form.Get("suspend"))
	assert.Equal(t, "Overdue on payment", form.Get("reason"))
}

func TestSuspendUnknownUserMapsNotFound(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "error=1&text=Error&details=user+bob+does+not+exist", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Suspend(context.Background(), serverConfig(t, ts), "bob", "test")
	requireAppErr(t, err, apperr.CodeNotFound)
}

func TestUnsuspendSuccess(t *testing.T) {
	var form url.Values
	ts := httptest.NewServer(legacyHandler(t, 200, "error=0&text=Success&details=1+user(s)+unsuspended", &form, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Unsuspend(context.Background(), serverConfig(t, ts), "alice")
	require.NoError(t, err)
	assert.Equal(t, "alice", form.Get("select0"))
	assert.Equal(t, "Unsuspend", form.Get("suspend"))
}

func TestTerminateSuccess(t *testing.T) {
	var form url.Values
	ts := httptest.NewServer(legacyHandler(t, 200, "error=0&text=Success&details=1+user(s)+deleted", &form, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Terminate(context.Background(), serverConfig(t, ts), "alice")
	require.NoError(t, err)
	assert.Equal(t, "alice", form.Get("select0"))
	assert.Equal(t, "yes", form.Get("delete"))
}

// TestTerminateNotFoundIsSuccess is the regression test for a real incident:
// deleting an already-gone account used to propagate NOT_FOUND like every
// other mutating op, so ProvisionTerminate returned an error AFTER the
// account had genuinely already been deleted (by an earlier attempt of the
// same asynq-retried job) - the job then retried forever, failing at the
// exact same step every time even though the account was long gone. Mirrors
// DeletePackage's existing idempotent-NotFound handling.
func TestTerminateNotFoundIsSuccess(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "error=1&text=Error&details=user+ghost+does+not+exist", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Terminate(context.Background(), serverConfig(t, ts), "ghost")
	assert.NoError(t, err)
}

func TestTerminateErrorPropagates(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "error=1&text=Error&details=permission+denied", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Terminate(context.Background(), serverConfig(t, ts), "alice")
	requireAppErr(t, err, apperr.CodeExternal)
}

// --- ChangePackage / ChangePassword ------------------------------------------

func TestChangePackageSuccess(t *testing.T) {
	var form url.Values
	var req http.Request
	ts := httptest.NewServer(legacyHandler(t, 200, "error=0&text=Success&details=Package+changed+to+silver", &form, &req))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.ChangePackage(context.Background(), serverConfig(t, ts), "alice", "silver")
	require.NoError(t, err)
	assert.Equal(t, "/CMD_API_MODIFY_USER", req.URL.Path)
	assert.Equal(t, "package", form.Get("action"))
	assert.Equal(t, "alice", form.Get("user"))
	assert.Equal(t, "silver", form.Get("package"))
}

func TestChangePackageUnknownUser(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "error=1&text=Error&details=user+ghost+does+not+exist", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.ChangePackage(context.Background(), serverConfig(t, ts), "ghost", "silver")
	requireAppErr(t, err, apperr.CodeNotFound)
}

func TestChangePasswordSuccess(t *testing.T) {
	var form url.Values
	var req http.Request
	ts := httptest.NewServer(legacyHandler(t, 200, "error=0&text=Success&details=Password+changed", &form, &req))
	defer ts.Close()

	c, il := newTestClient(t, testConfig())
	err := c.ChangePassword(context.Background(), serverConfig(t, ts), "alice", "N3wPass!word")
	require.NoError(t, err)
	assert.Equal(t, "/CMD_API_USER_PASSWD", req.URL.Path)
	assert.Equal(t, "alice", form.Get("username"))
	assert.Equal(t, "N3wPass!word", form.Get("passwd"))
	assert.Equal(t, "N3wPass!word", form.Get("passwd2"))

	require.Len(t, il.Calls, 1)
	reqLog := il.Calls[0].Request.(map[string]string)
	assert.Equal(t, "[REDACTED]", reqLog["passwd"])
	assert.Equal(t, "[REDACTED]", reqLog["passwd2"])
}

// --- AccountInfo ---------------------------------------------------------------

func TestTestConnectionSuccess(t *testing.T) {
	var req http.Request
	// CMD_API_SHOW_USERS returns a legacy list; no error marker = success.
	ts := httptest.NewServer(legacyHandler(t, 200, "list[]=alice&list[]=bob", nil, &req))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	info, err := c.TestConnection(context.Background(), serverConfig(t, ts))
	require.NoError(t, err)
	require.NotNil(t, info)
	// DirectAdmin has no reseller-safe version/nameserver API - metadata empty.
	assert.Empty(t, info.Version)
	assert.Empty(t, info.Nameservers)

	assert.Equal(t, http.MethodGet, req.Method)
	assert.Equal(t, "/CMD_API_SHOW_USERS", req.URL.Path)
}

func TestTestConnectionBadCredentials(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "error=1&text=Unable+to+authenticate", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	_, err := c.TestConnection(context.Background(), serverConfig(t, ts))
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestAccountInfoSuspendedYes(t *testing.T) {
	var req http.Request
	dump := url.Values{
		"username":     {"alice"},
		"email":        {"alice@example.com"},
		"domain":       {"alice.example.com"},
		"package":      {"gold"},
		"ip":           {"10.0.0.5"},
		"suspended":    {"yes"},
		"quota":        {"500"},
		"passwd":       {"leaked-secret"},
		"date_created": {"2026-07-01 10:00:00"},
	}
	ts := httptest.NewServer(legacyHandler(t, 200, dump.Encode(), nil, &req))
	defer ts.Close()

	c, il := newTestClient(t, testConfig())
	info, err := c.AccountInfo(context.Background(), serverConfig(t, ts), "alice")
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, req.Method)
	assert.Equal(t, "/CMD_API_SHOW_USER_CONFIG", req.URL.Path)
	assert.Equal(t, "alice", req.URL.Query().Get("user"))

	assert.Equal(t, "alice", info.Username)
	assert.Equal(t, "alice.example.com", info.Domain)
	assert.Equal(t, "gold", info.Package)
	assert.True(t, info.Suspended)
	assert.Equal(t, int64(500), info.DiskLimit)
	assert.Equal(t, "alice@example.com", info.Meta["email"])
	// Secret-ish keys are excluded from Meta and redacted in logs.
	_, hasSecret := info.Meta["passwd"]
	assert.False(t, hasSecret)
	require.Len(t, il.Calls, 1)
	respLog := il.Calls[0].Response.(map[string]string)
	assert.Equal(t, "[REDACTED]", respLog["passwd"])
}

func TestAccountInfoSuspendedNo(t *testing.T) {
	dump := "username=bob&domain=bob.example.com&package=basic&suspended=no&quota=unlimited"
	ts := httptest.NewServer(legacyHandler(t, 200, dump, nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	info, err := c.AccountInfo(context.Background(), serverConfig(t, ts), "bob")
	require.NoError(t, err)
	assert.False(t, info.Suspended)
	assert.Equal(t, int64(0), info.DiskLimit) // "unlimited" ignored
}

func TestAccountInfoUsernameFallback(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "domain=x.example.com&suspended=no", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	info, err := c.AccountInfo(context.Background(), serverConfig(t, ts), "carol")
	require.NoError(t, err)
	assert.Equal(t, "carol", info.Username)
}

func TestAccountInfoUnknownUserMapsNotFound(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "error=1&text=Error&details=User+ghost+does+not+exist", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	_, err := c.AccountInfo(context.Background(), serverConfig(t, ts), "ghost")
	requireAppErr(t, err, apperr.CodeNotFound)
}

func TestAccountInfoJSONResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"username":"dave","domain":"dave.example.com","package":"gold",` +
			`"suspended":"yes","quota":123,"active":true,"nested":{"a":1},"note":null}`))
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	info, err := c.AccountInfo(context.Background(), serverConfig(t, ts), "dave")
	require.NoError(t, err)
	assert.Equal(t, "dave", info.Username)
	assert.True(t, info.Suspended)
	assert.Equal(t, int64(123), info.DiskLimit)
	assert.Equal(t, "true", info.Meta["active"])
	assert.JSONEq(t, `{"a":1}`, info.Meta["nested"].(string))
	assert.Equal(t, "", info.Meta["note"])
}

// --- JSON / parse edge cases ---------------------------------------------------

func TestJSONErrorResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"error":1,"text":"Error","details":"boom"}`))
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Suspend(context.Background(), serverConfig(t, ts), "alice", "r")
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "boom")
}

func TestJSONSuccessResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":0,"text":"Success","details":"done"}`))
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Suspend(context.Background(), serverConfig(t, ts), "alice", "r")
	require.NoError(t, err)
}

func TestInvalidJSONIsUnparseable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{nope`))
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Suspend(context.Background(), serverConfig(t, ts), "alice", "r")
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "unparseable")
}

func TestUnparseableLegacyBody(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "%zz", nil, nil))
	defer ts.Close()

	c, il := newTestClient(t, testConfig())
	err := c.Unsuspend(context.Background(), serverConfig(t, ts), "alice")
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "unparseable")
	require.Len(t, il.Calls, 1)
	assert.Equal(t, "%zz", il.Calls[0].Response)
}

func TestUnexpectedResponseFormat(t *testing.T) {
	// 200 with parseable body but no error flag on a non-dump command.
	ts := httptest.NewServer(legacyHandler(t, 200, "foo=bar", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Terminate(context.Background(), serverConfig(t, ts), "alice")
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "unexpected response")
}

// --- HTTP-level failures --------------------------------------------------------

func TestUnauthorizedMapsExternal(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 401, "error=1&text=Login+failed&details=invalid+credentials", nil, nil))
	defer ts.Close()

	c, il := newTestClient(t, testConfig())
	_, err := c.AccountInfo(context.Background(), serverConfig(t, ts), "alice")
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "Login failed")
	require.Len(t, il.Calls, 1)
	assert.Equal(t, 401, il.Calls[0].StatusCode)
}

func TestClientErrorWithoutErrorFlag(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 404, "foo=bar", nil, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	err := c.Unsuspend(context.Background(), serverConfig(t, ts), "alice")
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "http 404")
}

func TestServerErrorPOSTNoRetry(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	defer ts.Close()

	c, il := newTestClient(t, testConfig())
	_, err := c.Create(context.Background(), serverConfig(t, ts), ports.CreateAccountParams{Username: "alice", IP: "10.0.0.5"})
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "server error")
	assert.Equal(t, int32(1), hits.Load(), "POST must not be retried")
	require.Len(t, il.Calls, 1)
	assert.Equal(t, 500, il.Calls[0].StatusCode)
}

func TestGETRetriesOn5xxThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(502)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("username=alice&suspended=no"))
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	info, err := c.AccountInfo(context.Background(), serverConfig(t, ts), "alice")
	require.NoError(t, err)
	assert.Equal(t, "alice", info.Username)
	assert.Equal(t, int32(3), hits.Load())
}

func TestGETRetriesExhausted(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(503)
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	_, err := c.AccountInfo(context.Background(), serverConfig(t, ts), "alice")
	requireAppErr(t, err, apperr.CodeExternal)
	assert.Equal(t, int32(3), hits.Load(), "1 attempt + 2 retries")
}

func TestNetworkErrorMapsExternal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	sc := serverConfig(t, ts)
	ts.Close() // guarantee connection refused

	c, il := newTestClient(t, testConfig())
	err := c.Suspend(context.Background(), sc, "alice", "r")
	requireAppErr(t, err, apperr.CodeExternal)
	require.Len(t, il.Calls, 1)
	assert.Equal(t, 0, il.Calls[0].StatusCode)
	assert.False(t, il.Calls[0].Success)
}

func TestContextCancelledDuringRetryBackoff(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts.Close()

	cfg := testConfig()
	cfg.RetryBackoff = time.Second
	c, _ := newTestClient(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.AccountInfo(ctx, serverConfig(t, ts), "alice")
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.True(t, errors.Is(ae, context.Canceled) || ae.Unwrap() != nil)
}

// --- Circuit breaker -------------------------------------------------------------

func TestCircuitBreakerOpensAndRecovers(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	clk := &mocks.MockClock{NowFn: func() time.Time { return now }}
	il := &mocks.MockIntegrationLogger{}

	cfg := testConfig()
	cfg.BreakerThreshold = 2
	cfg.BreakerCooldown = time.Minute
	c := New(cfg, &http.Client{Timeout: 5 * time.Second}, il, clk)

	// A server that always works, to prove fast-fail happens without a call.
	var hits atomic.Int32
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("error=0&text=Success&details=ok"))
	}))
	defer live.Close()
	liveCfg := serverConfig(t, live)

	// A dead server for the two failures that trip the breaker.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadCfg := serverConfig(t, dead)
	dead.Close()

	requireAppErr(t, c.Unsuspend(context.Background(), deadCfg, "alice"), apperr.CodeExternal)
	requireAppErr(t, c.Unsuspend(context.Background(), deadCfg, "alice"), apperr.CodeExternal)

	// Breaker now open: even the live server fast-fails without an HTTP call.
	err := c.Unsuspend(context.Background(), liveCfg, "alice")
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "circuit open")
	assert.Equal(t, int32(0), hits.Load())

	// After the cooldown the call goes through and resets the breaker.
	now = now.Add(61 * time.Second)
	require.NoError(t, c.Unsuspend(context.Background(), liveCfg, "alice"))
	assert.Equal(t, int32(1), hits.Load())
	require.NoError(t, c.Unsuspend(context.Background(), liveCfg, "alice"))
	assert.Equal(t, int32(2), hits.Load())
}

func TestCircuitBreakerReTripsInHalfOpen(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	clk := &mocks.MockClock{NowFn: func() time.Time { return now }}

	cfg := testConfig()
	cfg.BreakerThreshold = 2
	cfg.BreakerCooldown = time.Minute
	c := New(cfg, &http.Client{Timeout: 5 * time.Second}, nil, clk)

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadCfg := serverConfig(t, dead)
	dead.Close()

	requireAppErr(t, c.Unsuspend(context.Background(), deadCfg, "a"), apperr.CodeExternal)
	requireAppErr(t, c.Unsuspend(context.Background(), deadCfg, "a"), apperr.CodeExternal)

	// Half-open after cooldown; failure re-trips immediately (threshold kept).
	now = now.Add(61 * time.Second)
	requireAppErr(t, c.Unsuspend(context.Background(), deadCfg, "a"), apperr.CodeExternal)

	err := c.Unsuspend(context.Background(), deadCfg, "a")
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "circuit open")
}

func TestAPILevelErrorDoesNotTripBreaker(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "error=1&text=Error&details=nope", nil, nil))
	defer ts.Close()

	cfg := testConfig()
	cfg.BreakerThreshold = 2
	c, _ := newTestClient(t, cfg)
	sc := serverConfig(t, ts)

	for i := 0; i < 4; i++ {
		err := c.Unsuspend(context.Background(), sc, "alice")
		ae := requireAppErr(t, err, apperr.CodeExternal)
		assert.NotContains(t, ae.Message, "circuit open")
	}
}

// --- SSOURL -----------------------------------------------------------------------

func TestSSOURLUnconfigured(t *testing.T) {
	c, _ := newTestClient(t, testConfig())
	_, err := c.SSOURL(context.Background(), ports.ServerConfig{Hostname: "da.example.com"}, "alice")
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, strings.ToLower(ae.Message), "unsupported")
}

func TestSSOURLPattern(t *testing.T) {
	cfg := testConfig()
	cfg.LoginKeyURLPattern = "{scheme}://{host}:{port}/CMD_LOGIN?username={username}"
	c := New(cfg, nil, nil, nil)

	u, err := c.SSOURL(context.Background(), ports.ServerConfig{
		Hostname: "da.example.com", Port: 2222, UseSSL: true,
	}, "alice bob")
	require.NoError(t, err)
	assert.Equal(t, "https://da.example.com:2222/CMD_LOGIN?username=alice+bob", u)
}

func TestSSOURLPatternDefaultsPortAndScheme(t *testing.T) {
	cfg := testConfig()
	cfg.LoginKeyURLPattern = "{scheme}://{host}:{port}/sso/{username}"
	c := New(cfg, nil, nil, nil)

	u, err := c.SSOURL(context.Background(), ports.ServerConfig{Hostname: "da.example.com"}, "alice")
	require.NoError(t, err)
	assert.Equal(t, "http://da.example.com:2222/sso/alice", u)
}

// --- auth fallback ------------------------------------------------------------------

func TestBasicAuthFallsBackToAPIToken(t *testing.T) {
	var req http.Request
	ts := httptest.NewServer(legacyHandler(t, 200, "error=0&text=Success&details=ok", nil, &req))
	defer ts.Close()

	sc := serverConfig(t, ts)
	sc.Password = ""
	sc.APIToken = "login-key-123"

	c, _ := newTestClient(t, testConfig())
	require.NoError(t, c.Unsuspend(context.Background(), sc, "alice"))
	_, pass, ok := req.BasicAuth()
	require.True(t, ok)
	assert.Equal(t, "login-key-123", pass)
}

// --- TLS ---------------------------------------------------------------------

func TestTLSVerifiesByDefault(t *testing.T) {
	ts := httptest.NewTLSServer(legacyHandler(t, 200, "error=0&text=Success&details=ok", nil, nil))
	defer ts.Close()
	s := serverConfig(t, ts)
	s.UseSSL = true

	c := New(Config{}, nil, nil, nil)
	err := c.Unsuspend(context.Background(), s, "alice")
	require.Error(t, err, "default config must reject self-signed certificates")
}

func TestTLSSkipVerifyOptIn(t *testing.T) {
	ts := httptest.NewTLSServer(legacyHandler(t, 200, "error=0&text=Success&details=ok", nil, nil))
	defer ts.Close()
	s := serverConfig(t, ts)
	s.UseSSL = true

	c := New(Config{TLSInsecureSkipVerify: true}, nil, nil, nil)
	err := c.Unsuspend(context.Background(), s, "alice")
	require.NoError(t, err, "opt-in skip-verify must accept self-signed certificates")
}
