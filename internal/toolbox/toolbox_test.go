package toolbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fake(t *testing.T) (*Service, *[]string) {
	root := t.TempDir()
	for p, body := range map[string]string{
		"/etc/fstab":                 "UUID=x / ext4 defaults 0 1\n/swapfile none swap sw 0 0\n",
		"/etc/hosts":                 "127.0.0.1 localhost\n127.0.1.1 old old\n10.0.0.5 old\n",
		"/etc/ssh/sshd_config":       "#Port 22\nPermitRootLogin yes\n",
		"/root/.ssh/authorized_keys": "",
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(body), 0o644)
	}
	var calls []string
	sv := &Service{Root: root, Run: func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch name {
		case "sshd":
			if len(args) > 0 && args[0] == "-T" {
				return "port 22\npermitrootlogin yes\npasswordauthentication yes\npubkeyauthentication yes\n", nil
			}
		case "systemctl":
			if len(args) > 1 && args[1] == "--quiet" {
				return "", errors.New("inactive")
			}
		}
		return "", nil
	}}
	return sv, &calls
}

func TestFstabAndHosts(t *testing.T) {
	sv, _ := fake(t)
	if err := sv.fstabSwap(false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(sv.path("/etc/fstab"))
	if strings.Contains(string(b), "swapfile") {
		t.Fatalf("fstab: %s", b)
	}
	sv.fstabSwap(true)
	sv.fstabSwap(true)
	b, _ = os.ReadFile(sv.path("/etc/fstab"))
	if strings.Count(string(b), "/swapfile none swap") != 1 {
		t.Fatalf("fstab: %s", b)
	}
	if err := sv.SetHostname(context.Background(), "bad_name!"); !errors.Is(err, ErrHostname) {
		t.Fatal("bad hostname accepted")
	}
	if err := sv.SetHostname(context.Background(), "web1.example.com"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(sv.path("/etc/hosts"))
	if !strings.Contains(string(b), "127.0.1.1 web1.example.com web1\n") || !strings.Contains(string(b), "127.0.0.1 localhost") {
		t.Fatalf("hosts: %s", b)
	}
}

func TestSSH(t *testing.T) {
	sv, calls := fake(t)
	ctx := context.Background()
	in := SSH{Port: 2222, PermitRoot: "prohibit-password", PasswordAuth: false, PubkeyAuth: true}
	if err := sv.SetSSH(ctx, in); !errors.Is(err, ErrLockout) {
		t.Fatalf("no keys + no password must be refused: %v", err)
	}
	opened := false
	sv.SSHChanged = func(context.Context) error { opened = true; return nil }
	in.PasswordAuth = true
	if err := sv.SetSSH(ctx, in); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(sv.path(sshDropIn))
	for _, want := range []string{"Port 2222", "PermitRootLogin prohibit-password", "PasswordAuthentication yes"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("drop-in missing %q:\n%s", want, b)
		}
	}
	main, _ := os.ReadFile(sv.path("/etc/ssh/sshd_config"))
	if !strings.HasPrefix(string(main), "Include /etc/ssh/sshd_config.d/*.conf") {
		t.Errorf("Include not added:\n%s", main)
	}
	if !opened {
		t.Error("firewall hook not called on port change")
	}
	if !strings.Contains(strings.Join(*calls, "\n"), "systemctl restart ssh") {
		t.Errorf("ssh not restarted: %v", *calls)
	}
	if err := sv.AddKey(ctx, "not a key"); !errors.Is(err, ErrKey) {
		t.Fatal("bad key accepted")
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{
		"Archived and active journals take up 1.5G in the file system.": 3 << 29,
		"120MB": 120 << 20, "Total reclaimed space: 0B": 0, "512kB": 512 << 10,
	} {
		if got := parseSize(in); got != want {
			t.Errorf("%q = %d, want %d", in, got, want)
		}
	}
}
