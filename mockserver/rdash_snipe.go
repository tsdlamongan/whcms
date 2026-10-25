package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RDash scripted-drop / fault-injection hooks, used to exercise the backend's
// "domain sniper" (repeated POST /v1/domains until a dropping domain is won):
//
//   - POST /mock/rdash/script   a domain that fails its first N registrations,
//     then registers normally
//   - GET  /mock/rdash/attempts how many registrations a name has received
//   - POST /mock/rdash/fault    the next N requests to a /v1 path answer 429/5xx
//     (optionally slowly) before any real handling
//   - POST /mock/rdash/balance  set the (otherwise constant) reseller balance
//
// Like the other /mock/* control endpoints they take JSON, need no auth and
// answer {"error": "..."} with 400 on bad input. All state lives on Server,
// guarded by s.mu, and is cleared by POST /mock/reset.

const (
	// rdashDefaultBalance is the reseller balance (whole IDR) after a reset.
	rdashDefaultBalance int64 = 10000000
	// rdashMockPricePerYear is what POST /v1/domains debits per year of period.
	rdashMockPricePerYear int64 = 10000
	// rdashMaxFaultDelayMs caps a fault's delay_ms.
	rdashMaxFaultDelayMs = 10000
)

// rdashScript makes a domain fail its first FailTimes registrations.
type rdashScript struct {
	FailTimes   int
	FailStatus  int
	FailMessage string
}

// rdashFault is one armed fault: the next Remaining requests to Path.
type rdashFault struct {
	Path       string
	Status     int // 0 = no error response, only Delay (then normal handling)
	Message    string
	RetryAfter int // seconds; 0 = no Retry-After header
	Delay      time.Duration
	Remaining  int
}

func normalizeDomain(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// writeMockError writes the {"error": msg} 400 the /mock/* endpoints use.
func writeMockError(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

// recordRDashAttempt counts one POST /v1/domains attempt for name (whatever
// its outcome) and returns the new total, so the first attempt returns 1.
func (s *Server) recordRDashAttempt(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rdashAttempts[name]++
	return s.rdashAttempts[name]
}

// rdashScriptHoldingLocked reports whether name still has scripted failures
// left to serve, i.e. the next registration would be rejected by its script.
// Caller must hold s.mu.
func (s *Server) rdashScriptHoldingLocked(name string) bool {
	sc, ok := s.rdashScripts[name]
	return ok && s.rdashAttempts[name] < sc.FailTimes
}

// rdashRegisterRejectLocked decides whether the attempt-th registration of
// name must be refused, returning the HTTP status and message to answer with
// (status 0 = go ahead and register). Order: drop script, already taken,
// balance. A scripted name ignores the `taken` substring rule - the script
// alone decides when it drops. Caller must hold s.mu.
func (s *Server) rdashRegisterRejectLocked(name string, attempt, period int) (int, string) {
	sc, scripted := s.rdashScripts[name]
	if scripted && attempt <= sc.FailTimes {
		return sc.FailStatus, sc.FailMessage
	}
	_, registered := s.rdashDomainsByName[name]
	if registered || (!scripted && strings.Contains(name, "taken")) {
		return http.StatusConflict, "domain not available"
	}
	// period > balance/price  <=>  price*period > balance, without overflow.
	if int64(period) > s.rdashBalance/rdashMockPricePerYear {
		return http.StatusUnprocessableEntity, "Insufficient balance"
	}
	return 0, ""
}

// POST /mock/rdash/script
// {"domain":"x.com","fail_times":3,"fail_status":409,"fail_message":"..."}
func (s *Server) handleRDashScript(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain      string `json:"domain"`
		FailTimes   int    `json:"fail_times"`
		FailStatus  int    `json:"fail_status"`
		FailMessage string `json:"fail_message"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeMockError(w, "invalid request body")
		return
	}
	name := normalizeDomain(req.Domain)
	switch {
	case name == "":
		writeMockError(w, "missing required field: domain")
		return
	case req.FailTimes < 0:
		writeMockError(w, "fail_times must be >= 0")
		return
	case req.FailStatus != 0 && (req.FailStatus < 400 || req.FailStatus > 599):
		writeMockError(w, "fail_status must be a 4xx/5xx status")
		return
	}
	if req.FailStatus == 0 {
		req.FailStatus = http.StatusConflict
	}
	if strings.TrimSpace(req.FailMessage) == "" {
		req.FailMessage = "Domain not available"
	}

	s.mu.Lock()
	s.rdashScripts[name] = &rdashScript{FailTimes: req.FailTimes, FailStatus: req.FailStatus, FailMessage: req.FailMessage}
	s.rdashAttempts[name] = 0 // (re)scripting starts the sequence over
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /mock/rdash/attempts?domain=x.com
func (s *Server) handleRDashAttempts(w http.ResponseWriter, r *http.Request) {
	name := normalizeDomain(r.URL.Query().Get("domain"))
	if name == "" {
		writeMockError(w, "missing required query parameter: domain")
		return
	}
	s.mu.Lock()
	attempts := s.rdashAttempts[name]
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"domain": name, "attempts": attempts})
}

// POST /mock/rdash/fault
// {"path":"/v1/domains","status":429,"count":3,"retry_after":2,"message":"...","delay_ms":0}
func (s *Server) handleRDashFault(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path       string `json:"path"`
		Status     int    `json:"status"`
		Count      int    `json:"count"`
		RetryAfter int    `json:"retry_after"`
		Message    string `json:"message"`
		DelayMs    int    `json:"delay_ms"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeMockError(w, "invalid request body")
		return
	}
	switch {
	case strings.TrimSpace(req.Path) == "":
		writeMockError(w, "missing required field: path")
		return
	case !strings.HasPrefix(req.Path, "/v1/"):
		writeMockError(w, "path must be an RDash route starting with /v1/")
		return
	case req.Count < 0:
		writeMockError(w, "count must be >= 0")
		return
	case req.RetryAfter < 0:
		writeMockError(w, "retry_after must be >= 0")
		return
	case req.DelayMs < 0:
		writeMockError(w, "delay_ms must be >= 0")
		return
	case req.Status != 0 && (req.Status < 400 || req.Status > 599):
		writeMockError(w, "status must be a 4xx/5xx status (or 0 with delay_ms)")
		return
	case req.Status == 0 && req.DelayMs == 0:
		writeMockError(w, "status is required unless delay_ms is set")
		return
	}
	if req.Count == 0 {
		req.Count = 1
	}
	if req.DelayMs > rdashMaxFaultDelayMs {
		req.DelayMs = rdashMaxFaultDelayMs
	}
	if strings.TrimSpace(req.Message) == "" {
		req.Message = http.StatusText(req.Status)
	}

	s.mu.Lock()
	s.rdashFaults = append(s.rdashFaults, &rdashFault{
		Path: req.Path, Status: req.Status, Message: req.Message, RetryAfter: req.RetryAfter,
		Delay: time.Duration(req.DelayMs) * time.Millisecond, Remaining: req.Count,
	})
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /mock/rdash/balance {"amount":50000}
func (s *Server) handleRDashSetBalance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Amount flexInt64 `json:"amount"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeMockError(w, "invalid request body")
		return
	}
	if !req.Amount.Set {
		writeMockError(w, "missing required field: amount")
		return
	}
	if req.Amount.Value < 0 {
		writeMockError(w, "amount must be >= 0")
		return
	}
	s.mu.Lock()
	s.rdashBalance = req.Amount.Value
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "balance": req.Amount.Value})
}

// rdashFaultInjector answers armed faults ahead of the RDash handlers. A
// request only reaches it when its exact /v1 path has a fault queued; the
// reseller must still authenticate first (a 401 leaves the fault armed).
// Every other request passes straight through untouched.
func (s *Server) rdashFaultInjector(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") || !s.hasRDashFault(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := rdashAuth(w, r); !ok {
			return
		}
		fault, sleep, ok := s.takeRDashFault(r.URL.Path)
		if !ok { // consumed by a concurrent request after the peek
			next.ServeHTTP(w, r)
			return
		}
		if fault.Delay > 0 {
			sleep(fault.Delay)
		}
		if fault.Status == 0 { // delay-only fault: slow, but otherwise normal
			next.ServeHTTP(w, r)
			return
		}
		// A faulted registration still counts as an attempt for its domain.
		if r.Method == http.MethodPost && r.URL.Path == "/v1/domains" {
			if name := normalizeDomain(requireForm(r).Get("name")); name != "" {
				s.recordRDashAttempt(name)
			}
		}
		if fault.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(fault.RetryAfter))
		}
		writeRDash(w, fault.Status, fault.Message, nil)
	})
}

// hasRDashFault peeks whether an unconsumed fault is queued for path.
func (s *Server) hasRDashFault(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.rdashFaults {
		if f.Path == path {
			return true
		}
	}
	return false
}

// takeRDashFault consumes one request from the oldest fault queued for path.
// It also returns the sleep func to use for the fault's delay (read under the
// lock because tests swap it).
func (s *Server) takeRDashFault(path string) (rdashFault, func(time.Duration), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, f := range s.rdashFaults {
		if f.Path != path {
			continue
		}
		taken := *f
		f.Remaining--
		if f.Remaining <= 0 {
			s.rdashFaults = append(s.rdashFaults[:i], s.rdashFaults[i+1:]...)
		}
		return taken, s.sleep, true
	}
	return rdashFault{}, nil, false
}
