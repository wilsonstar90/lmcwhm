package firewall

import (
	"context"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseFeed(t *testing.T) {
	in := `# FireHOL style
0.0.0.0/8
10.0.0.0/8
1.10.16.0/20
5.6.7.8	4
5.6.7.8 ; duplicate
{"cidr":"2.56.192.0/22","sblid":"SBL459831","rir":"ripencc"}
{"type":"metadata","timestamp":1730000000,"size":1500}
2001:db8::/48
2a0e:1c80::/29
203.0.113.9
198.51.100.0/24
garbage line
9.9.9.9/32  # a guarded address
`
	guard := append(append([]netip.Prefix{}, reserved...), netip.MustParsePrefix("9.9.9.9/32"))
	got, skipped, err := ParseFeed(strings.NewReader(in), guard)
	if err != nil {
		t.Fatal(err)
	}
	want := "1.10.16.0/20 5.6.7.8 2.56.192.0/22 2a0e:1c80::/29"
	if strings.Join(got, " ") != want {
		t.Fatalf("entries %q, want %q", strings.Join(got, " "), want)
	}
	if skipped != 6 {
		t.Errorf("skipped %d, want 6", skipped)
	}
}

func TestRefreshFeeds(t *testing.T) {
	ConfirmWindow = 200 * time.Millisecond
	sv, log := newSvc(t)
	ctx := context.Background()
	if _, err := sv.AddIP(ctx, "45.1.2.3", "allow", 0, "manual", ""); err != nil {
		t.Fatal(err)
	}
	// A confirmed drop policy must survive background syncs.
	sv.Update(ctx, func(c *Config) { c.Enabled, c.Policy = true, "drop" })
	if err := sv.Confirm(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	lists := map[string]string{
		"https://www.spamhaus.org/drop/drop_v4.json": `{"cidr":"1.10.16.0/20"}` + "\n" + `{"cidr":"45.1.0.0/16"}`,
		"https://lists.blocklist.de/lists/all.txt":   "5.6.7.8\n5.6.7.9\n",
	}
	fetch = func(ctx context.Context, u string, guard []netip.Prefix) ([]string, int, error) {
		return ParseFeed(strings.NewReader(lists[u]), guard)
	}
	defer func() { fetch = fetchFeed }()

	if err := sv.SaveFeeds(ctx, []string{"spamhaus_drop", "blocklist_de", "nope"}, nil, 6); err != ErrFeedUnknown {
		t.Fatalf("unknown feed: %v", err)
	}
	if err := sv.SaveFeeds(ctx, []string{"spamhaus_drop", "blocklist_de"}, []Feed{{URL: "ftp://x"}}, 6); err != ErrFeedURL {
		t.Fatalf("bad url: %v", err)
	}
	if err := sv.SaveFeeds(ctx, []string{"spamhaus_drop", "blocklist_de"}, nil, 6); err != nil {
		t.Fatal(err)
	}
	if err := sv.RefreshFeeds(ctx); err != nil {
		t.Fatal(err)
	}
	f := sv.Feeds(ctx)
	// 45.1.0.0/16 covers an allowed address: skipped.
	if f.Blocking != 3 || f.Status["spamhaus_drop"].Entries != 1 || f.Status["spamhaus_drop"].Skipped != 1 || f.Status["blocklist_de"].Entries != 2 {
		t.Fatalf("feeds %+v", f)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "1.10.16.0/20") || !strings.Contains(string(b), "5.6.7.9 timeout") {
		t.Fatalf("ruleset lacks feed entries:\n%s", b)
	}
	// The list shrinks: the dropped address goes; a failing feed keeps its entries.
	lists["https://lists.blocklist.de/lists/all.txt"] = "5.6.7.8\n"
	lists["https://www.spamhaus.org/drop/drop_v4.json"] = ""
	sv.RefreshFeeds(ctx)
	f = sv.Feeds(ctx)
	if f.Blocking != 2 || f.Status["spamhaus_drop"].Error == "" || f.Status["spamhaus_drop"].Entries != 1 {
		t.Fatalf("after refresh %+v", f)
	}
	// Unsubscribing lifts the feed's blocks.
	if err := sv.SaveFeeds(ctx, []string{"blocklist_de"}, nil, 6); err != nil {
		t.Fatal(err)
	}
	if f = sv.Feeds(ctx); f.Blocking != 1 {
		t.Fatalf("after unsubscribe %+v", f)
	}
	// A custom list is enabled without being named.
	if err := sv.SaveFeeds(ctx, nil, []Feed{{URL: "https://example.com/l.txt"}}, 6); err != nil {
		t.Fatal(err)
	}
	if f = sv.Feeds(ctx); len(f.Enabled) != 1 || f.Enabled[0] != "custom1" || f.Custom[0].Name != "example.com" || f.Blocking != 0 {
		t.Fatalf("custom feed %+v", f)
	}
	time.Sleep(400 * time.Millisecond)
	if c, _ := sv.Config(ctx); c.Policy != "drop" || sv.Pending() {
		t.Fatalf("ban sync armed or reverted the drop policy: %+v pending=%v", c, sv.Pending())
	}
}

func TestTrustFeeds(t *testing.T) {
	sv, log := newSvc(t)
	ctx := context.Background()
	sv.Update(ctx, func(c *Config) { c.Enabled, c.Policy = true, "accept" })
	lists := map[string]string{
		"https://www.cloudflare.com/ips-v4": "173.245.48.0/20\n103.21.244.0/22\n10.0.0.0/8\n",
		"https://bl.example/list":           "173.245.0.0/16\n5.6.7.8\n",
	}
	fetch = func(ctx context.Context, u string, guard []netip.Prefix) ([]string, int, error) {
		return ParseFeed(strings.NewReader(lists[u]), guard)
	}
	defer func() { fetch = fetchFeed }()
	if err := sv.SaveFeeds(ctx, []string{"cloudflare4"}, []Feed{{Name: "bl", URL: "https://bl.example/list"}}, 6); err != nil {
		t.Fatal(err)
	}
	if err := sv.RefreshFeeds(ctx); err != nil {
		t.Fatal(err)
	}
	f := sv.Feeds(ctx)
	// The block CIDR overlapping Cloudflare is kept whole; the ruleset exempts the trusted part.
	if f.Trusted != 2 || f.Blocking != 2 || f.Status["cloudflare4"].Skipped != 1 || f.Custom[0].Kind != KindBlock {
		t.Fatalf("feeds %+v", f)
	}
	if ips, _ := sv.IPs(ctx, "allow"); len(ips) != 0 {
		t.Fatalf("trusted ranges in the allow list: %+v", ips)
	}
	b, _ := os.ReadFile(log)
	rs := string(b)
	if !strings.Contains(rs, "set trust4 { type ipv4_addr; flags interval; auto-merge; elements = { 103.21.244.0/22, 173.245.48.0/20 }") {
		t.Fatalf("trust set missing:\n%s", rs)
	}
	if !strings.Contains(rs, "ip saddr @block4 ip saddr != @trust4 counter drop") || !strings.Contains(rs, "ip6 saddr @ban6 ip6 saddr != @trust6 counter drop") {
		t.Fatalf("drops do not exempt trusted ranges:\n%s", rs)
	}
	// Trusted ranges open nothing: no accept on them.
	if strings.Contains(rs, "@trust4 accept") {
		t.Fatal("trusted ranges accepted")
	}
}
