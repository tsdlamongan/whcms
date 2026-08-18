package cpanel

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

func testPackageSpec() ports.PackageSpec {
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

func TestEnsurePackage_Create(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "addpkg", nil)
	})

	err := c.EnsurePackage(context.Background(), s, testPackageSpec())
	require.NoError(t, err)

	require.Equal(t, 1, rec.count())
	req := rec.req(0)
	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "/json-api/addpkg", req.Path)
	assert.Equal(t, "whcms_s42", req.Form.Get("name"))
	assert.Equal(t, "default", req.Form.Get("featurelist"))
	assert.Equal(t, "0", req.Form.Get("hasshell"))
	assert.Equal(t, "0", req.Form.Get("cgi"))
	assert.Equal(t, "10240", req.Form.Get("quota"))
	assert.Equal(t, "unlimited", req.Form.Get("bwlimit"))
	assert.Equal(t, "5", req.Form.Get("maxaddon"))
	assert.Equal(t, "10", req.Form.Get("maxsub"))
	assert.Equal(t, "2", req.Form.Get("maxpark"))
	assert.Equal(t, "50", req.Form.Get("maxpop"))
	assert.Equal(t, "20", req.Form.Get("maxsql"))
	assert.Equal(t, "8", req.Form.Get("maxftp"))
}

func TestEnsurePackage_CustomFeatureList(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "addpkg", nil)
	})
	spec := testPackageSpec()
	spec.FeatureList = "premium"
	require.NoError(t, c.EnsurePackage(context.Background(), s, spec))
	assert.Equal(t, "premium", rec.req(0).Form.Get("featurelist"))
}

func TestEnsurePackage_ShellAndCGIAccess(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "addpkg", nil)
	})
	spec := testPackageSpec()
	spec.ShellAccess = true
	spec.CGIAccess = true
	require.NoError(t, c.EnsurePackage(context.Background(), s, spec))
	assert.Equal(t, "1", rec.req(0).Form.Get("hasshell"))
	assert.Equal(t, "1", rec.req(0).Form.Get("cgi"))
}

func TestEnsurePackage_EditsWhenExists(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "addpkg") {
			whmFail(w, "addpkg", "package already exists")
			return
		}
		whmOK(w, "editpkg", nil)
	})

	require.NoError(t, c.EnsurePackage(context.Background(), s, testPackageSpec()))
	require.Equal(t, 2, rec.count())
	assert.Equal(t, "/json-api/addpkg", rec.req(0).Path)
	assert.Equal(t, "/json-api/editpkg", rec.req(1).Path)
	assert.Equal(t, "whcms_s42", rec.req(1).Form.Get("name"))
}

func TestEnsurePackage_InvalidName(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "addpkg", nil)
	})
	spec := testPackageSpec()
	spec.Name = "bad name!"
	err := c.EnsurePackage(context.Background(), s, spec)
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, err).Code)
	assert.Equal(t, 0, rec.count())
}

func TestEnsurePackage_GenericErrorPropagates(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "addpkg", "quota out of range")
	})
	err := c.EnsurePackage(context.Background(), s, testPackageSpec())
	assert.Equal(t, apperr.CodeExternal, asAppErr(t, err).Code)
}

func TestDeletePackage_Success(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "killpkg", nil)
	})
	require.NoError(t, c.DeletePackage(context.Background(), s, "whcms_s42"))
	req := rec.req(0)
	assert.Equal(t, "/json-api/killpkg", req.Path)
	assert.Equal(t, "whcms_s42", req.Form.Get("pkg"))
}

func TestDeletePackage_NotFoundIsSuccess(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "killpkg", "package does not exist")
	})
	assert.NoError(t, c.DeletePackage(context.Background(), s, "whcms_s404"))
}

func TestDeletePackage_EmptyName(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {})
	err := c.DeletePackage(context.Background(), s, "")
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, err).Code)
}

func TestDeletePackage_ErrorPropagates(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "killpkg", "permission denied")
	})
	err := c.DeletePackage(context.Background(), s, "whcms_s42")
	assert.Equal(t, apperr.CodeExternal, asAppErr(t, err).Code)
}

func TestPackageInUse_True(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "listaccts", map[string]any{
			"acct": []map[string]any{{"user": "alice", "plan": "whcms_spec_abc"}},
		})
	})
	inUse, err := c.PackageInUse(context.Background(), s, "whcms_spec_abc")
	require.NoError(t, err)
	assert.True(t, inUse)
	req := rec.req(0)
	assert.Equal(t, http.MethodGet, req.Method)
	assert.Equal(t, "/json-api/listaccts", req.Path)
	assert.Equal(t, "package", req.Query.Get("searchtype"))
	assert.Equal(t, "^whcms_spec_abc$", req.Query.Get("search"), "exact-name anchored regex")
}

func TestPackageInUse_False(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "listaccts", map[string]any{"acct": []map[string]any{}})
	})
	inUse, err := c.PackageInUse(context.Background(), s, "whcms_spec_abc")
	require.NoError(t, err)
	assert.False(t, inUse)
}

func TestPackageInUse_Errors(t *testing.T) {
	// Empty name is a local validation error, no HTTP call.
	c := New(Config{}, nil, nil, nil)
	_, err := c.PackageInUse(context.Background(), ports.ServerConfig{}, "")
	assert.Equal(t, apperr.CodeValidation, asAppErr(t, err).Code)

	// API-level failure surfaces as EXTERNAL.
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "listaccts", "internal error")
	})
	_, err = c.PackageInUse(context.Background(), s, "whcms_spec_abc")
	assert.Equal(t, apperr.CodeExternal, asAppErr(t, err).Code)
}

func TestListPackages_Success(t *testing.T) {
	c, s, rec, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		// Real WHM nests the array under "pkg", not "package" - this exact
		// shape is what production WHM returns (confirmed against
		// api.docs.cpanel.net / `whmapi1 listpkgs | yq '.data.pkg[].name'`).
		whmOK(w, "listpkgs", map[string]any{
			"pkg": []map[string]any{
				{"name": "business"},
				{"name": "default"},
			},
		})
	})
	names, err := c.ListPackages(context.Background(), s)
	require.NoError(t, err)
	assert.Equal(t, []string{"business", "default"}, names)
	assert.Equal(t, http.MethodGet, rec.req(0).Method)
	assert.Equal(t, "/json-api/listpkgs", rec.req(0).Path)
}

func TestListPackages_Empty(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmOK(w, "listpkgs", map[string]any{"pkg": []map[string]any{}})
	})
	names, err := c.ListPackages(context.Background(), s)
	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestListPackages_TransportError(t *testing.T) {
	c, s, _, _ := newHarness(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		whmFail(w, "listpkgs", "internal error")
	})
	_, err := c.ListPackages(context.Background(), s)
	assert.Equal(t, apperr.CodeExternal, asAppErr(t, err).Code)
}
