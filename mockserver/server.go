package main

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config holds all runtime configuration, sourced from environment variables.
type Config struct {
	Port               string // MOCK_PORT, default 9090
	BaseURL            string // http://localhost:<port>, used to build paymentUrl / SSO URLs
	DuitkuMerchantCode string // DUITKU_MERCHANT_CODE, default DEMO
	DuitkuAPIKey       string // DUITKU_API_KEY, default secretkey
}

func configFromEnv() Config {
	port := envOr("MOCK_PORT", "9090")
	return Config{
		Port:               port,
		BaseURL:            "http://localhost:" + port,
		DuitkuMerchantCode: envOr("DUITKU_MERCHANT_CODE", "DEMO"),
		DuitkuAPIKey:       envOr("DUITKU_API_KEY", "secretkey"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Server is the mock server. All mutable state is guarded by mu.
type Server struct {
	cfg     Config
	handler http.Handler

	httpClient *http.Client
	sleep      func(time.Duration) // swapped out in tests to avoid real backoff waits

	mu sync.Mutex
	// Duitku
	duitkuSeq     int64
	duitkuTxs     map[string]*duitkuTx // by reference
	duitkuByOrder map[string]*duitkuTx // by merchantOrderId (latest attempt wins)
	// WHM / cPanel
	whmAccounts map[string]*whmAccount
	whmPackages map[string]*panelPackage
	// DirectAdmin
	daAccounts map[string]*daAccount
	daPackages map[string]*panelPackage
	// RDash
	rdashCustomerSeq    int64
	rdashContactSeq     int64
	rdashDomainSeq      int64
	rdashCustomers      map[int64]*rdashCustomer
	rdashContacts       map[int64]*rdashContact
	rdashDomains        map[int64]*rdashDomain
	rdashDomainsByName  map[string]int64
	rdashBalance        int64                   // reseller balance, whole IDR; debited by POST /v1/domains
	rdashScripts        map[string]*rdashScript // drop scripts by normalized domain name
	rdashAttempts       map[string]int          // POST /v1/domains attempts by normalized domain name
	rdashFaults         []*rdashFault           // armed fault injections, consumed in order
	rdashAutoActivation bool                    // reseller's Auto Activation switch (default on)
	rdashDocTokens      map[string]int64        // upload-page token -> domain id
	// Mail capture
	mailSeq int64
	mails   []mailMessage
}

// NewServer builds a fully-routed mock server. It implements http.Handler.
func NewServer(cfg Config) *Server {
	s := &Server{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		sleep:      time.Sleep,
	}
	s.resetState()

	mux := http.NewServeMux()

	// Duitku V2
	mux.HandleFunc("POST /webapi/api/merchant/paymentmethod/getpaymentmethod", s.handleDuitkuGetPaymentMethods)
	mux.HandleFunc("POST /webapi/api/merchant/v2/inquiry", s.handleDuitkuInquiry)
	mux.HandleFunc("POST /webapi/api/merchant/transactionStatus", s.handleDuitkuTransactionStatus)
	mux.HandleFunc("GET /payment/{reference}", s.handleDuitkuPaymentPage)
	mux.HandleFunc("POST /mock/duitku/pay/{reference}", s.handleDuitkuPay)
	mux.HandleFunc("POST /mock/duitku/expire/{reference}", s.handleDuitkuExpire)

	// WHM API 1
	mux.HandleFunc("/json-api/{fn}", s.handleWHM)
	mux.HandleFunc("GET /cpanel-sso/{user}", s.handleCpanelSSO)

	// DirectAdmin legacy API
	mux.HandleFunc("/CMD_API_ACCOUNT_USER", s.handleDAAccountUser)
	mux.HandleFunc("/CMD_API_SELECT_USERS", s.handleDASelectUsers)
	mux.HandleFunc("/CMD_API_MODIFY_USER", s.handleDAModifyUser)
	mux.HandleFunc("/CMD_API_MANAGE_USER_PACKAGES", s.handleDAManageUserPackages)
	mux.HandleFunc("GET /CMD_API_PACKAGES_USER", s.handleDAPackagesUser)
	mux.HandleFunc("/CMD_API_USER_PASSWD", s.handleDAUserPasswd)
	mux.HandleFunc("/CMD_API_SHOW_USERS", s.handleDAShowUsers)
	mux.HandleFunc("/CMD_API_SHOW_USER_CONFIG", s.handleDAShowUserConfig)

	// RDash v1 (Dewabiz Domain Reseller Open API shape)
	mux.HandleFunc("GET /v1/domains/availability", s.handleRDashAvailability)
	mux.HandleFunc("GET /v1/domains/details", s.handleRDashDetails)
	mux.HandleFunc("POST /v1/domains", s.handleRDashRegisterDomain)
	mux.HandleFunc("POST /v1/domains/transfer", s.handleRDashTransferDomain)
	mux.HandleFunc("POST /v1/domains/{id}/renew", s.handleRDashRenew)
	mux.HandleFunc("PUT /v1/domains/{id}/ns", s.handleRDashUpdateNS)
	mux.HandleFunc("PUT /v1/domains/{id}/contacts", s.handleRDashUpdateContacts)
	mux.HandleFunc("GET /v1/domains/{id}/auth_code", s.handleRDashAuthCode)
	mux.HandleFunc("GET /v1/domains/{id}/dns", s.handleRDashDNSGet)
	mux.HandleFunc("POST /v1/domains/{id}/dns", s.handleRDashDNSReplace)
	mux.HandleFunc("POST /v1/domains/{id}/documents/link", s.handleRDashDocumentLink)
	mux.HandleFunc("POST /v1/domains/{id}/verification/resend", s.handleRDashVerificationResend)
	mux.HandleFunc("GET /v1/customers", s.handleRDashCustomerList)
	mux.HandleFunc("POST /v1/customers", s.handleRDashCustomerCreate)
	mux.HandleFunc("POST /v1/customers/{customer_id}/contacts", s.handleRDashContactCreate)
	mux.HandleFunc("GET /v1/account/profile", s.handleRDashProfile)
	mux.HandleFunc("GET /v1/account/balance", s.handleRDashBalance)
	mux.HandleFunc("GET /v1/account/prices", s.handleRDashPrices)

	// Cloudflare Turnstile siteverify (CAPTCHA)
	mux.HandleFunc("POST /turnstile/v0/siteverify", s.handleTurnstileVerify)

	// Mail capture
	mux.HandleFunc("POST /mail/send", s.handleMailSend)
	mux.HandleFunc("GET /mail/messages", s.handleMailList)
	mux.HandleFunc("DELETE /mail/messages", s.handleMailClear)

	// Ops
	mux.HandleFunc("POST /mock/reset", s.handleReset)
	mux.HandleFunc("POST /mock/rdash/script", s.handleRDashScript)
	mux.HandleFunc("GET /mock/rdash/attempts", s.handleRDashAttempts)
	mux.HandleFunc("POST /mock/rdash/fault", s.handleRDashFault)
	mux.HandleFunc("POST /mock/rdash/balance", s.handleRDashSetBalance)
	mux.HandleFunc("POST /mock/rdash/domain-state", s.handleRDashDomainState)
	mux.HandleFunc("GET /mock/rdash/state", s.handleRDashGetState)
	mux.HandleFunc("POST /mock/rdash/auto-activation", s.handleRDashAutoActivation)
	mux.HandleFunc("GET /documents/upload/{token}", s.handleDocumentUploadPage)
	mux.HandleFunc("POST /documents/upload/{token}", s.handleDocumentUploadSubmit)
	mux.HandleFunc("GET /mock/whm/packages", s.handleListWHMPackages)
	mux.HandleFunc("GET /mock/da/packages", s.handleListDAPackages)
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	s.handler = logRequests(s.rdashFaultInjector(mux))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// resetState (re)initializes every in-memory store.
func (s *Server) resetState() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.duitkuSeq = 0
	s.duitkuTxs = make(map[string]*duitkuTx)
	s.duitkuByOrder = make(map[string]*duitkuTx)
	s.whmAccounts = make(map[string]*whmAccount)
	s.whmPackages = seedPackages()
	s.daAccounts = make(map[string]*daAccount)
	s.daPackages = seedPackages()
	s.rdashCustomerSeq = 0
	s.rdashContactSeq = 0
	s.rdashDomainSeq = 0
	s.rdashCustomers = make(map[int64]*rdashCustomer)
	s.rdashContacts = make(map[int64]*rdashContact)
	s.rdashDomains = make(map[int64]*rdashDomain)
	s.rdashDomainsByName = make(map[string]int64)
	s.rdashBalance = rdashDefaultBalance
	s.rdashScripts = make(map[string]*rdashScript)
	s.rdashAttempts = make(map[string]int)
	s.rdashFaults = nil
	s.rdashAutoActivation = true
	s.rdashDocTokens = make(map[string]int64)
	s.mailSeq = 0
	s.mails = nil
}

func (s *Server) handleReset(w http.ResponseWriter, _ *http.Request) {
	s.resetState()
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

// handleListWHMPackages / handleListDAPackages expose the on-the-fly packages
// created by EnsurePackage so E2E tests can assert dynamic-product provisioning.
func (s *Server) handleListWHMPackages(w http.ResponseWriter, _ *http.Request) {
	s.writePackages(w, s.whmPackages)
}

func (s *Server) handleListDAPackages(w http.ResponseWriter, _ *http.Request) {
	s.writePackages(w, s.daPackages)
}

func (s *Server) writePackages(w http.ResponseWriter, store map[string]*panelPackage) {
	s.mu.Lock()
	out := make([]map[string]any, 0, len(store))
	for _, p := range store {
		out = append(out, map[string]any{"name": p.Name, "params": p.Params})
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"packages": out})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// ---- shared helpers ----

// decodeJSON reads a JSON request body into v (up to 1 MiB).
func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func md5Hex(in string) string {
	sum := md5.Sum([]byte(in))
	return hex.EncodeToString(sum[:])
}

func sha256Hex(in string) string {
	sum := sha256.Sum256([]byte(in))
	return hex.EncodeToString(sum[:])
}

// flexInt64 unmarshals a JSON number or numeric string into an int64.
// Duitku clients variously send amounts as `10000` or `"10000"`.
type flexInt64 struct {
	Value int64
	Set   bool
}

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		return nil
	}
	s = strings.Trim(s, `"`)
	if s == "" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		fl, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil {
			return err
		}
		n = int64(fl)
	}
	f.Value = n
	f.Set = true
	return nil
}

// logRequests logs one line per request to stdout.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.RequestURI(), rec.status, time.Since(start).Round(time.Microsecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
