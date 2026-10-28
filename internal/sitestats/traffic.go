package sitestats

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
)

// The security page's traffic overview: every website together for one day
// (today, or yesterday), compared with the day before at the same time.

type HourPoint struct {
	T       int64 `json:"t"`
	PV      int64 `json:"pv"`
	Blocked int64 `json:"blocked"`
	Bytes   int64 `json:"bytes"`
}

type Count struct {
	Key  string `json:"key"`
	Hits int64  `json:"hits"`
}

type Traffic struct {
	Day         int64       `json:"day"`
	Requests    int64       `json:"requests"`
	Blocked     int64       `json:"blocked"`
	Bytes       int64       `json:"bytes"`
	IPs         int64       `json:"ips"`
	PrevReq     int64       `json:"prev_requests"` // the day before, up to the same time
	PrevBlocked int64       `json:"prev_blocked"`
	PrevTotal   int64       `json:"prev_total"` // the whole day before
	Hours       []HourPoint `json:"hours"`
	Status      []Count     `json:"status"`
	Browsers    []Count     `json:"browsers"`
	OS          []Count     `json:"os"`
	Countries   []Count     `json:"countries"`         // visitors
	BlockedBy   []Count     `json:"blocked_countries"` // intercepted requests
	BlockedIPs  []Count     `json:"blocked_ips"`
}

func (sv *Service) dayStart(t time.Time) time.Time {
	t = t.In(sv.Loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, sv.Loc)
}

func (sv *Service) sums(ctx context.Context, from, to int64) (pv, blocked int64) {
	sv.st.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(pv), 0), COALESCE(SUM(blocked), 0) FROM site_stats_hourly WHERE hour >= ? AND hour < ?`, from, to).Scan(&pv, &blocked)
	return
}

func (sv *Service) keys(ctx context.Context, day int64, kind string, limit int) []Count {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT key, SUM(hits) h FROM site_stats_keys WHERE day = ? AND kind = ? GROUP BY key ORDER BY h DESC LIMIT ?`, day, kind, limit)
	if err != nil {
		return []Count{}
	}
	defer rows.Close()
	out := []Count{}
	for rows.Next() {
		var c Count
		rows.Scan(&c.Key, &c.Hits)
		out = append(out, c)
	}
	return out
}

// byCountry folds the top IPs of a kind into countries (ISO code; "" unknown).
func (sv *Service) byCountry(ctx context.Context, day int64, kind string, country func(string) string) []Count {
	m := map[string]int64{}
	for _, c := range sv.keys(ctx, day, kind, 20000) {
		if c.Key == "(other)" {
			continue
		}
		m[country(c.Key)] += c.Hits
	}
	out := make([]Count, 0, len(m))
	for k, v := range m {
		out = append(out, Count{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Hits > out[j].Hits || out[i].Hits == out[j].Hits && out[i].Key < out[j].Key
	})
	return out
}

// Traffic reports one day: back = 0 for today, 1 for yesterday.
func (sv *Service) Traffic(ctx context.Context, back int, country func(ip string) string) Traffic {
	now := sv.now()
	start := sv.dayStart(now).AddDate(0, 0, -back)
	end := start.AddDate(0, 0, 1)
	prev := start.AddDate(0, 0, -1)
	cut := now // compare like with like: the day before up to the same time
	if back > 0 {
		cut = end
	}
	t := Traffic{Day: start.Unix(), Hours: []HourPoint{}}
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT hour, SUM(pv), SUM(blocked), SUM(bytes) FROM site_stats_hourly WHERE hour >= ? AND hour < ? GROUP BY hour ORDER BY hour`, start.Unix(), end.Unix())
	if err == nil {
		got := map[int64]HourPoint{}
		for rows.Next() {
			var p HourPoint
			rows.Scan(&p.T, &p.PV, &p.Blocked, &p.Bytes)
			got[p.T] = p
			t.Requests += p.PV
			t.Blocked += p.Blocked
			t.Bytes += p.Bytes
		}
		rows.Close()
		for h := start; h.Before(end) && !h.After(now); h = h.Add(time.Hour) {
			p := got[h.Unix()]
			p.T = h.Unix()
			t.Hours = append(t.Hours, p)
		}
	}
	t.PrevReq, t.PrevBlocked = sv.sums(ctx, prev.Unix(), prev.Add(cut.Sub(start)).Unix())
	t.PrevTotal, _ = sv.sums(ctx, prev.Unix(), start.Unix())
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT key) FROM site_stats_keys WHERE day = ? AND kind = 'ip'`, start.Unix()).Scan(&t.IPs)
	day := start.Unix()
	t.Status = sv.keys(ctx, day, "status", 20)
	t.Browsers = sv.keys(ctx, day, "browser", 12)
	t.OS = sv.keys(ctx, day, "os", 8)
	t.BlockedIPs = sv.keys(ctx, day, "blocked_ip", 50)
	if country == nil {
		country = func(string) string { return "" }
	}
	t.Countries = sv.byCountry(ctx, day, "ip", country)
	t.BlockedBy = sv.byCountry(ctx, day, "blocked_ip", country)
	return t
}

// ---- live ----

// Live is the last minute across every website, read from the end of the
// access logs: requests and bytes per second, per 5-second step.
type Live struct {
	At    int64     `json:"at"`
	Steps []int64   `json:"steps"` // start of each step (unix)
	QPS   []float64 `json:"qps"`
	BPS   []float64 `json:"bps"` // response bytes per second
}

const liveStep, liveSteps = 5, 12

func (sv *Service) Live(ctx context.Context, domains []string) Live {
	now := sv.now().Unix()
	first := (now/liveStep - liveSteps + 1) * liveStep
	l := Live{At: now, Steps: make([]int64, liveSteps), QPS: make([]float64, liveSteps), BPS: make([]float64, liveSteps)}
	for i := range l.Steps {
		l.Steps[i] = first + int64(i)*liveStep
	}
	for _, d := range domains {
		tailLines(filepath.Join(sv.LogDir, d+".log"), 2<<20, func(line string) bool {
			a := logcenter.ParseAccess(line)
			if a.Time == 0 {
				return true
			}
			if a.Time < first {
				return false // older than the window: stop reading back
			}
			if i := (a.Time - first) / liveStep; i >= 0 && i < liveSteps {
				l.QPS[i]++
				l.BPS[i] += float64(a.Bytes)
			}
			return true
		})
	}
	for i := range l.QPS {
		// The current step is still running: divide by the seconds elapsed.
		secs := float64(liveStep)
		if i == liveSteps-1 {
			secs = float64(now-l.Steps[i]) + 1
		}
		l.QPS[i] /= secs
		l.BPS[i] /= secs
	}
	return l
}

// tailLines calls fn with the file's lines from the last one backwards
// (within the last max bytes) until fn returns false.
func tailLines(path string, max int64, fn func(string) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return
	}
	off := fi.Size() - max
	if off < 0 {
		off = 0
	}
	f.Seek(off, io.SeekStart)
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // partial first line
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if !fn(lines[i]) {
			return
		}
	}
}
