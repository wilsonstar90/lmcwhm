package secevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Auto-ban: an address refused Threshold times within Window minutes is
// blocked for BanMinutes. Only the sources picked count; requests that were
// only detected never do, nor do addresses already refused by the IP lists.

const settingsKey = "auto_ban"

type AutoBan struct {
	Enabled    bool     `json:"enabled"`
	Window     int      `json:"window"`      // minutes
	Threshold  int      `json:"threshold"`   // refusals within the window
	BanMinutes int      `json:"ban_minutes"` // how long the block lasts
	Sources    []string `json:"sources"`     // waf | rule | list | protection | cc
}

var AutoBanSources = []string{"waf", "rule", "list", "protection", "cc"}

var ErrBadAutoBan = errors.New("autoban.invalid")

func DefaultAutoBan() AutoBan {
	return AutoBan{Window: 5, Threshold: 10, BanMinutes: 60, Sources: append([]string{}, AutoBanSources...)}
}

func (a *AutoBan) normalize() error {
	if a.Window < 1 || a.Window > 1440 || a.Threshold < 2 || a.Threshold > 100000 || a.BanMinutes < 1 || a.BanMinutes > 525600 {
		return ErrBadAutoBan
	}
	seen := map[string]bool{}
	var src []string
	for _, s := range a.Sources {
		ok := false
		for _, k := range AutoBanSources {
			ok = ok || s == k
		}
		if !ok {
			return ErrBadAutoBan
		}
		if !seen[s] {
			seen[s] = true
			src = append(src, s)
		}
	}
	sort.Strings(src)
	if src == nil {
		src = []string{}
	}
	a.Sources = src
	return nil
}

func (sv *Service) AutoBanSettings(ctx context.Context) AutoBan {
	a := DefaultAutoBan()
	var raw string
	if sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &a)
	}
	if a.normalize() != nil {
		d := DefaultAutoBan()
		d.Enabled = a.Enabled
		return d
	}
	return a
}

func (sv *Service) SaveAutoBan(ctx context.Context, a AutoBan) (AutoBan, error) {
	if err := a.normalize(); err != nil {
		return a, err
	}
	b, _ := json.Marshal(a)
	_, err := sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingsKey, string(b))
	return a, err
}

// autoBan checks the addresses of a batch of new events.
func (sv *Service) autoBan(ctx context.Context, evs []Event, logf func(string, ...any)) {
	if sv.Ban == nil {
		return
	}
	a := sv.AutoBanSettings(ctx)
	if !a.Enabled || len(a.Sources) == 0 {
		return
	}
	counted := map[string]bool{}
	for _, s := range a.Sources {
		counted[s] = true
	}
	since := sv.now().Add(-time.Duration(a.Window) * time.Minute).Unix()
	ips := map[string]bool{}
	for _, e := range evs {
		if e.Action != "detected" && counted[e.Source] && e.Time >= since {
			ips[e.IP] = true
		}
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(a.Sources)), ",")
	for ip := range ips {
		if sv.Exempt != nil && sv.Exempt(ctx, ip) {
			continue
		}
		args := []any{ip, since}
		for _, s := range a.Sources {
			args = append(args, s)
		}
		var n int64
		sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sec_events WHERE ip = ? AND time >= ? AND action != 'detected' AND source IN (`+marks+`)`, args...).Scan(&n)
		if n < int64(a.Threshold) {
			continue
		}
		// The rule seen most often names the ban.
		var src, rule string
		sv.st.DB.QueryRowContext(ctx, `SELECT source, rule FROM sec_events WHERE ip = ? AND time >= ? AND action != 'detected' AND source IN (`+marks+`)
			GROUP BY source, rule ORDER BY COUNT(*) DESC LIMIT 1`, args...).Scan(&src, &rule)
		remark := fmt.Sprintf("%d / %dm · %s %s", n, a.Window, src, rule)
		if err := sv.Ban(ctx, ip, time.Duration(a.BanMinutes)*time.Minute, remark); err != nil && logf != nil {
			logf("auto-ban", "ip", ip, "err", err)
		}
	}
}
