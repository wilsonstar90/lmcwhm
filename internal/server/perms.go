package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/wilsonstar90/lmcwhm/internal/auth"
	"github.com/wilsonstar90/lmcwhm/internal/mcp"
)

// Module permissions of panel sub-users, and REST access with API tokens.

var errForbidden = errors.New("auth.forbidden")

// moduleOf maps the first path segment under /api/v1 to a panel module.
// "" = any signed-in user; "admin" = administrators only (also the default
// for anything not listed, so a new route is closed until it is mapped).
var moduleOf = map[string]string{
	"auth": "", "tasks": "", "geoip": "", "openapi.json": "",
	"sites": "website", "routes": "website", "web": "website", "wordpress": "website", "sitestats": "website", "varnish": "website",
	"dns":       "domains",
	"databases": "database",
	"docker":    "docker",
	"apps":      "appstore", "php": "appstore",
	"files":     "files",
	"terminal":  "terminal",
	"ftp":       "ftp",
	"cron":      "cron",
	"processes": "processes",
	"monitor":   "monitor",
	"logs":      "logs",
	"probes":    "uptime",
	"alerts":    "alerts",
	"firewall":  "security", "crowdsec": "security", "waf": "security", "tamper": "security", "malware": "security", "secscan": "security",
	"backups": "backup", "snapshots": "backup",
	"toolbox": "toolbox",
	"mail":    "mail",
	"ai":      "ai", "mcp": "ai",
}

// selfService paths act on the caller's own account.
var selfService = map[string]bool{
	"/settings/password": true, "/settings/username": true, "/settings/2fa/begin": true, "/settings/2fa/enable": true,
	"/settings/2fa/disable": true, "/system/overview": true, "/system/summary": true,
}

// siteScoped are the website routes a site-restricted user may call, with the
// position of the site id; every other website route needs full access.
var siteScoped = regexp.MustCompile(`^/(sites|wordpress|wordpress/install)/(\d+)(/|$)`)

// module returns the module of an /api/v1 path.
func module(p string) string {
	p = strings.TrimPrefix(p, "/api/v1")
	if selfService[p] {
		return ""
	}
	seg, _, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	if m, ok := moduleOf[seg]; ok {
		return m
	}
	return "admin"
}

// allowed checks a request against the user's modules and site scope.
func allowed(u auth.User, r *http.Request) bool {
	if u.IsAdmin {
		return true
	}
	m := module(r.URL.Path)
	switch m {
	case "":
		return true
	case "admin":
		return false
	}
	if !u.Can(m) {
		return false
	}
	if m == "website" && len(u.Sites) > 0 {
		p := strings.TrimPrefix(r.URL.Path, "/api/v1")
		if r.Method == http.MethodGet && (p == "/sites" || p == "/web/status") {
			return true // the handler filters the list
		}
		if p == "/varnish/purge" {
			return true // checked in the handler
		}
		mm := siteScoped.FindStringSubmatch(p)
		if mm == nil {
			return false
		}
		id, _ := strconv.ParseInt(mm[2], 10, 64)
		return u.CanSite(id)
	}
	return true
}

// tokenUser authenticates "Authorization: Bearer lmc_..." for the REST API.
// read tokens may only GET; operate tokens get every module but not the
// admin-only routes; admin tokens get everything.
func (s *Server) tokenUser(r *http.Request) (auth.User, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || s.Tokens == nil {
		return auth.User{}, false
	}
	t, err := s.Tokens.Verify(r.Context(), strings.TrimSpace(tok), clientIP(r))
	if err != nil {
		return auth.User{}, false
	}
	u := auth.User{ID: 0, Username: "token:" + t.Name, Enabled: true, Perms: append([]string{}, auth.Modules...)}
	switch t.Scope {
	case mcp.ScopeAdmin:
		u.IsAdmin = true
	case mcp.ScopeRead:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			return auth.User{}, false
		}
	}
	return u, true
}

func (s *Server) withUser(r *http.Request, sess auth.Session) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), sessionKey, sess))
}
