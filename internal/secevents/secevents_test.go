package secevents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func line(t time.Time, ip, path, block string) string {
	return fmt.Sprintf(`%s - - [%s] "GET %s HTTP/1.1" 403 10 "-" "curl/8" "a.com" "%s" "-"`+"\n", ip, t.Format("02/Jan/2006:15:04:05 -0700"), path, block)
}

func TestCollectQueryAutoBan(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	sv := New(st, dir, filepath.Join(dir, "waf", "audit.log"))
	sv.now = func() time.Time { return now }
	sv.Sites = func(context.Context) []string { return []string{"a.com"} }
	var bans []string
	sv.Ban = func(_ context.Context, ip string, ttl time.Duration, remark string) error {
		bans = append(bans, fmt.Sprintf("%s %s %s", ip, ttl, remark))
		return nil
	}
	sv.Exempt = func(_ context.Context, ip string) bool { return ip == "198.51.100.1" }

	logf := filepath.Join(dir, "a.com.log")
	var b strings.Builder
	b.WriteString(line(now.AddDate(0, 0, -2), "192.0.2.1", "/old", "403:rule-wp-1002"))
	b.WriteString(`192.0.2.9 - - [05/Nov/2026:11:00:00 +0000] "GET / HTTP/1.1" 200 10 "-" "curl/8" "a.com" "-" "-"` + "\n") // not refused
	for i := range 12 {
		b.WriteString(line(now.Add(-time.Duration(i)*10*time.Second), "203.0.113.7", "/wp-login.php", "403:idc"))
		b.WriteString(line(now.Add(-time.Duration(i)*10*time.Second), "198.51.100.1", "/x", "403:list")) // exempt
	}
	b.WriteString(line(now, "203.0.113.8", "/y", "403:ip")) // the IP lists do not count
	os.WriteFile(logf, []byte(b.String()), 0o644)
	os.MkdirAll(filepath.Join(dir, "waf"), 0o755)
	os.WriteFile(filepath.Join(dir, "waf", "audit.log"), []byte(`{"transaction":{"client_ip":"203.0.113.9","time_stamp":"Thu Nov  5 11:59:00 2026","request":{"method":"POST","uri":"/x","headers":{"Host":"a.com"}},"response":{"http_code":403},"messages":[{"message":"RCE","details":{"ruleId":"932380","severity":"2"}}]}}`+"\n"), 0o644)

	sv.Collect(ctx, nil)
	if len(bans) != 0 {
		t.Fatalf("banned while auto-ban is off: %v", bans)
	}
	if _, err := sv.SaveAutoBan(ctx, AutoBan{Enabled: true, Window: 5, Threshold: 10, BanMinutes: 60, Sources: []string{"protection", "list", "waf"}}); err != nil {
		t.Fatal(err)
	}
	// One more refusal: the batch triggers the check.
	f, _ := os.OpenFile(logf, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(line(now, "203.0.113.7", "/xmlrpc.php", "403:idc"))
	f.Close()
	sv.Collect(ctx, nil)
	if len(bans) != 1 || !strings.HasPrefix(bans[0], "203.0.113.7 1h0m0s 13 / 5m · protection idc") {
		t.Fatalf("bans %v", bans)
	}

	day := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	p, err := sv.Query(ctx, Filter{From: day.Unix(), To: day.AddDate(0, 0, 1).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 27 || p.Sources["waf"] != 1 || p.Sources["protection"] != 13 || p.TopIPs[0].IP != "203.0.113.7" {
		t.Fatalf("today: total %d sources %v top %v", p.Total, p.Sources, p.TopIPs)
	}
	if p.Items[0].Time < p.Items[len(p.Items)-1].Time {
		t.Error("not newest first")
	}
	old, _ := sv.Query(ctx, Filter{From: day.AddDate(0, 0, -2).Unix(), To: day.AddDate(0, 0, -1).Unix()})
	if old.Total != 1 || old.Items[0].Rule != "wp-1002" || old.Items[0].Source != "rule" {
		t.Fatalf("two days ago: %+v", old)
	}
	w, _ := sv.Query(ctx, Filter{From: day.Unix(), Source: "waf"})
	if w.Total != 1 || w.Items[0].Rule != "932380" || w.Items[0].Site != "" || len(w.Items[0].Messages) != 1 {
		t.Fatalf("waf %+v", w.Items)
	}
	q, _ := sv.Query(ctx, Filter{From: day.Unix(), Q: "xmlrpc"})
	if q.Total != 1 {
		t.Fatalf("search %d", q.Total)
	}
	// Nothing is read twice.
	sv.Collect(ctx, nil)
	if p2, _ := sv.Query(ctx, Filter{From: 0}); p2.Total != 28 {
		t.Fatalf("after a second read: %d", p2.Total)
	}
	if _, err := sv.SaveAutoBan(ctx, AutoBan{Window: 0, Threshold: 10, BanMinutes: 60}); err == nil {
		t.Error("window 0 accepted")
	}
	if _, err := sv.SaveAutoBan(ctx, AutoBan{Window: 5, Threshold: 10, BanMinutes: 60, Sources: []string{"ip"}}); err == nil {
		t.Error("the IP lists as a source accepted")
	}
}
