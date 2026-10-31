package backup

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const sqlText = "CREATE TABLE t (id INT);\n"

func gz(b []byte) []byte {
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	w.Write(b)
	w.Close()
	return out.Bytes()
}

func zipOf(name string, b []byte) []byte {
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	z.Create("__MACOSX/._" + name)
	w, _ := z.Create("dump/" + name)
	w.Write(b)
	z.Close()
	return out.Bytes()
}

func tgzOf(name string, b []byte) []byte {
	var raw bytes.Buffer
	t := tar.NewWriter(&raw)
	t.WriteHeader(&tar.Header{Name: "README", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	t.Write([]byte("hi"))
	t.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg})
	t.Write(b)
	t.Close()
	return gz(raw.Bytes())
}

func TestOpenSQL(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string][]byte{
		"a.sql":         []byte(sqlText),
		"b.sql.gz":      gz([]byte(sqlText)),
		"c.zip":         zipOf("c.sql", []byte(sqlText)),
		"d.zip":         zipOf("d.sql.gz", gz([]byte(sqlText))),
		"e.tar.gz":      tgzOf("e.sql", []byte(sqlText)),
		"f.tgz":         tgzOf("x/f.sql.gz", gz([]byte(sqlText))),
		"G.SQL.GZ":      gz([]byte(sqlText)),
		"nosql.zip":     zipOf("readme.txt", []byte("x")),
		"broken.sql.gz": []byte("not gzip"),
	} {
		os.WriteFile(filepath.Join(dir, name), body, 0o644)
	}
	for _, name := range []string{"a.sql", "b.sql.gz", "c.zip", "d.zip", "e.tar.gz", "f.tgz", "G.SQL.GZ"} {
		rc, err := OpenSQL(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != sqlText {
			t.Fatalf("%s: %q", name, b)
		}
	}
	if _, err := OpenSQL(filepath.Join(dir, "nosql.zip")); err != ErrNoSQL {
		t.Fatalf("zip without SQL: %v", err)
	}
	if _, err := OpenSQL(filepath.Join(dir, "broken.sql.gz")); err != ErrImportFormat {
		t.Fatalf("broken gzip: %v", err)
	}
}

func TestImportFiles(t *testing.T) {
	sv := &Service{Dir: t.TempDir()}
	for _, p := range []string{"database/shop/shop_1.sql.gz", "database/upload/x.zip", "database/upload/notes.txt", "database/.tmp/y.sql", "site/a.com/a.tar.gz"} {
		f := filepath.Join(sv.Dir, p)
		os.MkdirAll(filepath.Dir(f), 0o755)
		os.WriteFile(f, []byte("x"), 0o644)
	}
	all, err := sv.ImportFiles("")
	if err != nil || len(all) != 2 {
		t.Fatalf("%v %+v", err, all)
	}
	if got, _ := sv.ImportFiles("SHOP"); len(got) != 1 || got[0].Name != "shop_1.sql.gz" {
		t.Fatalf("search: %+v", got)
	}
	if !sv.InImportDir(filepath.Join(sv.Dir, "database/upload/x.zip")) || sv.InImportDir(filepath.Join(sv.Dir, "database/../site/a.com/a.tar.gz")) ||
		sv.InImportDir(sv.ImportDir()) || sv.InImportDir("/etc/passwd") {
		t.Fatal("InImportDir")
	}
	for name, ok := range map[string]bool{"dump.sql": true, "a.tar.gz": true, "../x.sql": false, ".hidden.sql": false, "x.exe": false, "a/b.sql": false} {
		if _, got := UploadName(name); got != ok {
			t.Errorf("UploadName(%q) = %v", name, got)
		}
	}
}

func TestWriteChunk(t *testing.T) {
	sv := &Service{Dir: t.TempDir()}
	if n, err := sv.WriteChunk("a.sql", 0, 100, bytes.NewReader([]byte("hello ")), false); err != nil || n != 6 {
		t.Fatal(n, err)
	}
	// A chunk at the wrong offset is refused with where to resume.
	if n, err := sv.WriteChunk("a.sql", 3, 100, bytes.NewReader([]byte("x")), false); err != ErrOffset || n != 6 {
		t.Fatal(n, err)
	}
	if n, err := sv.WriteChunk("a.sql", 6, 100, bytes.NewReader([]byte("world")), true); err != nil || n != 11 {
		t.Fatal(n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(sv.UploadDir(), "a.sql")); string(b) != "hello world" {
		t.Fatalf("%q", b)
	}
	if _, err := sv.WriteChunk("big.sql", 0, 4, bytes.NewReader([]byte("too long")), true); err != ErrBadName {
		t.Fatal("over the limit accepted")
	}
}
