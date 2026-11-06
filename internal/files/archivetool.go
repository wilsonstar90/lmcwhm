package files

// 7z and rar go through the 7-Zip and RAR command-line tools, installed with
// apt the first time they are needed (7zip / p7zip-full, rar from multiverse).
//
// Extraction is as strict as for zip and tar: the archive is listed first and
// refused when it holds links or unsafe names, the tool unpacks into a fresh
// directory inside the destination, and only regular files and directories
// are then moved into place through an os.Root (no escapes, no symlinks).

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrToolMissing = errors.New("files.archive_tool_missing")
	ErrArchiveLink = errors.New("files.archive_has_links")
	ErrArchiveTool = errors.New("files.archive_tool_failed")
)

// ToolKind tells which tool handles name ("" = handled in Go).
func ToolKind(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, ".7z"):
		return "7z"
	case strings.HasSuffix(l, ".rar"):
		return "rar"
	}
	return ""
}

// toolTimeout bounds one compress or extract run.
const toolTimeout = 2 * time.Hour

var installMu sync.Mutex

// lookTool returns the first binary found, installing pkgs (in order, the
// first that installs wins) when none is.
func lookTool(bins []string, pkgs []string) (string, error) {
	find := func() string {
		for _, b := range bins {
			if p, err := exec.LookPath(b); err == nil {
				return p
			}
		}
		return ""
	}
	if p := find(); p != "" {
		return p, nil
	}
	installMu.Lock()
	defer installMu.Unlock()
	if p := find(); p != "" {
		return p, nil
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return "", ErrToolMissing
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, pkg := range pkgs {
		cmd := exec.CommandContext(ctx, "apt-get", "install", "-y", "-q", "--no-install-recommends", pkg)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		if cmd.Run() != nil {
			// The package list may be stale on a fresh server.
			up := exec.CommandContext(ctx, "apt-get", "update", "-q")
			up.Env = cmd.Env
			_ = up.Run()
			again := exec.CommandContext(ctx, "apt-get", "install", "-y", "-q", "--no-install-recommends", pkg)
			again.Env = cmd.Env
			_ = again.Run()
		}
		if p := find(); p != "" {
			return p, nil
		}
	}
	return "", ErrToolMissing
}

func sevenZip() (string, error) {
	return lookTool([]string{"7zz", "7z", "7za"}, []string{"7zip", "p7zip-full"})
}
func rarTool() (string, error) { return lookTool([]string{"rar"}, []string{"rar"}) }

func runTool(dir, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return out, fmt.Errorf("%w: %s: %v %s", ErrArchiveTool, filepath.Base(bin), err, msg)
	}
	return out, nil
}

// compressTool packs names (relative to absBase) into absDest.
func compressTool(kind, absBase, absDest string, names []string) error {
	switch kind {
	case "7z":
		bin, err := sevenZip()
		if err != nil {
			return err
		}
		_, err = runTool(absBase, bin, append([]string{"a", "-t7z", "-y", "-bd", "--", absDest}, names...)...)
		return err
	case "rar":
		bin, err := rarTool()
		if err != nil {
			return err
		}
		// -ol stores symlinks as links instead of following them.
		_, err = runTool(absBase, bin, append([]string{"a", "-r", "-ol", "-idq", "-y", "--", absDest}, names...)...)
		return err
	}
	return ErrBadFormat
}

type toolEntry struct {
	name string
	dir  bool
	link bool
}

// listTool reads the archive's entries.
func listTool(kind, absArchive string) ([]toolEntry, error) {
	var out []byte
	var err error
	switch kind {
	case "7z":
		bin, e := sevenZip()
		if e != nil {
			return nil, e
		}
		out, err = runTool(filepath.Dir(absArchive), bin, "l", "-slt", "-ba", "--", absArchive)
		if err != nil {
			return nil, ErrBadFormat
		}
		var list []toolEntry
		for _, e := range parse7zList(out) {
			if e.name != absArchive { // the archive's own block, when the tool prints one
				list = append(list, e)
			}
		}
		return list, nil
	case "rar":
		bin, e := rarTool()
		if e != nil {
			return nil, e
		}
		out, err = runTool(filepath.Dir(absArchive), bin, "lt", "-idq", "--", absArchive)
		if err != nil {
			return nil, ErrBadFormat
		}
		return parseRarList(out), nil
	}
	return nil, ErrBadFormat
}

// parse7zList reads `7z l -slt -ba`: blocks of "Key = value" lines.
func parse7zList(out []byte) []toolEntry {
	var list []toolEntry
	var cur *toolEntry
	flush := func() {
		if cur != nil && cur.name != "" {
			list = append(list, *cur)
		}
		cur = nil
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " = ")
		if !ok {
			if strings.TrimSpace(sc.Text()) == "" {
				flush()
			}
			continue
		}
		switch k {
		case "Path":
			flush()
			cur = &toolEntry{name: v}
		case "Folder":
			if cur != nil {
				cur.dir = v == "+"
			}
		case "Attributes":
			// "A_ -rw-r--r--", "D_ drwxr-xr-x", "A_ lrwxrwxrwx" (unix mode after the DOS flags).
			if cur != nil {
				for _, f := range strings.Fields(v) {
					if len(f) == 10 && f[0] == 'l' {
						cur.link = true
					}
					if len(f) == 10 && f[0] == 'd' {
						cur.dir = true
					}
				}
			}
		case "Symbolic Link", "Link", "Hard Link":
			if cur != nil && v != "" {
				cur.link = true
			}
		}
	}
	flush()
	return list
}

// parseRarList reads `rar lt`: "Name: x" / "Type: File|Directory|Symlink…".
func parseRarList(out []byte) []toolEntry {
	var list []toolEntry
	var cur *toolEntry
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), ": ")
		if !ok {
			continue
		}
		switch k {
		case "Name":
			if cur != nil {
				list = append(list, *cur)
			}
			cur = &toolEntry{name: v}
		case "Type":
			if cur != nil {
				t := strings.ToLower(v)
				cur.dir = strings.Contains(t, "directory")
				cur.link = strings.Contains(t, "link")
			}
		case "Target":
			if cur != nil && v != "" {
				cur.link = true
			}
		}
	}
	if cur != nil {
		list = append(list, *cur)
	}
	return list
}

// checkEntries refuses archives with links or names that leave the target.
func checkEntries(list []toolEntry) error {
	for _, e := range list {
		if e.link {
			return ErrArchiveLink
		}
		if _, err := safeName(e.name); err != nil {
			return err
		}
	}
	return nil
}

// extractTool unpacks absArchive into dst (an os.Root at absDest).
func extractTool(kind, absArchive, absDest string, dst *os.Root, skipExisting bool) error {
	list, err := listTool(kind, absArchive)
	if err != nil {
		return err
	}
	if err := checkEntries(list); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(absDest, ".lmcwhm-extract-")
	if err != nil {
		return mapErr(err)
	}
	defer os.RemoveAll(tmp)
	switch kind {
	case "7z":
		bin, err := sevenZip()
		if err != nil {
			return err
		}
		if _, err := runTool(tmp, bin, "x", "-y", "-bd", "-o"+tmp, "--", absArchive); err != nil {
			return err
		}
	case "rar":
		bin, err := rarTool()
		if err != nil {
			return err
		}
		if _, err := runTool(tmp, bin, "x", "-o+", "-idq", "-y", "--", absArchive, tmp+"/"); err != nil {
			return err
		}
	}
	tmpRel := filepath.Base(tmp)
	return filepath.WalkDir(tmp, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == tmp {
			return err
		}
		r, err := filepath.Rel(tmp, p)
		if err != nil {
			return err
		}
		name, err := safeName(filepath.ToSlash(r))
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if fi, err := dst.Lstat(name); err == nil && !fi.IsDir() {
				return ErrExists
			}
			return mapErr(dst.MkdirAll(name, 0o755))
		case d.Type().IsRegular():
			if fi, err := dst.Lstat(name); err == nil {
				if skipExisting || fi.IsDir() {
					return nil
				}
				if err := dst.Remove(name); err != nil {
					return mapErr(err)
				}
			}
			return mapErr(dst.Rename(path.Join(tmpRel, name), name))
		}
		return nil // links, devices, fifos are skipped
	})
}
