package files

// Name conflicts on paste, move, copy and extract (like aaPanel): the panel
// lists what already exists, and the user overwrites or skips it.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// maxConflicts caps the names a conflict check returns.
const maxConflicts = 200

// Conflicts returns the paths among tos that already exist.
func (f *FS) Conflicts(tos []string) ([]string, error) {
	root, err := f.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	out := []string{}
	for _, to := range tos {
		b, err := rel(to)
		if err != nil || b == "." {
			return nil, ErrBadPath
		}
		if _, err := root.Lstat(b); err == nil {
			out = append(out, Clean(to))
			if len(out) >= maxConflicts {
				break
			}
		}
	}
	return out, nil
}

// MoveOver moves from to to, replacing what is there: a directory onto a
// directory merges (its files replace those of the same name).
func (f *FS) MoveOver(from, to string) error {
	a, b, root, err := f.pair(from, to)
	if err != nil {
		return err
	}
	defer root.Close()
	return mapErr(moveOver(root, a, b))
}

func moveOver(root *os.Root, a, b string) error {
	src, err := root.Lstat(a)
	if err != nil {
		return err
	}
	dst, err := root.Lstat(b)
	if errors.Is(err, fs.ErrNotExist) {
		return root.Rename(a, b)
	}
	if err != nil {
		return err
	}
	if src.IsDir() && dst.IsDir() {
		names, err := readNames(root, a)
		if err != nil {
			return err
		}
		for _, n := range names {
			if err := moveOver(root, path.Join(a, n), path.Join(b, n)); err != nil {
				return err
			}
		}
		return root.Remove(a)
	}
	if err := root.RemoveAll(b); err != nil {
		return err
	}
	return root.Rename(a, b)
}

// CopyOver copies from to to, replacing what is there; a directory onto a
// directory merges.
func (f *FS) CopyOver(from, to string) error {
	a, b, root, err := f.pair(from, to)
	if err != nil {
		return err
	}
	defer root.Close()
	return mapErr(copyOver(root, a, b))
}

func copyOver(root *os.Root, a, b string) error {
	src, err := root.Lstat(a)
	if err != nil {
		return err
	}
	dst, err := root.Lstat(b)
	if errors.Is(err, fs.ErrNotExist) {
		return copyTree(root, a, b)
	}
	if err != nil {
		return err
	}
	if src.IsDir() && dst.IsDir() {
		names, err := readNames(root, a)
		if err != nil {
			return err
		}
		for _, n := range names {
			if err := copyOver(root, path.Join(a, n), path.Join(b, n)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := root.RemoveAll(b); err != nil {
		return err
	}
	return copyTree(root, a, b)
}

// pair resolves a source and a destination that may not be the same or nested.
func (f *FS) pair(from, to string) (string, string, *os.Root, error) {
	a, err := rel(from)
	if err != nil || a == "." {
		return "", "", nil, ErrBadPath
	}
	b, err := rel(to)
	if err != nil || b == "." {
		return "", "", nil, ErrBadPath
	}
	if b == a || strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/") {
		return "", "", nil, ErrBadPath // onto itself, into itself, or over its own parent
	}
	root, err := f.open()
	return a, b, root, err
}

func readNames(root *os.Root, dir string) ([]string, error) {
	d, err := root.Open(dir)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.Readdirnames(-1)
}

// ExtractConflicts lists the archive's files that already exist in destDir.
func (f *FS) ExtractConflicts(archive, destDir string) ([]string, error) {
	a, err := rel(archive)
	if err != nil {
		return nil, err
	}
	d, err := rel(destDir)
	if err != nil {
		return nil, err
	}
	root, err := f.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	out := []string{}
	if _, err := root.Lstat(d); err != nil {
		return out, nil // a new directory
	}
	exists := func(name string) bool {
		n, err := safeName(name)
		if err != nil {
			return false
		}
		fi, err := root.Lstat(path.Join(d, n))
		return err == nil && !fi.IsDir()
	}
	add := func(name string) bool {
		if exists(name) {
			out = append(out, Clean("/"+path.Join(d, strings.TrimPrefix(path.Clean(strings.ReplaceAll(name, "\\", "/")), "/"))))
		}
		return len(out) < maxConflicts
	}
	in, err := root.Open(a)
	if err != nil {
		return nil, mapErr(err)
	}
	defer in.Close()
	lower := strings.ToLower(a)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		fi, err := in.Stat()
		if err != nil {
			return nil, err
		}
		zr, err := zip.NewReader(in, fi.Size())
		if err != nil {
			return nil, ErrBadFormat
		}
		for _, zf := range zr.File {
			if !zf.Mode().IsDir() && !add(zf.Name) {
				break
			}
		}
		return out, nil
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"), strings.HasSuffix(lower, ".tar"):
		var r io.Reader = in
		if !strings.HasSuffix(lower, ".tar") {
			gz, err := gzip.NewReader(in)
			if err != nil {
				return nil, ErrBadFormat
			}
			defer gz.Close()
			r = gz
		}
		tr := tar.NewReader(r)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			if err != nil {
				return nil, ErrBadFormat
			}
			if h.Typeflag == tar.TypeReg && !add(h.Name) {
				return out, nil
			}
		}
	}
	return nil, ErrBadFormat
}
