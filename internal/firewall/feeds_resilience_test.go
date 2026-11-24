package firewall

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestFeedPartialFailureDoesNotFailCron(t *testing.T) {
	sv, _ := newSvc(t)
	ctx := context.Background()
	lists := map[string]string{
		"https://www.spamhaus.org/drop/drop_v4.json": `{"cidr":"1.10.16.0/20"}`,
		"https://lists.blocklist.de/lists/all.txt":   "5.6.7.8\n5.6.7.9\n",
	}
	fetch = func(_ context.Context, u string, guard []netip.Prefix) ([]string, int, error) {
		return ParseFeed(strings.NewReader(lists[u]), guard)
	}
	defer func() { fetch = fetchFeed }()
	if err := sv.SaveFeeds(ctx, []string{"spamhaus_drop", "blocklist_de"}, nil); err != nil {
		t.Fatal(err)
	}
	lists["https://lists.blocklist.de/lists/all.txt"] = ""
	var out strings.Builder
	if err := sv.RunFeeds(ctx, KindBlock, 12*time.Hour, &out); err != nil {
		t.Fatalf("one failed list failed the cron: %v", err)
	}
	if !strings.Contains(out.String(), "warning: 1 of 2 lists failed") {
		t.Fatalf("partial failure warning missing: %s", out.String())
	}
	lists["https://www.spamhaus.org/drop/drop_v4.json"] = ""
	if err := sv.RunFeeds(ctx, KindBlock, 12*time.Hour, &out); err == nil || !strings.Contains(err.Error(), "all 2 lists failed") {
		t.Fatalf("total feed failure was accepted: %v", err)
	}
}

func TestEmptyFeedResponseIncludesContentType(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>temporarily unavailable</html>"))
	}))
	defer ts.Close()
	if _, _, err := fetchFeed(context.Background(), ts.URL, nil); err == nil || !strings.Contains(err.Error(), "Content-Type text/html") {
		t.Fatalf("empty response diagnostic = %v", err)
	}
}
