package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/config"
)

// Panel snapshot (面板快照, 1Panel style): everything needed to move the server
// to a new machine in one archive — the panel database and secrets, edge and
// web configuration, App Store data (databases are paused while copied, so
// the copy is crash-consistent) and the website files. Restore it with
// `lmcwhm snapshot restore` on a freshly installed panel.

const KindSnapshot = "snapshot"

// SnapshotManifest is snapshot.json at the top of the archive.
type SnapshotManifest struct {
	Version   string   `json:"version"`
	Hostname  string   `json:"hostname"`
	Arch      string   `json:"arch"`
	CreatedAt int64    `json:"created_at"`
	Parts     []string `json:"parts"`
}

// snapshotSkip are panel-home entries a snapshot leaves out: the binary is
// installed separately; backups, logs and the GeoIP files are re-created.
var snapshotSkip = []string{"bin", "backup", "logs", "geoip", "tmp", "data/panel.db", "data/panel.db-wal", "data/panel.db-shm", "data/lmcwhm.pid", "data/mcp.sock"}

func skipHome(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, s := range snapshotSkip {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return strings.HasPrefix(rel, ".panel-restore-") || strings.HasPrefix(rel, "data/.tmp")
}

// pauseApps freezes the running App Store service containers and returns the
// function that resumes them.
func (sv *Service) pauseApps(ctx context.Context) func() {
	if sv.Apps == nil {
		return func() {}
	}
	list, err := sv.Apps.List(ctx)
	if err != nil {
		return func() {}
	}
	var paused []string
	for _, a := range list {
		if a.Container != "" && a.State == "running" {
			if exec.CommandContext(ctx, "docker", "pause", a.Container).Run() == nil {
				paused = append(paused, a.Container)
			}
		}
	}
	return func() {
		for _, c := range paused {
			exec.Command("docker", "unpause", c).Run()
		}
	}
}

func (sv *Service) writeSnapshot(ctx context.Context, w io.Writer) error {
	tmp := filepath.Join(sv.LocalDir(), ".tmp")
	os.MkdirAll(tmp, 0o700)
	db := filepath.Join(tmp, "snapshot-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".db")
	defer os.Remove(db)
	if _, err := sv.st.DB.ExecContext(ctx, `VACUUM INTO ?`, db); err != nil {
		return err
	}
	host, _ := os.Hostname()
	m := SnapshotManifest{Version: config.Version, Hostname: host, Arch: runtime.GOARCH, CreatedAt: time.Now().Unix(), Parts: []string{"home"}}
	srcs := []tarSource{}
	// The local backup directory may live inside the panel home or the web root.
	inBackup := func(base string) func(string) bool {
		return func(rel string) bool {
			p := filepath.Join(base, rel)
			return p == sv.LocalDir() || strings.HasPrefix(p, sv.LocalDir()+"/")
		}
	}
	homeSkip := inBackup(sv.Home)
	srcs = append(srcs, tarSource{dir: sv.Home, prefix: "home", skip: func(rel string) bool {
		return skipHome(rel) || homeSkip(rel) || filepath.ToSlash(rel) == "apps"
	}})
	srcs = append(srcs, tarSource{dir: db, prefix: "home/data/panel.db"})
	if fi, err := os.Stat(filepath.Join(sv.Home, "apps")); err == nil && fi.IsDir() {
		srcs = append(srcs, tarSource{dir: filepath.Join(sv.Home, "apps"), prefix: "home/apps", pause: true})
	}
	if sv.WWWRoot != "" {
		if _, err := os.Stat(sv.WWWRoot); err == nil {
			m.Parts = append(m.Parts, "www")
			srcs = append(srcs, tarSource{dir: sv.WWWRoot, prefix: "www", skip: inBackup(sv.WWWRoot)})
		}
	}
	mj, _ := json.MarshalIndent(m, "", "  ")
	mf := filepath.Join(tmp, "snapshot-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".json")
	if err := os.WriteFile(mf, mj, 0o600); err != nil {
		return err
	}
	defer os.Remove(mf)
	srcs = append([]tarSource{{dir: mf, prefix: "snapshot.json"}}, srcs...)
	for i := range srcs {
		if srcs[i].pause {
			srcs[i].before = func() func() { return sv.pauseApps(ctx) }
		}
	}
	return writeTarGz(ctx, w, srcs, nil)
}

var ErrSnapshot = errors.New("backup.invalid_snapshot")

// RestoreSnapshot unpacks a snapshot over home and wwwRoot. The panel must be
// stopped; the current database is kept as panel.db.before-restore-<time>.
// Ownership and modes are kept from the archive (database files belong to
// the container users). On the next start the panel recreates the web stack
// and App Store containers from the restored database.
func RestoreSnapshot(home, wwwRoot string, r io.Reader, log func(string, ...any)) (SnapshotManifest, error) {
	var m SnapshotManifest
	gz, err := gzip.NewReader(r)
	if err != nil {
		return m, ErrBadArchive
	}
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil || h.Name != "snapshot.json" {
		return m, ErrSnapshot
	}
	if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m); err != nil {
		return m, ErrSnapshot
	}
	log("snapshot of %s (lmcwhm %s, %s) from %s", m.Hostname, m.Version, m.Arch, time.Unix(m.CreatedAt, 0).Format(time.RFC3339))
	if m.Arch != runtime.GOARCH {
		log("note: the snapshot was made on %s; App Store images are pulled for %s on start", m.Arch, runtime.GOARCH)
	}
	db := filepath.Join(home, "data", "panel.db")
	if _, err := os.Stat(db); err == nil {
		if err := os.Rename(db, db+".before-restore-"+time.Now().Format("20060102150405")); err != nil {
			return m, err
		}
	}
	for _, s := range []string{"-wal", "-shm"} {
		os.Remove(db + s)
	}
	for _, d := range []string{home, wwwRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return m, err
		}
	}
	homeRoot, err := os.OpenRoot(home)
	if err != nil {
		return m, err
	}
	defer homeRoot.Close()
	wwwR, err := os.OpenRoot(wwwRoot)
	if err != nil {
		return m, err
	}
	defer wwwR.Close()
	files := 0
	last := time.Now()
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, fmt.Errorf("%w: %v", ErrBadArchive, err)
		}
		part, rest, _ := strings.Cut(strings.TrimPrefix(h.Name, "/"), "/")
		var root *os.Root
		switch part {
		case "home":
			root = homeRoot
		case "www":
			root = wwwR
		default:
			continue
		}
		name := filepath.Clean(filepath.FromSlash(rest))
		if name == "." || name == "" {
			continue
		}
		if name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return m, fmt.Errorf("%w: unsafe path %q", ErrBadArchive, h.Name)
		}
		if err := writeEntry(root, name, h, tr); err != nil {
			return m, fmt.Errorf("%s: %w", h.Name, err)
		}
		if files++; time.Since(last) > 5*time.Second {
			log("%d entries restored", files)
			last = time.Now()
		}
	}
	log("%d entries restored", files)
	return m, nil
}

// writeEntry writes one tar entry below root keeping mode and ownership.
func writeEntry(root *os.Root, name string, h *tar.Header, r io.Reader) error {
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
	case tar.TypeReg:
		root.Remove(name)
		f, err := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
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
		if err := root.Symlink(h.Linkname, name); err != nil {
			return err
		}
	default:
		return nil
	}
	root.Lchown(name, h.Uid, h.Gid)
	return nil
}
