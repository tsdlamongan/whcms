package cpanel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// test harness

type capturedReq struct {
	Method string
	Path   string
	Query  url.Values
	Form   url.Values
	Auth   string
}

// recorder wraps a handler, capturing every request thread-safely.
type recorder struct {
	mu   sync.Mutex
	reqs []capturedReq
	next http.HandlerFunc
}

func (rec *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	rec.mu.Lock()
	rec.reqs = append(rec.reqs, capturedReq{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.Query(),
		Form:   r.PostForm,
		Auth:   r.Header.Get("Authorization"),
	})
	rec.mu.Unlock()
	rec.next(w, r)
}

func (rec *recorder) count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.reqs)
}

func (rec *recorder) req(i int) capturedReq {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.reqs[i]
}

func whmBody(result int, reason, command string, data any) string {
	if data == nil {
		data = map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"version": 1, "reason": reason, "result": result, "command": command},
		"data":     data,
	})
	return string(b)
}

func whmOK(w http.ResponseWriter, command string, data any) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(whmBody(1, "OK", command, data)))
}

func whmFail(w http.ResponseWriter, command, reason string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(whmBody(0, reason, command, nil)))
}

func serverConfigFor(t *testing.T, rawURL string) ports.ServerConfig {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return ports.ServerConfig{
		ID: 1, Name: "test-whm", Module: domain.ModuleCpanel,
		Hostname: u.Hostname(), Port: port,
		Username: "root", APIToken: "sometoken",
		UseSSL: u.Scheme == "https",
	}
}

// newHarness spins up an httptest server around handler and returns a Client
// pointed at it plus the capture/log fixtures.
func newHarness(t *testing.T, cfg Config, handler http.HandlerFunc) (*Client, ports.ServerConfig, *recorder, *mocks.MockIntegrationLogger) {
	t.Helper()
	rec := &recorder{next: handler}
	ts := httptest.NewServer(rec)
	t.Cleanup(ts.Close)
	log := &mocks.MockIntegrationLogger{}
	clk := &mocks.MockClock{FixedTime: time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)}
	c := New(cfg, nil, log, clk)
	return c, serverConfigFor(t, ts.URL), rec, log
}

func asAppErr(t *testing.T, err error) *apperr.Error {
	t.Helper()
	require.Error(t, err)
	ae := apperr.From(err)
	require.NotNil(t, ae)
	return ae
}

var testAccount = ports.CreateAccountParams{
	Username: "alice",
	Domain:   "alice.example.com",
	Password: "Str0ngPass!23",
	Package:  "gold",
	Email:    "alice@example.com",
}

// basics

func TestName(t *testing.T) {
	c := New(Config{}, nil, nil, nil)
	assert.Equal(t, "cpanel", c.Name())
}

func TestBaseURL(t *testing.T) {
	cases := []struct {
		name string
		in   ports.ServerConfig
		want string
	}{
		{"ssl default port", ports.ServerConfig{Hostname: "whm1.example.com", UseSSL: true}, "https://whm1.example.com:2087"},
		{"plain default port", ports.ServerConfig{Hostname: "whm1.example.com"}, "http://whm1.example.com:2086"},
		{"explicit port", ports.ServerConfig{Hostname: "localhost", Port: 9090}, "http://localhost:9090"},
		{"explicit ssl port", ports.ServerConfig{Hostname: "localhost", Port: 2083, UseSSL: true}, "https://localhost:2083"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, baseURL(tc.in))
		})
	}
}

// Create

func TestCreate_Success(t *testing.T) {
	c, s, rec, log := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "createacct", map[string]any{
			"username": "alice", "domain": "alice.example.com", "package": "gold", "ip": "10.0.0.5",
		})
	})

	res, err := c.Create(context.Background(), s, testAccount)
	require.NoError(t, err)
	assert.Equal(t, "alice", res.Username)
	assert.Equal(t, "alice.example.com", res.Domain)
	assert.Equal(t, "10.0.0.5", res.IP)
	assert.Equal(t, "gold", res.Meta["package"])

	require.Equal(t, 1, rec.count())
	req := rec.req(0)
	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "/json-api/createacct", req.Path)
	assert.Equal(t, "1", req.Query.Get("api.version"))
	assert.Equal(t, "whm root:sometoken", req.Auth)
	assert.Equal(t, "alice", req.Form.Get("username"))
	assert.Equal(t, "alice.example.com", req.Form.Get("domain"))
	assert.Equal(t, testAccount.Password, req.Form.Get("password"))
	assert.Equal(t, "gold", req.Form.Get("plan"))
	assert.Equal(t, "alice@example.com", req.Form.Get("contactemail"))

	// Integration log: success + password redacted in request and response.
	require.Len(t, log.Calls, 1)
	call := log.Calls[0]
	assert.Equal(t, "cpanel", call.Provider)
	assert.Equal(t, "/json-api/createacct", call.Endpoint)
	assert.Equal(t, http.MethodPost, call.Method)
	assert.Equal(t, http.StatusOK, call.StatusCode)
	assert.True(t, call.Success)
	reqLog, ok := call.Request.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "[REDACTED]", reqLog["password"])
	assert.Equal(t, "alice", reqLog["username"])
}

func TestCreate_FallbackToInputsWhenDataSparse(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "createacct", map[string]any{})
	})
	res, err := c.Create(context.Background(), s, testAccount)
	require.NoError(t, err)
	assert.Equal(t, "alice", res.Username)
	assert.Equal(t, "alice.example.com", res.Domain)
	assert.Empty(t, res.IP)
}

func TestCreate_SendsIPWhenGiven(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "createacct", nil)
	})
	a := testAccount
	a.IP = "10.1.2.3"
	_, err := c.Create(context.Background(), s, a)
	require.NoError(t, err)
	assert.Equal(t, "10.1.2.3", rec.req(0).Form.Get("ip"))
}

func TestCreate_APIError_AccountExists(t *testing.T) {
	c, s, _, log := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "createacct", "account exists")
	})
	_, err := c.Create(context.Background(), s, testAccount)
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeConflict, ae.Code)
	require.Len(t, log.Calls, 1)
	assert.False(t, log.Calls[0].Success)
	assert.Equal(t, http.StatusOK, log.Calls[0].StatusCode) // HTTP 200 but API-level failure
}

func TestCreate_APIError_ReservedUsername(t *testing.T) {
	c, s, _, log := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "createacct", `(XID t95cwn) "testhost" is a reserved username on this system.`)
	})
	_, err := c.Create(context.Background(), s, testAccount)
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeConflict, ae.Code, "must map to CONFLICT so ProvisionCreate retries under a new username instead of forever")
	require.Len(t, log.Calls, 1)
	assert.False(t, log.Calls[0].Success)
}

func TestCreate_APIError_Generic(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "createacct", "forced failure")
	})
	_, err := c.Create(context.Background(), s, testAccount)
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Error(), "forced failure")
}

func TestCreate_APIError_EmptyReason(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "createacct", "")
	})
	_, err := c.Create(context.Background(), s, testAccount)
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Error(), "unknown WHM API error")
}

func TestCreate_BadToken403(t *testing.T) {
	c, s, _, log := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(whmBody(0, "Access denied: invalid or missing WHM token", "createacct", nil)))
	})
	_, err := c.Create(context.Background(), s, testAccount)
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Error(), "http 403")
	assert.Contains(t, ae.Error(), "Access denied")
	require.Len(t, log.Calls, 1)
	assert.Equal(t, http.StatusForbidden, log.Calls[0].StatusCode)
	assert.False(t, log.Calls[0].Success)
}

func TestCreate_NetworkError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	s := serverConfigFor(t, ts.URL)
	ts.Close() // nothing listening anymore

	log := &mocks.MockIntegrationLogger{}
	c := New(Config{}, nil, log, &mocks.MockClock{FixedTime: time.Unix(1_750_000_000, 0)})
	_, err := c.Create(context.Background(), s, testAccount)
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	require.Len(t, log.Calls, 1) // POSTs are never retried
	assert.Equal(t, 0, log.Calls[0].StatusCode)
	assert.NotEmpty(t, log.Calls[0].Error)
}

func TestCreate_ServerError500_NoRetryOnPOST(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.Create(context.Background(), s, testAccount)
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Equal(t, 1, rec.count())
}

func TestCreate_MalformedJSON(t *testing.T) {
	c, s, _, log := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	})
	_, err := c.Create(context.Background(), s, testAccount)
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Error(), "malformed response")
	require.Len(t, log.Calls, 1)
	assert.Equal(t, "<html>not json</html>", log.Calls[0].Response)
}

func TestCreate_EmptyEnvelope(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	_, err := c.Create(context.Background(), s, testAccount)
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Error(), "malformed response")
}

func TestCreate_Validation(t *testing.T) {
	hit := false
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) { hit = true })

	cases := []struct {
		name   string
		mutate func(*ports.CreateAccountParams)
		field  string
	}{
		{"empty username", func(a *ports.CreateAccountParams) { a.Username = "" }, "username"},
		{"uppercase username", func(a *ports.CreateAccountParams) { a.Username = "Alice" }, "username"},
		{"leading digit", func(a *ports.CreateAccountParams) { a.Username = "9alice" }, "username"},
		{"too long username", func(a *ports.CreateAccountParams) { a.Username = "abcdefghijklmnopq" }, "username"},
		{"empty domain", func(a *ports.CreateAccountParams) { a.Domain = "" }, "domain"},
		{"short password", func(a *ports.CreateAccountParams) { a.Password = "short" }, "password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := testAccount
			tc.mutate(&a)
			_, err := c.Create(context.Background(), s, a)
			ae := asAppErr(t, err)
			assert.Equal(t, apperr.CodeValidation, ae.Code)
			require.NotEmpty(t, ae.Details)
			assert.Equal(t, tc.field, ae.Details[0].Field)
		})
	}
	assert.False(t, hit, "validation failures must not reach the server")
}

// mutating operations (suspend/unsuspend/terminate/changepackage/passwd)

func TestMutatingOps_Success(t *testing.T) {
	cases := []struct {
		name       string
		fn         string
		invoke     func(c *Client, s ports.ServerConfig) error
		wantParams map[string]string
	}{
		{
			"suspend", "suspendacct",
			func(c *Client, s ports.ServerConfig) error {
				return c.Suspend(context.Background(), s, "alice", "Overdue on payment")
			},
			map[string]string{"user": "alice", "reason": "Overdue on payment"},
		},
		{
			"suspend without reason", "suspendacct",
			func(c *Client, s ports.ServerConfig) error { return c.Suspend(context.Background(), s, "alice", "") },
			map[string]string{"user": "alice"},
		},
		{
			"unsuspend", "unsuspendacct",
			func(c *Client, s ports.ServerConfig) error { return c.Unsuspend(context.Background(), s, "alice") },
			map[string]string{"user": "alice"},
		},
		{
			"terminate", "removeacct",
			func(c *Client, s ports.ServerConfig) error { return c.Terminate(context.Background(), s, "alice") },
			map[string]string{"user": "alice"},
		},
		{
			"change package", "changepackage",
			func(c *Client, s ports.ServerConfig) error {
				return c.ChangePackage(context.Background(), s, "alice", "platinum")
			},
			map[string]string{"user": "alice", "pkg": "platinum"},
		},
		{
			"change password", "passwd",
			func(c *Client, s ports.ServerConfig) error {
				return c.ChangePassword(context.Background(), s, "alice", "N3wPassw0rd!")
			},
			map[string]string{"user": "alice", "password": "N3wPassw0rd!"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, s, rec, log := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
				whmOK(w, tc.fn, nil)
			})
			require.NoError(t, tc.invoke(c, s))
			require.Equal(t, 1, rec.count())
			req := rec.req(0)
			assert.Equal(t, http.MethodPost, req.Method)
			assert.Equal(t, "/json-api/"+tc.fn, req.Path)
			assert.Equal(t, "1", req.Query.Get("api.version"))
			for k, v := range tc.wantParams {
				assert.Equal(t, v, req.Form.Get(k), "param %s", k)
			}
			require.Len(t, log.Calls, 1)
			assert.True(t, log.Calls[0].Success)
		})
	}
}

func TestMutatingOps_AccountDoesNotExist(t *testing.T) {
	// NOTE: Terminate is deliberately NOT in this table - unlike every other
	// mutating op, a missing account is a SUCCESS for Terminate (idempotent -
	// see TestTerminate_NotFoundIsSuccess), since the desired end state
	// ("account gone") was already achieved, most likely by an earlier
	// attempt of the very same retried job.
	ops := []struct {
		name   string
		invoke func(c *Client, s ports.ServerConfig) error
	}{
		{"suspend", func(c *Client, s ports.ServerConfig) error { return c.Suspend(context.Background(), s, "ghost", "r") }},
		{"unsuspend", func(c *Client, s ports.ServerConfig) error { return c.Unsuspend(context.Background(), s, "ghost") }},
		{"change package", func(c *Client, s ports.ServerConfig) error {
			return c.ChangePackage(context.Background(), s, "ghost", "gold")
		}},
		{"change password", func(c *Client, s ports.ServerConfig) error {
			return c.ChangePassword(context.Background(), s, "ghost", "N3wPassw0rd!")
		}},
	}
	for _, tc := range ops {
		t.Run(tc.name, func(t *testing.T) {
			c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
				whmFail(w, "x", "account does not exist")
			})
			ae := asAppErr(t, tc.invoke(c, s))
			assert.Equal(t, apperr.CodeNotFound, ae.Code)
		})
	}
}

// TestTerminate_NotFoundIsSuccess is the regression test for a real incident:
// removeacct on an already-gone account used to propagate NOT_FOUND like
// every other mutating op, so ProvisionTerminate returned an error AFTER the
// account had genuinely already been deleted (by an earlier attempt of the
// same asynq-retried job) - the job then retried forever, calling removeacct
// again on an account that no longer existed, failing at the exact same step
// every time. Mirrors DeletePackage's existing idempotent-NotFound handling.
func TestTerminate_NotFoundIsSuccess(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "removeacct", "account does not exist")
	})
	assert.NoError(t, c.Terminate(context.Background(), s, "ghost"))
}

func TestTerminate_ErrorPropagates(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "removeacct", "permission denied")
	})
	err := c.Terminate(context.Background(), s, "user1")
	assert.Equal(t, apperr.CodeExternal, asAppErr(t, err).Code)
}

func TestMutatingOps_Validation(t *testing.T) {
	c := New(Config{}, nil, nil, nil)
	s := ports.ServerConfig{Hostname: "unused"}
	ctx := context.Background()

	assert.Equal(t, apperr.CodeValidation, asAppErr(t, c.Suspend(ctx, s, "", "r")).Code)
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, c.Unsuspend(ctx, s, "")).Code)
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, c.Terminate(ctx, s, "")).Code)
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, c.ChangePackage(ctx, s, "", "gold")).Code)
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, c.ChangePackage(ctx, s, "alice", "")).Code)
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, c.ChangePassword(ctx, s, "", "N3wPassw0rd!")).Code)
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, c.ChangePassword(ctx, s, "alice", "short")).Code)

	_, err := c.AccountInfo(ctx, s, "")
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, err).Code)
	_, err = c.SSOURL(ctx, s, "")
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, err).Code)
}

// AccountInfo

func TestAccountInfo_Success(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "accountsummary", map[string]any{
			"acct": []map[string]any{{
				"user": "alice", "domain": "alice.example.com", "plan": "gold",
				"email": "alice@example.com", "suspended": 1, "suspendreason": "abuse",
				"diskused": "512M", "disklimit": "unlimited", "ip": "127.0.0.1",
			}},
		})
	})

	info, err := c.AccountInfo(context.Background(), s, "alice")
	require.NoError(t, err)
	assert.Equal(t, "alice", info.Username)
	assert.Equal(t, "alice.example.com", info.Domain)
	assert.Equal(t, "gold", info.Package)
	assert.True(t, info.Suspended)
	assert.Equal(t, int64(512), info.DiskUsed)
	assert.Equal(t, int64(0), info.DiskLimit)
	assert.Equal(t, "abuse", info.Meta["suspendreason"])

	require.Equal(t, 1, rec.count())
	req := rec.req(0)
	assert.Equal(t, http.MethodGet, req.Method)
	assert.Equal(t, "/json-api/accountsummary", req.Path)
	assert.Equal(t, "alice", req.Query.Get("user"))
	assert.Equal(t, "1", req.Query.Get("api.version"))
}

func TestAccountInfo_NotSuspended_UsernameFallback(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "accountsummary", map[string]any{
			"acct": []map[string]any{{"suspended": 0, "diskused": 1024.0}},
		})
	})
	info, err := c.AccountInfo(context.Background(), s, "bob")
	require.NoError(t, err)
	assert.Equal(t, "bob", info.Username)
	assert.False(t, info.Suspended)
	assert.Equal(t, int64(1024), info.DiskUsed)
}

func TestAccountInfo_UnknownAccount(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "accountsummary", "account does not exist")
	})
	_, err := c.AccountInfo(context.Background(), s, "ghost")
	assert.Equal(t, apperr.CodeNotFound, asAppErr(t, err).Code)
}

func TestAccountInfo_MissingAcctData(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "accountsummary", map[string]any{"acct": []map[string]any{}})
	})
	_, err := c.AccountInfo(context.Background(), s, "alice")
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Error(), "missing acct data")
}

func TestAccountInfo_RetriesOn500ThenSucceeds(t *testing.T) {
	var hits int
	var mu sync.Mutex
	c, s, rec, log := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		whmOK(w, "accountsummary", map[string]any{"acct": []map[string]any{{"user": "alice"}}})
	})

	info, err := c.AccountInfo(context.Background(), s, "alice")
	require.NoError(t, err)
	assert.Equal(t, "alice", info.Username)
	assert.Equal(t, 2, rec.count())
	require.Len(t, log.Calls, 2)
	assert.False(t, log.Calls[0].Success)
	assert.True(t, log.Calls[1].Success)
}

func TestAccountInfo_RetriesExhausted(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{RetryDelay: time.Millisecond}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	_, err := c.AccountInfo(context.Background(), s, "alice")
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Equal(t, 1+getRetries, rec.count()) // initial attempt + 2 retries
}

func TestAccountInfo_NoRetryOnAPIError(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "accountsummary", "some transient reason")
	})
	_, err := c.AccountInfo(context.Background(), s, "alice")
	assert.Equal(t, apperr.CodeExternal, asAppErr(t, err).Code)
	assert.Equal(t, 1, rec.count(), "API-level failures are not retried")
}

// SSOURL

func TestTestConnection_Success(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json-api/version":
			whmOK(w, "version", map[string]any{"version": "11.134.0.45"})
		case "/json-api/gethostname":
			whmOK(w, "gethostname", map[string]any{"hostname": "server.wew.my.id"})
		case "/json-api/get_nameserver_config":
			whmOK(w, "get_nameserver_config", map[string]any{
				"nameservers": []string{"ns1.wew.my.id", "ns2.wew.my.id"},
			})
		default:
			whmFail(w, "unknown", "Unknown app requested")
		}
	})

	info, err := c.TestConnection(context.Background(), s)
	require.NoError(t, err)
	assert.Equal(t, "11.134.0.45", info.Version)
	assert.Equal(t, "server.wew.my.id", info.Hostname)
	assert.Equal(t, []string{"ns1.wew.my.id", "ns2.wew.my.id"}, info.Nameservers)

	// The authoritative probe is `version`, called with the WHM auth header.
	req := rec.req(0)
	assert.Equal(t, http.MethodGet, req.Method)
	assert.Equal(t, "/json-api/version", req.Path)
	assert.Equal(t, "whm root:sometoken", req.Auth)
}

func TestTestConnection_VersionFails(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json-api/version" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(whmBody(0, "Access denied: invalid token", "version", nil)))
			return
		}
		whmOK(w, "gethostname", map[string]any{"hostname": "x"})
	})
	_, err := c.TestConnection(context.Background(), s)
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Error(), "403")
}

func TestTestConnection_MetadataBestEffort(t *testing.T) {
	// version succeeds but every metadata read fails (permission denied / 500 /
	// null nv keys); the connection still counts as good with no auto-fill -
	// this is the "reseller inherits nameservers from root" case.
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json-api/version":
			whmOK(w, "version", map[string]any{"version": "11.134.0.45"})
		case "/json-api/gethostname":
			whmFail(w, "gethostname", "Permission denied")
		case "/json-api/get_nameserver_config":
			whmOK(w, "get_nameserver_config", map[string]any{"nameservers": []string{}})
		case "/json-api/nvget":
			// WHM returns [null] for an unset Basic-Setup nameserver key.
			whmOK(w, "nvget", map[string]any{
				"nvdatum": map[string]any{"key": r.URL.Query().Get("key"), "value": []any{nil}},
			})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	info, err := c.TestConnection(context.Background(), s)
	require.NoError(t, err)
	assert.Equal(t, "11.134.0.45", info.Version)
	assert.Empty(t, info.Hostname)
	assert.Empty(t, info.Nameservers)
}

func TestTestConnection_NameserversFromNVFallback(t *testing.T) {
	// get_nameserver_config is empty (reseller/inherited) but the Basic-Setup nv
	// keys are populated (a root/privileged token) -> nameservers auto-fill.
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json-api/version":
			whmOK(w, "version", map[string]any{"version": "11.134.0.45"})
		case "/json-api/gethostname":
			whmOK(w, "gethostname", map[string]any{"hostname": "server.wew.my.id"})
		case "/json-api/get_nameserver_config":
			whmOK(w, "get_nameserver_config", map[string]any{"nameservers": []string{}})
		case "/json-api/nvget":
			vals := map[string]string{"nameserver": "ns1.wew.my.id", "nameserver2": "ns2.wew.my.id"}
			key := r.URL.Query().Get("key")
			whmOK(w, "nvget", map[string]any{
				"nvdatum": map[string]any{"key": key, "value": []any{vals[key]}},
			})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	info, err := c.TestConnection(context.Background(), s)
	require.NoError(t, err)
	assert.Equal(t, []string{"ns1.wew.my.id", "ns2.wew.my.id"}, info.Nameservers)

	// nvget is only consulted after get_nameserver_config comes back empty.
	var sawNSConfig, sawNVGet bool
	for i := 0; i < rec.count(); i++ {
		switch rec.req(i).Path {
		case "/json-api/get_nameserver_config":
			sawNSConfig = true
		case "/json-api/nvget":
			sawNVGet = true
		}
	}
	assert.True(t, sawNSConfig && sawNVGet)
}

func TestParseNameservers(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"string form", `{"nameservers":["ns1.x.id","ns2.x.id"]}`, []string{"ns1.x.id", "ns2.x.id"}},
		{"object form", `{"nameservers":[{"nameserver":"ns1.x.id"},{"name":"ns2.x.id"}]}`, []string{"ns1.x.id", "ns2.x.id"}},
		{"mixed + blanks", `{"nameservers":["ns1.x.id"," ",{"host":"ns2.x.id"},{"foo":"bar"}]}`, []string{"ns1.x.id", "ns2.x.id"}},
		{"empty list", `{"nameservers":[]}`, nil},
		{"no key", `{"other":1}`, nil},
		{"malformed", `not json`, nil},
		{"empty", ``, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseNameservers(json.RawMessage(tc.raw)))
		})
	}
}

func TestAuthHeader(t *testing.T) {
	// API token -> token scheme.
	assert.Equal(t, "whm root:tok123",
		authHeader(ports.ServerConfig{Username: "root", APIToken: "tok123"}))
	// Password only -> HTTP Basic auth (the WHMCS-compatible fallback).
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("root:s3cret")),
		authHeader(ports.ServerConfig{Username: "root", Password: "s3cret"}))
	// Token wins when both are set.
	assert.Equal(t, "whm root:tok123",
		authHeader(ports.ServerConfig{Username: "root", APIToken: "tok123", Password: "s3cret"}))
	// No credential -> no header.
	assert.Equal(t, "", authHeader(ports.ServerConfig{Username: "root"}))
}

func TestTestConnection_PasswordBasicAuth(t *testing.T) {
	// Username+password (no API token) must authenticate via HTTP Basic - the
	// regression where it was sent as a bogus `whm user:password` token and 403'd.
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json-api/version" {
			whmOK(w, "version", map[string]any{"version": "11.134.0.45"})
			return
		}
		whmOK(w, r.URL.Path, map[string]any{})
	})
	s.APIToken = ""
	s.Password = "s3cret-pw"

	info, err := c.TestConnection(context.Background(), s)
	require.NoError(t, err)
	assert.Equal(t, "11.134.0.45", info.Version)

	auth := rec.req(0).Auth
	require.True(t, strings.HasPrefix(auth, "Basic "), "want Basic auth, got %q", auth)
	raw, derr := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
	require.NoError(t, derr)
	assert.Equal(t, "root:s3cret-pw", string(raw))
}

func TestNvgetString(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"value set", `{"nvdatum":{"key":"nameserver","value":["ns1.x.id"]}}`, "ns1.x.id"},
		{"null value", `{"nvdatum":{"key":"nameserver","value":[null]}}`, ""},
		{"blank trimmed", `{"nvdatum":{"value":["  "]}}`, ""},
		{"non-string skipped then string", `{"nvdatum":{"value":[123,"ns2.x.id"]}}`, "ns2.x.id"},
		{"empty array", `{"nvdatum":{"value":[]}}`, ""},
		{"malformed", `nope`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, nvgetString(json.RawMessage(tc.raw)))
		})
	}
}

func TestSSOURL_Success(t *testing.T) {
	c, s, rec, log := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "create_user_session", map[string]any{
			"url": "http://localhost:9090/cpanel-sso/alice", "session": "mock-session-alice",
		})
	})
	u, err := c.SSOURL(context.Background(), s, "alice")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:9090/cpanel-sso/alice", u)

	req := rec.req(0)
	assert.Equal(t, http.MethodGet, req.Method)
	assert.Equal(t, "/json-api/create_user_session", req.Path)
	assert.Equal(t, "alice", req.Query.Get("user"))
	assert.Equal(t, "cpaneld", req.Query.Get("service"))

	// Regression: the one-time cPanel login URL/session token is a live
	// bearer credential and must never be persisted in cleartext in
	// integration_logs (see CONTRACTS §3).
	require.Len(t, log.Calls, 1)
	respLog, ok := log.Calls[0].Response.(map[string]any)
	require.True(t, ok)
	data, ok := respLog["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, redactedPlaceholder, data["url"])
	assert.Equal(t, redactedPlaceholder, data["session"])
}

func TestSSOURL_MissingURL(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "create_user_session", map[string]any{})
	})
	_, err := c.SSOURL(context.Background(), s, "alice")
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Error(), "missing url")
}

func TestSSOURL_UnknownAccount(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "create_user_session", "account does not exist")
	})
	_, err := c.SSOURL(context.Background(), s, "ghost")
	assert.Equal(t, apperr.CodeNotFound, asAppErr(t, err).Code)
}

// TLS skip-verify

func TestTLS_SkipVerifySelfSigned(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "unsuspendacct", nil)
	}))
	t.Cleanup(ts.Close)
	s := serverConfigFor(t, ts.URL)
	require.True(t, s.UseSSL)

	c := New(Config{TLSInsecureSkipVerify: true}, nil, &mocks.MockIntegrationLogger{}, nil)
	err := c.Unsuspend(context.Background(), s, "alice")
	require.NoError(t, err, "self-signed WHM certificates must be accepted when skip-verify is opted in")
}

func TestTLS_VerifiesByDefault(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "unsuspendacct", nil)
	}))
	t.Cleanup(ts.Close)
	s := serverConfigFor(t, ts.URL)
	require.True(t, s.UseSSL)

	c := New(Config{}, nil, &mocks.MockIntegrationLogger{}, nil)
	err := c.Unsuspend(context.Background(), s, "alice")
	require.Error(t, err, "default config must reject self-signed certificates")
}

// circuit breaker

func TestBreaker_OpensThenCoolsDown(t *testing.T) {
	base := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	now := base
	clk := &mocks.MockClock{NowFn: func() time.Time { return now }}

	rec := &recorder{next: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}}
	ts := httptest.NewServer(rec)
	t.Cleanup(ts.Close)
	s := serverConfigFor(t, ts.URL)

	c := New(Config{BreakerThreshold: 2, BreakerCooldown: time.Minute}, nil, &mocks.MockIntegrationLogger{}, clk)
	ctx := context.Background()

	// Two consecutive transport failures open the circuit.
	assert.Equal(t, apperr.CodeExternal, asAppErr(t, c.Suspend(ctx, s, "alice", "r")).Code)
	assert.Equal(t, apperr.CodeExternal, asAppErr(t, c.Suspend(ctx, s, "alice", "r")).Code)
	assert.Equal(t, 2, rec.count())

	// Third call fast-fails without touching the server.
	err := c.Suspend(ctx, s, "alice", "r")
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Error(), "circuit open")
	assert.Equal(t, 2, rec.count())

	// After the cooldown the client tries again (half-open).
	now = base.Add(61 * time.Second)
	assert.Equal(t, apperr.CodeExternal, asAppErr(t, c.Suspend(ctx, s, "alice", "r")).Code)
	assert.Equal(t, 3, rec.count())

	// That failure re-opens the circuit immediately.
	ae = asAppErr(t, c.Suspend(ctx, s, "alice", "r"))
	assert.Contains(t, ae.Error(), "circuit open")
	assert.Equal(t, 3, rec.count())
}

func TestBreaker_ResetOnSuccessAndAPIErrors(t *testing.T) {
	var hits int
	var mu sync.Mutex
	script := []int{500, 200, 500, 200} // alternating transport failure / success
	c, s, rec, _ := newHarness(t, Config{BreakerThreshold: 2, BreakerCooldown: time.Minute},
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			code := script[hits%len(script)]
			hits++
			mu.Unlock()
			if code != 200 {
				w.WriteHeader(code)
				return
			}
			whmOK(w, "suspendacct", nil)
		})
	ctx := context.Background()

	// fail, success, fail, success - consecutive counter never reaches 2.
	require.Error(t, c.Suspend(ctx, s, "alice", "r"))
	require.NoError(t, c.Suspend(ctx, s, "alice", "r"))
	require.Error(t, c.Suspend(ctx, s, "alice", "r"))
	require.NoError(t, c.Suspend(ctx, s, "alice", "r"))
	assert.Equal(t, 4, rec.count(), "breaker must not have opened")
}

func TestBreaker_APIErrorDoesNotTrip(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{BreakerThreshold: 2, BreakerCooldown: time.Minute},
		func(w http.ResponseWriter, r *http.Request) {
			whmFail(w, "suspendacct", "account does not exist")
		})
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		assert.Equal(t, apperr.CodeNotFound, asAppErr(t, c.Suspend(ctx, s, "ghost", "r")).Code)
	}
	assert.Equal(t, 4, rec.count(), "API-level errors must not open the breaker")
}

// unit helpers

func TestWhmInt_Unmarshal(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{`1`, 1, false},
		{`0`, 0, false},
		{`"1"`, 1, false},
		{`"0"`, 0, false},
		{`1.0`, 1, false},
		{`null`, 0, false},
		{`""`, 0, false},
		{`"abc"`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			var v whmInt
			err := json.Unmarshal([]byte(tc.in), &v)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, int(v))
		})
	}
}

func TestTruthy(t *testing.T) {
	assert.True(t, truthy(true))
	assert.True(t, truthy(float64(1)))
	assert.True(t, truthy("1"))
	assert.True(t, truthy("true"))
	assert.True(t, truthy("YES"))
	assert.True(t, truthy(json.Number("1")))
	assert.False(t, truthy(false))
	assert.False(t, truthy(float64(0)))
	assert.False(t, truthy("0"))
	assert.False(t, truthy(""))
	assert.False(t, truthy(nil))
	assert.False(t, truthy(json.Number("0")))
	assert.False(t, truthy([]any{}))
}

func TestParseMB(t *testing.T) {
	assert.Equal(t, int64(512), parseMB("512M"))
	assert.Equal(t, int64(512), parseMB(" 512M "))
	assert.Equal(t, int64(100), parseMB("100"))
	assert.Equal(t, int64(0), parseMB("unlimited"))
	assert.Equal(t, int64(0), parseMB(""))
	assert.Equal(t, int64(0), parseMB("garbage"))
	assert.Equal(t, int64(42), parseMB(float64(42)))
	assert.Equal(t, int64(7), parseMB(json.Number("7")))
	assert.Equal(t, int64(0), parseMB(nil))
	assert.Equal(t, int64(0), parseMB([]any{}))
}

func TestRedaction(t *testing.T) {
	in := map[string]any{
		"password":  "supersecret",
		"passwd":    "supersecret",
		"api_token": "tok123",
		"apiKey":    "key123",
		"signature": "sig",
		"url":       "https://host:2087/login/?session=abc123SECRETTOKEN",
		"session":   "abc123SECRETTOKEN",
		"nested": map[string]any{
			"Authorization": "whm root:tok",
			"domain":        "ok.example.com",
		},
		"list":   []any{map[string]any{"password": "x"}, "plain"},
		"domain": "example.com",
	}
	out, ok := redactAny(in).(map[string]any)
	require.True(t, ok)
	assert.Equal(t, redactedPlaceholder, out["password"])
	assert.Equal(t, redactedPlaceholder, out["passwd"])
	assert.Equal(t, redactedPlaceholder, out["api_token"])
	// Regression: cPanel SSO one-time login URL/session token
	// (create_user_session) is a live bearer credential and must be
	// redacted, not just password/token/secret-style keys.
	assert.Equal(t, redactedPlaceholder, out["url"])
	assert.Equal(t, redactedPlaceholder, out["session"])
	assert.Equal(t, redactedPlaceholder, out["apiKey"])
	assert.Equal(t, redactedPlaceholder, out["signature"])
	assert.Equal(t, "example.com", out["domain"])
	nested := out["nested"].(map[string]any)
	assert.Equal(t, redactedPlaceholder, nested["Authorization"])
	assert.Equal(t, "ok.example.com", nested["domain"])
	list := out["list"].([]any)
	assert.Equal(t, redactedPlaceholder, list[0].(map[string]any)["password"])
	assert.Equal(t, "plain", list[1])

	// url.Values redaction
	params := url.Values{"password": {"x"}, "user": {"alice"}, "ns": {"a", "b"}}
	flat := redactParams(params)
	assert.Equal(t, redactedPlaceholder, flat["password"])
	assert.Equal(t, "alice", flat["user"])
	assert.Equal(t, "a,b", flat["ns"])
}

func TestMetaFromRaw(t *testing.T) {
	assert.Nil(t, metaFromRaw(nil))
	assert.Nil(t, metaFromRaw(json.RawMessage(`[1,2]`)))
	m := metaFromRaw(json.RawMessage(`{"password":"x","ip":"1.2.3.4"}`))
	require.NotNil(t, m)
	assert.Equal(t, redactedPlaceholder, m["password"])
	assert.Equal(t, "1.2.3.4", m["ip"])
}

func TestRawToAny(t *testing.T) {
	assert.Equal(t, map[string]any{"a": float64(1)}, rawToAny([]byte(`{"a":1}`)))
	long := strings.Repeat("x", 600)
	got, ok := rawToAny([]byte(long)).(string)
	require.True(t, ok)
	assert.Len(t, got, 512, "non-JSON bodies are truncated for logging")
	assert.Equal(t, "plain", rawToAny([]byte(`"plain"`)))
}

func TestStrField(t *testing.T) {
	m := map[string]any{"a": "", "b": "val", "c": 7}
	assert.Equal(t, "val", strField(m, "a", "b"))
	assert.Equal(t, "", strField(m, "a", "c", "missing"))
}

func TestNilDependencyDefaults(t *testing.T) {
	c := New(Config{}, nil, nil, nil)
	assert.Equal(t, defaultTimeout, c.cfg.Timeout)
	assert.Equal(t, defaultBreakerThreshold, c.cfg.BreakerThreshold)
	assert.Equal(t, defaultBreakerCooldown, c.cfg.BreakerCooldown)
	assert.NotNil(t, c.plain)
	assert.NotNil(t, c.insecure)
	// nop logger + system clock must not panic
	c.log.Log(context.Background(), ports.IntegrationCall{})
	assert.False(t, c.clock.Now().IsZero())
}

func TestInsecureClient_PreservesCustomTransport(t *testing.T) {
	base := &http.Client{Transport: &http.Transport{MaxIdleConns: 7}, Timeout: 5 * time.Second}
	ic := insecureClient(base)
	tr, ok := ic.Transport.(*http.Transport)
	require.True(t, ok)
	assert.True(t, tr.TLSClientConfig.InsecureSkipVerify)
	assert.Equal(t, 7, tr.MaxIdleConns)
	assert.Equal(t, 5*time.Second, ic.Timeout)

	// Non-*http.Transport RoundTripper falls back to a fresh transport.
	ic2 := insecureClient(&http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unused")
	})})
	tr2, ok := ic2.Transport.(*http.Transport)
	require.True(t, ok)
	assert.True(t, tr2.TLSClientConfig.InsecureSkipVerify)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRetry_ContextCancelledDuringDelay(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{RetryDelay: 5 * time.Second}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := c.AccountInfo(ctx, s, "alice")
	ae := asAppErr(t, err)
	assert.Equal(t, apperr.CodeExternal, ae.Code)
	assert.Contains(t, ae.Error(), "context canceled")
}

func TestErrorFromReason(t *testing.T) {
	assert.Equal(t, apperr.CodeNotFound, apperr.From(errorFromReason("f", "Account does NOT exist")).Code)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(errorFromReason("f", "user not found")).Code)
	assert.Equal(t, apperr.CodeConflict, apperr.From(errorFromReason("f", "account exists")).Code)
	assert.Equal(t, apperr.CodeConflict, apperr.From(errorFromReason("f", "username already in use")).Code)
	assert.Equal(t, apperr.CodeExternal, apperr.From(errorFromReason("f", "forced failure")).Code)
	assert.Equal(t, apperr.CodeExternal, apperr.From(errorFromReason("f", "")).Code)

	reserved := `(XID t95cwn) "testhost" is a reserved username on this system.`
	assert.Equal(t, apperr.CodeConflict, apperr.From(errorFromReason("createacct", reserved)).Code,
		"a reserved-username rejection on account creation must route through the same retry-with-a-new-name path as a genuine conflict")
	assert.Equal(t, apperr.CodeExternal, apperr.From(errorFromReason("f", reserved)).Code,
		"the reserved-username special case is scoped to createacct only")
}
