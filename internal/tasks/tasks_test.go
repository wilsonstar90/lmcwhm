package tasks

import (
	"context"
	"strings"
	"testing"
	"time"
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
