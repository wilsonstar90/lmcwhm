package server

import (
	"net/http/httptest"
	"testing"

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
