package rdash

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

// wireOf asserts err carries registrar wire detail and returns it.
func wireOf(t *testing.T, err error) *ports.RegistrarCallError {
	t.Helper()
	ce := ports.RegistrarCallErrorOf(err)
	require.NotNil(t, ce, "expected wire detail on %v", err)
	return ce
}

func TestRegisterForCustomerSuccessSkipsCustomerLookup(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/domains", r.URL.Path)
		requireBasicAuth(t, r)
		form := requireForm(t, r)
		assert.Equal(t, "drop.my.id", form.Get("name"))
		assert.Equal(t, "1", form.Get("period"))
		assert.Equal(t, "501", form.Get("customer_id"))
		assert.Equal(t, "ns1.rdash.id", form.Get("nameserver[0]"))
		writeEnv(w, http.StatusOK, "Domain registered successfully", map[string]any{
			"id": 77, "name": "drop.my.id", "status_label": "Pending", "expired_at": "2027-10-24",
		})
	})

	res, err := f.client.RegisterForCustomer(context.Background(), " Drop.My.ID ", 0, 501, []string{"ns1.rdash.id"})
	require.NoError(t, err)
	assert.Equal(t, "drop.my.id", res.Name)
	assert.Equal(t, "pending", res.Status)
	assert.Equal(t, "77", res.OrderID)
	assert.Equal(t, time.Date(2027, 10, 24, 0, 0, 0, 0, time.UTC), res.ExpiryDate)
	assert.Equal(t, int32(1), f.hits.Load(), "must be a single POST /domains: no customer lookup")
}

func TestRegisterForCustomerValidation(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("no HTTP call expected") })
	_, err := f.client.RegisterForCustomer(context.Background(), "  ", 1, 5, nil)
	requireAppErr(t, err, apperr.CodeValidation)
	_, err = f.client.RegisterForCustomer(context.Background(), "x.id", 1, 0, nil)
	requireAppErr(t, err, apperr.CodeValidation)
	assert.Zero(t, f.hits.Load())
}

func TestRegisterForCustomerErrorWireDetail(t *testing.T) {
	tests := []struct {
		name          string
		handler       http.HandlerFunc
		code          apperr.Code
		status        int
		retryAfter    time.Duration
		ambiguous     bool
		messageHas    string
		wantNoWireMsg bool
	}{
		{
			name: "taken is a definitive 409",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeEnv(w, http.StatusConflict, "Domain not available", nil)
			},
			code: apperr.CodeConflict, status: 409, messageHas: "Domain not available",
		},
		{
			name: "429 carries Retry-After seconds",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "3")
				writeEnv(w, http.StatusTooManyRequests, "Too Many Requests", nil)
			},
			code: apperr.CodeRateLimited, status: 429, retryAfter: 3 * time.Second, messageHas: "Too Many Requests",
		},
		{
			name: "422 validation is definitive",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeValidation(w, "Insufficient balance", nil)
			},
			code: apperr.CodeValidation, status: 422, messageHas: "Insufficient balance",
		},
		{
			name: "401 is definitive",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeEnv(w, http.StatusUnauthorized, "Unauthenticated", nil)
			},
			code: apperr.CodeExternal, status: 401, messageHas: "Unauthenticated",
		},
		{
			name: "5xx is ambiguous",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeEnv(w, http.StatusServiceUnavailable, "maintenance", nil)
			},
			code: apperr.CodeExternal, status: 503, ambiguous: true, messageHas: "maintenance",
		},
		{
			name: "non-envelope 4xx maps by status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("<html>blocked</html>"))
			},
			code: apperr.CodeExternal, status: 403, messageHas: http.StatusText(http.StatusForbidden),
		},
		{
			name: "2xx with unparseable body is ambiguous",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("not json"))
			},
			code: apperr.CodeExternal, status: 200, ambiguous: true, messageHas: "invalid response body",
		},
		{
			name: "2xx with undecodable data is ambiguous",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeEnv(w, http.StatusOK, "ok", "a string where an object belongs")
			},
			code: apperr.CodeExternal, status: 200, ambiguous: true, messageHas: "decode response data",
		},
		{
			name: "2xx with a bad expiry date is ambiguous",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "name": "x.id", "expired_at": "not-a-date"})
			},
			code: apperr.CodeExternal, status: 200, ambiguous: true, messageHas: "unreadable registration result",
		},
		{
			name: "2xx with success=false is a definitive failure, not ambiguous",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":false,"message":"refused","data":null}`))
			},
			code: apperr.CodeValidation, status: 200, messageHas: "refused",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.handler)
			_, err := f.client.RegisterForCustomer(context.Background(), "x.id", 1, 9, nil)
			requireAppErr(t, err, tt.code)
			ce := wireOf(t, err)
			assert.Equal(t, tt.status, ce.HTTPStatus)
			assert.Equal(t, tt.retryAfter, ce.RetryAfter)
			assert.Equal(t, tt.ambiguous, ce.Ambiguous)
			assert.False(t, ce.NotSent)
			assert.Contains(t, ce.Message, tt.messageHas)
			assert.EqualValues(t, 1, f.hits.Load(), "POST is never retried")
		})
	}
}

func TestRegisterForCustomerTransportErrorIsAmbiguous(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		require.True(t, ok)
		conn, _, err := hj.Hijack()
		require.NoError(t, err)
		_ = conn.Close() // the order may or may not have been placed: no response at all
	})
	_, err := f.client.RegisterForCustomer(context.Background(), "x.id", 1, 9, nil)
	requireAppErr(t, err, apperr.CodeExternal)
	ce := wireOf(t, err)
	assert.Zero(t, ce.HTTPStatus)
	assert.True(t, ce.Ambiguous)
	assert.NotNil(t, ce.Err)
}

func TestRegisterForCustomerCancelledContextKeepsCauseChain(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		// The server only notices the client going away once the request body
		// has been consumed.
		_ = r.ParseForm()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := f.client.RegisterForCustomer(ctx, "x.id", 1, 9, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "cancellation must stay visible through the wrapped chain: %v", err)
	assert.True(t, wireOf(t, err).Ambiguous)
}

func TestBreakerOpenIsReportedAsNotSent(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { writeEnv(w, http.StatusBadGateway, "boom", nil) },
		func(cfg *Config) { cfg.BreakerThreshold = 1; cfg.BreakerCooldown = time.Minute })

	_, err := f.client.RegisterForCustomer(context.Background(), "x.id", 1, 9, nil)
	assert.True(t, wireOf(t, err).Ambiguous, "the failing 5xx call itself is ambiguous")

	hits := f.hits.Load()
	_, err = f.client.RegisterForCustomer(context.Background(), "x.id", 1, 9, nil)
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "circuit breaker open")
	ce := wireOf(t, err)
	assert.True(t, ce.NotSent)
	assert.False(t, ce.Ambiguous)
	assert.Equal(t, hits, f.hits.Load(), "nothing may reach the wire while open")
}

func TestIsAvailable(t *testing.T) {
	t.Run("available", func(t *testing.T) {
		f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/domains/availability", r.URL.Path)
			assert.Equal(t, "free.id", r.URL.Query().Get("domain"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"name": "free.id", "available": 1})
		})
		ok, err := f.client.IsAvailable(context.Background(), " Free.ID ")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, int32(1), f.hits.Load(), "no price lookup")
	})
	t.Run("taken", func(t *testing.T) {
		f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"name": "taken.id", "available": 0})
		})
		ok, err := f.client.IsAvailable(context.Background(), "taken.id")
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("empty name", func(t *testing.T) {
		f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("no HTTP call expected") })
		_, err := f.client.IsAvailable(context.Background(), " ")
		requireAppErr(t, err, apperr.CodeValidation)
	})
	t.Run("error propagates with wire detail", func(t *testing.T) {
		f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "1")
			writeEnv(w, http.StatusTooManyRequests, "slow down", nil)
		})
		_, err := f.client.IsAvailable(context.Background(), "x.id")
		requireAppErr(t, err, apperr.CodeRateLimited)
		assert.Equal(t, time.Second, wireOf(t, err).RetryAfter)
	})
}

func TestEnsureCustomerExported(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", []any{map[string]any{"id": 42, "email": "jane@example.test"}})
		},
	}))
	id, err := f.client.EnsureCustomer(context.Background(), sampleContact())
	require.NoError(t, err)
	assert.EqualValues(t, 42, id)
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"  ", 0},
		{"5", 5 * time.Second},
		{" 2 ", 2 * time.Second},
		{"0", 0},
		{"-3", 0},
		{"soon", 0},
		{now.Add(90 * time.Second).UTC().Format(http.TimeFormat), 90 * time.Second},
		{now.Add(-time.Minute).UTC().Format(http.TimeFormat), 0},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, parseRetryAfter(tt.in, now))
		})
	}
}

func TestCacheCredentials(t *testing.T) {
	clock := &mocks.MockClock{FixedTime: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	calls := 0
	fail := false
	resolve := func(context.Context) (Credentials, error) {
		calls++
		if fail {
			return Credentials{}, errBoom
		}
		return Credentials{ResellerID: "r" + strconv.Itoa(calls)}, nil
	}
	cached := CacheCredentials(resolve, 10*time.Second, clock)

	c, err := cached(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "r1", c.ResellerID)

	clock.FixedTime = clock.FixedTime.Add(5 * time.Second)
	c, err = cached(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "r1", c.ResellerID, "served from cache inside the TTL")
	assert.Equal(t, 1, calls)

	clock.FixedTime = clock.FixedTime.Add(6 * time.Second)
	c, err = cached(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "r2", c.ResellerID, "refreshed after the TTL")

	fail = true
	clock.FixedTime = clock.FixedTime.Add(time.Minute)
	_, err = cached(context.Background())
	assert.ErrorIs(t, err, errBoom)
	fail = false
	c, err = cached(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "r4", c.ResellerID, "a failed refresh is not cached")
}

func TestCacheCredentialsNilClockUsesSystemTime(t *testing.T) {
	calls := 0
	cached := CacheCredentials(func(context.Context) (Credentials, error) {
		calls++
		return Credentials{}, nil
	}, time.Hour, nil)
	_, _ = cached(context.Background())
	_, _ = cached(context.Background())
	assert.Equal(t, 1, calls)
}
