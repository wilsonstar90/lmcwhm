package ftp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestSFTPAccountLifecycle(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	box, _ := secrets.Open(dir)
	www := filepath.Join(dir, "www")
	os.MkdirAll(filepath.Join(www, "a.com"), 0o755)
	os.Symlink("/etc", filepath.Join(www, "evil"))
	sv := New(st, box, dir, www)
	sv.SFTPRoot = filepath.Join(dir, "sftp")
	sv.SSHD = filepath.Join(dir, "sshd_config")
	os.WriteFile(sv.SSHD, []byte("Include /etc/ssh/sshd_config.d/*.conf\nPasswordAuthentication no\n"), 0o644)
	var calls []string
	sv.run = func(_ context.Context, stdin, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " ")+" <"+stdin)
		if name == "mountpoint" || name == "getent" {
			return "", os.ErrNotExist
		}
		return "", nil
	}
	ctx := context.Background()
	for _, bad := range []Input{
		{Kind: KindSFTP, Username: "root", Password: "password1", Dir: filepath.Join(www, "a.com")},
		{Kind: KindSFTP, Username: "Bad Name", Password: "password1", Dir: filepath.Join(www, "a.com")},
		{Kind: KindSFTP, Username: "alice", Password: "short", Dir: filepath.Join(www, "a.com")},
		{Kind: KindSFTP, Username: "alice", Password: "password1", Dir: "/etc"},
		{Kind: KindSFTP, Username: "alice", Password: "password1", Dir: filepath.Join(www, "evil")},
		{Kind: KindSFTP, Username: "alice", Password: "password1", Dir: www},
	} {
		if _, err := sv.Create(ctx, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	a, err := sv.Create(ctx, Input{Kind: KindSFTP, Username: "alice", Password: "s3cret-pass", Dir: filepath.Join(www, "a.com")})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(sv.SSHD)
	if !strings.HasSuffix(string(cfg), markEnd+"\n") || !strings.Contains(string(cfg), "ForceCommand internal-sftp -d /site") {
		t.Fatalf("sshd_config:\n%s", cfg)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"sshd -t", "groupadd --system lmcwhm-sftp", "--uid 33", "chpasswd  <alice:s3cret-pass", "mount --bind " + filepath.Join(www, "a.com")} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if pw, _ := sv.Password(ctx, a.ID); pw != "s3cret-pass" {
		t.Fatal("password not kept")
	}
	// The block is added once.
	sv.Create(ctx, Input{Kind: KindSFTP, Username: "bob1", Password: "s3cret-pass", Dir: filepath.Join(www, "a.com")})
	cfg, _ = os.ReadFile(sv.SSHD)
	if strings.Count(string(cfg), markBegin) != 1 {
		t.Fatal("block duplicated")
	}
	if err := sv.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := sv.List(ctx); len(list) != 1 {
		t.Fatalf("list after delete: %v", list)
	}
}
