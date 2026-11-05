// Package secevents keeps the block log: every request the security layers
// refused or flagged — at the edge (lists, website protection, dedicated
// rules, CC; from each site's access log) and by the WAF (the ModSecurity
// audit log) — read in as the logs grow and kept for 30 days. It also bans
// addresses that are refused too often (auto-ban).
package secevents

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
	"github.com/wilsonstar90/lmcwhm/internal/security"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

const (
	keepDays  = 30
	maxPerDay = 200000   // events kept per day (a scan floods the log)
	firstRead = 16 << 20 // a log seen for the first time is read from its last 16 MiB
)

type Event struct {
	ID       int64                `json:"id"`
	Time     int64                `json:"time"`
	IP       string               `json:"ip"`
	Host     string               `json:"host"`
	Site     string               `json:"site"`
	Method   string               `json:"method"`
	URI      string               `json:"uri"`
	UA       string               `json:"ua,omitempty"`
	Status   int                  `json:"status"`
	Action   string               `json:"action"` // blocked | detected | limited
	Source   string               `json:"source"` // waf | rule | list | ip | protection | cc
	Rule     string               `json:"rule"`   // CRS id, dedicated rule code or protection check
	Message  string               `json:"message,omitempty"`
	Severity string               `json:"severity,omitempty"`
	Messages []security.EventRule `json:"messages,omitempty"`
}

// EdgeEvent turns an access log line with a reason into an event.
func EdgeEvent(a logcenter.Access, domain string) (Event, bool) {
	e := Event{Time: a.Time, IP: a.IP, Host: a.Host, Site: domain, Method: a.Method, URI: a.Path, UA: a.UA, Status: a.Status}
	reason := a.Block
	e.Action = "blocked"
	if reason == "" {
		if a.Detect == "" {
			return e, false
		}
		reason, e.Action = a.Detect, "detected"
	}
	switch {
	case strings.HasPrefix(reason, "rule-"):
		e.Source, e.Rule = "rule", strings.TrimPrefix(reason, "rule-")
	case reason == "cc":
		e.Source, e.Rule, e.Action = "cc", "cc", "limited"
	case reason == "ip" || reason == "list":
		e.Source, e.Rule = reason, reason
	default:
		e.Source, e.Rule = "protection", reason
	}
	return e, true
}

// WAFEvent turns an audit log transaction into an event.
func WAFEvent(w security.Event, siteOf func(string) string) Event {
	e := Event{Time: w.Time, IP: w.IP, Host: w.Host, Method: w.Method, URI: w.URI, Status: w.Status, Source: "waf", Messages: w.Messages, Action: "detected"}
	if w.Blocked {
		e.Action = "blocked"
	}
	if siteOf != nil {
		e.Site = siteOf(strings.Split(w.Host, ":")[0])
	}
	if len(w.Messages) > 0 {
		e.Rule, e.Message, e.Severity = w.Messages[0].ID, w.Messages[0].Message, w.Messages[0].Severity
	}
	return e
}

type Service struct {
	st     *store.Store
	LogDir string
	WAFLog string
	// Sites lists the domains with an access log (passthrough sites have none).
	Sites func(ctx context.Context) []string
	// SiteOf maps a host name to its site's domain ("" if none).
	SiteOf func(ctx context.Context) func(host string) string
	// Ban blocks an address for ttl (auto-ban); Exempt reports addresses
	// never to ban (the allow list, the server's own, trusted ranges).
	Ban    func(ctx context.Context, ip string, ttl time.Duration, remark string) error
	Exempt func(ctx context.Context, ip string) bool
	now    func() time.Time
	mu     sync.Mutex
}

func New(st *store.Store, logDir, wafLog string) *Service {
	return &Service{st: st, LogDir: logDir, WAFLog: wafLog, now: time.Now}
}

// Run reads new log lines every 10 seconds until ctx ends.
func (sv *Service) Run(ctx context.Context, logf func(string, ...any)) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	last := time.Time{}
	for {
		sv.Collect(ctx, logf)
		if time.Since(last) > time.Hour {
			sv.prune(ctx)
			last = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Collect reads what the logs gained since the last call, stores the events
// and applies auto-ban to the addresses seen.
func (sv *Service) Collect(ctx context.Context, logf func(string, ...any)) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	var evs []Event
	if sv.Sites != nil {
		for _, d := range sv.Sites(ctx) {
			err := sv.read(ctx, filepath.Join(sv.LogDir, d+".log"), func(line string) {
				if e, ok := EdgeEvent(logcenter.ParseAccess(line), d); ok && e.Time > 0 {
					evs = append(evs, e)
				}
			})
			if err != nil && logf != nil {
				logf("block log", "domain", d, "err", err)
			}
		}
	}
	if sv.WAFLog != "" {
		var siteOf func(string) string
		if sv.SiteOf != nil {
			siteOf = sv.SiteOf(ctx)
		}
		err := sv.read(ctx, sv.WAFLog, func(line string) {
			if w, ok := security.ParseEvent(line); ok && w.Time > 0 {
				evs = append(evs, WAFEvent(w, siteOf))
			}
		})
		if err != nil && logf != nil {
			logf("block log", "file", sv.WAFLog, "err", err)
		}
	}
	if len(evs) == 0 {
		return
	}
	if err := sv.store(ctx, evs); err != nil && logf != nil {
		logf("block log", "err", err)
	}
	sv.autoBan(ctx, evs, logf)
}

// read calls fn with each complete line added to path since the last read.
func (sv *Service) read(ctx context.Context, path string, fn func(string)) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	var inode int64
	if s, ok := fi.Sys().(*syscall.Stat_t); ok {
		inode = int64(s.Ino)
	}
	var curInode, offset int64
	err = sv.st.DB.QueryRowContext(ctx, `SELECT inode, offset FROM sec_events_cursor WHERE path = ?`, path).Scan(&curInode, &offset)
	fresh := false
	switch {
	case err == sql.ErrNoRows:
		offset, fresh = max(0, fi.Size()-firstRead), true
	case err != nil:
		return err
	case curInode != inode || offset > fi.Size():
		offset = 0 // rotated or truncated
	}
	if offset == fi.Size() && !fresh {
		return nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	rd := bufio.NewReaderSize(f, 256<<10)
	if fresh && offset > 0 { // started mid-file: skip the partial line
		skipped, _ := rd.ReadString('\n')
		offset += int64(len(skipped))
	}
	for {
		line, err := rd.ReadString('\n')
		if err != nil { // an incomplete last line waits for the next read
			break
		}
		offset += int64(len(line))
		if l := strings.TrimRight(line, "\r\n"); l != "" {
			fn(l)
		}
	}
	_, err = sv.st.DB.ExecContext(ctx, `INSERT INTO sec_events_cursor(path, inode, offset) VALUES(?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET inode = excluded.inode, offset = excluded.offset`, path, inode, offset)
	return err
}

func (sv *Service) store(ctx context.Context, evs []Event) error {
	// Per-day cap: count what the newest day already holds.
	var today int64
	day := sv.now().Add(-24 * time.Hour).Unix()
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sec_events WHERE time > ?`, day).Scan(&today)
	tx, err := sv.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO sec_events(time, ip, host, site, method, uri, ua, status, action, source, rule, message, severity, messages)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range evs {
		if today >= maxPerDay {
			break
		}
		msgs := ""
		if len(e.Messages) > 0 {
			b, _ := json.Marshal(e.Messages)
			msgs = string(b)
		}
		if _, err := stmt.ExecContext(ctx, e.Time, e.IP, e.Host, e.Site, e.Method, clip(e.URI, 2000), clip(e.UA, 500), e.Status,
			e.Action, e.Source, e.Rule, clip(e.Message, 500), e.Severity, msgs); err != nil {
			return err
		}
		today++
	}
	return tx.Commit()
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (sv *Service) prune(ctx context.Context) {
	sv.st.DB.ExecContext(ctx, `DELETE FROM sec_events WHERE time < ?`, sv.now().AddDate(0, 0, -keepDays).Unix())
}

// ---- query ----

type Filter struct {
	From, To int64 // unix seconds, To exclusive (0 = now)
	Site     string
	Source   string
	Action   string
	IP       string
	Q        string
	Limit    int
	Offset   int
}

type Page struct {
	Items []Event `json:"items"`
	Total int64   `json:"total"`
	// Per source / action, for the filter counts.
	Sources map[string]int64 `json:"sources"`
	Actions map[string]int64 `json:"actions"`
	TopIPs  []IPCount        `json:"top_ips"`
	Oldest  int64            `json:"oldest"` // first day kept
}

type IPCount struct {
	IP   string `json:"ip"`
	Hits int64  `json:"hits"`
}

func (f Filter) where() (string, []any) {
	w := []string{"time >= ?"}
	args := []any{f.From}
	if f.To > 0 {
		w, args = append(w, "time < ?"), append(args, f.To)
	}
	add := func(cond string, v any) {
		w, args = append(w, cond), append(args, v)
	}
	if f.Site != "" {
		add("site = ?", f.Site)
	}
	if f.IP != "" {
		add("ip = ?", f.IP)
	}
	if q := strings.ToLower(strings.TrimSpace(f.Q)); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
		w = append(w, `(LOWER(ip) LIKE ? ESCAPE '\' OR LOWER(host) LIKE ? ESCAPE '\' OR LOWER(uri) LIKE ? ESCAPE '\' OR LOWER(ua) LIKE ? ESCAPE '\' OR LOWER(rule) LIKE ? ESCAPE '\' OR LOWER(message) LIKE ? ESCAPE '\')`)
		for range 6 {
			args = append(args, like)
		}
	}
	return strings.Join(w, " AND "), args
}

// Query lists the events of a filter, newest first, with counts.
func (sv *Service) Query(ctx context.Context, f Filter) (Page, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	p := Page{Items: []Event{}, Sources: map[string]int64{}, Actions: map[string]int64{}, TopIPs: []IPCount{}}
	base, args := f.where()
	// Source and action counts ignore their own filter (so each option shows its number).
	if rows, err := sv.st.DB.QueryContext(ctx, `SELECT source, action, COUNT(*) FROM sec_events WHERE `+base+` GROUP BY source, action`, args...); err == nil {
		for rows.Next() {
			var s, a string
			var n int64
			rows.Scan(&s, &a, &n)
			if f.Action == "" || f.Action == a {
				p.Sources[s] += n
			}
			if f.Source == "" || f.Source == s {
				p.Actions[a] += n
			}
		}
		rows.Close()
	}
	where, wargs := base, append([]any{}, args...)
	if f.Source != "" {
		where, wargs = where+" AND source = ?", append(wargs, f.Source)
	}
	if f.Action != "" {
		where, wargs = where+" AND action = ?", append(wargs, f.Action)
	}
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sec_events WHERE `+where, wargs...).Scan(&p.Total); err != nil {
		return p, err
	}
	if rows, err := sv.st.DB.QueryContext(ctx, `SELECT ip, COUNT(*) n FROM sec_events WHERE `+where+` GROUP BY ip ORDER BY n DESC LIMIT 10`, wargs...); err == nil {
		for rows.Next() {
			var c IPCount
			rows.Scan(&c.IP, &c.Hits)
			p.TopIPs = append(p.TopIPs, c)
		}
		rows.Close()
	}
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, time, ip, host, site, method, uri, ua, status, action, source, rule, message, severity, messages
		FROM sec_events WHERE `+where+` ORDER BY time DESC, id DESC LIMIT ? OFFSET ?`, append(wargs, f.Limit, f.Offset)...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		var msgs string
		if err := rows.Scan(&e.ID, &e.Time, &e.IP, &e.Host, &e.Site, &e.Method, &e.URI, &e.UA, &e.Status, &e.Action, &e.Source, &e.Rule, &e.Message, &e.Severity, &msgs); err != nil {
			return p, err
		}
		if msgs != "" {
			json.Unmarshal([]byte(msgs), &e.Messages)
		}
		p.Items = append(p.Items, e)
	}
	sv.st.DB.QueryRowContext(ctx, `SELECT COALESCE(MIN(time), 0) FROM sec_events`).Scan(&p.Oldest)
	return p, rows.Err()
}

// BlockedBySite counts the refused requests per site from a time on (website
// protection's totals).
func (sv *Service) BlockedBySite(ctx context.Context, from int64) map[string]int64 {
	out := map[string]int64{}
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT site, COUNT(*) FROM sec_events WHERE time >= ? AND action != 'detected' GROUP BY site`, from)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var n int64
		rows.Scan(&s, &n)
		out[s] = n
	}
	return out
}
