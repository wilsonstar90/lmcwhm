package netinfo

import (
	"net/http/httptest"
	"testing"
)

func TestResolveRequest(t *testing.T) {
	tests := []struct {
		name       string
		peer       string
		cfip       string
		proto      string
		cfg        ProxyConfig
		wantPeer   string
		wantClient string
		trusted    bool
		secure     bool
	}{
		{name: "direct ignores header", peer: "198.51.100.8:1234", cfip: "203.0.113.5", proto: "https", wantPeer: "198.51.100.8", wantClient: "198.51.100.8"},
		{name: "cloudflare", peer: "172.64.1.2:443", cfip: "203.0.113.5", proto: "https", wantPeer: "172.64.1.2", wantClient: "203.0.113.5", trusted: true, secure: true},
		{name: "extra cloudflare range", peer: "192.0.2.4:443", cfip: "2001:db8::5", cfg: ProxyConfig{CloudflareRanges: []string{"192.0.2.0/24"}}, wantPeer: "192.0.2.4", wantClient: "2001:db8::5", trusted: true},
		{name: "invalid header", peer: "172.64.1.2:443", cfip: "203.0.113.5, 192.0.2.1", wantPeer: "172.64.1.2", wantClient: "172.64.1.2", trusted: true},
		{name: "tunnel disabled", peer: "127.0.0.1:1234", cfip: "203.0.113.5", proto: "https", wantPeer: "127.0.0.1", wantClient: "127.0.0.1"},
		{name: "tunnel enabled", peer: "127.0.0.1:1234", cfip: "203.0.113.5", proto: "https", cfg: ProxyConfig{CloudflareTunnel: true}, wantPeer: "127.0.0.1", wantClient: "203.0.113.5", trusted: true, secure: true},
		{name: "mapped address", peer: "[::ffff:192.0.2.4]:1234", cfg: ProxyConfig{CloudflareRanges: []string{"192.0.2.4"}}, wantPeer: "192.0.2.4", wantClient: "192.0.2.4", trusted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://panel.example/", nil)
			r.TLS = nil
			r.RemoteAddr = tt.peer
			r.Header.Set("CF-Connecting-IP", tt.cfip)
			r.Header.Set("X-Forwarded-Proto", tt.proto)
			got := ResolveRequest(r, tt.cfg)
			if got.Peer != tt.wantPeer || got.Client != tt.wantClient || got.TrustedProxy != tt.trusted || got.ExternalTLS != tt.secure {
				t.Fatalf("got %+v, want peer=%s client=%s trusted=%v secure=%v", got, tt.wantPeer, tt.wantClient, tt.trusted, tt.secure)
			}
		})
	}
}
