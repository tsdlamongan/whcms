package main

import (
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// WHM API 1 mock: /json-api/<function> with GET or POST (query or form params).
// Auth: `Authorization: whm <user>:<token>` - any non-empty token is accepted
// except the literal token "badtoken" (-> 403).

type whmAccount struct {
	Username      string
	Domain        string
	Plan          string
	Password      string
	Email         string
	Suspended     bool
	SuspendReason string
	CreatedAt     time.Time
}

// panelPackage is a control-panel package/plan created on the fly for a
// dynamic/custom-spec product. Params holds the raw limit fields as received,
// so tests can introspect what the adapter sent.
type panelPackage struct {
	Name      string
	Params    map[string]string
	CreatedAt time.Time
}

// seedPackages returns a fresh set of default hosting packages, as if an
// admin had already created them on the panel before ever touching WHCMS -
// so ListPackages has something realistic to offer out of the box, on top of
// whatever EnsurePackage creates on the fly for configurable products.
func seedPackages() map[string]*panelPackage {
	now := time.Now()
	out := map[string]*panelPackage{}
	for _, name := range []string{"default", "starter", "business"} {
		out[name] = &panelPackage{Name: name, CreatedAt: now}
	}
	return out
}

func writeWHM(w http.ResponseWriter, status int, fn string, result int, reason string, data any) {
	if data == nil {
		data = map[string]any{}
	}
	writeJSON(w, status, map[string]any{
		"metadata": map[string]any{
			"version": 1,
			"reason":  reason,
			"result":  result,
			"command": fn,
		},
		"data": data,
	})
}

// whmAuthOK validates either the API-token header (`Authorization: whm
// user:token`) or HTTP Basic auth with a password (`Authorization: Basic
// base64(user:password)`) - WHMCS-style. Any non-empty credential is accepted
// except the sentinels "badtoken"/"badpassword" (-> 403 in tests).
func whmAuthOK(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if h == "" {
		return false
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 {
		return false
	}
	switch {
	case strings.EqualFold(parts[0], "whm"):
		creds := strings.SplitN(strings.TrimSpace(parts[1]), ":", 2)
		return len(creds) == 2 && creds[0] != "" && creds[1] != "" && creds[1] != "badtoken"
	case strings.EqualFold(parts[0], "basic"):
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
		if err != nil {
			return false
		}
		creds := strings.SplitN(string(raw), ":", 2)
		return len(creds) == 2 && creds[0] != "" && creds[1] != "" && creds[1] != "badpassword"
	default:
		return false
	}
}

func (s *Server) handleWHM(w http.ResponseWriter, r *http.Request) {
	fn := r.PathValue("fn")
	if !whmAuthOK(r) {
		writeWHM(w, http.StatusForbidden, fn, 0, "Access denied: invalid or missing WHM token", nil)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWHM(w, http.StatusOK, fn, 0, "could not parse parameters", nil)
		return
	}
	// param returns the first non-empty value among the given keys
	// (query string and POST form are both consulted via r.Form).
	param := func(keys ...string) string {
		for _, k := range keys {
			if v := r.Form.Get(k); v != "" {
				return v
			}
		}
		return ""
	}

	switch fn {
	case "version":
		// Read-only connectivity/credential probe (Test Connection).
		writeWHM(w, http.StatusOK, "version", 1, "OK", map[string]any{"version": "11.126.0.5"})
	case "gethostname":
		writeWHM(w, http.StatusOK, "gethostname", 1, "OK", map[string]any{"hostname": "mock.whm.local"})
	case "get_nameserver_config":
		// Nameservers offered for auto-population by the admin UI.
		writeWHM(w, http.StatusOK, "get_nameserver_config", 1, "OK", map[string]any{
			"nameservers": []string{"ns1.mock.local", "ns2.mock.local"},
		})
	case "createacct":
		s.whmCreateAcct(w, param)
	case "suspendacct":
		s.whmSuspendAcct(w, param)
	case "unsuspendacct":
		s.whmUnsuspendAcct(w, param)
	case "removeacct":
		s.whmRemoveAcct(w, param)
	case "changepackage":
		s.whmChangePackage(w, param)
	case "addpkg":
		s.whmAddPkg(w, r, param)
	case "editpkg":
		s.whmEditPkg(w, r, param)
	case "killpkg":
		s.whmKillPkg(w, param)
	case "listpkgs":
		s.whmListPkgs(w)
	case "passwd":
		s.whmPasswd(w, param)
	case "accountsummary":
		s.whmAccountSummary(w, param)
	case "listaccts":
		s.whmListAccts(w, param)
	case "create_user_session":
		s.whmCreateUserSession(w, param)
	default:
		writeWHM(w, http.StatusNotFound, fn, 0, "Unknown API function: "+fn, nil)
	}
}

func (s *Server) whmCreateAcct(w http.ResponseWriter, param func(...string) string) {
	username := param("username", "user")
	domain := param("domain")
	plan := param("plan", "pkg", "package")
	password := param("password", "pass")
	email := param("contactemail", "email")

	if username == "" || domain == "" {
		writeWHM(w, http.StatusOK, "createacct", 0, "missing required parameter: username and domain are required", nil)
		return
	}
	if username == "failme" {
		writeWHM(w, http.StatusOK, "createacct", 0, "forced failure", nil)
		return
	}

	s.mu.Lock()
	if _, exists := s.whmAccounts[username]; exists {
		s.mu.Unlock()
		writeWHM(w, http.StatusOK, "createacct", 0, "account exists", nil)
		return
	}
	s.whmAccounts[username] = &whmAccount{
		Username:  username,
		Domain:    domain,
		Plan:      plan,
		Password:  password,
		Email:     email,
		CreatedAt: time.Now(),
	}
	s.mu.Unlock()

	writeWHM(w, http.StatusOK, "createacct", 1, "OK", map[string]any{
		"username": username,
		"domain":   domain,
		"package":  plan,
		"ip":       "127.0.0.1",
	})
}

// withWHMAccount runs fn on the named account under lock; writes the standard
// "account does not exist" error when it is missing.
func (s *Server) withWHMAccount(w http.ResponseWriter, command, username string, fn func(a *whmAccount)) bool {
	if username == "" {
		writeWHM(w, http.StatusOK, command, 0, "missing required parameter: user", nil)
		return false
	}
	s.mu.Lock()
	acct, ok := s.whmAccounts[username]
	if ok {
		fn(acct)
	}
	s.mu.Unlock()
	if !ok {
		writeWHM(w, http.StatusOK, command, 0, "account does not exist", nil)
		return false
	}
	return true
}

func (s *Server) whmSuspendAcct(w http.ResponseWriter, param func(...string) string) {
	user := param("user", "username")
	reason := param("reason")
	if !s.withWHMAccount(w, "suspendacct", user, func(a *whmAccount) {
		a.Suspended = true
		a.SuspendReason = reason
	}) {
		return
	}
	writeWHM(w, http.StatusOK, "suspendacct", 1, "OK", nil)
}

func (s *Server) whmUnsuspendAcct(w http.ResponseWriter, param func(...string) string) {
	user := param("user", "username")
	if !s.withWHMAccount(w, "unsuspendacct", user, func(a *whmAccount) {
		a.Suspended = false
		a.SuspendReason = ""
	}) {
		return
	}
	writeWHM(w, http.StatusOK, "unsuspendacct", 1, "OK", nil)
}

func (s *Server) whmRemoveAcct(w http.ResponseWriter, param func(...string) string) {
	user := param("user", "username")
	if user == "" {
		writeWHM(w, http.StatusOK, "removeacct", 0, "missing required parameter: user", nil)
		return
	}
	s.mu.Lock()
	_, ok := s.whmAccounts[user]
	delete(s.whmAccounts, user)
	s.mu.Unlock()
	if !ok {
		writeWHM(w, http.StatusOK, "removeacct", 0, "account does not exist", nil)
		return
	}
	writeWHM(w, http.StatusOK, "removeacct", 1, "OK", nil)
}

func (s *Server) whmChangePackage(w http.ResponseWriter, param func(...string) string) {
	user := param("user", "username")
	pkg := param("pkg", "plan", "package")
	if pkg == "" {
		writeWHM(w, http.StatusOK, "changepackage", 0, "missing required parameter: pkg", nil)
		return
	}
	if !s.withWHMAccount(w, "changepackage", user, func(a *whmAccount) {
		a.Plan = pkg
	}) {
		return
	}
	writeWHM(w, http.StatusOK, "changepackage", 1, "OK", nil)
}

// whmPkgParams collects the package limit fields from the request form.
func whmPkgParams(r *http.Request) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"featurelist", "quota", "bwlimit", "maxaddon", "maxsub", "maxpark", "maxpop", "maxsql", "maxftp", "hasshell", "cgi"} {
		if v := r.Form.Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}

func (s *Server) whmAddPkg(w http.ResponseWriter, r *http.Request, param func(...string) string) {
	name := param("name", "pkg")
	if name == "" {
		writeWHM(w, http.StatusOK, "addpkg", 0, "missing required parameter: name", nil)
		return
	}
	s.mu.Lock()
	if _, exists := s.whmPackages[name]; exists {
		s.mu.Unlock()
		writeWHM(w, http.StatusOK, "addpkg", 0, "package already exists", nil)
		return
	}
	s.whmPackages[name] = &panelPackage{Name: name, Params: whmPkgParams(r), CreatedAt: time.Now()}
	s.mu.Unlock()
	writeWHM(w, http.StatusOK, "addpkg", 1, "OK", map[string]any{"pkg": name})
}

func (s *Server) whmEditPkg(w http.ResponseWriter, r *http.Request, param func(...string) string) {
	name := param("name", "pkg")
	s.mu.Lock()
	pkg, ok := s.whmPackages[name]
	if ok {
		pkg.Params = whmPkgParams(r)
	}
	s.mu.Unlock()
	if !ok {
		writeWHM(w, http.StatusOK, "editpkg", 0, "package does not exist", nil)
		return
	}
	writeWHM(w, http.StatusOK, "editpkg", 1, "OK", map[string]any{"pkg": name})
}

func (s *Server) whmKillPkg(w http.ResponseWriter, param func(...string) string) {
	name := param("pkg", "name")
	if name == "" {
		writeWHM(w, http.StatusOK, "killpkg", 0, "missing required parameter: pkg", nil)
		return
	}
	s.mu.Lock()
	_, ok := s.whmPackages[name]
	delete(s.whmPackages, name)
	s.mu.Unlock()
	if !ok {
		writeWHM(w, http.StatusOK, "killpkg", 0, "package does not exist", nil)
		return
	}
	writeWHM(w, http.StatusOK, "killpkg", 1, "OK", nil)
}

// whmListPkgs lists configured WHM package names (read-only, no auth beyond
// the standard WHM token check) - the real `listpkgs` counterpart to
// addpkg/editpkg/killpkg above.
func (s *Server) whmListPkgs(w http.ResponseWriter) {
	s.mu.Lock()
	names := make([]string, 0, len(s.whmPackages))
	for n := range s.whmPackages {
		names = append(names, n)
	}
	sort.Strings(names)
	pkgs := make([]map[string]any, 0, len(names))
	for _, n := range names {
		pkgs = append(pkgs, map[string]any{"name": n})
	}
	s.mu.Unlock()
	writeWHM(w, http.StatusOK, "listpkgs", 1, "OK", map[string]any{"pkg": pkgs})
}

func (s *Server) whmPasswd(w http.ResponseWriter, param func(...string) string) {
	user := param("user", "username")
	password := param("password", "pass")
	if password == "" {
		writeWHM(w, http.StatusOK, "passwd", 0, "missing required parameter: password", nil)
		return
	}
	if !s.withWHMAccount(w, "passwd", user, func(a *whmAccount) {
		a.Password = password
	}) {
		return
	}
	writeWHM(w, http.StatusOK, "passwd", 1, "OK", map[string]any{
		"app": []map[string]any{{"app": "system", "applist": "passwd"}},
	})
}

func whmAcctJSON(a *whmAccount) map[string]any {
	suspended := 0
	if a.Suspended {
		suspended = 1
	}
	return map[string]any{
		"user":          a.Username,
		"domain":        a.Domain,
		"plan":          a.Plan,
		"email":         a.Email,
		"suspended":     suspended,
		"suspendreason": a.SuspendReason,
		"ip":            "127.0.0.1",
		"startdate":     a.CreatedAt.UTC().Format("02 Jan 2006 15:04"),
	}
}

func (s *Server) whmAccountSummary(w http.ResponseWriter, param func(...string) string) {
	user := param("user", "username")
	var acctJSON map[string]any
	if !s.withWHMAccount(w, "accountsummary", user, func(a *whmAccount) {
		acctJSON = whmAcctJSON(a)
	}) {
		return
	}
	writeWHM(w, http.StatusOK, "accountsummary", 1, "OK", map[string]any{
		"acct": []map[string]any{acctJSON},
	})
}

// whmListAccts lists accounts, honoring real listaccts' search filter:
// `search` is a regex matched against the field selected by `searchtype`
// (package -> plan, domain -> domain, anything else -> username). The
// package variant is what the backend's PackageInUse guard calls before
// deleting a dynamic package.
func (s *Server) whmListAccts(w http.ResponseWriter, param func(...string) string) {
	searchType := param("searchtype")
	var re *regexp.Regexp
	if search := param("search"); search != "" {
		var err error
		if re, err = regexp.Compile(search); err != nil {
			writeWHM(w, http.StatusOK, "listaccts", 0, "invalid search regex: "+search, nil)
			return
		}
	}
	s.mu.Lock()
	usernames := make([]string, 0, len(s.whmAccounts))
	for u := range s.whmAccounts {
		usernames = append(usernames, u)
	}
	sort.Strings(usernames)
	accts := make([]map[string]any, 0, len(usernames))
	for _, u := range usernames {
		a := s.whmAccounts[u]
		if re != nil {
			hay := a.Username
			switch searchType {
			case "package":
				hay = a.Plan
			case "domain":
				hay = a.Domain
			}
			if !re.MatchString(hay) {
				continue
			}
		}
		accts = append(accts, whmAcctJSON(a))
	}
	s.mu.Unlock()
	writeWHM(w, http.StatusOK, "listaccts", 1, "OK", map[string]any{"acct": accts})
}

func (s *Server) whmCreateUserSession(w http.ResponseWriter, param func(...string) string) {
	user := param("user", "username")
	if !s.withWHMAccount(w, "create_user_session", user, func(*whmAccount) {}) {
		return
	}
	writeWHM(w, http.StatusOK, "create_user_session", 1, "OK", map[string]any{
		"url":     s.cfg.BaseURL + "/cpanel-sso/" + user,
		"session": "mock-session-" + user,
		"expires": time.Now().Add(5 * time.Minute).Unix(),
	})
}

// GET /cpanel-sso/{user} - landing page so SSO URLs actually resolve in E2E.
func (s *Server) handleCpanelSSO(w http.ResponseWriter, r *http.Request) {
	user := r.PathValue("user")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<!doctype html><html><head><title>cPanel Mock SSO</title></head><body><h1>cPanel Mock SSO</h1><p>Logged in as <span id="sso-user">%s</span></p></body></html>`, html.EscapeString(user))
}
