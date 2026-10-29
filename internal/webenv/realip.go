package webenv

// Visitor addresses behind a CDN. With Cloudflare (or another CDN) in front,
// the TCP source HAProxy sees is the CDN edge, so logs, the WAF, CrowdSec,
// rate limits and IP rules would all act on the CDN. HAProxy replaces the
// source with the address in the CDN's header, but only for connections that
// come from the CDN's own ranges (anyone else could send the header), and
// then refuses blocked visitors by that address. The block / allow lists are
// changed at runtime over the admin socket, without a reload.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// CloudflareRanges are Cloudflare's published edge ranges
// (https://www.cloudflare.com/ips/); they rarely change. The Cloudflare
// trust list, when enabled, adds anything newer.
var CloudflareRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18", "108.162.192.0/18",
	"190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

// CDN describes the proxies whose header carries the visitor's address.
type CDN struct {
	Cloudflare []string // extra Cloudflare ranges (from the trust list)
	Ranges     []string // another CDN's ranges
	Header     string   // and the header it sets (last value is used)
}

var headerRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// ValidHeader reports whether h can name the custom CDN's address header.
func ValidHeader(h string) bool { return headerRe.MatchString(h) }

func (e *Env) cdn() CDN {
	if e.CDN == nil {
		return CDN{}
	}
	return e.CDN()
}

// cdnLists renders the two range files of the CDN configuration.
func cdnLists(c CDN) (cf, custom []byte) {
	return rangeList(append(slices.Clone(CloudflareRanges), c.Cloudflare...)), rangeList(c.Ranges)
}

func rangeList(v []string) []byte {
	seen := map[string]bool{}
	var b bytes.Buffer
	for _, s := range v {
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			a, err2 := netip.ParseAddr(strings.TrimSpace(s))
			if err2 != nil {
				continue
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if k := p.Masked().String(); !seen[k] {
			seen[k] = true
			b.WriteString(k + "\n")
		}
	}
	return b.Bytes()
}

// ---- block / allow lists ----

const (
	blockList = "block.lst"
	allowList = "allow.lst"
)

func (e *Env) listsDir() string  { return filepath.Join(e.EdgeDir(), "lists") }
func (e *Env) adminSock() string { return filepath.Join(e.EdgeDir(), "run", "admin.sock") }

// ensureLists creates empty list files so the config always loads.
func (e *Env) ensureLists() error {
	if err := os.MkdirAll(e.listsDir(), 0o755); err != nil {
		return err
	}
	for _, n := range []string{blockList, allowList} {
		p := filepath.Join(e.listsDir(), n)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if err := os.WriteFile(p, nil, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

var listsMu sync.Mutex

// SetLists stores the refused (block) and never-refused (allow) addresses and
// pushes the difference to the running HAProxy.
func (e *Env) SetLists(ctx context.Context, block, allow []string) error {
	listsMu.Lock()
	defer listsMu.Unlock()
	if err := e.ensureLists(); err != nil {
		return err
	}
	var errs []string
	for _, l := range []struct {
		name string
		v    []string
	}{{blockList, block}, {allowList, allow}} {
		path := filepath.Join(e.listsDir(), l.name)
		old := readList(path)
		next := strings.Join(l.v, "\n")
		if len(l.v) > 0 {
			next += "\n"
		}
		if err := writeIfChanged(path, []byte(next)); err != nil {
			return err
		}
		if _, err := os.Stat(e.adminSock()); err != nil {
			continue // not running: the file is read at start
		}
		if err := e.pushList(ctx, path, old, l.v); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("haproxy lists: %s", strings.Join(errs, "; "))
	}
	return nil
}

func readList(path string) map[string]bool {
	m := map[string]bool{}
	b, _ := os.ReadFile(path)
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			m[l] = true
		}
	}
	return m
}

// pushList changes the loaded ACL: small changes entry by entry, larger ones
// as a new version that replaces the whole list atomically.
func (e *Env) pushList(ctx context.Context, path string, old map[string]bool, next []string) error {
	nextSet := make(map[string]bool, len(next))
	var add, del []string
	for _, x := range next {
		nextSet[x] = true
		if !old[x] {
			add = append(add, x)
		}
	}
	for x := range old {
		if !nextSet[x] {
			del = append(del, x)
		}
	}
	if len(add)+len(del) == 0 {
		return nil
	}
	if len(add)+len(del) <= 500 {
		cmds := make([]string, 0, len(add)+len(del))
		for _, x := range add {
			cmds = append(cmds, "add acl "+path+" "+x)
		}
		for _, x := range del {
			cmds = append(cmds, "del acl "+path+" "+x)
		}
		_, err := e.runtime(ctx, cmds)
		return err
	}
	out, err := e.runtime(ctx, []string{"prepare acl " + path})
	if err != nil {
		return err
	}
	ver := ""
	for _, a := range out {
		if a = strings.TrimSpace(a); strings.HasPrefix(a, "New version created:") {
			ver = strings.TrimSpace(strings.TrimPrefix(a, "New version created:"))
		}
	}
	if ver == "" || strings.ContainsAny(ver, " \t") {
		return fmt.Errorf("prepare acl: %q", out)
	}
	cmds := make([]string, 0, len(next)+1)
	for _, x := range next {
		cmds = append(cmds, "add acl @"+ver+" "+path+" "+x)
	}
	cmds = append(cmds, "commit acl @"+ver+" "+path)
	_, err = e.runtime(ctx, cmds)
	return err
}

// runtime sends commands over the admin socket in prompt mode and returns
// each answer; an answer that is not empty and not a version line is an error.
func (e *Env) runtime(ctx context.Context, cmds []string) ([]string, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "unix", e.adminSock())
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Minute))
	w := bufio.NewWriter(c)
	w.WriteString("prompt\n")
	go func() {
		for _, cmd := range cmds {
			w.WriteString(cmd + "\n")
		}
		w.WriteString("quit\n")
		w.Flush()
	}()
	// Each answer ends with an empty line followed by the "> " prompt.
	r := bufio.NewReader(c)
	var out []string
	var cur strings.Builder
	for {
		line, err := r.ReadString('\n')
		line = strings.TrimPrefix(line, "> ")
		if strings.TrimSpace(line) == "" && err == nil {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteString(line)
		if err != nil {
			break
		}
	}
	var bad []string
	for _, a := range out {
		if a = strings.TrimSpace(a); a != "" && !strings.HasPrefix(a, "New version created:") {
			bad = append(bad, a)
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	if len(bad) > 0 {
		return out, fmt.Errorf("haproxy: %s", bad[0])
	}
	return out, nil
}
