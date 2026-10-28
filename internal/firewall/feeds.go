package firewall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Public IP blocklists ("feeds"): downloaded on a schedule and enforced as
// temporary blocks (source "feed:<id>") that expire on their own when a feed
// stops being refreshed. Unlike the CrowdSec community blocklist they need no
// signal sharing.

type Feed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Info string `json:"info,omitempty"`
}

var FeedCatalog = []Feed{
	{ID: "spamhaus_drop", Name: "Spamhaus DROP (IPv4)", URL: "https://www.spamhaus.org/drop/drop_v4.json", Info: "hijacked / criminal networks"},
	{ID: "spamhaus_drop6", Name: "Spamhaus DROP (IPv6)", URL: "https://www.spamhaus.org/drop/drop_v6.json", Info: "hijacked / criminal networks"},
	{ID: "firehol_level1", Name: "FireHOL level 1", URL: "https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/firehol_level1.netset", Info: "DShield, Spamhaus, Feodo, Fullbogons (bogons skipped)"},
	{ID: "blocklist_de", Name: "blocklist.de (all)", URL: "https://lists.blocklist.de/lists/all.txt", Info: "SSH / mail / web attackers, last 48 hours"},
	{ID: "ipsum3", Name: "IPsum level 3", URL: "https://raw.githubusercontent.com/stamparm/ipsum/master/levels/3.txt", Info: "on 3+ public blocklists, daily"},
	{ID: "cins", Name: "CINS Army", URL: "https://cinsscore.com/list/ci-badguys.txt", Info: "scanners and attackers"},
	{ID: "et_compromised", Name: "Emerging Threats compromised", URL: "https://rules.emergingthreats.net/blockrules/compromised-ips.txt", Info: "compromised hosts"},
	{ID: "feodo", Name: "abuse.ch Feodo Tracker", URL: "https://feodotracker.abuse.ch/downloads/ipblocklist.txt", Info: "botnet C2 servers"},
}

const (
	feedsKey      = "fw_feeds"
	FeedMaxTotal  = 300000
	feedMaxBody   = 32 << 20
	feedSourcePfx = "feed:"
)

var (
	ErrFeedUnknown = errors.New("fw.feed_unknown")
	ErrFeedURL     = errors.New("fw.feed_bad_url")
)

type FeedStatus struct {
	LastRun int64  `json:"last_run"`
	Entries int    `json:"entries"`
	Skipped int    `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

type FeedConfig struct {
	Enabled  []string              `json:"enabled"`
	Custom   []Feed                `json:"custom"`
	Hours    int                   `json:"hours"` // refresh interval
	Status   map[string]FeedStatus `json:"status"`
	LastRun  int64                 `json:"last_run"`
	Running  bool                  `json:"running"`
	Catalog  []Feed                `json:"catalog,omitempty"`
	Blocking int                   `json:"blocking"`
}

var feedMu sync.Mutex

func (sv *Service) FeedConfig(ctx context.Context) FeedConfig {
	c := FeedConfig{Hours: 6}
	var raw string
	if sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, feedsKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &c)
	}
	if c.Hours < 1 || c.Hours > 48 {
		c.Hours = 6
	}
	if c.Status == nil {
		c.Status = map[string]FeedStatus{}
	}
	if c.Enabled == nil {
		c.Enabled = []string{}
	}
	if c.Custom == nil {
		c.Custom = []Feed{}
	}
	return c
}

func (sv *Service) saveFeeds(ctx context.Context, c FeedConfig) error {
	c.Catalog, c.Running, c.Blocking = nil, false, 0
	b, _ := json.Marshal(c)
	_, err := sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, feedsKey, string(b))
	return err
}

// Feeds returns the configuration with the catalog and live counts (UI).
func (sv *Service) Feeds(ctx context.Context) FeedConfig {
	c := sv.FeedConfig(ctx)
	c.Catalog = FeedCatalog
	c.Running = !feedMu.TryLock()
	if !c.Running {
		feedMu.Unlock()
	}
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM fw_ips WHERE source LIKE 'feed:%' AND expires_at > ?`, time.Now().Unix()).Scan(&c.Blocking)
	return c
}

func (c FeedConfig) all() []Feed {
	return append(append([]Feed{}, FeedCatalog...), c.Custom...)
}

// SaveFeeds stores the selection (custom lists are always enabled); feeds no
// longer enabled are lifted at once.
func (sv *Service) SaveFeeds(ctx context.Context, enabled []string, custom []Feed, hours int) error {
	c := sv.FeedConfig(ctx)
	seen := map[string]bool{}
	var cus []Feed
	for i, f := range custom {
		u, err := url.Parse(strings.TrimSpace(f.URL))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return ErrFeedURL
		}
		f.URL = u.String()
		f.ID = fmt.Sprintf("custom%d", i+1)
		if strings.TrimSpace(f.Name) == "" {
			f.Name = u.Host
		}
		cus = append(cus, f)
	}
	c.Custom = cus
	known := map[string]bool{}
	for _, f := range c.all() {
		known[f.ID] = true
	}
	c.Enabled = []string{}
	// Custom lists are always on.
	for _, f := range cus {
		enabled = append(enabled, f.ID)
	}
	for _, id := range enabled {
		if !known[id] {
			return ErrFeedUnknown
		}
		if !seen[id] {
			seen[id] = true
			c.Enabled = append(c.Enabled, id)
		}
	}
	if hours >= 1 && hours <= 48 {
		c.Hours = hours
	}
	for id := range c.Status {
		if !seen[id] {
			delete(c.Status, id)
		}
	}
	if err := sv.saveFeeds(ctx, c); err != nil {
		return err
	}
	// Lift what is no longer subscribed (including renamed custom feeds).
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT DISTINCT source FROM fw_ips WHERE source LIKE 'feed:%'`)
	if err != nil {
		return err
	}
	var drop []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		if !seen[strings.TrimPrefix(s, feedSourcePfx)] {
			drop = append(drop, s)
		}
	}
	rows.Close()
	for _, s := range drop {
		if _, err := sv.st.DB.ExecContext(ctx, `DELETE FROM fw_ips WHERE source = ?`, s); err != nil {
			return err
		}
	}
	if len(drop) > 0 {
		return sv.applyBans(ctx)
	}
	return nil
}

// reserved ranges never come from a feed (FireHOL level 1 carries the
// bogons, which include the Docker bridges and the LAN).
var reserved = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
		"224.0.0.0/3", "::/8", "64:ff9b::/96", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10", "ff00::/8"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// protected collects what a feed must never cover: the allow list, this
// server's addresses and where the panel was logged into in the last 30 days.
func (sv *Service) protected(ctx context.Context) []netip.Prefix {
	out := append([]netip.Prefix{}, reserved...)
	addP := func(s string) {
		if p, _, err := NormIP(s); err == nil {
			out = append(out, p)
		}
	}
	if rows, err := sv.st.DB.QueryContext(ctx, `SELECT ip FROM fw_ips WHERE list = 'allow'`); err == nil {
		for rows.Next() {
			var s string
			rows.Scan(&s)
			addP(s)
		}
		rows.Close()
	}
	if rows, err := sv.st.DB.QueryContext(ctx, `SELECT DISTINCT ip FROM login_logs WHERE success = 1 AND created_at > ?`, time.Now().Add(-30*24*time.Hour).Unix()); err == nil {
		for rows.Next() {
			var s string
			rows.Scan(&s)
			addP(s)
		}
		rows.Close()
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				addP(n.IP.String())
			}
		}
	}
	return out
}

// ParseFeed extracts addresses and CIDRs from a list: one per line, with
// "#" / ";" comments, extra columns or JSON lines (Spamhaus) around them.
func ParseFeed(r io.Reader, guard []netip.Prefix) (entries []string, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	seen := map[string]bool{}
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = line[:i]
		}
		tok := strings.FieldsFunc(line, func(r rune) bool {
			return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' || r == '.' || r == ':' || r == '/')
		})
		for _, t := range tok {
			if !strings.ContainsAny(t, ".:") {
				continue
			}
			p, norm, err := NormIP(t)
			if err != nil {
				continue
			}
			if p.Bits() < 8 && p.Addr().Is4() || p.Bits() < 19 && p.Addr().Is6() || overlapsAny(p, guard) {
				skipped++
			} else if !seen[norm] {
				seen[norm] = true
				entries = append(entries, norm)
			}
			break
		}
	}
	return entries, skipped, sc.Err()
}

func overlapsAny(p netip.Prefix, list []netip.Prefix) bool {
	for _, q := range list {
		if p.Overlaps(q) {
			return true
		}
	}
	return false
}

var feedClient = &http.Client{Timeout: 90 * time.Second}

func fetchFeed(ctx context.Context, u string, guard []netip.Prefix) ([]string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "lmcwhm-firewall")
	res, err := feedClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("HTTP %s", res.Status)
	}
	return ParseFeed(io.LimitReader(res.Body, feedMaxBody), guard)
}

// fetch is replaceable in tests.
var fetch = fetchFeed

// RefreshFeeds downloads every enabled feed and replaces its blocks; a feed
// that fails keeps its previous entries until they expire.
func (sv *Service) RefreshFeeds(ctx context.Context) error {
	if !feedMu.TryLock() {
		return nil
	}
	defer feedMu.Unlock()
	c := sv.FeedConfig(ctx)
	byID := map[string]Feed{}
	for _, f := range c.all() {
		byID[f.ID] = f
	}
	guard := sv.protected(ctx)
	// Twice the interval plus slack: survives one failed download.
	ttl := time.Duration(2*c.Hours)*time.Hour + time.Hour
	total, changed := 0, false
	for _, id := range c.Enabled {
		f, ok := byID[id]
		if !ok {
			continue
		}
		st := FeedStatus{LastRun: time.Now().Unix()}
		entries, skipped, err := fetch(ctx, f.URL, guard)
		if err == nil && len(entries) == 0 {
			err = errors.New("no addresses in the list")
		}
		if err == nil && total+len(entries) > FeedMaxTotal {
			err = fmt.Errorf("over the %d-entry limit for all feeds", FeedMaxTotal)
		}
		if err != nil {
			prev := c.Status[id]
			st.Entries, st.Error = prev.Entries, err.Error()
			c.Status[id] = st
			continue
		}
		total += len(entries)
		st.Entries, st.Skipped = len(entries), skipped
		if err := sv.replaceFeed(ctx, feedSourcePfx+id, f.Name, entries, ttl); err != nil {
			st.Error = err.Error()
		}
		changed = true
		c.Status[id] = st
	}
	c.LastRun = time.Now().Unix()
	sv.saveFeeds(ctx, c)
	if changed {
		return sv.applyBans(ctx)
	}
	return nil
}

// replaceFeed makes source's blocks exactly entries, all expiring after ttl.
func (sv *Service) replaceFeed(ctx context.Context, source, remark string, entries []string, ttl time.Duration) error {
	tx, err := sv.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `DELETE FROM fw_ips WHERE source = ?`, source); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO fw_ips(ip, list, source, remark, expires_at, created_at) VALUES(?, 'block', ?, ?, ?, ?)
		ON CONFLICT(ip, list) DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, ip := range entries {
		if _, err := stmt.ExecContext(ctx, ip, source, remark, now.Add(ttl).Unix(), now.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FeedLoop refreshes the feeds on their interval.
func (sv *Service) FeedLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		c := sv.FeedConfig(ctx)
		if len(c.Enabled) > 0 && time.Since(time.Unix(c.LastRun, 0)) >= time.Duration(c.Hours)*time.Hour {
			if err := sv.RefreshFeeds(ctx); err != nil {
				sv.logf("firewall feeds", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
