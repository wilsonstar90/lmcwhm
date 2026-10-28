// Package supervisor is the process guardian (進程守護): long-running
// commands kept alive as systemd services (lmcwhm-proc-<name>), restarted on
// exit and started at boot, with their output in the journal.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

var (
	ErrNotFound = errors.New("proc.not_found")
	ErrName     = errors.New("proc.invalid_name")
	ErrExists   = errors.New("proc.exists")
	ErrCommand  = errors.New("proc.invalid_command")
	ErrUser     = errors.New("proc.invalid_user")
	ErrDir      = errors.New("proc.invalid_dir")
	ErrEnv      = errors.New("proc.invalid_env")
	nameRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)
	envRe       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=[^\n]*$`)
)

type Process struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	Dir       string   `json:"dir"`
	User      string   `json:"user"`
	Env       []string `json:"env"`
	Remark    string   `json:"remark"`
	CreatedAt int64    `json:"created_at"`
	// from systemd
	State    string `json:"state"`     // active | inactive | failed | activating ...
	SubState string `json:"sub_state"` // running | exited | auto-restart ...
	PID      int    `json:"pid"`
	Since    int64  `json:"since"`
	Restarts int    `json:"restarts"`
	MemBytes int64  `json:"mem_bytes"`
}

type Service struct {
	st *store.Store
	// UnitDir and ScriptDir are variables for tests.
	UnitDir   string
	ScriptDir string
	systemctl func(ctx context.Context, args ...string) (string, error)
	journal   func(ctx context.Context, unit string, n int) (string, error)
}

func New(st *store.Store, home string) *Service {
	return &Service{st: st, UnitDir: "/etc/systemd/system", ScriptDir: filepath.Join(home, "data", "proc"),
		systemctl: func(ctx context.Context, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
			if err != nil {
				return string(out), fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			}
			return string(out), nil
		},
		journal: func(ctx context.Context, unit string, n int) (string, error) {
			out, err := exec.CommandContext(ctx, "journalctl", "-u", unit, "-n", strconv.Itoa(n), "--no-pager", "-o", "short-iso").CombinedOutput()
			return string(out), err
		},
	}
}

func Unit(name string) string { return "lmcwhm-proc-" + name + ".service" }

func (sv *Service) query(ctx context.Context, where string, args ...any) ([]Process, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, name, command, dir, user, env, remark, created_at FROM processes `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Process{}
	for rows.Next() {
		var p Process
		var env string
		if err := rows.Scan(&p.ID, &p.Name, &p.Command, &p.Dir, &p.User, &env, &p.Remark, &p.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(env), &p.Env)
		if p.Env == nil {
			p.Env = []string{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (sv *Service) status(ctx context.Context, p *Process) {
	out, _ := sv.systemctl(ctx, "show", Unit(p.Name), "-p", "ActiveState,SubState,MainPID,ActiveEnterTimestampMonotonic,NRestarts,MemoryCurrent,ActiveEnterTimestamp")
	for _, l := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), "=")
		switch k {
		case "ActiveState":
			p.State = v
		case "SubState":
			p.SubState = v
		case "MainPID":
			p.PID, _ = strconv.Atoi(v)
		case "NRestarts":
			p.Restarts, _ = strconv.Atoi(v)
		case "MemoryCurrent":
			p.MemBytes, _ = strconv.ParseInt(v, 10, 64) // "[not set]" → 0
		case "ActiveEnterTimestamp":
			if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", v); err == nil {
				p.Since = t.Unix()
			}
		}
	}
}

func (sv *Service) List(ctx context.Context) ([]Process, error) {
	list, err := sv.query(ctx, "")
	for i := range list {
		sv.status(ctx, &list[i])
	}
	return list, err
}

func (sv *Service) Get(ctx context.Context, id int64) (Process, error) {
	list, err := sv.query(ctx, "WHERE id = ?", id)
	if err != nil {
		return Process{}, err
	}
	if len(list) == 0 {
		return Process{}, ErrNotFound
	}
	sv.status(ctx, &list[0])
	return list[0], nil
}

func (p *Process) validate() error {
	p.Name, p.Command, p.Dir, p.User = strings.TrimSpace(p.Name), strings.TrimSpace(p.Command), filepath.Clean(strings.TrimSpace(p.Dir)), strings.TrimSpace(p.User)
	if !nameRe.MatchString(p.Name) {
		return ErrName
	}
	if p.Command == "" || len(p.Command) > 8192 {
		return ErrCommand
	}
	if p.Dir == "" || p.Dir == "." {
		p.Dir = "/"
	}
	if !filepath.IsAbs(p.Dir) {
		return ErrDir
	}
	if fi, err := os.Stat(p.Dir); err != nil || !fi.IsDir() {
		return ErrDir
	}
	if p.User == "" {
		p.User = "root"
	}
	if _, err := user.Lookup(p.User); err != nil {
		return ErrUser
	}
	for _, e := range p.Env {
		if !envRe.MatchString(e) {
			return ErrEnv
		}
	}
	if p.Env == nil {
		p.Env = []string{}
	}
	return nil
}

// systemdQuote quotes a value for an Environment= assignment.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	return `"` + r.Replace(s) + `"`
}

func (sv *Service) unitFile(p Process) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Managed by lmcwhm (進程守護) — edit in the panel.\n[Unit]\nDescription=lmcwhm process %s\nAfter=network-online.target docker.service\nWants=network-online.target\n\n", p.Name)
	fmt.Fprintf(&b, "[Service]\nType=simple\nUser=%s\nWorkingDirectory=%s\nExecStart=/bin/bash %s\n", p.User, p.Dir, sv.script(p.Name))
	for _, e := range p.Env {
		fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(e))
	}
	b.WriteString("Restart=always\nRestartSec=3\nStartLimitIntervalSec=0\nKillMode=mixed\nTimeoutStopSec=20\n\n[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

func (sv *Service) script(name string) string { return filepath.Join(sv.ScriptDir, name+".sh") }

func (sv *Service) write(p Process) error {
	if err := os.MkdirAll(sv.ScriptDir, 0o755); err != nil {
		return err
	}
	// The command runs from a script so it needs no systemd quoting at all.
	if err := os.WriteFile(sv.script(p.Name), []byte("#!/bin/bash\n# lmcwhm process "+p.Name+"\n"+p.Command+"\n"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sv.UnitDir, Unit(p.Name)), []byte(sv.unitFile(p)), 0o644)
}

// Save creates or updates a process and (re)starts it.
func (sv *Service) Save(ctx context.Context, p Process) (Process, error) {
	if err := p.validate(); err != nil {
		return p, err
	}
	env, _ := json.Marshal(p.Env)
	if p.ID == 0 {
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO processes(name, command, dir, user, env, remark, created_at) VALUES(?,?,?,?,?,?,?)`,
			p.Name, p.Command, p.Dir, p.User, string(env), p.Remark, time.Now().Unix())
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return p, ErrExists
			}
			return p, err
		}
		p.ID, _ = res.LastInsertId()
	} else {
		old, err := sv.Get(ctx, p.ID)
		if err != nil {
			return p, err
		}
		if old.Name != p.Name { // renamed: remove the old unit
			sv.removeUnit(ctx, old.Name)
		}
		if _, err := sv.st.DB.ExecContext(ctx, `UPDATE processes SET name=?, command=?, dir=?, user=?, env=?, remark=? WHERE id=?`,
			p.Name, p.Command, p.Dir, p.User, string(env), p.Remark, p.ID); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return p, ErrExists
			}
			return p, err
		}
	}
	if err := sv.write(p); err != nil {
		return p, err
	}
	if _, err := sv.systemctl(ctx, "daemon-reload"); err != nil {
		return p, err
	}
	if _, err := sv.systemctl(ctx, "enable", Unit(p.Name)); err != nil {
		return p, err
	}
	if _, err := sv.systemctl(ctx, "restart", Unit(p.Name)); err != nil {
		return p, err
	}
	return sv.Get(ctx, p.ID)
}

func (sv *Service) removeUnit(ctx context.Context, name string) {
	sv.systemctl(ctx, "disable", "--now", Unit(name))
	os.Remove(filepath.Join(sv.UnitDir, Unit(name)))
	os.Remove(sv.script(name))
	sv.systemctl(ctx, "daemon-reload")
	sv.systemctl(ctx, "reset-failed", Unit(name))
}

func (sv *Service) Delete(ctx context.Context, id int64) error {
	p, err := sv.Get(ctx, id)
	if err != nil {
		return err
	}
	sv.removeUnit(ctx, p.Name)
	_, err = sv.st.DB.ExecContext(ctx, `DELETE FROM processes WHERE id = ?`, id)
	return err
}

// Action starts, stops or restarts a process.
func (sv *Service) Action(ctx context.Context, id int64, action string) error {
	if action != "start" && action != "stop" && action != "restart" {
		return ErrCommand
	}
	p, err := sv.Get(ctx, id)
	if err != nil {
		return err
	}
	_, err = sv.systemctl(ctx, action, Unit(p.Name))
	return err
}

func (sv *Service) Logs(ctx context.Context, id int64, n int) (string, error) {
	p, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return sv.journal(ctx, Unit(p.Name), n)
}

// Boot rewrites units that went missing (e.g. after a panel restore).
func (sv *Service) Boot(ctx context.Context) {
	list, err := sv.query(ctx, "")
	if err != nil {
		return
	}
	changed := false
	for _, p := range list {
		if _, err := os.Stat(filepath.Join(sv.UnitDir, Unit(p.Name))); err == nil {
			continue
		}
		if sv.write(p) == nil {
			changed = true
		}
	}
	if changed {
		sv.systemctl(ctx, "daemon-reload")
		for _, p := range list {
			sv.systemctl(ctx, "enable", "--now", Unit(p.Name))
		}
	}
}
