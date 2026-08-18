package directadmin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

func testDAPackageSpec() ports.PackageSpec {
	return ports.PackageSpec{
		Name: "whcms_s42",
		Limits: map[domain.ProvisionKey]int64{
			domain.SpecDisk:          10240,           // MB
			domain.SpecBandwidth:     ports.Unlimited, // unlimited
			domain.SpecAddonDomains:  5,
			domain.SpecSubdomains:    10,
			domain.SpecParkedDomains: 2,
			domain.SpecEmailAccounts: 50,
			domain.SpecDatabases:     20,
			domain.SpecFTPAccounts:   8,
		},
	}
}

func TestEnsurePackageCreate(t *testing.T) {
	var form url.Values
	var req http.Request
	ts := httptest.NewServer(legacyHandler(t, 200, "error=0&text=Success&details=ok", &form, &req))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	require.NoError(t, c.EnsurePackage(context.Background(), serverConfig(t, ts), testDAPackageSpec()))

	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "/CMD_API_MANAGE_USER_PACKAGES", req.URL.Path)
	assert.Equal(t, "create", form.Get("action"))
	assert.Equal(t, "Submit", form.Get("add"))
	assert.Equal(t, "whcms_s42", form.Get("packagename"))
	assert.Equal(t, "10240", form.Get("quota"))
	// Unlimited bandwidth sets the paired ubandwidth=ON flag.
	assert.Equal(t, "ON", form.Get("ubandwidth"))
	assert.Equal(t, "0", form.Get("bandwidth"))
	assert.Equal(t, "5", form.Get("vdomains"))
	assert.Equal(t, "10", form.Get("nsubdomains"))
	assert.Equal(t, "2", form.Get("domainptr"))
	assert.Equal(t, "50", form.Get("nemails"))
	assert.Equal(t, "20", form.Get("mysql"))
	assert.Equal(t, "8", form.Get("ftp"))
	assert.Equal(t, "OFF", form.Get("cgi"))
	assert.Equal(t, "OFF", form.Get("ssh"))
}

func TestEnsurePackage_ShellAndCGIAccess(t *testing.T) {
	var form url.Values
	ts := httptest.NewServer(legacyHandler(t, 200, "error=0&text=Success&details=ok", &form, nil))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	spec := testDAPackageSpec()
	spec.ShellAccess = true
	spec.CGIAccess = true
	require.NoError(t, c.EnsurePackage(context.Background(), serverConfig(t, ts), spec))
	assert.Equal(t, "ON", form.Get("cgi"))
	assert.Equal(t, "ON", form.Get("ssh"))
}

// TestEnsurePackage_TemplatePackage_MergesFields proves EnsurePackage reads a
// TemplatePackage's raw fields as the base and lets our own computed fields
// (limits, cgi, ssh) win - so long-tail settings the adapter never models
// (e.g. clamav) survive onto the new per-service package.
func TestEnsurePackage_TemplatePackage_MergesFields(t *testing.T) {
	var captured url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/CMD_API_PACKAGES_USER":
			assert.Equal(t, "template_pkg", r.Form.Get("package"))
			_, _ = w.Write([]byte("clamav=ON&quota=99999&packagename=template_pkg"))
		case r.Method == http.MethodPost && r.URL.Path == "/CMD_API_MANAGE_USER_PACKAGES":
			captured = r.Form
			_, _ = w.Write([]byte("error=0&text=Success&details=ok"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	spec := testDAPackageSpec()
	spec.TemplatePackage = "template_pkg"
	require.NoError(t, c.EnsurePackage(context.Background(), serverConfig(t, ts), spec))

	require.NotNil(t, captured)
	assert.Equal(t, "ON", captured.Get("clamav"), "long-tail template field must be inherited")
	assert.Equal(t, "10240", captured.Get("quota"), "our resolved limit must win over the template's stale value")
	assert.Equal(t, "whcms_s42", captured.Get("packagename"), "the new package's own name must win over the template's")
	assert.Equal(t, "OFF", captured.Get("cgi"))
	assert.Equal(t, "OFF", captured.Get("ssh"))
}

// TestEnsurePackage_TemplatePackage_DoesNotLeakUnmodeledResourceLimits guards
// a real bug: a template package with EVERY resource limit checked
// "Unlimited" (a common WHMCS/DA base package) must not make the
// per-service package unlimited for any resource the admin hasn't modeled
// as a Dynamic Spec on this product - those fields must be entirely omitted
// (falling back to the panel's own creation default, exactly as they already
// are for a product with no TemplatePackage at all), never silently
// inherited from the template. Only genuinely unmodeled long-tail settings
// (e.g. clamav) are meant to pass through.
func TestEnsurePackage_TemplatePackage_DoesNotLeakUnmodeledResourceLimits(t *testing.T) {
	var captured url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/CMD_API_PACKAGES_USER":
			// A template with every resource limit unlimited, mirroring a real
			// DirectAdmin "Edit Package" screen with every checkbox ticked.
			_, _ = w.Write([]byte(
				"clamav=ON&uquota=ON&quota=0&ubandwidth=ON&bandwidth=0&" +
					"uvdomains=ON&vdomains=0&unsubdomains=ON&nsubdomains=0&" +
					"udomainptr=ON&domainptr=0&unemails=ON&nemails=0&" +
					"umysql=ON&mysql=0&uftp=ON&ftp=0&packagename=template_pkg"))
		case r.Method == http.MethodPost && r.URL.Path == "/CMD_API_MANAGE_USER_PACKAGES":
			captured = r.Form
			_, _ = w.Write([]byte("error=0&text=Success&details=ok"))
		}
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	// Only "disk" is a Dynamic Spec the customer configured - every other
	// canonical resource key is intentionally absent from Limits, as it
	// would be for a product where the admin only modeled disk.
	spec := ports.PackageSpec{
		Name:            "whcms_s42",
		TemplatePackage: "template_pkg",
		Limits:          map[domain.ProvisionKey]int64{domain.SpecDisk: 10240},
	}
	require.NoError(t, c.EnsurePackage(context.Background(), serverConfig(t, ts), spec))

	require.NotNil(t, captured)
	// The customer's chosen disk quota wins and is no longer unlimited - the
	// unlimited flag is entirely absent, not sent as "OFF" (real DirectAdmin
	// treats its mere presence as "checked").
	assert.Equal(t, "10240", captured.Get("quota"))
	assert.False(t, captured.Has("uquota"))
	// Every other resource limit the admin never modeled as a Dynamic Spec
	// must be entirely absent - not silently inherited as unlimited from
	// the template.
	for _, field := range []string{
		"bandwidth", "ubandwidth", "vdomains", "uvdomains",
		"nsubdomains", "unsubdomains", "domainptr", "udomainptr",
		"nemails", "unemails", "mysql", "umysql", "ftp", "uftp",
	} {
		assert.Empty(t, captured.Get(field), "field %q must not leak from the template", field)
	}
	// Genuinely unmodeled long-tail settings still pass through untouched.
	assert.Equal(t, "ON", captured.Get("clamav"))
}

// TestEnsurePackage_TemplatePackage_NotFound proves a missing template fails
// the whole EnsurePackage call up front - never issuing the create/modify
// POST - rather than silently proceeding without the template's settings.
func TestEnsurePackage_TemplatePackage_NotFound(t *testing.T) {
	var createCalls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/CMD_API_PACKAGES_USER":
			_, _ = w.Write([]byte("error=1&text=Error&details=Package+missing_pkg+does+not+exist"))
		case r.Method == http.MethodPost && r.URL.Path == "/CMD_API_MANAGE_USER_PACKAGES":
			atomic.AddInt32(&createCalls, 1)
			_, _ = w.Write([]byte("error=0&text=Success&details=ok"))
		}
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	spec := testDAPackageSpec()
	spec.TemplatePackage = "missing_pkg"
	err := c.EnsurePackage(context.Background(), serverConfig(t, ts), spec)
	requireAppErr(t, err, apperr.CodeNotFound)
	assert.Equal(t, int32(0), atomic.LoadInt32(&createCalls), "must not attempt create/modify when the template read fails")
}

// TestEnsurePackage_TemplatePackage_UnlimitedFlagNormalized proves a stale
// u<field>=ON inherited from the template never reaches the request at all
// when our own spec resolves that same key to a concrete (non-unlimited)
// value - daManagedFields excludes it from the template base entirely, and
// it must stay ABSENT (not "OFF"): real DirectAdmin treats u<field> as a
// checkbox keyed on the field's mere presence, so explicitly sending
// u<field>=OFF is read as "checked" (unlimited) regardless of the string
// value - confirmed against a live server (a correct concrete quota
// alongside an explicit uquota=OFF still resulted in an unlimited package).
func TestEnsurePackage_TemplatePackage_UnlimitedFlagNormalized(t *testing.T) {
	var captured url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/CMD_API_PACKAGES_USER":
			_, _ = w.Write([]byte("uquota=ON&quota=0&packagename=template_pkg"))
		case r.Method == http.MethodPost && r.URL.Path == "/CMD_API_MANAGE_USER_PACKAGES":
			captured = r.Form
			_, _ = w.Write([]byte("error=0&text=Success&details=ok"))
		}
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	spec := testDAPackageSpec() // SpecDisk: 10240 (concrete, not unlimited)
	spec.TemplatePackage = "template_pkg"
	require.NoError(t, c.EnsurePackage(context.Background(), serverConfig(t, ts), spec))

	require.NotNil(t, captured)
	assert.Equal(t, "10240", captured.Get("quota"))
	assert.False(t, captured.Has("uquota"), "a concrete resolved limit must omit the unlimited flag entirely, not send it as OFF")
}

func TestEnsurePackageModifiesWhenExists(t *testing.T) {
	var calls int32
	var lastForm url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		lastForm = r.Form
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if atomic.AddInt32(&calls, 1) == 1 {
			_, _ = w.Write([]byte("error=1&text=Cannot+Create&details=That+package+already+exists"))
			return
		}
		_, _ = w.Write([]byte("error=0&text=Success&details=modified"))
	}))
	defer ts.Close()

	c, _ := newTestClient(t, testConfig())
	require.NoError(t, c.EnsurePackage(context.Background(), serverConfig(t, ts), testDAPackageSpec()))
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	assert.Equal(t, "modify", lastForm.Get("action"))
	assert.Empty(t, lastForm.Get("add")) // modify must not send add=Submit
}

func TestEnsurePackageMissingName(t *testing.T) {
	c, _ := newTestClient(t, testConfig())
	err := c.EnsurePackage(context.Background(), ports.ServerConfig{Hostname: "x"}, ports.PackageSpec{})
	requireAppErr(t, err, apperr.CodeValidation)
}

func TestEnsurePackageGenericErrorPropagates(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "error=1&text=Bad&details=quota+invalid", nil, nil))
	defer ts.Close()
	c, _ := newTestClient(t, testConfig())
	err := c.EnsurePackage(context.Background(), serverConfig(t, ts), testDAPackageSpec())
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestDeletePackageSuccess(t *testing.T) {
	var form url.Values
	var req http.Request
	ts := httptest.NewServer(legacyHandler(t, 200, "error=0&text=Success&details=deleted", &form, &req))
	defer ts.Close()
	c, _ := newTestClient(t, testConfig())
	require.NoError(t, c.DeletePackage(context.Background(), serverConfig(t, ts), "whcms_s42"))
	assert.Equal(t, "/CMD_API_MANAGE_USER_PACKAGES", req.URL.Path)
	assert.Equal(t, "delete", form.Get("action"))
	assert.Equal(t, "Submit", form.Get("delete"))
	assert.Equal(t, "whcms_s42", form.Get("select0"))
}

func TestDeletePackageNotFoundIsSuccess(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "error=1&text=Bad&details=package+does+not+exist", nil, nil))
	defer ts.Close()
	c, _ := newTestClient(t, testConfig())
	assert.NoError(t, c.DeletePackage(context.Background(), serverConfig(t, ts), "whcms_s404"))
}

func TestDeletePackageMissingName(t *testing.T) {
	c, _ := newTestClient(t, testConfig())
	err := c.DeletePackage(context.Background(), ports.ServerConfig{Hostname: "x"}, "")
	requireAppErr(t, err, apperr.CodeValidation)
}

// daUsersHandler answers CMD_API_SHOW_USERS with the given user list and
// CMD_API_SHOW_USER_CONFIG with each user's raw config body.
func daUsersHandler(users []string, configs map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		switch r.URL.Path {
		case "/CMD_API_SHOW_USERS":
			vals := url.Values{}
			for _, u := range users {
				vals.Add("list[]", u)
			}
			_, _ = w.Write([]byte(vals.Encode()))
		case "/CMD_API_SHOW_USER_CONFIG":
			body, ok := configs[r.Form.Get("user")]
			if !ok {
				body = "error=1&text=Error&details=User+" + r.Form.Get("user") + "+does+not+exist"
			}
			_, _ = w.Write([]byte(body))
		default:
			_, _ = w.Write([]byte("error=1&text=Error&details=unknown+command"))
		}
	}
}

func TestPackageInUseTrue(t *testing.T) {
	ts := httptest.NewServer(daUsersHandler([]string{"alice", "bob"}, map[string]string{
		"alice": "username=alice&package=other",
		"bob":   "username=bob&package=whcms_spec_abc",
	}))
	defer ts.Close()
	c, _ := newTestClient(t, testConfig())
	inUse, err := c.PackageInUse(context.Background(), serverConfig(t, ts), "whcms_spec_abc")
	require.NoError(t, err)
	assert.True(t, inUse)
}

func TestPackageInUseFalse(t *testing.T) {
	ts := httptest.NewServer(daUsersHandler([]string{"alice"}, map[string]string{
		"alice": "username=alice&package=other",
	}))
	defer ts.Close()
	c, _ := newTestClient(t, testConfig())
	inUse, err := c.PackageInUse(context.Background(), serverConfig(t, ts), "whcms_spec_abc")
	require.NoError(t, err)
	assert.False(t, inUse)
}

// TestPackageInUseSkipsUserDeletedMidScan: a user listed by SHOW_USERS but
// already gone by the time its config is read (NotFound) must be skipped,
// not fail the whole check.
func TestPackageInUseSkipsUserDeletedMidScan(t *testing.T) {
	ts := httptest.NewServer(daUsersHandler([]string{"ghost", "bob"}, map[string]string{
		"bob": "username=bob&package=whcms_spec_abc",
	}))
	defer ts.Close()
	c, _ := newTestClient(t, testConfig())
	inUse, err := c.PackageInUse(context.Background(), serverConfig(t, ts), "whcms_spec_abc")
	require.NoError(t, err)
	assert.True(t, inUse)
}

func TestPackageInUseErrors(t *testing.T) {
	// Empty name is a local validation error.
	c, _ := newTestClient(t, testConfig())
	_, err := c.PackageInUse(context.Background(), ports.ServerConfig{Hostname: "x"}, "")
	requireAppErr(t, err, apperr.CodeValidation)

	// A non-NotFound per-user read failure surfaces (fail safe at the caller).
	ts := httptest.NewServer(daUsersHandler([]string{"alice"}, map[string]string{
		"alice": "error=1&text=Error&details=permission+denied",
	}))
	defer ts.Close()
	c, _ = newTestClient(t, testConfig())
	_, err = c.PackageInUse(context.Background(), serverConfig(t, ts), "whcms_spec_abc")
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestListPackagesSuccess(t *testing.T) {
	var req http.Request
	// Real DirectAdmin repeats the literal key "list[]" (confirmed against a
	// real server: forum.directadmin.com/threads/whmcs-directadmin-packages)
	// and carries no `error` key on success for this dump-style endpoint.
	ts := httptest.NewServer(legacyHandler(t, 200, "list[]=business&list[]=default", nil, &req))
	defer ts.Close()
	c, _ := newTestClient(t, testConfig())
	names, err := c.ListPackages(context.Background(), serverConfig(t, ts))
	require.NoError(t, err)
	assert.Equal(t, []string{"business", "default"}, names)
	assert.Equal(t, http.MethodGet, req.Method)
	assert.Equal(t, "/CMD_API_PACKAGES_USER", req.URL.Path)
}

func TestListPackagesEmpty(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "", nil, nil))
	defer ts.Close()
	c, _ := newTestClient(t, testConfig())
	names, err := c.ListPackages(context.Background(), serverConfig(t, ts))
	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestListPackagesErrorPropagates(t *testing.T) {
	ts := httptest.NewServer(legacyHandler(t, 200, "error=1&text=Bad&details=permission+denied", nil, nil))
	defer ts.Close()
	c, _ := newTestClient(t, testConfig())
	_, err := c.ListPackages(context.Background(), serverConfig(t, ts))
	requireAppErr(t, err, apperr.CodeExternal)
}
