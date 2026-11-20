package tasks

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestCancellationAndQueueDeadline(t *testing.T) {
	m := NewManager()
	started := make(chan struct{})
	release := make(chan struct{})
	first := m.StartAction(Action{Name: "first", Resource: "database"}, func(ctx context.Context, log Logf) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	<-started
	second := m.StartAction(Action{Name: "second", Resource: "database", Timeout: 40 * time.Millisecond}, func(ctx context.Context, log Logf) error { return nil })
	if err := m.Wait(context.Background(), second); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("queue deadline: %v", err)
	}
	if !m.Cancel(first) {
		t.Fatal("running task was not cancellable")
	}
	close(release)
	waitForStatus(t, m, first, Cancelled)
}

func TestCheckpointAndPersistentIdempotency(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewManager(st)
	started := make(chan struct{})
	release := make(chan struct{})
	id := m.StartAction(Action{Name: "restore", Owner: "admin", Module: "backup"}, func(ctx context.Context, log Logf) error {
		log("checkpoint")
		close(started)
		<-release
		return nil
	})
	<-started
	var log string
	if err := st.DB.QueryRow(`SELECT log FROM task_history WHERE id=?`, id).Scan(&log); err != nil || !strings.Contains(log, "checkpoint") {
		t.Fatalf("log checkpoint not persisted: %q %v", log, err)
	}
	close(release)
	waitForStatus(t, m, id, Success)
	var owner, module string
	if err := st.DB.QueryRow(`SELECT owner, module FROM task_history WHERE id=?`, id).Scan(&owner, &module); err != nil || owner != "admin" || module != "backup" {
		t.Fatalf("ownership not persisted: %q %q %v", owner, module, err)
	}
	var calls atomic.Int32
	action := Action{Name: "restore.once", IdempotencyKey: "request-1", PersistResult: true}
	first := m.StartAction(action, func(ctx context.Context, log Logf) error { calls.Add(1); return nil })
	waitForStatus(t, m, first, Success)
	second := m.StartAction(action, func(ctx context.Context, log Logf) error { calls.Add(1); return nil })
	if first != second || calls.Load() != 1 {
		t.Fatalf("persistent idempotency ran again: %q %q calls=%d", first, second, calls.Load())
	}
}
