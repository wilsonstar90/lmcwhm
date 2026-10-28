package cron

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, t.TempDir(), time.UTC)
}

// wait returns the job's last finished run.
func wait(t *testing.T, sv *Service, id int64) Run {
	t.Helper()
	for i := 0; i < 100; i++ {
		runs, _ := sv.Runs(context.Background(), id, 1)
		if len(runs) > 0 && runs[0].Status != "running" {
			r, _ := sv.Run(context.Background(), runs[0].ID)
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return Run{}
}

func TestValidate(t *testing.T) {
	sv := newSvc(t)
	ctx := context.Background()
	for _, in := range []Input{
		{Name: "", Type: "shell", Schedule: "* * * * *", Command: "true"},
		{Name: "x", Type: "shell", Schedule: "61 * * * *", Command: "true"},
		{Name: "x", Type: "nope", Schedule: "@hourly", Command: "true"},
		{Name: "x", Type: "url", Schedule: "@hourly", Command: "ftp://a"},
		{Name: "x", Type: "logrotate", Schedule: "@daily", Command: `{"keep":0}`},
	} {
		if _, err := sv.Create(ctx, in); err == nil {
			t.Errorf("accepted %+v", in)
		}
	}
	next, err := sv.Next("0 3 * * *", 3)
	if err != nil || len(next) != 3 || next[1]-next[0] != 86400 {
		t.Fatalf("Next = %v %v", next, err)
	}
	if n, err := sv.Next("*/10 * * * * *", 2); err != nil || n[1]-n[0] != 10 {
		t.Fatalf("seconds field: %v %v", n, err)
	}
}

func TestShellAndTimeout(t *testing.T) {
	sv := newSvc(t)
	ctx := context.Background()
	ok, _ := sv.Create(ctx, Input{Name: "ok", Type: "shell", Schedule: "@yearly", Command: "echo hello; echo err >&2"})
	sv.RunNow(ctx, ok.ID)
	if r := wait(t, sv, ok.ID); r.Status != "success" || !strings.Contains(r.Output, "hello") || !strings.Contains(r.Output, "err") {
		t.Fatalf("ok run = %+v", r)
	}
	bad, _ := sv.Create(ctx, Input{Name: "bad", Type: "shell", Schedule: "@yearly", Command: "exit 3"})
	sv.RunNow(ctx, bad.ID)
	if r := wait(t, sv, bad.ID); r.Status != "failed" || r.ExitCode != 3 {
		t.Fatalf("bad run = %+v", r)
	}
	slow, _ := sv.Create(ctx, Input{Name: "slow", Type: "shell", Schedule: "@yearly", Command: "sleep 30 & sleep 30", Timeout: 1})
	sv.RunNow(ctx, slow.ID)
	time.Sleep(100 * time.Millisecond)
	if err := sv.RunNow(ctx, slow.ID); err != ErrRunning {
		t.Errorf("second RunNow = %v", err)
	}
	if r := wait(t, sv, slow.ID); r.Status != "timeout" {
		t.Fatalf("slow run = %+v", r)
	}
}

func TestURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			http.Error(w, "nope", 500)
			return
		}
		io.WriteString(w, "pong")
	}))
	defer srv.Close()
	sv := newSvc(t)
	ctx := context.Background()
	j, _ := sv.Create(ctx, Input{Name: "u", Type: "url", Schedule: "@hourly", Command: srv.URL + "/ping"})
	sv.RunNow(ctx, j.ID)
	if r := wait(t, sv, j.ID); r.Status != "success" || !strings.Contains(r.Output, "pong") {
		t.Fatalf("url run = %+v", r)
	}
	f, _ := sv.Create(ctx, Input{Name: "f", Type: "url", Schedule: "@hourly", Command: srv.URL + "/fail"})
	sv.RunNow(ctx, f.ID)
	if r := wait(t, sv, f.ID); r.Status != "failed" {
		t.Fatalf("failing url run = %+v", r)
	}
}

func TestLogRotate(t *testing.T) {
	sv := newSvc(t)
	ctx := context.Background()
	log := filepath.Join(sv.LogDir, "a.com.log")
	for i := 0; i < 3; i++ {
		os.WriteFile(log, []byte("line\n"), 0o644)
		if err := sv.rotate(ctx, 2, io.Discard); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond) // distinct timestamps
	}
	old, _ := filepath.Glob(log + ".*.gz")
	if len(old) != 2 {
		t.Fatalf("kept %v", old)
	}
	if fi, _ := os.Stat(log); fi.Size() != 0 {
		t.Error("log not truncated")
	}
	f, _ := os.Open(old[0])
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(gz); string(b) != "line\n" {
		t.Errorf("gz = %q", b)
	}
}

func TestScheduled(t *testing.T) {
	sv := newSvc(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := sv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	j, _ := sv.Create(ctx, Input{Name: "tick", Type: "shell", Schedule: "* * * * * *", Command: "echo tick", Enabled: true})
	if j.Next == 0 {
		t.Error("no next run")
	}
	if r := wait(t, sv, j.ID); r.Status != "success" || r.Manual {
		t.Fatalf("scheduled run = %+v", r)
	}
	sv.SetEnabled(ctx, j.ID, false)
	if g, _ := sv.Get(ctx, j.ID); g.Next != 0 {
		t.Error("disabled job still scheduled")
	}
}
