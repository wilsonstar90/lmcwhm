package webenv

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
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
	// Access log off (aaPanel "write access log"); on by default.
	NoAccessLog bool `json:"no_access_log"`
	// Password for the whole site, and for directories.
	BasicAuth *AuthUser `json:"basic_auth"`
	AuthDirs  []AuthDir `json:"auth_dirs"`
	// Traffic: concurrent connections for the whole site, and the
	// bandwidth of one response (KB/s).
	ServerConn  int `json:"server_conn"`
	LimitRateKB int `json:"limit_rate_kb"`
	// Hotlink protection: file extensions checked, and whether requests
	// without a Referer are refused too.
	HotlinkExt       []string `json:"hotlink_ext"`
	HotlinkDenyEmpty bool     `json:"hotlink_deny_empty"`
	// Reverse proxies for path prefixes of the site.
	Proxies []PathProxy `json:"proxies"`
}

type Redirect struct {
	From     string `json:"from"`      // exact path, e.g. /old
	To       string `json:"to"`        // path or URL
	Code     int    `json:"code"`      // 301 | 302 | 307 | 308
	KeepPath bool   `json:"keep_path"` // append the rest of the request URI
}

// AuthUser is a user name and its {SSHA} hash; Password is only accepted
// on input (hashed by Normalize, never stored or returned).
type AuthUser struct {
	User     string `json:"user"`
	Hash     string `json:"hash,omitempty"`
	Password string `json:"password,omitempty"`
}

type AuthDir struct {
	Name string `json:"name"`
	Path string `json:"path"` // e.g. /admin/
	AuthUser
}

// PathProxy forwards a path prefix to a URL.
type PathProxy struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Target  string `json:"target"` // http(s)://host[:port][/path]
	Host    string `json:"host"`   // Host header sent ("" = $host)
	Enabled bool   `json:"enabled"`
}

var DefaultHotlinkExt = []string{"jpg", "jpeg", "gif", "png", "webp", "svg", "ico", "mp4", "webm", "mp3", "pdf"}

var (
	ErrAdvanced = errors.New("site.invalid_advanced")
	uaRe        = regexp.MustCompile(`^[A-Za-z0-9 ._/()+-]{2,64}$`)
	refRe       = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9.-]{1,253}$`)
	pathRe      = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,=:@%/-]*$`)
	authUserRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
	sshaRe      = regexp.MustCompile(`^\{SSHA\}[A-Za-z0-9+/=]{20,64}$`)
	extRe       = regexp.MustCompile(`^[a-z0-9]{1,10}$`)
	proxyURLRe  = regexp.MustCompile(`^https?://[A-Za-z0-9.\-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~!$&'()*+,=:@%/-]*)?$|^https?://\[[0-9a-fA-F:]+\](:[0-9]{1,5})?(/[A-Za-z0-9._~%/-]*)?$`)
	proxyHostRe = regexp.MustCompile(`^[A-Za-z0-9.\-]{1,253}(:[0-9]{1,5})?$`)
	nameRe      = regexp.MustCompile(`^[^\x00-\x1f"'{};\\]{0,64}$`)
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
		if !pathRe.MatchString(r.From) || !targetRe.MatchString(r.To) || redirectCode(r.Code) != r.Code || seen[r.From] {
			return ErrAdvanced
		}
		seen[r.From] = true
	}
	if a.ServerConn < 0 || a.ServerConn > 1000000 || a.LimitRateKB < 0 || a.LimitRateKB > 10000000 ||
		len(a.AuthDirs) > 50 || len(a.Proxies) > 50 || len(a.HotlinkExt) > 50 {
		return ErrAdvanced
	}
	if a.BasicAuth != nil {
		if a.BasicAuth.User == "" {
			a.BasicAuth = nil
		} else if err := a.BasicAuth.normalize(); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for i := range a.AuthDirs {
		d := &a.AuthDirs[i]
		if !pathRe.MatchString(d.Path) || !nameRe.MatchString(d.Name) || seen[d.Path] {
			return ErrAdvanced
		}
		seen[d.Path] = true
		if err := d.AuthUser.normalize(); err != nil {
			return err
		}
	}
	ext := []string{}
	for _, e := range a.HotlinkExt {
		if e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), ".")); e == "" {
			continue
		}
		if !extRe.MatchString(e) {
			return ErrAdvanced
		}
		ext = append(ext, e)
	}
	a.HotlinkExt = ext
	seen = map[string]bool{}
	for i := range a.Proxies {
		p := &a.Proxies[i]
		p.Target = strings.TrimSpace(p.Target)
		p.Host = strings.TrimSpace(p.Host)
		if !pathRe.MatchString(p.Path) || !proxyURLRe.MatchString(p.Target) || p.Host != "" && !proxyHostRe.MatchString(p.Host) ||
			!nameRe.MatchString(p.Name) || seen[p.Path] {
			return ErrAdvanced
		}
		seen[p.Path] = true
	}
	if a.AuthDirs == nil {
		a.AuthDirs = []AuthDir{}
	}
	if a.Proxies == nil {
		a.Proxies = []PathProxy{}
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

// normalize hashes a new password, or keeps the stored hash.
func (u *AuthUser) normalize() error {
	if !authUserRe.MatchString(u.User) {
		return ErrAdvanced
	}
	if u.Password != "" {
		if len(u.Password) > 128 {
			return ErrAdvanced
		}
		u.Hash, u.Password = SSHA(u.Password), ""
	}
	if !sshaRe.MatchString(u.Hash) {
		return ErrAdvanced
	}
	return nil
}

// SSHA is the salted SHA-1 password format nginx's auth_basic reads natively.
func SSHA(pass string) string {
	salt := make([]byte, 8)
	rand.Read(salt)
	h := sha1.Sum(append([]byte(pass), salt...))
	return "{SSHA}" + base64.StdEncoding.EncodeToString(append(h[:], salt...))
}

// hotlinkExt is the extension alternation of the hotlink location.
func (a Advanced) hotlinkExt() string {
	if len(a.HotlinkExt) == 0 {
		return strings.Join(DefaultHotlinkExt, "|")
	}
	return strings.Join(a.HotlinkExt, "|")
}

// KeepHashes carries stored password hashes over to settings sent back by
// the browser (which never sees them) for the same user and path.
func (a *Advanced) KeepHashes(old Advanced) {
	if u := a.BasicAuth; u != nil && u.Hash == "" && u.Password == "" && old.BasicAuth != nil && old.BasicAuth.User == u.User {
		u.Hash = old.BasicAuth.Hash
	}
	for i := range a.AuthDirs {
		d := &a.AuthDirs[i]
		if d.Hash != "" || d.Password != "" {
			continue
		}
		for _, o := range old.AuthDirs {
			if o.Path == d.Path && o.User == d.User {
				d.Hash = o.Hash
			}
		}
	}
}

// Public drops the password hashes before settings are sent to the browser.
func (a Advanced) Public() Advanced {
	if a.BasicAuth != nil {
		u := *a.BasicAuth
		u.Hash = ""
		a.BasicAuth = &u
	}
	dirs := make([]AuthDir, len(a.AuthDirs))
	for i, d := range a.AuthDirs {
		d.Hash = ""
		dirs[i] = d
	}
	a.AuthDirs = dirs
	return a
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
