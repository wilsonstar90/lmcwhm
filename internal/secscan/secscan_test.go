package secscan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestScan(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	root := filepath.Join(dir, "root")
	for p, body := range map[string]string{
		"/etc/passwd":                                 "root:x:0:0::/root:/bin/bash\ntoor:x:0:0::/root:/bin/sh\nbob:x:1000:1000::/home/bob:/bin/bash\n",
		"/etc/shadow":                                 "root:$6$abc:19000::::::\nbob::19000::::::\n",
		"/var/run/reboot-required":                    "",
		"/proc/sys/kernel/randomize_va_space":         "0\n",
		"/www/wwwroot/a.com/index.php":                "<?php",
		"/www/wwwroot/a.com/wp-content/uploads/x.php": "<?php",
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(body), 0o644)
	}
	os.Chmod(filepath.Join(root, "/www/wwwroot/a.com/index.php"), 0o666)
	sv := New(st)
	sv.Root = root
	sv.Run = func(_ context.Context, name string, args ...string) (string, error) {
		switch name {
		case "sshd":
			return "port 22\npermitrootlogin yes\npasswordauthentication yes\nmaxauthtries 6\n", nil
		case "apt-get":
			return "Inst libssl3 [3.0.2] (3.0.2-0ubuntu1.18 Ubuntu:22.04/jammy-security [amd64])\nInst vim [2:8.2]\n", nil
		}
		return "", nil
	}
	rep, err := sv.Scan(context.Background(), Input{WebRoot: "/www/wwwroot", EOLPHP: map[string]int{"7.4": 2},
		PanelFindings: []Finding{{Code: "no_2fa", Level: "warning"}}})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]Finding{}
	for _, f := range rep.Findings {
		codes[f.Code] = f
	}
	for _, c := range []string{"ssh_root_login", "ssh_password_auth", "ssh_default_port", "uid0_users", "empty_passwords", "reboot_required",
		"security_updates", "no_auto_updates", "sysctl", "php_eol", "world_writable", "php_in_uploads", "no_2fa"} {
		if _, ok := codes[c]; !ok {
			t.Errorf("missing finding %s", c)
		}
	}
	if codes["ssh_root_login"].Level != "critical" || codes["uid0_users"].Value != "toor" || codes["empty_passwords"].Value != "bob" ||
		codes["security_updates"].Value != "1" || !strings.Contains(codes["sysctl"].Value, "randomize_va_space=0") {
		t.Errorf("details: %+v", codes)
	}
	if rep.Findings[0].Level != "critical" || rep.Score != 0 {
		t.Errorf("order/score: %d %+v", rep.Score, rep.Findings[0])
	}
	if last, ok := sv.Latest(context.Background()); !ok || last.ID != rep.ID || len(last.Findings) != len(rep.Findings) {
		t.Fatal("report not stored")
	}
	if err := sv.Fix(context.Background(), "chmod_web", "/www/wwwroot", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(root, "/www/wwwroot/a.com/index.php")); fi.Mode().Perm() != 0o664 {
		t.Fatalf("chmod fix: %v", fi.Mode())
	}
}
