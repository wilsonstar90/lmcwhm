// Package probe runs uptime checks (撥測): HTTP(S) with status / keyword
// checks and certificate expiry, TCP connects and ICMP ping, keeps their
// history and reports up / down transitions.
package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

var (
	ErrNotFound = errors.New("probe.not_found")
	ErrInvalid  = errors.New("probe.invalid")
	hostRe      = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]+$`)
	keepDays    = 30
	pingLatency = regexp.MustCompile(`time[=<]([\d.]+) ?ms`)
)

type Probe struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"` // http | tcp | ping
	Target       string `json:"target"`
	Interval     int    `json:"interval"` // seconds
	Timeout      int    `json:"timeout"`  // seconds
	Keyword      string `json:"keyword"`
	ExpectStatus int    `json:"expect_status"`
	Fails        int    `json:"fails"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    int64  `json:"created_at"`
	// live state
	State     string  `json:"state"` // up | down | unknown
	LastCheck int64   `json:"last_check"`
	Latency   int64   `json:"latency"` // ms
	LastError string  `json:"last_error"`
	CertDays  int     `json:"cert_days"` // HTTPS: days until the certificate expires (-1 = n/a)
	Uptime24h float64 `json:"uptime_24h"`
	Uptime7d  float64 `json:"uptime_7d"`
	Since     int64   `json:"since"` // current state since
}

type Result struct {
	T       int64  `json:"t"`
	OK      bool   `json:"ok"`
	Latency int64  `json:"latency"`
	Error   string `json:"error,omitempty"`
}

type state struct {
	state     string
	since     int64
	last      Result
	certDays  int
	failures  int
	nextCheck time.Time
}

// Notify is told about up / down transitions.
type Notify func(p Probe, down bool, detail string)

type Service struct {
	st     *store.Store
	Notify Notify
	mu     sync.Mutex
	live   map[int64]*state
	now    func() time.Time
}

func New(st *store.Store) *Service {
	return &Service{st: st, live: map[int64]*state{}, now: time.Now}
}

const cols = `id, name, type, target, interval_s, timeout_s, keyword, expect_status, fails, enabled, created_at`

func (sv *Service) query(ctx context.Context, where string, args ...any) ([]Probe, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT `+cols+` FROM probes `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Probe{}
	for rows.Next() {
		var p Probe
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.Target, &p.Interval, &p.Timeout, &p.Keyword, &p.ExpectStatus, &p.Fails, &p.Enabled, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (sv *Service) decorate(ctx context.Context, p *Probe) {
	sv.mu.Lock()
	if s := sv.live[p.ID]; s != nil {
		p.State, p.Since, p.LastCheck, p.Latency, p.LastError, p.CertDays = s.state, s.since, s.last.T, s.last.Latency, s.last.Error, s.certDays
	} else {
		p.State, p.CertDays = "unknown", -1
	}
	sv.mu.Unlock()
	now := sv.now().Unix()
	uptime := func(since int64) float64 {
		var total, ok int
		sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(ok), 0) FROM probe_results WHERE probe_id = ? AND t >= ?`, p.ID, since).Scan(&total, &ok)
		if total == 0 {
			return -1
		}
		return float64(ok) * 100 / float64(total)
	}
	p.Uptime24h, p.Uptime7d = uptime(now-86400), uptime(now-7*86400)
}

func (sv *Service) List(ctx context.Context) ([]Probe, error) {
	list, err := sv.query(ctx, "")
	for i := range list {
		sv.decorate(ctx, &list[i])
	}
	return list, err
}

func (sv *Service) Get(ctx context.Context, id int64) (Probe, error) {
	list, err := sv.query(ctx, "WHERE id = ?", id)
	if err != nil {
		return Probe{}, err
	}
	if len(list) == 0 {
		return Probe{}, ErrNotFound
	}
	sv.decorate(ctx, &list[0])
	return list[0], nil
}

func (p *Probe) validate() error {
	p.Name, p.Target = strings.TrimSpace(p.Name), strings.TrimSpace(p.Target)
	if p.Name == "" || len(p.Name) > 64 || len(p.Keyword) > 200 {
		return ErrInvalid
	}
	switch p.Type {
	case "http":
		u, err := url.Parse(p.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return ErrInvalid
		}
	case "tcp":
		host, port, err := net.SplitHostPort(p.Target)
		if n, _ := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || !hostRe.MatchString(host) {
			return ErrInvalid
		}
	case "ping":
		if !hostRe.MatchString(p.Target) || strings.HasPrefix(p.Target, "-") {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if p.Interval == 0 {
		p.Interval = 60
	}
	if p.Timeout == 0 {
		p.Timeout = 10
	}
	if p.Fails == 0 {
		p.Fails = 2
	}
	if p.Interval < 30 || p.Interval > 86400 || p.Timeout < 1 || p.Timeout > 60 || p.Fails < 1 || p.Fails > 10 || p.ExpectStatus < 0 || p.ExpectStatus > 599 {
		return ErrInvalid
	}
	return nil
}

func (sv *Service) Save(ctx context.Context, p Probe) (Probe, error) {
	if err := p.validate(); err != nil {
		return p, err
	}
	if p.ID == 0 {
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO probes(name, type, target, interval_s, timeout_s, keyword, expect_status, fails, enabled, created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, p.Name, p.Type, p.Target, p.Interval, p.Timeout, p.Keyword, p.ExpectStatus, p.Fails, p.Enabled, sv.now().Unix())
		if err != nil {
			return p, err
		}
		p.ID, _ = res.LastInsertId()
	} else {
		res, err := sv.st.DB.ExecContext(ctx, `UPDATE probes SET name=?, type=?, target=?, interval_s=?, timeout_s=?, keyword=?, expect_status=?, fails=?, enabled=? WHERE id=?`,
			p.Name, p.Type, p.Target, p.Interval, p.Timeout, p.Keyword, p.ExpectStatus, p.Fails, p.Enabled, p.ID)
		if err != nil {
			return p, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return p, ErrNotFound
		}
	}
	sv.mu.Lock()
	if s := sv.live[p.ID]; s != nil {
		s.nextCheck = time.Time{} // check the new settings right away
	}
	sv.mu.Unlock()
	return sv.Get(ctx, p.ID)
}

func (sv *Service) Delete(ctx context.Context, id int64) error {
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM probes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	sv.st.DB.ExecContext(ctx, `DELETE FROM probe_results WHERE probe_id = ?`, id)
	sv.mu.Lock()
	delete(sv.live, id)
	sv.mu.Unlock()
	return nil
}

// Results returns a probe's checks since a time, oldest first, thinned to at most 500 points.
func (sv *Service) Results(ctx context.Context, id int64, since int64) ([]Result, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT t, ok, latency_ms, error FROM probe_results WHERE probe_id = ? AND t >= ? ORDER BY t`, id, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := []Result{}
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.T, &r.OK, &r.Latency, &r.Error); err != nil {
			return nil, err
		}
		all = append(all, r)
	}
	if step := len(all)/500 + 1; step > 1 {
		thin := all[:0:0]
		for i := 0; i < len(all); i += step {
			// keep any failure inside the bucket visible
			r := all[i]
			for j := i; j < min(i+step, len(all)); j++ {
				if !all[j].OK {
					r = all[j]
					break
				}
			}
			thin = append(thin, r)
		}
		all = thin
	}
	return all, rows.Err()
}

// ---- checking ----

// Check runs one probe now.
func Check(ctx context.Context, p Probe) (Result, int) {
	start := time.Now()
	r := Result{T: start.Unix()}
	certDays := -1
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.Timeout)*time.Second)
	defer cancel()
	var err error
	switch p.Type {
	case "http":
		certDays, err = checkHTTP(ctx, p)
	case "tcp":
		var c net.Conn
		c, err = (&net.Dialer{}).DialContext(ctx, "tcp", p.Target)
		if err == nil {
			c.Close()
		}
	case "ping":
		var ms float64
		ms, err = ping(ctx, p.Target, p.Timeout)
		if err == nil {
			r.OK, r.Latency = true, int64(ms)
			return r, certDays
		}
	}
	r.Latency = time.Since(start).Milliseconds()
	if err != nil {
		r.Error = err.Error()
		if len(r.Error) > 300 {
			r.Error = r.Error[:300]
		}
	} else {
		r.OK = true
	}
	return r, certDays
}

var client = &http.Client{
	Transport: &http.Transport{TLSClientConfig: &tls.Config{}, DisableKeepAlives: true},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

func checkHTTP(ctx context.Context, p Probe) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Target, nil)
	if err != nil {
		return -1, err
	}
	req.Header.Set("User-Agent", "lmcwhm-probe/1.0")
	res, err := client.Do(req)
	if err != nil {
		return -1, err
	}
	defer res.Body.Close()
	certDays := -1
	if res.TLS != nil && len(res.TLS.PeerCertificates) > 0 {
		certDays = int(time.Until(res.TLS.PeerCertificates[0].NotAfter).Hours() / 24)
	}
	if p.ExpectStatus != 0 && res.StatusCode != p.ExpectStatus {
		return certDays, fmt.Errorf("HTTP %d, expected %d", res.StatusCode, p.ExpectStatus)
	}
	if p.ExpectStatus == 0 && res.StatusCode >= 400 {
		return certDays, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if p.Keyword != "" {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		if !strings.Contains(string(body), p.Keyword) {
			return certDays, fmt.Errorf("keyword %q not found", p.Keyword)
		}
	}
	return certDays, nil
}

func ping(ctx context.Context, host string, timeout int) (float64, error) {
	bin := "ping"
	if strings.Contains(host, ":") {
		bin = "ping6"
		if _, err := exec.LookPath(bin); err != nil {
			bin = "ping"
		}
	}
	out, err := exec.CommandContext(ctx, bin, "-c", "1", "-W", strconv.Itoa(timeout), "-n", host).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		return 0, fmt.Errorf("ping: %v %s", err, msg)
	}
	m := pingLatency.FindSubmatch(out)
	if m == nil {
		return 0, nil
	}
	v, _ := strconv.ParseFloat(string(m[1]), 64)
	return v, nil
}

// Run checks due probes every few seconds until ctx ends.
func (sv *Service) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-prune.C:
			sv.st.DB.ExecContext(ctx, `DELETE FROM probe_results WHERE t < ?`, sv.now().AddDate(0, 0, -keepDays).Unix())
		case <-t.C:
			list, err := sv.query(ctx, "WHERE enabled = 1")
			if err != nil {
				continue
			}
			for _, p := range list {
				if sv.due(p) {
					wg.Add(1)
					go func(p Probe) {
						defer wg.Done()
						sv.RunOnce(ctx, p)
					}(p)
				}
			}
		}
	}
}

// due marks a probe as scheduled and reports whether it should run now.
func (sv *Service) due(p Probe) bool {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	s := sv.live[p.ID]
	if s == nil {
		s = &state{state: "unknown", certDays: -1}
		sv.live[p.ID] = s
	}
	now := sv.now()
	if now.Before(s.nextCheck) {
		return false
	}
	s.nextCheck = now.Add(time.Duration(p.Interval) * time.Second)
	return true
}

// RunOnce checks a probe, stores the result and updates its state.
func (sv *Service) RunOnce(ctx context.Context, p Probe) Result {
	r, certDays := Check(ctx, p)
	sv.st.DB.ExecContext(ctx, `INSERT INTO probe_results(probe_id, t, ok, latency_ms, error) VALUES(?,?,?,?,?)`, p.ID, r.T, r.OK, r.Latency, r.Error)
	sv.mu.Lock()
	s := sv.live[p.ID]
	if s == nil {
		s = &state{state: "unknown"}
		sv.live[p.ID] = s
	}
	s.last, s.certDays = r, certDays
	prev := s.state
	if r.OK {
		s.failures = 0
		if s.state != "up" {
			s.state, s.since = "up", r.T
		}
	} else {
		s.failures++
		if s.failures >= p.Fails && s.state != "down" {
			s.state, s.since = "down", r.T
		}
	}
	changed := prev != s.state && !(prev == "unknown" && s.state == "up")
	cur := s.state
	sv.mu.Unlock()
	if changed && sv.Notify != nil {
		detail := r.Error
		if cur == "up" {
			detail = fmt.Sprintf("responding again (%d ms)", r.Latency)
		}
		sv.Notify(p, cur == "down", detail)
	}
	return r
}
