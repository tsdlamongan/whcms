package cpanel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// usernameRe enforces cPanel account-name rules: lowercase alphanumeric,
// starting with a letter, at most 16 characters.
var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)

// pkgNameRe enforces WHM package-name rules: letters, digits and underscores,
// at most 45 characters (WHM rejects longer/other names).
var pkgNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,45}$`)

// minPasswordLen is the minimum accepted account password length
// (createacct/passwd password rules).
const minPasswordLen = 8

// Name implements ports.ServerModule.
func (c *Client) Name() string { return string(domain.ModuleCpanel) }

// Create provisions a new cPanel account via createacct.
func (c *Client) Create(ctx context.Context, s ports.ServerConfig, a ports.CreateAccountParams) (*ports.AccountResult, error) {
	var details []apperr.FieldError
	if !usernameRe.MatchString(a.Username) {
		details = append(details, apperr.FieldError{
			Field: "username", Message: "must be lowercase alphanumeric, start with a letter, max 16 characters",
		})
	}
	if a.Domain == "" {
		details = append(details, apperr.FieldError{Field: "domain", Message: "is required"})
	}
	if len(a.Password) < minPasswordLen {
		details = append(details, apperr.FieldError{
			Field: "password", Message: fmt.Sprintf("must be at least %d characters", minPasswordLen),
		})
	}
	if len(details) > 0 {
		return nil, apperr.Validation("invalid cpanel account parameters", details...)
	}

	params := url.Values{}
	params.Set("username", a.Username)
	params.Set("domain", a.Domain)
	params.Set("password", a.Password)
	if a.Package != "" {
		params.Set("plan", a.Package)
	}
	if a.Email != "" {
		params.Set("contactemail", a.Email)
	}
	if a.IP != "" {
		params.Set("ip", a.IP)
	}

	resp, err := c.post(ctx, s, "createacct", params)
	if err != nil {
		return nil, err
	}

	var data struct {
		Username string `json:"username"`
		Domain   string `json:"domain"`
		IP       string `json:"ip"`
	}
	_ = json.Unmarshal(resp.Data, &data) // best-effort; fall back to inputs
	out := &ports.AccountResult{
		Username: a.Username,
		Domain:   a.Domain,
		IP:       data.IP,
		Meta:     metaFromRaw(resp.Data),
	}
	if data.Username != "" {
		out.Username = data.Username
	}
	if data.Domain != "" {
		out.Domain = data.Domain
	}
	return out, nil
}

// Suspend suspends an account via suspendacct.
func (c *Client) Suspend(ctx context.Context, s ports.ServerConfig, username, reason string) error {
	if err := requireUsername(username); err != nil {
		return err
	}
	params := url.Values{}
	params.Set("user", username)
	if reason != "" {
		params.Set("reason", reason)
	}
	_, err := c.post(ctx, s, "suspendacct", params)
	return err
}

// Unsuspend reactivates an account via unsuspendacct.
func (c *Client) Unsuspend(ctx context.Context, s ports.ServerConfig, username string) error {
	if err := requireUsername(username); err != nil {
		return err
	}
	params := url.Values{}
	params.Set("user", username)
	_, err := c.post(ctx, s, "unsuspendacct", params)
	return err
}

// Terminate deletes an account via removeacct. A missing account is not an
// error (idempotent - a retried terminate after the account was already
// removed on an earlier attempt must succeed, not fail forever).
func (c *Client) Terminate(ctx context.Context, s ports.ServerConfig, username string) error {
	if err := requireUsername(username); err != nil {
		return err
	}
	params := url.Values{}
	params.Set("user", username)
	_, err := c.post(ctx, s, "removeacct", params)
	if err != nil && apperr.From(err).Code == apperr.CodeNotFound {
		return nil
	}
	return err
}

// ChangePackage moves an account to another package via changepackage.
func (c *Client) ChangePackage(ctx context.Context, s ports.ServerConfig, username, pkg string) error {
	if err := requireUsername(username); err != nil {
		return err
	}
	if pkg == "" {
		return apperr.Validation("invalid cpanel parameters",
			apperr.FieldError{Field: "package", Message: "is required"})
	}
	params := url.Values{}
	params.Set("user", username)
	params.Set("pkg", pkg)
	_, err := c.post(ctx, s, "changepackage", params)
	return err
}

// ChangePassword sets a new account password via passwd.
func (c *Client) ChangePassword(ctx context.Context, s ports.ServerConfig, username, password string) error {
	if err := requireUsername(username); err != nil {
		return err
	}
	if len(password) < minPasswordLen {
		return apperr.Validation("invalid cpanel parameters", apperr.FieldError{
			Field: "password", Message: fmt.Sprintf("must be at least %d characters", minPasswordLen),
		})
	}
	params := url.Values{}
	params.Set("user", username)
	params.Set("password", password)
	_, err := c.post(ctx, s, "passwd", params)
	return err
}

// EnsurePackage creates the WHM package if absent, or updates it to match spec
// when it already exists (addpkg -> editpkg on CONFLICT). Idempotent so
// provisioning retries are safe. Size limits are in MB; a limit equal to
// ports.Unlimited is rendered as WHM's "unlimited".
func (c *Client) EnsurePackage(ctx context.Context, s ports.ServerConfig, spec ports.PackageSpec) error {
	if !pkgNameRe.MatchString(spec.Name) {
		return apperr.Validation("invalid cpanel package parameters", apperr.FieldError{
			Field: "name", Message: "must be letters, digits or underscores, max 45 characters",
		})
	}
	params := packageParams(spec)
	_, err := c.post(ctx, s, "addpkg", params)
	if err != nil && apperr.From(err).Code == apperr.CodeConflict {
		// Package already exists - update it to match the requested spec.
		_, err = c.post(ctx, s, "editpkg", params)
	}
	return err
}

// DeletePackage removes a WHM package via killpkg. A missing package is not an
// error (idempotent terminate).
func (c *Client) DeletePackage(ctx context.Context, s ports.ServerConfig, name string) error {
	if name == "" {
		return apperr.Validation("invalid cpanel parameters",
			apperr.FieldError{Field: "name", Message: "is required"})
	}
	params := url.Values{}
	params.Set("pkg", name)
	_, err := c.post(ctx, s, "killpkg", params)
	if err != nil && apperr.From(err).Code == apperr.CodeNotFound {
		return nil
	}
	return err
}

// PackageInUse reports whether any account on the server still uses the named
// package, via listaccts with a package search (searchtype=package, exact-name
// regex). Read-only, idempotent GET, retried on transport failures. It is the
// panel-side guard before DeletePackage: an account created outside this app
// (or tracked under another servers row) does not appear in the DB-side
// sibling count, but does appear here.
func (c *Client) PackageInUse(ctx context.Context, s ports.ServerConfig, name string) (bool, error) {
	if name == "" {
		return false, apperr.Validation("invalid cpanel parameters",
			apperr.FieldError{Field: "name", Message: "is required"})
	}
	params := url.Values{}
	params.Set("searchtype", "package")
	// listaccts' search is a regex; anchor and quote so e.g. "starter" never
	// matches an account on "starter_plus".
	params.Set("search", "^"+regexp.QuoteMeta(name)+"$")
	resp, err := c.get(ctx, s, "listaccts", params, getRetries)
	if err != nil {
		return false, err
	}
	var data struct {
		Acct []map[string]any `json:"acct"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return false, apperr.External(providerName, errors.New("whm listaccts: malformed response"))
	}
	return len(data.Acct) > 0, nil
}

// packageParams builds the addpkg/editpkg form values from a PackageSpec,
// mapping each canonical knob to its WHM parameter via domain.PanelParam.
func packageParams(spec ports.PackageSpec) url.Values {
	params := url.Values{}
	params.Set("name", spec.Name)
	fl := spec.FeatureList
	if fl == "" {
		fl = "default"
	}
	params.Set("featurelist", fl)
	params.Set("hasshell", boolToOneZero(spec.ShellAccess))
	params.Set("cgi", boolToOneZero(spec.CGIAccess))
	for key, v := range spec.Limits {
		name, _, ok := domain.PanelParam(domain.ModuleCpanel, key)
		if !ok {
			continue
		}
		params.Set(name, renderLimit(v))
	}
	return params
}

// boolToOneZero renders a boolean toggle as WHM's integer boolean ("1"/"0"),
// e.g. addpkg's hasshell/cgi params.
func boolToOneZero(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// renderLimit renders a package limit for WHM: the unlimited sentinel becomes
// the literal "unlimited", any other value its decimal form.
func renderLimit(v int64) string {
	if v == ports.Unlimited {
		return "unlimited"
	}
	return strconv.FormatInt(v, 10)
}

// ListPackages returns configured WHM package names via listpkgs (read-only,
// idempotent GET, retried on transport failures), for the admin product
// form's package-name picker.
func (c *Client) ListPackages(ctx context.Context, s ports.ServerConfig) ([]string, error) {
	resp, err := c.get(ctx, s, "listpkgs", nil, getRetries)
	if err != nil {
		return nil, err
	}
	var data struct {
		Pkg []map[string]any `json:"pkg"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, apperr.External(providerName, errors.New("whm listpkgs: malformed response"))
	}
	names := make([]string, 0, len(data.Pkg))
	for _, p := range data.Pkg {
		if n := strField(p, "name"); n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

// AccountInfo fetches account state via accountsummary (idempotent GET,
// retried on transport failures).
func (c *Client) AccountInfo(ctx context.Context, s ports.ServerConfig, username string) (*ports.AccountInfo, error) {
	if err := requireUsername(username); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("user", username)
	resp, err := c.get(ctx, s, "accountsummary", params, getRetries)
	if err != nil {
		return nil, err
	}

	var data struct {
		Acct []map[string]any `json:"acct"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil || len(data.Acct) == 0 {
		return nil, apperr.External(providerName, errors.New("whm accountsummary: malformed response: missing acct data"))
	}
	m := data.Acct[0]
	info := &ports.AccountInfo{
		Username:  strField(m, "user", "username"),
		Domain:    strField(m, "domain"),
		Package:   strField(m, "plan", "package"),
		Suspended: truthy(m["suspended"]),
		DiskUsed:  parseMB(m["diskused"]),
		DiskLimit: parseMB(m["disklimit"]),
	}
	if meta, ok := redactAny(m).(map[string]any); ok {
		info.Meta = meta
	}
	if info.Username == "" {
		info.Username = username
	}
	return info, nil
}

// TestConnection verifies WHM connectivity and credentials with the read-only
// `version` call (no account required - this is what fixes the historical
// "cpanel account not found" false failure) and best-effort reads the server
// hostname (`gethostname`) and configured nameservers (`get_nameserver_config`)
// for auto-population. The version call is authoritative: if it fails the whole
// probe fails; the metadata reads never turn a good connection into a bad one.
func (c *Client) TestConnection(ctx context.Context, s ports.ServerConfig) (*ports.ServerInfo, error) {
	resp, err := c.get(ctx, s, "version", nil, getRetries)
	if err != nil {
		return nil, err
	}
	info := &ports.ServerInfo{}
	var ver struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(resp.Data, &ver) == nil {
		info.Version = ver.Version
	}

	if hr, herr := c.get(ctx, s, "gethostname", nil, getRetries); herr == nil {
		var h struct {
			Hostname string `json:"hostname"`
		}
		if json.Unmarshal(hr.Data, &h) == nil {
			info.Hostname = h.Hostname
		}
	}
	// Nameservers for auto-population, from the first source the credentials can
	// read: the reseller/root nameserver config, then the Basic-Setup nv vars.
	// A reseller that inherits its nameservers from root exposes neither, so the
	// list is legitimately empty for such tokens (use a root token to populate).
	if nr, nerr := c.get(ctx, s, "get_nameserver_config", nil, getRetries); nerr == nil {
		info.Nameservers = parseNameservers(nr.Data)
	}
	if len(info.Nameservers) == 0 {
		info.Nameservers = c.nameserversFromNV(ctx, s)
	}
	return info, nil
}

// nameserversFromNV reads the "Basic WebHost Manager Setup" nameservers from
// WHM's non-volatile store (nvget key=nameserver[2-4]). Best-effort: keys the
// token cannot read or that are unset are skipped.
func (c *Client) nameserversFromNV(ctx context.Context, s ports.ServerConfig) []string {
	var out []string
	for _, key := range []string{"nameserver", "nameserver2", "nameserver3", "nameserver4"} {
		resp, err := c.get(ctx, s, "nvget", url.Values{"key": {key}}, getRetries)
		if err != nil {
			continue
		}
		if ns := nvgetString(resp.Data); ns != "" {
			out = append(out, ns)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// nvgetString extracts the first non-empty string from an nvget `data` payload
// (`{"nvdatum":{"key":..,"value":[<v>]}}`); WHM returns [null] for unset keys.
func nvgetString(raw json.RawMessage) string {
	var d struct {
		Nvdatum struct {
			Value []any `json:"value"`
		} `json:"nvdatum"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return ""
	}
	for _, v := range d.Nvdatum.Value {
		if str, ok := v.(string); ok {
			if str = strings.TrimSpace(str); str != "" {
				return str
			}
		}
	}
	return ""
}

// parseNameservers extracts nameserver hostnames from a WHM
// get_nameserver_config `data` payload. WHM has returned the list both as bare
// strings and as objects keyed by "nameserver"/"name"/"host" across versions,
// so both shapes are accepted; anything unrecognised yields no nameservers.
func parseNameservers(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var payload struct {
		Nameservers []json.RawMessage `json:"nameservers"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	out := make([]string, 0, len(payload.Nameservers))
	for _, e := range payload.Nameservers {
		var str string
		if json.Unmarshal(e, &str) == nil {
			if str = strings.TrimSpace(str); str != "" {
				out = append(out, str)
			}
			continue
		}
		var obj map[string]any
		if json.Unmarshal(e, &obj) == nil {
			if ns := strField(obj, "nameserver", "name", "host", "hostname"); ns != "" {
				out = append(out, ns)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SSOURL returns a one-time cPanel login URL via create_user_session.
func (c *Client) SSOURL(ctx context.Context, s ports.ServerConfig, username string) (string, error) {
	if err := requireUsername(username); err != nil {
		return "", err
	}
	params := url.Values{}
	params.Set("user", username)
	params.Set("service", "cpaneld")
	resp, err := c.get(ctx, s, "create_user_session", params, 0)
	if err != nil {
		return "", err
	}
	var data struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil || data.URL == "" {
		return "", apperr.External(providerName, errors.New("whm create_user_session: malformed response: missing url"))
	}
	return data.URL, nil
}

// helpers

func requireUsername(username string) error {
	if username == "" {
		return apperr.Validation("invalid cpanel parameters",
			apperr.FieldError{Field: "username", Message: "is required"})
	}
	return nil
}

// strField returns the first non-empty string value among keys.
func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// truthy interprets WHM's loose boolean encodings (0/1 numbers, "0"/"1"
// strings, bools).
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case json.Number:
		f, _ := t.Float64()
		return f != 0
	case string:
		return t == "1" || strings.EqualFold(t, "true") || strings.EqualFold(t, "yes")
	default:
		return false
	}
}

// parseMB parses WHM disk figures like 512, "512M", "unlimited" into MB.
// Unknown/unlimited values become 0.
func parseMB(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case json.Number:
		f, _ := t.Float64()
		return int64(f)
	case string:
		s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "M"))
		if s == "" || strings.EqualFold(s, "unlimited") {
			return 0
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return int64(f)
	default:
		return 0
	}
}

// metaFromRaw decodes a WHM data payload into a redacted map for panel_meta.
func metaFromRaw(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	red, _ := redactAny(m).(map[string]any)
	return red
}
