package webenv

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"
)

// Advanced holds a website's optional nginx behaviour (網站進階設定).
type Advanced struct {
	Autoindex   bool       `json:"autoindex"`    // directory listing
	AllowIPs    []string   `json:"allow_ips"`    // when set, only these may connect
	DenyIPs     []string   `json:"deny_ips"`     // always refused
	DenyUA      []string   `json:"deny_ua"`      // case-insensitive user-agent substrings refused
	RateLimit   int        `json:"rate_limit"`   // requests per second per IP (0 = off)
	RateBurst   int        `json:"rate_burst"`   // extra requests allowed in a burst
	ConnLimit   int        `json:"conn_limit"`   // concurrent connections per IP (0 = off)
	ErrorPages  bool       `json:"error_pages"`  // serve /404.html and /50x.html from the root
	BlockXMLRPC bool       `json:"block_xmlrpc"` // WordPress xmlrpc.php
	OpenBasedir bool       `json:"open_basedir"` // PHP may only open files in the root and /tmp
	Hotlink     []string   `json:"hotlink"`      // referer domains allowed to embed media (empty = off)
	MaxBodyMB   int        `json:"max_body_mb"`  // upload limit (0 = default 128)
	Redirects   []Redirect `json:"redirects"`
	// Varnish page cache: TTL (seconds) for responses without their own
	// Cache-Control, and path prefixes never cached.
	Varnish        bool     `json:"varnish"`
	VarnishTTL     int      `json:"varnish_ttl"`
	VarnishExclude []string `json:"varnish_exclude"`
}

type Redirect struct {
	From string `json:"from"` // exact path, e.g. /old
	To   string `json:"to"`   // path or URL
	Code int    `json:"code"` // 301 | 302
}

var (
	ErrAdvanced = errors.New("site.invalid_advanced")
	uaRe        = regexp.MustCompile(`^[A-Za-z0-9 ._/()+-]{2,64}$`)
	refRe       = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9.-]{1,253}$`)
	pathRe      = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,=:@%/-]*$`)
	targetRe    = regexp.MustCompile(`^(/|https?://)[A-Za-z0-9._~!$&'()*+,=:@%/?#\[\]-]*$`)
)

func normIPs(in []string) ([]string, error) {
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked().String())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, ErrAdvanced
		}
		out = append(out, a.String())
	}
	return out, nil
}

// Normalize validates every field so it can be rendered into nginx safely.
func (a *Advanced) Normalize() error {
	var err error
	if a.AllowIPs, err = normIPs(a.AllowIPs); err != nil {
		return err
	}
	if a.DenyIPs, err = normIPs(a.DenyIPs); err != nil {
		return err
	}
	ua := []string{}
	for _, u := range a.DenyUA {
		if u = strings.TrimSpace(u); u == "" {
			continue
		}
		if !uaRe.MatchString(u) {
			return ErrAdvanced
		}
		ua = append(ua, u)
	}
	a.DenyUA = ua
	ref := []string{}
	for _, h := range a.Hotlink {
		if h = strings.ToLower(strings.TrimSpace(h)); h == "" {
			continue
		}
		if !refRe.MatchString(h) {
			return ErrAdvanced
		}
		ref = append(ref, h)
	}
	a.Hotlink = ref
	if a.VarnishTTL < 0 || a.VarnishTTL > 30*86400 || len(a.VarnishExclude) > 100 {
		return ErrAdvanced
	}
	ex := []string{}
	for _, p := range a.VarnishExclude {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if !vclPathRe.MatchString(p) {
			return ErrAdvanced
		}
		ex = append(ex, p)
	}
	a.VarnishExclude = ex
	if a.RateLimit < 0 || a.RateLimit > 10000 || a.RateBurst < 0 || a.RateBurst > 100000 || a.ConnLimit < 0 || a.ConnLimit > 100000 ||
		a.MaxBodyMB < 0 || a.MaxBodyMB > 10240 || len(a.Redirects) > 200 || len(a.AllowIPs)+len(a.DenyIPs) > 1000 {
		return ErrAdvanced
	}
	seen := map[string]bool{}
	for i := range a.Redirects {
		r := &a.Redirects[i]
		if r.Code == 0 {
			r.Code = 301
		}
		if !pathRe.MatchString(r.From) || !targetRe.MatchString(r.To) || r.Code != 301 && r.Code != 302 || seen[r.From] {
			return ErrAdvanced
		}
		seen[r.From] = true
	}
	if a.AllowIPs == nil {
		a.AllowIPs = []string{}
	}
	if a.DenyIPs == nil {
		a.DenyIPs = []string{}
	}
	if a.Redirects == nil {
		a.Redirects = []Redirect{}
	}
	return nil
}

// zone names an nginx shared memory zone after the site.
func zone(prefix, domain string) string {
	return prefix + "_" + strings.NewReplacer(".", "_", "-", "_", "*", "w").Replace(domain)
}

// uaRegex is the case-insensitive alternation of the refused user agents.
func uaRegex(list []string) string {
	q := make([]string, len(list))
	for i, u := range list {
		q[i] = regexp.QuoteMeta(u)
	}
	return strings.Join(q, "|")
}
