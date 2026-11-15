package wordpress

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/site"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

func TestDownloadCoreKeepsLongPaths(t *testing.T) {
	long := "wp-includes/php-ai-client/third-party/Http/Discovery/Exception/StrategyUnavailableExceptionWithAVeryLongName.php"
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for name, body := range map[string]string{"wordpress/index.php": "<?php", "wordpress/" + long: "x", "../evil.php": "no"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zip" {
			w.Write(zbuf.Bytes())
			return
		}
		w.Write([]byte(`{"offers":[{"download":"` + srv.URL + `/zip","current":"9.9"}]}`))
	}))
	defer srv.Close()
	coreAPI = srv.URL + "/api"
	newClient = srv.Client
	root := t.TempDir()
	if err := downloadCore(context.Background(), root, t.Logf); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, long)); string(b) != "x" {
		t.Fatal("long path lost")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.php")); err == nil {
		t.Fatal("entry outside wordpress/ extracted")
	}
}

func newTestWP(t *testing.T, withCLI bool) (*Service, int64, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env := webenv.New(dir)
	env.WWWRoot = filepath.Join(dir, "www")
	root := filepath.Join(env.WWWRoot, "example.com")
	if err := os.MkdirAll(filepath.Join(root, "wp-includes"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "wp-config.php"), []byte("<?php"), 0o644)
	os.WriteFile(filepath.Join(root, "wp-includes", "version.php"), []byte("<?php $wp_version = '6.8';"), 0o644)
	res, err := st.DB.Exec(`INSERT INTO sites(domain,type,php_version,root,created_at,updated_at) VALUES('example.com','php','8.4',?,1,1)`, root)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if withCLI {
		if err := os.MkdirAll(filepath.Join(env.WWWRoot, toolsDirName), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(env.WWWRoot, toolsDirName, wpCLIFileName), []byte("test"), 0o644)
	}
	return New(site.NewService(st, env, nil, nil, tasks.NewManager()), nil, env, tasks.NewManager()), id, st
}

func TestInfoReturnsSiteWhenPluginListFails(t *testing.T) {
	sv, id, _ := newTestWP(t, true)
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n  *\"option get siteurl\"*) printf 'https://example.com' ;;\n  *\"plugin list\"*) echo 'plugin query failed' >&2; exit 1 ;;\n  *\"config get\"*) exit 1 ;;\n  *) printf '[]' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	info, err := sv.Info(context.Background(), id)
	if err != nil {
		t.Fatalf("plugin list failure hid the whole site: %v", err)
	}
	if info.Domain != "example.com" || info.SiteURL != "https://example.com" || !slices.Contains(info.Warnings, "plugins_unavailable") {
		t.Fatalf("partial WordPress info: %+v", info)
	}
}

func TestInfoReturnsSiteWhenCLIDownloadFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "blocked", http.StatusForbidden) }))
	defer srv.Close()
	oldURL := wpCLIURL
	wpCLIURL = srv.URL
	t.Cleanup(func() { wpCLIURL = oldURL })
	sv, id, _ := newTestWP(t, false)
	info, err := sv.Info(context.Background(), id)
	if err != nil {
		t.Fatalf("CLI download failure hid the whole site: %v", err)
	}
	if info.Domain != "example.com" || !slices.Contains(info.Warnings, "cli_unavailable") {
		t.Fatalf("CLI warning missing: %+v", info)
	}
}

func TestInfoUsesAppStoreWPCLI(t *testing.T) {
	sv, id, st := newTestWP(t, false)
	box, err := secrets.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sv.Apps = appstore.New(st, box, tasks.NewManager(), t.TempDir())
	if _, err := st.DB.Exec(`INSERT INTO apps(app,version,port,secret,created_at) VALUES('wp-cli','2',0,'',1)`); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n  *\"inspect -f\"*) printf 'running' ;;\n  *\"php -d memory_limit\"*) echo 'unexpected PHP fallback' >&2; exit 1 ;;\n  *\"option get siteurl\"*) printf 'https://example.com' ;;\n  *\"config get\"*) exit 1 ;;\n  *) printf '[]' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	info, err := sv.Info(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if info.SiteURL != "https://example.com" || len(info.Warnings) != 0 {
		t.Fatalf("App Store WP-CLI not used: %+v", info)
	}
}
