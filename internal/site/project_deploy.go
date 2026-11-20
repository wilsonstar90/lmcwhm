package site

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

type projectDeployment struct {
	SHA256    string `json:"sha256"`
	CreatedAt int64  `json:"created_at"`
}

func projectBase(root string) string     { return filepath.Join(root, ".lmcwhm") }
func projectReleases(root string) string { return filepath.Join(projectBase(root), "releases") }
func projectCurrent(root string) string  { return filepath.Join(projectBase(root), "current") }

func currentProjectRelease(root string) (string, error) {
	current := projectCurrent(root)
	if _, err := os.Lstat(current); err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(projectReleases(root))
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(target, base+string(os.PathSeparator)) {
		return "", ErrBadTarget
	}
	return current, nil
}

func copyProjectSource(root, destination string) (string, error) {
	hasher := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		first := strings.Split(filepath.ToSlash(rel), "/")[0]
		if first == ".lmcwhm" || first == ".git" || first == "node_modules" || first == ".venv" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case !info.Mode().IsRegular():
			return nil
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				in.Close()
				return err
			}
			_, err = io.Copy(io.MultiWriter(out, hasher), in)
			if err == nil {
				_, _ = io.WriteString(hasher, rel)
			}
			if closeErr := out.Close(); err == nil {
				err = closeErr
			}
			if closeErr := in.Close(); err == nil {
				err = closeErr
			}
			return err
		}
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (sv *Service) deployProject(ctx context.Context, s Site, log tasks.Logf) error {
	if sv.Runtimes == nil {
		return ErrNoRuntime
	}
	image, err := sv.Runtimes.RuntimeImage(ctx, s.Type, s.RuntimeVersion)
	if err != nil {
		return ErrNoRuntime
	}
	releases := projectReleases(s.Root)
	if err := os.MkdirAll(releases, 0o755); err != nil {
		return err
	}
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	partial := filepath.Join(releases, ".partial-"+stamp)
	final := filepath.Join(releases, stamp)
	defer os.RemoveAll(partial)
	if err := os.MkdirAll(partial, 0o755); err != nil {
		return err
	}
	sum, err := copyProjectSource(s.Root, partial)
	if err != nil {
		return err
	}
	if err := os.Rename(partial, final); err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(final)
		}
	}()
	install := ""
	switch s.Type {
	case "node":
		if _, err := os.Stat(filepath.Join(final, "package.json")); err == nil {
			install = "npm install --omit=dev --no-audit --no-fund"
			if _, err := os.Stat(filepath.Join(final, "package-lock.json")); err == nil {
				install = "npm ci --omit=dev --no-audit --no-fund"
			}
		}
	case "python":
		install = "python -m venv .venv; if [ -f requirements.txt ]; then .venv/bin/pip install -q -r requirements.txt; fi"
	}
	if install != "" {
		log("building an immutable project release")
		if _, err := dockerx.Run(ctx, "run", "--rm", "-v", s.Root+":"+s.Root, "-w", final, image, "sh", "-c", install); err != nil {
			return err
		}
	}
	body, _ := json.Marshal(projectDeployment{SHA256: sum, CreatedAt: time.Now().Unix()})
	if err := os.WriteFile(filepath.Join(final, "deployment.json"), body, 0o600); err != nil {
		return err
	}
	next := projectCurrent(s.Root) + ".next"
	os.Remove(next)
	if err := os.Symlink(final, next); err != nil {
		return err
	}
	if err := os.Rename(next, projectCurrent(s.Root)); err != nil {
		return err
	}
	keep = true
	pruneProjectReleases(s.Root, 3)
	return nil
}

func pruneProjectReleases(root string, keep int) {
	entries, err := os.ReadDir(projectReleases(root))
	if err != nil {
		return
	}
	names := []string{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for i := keep; i < len(names); i++ {
		os.RemoveAll(filepath.Join(projectReleases(root), names[i]))
	}
}

func (sv *Service) rollbackProject(ctx context.Context, s Site) error {
	current, err := filepath.EvalSymlinks(projectCurrent(s.Root))
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(projectReleases(s.Root))
	if err != nil {
		return err
	}
	names := []string{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	previous := ""
	for _, name := range names {
		candidate := filepath.Join(projectReleases(s.Root), name)
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return err
		}
		if resolved != current {
			previous = candidate
			break
		}
	}
	if previous == "" {
		return ErrNoContent
	}
	next := projectCurrent(s.Root) + ".next"
	os.Remove(next)
	if err := os.Symlink(previous, next); err != nil {
		return err
	}
	if err := os.Rename(next, projectCurrent(s.Root)); err != nil {
		return err
	}
	if dockerx.State(ctx, ProjectContainer(s.Domain)) != "" {
		if _, err := dockerx.Run(ctx, "restart", ProjectContainer(s.Domain)); err != nil {
			os.Remove(next)
			if linkErr := os.Symlink(current, next); linkErr == nil {
				_ = os.Rename(next, projectCurrent(s.Root))
			}
			return err
		}
	}
	return nil
}
