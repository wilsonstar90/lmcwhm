package files

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- automatic remarks ----

// AutoRemark names what a system path is (a translation key and an
// argument), shown when the user has not written a remark.
type AutoRemark struct {
	Code string `json:"code"`
	Arg  string `json:"arg,omitempty"`
}

var sysDirs = map[string]string{
	"/www": "www", "/www/wwwroot": "wwwroot", "/www/wwwlogs": "wwwlogs", "/www/.Recycle_bin": "recycle",
	"/etc": "etc", "/root": "root", "/home": "home", "/tmp": "tmp", "/var/log": "var_log", "/var/lib/docker": "docker",
	"/usr": "usr", "/boot": "boot", "/proc": "proc", "/sys": "sys", "/dev": "dev", "/bin": "bin", "/sbin": "bin",
	"/lib": "lib", "/opt": "opt", "/var": "var", "/srv": "srv", "/mnt": "mnt", "/media": "mnt", "/run": "run",
	"/etc/ssh": "ssh", "/etc/nftables.conf": "nftables", "/etc/hosts": "hosts", "/etc/fstab": "fstab", "/etc/crontab": "crontab",
}

var panelDirs = map[string]string{
	"": "panel", "data": "panel_data", "backup": "panel_backup", "web": "panel_web", "edge": "panel_edge", "apps": "panel_apps",
	"logs": "panel_logs", "geoip": "panel_geoip", "bin": "panel_bin", "data/filehist": "file_history", "data/trash": "db_trash",
}

var sysNames = map[string]string{
	".user.ini": "user_ini", "wp-config.php": "wp_config", ".htaccess": "htaccess", "404.html": "page_404", "50x.html": "page_50x",
	".well-known": "well_known", "wp-content": "wp_content", "wp-admin": "wp_admin", "wp-includes": "wp_includes",
	"composer.json": "composer", "package.json": "npm", ".env": "env_file", "robots.txt": "robots", "xmlrpc.php": "xmlrpc",
	"uploads": "", "plugins": "", "themes": "",
}

// SystemRemark describes p if it is a known system or panel path; home is
// the panel home (/opt/lmcwhm). Website roots are added by the caller.
func SystemRemark(p, home string) *AutoRemark {
	p = Clean(p)
	if c, ok := sysDirs[p]; ok {
		return &AutoRemark{Code: c}
	}
	if home != "" {
		if rest, ok := strings.CutPrefix(p, Clean(home)); ok && (rest == "" || rest[0] == '/') {
			if c, ok := panelDirs[strings.TrimPrefix(rest, "/")]; ok {
				return &AutoRemark{Code: c}
			}
		}
	}
	base := path.Base(p)
	if c := sysNames[base]; c != "" {
		return &AutoRemark{Code: c}
	}
	// wp-content/{plugins,themes,uploads}
	if parent := path.Base(path.Dir(p)); parent == "wp-content" {
		switch base {
		case "plugins", "themes", "uploads":
			return &AutoRemark{Code: "wp_" + base}
		}
	}
	return nil
}

// ---- size ----

// DirSize adds up the sizes of the files below p (symlinks not followed)
// until ctx ends; done is false when it stopped early.
func (f *FS) DirSize(ctx context.Context, p string) (size int64, files int, done bool, err error) {
	r, err := rel(p)
	if err != nil {
		return 0, 0, false, ErrBadPath
	}
	root, err := f.open()
	if err != nil {
		return 0, 0, false, err
	}
	defer root.Close()
	err = fs.WalkDir(root.FS(), r, func(_ string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir // unreadable directory
			}
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				size += fi.Size()
				files++
			}
		}
		return nil
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return size, files, false, nil
	}
	if err != nil {
		return 0, 0, false, mapErr(err)
	}
	return size, files, true, nil
}

// ---- history (editor saves) ----

// HistoryKeep is how many earlier versions of a file are kept.
const HistoryKeep = 10

type Version struct {
	ID   string `json:"id"` // unix nanoseconds
	Time int64  `json:"time"`
	Size int64  `json:"size"`
}

func (f *FS) histDir(p string) string {
	h := sha1.Sum([]byte(Clean(p)))
	return filepath.Join(f.HistDir, hex.EncodeToString(h[:]))
}

// keepVersion saves the current content of p before it is overwritten.
func (f *FS) keepVersion(p string, old []byte) {
	if f.HistDir == "" {
		return
	}
	dir := f.histDir(p)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	os.WriteFile(filepath.Join(dir, "path"), []byte(Clean(p)), 0o600)
	os.WriteFile(filepath.Join(dir, strconv.FormatInt(time.Now().UnixNano(), 10)), old, 0o600)
	vs, _ := f.History(p)
	for _, v := range vs[min(len(vs), HistoryKeep):] {
		os.Remove(filepath.Join(dir, v.ID))
	}
}

// History lists the saved versions of p, newest first.
func (f *FS) History(p string) ([]Version, error) {
	out := []Version{}
	if f.HistDir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(f.histDir(p))
	if err != nil {
		return out, nil
	}
	for _, e := range entries {
		n, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Version{ID: e.Name(), Time: n / int64(time.Second), Size: fi.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// ReadVersion returns a saved version of p.
func (f *FS) ReadVersion(p, id string) (string, error) {
	if _, err := strconv.ParseInt(id, 10, 64); err != nil || f.HistDir == "" {
		return "", ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(f.histDir(p), id))
	if err != nil {
		return "", ErrNotFound
	}
	return string(b), nil
}
