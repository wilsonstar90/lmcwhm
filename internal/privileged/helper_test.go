package privileged

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHelperAllowsOnlyFixedActions(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "docker.log")
	fake := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$DOCKER_LOG\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_LOG", logPath)
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("lmcwhm-helper-%d.sock", time.Now().UnixNano()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Serve(ctx, socket) }()
	client := Client{Socket: socket}
	for range 50 {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := client.Call(ctx, "docker.restart", "lmcwhm-redis74"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(logPath)
	if !strings.Contains(string(body), "restart -t 30 lmcwhm-redis74") {
		t.Fatalf("unexpected helper command: %q", body)
	}
	if err := client.Call(ctx, "docker.restart", "../escape"); err == nil {
		t.Fatal("invalid container name accepted")
	}
	if err := client.Call(ctx, "shell.exec", "id"); err == nil {
		t.Fatal("arbitrary shell action accepted")
	}
}
