package server

import (
	"net/http"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/settings"
)

func TestHealthzBypassesBrowserGates(t *testing.T) {
	e := newEnv(t, func(s *settings.Settings) {
		s.SecurityEntry = "/console"
		s.BindDomain = "panel.example.com"
		s.UnauthStatus = 444
	})
	e.srv.Readiness = NewReadiness()
	e.srv.Readiness.Set("web", true)
	r, body := e.do("GET", "/healthz", nil, nil)
	if r.StatusCode != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("healthz: %d %v", r.StatusCode, body)
	}
	components := body["components"].(map[string]any)
	if components["web"] != "ready" {
		t.Fatalf("web readiness missing: %v", body)
	}
}

func TestHealthzReportsNotReady(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Readiness = NewReadiness()
	e.srv.Readiness.Set("apps", false)
	r, body := e.do("GET", "/healthz", nil, nil)
	if r.StatusCode != http.StatusServiceUnavailable || body["status"] != "unavailable" {
		t.Fatalf("not-ready healthz: %d %v", r.StatusCode, body)
	}
	components := body["components"].(map[string]any)
	if components["apps"] != "not_ready" {
		t.Fatalf("failed component missing: %v", body)
	}
}
