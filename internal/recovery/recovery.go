// Package recovery adds bounded, application-aware recovery for managed containers.
package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

type State struct {
	Running    bool
	Restarting bool
	OOMKilled  bool
	Status     string
	Health     string
}

type Runner interface {
	Targets(ctx context.Context) ([]string, error)
	Inspect(ctx context.Context, name string) (State, error)
	Ready(ctx context.Context, name string) error
	Restart(ctx context.Context, name string) error
}

type Event struct {
	Name     string
	Severity string
	Title    string
	Body     string
	Resolved bool
}

type Service struct {
	Runner        Runner
	Interval      time.Duration
	StartDelay    time.Duration
	FailureLimit  int
	Cooldown      time.Duration
	MaxRestarts   int
	Allow         func() bool
	AllowResource func(string) bool
	StatePath     string
	Notify        func(Event)
	Log           func(string, ...any)

	now   func() time.Time
	mu    sync.Mutex
	state map[string]*containerState
}

type containerState struct {
	failures   int
	last       time.Time
	attempts   []time.Time
	limited    bool
	recovering bool
}

func New(r Runner) *Service {
	return &Service{
		Runner: r, Interval: 30 * time.Second, StartDelay: 5 * time.Minute,
		FailureLimit: 3, Cooldown: 10 * time.Minute, MaxRestarts: 3,
		now: time.Now, state: map[string]*containerState{},
	}
}

func (s *Service) Run(ctx context.Context) {
	if s.StartDelay > 0 {
		t := time.NewTimer(s.StartDelay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
	s.Check(ctx)
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Check(ctx)
		}
	}
}

func (s *Service) Check(ctx context.Context) {
	if s.Allow != nil && !s.Allow() {
		return
	}
	targets, err := s.Runner.Targets(ctx)
	if err != nil {
		s.log("recovery targets: %v", err)
		return
	}
	for _, name := range targets {
		s.check(ctx, name)
	}
}

func (s *Service) check(ctx context.Context, name string) {
	if s.AllowResource != nil && !s.AllowResource(name) {
		return
	}
	st, err := s.Runner.Inspect(ctx, name)
	if err != nil {
		s.log("recovery inspect %s: %v", name, err)
		return
	}
	readyErr := readiness(ctx, s.Runner, name, st)
	if readyErr == nil {
		s.healthy(name)
		return
	}

	s.mu.Lock()
	c := s.state[name]
	if c == nil {
		c = &containerState{}
		s.state[name] = c
	}
	c.failures++
	limit := max(s.FailureLimit, 1)
	if c.failures < limit {
		s.persistLocked()
		s.mu.Unlock()
		return
	}
	now := s.now()
	c.attempts = recentAttempts(c.attempts, now)
	can := now.Sub(c.last) >= s.Cooldown && len(c.attempts) < max(s.MaxRestarts, 1)
	if !can {
		if !c.limited {
			c.limited = true
			s.persistLocked()
			s.mu.Unlock()
			s.notify(Event{Name: name, Severity: "critical", Title: "Container recovery paused: " + name,
				Body: fmt.Sprintf("%v; restart cooldown or hourly limit reached", readyErr)})
		} else {
			s.persistLocked()
			s.mu.Unlock()
		}
		return
	}
	c.last = now
	c.failures = 0
	c.limited = false
	s.persistLocked()
	s.mu.Unlock()

	reason := fmt.Sprintf("status=%s health=%s oom=%v: %v", st.Status, st.Health, st.OOMKilled, readyErr)
	if err := s.Runner.Restart(ctx, name); err != nil {
		s.notify(Event{Name: name, Severity: "critical", Title: "Container restart failed: " + name, Body: err.Error()})
		return
	}
	s.mu.Lock()
	c.attempts = append(c.attempts, now)
	c.recovering = true
	s.persistLocked()
	s.mu.Unlock()
	s.notify(Event{Name: name, Severity: "warning", Title: "Container restarted: " + name, Body: reason})
}

func (s *Service) healthy(name string) {
	s.mu.Lock()
	c := s.state[name]
	recovered := c != nil && c.recovering
	if c != nil {
		c.failures = 0
		c.limited = false
		c.recovering = false
	}
	s.persistLocked()
	s.mu.Unlock()
	if recovered {
		s.notify(Event{Name: name, Severity: "resolved", Title: "Container recovered: " + name, Resolved: true})
	}
}

func recentAttempts(in []time.Time, now time.Time) []time.Time {
	out := in[:0]
	for _, t := range in {
		if now.Sub(t) < time.Hour {
			out = append(out, t)
		}
	}
	return out
}

func readiness(ctx context.Context, r Runner, name string, st State) error {
	if !st.Running || st.Restarting {
		return errors.New("container is not running")
	}
	if st.Health == "unhealthy" {
		return errors.New("container healthcheck is unhealthy")
	}
	if st.Health != "" && st.Health != "healthy" {
		return fmt.Errorf("container healthcheck is %s", st.Health)
	}
	return r.Ready(ctx, name)
}

func (s *Service) notify(e Event) {
	if s.Notify != nil {
		s.Notify(e)
	}
}

func (s *Service) log(f string, a ...any) {
	if s.Log != nil {
		s.Log(f, a...)
	}
}

type Docker struct {
	Installed func(context.Context) (map[string]bool, error)
}

func (d Docker) Targets(ctx context.Context) ([]string, error) {
	out, err := dockerx.Run(ctx, "ps", "-a", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	var installed map[string]bool
	if d.Installed != nil {
		installed, err = d.Installed(ctx)
		if err != nil {
			return nil, err
		}
	}
	var names []string
	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if d.Installed != nil && appstore.IsAppContainer(name) && !installed[name] {
			continue
		}
		if strings.HasPrefix(name, "lmcwhm-mariadb") || strings.HasPrefix(name, "lmcwhm-varnish") ||
			strings.HasPrefix(name, "lmcwhm-redis") || strings.HasPrefix(name, "lmcwhm-postgres") || strings.HasPrefix(name, "lmcwhm-mongo") ||
			name == "lmcwhm-nginx" || name == "lmcwhm-haproxy" ||
			(strings.HasPrefix(name, "lmcwhm-php") && !strings.HasPrefix(name, "lmcwhm-phpmyadmin")) {
			names = append(names, name)
		}
	}
	return names, nil
}

func (Docker) Inspect(ctx context.Context, name string) (State, error) {
	out, err := dockerx.Run(ctx, "inspect", "-f", "{{json .State}}", name)
	if err != nil {
		return State{}, err
	}
	var raw struct {
		Status                         string
		Running, Restarting, OOMKilled bool
		Health                         *struct{ Status string }
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return State{}, err
	}
	st := State{Status: raw.Status, Running: raw.Running, Restarting: raw.Restarting, OOMKilled: raw.OOMKilled}
	if raw.Health != nil {
		st.Health = raw.Health.Status
	}
	return st, nil
}

func (Docker) Ready(ctx context.Context, name string) error {
	switch {
	case strings.HasPrefix(name, "lmcwhm-mariadb"):
		_, err := dockerx.Run(ctx, "exec", name, "mariadb-admin", "ping", "--silent")
		return err
	case strings.HasPrefix(name, "lmcwhm-varnish"):
		_, err := dockerx.Run(ctx, "exec", name, "varnishadm", "status")
		return err
	case strings.HasPrefix(name, "lmcwhm-php"):
		v := strings.TrimPrefix(name, "lmcwhm-php")
		if len(v) < 2 {
			return errors.New("unknown PHP container")
		}
		v = v[:1] + "." + v[1:]
		_, err := webenv.PHPStatus(ctx, v)
		return err
	case strings.HasPrefix(name, "lmcwhm-redis"):
		out, err := dockerx.Run(ctx, "exec", name, "redis-cli", "--no-auth-warning", "ping")
		if err != nil {
			return err
		}
		if strings.Contains(out, "PONG") || strings.Contains(out, "NOAUTH") {
			return nil
		}
		return errors.New("redis did not answer")
	case strings.HasPrefix(name, "lmcwhm-postgres"):
		_, err := dockerx.Run(ctx, "exec", name, "pg_isready", "-q", "-h", "127.0.0.1")
		return err
	case strings.HasPrefix(name, "lmcwhm-mongo"):
		return dialPublished(ctx, name)
	case name == "lmcwhm-nginx":
		_, err := dockerx.Run(ctx, "exec", name, "nginx", "-t", "-c", "/etc/nginx/lmcwhm/nginx.conf")
		return err
	case name == "lmcwhm-haproxy":
		_, err := dockerx.Run(ctx, "exec", name, "haproxy", "-c", "-q", "-f", "/opt/lmcwhm/edge/haproxy.cfg")
		return err
	}
	return nil
}

func dialPublished(ctx context.Context, name string) error {
	out, err := dockerx.Run(ctx, "port", name)
	if err != nil {
		return err
	}
	line := strings.TrimSpace(strings.Split(out, "\n")[0])
	_, target, ok := strings.Cut(line, " -> ")
	if !ok || target == "" {
		return errors.New("container has no published port")
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err == nil {
		conn.Close()
	}
	return err
}

func (Docker) Restart(ctx context.Context, name string) error {
	_, err := dockerx.Run(ctx, "restart", "-t", "30", name)
	return err
}
