package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newFS(t *testing.T) (*FS, string) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "www", "site"), 0o755)
	os.WriteFile(filepath.Join(root, "www", "site", "index.html"), []byte("hi"), 0o644)
	return &FS{Root: root, Trash: "/www/.Recycle_bin"}, root
}

func TestTraversal(t *testing.T) {
	f, root := newFS(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600)
	// A symlink planted inside the root that points outside of it.
	os.Symlink(outside, filepath.Join(root, "www", "escape"))

	// ".." is clamped to the root: these name files inside it, never above.
	if _, err := f.ReadText("/../../../../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Errorf("dot-dot read: %v", err)
	}
	if err := f.WriteText("/../x.txt", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "x.txt")); err != nil {
		t.Errorf("../x.txt should land inside the root: %v", err)
	}
	// Symlinks cannot be used to leave the root.
	if _, err := f.ReadText("/www/escape/secret"); err == nil {
		t.Error("read through escaping symlink succeeded")
	}
	if err := f.WriteText("/www/escape/new", "x"); err == nil {
		t.Error("write through escaping symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); err == nil {
		t.Error("file created outside the root")
	}
	if _, err := f.List("/www/escape"); err == nil {
		t.Error("listed a directory outside the root")
	}
	if err := f.Copy("/www/escape/secret", "/www/copied"); err == nil {
		t.Error("copied a file from outside the root")
	}
	if _, err := f.WriteChunk("/www/escape/up", 0, strings.NewReader("x")); err == nil {
		t.Error("upload through escaping symlink succeeded")
	}
	if _, _, err := f.Open("/www/escape/secret"); err == nil {
		t.Error("downloaded a file outside the root")
	}
}

func TestEditAndTrash(t *testing.T) {
	f, root := newFS(t)
	if err := f.Create("/www/site/a.txt", false); err != nil {
		t.Fatal(err)
	}
	if err := f.Create("/www/site/a.txt", false); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate create: %v", err)
	}
	if err := f.WriteText("/www/site/a.txt", "hello"); err != nil {
		t.Fatal(err)
	}
	if s, _ := f.ReadText("/www/site/a.txt"); s != "hello" {
		t.Errorf("read %q", s)
	}
	os.WriteFile(filepath.Join(root, "bin"), []byte{1, 0, 2}, 0o644)
	if _, err := f.ReadText("/bin"); !errors.Is(err, ErrBinary) {
		t.Errorf("binary: %v", err)
	}
	if err := f.Copy("/www/site", "/www/site/inner"); !errors.Is(err, ErrBadPath) {
		t.Errorf("copy into itself: %v", err)
	}
	if err := f.Copy("/www/site", "/www/site2"); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete([]string{"/www/site2"}, false); err != nil {
		t.Fatal(err)
	}
	items, err := f.TrashList()
	if err != nil || len(items) != 1 || items[0].Path != "/www/site2" || !items[0].IsDir {
		t.Fatalf("trash = %+v %v", items, err)
	}
	if err := f.Restore(items[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "www", "site2", "index.html")); err != nil {
		t.Errorf("not restored: %v", err)
	}
	if err := f.Restore("../../etc"); err == nil {
		t.Error("restore accepted a bad id")
	}
	if err := f.Chmod("/www/site/a.txt", "600", false); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(root, "www", "site", "a.txt")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", fi.Mode().Perm())
	}
	if err := f.Chmod("/www/site/a.txt", "9z", false); !errors.Is(err, ErrBadMode) {
		t.Errorf("bad mode: %v", err)
	}
	n, err := f.WriteChunk("/www/up.bin", 0, strings.NewReader("abc"))
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	if n, _ = f.WriteChunk("/www/up.bin", 3, strings.NewReader("def")); n != 6 {
		t.Errorf("resumed size %d", n)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "www", "up.bin")); string(b) != "abcdef" {
		t.Errorf("chunks = %q", b)
	}
}

func TestCompressExtract(t *testing.T) {
	f, root := newFS(t)
	for _, format := range []string{"zip", "tar.gz"} {
		dest := "/www/site." + format
		if err := f.Compress([]string{"/www/site"}, dest, format); err != nil {
			t.Fatal(format, err)
		}
		if err := f.Extract(dest, "/out-"+format); err != nil {
			t.Fatal(format, err)
		}
		if b, err := os.ReadFile(filepath.Join(root, "out-"+format, "site", "index.html")); err != nil || string(b) != "hi" {
			t.Errorf("%s round trip: %q %v", format, b, err)
		}
	}
}

func TestZipSlip(t *testing.T) {
	f, root := newFS(t)
	outside := t.TempDir()

	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("../../evil.txt")
	w.Write([]byte("x"))
	zw.Close()
	os.WriteFile(filepath.Join(root, "evil.zip"), zb.Bytes(), 0o644)
	if err := f.Extract("/evil.zip", "/www/x"); !errors.Is(err, ErrBadPath) {
		t.Errorf("zip-slip: %v", err)
	}

	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "/abs.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	os.WriteFile(filepath.Join(root, "abs.tar.gz"), tb.Bytes(), 0o644)
	if err := f.Extract("/abs.tar.gz", "/www/x"); !errors.Is(err, ErrBadPath) {
		t.Errorf("absolute tar entry: %v", err)
	}

	// An entry through a symlink planted in the destination is refused by os.Root.
	os.MkdirAll(filepath.Join(root, "www", "y"), 0o755)
	os.Symlink(outside, filepath.Join(root, "www", "y", "link"))
	var lb bytes.Buffer
	zw = zip.NewWriter(&lb)
	w, _ = zw.Create("link/pwned.txt")
	w.Write([]byte("x"))
	zw.Close()
	os.WriteFile(filepath.Join(root, "link.zip"), lb.Bytes(), 0o644)
	if err := f.Extract("/link.zip", "/www/y"); err == nil {
		t.Error("extracted through a symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); err == nil {
		t.Error("file written outside the destination")
	}
}

func TestChmodSpecialBits(t *testing.T) {
	f, root := newFS(t)
	if err := os.WriteFile(filepath.Join(root, "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Chmod("/x", "2755", false); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 || fi.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("mode = %v, want setgid 0755", fi.Mode())
	}
}
