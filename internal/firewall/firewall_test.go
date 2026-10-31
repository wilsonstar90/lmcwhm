package firewall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

// newSvc uses a fake nft that records every script it is given.
func newSvc(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "nft.log")
	fake := filepath.Join(dir, "nft")
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"== $*\" >> "+log+"\ncat >> "+log+"\n"), 0o755)
	sv := New(st, box, nil)
	sv.nft = fake
	sv.PanelPort = func() int { return 8888 }
	return sv, log
}

func TestNormIP(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3.4": "1.2.3.4", " 10.1.2.3/8": "10.0.0.0/8", "1.2.3.4/32": "1.2.3.4", "::ffff:1.2.3.4": "1.2.3.4",
		"2001:DB8::1": "2001:db8::1", "2001:db8::/32": "2001:db8::/32", "0.0.0.0/0": "", "::/0": "", "x": "",
	} {
		_, got, err := NormIP(in)
		if (err != nil) != (want == "") || got != want {
			t.Errorf("NormIP(%q) = %q, %v", in, got, err)
		}
	}
	for p, ok := range map[string]bool{"22": true, "80,443": true, "8000-9000": true, "0": false, "70000": false, "9-1": false, "1,": false} {
		if validPorts(p) != ok {
			t.Errorf("validPorts(%s) != %v", p, ok)
		}
	}
}

func TestRuleset(t *testing.T) {
	sv, _ := newSvc(t)
	ctx := context.Background()
	sv.SaveRule(ctx, Rule{Action: "drop", Proto: "both", Ports: "3306, 6379", Enabled: true})
	sv.SaveRule(ctx, Rule{Action: "accept", Proto: "tcp", Ports: "9000-9100", Source: "203.0.113.0/24", Enabled: true})
	// Several sources, one per line, both families.
	if _, _, err := sv.SaveRule(ctx, Rule{Action: "accept", Proto: "tcp", Ports: "9200", Source: "198.51.100.1\n198.51.100.2, 2001:db8::/32\n198.51.100.1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sv.SaveRule(ctx, Rule{Action: "accept", Proto: "tcp", Ports: "9201", Source: "198.51.100.1\nnot-an-ip", Enabled: true}); err == nil {
		t.Fatal("bad source accepted")
	}
	sv.SaveRule(ctx, Rule{Action: "accept", Proto: "tcp", Ports: "1", Enabled: false})
	sv.AddIP(ctx, "198.51.100.7", "block", 0, "manual", "")
	sv.AddIP(ctx, "2001:db8::/48", "block", 0, "manual", "")
	sv.AddIP(ctx, "192.0.2.9", "block", time.Hour, "crowdsec", "")
	sv.AddIP(ctx, "192.0.2.10", "allow", 0, "manual", "")
	if _, err := sv.AddIP(ctx, "0.0.0.0/0", "block", 0, "manual", ""); err == nil {
		t.Error("blocked the whole Internet")
	}
	rs, err := sv.Ruleset(ctx, Config{Enabled: true, Policy: "drop", Ping: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"table inet lmcwhm {", "policy drop;", "set block4 { type ipv4_addr; flags interval; auto-merge; elements = { 198.51.100.7 }; }",
		"set block6 { type ipv6_addr; flags interval; auto-merge; elements = { 2001:db8::/48 }; }", "192.0.2.9 timeout 36",
		"elements = { 192.0.2.10 }", "icmp type echo-request drop", "meta l4proto { icmp, ipv6-icmp } accept",
		"tcp dport { 8888 } accept comment \"system panel\"", "tcp dport { 80, 443 } accept comment \"system web\"", "tcp dport { 3306, 6379 } drop", "udp dport { 3306, 6379 } drop",
		"ip saddr 203.0.113.0/24 tcp dport { 9000-9100 } accept",
		"ip saddr { 198.51.100.1, 198.51.100.2 } tcp dport { 9200 } accept", "ip6 saddr 2001:db8::/32 tcp dport { 9200 } accept", "iifname \"docker0\" accept", "chain forward",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("ruleset missing %q:\n%s", want, rs)
		}
	}
	if strings.Contains(rs, "dport { 1 }") {
		t.Error("disabled rule rendered")
	}
	// System rules come before user rules, so a drop rule cannot lock SSH out.
	if !strings.Contains(rs, "comment \"system ssh\"") || strings.Index(rs, "system ssh") > strings.Index(rs, "rule 1\"") {
		t.Error("system rules missing or after user rules")
	}
}

func TestSystemRules(t *testing.T) {
	sv, _ := newSvc(t)
	ctx := context.Background()
	rules, err := sv.Rules(ctx)
	if err != nil || len(rules) != 3 {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	panel := rules[1]
	if panel.System != "panel" || panel.Ports != "8888" || !panel.Enabled {
		t.Fatalf("panel rule = %+v", panel)
	}
	// Ports follow the panel; the kind survives an edit that tries to change it.
	sv.PanelPort = func() int { return 9443 }
	panel.Ports, panel.System, panel.Enabled = "1", "", false
	if _, _, err := sv.SaveRule(ctx, panel); err != nil {
		t.Fatal(err)
	}
	rules, _ = sv.Rules(ctx)
	if rules[1].System != "panel" || rules[1].Ports != "9443" || rules[1].Enabled {
		t.Fatalf("after edit = %+v", rules[1])
	}
	if got := sv.ProtectedPorts(ctx); strings.Contains(joinInts(got), "9443") {
		t.Errorf("disabled rule still protected: %v", got)
	}
	// Deleted system rules stay deleted.
	sv.DeleteRule(ctx, rules[0].ID)
	sv.seeded.Store(false)
	if rules, _ = sv.Rules(ctx); len(rules) != 2 {
		t.Errorf("deleted system rule came back: %+v", rules)
	}
}

func TestDropPolicyRevert(t *testing.T) {
	ConfirmWindow = 200 * time.Millisecond
	sv, log := newSvc(t)
	ctx := context.Background()
	_, armed, err := sv.Update(ctx, func(c *Config) { c.Enabled, c.Policy = true, "drop" })
	if err != nil || !armed || !sv.Pending() {
		t.Fatalf("armed=%v err=%v", armed, err)
	}
	time.Sleep(400 * time.Millisecond)
	c, _ := sv.Config(ctx)
	if c.Policy != "accept" || c.Unconfirmed || sv.Pending() {
		t.Fatalf("not reverted: %+v", c)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "policy drop") || !strings.HasSuffix(strings.TrimSpace(string(b)), "}") || strings.LastIndex(string(b), "policy accept") < strings.LastIndex(string(b), "policy drop") {
		t.Fatalf("revert not installed:\n%s", b)
	}
	// Confirmed in time: stays.
	sv.Update(ctx, func(c *Config) { c.Policy = "drop" })
	if err := sv.Confirm(ctx, time.Now().Add(-time.Hour)); err != ErrOldConnection {
		t.Fatalf("old connection confirmed: %v", err)
	}
	if err := sv.Confirm(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if c, _ := sv.Config(ctx); c.Policy != "drop" || c.Unconfirmed {
		t.Fatalf("confirmed drop reverted: %+v", c)
	}
	// A restart while unconfirmed reverts at boot.
	sv.Update(ctx, func(c *Config) { c.Policy = "drop" })
	sv2 := New(sv.st, sv.box, nil)
	sv2.nft = sv.nft
	bctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sv2.Boot(bctx)
	if c, _ := sv2.Config(ctx); c.Policy != "accept" {
		t.Fatalf("boot kept unconfirmed drop: %+v", c)
	}
}

func TestSyncBans(t *testing.T) {
	sv, _ := newSvc(t)
	ctx := context.Background()
	sv.AddIP(ctx, "198.51.100.1", "block", 0, "manual", "keep me")
	add := []Ban{{IP: "198.51.100.1", TTL: time.Hour}, {IP: "203.0.113.5", TTL: time.Hour}, {IP: "203.0.113.0/24", TTL: time.Hour}, {IP: "bad", TTL: time.Hour}}
	if err := sv.SyncBans(ctx, "crowdsec", add, nil); err != nil {
		t.Fatal(err)
	}
	ips, _ := sv.IPs(ctx, "block")
	got := map[string]IP{}
	for _, x := range ips {
		got[x.IP] = x
	}
	if len(got) != 3 || got["198.51.100.1"].Source != "manual" || got["198.51.100.1"].ExpiresAt != 0 || got["203.0.113.5"].Source != "crowdsec" {
		t.Fatalf("%+v", got)
	}
	sv.SyncBans(ctx, "crowdsec", nil, []string{"203.0.113.5", "198.51.100.1"})
	ips, _ = sv.IPs(ctx, "block")
	if len(ips) != 2 {
		t.Fatalf("after removal %+v", ips)
	}
}

func TestDropLog(t *testing.T) {
	sv, _ := newSvc(t)
	ctx := context.Background()
	sv.SaveRule(ctx, Rule{Action: "drop", Proto: "udp", Ports: "53", Enabled: true})
	rs, _ := sv.Ruleset(ctx, Config{Enabled: true, Policy: "drop"})
	for _, want := range []string{
		"ip saddr @block4 ip saddr != @trust4 limit rate 10/second burst 20 packets log prefix \"lmcwhm-drop block \" level info\n\t\tip saddr @block4 ip saddr != @trust4 counter drop",
		"ip6 saddr @ban6 ip6 saddr != @trust6 limit rate 10/second burst 20 packets log prefix \"lmcwhm-drop ban \"",
		"udp dport { 53 } limit rate 10/second burst 20 packets log prefix \"lmcwhm-drop rule1 \"",
		"ct state new limit rate 10/second burst 20 packets log prefix \"lmcwhm-drop policy \"",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("ruleset missing %q:\n%s", want, rs)
		}
	}
	if rs, _ := sv.Ruleset(ctx, Config{Enabled: true, Policy: "accept"}); strings.Contains(rs, "drop policy") {
		t.Error("policy drop logged under accept")
	}
}
