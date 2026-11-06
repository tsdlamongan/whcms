package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/modules/auth"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memCache is an in-memory ports.Cache double that keeps the JSON bytes and
// the TTL each key was stored with.
type memCache struct {
	vals map[string][]byte
	ttls map[string]time.Duration
}

func newMemCache() (*memCache, *mocks.MockCache) {
	m := &memCache{vals: map[string][]byte{}, ttls: map[string]time.Duration{}}
	return m, &mocks.MockCache{
		GetJSONFn: func(_ context.Context, key string, out any) (bool, error) {
			raw, ok := m.vals[key]
			if !ok {
				return false, nil
			}
			return true, json.Unmarshal(raw, out)
		},
		SetJSONFn: func(_ context.Context, key string, val any, ttl time.Duration) error {
			raw, err := json.Marshal(val)
			m.vals[key] = raw
			m.ttls[key] = ttl
			return err
		},
		DeleteFn: func(_ context.Context, key string) error {
			delete(m.vals, key)
			return nil
		},
	}
}

func confirmFixture(t *testing.T) (*env, *memCache) {
	t.Helper()
	e := newFixture()
	mem, mock := newMemCache()
	e.cache = mock
	withUser(e, activeUser())
	return e, mem
}

const confirmKey = "auth:pwconfirm:10"

func TestConfirmPasswordOpensWindow(t *testing.T) {
	e, mem := confirmFixture(t)
	var resetKeys []string
	e.limiter.ResetFn = func(_ context.Context, key string) error { resetKeys = append(resetKeys, key); return nil }
	svc := e.service()

	st, err := svc.PasswordConfirmation(ctx, 10)
	require.NoError(t, err)
	assert.False(t, st.Confirmed, "nothing confirmed yet")
	assert.Nil(t, st.ExpiresAt)

	got, err := svc.ConfirmPassword(ctx, 10, auth.ConfirmPasswordRequest{Password: "correct-password"})
	require.NoError(t, err)
	assert.True(t, got.Confirmed)
	require.NotNil(t, got.ExpiresAt)
	assert.Equal(t, e.now.Add(15*time.Minute), *got.ExpiresAt)
	assert.Equal(t, 15*time.Minute, mem.ttls[confirmKey])
	assert.Equal(t, []string{"auth:pwconfirm:attempts:10"}, resetKeys, "success clears the failure counter")
	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.password_confirm", e.audit.Entries[0].Action)

	st, err = svc.PasswordConfirmation(ctx, 10)
	require.NoError(t, err)
	assert.True(t, st.Confirmed)
	require.NotNil(t, st.ExpiresAt)
	assert.Equal(t, e.now.Add(15*time.Minute), *st.ExpiresAt)
}

func TestConfirmPasswordWrongPassword(t *testing.T) {
	e, mem := confirmFixture(t)

	_, err := e.service().ConfirmPassword(ctx, 10, auth.ConfirmPasswordRequest{Password: "wrong"})
	assertCode(t, err, apperr.CodeUnauthorized)
	assert.Empty(t, mem.vals, "a wrong password must not open the window")
	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "auth.password_confirm_failed", e.audit.Entries[0].Action)
}

func TestConfirmPasswordValidation(t *testing.T) {
	e, _ := confirmFixture(t)
	_, err := e.service().ConfirmPassword(ctx, 10, auth.ConfirmPasswordRequest{})
	assertCode(t, err, apperr.CodeValidation)
}

func TestConfirmPasswordRateLimited(t *testing.T) {
	e, mem := confirmFixture(t)
	var limitKey string
	var limit int
	e.limiter.AllowFn = func(_ context.Context, key string, l int, _ time.Duration) (bool, error) {
		limitKey, limit = key, l
		return false, nil
	}
	e.hasher.VerifyFn = func(string, string) (bool, error) {
		t.Fatal("password must not be verified while locked out")
		return false, nil
	}

	_, err := e.service().ConfirmPassword(ctx, 10, auth.ConfirmPasswordRequest{Password: "correct-password"})
	assertCode(t, err, apperr.CodeRateLimited)
	assert.Equal(t, "auth:pwconfirm:attempts:10", limitKey)
	assert.Equal(t, 5, limit)
	assert.Empty(t, mem.vals)
}

func TestConfirmPasswordLimiterFailsOpen(t *testing.T) {
	e, _ := confirmFixture(t)
	e.limiter.AllowFn = func(context.Context, string, int, time.Duration) (bool, error) {
		return false, errors.New("redis down")
	}
	got, err := e.service().ConfirmPassword(ctx, 10, auth.ConfirmPasswordRequest{Password: "correct-password"})
	require.NoError(t, err)
	assert.True(t, got.Confirmed)
}

func TestConfirmPasswordFailurePaths(t *testing.T) {
	t.Run("unknown user", func(t *testing.T) {
		e, _ := confirmFixture(t)
		_, err := e.service().ConfirmPassword(ctx, 999, auth.ConfirmPasswordRequest{Password: "x"})
		assertCode(t, err, apperr.CodeNotFound)
	})
	t.Run("hasher error", func(t *testing.T) {
		e, _ := confirmFixture(t)
		e.hasher.VerifyFn = func(string, string) (bool, error) { return false, errors.New("boom") }
		_, err := e.service().ConfirmPassword(ctx, 10, auth.ConfirmPasswordRequest{Password: "x"})
		assertCode(t, err, apperr.CodeInternal)
	})
	t.Run("cache write error", func(t *testing.T) {
		e, _ := confirmFixture(t)
		e.cache = &mocks.MockCache{SetJSONFn: func(context.Context, string, any, time.Duration) error {
			return errors.New("redis down")
		}}
		_, err := e.service().ConfirmPassword(ctx, 10, auth.ConfirmPasswordRequest{Password: "correct-password"})
		assertCode(t, err, apperr.CodeInternal)
	})
	t.Run("cache not configured", func(t *testing.T) {
		e, _ := confirmFixture(t)
		e.cache = nil
		_, err := e.service().ConfirmPassword(ctx, 10, auth.ConfirmPasswordRequest{Password: "correct-password"})
		assertCode(t, err, apperr.CodeInternal)
		_, err = e.service().PasswordConfirmation(ctx, 10)
		assertCode(t, err, apperr.CodeInternal)
	})
}

func TestPasswordConfirmationExpiredAndErrors(t *testing.T) {
	t.Run("elapsed record is not confirmed", func(t *testing.T) {
		e, mem := confirmFixture(t)
		raw, _ := json.Marshal(map[string]any{"expires_at": e.now.Add(-time.Second)})
		mem.vals[confirmKey] = raw
		st, err := e.service().PasswordConfirmation(ctx, 10)
		require.NoError(t, err)
		assert.False(t, st.Confirmed)
	})
	t.Run("cache read error", func(t *testing.T) {
		e, _ := confirmFixture(t)
		e.cache = &mocks.MockCache{GetJSONFn: func(context.Context, string, any) (bool, error) {
			return false, errors.New("redis down")
		}}
		_, err := e.service().PasswordConfirmation(ctx, 10)
		assertCode(t, err, apperr.CodeInternal)
	})
}

func TestLogoutClearsPasswordConfirmation(t *testing.T) {
	e, mem := confirmFixture(t)
	svc := e.service()
	_, err := svc.ConfirmPassword(ctx, 10, auth.ConfirmPasswordRequest{Password: "correct-password"})
	require.NoError(t, err)
	require.Contains(t, mem.vals, confirmKey)

	require.NoError(t, svc.Logout(ctx, 10, ""))
	assert.NotContains(t, mem.vals, confirmKey, "logout must close the confirmation window")

	st, err := svc.PasswordConfirmation(ctx, 10)
	require.NoError(t, err)
	assert.False(t, st.Confirmed)

	// With no cache wired, logout still succeeds.
	e.cache = nil
	require.NoError(t, e.service().Logout(ctx, 10, ""))
}

func TestLoginClearsPasswordConfirmation(t *testing.T) {
	e, mem := confirmFixture(t)
	svc := e.service()
	_, err := svc.ConfirmPassword(ctx, 10, auth.ConfirmPasswordRequest{Password: "correct-password"})
	require.NoError(t, err)
	require.Contains(t, mem.vals, confirmKey)

	_, err = svc.Login(ctx, auth.LoginRequest{Email: "user@example.com", Password: "correct-password"})
	require.NoError(t, err)
	assert.NotContains(t, mem.vals, confirmKey, "a new sign-in must require confirming the password again")
}
