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

// Public IP lists ("feeds"), downloaded on a schedule as entries (source
// "feed:<id>") that expire on their own when a feed stops being refreshed:
//   - block feeds: malicious IPs, enforced as temporary blocks. Unlike the
//     CrowdSec community blocklist they need no signal sharing.
//   - trust feeds: Cloudflare and search / AI crawlers, kept in the "trust"
//     list, which exempts them from every block and ban (CrowdSec, feeds,
//     manual) but, unlike the allow list, does not open any port: anyone can
//     originate traffic from Cloudflare (Workers), so trusting it on SSH or a
//     database port would bypass the firewall.

type Feed struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Info  string `json:"info,omitempty"`
	Kind  string `json:"kind"`            // block | trust
	Group string `json:"group,omitempty"` // trust catalog: cdn | search | ai | social
}

const sefinek = "https://raw.githubusercontent.com/sefinek/trusted-ips-whitelist/main/lists/"

var FeedCatalog = []Feed{
	{ID: "spamhaus_drop", Name: "Spamhaus DROP (IPv4)", URL: "https://www.spamhaus.org/drop/drop_v4.json", Info: "hijacked / criminal networks"},
	{ID: "spamhaus_drop6", Name: "Spamhaus DROP (IPv6)", URL: "https://www.spamhaus.org/drop/drop_v6.json", Info: "hijacked / criminal networks"},
	{ID: "firehol_level1", Name: "FireHOL level 1", URL: "https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/firehol_level1.netset", Info: "DShield, Spamhaus, Feodo, Fullbogons (bogons skipped)"},
	{ID: "blocklist_de", Name: "blocklist.de (all)", URL: "https://lists.blocklist.de/lists/all.txt", Info: "SSH / mail / web attackers, last 48 hours"},
	{ID: "ipsum3", Name: "IPsum level 3", URL: "https://raw.githubusercontent.com/stamparm/ipsum/master/levels/3.txt", Info: "on 3+ public blocklists, daily"},
	{ID: "cins", Name: "CINS Army", URL: "https://cinsscore.com/list/ci-badguys.txt", Info: "scanners and attackers"},
	{ID: "et_compromised", Name: "Emerging Threats compromised", URL: "https://rules.emergingthreats.net/blockrules/compromised-ips.txt", Info: "compromised hosts"},
	{ID: "feodo", Name: "abuse.ch Feodo Tracker", URL: "https://feodotracker.abuse.ch/downloads/ipblocklist.txt", Info: "botnet C2 servers"},

	{ID: "cloudflare4", Kind: KindTrust, Group: "cdn", Name: "Cloudflare (IPv4)", URL: "https://www.cloudflare.com/ips-v4"},
	{ID: "cloudflare6", Kind: KindTrust, Group: "cdn", Name: "Cloudflare (IPv6)", URL: "https://www.cloudflare.com/ips-v6"},
	{ID: "googlebot", Kind: KindTrust, Group: "search", Name: "Googlebot", URL: sefinek + "googlebot/ips.txt"},
	{ID: "google_special", Kind: KindTrust, Group: "search", Name: "Google special crawlers", URL: sefinek + "google-special-crawlers/ips.txt", Info: "AdsBot, Google-Safety, …"},
	{ID: "google_fetchers", Kind: KindTrust, Group: "search", Name: "Google user-triggered fetchers", URL: sefinek + "google-user-triggered-fetchers/ips.txt", Info: "Site Verification, Feedfetcher, …"},
	{ID: "bingbot", Kind: KindTrust, Group: "search", Name: "Bingbot", URL: sefinek + "bingbot/ips.txt", Info: "Bing; also powers Yahoo search"},
	{ID: "baidu", Kind: KindTrust, Group: "search", Name: "Baiduspider", URL: sefinek + "baidu/ips.txt"},
	{ID: "yandexbot", Kind: KindTrust, Group: "search", Name: "YandexBot", URL: sefinek + "yandexbot/ips.txt"},
	{ID: "duckduckbot", Kind: KindTrust, Group: "search", Name: "DuckDuckBot", URL: sefinek + "duckduckbot/ips.txt"},
	{ID: "applebot", Kind: KindTrust, Group: "search", Name: "Applebot", URL: sefinek + "applebot/ips.txt"},
	{ID: "all_ai", Kind: KindTrust, Group: "ai", Name: "All AI crawlers", URL: sefinek + "all-ai-ips.txt", Info: "GPTBot, ChatGPT, ClaudeBot, Perplexity, …"},
	{ID: "gptbot", Kind: KindTrust, Group: "ai", Name: "OpenAI GPTBot", URL: sefinek + "gptbot/ips.txt"},
	{ID: "oai_searchbot", Kind: KindTrust, Group: "ai", Name: "OpenAI OAI-SearchBot", URL: sefinek + "oai-searchbot/ips.txt"},
	{ID: "chatgpt_user", Kind: KindTrust, Group: "ai", Name: "ChatGPT-User", URL: sefinek + "chatgpt-user/ips.txt"},
	{ID: "claudebot", Kind: KindTrust, Group: "ai", Name: "Anthropic ClaudeBot", URL: sefinek + "claudebot/ips.txt"},
	{ID: "perplexitybot", Kind: KindTrust, Group: "ai", Name: "PerplexityBot", URL: sefinek + "perplexitybot/ips.txt"},
	{ID: "perplexity_user", Kind: KindTrust, Group: "ai", Name: "Perplexity-User", URL: sefinek + "perplexity-user/ips.txt"},
	{ID: "facebookbot", Kind: KindTrust, Group: "social", Name: "Facebook crawler", URL: sefinek + "facebookbot/ips.txt", Info: "link previews"},
	{ID: "telegrambot", Kind: KindTrust, Group: "social", Name: "Telegram bot", URL: sefinek + "telegrambot/ips.txt", Info: "link previews"},
}

const (
	KindBlock = "block"
	KindTrust = "trust"
)

func init() {
	for i := range FeedCatalog {
		if FeedCatalog[i].Kind == "" {
			FeedCatalog[i].Kind = KindBlock
		}
	}
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

// FeedConfig is the selection; updates run as scheduled tasks ("ip_feeds"
// cron jobs, one per kind), which also set how long entries live.
type FeedConfig struct {
	Enabled  []string              `json:"enabled"`
	Custom   []Feed                `json:"custom"`
	Status   map[string]FeedStatus `json:"status"`
	LastRun  int64                 `json:"last_run"`
	TTL      map[string]int64      `json:"ttl"` // seconds per kind, from the last scheduled run
	Running  bool                  `json:"running"`
	Catalog  []Feed                `json:"catalog,omitempty"`
	Blocking int                   `json:"blocking"`
	Trusted  int                   `json:"trusted"`
}

var feedMu sync.Mutex

func (sv *Service) FeedConfig(ctx context.Context) FeedConfig {
	var c FeedConfig
	var raw string
	if sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, feedsKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &c)
	}
	if c.TTL == nil {
		c.TTL = map[string]int64{}
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
	c.Catalog, c.Running, c.Blocking, c.Trusted = nil, false, 0, 0
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
	now := time.Now().Unix()
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM fw_ips WHERE list = 'block' AND source LIKE 'feed:%' AND expires_at > ?`, now).Scan(&c.Blocking)
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM fw_ips WHERE list = 'trust' AND source LIKE 'feed:%' AND expires_at > ?`, now).Scan(&c.Trusted)
	return c
}

func (c FeedConfig) all() []Feed {
	return append(append([]Feed{}, FeedCatalog...), c.Custom...)
}

// EnabledKinds reports which kinds ("trust", "block") have lists selected.
func (c FeedConfig) EnabledKinds() map[string]bool {
	byID := map[string]string{}
	for _, f := range c.all() {
		byID[f.ID] = f.Kind
	}
	out := map[string]bool{}
	for _, id := range c.Enabled {
		if k := byID[id]; k != "" {
			out[k] = true
		}
	}
	return out
}

// SaveFeeds stores the selection (custom lists are always enabled); feeds no
// longer enabled are lifted at once.
func (sv *Service) SaveFeeds(ctx context.Context, enabled []string, custom []Feed) error {
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
		if f.Kind != KindTrust {
			f.Kind = KindBlock
		}
		f.Group = ""
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

// protected collects what a block feed must never cover: the allow list, this
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

// defaultFeedTTL applies to an update made before any scheduled run.
const defaultFeedTTL = 13 * time.Hour

// RefreshFeeds updates every selected list now (after saving, "update now");
// entries live as long as the last scheduled run of their kind set.
func (sv *Service) RefreshFeeds(ctx context.Context) error {
	c := sv.FeedConfig(ctx)
	var errs []string
	for kind := range c.EnabledKinds() {
		ttl := time.Duration(c.TTL[kind]) * time.Second
		if ttl <= 0 {
			ttl = defaultFeedTTL
		}
		if err := sv.RunFeeds(ctx, kind, ttl, io.Discard); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// RunFeeds downloads the selected lists of one kind and replaces their
// entries (the "ip_feeds" scheduled task); a list that fails keeps its
// previous entries until they expire. A line per list goes to out.
func (sv *Service) RunFeeds(ctx context.Context, kind string, ttl time.Duration, out io.Writer) error {
	feedMu.Lock()
	defer feedMu.Unlock()
	c := sv.FeedConfig(ctx)
	if kind != KindTrust {
		kind = KindBlock
	}
	c.TTL[kind] = int64(ttl / time.Second)
	byID := map[string]Feed{}
	for _, f := range c.all() {
		byID[f.ID] = f
	}
	// Trusted ranges need no guard here: the ruleset exempts them from drops,
	// so a block CIDR that overlaps one is kept whole.
	guard := reserved
	if kind == KindBlock {
		guard = sv.protected(ctx)
	}
	// The other kind's entries count towards the limit too.
	total := 0
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM fw_ips WHERE source LIKE 'feed:%' AND list != ?`, kind).Scan(&total)
	changed, failed, n := false, 0, 0
	for _, id := range c.Enabled {
		f, ok := byID[id]
		if !ok || f.Kind != kind {
			continue
		}
		n++
		st := FeedStatus{LastRun: time.Now().Unix()}
		entries, skipped, err := fetch(ctx, f.URL, guard)
		if err == nil && len(entries) == 0 {
			err = errors.New("no addresses in the list")
		}
		if err == nil && total+len(entries) > FeedMaxTotal {
			err = fmt.Errorf("over the %d-entry limit for all lists", FeedMaxTotal)
		}
		if err == nil {
			err = sv.replaceFeed(ctx, kind, feedSourcePfx+id, f.Name, entries, ttl)
		}
		if err != nil {
			failed++
			st.Entries, st.Error = c.Status[id].Entries, err.Error()
			fmt.Fprintf(out, "%s: FAILED: %v (previous entries kept)\n", f.Name, err)
		} else {
			total += len(entries)
			st.Entries, st.Skipped = len(entries), skipped
			changed = true
			fmt.Fprintf(out, "%s: %d entries, %d skipped\n", f.Name, len(entries), skipped)
		}
		c.Status[id] = st
	}
	if n == 0 {
		fmt.Fprintln(out, "no lists selected")
	}
	c.LastRun = time.Now().Unix()
	sv.saveFeeds(ctx, c)
	if changed {
		if err := sv.applyBans(ctx); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d lists failed", failed, n)
	}
	return nil
}

// replaceFeed makes source's entries in list exactly entries, all expiring after ttl.
func (sv *Service) replaceFeed(ctx context.Context, list, source, remark string, entries []string, ttl time.Duration) error {
	tx, err := sv.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `DELETE FROM fw_ips WHERE source = ?`, source); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO fw_ips(ip, list, source, remark, expires_at, created_at) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(ip, list) DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, ip := range entries {
		if _, err := stmt.ExecContext(ctx, ip, list, source, remark, now.Add(ttl).Unix(), now.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
