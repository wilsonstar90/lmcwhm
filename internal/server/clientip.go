package server

import (
	"net/http"

	"github.com/wilsonstar90/lmcwhm/internal/netinfo"
)

func (s *Server) requestAddress(r *http.Request) netinfo.RequestAddress {
	cfg := netinfo.ProxyConfig{}
	if s.Firewall != nil {
		c := s.Firewall.CDN(r.Context())
		cfg.CloudflareTunnel = c.CloudflareTunnel
		cfg.CloudflareRanges = s.Firewall.CloudflareTrust(r.Context())
	}
	return netinfo.ResolveRequest(r, cfg)
}

func (s *Server) clientIP(r *http.Request) string {
	return s.requestAddress(r).Client
}

func (s *Server) externalTLS(r *http.Request) bool {
	return s.requestAddress(r).ExternalTLS
}
