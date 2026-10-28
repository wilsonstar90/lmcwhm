// Package tasks runs long operations (site creation, certificate issuance,
// environment installs) in the background and keeps their logs for the UI.
package tasks

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/settings"
)

type Status string

const (
	Running Status = "running"
	Success Status = "success"
	Failed  Status = "failed"
)

type Task struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Status   Status   `json:"status"`
	Log      []string `json:"log"`
	Error    string   `json:"error,omitempty"`
	Started  int64    `json:"started"`
	Finished int64    `json:"finished,omitempty"`
}

// Logf appends a progress line to the running task.
type Logf func(format string, args ...any)

const keep = 100

type Manager struct {
	mu    sync.Mutex
	tasks map[string]*Task
	order []string
	// One heavy job at a time: docker builds and config reloads must not interleave.
	run sync.Mutex
}

func NewManager() *Manager { return &Manager{tasks: map[string]*Task{}} }

// Start runs fn in the background and returns its task id immediately.
func (m *Manager) Start(name string, fn func(ctx context.Context, log Logf) error) string {
	return m.start(name, true, 30*time.Minute, fn)
}

// StartLong runs fn without the exclusive lock and with a longer deadline,
// for jobs such as backups that may take hours and must not block site changes.
func (m *Manager) StartLong(name string, timeout time.Duration, fn func(ctx context.Context, log Logf) error) string {
	return m.start(name, false, timeout, fn)
}

func (m *Manager) start(name string, exclusive bool, timeout time.Duration, fn func(ctx context.Context, log Logf) error) string {
	t := &Task{ID: settings.RandomString(12, settings.Alnum), Name: name, Status: Running, Log: []string{}, Started: time.Now().Unix()}
	m.mu.Lock()
	m.tasks[t.ID] = t
	m.order = append(m.order, t.ID)
	if len(m.order) > keep {
		delete(m.tasks, m.order[0])
		m.order = m.order[1:]
	}
	m.mu.Unlock()

	go func() {
		if exclusive {
			m.run.Lock()
			defer m.run.Unlock()
		}
		logf := func(format string, args ...any) {
			line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
			m.mu.Lock()
			t.Log = append(t.Log, line)
			m.mu.Unlock()
		}
		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			return fn(ctx, logf)
		}()
		m.mu.Lock()
		defer m.mu.Unlock()
		t.Finished = time.Now().Unix()
		if err != nil {
			t.Status, t.Error = Failed, err.Error()
			t.Log = append(t.Log, "ERROR: "+err.Error())
		} else {
			t.Status = Success
		}
	}()
	return t.ID
}

// RunSync runs fn under the same exclusive lock, for callers that need the result.
func (m *Manager) RunSync(ctx context.Context, fn func(ctx context.Context) error) error {
	m.run.Lock()
	defer m.run.Unlock()
	return fn(ctx)
}

func (m *Manager) Get(id string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return Task{}, false
	}
	c := *t
	c.Log = append([]string(nil), t.Log...)
	return c, true
}
