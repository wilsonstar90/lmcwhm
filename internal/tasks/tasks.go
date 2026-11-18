// Package tasks runs panel actions synchronously or in the background and
// keeps their state available to every API, MCP and UI caller.
package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

type Status string

const (
	Running Status = "running"
	Success Status = "success"
	Failed  Status = "failed"
)

type Task struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Action         string   `json:"action,omitempty"`
	Resource       string   `json:"resource,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	Status         Status   `json:"status"`
	Log            []string `json:"log"`
	Error          string   `json:"error,omitempty"`
	Started        int64    `json:"started"`
	Finished       int64    `json:"finished,omitempty"`
}

// Action describes one mutation. Exclusive actions retain the historical
// global lock; Resource adds an independent lock for a shared subsystem.
type Action struct {
	Name           string
	Resource       string
	IdempotencyKey string
	Exclusive      bool
	NoLock         bool
	Timeout        time.Duration
	// Coalesce waits briefly before applying and reruns once more when a new
	// request arrived during execution. The function must read current state.
	Coalesce time.Duration
}

type Logf func(format string, args ...any)

const keep = 100

type Manager struct {
	OnDone func(Task)
	// OnError reports a persistence failure without stopping the action itself.
	OnError func(error)
	Loc     *time.Location

	st *store.Store

	mu        sync.Mutex
	tasks     map[string]*Task
	order     []string
	pending   map[string]bool
	run       sync.Mutex
	resources map[string]*sync.Mutex
}

// NewManager opens a task manager. With a store, running and finished tasks
// survive panel restarts. Interrupted work is marked failed, never repeated.
func NewManager(st ...*store.Store) *Manager {
	m := &Manager{tasks: map[string]*Task{}, pending: map[string]bool{}, resources: map[string]*sync.Mutex{}}
	if len(st) > 0 {
		m.st = st[0]
	}
	if m.st != nil {
		now := time.Now().Unix()
		_, err := m.st.DB.Exec(`UPDATE task_history SET status = ?, error = ?, finished = ? WHERE status = ?`,
			Failed, "task interrupted by panel restart", now, Running)
		if err != nil {
			m.report(err)
		}
	}
	return m
}

func (m *Manager) report(err error) {
	if err != nil && m.OnError != nil {
		m.OnError(err)
	}
}

// Start runs an exclusive background action with the historical default timeout.
func (m *Manager) Start(name string, fn func(ctx context.Context, log Logf) error) string {
	return m.StartAction(Action{Name: name, Exclusive: true, Timeout: 30 * time.Minute}, fn)
}

// StartLong runs a background action without the global lock, for jobs such as
// backups that must not block unrelated site changes.
func (m *Manager) StartLong(name string, timeout time.Duration, fn func(ctx context.Context, log Logf) error) string {
	return m.StartAction(Action{Name: name, Timeout: timeout}, fn)
}

// StartAction queues an action. Repeating the same non-empty idempotency key
// while it is running returns the existing task instead of doing the work twice.
func (m *Manager) StartAction(a Action, fn func(ctx context.Context, log Logf) error) string {
	if a.Timeout <= 0 {
		a.Timeout = 30 * time.Minute
	}
	m.mu.Lock()
	if a.IdempotencyKey != "" {
		for _, t := range m.tasks {
			if t.IdempotencyKey == a.IdempotencyKey && t.Status == Running {
				id := t.ID
				if a.Coalesce > 0 {
					m.pending[a.IdempotencyKey] = true
				}
				m.mu.Unlock()
				return id
			}
		}
	}
	t := &Task{
		ID: settings.RandomString(12, settings.Alnum), Name: a.Name, Action: a.Name,
		Resource: a.Resource, IdempotencyKey: a.IdempotencyKey, Status: Running,
		Log: []string{}, Started: time.Now().Unix(),
	}
	m.tasks[t.ID] = t
	m.order = append(m.order, t.ID)
	if len(m.order) > keep {
		delete(m.tasks, m.order[0])
		m.order = m.order[1:]
	}
	m.mu.Unlock()
	m.persist(*t)

	go func() {
		unlock := m.lockAction(a)
		defer unlock()
		logf := func(format string, args ...any) {
			now := time.Now()
			if m.Loc != nil {
				now = now.In(m.Loc)
			}
			line := fmt.Sprintf("[%s] %s", now.Format("15:04:05"), fmt.Sprintf(format, args...))
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
			ctx, cancel := context.WithTimeout(WithAction(context.Background(), a), a.Timeout)
			defer cancel()
			for {
				if a.Coalesce > 0 {
					timer := time.NewTimer(a.Coalesce)
					select {
					case <-ctx.Done():
						timer.Stop()
						return ctx.Err()
					case <-timer.C:
					}
				}
				if err := fn(ctx, logf); err != nil {
					return err
				}
				m.mu.Lock()
				again := a.IdempotencyKey != "" && m.pending[a.IdempotencyKey]
				if again {
					m.pending[a.IdempotencyKey] = false
				}
				m.mu.Unlock()
				if !again {
					return nil
				}
			}
		}()
		m.mu.Lock()
		delete(m.pending, a.IdempotencyKey)
		t.Finished = time.Now().Unix()
		if err != nil {
			t.Status, t.Error = Failed, err.Error()
			t.Log = append(t.Log, "ERROR: "+err.Error())
		} else {
			t.Status = Success
		}
		done := *t
		done.Log = append([]string(nil), t.Log...)
		m.mu.Unlock()
		m.persist(done)
		if m.OnDone != nil {
			m.OnDone(done)
		}
	}()
	return t.ID
}

// RunAction executes a synchronous action under the requested locks.
func (m *Manager) RunAction(ctx context.Context, a Action, fn func(ctx context.Context) error) error {
	unlock := m.lockAction(a)
	defer unlock()
	return fn(WithAction(ctx, a))
}

// ExclusiveIdle reports whether a configuration-changing task is active.
func (m *Manager) ExclusiveIdle() bool {
	if !m.run.TryLock() {
		return false
	}
	m.run.Unlock()
	return true
}

// RunSync retains the original global synchronous lock.
func (m *Manager) RunSync(ctx context.Context, fn func(ctx context.Context) error) error {
	return m.RunAction(ctx, Action{Exclusive: true}, fn)
}

func (m *Manager) lockAction(a Action) func() {
	if a.NoLock {
		return func() {}
	}
	if a.Exclusive {
		m.run.Lock()
	}
	var r *sync.Mutex
	if a.Resource != "" {
		m.mu.Lock()
		r = m.resources[a.Resource]
		if r == nil {
			r = &sync.Mutex{}
			m.resources[a.Resource] = r
		}
		m.mu.Unlock()
		r.Lock()
	}
	return func() {
		if r != nil {
			r.Unlock()
		}
		if a.Exclusive {
			m.run.Unlock()
		}
	}
}

func (m *Manager) Get(id string) (Task, bool) {
	m.mu.Lock()
	t, ok := m.tasks[id]
	if ok {
		c := *t
		c.Log = append(make([]string, 0, len(t.Log)), t.Log...)
		m.mu.Unlock()
		return c, true
	}
	m.mu.Unlock()
	if m.st == nil {
		return Task{}, false
	}
	return m.fromStore(id)
}

// Wait preserves the synchronous API contract while sharing the underlying task.
func (m *Manager) Wait(ctx context.Context, id string) error {
	for {
		t, ok := m.Get(id)
		if !ok {
			return errors.New("task not found")
		}
		switch t.Status {
		case Success:
			return nil
		case Failed:
			if t.Error != "" {
				return errors.New(t.Error)
			}
			return errors.New("task failed")
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (m *Manager) fromStore(id string) (Task, bool) {
	var t Task
	var status, logText string
	err := m.st.DB.QueryRow(`SELECT id, name, action, resource, idempotency_key, status, error, started, finished, log
		FROM task_history WHERE id = ?`, id).Scan(&t.ID, &t.Name, &t.Action, &t.Resource, &t.IdempotencyKey,
		&status, &t.Error, &t.Started, &t.Finished, &logText)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false
	}
	if err != nil {
		m.report(err)
		return Task{}, false
	}
	t.Status = Status(status)
	if logText != "" {
		t.Log = strings.Split(logText, "\n")
	} else {
		t.Log = []string{}
	}
	return t, true
}

func (m *Manager) persist(t Task) {
	if m.st == nil {
		return
	}
	logText := t.Log
	if len(logText) > 200 {
		logText = logText[len(logText)-200:]
	}
	_, err := m.st.DB.Exec(`INSERT INTO task_history(id, name, action, resource, idempotency_key, status, error, started, finished, log)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, action=excluded.action, resource=excluded.resource,
		idempotency_key=excluded.idempotency_key, status=excluded.status, error=excluded.error,
		started=excluded.started, finished=excluded.finished, log=excluded.log`,
		t.ID, t.Name, t.Action, t.Resource, t.IdempotencyKey, t.Status, t.Error, t.Started, t.Finished, strings.Join(logText, "\n"))
	m.report(err)
}

// List returns recent in-process tasks, newest first, without their logs.
func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Task, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		c := *m.tasks[m.order[i]]
		c.Log = nil
		out = append(out, c)
	}
	return out
}
