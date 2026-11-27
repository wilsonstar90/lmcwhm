package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pquerna/otp/totp"

	"golang.org/x/crypto/bcrypt"

	"github.com/wilsonstar90/lmcwhm/internal/apitoken"
	"github.com/wilsonstar90/lmcwhm/internal/audit"
	"github.com/wilsonstar90/lmcwhm/internal/auth"
	"github.com/wilsonstar90/lmcwhm/internal/config"
	"github.com/wilsonstar90/lmcwhm/internal/firewall"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

type env struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
	c   *http.Client
}

func newEnv(t *testing.T, mut func(*settings.Settings)) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	sm, err := settings.NewManager(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		if _, err := sm.Update(ctx, mut); err != nil {
			t.Fatal(err)
		}
	}
	a := auth.New(st)
	if err := a.CreateUser(ctx, "admin", "password123", true); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := apitoken.New(st, box)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Deps{
		Paths: config.Paths{CertDir: filepath.Join(dir, "ssl")}, Settings: sm, Auth: a, Audit: audit.New(st),
		WebFS: fstest.MapFS{
			"index.html":    {Data: []byte("<html>spa</html>")},
			"sw.js":         {Data: []byte("// service worker")},
			"assets/app.js": {Data: []byte("console.log('app')")},
		},
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store: st, Tasks: tasks.NewManager(),
		Tokens: tokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &env{t: t, srv: srv, ts: ts, c: c}
}

func (e *env) do(method, path string, body any, hdr map[string]string) (*http.Response, map[string]any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

func TestSecurityEntryGate(t *testing.T) {
	e := newEnv(t, func(s *settings.Settings) { s.SecurityEntry = "/console" })

	for _, p := range []string{"/", "/login", "/api/v1/public/meta"} {
		if r, _ := e.do("GET", p, nil, nil); r.StatusCode != 404 {
			t.Fatalf("%s without entry: got %d want 404", p, r.StatusCode)
		}
	}
	r, _ := e.do("GET", "/console", nil, nil)
	if r.StatusCode != 302 || r.Header.Get("Location") != "/login" {
		t.Fatalf("entry: got %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	var entry *http.Cookie
	for _, c := range r.Cookies() {
		if c.Name == entryCookie {
			entry = c
		}
	}
	if entry == nil {
		t.Fatal("entry response did not set an entry cookie")
	}
	if entry.SameSite != http.SameSiteLaxMode {
		t.Fatalf("entry cookie SameSite = %v, want Lax for identity provider redirects", entry.SameSite)
	}
	if !entry.HttpOnly {
		t.Fatal("entry cookie is not HttpOnly")
	}
	if r, _ := e.do("GET", "/api/v1/public/meta", nil, nil); r.StatusCode != 200 {
		t.Fatalf("after entry: got %d", r.StatusCode)
	}

	// Changing the entry invalidates the old cookie.
	e.srv.Settings.Update(context.Background(), func(s *settings.Settings) { s.SecurityEntry = "/other1" })
	if r, _ := e.do("GET", "/api/v1/public/meta", nil, nil); r.StatusCode != 404 {
		t.Fatalf("old entry cookie still accepted: %d", r.StatusCode)
	}
}

func TestUnauthStatus(t *testing.T) {
	e := newEnv(t, func(s *settings.Settings) { s.SecurityEntry = "/console"; s.UnauthStatus = 403 })
	r, _ := e.do("GET", "/login", nil, nil)
	if r.StatusCode != 403 {
		t.Fatalf("got %d want 403", r.StatusCode)
	}
	e.srv.Settings.Update(context.Background(), func(s *settings.Settings) { s.UnauthStatus = 444 })
	if _, err := e.c.Get(e.ts.URL + "/login"); err == nil {
		t.Fatal("444 answered instead of closing the connection")
	}
	if _, err := e.srv.Settings.Update(context.Background(), func(s *settings.Settings) { s.UnauthStatus = 302 }); err == nil {
		t.Fatal("302 accepted as an unauthenticated response")
	}
}

func TestLoginCSRFAndLockout(t *testing.T) {
	e := newEnv(t, func(s *settings.Settings) { s.LoginMaxFails = 3 })

	if r, _ := e.do("GET", "/api/v1/auth/me", nil, nil); r.StatusCode != 401 {
		t.Fatalf("me without session: %d", r.StatusCode)
	}
	r, body := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "password123"}, nil)
	if r.StatusCode != 200 {
		t.Fatalf("login: %d %v", r.StatusCode, body)
	}
	csrf, _ := body["csrf"].(string)

	if r, b := e.do("PUT", "/api/v1/settings", map[string]any{"panel_alias": "x"}, nil); r.StatusCode != 403 || b["code"] != "auth.csrf" {
		t.Fatalf("put without csrf: %d %v", r.StatusCode, b)
	}
	if r, b := e.do("PUT", "/api/v1/settings", map[string]any{"panel_alias": "mypanel"}, map[string]string{"X-CSRF-Token": csrf}); r.StatusCode != 200 {
		t.Fatalf("put with csrf: %d %v", r.StatusCode, b)
	}
	if got := e.srv.Settings.Get().PanelAlias; got != "mypanel" {
		t.Fatalf("alias not saved: %q", got)
	}
	// Partial update must keep other fields.
	if got := e.srv.Settings.Get().LoginMaxFails; got != 3 {
		t.Fatalf("partial update clobbered login_max_fails: %d", got)
	}

	e.c.Jar, _ = cookiejar.New(nil)
	for i := 0; i < 3; i++ {
		if r, b := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong"}, nil); b["code"] != "auth.bad_credentials" {
			t.Fatalf("attempt %d: %d %v", i, r.StatusCode, b)
		}
	}
	if r, b := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "password123"}, nil); r.StatusCode != 429 || b["code"] != "auth.locked" {
		t.Fatalf("expected lockout, got %d %v", r.StatusCode, b)
	}
}

func TestIPAndDomainAndBasicAuth(t *testing.T) {
	e := newEnv(t, func(s *settings.Settings) { s.AllowedIPs = []string{"203.0.113.1"} })
	// httptest connects from 127.0.0.1, which is always allowed (local rescue).
	if r, _ := e.do("GET", "/api/v1/public/meta", nil, nil); r.StatusCode != 200 {
		t.Fatalf("loopback blocked: %d", r.StatusCode)
	}

	e = newEnv(t, func(s *settings.Settings) { s.BindDomain = "panel.example.com" })
	if r, _ := e.do("GET", "/api/v1/public/meta", nil, nil); r.StatusCode != 404 {
		t.Fatalf("wrong host allowed: %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/public/meta", nil)
	req.Host = "panel.example.com"
	if r, err := e.c.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("bound host rejected: %v %v", err, r)
	}

	h, _ := bcrypt.GenerateFromPassword([]byte("bapass"), bcrypt.MinCost)
	e = newEnv(t, func(s *settings.Settings) { s.BasicAuthUser, s.BasicAuthHash = "ba", string(h) })
	if r, _ := e.do("GET", "/api/v1/public/meta", nil, nil); r.StatusCode != 401 {
		t.Fatalf("basic auth not enforced: %d", r.StatusCode)
	}
	u, _ := url.Parse(e.ts.URL)
	u.User = url.UserPassword("ba", "bapass")
	if r, err := e.c.Get(u.String() + "/api/v1/public/meta"); err != nil || r.StatusCode != 200 {
		t.Fatalf("basic auth with creds: %v %v", err, r)
	}
}

func TestCloudflareTunnelPanelClientIP(t *testing.T) {
	const client = "203.0.113.55"
	e := newEnv(t, func(s *settings.Settings) { s.AllowedIPs = []string{client} })
	fw := firewall.New(e.srv.Store, nil, nil)
	e.srv.Firewall = fw
	if _, err := fw.SaveCDN(context.Background(), firewall.CDNConfig{CloudflareTunnel: true}); err != nil {
		t.Fatal(err)
	}
	cf := map[string]string{"CF-Connecting-IP": client, "X-Forwarded-Proto": "https"}
	if r, _ := e.do("GET", "/api/v1/public/meta", nil, cf); r.StatusCode != http.StatusOK {
		t.Fatalf("allowed Cloudflare client rejected: %d", r.StatusCode)
	}
	if r, _ := e.do("GET", "/api/v1/public/meta", nil, map[string]string{"CF-Connecting-IP": "203.0.113.66"}); r.StatusCode != http.StatusNotFound {
		t.Fatalf("different Cloudflare client bypassed panel allow-list: %d", r.StatusCode)
	}
	if _, err := e.srv.Settings.Update(context.Background(), func(s *settings.Settings) { s.AllowedIPs = nil }); err != nil {
		t.Fatal(err)
	}
	_, secret, err := e.srv.Tokens.Create(context.Background(), apitoken.Input{Name: "tunnel", Scope: "read", IPs: []string{client}})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + secret, "CF-Connecting-IP": client}
	if r, _ := e.do("GET", "/api/v1/auth/me", nil, headers); r.StatusCode != http.StatusOK {
		t.Fatalf("token's real client IP rejected: %d", r.StatusCode)
	}
	headers["CF-Connecting-IP"] = "203.0.113.66"
	if r, _ := e.do("GET", "/api/v1/auth/me", nil, headers); r.StatusCode != http.StatusNotFound {
		t.Fatalf("token IP allow-list used the proxy peer: %d", r.StatusCode)
	}
	r, _ := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "password123"}, cf)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("tunnel login: %d", r.StatusCode)
	}
	if len(r.Cookies()) == 0 || !r.Cookies()[0].Secure {
		t.Fatal("externally HTTPS tunnel session cookie is not Secure")
	}
	logins, _, err := e.srv.Audit.ListLogins(context.Background(), 10, 0)
	if err != nil || len(logins) == 0 || logins[0].IP != client {
		t.Fatalf("login audit did not use real client: %+v %v", logins, err)
	}
}

func TestSettingsNeverLeakBasicAuthHash(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("bapass"), bcrypt.MinCost)
	e := newEnv(t, nil)
	e.srv.Settings.Update(context.Background(), func(s *settings.Settings) { s.BasicAuthHash = string(h) })
	_, body := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "password123"}, nil)
	_ = body
	_, s := e.do("GET", "/api/v1/settings", nil, nil)
	if _, ok := s["basic_auth_hash"]; ok {
		t.Fatal("basic_auth_hash leaked to client")
	}
}

func TestSPAFallback(t *testing.T) {
	e := newEnv(t, nil)
	req, _ := http.NewRequest("GET", e.ts.URL+"/settings", nil)
	r, err := e.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || string(b) != "<html>spa</html>" {
		t.Fatalf("spa fallback: %d %q", r.StatusCode, b)
	}
	r.Body.Close()

	for path, want := range map[string]string{
		"/sw.js":         "no-cache",
		"/assets/app.js": "public, max-age=31536000, immutable",
	} {
		r, err = e.c.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Header.Get("Cache-Control"); got != want {
			t.Fatalf("%s Cache-Control: got %q want %q", path, got, want)
		}
		r.Body.Close()
	}
}

func TestTOTPBeginRefusedWhenEnabled(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	u, _ := e.srv.Auth.PrimaryAdmin(ctx)
	secret, _, err := e.srv.Auth.BeginTOTP(ctx, u, "test")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	if err := e.srv.Auth.EnableTOTP(ctx, u.ID, code); err != nil {
		t.Fatal(err)
	}
	code, _ = totp.GenerateCode(secret, time.Now())
	_, body := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "password123", "totp": code}, nil)
	csrf, _ := body["csrf"].(string)
	r, b := e.do("POST", "/api/v1/settings/2fa/begin", map[string]any{}, map[string]string{"X-CSRF-Token": csrf})
	if r.StatusCode != 409 || b["code"] != "auth.totp_already_enabled" {
		t.Fatalf("begin with 2FA on: %d %v", r.StatusCode, b)
	}
	if u, _ := e.srv.Auth.PrimaryAdmin(ctx); !u.TOTPEnabled {
		t.Fatal("2FA was disabled by begin")
	}
}

func TestLogo(t *testing.T) {
	e := newEnv(t, nil)
	_, body := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "password123"}, nil)
	csrf, _ := body["csrf"].(string)
	if r, _ := e.do("GET", "/api/v1/public/logo", nil, nil); r.StatusCode != 404 {
		t.Fatalf("no logo: %d", r.StatusCode)
	}
	up := func(body string) int {
		req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/settings/logo", strings.NewReader(body))
		req.Header.Set("X-CSRF-Token", csrf)
		r, err := e.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if c := up("<html><script>alert(1)</script>"); c != 400 {
		t.Fatalf("HTML accepted as a logo: %d", c)
	}
	if c := up(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`); c != 200 {
		t.Fatalf("svg upload: %d", c)
	}
	r, _ := e.do("GET", "/api/v1/public/logo", nil, nil)
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/svg+xml" || !strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("logo: %d %v", r.StatusCode, r.Header)
	}
}

// Browsers reach an HTTPS panel over HTTP/2, where 444 must still close
// the stream without an answer.
func TestUnauthStatusHTTP2(t *testing.T) {
	e := newEnv(t, func(s *settings.Settings) { s.SecurityEntry = "/console"; s.UnauthStatus = 444 })
	ts := httptest.NewUnstartedServer(e.ts.Config.Handler)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	c := ts.Client()
	if r, err := c.Get(ts.URL + "/login"); err == nil {
		t.Fatalf("HTTP/2: got %s (%s) instead of an aborted stream", r.Status, r.Proto)
	}
}
