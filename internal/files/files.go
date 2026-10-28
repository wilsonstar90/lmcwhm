// Package files is the file manager. Every operation goes through os.Root,
// so a path (including one reached through a symlink or an archive entry)
// can never leave the configured root.
package files

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrBadPath   = errors.New("files.invalid_path")
	ErrExists    = errors.New("files.exists")
	ErrNotFound  = errors.New("files.not_found")
	ErrTooLarge  = errors.New("files.too_large")
	ErrBinary    = errors.New("files.binary")
	ErrIsDir     = errors.New("files.is_dir")
	ErrBadMode   = errors.New("files.invalid_mode")
	ErrBadOwner  = errors.New("files.invalid_owner")
	ErrBadFormat = errors.New("files.invalid_format")
	ErrCrossDev  = errors.New("files.trash_cross_device")
)

// MaxEdit is the largest file the editor opens.
const MaxEdit = 5 << 20

type FS struct {
	// Root confines every path ("/" on a server: the panel admin is root).
	Root string
	// Trash is the recycle bin, a path inside Root.
	Trash string
}

// New returns the manager for root (default "/"), with the recycle bin at
// /www/.Recycle_bin like aaPanel.
func New(root string) *FS {
	if root == "" {
		root = "/"
	}
	return &FS{Root: root, Trash: "/www/.Recycle_bin"}
}

// rel turns a panel path ("/www/wwwroot/a") into a name relative to Root.
func rel(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", ErrBadPath
	}
	c := path.Clean("/" + p)
	if c == "/" {
		return ".", nil
	}
	return strings.TrimPrefix(c, "/"), nil
}

// Clean normalises a panel path.
func Clean(p string) string { return path.Clean("/" + p) }

func (f *FS) open() (*os.Root, error) { return os.OpenRoot(f.Root) }

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrExist):
		return ErrExists
	case strings.Contains(err.Error(), "path escapes"):
		return ErrBadPath
	}
	return err
}

// ---- listing ----

type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"` // octal, e.g. "755"
	ModTime int64  `json:"mtime"`
	Owner   string `json:"owner"`
	Group   string `json:"group"`
	Link    string `json:"link,omitempty"` // symlink target
}

var (
	idMu       sync.Mutex
	userCache  = map[uint32]string{}
	groupCache = map[uint32]string{}
)

func owner(fi fs.FileInfo) (string, string) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}
	idMu.Lock()
	defer idMu.Unlock()
	uid, gid := st.Uid, st.Gid
	u, ok := userCache[uid]
	if !ok {
		u = strconv.Itoa(int(uid))
		if x, err := user.LookupId(u); err == nil {
			u = x.Username
		}
		userCache[uid] = u
	}
	g, ok := groupCache[gid]
	if !ok {
		g = strconv.Itoa(int(gid))
		if x, err := user.LookupGroupId(g); err == nil {
			g = x.Name
		}
		groupCache[gid] = g
	}
	return u, g
}

func entry(dir string, fi fs.FileInfo) Entry {
	e := Entry{
		Name: fi.Name(), Path: path.Join(dir, fi.Name()), IsDir: fi.IsDir(), Size: fi.Size(),
		Mode: fmt.Sprintf("%o", fi.Mode().Perm()), ModTime: fi.ModTime().Unix(),
	}
	e.Owner, e.Group = owner(fi)
	return e
}

// List returns a directory's entries, directories first.
func (f *FS) List(dir string) ([]Entry, error) {
	r, err := rel(dir)
	if err != nil {
		return nil, err
	}
	root, err := f.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	d, err := root.Open(r)
	if err != nil {
		return nil, mapErr(err)
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return nil, ErrIsDir
	}
	dir = Clean(dir)
	out := make([]Entry, 0, len(names))
	for _, n := range names {
		p := path.Join(r, n)
		fi, err := root.Lstat(p)
		if err != nil {
			continue
		}
		e := entry(dir, fi)
		if fi.Mode()&fs.ModeSymlink != 0 {
			e.Link, _ = root.Readlink(p)
			if st, err := root.Stat(p); err == nil {
				e.IsDir = st.IsDir()
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (f *FS) Stat(p string) (Entry, error) {
	r, err := rel(p)
	if err != nil {
		return Entry{}, err
	}
	root, err := f.open()
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	fi, err := root.Stat(r)
	if err != nil {
		return Entry{}, mapErr(err)
	}
	return entry(path.Dir(Clean(p)), fi), nil
}

// ---- reading / writing ----

func (f *FS) ReadText(p string) (string, error) {
	r, err := rel(p)
	if err != nil {
		return "", err
	}
	root, err := f.open()
	if err != nil {
		return "", err
	}
	defer root.Close()
	fi, err := root.Stat(r)
	if err != nil {
		return "", mapErr(err)
	}
	if fi.IsDir() {
		return "", ErrIsDir
	}
	if fi.Size() > MaxEdit {
		return "", ErrTooLarge
	}
	b, err := root.ReadFile(r)
	if err != nil {
		return "", mapErr(err)
	}
	head := b[:min(len(b), 8000)]
	for _, c := range head {
		if c == 0 {
			return "", ErrBinary
		}
	}
	return string(b), nil
}

// WriteText saves a file, keeping the mode of an existing one.
func (f *FS) WriteText(p, body string) error {
	r, err := rel(p)
	if err != nil || r == "." {
		return ErrBadPath
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	mode := fs.FileMode(0o644)
	if fi, err := root.Stat(r); err == nil {
		if fi.IsDir() {
			return ErrIsDir
		}
		mode = fi.Mode().Perm()
	}
	return mapErr(root.WriteFile(r, []byte(body), mode))
}

// Create makes an empty file or a directory; it fails when the name exists.
func (f *FS) Create(p string, dir bool) error {
	r, err := rel(p)
	if err != nil || r == "." {
		return ErrBadPath
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if dir {
		return mapErr(root.Mkdir(r, 0o755))
	}
	fh, err := root.OpenFile(r, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return mapErr(err)
	}
	return fh.Close()
}

// Open returns a file for download.
func (f *FS) Open(p string) (*os.File, fs.FileInfo, error) {
	r, err := rel(p)
	if err != nil {
		return nil, nil, err
	}
	root, err := f.open()
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	fh, err := root.Open(r)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	fi, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, nil, err
	}
	if fi.IsDir() {
		fh.Close()
		return nil, nil, ErrIsDir
	}
	return fh, fi, nil
}

// WriteChunk writes one upload chunk at offset (offset 0 truncates), so a
// large upload can resume from the size already on disk.
func (f *FS) WriteChunk(p string, offset int64, body io.Reader) (int64, error) {
	r, err := rel(p)
	if err != nil || r == "." || offset < 0 {
		return 0, ErrBadPath
	}
	root, err := f.open()
	if err != nil {
		return 0, err
	}
	defer root.Close()
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	fh, err := root.OpenFile(r, flags, 0o644)
	if err != nil {
		return 0, mapErr(err)
	}
	defer fh.Close()
	if _, err := fh.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.Copy(fh, body)
	return offset + n, err
}

// ---- rename / copy / delete ----

func (f *FS) Rename(from, to string) error {
	a, err := rel(from)
	if err != nil || a == "." {
		return ErrBadPath
	}
	b, err := rel(to)
	if err != nil || b == "." {
		return ErrBadPath
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(b); err == nil {
		return ErrExists
	}
	return mapErr(root.Rename(a, b))
}

// Copy copies a file or directory tree (symlinks are recreated, not followed).
func (f *FS) Copy(from, to string) error {
	a, err := rel(from)
	if err != nil || a == "." {
		return ErrBadPath
	}
	b, err := rel(to)
	if err != nil || b == "." {
		return ErrBadPath
	}
	if b == a || strings.HasPrefix(b, a+"/") {
		return ErrBadPath // into itself
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(b); err == nil {
		return ErrExists
	}
	return mapErr(copyTree(root, a, b))
}

func copyTree(root *os.Root, src, dst string) error {
	fi, err := root.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := root.Readlink(src)
		if err != nil {
			return err
		}
		return root.Symlink(target, dst)
	case fi.IsDir():
		if err := root.Mkdir(dst, fi.Mode().Perm()); err != nil {
			return err
		}
		d, err := root.Open(src)
		if err != nil {
			return err
		}
		names, err := d.Readdirnames(-1)
		d.Close()
		if err != nil {
			return err
		}
		for _, n := range names {
			if err := copyTree(root, path.Join(src, n), path.Join(dst, n)); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := root.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := root.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Delete removes paths permanently, or moves them to the recycle bin.
func (f *FS) Delete(paths []string, permanent bool) error {
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	trash, _ := rel(f.Trash)
	if !permanent {
		if err := root.MkdirAll(trash, 0o700); err != nil {
			return mapErr(err)
		}
	}
	for _, p := range paths {
		r, err := rel(p)
		if err != nil || r == "." {
			return ErrBadPath
		}
		if r == trash || strings.HasPrefix(r, trash+"/") {
			permanent = true // deleting inside the bin empties it
		}
		if permanent {
			if err := root.RemoveAll(r); err != nil {
				return mapErr(err)
			}
			continue
		}
		name := fmt.Sprintf("%d_%s", time.Now().UnixNano(), base64.RawURLEncoding.EncodeToString([]byte(Clean(p))))
		if err := root.Rename(r, path.Join(trash, name)); err != nil {
			if errors.Is(err, syscall.EXDEV) {
				return ErrCrossDev
			}
			return mapErr(err)
		}
	}
	return nil
}

type TrashItem struct {
	ID        string `json:"id"`
	Path      string `json:"path"` // original location
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size"`
	DeletedAt int64  `json:"deleted_at"`
}

func (f *FS) TrashList() ([]TrashItem, error) {
	entries, err := f.List(f.Trash)
	if errors.Is(err, ErrNotFound) {
		return []TrashItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []TrashItem{}
	for _, e := range entries {
		ts, enc, ok := strings.Cut(e.Name, "_")
		orig, err := base64.RawURLEncoding.DecodeString(enc)
		if !ok || err != nil {
			continue
		}
		n, _ := strconv.ParseInt(ts, 10, 64)
		out = append(out, TrashItem{ID: e.Name, Path: string(orig), IsDir: e.IsDir, Size: e.Size, DeletedAt: n / int64(time.Second)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt > out[j].DeletedAt })
	return out, nil
}

func (f *FS) trashItem(id string) (string, string, error) {
	if strings.ContainsAny(id, "/\x00") || id == "." || id == ".." {
		return "", "", ErrBadPath
	}
	_, enc, ok := strings.Cut(id, "_")
	orig, err := base64.RawURLEncoding.DecodeString(enc)
	if !ok || err != nil {
		return "", "", ErrBadPath
	}
	return path.Join(f.Trash, id), string(orig), nil
}

func (f *FS) Restore(id string) error {
	src, orig, err := f.trashItem(id)
	if err != nil {
		return err
	}
	return f.Rename(src, orig)
}

// EmptyTrash removes one item, or everything when id is "".
func (f *FS) EmptyTrash(id string) error {
	if id != "" {
		p, _, err := f.trashItem(id)
		if err != nil {
			return err
		}
		return f.Delete([]string{p}, true)
	}
	items, err := f.TrashList()
	if err != nil {
		return err
	}
	for _, it := range items {
		if err := f.Delete([]string{path.Join(f.Trash, it.ID)}, true); err != nil {
			return err
		}
	}
	return nil
}

// ---- permissions ----

func walk(root *os.Root, r string, recursive bool, fn func(string) error) error {
	if err := fn(r); err != nil {
		return err
	}
	if !recursive {
		return nil
	}
	fi, err := root.Lstat(r)
	if err != nil || !fi.IsDir() {
		return err
	}
	d, err := root.Open(r)
	if err != nil {
		return err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := walk(root, path.Join(r, n), true, fn); err != nil {
			return err
		}
	}
	return nil
}

func (f *FS) Chmod(p, mode string, recursive bool) error {
	m, err := strconv.ParseUint(mode, 8, 32)
	if err != nil || m > 0o7777 {
		return ErrBadMode
	}
	fm := fileMode(uint32(m))
	r, err := rel(p)
	if err != nil {
		return err
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	return mapErr(walk(root, r, recursive, func(x string) error {
		if fi, err := root.Lstat(x); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return nil // chmod would follow the link
		}
		return root.Chmod(x, fm)
	}))
}

// fileMode converts Unix mode bits to an fs.FileMode, whose setuid, setgid and
// sticky flags are not the Unix bit positions (fs.FileMode(0o4755) drops setuid).
func fileMode(m uint32) fs.FileMode {
	fm := fs.FileMode(m & 0o777)
	if m&0o4000 != 0 {
		fm |= fs.ModeSetuid
	}
	if m&0o2000 != 0 {
		fm |= fs.ModeSetgid
	}
	if m&0o1000 != 0 {
		fm |= fs.ModeSticky
	}
	return fm
}

func lookupID(name string, group bool) (int, error) {
	if n, err := strconv.Atoi(name); err == nil && n >= 0 {
		return n, nil
	}
	var id string
	if group {
		g, err := user.LookupGroup(name)
		if err != nil {
			return 0, ErrBadOwner
		}
		id = g.Gid
	} else {
		u, err := user.Lookup(name)
		if err != nil {
			return 0, ErrBadOwner
		}
		id = u.Uid
	}
	return strconv.Atoi(id)
}

func (f *FS) Chown(p, owner, group string, recursive bool) error {
	uid, err := lookupID(owner, false)
	if err != nil {
		return err
	}
	gid, err := lookupID(group, true)
	if err != nil {
		return err
	}
	r, err := rel(p)
	if err != nil {
		return err
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	return mapErr(walk(root, r, recursive, func(x string) error { return root.Lchown(x, uid, gid) }))
}
