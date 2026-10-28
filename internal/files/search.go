package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

// Hit is one search result; Line/Text are set for content matches.
type Hit struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
	Line  int    `json:"line,omitempty"`
	Text  string `json:"text,omitempty"`
}

// SearchInput searches names or text contents below Dir.
type SearchInput struct {
	Dir     string
	Query   string
	Content bool   // search inside files instead of names
	Ext     string // only files with this extension (content search)
	Limit   int
}

var ErrBadQuery = errors.New("files.invalid_query")

// Search walks Dir (through os.Root) for matching names or contents. Binary
// files and files over 2 MB are skipped by the content search.
func (f *FS) Search(ctx context.Context, in SearchInput) ([]Hit, bool, error) {
	q := strings.TrimSpace(in.Query)
	if len(q) < 2 || len(q) > 200 {
		return nil, false, ErrBadQuery
	}
	if in.Limit <= 0 || in.Limit > 1000 {
		in.Limit = 500
	}
	root, err := f.open()
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	start, err := rel(in.Dir)
	if err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lq := strings.ToLower(q)
	ext := strings.ToLower(strings.TrimPrefix(in.Ext, "."))
	hits := []Hit{}
	truncated := false
	walkErr := fs.WalkDir(root.FS(), start, func(name string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			truncated = true
			return fs.SkipAll
		}
		if err != nil {
			return nil
		}
		if d.IsDir() && name != start {
			switch d.Name() {
			case "proc", "sys", "dev", "run", ".Recycle_bin", "node_modules", ".git":
				return fs.SkipDir
			}
		}
		full := "/" + strings.TrimPrefix(name, ".")
		full = path.Clean(full)
		if !in.Content {
			if name != start && strings.Contains(strings.ToLower(d.Name()), lq) {
				h := Hit{Path: full, IsDir: d.IsDir()}
				if fi, err := d.Info(); err == nil && !d.IsDir() {
					h.Size = fi.Size()
				}
				hits = append(hits, h)
			}
		} else if d.Type().IsRegular() && (ext == "" || strings.EqualFold(path.Ext(d.Name()), "."+ext)) {
			if fi, err := d.Info(); err == nil && fi.Size() <= 2<<20 {
				if line, text, ok := grepFile(root.FS(), name, q); ok {
					hits = append(hits, Hit{Path: full, Size: fi.Size(), Line: line, Text: text})
				}
			}
		}
		if len(hits) >= in.Limit {
			truncated = true
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return hits, truncated, mapErr(walkErr)
	}
	return hits, truncated, nil
}

func grepFile(fsys fs.FS, name, q string) (int, string, bool) {
	fh, err := fsys.Open(name)
	if err != nil {
		return 0, "", false
	}
	b, err := io.ReadAll(io.LimitReader(fh, 2<<20))
	fh.Close()
	if err != nil || bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return 0, "", false
	}
	i := bytes.Index(bytes.ToLower(b), bytes.ToLower([]byte(q)))
	if i < 0 {
		return 0, "", false
	}
	line := bytes.Count(b[:i], []byte("\n")) + 1
	ls := bytes.LastIndexByte(b[:i], '\n') + 1
	le := bytes.IndexByte(b[i:], '\n')
	if le < 0 {
		le = len(b)
	} else {
		le += i
	}
	text := strings.TrimSpace(strings.ToValidUTF8(string(b[ls:min(le, ls+300)]), "?"))
	return line, text, true
}

// Fetch downloads rawURL into dir (remote download / 遠程下載); name defaults
// to the URL's last path element. progress receives the bytes written.
func (f *FS) Fetch(ctx context.Context, rawURL, dir, name string, progress func(n, total int64)) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", ErrBadQuery
	}
	if name == "" {
		name = path.Base(u.Path)
	}
	if name == "" || name == "/" || name == "." || strings.ContainsAny(name, "/\\\x00") {
		name = "download"
	}
	dest := path.Join(Clean(dir), name)
	r, err := rel(dest)
	if err != nil {
		return "", err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "lmcwhm")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	root, err := f.open()
	if err != nil {
		return "", err
	}
	defer root.Close()
	if _, err := root.Stat(r); err == nil {
		return "", ErrExists
	}
	tmp := r + ".part"
	out, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", mapErr(err)
	}
	var n int64
	buf := make([]byte, 256<<10)
	last := time.Now()
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			if _, err := out.Write(buf[:k]); err != nil {
				out.Close()
				root.Remove(tmp)
				return "", err
			}
			n += int64(k)
			if progress != nil && time.Since(last) > time.Second {
				progress(n, resp.ContentLength)
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			root.Remove(tmp)
			return "", rerr
		}
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if progress != nil {
		progress(n, resp.ContentLength)
	}
	return dest, mapErr(root.Rename(tmp, r))
}
