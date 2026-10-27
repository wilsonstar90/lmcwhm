// Package site manages websites: the database record is the source of truth,
// and every change re-renders the web stack (webenv) from it.
package site

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/acme"
	"github.com/wilsonstar90/lmcwhm/internal/cfdns"
	"github.com/wilsonstar90/lmcwhm/internal/netinfo"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/webcfg"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

type Site struct {
	ID           int64    `json:"id"`
	Domain       string   `json:"domain"`
	Aliases      []string `json:"aliases"`
	Type         string   `json:"type"`
	PHPVersion   string   `json:"php_version"`
	Root         string   `json:"root"`
	Stopped      bool     `json:"stopped"`
	ForceHTTPS   bool     `json:"force_https"`
	HSTS         bool     `json:"hsts"`
	SSLMode      string   `json:"ssl_mode"`      // none | acme | custom
	SSLChallenge string   `json:"ssl_challenge"` // http | dns
	SSLWildcard  bool     `json:"ssl_wildcard"`
	CertIssuer   string   `json:"cert_issuer"`
	CertNotAfter int64    `json:"cert_not_after"`
	CertError    string   `json:"cert_error"`
	DNSManaged   bool     `json:"dns_managed"`
	DNSProxied   bool     `json:"dns_proxied"`
	Remark       string   `json:"remark"`
	CreatedAt    int64    `json:"created_at"`
	UpdatedAt    int64    `json:"updated_at"`
}

func (s Site) Hosts() []string { return append([]string{s.Domain}, s.Aliases...) }

var (
	ErrBadDomain   = errors.New("site.invalid_domain")
	ErrDomainTaken = errors.New("site.domain_taken")
	ErrBadType     = errors.New("site.invalid_type")
	ErrBadPHP      = errors.New("site.invalid_php")
	ErrNotFound    = errors.New("site.not_found")
	ErrWildcardDNS = errors.New("site.wildcard_needs_dns")
	ErrNoCFToken   = errors.New("cf.no_token")
	ErrNoCert      = errors.New("site.no_cert")
)

var hostRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func normHost(h string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".") }

type Service struct {
	st     *store.Store
	Env    *webenv.Env
	cfg    *webcfg.Store
	issuer *acme.Issuer
	tasks  *tasks.Manager
}

func NewService(st *store.Store, env *webenv.Env, cfg *webcfg.Store, issuer *acme.Issuer, tm *tasks.Manager) *Service {
	return &Service{st: st, Env: env, cfg: cfg, issuer: issuer, tasks: tm}
}

// ---- persistence ----

const cols = `id, domain, aliases, type, php_version, root, stopped, force_https, hsts, ssl_mode, ssl_challenge,
	ssl_wildcard, cert_issuer, cert_not_after, cert_error, dns_managed, dns_proxied, remark, created_at, updated_at`

func scan(row interface{ Scan(...any) error }) (Site, error) {
	var s Site
	var aliases string
	err := row.Scan(&s.ID, &s.Domain, &aliases, &s.Type, &s.PHPVersion, &s.Root, &s.Stopped, &s.ForceHTTPS, &s.HSTS,
		&s.SSLMode, &s.SSLChallenge, &s.SSLWildcard, &s.CertIssuer, &s.CertNotAfter, &s.CertError,
		&s.DNSManaged, &s.DNSProxied, &s.Remark, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, err
	}
	json.Unmarshal([]byte(aliases), &s.Aliases)
	if s.Aliases == nil {
		s.Aliases = []string{}
	}
	return s, nil
}

func (sv *Service) List(ctx context.Context) ([]Site, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT `+cols+` FROM sites ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Site{}
	for rows.Next() {
		s, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (sv *Service) Get(ctx context.Context, id int64) (Site, error) {
	s, err := scan(sv.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM sites WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func (sv *Service) save(ctx context.Context, s *Site) error {
	aliases, _ := json.Marshal(s.Aliases)
	now := time.Now().Unix()
	s.UpdatedAt = now
	if s.ID == 0 {
		s.CreatedAt = now
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO sites(domain, aliases, type, php_version, root, stopped, force_https, hsts,
			ssl_mode, ssl_challenge, ssl_wildcard, cert_issuer, cert_not_after, cert_error, dns_managed, dns_proxied, remark, created_at, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.Domain, string(aliases), s.Type, s.PHPVersion, s.Root, s.Stopped, s.ForceHTTPS, s.HSTS, s.SSLMode, s.SSLChallenge,
			s.SSLWildcard, s.CertIssuer, s.CertNotAfter, s.CertError, s.DNSManaged, s.DNSProxied, s.Remark, s.CreatedAt, s.UpdatedAt)
		if err != nil {
			return err
		}
		s.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := sv.st.DB.ExecContext(ctx, `UPDATE sites SET aliases=?, type=?, php_version=?, root=?, stopped=?, force_https=?, hsts=?,
		ssl_mode=?, ssl_challenge=?, ssl_wildcard=?, cert_issuer=?, cert_not_after=?, cert_error=?, dns_managed=?, dns_proxied=?,
		remark=?, updated_at=? WHERE id=?`,
		string(aliases), s.Type, s.PHPVersion, s.Root, s.Stopped, s.ForceHTTPS, s.HSTS, s.SSLMode, s.SSLChallenge, s.SSLWildcard,
		s.CertIssuer, s.CertNotAfter, s.CertError, s.DNSManaged, s.DNSProxied, s.Remark, s.UpdatedAt, s.ID)
	return err
}

// ---- validation ----

func (sv *Service) checkHosts(ctx context.Context, selfID int64, hosts []string) error {
	seen := map[string]bool{}
	for _, h := range hosts {
		if !hostRe.MatchString(h) || seen[h] {
			return ErrBadDomain
		}
		seen[h] = true
	}
	all, err := sv.List(ctx)
	if err != nil {
		return err
	}
	for _, s := range all {
		if s.ID == selfID {
			continue
		}
		for _, h := range s.Hosts() {
			if seen[h] {
				return fmt.Errorf("%w: %s", ErrDomainTaken, h)
			}
		}
	}
	return nil
}

func cleanHosts(in []string) []string {
	out := []string{}
	for _, h := range in {
		if h = normHost(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// ---- stack sync ----

func (sv *Service) specs(ctx context.Context) ([]webenv.Site, error) {
	all, err := sv.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]webenv.Site, 0, len(all))
	for _, s := range all {
		out = append(out, sv.spec(s))
	}
	return out, nil
}

func (sv *Service) spec(s Site) webenv.Site {
	has := false
	if s.SSLMode != "none" && s.CertNotAfter > 0 {
		_, err := os.Stat(sv.Env.CertPath(s.Domain))
		has = err == nil
	}
	return webenv.Site{
		Domain: s.Domain, Aliases: s.Aliases, Type: s.Type, PHPVersion: s.PHPVersion, Root: s.Root,
		Stopped: s.Stopped, ForceHTTPS: s.ForceHTTPS, HSTS: s.HSTS, HasCert: has,
	}
}

// Apply re-renders and reloads the whole stack from the database.
func (sv *Service) Apply(ctx context.Context) error {
	specs, err := sv.specs(ctx)
	if err != nil {
		return err
	}
	return sv.Env.Apply(ctx, specs, nil)
}

// ensureStack installs the base stack and every PHP version in use.
func (sv *Service) ensureStack(ctx context.Context, extraPHP string, log tasks.Logf) error {
	specs, err := sv.specs(ctx)
	if err != nil {
		return err
	}
	if err := sv.Env.Ensure(ctx, specs, nil, log); err != nil {
		return err
	}
	need := map[string]bool{}
	if extraPHP != "" {
		need[extraPHP] = true
	}
	for _, s := range specs {
		if s.Type == "php" {
			need[s.PHPVersion] = true
		}
	}
	for v := range need {
		if err := sv.Env.EnsurePHP(ctx, v, log); err != nil {
			return err
		}
	}
	return nil
}

// InstallEnv installs / repairs the web stack (task).
func (sv *Service) InstallEnv() string {
	return sv.tasks.Start("web.install", func(ctx context.Context, log tasks.Logf) error {
		if err := sv.ensureStack(ctx, "", log); err != nil {
			return err
		}
		log("applying configuration")
		return sv.Apply(ctx)
	})
}

// Boot re-applies configs at panel start when the stack is installed, so a
// repaired or upgraded panel regenerates everything from the database.
func (sv *Service) Boot(ctx context.Context) error {
	if !sv.Env.Installed(ctx) {
		return nil
	}
	return sv.tasks.RunSync(ctx, func(ctx context.Context) error {
		if err := sv.ensureStack(ctx, "", func(string, ...any) {}); err != nil {
			return err
		}
		return sv.Apply(ctx)
	})
}

// ---- create ----

type CreateInput struct {
	Domain     string   `json:"domain"`
	Aliases    []string `json:"aliases"`
	Type       string   `json:"type"`
	PHPVersion string   `json:"php_version"`
	DNS        bool     `json:"dns"`
	DNSProxied bool     `json:"dns_proxied"`
	SSL        string   `json:"ssl"` // none | http | dns
	Wildcard   bool     `json:"wildcard"`
	ForceHTTPS bool     `json:"force_https"`
	Remark     string   `json:"remark"`
}

func (sv *Service) Create(ctx context.Context, in CreateInput) (string, error) {
	s := Site{
		Domain: normHost(in.Domain), Aliases: cleanHosts(in.Aliases), Type: in.Type, PHPVersion: in.PHPVersion,
		ForceHTTPS: in.ForceHTTPS, DNSManaged: in.DNS, DNSProxied: in.DNSProxied, Remark: in.Remark,
		SSLMode: "none", SSLChallenge: "http",
	}
	if strings.HasPrefix(s.Domain, "*.") {
		return "", ErrBadDomain
	}
	if err := sv.checkHosts(ctx, 0, s.Hosts()); err != nil {
		return "", err
	}
	switch s.Type {
	case "php":
		if !webenv.ValidPHP(s.PHPVersion) {
			return "", ErrBadPHP
		}
	case "static":
		s.PHPVersion = ""
	default:
		return "", ErrBadType
	}
	cfg, err := sv.cfg.Get(ctx)
	if err != nil {
		return "", err
	}
	if (in.DNS || in.SSL == "dns") && cfg.CFToken == "" {
		return "", ErrNoCFToken
	}
	if in.Wildcard && in.SSL != "dns" {
		return "", ErrWildcardDNS
	}
	s.Root = sv.Env.SiteRoot(s.Domain)

	return sv.tasks.Start("site.create:"+s.Domain, func(ctx context.Context, log tasks.Logf) error {
		log("preparing web stack")
		if err := sv.ensureStack(ctx, s.PHPVersion, log); err != nil {
			return err
		}
		if err := createRoot(s.Root, s.Domain); err != nil {
			return err
		}
		log("document root %s", s.Root)
		if err := sv.save(ctx, &s); err != nil {
			return err
		}
		if s.DNSManaged {
			if err := sv.syncDNS(ctx, s, log); err != nil {
				log("DNS: %v", err)
			}
		}
		log("applying nginx / haproxy configuration")
		if err := sv.Apply(ctx); err != nil {
			return err
		}
		if in.SSL == "http" || in.SSL == "dns" {
			if err := sv.issue(ctx, &s, acme.Challenge(in.SSL), in.Wildcard, log); err != nil {
				log("SSL: %v (the site is online over HTTP; retry from the SSL tab)", err)
			}
		}
		log("done: http://%s", s.Domain)
		return nil
	}), nil
}

func createRoot(root, domain string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	entries, _ := os.ReadDir(root)
	if len(entries) == 0 {
		page := fmt.Sprintf("<!doctype html><meta charset=utf-8><title>%s</title><h1>%s</h1><p>Site created by lmcwhm.</p>\n", domain, domain)
		if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(page), 0o644); err != nil {
			return err
		}
	}
	// PHP-FPM runs as www-data (uid 33) in the official images.
	filepath.WalkDir(root, func(p string, _ os.DirEntry, _ error) error {
		os.Lchown(p, 33, 33)
		return nil
	})
	return nil
}

// ---- DNS ----

func (sv *Service) serverIPs(ctx context.Context, cfg webcfg.Config) (v4, v6 string) {
	v4, v6 = cfg.ServerIPv4, cfg.ServerIPv6
	if v4 == "" {
		v4 = netinfo.PublicIPv4(ctx)
	}
	if v6 == "" {
		v6 = netinfo.PublicIPv6(ctx)
	}
	return
}

func (sv *Service) syncDNS(ctx context.Context, s Site, log tasks.Logf) error {
	cfg, err := sv.cfg.Get(ctx)
	if err != nil {
		return err
	}
	if cfg.CFToken == "" {
		return ErrNoCFToken
	}
	cf := cfdns.New(cfg.CFToken)
	v4, v6 := sv.serverIPs(ctx, cfg)
	for _, h := range s.Hosts() {
		z, err := cf.ZoneFor(ctx, strings.TrimPrefix(h, "*."))
		if err != nil {
			return fmt.Errorf("%s: %w", h, err)
		}
		if v4 != "" {
			if err := cf.Upsert(ctx, z.ID, "A", h, v4, s.DNSProxied); err != nil {
				return err
			}
			log("DNS A    %s -> %s (zone %s)", h, v4, z.Name)
		}
		if v6 != "" {
			if err := cf.Upsert(ctx, z.ID, "AAAA", h, v6, s.DNSProxied); err != nil {
				return err
			}
			log("DNS AAAA %s -> %s", h, v6)
		}
	}
	return nil
}

func (sv *Service) removeDNS(ctx context.Context, s Site, log tasks.Logf) error {
	cfg, err := sv.cfg.Get(ctx)
	if err != nil || cfg.CFToken == "" {
		return err
	}
	cf := cfdns.New(cfg.CFToken)
	v4, v6 := sv.serverIPs(ctx, cfg)
	for _, h := range s.Hosts() {
		z, err := cf.ZoneFor(ctx, strings.TrimPrefix(h, "*."))
		if err != nil {
			continue
		}
		// Only records that still point at this server are removed.
		if v4 != "" {
			cf.Delete(ctx, z.ID, "A", h, v4)
		}
		if v6 != "" {
			cf.Delete(ctx, z.ID, "AAAA", h, v6)
		}
		log("DNS records for %s removed", h)
	}
	return nil
}

// ---- SSL ----

func (sv *Service) issue(ctx context.Context, s *Site, ch acme.Challenge, wildcard bool, log tasks.Logf) error {
	domains := []string{}
	for _, h := range s.Hosts() {
		if strings.HasPrefix(h, "*.") && ch != acme.DNS01 {
			return ErrWildcardDNS
		}
		domains = append(domains, h)
	}
	if wildcard {
		domains = append(domains, "*."+s.Domain)
	}
	res, err := sv.issuer.Obtain(ctx, acme.Request{Domains: dedupe(domains), Challenge: ch}, log)
	if err != nil && ch == acme.HTTP01 {
		// HTTP-01 cannot pass when a proxy in front (e.g. Cloudflare with
		// Full (strict)) rejects the origin before it has a valid certificate.
		// DNS-01 does not depend on the HTTP path, so retry with it when possible.
		if cfg, cerr := sv.cfg.Get(ctx); cerr == nil && cfg.CFToken != "" {
			log("HTTP-01 failed: %v", err)
			log("retrying with DNS-01 via Cloudflare")
			ch = acme.DNS01
			res, err = sv.issuer.Obtain(ctx, acme.Request{Domains: dedupe(domains), Challenge: ch}, log)
		}
	}
	if err != nil {
		s.CertError = err.Error()
		sv.save(ctx, s)
		return err
	}
	if err := os.WriteFile(sv.Env.CertPath(s.Domain), append(append(res.CertPEM, '\n'), res.KeyPEM...), 0o600); err != nil {
		return err
	}
	s.SSLMode, s.SSLChallenge, s.SSLWildcard = "acme", string(ch), wildcard
	s.CertIssuer, s.CertNotAfter, s.CertError = res.Issuer, res.NotAfter.Unix(), ""
	if err := sv.save(ctx, s); err != nil {
		return err
	}
	log("installing certificate")
	return sv.Apply(ctx)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// IssueCert requests an ACME certificate (task).
func (sv *Service) IssueCert(ctx context.Context, id int64, challenge string, wildcard bool) (string, error) {
	s, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if challenge != "http" && challenge != "dns" {
		challenge = "http"
	}
	if wildcard && challenge != "dns" {
		return "", ErrWildcardDNS
	}
	return sv.tasks.Start("site.ssl:"+s.Domain, func(ctx context.Context, log tasks.Logf) error {
		return sv.issue(ctx, &s, acme.Challenge(challenge), wildcard, log)
	}), nil
}

// UploadCert installs a user-supplied certificate.
func (sv *Service) UploadCert(ctx context.Context, id int64, certPEM, keyPEM string) error {
	s, err := sv.Get(ctx, id)
	if err != nil {
		return err
	}
	leaf, err := acme.ParsePair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return err
	}
	return sv.tasks.RunSync(ctx, func(ctx context.Context) error {
		if err := os.WriteFile(sv.Env.CertPath(s.Domain), []byte(strings.TrimSpace(certPEM)+"\n"+strings.TrimSpace(keyPEM)+"\n"), 0o600); err != nil {
			return err
		}
		s.SSLMode, s.CertIssuer, s.CertNotAfter, s.CertError = "custom", leaf.Issuer.CommonName, leaf.NotAfter.Unix(), ""
		if err := sv.save(ctx, &s); err != nil {
			return err
		}
		return sv.Apply(ctx)
	})
}

// DisableSSL removes the certificate; HTTPS falls back to the default cert.
func (sv *Service) DisableSSL(ctx context.Context, id int64) error {
	s, err := sv.Get(ctx, id)
	if err != nil {
		return err
	}
	return sv.tasks.RunSync(ctx, func(ctx context.Context) error {
		os.Remove(sv.Env.CertPath(s.Domain))
		s.SSLMode, s.CertIssuer, s.CertNotAfter, s.CertError, s.ForceHTTPS, s.HSTS = "none", "", 0, "", false, false
		if err := sv.save(ctx, &s); err != nil {
			return err
		}
		return sv.Apply(ctx)
	})
}

// ---- update / delete ----

type UpdateInput struct {
	Aliases    *[]string `json:"aliases"`
	PHPVersion *string   `json:"php_version"`
	ForceHTTPS *bool     `json:"force_https"`
	HSTS       *bool     `json:"hsts"`
	Stopped    *bool     `json:"stopped"`
	Remark     *string   `json:"remark"`
	DNSProxied *bool     `json:"dns_proxied"`
}

// Update changes settings; it returns a task id because a new PHP version may
// need an image build.
func (sv *Service) Update(ctx context.Context, id int64, in UpdateInput) (string, error) {
	s, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if in.Aliases != nil {
		s.Aliases = cleanHosts(*in.Aliases)
		if err := sv.checkHosts(ctx, s.ID, s.Hosts()); err != nil {
			return "", err
		}
	}
	if in.PHPVersion != nil {
		if s.Type != "php" || !webenv.ValidPHP(*in.PHPVersion) {
			return "", ErrBadPHP
		}
		s.PHPVersion = *in.PHPVersion
	}
	for dst, src := range map[*bool]*bool{&s.ForceHTTPS: in.ForceHTTPS, &s.HSTS: in.HSTS, &s.Stopped: in.Stopped, &s.DNSProxied: in.DNSProxied} {
		if src != nil {
			*dst = *src
		}
	}
	if in.Remark != nil {
		s.Remark = *in.Remark
	}
	return sv.tasks.Start("site.update:"+s.Domain, func(ctx context.Context, log tasks.Logf) error {
		if err := sv.ensureStack(ctx, s.PHPVersion, log); err != nil {
			return err
		}
		if err := sv.save(ctx, &s); err != nil {
			return err
		}
		if s.DNSManaged && (in.Aliases != nil || in.DNSProxied != nil) {
			if err := sv.syncDNS(ctx, s, log); err != nil {
				log("DNS: %v", err)
			}
		}
		log("applying configuration")
		return sv.Apply(ctx)
	}), nil
}

type DeleteInput struct {
	Files bool `json:"files"`
	DNS   bool `json:"dns"`
}

func (sv *Service) Delete(ctx context.Context, id int64, in DeleteInput) (string, error) {
	s, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return sv.tasks.Start("site.delete:"+s.Domain, func(ctx context.Context, log tasks.Logf) error {
		if _, err := sv.st.DB.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, s.ID); err != nil {
			return err
		}
		os.Remove(sv.Env.CertPath(s.Domain))
		if sv.Env.Installed(ctx) {
			if err := sv.Apply(ctx); err != nil {
				return err
			}
		}
		if in.DNS && s.DNSManaged {
			sv.removeDNS(ctx, s, log)
		}
		if in.Files && strings.HasPrefix(s.Root, sv.Env.WWWRoot+string(os.PathSeparator)) {
			log("removing %s", s.Root)
			if err := os.RemoveAll(s.Root); err != nil {
				return err
			}
		}
		log("site %s deleted", s.Domain)
		return nil
	}), nil
}

// Vhost returns the generated nginx config of a site.
func (sv *Service) Vhost(ctx context.Context, id int64) (string, error) {
	s, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return sv.Env.VhostConfig(sv.spec(s))
}

// ---- renewal ----

// RenewLoop renews ACME certificates that expire within 30 days, once a day.
func (sv *Service) RenewLoop(ctx context.Context, log func(string, ...any)) {
	t := time.NewTimer(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		all, err := sv.List(ctx)
		if err == nil {
			soon := time.Now().Add(30 * 24 * time.Hour).Unix()
			for _, s := range all {
				if s.SSLMode == "acme" && s.CertNotAfter > 0 && s.CertNotAfter < soon && !s.Stopped {
					log("renewing certificate", "domain", s.Domain)
					sv.IssueCert(ctx, s.ID, s.SSLChallenge, s.SSLWildcard)
				}
			}
		}
		t.Reset(24 * time.Hour)
	}
}
