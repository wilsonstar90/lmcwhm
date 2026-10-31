package site

import (
	"context"
	"testing"
)

func TestSiteSettings(t *testing.T) {
	sv := newTestSvc(t)
	ctx := context.Background()
	web := Site{Domain: "a.com", Type: "php", PHPVersion: "8.3", Root: "/www/wwwroot/a.com", SSLMode: "none", Target: TargetSite}
	entry := Site{Domain: "b.com", Type: "proxy", SSLMode: "none", Target: TargetURL, ProxyTarget: "http://10.0.0.2:8080"}
	for _, s := range []*Site{&web, &entry} {
		if err := sv.save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	str := func(s string) *string { return &s }
	in := UpdateInput{RunDir: str("public/"), IndexFiles: &[]string{"home.html", " ", "index.php"}, Rewrite: str("location / {\r\n  try_files $uri /index.php?$args;\r\n}\n"),
		CustomConf: str("add_header X-Test 1;"), Category: str("客戶"), StopMode: str("page"), StopPage: new(int64(3)), ExpiresAt: new(int64(1700000000))}
	if err := applySettings(&web, in); err != nil {
		t.Fatal(err)
	}
	if web.RunDir != "/public" || len(web.IndexFiles) != 2 || web.Rewrite != "location / {\n  try_files $uri /index.php?$args;\n}" || web.Category != "客戶" {
		t.Fatalf("settings %+v", web)
	}
	web.Stopped = true
	if err := sv.save(ctx, &web); err != nil {
		t.Fatal(err)
	}
	got, _ := sv.Get(ctx, web.ID)
	if got.StopMode != "page" || got.StopPage != 3 || got.ExpiresAt != 1700000000 || got.IndexFiles[0] != "home.html" || got.CustomConf == "" {
		t.Fatalf("stored %+v", got)
	}
	sp := sv.spec(got)
	if sp.Root != "/www/wwwroot/a.com/public" || sp.StopPage != "p3" || sp.Index[1] != "index.php" || sp.Custom != "add_header X-Test 1;" {
		t.Fatalf("spec %+v", sp)
	}
	for _, bad := range []UpdateInput{
		{RunDir: str("../etc")}, {RunDir: str("/a/../../b")}, {IndexFiles: &[]string{"a b"}}, {Rewrite: str("}\nserver {")},
		{CustomConf: str("location / {")}, {StopMode: str("503")}, {Category: str("<b>")}, {ExpiresAt: new(int64(-1))},
	} {
		w := web
		if applySettings(&w, bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// Website settings need website files.
	if applySettings(&entry, UpdateInput{Rewrite: str("")}) != ErrNoContent {
		t.Error("rewrite on a pure domain entry")
	}
	// Subdirectory binding: b.com serves a.com/blog.
	if err := sv.applyTarget(ctx, &entry, TargetInput{Target: TargetLink, TargetSite: web.ID, TargetSubdir: "blog"}); err != nil {
		t.Fatal(err)
	}
	sv.save(ctx, &entry)
	if sp := sv.spec(entry); sp.Root != "/www/wwwroot/a.com/blog" || sp.Type != "php" {
		t.Fatalf("subdir spec %+v", sp)
	}
	if err := sv.applyTarget(ctx, &entry, TargetInput{Target: TargetLink, TargetSite: web.ID, TargetSubdir: "../x"}); err == nil {
		t.Error("subdir outside the site accepted")
	}
	cats, _ := sv.Categories(ctx)
	if len(cats) != 1 || cats[0] != "客戶" {
		t.Errorf("categories %v", cats)
	}
	// A stopped site in 404 mode has no stop page.
	got.StopMode = "404"
	if sp := sv.spec(got); sp.StopPage != "" {
		t.Errorf("404 mode stop page %q", sp.StopPage)
	}
}
