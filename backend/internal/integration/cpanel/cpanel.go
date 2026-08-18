// Package cpanel implements ports.ServerModule for WHM/cPanel servers via
// WHM API 1: `https://<host>:2087/json-api/<fn>?api.version=1`. Authentication
// is an API token (`Authorization: whm <user>:<token>`) when one is set,
// otherwise HTTP Basic auth with the account password (`Authorization: Basic
// base64(<user>:<password>)`) - mirroring WHMCS, which also accepts either.
//
// Operation table (ports.ServerModule -> WHM API 1 function):
//
//	Create          POST /json-api/createacct           (username, domain, password, plan, contactemail, ip)
//	Suspend         POST /json-api/suspendacct          (user, reason)
//	Unsuspend       POST /json-api/unsuspendacct        (user)
//	Terminate       POST /json-api/removeacct           (user)
//	ChangePackage   POST /json-api/changepackage        (user, pkg)
//	ChangePassword  POST /json-api/passwd               (user, password)
//	EnsurePackage   POST /json-api/addpkg -> editpkg     (name, featurelist, quota, bwlimit, maxaddon, maxsub, maxpark, maxpop, maxsql, maxftp)
//	DeletePackage   POST /json-api/killpkg              (pkg)
//	PackageInUse    GET  /json-api/listaccts            (searchtype=package, search=^<pkg>$) - read-only, retried 2x
//	ListPackages    GET  /json-api/listpkgs             () - read-only, no account required
//	AccountInfo     GET  /json-api/accountsummary       (user) - retried 2x on network/5xx
//	TestConnection  GET  /json-api/version              (+ best-effort gethostname, get_nameserver_config, nvget key=nameserver[2-4]) - no account required
//	SSOURL          GET  /json-api/create_user_session  (user, service=cpaneld)
//
// Behaviors:
//   - WHM returns HTTP 200 with metadata.result=0 on API-level failures; these
//     are mapped to apperr errors (NOT_FOUND / CONFLICT / EXTERNAL by reason).
//   - TLS certificates are verified by default. Config.TLSInsecureSkipVerify
//     opts out for use_ssl servers (WHM boxes routinely run self-signed
//     certificates) - wired to PANEL_TLS_INSECURE_SKIP_VERIFY.
//   - Idempotent GETs retry up to 2 extra times on network errors / 5xx;
//     mutating POSTs never auto-retry.
//   - Circuit-breaker-lite: after BreakerThreshold consecutive transport
//     failures the client fast-fails with EXTERNAL for BreakerCooldown.
//   - Every HTTP attempt is recorded via ports.IntegrationLogger with
//     sensitive keys redacted (password, passwd, token, signature,
//     authorization, apikey/api_key, secret).
package cpanel

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/integration/redact"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// Compile-time interface check.
var _ ports.ServerModule = (*Client)(nil)

const (
	providerName = "cpanel"

	defaultTimeout          = 30 * time.Second
	defaultBreakerThreshold = 5
	defaultBreakerCooldown  = 60 * time.Second
	defaultHTTPSPort        = 2087
	defaultHTTPPort         = 2086

	// getRetries is the number of EXTRA attempts for idempotent GETs on
	// network errors / 5xx responses.
	getRetries = 2

	// maxBodyBytes caps how much of a WHM response body is read.
	maxBodyBytes = 4 << 20
)

// Config tunes the WHM client. Zero values fall back to safe defaults.
type Config struct {
	// Timeout is the per-request timeout (default 30s).
	Timeout time.Duration
	// RetryDelay is the pause between retries of idempotent GETs (default 0).
	RetryDelay time.Duration
	// BreakerThreshold is the number of consecutive transport failures
	// (network errors / 5xx) after which the circuit opens (default 5).
	BreakerThreshold int
	// BreakerCooldown is how long the open circuit fast-fails (default 60s).
	BreakerCooldown time.Duration
	// TLSInsecureSkipVerify disables TLS certificate verification for
	// use_ssl servers (self-signed WHM certificates). Default false:
	// certificates are verified.
	TLSInsecureSkipVerify bool
}

// Client talks WHM API 1 and implements ports.ServerModule.
type Client struct {
	cfg      Config
	plain    *http.Client // for use_ssl=false servers
	insecure *http.Client // TLS skip-verify, for use_ssl=true servers
	log      ports.IntegrationLogger
	clock    ports.Clock

	mu          sync.Mutex
	consecFails int
	openUntil   time.Time
}

// New builds a Client. hc, log and clock may be nil (defaults are used).
func New(cfg Config, hc *http.Client, log ports.IntegrationLogger, clock ports.Clock) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.BreakerThreshold <= 0 {
		cfg.BreakerThreshold = defaultBreakerThreshold
	}
	if cfg.BreakerCooldown <= 0 {
		cfg.BreakerCooldown = defaultBreakerCooldown
	}
	if hc == nil {
		hc = &http.Client{}
	}
	if log == nil {
		log = nopLogger{}
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &Client{
		cfg:      cfg,
		plain:    hc,
		insecure: insecureClient(hc),
		log:      log,
		clock:    clock,
	}
}

// insecureClient derives a copy of base whose transport skips TLS
// certificate verification (WHM self-signed certificates).
func insecureClient(base *http.Client) *http.Client {
	var tr *http.Transport
	switch t := base.Transport.(type) {
	case *http.Transport:
		tr = t.Clone()
	default:
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			tr = dt.Clone()
		} else {
			tr = &http.Transport{}
		}
	}
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{} //nolint:gosec // skip-verify is set below by design
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	clone := *base
	clone.Transport = tr
	return &clone
}

// baseURL builds the WHM base URL from a server config. Default ports:
// 2087 (SSL) / 2086 (plain).
func baseURL(s ports.ServerConfig) string {
	scheme, port := "http", s.Port
	if s.UseSSL {
		scheme = "https"
	}
	if port == 0 {
		if s.UseSSL {
			port = defaultHTTPSPort
		} else {
			port = defaultHTTPPort
		}
	}
	return fmt.Sprintf("%s://%s:%d", scheme, s.Hostname, port)
}

func (c *Client) httpClient(s ports.ServerConfig) *http.Client {
	if s.UseSSL && c.cfg.TLSInsecureSkipVerify {
		return c.insecure
	}
	return c.plain
}

// authHeader builds the WHM Authorization header. An API token uses the token
// scheme (`whm user:token`); a password falls back to HTTP Basic auth
// (`Basic base64(user:password)`) - the same choice WHMCS makes, and the reason
// password logins used to 403 (they were being sent as a bogus token). A token
// takes precedence when both are present. Empty when no credential is set.
func authHeader(s ports.ServerConfig) string {
	switch {
	case s.APIToken != "":
		return "whm " + s.Username + ":" + s.APIToken
	case s.Password != "":
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(s.Username+":"+s.Password))
	default:
		return ""
	}
}

// WHM response envelope

// whmInt tolerates WHM returning result as a number or a numeric string.
type whmInt int

// UnmarshalJSON implements json.Unmarshaler.
func (v *whmInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*v = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		f, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil {
			return fmt.Errorf("invalid whm result value %q", s)
		}
		n = int(f)
	}
	*v = whmInt(n)
	return nil
}

type whmMetadata struct {
	Version int    `json:"version"`
	Reason  string `json:"reason"`
	Result  whmInt `json:"result"`
	Command string `json:"command"`
}

type whmResponse struct {
	Metadata whmMetadata     `json:"metadata"`
	Data     json.RawMessage `json:"data"`
}

func parseWHM(raw []byte) (*whmResponse, error) {
	var r whmResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if r.Metadata.Command == "" && r.Metadata.Reason == "" && r.Metadata.Result == 0 && len(r.Data) == 0 {
		return nil, errors.New("missing whm metadata envelope")
	}
	return &r, nil
}

// errorFromReason maps a metadata.result=0 reason to an apperr error.
// CRITICAL: WHM answers HTTP 200 for API-level failures - this mapping is the
// only place they become Go errors.
func errorFromReason(fn, reason string) error {
	if reason == "" {
		reason = "unknown WHM API error"
	}
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "does not exist"), strings.Contains(lower, "not found"):
		return apperr.NotFound("cpanel account")
	case strings.Contains(lower, "exists"), strings.Contains(lower, "already"):
		return apperr.Conflict(fmt.Sprintf("whm %s: %s", fn, reason))
	case fn == "createacct" && strings.Contains(lower, "reserved"):
		// WHM refuses certain system-significant usernames outright (e.g.
		// "test", "mail", "www") - not a collision with another account, but
		// ProvisionCreate's conflict handling already does exactly what this
		// needs: confirm no account was actually created (AccountInfo returns
		// NotFound) then retry under domain.DisambiguateUsername instead of
		// proposing the same doomed username forever.
		return apperr.Conflict(fmt.Sprintf("whm %s: %s", fn, reason))
	default:
		return apperr.External(providerName, fmt.Errorf("whm %s: %s", fn, reason))
	}
}

// HTTP plumbing

func (c *Client) post(ctx context.Context, s ports.ServerConfig, fn string, params url.Values) (*whmResponse, error) {
	return c.call(ctx, s, fn, http.MethodPost, params, 0)
}

func (c *Client) get(ctx context.Context, s ports.ServerConfig, fn string, params url.Values, retries int) (*whmResponse, error) {
	return c.call(ctx, s, fn, http.MethodGet, params, retries)
}

// call runs one WHM API function with up to `retries` extra attempts on
// retryable (network / 5xx) failures.
func (c *Client) call(ctx context.Context, s ports.ServerConfig, fn, method string, params url.Values, retries int) (*whmResponse, error) {
	if err := c.breakerCheck(); err != nil {
		return nil, err
	}
	endpoint := "/json-api/" + fn
	reqLog := redactParams(params)
	attempts := retries + 1
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		resp, retryable, err := c.doOnce(ctx, s, fn, method, params, endpoint, reqLog)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable || attempt == attempts {
			break
		}
		if c.cfg.RetryDelay > 0 {
			select {
			case <-ctx.Done():
				return nil, apperr.External(providerName, ctx.Err())
			case <-time.After(c.cfg.RetryDelay):
			}
		}
	}
	return nil, lastErr
}

// doOnce performs a single HTTP attempt. The bool result reports whether the
// failure is retryable (network error or 5xx).
func (c *Client) doOnce(ctx context.Context, s ports.ServerConfig, fn, method string, params url.Values, endpoint string, reqLog any) (*whmResponse, bool, error) {
	rctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	q := url.Values{"api.version": {"1"}}
	var body io.Reader
	if method == http.MethodGet {
		for k, vs := range params {
			q[k] = vs
		}
	} else {
		body = strings.NewReader(params.Encode())
	}
	req, err := http.NewRequestWithContext(rctx, method, baseURL(s)+endpoint+"?"+q.Encode(), body)
	if err != nil {
		return nil, false, apperr.External(providerName, err)
	}
	if h := authHeader(s); h != "" {
		req.Header.Set("Authorization", h)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	start := c.clock.Now()
	httpResp, err := c.httpClient(s).Do(req)
	latency := c.clock.Now().Sub(start).Milliseconds()
	if err != nil {
		c.recordFailure()
		c.logCall(ctx, endpoint, method, 0, false, latency, reqLog, nil, err.Error())
		return nil, true, apperr.External(providerName, err)
	}
	defer httpResp.Body.Close() //nolint:errcheck

	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, maxBodyBytes))
	if err != nil {
		c.recordFailure()
		c.logCall(ctx, endpoint, method, httpResp.StatusCode, false, latency, reqLog, nil, err.Error())
		return nil, true, apperr.External(providerName, err)
	}

	parsed, parseErr := parseWHM(raw)
	respLog := redactAny(rawToAny(raw))

	if httpResp.StatusCode >= 500 {
		c.recordFailure()
		errMsg := fmt.Sprintf("whm %s: http %d", fn, httpResp.StatusCode)
		c.logCall(ctx, endpoint, method, httpResp.StatusCode, false, latency, reqLog, respLog, errMsg)
		return nil, true, apperr.External(providerName, errors.New(errMsg))
	}

	// The server answered conclusively - reset the breaker even when the
	// answer is an HTTP or API-level error.
	c.recordSuccess()

	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		errMsg := fmt.Sprintf("whm %s: http %d", fn, httpResp.StatusCode)
		if parseErr == nil && parsed.Metadata.Reason != "" {
			errMsg += ": " + parsed.Metadata.Reason
		}
		c.logCall(ctx, endpoint, method, httpResp.StatusCode, false, latency, reqLog, respLog, errMsg)
		return nil, false, apperr.External(providerName, errors.New(errMsg))
	}
	if parseErr != nil {
		errMsg := fmt.Sprintf("whm %s: malformed response: %v", fn, parseErr)
		c.logCall(ctx, endpoint, method, httpResp.StatusCode, false, latency, reqLog, respLog, errMsg)
		return nil, false, apperr.External(providerName, errors.New(errMsg))
	}
	if int(parsed.Metadata.Result) != 1 {
		apiErr := errorFromReason(fn, parsed.Metadata.Reason)
		c.logCall(ctx, endpoint, method, httpResp.StatusCode, false, latency, reqLog, respLog, apiErr.Error())
		return nil, false, apiErr
	}
	c.logCall(ctx, endpoint, method, httpResp.StatusCode, true, latency, reqLog, respLog, "")
	return parsed, false, nil
}

func (c *Client) logCall(ctx context.Context, endpoint, method string, status int, success bool, latencyMS int64, req, resp any, errMsg string) {
	c.log.Log(ctx, ports.IntegrationCall{
		Provider:   providerName,
		Endpoint:   endpoint,
		Method:     method,
		StatusCode: status,
		Success:    success,
		LatencyMS:  latencyMS,
		Request:    req,
		Response:   resp,
		Error:      errMsg,
	})
}

// rawToAny parses raw JSON for logging; non-JSON bodies are logged as a
// (truncated) string.
func rawToAny(raw []byte) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		s := string(raw)
		if len(s) > 512 {
			s = s[:512]
		}
		return s
	}
	return v
}

// Circuit-breaker-lite

func (c *Client) breakerCheck() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.consecFails >= c.cfg.BreakerThreshold && c.clock.Now().Before(c.openUntil) {
		return apperr.New(apperr.CodeExternal, "cpanel: circuit open, failing fast")
	}
	return nil
}

func (c *Client) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consecFails++
	if c.consecFails >= c.cfg.BreakerThreshold {
		c.openUntil = c.clock.Now().Add(c.cfg.BreakerCooldown)
	}
}

func (c *Client) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consecFails = 0
}

// Redaction (CONTRACTS.md section 3)

const redactedPlaceholder = redact.Placeholder

// redactor masks any key containing one of these fragments. "url"/"session"
// cover WHM's create_user_session (SSO) response, whose data.url/data.session
// fields are a live one-time cPanel login bearer credential - they must never
// be persisted in cleartext in integration_logs.
var redactor = redact.Substrings(
	"password", "passwd", "token", "signature", "authorization", "apikey", "api_key", "secret",
	"url", "session",
)

// redactParams flattens url.Values into a loggable map with sensitive values
// masked.
func redactParams(v url.Values) map[string]string { return redactor.FormJoined(v) }

// redactAny recursively masks sensitive keys in decoded JSON values.
func redactAny(v any) any { return redactor.Value(v) }

// nil-dep defaults

type nopLogger struct{}

func (nopLogger) Log(context.Context, ports.IntegrationCall) {}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
