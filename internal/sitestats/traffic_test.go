package sitestats

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestTrafficAndLive(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	sv := New(st, dir, time.UTC)
	now := time.Date(2026, 10, 28, 12, 30, 0, 0, time.UTC)
	sv.now = func() time.Time { return now }
	ctx := context.Background()
	chrome := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
	iphone := "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
	body := line(now.Add(-25*time.Hour), "9.9.9.9", "/", 200, chrome, "-") + // yesterday, before the same time
		line(now.Add(-3*time.Hour), "1.1.1.1", "/", 200, chrome, "-") +
		line(now.Add(-3*time.Second), "1.1.1.1", "/", 200, iphone, "-") +
		line(now.Add(-2*time.Second), "5.5.5.5", "/wp-login.php", 403, "Go-http-client/1.1", "-") +
		line(now.Add(-1*time.Second), "5.5.5.5", "/.env", 444, "curl/8.0", "-")
	os.WriteFile(filepath.Join(dir, "a.com.log"), []byte(body), 0o644)
	sv.Sites = func(context.Context) []string { return []string{"a.com"} }
	sv.IngestAll(ctx, nil)

	tr := sv.Traffic(ctx, 0, func(ip string) string { return map[string]string{"5.5.5.5": "RU"}[ip] })
	if tr.Requests != 4 || tr.Blocked != 2 || tr.PrevReq != 1 || tr.IPs != 2 || len(tr.Hours) != 13 {
		t.Fatalf("totals %+v", tr)
	}
	if tr.Hours[12].PV != 3 || tr.Hours[12].Blocked != 2 {
		t.Fatalf("hour %+v", tr.Hours[12])
	}
	got := map[string]int64{}
	for _, c := range append(tr.Browsers, tr.OS...) {
		got[c.Key] = c.Hits
	}
	if got["Chrome"] != 1 || got["Safari"] != 1 || got["Go-http-client"] != 1 || got["curl"] != 1 || got["Windows"] != 1 || got["iOS"] != 1 {
		t.Fatalf("clients %v", got)
	}
	if len(tr.BlockedIPs) != 1 || tr.BlockedIPs[0].Key != "5.5.5.5" || tr.BlockedIPs[0].Hits != 2 ||
		len(tr.BlockedBy) != 1 || tr.BlockedBy[0].Key != "RU" {
		t.Fatalf("blocked %+v %+v", tr.BlockedIPs, tr.BlockedBy)
	}
	if y := sv.Traffic(ctx, 1, nil); y.Requests != 1 || len(y.Hours) != 24 {
		t.Fatalf("yesterday %+v", y)
	}

	l := sv.Live(ctx, []string{"a.com"})
	var reqs float64
	for i, q := range l.QPS {
		secs := 5.0
		if i == len(l.QPS)-1 {
			secs = float64(l.At-l.Steps[i]) + 1
		}
		reqs += q * secs
	}
	if reqs != 3 || len(l.Steps) != 12 {
		t.Fatalf("live %+v (%v requests)", l, reqs)
	}
}
