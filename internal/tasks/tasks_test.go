package tasks

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestLogStampsInPanelZone(t *testing.T) {
	m := NewManager()
	m.Loc = time.FixedZone("X", 5*3600+30*60)
	before := "[" + time.Now().In(m.Loc).Format("15:04")
	done := make(chan struct{})
	id := m.StartLong("t", time.Minute, func(ctx context.Context, log Logf) error {
		log("hello")
		close(done)
		return nil
	})
	<-done
	want := "[" + time.Now().In(m.Loc).Format("15:04") // minute precision: the clock may tick over
	for range 50 {
		if task, ok := m.Get(id); ok && len(task.Log) > 0 {
			if !(strings.HasPrefix(task.Log[0], want) || strings.HasPrefix(task.Log[0], before)) || !strings.HasSuffix(task.Log[0], "hello") {
				t.Fatalf("line %q, want prefix %q", task.Log[0], want)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no log line")
}

func TestDurableTasksAndInterruptedRecovery(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewManager(st)
	started := make(chan struct{})
	release := make(chan struct{})
	id := m.StartAction(Action{Name: "backup.run", Resource: "database:1", IdempotencyKey: "backup-1"}, func(ctx context.Context, log Logf) error {
		close(started)
		<-release
		log("finished")
		return nil
	})
	<-started
	var status string
	if err := st.DB.QueryRow(`SELECT status FROM task_history WHERE id = ?`, id).Scan(&status); err != nil || status != string(Running) {
		t.Fatalf("running state not persisted: %q %v", status, err)
	}
	close(release)
	waitForStatus(t, m, id, Success)

	reopened := NewManager(st)
	if got, ok := reopened.Get(id); !ok || got.Status != Success || len(got.Log) != 1 || got.Resource != "database:1" {
		t.Fatalf("stored task not recovered: %+v %v", got, ok)
	}

	started2 := make(chan struct{})
	release2 := make(chan struct{})
	defer close(release2)
	id2 := reopened.StartAction(Action{Name: "restore"}, func(ctx context.Context, log Logf) error {
		close(started2)
		<-release2
		return nil
	})
	<-started2
	reopened = NewManager(st)
	got, ok := reopened.Get(id2)
	if !ok || got.Status != Failed || !strings.Contains(got.Error, "interrupted") {
		t.Fatalf("interrupted task was not closed safely: %+v %v", got, ok)
	}
}

func TestActionDeduplicationAndResourceLock(t *testing.T) {
	m := NewManager()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	action := Action{Name: "web.apply", Resource: "web", IdempotencyKey: "web-current"}
	first := m.StartAction(action, func(ctx context.Context, log Logf) error {
		calls.Add(1)
		close(started)
		<-release
		return nil
	})
	second := m.StartAction(action, func(ctx context.Context, log Logf) error {
		calls.Add(1)
		return nil
	})
	if first != second {
		t.Fatalf("duplicate action started another task: %q %q", first, second)
	}
	<-started
	otherStarted := make(chan struct{})
	other := m.StartAction(Action{Name: "web.apply.other", Resource: "web"}, func(ctx context.Context, log Logf) error {
		close(otherStarted)
		return nil
	})
	select {
	case <-otherStarted:
		t.Fatal("same resource ran concurrently")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	waitForStatus(t, m, first, Success)
	waitForStatus(t, m, other, Success)
	if calls.Load() != 1 {
		t.Fatalf("deduplicated action ran %d times", calls.Load())
	}
}

func TestWait(t *testing.T) {
	m := NewManager()
	ok := m.Start("ok", func(ctx context.Context, log Logf) error { return nil })
	if err := m.Wait(context.Background(), ok); err != nil {
		t.Fatal(err)
	}
	bad := m.Start("bad", func(ctx context.Context, log Logf) error { return errors.New("apply failed") })
	if err := m.Wait(context.Background(), bad); err == nil || err.Error() != "apply failed" {
		t.Fatalf("wait error: %v", err)
	}
}

func TestCoalescedActionRerunsLatestState(t *testing.T) {
	m := NewManager()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	action := Action{Name: "web.apply", IdempotencyKey: "web", Coalesce: 5 * time.Millisecond}
	fn := func(ctx context.Context, log Logf) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil
	}
	id := m.StartAction(action, fn)
	<-started
	for range 5 {
		if got := m.StartAction(action, fn); got != id {
			t.Fatalf("coalesced action got new task %q, want %q", got, id)
		}
	}
	close(release)
	waitForStatus(t, m, id, Success)
	if got := calls.Load(); got != 2 {
		t.Fatalf("five updates during apply produced %d runs, want 2", got)
	}
}

func waitForStatus(t *testing.T, m *Manager, id string, want Status) {
	t.Helper()
	for range 200 {
		if task, ok := m.Get(id); ok && task.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach %s", id, want)
}
