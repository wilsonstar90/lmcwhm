package webenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncRuntimeMaps(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "lw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	e := &Env{Home: home}
	if err := os.MkdirAll(filepath.Join(e.EdgeDir(), "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := fakeHAProxy(t, e.adminSock())
	mapPath := filepath.Join(e.EdgeDir(), "maps", "prot_exact.map")
	listPath := filepath.Join(e.EdgeDir(), "maps", "idc.lst")
	before := map[string][]byte{
		mapPath:  []byte("a.example ,on,\nb.example ,on,\n"),
		listPath: []byte("192.0.2.1\n"),
	}
	after := map[string][]byte{
		mapPath:  []byte("a.example ,on,idc,\nc.example ,on,\n"),
		listPath: []byte("192.0.2.2\n"),
	}
	attempted, err := e.syncRuntimeMaps(context.Background(), before, after)
	if err != nil || !attempted {
		t.Fatalf("runtime update: attempted=%v err=%v", attempted, err)
	}
	for _, want := range []string{
		"set map " + mapPath + " a.example ,on,idc,",
		"del map " + mapPath + " b.example",
		"set map " + mapPath + " c.example ,on,",
		"del acl " + listPath + " 192.0.2.1",
		"add acl " + listPath + " 192.0.2.2",
	} {
		if !got.Contains(want) {
			t.Errorf("missing %q in %q", want, got.Snapshot())
		}
	}
}

func TestRuntimeMapsFallback(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "lw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	e := &Env{Home: home}
	if err := os.MkdirAll(filepath.Join(e.EdgeDir(), "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeHAProxy(t, e.adminSock())
	path := filepath.Join(e.EdgeDir(), "maps", "prot_exact.map")
	before := map[string][]byte{path: []byte("a.example ,on,\n")}
	after := map[string][]byte{path: []byte("a.example value with spaces\n")}
	attempted, err := e.syncRuntimeMaps(context.Background(), before, after)
	if attempted || err != nil {
		t.Fatalf("unsafe value did not select hot reload: attempted=%v err=%v", attempted, err)
	}
	attempted, err = e.syncRuntimeMaps(context.Background(), after, before)
	if attempted || err != nil {
		t.Fatalf("unsafe old value did not select hot reload: attempted=%v err=%v", attempted, err)
	}
	cfg := filepath.Join(e.EdgeDir(), "haproxy.cfg")
	attempted, err = e.syncRuntimeMaps(context.Background(), map[string][]byte{cfg: []byte("old")}, map[string][]byte{cfg: []byte("new")})
	if attempted || err != nil {
		t.Fatalf("config change did not select hot reload: attempted=%v err=%v", attempted, err)
	}
}

func TestSnapshotsEqual(t *testing.T) {
	a := map[string][]byte{"a": []byte("one")}
	if !snapshotsEqual(a, map[string][]byte{"a": []byte("one")}) || snapshotsEqual(a, map[string][]byte{"a": []byte("two")}) {
		t.Fatal("snapshot comparison does not compare file contents")
	}
	if got := changedPaths(a, map[string][]byte{"a": []byte("one"), "b": []byte("new")}); len(got) != 1 || !strings.HasSuffix(got[0], "b") {
		t.Fatalf("changed paths: %v", got)
	}
}
