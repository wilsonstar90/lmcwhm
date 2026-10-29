package firewall

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

func TestPatterns(t *testing.T) {
	sv, _ := newSvc(t)
	ctx := context.Background()
	for _, bad := range []Pattern{
		{Kind: "ua", List: "block", Value: ""}, {Kind: "ua", List: "block", Value: "#comment"}, {Kind: "x", List: "block", Value: "a"},
		{Kind: "ua", List: "block", Match: "regex", Value: "(unclosed"}, {Kind: "url", List: "block", Match: "prefix", Value: "wp-login.php"},
		{Kind: "ua", List: "block", Value: "line\nbreak"},
	} {
		if _, err := sv.SavePattern(ctx, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	p, err := sv.SavePattern(ctx, Pattern{Kind: "ua", List: "block", Value: " python-requests ", Enabled: true})
	if err != nil || p.Match != "contains" || p.Value != "python-requests" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := sv.SavePattern(ctx, Pattern{Kind: "ua", List: "block", Value: "python-requests", Enabled: true}); err != ErrDuplicate {
		t.Errorf("duplicate: %v", err)
	}
	sv.SavePattern(ctx, Pattern{Kind: "url", List: "allow", Match: "prefix", Value: "/api/webhook", Enabled: true})
	sv.SavePattern(ctx, Pattern{Kind: "url", List: "block", Match: "regex", Value: `\.(env|git)`, Enabled: false})
	added, skipped, err := sv.ImportPatterns(ctx, "ua", "block", "contains", []string{"sqlmap", "python-requests", "", "nikto"}, "import")
	if err != nil || added != 2 || skipped != 2 {
		t.Fatalf("import %d %d %v", added, skipped, err)
	}
	got := map[string][]string{}
	for _, l := range sv.EdgePatterns(ctx) {
		got[l.Kind+"_"+l.List+"_"+l.Match] = l.Values
	}
	if len(got) != 2 || len(got["ua_block_contains"]) != 3 || got["ua_block_contains"][0] != "python-requests" || got["url_allow_prefix"][0] != "/api/webhook" {
		t.Fatalf("edge %v", got) // the disabled regex is left out
	}
	if n, _ := sv.ClearPatterns(ctx, "ua", "block"); n != 3 {
		t.Errorf("cleared %d", n)
	}
}

func TestImportAndAutoIPs(t *testing.T) {
	sv, _ := newSvc(t)
	ctx := context.Background()
	self := netip.MustParseAddr("203.0.113.5")
	added, skipped, err := sv.ImportIPs(ctx, "block", []string{"198.51.100.1", "bad", "203.0.113.0/24", "2001:db8::/48"}, 0, "imp", self)
	if err != nil || added != 2 || skipped != 2 {
		t.Fatalf("import %d %d %v", added, skipped, err)
	}
	sv.AddIP(ctx, "192.0.2.9", "block", time.Hour, "crowdsec", "ssh-bf")
	sv.SyncBans(ctx, "feed:test", []Ban{{IP: "192.0.2.10", TTL: time.Hour}}, nil)
	p, err := sv.AutoIPs(ctx, AutoFilter{})
	if err != nil || p.Total < 1 || p.Sources["crowdsec"] != 1 {
		t.Fatalf("auto %+v %v", p, err)
	}
	for _, x := range p.Items {
		if x.Source == "manual" {
			t.Errorf("manual entry in the automatic list: %+v", x)
		}
	}
	cs, _ := sv.AutoIPs(ctx, AutoFilter{Source: "crowdsec", Query: "192.0.2"})
	if cs.Total != 1 || cs.Items[0].IP != "192.0.2.9" {
		t.Fatalf("filtered %+v", cs)
	}
	if err := sv.KeepIP(ctx, cs.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	if after, _ := sv.AutoIPs(ctx, AutoFilter{Source: "crowdsec"}); after.Total != 0 {
		t.Errorf("kept entry still automatic")
	}
	if n, _ := sv.ClearIPs(ctx, "block"); n != 3 {
		t.Errorf("cleared %d (two imported + one kept)", n)
	}
}
