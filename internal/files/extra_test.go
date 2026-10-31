package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSystemRemark(t *testing.T) {
	for p, want := range map[string]string{
		"/www/wwwroot": "wwwroot", "/etc": "etc", "/opt/lmcwhm/backup": "panel_backup", "/opt/lmcwhm": "panel",
		"/www/wwwroot/a.com/wp-config.php": "wp_config", "/www/wwwroot/a.com/.user.ini": "user_ini",
		"/www/wwwroot/a.com/wp-content/plugins": "wp_plugins", "/www/wwwroot/a.com/plugins": "",
		"/opt/lmcwhm-x": "", "/www/wwwroot/a.com/index.php": "",
	} {
		got := ""
		if r := SystemRemark(p, "/opt/lmcwhm"); r != nil {
			got = r.Code
		}
		if got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
}

func TestDirSizeAndHistory(t *testing.T) {
	f, root := newFS(t)
	os.WriteFile(filepath.Join(root, "www", "site", "big"), make([]byte, 1000), 0o644)
	size, n, done, err := f.DirSize(context.Background(), "/www")
	if err != nil || !done || size != 1002 || n != 2 {
		t.Fatalf("size %d %d %v %v", size, n, done, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, done, _ := f.DirSize(ctx, "/www"); done {
		t.Error("cancelled walk reported done")
	}

	f.HistDir = t.TempDir()
	p := "/www/site/index.html"
	for i := range 13 {
		if err := f.WriteText(p, fmt.Sprintf("v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	f.WriteText(p, "v12") // unchanged: no new version
	vs, _ := f.History(p)
	if len(vs) != HistoryKeep {
		t.Fatalf("%d versions", len(vs))
	}
	newest, _ := f.ReadVersion(p, vs[0].ID)
	oldest, _ := f.ReadVersion(p, vs[len(vs)-1].ID)
	if newest != "v11" || oldest != "v2" {
		t.Errorf("newest %q oldest %q", newest, oldest)
	}
	if _, err := f.ReadVersion(p, "../path"); err == nil {
		t.Error("bad version id read")
	}
}
