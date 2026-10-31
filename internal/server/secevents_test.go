package server

import (
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
	"github.com/wilsonstar90/lmcwhm/internal/security"
)

func TestEdgeEvent(t *testing.T) {
	for in, want := range map[[2]string][3]string{
		{"rule-wp-1002", ""}: {"blocked", "rule", "wp-1002"},
		{"", "rule-c9"}:      {"detected", "rule", "c9"},
		{"cc", ""}:           {"limited", "cc", "cc"},
		{"ip", ""}:           {"blocked", "ip", "ip"},
		{"fake_spider", ""}:  {"blocked", "protection", "fake_spider"},
	} {
		e, ok := edgeEvent(logcenter.Access{Block: in[0], Detect: in[1], IP: "1.2.3.4"}, "a.com")
		if !ok || e.Action != want[0] || e.Source != want[1] || e.Rule != want[2] || e.Site != "a.com" {
			t.Errorf("%v → %+v", in, e)
		}
	}
	if _, ok := edgeEvent(logcenter.Access{Status: 403}, "a.com"); ok {
		t.Error("a 403 without a reason (the WAF's, already in its log) became an edge event")
	}
	w := wafEvent(security.Event{Host: "www.a.com:443", Blocked: true, Messages: []security.EventRule{{ID: "942100", Message: "SQLi"}}},
		func(h string) string { return map[string]string{"www.a.com": "a.com"}[h] })
	if w.Site != "a.com" || w.Rule != "942100" || w.Action != "blocked" || w.Source != "waf" {
		t.Errorf("waf %+v", w)
	}
}
