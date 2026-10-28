// Package secscan is the host security baseline scan (寶塔安全風險 / 1Panel
// 安全報告): SSH, accounts, updates, kernel settings, exposed ports and
// website files. Results are scored and stored; the panel renders the report.
package secscan

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	psnet "github.com/shirou/gopsutil/v4/net"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

var ErrNotFound = errors.New("secscan.not_found")

// Finding is one failed check. Fix names a one-click repair ("" = manual).
type Finding struct {
	Code     string `json:"code"`
	Level    string `json:"level"` // critical | warning | info
	Category string `json:"category"`
	Value    string `json:"value,omitempty"`
	Fix      string `json:"fix,omitempty"`
	Route    string `json:"route,omitempty"` // panel page where it is fixed
}

type Report struct {
	ID        int64     `json:"id"`
	CreatedAt int64     `json:"created_at"`
	Score     int       `json:"score"`
	Checks    int       `json:"checks"`
	Findings  []Finding `json:"findings"`
	Hostname  string    `json:"hostname"`
	Millis    int64     `json:"millis"`
}

// Input is what the panel knows that the host cannot tell.
type Input struct {
	PanelFindings []Finding      // panel settings checks (entry, 2FA, ...)
	EOLPHP        map[string]int // EOL PHP version -> sites
	WebRoot       string         // /www/wwwroot
	AllowedPorts  map[int]bool   // ports the panel itself opens (80, 443, SSH, panel, FTP)
}

type Service struct {
	st   *store.Store
	Root string // host path prefix (tests)
	Run  func(ctx context.Context, name string, args ...string) (string, error)
}

func New(st *store.Store) *Service {
	return &Service{st: st, Run: func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, name, args...).Output()
		return string(out), err
	}}
}

func (sv *Service) path(p string) string { return filepath.Join(sv.Root, p) }

// Scan runs every check and stores the report.
func (sv *Service) Scan(ctx context.Context, in Input) (Report, error) {
	start := time.Now()
	r := Report{CreatedAt: start.Unix(), Findings: []Finding{}}
	r.Hostname, _ = os.Hostname()
	add := func(f Finding) { r.Findings = append(r.Findings, f) }
	for _, f := range in.PanelFindings {
		f.Category = "panel"
		add(f)
	}
	checks := []func(context.Context, Input, func(Finding)){
		sv.checkSSH, sv.checkAccounts, sv.checkUpdates, sv.checkKernel, sv.checkPorts, sv.checkWeb,
	}
	for _, c := range checks {
		c(ctx, in, add)
	}
	r.Checks = 22 + len(in.PanelFindings)
	r.Score = Score(r.Findings)
	sort.SliceStable(r.Findings, func(i, j int) bool { return rank(r.Findings[i].Level) < rank(r.Findings[j].Level) })
	r.Millis = time.Since(start).Milliseconds()
	b, _ := json.Marshal(r.Findings)
	res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO security_scans(created_at, score, findings, hostname, millis) VALUES(?,?,?,?,?)`,
		r.CreatedAt, r.Score, string(b), r.Hostname, r.Millis)
	if err != nil {
		return r, err
	}
	r.ID, _ = res.LastInsertId()
	sv.st.DB.ExecContext(ctx, `DELETE FROM security_scans WHERE id NOT IN (SELECT id FROM security_scans ORDER BY id DESC LIMIT 50)`)
	return r, nil
}

func rank(l string) int {
	switch l {
	case "critical":
		return 0
	case "warning":
		return 1
	}
	return 2
}

// Score starts at 100: -20 per critical, -8 per warning, -2 per info.
func Score(fs []Finding) int {
	s := 100
	for _, f := range fs {
		switch f.Level {
		case "critical":
			s -= 20
		case "warning":
			s -= 8
		default:
			s -= 2
		}
	}
	return max(s, 0)
}

func (sv *Service) List(ctx context.Context) ([]Report, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, created_at, score, findings, hostname, millis FROM security_scans ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Report{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scan(row interface{ Scan(...any) error }) (Report, error) {
	var r Report
	var f string
	if err := row.Scan(&r.ID, &r.CreatedAt, &r.Score, &f, &r.Hostname, &r.Millis); err != nil {
		return r, err
	}
	json.Unmarshal([]byte(f), &r.Findings)
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	return r, nil
}

func (sv *Service) Get(ctx context.Context, id int64) (Report, error) {
	r, err := scan(sv.st.DB.QueryRowContext(ctx, `SELECT id, created_at, score, findings, hostname, millis FROM security_scans WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// Latest returns the newest report (ok=false when none).
func (sv *Service) Latest(ctx context.Context) (Report, bool) {
	r, err := scan(sv.st.DB.QueryRowContext(ctx, `SELECT id, created_at, score, findings, hostname, millis FROM security_scans ORDER BY id DESC LIMIT 1`))
	return r, err == nil
}

// ---- checks ----

func (sv *Service) sshd(ctx context.Context) map[string]string {
	m := map[string]string{}
	out, err := sv.Run(ctx, "sshd", "-T")
	if err != nil {
		return m
	}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), " "); ok {
			if _, dup := m[k]; !dup {
				m[k] = v
			}
		}
	}
	return m
}

func (sv *Service) checkSSH(ctx context.Context, _ Input, add func(Finding)) {
	c := sv.sshd(ctx)
	if len(c) == 0 {
		return
	}
	pw := c["passwordauthentication"] == "yes"
	if c["permitrootlogin"] == "yes" {
		lvl := "warning"
		if pw {
			lvl = "critical"
		}
		add(Finding{Code: "ssh_root_login", Level: lvl, Category: "ssh", Fix: "ssh_root", Route: "toolbox"})
	}
	if pw {
		add(Finding{Code: "ssh_password_auth", Level: "warning", Category: "ssh", Route: "toolbox"})
	}
	if c["port"] == "22" {
		add(Finding{Code: "ssh_default_port", Level: "info", Category: "ssh", Route: "toolbox"})
	}
	if n, _ := strconv.Atoi(c["maxauthtries"]); n > 6 {
		add(Finding{Code: "ssh_max_auth_tries", Level: "info", Category: "ssh", Value: c["maxauthtries"], Route: "toolbox"})
	}
	if c["permitemptypasswords"] == "yes" {
		add(Finding{Code: "ssh_empty_passwords", Level: "critical", Category: "ssh", Route: "toolbox"})
	}
	if c["x11forwarding"] == "yes" {
		add(Finding{Code: "ssh_x11", Level: "info", Category: "ssh"})
	}
}

func (sv *Service) checkAccounts(_ context.Context, _ Input, add func(Finding)) {
	if b, err := os.ReadFile(sv.path("/etc/passwd")); err == nil {
		var root []string
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Split(l, ":")
			if len(f) > 3 && f[2] == "0" && f[0] != "root" {
				root = append(root, f[0])
			}
		}
		if len(root) > 0 {
			add(Finding{Code: "uid0_users", Level: "critical", Category: "accounts", Value: strings.Join(root, ", ")})
		}
	}
	if b, err := os.ReadFile(sv.path("/etc/shadow")); err == nil {
		var empty []string
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Split(l, ":")
			if len(f) > 1 && f[0] != "" && f[1] == "" {
				empty = append(empty, f[0])
			}
		}
		if len(empty) > 0 {
			add(Finding{Code: "empty_passwords", Level: "critical", Category: "accounts", Value: strings.Join(empty, ", "), Fix: "lock_empty"})
		}
	}
}

func (sv *Service) checkUpdates(ctx context.Context, _ Input, add func(Finding)) {
	if _, err := os.Stat(sv.path("/var/run/reboot-required")); err == nil {
		add(Finding{Code: "reboot_required", Level: "warning", Category: "system"})
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if out, err := sv.Run(ctx, "apt-get", "-s", "-o", "Debug::NoLocking=1", "upgrade"); err == nil {
		total, security := 0, 0
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "Inst ") {
				total++
				if strings.Contains(l, "-security") {
					security++
				}
			}
		}
		switch {
		case security > 0:
			add(Finding{Code: "security_updates", Level: "critical", Category: "system", Value: strconv.Itoa(security), Fix: "apt_upgrade"})
		case total > 0:
			add(Finding{Code: "pending_updates", Level: "info", Category: "system", Value: strconv.Itoa(total), Fix: "apt_upgrade"})
		}
	}
	if b, err := os.ReadFile(sv.path("/etc/apt/apt.conf.d/20auto-upgrades")); err != nil || !strings.Contains(string(b), `Unattended-Upgrade "1"`) {
		add(Finding{Code: "no_auto_updates", Level: "info", Category: "system", Fix: "auto_updates"})
	}
}

func readSysctl(root, key string) string {
	b, err := os.ReadFile(filepath.Join(root, "/proc/sys", strings.ReplaceAll(key, ".", "/")))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Sysctls are the kernel settings checked and fixed by "sysctl_harden".
var Sysctls = map[string]string{
	"kernel.randomize_va_space":             "2",
	"net.ipv4.tcp_syncookies":               "1",
	"net.ipv4.conf.all.accept_redirects":    "0",
	"net.ipv4.conf.all.send_redirects":      "0",
	"net.ipv4.conf.all.accept_source_route": "0",
	"kernel.kptr_restrict":                  "1",
}

func (sv *Service) checkKernel(_ context.Context, _ Input, add func(Finding)) {
	var bad []string
	for k, want := range Sysctls {
		if v := readSysctl(sv.Root, k); v != "" && v != want && !(k == "kernel.kptr_restrict" && v == "2") {
			bad = append(bad, k+"="+v)
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		add(Finding{Code: "sysctl", Level: "warning", Category: "system", Value: strings.Join(bad, ", "), Fix: "sysctl_harden"})
	}
}

// exposedPorts lists TCP ports listening on all addresses that the panel
// does not open itself (databases, debug servers, ...).
func (sv *Service) checkPorts(ctx context.Context, in Input, add func(Finding)) {
	cs, err := psnet.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		return
	}
	seen := map[int]bool{}
	var open []string
	for _, c := range cs {
		if c.Status != "LISTEN" {
			continue
		}
		ip := net.ParseIP(c.Laddr.IP)
		if ip == nil || ip.IsLoopback() {
			continue
		}
		p := int(c.Laddr.Port)
		if seen[p] || in.AllowedPorts[p] || p >= 39000 && p <= 39100 { // FTP passive range
			continue
		}
		seen[p] = true
		open = append(open, strconv.Itoa(p))
		if p == 2375 || p == 2376 {
			add(Finding{Code: "docker_api_exposed", Level: "critical", Category: "network", Value: strconv.Itoa(p)})
		}
	}
	if len(open) > 0 {
		sort.Slice(open, func(i, j int) bool { a, _ := strconv.Atoi(open[i]); b, _ := strconv.Atoi(open[j]); return a < b })
		add(Finding{Code: "exposed_ports", Level: "warning", Category: "network", Value: strings.Join(open, ", "), Route: "security"})
	}
}

func (sv *Service) checkWeb(_ context.Context, in Input, add func(Finding)) {
	if len(in.EOLPHP) > 0 {
		var vs []string
		for v, n := range in.EOLPHP {
			vs = append(vs, fmt.Sprintf("PHP %s (%d)", v, n))
		}
		sort.Strings(vs)
		add(Finding{Code: "php_eol", Level: "warning", Category: "web", Value: strings.Join(vs, ", "), Route: "website"})
	}
	if in.WebRoot == "" {
		return
	}
	root := sv.path(in.WebRoot)
	var writable, uploadsPHP []string
	n := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if n++; n > 200000 { // very large trees: sample the first 200k entries
			return filepath.SkipAll
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		fi, err := d.Info()
		if err != nil || fi.Mode()&fs.ModeSymlink != 0 {
			return nil
		}
		rel := strings.TrimPrefix(p, sv.Root)
		if fi.Mode().Perm()&0o002 != 0 && !(fi.IsDir() && fi.Mode()&fs.ModeSticky != 0) && len(writable) < 20 {
			writable = append(writable, rel)
		}
		if !d.IsDir() && strings.Contains(p, "/wp-content/uploads/") && strings.HasSuffix(strings.ToLower(p), ".php") && len(uploadsPHP) < 20 {
			uploadsPHP = append(uploadsPHP, rel)
		}
		return nil
	})
	if len(writable) > 0 {
		add(Finding{Code: "world_writable", Level: "warning", Category: "web", Value: strings.Join(writable, ", "), Fix: "chmod_web"})
	}
	if len(uploadsPHP) > 0 {
		add(Finding{Code: "php_in_uploads", Level: "critical", Category: "web", Value: strings.Join(uploadsPHP, ", "), Route: "security"})
	}
}

// ---- fixes ----

var ErrFix = errors.New("secscan.unknown_fix")

// Fix applies a one-click repair for host findings (panel and SSH fixes are
// done by the server through the settings / toolbox services).
func (sv *Service) Fix(ctx context.Context, code, webRoot string, log func(string, ...any)) error {
	switch code {
	case "sysctl_harden":
		var b strings.Builder
		b.WriteString("# lmcwhm security scan hardening\n")
		keys := make([]string, 0, len(Sysctls))
		for k := range Sysctls {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s = %s\n", k, Sysctls[k])
		}
		if err := os.WriteFile(sv.path("/etc/sysctl.d/90-lmcwhm.conf"), []byte(b.String()), 0o644); err != nil {
			return err
		}
		_, err := sv.Run(ctx, "sysctl", "-p", "/etc/sysctl.d/90-lmcwhm.conf")
		return err
	case "apt_upgrade":
		log("apt-get update")
		if _, err := sv.Run(ctx, "apt-get", "update", "-q"); err != nil {
			return err
		}
		log("apt-get upgrade (security and bug fixes; held packages are kept)")
		_, err := sv.Run(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "-y", "-q",
			"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "upgrade")
		return err
	case "auto_updates":
		if _, err := sv.Run(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "-q", "unattended-upgrades"); err != nil {
			return err
		}
		return os.WriteFile(sv.path("/etc/apt/apt.conf.d/20auto-upgrades"),
			[]byte("APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n"), 0o644)
	case "lock_empty":
		b, err := os.ReadFile(sv.path("/etc/shadow"))
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			f := strings.Split(sc.Text(), ":")
			if len(f) > 1 && f[0] != "" && f[1] == "" {
				log("locking %s", f[0])
				if _, err := sv.Run(ctx, "passwd", "-l", f[0]); err != nil {
					return err
				}
			}
		}
		return nil
	case "chmod_web":
		if webRoot == "" {
			return ErrFix
		}
		return filepath.WalkDir(sv.path(webRoot), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			fi, err := d.Info()
			if err == nil && fi.Mode()&fs.ModeSymlink == 0 && fi.Mode().Perm()&0o002 != 0 && fi.Mode()&fs.ModeSticky == 0 {
				os.Chmod(p, fi.Mode().Perm()&^0o002)
			}
			return nil
		})
	}
	return ErrFix
}
