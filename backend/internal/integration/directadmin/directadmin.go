// Package directadmin implements ports.ServerModule ("directadmin") over the
// DirectAdmin legacy API (https://host:2222/CMD_API_*, HTTP Basic auth).
//
// Command mapping (CONTRACTS.md §12 / mockserver README §3):
//
//	Create         POST /CMD_API_ACCOUNT_USER      action=create, username, email, passwd, passwd2, domain, package, ip, notify=no
//	Suspend        POST /CMD_API_SELECT_USERS      select0=<user>, suspend=Suspend, reason=<reason>
//	Unsuspend      POST /CMD_API_SELECT_USERS      select0=<user>, suspend=Unsuspend
//	Terminate      POST /CMD_API_SELECT_USERS      select0=<user>, delete=yes, confirmed=Confirm
//	ChangePackage  POST /CMD_API_MODIFY_USER       action=package, user, package
//	EnsurePackage  POST /CMD_API_MANAGE_USER_PACKAGES action=create->modify, packagename, cgi, ssh, bandwidth, quota, vdomains, nsubdomains, domainptr, nemails, mysql, ftp (+u<field>=ON); when TemplatePackage is set, first GETs CMD_API_PACKAGES_USER?package=<name> and merges its raw fields as the base
//	DeletePackage  POST /CMD_API_MANAGE_USER_PACKAGES action=delete, delete=Submit, select0=<name>
//	ListPackages   GET  /CMD_API_PACKAGES_USER        () - read-only, repeated list[]=<name> values; package=<name> instead returns that one package's full raw field set
//	ChangePassword POST /CMD_API_USER_PASSWD       username, passwd, passwd2
//	AccountInfo    GET  /CMD_API_SHOW_USER_CONFIG  user=<user> (raw url-encoded config dump)
//	SSOURL         no API call - formatted from Config.LoginKeyURLPattern, EXTERNAL "unsupported" when empty
//
// Responses are parsed as legacy URL-encoded values (`error=0&text=...&details=...`,
// `error=1` on failure). JSON bodies are tolerated when the Content-Type says
// so (json=yes is NOT assumed). Every call is logged through
// ports.IntegrationLogger with secrets redacted. Failures at the transport
// level (network errors, 5xx) feed a circuit-breaker-lite: after
// BreakerThreshold consecutive failures the client fast-fails with an
// EXTERNAL error until BreakerCooldown has elapsed. Idempotent GETs retry up
// to MaxRetries on 5xx/network errors; POSTs are never auto-retried.
package directadmin

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

const providerName = "directadmin"

// Defaults for Config zero values.
const (
	defaultPort             = 2222
	defaultTimeout          = 30 * time.Second
	defaultMaxRetries       = 2
	defaultRetryBackoff     = 500 * time.Millisecond
	defaultBreakerThreshold = 3
	defaultBreakerCooldown  = 60 * time.Second
)

// Config carries adapter-level (not per-server) configuration.
type Config struct {
	// LoginKeyURLPattern enables SSOURL. When empty, SSOURL returns an
	// EXTERNAL "unsupported" error. Placeholders replaced per call:
	// {scheme}, {host}, {port}, {username}.
	LoginKeyURLPattern string
	// Timeout for the default HTTP client (only used when New receives a nil
	// *http.Client). Default 30s.
	Timeout time.Duration
	// MaxRetries is the number of retries (on top of the first attempt) for
	// idempotent GET requests on 5xx/network failures. Default 2.
	MaxRetries int
	// RetryBackoff is the wait between retry attempts. Default 500ms.
	RetryBackoff time.Duration
	// BreakerThreshold is the number of consecutive transport failures that
	// opens the circuit. Default 3.
	BreakerThreshold int
	// BreakerCooldown is how long the circuit stays open. Default 60s.
	BreakerCooldown time.Duration
	// TLSInsecureSkipVerify disables TLS certificate verification for
	// use_ssl servers (self-signed DirectAdmin certificates). Default
	// false: certificates are verified.
	TLSInsecureSkipVerify bool
}

func (c Config) withDefaults() Config {
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = defaultMaxRetries
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = defaultRetryBackoff
	}
	if c.BreakerThreshold <= 0 {
		c.BreakerThreshold = defaultBreakerThreshold
	}
	if c.BreakerCooldown <= 0 {
		c.BreakerCooldown = defaultBreakerCooldown
	}
	return c
}

// Client is the DirectAdmin adapter. Safe for concurrent use.
type Client struct {
	cfg        Config
	hc         *http.Client
	hcInsecure *http.Client // TLS skip-verify variant, only when cfg.TLSInsecureSkipVerify
	il         ports.IntegrationLogger
	clk        ports.Clock

	mu        sync.Mutex
	failures  int       // consecutive transport failures
	openUntil time.Time // circuit open until this instant
}

// Compile-time interface check.
var _ ports.ServerModule = (*Client)(nil)

// New builds the adapter. hc may be nil (a client with cfg.Timeout is
// created); il may be nil (calls are not logged); clk may be nil (real time).
func New(cfg Config, hc *http.Client, il ports.IntegrationLogger, clk ports.Clock) *Client {
	cfg = cfg.withDefaults()
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	if il == nil {
		il = nopLogger{}
	}
	if clk == nil {
		clk = systemClock{}
	}
	return &Client{cfg: cfg, hc: hc, hcInsecure: insecureClient(hc), il: il, clk: clk}
}

// insecureClient derives a copy of base whose transport skips TLS
// certificate verification (self-signed DirectAdmin certificates). Only used
// when Config.TLSInsecureSkipVerify is set.
func insecureClient(base *http.Client) *http.Client {
	var tr *http.Transport
	switch t := base.Transport.(type) {
	case *http.Transport:
		tr = t.Clone()
	default:
		if def, ok := http.DefaultTransport.(*http.Transport); ok {
			tr = def.Clone()
		} else {
			tr = &http.Transport{}
		}
	}
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{} //nolint:gosec // skip-verify is an explicit operator opt-in
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	clone := *base
	clone.Transport = tr
	return &clone
}

// Name returns the module name used in products/servers rows.
func (c *Client) Name() string { return providerName }

// Create creates a hosting account (CMD_API_ACCOUNT_USER action=create).
// Unlike WHM's createacct, DirectAdmin does not auto-assign a shared IP when
// ip is omitted - it requires a valid IP from the reseller's own pool, so a
// blank a.IP is rejected locally instead of round-tripping to a confusing
// "A valid IP was not provided" from the panel.
func (c *Client) Create(ctx context.Context, s ports.ServerConfig, a ports.CreateAccountParams) (*ports.AccountResult, error) {
	if a.IP == "" {
		return nil, apperr.Validation("invalid directadmin account parameters",
			apperr.FieldError{Field: "ip", Message: "server has no IP address configured"})
	}
	params := url.Values{
		"action":   {"create"},
		"add":      {"Submit"},
		"username": {a.Username},
		"email":    {a.Email},
		"passwd":   {a.Password},
		"passwd2":  {a.Password},
		"domain":   {a.Domain},
		"package":  {a.Package},
		"ip":       {a.IP},
		"notify":   {"no"},
	}
	vals, err := c.call(ctx, s, http.MethodPost, "CMD_API_ACCOUNT_USER", params, false)
	if err != nil {
		return nil, err
	}
	return &ports.AccountResult{
		Username: a.Username,
		Domain:   a.Domain,
		IP:       a.IP,
		Meta: map[string]any{
			"text":    vals.Get("text"),
			"details": vals.Get("details"),
		},
	}, nil
}

// Suspend suspends an account (CMD_API_SELECT_USERS suspend=Suspend).
func (c *Client) Suspend(ctx context.Context, s ports.ServerConfig, username, reason string) error {
	params := url.Values{
		"select0": {username},
		"suspend": {"Suspend"},
		"reason":  {reason},
	}
	_, err := c.call(ctx, s, http.MethodPost, "CMD_API_SELECT_USERS", params, false)
	return err
}

// Unsuspend unsuspends an account (CMD_API_SELECT_USERS suspend=Unsuspend).
func (c *Client) Unsuspend(ctx context.Context, s ports.ServerConfig, username string) error {
	params := url.Values{
		"select0": {username},
		"suspend": {"Unsuspend"},
	}
	_, err := c.call(ctx, s, http.MethodPost, "CMD_API_SELECT_USERS", params, false)
	return err
}

// Terminate deletes an account (CMD_API_SELECT_USERS delete=yes). A missing
// account is not an error (idempotent - same as DeletePackage - a retried
// terminate after the account was already removed on an earlier attempt
// must succeed, not fail forever).
func (c *Client) Terminate(ctx context.Context, s ports.ServerConfig, username string) error {
	params := url.Values{
		"select0":   {username},
		"delete":    {"yes"},
		"confirmed": {"Confirm"},
	}
	_, err := c.call(ctx, s, http.MethodPost, "CMD_API_SELECT_USERS", params, false)
	if err != nil && apperr.From(err).Code == apperr.CodeNotFound {
		return nil
	}
	return err
}

// ChangePackage moves an account to another package (CMD_API_MODIFY_USER).
func (c *Client) ChangePackage(ctx context.Context, s ports.ServerConfig, username, pkg string) error {
	params := url.Values{
		"action":  {"package"},
		"user":    {username},
		"package": {pkg},
	}
	_, err := c.call(ctx, s, http.MethodPost, "CMD_API_MODIFY_USER", params, false)
	return err
}

// ChangePassword sets a new account password (CMD_API_USER_PASSWD).
func (c *Client) ChangePassword(ctx context.Context, s ports.ServerConfig, username, password string) error {
	params := url.Values{
		"username": {username},
		"passwd":   {password},
		"passwd2":  {password},
	}
	_, err := c.call(ctx, s, http.MethodPost, "CMD_API_USER_PASSWD", params, false)
	return err
}

// EnsurePackage creates the user package if absent, or updates it to match
// spec when it already exists (action=create -> action=modify on CONFLICT).
// When spec.TemplatePackage is set, it first reads that package's full raw
// field set and merges it as the base - under our own computed fields,
// which always win - so long-tail settings not modeled by PackageSpec (Git,
// WordPress, ClamAV, etc.) survive onto the new package, since DirectAdmin
// has no separate feature-list object like cPanel's. Idempotent so
// provisioning retries are safe. Size limits are in MB; a limit equal to
// ports.Unlimited sets the paired u<field>=ON flag.
func (c *Client) EnsurePackage(ctx context.Context, s ports.ServerConfig, spec ports.PackageSpec) error {
	if spec.Name == "" {
		return apperr.Validation("invalid directadmin package parameters",
			apperr.FieldError{Field: "name", Message: "is required"})
	}
	params, err := c.mergedPackageParams(ctx, s, spec, "create")
	if err != nil {
		return err
	}
	_, err = c.call(ctx, s, http.MethodPost, "CMD_API_MANAGE_USER_PACKAGES", params, false)
	if err != nil && apperr.From(err).Code == apperr.CodeConflict {
		modify := cloneValues(params)
		modify.Set("action", "modify")
		modify.Del("add")
		_, err = c.call(ctx, s, http.MethodPost, "CMD_API_MANAGE_USER_PACKAGES", modify, false)
	}
	return err
}

// mergedPackageParams builds the CMD_API_MANAGE_USER_PACKAGES form values:
// spec.TemplatePackage's current raw fields (if set) as the base - EXCEPT
// the canonical resource-limit fields (daManagedFields: quota, bandwidth,
// vdomains, nsubdomains, domainptr, nemails, mysql, ftp and their u<field>
// companions), which this app fully owns via domain.ProvisionKey and must
// never silently inherit from the template, even when the admin hasn't
// modeled that particular resource as a Dynamic Spec on this product (a
// template with every field checked "Unlimited" - a common WHMCS/DA base
// package - must not make an unmodeled resource unlimited on the per-service
// package just because it happened to be unlimited on the template; it's
// simply omitted, exactly as it already is for a product with no
// TemplatePackage at all). Our own daPackageParams overlay is then applied on
// top and is the ONLY source for these fields - deliberately no separate
// normalization pass forcing u<field>=OFF for the non-unlimited case: real
// DirectAdmin treats u<field> as a checkbox, keyed on the field's mere
// PRESENCE in the request rather than its string value (confirmed against a
// live server: sending the intended "not unlimited" as u<field>=OFF was
// itself read as "checked" and made the package unlimited regardless of a
// correct concrete quota alongside it). daPackageParams already gets this
// right on its own: it sets u<field>=ON only when truly unlimited, and
// otherwise omits the key entirely - never send "OFF" as a value here. The
// read (if any) happens once per call, reused for both the create and modify
// attempt.
func (c *Client) mergedPackageParams(ctx context.Context, s ports.ServerConfig, spec ports.PackageSpec, action string) (url.Values, error) {
	params := url.Values{}
	if spec.TemplatePackage != "" {
		tmpl, err := c.readPackageFields(ctx, s, spec.TemplatePackage)
		if err != nil {
			return nil, fmt.Errorf("directadmin: read template package %q: %w", spec.TemplatePackage, err)
		}
		managed := daManagedFields()
		for k := range tmpl {
			if managed[k] {
				continue
			}
			params.Set(k, tmpl.Get(k))
		}
	}
	overlay := daPackageParams(spec, action)
	for k := range overlay {
		params.Set(k, overlay.Get(k))
	}
	return params, nil
}

// daManagedFields is the set of DirectAdmin package fields (and their
// u<field> unlimited-flag companions) this app fully owns via
// domain.ProvisionKey/domain.PanelParam - always derived from the resolved
// spec.Limits, or entirely omitted when the admin hasn't modeled that
// resource as a Dynamic Spec, never inherited from a TemplatePackage. Only
// genuinely unmodeled long-tail fields (Git, WordPress, ClamAV, skin,
// language, etc.) are meant to pass through from the template untouched.
func daManagedFields() map[string]bool {
	keys := []domain.ProvisionKey{
		domain.SpecDisk, domain.SpecBandwidth, domain.SpecAddonDomains,
		domain.SpecSubdomains, domain.SpecParkedDomains, domain.SpecEmailAccounts,
		domain.SpecDatabases, domain.SpecFTPAccounts,
	}
	out := make(map[string]bool, len(keys)*2)
	for _, k := range keys {
		name, _, ok := domain.PanelParam(domain.ModuleDirectAdmin, k)
		if !ok {
			continue
		}
		out[name] = true
		out["u"+name] = true
	}
	return out
}

// readPackageFields reads one package's full raw field set via
// CMD_API_PACKAGES_USER?package=<name> - the same read-only command
// ListPackages already calls, switched by DirectAdmin to a single-package
// dump when a name is given. Internal to EnsurePackage's template-clone
// merge; deliberately not exposed on ports.ServerModule since cPanel has no
// equivalent concept (FeatureList is a live reference, never cloned).
func (c *Client) readPackageFields(ctx context.Context, s ports.ServerConfig, name string) (url.Values, error) {
	return c.call(ctx, s, http.MethodGet, "CMD_API_PACKAGES_USER", url.Values{"package": {name}}, true)
}

// cloneValues returns an independent copy of v so mutating the copy (e.g.
// swapping action=create for action=modify on retry) never affects the
// original.
func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vv := range v {
		out[k] = append([]string(nil), vv...)
	}
	return out
}

// DeletePackage removes a user package (action=delete). A missing package is not
// an error (idempotent terminate).
func (c *Client) DeletePackage(ctx context.Context, s ports.ServerConfig, name string) error {
	if name == "" {
		return apperr.Validation("invalid directadmin parameters",
			apperr.FieldError{Field: "name", Message: "is required"})
	}
	params := url.Values{
		"action":  {"delete"},
		"delete":  {"Submit"},
		"select0": {name},
	}
	_, err := c.call(ctx, s, http.MethodPost, "CMD_API_MANAGE_USER_PACKAGES", params, false)
	if err != nil && apperr.From(err).Code == apperr.CodeNotFound {
		return nil
	}
	return err
}

// daPackageParams builds CMD_API_MANAGE_USER_PACKAGES form values from a
// PackageSpec, mapping each canonical knob to its DirectAdmin field via
// domain.PanelParam. Unlimited limits set the paired u<field>=ON flag.
func daPackageParams(spec ports.PackageSpec, action string) url.Values {
	params := url.Values{"action": {action}}
	if action == "create" {
		params.Set("add", "Submit")
	}
	params.Set("packagename", spec.Name)
	params.Set("cgi", daOnOff(spec.CGIAccess))
	params.Set("ssh", daOnOff(spec.ShellAccess))
	// userssh deliberately left unset - its semantics relative to ssh are
	// unconfirmed against official docs; it inherits from TemplatePackage
	// (if any) or the panel's own create-time default.
	for key, v := range spec.Limits {
		name, _, ok := domain.PanelParam(domain.ModuleDirectAdmin, key)
		if !ok {
			continue
		}
		if v == ports.Unlimited {
			params.Set("u"+name, "ON")
			params.Set(name, "0")
		} else {
			params.Set(name, strconv.FormatInt(v, 10))
		}
	}
	return params
}

// daOnOff renders a boolean toggle as DirectAdmin's ON/OFF string, e.g.
// CMD_API_MANAGE_USER_PACKAGES's cgi/ssh params.
func daOnOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

// ListPackages returns configured DirectAdmin package names via
// CMD_API_PACKAGES_USER (GET - read-only, idempotent, retried on transport
// failures), for the admin product form's package-name picker. Like
// CMD_API_SHOW_USER_CONFIG/CMD_API_SHOW_USERS, this is a read-only dump
// command: it carries no `error=0` marker on success, only `list[]=...`.
func (c *Client) ListPackages(ctx context.Context, s ports.ServerConfig) ([]string, error) {
	vals, err := c.call(ctx, s, http.MethodGet, "CMD_API_PACKAGES_USER", url.Values{}, true)
	if err != nil {
		return nil, err
	}
	// Real DirectAdmin repeats the literal key "list[]" (not "list") for each
	// package name, e.g. "list[]=Enterprise&list[]=Large&list[]=Medium".
	names := append([]string(nil), vals["list[]"]...)
	sort.Strings(names)
	return names, nil
}

// AccountInfo fetches the raw user config dump (CMD_API_SHOW_USER_CONFIG) and
// maps it to ports.AccountInfo. The dump has no `error=0` marker on success;
// `error=1` marks failure (mapped to NOT_FOUND/EXTERNAL).
func (c *Client) AccountInfo(ctx context.Context, s ports.ServerConfig, username string) (*ports.AccountInfo, error) {
	params := url.Values{"user": {username}}
	vals, err := c.call(ctx, s, http.MethodGet, "CMD_API_SHOW_USER_CONFIG", params, true)
	if err != nil {
		return nil, err
	}

	info := &ports.AccountInfo{
		Username:  vals.Get("username"),
		Domain:    vals.Get("domain"),
		Package:   vals.Get("package"),
		Suspended: parseDABool(vals.Get("suspended")),
		Meta:      map[string]any{},
	}
	if info.Username == "" {
		info.Username = username
	}
	// quota is the disk limit in MB when numeric ("unlimited" ignored).
	if q := vals.Get("quota"); q != "" {
		if n, perr := strconv.ParseInt(q, 10, 64); perr == nil {
			info.DiskLimit = n
		}
	}
	for k := range vals {
		if isSecretKey(k) {
			continue
		}
		info.Meta[k] = vals.Get(k)
	}
	return info, nil
}

// TestConnection verifies DirectAdmin connectivity and credentials with the
// read-only CMD_API_SHOW_USERS listing - it needs no existing account, only a
// valid admin/reseller login. DirectAdmin exposes no reseller-safe version or
// nameserver API comparable to WHM's, so the returned ServerInfo carries no
// auto-population metadata; a nil error simply means the connection works.
func (c *Client) TestConnection(ctx context.Context, s ports.ServerConfig) (*ports.ServerInfo, error) {
	if _, err := c.call(ctx, s, http.MethodGet, "CMD_API_SHOW_USERS", url.Values{}, true); err != nil {
		return nil, err
	}
	return &ports.ServerInfo{}, nil
}

// SSOURL returns a control-panel login URL formatted from the configured
// login-key URL pattern. DirectAdmin has no session-creation API comparable
// to cPanel's create_user_session, so without a configured pattern this
// returns an EXTERNAL "unsupported" error.
func (c *Client) SSOURL(_ context.Context, s ports.ServerConfig, username string) (string, error) {
	if c.cfg.LoginKeyURLPattern == "" {
		return "", apperr.New(apperr.CodeExternal,
			"directadmin: SSO unsupported: login-key URL pattern not configured")
	}
	scheme, port := schemeAndPort(s)
	r := strings.NewReplacer(
		"{scheme}", scheme,
		"{host}", s.Hostname,
		"{port}", strconv.Itoa(port),
		"{username}", url.QueryEscape(username),
	)
	return r.Replace(c.cfg.LoginKeyURLPattern), nil
}

// parseDABool interprets DirectAdmin boolean-ish values ("yes"/"no", ...).
func parseDABool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "y", "1", "true", "on":
		return true
	}
	return false
}

// nopLogger is used when no IntegrationLogger is provided.
type nopLogger struct{}

func (nopLogger) Log(context.Context, ports.IntegrationCall) {}

// systemClock is used when no Clock is provided.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
