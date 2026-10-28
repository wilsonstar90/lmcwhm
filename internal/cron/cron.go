// Package cron runs scheduled jobs (aaPanel 計劃任務): shell scripts, URL
// requests and website log rotation, with a per-run output history.
package cron

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	rcron "github.com/robfig/cron/v3"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

var (
	ErrNotFound = errors.New("cron.not_found")
	ErrSchedule = errors.New("cron.invalid_schedule")
	ErrType     = errors.New("cron.invalid_type")
	ErrName     = errors.New("cron.invalid_name")
	ErrCommand  = errors.New("cron.invalid_command")
	ErrRunning  = errors.New("cron.running")
	ErrTimeout  = errors.New("cron.invalid_timeout")
	errTimedOut = errors.New("timed out")
	parser      = rcron.NewParser(rcron.SecondOptional | rcron.Minute | rcron.Hour | rcron.Dom | rcron.Month | rcron.Dow | rcron.Descriptor)
	maxOutput   = 64 << 10
	keepRuns    = 200
	nginxName   = "lmcwhm-nginx"
)

type Run struct {
	ID         int64  `json:"id"`
	JobID      int64  `json:"job_id"`
	StartedAt  int64  `json:"started_at"`
	DurationMS int64  `json:"duration_ms"`
	Status     string `json:"status"` // running | success | failed | timeout | skipped
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output,omitempty"`
	Manual     bool   `json:"manual"`
}

type Job struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // shell | url | logrotate
	Schedule  string `json:"schedule"`
	Command   string `json:"command"`
	Enabled   bool   `json:"enabled"`
	Timeout   int    `json:"timeout"` // seconds
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	Next      int64  `json:"next"`
	Running   bool   `json:"running"`
	Last      *Run   `json:"last,omitempty"`
}

// LogRotate is the command of a logrotate job.
type LogRotate struct {
	Keep int `json:"keep"` // rotated files kept per log
}

// BackupRunner executes "backup" jobs, whose command is a backup plan.
type BackupRunner interface {
	Validate(ctx context.Context, cmd string) error
	RunPlan(ctx context.Context, cmd string, jobID int64, out io.Writer) error
}

type Service struct {
	// Backups enables the "backup" job type.
	Backups BackupRunner
	// OnResult is told about every finished run (alerts).
	OnResult func(j Job, r Run)
	st       *store.Store
	LogDir   string // website logs (/www/wwwlogs)
	loc      *time.Location
	mu       sync.Mutex
	sched    *rcron.Cron
	entries  map[int64]rcron.EntryID
	running  map[int64]context.CancelFunc
}

func New(st *store.Store, logDir string, loc *time.Location) *Service {
	if loc == nil {
		loc = time.Local
	}
	return &Service{st: st, LogDir: logDir, loc: loc, entries: map[int64]rcron.EntryID{}, running: map[int64]context.CancelFunc{}}
}

// Next returns the next n run times of a schedule.
func (sv *Service) Next(schedule string, n int) ([]int64, error) {
	s, err := parser.Parse(strings.TrimSpace(schedule))
	if err != nil {
		return nil, ErrSchedule
	}
	out := []int64{}
	t := time.Now().In(sv.loc)
	for i := 0; i < n; i++ {
		t = s.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t.Unix())
	}
	return out, nil
}

// ---- persistence ----

const jobCols = `id, name, type, schedule, command, enabled, timeout, created_at, updated_at`

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Name, &j.Type, &j.Schedule, &j.Command, &j.Enabled, &j.Timeout, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

func (sv *Service) decorate(ctx context.Context, j *Job) {
	sv.mu.Lock()
	if id, ok := sv.entries[j.ID]; ok && sv.sched != nil {
		j.Next = sv.sched.Entry(id).Next.Unix()
	}
	_, j.Running = sv.running[j.ID]
	sv.mu.Unlock()
	var r Run
	err := sv.st.DB.QueryRowContext(ctx, `SELECT id, job_id, started_at, duration_ms, status, exit_code, manual FROM cron_runs WHERE job_id = ? ORDER BY id DESC LIMIT 1`, j.ID).
		Scan(&r.ID, &r.JobID, &r.StartedAt, &r.DurationMS, &r.Status, &r.ExitCode, &r.Manual)
	if err == nil {
		j.Last = &r
	}
}

func (sv *Service) List(ctx context.Context) ([]Job, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT `+jobCols+` FROM cron_jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, j)
	}
	rows.Close()
	for i := range out {
		sv.decorate(ctx, &out[i])
	}
	return out, nil
}

func (sv *Service) Get(ctx context.Context, id int64) (Job, error) {
	j, err := scanJob(sv.st.DB.QueryRowContext(ctx, `SELECT `+jobCols+` FROM cron_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	if err == nil {
		sv.decorate(ctx, &j)
	}
	return j, err
}

// Input creates or replaces a job.
type Input struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  bool   `json:"enabled"`
	Timeout  int    `json:"timeout"`
}

func (sv *Service) validate(ctx context.Context, in *Input) error {
	in.Name, in.Schedule = strings.TrimSpace(in.Name), strings.TrimSpace(in.Schedule)
	if in.Name == "" || len(in.Name) > 100 {
		return ErrName
	}
	if _, err := parser.Parse(in.Schedule); err != nil {
		return ErrSchedule
	}
	if in.Timeout == 0 {
		in.Timeout = 3600
	}
	if in.Timeout < 1 || in.Timeout > 7*24*3600 {
		return ErrTimeout
	}
	switch in.Type {
	case "shell":
		if strings.TrimSpace(in.Command) == "" || len(in.Command) > 64<<10 {
			return ErrCommand
		}
	case "url":
		in.Command = strings.TrimSpace(in.Command)
		if !strings.HasPrefix(in.Command, "http://") && !strings.HasPrefix(in.Command, "https://") || strings.ContainsAny(in.Command, " \n") {
			return ErrCommand
		}
	case "logrotate":
		var lr LogRotate
		if json.Unmarshal([]byte(in.Command), &lr) != nil || lr.Keep < 1 || lr.Keep > 365 {
			return ErrCommand
		}
	case "backup":
		if sv.Backups == nil {
			return ErrType
		}
		return sv.Backups.Validate(ctx, in.Command)
	default:
		return ErrType
	}
	return nil
}

func (sv *Service) Create(ctx context.Context, in Input) (Job, error) {
	if err := sv.validate(ctx, &in); err != nil {
		return Job{}, err
	}
	now := time.Now().Unix()
	res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO cron_jobs(name, type, schedule, command, enabled, timeout, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		in.Name, in.Type, in.Schedule, in.Command, in.Enabled, in.Timeout, now, now)
	if err != nil {
		return Job{}, err
	}
	id, _ := res.LastInsertId()
	sv.reschedule(ctx, id)
	return sv.Get(ctx, id)
}

func (sv *Service) Update(ctx context.Context, id int64, in Input) (Job, error) {
	if err := sv.validate(ctx, &in); err != nil {
		return Job{}, err
	}
	res, err := sv.st.DB.ExecContext(ctx, `UPDATE cron_jobs SET name=?, type=?, schedule=?, command=?, enabled=?, timeout=?, updated_at=? WHERE id=?`,
		in.Name, in.Type, in.Schedule, in.Command, in.Enabled, in.Timeout, time.Now().Unix(), id)
	if err != nil {
		return Job{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Job{}, ErrNotFound
	}
	sv.reschedule(ctx, id)
	return sv.Get(ctx, id)
}

func (sv *Service) SetEnabled(ctx context.Context, id int64, on bool) error {
	res, err := sv.st.DB.ExecContext(ctx, `UPDATE cron_jobs SET enabled=?, updated_at=? WHERE id=?`, on, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	sv.reschedule(ctx, id)
	return nil
}

func (sv *Service) Delete(ctx context.Context, id int64) error {
	sv.mu.Lock()
	if cancel, ok := sv.running[id]; ok {
		cancel()
	}
	if e, ok := sv.entries[id]; ok && sv.sched != nil {
		sv.sched.Remove(e)
		delete(sv.entries, id)
	}
	sv.mu.Unlock()
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (sv *Service) Runs(ctx context.Context, jobID int64, limit int) ([]Run, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, job_id, started_at, duration_ms, status, exit_code, manual,
		substr(output, 1, 200) FROM cron_runs WHERE job_id = ? ORDER BY id DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.JobID, &r.StartedAt, &r.DurationMS, &r.Status, &r.ExitCode, &r.Manual, &r.Output); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (sv *Service) Run(ctx context.Context, id int64) (Run, error) {
	var r Run
	err := sv.st.DB.QueryRowContext(ctx, `SELECT id, job_id, started_at, duration_ms, status, exit_code, manual, output FROM cron_runs WHERE id = ?`, id).
		Scan(&r.ID, &r.JobID, &r.StartedAt, &r.DurationMS, &r.Status, &r.ExitCode, &r.Manual, &r.Output)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// RecentRuns lists the latest runs of every job (log center).
func (sv *Service) RecentRuns(ctx context.Context, limit, offset int) ([]Run, int, error) {
	var total int
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM cron_runs`).Scan(&total)
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, job_id, started_at, duration_ms, status, exit_code, manual, substr(output, 1, 300)
		FROM cron_runs ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.JobID, &r.StartedAt, &r.DurationMS, &r.Status, &r.ExitCode, &r.Manual, &r.Output); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// ---- scheduling ----

// Start loads enabled jobs and runs the scheduler until ctx ends.
func (sv *Service) Start(ctx context.Context) error {
	// Runs cut short by a panel restart are not running any more.
	sv.st.DB.ExecContext(ctx, `UPDATE cron_runs SET status = 'failed', output = output || '\n[interrupted: panel restarted]' WHERE status = 'running'`)
	sv.mu.Lock()
	sv.sched = rcron.New(rcron.WithParser(parser), rcron.WithLocation(sv.loc))
	sv.mu.Unlock()
	jobs, err := sv.List(ctx)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		sv.reschedule(ctx, j.ID)
	}
	sv.sched.Start()
	go func() {
		<-ctx.Done()
		sv.sched.Stop()
	}()
	return nil
}

func (sv *Service) reschedule(ctx context.Context, id int64) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.sched == nil {
		return
	}
	if e, ok := sv.entries[id]; ok {
		sv.sched.Remove(e)
		delete(sv.entries, id)
	}
	var schedule string
	var enabled bool
	if sv.st.DB.QueryRowContext(ctx, `SELECT schedule, enabled FROM cron_jobs WHERE id = ?`, id).Scan(&schedule, &enabled) != nil || !enabled {
		return
	}
	s, err := parser.Parse(schedule)
	if err != nil {
		return
	}
	sv.entries[id] = sv.sched.Schedule(s, rcron.FuncJob(func() { sv.execute(context.Background(), id, false) }))
}

// RunNow starts a job immediately in the background.
func (sv *Service) RunNow(ctx context.Context, id int64) error {
	if _, err := sv.Get(ctx, id); err != nil {
		return err
	}
	sv.mu.Lock()
	_, busy := sv.running[id]
	sv.mu.Unlock()
	if busy {
		return ErrRunning
	}
	go sv.execute(context.Background(), id, true)
	return nil
}

// tail keeps the end of the output, which holds the result and errors.
type tail struct {
	mu  sync.Mutex
	buf []byte
	cut bool
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > maxOutput {
		t.buf, t.cut = t.buf[len(t.buf)-maxOutput:], true
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cut {
		return "[… earlier output truncated]\n" + string(t.buf)
	}
	return string(t.buf)
}

func (sv *Service) execute(parent context.Context, id int64, manual bool) {
	j, err := sv.Get(parent, id)
	if err != nil {
		return
	}
	start := time.Now()
	sv.mu.Lock()
	if _, busy := sv.running[id]; busy {
		sv.mu.Unlock()
		// The previous run is still going: record the skip instead of piling up.
		sv.st.DB.ExecContext(parent, `INSERT INTO cron_runs(job_id, started_at, status, output, manual) VALUES(?,?,?,?,?)`,
			id, start.Unix(), "skipped", "previous run still in progress", manual)
		return
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(j.Timeout)*time.Second)
	sv.running[id] = cancel
	sv.mu.Unlock()
	defer func() {
		cancel()
		sv.mu.Lock()
		delete(sv.running, id)
		sv.mu.Unlock()
	}()

	res, err := sv.st.DB.ExecContext(parent, `INSERT INTO cron_runs(job_id, started_at, status, manual) VALUES(?,?,?,?)`, id, start.Unix(), "running", manual)
	if err != nil {
		return
	}
	runID, _ := res.LastInsertId()
	out := &tail{}
	code, err := sv.dispatch(ctx, j, out)
	status := "success"
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		status = "timeout"
		fmt.Fprintf(out, "\n[timed out after %ds]", j.Timeout)
	case errors.Is(ctx.Err(), context.Canceled):
		status = "failed"
		fmt.Fprint(out, "\n[cancelled]")
	case err != nil:
		status = "failed"
		fmt.Fprintf(out, "\n[%v]", err)
	}
	r := Run{ID: runID, JobID: id, StartedAt: start.Unix(), DurationMS: time.Since(start).Milliseconds(), Status: status, ExitCode: code, Output: out.String(), Manual: manual}
	sv.st.DB.ExecContext(parent, `UPDATE cron_runs SET duration_ms=?, status=?, exit_code=?, output=? WHERE id=?`,
		r.DurationMS, status, code, r.Output, runID)
	if sv.OnResult != nil {
		sv.OnResult(j, r)
	}
	sv.st.DB.ExecContext(parent, `DELETE FROM cron_runs WHERE job_id = ? AND id <= (SELECT id FROM cron_runs WHERE job_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		id, id, keepRuns)
}

func (sv *Service) dispatch(ctx context.Context, j Job, out io.Writer) (int, error) {
	switch j.Type {
	case "shell":
		return runShell(ctx, j.Command, out)
	case "url":
		return 0, runURL(ctx, j.Command, out)
	case "logrotate":
		var lr LogRotate
		json.Unmarshal([]byte(j.Command), &lr)
		return 0, sv.rotate(ctx, lr.Keep, out)
	case "backup":
		if sv.Backups != nil {
			return 0, sv.Backups.RunPlan(ctx, j.Command, j.ID, out)
		}
	}
	return 0, ErrType
}

// runShell runs the script with bash as root in its own process group, so a
// timeout kills the whole tree.
func runShell(ctx context.Context, script string, out io.Writer) (int, error) {
	cmd := exec.Command("/bin/bash", "-c", script)
	// The panel runs as root, so this is /root; "/" when there is no usable home.
	cmd.Dir = "/"
	if home, err := os.UserHomeDir(); err == nil {
		if f, err := os.Open(home); err == nil {
			f.Close()
			cmd.Dir = home
		}
	}
	cmd.Env = append(os.Environ(), "HOME="+cmd.Dir, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), fmt.Errorf("exit status %d", ee.ExitCode())
		}
		return 0, err
	case <-ctx.Done():
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return -1, errTimedOut
	}
}

func runURL(ctx context.Context, url string, out io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "lmcwhm-cron")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	fmt.Fprintf(out, "HTTP %s\n", res.Status)
	io.Copy(out, io.LimitReader(res.Body, 16<<10))
	if res.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

// rotate gzips every non-empty website log, asks nginx to reopen its files
// and keeps the newest `keep` rotations of each log.
func (sv *Service) rotate(ctx context.Context, keep int, out io.Writer) error {
	logs, _ := filepath.Glob(filepath.Join(sv.LogDir, "*.log"))
	stamp := time.Now().In(sv.loc).Format("20060102-150405")
	var rotated []string
	for _, p := range logs {
		fi, err := os.Stat(p)
		if err != nil || fi.Size() == 0 {
			continue
		}
		dst := p + "." + stamp
		if err := os.Rename(p, dst); err != nil {
			return err
		}
		os.WriteFile(p, nil, fi.Mode().Perm())
		rotated = append(rotated, dst)
	}
	if len(rotated) > 0 && dockerx.State(ctx, nginxName) == "running" {
		if _, err := dockerx.Run(ctx, "exec", nginxName, "nginx", "-s", "reopen"); err != nil {
			fmt.Fprintf(out, "nginx reopen: %v\n", err)
		}
	}
	for _, p := range rotated {
		if err := gzipFile(p); err != nil {
			fmt.Fprintf(out, "%s: %v\n", filepath.Base(p), err)
			continue
		}
		fmt.Fprintf(out, "rotated %s\n", filepath.Base(p))
	}
	for _, p := range logs {
		old, _ := filepath.Glob(p + ".*.gz")
		sort.Sort(sort.Reverse(sort.StringSlice(old))) // timestamps sort lexically
		for i, o := range old {
			if i >= keep {
				os.Remove(o)
				fmt.Fprintf(out, "removed %s\n", filepath.Base(o))
			}
		}
	}
	if len(rotated) == 0 {
		fmt.Fprintln(out, "nothing to rotate")
	}
	return nil
}

func gzipFile(p string) error {
	in, err := os.Open(p)
	if err != nil {
		return err
	}
	defer in.Close()
	f, err := os.OpenFile(p+".gz", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	if _, err = io.Copy(gz, in); err == nil {
		err = gz.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(p + ".gz")
		return err
	}
	return os.Remove(p)
}
