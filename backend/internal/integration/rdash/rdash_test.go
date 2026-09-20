package rdash

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
)

const (
	testResellerID = "RSL-1"
	testAPIKey     = "apikey-1"
)

var errBoom = errors.New("boom")

// writeEnv writes the Dewabiz {success, data, message} envelope, HTTP status
// mirroring success, like the real API and the mockserver.
func writeEnv(w http.ResponseWriter, status int, message string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": status < 400,
		"data":    data,
		"message": message,
	})
}

// writeValidation writes a 422 {success:false, message, errors} envelope.
func writeValidation(w http.ResponseWriter, message string, errs map[string][]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"message": message,
		"errors":  errs,
	})
}

// requireBasicAuth asserts the adapter sent the configured credentials.
func requireBasicAuth(t *testing.T, r *http.Request) {
	t.Helper()
	user, pass, ok := r.BasicAuth()
	require.True(t, ok, "expected basic auth header")
	assert.Equal(t, testResellerID, user)
	assert.Equal(t, testAPIKey, pass)
}

// requireForm parses and returns the request's form body, asserting the
// registrar's documented content type.
func requireForm(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
	require.NoError(t, r.ParseForm())
	return r.PostForm
}

type fixture struct {
	client *Client
	logger *mocks.MockIntegrationLogger
	srv    *httptest.Server
	hits   *atomic.Int32
}

// newFixture spins up an httptest server, counting hits, with a fast-test
// client (1ms retry delay) pointed at it.
func newFixture(t *testing.T, handler http.HandlerFunc, mutate ...func(*Config)) *fixture {
	t.Helper()
	hits := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		BaseURL:    srv.URL,
		ResellerID: testResellerID,
		APIKey:     testAPIKey,
		RetryDelay: time.Millisecond,
	}
	for _, m := range mutate {
		m(&cfg)
	}
	logger := &mocks.MockIntegrationLogger{}
	return &fixture{
		client: New(cfg, nil, srv.Client(), logger, nil),
		logger: logger,
		srv:    srv,
		hits:   hits,
	}
}

// routes builds a method+path-routed handler (Go 1.22+ ServeMux patterns),
// used by tests whose op makes more than one HTTP call. Every Register/
// Transfer op now looks up an existing customer by email before creating one
// (findCustomerByEmail) - default that lookup to "not found" (an empty list)
// so the many existing register/transfer tests below don't all need their
// own "GET /customers" entry; a test that cares about the lookup itself can
// still override it via its own map entry.
func routes(t *testing.T, m map[string]http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	mux := http.NewServeMux()
	if _, ok := m["GET /customers"]; !ok {
		mux.HandleFunc("GET /customers", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		})
	}
	for pattern, h := range m {
		mux.HandleFunc(pattern, h)
	}
	return mux.ServeHTTP
}

func requireAppErr(t *testing.T, err error, code apperr.Code) *apperr.Error {
	t.Helper()
	require.Error(t, err)
	ae := apperr.From(err)
	require.Equal(t, code, ae.Code, "unexpected apperr code: %v", err)
	return ae
}

// sampleContact is a fully populated registrant contact used across tests.
func sampleContact() ports.RegistrantContact {
	return ports.RegistrantContact{
		FirstName: "Jane", LastName: "Doe", Company: "Acme",
		Email: "jane@example.test", Phone: "081234567890",
		Address1: "Jl. Merdeka 1", City: "Jakarta", State: "DKI Jakarta",
		Postcode: "12345", Country: "id",
	}
}

// Constructor

func TestNewAppliesDefaults(t *testing.T) {
	c := New(Config{ResellerID: "r", APIKey: "k"}, nil, nil, nil, nil)
	assert.Equal(t, defaultBaseURL, c.cfg.BaseURL)
	assert.Equal(t, defaultTimeout, c.cfg.Timeout)
	assert.Equal(t, defaultRetryDelay, c.cfg.RetryDelay)
	assert.Equal(t, defaultBreakerThreshold, c.cfg.BreakerThreshold)
	assert.Equal(t, defaultBreakerCooldown, c.cfg.BreakerCooldown)
	require.NotNil(t, c.hc)
	assert.Equal(t, defaultTimeout, c.hc.Timeout)
	assert.NotNil(t, c.log)
	assert.NotNil(t, c.clock)
	assert.False(t, c.clock.Now().IsZero())
}

func TestNewTrimsBaseURLSlash(t *testing.T) {
	c := New(Config{BaseURL: "http://example.test/v1/"}, nil, nil, nil, nil)
	assert.Equal(t, "http://example.test/v1", c.cfg.BaseURL)
}

// CheckAvailability

func TestCheckAvailability(t *testing.T) {
	// Names are checked concurrently (one goroutine per name), so the shared
	// bookkeeping below and the resulting assertions must not assume a fixed
	// arrival order.
	var mu sync.Mutex
	var seen, priceLookups []string
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/availability": func(w http.ResponseWriter, r *http.Request) {
			requireBasicAuth(t, r)
			name := r.URL.Query().Get("domain")
			mu.Lock()
			seen = append(seen, name)
			mu.Unlock()
			available := 1
			if strings.Contains(name, "taken") {
				available = 0
			}
			writeEnv(w, http.StatusOK, "Success", map[string]any{
				"name": name, "available": available, "message": "ok",
			})
		},
		"GET /account/prices": func(w http.ResponseWriter, r *http.Request) {
			ext := r.URL.Query().Get("domainExtension[extension]")
			mu.Lock()
			priceLookups = append(priceLookups, ext)
			mu.Unlock()
			writeEnv(w, http.StatusOK, "ok", []map[string]any{{
				"domain_extension": map[string]any{"extension": ext},
				"registration":     map[string]any{"1": 150000},
			}})
		},
	}))

	res, err := f.client.CheckAvailability(context.Background(), []string{" Available.com ", "TakenDomain.id", "", "web.co.id"})
	require.NoError(t, err)
	// Names arrive normalized (trimmed + lowercased, empties dropped), one GET
	// per name - order preserves the input, but the results are built from
	// concurrent goroutines matched back up by index.
	assert.Equal(t, []string{"available.com", "takendomain.id", "web.co.id"}, seenSorted(seen))
	// One price lookup per distinct extension, cached across names.
	assert.ElementsMatch(t, []string{".com", ".id", ".co.id"}, priceLookups)
	assert.Equal(t, int32(6), f.hits.Load())
	require.Len(t, res, 3)
	assert.Equal(t, ports.DomainAvailability{Name: "available.com", Available: true, Price: 150000}, res[0])
	assert.Equal(t, ports.DomainAvailability{Name: "takendomain.id", Available: false, Price: 150000}, res[1])
	assert.Equal(t, ports.DomainAvailability{Name: "web.co.id", Available: true, Price: 150000}, res[2])
}

// seenSorted returns a stable-sorted copy for order-independent comparison
// (requests race across goroutines, so arrival order isn't guaranteed).
func seenSorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestCheckAvailabilityPriceCachedPerExtension(t *testing.T) {
	var priceHits atomic.Int32
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/availability": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"name": r.URL.Query().Get("domain"), "available": 1})
		},
		"GET /account/prices": func(w http.ResponseWriter, r *http.Request) {
			priceHits.Add(1)
			writeEnv(w, http.StatusOK, "ok", []map[string]any{{
				"domain_extension": map[string]any{"extension": ".id"},
				"registration":     map[string]any{"1": 25000},
			}})
		},
	}))
	res, err := f.client.CheckAvailability(context.Background(), []string{"one.id", "two.id", "three.id"})
	require.NoError(t, err)
	require.Len(t, res, 3)
	for _, r := range res {
		assert.Equal(t, int64(25000), r.Price)
	}
	assert.Equal(t, int32(1), priceHits.Load())
}

func TestCheckAvailabilityNoPriceOnFile(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/availability": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"name": "x.zz", "available": 1})
		},
		"GET /account/prices": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", []map[string]any{})
		},
	}))
	res, err := f.client.CheckAvailability(context.Background(), []string{"x.zz"})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Zero(t, res[0].Price)
}

func TestCheckAvailabilityPriceLookupPropagatesError(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/availability": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"name": "x.id", "available": 1})
		},
		"GET /account/prices": func(w http.ResponseWriter, r *http.Request) { writeEnv(w, http.StatusInternalServerError, "boom", nil) },
	}))
	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestToInt64Lenient(t *testing.T) {
	n, ok := toInt64Lenient(float64(25000))
	assert.True(t, ok)
	assert.Equal(t, int64(25000), n)
	n, ok = toInt64Lenient(" 25000 ")
	assert.True(t, ok)
	assert.Equal(t, int64(25000), n)
	_, ok = toInt64Lenient("not-a-number")
	assert.False(t, ok)
	_, ok = toInt64Lenient(true)
	assert.False(t, ok)
}

func TestExtensionOf(t *testing.T) {
	assert.Equal(t, ".co.id", extensionOf("example.co.id"))
	assert.Equal(t, ".com", extensionOf("example.com"))
	assert.Equal(t, "", extensionOf("nodot"))
}

func TestCheckAvailabilityEmptyNames(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no HTTP call expected")
	})
	_, err := f.client.CheckAvailability(context.Background(), []string{"", "  "})
	requireAppErr(t, err, apperr.CodeValidation)
	assert.Zero(t, f.hits.Load())
}

func TestCheckAvailabilityNullData(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "Success", nil)
	})
	res, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, ports.DomainAvailability{Name: "x.id", Available: false}, res[0])
}

// Register

func TestRegisterSuccess(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			requireBasicAuth(t, r)
			form := requireForm(t, r)
			assert.Equal(t, "jane@example.test", form.Get("email"))
			assert.Equal(t, "Acme", form.Get("organization"))
			assert.Equal(t, "ID", form.Get("country_code"))
			assert.NotEmpty(t, form.Get("password"))
			assert.Equal(t, form.Get("password"), form.Get("password_confirmation"))
			writeEnv(w, http.StatusOK, "Customer created successfully", map[string]any{"id": 501})
		},
		"POST /domains": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "example.my.id", form.Get("name"))
			assert.Equal(t, "2", form.Get("period"))
			assert.Equal(t, "501", form.Get("customer_id"))
			assert.Equal(t, "ns1.rdash.id", form.Get("nameserver[0]"))
			assert.Equal(t, "ns2.rdash.id", form.Get("nameserver[1]"))
			writeEnv(w, http.StatusOK, "Domain registered successfully", map[string]any{
				"id": 269, "name": "example.my.id", "nameserver_1": "ns1.rdash.id", "nameserver_2": "ns2.rdash.id",
				"status": nil, "status_label": "Active", "expired_at": "2028-04-13",
			})
		},
	}))

	res, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{
		Name: " Example.My.ID ", Years: 2, NS: []string{"ns1.rdash.id", "ns2.rdash.id"}, Contact: sampleContact(),
	})
	require.NoError(t, err)
	assert.Equal(t, "example.my.id", res.Name)
	assert.Equal(t, "active", res.Status)
	assert.Equal(t, "269", res.OrderID)
	assert.Equal(t, time.Date(2028, 4, 13, 0, 0, 0, 0, time.UTC), res.ExpiryDate)
	assert.Equal(t, int32(3), f.hits.Load()) // GET /customers (lookup) + POST /customers + POST /domains
}

func TestRegisterDefaultsYearsToOne(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"POST /domains": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "1", form.Get("period"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "name": "x.id", "status_label": "Active"})
		},
	}))
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{Name: "x.id", Contact: sampleContact()})
	require.NoError(t, err)
}

func TestRegisterMoreThanFiveNameserversCapped(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"POST /domains": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "ns6.example.com", form.Get("nameserver[4]"))
			assert.Empty(t, form.Get("nameserver[5]"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "name": "x.id", "status_label": "Active"})
		},
	}))
	ns := []string{"ns1.example.com", "ns2.example.com", "ns3.example.com", "ns4.example.com", "ns6.example.com", "ns7.example.com"}
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{Name: "x.id", NS: ns, Contact: sampleContact()})
	require.NoError(t, err)
}

func TestRegisterTakenMapsConflict(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"POST /domains": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusConflict, "domain not available", nil)
		},
	}))
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{Name: "taken.id", Contact: sampleContact()})
	requireAppErr(t, err, apperr.CodeConflict)
}

func TestRegisterEmptyName(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("no HTTP call expected") })
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{Name: "  ", Contact: sampleContact()})
	requireAppErr(t, err, apperr.CodeValidation)
	assert.Zero(t, f.hits.Load())
}

func TestRegisterCustomerCreationFailurePropagates(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeValidation(w, "The email field is required.", map[string][]string{"email": {"The email field is required."}})
		},
		"POST /domains": func(w http.ResponseWriter, r *http.Request) { t.Fatal("domain should not be registered") },
	}))
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{Name: "x.id", Contact: sampleContact()})
	ae := requireAppErr(t, err, apperr.CodeValidation)
	require.Len(t, ae.Details, 1)
	assert.Equal(t, "email", ae.Details[0].Field)
	assert.Equal(t, int32(2), f.hits.Load()) // GET /customers (lookup) + POST /customers
}

func TestRegisterBadExpiryDate(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"POST /domains": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "name": "x.id", "expired_at": "not-a-date"})
		},
	}))
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{Name: "x.id", Contact: sampleContact()})
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestRegisterMissingExpiryDateYieldsZeroTime(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"POST /domains": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "name": "x.id", "status_label": "Active"})
		},
	}))
	res, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{Name: "x.id", Contact: sampleContact()})
	require.NoError(t, err)
	assert.True(t, res.ExpiryDate.IsZero())
}

// Dewabiz rejects a second POST /customers for an email that already has a
// customer on this reseller ("Email has already on this reseller") - a
// client's second, third, ... domain must reuse the id GET /customers?email=
// already reports instead of trying (and failing) to create a duplicate.
func TestRegisterReusesExistingCustomerByEmail(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /customers": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "jane@example.test", r.URL.Query().Get("email"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": 501, "email": "jane@example.test"}},
			})
		},
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("must not create a duplicate customer when one already exists")
		},
		"POST /domains": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "501", form.Get("customer_id"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "name": "x.id", "status_label": "Active"})
		},
	}))
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{
		Name: "x.id", Contact: sampleContact(),
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), f.hits.Load()) // GET /customers (lookup, hit) + POST /domains
}

// The lookup only reuses an EXACT (case-insensitive) email match - Dewabiz's
// own filter should already guarantee this, but the adapter must not trust a
// loosely-matching or empty result set as "found".
func TestRegisterCustomerLookupNoMatchCreatesFresh(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /customers": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		},
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 77})
		},
		"POST /domains": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "77", form.Get("customer_id"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "name": "x.id", "status_label": "Active"})
		},
	}))
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{
		Name: "x.id", Contact: sampleContact(),
	})
	require.NoError(t, err)
}

func TestRegisterCustomerLookupFailurePropagates(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusInternalServerError, "boom", nil)
		},
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("must not attempt to create while the lookup itself failed")
		},
	}))
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{
		Name: "x.id", Contact: sampleContact(),
	})
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestFindCustomerByEmailEmptyEmailSkipsLookup(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("no HTTP call expected") })
	id, err := f.client.findCustomerByEmail(context.Background(), "")
	require.NoError(t, err)
	assert.Zero(t, id)
}

func TestRegisterCustomerNameFallsBackToEmail(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "jane@example.test", form.Get("name"))
			assert.Equal(t, "jane@example.test", form.Get("organization"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"POST /domains": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "name": "x.id", "status_label": "Active"})
		},
	}))
	contact := ports.RegistrantContact{Email: "jane@example.test"}
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{Name: "x.id", Contact: contact})
	require.NoError(t, err)
}

// Transfer

func TestTransferSuccess(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 9})
		},
		"POST /domains/transfer": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "in-transfer.id", form.Get("name"))
			assert.Equal(t, "EPP-CODE-1", form.Get("auth_code"))
			assert.Equal(t, "9", form.Get("customer_id"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{
				"id": 300, "name": "in-transfer.id", "status_label": "Pending Transfer", "expired_at": "2027-01-01",
			})
		},
	}))
	res, err := f.client.Transfer(context.Background(), ports.TransferDomainRequest{
		Name: "in-transfer.id", Years: 1, Contact: sampleContact(), EPPCode: "EPP-CODE-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "pending transfer", res.Status)
	assert.Equal(t, "300", res.OrderID)
}

func TestTransferEmptyEPPCode(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("no HTTP call expected") })
	_, err := f.client.Transfer(context.Background(), ports.TransferDomainRequest{Name: "x.id", Contact: sampleContact()})
	requireAppErr(t, err, apperr.CodeValidation)
	assert.Zero(t, f.hits.Load())
}

func TestTransferServerRejectsAuthCode(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"POST /domains/transfer": func(w http.ResponseWriter, r *http.Request) {
			writeValidation(w, "The auth code is invalid.", map[string][]string{"auth_code": {"The auth code is invalid."}})
		},
	}))
	_, err := f.client.Transfer(context.Background(), ports.TransferDomainRequest{Name: "x.id", Contact: sampleContact(), EPPCode: "WRONG"})
	requireAppErr(t, err, apperr.CodeValidation)
}

func TestTransferDuplicateMapsConflict(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"POST /domains/transfer": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusConflict, "already in account", nil)
		},
	}))
	_, err := f.client.Transfer(context.Background(), ports.TransferDomainRequest{Name: "x.id", Contact: sampleContact(), EPPCode: "E"})
	requireAppErr(t, err, apperr.CodeConflict)
}

func TestTransferEmptyName(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("no HTTP call expected") })
	_, err := f.client.Transfer(context.Background(), ports.TransferDomainRequest{Name: " ", Contact: sampleContact(), EPPCode: "E"})
	requireAppErr(t, err, apperr.CodeValidation)
	assert.Zero(t, f.hits.Load())
}

// Renew

func TestRenewSuccess(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "renew-me.id", r.URL.Query().Get("domain_name"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 77, "name": "renew-me.id", "expired_at": "2026-04-13"})
		},
		"POST /domains/77/renew": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "2026-04-13", form.Get("current_date"))
			assert.Equal(t, "3", form.Get("period"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 77, "name": "renew-me.id", "status_label": "Active", "expired_at": "2029-04-13"})
		},
	}))
	res, err := f.client.Renew(context.Background(), "Renew-Me.ID", 3)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2029, 4, 13, 0, 0, 0, 0, time.UTC), res.ExpiryDate)
}

func TestRenewDefaultsYearsToOne(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "expired_at": "2026-01-01"})
		},
		"POST /domains/1/renew": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "1", form.Get("period"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "status_label": "Active", "expired_at": "2027-01-01"})
		},
	}))
	_, err := f.client.Renew(context.Background(), "x.id", 0)
	require.NoError(t, err)
}

func TestRenewMissingExpiryUsesClockNow(t *testing.T) {
	fixedClock := &mocks.MockClock{FixedTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"POST /domains/1/renew": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "2026-06-01", form.Get("current_date"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "status_label": "Active"})
		},
	}))
	f.client.clock = fixedClock
	_, err := f.client.Renew(context.Background(), "x.id", 1)
	require.NoError(t, err)
}

func TestRenewUnknownDomainMapsNotFound(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusNotFound, "domain not found", nil)
	})
	_, err := f.client.Renew(context.Background(), "unknown.id", 1)
	requireAppErr(t, err, apperr.CodeNotFound)
}

func TestRenewEmptyName(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("no HTTP call expected") })
	_, err := f.client.Renew(context.Background(), " ", 1)
	requireAppErr(t, err, apperr.CodeValidation)
	assert.Zero(t, f.hits.Load())
}

// Nameservers

func TestGetNameservers(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/domains/details", r.URL.Path)
		writeEnv(w, http.StatusOK, "ok", map[string]any{
			"id": 1, "nameserver_1": "ns1.example.com", "nameserver_2": "ns2.example.com", "nameserver_3": nil,
		})
	})
	ns, err := f.client.GetNameservers(context.Background(), "x.id")
	require.NoError(t, err)
	assert.Equal(t, []string{"ns1.example.com", "ns2.example.com"}, ns)
}

func TestUpdateNameservers(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 42})
		},
		"PUT /domains/42/ns": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "ns1.new.com", form.Get("nameserver[0]"))
			assert.Equal(t, "ns2.new.com", form.Get("nameserver[1]"))
			writeEnv(w, http.StatusOK, "ok", nil)
		},
	}))
	err := f.client.UpdateNameservers(context.Background(), "x.id", []string{"ns1.new.com", "ns2.new.com"})
	require.NoError(t, err)
}

func TestUpdateNameserversRequiresTwo(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("no HTTP call expected") })
	err := f.client.UpdateNameservers(context.Background(), "x.id", []string{"only-one.com"})
	requireAppErr(t, err, apperr.CodeValidation)
	assert.Zero(t, f.hits.Load())
}

// Contact

func TestGetContact(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "ok", map[string]any{
			"id": 1,
			"registrant_contact": map[string]any{
				"id": 9, "name": "Jane Doe", "email": "jane@example.test", "organization": "Acme",
				"street_1": "Jl. Merdeka 1", "city": "Jakarta", "state": "DKI Jakarta",
				"country_code": "id", "postal_code": "12345", "voice": "0812",
			},
		})
	})
	c, err := f.client.GetContact(context.Background(), "x.id")
	require.NoError(t, err)
	assert.Equal(t, "Jane", c.FirstName)
	assert.Equal(t, "Doe", c.LastName)
	assert.Equal(t, "Acme", c.Company)
	assert.Equal(t, "ID", c.Country)
}

func TestGetContactSingleWordName(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "ok", map[string]any{
			"id":                 1,
			"registrant_contact": map[string]any{"id": 9, "name": "Cher"},
		})
	})
	c, err := f.client.GetContact(context.Background(), "x.id")
	require.NoError(t, err)
	assert.Equal(t, "Cher", c.FirstName)
	assert.Equal(t, "", c.LastName)
}

func TestGetContactMissing(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
	})
	_, err := f.client.GetContact(context.Background(), "x.id")
	requireAppErr(t, err, apperr.CodeNotFound)
}

func TestUpdateContact(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 42, "customer": map[string]any{"id": 7}})
		},
		"POST /customers/7/contacts": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "Default", form.Get("label"))
			assert.Equal(t, "Jane Doe", form.Get("name"))
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 88})
		},
		"PUT /domains/42/contacts": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "88", form.Get("admin_contact_id"))
			assert.Equal(t, "88", form.Get("tech_contact_id"))
			assert.Equal(t, "88", form.Get("billing_contact_id"))
			assert.Equal(t, "88", form.Get("registrant_contact_id"))
			writeEnv(w, http.StatusOK, "ok", nil)
		},
	}))
	err := f.client.UpdateContact(context.Background(), "x.id", sampleContact())
	require.NoError(t, err)
}

func TestUpdateContactNoLinkedCustomer(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 42})
	})
	err := f.client.UpdateContact(context.Background(), "x.id", sampleContact())
	requireAppErr(t, err, apperr.CodeExternal)
	assert.Equal(t, int32(1), f.hits.Load())
}

// EPP / auth code

func TestGetEPPCode(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 5})
		},
		"GET /domains/5/auth_code": func(w http.ResponseWriter, r *http.Request) { writeEnv(w, http.StatusOK, "ok", "4oyxcvMDgXp") },
	}))
	code, err := f.client.GetEPPCode(context.Background(), "x.id")
	require.NoError(t, err)
	assert.Equal(t, "4oyxcvMDgXp", code)
}

// DNS

func TestGetDNSRecords(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 5})
		},
		"GET /domains/5/dns": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", []map[string]any{
				{"prefix": "@", "type": "A", "content": "1.1.1.1", "ttl": 3600},
				{"prefix": "@", "type": "MX", "content": "10 mail.example.com", "ttl": 3600},
				{"prefix": "@", "type": "MX", "content": "malformed-no-space", "ttl": 3600},
			})
		},
	}))
	recs, err := f.client.GetDNSRecords(context.Background(), "x.id")
	require.NoError(t, err)
	require.Len(t, recs, 3)
	assert.Equal(t, ports.DNSRecord{Type: "A", Host: "@", Value: "1.1.1.1", TTL: 3600}, recs[0])
	assert.Equal(t, ports.DNSRecord{Type: "MX", Host: "@", Value: "mail.example.com", TTL: 3600, Prio: 10}, recs[1])
	assert.Equal(t, ports.DNSRecord{Type: "MX", Host: "@", Value: "malformed-no-space", TTL: 3600}, recs[2])
}

func TestUpdateDNSRecords(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 5})
		},
		"POST /domains/5/dns": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Equal(t, "www", form.Get("records[0][name]"))
			assert.Equal(t, "CNAME", form.Get("records[0][type]"))
			assert.Equal(t, "example.com.", form.Get("records[0][content]"))
			assert.Equal(t, "3600", form.Get("records[0][ttl]"))
			assert.Equal(t, "@", form.Get("records[1][name]"))
			assert.Equal(t, "MX", form.Get("records[1][type]"))
			assert.Equal(t, "10 mail.example.com", form.Get("records[1][content]"))
			writeEnv(w, http.StatusOK, "Records added successfully", "")
		},
	}))
	err := f.client.UpdateDNSRecords(context.Background(), "x.id", []ports.DNSRecord{
		{Type: "cname", Host: "www", Value: "example.com.", TTL: 0},
		{Type: "mx", Host: "@", Value: "mail.example.com", TTL: 3600, Prio: 10},
	})
	require.NoError(t, err)
}

func TestUpdateDNSRecordsEmptySendsNoFields(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 5})
		},
		"POST /domains/5/dns": func(w http.ResponseWriter, r *http.Request) {
			form := requireForm(t, r)
			assert.Empty(t, form)
			writeEnv(w, http.StatusOK, "ok", "")
		},
	}))
	err := f.client.UpdateDNSRecords(context.Background(), "x.id", nil)
	require.NoError(t, err)
}

// Sync / AccountInfo

func TestSyncDomain(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "ok", map[string]any{
			"id": 1, "status_label": "Active", "expired_at": "2027-01-01",
			"nameserver_1": "ns1.example.com", "nameserver_2": "ns2.example.com",
		})
	})
	info, err := f.client.SyncDomain(context.Background(), "x.id")
	require.NoError(t, err)
	assert.Equal(t, "active", info.Status)
	assert.Equal(t, []string{"ns1.example.com", "ns2.example.com"}, info.NS)
	assert.Equal(t, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), info.ExpiryDate)
}

func TestSyncDomainStatusFallsBackToNumericCode(t *testing.T) {
	for status, want := range map[int]string{
		0: "pending", 1: "active", 2: "expired", 3: "pending delete", 4: "deleted",
		5: "pending transfer", 6: "transferred away", 7: "suspended", 8: "rejected",
	} {
		f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "status": status})
		})
		info, err := f.client.SyncDomain(context.Background(), "x.id")
		require.NoError(t, err)
		assert.Equal(t, want, info.Status, "status code %d", status)
	}
}

func TestSyncDomainUnknownStatusCode(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "status": 99})
	})
	info, err := f.client.SyncDomain(context.Background(), "x.id")
	require.NoError(t, err)
	assert.Empty(t, info.Status)
}

func TestSyncDomainNoStatusAtAll(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
	})
	info, err := f.client.SyncDomain(context.Background(), "x.id")
	require.NoError(t, err)
	assert.Empty(t, info.Status)
}

func TestSyncDomainBadDate(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1, "expired_at": "bad-date"})
	})
	_, err := f.client.SyncDomain(context.Background(), "x.id")
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestSyncDomainUnknownMapsNotFound(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusNotFound, "domain not found", nil)
	})
	_, err := f.client.SyncDomain(context.Background(), "x.id")
	requireAppErr(t, err, apperr.CodeNotFound)
}

func TestAccountInfo(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /account/profile": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 584, "name": "PT Example"})
		},
		"GET /account/balance": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"currency": "IDR", "balance": "1013000.00"})
		},
	}))
	info, err := f.client.AccountInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "584", info.AccountID)
	assert.Equal(t, "PT Example", info.Name)
	assert.Equal(t, "IDR", info.Currency)
	assert.Equal(t, int64(1013000), info.Balance)
}

func TestAccountInfoBadBalance(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /account/profile": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"GET /account/balance": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"currency": "IDR", "balance": "not-a-number"})
		},
	}))
	_, err := f.client.AccountInfo(context.Background())
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestAccountInfoProfileFailurePropagates(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /account/profile": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusUnauthorized, "unauthorized", nil)
		},
		"GET /account/balance": func(w http.ResponseWriter, r *http.Request) { t.Fatal("balance should not be called") },
	}))
	_, err := f.client.AccountInfo(context.Background())
	requireAppErr(t, err, apperr.CodeExternal)
}

// ListCatalogPrices

// writeEnvMeta writes the bare {data, meta} envelope real Dewabiz list
// endpoints use (no success/message field) - GET /account/prices without a
// domainExtension filter.
func writeEnvMeta(w http.ResponseWriter, data any, currentPage, lastPage int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": data,
		"meta": map[string]any{"current_page": currentPage, "last_page": lastPage},
	})
}

func catalogItem(ext string) map[string]any {
	return map[string]any{
		"domain_extension": map[string]any{"extension": ext},
		"currency":         "IDR",
		// Mixed numeric/string year values, matching the real API's
		// observed inconsistency (see TestToInt64Lenient).
		"registration": map[string]any{"1": 150000, "2": "300000"},
		"renewal":      map[string]any{"1": "165000", "2": 330000},
		"transfer":     "150000.00",
		"redemption":   "750000.00",
	}
}

func TestListCatalogPricesSinglePage(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/account/prices", r.URL.Path)
		assert.Empty(t, r.URL.Query().Get("domainExtension[extension]"))
		writeEnvMeta(w, []map[string]any{catalogItem(".co.id"), catalogItem(".my.id")}, 1, 1)
	})
	prices, err := f.client.ListCatalogPrices(context.Background())
	require.NoError(t, err)
	require.Len(t, prices, 2)
	assert.Equal(t, ".co.id", prices[0].Extension)
	assert.Equal(t, "IDR", prices[0].Currency)
	assert.Equal(t, int64(150000), prices[0].RegisterPrices["1"])
	assert.Equal(t, int64(300000), prices[0].RegisterPrices["2"])
	assert.Equal(t, int64(165000), prices[0].RenewPrices["1"])
	assert.Equal(t, int64(330000), prices[0].RenewPrices["2"])
	assert.Equal(t, int64(150000), prices[0].TransferPrice)
	assert.Equal(t, int64(750000), prices[0].RestorePrice)
	assert.Equal(t, int32(1), f.hits.Load())
}

func TestListCatalogPricesWalksEveryPage(t *testing.T) {
	var seenPages []string
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		seenPages = append(seenPages, page)
		switch page {
		case "1":
			writeEnvMeta(w, []map[string]any{catalogItem(".id"), catalogItem(".co.id")}, 1, 3)
		case "2":
			writeEnvMeta(w, []map[string]any{catalogItem(".my.id")}, 2, 3)
		case "3":
			writeEnvMeta(w, []map[string]any{catalogItem(".web.id")}, 3, 3)
		default:
			t.Fatalf("unexpected page %q", page)
		}
	})
	prices, err := f.client.ListCatalogPrices(context.Background())
	require.NoError(t, err)
	require.Len(t, prices, 4)
	assert.Equal(t, []string{"1", "2", "3"}, seenPages)
	exts := make([]string, len(prices))
	for i, p := range prices {
		exts[i] = p.Extension
	}
	assert.Equal(t, []string{".id", ".co.id", ".my.id", ".web.id"}, exts)
}

func TestListCatalogPricesStopsOnEmptyPage(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			writeEnvMeta(w, []map[string]any{catalogItem(".id")}, 1, 0)
			return
		}
		writeEnvMeta(w, []map[string]any{}, 2, 0)
	})
	prices, err := f.client.ListCatalogPrices(context.Background())
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.Equal(t, int32(1), f.hits.Load())
}

func TestListCatalogPricesPropagatesTransportError(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusInternalServerError, "boom", nil)
	}, func(cfg *Config) { cfg.BreakerThreshold = 100 })
	_, err := f.client.ListCatalogPrices(context.Background())
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestListCatalogPricesBadTransferPriceErrors(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		item := catalogItem(".id")
		item["transfer"] = "not-a-number"
		writeEnvMeta(w, []map[string]any{item}, 1, 1)
	})
	_, err := f.client.ListCatalogPrices(context.Background())
	requireAppErr(t, err, apperr.CodeExternal)
}

// parseRupiah

func TestParseRupiah(t *testing.T) {
	cases := map[string]int64{"": 0, "0": 0, "1013000.00": 1013000, "500": 500, "  250000.50  ": 250000}
	for in, want := range cases {
		got, err := parseRupiah(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	_, err := parseRupiah("abc")
	require.Error(t, err)
}

// Error mapping / envelope edge cases

func TestErrorMappingTable(t *testing.T) {
	cases := []struct {
		status int
		code   apperr.Code
	}{
		{http.StatusNotFound, apperr.CodeNotFound},
		{http.StatusConflict, apperr.CodeConflict},
		{http.StatusBadRequest, apperr.CodeValidation},
		{http.StatusUnprocessableEntity, apperr.CodeValidation},
		{http.StatusUnauthorized, apperr.CodeExternal},
		{http.StatusForbidden, apperr.CodeExternal},
		{http.StatusTooManyRequests, apperr.CodeRateLimited},
		{http.StatusInternalServerError, apperr.CodeExternal},
		{http.StatusBadGateway, apperr.CodeExternal},
	}
	for _, tc := range cases {
		f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { writeEnv(w, tc.status, "boom", nil) })
		_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
		requireAppErr(t, err, tc.code)
	}
}

func TestFieldErrorsFromSortedDeterministic(t *testing.T) {
	errs := fieldErrorsFrom(map[string][]string{
		"zeta":  {"z message"},
		"alpha": {"a message"},
		"empty": {},
	})
	require.Len(t, errs, 2)
	assert.Equal(t, "alpha", errs[0].Field)
	assert.Equal(t, "zeta", errs[1].Field)
	assert.Nil(t, fieldErrorsFrom(nil))
}

func TestMapErrorFallbackMessage(t *testing.T) {
	err := mapError(http.StatusTeapot, "", nil)
	assert.Contains(t, err.Message, http.StatusText(http.StatusTeapot))
}

func TestUnauthorizedMentionsCredentials(t *testing.T) {
	err := mapError(http.StatusUnauthorized, "nope", nil)
	assert.Contains(t, err.Message, "check RDash credentials")
}

func TestEnvelopeSuccessFalseWithOKStatusMapsBadRequest(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "nope", "data": nil})
	})
	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	requireAppErr(t, err, apperr.CodeValidation)
}

// Paginated list endpoints (GET /customers, /domains, /account/prices,
// /account/transactions, contact lists) return a bare {data, links, meta}
// with NO "success" field at all - confirmed against the real production
// API. Its absence must never be misread as an explicit false; a 2xx with no
// success key at all is trusted as success.
func TestEnvelopeWithoutSuccessFieldIsTrusted(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		data := any(map[string]any{"name": "x.id", "available": 1})
		if strings.Contains(r.URL.Path, "prices") {
			data = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": data,
			"meta": map[string]any{"current_page": 1},
		})
	})
	res, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.True(t, res[0].Available)
}

func TestInvalidJSONBodyOn2xx(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	})
	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestNonEnvelope4xxMapsByStatus(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not json"))
	})
	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	requireAppErr(t, err, apperr.CodeNotFound)
}

func TestNetworkErrorMapsExternal(t *testing.T) {
	c := New(Config{BaseURL: "http://127.0.0.1:1", ResellerID: "r", APIKey: "k", RetryDelay: time.Millisecond}, nil, nil, nil, nil)
	_, err := c.CheckAvailability(context.Background(), []string{"x.id"})
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestTruncatedResponseBodyMapsExternal(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true`))
	})
	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	requireAppErr(t, err, apperr.CodeExternal)
}

// Retry behavior

func TestGetRetriesTwiceOn5xxThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/availability": func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) <= 2 {
				writeEnv(w, http.StatusBadGateway, "boom", nil)
				return
			}
			writeEnv(w, http.StatusOK, "ok", map[string]any{"name": "x.id", "available": 1})
		},
		"GET /account/prices": func(w http.ResponseWriter, r *http.Request) { writeEnv(w, http.StatusOK, "ok", []map[string]any{}) },
	}))
	res, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, int32(3), calls.Load())
	assert.Equal(t, int32(4), f.hits.Load()) // 2 failed + 1 successful availability GET, + 1 price lookup
}

func TestGetRetriesExhausted(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { writeEnv(w, http.StatusBadGateway, "boom", nil) })
	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	requireAppErr(t, err, apperr.CodeExternal)
	assert.Equal(t, int32(1+maxGetRetries), f.hits.Load())
}

func TestPostNeverRetries(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"POST /customers": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusBadGateway, "boom", nil)
		},
	}))
	_, err := f.client.Register(context.Background(), ports.RegisterDomainRequest{Name: "x.id", Contact: sampleContact()})
	requireAppErr(t, err, apperr.CodeExternal)
	assert.Equal(t, int32(2), f.hits.Load()) // GET /customers (lookup, not found) + POST /customers (no retry)
}

func TestPutNeverRetries(t *testing.T) {
	f := newFixture(t, routes(t, map[string]http.HandlerFunc{
		"GET /domains/details": func(w http.ResponseWriter, r *http.Request) {
			writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
		},
		"PUT /domains/1/ns": func(w http.ResponseWriter, r *http.Request) { writeEnv(w, http.StatusBadGateway, "boom", nil) },
	}))
	err := f.client.UpdateNameservers(context.Background(), "x.id", []string{"a.com", "b.com"})
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestGetRetryStopsOnCancelledContext(t *testing.T) {
	var calls atomic.Int32
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeEnv(w, http.StatusBadGateway, "boom", nil)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.client.CheckAvailability(ctx, []string{"x.id"})
	requireAppErr(t, err, apperr.CodeExternal)
	assert.LessOrEqual(t, calls.Load(), int32(1))
}

// Circuit breaker

func TestCircuitBreakerOpensAndCoolsDown(t *testing.T) {
	fixedClock := &mocks.MockClock{FixedTime: time.Now()}
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { writeEnv(w, http.StatusBadGateway, "boom", nil) },
		func(cfg *Config) { cfg.BreakerThreshold = 2; cfg.BreakerCooldown = time.Minute })
	f.client.clock = fixedClock

	// A GET op retries maxGetRetries+1 times per call, so the very first call
	// already drives enough consecutive failures to open the breaker
	// (threshold 2); every call after that fails fast without hitting the
	// server until the cooldown elapses.
	for i := 0; i < 2; i++ {
		_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
		requireAppErr(t, err, apperr.CodeExternal)
	}

	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	ae := requireAppErr(t, err, apperr.CodeExternal)
	assert.Contains(t, ae.Message, "circuit breaker open")

	fixedClock.FixedTime = fixedClock.FixedTime.Add(2 * time.Minute)
	hitsBefore := f.hits.Load()
	_, err = f.client.CheckAvailability(context.Background(), []string{"x.id"})
	requireAppErr(t, err, apperr.CodeExternal)
	assert.Greater(t, f.hits.Load(), hitsBefore, "breaker should have let a real request through after cooldown")
}

func TestBusinessErrorsDoNotTripBreaker(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { writeEnv(w, http.StatusConflict, "taken", nil) },
		func(cfg *Config) { cfg.BreakerThreshold = 2 })
	for i := 0; i < 5; i++ {
		_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
		requireAppErr(t, err, apperr.CodeConflict)
	}
	require.NoError(t, f.client.checkBreaker())
}

// Live credential resolver (dynamic, admin-configurable, no-restart-needed)

func TestResolveCredentialsUsesResolverWhenSet(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		require.True(t, ok)
		assert.Equal(t, "live-reseller", user)
		assert.Equal(t, "live-key", pass)
		if strings.Contains(r.URL.Path, "prices") {
			writeEnv(w, http.StatusOK, "ok", []map[string]any{})
			return
		}
		writeEnv(w, http.StatusOK, "ok", map[string]any{"name": "x.id", "available": 1})
	})
	f.client.resolve = func(ctx context.Context) (Credentials, error) {
		return Credentials{BaseURL: f.srv.URL, ResellerID: "live-reseller", APIKey: "live-key"}, nil
	}
	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	require.NoError(t, err)
}

// A "custom endpoint" (admin-configured base URL) redirects every call to a
// different server entirely - proving the mechanism the user asked for
// (switching between the mockserver and the real Dewabiz API without a
// process restart) actually changes where requests go, not just the auth
// header.
func TestResolveCredentialsUsesResolverBaseURL(t *testing.T) {
	var hitCustom, hitStatic atomic.Bool
	customEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCustom.Store(true)
		if strings.Contains(r.URL.Path, "prices") {
			writeEnv(w, http.StatusOK, "ok", []map[string]any{})
			return
		}
		writeEnv(w, http.StatusOK, "ok", map[string]any{"name": "x.id", "available": 1})
	}))
	t.Cleanup(customEndpoint.Close)

	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hitStatic.Store(true)
		writeEnv(w, http.StatusOK, "ok", map[string]any{"name": "x.id", "available": 1})
	})
	f.client.resolve = func(ctx context.Context) (Credentials, error) {
		return Credentials{BaseURL: customEndpoint.URL + "/", ResellerID: testResellerID, APIKey: testAPIKey}, nil
	}

	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	require.NoError(t, err)
	assert.True(t, hitCustom.Load(), "the resolved custom endpoint must receive the request")
	assert.False(t, hitStatic.Load(), "the static (env-configured) base URL must NOT be used once a custom endpoint resolves")
	assert.Zero(t, f.hits.Load(), "fixture's own static server saw zero hits")
}

func TestResolveCredentialsFallsBackToStaticConfigOnError(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requireBasicAuth(t, r) // testResellerID/testAPIKey - the static fixture config
		if strings.Contains(r.URL.Path, "prices") {
			writeEnv(w, http.StatusOK, "ok", []map[string]any{})
			return
		}
		writeEnv(w, http.StatusOK, "ok", map[string]any{"name": "x.id", "available": 1})
	})
	f.client.resolve = func(ctx context.Context) (Credentials, error) {
		return Credentials{}, errBoom
	}
	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	require.NoError(t, err)
}

func TestResolveCredentialsNilResolverUsesStaticConfig(t *testing.T) {
	creds := (&Client{cfg: Config{BaseURL: "https://static.test/v1", ResellerID: "r", APIKey: "k"}}).resolveCredentials(context.Background())
	assert.Equal(t, "https://static.test/v1", creds.BaseURL)
	assert.Equal(t, "r", creds.ResellerID)
	assert.Equal(t, "k", creds.APIKey)
}

// Misc / helpers

func TestNilLoggerIsSafe(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "prices") {
			writeEnv(w, http.StatusOK, "ok", []map[string]any{})
			return
		}
		writeEnv(w, http.StatusOK, "ok", map[string]any{"name": "x.id", "available": 1})
	})
	f.client.log = nopLogger{}
	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	require.NoError(t, err)
}

func TestDataDecodeMismatchMapsExternal(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnv(w, http.StatusOK, "ok", map[string]any{"unexpected": "shape"})
	})
	_, err := f.client.CheckAvailability(context.Background(), []string{"x.id"})
	requireAppErr(t, err, apperr.CodeExternal)
}

func TestDomainNameIsQueryEscaped(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		// r.URL.Query() decodes percent-encoding, so a correctly escaped IDN
		// name round-trips back to its normalized (trimmed+lowercased) form.
		assert.Equal(t, "café.id", r.URL.Query().Get("domain_name"))
		writeEnv(w, http.StatusOK, "ok", map[string]any{"id": 1})
	})
	_, err := f.client.GetNameservers(context.Background(), "  Café.ID  ")
	require.NoError(t, err)
}

func TestAllDomainOpsRejectEmptyName(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("no HTTP call expected") })
	ctx := context.Background()

	_, err := f.client.GetNameservers(ctx, " ")
	requireAppErr(t, err, apperr.CodeValidation)
	err = f.client.UpdateNameservers(ctx, " ", []string{"a.com", "b.com"})
	requireAppErr(t, err, apperr.CodeValidation)
	_, err = f.client.GetContact(ctx, " ")
	requireAppErr(t, err, apperr.CodeValidation)
	err = f.client.UpdateContact(ctx, " ", sampleContact())
	requireAppErr(t, err, apperr.CodeValidation)
	_, err = f.client.GetEPPCode(ctx, " ")
	requireAppErr(t, err, apperr.CodeValidation)
	_, err = f.client.GetDNSRecords(ctx, " ")
	requireAppErr(t, err, apperr.CodeValidation)
	err = f.client.UpdateDNSRecords(ctx, " ", nil)
	requireAppErr(t, err, apperr.CodeValidation)
	_, err = f.client.SyncDomain(ctx, " ")
	requireAppErr(t, err, apperr.CodeValidation)
	_, err = f.client.Renew(ctx, " ", 1)
	requireAppErr(t, err, apperr.CodeValidation)

	assert.Zero(t, f.hits.Load())
}

// Redaction

func TestRedactJSON(t *testing.T) {
	raw := []byte(`{"name":"Jane","password":"secret","nested":{"api_key":"k","ok":"visible"},"list":[{"epp":"E1"},{"fine":"yes"}]}`)
	got := redactJSON(raw)
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Jane", m["name"])
	assert.Equal(t, redactedPlaceholder, m["password"])
	nested := m["nested"].(map[string]any)
	assert.Equal(t, redactedPlaceholder, nested["api_key"])
	assert.Equal(t, "visible", nested["ok"])
	list := m["list"].([]any)
	assert.Equal(t, redactedPlaceholder, list[0].(map[string]any)["epp"])
	assert.Equal(t, "yes", list[1].(map[string]any)["fine"])
}

func TestRedactJSONInvalidBodyTruncates(t *testing.T) {
	raw := []byte(strings.Repeat("x", 600))
	got := redactJSON(raw)
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.Len(t, m["raw"].(string), 512)
}

func TestRedactValueScalars(t *testing.T) {
	assert.Equal(t, "plain", redactValue("plain"))
	assert.Equal(t, float64(1), redactValue(float64(1)))
	assert.Nil(t, redactValue(nil))
}

func TestRedactForm(t *testing.T) {
	form := url.Values{
		"email":                 {"jane@example.test"},
		"password":              {"secret"},
		"password_confirmation": {"secret"},
		"auth_code":             {"EPP-1"},
		"nameserver[0]":         {"ns1.example.com"},
	}
	got := redactForm(form).(map[string]any)
	assert.Equal(t, "jane@example.test", got["email"])
	assert.Equal(t, redactedPlaceholder, got["password"])
	assert.Equal(t, redactedPlaceholder, got["password_confirmation"])
	assert.Equal(t, redactedPlaceholder, got["auth_code"])
	assert.Equal(t, "ns1.example.com", got["nameserver[0]"])
}

func TestRedactFormMultiValue(t *testing.T) {
	form := url.Values{"tag": {"a", "b"}}
	got := redactForm(form).(map[string]any)
	assert.Equal(t, []any{"a", "b"}, got["tag"])
}
