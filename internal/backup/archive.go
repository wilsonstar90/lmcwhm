package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/database"
)

type tarSource struct {
	dir    string // directory on disk
	prefix string // path inside the archive
	skip   func(rel string) bool
	// before runs before the source is read and returns what to run after
	// (snapshots pause the App Store containers around their data).
	before func() func()
	pause  bool
}

func excluded(rel string, patterns []string) bool {
	base := filepath.Base(rel)
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, rel); ok {
			return true
		}
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
	}
	return false
}

// writeTarGz archives the sources (files, directories, symlinks) as tar.gz.
func writeTarGz(ctx context.Context, w io.Writer, srcs []tarSource, exclude []string) error {
	gz, _ := gzip.NewWriterLevel(w, gzip.DefaultCompression)
	tw := tar.NewWriter(gz)
	for _, s := range srcs {
		if _, err := os.Lstat(s.dir); err != nil {
			return err
		}
		after := func() {}
		if s.before != nil {
			after = s.before()
		}
		err := filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rel, _ := filepath.Rel(s.dir, p)
			if rel != "." && (excluded(rel, exclude) || s.skip != nil && s.skip(rel)) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if !fi.Mode().IsRegular() && !fi.IsDir() && fi.Mode()&fs.ModeSymlink == 0 {
				return nil // sockets, devices, fifos
			}
			link := ""
			if fi.Mode()&fs.ModeSymlink != 0 {
				if link, err = os.Readlink(p); err != nil {
					return err
				}
			}
			h, err := tar.FileInfoHeader(fi, link)
			if err != nil {
				return err
			}
			h.Name = filepath.ToSlash(filepath.Join(s.prefix, rel))
			if fi.IsDir() {
				h.Name += "/"
			}
			h.Uname, h.Gname = "", ""
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			if fi.Mode().IsRegular() {
				f, err := os.Open(p)
				if err != nil {
					return err
				}
				// The file may change while it is read: copy exactly the header size.
				_, err = io.CopyN(tw, io.MultiReader(f, zeros{}), h.Size)
				f.Close()
				if err != nil {
					return err
				}
			}
			return nil
		})
		after()
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// zeros pads a file that shrank while it was archived.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// extractTarGz unpacks into dir, refusing entries that escape it.
func extractTarGz(r io.Reader, dir string, uid, gid int) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return ErrBadArchive
	}
	tr := tar.NewReader(gz)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	type dirTime struct {
		name string
		t    time.Time
	}
	var dirs []dirTime
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrBadArchive, err)
		}
		name := filepath.Clean(strings.TrimPrefix(filepath.FromSlash(h.Name), "/"))
		if name == "." {
			continue
		}
		if name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: unsafe path %q", ErrBadArchive, h.Name)
		}
		mode := fs.FileMode(h.Mode).Perm() | fs.FileMode(h.Mode)&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)
		if parent := filepath.Dir(name); parent != "." {
			if err := root.MkdirAll(parent, 0o755); err != nil {
				return err
			}
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
			root.Chmod(name, mode)
			dirs = append(dirs, dirTime{name, h.ModTime})
		case tar.TypeReg:
			root.Remove(name)
			f, err := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			root.Chmod(name, mode)
			root.Chtimes(name, h.ModTime, h.ModTime)
		case tar.TypeSymlink:
			root.Remove(name)
			// os.Root confines later writes; the link itself is stored as is.
			if err := root.Symlink(h.Linkname, name); err != nil {
				return err
			}
		default:
			continue
		}
		if uid >= 0 {
			root.Lchown(name, uid, gid)
		} else {
			root.Lchown(name, h.Uid, h.Gid)
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		root.Chtimes(dirs[i].name, dirs[i].t, dirs[i].t)
	}
	return nil
}

// swapRestore extracts into a sibling directory, then swaps it in for dir
// so a failed restore leaves the current content untouched.
func swapRestore(r io.Reader, dir string, uid, gid int) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stamp := time.Now().Format("20060102150405")
	tmp := filepath.Join(parent, "."+filepath.Base(dir)+".restore-"+stamp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	if err := extractTarGz(r, tmp, uid, gid); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if fi, err := os.Stat(dir); err == nil {
		os.Chmod(tmp, fi.Mode().Perm())
		if uid >= 0 {
			os.Lchown(tmp, uid, gid)
		}
	}
	old := filepath.Join(parent, "."+filepath.Base(dir)+".old-"+stamp)
	if _, err := os.Lstat(dir); err == nil {
		if err := os.Rename(dir, old); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.Rename(old, dir)
		os.RemoveAll(tmp)
		return err
	}
	return os.RemoveAll(old)
}

// ---- Docker volumes ----

func volumeTar(ctx context.Context, vol string, w io.Writer) error {
	return dockerStream(ctx, nil, w, "run", "--rm", "--network", "none", "-v", vol+":/v:ro", VolumeImage, "tar", "czf", "-", "-C", "/v", ".")
}

func volumeRestore(ctx context.Context, vol string, r io.Reader) error {
	return dockerStream(ctx, r, io.Discard, "run", "--rm", "-i", "--network", "none", "-v", vol+":/v", VolumeImage,
		"sh", "-c", "find /v -mindepth 1 -delete && tar xzf - -C /v")
}

// dockerStream runs docker with streamed stdin/stdout; stderr goes into the error.
func dockerStream(ctx context.Context, in io.Reader, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var errb bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return fmt.Errorf("docker %s: %v: %s", strings.Join(args[:min(2, len(args))], " "), err, msg)
	}
	return nil
}

// ---- databases ----

func dumpExt(engine string) string {
	switch engine {
	case "mongodb":
		return ".archive.gz"
	case "sqlserver":
		return ".bak.gz"
	}
	return ".sql.gz"
}

const mssqlFile = "/var/opt/mssql/data/lmcwhm-transfer.bak"

func mssqlBin(ctx context.Context, eng appstore.Installed) string {
	for _, b := range []string{"/opt/mssql-tools18/bin/sqlcmd", "/opt/mssql-tools/bin/sqlcmd"} {
		if exec.CommandContext(ctx, "docker", "exec", eng.Container, "test", "-x", b).Run() == nil {
			return b
		}
	}
	return "sqlcmd"
}

func mssql(ctx context.Context, eng appstore.Installed, q string) error {
	return dockerStream(ctx, nil, io.Discard, "exec", "-e", "SQLCMDPASSWORD="+eng.Password(), eng.Container,
		mssqlBin(ctx, eng), "-S", "127.0.0.1", "-U", "sa", "-C", "-b", "-Q", q)
}

// dumpDB writes a gzip-compressed logical dump of database name.
func dumpDB(ctx context.Context, eng appstore.Installed, name string, w io.Writer) error {
	if !database.ValidName(name) {
		return ErrBadTarget
	}
	gz := gzip.NewWriter(w)
	var err error
	switch eng.App {
	case "mysql", "mariadb":
		bin := "mysqldump"
		if eng.App == "mariadb" {
			bin = "mariadb-dump"
		}
		err = dockerStream(ctx, nil, gz, "exec", "-e", "MYSQL_PWD="+eng.Password(), eng.Container, bin,
			"-uroot", "-h127.0.0.1", "-P3306", "--single-transaction", "--quick", "--routines", "--triggers", "--events",
			"--default-character-set=utf8mb4", name)
	case "postgres":
		err = dockerStream(ctx, nil, gz, "exec", "-e", "PGPASSWORD="+eng.Password(), eng.Container,
			"pg_dump", "-h", "127.0.0.1", "-U", "postgres", "--clean", "--if-exists", name)
	case "mongodb":
		err = dockerStream(ctx, nil, gz, "exec", eng.Container, "mongodump", "--quiet", "--host", "127.0.0.1",
			"-u", "root", "-p", eng.Password(), "--authenticationDatabase", "admin", "--db", name, "--archive")
	case "sqlserver":
		if err = mssql(ctx, eng, fmt.Sprintf("BACKUP DATABASE [%s] TO DISK = N'%s' WITH COPY_ONLY, INIT, FORMAT", name, mssqlFile)); err == nil {
			err = dockerStream(ctx, nil, gz, "exec", eng.Container, "cat", mssqlFile)
			dockerStream(ctx, nil, io.Discard, "exec", eng.Container, "rm", "-f", mssqlFile)
		}
	default:
		err = database.ErrNoEngine
	}
	if err != nil {
		return err
	}
	return gz.Close()
}

// restoreDB loads a dump made by dumpDB into database name (replacing it).
func restoreDB(ctx context.Context, eng appstore.Installed, name string, r io.Reader) error {
	if !database.ValidName(name) {
		return ErrBadTarget
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return ErrBadArchive
	}
	switch eng.App {
	case "mysql", "mariadb":
		bin := "mysql"
		if eng.App == "mariadb" {
			bin = "mariadb"
		}
		return dockerStream(ctx, gz, io.Discard, "exec", "-i", "-e", "MYSQL_PWD="+eng.Password(), eng.Container, bin,
			"-uroot", "-h127.0.0.1", "-P3306", "--default-character-set=utf8mb4", name)
	case "postgres":
		return dockerStream(ctx, gz, io.Discard, "exec", "-i", "-e", "PGPASSWORD="+eng.Password(), eng.Container,
			"psql", "-h", "127.0.0.1", "-U", "postgres", "-v", "ON_ERROR_STOP=1", "-q", "-d", name)
	case "mongodb":
		return dockerStream(ctx, gz, io.Discard, "exec", "-i", eng.Container, "mongorestore", "--quiet", "--host", "127.0.0.1",
			"-u", "root", "-p", eng.Password(), "--authenticationDatabase", "admin", "--archive", "--drop",
			"--nsFrom", "$db$.$coll$", "--nsTo", name+".$coll$")
	case "sqlserver":
		if err := dockerStream(ctx, gz, io.Discard, "exec", "-i", eng.Container, "sh", "-c", "cat > "+mssqlFile); err != nil {
			return err
		}
		defer dockerStream(ctx, nil, io.Discard, "exec", eng.Container, "rm", "-f", mssqlFile)
		return mssql(ctx, eng, fmt.Sprintf(`IF DB_ID(N'%[1]s') IS NOT NULL ALTER DATABASE [%[1]s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE;
RESTORE DATABASE [%[1]s] FROM DISK = N'%[2]s' WITH REPLACE;
ALTER DATABASE [%[1]s] SET MULTI_USER;`, name, mssqlFile))
	}
	return database.ErrNoEngine
}

// ---- panel state ----

// panelDirs are the parts of the panel home a panel backup holds: settings
// and secrets (data), the edge proxy and its certificates, and the web
// server configuration. App data, logs and backups are not included.
var panelDirs = []string{"data", "edge", "web"}

func (sv *Service) writePanel(ctx context.Context, w io.Writer) error {
	snap := filepath.Join(sv.Dir, ".tmp", "panel-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".db")
	os.MkdirAll(filepath.Dir(snap), 0o700)
	defer os.Remove(snap)
	// VACUUM INTO makes a consistent copy while the panel keeps writing.
	if _, err := sv.st.DB.ExecContext(ctx, `VACUUM INTO ?`, snap); err != nil {
		return err
	}
	var srcs []tarSource
	for _, d := range panelDirs {
		dir := filepath.Join(sv.Home, d)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		src := tarSource{dir: dir, prefix: d}
		if d == "data" {
			src.skip = func(rel string) bool {
				return strings.HasPrefix(rel, "panel.db") || rel == "lmcwhm.pid"
			}
		}
		srcs = append(srcs, src)
	}
	srcs = append(srcs, tarSource{dir: snap, prefix: "data/panel.db"})
	return writeTarGz(ctx, w, srcs, nil)
}

// RestorePanel unpacks a panel backup into home. The panel must be stopped:
// it replaces the database underneath. The previous database is kept as
// data/panel.db.before-restore-<time>.
func RestorePanel(home string, r io.Reader) error {
	tmp, err := os.MkdirTemp(home, ".panel-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := extractTarGz(r, tmp, -1, -1); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(tmp, "data", "panel.db")); err != nil {
		return fmt.Errorf("%w: no data/panel.db", ErrBadArchive)
	}
	db := filepath.Join(home, "data", "panel.db")
	if _, err := os.Stat(db); err == nil {
		if err := os.Rename(db, db+".before-restore-"+time.Now().Format("20060102150405")); err != nil {
			return err
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(db + suffix)
	}
	for _, d := range panelDirs {
		src := filepath.Join(tmp, d)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyTree(src, filepath.Join(home, d)); err != nil {
			return err
		}
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		fi, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, fi.Mode().Perm())
		case fi.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			os.Remove(target)
			return os.Symlink(link, target)
		default:
			if err := copyFile(p, target); err != nil {
				return err
			}
			return os.Chmod(target, fi.Mode().Perm())
		}
	})
}

// ---- restore ----

// Restore puts a stored backup back. For a database, into (if non-zero)
// restores into another database instead of the original.
func (sv *Service) Restore(ctx context.Context, id, into int64, log func(string, ...any)) error {
	rec, err := sv.Get(ctx, id)
	if err != nil {
		return err
	}
	rc, err := sv.Open(ctx, rec)
	if err != nil {
		return err
	}
	defer rc.Close()
	log("restoring %s (%s) from %s", rec.Name, human(rec.Size), sv.destName(ctx, rec.Dest))
	switch rec.Kind {
	case KindSite:
		if sv.Sites == nil {
			return ErrNoRestore
		}
		s := sv.siteByDomain(ctx, rec.Target)
		if s == nil {
			return fmt.Errorf("%w: site %s", ErrBadTarget, rec.Target)
		}
		// PHP-FPM runs as www-data (33) in the official images.
		return swapRestore(rc, s.Root, 33, 33)
	case KindDatabase:
		if sv.DBs == nil {
			return ErrNoRestore
		}
		dbID := rec.TargetID
		if into != 0 {
			dbID = into
		}
		d, err := sv.DBs.Get(ctx, dbID)
		if err != nil {
			return fmt.Errorf("%w: database %s", ErrBadTarget, rec.Target)
		}
		eng, err := sv.Apps.Get(ctx, d.AppID)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(rec.Name, dumpExt(d.Engine)) {
			return fmt.Errorf("%w: %s is not a %s dump", ErrBadArchive, rec.Name, d.Engine)
		}
		log("loading into %s database %s", d.Engine, d.Name)
		return restoreDB(ctx, eng, d.Name, rc)
	case KindVolume:
		if !volumeRe.MatchString(rec.Target) {
			return ErrBadTarget
		}
		return volumeRestore(ctx, rec.Target, rc)
	case KindPath:
		if !filepath.IsAbs(rec.Target) || rec.Target == "/" {
			return ErrBadTarget
		}
		return swapRestore(rc, rec.Target, -1, -1)
	}
	// The panel database cannot be replaced under the running panel.
	return ErrNoRestore
}
