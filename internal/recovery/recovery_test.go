package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	state    State
	readyErr error
	restarts int
	inspects int
}

func (f *fakeRunner) Targets(context.Context) ([]string, error) { return []string{"db"}, nil }
func (f *fakeRunner) Inspect(context.Context, string) (State, error) {
	f.inspects++
	return f.state, nil
}
func (f *fakeRunner) Ready(context.Context, string) error { return f.readyErr }
func (f *fakeRunner) Restart(context.Context, string) error {
	f.restarts++
	return nil
}

func TestRestartRequiresConsecutiveFailures(t *testing.T) {
	f := &fakeRunner{state: State{Running: true, Status: "running"}, readyErr: errors.New("not ready")}
	s := New(f)
	s.StartDelay = 0
	var events []Event
	s.Notify = func(e Event) { events = append(events, e) }
	s.Check(context.Background())
	s.Check(context.Background())
	if f.restarts != 0 {
		t.Fatal("transient failures caused a restart")
	}
	s.Check(context.Background())
	if f.restarts != 1 {
		t.Fatal("third consecutive failure did not restart")
	}
	f.readyErr = nil
	s.Check(context.Background())
	if len(events) != 2 || events[0].Title != "Container restarted: db" || !events[1].Resolved {
		t.Fatalf("recovery events: %+v", events)
	}
}

func TestHealthyCheckResetsFailureCount(t *testing.T) {
	f := &fakeRunner{state: State{Running: true, Status: "running"}, readyErr: errors.New("not ready")}
	s := New(f)
	s.Check(context.Background())
	s.Check(context.Background())
	f.readyErr = nil
	s.Check(context.Background())
	f.readyErr = errors.New("not ready again")
	s.Check(context.Background())
	s.Check(context.Background())
	if f.restarts != 0 {
		t.Fatal("non-consecutive failures caused a restart")
	}
}

func TestCooldownAndHourlyLimit(t *testing.T) {
	f := &fakeRunner{state: State{Running: true, Status: "running"}, readyErr: errors.New("not ready")}
	s := New(f)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	var events []Event
	s.Notify = func(e Event) { events = append(events, e) }
	fail := func() {
		s.Check(context.Background())
		s.Check(context.Background())
		s.Check(context.Background())
	}
	fail()
	if f.restarts != 1 {
		t.Fatal("first recovery did not restart")
	}
	fail()
	if f.restarts != 1 {
		t.Fatal("cooldown was ignored")
	}
	now = now.Add(11 * time.Minute)
	fail()
	now = now.Add(11 * time.Minute)
	fail()
	if f.restarts != 3 {
		t.Fatalf("expected three restarts, got %d", f.restarts)
	}
	now = now.Add(11 * time.Minute)
	before := len(events)
	fail()
	if f.restarts != 3 || len(events) != before+1 || events[len(events)-1].Title != "Container recovery paused: db" {
		t.Fatalf("hourly restart limit was not applied: restarts=%d events=%+v", f.restarts, events)
	}
}

func TestDockerRunner(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_ARGS_LOG"
case "$1 $2" in
  "ps -a") printf 'lmcwhm-mariadb114\nlmcwhm-php82\nlmcwhm-phpmyadmin52\nlmcwhm-varnish\n' ;;
  "inspect -f") printf '{"Status":"running","Running":true,"Restarting":false,"OOMKilled":false}' ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_ARGS_LOG", logPath)
	d := Docker{}
	targets, err := d.Targets(context.Background())
	if err != nil || len(targets) != 3 {
		t.Fatalf("targets: %v %v", targets, err)
	}
	st, err := d.Inspect(context.Background(), "db")
	if err != nil || !st.Running || st.OOMKilled {
		t.Fatalf("inspect: %+v %v", st, err)
	}
	if err := d.Ready(context.Background(), "lmcwhm-mariadb114"); err != nil {
		t.Fatal(err)
	}
	if err := d.Ready(context.Background(), "lmcwhm-varnish"); err != nil {
		t.Fatal(err)
	}
	if err := d.Restart(context.Background(), "db"); err != nil {
		t.Fatal(err)
	}
	logBytes, _ := os.ReadFile(logPath)
	logText := string(logBytes)
	for _, want := range []string{"exec lmcwhm-mariadb114 mariadb-admin ping --silent", "exec lmcwhm-varnish varnishadm status", "restart -t 30 db"} {
		if !strings.Contains(logText, want) {
			t.Errorf("missing %q in %q", want, logText)
		}
	}
}

func TestAllowSkipsRecovery(t *testing.T) {
	f := &fakeRunner{state: State{Running: true, Status: "running"}, readyErr: errors.New("not ready")}
	s := New(f)
	s.Allow = func() bool { return false }
	s.Check(context.Background())
	if f.inspects != 0 || f.restarts != 0 {
		t.Fatal("recovery ran while a configuration task was active")
	}
}
