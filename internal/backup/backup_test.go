package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secrets.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	return New(st, box, home)
}

func write(t *testing.T, p, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestTarRoundTripAndExclude(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "index.php"), "<?php echo 1;")
	write(t, filepath.Join(src, "wp-content/uploads/a.jpg"), "jpg")
	write(t, filepath.Join(src, "node_modules/x/y.js"), "skip me")
	write(t, filepath.Join(src, "cache.log"), "skip me too")
	os.Symlink("index.php", filepath.Join(src, "link.php"))

	var buf bytes.Buffer
	if err := writeTarGz(context.Background(), &buf, []tarSource{{dir: src, prefix: "."}}, []string{"node_modules", "*.log"}); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "site")
	write(t, filepath.Join(dst, "old.txt"), "replaced")
	if err := swapRestore(bytes.NewReader(buf.Bytes()), dst, -1, -1); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "wp-content/uploads/a.jpg")); string(b) != "jpg" {
		t.Fatalf("nested file: %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(dst, "index.php")); fi == nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode not kept: %v", fi)
	}
	if l, _ := os.Readlink(filepath.Join(dst, "link.php")); l != "index.php" {
		t.Fatalf("symlink: %q", l)
	}
	for _, gone := range []string{"node_modules", "cache.log", "old.txt"} {
		if _, err := os.Stat(filepath.Join(dst, gone)); err == nil {
			t.Fatalf("%s should not exist", gone)
		}
	}
	// No leftovers next to the restored directory.
	entries, _ := os.ReadDir(filepath.Dir(dst))
	if len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func evilArchive(name string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 4, Typeflag: tar.TypeReg})
	tw.Write([]byte("evil"))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractRejectsEscape(t *testing.T) {
	base := t.TempDir()
	dst := filepath.Join(base, "site")
	os.Mkdir(dst, 0o755)
	if err := extractTarGz(bytes.NewReader(evilArchive("../escape.txt")), dst, -1, -1); err == nil {
		t.Fatal("../ entry accepted")
	}
	// A symlink pointing out followed by a write through it must fail too.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "out", Linkname: base, Typeflag: tar.TypeSymlink})
	tw.WriteHeader(&tar.Header{Name: "out/pwned.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	extractTarGz(&buf, dst, -1, -1)
	if _, err := os.Stat(filepath.Join(base, "pwned.txt")); err == nil {
		t.Fatal("wrote through a symlink outside the destination")
	}
	if _, err := os.Stat(filepath.Join(base, "escape.txt")); err == nil {
		t.Fatal("escaped")
	}
}

func TestPanelBackupRestore(t *testing.T) {
	sv := newSvc(t)
	ctx := context.Background()
	sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES('probe', 'before')`)
	write(t, filepath.Join(sv.Home, "data", "master.key"), strings.Repeat("k", 32))
	write(t, filepath.Join(sv.Home, "edge", "haproxy.cfg"), "global")
	write(t, filepath.Join(sv.Home, "logs", "panel.log"), "not included")

	var buf bytes.Buffer
	if err := sv.writePanel(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	write(t, filepath.Join(home, "data", "panel.db"), "old db")
	if err := RestorePanel(home, &buf); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(home, "data", "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var v string
	if err := st.DB.QueryRow(`SELECT value FROM settings WHERE key = 'probe'`).Scan(&v); err != nil || v != "before" {
		t.Fatalf("restored db: %q %v", v, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "edge", "haproxy.cfg")); string(b) != "global" {
		t.Fatal("edge dir not restored")
	}
	if _, err := os.Stat(filepath.Join(home, "logs", "panel.log")); err == nil {
		t.Fatal("logs should not be in a panel backup")
	}
	old, _ := filepath.Glob(filepath.Join(home, "data", "panel.db.before-restore-*"))
	if len(old) != 1 {
		t.Fatal("previous database not kept")
	}
}

func TestPathPlanRunPruneRestore(t *testing.T) {
	sv := newSvc(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data")
	write(t, filepath.Join(dir, "a.txt"), "v1")
	if _, err := sv.ParsePlan(ctx, `{"kind":"path","target":"/","dests":["local"]}`); err == nil {
		t.Fatal("/ accepted")
	}
	if _, err := sv.ParsePlan(ctx, `{"kind":"path","target":"`+dir+`","dests":["remote:9"]}`); err == nil {
		t.Fatal("unknown remote accepted")
	}
	plan := `{"kind":"path","target":"` + dir + `","dests":["local"],"keep":2}`
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sv.now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	for i := 0; i < 3; i++ {
		if err := sv.RunPlan(ctx, plan, 7, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	recs, _ := sv.List(ctx, KindPath)
	if len(recs) != 2 {
		t.Fatalf("keep=2 left %d", len(recs))
	}
	files, _ := filepath.Glob(filepath.Join(sv.Dir, "path", "*", "*.tar.gz"))
	if len(files) != 2 {
		t.Fatalf("files on disk: %v", files)
	}
	write(t, filepath.Join(dir, "a.txt"), "v2")
	if err := sv.Restore(ctx, recs[0].ID, 0, t.Logf); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "v1" {
		t.Fatalf("restored %q", b)
	}
	if err := sv.Delete(ctx, recs[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(recs[1].Location); err == nil {
		t.Fatal("file not deleted")
	}
}

// fakeCloud imitates the device flow, a resumable upload session that fails
// once mid-way, download and delete for both providers.
type fakeCloud struct {
	mu        sync.Mutex
	approved  bool
	files     map[string][]byte
	uploading []byte
	failOnce  bool
	refreshes int
}

func (f *fakeCloud) handler(t *testing.T, srv **httptest.Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/device/code") || strings.HasSuffix(p, "/devicecode"):
			json.NewEncoder(w).Encode(map[string]any{"device_code": "dev", "user_code": "ABCD-EFGH", "verification_url": "https://example.test/device", "interval": 1})
		case strings.HasSuffix(p, "/token"):
			r.ParseForm()
			if r.Form.Get("grant_type") == "refresh_token" {
				f.refreshes++
				json.NewEncoder(w).Encode(map[string]any{"access_token": "acc2", "expires_in": 3600})
				return
			}
			if !f.approved {
				json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "acc", "refresh_token": "ref", "expires_in": 1})
		case r.Method == http.MethodPost && p == "/drive/v3/files":
			json.NewEncoder(w).Encode(map[string]any{"id": "folder1"})
		case r.Method == http.MethodPost && p == "/upload/drive/v3/files",
			strings.HasSuffix(p, ":/createUploadSession"):
			f.uploading = nil
			loc := (*srv).URL + "/session"
			if strings.HasSuffix(p, "createUploadSession") {
				json.NewEncoder(w).Encode(map[string]any{"uploadUrl": loc})
				return
			}
			w.Header().Set("Location", loc)
		case p == "/session" && r.Method == http.MethodGet: // OneDrive status
			json.NewEncoder(w).Encode(map[string]any{"nextExpectedRanges": []string{fmt.Sprintf("%d-", len(f.uploading))}})
		case p == "/session":
			cr := r.Header.Get("Content-Range")
			body, _ := io.ReadAll(r.Body)
			if strings.HasPrefix(cr, "bytes */") { // Google status query
				if len(f.uploading) > 0 {
					w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(f.uploading)-1))
				}
				w.WriteHeader(308)
				return
			}
			var a, b, total int
			fmt.Sscanf(cr, "bytes %d-%d/%d", &a, &b, &total)
			if a != len(f.uploading) {
				t.Errorf("chunk starts at %d, have %d", a, len(f.uploading))
			}
			if f.failOnce && a > 0 {
				f.failOnce = false
				// Keep half of the chunk, then fail.
				f.uploading = append(f.uploading, body[:len(body)/2]...)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			f.uploading = append(f.uploading, body...)
			if len(f.uploading) < total {
				w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(f.uploading)-1))
				w.WriteHeader(308)
				return
			}
			id := "file" + strconv.Itoa(len(f.files)+1)
			f.files[id] = f.uploading
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"id": id})
		case r.Method == http.MethodDelete:
			delete(f.files, p[strings.LastIndex(p, "/")+1:])
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && (r.URL.Query().Get("alt") == "media" || strings.HasSuffix(p, "/content")):
			id := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(p, "/drive/v3/files/"), "/me/drive/items/"), "/content")
			b, ok := f.files[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Write(b)
		case p == "/drive/v3/about" || p == "/me":
			json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"emailAddress": "a@example.test"}, "mail": "a@example.test"})
		default:
			t.Errorf("unexpected %s %s", r.Method, p)
			http.NotFound(w, r)
		}
	})
}

func TestRemotes(t *testing.T) {
	for _, kind := range []string{KindGDrive, KindOneDrive} {
		t.Run(kind, func(t *testing.T) {
			fc := &fakeCloud{files: map[string][]byte{}, failOnce: true}
			var srv *httptest.Server
			srv = httptest.NewServer(fc.handler(t, &srv))
			defer srv.Close()
			googleDeviceURL, googleTokenURL, googleAPI = srv.URL+"/device/code", srv.URL+"/token", srv.URL
			msLogin, msGraph = srv.URL, srv.URL
			gdriveChunk, onedriveChunk = 256<<10, 320<<10

			sv := newSvc(t)
			ctx := context.Background()
			v, err := sv.AddRemote(ctx, RemoteInput{Name: "cloud", Kind: kind, ClientID: "cid", ClientSecret: "sec"})
			if err != nil {
				t.Fatal(err)
			}
			if v.Device == nil || v.Device.UserCode != "ABCD-EFGH" || v.Authorized {
				t.Fatalf("device: %+v", v)
			}
			if _, err := sv.PollRemote(ctx, v.ID); err != ErrAuthPending {
				t.Fatalf("want pending, got %v", err)
			}
			fc.approved = true
			if v, err = sv.PollRemote(ctx, v.ID); err != nil || !v.Authorized || v.Account != "a@example.test" {
				t.Fatalf("authorise: %+v %v", v, err)
			}

			dir := filepath.Join(t.TempDir(), "big")
			// Incompressible content spanning several chunks.
			data := make([]byte, 1<<20)
			for i := range data {
				data[i] = byte((i*7919 + i/13) ^ (i >> 5))
			}
			write(t, filepath.Join(dir, "blob.bin"), string(data))
			plan := fmt.Sprintf(`{"kind":"path","target":%q,"dests":["remote:%d"],"keep":1}`, dir, v.ID)
			var out bytes.Buffer
			if err := sv.RunPlan(ctx, plan, 0, &out); err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}
			if fc.refreshes == 0 {
				t.Error("expired access token was not refreshed")
			}
			recs, _ := sv.List(ctx, "")
			if len(recs) != 1 || recs[0].DestName != "cloud" || fc.files[recs[0].Location] == nil {
				t.Fatalf("records %+v files %d", recs, len(fc.files))
			}
			if int64(len(fc.files[recs[0].Location])) != recs[0].Size {
				t.Fatalf("uploaded %d of %d bytes", len(fc.files[recs[0].Location]), recs[0].Size)
			}
			os.RemoveAll(dir)
			if err := sv.Restore(ctx, recs[0].ID, 0, t.Logf); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(dir, "blob.bin")); !bytes.Equal(b, data) {
				t.Fatal("restored content differs")
			}
			// keep=1: the second run replaces the first copy on the remote.
			if err := sv.RunPlan(ctx, plan, 0, io.Discard); err != nil {
				t.Fatal(err)
			}
			if len(fc.files) != 1 {
				t.Fatalf("remote holds %d files, want 1", len(fc.files))
			}
		})
	}
}
