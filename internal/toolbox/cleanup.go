package toolbox

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Junk cleanup (系統垃圾清理): each item reports its size and can be cleaned.

type Junk struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
	// Count is the number of files (0 when not counted).
	Count int `json:"count"`
}

var rotatedRe = regexp.MustCompile(`\.(\d+|\d+\.gz|gz|old|bak)$`)

// junkFiles walks dir and returns the files keep() rejects.
func walkSize(root string, match func(path string, d fs.DirEntry) bool) (int64, int, []string) {
	var size int64
	var n int
	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !match(p, d) {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			size += fi.Size()
			n++
			files = append(files, p)
		}
		return nil
	})
	return size, n, files
}

func (sv *Service) junkItems(home string) map[string]func() (int64, int, []string) {
	week := time.Now().Add(-7 * 24 * time.Hour)
	return map[string]func() (int64, int, []string){
		// Rotated system logs (*.1, *.gz).
		"system_logs": func() (int64, int, []string) {
			return walkSize(sv.path("/var/log"), func(p string, _ fs.DirEntry) bool {
				return rotatedRe.MatchString(p) && !strings.Contains(p, "/journal/")
			})
		},
		// Downloaded .deb packages.
		"apt_cache": func() (int64, int, []string) {
			return walkSize(sv.path("/var/cache/apt/archives"), func(p string, _ fs.DirEntry) bool { return strings.HasSuffix(p, ".deb") })
		},
		// Files under /tmp untouched for a week.
		"tmp": func() (int64, int, []string) {
			return walkSize(sv.path("/tmp"), func(p string, d fs.DirEntry) bool {
				fi, err := d.Info()
				return err == nil && fi.ModTime().Before(week) && fi.Mode().IsRegular()
			})
		},
		// Rotated website logs.
		"site_logs": func() (int64, int, []string) {
			return walkSize(sv.path("/www/wwwlogs"), func(p string, _ fs.DirEntry) bool { return rotatedRe.MatchString(p) })
		},
		// The panel's own rotated logs and stale upload chunks.
		"panel_tmp": func() (int64, int, []string) {
			return walkSize(sv.path(filepath.Join(home, "tmp")), func(string, fs.DirEntry) bool { return true })
		},
	}
}

// JunkKeys is the display order; journal and docker are measured by their tools.
var JunkKeys = []string{"system_logs", "journal", "apt_cache", "tmp", "site_logs", "panel_tmp", "docker"}

func (sv *Service) ScanJunk(ctx context.Context, home string) []Junk {
	items := sv.junkItems(home)
	out := []Junk{}
	for _, k := range JunkKeys {
		j := Junk{Key: k}
		switch k {
		case "journal":
			if o, err := sv.Run(ctx, "journalctl", "--disk-usage"); err == nil {
				j.Size = parseSize(o)
			}
		case "docker":
			// Reclaimable: dangling images and build cache.
			if o, err := sv.Run(ctx, "docker", "system", "df", "--format", "{{.Type}}\t{{.Reclaimable}}"); err == nil {
				for _, l := range strings.Split(o, "\n") {
					typ, rec, _ := strings.Cut(l, "\t")
					if typ == "Images" || typ == "Build Cache" {
						j.Size += parseSize(rec)
					}
				}
			}
		default:
			j.Size, j.Count, _ = items[k]()
		}
		out = append(out, j)
	}
	return out
}

// parseSize reads the first "12.3MB" / "1.2G" style figure in s.
var sizeRe = regexp.MustCompile(`(?i)([\d.]+)\s*([KMGT]?)i?B?`)

func parseSize(s string) int64 {
	for _, m := range sizeRe.FindAllStringSubmatch(s, -1) {
		f, err := strconv.ParseFloat(m[1], 64)
		if err != nil || m[1] == "." {
			continue
		}
		mult := map[string]float64{"": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[strings.ToUpper(m[2])]
		if m[2] == "" && !strings.ContainsAny(m[0], "Bb") {
			continue
		}
		return int64(f * mult)
	}
	return 0
}

// CleanJunk removes the selected items and returns the bytes freed (estimate).
func (sv *Service) CleanJunk(ctx context.Context, home string, keys []string) (int64, error) {
	items := sv.junkItems(home)
	var freed int64
	for _, k := range keys {
		switch k {
		case "journal":
			before := sv.ScanJunk(ctx, home)[1].Size
			if _, err := sv.Run(ctx, "journalctl", "--vacuum-size=100M"); err != nil {
				return freed, err
			}
			if o, err := sv.Run(ctx, "journalctl", "--disk-usage"); err == nil {
				freed += max(before-parseSize(o), 0)
			}
		case "docker":
			o, err := sv.Run(ctx, "docker", "image", "prune", "-f")
			if err != nil {
				return freed, err
			}
			freed += parseSize(strings.TrimPrefix(o[strings.LastIndex(o, ":")+1:], " "))
			if o, err := sv.Run(ctx, "docker", "builder", "prune", "-f"); err == nil {
				freed += parseSize(o[strings.LastIndex(o, ":")+1:])
			}
		default:
			f, ok := items[k]
			if !ok {
				return freed, ErrInvalid
			}
			size, _, files := f()
			for _, p := range files {
				os.Remove(p)
			}
			freed += size
		}
	}
	return freed, nil
}
