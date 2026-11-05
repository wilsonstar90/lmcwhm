package firewall

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sort"
	"strings"
)

// Precedence of the address lists, highest first:
//
//  1. Manual entries (added in the panel). A manual block is never exempted
//     by a trusted range; where a manual block and a manual allow overlap the
//     narrower one wins (allow 1.2.3.0/24 + block 1.2.3.4 blocks 1.2.3.4).
//  2. The server's own addresses (every interface, loopback, container
//     bridges): always allowed, never banned.
//  3. Trusted ranges (Cloudflare, crawlers): never banned automatically.
//  4. Automatic blocks (CrowdSec, lists, MCP, auto-ban), skipped wherever a
//     manual allow or the server's own address covers them.

var (
	ErrInAllow    = errors.New("fw.in_allow")    // a manual block of an address the allow list names
	ErrInBlock    = errors.New("fw.in_block")    // a manual allow of an address the block list names
	ErrSelfServer = errors.New("fw.self_server") // blocking the server's own address
	ErrAllowed    = errors.New("fw.allowed")     // an automatic block of an allowed address: skipped
)

// SelfPrefixes are the server's own addresses: each interface address, and
// the whole network of loopback and container bridges (Docker containers
// reach the host from there).
func SelfPrefixes() []netip.Prefix {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Prefix
	for _, ifc := range ifs {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		bridge := ifc.Flags&net.FlagLoopback != 0 || ifc.Name == "docker0" || strings.HasPrefix(ifc.Name, "br-")
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			addr, ok := netip.AddrFromSlice(n.IP)
			if !ok || addr.IsLinkLocalUnicast() {
				continue
			}
			addr = addr.Unmap()
			if bridge {
				ones, _ := n.Mask.Size()
				if addr.Is4() && ones > 32 {
					ones -= 96
				}
				out = append(out, netip.PrefixFrom(addr, ones).Masked())
			} else {
				out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
			}
		}
	}
	return out
}

// SystemAllow lists the server's own addresses for display, loopback last.
func (sv *Service) SystemAllow() []string {
	var pub, local []string
	for _, p := range sv.self() {
		s := p.String()
		if p.IsSingleIP() {
			s = p.Addr().String()
		}
		if p.Addr().IsLoopback() || p.Addr().IsPrivate() {
			local = append(local, s)
		} else {
			pub = append(pub, s)
		}
	}
	sort.Strings(pub)
	sort.Strings(local)
	return append(pub, local...)
}

func (sv *Service) self() []netip.Prefix {
	if sv.Self != nil {
		return sv.Self()
	}
	return SelfPrefixes()
}

// covers reports whether a contains all of b.
func covers(a, b netip.Prefix) bool {
	return a.Addr().Is4() == b.Addr().Is4() && a.Bits() <= b.Bits() && a.Contains(b.Addr())
}

func coveredBy(p netip.Prefix, list []netip.Prefix) bool {
	for _, a := range list {
		if covers(a, p) {
			return true
		}
	}
	return false
}

// ipSets is every entry sorted by what it does.
type ipSets struct {
	allow  []IP // manual allow
	self   []netip.Prefix
	over   []IP // manual blocks narrower than a manual allow: matched before the allow list
	mblock []IP // manual blocks (no trusted-range exemption)
	block  []IP // automatic blocks not covered by an allow
}

func (sv *Service) classify(ips []IP) ipSets {
	s := ipSets{self: sv.self()}
	var allowP []netip.Prefix
	for _, x := range ips {
		if x.List == "allow" {
			if p, _, err := NormIP(x.IP); err == nil {
				s.allow = append(s.allow, x)
				allowP = append(allowP, p)
			}
		}
	}
	for _, x := range ips {
		if x.List != "block" {
			continue
		}
		p, _, err := NormIP(x.IP)
		if err != nil {
			continue
		}
		if x.Source == "manual" {
			narrower := false
			for _, a := range allowP {
				if covers(a, p) && a.Bits() < p.Bits() {
					narrower = true
					break
				}
			}
			if narrower {
				s.over = append(s.over, x)
			} else {
				s.mblock = append(s.mblock, x)
			}
			continue
		}
		if coveredBy(p, allowP) || coveredBy(p, s.self) {
			continue // an allowed address is never banned automatically
		}
		s.block = append(s.block, x)
	}
	return s
}

// Allowed reports whether ip is covered by a manual allow entry or is one of
// the server's own addresses (and so never blocked automatically).
func (sv *Service) Allowed(ctx context.Context, ip string) bool {
	p, _, err := NormIP(ip)
	if err != nil {
		return false
	}
	if coveredBy(p, sv.self()) {
		return true
	}
	ips, err := sv.IPs(ctx, "allow")
	if err != nil {
		return false
	}
	for _, x := range ips {
		if a, _, err := NormIP(x.IP); err == nil && covers(a, p) {
			return true
		}
	}
	return false
}

// AllowPrefixes are the manual allow entries and the server's own addresses
// (for CrowdSec's whitelist).
func (sv *Service) AllowPrefixes(ctx context.Context) []string {
	out := []string{}
	if ips, err := sv.IPs(ctx, "allow"); err == nil {
		for _, x := range ips {
			out = append(out, x.IP)
		}
	}
	for _, p := range sv.self() {
		if p.IsSingleIP() {
			out = append(out, p.Addr().String())
		} else {
			out = append(out, p.String())
		}
	}
	sort.Strings(out)
	return out
}

// checkManual refuses a manual entry that contradicts the other list for
// the same address, or that blocks the server itself.
func (sv *Service) checkManual(ctx context.Context, p netip.Prefix, norm, list string) error {
	if list == "block" && p.IsSingleIP() {
		for _, s := range sv.self() {
			if s.Contains(p.Addr()) {
				return ErrSelfServer
			}
		}
	}
	other := "allow"
	if list == "allow" {
		other = "block"
	}
	var n int
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM fw_ips WHERE ip = ? AND list = ? AND source = 'manual'`, norm, other).Scan(&n)
	if n > 0 {
		if list == "block" {
			return ErrInAllow
		}
		return ErrInBlock
	}
	return nil
}

// insideAllow reports whether p is narrower than a manual allow entry.
func (sv *Service) insideAllow(ctx context.Context, p netip.Prefix) bool {
	ips, err := sv.IPs(ctx, "allow")
	if err != nil {
		return false
	}
	for _, x := range ips {
		if a, _, err := NormIP(x.IP); err == nil && covers(a, p) && a.Bits() < p.Bits() {
			return true
		}
	}
	return false
}

// manualConflicts is checkManual for many entries: it reads the other list
// once.
func (sv *Service) manualConflicts(ctx context.Context, list string) func(netip.Prefix, string) bool {
	other := "allow"
	if list == "allow" {
		other = "block"
	}
	names := map[string]bool{}
	if rows, err := sv.st.DB.QueryContext(ctx, `SELECT ip FROM fw_ips WHERE list = ? AND source = 'manual'`, other); err == nil {
		for rows.Next() {
			var ip string
			rows.Scan(&ip)
			names[ip] = true
		}
		rows.Close()
	}
	self := sv.self()
	return func(p netip.Prefix, norm string) bool {
		if names[norm] {
			return true
		}
		if list == "block" && p.IsSingleIP() {
			for _, s := range self {
				if s.Contains(p.Addr()) {
					return true
				}
			}
		}
		return false
	}
}

// Trusted reports whether ip is in a trusted range (Cloudflare, crawlers).
func (sv *Service) Trusted(ctx context.Context, ip string) bool {
	p, _, err := NormIP(ip)
	if err != nil {
		return false
	}
	ips, err := sv.IPs(ctx, "trust")
	if err != nil {
		return false
	}
	for _, x := range ips {
		if a, _, err := NormIP(x.IP); err == nil && covers(a, p) {
			return true
		}
	}
	return false
}
