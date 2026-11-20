package site

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fakeRuntimes struct{}

func (fakeRuntimes) RuntimeImage(context.Context, string, string) (string, error) { return "java:21", nil }
func (fakeRuntimes) UsedPorts(context.Context) map[int]bool { return map[int]bool{} }

func TestImmutableProjectDeploymentAndRollback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.jar"), []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "large.js"), []byte("excluded"), 0o600); err != nil {
		t.Fatal(err)
	}
	sv := &Service{Runtimes: fakeRuntimes{}}
	s := Site{Type: "java", RuntimeVersion: "21", Domain: "app.test", Root: root}
	if err := sv.deployProject(context.Background(), s, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	first, err := filepath.EvalSymlinks(projectCurrent(root))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := currentProjectRelease(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(first, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("node_modules copied into the release")
	}
	if err := os.WriteFile(filepath.Join(root, "app.jar"), []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sv.deployProject(context.Background(), s, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	second, err := filepath.EvalSymlinks(projectCurrent(root))
	if err != nil || first == second {
		t.Fatalf("release did not switch: %q %q %v", first, second, err)
	}
	if body, _ := os.ReadFile(filepath.Join(first, "app.jar")); string(body) != "v1" {
		t.Fatalf("old release mutated: %q", body)
	}
	fakeDocker := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(fakeDocker)+":"+os.Getenv("PATH"))
	if err := sv.rollbackProject(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	current, _ := filepath.EvalSymlinks(projectCurrent(root))
	if current != first {
		t.Fatalf("rollback selected %q, want %q", current, first)
	}
}
