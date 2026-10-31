// Package sitestats turns nginx access logs into website reports (網站監控報表):
// PV, UV, bandwidth, status codes, spiders, top URLs / IPs / referrers and
// visitor countries, kept per hour and per day for 30 days.
package sitestats

import (
	"bufio"
	"context"
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

const (
	keepDays    = 30
	maxKeys     = 5000     // distinct URLs / referrers kept per site and day (IPs: 4x)
	firstReadMB = 64 << 20 // a log seen for the first time is read from its last 64 MiB
)

// Spiders maps a user-agent substring to a crawler name.
var Spiders = []struct{ Match, Name string }{
	{"googlebot", "Google"}, {"bingbot", "Bing"}, {"baiduspider", "Baidu"}, {"yandex", "Yandex"},
	{"duckduckbot", "DuckDuckGo"}, {"sogou", "Sogou"}, {"360spider", "360"}, {"bytespider", "ByteDance"},
	{"applebot", "Apple"}, {"petalbot", "Huawei"}, {"semrushbot", "Semrush"}, {"ahrefsbot", "Ahrefs"},
	{"mj12bot", "Majestic"}, {"gptbot", "OpenAI"}, {"claudebot", "Anthropic"}, {"facebookexternalhit", "Facebook"},
	{"twitterbot", "Twitter"}, {"slurp", "Yahoo"},
}

func spider(ua string) string {
	low := strings.ToLower(ua)
	for _, s := range Spiders {
		if strings.Contains(low, s.Match) {
			return s.Name
		}
	}
	if strings.Contains(low, "bot") || strings.Contains(low, "spider") || strings.Contains(low, "crawler") {
		return "Other bot"
	}
	return ""
}

type Service struct {
	st     *store.Store
	LogDir string
	Sites  func(ctx context.Context) []string // domains
	Loc    *time.Location
	now    func() time.Time
	mu     sync.Mutex
}

func New(st *store.Store, logDir string, loc *time.Location) *Service {
	if loc == nil {
		loc = time.Local
	}
	return &Service{st: st, LogDir: logDir, Loc: loc, now: time.Now}
}

// Run ingests new log lines every five minutes until ctx ends.
func (sv *Service) Run(ctx context.Context, logf func(string, ...any)) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		sv.IngestAll(ctx, logf)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (sv *Service) IngestAll(ctx context.Context, logf func(string, ...any)) {
	if sv.Sites == nil {
		return
	}
	// One reader at a time: two would both read from the same cursor.
	sv.mu.Lock()
	defer sv.mu.Unlock()
	for _, d := range sv.Sites(ctx) {
		if err := sv.Ingest(ctx, d); err != nil && logf != nil {
			logf("site stats", "domain", d, "err", err)
		}
	}
	cut := sv.now().AddDate(0, 0, -keepDays).Unix()
	sv.st.DB.ExecContext(ctx, `DELETE FROM site_stats_hourly WHERE hour < ?`, cut)
	sv.st.DB.ExecContext(ctx, `DELETE FROM site_stats_keys WHERE day < ?`, cut)
}

type hourAgg struct{ pv, bytes, s2, s3, s4, s5, spider, blocked int64 }

// Blocked reports whether a status is a refusal by the WAF (403), a rate
// limit (429) or nginx closing the connection (444, blocked user agents).
func Blocked(status int) bool { return status == 403 || status == 429 || status == 444 }

// Client names a user agent's browser and operating system.
func Client(ua string) (browser, osName string) {
	low := strings.ToLower(ua)
	switch {
	case ua == "" || ua == "-":
		browser = "(none)"
	case strings.Contains(low, "edg/"):
		browser = "Edge"
	case strings.Contains(low, "opr/") || strings.Contains(low, "opera"):
		browser = "Opera"
	case strings.Contains(low, "firefox/"):
		browser = "Firefox"
	case strings.Contains(low, "chrome/") || strings.Contains(low, "crios/"):
		browser = "Chrome"
	case strings.Contains(low, "safari/"):
		browser = "Safari"
	case strings.HasPrefix(low, "curl/"):
		browser = "curl"
	case strings.HasPrefix(low, "go-http-client"):
		browser = "Go-http-client"
	case strings.HasPrefix(low, "python"):
		browser = "Python"
	case strings.HasPrefix(low, "wget"):
		browser = "Wget"
	case strings.HasPrefix(low, "mozilla/"):
		browser = "Mozilla"
	default:
		browser = "Other"
	}
	switch {
	case strings.Contains(low, "android"):
		osName = "Android"
	case strings.Contains(low, "iphone") || strings.Contains(low, "ipad") || strings.Contains(low, "ios"):
		osName = "iOS"
	case strings.Contains(low, "windows"):
		osName = "Windows"
	case strings.Contains(low, "mac os x") || strings.Contains(low, "macintosh"):
		osName = "macOS"
	case strings.Contains(low, "linux") || strings.Contains(low, "x11"):
		osName = "Linux"
	default:
		osName = "Other"
	}
	return
}

type keyAgg struct{ hits, bytes int64 }

// Ingest reads the log lines added since the last run.
func (sv *Service) Ingest(ctx context.Context, domain string) error {
	path := filepath.Join(sv.LogDir, domain+".log")
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
	var inode uint64
	if s, ok := fi.Sys().(*syscall.Stat_t); ok {
		inode = uint64(s.Ino)
	}
	var curInode, offset int64
	err = sv.st.DB.QueryRowContext(ctx, `SELECT inode, offset FROM site_stats_cursor WHERE domain = ?`, domain).Scan(&curInode, &offset)
	switch {
	case err == sql.ErrNoRows:
		offset = max(0, fi.Size()-firstReadMB)
	case err != nil:
		return err
	case uint64(curInode) != inode || offset > fi.Size():
		offset = 0 // rotated or truncated
	}
	if offset == fi.Size() {
		return nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	rd := bufio.NewReaderSize(f, 256<<10)
	if offset > 0 && curInode == 0 { // started mid-file: skip the partial line
		skipped, _ := rd.ReadString('\n')
		offset += int64(len(skipped))
	}
	hours := map[int64]*hourAgg{}
	keys := map[[3]string]*keyAgg{} // day, kind, key
	addKey := func(day int64, kind, key string, bytes int64) {
		k := [3]string{strconv.FormatInt(day, 10), kind, key}
		a := keys[k]
		if a == nil {
			a = &keyAgg{}
			keys[k] = a
		}
		a.hits++
		a.bytes += bytes
	}
	read := offset
	for {
		line, err := rd.ReadString('\n')
		if err != nil { // incomplete last line: leave it for the next run
			break
		}
		read += int64(len(line))
		a := logcenter.ParseAccess(strings.TrimRight(line, "\n"))
		if a.Raw != "" || a.Time == 0 {
			continue
		}
		t := time.Unix(a.Time, 0).In(sv.Loc)
		hour := t.Truncate(time.Hour).Unix()
		day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, sv.Loc).Unix()
		h := hours[hour]
		if h == nil {
			h = &hourAgg{}
			hours[hour] = h
		}
		h.pv++
		h.bytes += a.Bytes
		switch a.Status / 100 {
		case 2:
			h.s2++
		case 3:
			h.s3++
		case 4:
			h.s4++
		case 5:
			h.s5++
		}
		if Blocked(a.Status) || a.Block != "" {
			h.blocked++
			addKey(day, "blocked_ip", a.IP, a.Bytes)
		}
		if sp := spider(a.UA); sp != "" {
			h.spider++
			addKey(day, "spider", sp, a.Bytes)
			continue // crawlers are not visitors
		}
		addKey(day, "ip", a.IP, a.Bytes)
		p, _, _ := strings.Cut(a.Path, "?")
		if len(p) > 300 {
			p = p[:300]
		}
		addKey(day, "url", p, a.Bytes)
		addKey(day, "status", strconv.Itoa(a.Status), a.Bytes)
		browser, osName := Client(a.UA)
		addKey(day, "browser", browser, 0)
		addKey(day, "os", osName, 0)
		if a.Referer != "" && a.Referer != "-" {
			if u, err := url.Parse(a.Referer); err == nil && u.Host != "" && !strings.EqualFold(u.Hostname(), domain) {
				addKey(day, "ref", strings.ToLower(u.Hostname()), 0)
			}
		}
	}
	tx, err := sv.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for hour, h := range hours {
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_stats_hourly(domain, hour, pv, bytes, s2xx, s3xx, s4xx, s5xx, spider, blocked) VALUES(?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(domain, hour) DO UPDATE SET pv=pv+excluded.pv, bytes=bytes+excluded.bytes, s2xx=s2xx+excluded.s2xx, s3xx=s3xx+excluded.s3xx,
			s4xx=s4xx+excluded.s4xx, s5xx=s5xx+excluded.s5xx, spider=spider+excluded.spider, blocked=blocked+excluded.blocked`,
			domain, hour, h.pv, h.bytes, h.s2, h.s3, h.s4, h.s5, h.spider, h.blocked); err != nil {
			return err
		}
	}
	counts := map[[2]string]int{}
	for k, a := range keys {
		day, kind := k[0], k[1]
		limit := maxKeys
		if kind == "ip" {
			limit *= 4
		}
		ck := [2]string{day, kind}
		if _, ok := counts[ck]; !ok {
			var n int
			tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_stats_keys WHERE domain = ? AND day = ? AND kind = ?`, domain, day, kind).Scan(&n)
			counts[ck] = n
		}
		key := k[2]
		if counts[ck] >= limit {
			// Too many distinct values (e.g. a scanner): fold new ones into "(other)".
			var exists int
			tx.QueryRowContext(ctx, `SELECT 1 FROM site_stats_keys WHERE domain = ? AND day = ? AND kind = ? AND key = ?`, domain, day, kind, key).Scan(&exists)
			if exists == 0 {
				key = "(other)"
			}
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO site_stats_keys(domain, day, kind, key, hits, bytes) VALUES(?,?,?,?,?,?)
			ON CONFLICT(domain, day, kind, key) DO UPDATE SET hits=hits+excluded.hits, bytes=bytes+excluded.bytes`,
			domain, day, kind, key, a.hits, a.bytes)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			counts[ck]++ // over-counts updates slightly; only bounds growth
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_stats_cursor(domain, inode, offset) VALUES(?,?,?)
		ON CONFLICT(domain) DO UPDATE SET inode=excluded.inode, offset=excluded.offset`, domain, int64(inode), read); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- reports ----

type Point struct {
	T      int64 `json:"t"`
	PV     int64 `json:"pv"`
	UV     int64 `json:"uv"`
	Bytes  int64 `json:"bytes"`
	S2xx   int64 `json:"s2xx"`
	S3xx   int64 `json:"s3xx"`
	S4xx   int64 `json:"s4xx"`
	S5xx   int64 `json:"s5xx"`
	Spider int64 `json:"spider"`
}

type Top struct {
	Key   string `json:"key"`
	Hits  int64  `json:"hits"`
	Bytes int64  `json:"bytes"`
}

type Report struct {
	Domain    string           `json:"domain"`
	From      int64            `json:"from"`
	To        int64            `json:"to"`
	Total     Point            `json:"total"`
	Series    []Point          `json:"series"` // hourly for ≤ 2 days, daily otherwise
	Daily     bool             `json:"daily"`
	Top       map[string][]Top `json:"top"` // ip | url | ref | spider | status
	Countries []Top            `json:"countries"`
}

// Report summarises the last `days` days (1 = today).
func (sv *Service) Report(ctx context.Context, domain string, days int, country func(ip string) string) (Report, error) {
	days = max(1, min(days, keepDays))
	now := sv.now().In(sv.Loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, sv.Loc).AddDate(0, 0, -(days - 1))
	rep := Report{Domain: domain, From: from.Unix(), To: now.Unix(), Daily: days > 2, Top: map[string][]Top{}, Series: []Point{}}
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT hour, pv, bytes, s2xx, s3xx, s4xx, s5xx, spider FROM site_stats_hourly WHERE domain = ? AND hour >= ? ORDER BY hour`, domain, from.Unix())
	if err != nil {
		return rep, err
	}
	byT := map[int64]*Point{}
	var order []int64
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.T, &p.PV, &p.Bytes, &p.S2xx, &p.S3xx, &p.S4xx, &p.S5xx, &p.Spider); err != nil {
			rows.Close()
			return rep, err
		}
		t := p.T
		if rep.Daily {
			tt := time.Unix(p.T, 0).In(sv.Loc)
			t = time.Date(tt.Year(), tt.Month(), tt.Day(), 0, 0, 0, 0, sv.Loc).Unix()
		}
		b := byT[t]
		if b == nil {
			b = &Point{T: t}
			byT[t] = b
			order = append(order, t)
		}
		b.PV += p.PV
		b.Bytes += p.Bytes
		b.S2xx += p.S2xx
		b.S3xx += p.S3xx
		b.S4xx += p.S4xx
		b.S5xx += p.S5xx
		b.Spider += p.Spider
		rep.Total.PV += p.PV
		rep.Total.Bytes += p.Bytes
		rep.Total.S2xx += p.S2xx
		rep.Total.S3xx += p.S3xx
		rep.Total.S4xx += p.S4xx
		rep.Total.S5xx += p.S5xx
		rep.Total.Spider += p.Spider
	}
	rows.Close()
	// UV per day = distinct visitor IPs of that day.
	uvRows, err := sv.st.DB.QueryContext(ctx, `SELECT day, COUNT(*) FROM site_stats_keys WHERE domain = ? AND day >= ? AND kind = 'ip' AND key != '(other)' GROUP BY day`, domain, from.Unix())
	if err == nil {
		for uvRows.Next() {
			var day, n int64
			uvRows.Scan(&day, &n)
			if rep.Daily && byT[day] != nil {
				byT[day].UV = n
			}
			rep.Total.UV += n
		}
		uvRows.Close()
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	for _, t := range order {
		rep.Series = append(rep.Series, *byT[t])
	}
	for _, kind := range []string{"ip", "url", "ref", "spider", "status"} {
		rows, err := sv.st.DB.QueryContext(ctx, `SELECT key, SUM(hits), SUM(bytes) FROM site_stats_keys WHERE domain = ? AND day >= ? AND kind = ?
			GROUP BY key ORDER BY SUM(hits) DESC LIMIT 20`, domain, from.Unix(), kind)
		if err != nil {
			return rep, err
		}
		list := []Top{}
		for rows.Next() {
			var t Top
			rows.Scan(&t.Key, &t.Hits, &t.Bytes)
			list = append(list, t)
		}
		rows.Close()
		rep.Top[kind] = list
	}
	if country != nil {
		rows, err := sv.st.DB.QueryContext(ctx, `SELECT key, SUM(hits) FROM site_stats_keys WHERE domain = ? AND day >= ? AND kind = 'ip' AND key != '(other)'
			GROUP BY key ORDER BY SUM(hits) DESC LIMIT 20000`, domain, from.Unix())
		if err == nil {
			agg := map[string]int64{}
			for rows.Next() {
				var ip string
				var hits int64
				rows.Scan(&ip, &hits)
				c := country(ip)
				if c == "" {
					c = "??"
				}
				agg[c] += hits
			}
			rows.Close()
			for c, h := range agg {
				rep.Countries = append(rep.Countries, Top{Key: c, Hits: h})
			}
			sort.Slice(rep.Countries, func(i, j int) bool {
				a, b := rep.Countries[i], rep.Countries[j]
				if a.Hits != b.Hits {
					return a.Hits > b.Hits
				}
				if (a.Key == "??") != (b.Key == "??") {
					return b.Key == "??" // unknown last among equals
				}
				return a.Key < b.Key
			})
			if len(rep.Countries) > 30 {
				rep.Countries = rep.Countries[:30]
			}
		}
	}
	if rep.Countries == nil {
		rep.Countries = []Top{}
	}
	return rep, nil
}

// Overview returns today's PV / UV / bandwidth for every site.
func (sv *Service) Overview(ctx context.Context) (map[string]Point, error) {
	now := sv.now().In(sv.Loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, sv.Loc).Unix()
	out := map[string]Point{}
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT domain, SUM(pv), SUM(bytes), SUM(s4xx), SUM(s5xx) FROM site_stats_hourly WHERE hour >= ? GROUP BY domain`, day)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d string
		var p Point
		rows.Scan(&d, &p.PV, &p.Bytes, &p.S4xx, &p.S5xx)
		out[d] = p
	}
	rows.Close()
	rows, err = sv.st.DB.QueryContext(ctx, `SELECT domain, COUNT(*) FROM site_stats_keys WHERE day = ? AND kind = 'ip' GROUP BY domain`, day)
	if err == nil {
		for rows.Next() {
			var d string
			var n int64
			rows.Scan(&d, &n)
			p := out[d]
			p.UV = n
			out[d] = p
		}
		rows.Close()
	}
	return out, nil
}
