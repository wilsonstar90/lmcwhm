package sitestats

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func line(t time.Time, ip, path string, status int, ua, ref string) string {
	return fmt.Sprintf("%s - - [%s] \"GET %s HTTP/1.1\" %d 100 %q %q \"a.com\"\n", ip, t.Format("02/Jan/2006:15:04:05 -0700"), path, status, ref, ua)
}

func TestIngestAndReport(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	loc := time.UTC
	sv := New(st, dir, loc)
	now := time.Date(2026, 10, 28, 12, 30, 0, 0, loc)
	sv.now = func() time.Time { return now }
	ctx := context.Background()
	log := filepath.Join(dir, "a.com.log")
	body := line(now.Add(-time.Hour), "1.1.1.1", "/?x=1", 200, "Mozilla", "https://google.com/search") +
		line(now, "1.1.1.1", "/about", 200, "Mozilla", "-") +
		line(now, "2.2.2.2", "/missing", 404, "Mozilla", "https://a.com/") +
		line(now, "66.249.66.1", "/", 200, "Mozilla/5.0 (compatible; Googlebot/2.1)", "-")
	os.WriteFile(log, []byte(body+"partial line without newline"), 0o644)
	if err := sv.Ingest(ctx, "a.com"); err != nil {
		t.Fatal(err)
	}
	// Re-ingesting reads nothing new; appending completes the partial line.
	sv.Ingest(ctx, "a.com")
	f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("\n" + line(now, "3.3.3.3", "/", 500, "curl", "-"))
	f.Close()
	if err := sv.Ingest(ctx, "a.com"); err != nil {
		t.Fatal(err)
	}
	rep, err := sv.Report(ctx, "a.com", 1, func(ip string) string { return map[string]string{"1.1.1.1": "AU"}[ip] })
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total.PV != 5 || rep.Total.Spider != 1 || rep.Total.UV != 3 || rep.Total.S4xx != 1 || rep.Total.S5xx != 1 {
		t.Fatalf("totals: %+v", rep.Total)
	}
	if len(rep.Series) != 2 {
		t.Fatalf("hourly series: %+v", rep.Series)
	}
	if rep.Top["url"][0].Key != "/" || rep.Top["ref"][0].Key != "google.com" || len(rep.Top["ref"]) != 1 || rep.Top["spider"][0].Key != "Google" {
		t.Fatalf("tops: %+v", rep.Top)
	}
	if rep.Countries[0].Key != "AU" || rep.Countries[0].Hits != 2 {
		t.Fatalf("countries: %+v", rep.Countries)
	}
	// Rotation: a new file starts from the beginning.
	os.Rename(log, log+".1")
	os.WriteFile(log, []byte(line(now, "4.4.4.4", "/new", 200, "Mozilla", "-")), 0o644)
	sv.Ingest(ctx, "a.com")
	ov, _ := sv.Overview(ctx)
	if ov["a.com"].PV != 6 || ov["a.com"].UV != 4 {
		t.Fatalf("overview after rotation: %+v", ov["a.com"])
	}
}
