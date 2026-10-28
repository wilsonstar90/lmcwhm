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
	sv.SaveRule(ctx, Rule{Action: "accept", Proto: "tcp", Ports: "1", Enabled: false})
	sv.AddIP(ctx, "198.51.100.7", "block", 0, "manual", "")
	sv.AddIP(ctx, "2001:db8::/48", "block", 0, "manual", "")
	sv.AddIP(ctx, "192.0.2.9", "block", time.Hour, "crowdsec", "")
	sv.AddIP(ctx, "192.0.2.10", "allow", 0, "manual", "")
	if _, err := sv.AddIP(ctx, "0.0.0.0/0", "block", 0, "manual", ""); err == nil {
		t.Error("blocked the whole Internet")
	}
	rs, err := sv.Ruleset(ctx, Config{Enabled: true, Policy: "drop", Ping: false, Protect: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"table inet lmcwhm {", "policy drop;", "set block4 { type ipv4_addr; flags interval; auto-merge; elements = { 198.51.100.7 }; }",
		"set block6 { type ipv6_addr; flags interval; auto-merge; elements = { 2001:db8::/48 }; }", "192.0.2.9 timeout 36",
		"elements = { 192.0.2.10 }", "icmp type echo-request drop", "meta l4proto { icmp, ipv6-icmp } accept",
		"tcp dport { 22, 80, 443, 8888 } accept", "tcp dport { 3306, 6379 } drop", "udp dport { 3306, 6379 } drop",
		"ip saddr 203.0.113.0/24 tcp dport { 9000-9100 } accept", "iifname \"docker0\" accept", "chain forward",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("ruleset missing %q:\n%s", want, rs)
		}
	}
	if strings.Contains(rs, "dport { 1 }") {
		t.Error("disabled rule rendered")
	}
	// Protected ports come before user rules, so a drop rule cannot lock SSH out.
	if strings.Index(rs, "protected") > strings.Index(rs, "rule 1") {
		t.Error("protected ports after user rules")
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
