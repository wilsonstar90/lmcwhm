// Package mail runs an optional mail server (aaPanel Mail Server) with
// docker-mailserver: Postfix + Dovecot + OpenDKIM + Fail2ban in one
// container. The panel manages domains, mailboxes, aliases and DKIM keys
// through the image's `setup` command, writes the DNS records through
// Cloudflare and checks deliverability (MX, SPF, DKIM, DMARC, PTR, port 25).
package mail

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

const (
	Image     = "ghcr.io/docker-mailserver/docker-mailserver:15"
	Container = "lmcwhm-mail"
	Selector  = "mail" // DKIM selector the image uses
)

// Ports the container publishes (SMTP, submissions, submission, IMAP, IMAPS).
var Ports = []int{25, 465, 587, 143, 993}

var (
	ErrHostname   = errors.New("mail.invalid_hostname")
	ErrDomain     = errors.New("mail.invalid_domain")
	ErrAddress    = errors.New("mail.invalid_address")
	ErrPassword   = errors.New("mail.weak_password")
	ErrNotRunning = errors.New("mail.not_running")
	ErrNoDomain   = errors.New("mail.unknown_domain")
	ErrPortBusy   = errors.New("mail.port_busy")
	ErrQuota      = errors.New("mail.invalid_quota")
)

type Settings struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname"` // FQDN of the server, e.g. mail.example.com
}

type Service struct {
	st   *store.Store
	Dir  string                   // /opt/lmcwhm/mail
	Cert func(host string) string // combined cert+key PEM for host ("" = none)
	// Run executes `docker args...` (replaced in tests).
	Run func(ctx context.Context, args ...string) (string, error)
}

func New(st *store.Store, home string) *Service {
	return &Service{st: st, Dir: filepath.Join(home, "mail"), Run: dockerx.Run}
}

var (
	hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	addrRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?@([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

func (sv *Service) Settings(ctx context.Context) Settings {
	var s Settings
	var raw string
	if sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'mail'`).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &s)
	}
	return s
}

func (sv *Service) saveSettings(ctx context.Context, s Settings) error {
	b, _ := json.Marshal(s)
	_, err := sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES('mail', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	return err
}

type Status struct {
	Settings
	State   string   `json:"state"`
	Image   string   `json:"image"`
	HasCert bool     `json:"has_cert"`
	Domains []Domain `json:"domains"`
	Ports   []int    `json:"ports"`
}

type Domain struct {
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	DKIM      string `json:"dkim"` // TXT value of mail._domainkey ("" = not generated)
	Accounts  int    `json:"accounts"`
}

func (sv *Service) Status(ctx context.Context) Status {
	s := Status{Settings: sv.Settings(ctx), State: dockerx.State(ctx, Container), Image: Image, Ports: Ports, Domains: []Domain{}}
	s.HasCert = sv.certCopy(s.Hostname)
	s.Domains, _ = sv.Domains(ctx)
	return s
}

func (sv *Service) cfgDir() string { return filepath.Join(sv.Dir, "config") }

func (sv *Service) args(s Settings) []string {
	a := []string{"--hostname", s.Hostname, "--stop-timeout", "30",
		"-e", "ONE_DIR=1", "-e", "ENABLE_FAIL2BAN=1", "-e", "ENABLE_CLAMAV=0", "-e", "ENABLE_SPAMASSASSIN=0",
		"-e", "ENABLE_RSPAMD=0", "-e", "ENABLE_OPENDKIM=1", "-e", "ENABLE_OPENDMARC=1", "-e", "ENABLE_POLICYD_SPF=1",
		"-e", "ENABLE_QUOTAS=1", "-e", "POSTMASTER_ADDRESS=postmaster@" + parentDomain(s.Hostname), "-e", "PERMIT_DOCKER=none",
		"-e", "SPOOF_PROTECTION=1", "-e", "ENABLE_POP3=0",
		"--cap-add", "NET_ADMIN", // fail2ban
		"-v", filepath.Join(sv.Dir, "data") + ":/var/mail",
		"-v", filepath.Join(sv.Dir, "state") + ":/var/mail-state",
		"-v", filepath.Join(sv.Dir, "logs") + ":/var/log/mail",
		"-v", sv.cfgDir() + ":/tmp/docker-mailserver",
		"-v", "/etc/localtime:/etc/localtime:ro",
	}
	if sv.certCopy(s.Hostname) {
		// A copy of the panel's certificate for the hostname: one PEM with the
		// chain and the key (only this certificate is shared with the container).
		a = append(a, "-e", "SSL_TYPE=manual", "-e", "SSL_CERT_PATH=/etc/lmcwhm-mail/cert.pem", "-e", "SSL_KEY_PATH=/etc/lmcwhm-mail/cert.pem",
			"-v", filepath.Join(sv.Dir, "ssl")+":/etc/lmcwhm-mail:ro")
	}
	for _, p := range Ports {
		a = append(a, "-p", fmt.Sprintf("%d:%d", p, p))
	}
	return append(a, Image)
}

// certCopy refreshes Dir/ssl/cert.pem from the panel's certificate for
// host; it reports whether a certificate is available.
func (sv *Service) certCopy(host string) bool {
	if sv.Cert == nil || host == "" {
		return false
	}
	src := sv.Cert(host)
	if src == "" {
		return false
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return false
	}
	dst := filepath.Join(sv.Dir, "ssl", "cert.pem")
	if old, err := os.ReadFile(dst); err == nil && string(old) == string(b) {
		return true
	}
	os.MkdirAll(filepath.Dir(dst), 0o700)
	return os.WriteFile(dst, b, 0o600) == nil
}

// SyncCert reloads Postfix and Dovecot after the certificate was renewed.
func (sv *Service) SyncCert(ctx context.Context) {
	s := sv.Settings(ctx)
	if !s.Enabled || dockerx.State(ctx, Container) != "running" {
		return
	}
	dst := filepath.Join(sv.Dir, "ssl", "cert.pem")
	before, _ := os.ReadFile(dst)
	if !sv.certCopy(s.Hostname) {
		return
	}
	after, _ := os.ReadFile(dst)
	if string(before) == string(after) {
		return
	}
	if len(before) == 0 {
		// TLS was off: the container must be recreated with the certificate.
		sv.Enable(ctx, s, func(string, ...any) {})
		return
	}
	sv.Run(ctx, "exec", Container, "postfix", "reload")
	sv.Run(ctx, "exec", Container, "doveadm", "reload")
}

func parentDomain(host string) string {
	if i := strings.IndexByte(host, '.'); i > 0 && strings.Count(host, ".") >= 2 {
		return host[i+1:]
	}
	return host
}

// Enable starts (or reconfigures) the mail server; off stops and removes the
// container (mail, accounts and keys stay in Dir).
func (sv *Service) Enable(ctx context.Context, s Settings, log func(string, ...any)) error {
	s.Hostname = strings.ToLower(strings.TrimSpace(s.Hostname))
	if s.Enabled && !hostRe.MatchString(s.Hostname) {
		return ErrHostname
	}
	if !s.Enabled {
		if err := sv.saveSettings(ctx, Settings{Hostname: s.Hostname}); err != nil {
			return err
		}
		log("stopping the mail server (mailboxes are kept)")
		return dockerx.Remove(ctx, Container)
	}
	for _, d := range []string{"data", "state", "logs", "config"} {
		if err := os.MkdirAll(filepath.Join(sv.Dir, d), 0o755); err != nil {
			return err
		}
	}
	if dockerx.State(ctx, Container) == "" {
		for _, p := range Ports {
			if !portFree(p) {
				return fmt.Errorf("%w: %d", ErrPortBusy, p)
			}
		}
	}
	if !dockerx.ImageExists(ctx, Image) {
		log("pulling %s (about 500 MB)", Image)
		if _, err := sv.Run(ctx, "pull", "-q", Image); err != nil {
			return err
		}
	}
	if err := sv.saveSettings(ctx, s); err != nil {
		return err
	}
	if s.Hostname != "" && sv.Cert != nil && sv.Cert(s.Hostname) == "" {
		log("no certificate for %s yet: mail runs without TLS until you add a website for it and issue SSL, then re-apply", s.Hostname)
	}
	// docker-mailserver refuses to start without a mailbox; the first one is
	// added before the container starts by `setup` in a throwaway container.
	if n, _ := sv.accountCount(); n == 0 {
		dom := parentDomain(s.Hostname)
		pw := randomPassword()
		log("creating postmaster@%s (password shown below; change it under Mail → Mailboxes)", dom)
		if _, err := sv.Run(ctx, "run", "--rm", "-v", sv.cfgDir()+":/tmp/docker-mailserver", "--entrypoint", "setup", Image,
			"email", "add", "postmaster@"+dom, pw); err != nil {
			return err
		}
		log("postmaster@%s password: %s", dom, pw)
		sv.addDomainRow(ctx, dom)
	}
	return ensureContainer(ctx, log, sv.args(s))
}

func (sv *Service) accountCount() (int, error) {
	b, err := os.ReadFile(filepath.Join(sv.cfgDir(), "postfix-accounts.cf"))
	if err != nil {
		return 0, err
	}
	return len(strings.Fields(strings.TrimSpace(string(b)))), nil
}

func portFree(p int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func (sv *Service) setup(ctx context.Context, args ...string) (string, error) {
	if dockerx.State(ctx, Container) != "running" {
		return "", ErrNotRunning
	}
	return sv.Run(ctx, append([]string{"exec", Container, "setup"}, args...)...)
}

// ---- domains ----

func (sv *Service) addDomainRow(ctx context.Context, d string) {
	sv.st.DB.ExecContext(ctx, `INSERT OR IGNORE INTO mail_domains(name, created_at) VALUES(?,?)`, d, time.Now().Unix())
}

func (sv *Service) Domains(ctx context.Context) ([]Domain, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT name, created_at FROM mail_domains ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.Name, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	accs, _ := sv.Accounts(ctx)
	for i := range out {
		out[i].DKIM = sv.DKIM(out[i].Name)
		for _, a := range accs {
			if strings.HasSuffix(a.Address, "@"+out[i].Name) {
				out[i].Accounts++
			}
		}
	}
	return out, rows.Err()
}

func (sv *Service) hasDomain(ctx context.Context, d string) bool {
	var n int
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_domains WHERE name = ?`, d).Scan(&n)
	return n > 0
}

// AddDomain registers a domain and generates its DKIM key (2048-bit RSA).
func (sv *Service) AddDomain(ctx context.Context, d string) error {
	d = strings.ToLower(strings.TrimSpace(d))
	if !hostRe.MatchString(d) {
		return ErrDomain
	}
	if _, err := sv.setup(ctx, "config", "dkim", "keysize", "2048", "domain", d); err != nil {
		return err
	}
	sv.addDomainRow(ctx, d)
	// OpenDKIM reads new keys on restart.
	_, err := sv.Run(ctx, "restart", Container)
	return err
}

// DeleteDomain forgets a domain; its mailboxes must be deleted first.
func (sv *Service) DeleteDomain(ctx context.Context, d string) error {
	accs, _ := sv.Accounts(ctx)
	for _, a := range accs {
		if strings.HasSuffix(a.Address, "@"+d) {
			return fmt.Errorf("%w: %s", ErrAddress, a.Address)
		}
	}
	_, err := sv.st.DB.ExecContext(ctx, `DELETE FROM mail_domains WHERE name = ?`, d)
	return err
}

var quotedRe = regexp.MustCompile(`"([^"]*)"`)

// DKIM returns the TXT value for mail._domainkey.<domain> ("" when none).
func (sv *Service) DKIM(d string) string {
	b, err := os.ReadFile(filepath.Join(sv.cfgDir(), "opendkim", "keys", d, Selector+".txt"))
	if err != nil {
		return ""
	}
	var parts []string
	for _, m := range quotedRe.FindAllStringSubmatch(string(b), -1) {
		parts = append(parts, m[1])
	}
	return strings.Join(parts, "")
}

// ---- mailboxes and aliases ----

type Account struct {
	Address string `json:"address"`
	Quota   string `json:"quota"` // e.g. "120M / 1G" as reported
}

var listRe = regexp.MustCompile(`^\*\s+(\S+@\S+)\s*(\(.*\))?`)

func (sv *Service) Accounts(ctx context.Context) ([]Account, error) {
	out := []Account{}
	b, err := os.ReadFile(filepath.Join(sv.cfgDir(), "postfix-accounts.cf"))
	if err != nil {
		return out, nil
	}
	quota := map[string]string{}
	if dockerx.State(ctx, Container) == "running" {
		if o, err := sv.setup(ctx, "email", "list"); err == nil {
			for _, l := range strings.Split(o, "\n") {
				if m := listRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
					quota[m[1]] = strings.Trim(m[2], "() ")
				}
			}
		}
	}
	for _, l := range strings.Split(string(b), "\n") {
		addr, _, ok := strings.Cut(strings.TrimSpace(l), "|")
		if ok && addr != "" {
			out = append(out, Account{Address: addr, Quota: quota[addr]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out, nil
}

func (sv *Service) checkAddr(ctx context.Context, a string) (string, error) {
	a = strings.ToLower(strings.TrimSpace(a))
	if !addrRe.MatchString(a) {
		return "", ErrAddress
	}
	_, dom, _ := strings.Cut(a, "@")
	if !sv.hasDomain(ctx, dom) {
		return "", ErrNoDomain
	}
	return a, nil
}

func checkPassword(p string) error {
	if len(p) < 8 || strings.ContainsAny(p, "\n\r") {
		return ErrPassword
	}
	return nil
}

func (sv *Service) AddAccount(ctx context.Context, addr, password string) error {
	a, err := sv.checkAddr(ctx, addr)
	if err != nil {
		return err
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	_, err = sv.setup(ctx, "email", "add", a, password)
	return err
}

func (sv *Service) SetPassword(ctx context.Context, addr, password string) error {
	a, err := sv.checkAddr(ctx, addr)
	if err != nil {
		return err
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	_, err = sv.setup(ctx, "email", "update", a, password)
	return err
}

var quotaRe = regexp.MustCompile(`^(0|\d{1,6}[MG])$`)

// SetQuota limits a mailbox ("0" = unlimited).
func (sv *Service) SetQuota(ctx context.Context, addr, quota string) error {
	a, err := sv.checkAddr(ctx, addr)
	if err != nil {
		return err
	}
	if !quotaRe.MatchString(quota) {
		return ErrQuota
	}
	if quota == "0" {
		_, err = sv.setup(ctx, "quota", "del", a)
	} else {
		_, err = sv.setup(ctx, "quota", "set", a, quota)
	}
	return err
}

func (sv *Service) DeleteAccount(ctx context.Context, addr string) error {
	a, err := sv.checkAddr(ctx, addr)
	if err != nil {
		return err
	}
	_, err = sv.setup(ctx, "email", "del", "-y", a)
	return err
}

type Alias struct {
	From string   `json:"from"`
	To   []string `json:"to"`
}

func (sv *Service) Aliases() []Alias {
	out := []Alias{}
	b, err := os.ReadFile(filepath.Join(sv.cfgDir(), "postfix-virtual.cf"))
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && !strings.HasPrefix(f[0], "#") {
			out = append(out, Alias{From: f[0], To: strings.Split(strings.Join(f[1:], ""), ",")})
		}
	}
	return out
}

func (sv *Service) AddAlias(ctx context.Context, from, to string) error {
	f := strings.ToLower(strings.TrimSpace(from))
	if strings.HasPrefix(f, "@") { // catch-all
		if !hostRe.MatchString(f[1:]) || !sv.hasDomain(ctx, f[1:]) {
			return ErrNoDomain
		}
	} else if _, err := sv.checkAddr(ctx, f); err != nil {
		return err
	}
	t := strings.ToLower(strings.TrimSpace(to))
	if !addrRe.MatchString(t) {
		return ErrAddress
	}
	_, err := sv.setup(ctx, "alias", "add", f, t)
	return err
}

func (sv *Service) DeleteAlias(ctx context.Context, from, to string) error {
	_, err := sv.setup(ctx, "alias", "del", strings.ToLower(from), strings.ToLower(to))
	return err
}

// ---- DNS ----

// DNSRecord is a record the domain should have.
type DNSRecord struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Priority int    `json:"priority,omitempty"`
}

// Records lists what a domain needs for this server (A of the hostname, MX,
// SPF, DKIM, DMARC).
func (sv *Service) Records(ctx context.Context, d, ip4, ip6 string) []DNSRecord {
	host := sv.Settings(ctx).Hostname
	out := []DNSRecord{}
	if ip4 != "" {
		out = append(out, DNSRecord{Type: "A", Name: host, Content: ip4})
	}
	if ip6 != "" {
		out = append(out, DNSRecord{Type: "AAAA", Name: host, Content: ip6})
	}
	out = append(out,
		DNSRecord{Type: "MX", Name: d, Content: host, Priority: 10},
		DNSRecord{Type: "TXT", Name: d, Content: "v=spf1 mx a:" + host + " ~all"},
	)
	if k := sv.DKIM(d); k != "" {
		out = append(out, DNSRecord{Type: "TXT", Name: Selector + "._domainkey." + d, Content: k})
	}
	return append(out, DNSRecord{Type: "TXT", Name: "_dmarc." + d, Content: "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + d + "; adkim=s; aspf=s"})
}

// ---- deliverability ----

type Check struct {
	Key    string `json:"key"`
	OK     bool   `json:"ok"`
	Level  string `json:"level"` // error | warning when not OK
	Detail string `json:"detail"`
}

// Resolver is the DNS lookup used by Deliverability (replaced in tests).
var Resolver = net.DefaultResolver

// Deliverability checks what makes mail from d accepted by other servers.
func (sv *Service) Deliverability(ctx context.Context, d, ip4 string) []Check {
	host := sv.Settings(ctx).Hostname
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out []Check
	add := func(k string, ok bool, lvl, detail string) { out = append(out, Check{k, ok, lvl, detail}) }

	mx, err := Resolver.LookupMX(ctx, d)
	found := false
	var names []string
	for _, m := range mx {
		n := strings.TrimSuffix(strings.ToLower(m.Host), ".")
		names = append(names, n)
		found = found || n == host
	}
	add("mx", found, "error", strings.Join(names, ", ")+errText(err))

	addrs, err := Resolver.LookupHost(ctx, host)
	add("a", ip4 != "" && contains(addrs, ip4), "error", strings.Join(addrs, ", ")+errText(err))

	if ip4 != "" {
		ptr, err := Resolver.LookupAddr(ctx, ip4)
		ok := false
		for _, p := range ptr {
			ok = ok || strings.TrimSuffix(strings.ToLower(p), ".") == host
		}
		add("ptr", ok, "warning", strings.Join(ptr, ", ")+errText(err))
	}

	txt, err := Resolver.LookupTXT(ctx, d)
	spf := ""
	for _, t := range txt {
		if strings.HasPrefix(t, "v=spf1") {
			spf = t
		}
	}
	add("spf", spf != "" && (strings.Contains(spf, " mx") || strings.Contains(spf, "a:"+host) || strings.Contains(spf, "ip4:"+ip4)), "error", spf+errText(err))

	want := sv.DKIM(d)
	got, err := Resolver.LookupTXT(ctx, Selector+"._domainkey."+d)
	dk := strings.Join(got, "")
	pub := func(s string) string {
		if i := strings.Index(s, "p="); i >= 0 {
			return strings.TrimSpace(strings.SplitN(s[i+2:], ";", 2)[0])
		}
		return ""
	}
	add("dkim", want != "" && pub(dk) != "" && pub(dk) == pub(want), "error", trimLong(dk)+errText(err))

	dm, err := Resolver.LookupTXT(ctx, "_dmarc."+d)
	dmarc := ""
	for _, t := range dm {
		if strings.HasPrefix(t, "v=DMARC1") {
			dmarc = t
		}
	}
	add("dmarc", dmarc != "", "warning", dmarc+errText(err))

	add("tls", sv.Cert != nil && sv.Cert(host) != "", "warning", host)

	// Many clouds (Linode, DigitalOcean, AWS) block outbound port 25 until asked.
	var dl net.Dialer
	c, err := dl.DialContext(ctx, "tcp", "gmail-smtp-in.l.google.com:25")
	if err == nil {
		c.Close()
	}
	add("port25", err == nil, "error", errText(err))
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	var de *net.DNSError
	if errors.As(err, &de) && de.IsNotFound {
		return "(none)"
	}
	return " (" + err.Error() + ")"
}

func trimLong(s string) string {
	if len(s) > 80 {
		return s[:77] + "…"
	}
	return s
}

func randomPassword() string {
	b := make([]byte, 16)
	rand.Read(b)
	const al = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = al[int(c)%len(al)]
	}
	return string(out)
}

func ensureContainer(ctx context.Context, log func(string, ...any), args []string) error {
	return webenv.EnsureContainer(ctx, log, Container, args, nil, true)
}

// Boot recreates the container when mail is on but it is missing (after a
// snapshot restore on a new server).
func (sv *Service) Boot(ctx context.Context) {
	s := sv.Settings(ctx)
	if s.Enabled && dockerx.Available(ctx) && dockerx.State(ctx, Container) == "" {
		sv.Enable(ctx, s, func(string, ...any) {})
	}
}
