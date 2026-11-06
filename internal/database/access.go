package database

// Remote access per database (MySQL / MariaDB, like aaPanel's 权限):
//
//   - local: the user connects from this server only — the socket
//     ("localhost"), loopback, and the Docker bridges (the host reaches the
//     container through docker0's gateway);
//   - all:   from anywhere ('%');
//   - ips:   from this server and the listed IPv4 / IPv6 addresses.
//
// While a database on an engine allows remote access the engine publishes
// its port on every address (container recreated once), and a firewall
// port rule lets the chosen sources in; the firewall guard drops the rest
// (see firewall/guard.go). Back to local, the rule the panel added goes.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

var (
	ErrAccessUnsupported = errors.New("db.access_unsupported")
	ErrBadAccess         = errors.New("db.invalid_access")
	ErrBadIP             = errors.New("db.invalid_ip")
)

const (
	AccessLocal = "local"
	AccessAll   = "all"
	AccessIPs   = "ips"
)

type Access struct {
	Mode string   `json:"mode"`
	IPs  []string `json:"ips"`
	// RuleID is the firewall rule that admits the sources (0 = none yet);
	// RuleOwned: the panel added it (and removes it when no longer needed).
	RuleID    int64 `json:"rule_id"`
	RuleOwned bool  `json:"rule_owned"`
	// Public: the engine's port is published on every address.
	Public bool `json:"public"`
}

// Firewall is what access needs from the firewall service.
type Firewall interface {
	EnsureRule(ctx context.Context, port int, sources []string, remark string) (int64, bool, error)
	RemoveRuleQuiet(ctx context.Context, id int64) error
	ApplyQuiet(ctx context.Context) error
}

func accessEngine(app string) bool { return app == "mysql" || app == "mariadb" }

// SupportsAccess reports whether the database's engine has per-database access.
func SupportsAccess(engine string) bool { return accessEngine(engine) }

// GetAccess returns the database's access (local when never set).
func (sv *Service) GetAccess(ctx context.Context, id int64) (Access, error) {
	d, err := sv.Get(ctx, id)
	if err != nil {
		return Access{}, err
	}
	a, _ := sv.access(ctx, id)
	if eng, err := sv.apps.Get(ctx, d.AppID); err == nil {
		a.Public = eng.Public
	}
	return a, nil
}

// AccessModes maps database id → mode for the list.
func (sv *Service) AccessModes(ctx context.Context) map[int64]Access {
	out := map[int64]Access{}
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT db_id, mode, ips, rule_id, rule_owned FROM db_access`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var a Access
		var ips string
		if rows.Scan(&id, &a.Mode, &ips, &a.RuleID, &a.RuleOwned) == nil {
			a.IPs = splitIPs(ips)
			out[id] = a
		}
	}
	return out
}

func (sv *Service) access(ctx context.Context, id int64) (Access, bool) {
	a := Access{Mode: AccessLocal, IPs: []string{}}
	var ips string
	err := sv.st.DB.QueryRowContext(ctx, `SELECT mode, ips, rule_id, rule_owned FROM db_access WHERE db_id = ?`, id).Scan(&a.Mode, &ips, &a.RuleID, &a.RuleOwned)
	if err != nil {
		return a, false
	}
	a.IPs = splitIPs(ips)
	return a, true
}

func splitIPs(s string) []string {
	out := []string{}
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// normAccess validates the mode and addresses: single IPv4 / IPv6 addresses
// and IPv4 networks (MySQL has no IPv6 network syntax).
func normAccess(mode string, ips []string) (string, []string, error) {
	switch mode {
	case AccessLocal, AccessAll:
		return mode, []string{}, nil
	case AccessIPs:
	default:
		return "", nil, ErrBadAccess
	}
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range ips {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var norm string
		if strings.Contains(raw, "/") {
			p, err := netip.ParsePrefix(raw)
			if err != nil || !p.Addr().Is4() {
				return "", nil, ErrBadIP
			}
			norm = p.Masked().String()
			if p.Bits() == 32 {
				norm = p.Addr().String()
			}
		} else {
			a, err := netip.ParseAddr(raw)
			if err != nil || a.Zone() != "" {
				return "", nil, ErrBadIP
			}
			norm = a.Unmap().String()
		}
		if !seen[norm] {
			seen[norm] = true
			out = append(out, norm)
		}
	}
	if len(out) == 0 || len(out) > 50 {
		return "", nil, ErrBadIP
	}
	return mode, out, nil
}

// mysqlHost turns an address into a MySQL account host: networks use the
// netmask form both MySQL and MariaDB accept ("10.0.0.0/255.0.0.0").
func mysqlHost(ip string) string {
	p, err := netip.ParsePrefix(ip)
	if err != nil {
		return ip
	}
	m := ^uint32(0) << (32 - p.Bits())
	return fmt.Sprintf("%s/%d.%d.%d.%d", p.Masked().Addr(), m>>24, m>>16&255, m>>8&255, m&255)
}

// localHosts are the hosts a local client shows up as.
func localHosts(ctx context.Context) []string {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	nets := map[string]bool{}
	if out, err := dockerx.Run(ctx, "network", "ls", "--filter", "driver=bridge", "-q"); err == nil {
		for _, id := range strings.Fields(out) {
			sub, err := dockerx.Run(ctx, "network", "inspect", "-f", "{{range .IPAM.Config}}{{.Subnet}} {{end}}", id)
			if err != nil {
				continue
			}
			for _, s := range strings.Fields(sub) {
				if p, err := netip.ParsePrefix(s); err == nil && p.Addr().Is4() {
					nets[p.Masked().String()] = true
				}
			}
		}
	}
	if len(nets) == 0 {
		nets["172.17.0.0/16"] = true // Docker's default bridge
	}
	for n := range nets {
		hosts = append(hosts, mysqlHost(n))
	}
	return hosts
}

func (sv *Service) wantHosts(ctx context.Context, a Access) []string {
	switch a.Mode {
	case AccessAll:
		return []string{"%"}
	case AccessIPs:
		h := localHosts(ctx)
		for _, ip := range a.IPs {
			h = append(h, mysqlHost(ip))
		}
		return h
	}
	return localHosts(ctx)
}

// userHosts lists the hosts the user has accounts for.
func (sv *Service) userHosts(ctx context.Context, eng appstore.Installed, user string) ([]string, error) {
	out, err := sv.apps.Query(ctx, eng, fmt.Sprintf("SELECT Host FROM mysql.user WHERE User = '%s';\n", user))
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			hosts = append(hosts, l)
		}
	}
	return hosts, nil
}

// hostRe keeps hosts we write into SQL to address characters.
func safeHost(h string) bool {
	for _, r := range h {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || strings.ContainsRune(".:/%_-", r)) {
			return false
		}
	}
	return h != ""
}

// reconcile gives the database's user exactly the accounts its access wants.
func (sv *Service) reconcile(ctx context.Context, eng appstore.Installed, d Database, a Access, log tasks.Logf) error {
	want := sv.wantHosts(ctx, a)
	have, err := sv.userHosts(ctx, eng, d.Username)
	if err != nil {
		return err
	}
	wantSet, haveSet := map[string]bool{}, map[string]bool{}
	for _, h := range want {
		wantSet[h] = true
	}
	for _, h := range have {
		haveSet[h] = true
	}
	var b strings.Builder
	for _, h := range want {
		if !haveSet[h] && safeHost(h) {
			fmt.Fprintf(&b, "CREATE USER IF NOT EXISTS '%[1]s'@'%[2]s' IDENTIFIED BY '%[3]s';\nGRANT ALL PRIVILEGES ON `%[4]s`.* TO '%[1]s'@'%[2]s';\n", d.Username, h, d.Password, d.Name)
		}
	}
	for _, h := range have {
		if !wantSet[h] && safeHost(h) {
			fmt.Fprintf(&b, "DROP USER IF EXISTS '%s'@'%s';\n", d.Username, h)
		}
	}
	if b.Len() == 0 {
		return nil
	}
	b.WriteString("FLUSH PRIVILEGES;\n")
	log("%s: user %s may connect from %s", d.Name, d.Username, strings.Join(want, ", "))
	return sv.exec(ctx, eng, b.String())
}

// SetAccess changes who may connect to the database (task).
func (sv *Service) SetAccess(ctx context.Context, id int64, mode string, ips []string) (string, error) {
	d, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if !accessEngine(d.Engine) {
		return "", ErrAccessUnsupported
	}
	if sv.Firewall == nil {
		return "", ErrAccessUnsupported
	}
	mode, ips, err = normAccess(mode, ips)
	if err != nil {
		return "", err
	}
	return sv.tasks.Start("db.access:"+d.Name, func(ctx context.Context, log tasks.Logf) error {
		eng, err := sv.engine(ctx, d.AppID)
		if err != nil {
			return err
		}
		old, _ := sv.access(ctx, id)
		a := Access{Mode: mode, IPs: ips}
		// The firewall rule first: a rule the panel added for the old setting goes.
		if old.RuleOwned && old.RuleID != 0 && old.Mode != AccessLocal {
			if sv.othersUse(ctx, id, old.RuleID) == 0 {
				log("removing firewall rule #%d", old.RuleID)
				if err := sv.Firewall.RemoveRuleQuiet(ctx, old.RuleID); err != nil {
					return err
				}
			}
		}
		if mode != AccessLocal {
			var src []string
			if mode == AccessIPs {
				src = ips
			}
			rid, created, err := sv.Firewall.EnsureRule(ctx, eng.Port, src, fmt.Sprintf("%s%s (%s %s)", ruleRemarkPrefix, d.Name, eng.Name, eng.Version))
			if err != nil {
				return err
			}
			a.RuleID, a.RuleOwned = rid, created || (old.RuleOwned && old.RuleID == rid)
			if created {
				log("firewall: added rule #%d for TCP %d", rid, eng.Port)
			} else {
				log("firewall: rule #%d already lets these sources reach TCP %d", rid, eng.Port)
			}
		}
		if _, err := sv.st.DB.ExecContext(ctx, `INSERT INTO db_access(db_id, mode, ips, rule_id, rule_owned) VALUES(?,?,?,?,?)
			ON CONFLICT(db_id) DO UPDATE SET mode = excluded.mode, ips = excluded.ips, rule_id = excluded.rule_id, rule_owned = excluded.rule_owned`,
			id, mode, strings.Join(ips, ","), a.RuleID, a.RuleOwned); err != nil {
			return err
		}
		// Publish (or stop publishing) the engine's port, then fix the accounts.
		if err := sv.syncEngine(ctx, eng, log); err != nil {
			return err
		}
		return sv.reconcile(ctx, eng, d, a, log)
	}), nil
}

// ruleRemarkPrefix starts the remark of firewall rules added for a database.
const ruleRemarkPrefix = "數據庫遠端存取："

// othersUse counts the other databases relying on the firewall rule.
func (sv *Service) othersUse(ctx context.Context, id, rule int64) int {
	var n int
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM db_access WHERE rule_id = ? AND db_id != ? AND mode != 'local'`, rule, id).Scan(&n)
	return n
}

// syncEngine publishes the engine's port while any of its databases allows
// remote access, and keeps its other databases' accounts local then.
func (sv *Service) syncEngine(ctx context.Context, eng appstore.Installed, log tasks.Logf) error {
	var remote int
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM db_access a JOIN databases d ON d.id = a.db_id
		WHERE d.app_id = ? AND a.mode != 'local'`, eng.ID).Scan(&remote); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	on := remote > 0
	if on != eng.Public {
		if err := sv.apps.SetPublic(ctx, eng.ID, on, log); err != nil {
			return err
		}
		eng.Public = on
		if on {
			// Accounts that were '%' while the port was loopback-only become local.
			dbs, _ := sv.query(ctx, "WHERE d.app_id = ?", eng.ID)
			for _, o := range dbs {
				if a, _ := sv.access(ctx, o.ID); a.Mode == AccessLocal {
					if err := sv.reconcile(ctx, eng, o, a, log); err != nil {
						return err
					}
				}
			}
		}
	}
	return sv.Firewall.ApplyQuiet(ctx) // the guard follows the published ports
}

// dropAccess cleans up after a database is dropped (inside its task).
func (sv *Service) dropAccess(ctx context.Context, d Database, log tasks.Logf) {
	a, ok := sv.access(ctx, d.ID)
	if !ok || sv.Firewall == nil {
		return
	}
	if a.Mode != AccessLocal && a.RuleOwned && a.RuleID != 0 && sv.othersUse(ctx, d.ID, a.RuleID) == 0 {
		sv.Firewall.RemoveRuleQuiet(ctx, a.RuleID)
	}
	sv.st.DB.ExecContext(ctx, `DELETE FROM db_access WHERE db_id = ?`, d.ID)
	if eng, err := sv.engine(ctx, d.AppID); err == nil {
		if err := sv.syncEngine(ctx, eng, log); err != nil {
			log("access: %v", err)
		}
	}
}
