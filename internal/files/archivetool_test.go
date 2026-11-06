package files

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParse7zList(t *testing.T) {
	out := []byte(`Path = site
Folder = +
Size = 0
Attributes = D_ drwxr-xr-x

Path = site/index.php
Folder = -
Size = 14
Attributes = A_ -rw-r--r--

Path = site/evil
Folder = -
Size = 4
Attributes = A_ lrwxrwxrwx
Symbolic Link = /etc
`)
	l := parse7zList(out)
	if len(l) != 3 || !l[0].dir || l[1].dir || l[1].name != "site/index.php" || l[1].link || !l[2].link {
		t.Fatalf("%+v", l)
	}
	if checkEntries(l[:2]) != nil || checkEntries(l) != ErrArchiveLink {
		t.Fatal("link not refused")
	}
	if checkEntries([]toolEntry{{name: "../x"}}) == nil {
		t.Fatal("../ not refused")
	}
}

func TestParseRarList(t *testing.T) {
	out := []byte(`
Archive: /tmp/a.rar
Details: RAR 5

        Name: site/index.php
        Type: File
        Size: 14

        Name: site
        Type: Directory

        Name: site/evil
        Type: File
  Attributes: lrwxrwxrwx
`)
	l := parseRarList(out)
	if len(l) != 3 || l[0].name != "site/index.php" || l[0].dir || !l[1].dir || !l[2].link {
		t.Fatalf("%+v", l)
	}
}

// With the tools installed (CI installs them), round-trip both formats.
func TestToolRoundTrip(t *testing.T) {
	for _, k := range []struct{ kind, bin string }{{"7z", "7z"}, {"7z", "7zz"}, {"rar", "rar"}} {
		if _, err := exec.LookPath(k.bin); err != nil {
			continue
		}
		dir := t.TempDir()
		f := &FS{Root: dir}
		mustWrite(t, filepath.Join(dir, "src", "a", "b.txt"), "hello")
		dest := "/out." + k.kind
		if err := f.Compress([]string{"/src/a"}, dest, k.kind); err != nil {
			t.Fatalf("%s compress: %v", k.bin, err)
		}
		if err := f.Compress([]string{"/src/a"}, dest, k.kind); err != ErrExists {
			t.Fatalf("%s: second compress = %v, want exists", k.bin, err)
		}
		if err := f.Extract(dest, "/x", false); err != nil {
			t.Fatalf("%s extract: %v", k.bin, err)
		}
		if b, err := readFile(filepath.Join(dir, "x", "a", "b.txt")); err != nil || b != "hello" {
			t.Fatalf("%s: extracted %q %v", k.bin, b, err)
		}
		c, err := f.ExtractConflicts(dest, "/x")
		if err != nil || len(c) != 1 {
			t.Fatalf("%s conflicts %v %v", k.bin, c, err)
		}
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
