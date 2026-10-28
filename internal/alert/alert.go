// Package alert is the alert center (告警中心): notification channels
// (email, Telegram, webhooks, WeCom, DingTalk, Slack, Discord), rules on
// resources, services, certificates, probes, cron/backup results and logins,
// and the history of what was sent.
package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

// Rule kinds. Metric kinds are checked every minute; event kinds are fired
// by other services through Emit.
const (
	KindCPU          = "cpu"          // threshold %, minutes
	KindMem          = "mem"          // threshold %, minutes
	KindLoad         = "load"         // threshold (1-minute load), minutes
	KindDisk         = "disk"         // threshold %
	KindServiceDown  = "service_down" // panel containers not running
	KindSSLExpiry    = "ssl_expiry"   // days
	KindProbeDown    = "probe_down"   // event
	KindCronFailed   = "cron_failed"  // event (includes backup jobs)
	KindLoginFailed  = "login_failed" // count, minutes
	KindPanelLogin   = "panel_login"  // every successful panel login
	KindSSHLogin     = "ssh_login"    // every accepted SSH login
	KindFirewallDrop = "firewall_ban" // event: an IP was banned automatically
	defaultCooldown  = 60             // minutes between repeats of an active alert
	keepEvents       = 2000
)

var (
	ErrNotFound = errors.New("alert.not_found")
	ErrRule     = errors.New("alert.invalid_rule")
	kinds       = map[string]bool{KindCPU: true, KindMem: true, KindLoad: true, KindDisk: true, KindServiceDown: true, KindSSLExpiry: true,
		KindProbeDown: true, KindCronFailed: true, KindLoginFailed: true, KindPanelLogin: true, KindSSHLogin: true}
)

type Channel struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	Kind      string        `json:"kind"`
	Config    ChannelConfig `json:"config"`
	Enabled   bool          `json:"enabled"`
	CreatedAt int64         `json:"created_at"`
}

type Params struct {
	Threshold float64 `json:"threshold,omitempty"`
	Minutes   int     `json:"minutes,omitempty"`
	Days      int     `json:"days,omitempty"`
	Count     int     `json:"count,omitempty"`
}

type Rule struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Params    Params  `json:"params"`
	Channels  []int64 `json:"channels"`
	Enabled   bool    `json:"enabled"`
	Cooldown  int     `json:"cooldown"` // minutes
	CreatedAt int64   `json:"created_at"`
	Active    int     `json:"active"` // alerts currently firing
}

type Event struct {
	ID        int64  `json:"id"`
	RuleID    int64  `json:"rule_id"`
	RuleName  string `json:"rule_name"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	Severity  string `json:"severity"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Result    string `json:"result"` // per-channel delivery outcome
	CreatedAt int64  `json:"created_at"`
}

// Sources reads the state metric rules look at; nil functions disable a kind.
type Sources struct {
	Metrics       func(ctx context.Context, d time.Duration) (cpu, mem, load, disk float64, ok bool)
	Services      func(ctx context.Context) map[string]string // container -> state
	Certs         func(ctx context.Context) map[string]int64  // domain -> not-after
	LoginFailures func(ctx context.Context, since time.Time) (int, []string)
	PanelLogins   func(ctx context.Context, since time.Time) []string
	SSHLogins     func(ctx context.Context, since time.Time) []string
}

type firing struct {
	since, last time.Time
}

type Service struct {
	st   *store.Store
	box  *secrets.Box
	Src  Sources
	Host string
	now  func() time.Time
	mu   sync.Mutex
	// active alerts per rule and key, for repeats and "resolved" notices
	active map[int64]map[string]*firing
	// event cursors for login rules
	seen map[int64]time.Time
}

func New(st *store.Store, box *secrets.Box) *Service {
	host, _ := os.Hostname()
	return &Service{st: st, box: box, Host: host, now: time.Now, active: map[int64]map[string]*firing{}, seen: map[int64]time.Time{}}
}

// ---- channels ----

func (sv *Service) channels(ctx context.Context, where string, args ...any) ([]Channel, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, name, kind, config, enabled, created_at FROM alert_channels `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		var c Channel
		var sealed string
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &sealed, &c.Enabled, &c.CreatedAt); err != nil {
			return nil, err
		}
		plain, err := sv.box.Open(sealed)
		if err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(plain), &c.Config)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Channels lists channels with secrets masked.
func (sv *Service) Channels(ctx context.Context) ([]Channel, error) {
	list, err := sv.channels(ctx, "")
	for i := range list {
		list[i].Config.redact()
	}
	return list, err
}

func (sv *Service) channel(ctx context.Context, id int64) (Channel, error) {
	list, err := sv.channels(ctx, "WHERE id = ?", id)
	if err != nil {
		return Channel{}, err
	}
	if len(list) == 0 {
		return Channel{}, ErrNotFound
	}
	return list[0], nil
}

// SaveChannel creates (id 0) or updates a channel; masked secrets keep their value.
func (sv *Service) SaveChannel(ctx context.Context, c Channel) (Channel, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len(c.Name) > 64 {
		return c, ErrChannel
	}
	if c.ID != 0 {
		old, err := sv.channel(ctx, c.ID)
		if err != nil {
			return c, err
		}
		c.Config.keepSecrets(old.Config)
	}
	if err := validate(c.Kind, &c.Config); err != nil {
		return c, err
	}
	b, _ := json.Marshal(c.Config)
	sealed := sv.box.Seal(string(b))
	if c.ID == 0 {
		c.CreatedAt = sv.now().Unix()
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO alert_channels(name, kind, config, enabled, created_at) VALUES(?,?,?,?,?)`,
			c.Name, c.Kind, sealed, c.Enabled, c.CreatedAt)
		if err != nil {
			return c, err
		}
		c.ID, _ = res.LastInsertId()
	} else if _, err := sv.st.DB.ExecContext(ctx, `UPDATE alert_channels SET name=?, kind=?, config=?, enabled=? WHERE id=?`,
		c.Name, c.Kind, sealed, c.Enabled, c.ID); err != nil {
		return c, err
	}
	c.Config.redact()
	return c, nil
}

func (sv *Service) DeleteChannel(ctx context.Context, id int64) error {
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM alert_channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TestChannel sends a test message.
func (sv *Service) TestChannel(ctx context.Context, id int64) error {
	c, err := sv.channel(ctx, id)
	if err != nil {
		return err
	}
	return send(ctx, c.Kind, c.Config, Message{Title: "lmcwhm test notification", Body: "If you can read this, the channel works.",
		Severity: "info", Kind: "test", Host: sv.Host, Time: sv.now().Unix()})
}

// ---- rules ----

func (sv *Service) Rules(ctx context.Context) ([]Rule, error) {
	return sv.rules(ctx, "")
}

func (sv *Service) rules(ctx context.Context, where string, args ...any) ([]Rule, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, name, kind, params, channels, enabled, cooldown, created_at FROM alert_rules `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		var params, chans string
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &params, &chans, &r.Enabled, &r.Cooldown, &r.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(params), &r.Params)
		json.Unmarshal([]byte(chans), &r.Channels)
		if r.Channels == nil {
			r.Channels = []int64{}
		}
		sv.mu.Lock()
		r.Active = len(sv.active[r.ID])
		sv.mu.Unlock()
		out = append(out, r)
	}
	return out, rows.Err()
}

func checkParams(r *Rule) error {
	p := &r.Params
	switch r.Kind {
	case KindCPU, KindMem:
		if p.Threshold <= 0 || p.Threshold > 100 {
			return ErrRule
		}
		p.Minutes = max(1, min(p.Minutes, 60))
	case KindLoad:
		if p.Threshold <= 0 {
			return ErrRule
		}
		p.Minutes = max(1, min(p.Minutes, 60))
	case KindDisk:
		if p.Threshold <= 0 || p.Threshold > 100 {
			return ErrRule
		}
	case KindSSLExpiry:
		if p.Days < 1 || p.Days > 90 {
			return ErrRule
		}
	case KindLoginFailed:
		if p.Count < 1 {
			return ErrRule
		}
		p.Minutes = max(1, min(p.Minutes, 1440))
	}
	return nil
}

func (sv *Service) SaveRule(ctx context.Context, r Rule) (Rule, error) {
	r.Name = strings.TrimSpace(r.Name)
	if !kinds[r.Kind] || r.Name == "" || len(r.Name) > 64 || len(r.Channels) == 0 {
		return r, ErrRule
	}
	if err := checkParams(&r); err != nil {
		return r, err
	}
	for _, id := range r.Channels {
		if _, err := sv.channel(ctx, id); err != nil {
			return r, ErrRule
		}
	}
	if r.Cooldown <= 0 {
		r.Cooldown = defaultCooldown
	}
	params, _ := json.Marshal(r.Params)
	chans, _ := json.Marshal(r.Channels)
	if r.ID == 0 {
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO alert_rules(name, kind, params, channels, enabled, cooldown, created_at) VALUES(?,?,?,?,?,?,?)`,
			r.Name, r.Kind, string(params), string(chans), r.Enabled, r.Cooldown, sv.now().Unix())
		if err != nil {
			return r, err
		}
		r.ID, _ = res.LastInsertId()
	} else {
		res, err := sv.st.DB.ExecContext(ctx, `UPDATE alert_rules SET name=?, kind=?, params=?, channels=?, enabled=?, cooldown=? WHERE id=?`,
			r.Name, r.Kind, string(params), string(chans), r.Enabled, r.Cooldown, r.ID)
		if err != nil {
			return r, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return r, ErrNotFound
		}
		sv.mu.Lock()
		delete(sv.active, r.ID) // thresholds changed: start over
		sv.mu.Unlock()
	}
	return r, nil
}

func (sv *Service) DeleteRule(ctx context.Context, id int64) error {
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	sv.mu.Lock()
	delete(sv.active, id)
	sv.mu.Unlock()
	return nil
}

func (sv *Service) Events(ctx context.Context, limit, offset int) ([]Event, int, error) {
	var total int
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_events`).Scan(&total)
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, rule_id, rule_name, kind, key, severity, title, body, result, created_at
		FROM alert_events ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.RuleID, &e.RuleName, &e.Kind, &e.Key, &e.Severity, &e.Title, &e.Body, &e.Result, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// ---- delivery ----

// deliver sends one message for a rule to all its enabled channels and records it.
func (sv *Service) deliver(ctx context.Context, r Rule, key, severity, title, body string) {
	m := Message{Title: title, Body: body, Severity: severity, Kind: r.Kind, Host: sv.Host, Time: sv.now().Unix()}
	var results []string
	for _, id := range r.Channels {
		c, err := sv.channel(ctx, id)
		if err != nil || !c.Enabled {
			continue
		}
		if err := send(ctx, c.Kind, c.Config, m); err != nil {
			results = append(results, c.Name+": "+err.Error())
		} else {
			results = append(results, c.Name+": ok")
		}
	}
	sv.st.DB.ExecContext(ctx, `INSERT INTO alert_events(rule_id, rule_name, kind, key, severity, title, body, result, created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Name, r.Kind, key, severity, title, body, strings.Join(results, "; "), m.Time)
	sv.st.DB.ExecContext(ctx, `DELETE FROM alert_events WHERE id <= (SELECT id FROM alert_events ORDER BY id DESC LIMIT 1 OFFSET ?)`, keepEvents)
}

// raise reports a condition that is (still) true; it notifies on first sight
// and then once per cooldown.
func (sv *Service) raise(ctx context.Context, r Rule, key, severity, title, body string) {
	now := sv.now()
	sv.mu.Lock()
	m := sv.active[r.ID]
	if m == nil {
		m = map[string]*firing{}
		sv.active[r.ID] = m
	}
	f := m[key]
	notify := f == nil || now.Sub(f.last) >= time.Duration(r.Cooldown)*time.Minute
	if f == nil {
		f = &firing{since: now}
		m[key] = f
	}
	if notify {
		f.last = now
	}
	sv.mu.Unlock()
	if notify {
		sv.deliver(ctx, r, key, severity, title, body)
	}
}

// clear reports a condition that is no longer true; an active alert is resolved.
func (sv *Service) clear(ctx context.Context, r Rule, key, title, body string) {
	sv.mu.Lock()
	f := sv.active[r.ID][key]
	if f != nil {
		delete(sv.active[r.ID], key)
	}
	sv.mu.Unlock()
	if f != nil {
		sv.deliver(ctx, r, key, "resolved", title, fmt.Sprintf("%s (after %s)", body, sv.now().Sub(f.since).Round(time.Second)))
	}
}

// Emit fires event-kind rules (probe_down, cron_failed, firewall_ban).
// resolved=true closes an earlier alert with the same key.
func (sv *Service) Emit(ctx context.Context, kind, key, severity, title, body string, resolved bool) {
	rules, err := sv.rules(ctx, "WHERE kind = ? AND enabled = 1", kind)
	if err != nil {
		return
	}
	for _, r := range rules {
		if resolved {
			sv.clear(ctx, r, key, title, body)
		} else if kind == KindCronFailed || kind == KindFirewallDrop {
			sv.deliver(ctx, r, key, severity, title, body) // one-off events: no repeat state
		} else {
			sv.raise(ctx, r, key, severity, title, body)
		}
	}
}

// ---- periodic checks ----

// Run evaluates metric rules every minute until ctx ends.
func (sv *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sv.Check(ctx)
		}
	}
}

// Check evaluates every enabled metric rule once.
func (sv *Service) Check(ctx context.Context) {
	rules, err := sv.rules(ctx, "WHERE enabled = 1")
	if err != nil {
		return
	}
	for _, r := range rules {
		sv.check(ctx, r)
	}
}

func (sv *Service) check(ctx context.Context, r Rule) {
	src, p := sv.Src, r.Params
	metric := func(name string, pick func(cpu, mem, load, disk float64) float64, unit string) {
		if src.Metrics == nil {
			return
		}
		mins := max(p.Minutes, 1)
		cpu, mem, load, disk, ok := src.Metrics(ctx, time.Duration(mins)*time.Minute)
		if !ok {
			return
		}
		v := pick(cpu, mem, load, disk)
		title := fmt.Sprintf("%s %.1f%s ≥ %.1f%s", name, v, unit, p.Threshold, unit)
		if r.Kind != KindDisk {
			title += fmt.Sprintf(" (%d min avg)", mins)
		}
		if v >= p.Threshold {
			sv.raise(ctx, r, "", "warning", title, r.Name)
		} else {
			sv.clear(ctx, r, "", fmt.Sprintf("%s back to %.1f%s", name, v, unit), r.Name)
		}
	}
	switch r.Kind {
	case KindCPU:
		metric("CPU", func(c, _, _, _ float64) float64 { return c }, "%")
	case KindMem:
		metric("Memory", func(_, m, _, _ float64) float64 { return m }, "%")
	case KindLoad:
		metric("Load", func(_, _, l, _ float64) float64 { return l }, "")
	case KindDisk:
		metric("Disk", func(_, _, _, d float64) float64 { return d }, "%")
	case KindServiceDown:
		if src.Services == nil {
			return
		}
		states := src.Services(ctx)
		for name, st := range states {
			if st != "running" {
				sv.raise(ctx, r, name, "critical", "Service down: "+name, "state: "+st)
			} else {
				sv.clear(ctx, r, name, "Service up: "+name, "running again")
			}
		}
		sv.mu.Lock()
		var gone []string
		for key := range sv.active[r.ID] {
			if _, ok := states[key]; !ok {
				gone = append(gone, key)
			}
		}
		sv.mu.Unlock()
		for _, key := range gone { // removed on purpose
			sv.clear(ctx, r, key, "Service removed: "+key, "no longer exists")
		}
	case KindSSLExpiry:
		if src.Certs == nil {
			return
		}
		for domain, notAfter := range src.Certs(ctx) {
			if notAfter == 0 {
				continue
			}
			left := time.Until(time.Unix(notAfter, 0))
			days := int(left.Hours() / 24)
			if left < time.Duration(p.Days)*24*time.Hour {
				sev := "warning"
				if days < 3 {
					sev = "critical"
				}
				sv.raise(ctx, r, domain, sev, fmt.Sprintf("SSL for %s expires in %d days", domain, days),
					"expires "+time.Unix(notAfter, 0).Format("2006-01-02")+"; automatic renewal may be failing")
			} else {
				sv.clear(ctx, r, domain, "SSL renewed: "+domain, fmt.Sprintf("valid for %d more days", days))
			}
		}
	case KindLoginFailed:
		if src.LoginFailures == nil {
			return
		}
		n, ips := src.LoginFailures(ctx, sv.now().Add(-time.Duration(p.Minutes)*time.Minute))
		if n >= p.Count {
			sv.raise(ctx, r, "", "warning", fmt.Sprintf("%d failed panel logins in %d min", n, p.Minutes), "from: "+strings.Join(ips, ", "))
		} else {
			sv.clear(ctx, r, "", "Failed panel logins back to normal", fmt.Sprintf("%d in %d min", n, p.Minutes))
		}
	case KindPanelLogin, KindSSHLogin:
		fn := src.PanelLogins
		what := "Panel login"
		if r.Kind == KindSSHLogin {
			fn, what = src.SSHLogins, "SSH login"
		}
		if fn == nil {
			return
		}
		sv.mu.Lock()
		since, ok := sv.seen[r.ID]
		now := sv.now()
		sv.seen[r.ID] = now
		sv.mu.Unlock()
		if !ok {
			return // first pass: only report logins from now on
		}
		for _, l := range fn(ctx, since) {
			sv.deliver(ctx, r, "", "info", what, l)
		}
	}
}
