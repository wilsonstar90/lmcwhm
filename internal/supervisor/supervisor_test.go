package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestUnitAndValidation(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	sv := New(st, dir)
	sv.UnitDir = dir
	var calls []string
	sv.systemctl = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "ActiveState=active\nSubState=running\nMainPID=42\n", nil
	}
	ctx := context.Background()
	for _, bad := range []Process{
		{Name: "../x", Command: "sleep 1"},
		{Name: "ok", Command: ""},
		{Name: "ok", Command: "sleep 1", User: "no-such-user-xyz"},
		{Name: "ok", Command: "sleep 1", Env: []string{"BAD KEY=1"}},
		{Name: "ok", Command: "sleep 1", Dir: "/no/such/dir"},
	} {
		if _, err := sv.Save(ctx, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	p, err := sv.Save(ctx, Process{Name: "worker", Command: "echo \"$HOME\" 100%\nexec sleep 1000", Env: []string{`MSG=a "b" $c 50%`}})
	if err != nil {
		t.Fatal(err)
	}
	if p.State != "active" || p.PID != 42 {
		t.Fatalf("status: %+v", p)
	}
	unit, _ := os.ReadFile(filepath.Join(dir, Unit("worker")))
	if !strings.Contains(string(unit), `Environment="MSG=a \"b\" $$c 50%%"`) || !strings.Contains(string(unit), "Restart=always") {
		t.Fatalf("unit:\n%s", unit)
	}
	script, _ := os.ReadFile(filepath.Join(dir, "data", "proc", "worker.sh"))
	if !strings.Contains(string(script), "exec sleep 1000") {
		t.Fatalf("script: %s", script)
	}
	if !strings.Contains(strings.Join(calls, ";"), "enable "+Unit("worker")) {
		t.Fatalf("calls: %v", calls)
	}
	if _, err := sv.Save(ctx, Process{Name: "worker", Command: "x"}); err != ErrExists {
		t.Fatalf("duplicate: %v", err)
	}
	if err := sv.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, Unit("worker"))); err == nil {
		t.Fatal("unit not removed")
	}
}
