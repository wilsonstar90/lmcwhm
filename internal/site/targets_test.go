package site

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

func newTestSvc(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Service{st: st, Env: webenv.New(t.TempDir())}
}

func TestDomainTargets(t *testing.T) {
	sv := newTestSvc(t)
	ctx := context.Background()
	web := Site{Domain: "a.com", Type: "php", PHPVersion: "8.3", Root: "/www/wwwroot/a.com", SSLMode: "none", Target: TargetSite}
	app := Site{Domain: "app.a.com", Type: "node", AppPort: 3001, Root: "/www/wwwroot/app.a.com", SSLMode: "none", Target: TargetSite}
	entry := Site{Domain: "b.com", Aliases: []string{"www.b.com"}, Type: "proxy", SSLMode: "none", Target: TargetURL, ProxyTarget: "http://10.0.0.2:8080"}
	for _, s := range []*Site{&web, &app, &entry} {
		if err := sv.save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	// A pure entry cannot serve "its own website".
	if err := sv.applyTarget(ctx, &entry, TargetInput{Target: TargetSite}); err != ErrNoContent {
		t.Errorf("site target on a pure entry: %v", err)
	}
	for _, bad := range []TargetInput{
		{Target: TargetLink, TargetSite: entry.ID}, {Target: TargetLink, TargetSite: 999}, {Target: TargetURL, ProxyTarget: "ftp://x"},
		{Target: TargetDocker, ProxyTarget: "http://127.0.0.1:8080", Container: "bad name"}, {Target: TargetPassthrough, ProxyTarget: "http://x"},
		{Target: TargetRedirect, Redirect: "https://x.com/a;rm"}, {Target: TargetRedirect, Redirect: "https://x.com/?a=1", RedirectPath: true},
		{Target: TargetRedirect, Redirect: "javascript:alert(1)"}, {Target: "nope"},
	} {
		e := entry
		if err := sv.applyTarget(ctx, &e, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// Link b.com to the PHP site: it serves a.com's files under its own name.
	if err := sv.applyTarget(ctx, &entry, TargetInput{Target: TargetLink, TargetSite: web.ID}); err != nil {
		t.Fatal(err)
	}
	sv.save(ctx, &entry)
	specs, _ := sv.specs(ctx)
	by := map[string]webenv.Site{}
	for _, s := range specs {
		by[s.Domain] = s
	}
	if b := by["b.com"]; b.Type != "php" || b.Root != web.Root || b.PHPVersion != "8.3" || b.Upstream != "" || strings.Join(b.Aliases, ",") != "www.b.com" {
		t.Fatalf("linked spec %+v", b)
	}
	// A linked website cannot be deleted.
	if err := sv.linkedErr(ctx, web.ID); err == nil || !strings.Contains(err.Error(), "b.com") {
		t.Errorf("linked err %v", err)
	}
	// Retarget the node project's domain to a container, then back: its files and port stay.
	if err := sv.applyTarget(ctx, &app, TargetInput{Target: TargetDocker, Container: "grafana", ProxyTarget: "http://127.0.0.1:3000/"}); err != nil {
		t.Fatal(err)
	}
	if sp := sv.spec(app); sp.Upstream != "http://127.0.0.1:3000" || sp.Type != "proxy" || app.Container != "grafana" {
		t.Fatalf("docker spec %+v", sp)
	}
	sv.applyTarget(ctx, &app, TargetInput{Target: TargetSite})
	if sp := sv.spec(app); sp.Upstream != "http://127.0.0.1:3001" || app.Container != "" {
		t.Fatalf("back to the project %+v", sp)
	}
	// Redirect keeps the path and drops a trailing slash.
	sv.applyTarget(ctx, &entry, TargetInput{Target: TargetRedirect, Redirect: "https://new.example.com/", RedirectPath: true, RedirectCode: 308})
	if sp := sv.spec(entry); sp.Redirect != "https://new.example.com" || sp.RedirectCode != 308 || !sp.RedirectPath || sp.Root == "" {
		t.Fatalf("redirect spec %+v", sp)
	}
	sv.applyTarget(ctx, &entry, TargetInput{Target: TargetPassthrough, ProxyTarget: "127.0.0.1:8443"})
	if sp := sv.spec(entry); !sp.Passthrough || !entry.Passthrough {
		t.Fatalf("passthrough %+v", sp)
	}
}
