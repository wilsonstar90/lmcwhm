package netinfo

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// CloudflareRanges are Cloudflare's published proxy networks.
var CloudflareRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18", "108.162.192.0/18",
	"190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

// ProxyConfig controls which immediate peers may supply Cloudflare headers.
type ProxyConfig struct {
	CloudflareTunnel bool
	CloudflareRanges []string
}

// RequestAddress keeps the network peer separate from the claimed client.
type RequestAddress struct {
	Peer         string
	Client       string
	TrustedProxy bool
	ExternalTLS  bool
}

// PeerIP extracts and normalizes the socket peer.
func PeerIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap().String()
	}
	return host
}

func inRanges(ip netip.Addr, ranges []string) bool {
	for _, value := range ranges {
		if n, err := netip.ParsePrefix(value); err == nil && n.Contains(ip) {
			return true
		}
		if a, err := netip.ParseAddr(value); err == nil && a.Unmap() == ip {
			return true
		}
	}
	return false
}

// ResolveRequest trusts Cloudflare headers only from published Cloudflare
// networks, or from a local cloudflared peer when Tunnel mode is enabled.
func ResolveRequest(r *http.Request, cfg ProxyConfig) RequestAddress {
	peer := PeerIP(r.RemoteAddr)
	addr, err := netip.ParseAddr(peer)
	trusted := err == nil && (inRanges(addr, CloudflareRanges) || inRanges(addr, cfg.CloudflareRanges))
	if cfg.CloudflareTunnel && err == nil && addr.IsLoopback() {
		trusted = true
	}
	out := RequestAddress{Peer: peer, Client: peer, TrustedProxy: trusted, ExternalTLS: r.TLS != nil}
	if !trusted {
		return out
	}
	if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
		out.Client = ip.Unmap().String()
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		out.ExternalTLS = true
	}
	return out
}
