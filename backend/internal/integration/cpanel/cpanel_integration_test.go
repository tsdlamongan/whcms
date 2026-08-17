//go:build integration

package cpanel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// mockWHMAddr returns the host:port of the mockserver WHM (README §2),
// overridable via CPANEL_MOCK_ADDR.
func mockWHMAddr() string {
	if v := os.Getenv("CPANEL_MOCK_ADDR"); v != "" {
		return v
	}
	return "localhost:9090"
}

// TestIntegration_MockserverWHM drives the adapter end-to-end against the
// REAL mockserver WHM implementation (not httptest fixtures): full account
// lifecycle plus the documented error behaviors (duplicate account, unknown
// account, bad token, forced failure). It creates its own uniquely-named
// account and skips automatically when the mockserver is not running.
func TestIntegration_MockserverWHM(t *testing.T) {
	addr := mockWHMAddr()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Skipf("mockserver WHM not reachable at %s: %v", addr, err)
	}
	_ = conn.Close()

	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port := 0
	for _, ch := range portStr {
		port = port*10 + int(ch-'0')
	}

	ctx := context.Background()
	c := New(Config{}, nil, nil, nil)
	s := ports.ServerConfig{
		ID: 1, Name: "mock-whm", Module: domain.ModuleCpanel,
		Hostname: host, Port: port,
		Username: "root", APIToken: "goodtoken", UseSSL: false,
	}

	// Unique fixture (shared mockserver state; never reset other agents' data).
	// cPanel usernames: lowercase alnum, letter first, max 16 chars.
	suffix := make([]byte, 6)
	_, err = rand.Read(suffix)
	require.NoError(t, err)
	username := "vf" + hex.EncodeToString(suffix) // 2+12 = 14 chars
	domainName := username + ".example.com"

	acct := ports.CreateAccountParams{
		Username: username, Domain: domainName,
		Password: "Vf!" + hex.EncodeToString(suffix), Package: "gold",
		Email: username + "@example.com",
	}

	res, err := c.Create(ctx, s, acct)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, username, res.Username)
	assert.Equal(t, domainName, res.Domain)

	_, err = c.Create(ctx, s, acct)
	assert.Equal(t, apperr.CodeConflict, apperr.From(err).Code, "duplicate createacct must map to CONFLICT: %v", err)

	info, err := c.AccountInfo(ctx, s, username)
	require.NoError(t, err)
	assert.Equal(t, username, info.Username)
	assert.Equal(t, domainName, info.Domain)
	assert.False(t, info.Suspended)

	require.NoError(t, c.Suspend(ctx, s, username, "Overdue on payment"))
	info, err = c.AccountInfo(ctx, s, username)
	require.NoError(t, err)
	assert.True(t, info.Suspended, "accountsummary must report suspended=1 after suspendacct")
	assert.Equal(t, "Overdue on payment", info.Meta["suspendreason"])

	require.NoError(t, c.Unsuspend(ctx, s, username))
	require.NoError(t, c.ChangePackage(ctx, s, username, "platinum"))
	info, err = c.AccountInfo(ctx, s, username)
	require.NoError(t, err)
	assert.False(t, info.Suspended)
	assert.Equal(t, "platinum", info.Package)

	require.NoError(t, c.ChangePassword(ctx, s, username, "N3w!"+hex.EncodeToString(suffix)))

	ssoURL, err := c.SSOURL(ctx, s, username)
	require.NoError(t, err)
	assert.NotEmpty(t, ssoURL)

	require.NoError(t, c.Terminate(ctx, s, username))
	_, err = c.AccountInfo(ctx, s, username)
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code, "terminated account must map to NOT_FOUND: %v", err)

	// A retried ProvisionTerminate job (e.g. after a failure elsewhere in the
	// same job run) calls Terminate again on an already-gone account -
	// against the real mockserver WHM, this must still succeed (idempotent),
	// not fail forever at the exact same step.
	require.NoError(t, c.Terminate(ctx, s, username),
		"a second Terminate on an already-removed account must be idempotent, not error")

	err = c.Suspend(ctx, s, username+"x", "x")
	assert.Equal(t, apperr.CodeNotFound, apperr.From(err).Code, "unknown account must map to NOT_FOUND: %v", err)

	bad := s
	bad.APIToken = "badtoken"
	_, err = c.AccountInfo(ctx, bad, username)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code, "403 bad token must map to EXTERNAL: %v", err)

	forced := acct
	forced.Username = "failme"
	forced.Domain = "failme-" + hex.EncodeToString(suffix) + ".example.com"
	_, err = c.Create(ctx, s, forced)
	assert.Equal(t, apperr.CodeExternal, apperr.From(err).Code, "forced result:0 failure must map to EXTERNAL: %v", err)
}
