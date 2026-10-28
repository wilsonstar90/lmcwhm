package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestHTTPAndTCPAndTransitions(t *testing.T) {
	up := true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("hello world"))
	}))
	defer ts.Close()
	st, _ := store.Open(filepath.Join(t.TempDir(), "p.db"))
	defer st.Close()
	sv := New(st)
	var events []bool
	sv.Notify = func(p Probe, down bool, _ string) { events = append(events, down) }
	ctx := context.Background()

	if _, err := sv.Save(ctx, Probe{Name: "x", Type: "ping", Target: "-f evil"}); err == nil {
		t.Fatal("option injection accepted")
	}
	p, err := sv.Save(ctx, Probe{Name: "web", Type: "http", Target: ts.URL, Keyword: "world", Fails: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := sv.RunOnce(ctx, p); !r.OK {
		t.Fatalf("up check failed: %+v", r)
	}
	up = false
	sv.RunOnce(ctx, p)
	if g, _ := sv.Get(ctx, p.ID); g.State != "up" {
		t.Fatalf("one failure should not flip to down: %s", g.State)
	}
	sv.RunOnce(ctx, p)
	g, _ := sv.Get(ctx, p.ID)
	if g.State != "down" || g.LastError == "" {
		t.Fatalf("state %s err %q", g.State, g.LastError)
	}
	up = true
	sv.RunOnce(ctx, p)
	if len(events) != 2 || !events[0] || events[1] {
		t.Fatalf("transitions: %v", events)
	}
	if g, _ := sv.Get(ctx, p.ID); g.Uptime24h != 50 {
		t.Fatalf("uptime %v", g.Uptime24h)
	}
	p.Keyword = "missing"
	if r, _ := Check(ctx, p); r.OK {
		t.Fatal("keyword check passed")
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	if r, _ := Check(ctx, Probe{Type: "tcp", Target: ln.Addr().String(), Timeout: 2}); !r.OK {
		t.Fatalf("tcp: %+v", r)
	}
	ln.Close()
	if r, _ := Check(ctx, Probe{Type: "tcp", Target: ln.Addr().String(), Timeout: 2}); r.OK {
		t.Fatal("closed port reported up")
	}
}
