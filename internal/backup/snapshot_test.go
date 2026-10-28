package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestSnapshotRoundTrip(t *testing.T) {
	sv := newSvc(t)
	home := sv.Home
	sv.WWWRoot = filepath.Join(t.TempDir(), "wwwroot")
	write(t, filepath.Join(home, "data", "secret.key"), "k")
	write(t, filepath.Join(home, "edge", "haproxy.cfg"), "cfg")
	write(t, filepath.Join(home, "apps", "mysql", "8.4", "data", "ibdata1"), "db")
	write(t, filepath.Join(home, "logs", "panel.log"), "skip")
	write(t, filepath.Join(home, "bin", "lmcwhm"), "skip")
	write(t, filepath.Join(home, "backup", "old.tar.gz"), "skip")
	write(t, filepath.Join(sv.WWWRoot, "a.com", "index.php"), "<?php")
	sv.st.DB.Exec(`INSERT INTO settings(key, value) VALUES('snapshot_test', 'yes')`)

	var buf bytes.Buffer
	if err := sv.writeSnapshot(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	www := filepath.Join(t.TempDir(), "wwwroot")
	write(t, filepath.Join(dst, "data", "panel.db"), "fresh install db")
	m, err := RestoreSnapshot(dst, www, &buf, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if m.Version == "" || len(m.Parts) != 2 {
		t.Fatalf("manifest %+v", m)
	}
	for p, want := range map[string]string{
		filepath.Join(dst, "data", "secret.key"):                      "k",
		filepath.Join(dst, "edge", "haproxy.cfg"):                     "cfg",
		filepath.Join(dst, "apps", "mysql", "8.4", "data", "ibdata1"): "db",
		filepath.Join(www, "a.com", "index.php"):                      "<?php",
	} {
		if b, err := os.ReadFile(p); err != nil || string(b) != want {
			t.Errorf("%s = %q %v", p, b, err)
		}
	}
	for _, p := range []string{"logs/panel.log", "bin/lmcwhm", "backup/old.tar.gz"} {
		if _, err := os.Stat(filepath.Join(dst, p)); err == nil {
			t.Errorf("%s should not be in a snapshot", p)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(dst, "data", "panel.db.before-restore-*")); len(m) != 1 {
		t.Error("previous database not kept")
	}
	st, err := store.Open(filepath.Join(dst, "data", "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var v string
	if st.DB.QueryRow(`SELECT value FROM settings WHERE key = 'snapshot_test'`).Scan(&v); v != "yes" {
		t.Fatal("restored database is not the snapshot's")
	}
}
