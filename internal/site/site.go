// Package site manages websites: the database record is the source of truth,
// and every change re-renders the web stack (webenv) from it.
package site

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/acme"
	"github.com/wilsonstar90/lmcwhm/internal/cfdns"
	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
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
	// Runtime projects (node | python | java) run Command in a container of
	// the App Store runtime image, listening on 127.0.0.1:AppPort.
	RuntimeVersion string `json:"runtime_version"`
	Command        string `json:"command"`
	AppPort        int    `json:"app_port"`
	// Reverse proxies (type proxy) forward to ProxyTarget: an http(s) URL, or
	// host:port when Passthrough hands the raw TLS stream to the backend.
	ProxyTarget string `json:"proxy_target"`
	Passthrough bool   `json:"passthrough"`
	// WAF (when enabled globally): off for this site, or CRS rules removed by ID.
	WAFOff     bool     `json:"waf_off"`
	WAFExclude []string `json:"waf_exclude"`
	// Advanced nginx settings (directory listing, IP / UA rules, rate limits, ...).
	Advanced webenv.Advanced `json:"advanced"`
	// Target is what the domain serves (see targets.go): its own website
	// files, another website (TargetSite), a Docker container (Container,
	// through ProxyTarget), a URL, TLS passthrough or a redirect.
	Target       string `json:"target"`
	TargetSite   int64  `json:"target_site"`
	Container    string `json:"container"`
	Redirect     string `json:"redirect"`
	RedirectCode int    `json:"redirect_code"`
	RedirectPath bool   `json:"redirect_path"`
}

// IsProject reports a runtime project type.
func IsProject(typ string) bool {
	return typ == "node" || typ == "python" || typ == "java" || typ == "tomcat"
}

// ProjectContainer is the container that runs a runtime project.
func ProjectContainer(domain string) string { return "lmcwhm-proj-" + domain }

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
	ErrNoRuntime   = errors.New("site.runtime_not_installed")
	ErrBadPort     = errors.New("site.invalid_port")
	ErrPortTaken   = errors.New("site.port_taken")
	ErrBadTarget   = errors.New("site.invalid_target")
	ErrBadCommand  = errors.New("site.invalid_command")
	ErrPassSSL     = errors.New("site.passthrough_no_ssl")
	ErrNotProject  = errors.New("site.not_project")
	ErrBadWAFRule  = errors.New("site.invalid_waf_rule")
)

var (
	wafRuleRe    = regexp.MustCompile(`^\d{1,7}$`)
	urlTargetRe  = regexp.MustCompile(`^https?://[A-Za-z0-9.\-]+(:[0-9]{1,5})?$|^https?://\[[0-9a-fA-F:]+\](:[0-9]{1,5})?$`)
	passTargetRe = regexp.MustCompile(`^([A-Za-z0-9.\-]+|\[[0-9a-fA-F:]+\]):[0-9]{1,5}$`)
)

// DefaultCommands start a project; PORT and HOST are set in its environment.
var DefaultCommands = map[string]string{
	"node":   "[ -f package.json ] && npm install --omit=dev --no-audit --no-fund; exec npm start",
	"python": "[ -d .venv ] || python -m venv .venv; . .venv/bin/activate; [ -f requirements.txt ] && pip install -q -r requirements.txt; exec python app.py",
	"java":   "exec java -jar app.jar --server.port=$PORT",
	// Tomcat serves the site root as ROOT, or the first *.war in it. The
	// shutdown port is disabled so several Tomcats can share the host network.
	"tomcat": `R=$PWD; cd /usr/local/tomcat && sed -i -e "s/port=\"8080\"/port=\"$PORT\" address=\"$HOST\"/" -e 's/port="8005"/port="-1"/' conf/server.xml && rm -rf webapps/ROOT webapps/ROOT.war && W=$(ls "$R"/*.war 2>/dev/null | head -1); if [ -n "$W" ]; then cp "$W" webapps/ROOT.war; else ln -s "$R" webapps/ROOT; fi; exec catalina.sh run`,
}

// Runtimes is the App Store as seen by websites.
type Runtimes interface {
	RuntimeImage(ctx context.Context, app, version string) (string, error)
	UsedPorts(ctx context.Context) map[int]bool
}

var hostRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func normHost(h string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".") }

type Service struct {
	st     *store.Store
	Env    *webenv.Env
	cfg    *webcfg.Store
	issuer *acme.Issuer
	tasks  *tasks.Manager
	// Runtimes is set when the App Store is available (runtime projects).
	Runtimes Runtimes
	// RebuildMarker, when the file exists at boot, recreates the web stack containers.
	RebuildMarker string
}

func NewService(st *store.Store, env *webenv.Env, cfg *webcfg.Store, issuer *acme.Issuer, tm *tasks.Manager) *Service {
	return &Service{st: st, Env: env, cfg: cfg, issuer: issuer, tasks: tm}
}

// ---- persistence ----

const cols = `id, domain, aliases, type, php_version, root, stopped, force_https, hsts, ssl_mode, ssl_challenge,
	ssl_wildcard, cert_issuer, cert_not_after, cert_error, dns_managed, dns_proxied, remark, created_at, updated_at,
	runtime_version, command, app_port, proxy_target, passthrough, waf_off, waf_exclude, advanced,
	target, target_site, container, redirect, redirect_code, redirect_path`

func scan(row interface{ Scan(...any) error }) (Site, error) {
	var s Site
	var aliases, wafEx, adv string
	err := row.Scan(&s.ID, &s.Domain, &aliases, &s.Type, &s.PHPVersion, &s.Root, &s.Stopped, &s.ForceHTTPS, &s.HSTS,
		&s.SSLMode, &s.SSLChallenge, &s.SSLWildcard, &s.CertIssuer, &s.CertNotAfter, &s.CertError,
		&s.DNSManaged, &s.DNSProxied, &s.Remark, &s.CreatedAt, &s.UpdatedAt,
		&s.RuntimeVersion, &s.Command, &s.AppPort, &s.ProxyTarget, &s.Passthrough, &s.WAFOff, &wafEx, &adv,
		&s.Target, &s.TargetSite, &s.Container, &s.Redirect, &s.RedirectCode, &s.RedirectPath)
	if err != nil {
		return s, err
	}
	json.Unmarshal([]byte(adv), &s.Advanced)
	s.Advanced.Normalize()
	s.WAFExclude = strings.Fields(wafEx)
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
			ssl_mode, ssl_challenge, ssl_wildcard, cert_issuer, cert_not_after, cert_error, dns_managed, dns_proxied, remark, created_at, updated_at,
			runtime_version, command, app_port, proxy_target, passthrough, waf_off, waf_exclude, advanced,
			target, target_site, container, redirect, redirect_code, redirect_path)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.Domain, string(aliases), s.Type, s.PHPVersion, s.Root, s.Stopped, s.ForceHTTPS, s.HSTS, s.SSLMode, s.SSLChallenge,
			s.SSLWildcard, s.CertIssuer, s.CertNotAfter, s.CertError, s.DNSManaged, s.DNSProxied, s.Remark, s.CreatedAt, s.UpdatedAt,
			s.RuntimeVersion, s.Command, s.AppPort, s.ProxyTarget, s.Passthrough, s.WAFOff, strings.Join(s.WAFExclude, " "), advJSON(s.Advanced),
			s.Target, s.TargetSite, s.Container, s.Redirect, s.RedirectCode, s.RedirectPath)
		if err != nil {
			return err
		}
		s.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := sv.st.DB.ExecContext(ctx, `UPDATE sites SET aliases=?, type=?, php_version=?, root=?, stopped=?, force_https=?, hsts=?,
		ssl_mode=?, ssl_challenge=?, ssl_wildcard=?, cert_issuer=?, cert_not_after=?, cert_error=?, dns_managed=?, dns_proxied=?,
		remark=?, updated_at=?, runtime_version=?, command=?, app_port=?, proxy_target=?, passthrough=?, waf_off=?, waf_exclude=?, advanced=?,
		target=?, target_site=?, container=?, redirect=?, redirect_code=?, redirect_path=? WHERE id=?`,
		string(aliases), s.Type, s.PHPVersion, s.Root, s.Stopped, s.ForceHTTPS, s.HSTS, s.SSLMode, s.SSLChallenge, s.SSLWildcard,
		s.CertIssuer, s.CertNotAfter, s.CertError, s.DNSManaged, s.DNSProxied, s.Remark, s.UpdatedAt,
		s.RuntimeVersion, s.Command, s.AppPort, s.ProxyTarget, s.Passthrough, s.WAFOff, strings.Join(s.WAFExclude, " "), advJSON(s.Advanced),
		s.Target, s.TargetSite, s.Container, s.Redirect, s.RedirectCode, s.RedirectPath, s.ID)
	return err
}

func advJSON(a webenv.Advanced) string {
	b, _ := json.Marshal(a)
	return string(b)
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

// reservedPort covers the stack's own loopback listeners.
func reservedPort(p int) bool {
	return p == webenv.NginxPort || p == webenv.ACMEPort || (p >= 9000 && p < 9100)
}

// checkPort validates a project port; isNew additionally requires it unbound.
func (sv *Service) checkPort(ctx context.Context, selfID int64, port int, isNew bool) error {
	if port < 1024 || port > 65535 || reservedPort(port) {
		return ErrBadPort
	}
	all, err := sv.List(ctx)
	if err != nil {
		return err
	}
	for _, s := range all {
		if s.ID != selfID && IsProject(s.Type) && s.AppPort == port {
			return fmt.Errorf("%w: %d", ErrPortTaken, port)
		}
	}
	if sv.Runtimes != nil && sv.Runtimes.UsedPorts(ctx)[port] {
		return fmt.Errorf("%w: %d", ErrPortTaken, port)
	}
	if isNew && !webenv.PortFree(port) {
		return fmt.Errorf("%w: %d", ErrPortTaken, port)
	}
	return nil
}

// freePort picks the first usable project port from 3000.
func (sv *Service) freePort(ctx context.Context) int {
	for p := 3000; p < 3999; p++ {
		if sv.checkPort(ctx, 0, p, true) == nil {
			return p
		}
	}
	return 0
}

func validTarget(target string, passthrough bool) bool {
	if passthrough {
		return passTargetRe.MatchString(target)
	}
	return urlTargetRe.MatchString(target)
}

func validCommand(c string) bool {
	return strings.TrimSpace(c) != "" && len(c) <= 2000 && !strings.ContainsAny(c, "\x00\n\r")
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
	byID := make(map[int64]Site, len(all))
	for _, s := range all {
		byID[s.ID] = s
	}
	out := make([]webenv.Site, 0, len(all))
	for _, s := range all {
		out = append(out, sv.specOf(s, byID))
	}
	return out, nil
}

// spec renders one entry; a link to another website needs the others (specs).
func (sv *Service) spec(s Site) webenv.Site { return sv.specOf(s, nil) }

func (sv *Service) specOf(s Site, byID map[int64]Site) webenv.Site {
	has := false
	if s.SSLMode != "none" && s.CertNotAfter > 0 {
		_, err := os.Stat(sv.Env.CertPath(s.Domain))
		has = err == nil
	}
	ws := webenv.Site{
		Domain: s.Domain, Aliases: s.Aliases, Stopped: s.Stopped, ForceHTTPS: s.ForceHTTPS, HSTS: s.HSTS, HasCert: has,
	}
	content := s
	switch s.target() {
	case TargetLink:
		t, ok := byID[s.TargetSite]
		if !ok && byID == nil {
			t, ok = sv.lookup(s.TargetSite)
		}
		if !ok || !t.HasContent() {
			ws.Type, ws.Stopped = "static", true // the website is gone: 503
			return ws
		}
		content = t
		ws.Stopped = s.Stopped || t.Stopped
	case TargetDocker, TargetURL:
		ws.Type, ws.Upstream = "proxy", s.ProxyTarget
		ws.WAFOff, ws.WAFExclude, ws.Advanced = s.WAFOff, s.WAFExclude, s.Advanced
		return ws
	case TargetPassthrough:
		ws.Type, ws.Upstream, ws.Passthrough = "proxy", s.ProxyTarget, true
		return ws
	case TargetRedirect:
		ws.Type, ws.Redirect, ws.RedirectCode, ws.RedirectPath = "static", s.Redirect, s.RedirectCode, s.RedirectPath
		return ws
	}
	ws.Type, ws.PHPVersion, ws.Root = content.Type, content.PHPVersion, content.Root
	ws.WAFOff, ws.WAFExclude, ws.Advanced = content.WAFOff, content.WAFExclude, content.Advanced
	if IsProject(content.Type) {
		ws.Upstream = fmt.Sprintf("http://127.0.0.1:%d", content.AppPort)
	}
	return ws
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
	all, err := sv.List(ctx)
	if err != nil {
		return err
	}
	for _, s := range all {
		if IsProject(s.Type) {
			if err := sv.ensureProject(ctx, s, log); err != nil {
				log("project %s: %v", s.Domain, err)
			}
		}
	}
	return nil
}

// RecreateProjects restarts the projects of a runtime version on its
// (updated) image.
func (sv *Service) RecreateProjects(ctx context.Context, typ, version string, log tasks.Logf) {
	all, err := sv.List(ctx)
	if err != nil {
		log("projects: %v", err)
		return
	}
	for _, s := range all {
		if s.Type != typ || s.RuntimeVersion != version {
			continue
		}
		log("recreating project %s", s.Domain)
		dockerx.Remove(ctx, ProjectContainer(s.Domain))
		if err := sv.ensureProject(ctx, s, log); err != nil {
			log("project %s: %v", s.Domain, err)
		}
	}
}

// ensureProject runs (or stops) the container of a runtime project. A project
// whose code is not deployed yet may crash-loop; that is not an error here.
func (sv *Service) ensureProject(ctx context.Context, s Site, log tasks.Logf) error {
	name := ProjectContainer(s.Domain)
	if s.Stopped {
		if dockerx.State(ctx, name) == "running" {
			log("stopping %s", name)
			_, err := dockerx.Run(ctx, "stop", name)
			return err
		}
		return nil
	}
	if sv.Runtimes == nil {
		return ErrNoRuntime
	}
	image, err := sv.Runtimes.RuntimeImage(ctx, s.Type, s.RuntimeVersion)
	if err != nil {
		return ErrNoRuntime
	}
	args := []string{
		"--network", "host", "-w", s.Root, "-v", s.Root + ":" + s.Root,
		"-e", fmt.Sprintf("PORT=%d", s.AppPort), "-e", "HOST=127.0.0.1",
		image, "sh", "-c", s.Command,
	}
	return webenv.EnsureContainer(ctx, log, name, args, nil, false)
}

// Reapply rebuilds the stack containers whose settings changed (e.g. the WAF
// switches the nginx image) and reloads every config (task).
func (sv *Service) Reapply(title string) string {
	return sv.tasks.Start(title, func(ctx context.Context, log tasks.Logf) error {
		if !sv.Env.Installed(ctx) {
			log("web stack not installed: settings saved, applied when it is installed")
			return nil
		}
		if err := sv.ensureStack(ctx, "", log); err != nil {
			return err
		}
		log("applying configuration")
		return sv.Apply(ctx)
	})
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
		if sv.RebuildMarker != "" {
			if _, err := os.Stat(sv.RebuildMarker); err == nil {
				os.Remove(sv.RebuildMarker)
				if err := sv.removeStack(ctx); err != nil {
					return err
				}
			}
		}
		if err := sv.ensureStack(ctx, "", func(string, ...any) {}); err != nil {
			return err
		}
		return sv.Apply(ctx)
	})
}

// removeStack deletes the edge, nginx and PHP-FPM containers so ensureStack
// recreates them from the database (panel repair "rebuild the web stack").
// Site files, certificates and databases live outside the containers.
func (sv *Service) removeStack(ctx context.Context) error {
	names := []string{webenv.HAProxyName, webenv.NginxName}
	for _, v := range webenv.PHPVersions {
		names = append(names, webenv.PHPName(v))
	}
	for _, n := range names {
		if dockerx.State(ctx, n) != "" {
			if err := dockerx.Remove(ctx, n); err != nil {
				return err
			}
		}
	}
	return nil
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
	// Runtime projects.
	RuntimeVersion string `json:"runtime_version"`
	Command        string `json:"command"`
	AppPort        int    `json:"app_port"`
	// Domain entries without a website (type "proxy"): what they serve.
	ProxyTarget  string `json:"proxy_target"`
	Passthrough  bool   `json:"passthrough"`
	Target       string `json:"target"` // link | docker | url | passthrough | redirect
	TargetSite   int64  `json:"target_site"`
	Container    string `json:"container"`
	Redirect     string `json:"redirect"`
	RedirectCode int    `json:"redirect_code"`
	RedirectPath bool   `json:"redirect_path"`
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
	case "node", "python", "java", "tomcat":
		s.PHPVersion, s.RuntimeVersion = "", in.RuntimeVersion
		if sv.Runtimes == nil {
			return "", ErrNoRuntime
		}
		if _, err := sv.Runtimes.RuntimeImage(ctx, s.Type, s.RuntimeVersion); err != nil {
			return "", ErrNoRuntime
		}
		s.Command = strings.TrimSpace(in.Command)
		if s.Command == "" {
			s.Command = DefaultCommands[s.Type]
		}
		if !validCommand(s.Command) {
			return "", ErrBadCommand
		}
		s.AppPort = in.AppPort
		if s.AppPort == 0 {
			s.AppPort = sv.freePort(ctx)
		}
		if err := sv.checkPort(ctx, 0, s.AppPort, true); err != nil {
			return "", err
		}
	case "proxy":
		s.PHPVersion = ""
		t := in.Target
		switch {
		case t != "":
		case in.Passthrough:
			t = TargetPassthrough
		case in.Container != "":
			t = TargetDocker
		default:
			t = TargetURL
		}
		if t == TargetSite {
			return "", ErrNoContent
		}
		if err := sv.applyTarget(ctx, &s, TargetInput{Target: t, TargetSite: in.TargetSite, Container: in.Container, ProxyTarget: in.ProxyTarget,
			Redirect: in.Redirect, RedirectCode: in.RedirectCode, RedirectPath: in.RedirectPath}); err != nil {
			return "", err
		}
		if s.Passthrough && in.SSL != "none" && in.SSL != "" {
			return "", ErrPassSSL
		}
	default:
		return "", ErrBadType
	}
	if s.HasContent() {
		s.Target = TargetSite
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
		if s.Type != "proxy" {
			if err := createRoot(s.Root, s.Domain, s.Type); err != nil {
				return err
			}
			log("document root %s", s.Root)
		}
		if err := sv.save(ctx, &s); err != nil {
			return err
		}
		if IsProject(s.Type) {
			log("starting project container on 127.0.0.1:%d", s.AppPort)
			if err := sv.ensureProject(ctx, s, log); err != nil {
				return err
			}
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

// placeholders is the starter code of a new, empty project directory.
func placeholders(typ, domain string) map[string]string {
	switch typ {
	case "node":
		return map[string]string{
			"package.json": `{"name": "app", "private": true, "scripts": {"start": "node server.js"}}` + "\n",
			"server.js": fmt.Sprintf(`const http = require('http')
http.createServer((req, res) => {
  res.writeHead(200, {'Content-Type': 'text/html; charset=utf-8'})
  res.end('<h1>%s</h1><p>Node.js project created by lmcwhm.</p>\n')
}).listen(process.env.PORT, process.env.HOST)
`, domain),
		}
	case "python":
		return map[string]string{
			"requirements.txt": "",
			"app.py": fmt.Sprintf(`import os
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.end_headers()
        self.wfile.write(b"<h1>%s</h1><p>Python project created by lmcwhm.</p>\n")


HTTPServer((os.environ["HOST"], int(os.environ["PORT"])), Handler).serve_forever()
`, domain),
		}
	case "java":
		return map[string]string{"README.txt": "Upload app.jar (e.g. a Spring Boot fat jar) here, then restart the project.\n"}
	case "tomcat":
		return map[string]string{"index.jsp": "<%@ page contentType=\"text/html; charset=UTF-8\" %><h1>" + domain + "</h1><p>Tomcat <%= application.getServerInfo() %></p>\n",
			"README.txt": "Upload a .war (deployed as ROOT) or the web app's files here, then restart the project.\n"}
	}
	page := fmt.Sprintf("<!doctype html><meta charset=utf-8><title>%s</title><h1>%s</h1><p>Site created by lmcwhm.</p>\n", domain, domain)
	return map[string]string{"index.html": page}
}

// writeUserINI keeps the PHP open_basedir restriction in the site's
// .user.ini (read by PHP-FPM per directory; dot-files are not served).
func writeUserINI(s Site) error {
	if s.Type != "php" {
		return nil
	}
	p := filepath.Join(s.Root, ".user.ini")
	if !s.Advanced.OpenBasedir {
		if b, err := os.ReadFile(p); err == nil && strings.HasPrefix(string(b), "; lmcwhm") {
			return os.Remove(p)
		}
		return nil
	}
	body := fmt.Sprintf("; lmcwhm: open_basedir (Website → Advanced)\nopen_basedir=%s/:/tmp/\n", s.Root)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Lchown(p, 0, 0) // root-owned: PHP (www-data) cannot lift it
}

func createRoot(root, domain, typ string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	entries, _ := os.ReadDir(root)
	if len(entries) == 0 {
		for name, body := range placeholders(typ, domain) {
			if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
				return err
			}
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
	if s.Passthrough {
		return ErrPassSSL
	}
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
	res, err := sv.issuer.Obtain(ctx, acme.Request{Domains: certNames(domains), Challenge: ch}, log)
	if err != nil && ch == acme.HTTP01 {
		// HTTP-01 cannot pass when a proxy in front (e.g. Cloudflare with
		// Full (strict)) rejects the origin before it has a valid certificate.
		// DNS-01 does not depend on the HTTP path, so retry with it when possible.
		if cfg, cerr := sv.cfg.Get(ctx); cerr == nil && cfg.CFToken != "" {
			log("HTTP-01 failed: %v", err)
			log("retrying with DNS-01 via Cloudflare")
			ch = acme.DNS01
			res, err = sv.issuer.Obtain(ctx, acme.Request{Domains: certNames(domains), Challenge: ch}, log)
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

// certNames removes duplicates and names already covered by a wildcard in the
// same list (CAs reject e.g. www.example.com together with *.example.com).
func certNames(in []string) []string {
	wild := map[string]bool{}
	for _, v := range in {
		if strings.HasPrefix(v, "*.") {
			wild[v[2:]] = true
		}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if seen[v] {
			continue
		}
		if !strings.HasPrefix(v, "*.") {
			// A wildcard matches exactly one extra label.
			if i := strings.IndexByte(v, '.'); i > 0 && wild[v[i+1:]] {
				continue
			}
		}
		seen[v] = true
		out = append(out, v)
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
	if s.Passthrough {
		return "", ErrPassSSL
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
	if s.Passthrough {
		return ErrPassSSL
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
	DNSManaged *bool     `json:"dns_managed"` // keep A / AAAA records in Cloudflare
	// Runtime projects.
	RuntimeVersion *string `json:"runtime_version"`
	Command        *string `json:"command"`
	AppPort        *int    `json:"app_port"`
	// Reverse proxies.
	ProxyTarget *string `json:"proxy_target"`
	// WAF.
	WAFOff     *bool     `json:"waf_off"`
	WAFExclude *[]string `json:"waf_exclude"`
	// Advanced nginx settings (replaced as a whole).
	Advanced *webenv.Advanced `json:"advanced"`
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
	if in.DNSManaged != nil {
		if *in.DNSManaged {
			if cfg, err := sv.cfg.Get(ctx); err != nil || cfg.CFToken == "" {
				return "", ErrNoCFToken
			}
		}
		s.DNSManaged = *in.DNSManaged
	}
	if in.RuntimeVersion != nil || in.Command != nil || in.AppPort != nil {
		if !IsProject(s.Type) {
			return "", ErrNotProject
		}
	}
	if in.RuntimeVersion != nil {
		if sv.Runtimes == nil {
			return "", ErrNoRuntime
		}
		if _, err := sv.Runtimes.RuntimeImage(ctx, s.Type, *in.RuntimeVersion); err != nil {
			return "", ErrNoRuntime
		}
		s.RuntimeVersion = *in.RuntimeVersion
	}
	if in.Command != nil {
		if s.Command = strings.TrimSpace(*in.Command); !validCommand(s.Command) {
			return "", ErrBadCommand
		}
	}
	if in.AppPort != nil && *in.AppPort != s.AppPort {
		if err := sv.checkPort(ctx, s.ID, *in.AppPort, true); err != nil {
			return "", err
		}
		s.AppPort = *in.AppPort
	}
	if in.Advanced != nil {
		a := *in.Advanced
		if err := a.Normalize(); err != nil {
			return "", err
		}
		s.Advanced = a
		if err := writeUserINI(s); err != nil {
			return "", err
		}
	}
	if in.WAFOff != nil {
		s.WAFOff = *in.WAFOff
	}
	if in.WAFExclude != nil {
		ids := []string{}
		for _, id := range *in.WAFExclude {
			if id = strings.TrimSpace(id); id != "" {
				if !wafRuleRe.MatchString(id) {
					return "", ErrBadWAFRule
				}
				ids = append(ids, id)
			}
		}
		s.WAFExclude = ids
	}
	if in.ProxyTarget != nil {
		target := strings.TrimRight(strings.TrimSpace(*in.ProxyTarget), "/")
		if t := s.target(); t != TargetURL && t != TargetDocker && t != TargetPassthrough || !validTarget(target, s.Passthrough) {
			return "", ErrBadTarget
		}
		s.ProxyTarget = target
	}
	return sv.tasks.Start("site.update:"+s.Domain, func(ctx context.Context, log tasks.Logf) error {
		if err := sv.ensureStack(ctx, s.PHPVersion, log); err != nil {
			return err
		}
		if err := sv.save(ctx, &s); err != nil {
			return err
		}
		if s.DNSManaged && (in.Aliases != nil || in.DNSProxied != nil || in.DNSManaged != nil) {
			if err := sv.syncDNS(ctx, s, log); err != nil {
				log("DNS: %v", err)
			}
		}
		if IsProject(s.Type) {
			if err := sv.ensureProject(ctx, s, log); err != nil {
				return err
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
	if s.HasContent() {
		if err := sv.linkedErr(ctx, s.ID); err != nil {
			return "", err
		}
	}
	return sv.tasks.Start("site.delete:"+s.Domain, func(ctx context.Context, log tasks.Logf) error {
		if _, err := sv.st.DB.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, s.ID); err != nil {
			return err
		}
		os.Remove(sv.Env.CertPath(s.Domain))
		if IsProject(s.Type) {
			log("removing container %s", ProjectContainer(s.Domain))
			dockerx.Remove(ctx, ProjectContainer(s.Domain))
		}
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

// ---- runtime projects ----

func (sv *Service) project(ctx context.Context, id int64) (Site, error) {
	s, err := sv.Get(ctx, id)
	if err != nil {
		return s, err
	}
	if !IsProject(s.Type) {
		return s, ErrNotProject
	}
	return s, nil
}

// ProjectLogs returns the tail of a project's stdout/stderr.
func (sv *Service) ProjectLogs(ctx context.Context, id int64, n int) (string, error) {
	s, err := sv.project(ctx, id)
	if err != nil {
		return "", err
	}
	return dockerx.Logs(ctx, ProjectContainer(s.Domain), n), nil
}

// ProjectState is the project container's state ("" when missing).
func (sv *Service) ProjectState(ctx context.Context, id int64) (string, error) {
	s, err := sv.project(ctx, id)
	if err != nil {
		return "", err
	}
	return dockerx.State(ctx, ProjectContainer(s.Domain)), nil
}

// RestartProject restarts the container, e.g. after deploying new code.
func (sv *Service) RestartProject(ctx context.Context, id int64) error {
	s, err := sv.project(ctx, id)
	if err != nil {
		return err
	}
	if s.Stopped {
		return nil
	}
	if dockerx.State(ctx, ProjectContainer(s.Domain)) == "" {
		return sv.tasks.RunSync(ctx, func(ctx context.Context) error { return sv.ensureProject(ctx, s, func(string, ...any) {}) })
	}
	_, err = dockerx.Run(ctx, "restart", ProjectContainer(s.Domain))
	return err
}

// UsesRuntime reports whether a website runs on an App Store runtime version.
func (sv *Service) UsesRuntime(ctx context.Context, app, version string) bool {
	var n int
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE type = ? AND runtime_version = ?`, app, version).Scan(&n)
	return n > 0
}

// ---- renewal ----

// RenewDue renews the ACME (Let's Encrypt / LiteSSL) certificates that
// expire within 30 days (the "ssl_renew" scheduled task), logging to out.
func (sv *Service) RenewDue(ctx context.Context, out io.Writer) error {
	all, err := sv.List(ctx)
	if err != nil {
		return err
	}
	soon := time.Now().Add(30 * 24 * time.Hour).Unix()
	var failed []string
	n := 0
	for _, s := range all {
		if s.SSLMode != "acme" || s.Stopped {
			continue
		}
		left := time.Until(time.Unix(s.CertNotAfter, 0)).Hours() / 24
		if s.CertNotAfter > 0 && s.CertNotAfter >= soon {
			fmt.Fprintf(out, "%s: valid for %.0f more days\n", s.Domain, left)
			continue
		}
		n++
		fmt.Fprintf(out, "%s: renewing (%.0f days left)\n", s.Domain, left)
		challenge := acme.Challenge(s.SSLChallenge)
		if challenge != "dns" {
			challenge = "http"
		}
		site := s
		err := sv.tasks.RunSync(ctx, func(ctx context.Context) error {
			return sv.issue(ctx, &site, challenge, s.SSLWildcard, func(f string, a ...any) { fmt.Fprintf(out, "  "+f+"\n", a...) })
		})
		if err != nil {
			failed = append(failed, s.Domain)
			fmt.Fprintf(out, "%s: FAILED: %v\n", s.Domain, err)
		} else {
			fmt.Fprintf(out, "%s: renewed\n", s.Domain)
		}
	}
	if n == 0 {
		fmt.Fprintln(out, "no certificate is due (renewed 30 days before expiry)")
	}
	if len(failed) > 0 {
		return fmt.Errorf("renewal failed: %s", strings.Join(failed, ", "))
	}
	return nil
}
