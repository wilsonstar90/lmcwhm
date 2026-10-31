package backup

// Database import (like aaPanel's 導入): an SQL dump uploaded to, or already
// on, the server is loaded into a database. Uploads and the database backups
// live under <backup dir>/database, which the import window lists.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

var (
	ErrImportUnsupported = errors.New("backup.import_unsupported")
	ErrImportFormat      = errors.New("backup.import_format")
	ErrNoSQL             = errors.New("backup.import_no_sql")
)

// ImportExts are the files the import window accepts.
var ImportExts = []string{".sql", ".sql.gz", ".zip", ".tar.gz", ".tgz"}

// ImportExt returns the accepted extension of name ("" = not accepted).
func ImportExt(name string) string {
	n := strings.ToLower(name)
	// Longest first: ".sql.gz" before ".sql", ".tar.gz" before ".gz".
	for _, e := range []string{".sql.gz", ".tar.gz", ".tgz", ".zip", ".sql"} {
		if strings.HasSuffix(n, e) {
			return e
		}
	}
	return ""
}

// ImportDir holds the database backups and the uploaded dumps.
func (sv *Service) ImportDir() string { return filepath.Join(sv.LocalDir(), KindDatabase) }

// UploadDir is where the import window's uploads go.
func (sv *Service) UploadDir() string { return filepath.Join(sv.ImportDir(), "upload") }

// ImportFile is one file the import window lists.
type ImportFile struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

// ImportFiles lists the importable files under ImportDir, newest first,
// whose name contains q.
func (sv *Service) ImportFiles(q string) ([]ImportFile, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	out := []ImportFile{}
	root := sv.ImportDir()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root && errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipAll
			}
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || ImportExt(d.Name()) == "" || q != "" && !strings.Contains(strings.ToLower(d.Name()), q) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, ImportFile{Path: p, Name: d.Name(), Size: info.Size(), MTime: info.ModTime().Unix()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].MTime > out[j].MTime })
	return out, err
}

// InImportDir reports whether p is a file inside ImportDir.
func (sv *Service) InImportDir(p string) bool {
	rel, err := filepath.Rel(sv.ImportDir(), filepath.Clean(p))
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && filepath.IsAbs(p)
}

// UploadName checks an uploaded file name: a plain name with an accepted extension.
func UploadName(name string) (string, bool) {
	if name != filepath.Base(name) || strings.HasPrefix(name, ".") || len(name) > 200 || ImportExt(name) == "" ||
		strings.ContainsAny(name, "/\\\x00") {
		return "", false
	}
	return name, true
}

// OpenSQL opens a dump as a stream of SQL: .sql as it is, .sql.gz unpacked,
// and from a .zip / .tar.gz / .tgz the first .sql or .sql.gz inside.
func OpenSQL(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	switch ImportExt(path) {
	case ".sql":
		return f, nil
	case ".sql.gz":
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, ErrImportFormat
		}
		return readCloser{gz, f}, nil
	case ".tar.gz", ".tgz":
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, ErrImportFormat
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				f.Close()
				return nil, ErrNoSQL
			}
			if err != nil {
				f.Close()
				return nil, ErrImportFormat
			}
			if h.Typeflag != tar.TypeReg || skipMember(h.Name) {
				continue
			}
			switch ImportExt(h.Name) {
			case ".sql":
				return readCloser{tr, f}, nil
			case ".sql.gz":
				inner, err := gzip.NewReader(tr)
				if err != nil {
					f.Close()
					return nil, ErrImportFormat
				}
				return readCloser{inner, f}, nil
			}
		}
	case ".zip":
		f.Close()
		zr, err := zip.OpenReader(path)
		if err != nil {
			return nil, ErrImportFormat
		}
		for _, m := range zr.File {
			if m.FileInfo().IsDir() || skipMember(m.Name) {
				continue
			}
			ext := ImportExt(m.Name)
			if ext != ".sql" && ext != ".sql.gz" {
				continue
			}
			r, err := m.Open()
			if err != nil {
				zr.Close()
				return nil, ErrImportFormat
			}
			if ext == ".sql" {
				return readCloser{r, multiCloser{r, zr}}, nil
			}
			inner, err := gzip.NewReader(r)
			if err != nil {
				r.Close()
				zr.Close()
				return nil, ErrImportFormat
			}
			return readCloser{inner, multiCloser{r, zr}}, nil
		}
		zr.Close()
		return nil, ErrNoSQL
	}
	f.Close()
	return nil, ErrImportFormat
}

// skipMember ignores macOS resource forks and hidden files in archives.
func skipMember(name string) bool {
	base := filepath.Base(name)
	return strings.HasPrefix(name, "__MACOSX/") || strings.HasPrefix(base, ".")
}

type readCloser struct {
	io.Reader
	c io.Closer
}

func (r readCloser) Close() error { return r.c.Close() }

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var first error
	for _, c := range m {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ImportDatabase loads the dump at path into database id; with backupFirst
// the database is backed up locally before.
func (sv *Service) ImportDatabase(ctx context.Context, id int64, path string, backupFirst bool, log tasks.Logf) error {
	if sv.DBs == nil || sv.Apps == nil {
		return ErrImportUnsupported
	}
	d, err := sv.DBs.Get(ctx, id)
	if err != nil {
		return ErrBadTarget
	}
	eng, err := sv.Apps.Get(ctx, d.AppID)
	if err != nil {
		return err
	}
	switch eng.App {
	case "mysql", "mariadb", "postgres":
	default:
		return ErrImportUnsupported
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ErrNotFound
	}
	if backupFirst {
		log("backing up %s before the import", d.Name)
		if err := sv.Run(ctx, Plan{Kind: KindDatabase, Target: strconv.FormatInt(id, 10), Dests: []string{DestLocal}}, 0, logTo(log)); err != nil {
			return fmt.Errorf("backup before the import: %w", err)
		}
	}
	rc, err := OpenSQL(path)
	if err != nil {
		return err
	}
	defer rc.Close()
	log("importing %s (%s) into %s database %s", filepath.Base(path), human(info.Size()), eng.Name, d.Name)
	if err := loadSQL(ctx, eng, d.Name, rc); err != nil {
		return err
	}
	log("import finished")
	return nil
}

// logTo turns task log lines into an io.Writer.
func logTo(log tasks.Logf) io.Writer {
	return logWriterFunc(func(p []byte) (int, error) {
		for _, l := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
			if l != "" {
				log("%s", l)
			}
		}
		return len(p), nil
	})
}

type logWriterFunc func([]byte) (int, error)

func (f logWriterFunc) Write(p []byte) (int, error) { return f(p) }
