package firewall

import (
	"strings"
	"testing"
)

func TestPortsCover(t *testing.T) {
	for ports, want := range map[string]bool{"3306": true, "80,3306": true, "3000-4000": true, "3307": false, "80, 443": false} {
		if portsCover(ports, 3306) != want {
			t.Errorf("%q", ports)
		}
	}
}

func TestSourcesCover(t *testing.T) {
	cases := []struct {
		have string
		want []string
		ok   bool
	}{
		{"", nil, true},
		{"", []string{"1.2.3.4"}, true},
		{"1.2.3.0/24", []string{"1.2.3.4"}, true},
		{"1.2.3.0/24", nil, false},
		{"1.2.3.4", []string{"1.2.3.4", "2001:db8::1"}, false},
		{"1.2.3.4,2001:db8::/32", []string{"1.2.3.4", "2001:db8::1"}, true},
		{"10.0.0.0/16", []string{"10.0.0.0/8"}, false},
	}
	for _, c := range cases {
		if sourcesCover(c.have, c.want) != c.ok {
			t.Errorf("%q covers %v: want %v", c.have, c.want, c.ok)
		}
	}
}

func TestGuardRules(t *testing.T) {
	rules := []Rule{
		{ID: 1, Action: "accept", Proto: "tcp", Ports: "3306", Source: "1.2.3.4,2001:db8::1", Enabled: true},
		{ID: 2, Action: "accept", Proto: "tcp", Ports: "80", Enabled: true},
		{ID: 3, Action: "accept", Proto: "tcp", Ports: "3306", Enabled: false},
	}
	in, fw := guardRules(rules, []int{3306})
	for _, want := range []string{
		"ip saddr 1.2.3.4 meta l4proto tcp ct original proto-dst 3306 ct state new accept",
		"ip6 saddr 2001:db8::1 meta l4proto tcp ct original proto-dst 3306 ct state new accept",
		"ct original proto-dst 3306 ct status dnat ct state new drop",
	} {
		if !strings.Contains(fw, want) {
			t.Errorf("forward lacks %q:\n%s", want, fw)
		}
	}
	if strings.Contains(fw, "rule 2") || strings.Contains(fw, "rule 3") {
		t.Errorf("other rules leaked:\n%s", fw)
	}
	if !strings.Contains(in, `iifname != "docker0" iifname != "br-*" tcp dport 3306 ct state new drop`) {
		t.Errorf("input guard:\n%s", in)
	}
	if a, b := guardRules(rules, nil); a != "" || b != "" {
		t.Error("guard without ports")
	}
}
