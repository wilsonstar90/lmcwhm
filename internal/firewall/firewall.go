// Package firewall manages the server firewall as one nftables table
// ("inet lmcwhm") that is regenerated from the database and replaced
// atomically. Other tables (ufw, Docker) are never touched. IP blocks can
// also be pushed to Cloudflare (edge) and a Linode Cloud Firewall (network).
package firewall

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

var (
	ErrNoNft      = errors.New("fw.no_nft")
	ErrBadRule    = errors.New("fw.invalid_rule")
	ErrBadIP      = errors.New("fw.invalid_ip")
	ErrBadPolicy  = errors.New("fw.invalid_policy")
	ErrNotFound   = errors.New("fw.not_found")
	ErrSelf       = errors.New("fw.self_block") // the panel user's own address
	ErrNoPending  = errors.New("fw.nothing_to_confirm")
	ErrNftRejects = errors.New("fw.ruleset_rejected")
)

const (
	Table       = "lmcwhm"
	settingsKey = "firewall"
)

// ConfirmWindow is how long a "drop" policy waits for the browser to confirm
// it can still reach the panel before reverting to "accept".
var ConfirmWindow = 60 * time.Second

// Config is stored as JSON in the settings table; tokens are sealed.
type Config struct {
	Enabled bool   `json:"enabled"`
	Policy  string `json:"policy"` // accept | drop (default for new inbound connections)
	Ping    bool   `json:"ping"`   // answer ICMP echo
	// Protect is the old "always accept SSH, panel and web" switch; it only
	// decides whether the system rules that replaced it start enabled.
	Protect bool `json:"protect,omitempty"`

	LinodeToken    string `json:"linode_token,omitempty"` // sealed at rest
	LinodeFirewall int    `json:"linode_firewall"`
	LinodeSync     bool   `json:"linode_sync"`  // push the block list
	LinodePorts    bool   `json:"linode_ports"` // keep an ACCEPT rule for protected ports
	CFSync         bool   `json:"cf_sync"`      // push the block list to every Cloudflare zone
	// Unconfirmed is set while a "drop" policy waits for confirmation, so a
	// panel restart in that window reverts instead of keeping it.
	Unconfirmed bool `json:"unconfirmed,omitempty"`
}

func defaults() Config { return Config{Policy: "accept", Ping: true, Protect: true} }

type Rule struct {
	ID        int64  `json:"id"`
	Action    string `json:"action"`
	Proto     string `json:"proto"`
	Ports     string `json:"ports"`
	Source    string `json:"source"`
	Remark    string `json:"remark"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
	// System marks the rules the panel creates for itself (ssh | panel | web).
	// Their ports follow sshd and the panel settings.
	System string `json:"system,omitempty"`
}

type IP struct {
	ID        int64  `json:"id"`
	IP        string `json:"ip"`
	List      string `json:"list"` // block | allow
	Source    string `json:"source"`
	Remark    string `json:"remark"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedAt int64  `json:"created_at"`
}

type Service struct {
	st  *store.Store
	box *secrets.Box
	// PanelPort returns the current panel port (protected under "drop").
	PanelPort func() int
	// OnBlocksChanged is called (debounced) after a manual permanent block or
	// allow entry changes (what is synced to Linode / Cloudflare).
	OnBlocksChanged func()
	// OnListsChanged is called (debounced) after any block / allow / trust
	// entry or the enabled switch changes: the edge proxy blocks by the
	// visitor's real address too (behind a CDN the TCP source is the CDN).
	OnListsChanged func()
	nft            string // nft binary ("" = not installed)

	mu      sync.Mutex
	revert  *time.Timer
	pending bool
	armedAt time.Time
	syncT   *time.Timer
	listT   *time.Timer
	logf    func(string, ...any)
	seeded  atomic.Bool
}

func New(st *store.Store, box *secrets.Box, logf func(string, ...any)) *Service {
	nft, _ := exec.LookPath("nft")
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{st: st, box: box, nft: nft, logf: logf, PanelPort: func() int { return 0 }}
}

func (sv *Service) Available() bool { return sv.nft != "" }

// ---- config ----

func (sv *Service) Config(ctx context.Context) (Config, error) {
	c := defaults()
	var raw string
	err := sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	if c.LinodeToken != "" {
		if c.LinodeToken, err = sv.box.Open(c.LinodeToken); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (sv *Service) saveConfig(ctx context.Context, c Config) error {
	if c.Policy != "accept" && c.Policy != "drop" {
		return ErrBadPolicy
	}
	if c.LinodeToken != "" {
		c.LinodeToken = sv.box.Seal(c.LinodeToken)
	}
	b, _ := json.Marshal(c)
	_, err := sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingsKey, string(b))
	return err
}

// Update changes the configuration and applies it. Switching to (or staying
// on) the "drop" policy arms the revert timer; see Confirm.
func (sv *Service) Update(ctx context.Context, fn func(*Config)) (Config, bool, error) {
	c, err := sv.Config(ctx)
	if err != nil {
		return c, false, err
	}
	fn(&c)
	if err := sv.saveConfig(ctx, c); err != nil {
		return c, false, err
	}
	armed, err := sv.applyGuarded(ctx, c)
	return c, armed, err
}

// SaveCloud changes only the Cloudflare / Linode settings: the local ruleset
// is not reinstalled, so no confirmation is needed.
func (sv *Service) SaveCloud(ctx context.Context, fn func(*Config)) error {
	c, err := sv.Config(ctx)
	if err != nil {
		return err
	}
	local := [3]any{c.Enabled, c.Policy, c.Ping}
	fn(&c)
	if local != [3]any{c.Enabled, c.Policy, c.Ping} {
		return ErrBadPolicy
	}
	return sv.saveConfig(ctx, c)
}

// ---- validation ----

var portsRe = regexp.MustCompile(`^\d{1,5}(-\d{1,5})?(,\d{1,5}(-\d{1,5})?){0,29}$`)

func validPorts(p string) bool {
	if !portsRe.MatchString(p) {
		return false
	}
	for _, part := range strings.Split(p, ",") {
		a, b, isRange := strings.Cut(part, "-")
		x, _ := strconv.Atoi(a)
		y := x
		if isRange {
			y, _ = strconv.Atoi(b)
		}
		if x < 1 || y > 65535 || y < x {
			return false
		}
	}
	return true
}

// NormIP returns the canonical form of an address or CIDR ("1.2.3.4",
// "10.0.0.0/8", "2001:db8::/32"); a /32 or /128 becomes a plain address.
func NormIP(s string) (netip.Prefix, string, error) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(s); err == nil {
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen()), a.String(), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return p, "", ErrBadIP
	}
	if p.Addr().Is4In6() {
		if p.Bits() < 96 {
			return p, "", ErrBadIP
		}
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
	}
	p = p.Masked()
	if p.IsSingleIP() {
		return p, p.Addr().String(), nil
	}
	if p.Bits() == 0 {
		return p, "", ErrBadIP // never block or allow the whole Internet
	}
	return p, p.String(), nil
}

func (r *Rule) validate() error {
	r.Ports, r.Source = strings.ReplaceAll(strings.TrimSpace(r.Ports), " ", ""), strings.TrimSpace(r.Source)
	if r.Action != "accept" && r.Action != "drop" || r.Proto != "tcp" && r.Proto != "udp" && r.Proto != "both" || !validPorts(r.Ports) {
		return ErrBadRule
	}
	if r.Source != "" {
		_, norm, err := NormIP(r.Source)
		if err != nil {
			return ErrBadRule
		}
		r.Source = norm
	}
	if len(r.Remark) > 200 {
		r.Remark = r.Remark[:200]
	}
	return nil
}

// ---- rules ----

func (sv *Service) Rules(ctx context.Context) ([]Rule, error) {
	if err := sv.seedSystem(ctx); err != nil {
		return nil, err
	}
	// System rules come first, so a user drop rule cannot lock SSH out.
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, action, proto, ports, source, remark, enabled, created_at, system FROM fw_rules ORDER BY system = '', id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.Action, &r.Proto, &r.Ports, &r.Source, &r.Remark, &r.Enabled, &r.CreatedAt, &r.System); err != nil {
			return nil, err
		}
		if p := sv.systemPorts(r.System); p != "" {
			r.Ports = p
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const seededKey = "fw_system_seeded"

// seedSystem creates the system rules once (they replace the old Protect
// switch and start enabled when it was on). Deleted ones are not recreated.
func (sv *Service) seedSystem(ctx context.Context) error {
	if sv.seeded.Load() {
		return nil
	}
	var x string
	if sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, seededKey).Scan(&x) == nil {
		sv.seeded.Store(true)
		return nil
	}
	c, err := sv.Config(ctx)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, sys := range []string{"ssh", "panel", "web"} {
		ports := sv.systemPorts(sys)
		if ports == "" {
			continue
		}
		if _, err := sv.st.DB.ExecContext(ctx, `INSERT INTO fw_rules(action, proto, ports, source, remark, enabled, created_at, system) VALUES('accept','tcp',?,'','',?,?,?)`,
			ports, c.Protect, now, sys); err != nil {
			return err
		}
	}
	if _, err := sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, '1') ON CONFLICT(key) DO NOTHING`, seededKey); err != nil {
		return err
	}
	sv.seeded.Store(true)
	return nil
}

// systemPorts are the live ports of a system rule ("" for user rules).
func (sv *Service) systemPorts(sys string) string {
	switch sys {
	case "ssh":
		return strings.ReplaceAll(joinInts(SSHPorts()), " ", "")
	case "panel":
		if p := sv.PanelPort(); p > 0 {
			return strconv.Itoa(p)
		}
	case "web":
		return "80,443"
	}
	return ""
}

// SaveRule creates (ID 0) or replaces a rule and applies the ruleset.
func (sv *Service) SaveRule(ctx context.Context, r Rule) (Rule, bool, error) {
	if err := r.validate(); err != nil {
		return r, false, err
	}
	if r.ID == 0 {
		r.System = ""
		r.CreatedAt = time.Now().Unix()
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO fw_rules(action, proto, ports, source, remark, enabled, created_at) VALUES(?,?,?,?,?,?,?)`,
			r.Action, r.Proto, r.Ports, r.Source, r.Remark, r.Enabled, r.CreatedAt)
		if err != nil {
			return r, false, err
		}
		r.ID, _ = res.LastInsertId()
	} else {
		// A system rule keeps its kind; its ports follow the services.
		var sys string
		if err := sv.st.DB.QueryRowContext(ctx, `SELECT system FROM fw_rules WHERE id = ?`, r.ID).Scan(&sys); errors.Is(err, sql.ErrNoRows) {
			return r, false, ErrNotFound
		} else if err != nil {
			return r, false, err
		}
		if r.System = sys; sys != "" {
			r.Ports = sv.systemPorts(sys)
		}
		res, err := sv.st.DB.ExecContext(ctx, `UPDATE fw_rules SET action=?, proto=?, ports=?, source=?, remark=?, enabled=? WHERE id=?`,
			r.Action, r.Proto, r.Ports, r.Source, r.Remark, r.Enabled, r.ID)
		if err != nil {
			return r, false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return r, false, ErrNotFound
		}
	}
	armed, err := sv.Apply(ctx)
	return r, armed, err
}

func (sv *Service) DeleteRule(ctx context.Context, id int64) (bool, error) {
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM fw_rules WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, ErrNotFound
	}
	return sv.Apply(ctx)
}

// ---- IP lists ----

func (sv *Service) IPs(ctx context.Context, list string) ([]IP, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, ip, list, source, remark, expires_at, created_at FROM fw_ips
		WHERE (? = '' OR list = ?) AND (expires_at = 0 OR expires_at > ?) ORDER BY id DESC`, list, list, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IP{}
	for rows.Next() {
		var x IP
		if err := rows.Scan(&x.ID, &x.IP, &x.List, &x.Source, &x.Remark, &x.ExpiresAt, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// AddIP blocks or allows an address / CIDR; ttl 0 is permanent. A temporary
// block is also pushed into the live ruleset without a full reload.
func (sv *Service) AddIP(ctx context.Context, ip, list string, ttl time.Duration, source, remark string) (IP, error) {
	pfx, norm, err := NormIP(ip)
	if err != nil {
		return IP{}, err
	}
	if list != "block" && list != "allow" {
		return IP{}, ErrBadIP
	}
	x := IP{IP: norm, List: list, Source: source, Remark: remark, CreatedAt: time.Now().Unix()}
	if ttl > 0 {
		x.ExpiresAt = time.Now().Add(ttl).Unix()
	}
	res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO fw_ips(ip, list, source, remark, expires_at, created_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(ip, list) DO UPDATE SET source=excluded.source, remark=excluded.remark, expires_at=excluded.expires_at`,
		x.IP, x.List, x.Source, x.Remark, x.ExpiresAt, x.CreatedAt)
	if err != nil {
		return x, err
	}
	x.ID, _ = res.LastInsertId()
	c, _ := sv.Config(ctx)
	if c.Enabled && sv.Available() {
		if list == "block" && ttl > 0 && pfx.IsSingleIP() {
			// Fast path for bans (CrowdSec, temporary blocks): no reload.
			set := "ban4"
			if pfx.Addr().Is6() {
				set = "ban6"
			}
			if err := sv.run(ctx, fmt.Sprintf("add element inet %s %s { %s timeout %ds }\n", Table, set, norm, int(ttl.Seconds())+1)); err != nil {
				sv.logf("firewall ban", "err", err)
			}
			sv.listsChanged()
		} else if _, err := sv.Apply(ctx); err != nil {
			return x, err
		}
	}
	if source == "manual" && ttl == 0 {
		sv.blocksChanged()
	}
	return x, nil
}

func (sv *Service) DeleteIP(ctx context.Context, id int64) error {
	var x IP
	err := sv.st.DB.QueryRowContext(ctx, `SELECT ip, list, source, expires_at FROM fw_ips WHERE id = ?`, id).Scan(&x.IP, &x.List, &x.Source, &x.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return sv.removeIP(ctx, x)
}

// RemoveIP deletes an entry by address (CrowdSec decision deleted).
func (sv *Service) RemoveIP(ctx context.Context, ip, list string) error {
	_, norm, err := NormIP(ip)
	if err != nil {
		return err
	}
	var x IP
	if sv.st.DB.QueryRowContext(ctx, `SELECT ip, list, source, expires_at FROM fw_ips WHERE ip = ? AND list = ?`, norm, list).Scan(&x.IP, &x.List, &x.Source, &x.ExpiresAt) != nil {
		return nil
	}
	return sv.removeIP(ctx, x)
}

func (sv *Service) removeIP(ctx context.Context, x IP) error {
	if _, err := sv.st.DB.ExecContext(ctx, `DELETE FROM fw_ips WHERE ip = ? AND list = ?`, x.IP, x.List); err != nil {
		return err
	}
	if x.List == "block" {
		// Lifting a block cannot cut anyone off: no confirmation needed.
		if err := sv.applyBans(ctx); err != nil {
			return err
		}
	} else if c, _ := sv.Config(ctx); c.Enabled && sv.Available() {
		if _, err := sv.Apply(ctx); err != nil {
			return err
		}
	}
	if x.Source == "manual" && x.ExpiresAt == 0 {
		sv.blocksChanged()
	}
	return nil
}

// ManualIPs lists the permanent entries of list ("block" or "allow") added
// by hand in the panel: the only ones synced to Linode and Cloudflare (bans
// from CrowdSec, MCP or the lists churn too fast for their APIs).
func (sv *Service) ManualIPs(ctx context.Context, list string) ([]string, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT ip FROM fw_ips WHERE list = ? AND source = 'manual' AND expires_at = 0 ORDER BY id`, list)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var ip string
		rows.Scan(&ip)
		out = append(out, ip)
	}
	return out, rows.Err()
}

func (sv *Service) blocksChanged() {
	if sv.OnBlocksChanged == nil {
		return
	}
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.syncT != nil {
		sv.syncT.Stop()
	}
	sv.syncT = time.AfterFunc(5*time.Second, sv.OnBlocksChanged)
}

func (sv *Service) listsChanged() {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.OnListsChanged == nil {
		return
	}
	if sv.listT != nil {
		sv.listT.Stop()
	}
	sv.listT = time.AfterFunc(2*time.Second, sv.OnListsChanged)
}

// EdgeLists are the addresses the edge proxy refuses (block, bans) and
// never refuses (allow, trusted ranges), canonical; both empty while the
// firewall is off.
func (sv *Service) EdgeLists(ctx context.Context) (block, allow []string, err error) {
	c, err := sv.Config(ctx)
	if err != nil || !c.Enabled {
		return nil, nil, err
	}
	ips, err := sv.IPs(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	block, allow = []string{}, []string{}
	for _, x := range ips {
		_, norm, err := NormIP(x.IP)
		if err != nil {
			continue
		}
		if x.List == "block" {
			block = append(block, norm)
		} else {
			allow = append(allow, norm) // allow and trust
		}
	}
	sort.Strings(block)
	sort.Strings(allow)
	return block, allow, nil
}

// ---- ruleset ----

// SSHPorts reads the ports sshd listens on (default 22).
func SSHPorts() []int {
	files := []string{"/etc/ssh/sshd_config"}
	extra, _ := filepath.Glob("/etc/ssh/sshd_config.d/*.conf")
	files = append(files, extra...)
	seen := map[int]bool{}
	var out []int
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) == 2 && strings.EqualFold(fields[0], "Port") {
				if p, err := strconv.Atoi(fields[1]); err == nil && !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
			}
		}
		fh.Close()
	}
	if len(out) == 0 {
		out = []int{22}
	}
	return out
}

// ProtectedPorts are the TCP ports of the enabled system accept rules (what
// the Linode Cloud Firewall keeps open).
func (sv *Service) ProtectedPorts(ctx context.Context) []int {
	rules, err := sv.Rules(ctx)
	if err != nil {
		return nil
	}
	set := map[int]bool{}
	for _, r := range rules {
		if r.System == "" || !r.Enabled || r.Action != "accept" || r.Source != "" || r.Proto == "udp" {
			continue
		}
		for _, part := range strings.Split(r.Ports, ",") {
			if p, err := strconv.Atoi(part); err == nil {
				set[p] = true
			}
		}
	}
	out := make([]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, x := range v {
		s[i] = strconv.Itoa(x)
	}
	return strings.Join(s, ", ")
}

func elems(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return " elements = { " + strings.Join(v, ", ") + " };"
}

// Ruleset renders the table definition.
func (sv *Service) Ruleset(ctx context.Context, c Config) (string, error) {
	rules, err := sv.Rules(ctx)
	if err != nil {
		return "", err
	}
	ips, err := sv.IPs(ctx, "")
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	sets := map[string][]string{}
	for _, x := range ips {
		p, norm, err := NormIP(x.IP)
		if err != nil {
			continue
		}
		fam := "4"
		if p.Addr().Is6() {
			fam = "6"
		}
		name := x.List + fam
		if x.List == "block" && x.ExpiresAt > 0 && p.IsSingleIP() {
			sets["ban"+fam] = append(sets["ban"+fam], fmt.Sprintf("%s timeout %ds", norm, x.ExpiresAt-now+1))
			continue
		}
		sets[name] = append(sets[name], norm)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by lmcwhm from its database — edit in the panel.\ntable inet %s {\n", Table)
	for _, s := range []struct{ name, typ, flags string }{
		{"allow4", "ipv4_addr", "interval; auto-merge;"}, {"allow6", "ipv6_addr", "interval; auto-merge;"},
		{"block4", "ipv4_addr", "interval; auto-merge;"}, {"block6", "ipv6_addr", "interval; auto-merge;"},
		{"ban4", "ipv4_addr", "timeout;"}, {"ban6", "ipv6_addr", "timeout;"},
		{"trust4", "ipv4_addr", "interval; auto-merge;"}, {"trust6", "ipv6_addr", "interval; auto-merge;"},
	} {
		fmt.Fprintf(&b, "\tset %s { type %s; flags %s%s }\n", s.name, s.typ, s.flags, elems(sets[s.name]))
	}
	// Trusted ranges (Cloudflare, crawlers) are never blocked or banned, but
	// get no other exemption: port rules and the default policy still apply.
	// Every drop is logged first (rate limited: a limit that fails only skips
	// the log, never the drop), for the firewall log: TCP, UDP and ICMP alike.
	var drops strings.Builder
	for _, x := range []struct{ fam, set string }{{"ip", "block4"}, {"ip6", "block6"}, {"ip", "ban4"}, {"ip6", "ban6"}} {
		trust := "@trust" + x.set[len(x.set)-1:]
		match := fmt.Sprintf("%s saddr @%s %s saddr != %s", x.fam, x.set, x.fam, trust)
		fmt.Fprintf(&drops, "\t\t%s %s\n\t\t%s counter drop\n", match, logDrop(strings.TrimRight(x.set, "46")), match)
	}
	fmt.Fprintf(&b, "\tchain input {\n\t\ttype filter hook input priority -10; policy %s;\n", c.Policy)
	b.WriteString("\t\tiif \"lo\" accept\n\t\tip saddr @allow4 accept\n\t\tip6 saddr @allow6 accept\n")
	b.WriteString(drops.String())
	b.WriteString("\t\tct state established,related accept\n\t\tct state invalid drop\n")
	if !c.Ping {
		b.WriteString("\t\ticmp type echo-request drop\n\t\ticmpv6 type echo-request drop\n")
	}
	// ICMPv6 carries neighbour discovery: dropping it breaks IPv6 entirely.
	b.WriteString("\t\tmeta l4proto { icmp, ipv6-icmp } accept\n")
	if c.Policy == "drop" {
		// Containers reach host services (DNS, published ports) over the bridges.
		b.WriteString("\t\tiifname \"docker0\" accept\n\t\tiifname \"br-*\" accept\n")
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		src := ""
		if r.Source != "" {
			p, _, _ := NormIP(r.Source)
			if p.Addr().Is6() {
				src = "ip6 saddr " + r.Source + " "
			} else {
				src = "ip saddr " + r.Source + " "
			}
		}
		ports := strings.ReplaceAll(r.Ports, ",", ", ")
		protos := []string{r.Proto}
		if r.Proto == "both" {
			protos = []string{"tcp", "udp"}
		}
		comment := "rule " + strconv.FormatInt(r.ID, 10)
		if r.System != "" {
			comment = "system " + r.System
		}
		for _, p := range protos {
			if r.Action == "drop" {
				fmt.Fprintf(&b, "\t\t%s%s dport { %s } %s\n", src, p, ports, logDrop("rule"+strconv.FormatInt(r.ID, 10)))
			}
			fmt.Fprintf(&b, "\t\t%s%s dport { %s } %s comment \"%s\"\n", src, p, ports, r.Action, comment)
		}
	}
	if c.Policy == "drop" {
		b.WriteString("\t\tct state new " + logDrop("policy") + "\n")
	}
	b.WriteString("\t}\n")
	// Docker-published ports are forwarded, not delivered to input.
	b.WriteString("\tchain forward {\n\t\ttype filter hook forward priority -10; policy accept;\n\t\tip saddr @allow4 accept\n\t\tip6 saddr @allow6 accept\n")
	b.WriteString(drops.String())
	b.WriteString("\t}\n}\n")
	return b.String(), nil
}

// DropPrefix starts the kernel log line of every logged drop.
const DropPrefix = "lmcwhm-drop "

// logDrop logs a packet that the next rule drops, at most 10 a second.
func logDrop(reason string) string {
	return fmt.Sprintf("limit rate 10/second burst 20 packets log prefix \"%s%s \" level info", DropPrefix, reason)
}

func (sv *Service) run(ctx context.Context, script string, args ...string) error {
	if !sv.Available() {
		return ErrNoNft
	}
	cmd := exec.CommandContext(ctx, sv.nft, append(args, "-f", "-")...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", ErrNftRejects, strings.TrimSpace(string(out)))
	}
	return nil
}

// install replaces the table atomically (one nft transaction), after a dry run.
func (sv *Service) install(ctx context.Context, c Config) error {
	if !c.Enabled {
		return sv.run(ctx, fmt.Sprintf("table inet %[1]s\ndelete table inet %[1]s\n", Table))
	}
	rs, err := sv.Ruleset(ctx, c)
	if err != nil {
		return err
	}
	script := fmt.Sprintf("table inet %[1]s\ndelete table inet %[1]s\n", Table) + rs
	if err := sv.run(ctx, script, "-c"); err != nil {
		return err
	}
	return sv.run(ctx, script)
}

// Apply installs the current configuration; see Update for the drop guard.
func (sv *Service) Apply(ctx context.Context) (bool, error) {
	c, err := sv.Config(ctx)
	if err != nil {
		return false, err
	}
	return sv.applyGuarded(ctx, c)
}

func (sv *Service) applyGuarded(ctx context.Context, c Config) (bool, error) {
	sv.listsChanged()
	if !sv.Available() {
		if !c.Enabled {
			return false, nil
		}
		return false, ErrNoNft
	}
	if err := sv.install(ctx, c); err != nil {
		return false, err
	}
	if !c.Enabled || c.Policy != "drop" {
		sv.disarm()
		if c.Unconfirmed {
			c.Unconfirmed = false
			sv.saveConfig(ctx, c)
		}
		return false, nil
	}
	c.Unconfirmed = true
	if err := sv.saveConfig(ctx, c); err != nil {
		return false, err
	}
	sv.arm()
	return true, nil
}

func (sv *Service) arm() {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.revert != nil {
		sv.revert.Stop()
	}
	sv.pending, sv.armedAt = true, time.Now()
	sv.revert = time.AfterFunc(ConfirmWindow, func() {
		sv.mu.Lock()
		if !sv.pending {
			sv.mu.Unlock()
			return
		}
		sv.pending = false
		sv.mu.Unlock()
		ctx := context.Background()
		c, err := sv.Config(ctx)
		if err != nil {
			return
		}
		c.Policy, c.Unconfirmed = "accept", false
		sv.saveConfig(ctx, c)
		if err := sv.install(ctx, c); err != nil {
			sv.logf("firewall revert", "err", err)
			return
		}
		sv.logf("firewall: drop policy not confirmed, reverted to accept")
	})
}

func (sv *Service) disarm() {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.revert != nil {
		sv.revert.Stop()
	}
	sv.pending = false
}

// Pending reports whether a change still waits for confirmation.
func (sv *Service) Pending() bool {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return sv.pending
}

// ErrOldConnection: a confirmation over a connection opened before the
// ruleset changed proves nothing (established connections are accepted).
var ErrOldConnection = errors.New("fw.confirm_old_connection")

// Confirm keeps the applied ruleset: the browser could still reach the panel
// over a connection opened at connStart (after the change).
func (sv *Service) Confirm(ctx context.Context, connStart time.Time) error {
	sv.mu.Lock()
	if !sv.pending {
		sv.mu.Unlock()
		return ErrNoPending
	}
	if connStart.Before(sv.armedAt) {
		sv.mu.Unlock()
		return ErrOldConnection
	}
	sv.pending = false
	sv.revert.Stop()
	sv.mu.Unlock()
	c, err := sv.Config(ctx)
	if err != nil {
		return err
	}
	c.Unconfirmed = false
	return sv.saveConfig(ctx, c)
}

// Boot reinstalls the table after a reboot (nftables state is not persistent)
// and expires temporary entries every minute.
func (sv *Service) Boot(ctx context.Context) {
	c, err := sv.Config(ctx)
	if err == nil && c.Unconfirmed {
		// The panel restarted before a drop policy was confirmed.
		c.Policy, c.Unconfirmed = "accept", false
		sv.saveConfig(ctx, c)
		sv.logf("firewall: unconfirmed drop policy reverted to accept at start")
	}
	if err := sv.seedSystem(ctx); err != nil {
		sv.logf("firewall: system rules", "err", err)
	}
	if err == nil && c.Enabled && sv.Available() {
		if err := sv.install(ctx, c); err != nil {
			sv.logf("firewall boot", "err", err)
		}
	}
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM fw_ips WHERE expires_at > 0 AND expires_at <= ?`, time.Now().Unix())
				if n, _ := res.RowsAffected(); err == nil && n > 0 {
					sv.listsChanged()
				}
			}
		}
	}()
}

// Live returns `nft list table` for the status view.
func (sv *Service) Live(ctx context.Context) string {
	if !sv.Available() {
		return ""
	}
	out, _ := exec.CommandContext(ctx, sv.nft, "list", "table", "inet", Table).CombinedOutput()
	return string(out)
}

// Ban is one entry of a bulk update (CrowdSec decision stream).
type Ban struct {
	IP     string
	TTL    time.Duration
	Remark string
}

// SyncBans adds and removes temporary blocks of one source in a single
// transaction and reloads the ruleset once (the community blocklist alone
// can carry tens of thousands of addresses).
func (sv *Service) SyncBans(ctx context.Context, source string, add []Ban, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	tx, err := sv.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	for _, ip := range remove {
		if _, norm, err := NormIP(ip); err == nil {
			tx.ExecContext(ctx, `DELETE FROM fw_ips WHERE ip = ? AND list = 'block' AND source = ?`, norm, source)
		}
	}
	for _, b := range add {
		_, norm, err := NormIP(b.IP)
		if err != nil || b.TTL <= 0 {
			continue
		}
		// Never shorten or replace a manual block with a ban.
		if _, err := tx.ExecContext(ctx, `INSERT INTO fw_ips(ip, list, source, remark, expires_at, created_at) VALUES(?, 'block', ?, ?, ?, ?)
			ON CONFLICT(ip, list) DO UPDATE SET expires_at = excluded.expires_at, remark = excluded.remark
			WHERE fw_ips.source = excluded.source`, norm, source, b.Remark, now.Add(b.TTL).Unix(), now.Unix()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return sv.applyBans(ctx)
}

// applyBans reinstalls the ruleset after only ban entries changed. It does
// not arm the drop-policy confirmation: bans cannot be what locks the admin
// out (the allow list is matched first), and nobody is there to confirm a
// background sync, so arming would revert "drop" to "accept".
func (sv *Service) applyBans(ctx context.Context) error {
	sv.listsChanged()
	c, err := sv.Config(ctx)
	if err != nil || !c.Enabled || !sv.Available() {
		return err
	}
	return sv.install(ctx, c)
}
