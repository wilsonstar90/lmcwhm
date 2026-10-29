package firewall

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestForwards(t *testing.T) {
	sv, log := newSvc(t)
	ctx := context.Background()
	used := map[int]string{8081: "grafana"}
	for _, bad := range []Forward{
		{Proto: "tcp", Listen: 80, TargetPort: 8080}, {Proto: "tcp", Listen: 8888, TargetPort: 1}, // edge / panel port
		{Proto: "sctp", Listen: 2000, TargetPort: 22}, {Proto: "tcp", Listen: 2000, TargetPort: 2000},
		{Proto: "tcp", Listen: 2000, TargetHost: "2001:db8::1", TargetPort: 22}, {Proto: "tcp", Listen: 2000, TargetHost: "example.com", TargetPort: 22},
		{Proto: "tcp", Listen: 8081, TargetPort: 22}, // published by a container
	} {
		if _, err := sv.SaveForward(ctx, bad, used); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	a, err := sv.SaveForward(ctx, Forward{Proto: "both", Listen: 5353, TargetPort: 53, Enabled: true, Remark: "dns"}, used)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sv.SaveForward(ctx, Forward{Proto: "udp", Listen: 5353, TargetPort: 54, Enabled: true}, used); err == nil {
		t.Error("same port and protocol twice")
	}
	b, err := sv.SaveForward(ctx, Forward{Proto: "tcp", Listen: 2222, TargetHost: "10.0.0.5", TargetPort: 22, Source: "203.0.113.0/24", Enabled: true}, used)
	if err != nil {
		t.Fatal(err)
	}
	sv.SaveForward(ctx, Forward{Proto: "udp", Listen: 51820, TargetHost: "127.0.0.1", TargetPort: 51821, Enabled: false}, used)
	fs, _ := sv.Forwards(ctx)
	rs := ForwardRuleset(fs)
	for _, want := range []string{
		"table inet lmcwhm_fwd {", "type nat hook prerouting priority dstnat;",
		"meta l4proto { tcp, udp } th dport 5353 counter redirect to :53 comment \"forward " + itoa(a.ID) + "\"",
		"meta nfproto ipv4 ip saddr 203.0.113.0/24 tcp dport 2222 counter dnat ip to 10.0.0.5:22 comment \"forward " + itoa(b.ID) + "\"",
		"ct status dnat ip daddr 10.0.0.5 tcp dport 22 masquerade",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("forward ruleset missing %q:\n%s", want, rs)
		}
	}
	if strings.Contains(rs, "51820") {
		t.Error("disabled forward rendered")
	}
	if body, _ := os.ReadFile(log); !strings.Contains(string(body), "table inet lmcwhm_fwd {") {
		t.Error("forward table not installed")
	}
	// The firewall lets forwarded connections through.
	if main, _ := sv.Ruleset(ctx, Config{Enabled: true, Policy: "drop"}); strings.Count(main, "ct status dnat accept") != 2 {
		t.Error("input / forward chains do not accept forwarded connections")
	}
	sv.DeleteForward(ctx, a.ID)
	sv.DeleteForward(ctx, b.ID)
	fs, _ = sv.Forwards(ctx)
	if ForwardRuleset(fs) != "" {
		t.Error("table kept without enabled forwards")
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
