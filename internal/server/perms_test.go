package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/apitoken"
	"github.com/wilsonstar90/lmcwhm/internal/auth"
)

func TestAllowed(t *testing.T) {
	ops := auth.User{Username: "ops", Enabled: true, Perms: []string{"website", "files"}, Sites: []int64{7}}
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/v1/sites", true},
		{"POST", "/api/v1/sites", false}, // scoped users cannot create sites
		{"GET", "/api/v1/sites/7", true},
		{"PATCH", "/api/v1/sites/7", true},
		{"PATCH", "/api/v1/sites/8", false},
		{"GET", "/api/v1/wordpress/7", true},
		{"GET", "/api/v1/web/config", false},
		{"GET", "/api/v1/files/list", true},
		{"POST", "/api/v1/terminal/ticket", false},
		{"GET", "/api/v1/settings", false},
		{"POST", "/api/v1/settings/password", true},
		{"GET", "/api/v1/users", false},
		{"GET", "/api/v1/tokens", false},
		{"GET", "/api/v1/brand-new-route", false},
		{"GET", "/api/v1/auth/me", true},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := allowed(ops, r); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.method, c.path, got, c.want)
		}
	}
	if !allowed(auth.User{IsAdmin: true}, httptest.NewRequest("GET", "/api/v1/users", nil)) {
		t.Error("admin denied")
	}
}

func TestRESTTokensAndOpenAPI(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	_, readTok, _ := e.srv.Tokens.Create(ctx, apitoken.Input{Name: "ro", Scope: "read"})
	_, opTok, _ := e.srv.Tokens.Create(ctx, apitoken.Input{Name: "op", Scope: "operate"})
	_, adminTok, _ := e.srv.Tokens.Create(ctx, apitoken.Input{Name: "root", Scope: "admin"})
	bearer := func(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

	if r, _ := e.do("GET", "/api/v1/auth/me", nil, bearer("lmc_wrong")); r.StatusCode != 401 {
		t.Fatalf("bad token: %d", r.StatusCode)
	}
	if r, b := e.do("GET", "/api/v1/auth/me", nil, bearer(readTok)); r.StatusCode != 200 || b["user"].(map[string]any)["username"] != "token:ro" {
		t.Fatalf("read token GET: %d %v", r.StatusCode, b)
	}
	if r, _ := e.do("PUT", "/api/v1/settings", map[string]any{"panel_alias": "x"}, bearer(readTok)); r.StatusCode != 401 {
		t.Fatalf("read token PUT: %d", r.StatusCode)
	}
	if r, _ := e.do("GET", "/api/v1/settings", nil, bearer(opTok)); r.StatusCode != 403 {
		t.Fatalf("operate token on admin route: %d", r.StatusCode)
	}
	if r, _ := e.do("PUT", "/api/v1/settings", map[string]any{"panel_alias": "viaapi"}, bearer(adminTok)); r.StatusCode != 200 {
		t.Fatalf("admin token PUT (no CSRF needed): %d", r.StatusCode)
	}
	r, spec := e.do("GET", "/api/v1/openapi.json", nil, bearer(adminTok))
	if r.StatusCode != 200 || spec["openapi"] != "3.0.3" {
		t.Fatalf("openapi: %d", r.StatusCode)
	}
	paths := spec["paths"].(map[string]any)
	if _, ok := paths["/users/{id}"].(map[string]any)["put"]; !ok {
		t.Fatalf("missing PUT /users/{id}")
	}
	if _, ok := paths["/settings"].(map[string]any)["get"]; !ok {
		t.Fatal("missing GET /settings")
	}
}

func TestSubUserSession(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	if _, err := e.srv.Auth.AddUser(ctx, auth.UserInput{Username: "ops", Password: "password123", Perms: []string{"files"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	r, body := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "ops", "password": "password123"}, nil)
	if r.StatusCode != 200 {
		t.Fatalf("login: %d", r.StatusCode)
	}
	if r, b := e.do("GET", "/api/v1/settings", nil, nil); r.StatusCode != 403 || b["code"] != "auth.forbidden" {
		t.Fatalf("sub-user on settings: %d %v", r.StatusCode, b)
	}
	if r, _ := e.do("GET", "/api/v1/users", nil, nil); r.StatusCode != 403 {
		t.Fatalf("sub-user on users: %d", r.StatusCode)
	}
	csrf := body["csrf"].(string)
	if r, b := e.do("POST", "/api/v1/settings/password", map[string]string{"current": "password123", "new": "password456"}, map[string]string{"X-CSRF-Token": csrf}); r.StatusCode == 403 {
		t.Fatalf("own password change forbidden: %v", b)
	}
}
