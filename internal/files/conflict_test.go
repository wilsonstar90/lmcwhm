package files

import (
	"os"
	"path/filepath"
	"testing"
)

func read(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

func TestOverwrite(t *testing.T) {
	f, root := newFS(t)
	w := func(p, body string) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(body), 0o644)
	}
	w("a/x.txt", "new-x")
	w("a/sub/y.txt", "new-y")
	w("b/a/x.txt", "old-x")
	w("b/a/keep.txt", "keep")

	got, err := f.Conflicts([]string{"/b/a", "/b/none"})
	if err != nil || len(got) != 1 || got[0] != "/b/a" {
		t.Fatalf("conflicts %v %v", got, err)
	}
	if err := f.Copy("/a", "/b/a"); err != ErrExists {
		t.Fatalf("copy without overwrite: %v", err)
	}
	// Copy over: the folders merge, files of the same name are replaced.
	if err := f.CopyOver("/a", "/b/a"); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "b/a/x.txt")) != "new-x" || read(t, filepath.Join(root, "b/a/keep.txt")) != "keep" || read(t, filepath.Join(root, "b/a/sub/y.txt")) != "new-y" {
		t.Fatal("copy over did not merge")
	}
	// Move over a file: the source is gone, the target replaced.
	w("c/x.txt", "moved")
	if err := f.MoveOver("/c/x.txt", "/b/a/x.txt"); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "b/a/x.txt")) != "moved" {
		t.Fatal("move over a file")
	}
	if _, err := os.Stat(filepath.Join(root, "c/x.txt")); err == nil {
		t.Fatal("moved file still at its source")
	}
	// Move a folder over a folder merges and removes the source.
	if err := f.MoveOver("/a", "/b/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a")); err == nil || read(t, filepath.Join(root, "b/a/keep.txt")) != "keep" {
		t.Fatal("move over a folder")
	}
	// Never into itself or over its own parent.
	if err := f.MoveOver("/b", "/b/a/b"); err != ErrBadPath {
		t.Fatalf("into itself: %v", err)
	}
	if err := f.CopyOver("/b/a", "/b"); err != ErrBadPath {
		t.Fatalf("over its parent: %v", err)
	}
}

func TestExtractConflicts(t *testing.T) {
	f, root := newFS(t)
	for _, format := range []string{"zip", "tar.gz"} {
		arc := "/www/s." + format
		if err := f.Compress([]string{"/www/site"}, arc, format); err != nil {
			t.Fatal(err)
		}
		out := "/out-" + format
		if got, err := f.ExtractConflicts(arc, out); err != nil || len(got) != 0 {
			t.Fatalf("%s: new folder: %v %v", format, got, err)
		}
		if err := f.Extract(arc, out, false); err != nil {
			t.Fatal(err)
		}
		got, err := f.ExtractConflicts(arc, out)
		if err != nil || len(got) != 1 || got[0] != out+"/site/index.html" {
			t.Fatalf("%s: %v %v", format, got, err)
		}
		p := filepath.Join(root, out, "site", "index.html")
		os.WriteFile(p, []byte("mine"), 0o644)
		if f.Extract(arc, out, true); read(t, p) != "mine" {
			t.Fatalf("%s: skip replaced the file", format)
		}
		if f.Extract(arc, out, false); read(t, p) != "hi" {
			t.Fatalf("%s: overwrite kept the file", format)
		}
	}
}
