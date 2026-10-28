package tamper

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestRuleMatching(t *testing.T) {
	r := Rule{Path: "/w/a", Exts: []string{"php", "htaccess"}, Exclude: []string{"wp-content/uploads"}}
	if !r.protectsFile("index.php") || !r.protectsFile(".htaccess") || r.protectsFile("photo.jpg") {
		t.Fatal("extension matching")
	}
	if !r.excluded("wp-content/uploads") || !r.excluded("wp-content/uploads/2026/x.php") || r.excluded("wp-content/uploadsx") {
		t.Fatal("exclusion matching")
	}
}

func setup(t *testing.T) (*Service, string) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	t.Cleanup(func() { st.Close() })
	web := filepath.Join(dir, "www")
	site := filepath.Join(web, "a.com")
	os.MkdirAll(filepath.Join(site, "uploads"), 0o755)
	os.WriteFile(filepath.Join(site, "index.php"), []byte("<?php echo 'good';"), 0o644)
	return New(st, web, dir), site
}

func TestSaveValidation(t *testing.T) {
	sv, site := setup(t)
	ctx := context.Background()
	if _, err := sv.Save(ctx, Rule{Path: "/etc", Mode: "watch"}); err != ErrPath {
		t.Fatalf("outside web root: %v", err)
	}
	if _, err := sv.Save(ctx, Rule{Path: site, Mode: "nope"}); err != ErrInvalid {
		t.Fatalf("bad mode: %v", err)
	}
	r, err := sv.Save(ctx, Rule{Path: site + "/", Mode: "watch", Enabled: false, Exclude: []string{"/uploads/", "../../etc"}})
	if err != nil || r.Path != site || len(r.Exts) == 0 || r.Exclude[0] != "uploads" || r.Exclude[1] != "etc" || r.State != "off" {
		t.Fatalf("saved: %+v %v", r, err)
	}
	if _, err := sv.Save(ctx, Rule{Path: site, Mode: "lock"}); err != ErrExists {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestWatchRestore(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("inotify")
	}
	sv, site := setup(t)
	ctx := context.Background()
	r, err := sv.Save(ctx, Rule{Path: site, Mode: "watch", Restore: true, Enabled: true, Exclude: []string{"uploads"}})
	if err != nil || r.State != "watching" {
		t.Fatalf("%+v %v", r, err)
	}
	defer sv.Stop()
	idx := filepath.Join(site, "index.php")
	os.WriteFile(idx, []byte("<?php evil();"), 0o644)
	os.WriteFile(filepath.Join(site, "new.php"), []byte("<?php shell();"), 0o644)
	os.WriteFile(filepath.Join(site, "uploads", "ok.php"), []byte("x"), 0o644)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(idx)
		_, err := os.Stat(filepath.Join(site, "new.php"))
		if string(b) == "<?php echo 'good';" && os.IsNotExist(err) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if b, _ := os.ReadFile(idx); string(b) != "<?php echo 'good';" {
		t.Fatalf("not restored: %s", b)
	}
	if _, err := os.Stat(filepath.Join(site, "new.php")); !os.IsNotExist(err) {
		t.Fatal("new file not removed")
	}
	if _, err := os.Stat(filepath.Join(site, "uploads", "ok.php")); err != nil {
		t.Fatal("excluded file touched")
	}
	evs, _ := sv.Events(ctx, r.ID, 10)
	if len(evs) < 2 {
		t.Fatalf("events: %+v", evs)
	}
}
