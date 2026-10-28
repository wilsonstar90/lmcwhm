package files

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

// Compress packs paths (all in the same directory) into dest as zip or tar.gz;
// entries are named relative to that directory.
func (f *FS) Compress(paths []string, dest, format string) error {
	if len(paths) == 0 || (format != "zip" && format != "tar.gz") {
		return ErrBadFormat
	}
	d, err := rel(dest)
	if err != nil || d == "." {
		return ErrBadPath
	}
	var srcs []string
	base := ""
	for _, p := range paths {
		r, err := rel(p)
		if err != nil || r == "." {
			return ErrBadPath
		}
		if base == "" {
			base = path.Dir(r)
		} else if path.Dir(r) != base {
			return ErrBadPath
		}
		srcs = append(srcs, r)
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	out, err := root.OpenFile(d, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return mapErr(err)
	}
	if format == "zip" {
		err = writeZip(root, base, srcs, out)
	} else {
		err = writeTarGz(root, base, srcs, out)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		root.Remove(d)
	}
	return mapErr(err)
}

// each walks the trees under srcs (symlinks are not followed).
func each(root *os.Root, srcs []string, fn func(name string, fi fs.FileInfo) error) error {
	for _, s := range srcs {
		if err := walk(root, s, true, func(p string) error {
			fi, err := root.Lstat(p)
			if err != nil {
				return err
			}
			return fn(p, fi)
		}); err != nil {
			return err
		}
	}
	return nil
}

func entryName(base, p string) string {
	if base == "." {
		return p
	}
	return strings.TrimPrefix(p, base+"/")
}

func writeZip(root *os.Root, base string, srcs []string, w io.Writer) error {
	zw := zip.NewWriter(w)
	err := each(root, srcs, func(p string, fi fs.FileInfo) error {
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil
		}
		h, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		h.Name = entryName(base, p)
		if fi.IsDir() {
			h.Name += "/"
		} else {
			h.Method = zip.Deflate
		}
		hw, err := zw.CreateHeader(h)
		if err != nil || fi.IsDir() {
			return err
		}
		in, err := root.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(hw, in)
		return err
	})
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	return err
}

func writeTarGz(root *os.Root, base string, srcs []string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := each(root, srcs, func(p string, fi fs.FileInfo) error {
		link := ""
		if fi.Mode()&fs.ModeSymlink != 0 {
			link, _ = root.Readlink(p)
		}
		h, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return err
		}
		h.Name = entryName(base, p)
		if fi.IsDir() {
			h.Name += "/"
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		in, err := root.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
	if cerr := tw.Close(); err == nil {
		err = cerr
	}
	if cerr := gz.Close(); err == nil {
		err = cerr
	}
	return err
}

// Extract unpacks a .zip / .tar.gz / .tgz / .tar archive into destDir. Writes
// go through an os.Root opened at destDir, so entries like "../../etc/x" or
// paths through planted symlinks are rejected (zip-slip); archive symlinks
// are skipped.
func (f *FS) Extract(archive, destDir string) error {
	a, err := rel(archive)
	if err != nil {
		return err
	}
	d, err := rel(destDir)
	if err != nil {
		return err
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(d, 0o755); err != nil {
		return mapErr(err)
	}
	dst, err := root.OpenRoot(d)
	if err != nil {
		return mapErr(err)
	}
	defer dst.Close()
	in, err := root.Open(a)
	if err != nil {
		return mapErr(err)
	}
	defer in.Close()
	lower := strings.ToLower(a)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		fi, err := in.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(in, fi.Size())
		if err != nil {
			return ErrBadFormat
		}
		return mapErr(extractZip(dst, zr))
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		gz, err := gzip.NewReader(in)
		if err != nil {
			return ErrBadFormat
		}
		defer gz.Close()
		return mapErr(extractTar(dst, tar.NewReader(gz)))
	case strings.HasSuffix(lower, ".tar"):
		return mapErr(extractTar(dst, tar.NewReader(in)))
	}
	return ErrBadFormat
}

// safeName validates an archive entry name before it reaches os.Root.
func safeName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", ErrBadPath
	}
	c := path.Clean(name)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", ErrBadPath
	}
	return c, nil
}

func writeEntry(dst *os.Root, name string, mode fs.FileMode, r io.Reader) error {
	if err := dst.MkdirAll(path.Dir(name), 0o755); err != nil {
		return err
	}
	out, err := dst.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func extractZip(dst *os.Root, zr *zip.Reader) error {
	for _, zf := range zr.File {
		name, err := safeName(zf.Name)
		if err != nil {
			return err
		}
		mode := zf.Mode()
		switch {
		case mode.IsDir():
			if err := dst.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case mode&fs.ModeSymlink != 0:
			continue
		default:
			rc, err := zf.Open()
			if err != nil {
				return err
			}
			err = writeEntry(dst, name, mode, rc)
			rc.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func extractTar(dst *os.Root, tr *tar.Reader) error {
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return ErrBadFormat
		}
		name, err := safeName(h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := dst.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeEntry(dst, name, fs.FileMode(h.Mode), tr); err != nil {
				return err
			}
		}
	}
}
