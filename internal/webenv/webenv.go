// Package webenv owns the web-serving stack: the HAProxy SNI edge on :80/:443,
// the shared Nginx, and one PHP-FPM container per PHP version. Every config
// file is rendered from the site list, so Apply() is idempotent and repairs drift.
package webenv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/tlsutil"
)

//go:embed templates/*.tmpl
var tmplFS embed.FS

var tmpl = template.Must(template.ParseFS(tmplFS, "templates/*.tmpl"))

const (
	HAProxyImage = "haproxy:3.0"
	NginxImage   = "nginx:stable"

	HAProxyName = "lmcwhm-haproxy"
	NginxName   = "lmcwhm-nginx"

	NginxPort = 8080 // loopback only
	ACMEPort  = 8899 // loopback only; served by the panel process
)

// PHPVersions supported by the stack (7.4–8.1 are end-of-life upstream).
var PHPVersions = []string{"8.4", "8.3", "8.2", "8.1", "8.0", "7.4"}

func ValidPHP(v string) bool {
	for _, x := range PHPVersions {
		if x == v {
			return true
		}
	}
	return false
}

// PHPPort maps "8.3" -> 9083.
func PHPPort(v string) int {
	return 9000 + int(v[0]-'0')*10 + int(v[2]-'0')
}

func PHPName(v string) string  { return "lmcwhm-php" + strings.ReplaceAll(v, ".", "") }
func PHPImage(v string) string { return "lmcwhm/php:" + v }

// Site is what the stack needs to know about a website.
type Site struct {
	Domain     string
	Aliases    []string
	Type       string // "php" | "static" | "node" | "python" | "java" | "proxy"
	PHPVersion string
	Root       string
	Stopped    bool
	ForceHTTPS bool
	HSTS       bool
	HasCert    bool
	// Upstream is the URL nginx proxies to (runtime projects and proxies).
	Upstream string
	// Passthrough sends the raw TLS stream for every host to Upstream
	// (host:port) instead of terminating it; nginx is not involved.
	Passthrough bool
}

func (s Site) Hosts() []string { return append([]string{s.Domain}, s.Aliases...) }

// Passthrough routes a hostname's raw TLS stream to Target.
type Passthrough struct {
	Host          string
	Target        string
	ProxyProtocol bool
}

type Env struct {
	Home     string // /opt/lmcwhm
	WWWRoot  string // /www/wwwroot
	WWWLogs  string // /www/wwwlogs
	Timezone string
}

func New(home string) *Env {
	root := os.Getenv("LMCWHM_WWW")
	if root == "" {
		root = "/www"
	}
	return &Env{Home: home, WWWRoot: filepath.Join(root, "wwwroot"), WWWLogs: filepath.Join(root, "wwwlogs"), Timezone: "UTC"}
}

func (e *Env) EdgeDir() string  { return filepath.Join(e.Home, "edge") }
func (e *Env) CertDir() string  { return filepath.Join(e.EdgeDir(), "certs") }
func (e *Env) NginxDir() string { return filepath.Join(e.Home, "web", "nginx") }
func (e *Env) PHPDir(v string) string {
	return filepath.Join(e.Home, "web", "php", strings.ReplaceAll(v, ".", ""))
}

// CertPath is the combined cert+key PEM HAProxy loads for a domain.
func (e *Env) CertPath(domain string) string { return filepath.Join(e.CertDir(), domain+".pem") }

// SiteRoot is the default document root for a domain.
func (e *Env) SiteRoot(domain string) string { return filepath.Join(e.WWWRoot, domain) }

func render(name string, data any) ([]byte, error) {
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// ---- status ----

type ServiceStatus struct {
	Name  string `json:"name"`
	Image string `json:"image"`
	State string `json:"state"` // running / exited / "" (missing)
}

type Status struct {
	Docker    bool            `json:"docker"`
	Installed bool            `json:"installed"`
	Services  []ServiceStatus `json:"services"`
	PHP       []ServiceStatus `json:"php"`
}

func (e *Env) Status(ctx context.Context) Status {
	st := Status{Docker: dockerx.Available(ctx)}
	if !st.Docker {
		return st
	}
	st.Services = []ServiceStatus{
		{Name: HAProxyName, Image: HAProxyImage, State: dockerx.State(ctx, HAProxyName)},
		{Name: NginxName, Image: NginxImage, State: dockerx.State(ctx, NginxName)},
	}
	st.Installed = st.Services[0].State != "" && st.Services[1].State != ""
	for _, v := range PHPVersions {
		if s := dockerx.State(ctx, PHPName(v)); s != "" {
			st.PHP = append(st.PHP, ServiceStatus{Name: v, Image: PHPImage(v), State: s})
		}
	}
	return st
}

// Installed reports whether the edge and nginx containers exist.
func (e *Env) Installed(ctx context.Context) bool {
	return dockerx.State(ctx, HAProxyName) != "" && dockerx.State(ctx, NginxName) != ""
}

// ---- install / ensure ----

var ErrPortBusy = errors.New("web.port_busy")

func portFree(port int) bool {
	for _, network := range []string{"tcp4", "tcp6"} {
		ln, err := net.Listen(network, fmt.Sprintf(":%d", port))
		if err != nil {
			if network == "tcp6" && strings.Contains(err.Error(), "address family") {
				continue // host without IPv6
			}
			return false
		}
		ln.Close()
	}
	return true
}

// hasIPv6 reports whether the host can bind IPv6 sockets.
func hasIPv6() bool {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// Ensure installs/starts the edge and nginx, writing base configs. Safe to repeat.
func (e *Env) Ensure(ctx context.Context, sites []Site, pass []Passthrough, log func(string, ...any)) error {
	if !dockerx.Available(ctx) {
		return dockerx.ErrNoDocker
	}
	for _, d := range []string{e.CertDir(), filepath.Join(e.EdgeDir(), "maps"), filepath.Join(e.EdgeDir(), "run"),
		filepath.Join(e.NginxDir(), "conf.d"), e.WWWRoot, e.WWWLogs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := e.ensureDefaultCert(); err != nil {
		return err
	}
	// Configs first so the containers start with a valid setup.
	if err := e.writeEdge(sites, pass); err != nil {
		return err
	}
	if err := e.writeNginx(sites); err != nil {
		return err
	}

	for _, img := range []string{HAProxyImage, NginxImage} {
		if !dockerx.ImageExists(ctx, img) {
			log("pulling %s", img)
			if _, err := dockerx.Run(ctx, "pull", "-q", img); err != nil {
				return err
			}
		}
	}

	if err := EnsureContainer(ctx, log, NginxName, []string{
		"--network", "host",
		"-v", filepath.Join(e.NginxDir(), "nginx.conf") + ":/etc/nginx/nginx.conf:ro",
		"-v", filepath.Join(e.NginxDir(), "conf.d") + ":/etc/nginx/conf.d:ro",
		"-v", e.WWWRoot + ":" + e.WWWRoot,
		"-v", e.WWWLogs + ":" + e.WWWLogs,
		NginxImage,
	}, nil, true); err != nil {
		return err
	}
	return EnsureContainer(ctx, log, HAProxyName, []string{
		"--network", "host", "--user", "0",
		"-v", e.EdgeDir() + ":" + e.EdgeDir(),
		HAProxyImage,
		"haproxy", "-W", "-db", "-f", filepath.Join(e.EdgeDir(), "haproxy.cfg"),
	}, []int{80, 443}, true)
}

// EnsureContainer makes sure container name runs with exactly args. A hash of
// the arguments is stored as a label, so changed settings recreate it. With
// wait, it returns only once the container stays up; otherwise a crash-looping
// container (e.g. a project whose code is not uploaded yet) is not an error.
func EnsureContainer(ctx context.Context, log func(string, ...any), name string, args []string, ports []int, wait bool) error {
	sum := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	spec := hex.EncodeToString(sum[:8])
	state := dockerx.State(ctx, name)
	if state != "" && dockerx.Label(ctx, name, "lmcwhm.spec") != spec {
		log("recreating container %s (configuration changed)", name)
		if err := dockerx.Remove(ctx, name); err != nil {
			return err
		}
		state = ""
	}
	switch state {
	case "running":
		return nil
	case "":
		for _, p := range ports {
			if !portFree(p) {
				return fmt.Errorf("%w: %d", ErrPortBusy, p)
			}
		}
		log("creating container %s", name)
		run := append([]string{"run", "-d", "--name", name, "--restart", "always",
			"--ulimit", "nofile=1048576:1048576",
			"--label", "lmcwhm.managed=true", "--label", "lmcwhm.spec=" + spec}, args...)
		if _, err := dockerx.Run(ctx, run...); err != nil {
			return err
		}
	default:
		log("starting container %s", name)
		if _, err := dockerx.Run(ctx, "start", name); err != nil {
			return err
		}
	}
	if !wait {
		return nil
	}
	return waitRunning(ctx, name)
}

// waitRunning waits until the container stays up; a crash loop is reported with its logs.
func waitRunning(ctx context.Context, name string) error {
	ok := 0
	for i := 0; i < 40; i++ {
		if dockerx.State(ctx, name) == "running" {
			if ok++; ok >= 3 {
				return nil
			}
		} else {
			ok = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("container %s is not running: %s", name, dockerx.Logs(ctx, name, 20))
}

// EnsurePHP builds the PHP image (first time only) and starts its FPM container.
func (e *Env) EnsurePHP(ctx context.Context, v string, log func(string, ...any)) error {
	if !ValidPHP(v) {
		return fmt.Errorf("unsupported PHP version %q", v)
	}
	dir := e.PHPDir(v)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fpm, err := render("php-fpm.conf.tmpl", map[string]any{"Port": PHPPort(v)})
	if err != nil {
		return err
	}
	ini, err := render("php.ini.tmpl", map[string]any{"Timezone": e.Timezone})
	if err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(dir, "zz-docker.conf"), fpm); err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(dir, "zz-lmcwhm.ini"), ini); err != nil {
		return err
	}
	if !dockerx.ImageExists(ctx, PHPImage(v)) {
		log("building PHP %s image (first time, a few minutes)", v)
		df, err := render("php.Dockerfile.tmpl", map[string]any{"Version": v})
		if err != nil {
			return err
		}
		if err := dockerx.Build(ctx, PHPImage(v), string(df)); err != nil {
			return err
		}
	}
	return EnsureContainer(ctx, log, PHPName(v), []string{
		"--network", "host",
		"-v", e.WWWRoot + ":" + e.WWWRoot,
		"-v", filepath.Join(dir, "zz-docker.conf") + ":/usr/local/etc/php-fpm.d/zz-docker.conf:ro",
		"-v", filepath.Join(dir, "zz-lmcwhm.ini") + ":/usr/local/etc/php/conf.d/zz-lmcwhm.ini:ro",
		PHPImage(v),
	}, []int{PHPPort(v)}, true)
}

// ---- config rendering ----

// PortFree reports whether nothing listens on port (IPv4 and IPv6).
func PortFree(port int) bool { return portFree(port) }

func (e *Env) ensureDefaultCert() error {
	p := filepath.Join(e.CertDir(), "default.pem")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	dir, err := os.MkdirTemp("", "lmcwhm-default-cert")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := tlsutil.GenerateSelfSigned(dir, []string{"lmcwhm.invalid"}); err != nil {
		return err
	}
	crt, _ := os.ReadFile(filepath.Join(dir, tlsutil.CertFile))
	key, _ := os.ReadFile(filepath.Join(dir, tlsutil.KeyFile))
	return os.WriteFile(p, append(crt, key...), 0o600)
}

// Host maps come in pairs, read by HAProxy in this order: <name>_exact.map
// (map_str, exact hostnames) and <name>_wild.map (map_end, "*.a.com" stored as
// ".a.com", longest suffix first). An exact hostname therefore always wins
// over a wildcard, and a deeper wildcard over a shallower one, like nginx's
// server_name. MatchHost applies the same rules in Go.
func hostMaps(name string, entries map[string]string, files map[string][]byte) {
	var exact, wild []string
	for h, v := range entries {
		if strings.HasPrefix(h, "*.") {
			wild = append(wild, h[1:]+" "+v)
		} else {
			exact = append(exact, h+" "+v)
		}
	}
	sort.Strings(exact)
	sort.Slice(wild, func(i, j int) bool {
		a, b := strings.Fields(wild[i])[0], strings.Fields(wild[j])[0]
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a < b
	})
	files["maps/"+name+"_exact.map"] = []byte(strings.Join(exact, "\n") + "\n")
	files["maps/"+name+"_wild.map"] = []byte(strings.Join(wild, "\n") + "\n")
}

// MatchHost returns the pattern (an exact hostname or "*.suffix") that routes
// host, or "" when none does (the default route).
func MatchHost(patterns []string, host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	best := ""
	for _, p := range patterns {
		p = strings.ToLower(p)
		if p == host {
			return p
		}
		if strings.HasPrefix(p, "*.") && strings.HasSuffix(host, p[1:]) && len(p) > len(best) {
			best = p
		}
	}
	return best
}

func flag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

type edgeData struct {
	Dir         string
	IPv6        bool
	ACMEPort    int
	NginxPort   int
	Passthrough []passBackend
}

type passBackend struct {
	Backend, Target string
	ProxyProtocol   bool
}

func (e *Env) edgeFiles(sites []Site, pass []Passthrough) (map[string][]byte, error) {
	dir := e.EdgeDir()
	files := map[string][]byte{}

	crt := []string{filepath.Join(e.CertDir(), "default.pem")} // first entry = default certificate
	for _, s := range sites {
		if s.HasCert {
			if _, err := os.Stat(e.CertPath(s.Domain)); err == nil {
				crt = append(crt, e.CertPath(s.Domain))
			}
		}
	}
	files["crt-list.txt"] = []byte(strings.Join(crt, "\n") + "\n")

	// Every hostname gets an entry in each map (also "0" / be_terminate), so an
	// exact hostname is never captured by another site's wildcard.
	sni, fh, hsts := map[string]string{}, map[string]string{}, map[string]string{}
	for _, s := range sites {
		for _, h := range s.Hosts() {
			h = strings.ToLower(h)
			sni[h] = "be_terminate"
			// Passthrough hosts terminate TLS at their backend, so plain HTTP always redirects.
			fh[h] = flag(!s.Stopped && (s.Passthrough || (s.ForceHTTPS && s.HasCert)))
			hsts[h] = flag(!s.Stopped && s.HSTS && s.HasCert)
		}
		if s.Passthrough && !s.Stopped {
			for _, h := range s.Hosts() {
				pass = append(pass, Passthrough{Host: h, Target: s.Upstream})
			}
		}
	}
	data := edgeData{Dir: dir, IPv6: hasIPv6(), ACMEPort: ACMEPort, NginxPort: NginxPort}
	backends := map[string]string{} // one backend per distinct target
	for _, p := range pass {
		key := fmt.Sprintf("%s|%v", p.Target, p.ProxyProtocol)
		be, ok := backends[key]
		if !ok {
			be = fmt.Sprintf("be_pass_%d", len(backends))
			backends[key] = be
			data.Passthrough = append(data.Passthrough, passBackend{Backend: be, Target: p.Target, ProxyProtocol: p.ProxyProtocol})
		}
		sni[strings.ToLower(p.Host)] = be
	}
	hostMaps("sni", sni, files)
	hostMaps("force_https", fh, files)
	hostMaps("hsts", hsts, files)
	cfg, err := render("haproxy.cfg.tmpl", data)
	if err != nil {
		return nil, err
	}
	files["haproxy.cfg"] = cfg
	return files, nil
}

func (e *Env) writeEdge(sites []Site, pass []Passthrough) error {
	files, err := e.edgeFiles(sites, pass)
	if err != nil {
		return err
	}
	for name, body := range files {
		if err := writeIfChanged(filepath.Join(e.EdgeDir(), name), body); err != nil {
			return err
		}
	}
	for _, old := range []string{"passthrough.map", "force_https.map", "hsts.map"} { // before v0.4
		os.Remove(filepath.Join(e.EdgeDir(), "maps", old))
	}
	return nil
}

func (e *Env) nginxFiles(sites []Site) (map[string][]byte, error) {
	files := map[string][]byte{}
	main, err := render("nginx.conf.tmpl", map[string]any{"NginxPort": NginxPort})
	if err != nil {
		return nil, err
	}
	files["nginx.conf"] = main
	for _, s := range sites {
		if s.Passthrough {
			continue
		}
		port := 0
		if s.Type == "php" {
			port = PHPPort(s.PHPVersion)
		}
		v, err := render("vhost.conf.tmpl", map[string]any{
			"Domain": s.Domain, "ServerNames": strings.Join(s.Hosts(), " "), "Root": s.Root,
			"LogDir": e.WWWLogs, "NginxPort": NginxPort, "PHPPort": port, "Stopped": s.Stopped,
			"Upstream": s.Upstream,
		})
		if err != nil {
			return nil, err
		}
		files[filepath.Join("conf.d", s.Domain+".conf")] = v
	}
	return files, nil
}

func (e *Env) writeNginx(sites []Site) error {
	files, err := e.nginxFiles(sites)
	if err != nil {
		return err
	}
	confd := filepath.Join(e.NginxDir(), "conf.d")
	// Remove vhosts of deleted sites.
	entries, _ := os.ReadDir(confd)
	for _, ent := range entries {
		if _, keep := files[filepath.Join("conf.d", ent.Name())]; !keep {
			os.Remove(filepath.Join(confd, ent.Name()))
		}
	}
	for name, body := range files {
		if err := writeIfChanged(filepath.Join(e.NginxDir(), name), body); err != nil {
			return err
		}
	}
	return nil
}

// VhostConfig returns the rendered nginx config for one site (for display).
func (e *Env) VhostConfig(s Site) (string, error) {
	if s.Passthrough {
		return fmt.Sprintf("# %s: TLS passthrough to %s (no nginx config)\n", strings.Join(s.Hosts(), " "), s.Upstream), nil
	}
	files, err := e.nginxFiles([]Site{s})
	if err != nil {
		return "", err
	}
	return string(files[filepath.Join("conf.d", s.Domain+".conf")]), nil
}

// Apply re-renders every config from sites, validates it and hot-reloads Nginx
// and HAProxy. On a validation failure the previous files are restored.
func (e *Env) Apply(ctx context.Context, sites []Site, pass []Passthrough) error {
	if !e.Installed(ctx) {
		return errors.New("web.not_installed")
	}
	// ---- nginx ----
	snap, err := snapshot(e.NginxDir())
	if err != nil {
		return err
	}
	if err := e.writeNginx(sites); err != nil {
		return err
	}
	if out, err := dockerx.Run(ctx, "exec", NginxName, "nginx", "-t"); err != nil {
		restore(e.NginxDir(), snap)
		return fmt.Errorf("nginx config test failed: %v %s", err, out)
	}
	if _, err := dockerx.Run(ctx, "exec", NginxName, "nginx", "-s", "reload"); err != nil {
		return err
	}

	// ---- haproxy ----
	esnap, err := snapshot(e.EdgeDir())
	if err != nil {
		return err
	}
	if err := e.writeEdge(sites, pass); err != nil {
		return err
	}
	if _, err := dockerx.Run(ctx, "exec", HAProxyName, "haproxy", "-c", "-q", "-f", filepath.Join(e.EdgeDir(), "haproxy.cfg")); err != nil {
		restore(e.EdgeDir(), esnap)
		return fmt.Errorf("haproxy config test failed: %w", err)
	}
	// Master-worker reload: new workers take over, old ones drain existing connections.
	if _, err := dockerx.Run(ctx, "kill", "-s", "USR2", HAProxyName); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}

// ---- file helpers ----

func writeIfChanged(path string, body []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, body) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// snapshot captures the generated text files under dir (certs excluded) so a
// failed apply can be rolled back.
func snapshot(dir string) (map[string][]byte, error) {
	snap := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "certs" || d.Name() == "run" {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(p)
		if err == nil {
			snap[p] = b
		}
		return nil
	})
	return snap, err
}

func restore(dir string, snap map[string][]byte) {
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && (d.Name() == "certs" || d.Name() == "run") {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := snap[p]; !ok {
			os.Remove(p)
		}
		return nil
	})
	for p, b := range snap {
		os.WriteFile(p, b, 0o644)
	}
}
