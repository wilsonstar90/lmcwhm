package firewall

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPrecedence(t *testing.T) {
	sv, _ := newSvc(t)
	ctx := context.Background()
	must := func(_ IP, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(sv.AddIP(ctx, "198.51.100.0/24", "allow", 0, "manual", ""))
	must(sv.AddIP(ctx, "198.51.100.9", "block", 0, "manual", "")) // narrower than the allow: wins
	must(sv.AddIP(ctx, "192.0.2.0/24", "block", 0, "manual", "")) // manual block
	must(sv.AddIP(ctx, "192.0.2.77", "allow", 0, "manual", ""))   // narrower allow inside it: wins
	must(sv.AddIP(ctx, "203.0.113.5", "block", time.Hour, "manual", ""))
	must(sv.AddIP(ctx, "100.64.0.1", "block", time.Hour, "crowdsec", ""))
	sv.st.DB.Exec(`INSERT INTO fw_ips(ip, list, source, remark, expires_at, created_at) VALUES('103.21.244.0/22', 'trust', 'feed:cloudflare4', '', 0, 0)`)

	// Automatic blocks of allowed addresses (or the server's own) are skipped.
	if _, err := sv.AddIP(ctx, "198.51.100.20", "block", time.Hour, "mcp", ""); !errors.Is(err, ErrAllowed) {
		t.Errorf("mcp ban of an allowed address: %v", err)
	}
	if _, err := sv.AddIP(ctx, "203.0.113.250", "block", time.Hour, "mcp", ""); !errors.Is(err, ErrAllowed) {
		t.Errorf("mcp ban of the server: %v", err)
	}
	sv.SyncBans(ctx, "crowdsec", []Ban{{IP: "198.51.100.30", TTL: time.Hour}, {IP: "100.64.0.2", TTL: time.Hour}}, nil)
	if ips, _ := sv.IPs(ctx, "block"); strings.Contains(ipList(ips), "198.51.100.30") || !strings.Contains(ipList(ips), "100.64.0.2") {
		t.Errorf("crowdsec bans: %s", ipList(ips))
	}
	// Manual conflicts.
	if _, err := sv.AddIP(ctx, "198.51.100.0/24", "block", 0, "manual", ""); !errors.Is(err, ErrInAllow) {
		t.Errorf("same entry on both lists: %v", err)
	}
	if _, err := sv.AddIP(ctx, "192.0.2.0/24", "allow", 0, "manual", ""); !errors.Is(err, ErrInBlock) {
		t.Errorf("same entry on both lists: %v", err)
	}
	if _, err := sv.AddIP(ctx, "203.0.113.250", "block", 0, "manual", ""); !errors.Is(err, ErrSelfServer) {
		t.Errorf("manual block of the server: %v", err)
	}

	rs, err := sv.Ruleset(ctx, Config{Enabled: true, Policy: "accept", Ping: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"set over4 { type ipv4_addr; flags interval; auto-merge; elements = { 198.51.100.9 }; }",
		"set mblock4 { type ipv4_addr; flags interval; auto-merge; elements = { 192.0.2.0/24 }; }",
		"set mban4 { type ipv4_addr; flags timeout; elements = { 203.0.113.5 timeout",
		"100.64.0.1 timeout",
		"ip saddr @mblock4 counter drop", // no trusted-range exemption
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("ruleset missing %q:\n%s", want, rs)
		}
	}
	// Order: the narrower manual block, then allow, then manual blocks, then automatic.
	iOver, iAllow := strings.Index(rs, "@over4 counter drop"), strings.Index(rs, "@allow4 accept")
	iManual, iAuto := strings.Index(rs, "@mblock4 counter drop"), strings.Index(rs, "@ban4 ip saddr != @trust4 ip saddr != @self4 counter drop")
	if !(iOver < iAllow && iAllow < iManual && iManual < iAuto) || strings.Contains(rs, "@self4 accept") {
		t.Errorf("order over %d allow %d manual %d auto %d (the server's addresses are not accepted outright)", iOver, iAllow, iManual, iAuto)
	}

	c, _ := sv.Config(ctx)
	c.Enabled = true
	sv.saveConfig(ctx, c)
	e, err := sv.EdgeLists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.Over, ",") != "198.51.100.9" || !strings.Contains(strings.Join(e.Manual, ","), "192.0.2.0/24") ||
		strings.Join(e.Self, ",") != "203.0.113.250/32" || strings.Contains(strings.Join(e.Allow, ","), "127.0.0.0/8") || strings.Contains(strings.Join(e.Block, ","), "198.51.100.30") {
		t.Errorf("edge lists %+v", e)
	}
	if !sv.Allowed(ctx, "198.51.100.200") || sv.Allowed(ctx, "100.64.0.1") {
		t.Error("Allowed")
	}
}

func ipList(xs []IP) string {
	var s []string
	for _, x := range xs {
		s = append(s, x.IP)
	}
	return strings.Join(s, ",")
}
