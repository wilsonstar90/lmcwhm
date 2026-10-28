package site

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

// The SNI page is a view over the sites table: every hostname belongs to
// exactly one site (its domain or an alias), and that site decides how the
// edge handles it. Docker bindings and remote servers are proxy sites, so
// there is a single owner per hostname and nothing to keep in sync.

// Route is one hostname on the edge.
type Route struct {
	Host     string `json:"host"`
	Wildcard bool   `json:"wildcard"`
	Primary  bool   `json:"primary"` // the site's main domain (not an alias)
	SiteID   int64  `json:"site_id"`
	Domain   string `json:"domain"` // owning site
	Type     string `json:"type"`
	// Mode is "terminate" (TLS ends at HAProxy, then nginx) or "passthrough".
	Mode     string `json:"mode"`
	Upstream string `json:"upstream"` // where nginx / HAProxy forwards ("" = served by nginx)
	Stopped  bool   `json:"stopped"`
	// Container runs the target: a runtime project, or the container whose
	// published port the upstream points at (filled in by the API layer).
	Container  string `json:"container"`
	ForceHTTPS bool   `json:"force_https"`
	HSTS       bool   `json:"hsts"`
	CertIssuer string `json:"cert_issuer"`
	CertExpiry int64  `json:"cert_not_after"`
	CertCovers bool   `json:"cert_covers"` // the installed certificate is valid for this hostname
	DNSManaged bool   `json:"dns_managed"`
	DNSProxied bool   `json:"dns_proxied"`
	// Overrides lists the exact hostnames of other sites inside a wildcard.
	Overrides []string `json:"overrides,omitempty"`
	// Health: "up", "down" or "" (not checked: served by nginx itself).
	Health string `json:"health"`
	// DNS: "ok" (points at this server), "other", "none" or "" (wildcard / not checked).
	DNS      string   `json:"dns"`
	DNSAddrs []string `json:"dns_addrs,omitempty"`
	Warnings []string `json:"warnings"`
}

// Resolution explains which route serves a hostname.
type Resolution struct {
	Host    string `json:"host"`
	Pattern string `json:"pattern"` // "" = no route: default certificate / nginx default server
	Match   string `json:"match"`   // exact | wildcard | default
	Route   *Route `json:"route,omitempty"`
}

func upstreamAddr(u string) string {
	if strings.Contains(u, "://") {
		p, err := url.Parse(u)
		if err != nil {
			return ""
		}
		if p.Port() != "" {
			return p.Host
		}
		if p.Scheme == "https" {
			return net.JoinHostPort(p.Hostname(), "443")
		}
		return net.JoinHostPort(p.Hostname(), "80")
	}
	return u
}

// UpstreamPort returns the port of a loopback upstream (0 otherwise), used to
// find the Docker container that publishes it.
func UpstreamPort(u string) int {
	host, port, err := net.SplitHostPort(upstreamAddr(u))
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

func certFor(path string) *x509.Certificate {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return nil
		}
		if blk.Type == "CERTIFICATE" {
			c, err := x509.ParseCertificate(blk.Bytes)
			if err != nil {
				return nil
			}
			return c
		}
	}
}

func covers(c *x509.Certificate, host string) bool {
	if c == nil || time.Now().After(c.NotAfter) {
		return false
	}
	if strings.HasPrefix(host, "*.") {
		// A wildcard hostname needs a wildcard certificate for the same suffix.
		for _, n := range c.DNSNames {
			if strings.EqualFold(n, host) {
				return true
			}
		}
		return false
	}
	return c.VerifyHostname(host) == nil
}

// Routes lists every hostname with its owner, mode and certificate status.
func (sv *Service) Routes(ctx context.Context) ([]Route, error) {
	sites, err := sv.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Route
	exact := map[string]int64{}
	for _, s := range sites {
		for _, h := range s.Hosts() {
			if !strings.HasPrefix(h, "*.") {
				exact[h] = s.ID
			}
		}
	}
	for _, s := range sites {
		spec := sv.spec(s)
		var cert *x509.Certificate
		if spec.HasCert {
			cert = certFor(sv.Env.CertPath(s.Domain))
		}
		for i, h := range s.Hosts() {
			r := Route{
				Host: h, Wildcard: strings.HasPrefix(h, "*."), Primary: i == 0, SiteID: s.ID, Domain: s.Domain, Type: s.Type,
				Mode: "terminate", Upstream: spec.Upstream, Stopped: s.Stopped, ForceHTTPS: s.ForceHTTPS, HSTS: s.HSTS,
				CertIssuer: s.CertIssuer, CertExpiry: s.CertNotAfter, DNSManaged: s.DNSManaged, DNSProxied: s.DNSProxied,
				Warnings: []string{},
			}
			if s.Passthrough {
				r.Mode = "passthrough"
			} else {
				r.CertCovers = covers(cert, h)
			}
			if IsProject(s.Type) {
				r.Container = ProjectContainer(s.Domain)
			}
			if r.Wildcard {
				for e, id := range exact {
					if id != s.ID && strings.HasSuffix(e, h[1:]) {
						r.Overrides = append(r.Overrides, e)
					}
				}
				sort.Strings(r.Overrides)
			}
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return reversed(out[i].Host) < reversed(out[j].Host) })
	return out, nil
}

// reversed sorts hostnames by their labels from the right, so subdomains sit
// under their parent domain ("*.a.com" next to "a.com" and "x.a.com").
func reversed(h string) string {
	l := strings.Split(strings.TrimPrefix(h, "*."), ".")
	for i, j := 0, len(l)-1; i < j; i, j = i+1, j-1 {
		l[i], l[j] = l[j], l[i]
	}
	k := strings.Join(l, ".")
	if strings.HasPrefix(h, "*.") {
		k += ".*"
	}
	return k
}

// Check fills in upstream health, DNS and warnings (network calls, in parallel).
func (sv *Service) Check(ctx context.Context, routes []Route) {
	c, _ := sv.cfg.Get(ctx)
	ours := map[string]bool{}
	for _, ip := range []string{c.ServerIPv4, c.ServerIPv6} {
		if ip != "" {
			ours[ip] = true
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	health := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, r := range routes {
		if a := upstreamAddr(r.Upstream); a != "" {
			health[a] = ""
		}
	}
	for a := range health {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st := "down"
			if conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", a); err == nil {
				conn.Close()
				st = "up"
			}
			mu.Lock()
			health[a] = st
			mu.Unlock()
		}(a)
	}
	for i := range routes {
		if routes[i].Wildcard {
			continue
		}
		wg.Add(1)
		go func(r *Route) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			addrs, err := net.DefaultResolver.LookupHost(ctx, r.Host)
			if err != nil || len(addrs) == 0 {
				r.DNS = "none"
				return
			}
			sort.Strings(addrs)
			r.DNSAddrs, r.DNS = addrs, "other"
			for _, a := range addrs {
				if ours[a] {
					r.DNS = "ok"
				}
			}
		}(&routes[i])
	}
	wg.Wait()
	for i := range routes {
		r := &routes[i]
		r.Health = health[upstreamAddr(r.Upstream)]
		r.Warnings = routeWarnings(*r, len(ours) > 0)
	}
}

func routeWarnings(r Route, knowIPs bool) []string {
	w := []string{}
	if r.Stopped {
		return w
	}
	if r.Mode == "terminate" && !r.CertCovers {
		w = append(w, "route.no_cert")
	}
	if r.Mode == "passthrough" && r.DNSProxied {
		// Cloudflare's proxy ends TLS itself; the backend must hold a certificate Cloudflare accepts.
		w = append(w, "route.cf_passthrough")
	}
	if r.Health == "down" {
		w = append(w, "route.upstream_down")
	}
	if r.DNS == "none" {
		w = append(w, "route.dns_none")
	}
	if r.DNS == "other" && knowIPs && !r.DNSProxied {
		w = append(w, "route.dns_elsewhere")
	}
	if r.Wildcard && len(r.Overrides) > 0 {
		w = append(w, "route.wildcard_overridden")
	}
	return w
}

// Resolve applies the edge's matching rules to host.
func Resolve(routes []Route, host string) Resolution {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	pats := make([]string, len(routes))
	for i, r := range routes {
		pats[i] = r.Host
	}
	res := Resolution{Host: host, Match: "default"}
	if p := webenv.MatchHost(pats, host); p != "" {
		res.Pattern, res.Match = p, "exact"
		if strings.HasPrefix(p, "*.") {
			res.Match = "wildcard"
		}
		for i := range routes {
			if routes[i].Host == p {
				res.Route = &routes[i]
			}
		}
	}
	return res
}
