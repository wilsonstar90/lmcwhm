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
	Queued    Status = "queued"
	Running   Status = "running"
	Success   Status = "success"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

type Task struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Action         string   `json:"action,omitempty"`
	Resource       string   `json:"resource,omitempty"`
	Owner          string   `json:"owner,omitempty"`
	Module         string   `json:"module,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	Status         Status   `json:"status"`
	Log            []string `json:"log"`
	Error          string   `json:"error,omitempty"`
	Started        int64    `json:"started"`
	Finished       int64    `json:"finished,omitempty"`
	checkpointAt   time.Time
}

// Action describes one mutation. Exclusive actions retain the historical
// global lock; Resource adds an independent lock for a shared subsystem.
type Action struct {
	Name           string
	Resource       string
	Owner          string
	Module         string
	IdempotencyKey string
	Exclusive      bool
	NoLock         bool
	PersistResult  bool
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
	cancel    map[string]context.CancelFunc
	run       chan struct{}
	resources map[string]chan struct{}
}

// NewManager opens a task manager. With a store, queued and running work is
// closed safely after a restart; non-idempotent work is never repeated.
func NewManager(st ...*store.Store) *Manager {
	m := &Manager{tasks: map[string]*Task{}, pending: map[string]bool{}, cancel: map[string]context.CancelFunc{},
		run: make(chan struct{}, 1), resources: map[string]chan struct{}{}}
	m.run <- struct{}{}
	if len(st) > 0 {
		m.st = st[0]
	}
	if m.st != nil {
		now := time.Now().Unix()
		_, err := m.st.DB.Exec(`UPDATE task_history SET status = ?, error = ?, finished = ? WHERE status IN (?, ?)`,
			Failed, "task interrupted by panel restart", now, Queued, Running)
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
// while queued or running returns the existing task instead of doing the work twice.
func (m *Manager) StartAction(a Action, fn func(ctx context.Context, log Logf) error) string {
	if a.Timeout <= 0 {
		a.Timeout = 30 * time.Minute
	}
	if a.PersistResult && a.IdempotencyKey != "" && m.st != nil {
		var id string
		if err := m.st.DB.QueryRow(`SELECT id FROM task_history WHERE idempotency_key = ? AND action = ? AND status = ? ORDER BY finished DESC LIMIT 1`,
			a.IdempotencyKey, a.Name, Success).Scan(&id); err == nil {
			return id
		}
	}
	m.mu.Lock()
	if a.IdempotencyKey != "" {
		for _, t := range m.tasks {
			if t.IdempotencyKey == a.IdempotencyKey && (t.Status == Queued || t.Status == Running) {
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
		Resource: a.Resource, Owner: a.Owner, Module: a.Module, IdempotencyKey: a.IdempotencyKey, Status: Queued,
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

	ctx, cancel := context.WithTimeout(WithAction(context.Background(), a), a.Timeout)
	m.mu.Lock()
	m.cancel[t.ID] = cancel
	m.mu.Unlock()
	go m.execute(t, a, ctx, fn)
	return t.ID
}

func (m *Manager) execute(t *Task, a Action, ctx context.Context, fn func(context.Context, Logf) error) {
	defer func() {
		m.mu.Lock()
		delete(m.cancel, t.ID)
		m.mu.Unlock()
	}()
	unlock, err := m.lockAction(ctx, a)
	if err == nil {
		m.mu.Lock()
		t.Status = Running
		started := *t
		m.mu.Unlock()
		m.persist(started)
		err = m.invoke(ctx, a, t, fn)
	}
	unlock()
	m.mu.Lock()
	delete(m.pending, a.IdempotencyKey)
	t.Finished = time.Now().Unix()
	switch {
	case err == nil:
		t.Status = Success
	case errors.Is(err, context.Canceled):
		t.Status, t.Error = Cancelled, "task cancelled"
	default:
		t.Status, t.Error = Failed, err.Error()
		t.Log = append(t.Log, "ERROR: "+err.Error())
	}
	done := *t
	done.Log = append([]string(nil), t.Log...)
	m.mu.Unlock()
	m.persist(done)
	if m.OnDone != nil {
		m.OnDone(done)
	}
}

func (m *Manager) invoke(ctx context.Context, a Action, t *Task, fn func(context.Context, Logf) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	logf := func(format string, args ...any) {
		now := time.Now()
		if m.Loc != nil {
			now = now.In(m.Loc)
		}
		line := fmt.Sprintf("[%s] %s", now.Format("15:04:05"), fmt.Sprintf(format, args...))
		m.mu.Lock()
		t.Log = append(t.Log, line)
		checkpoint := now.Sub(t.checkpointAt) >= time.Second
		if checkpoint {
			t.checkpointAt = now
		}
		copy := *t
		copy.Log = append([]string(nil), t.Log...)
		m.mu.Unlock()
		if checkpoint {
			m.persist(copy)
		}
	}
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
}

// RunAction executes a synchronous action under the requested locks.
func (m *Manager) RunAction(ctx context.Context, a Action, fn func(ctx context.Context) error) error {
	unlock, err := m.lockAction(ctx, a)
	if err != nil {
		return err
	}
	defer unlock()
	return fn(WithAction(ctx, a))
}

// Cancel requests cancellation of a queued or running task.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	cancel, ok := m.cancel[id]
	m.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// ExclusiveIdle reports whether a configuration-changing task is active.
func (m *Manager) ExclusiveIdle() bool {
	select {
	case <-m.run:
		m.run <- struct{}{}
		return true
	default:
		return false
	}
}

func (m *Manager) ResourceIdle(name string) bool {
	m.mu.Lock()
	lock := m.resources[name]
	m.mu.Unlock()
	return lock == nil || len(lock) != 0
}

// RunSync retains the original global synchronous lock.
func (m *Manager) RunSync(ctx context.Context, fn func(ctx context.Context) error) error {
	return m.RunAction(ctx, Action{Exclusive: true}, fn)
}

func acquire(ctx context.Context, lock chan struct{}) error {
	select {
	case <-lock:
		return nil
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lock:
		return nil
	}
}

func (m *Manager) lockAction(ctx context.Context, a Action) (func(), error) {
	if a.NoLock {
		return func() {}, nil
	}
	if a.Exclusive {
		if err := acquire(ctx, m.run); err != nil {
			return func() {}, err
		}
	}
	var resource chan struct{}
	if a.Resource != "" {
		m.mu.Lock()
		resource = m.resources[a.Resource]
		if resource == nil {
			resource = make(chan struct{}, 1)
			resource <- struct{}{}
			m.resources[a.Resource] = resource
		}
		m.mu.Unlock()
		if err := acquire(ctx, resource); err != nil {
			if a.Exclusive {
				m.run <- struct{}{}
			}
			return func() {}, err
		}
	}
	return func() {
		if resource != nil {
			resource <- struct{}{}
		}
		if a.Exclusive {
			m.run <- struct{}{}
		}
	}, nil
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
		case Cancelled:
			return errors.New("task cancelled")
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
	err := m.st.Read().QueryRow(`SELECT id, name, action, resource, owner, module, idempotency_key, status, error, started, finished, log
		FROM task_history WHERE id = ?`, id).Scan(&t.ID, &t.Name, &t.Action, &t.Resource, &t.Owner, &t.Module, &t.IdempotencyKey,
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
	_, err := m.st.DB.Exec(`INSERT INTO task_history(id, name, action, resource, owner, module, idempotency_key, status, error, started, finished, log)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, action=excluded.action, resource=excluded.resource,
		owner=excluded.owner, module=excluded.module, idempotency_key=excluded.idempotency_key, status=excluded.status, error=excluded.error,
		started=excluded.started, finished=excluded.finished, log=excluded.log`,
		t.ID, t.Name, t.Action, t.Resource, t.Owner, t.Module, t.IdempotencyKey, t.Status, t.Error, t.Started, t.Finished, strings.Join(logText, "\n"))
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
