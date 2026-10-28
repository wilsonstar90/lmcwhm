package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/firewall"
	"github.com/wilsonstar90/lmcwhm/internal/sitestats"
)

// The security page's overview: one tile per module with its state and a
// key number (details load when a module is opened).

type secOverview struct {
	Firewall struct {
		Available bool   `json:"available"`
		Enabled   bool   `json:"enabled"`
		Policy    string `json:"policy"`
		Rules     int    `json:"rules"`
		Pending   bool   `json:"pending"`
	} `json:"firewall"`
	IPs struct {
		Block int `json:"block"` // manual, permanent
		Allow int `json:"allow"`
		Bans  int `json:"bans"` // temporary blocks of any source except lists
	} `json:"ips"`
	Lists struct {
		Trusted  int `json:"trusted"`
		Blocking int `json:"blocking"`
		Enabled  int `json:"enabled"`
	} `json:"lists"`
	Cloud struct {
		Linode   bool  `json:"linode"` // token and firewall chosen
		CF       bool  `json:"cf"`
		Syncing  bool  `json:"syncing"` // any sync switched on
		LastSync int64 `json:"last_sync"`
		Errors   int   `json:"errors"`
	} `json:"cloud"`
	WAF struct {
		Enabled bool `json:"enabled"`
		Blocked int  `json:"blocked"` // in the last 24 hours (of the recent events)
	} `json:"waf"`
	CrowdSec struct {
		Installed bool   `json:"installed"`
		State     string `json:"state"`
		Bans      int    `json:"bans"`
	} `json:"crowdsec"`
	Scan struct {
		Score int   `json:"score"`
		At    int64 `json:"at"`
	} `json:"scan"`
	Malware struct {
		At       int64  `json:"at"`
		State    string `json:"state"`
		Findings int    `json:"findings"`
		Files    int    `json:"files"`
	} `json:"malware"`
	Tamper struct {
		Rules   int `json:"rules"`
		Enabled int `json:"enabled"`
		Events  int `json:"events"` // in the last 24 hours
	} `json:"tamper"`
}

func (s *Server) securityOverview(ctx context.Context) secOverview {
	var o secOverview
	day := time.Now().Add(-24 * time.Hour).Unix()
	if s.Firewall != nil {
		fw := s.Firewall
		c, _ := fw.Config(ctx)
		o.Firewall.Available, o.Firewall.Enabled, o.Firewall.Policy, o.Firewall.Pending = fw.Available(), c.Enabled, c.Policy, fw.Pending()
		if rules, err := fw.Rules(ctx); err == nil {
			o.Firewall.Rules = len(rules)
		}
		b, _ := fw.ManualIPs(ctx, "block")
		a, _ := fw.ManualIPs(ctx, "allow")
		o.IPs.Block, o.IPs.Allow = len(b), len(a)
		if ips, err := fw.IPs(ctx, "block"); err == nil {
			for _, x := range ips {
				if x.ExpiresAt > 0 && !isFeed(x) {
					o.IPs.Bans++
				}
			}
		}
		f := fw.Feeds(ctx)
		o.Lists.Trusted, o.Lists.Blocking, o.Lists.Enabled = f.Trusted, f.Blocking, len(f.Enabled)
		if s.Web != nil {
			wc, _ := s.Web.Get(ctx)
			o.Cloud.CF = wc.CFToken != ""
		}
		o.Cloud.Linode = c.LinodeToken != "" && c.LinodeFirewall > 0
		o.Cloud.Syncing = c.LinodeSync || c.LinodePorts || c.CFSync
		rep := firewall.LastSync()
		o.Cloud.LastSync, o.Cloud.Errors = rep.At, len(rep.Errors)
	}
	if s.WAF != nil {
		o.WAF.Enabled = s.WAF.Settings().Enabled
		if evs, err := s.WAF.Events(500, ""); err == nil {
			for _, e := range evs {
				if e.Blocked && e.Time > day {
					o.WAF.Blocked++
				}
			}
		}
	}
	if s.CrowdSec != nil {
		st := s.CrowdSec.Status(ctx)
		o.CrowdSec.Installed, o.CrowdSec.State, o.CrowdSec.Bans = st.Installed, st.State, st.Bans
	}
	if s.SecScan != nil {
		if r, ok := s.SecScan.Latest(ctx); ok {
			o.Scan.Score, o.Scan.At = r.Score, r.CreatedAt
		}
	}
	if s.Malware != nil {
		if list, err := s.Malware.List(ctx); err == nil && len(list) > 0 {
			m := list[0]
			o.Malware.At, o.Malware.State, o.Malware.Findings, o.Malware.Files = m.StartedAt, m.State, len(m.Findings), m.Files
		}
	}
	if s.Tamper != nil {
		if rules, err := s.Tamper.List(ctx); err == nil {
			o.Tamper.Rules = len(rules)
			for _, r := range rules {
				if r.Enabled {
					o.Tamper.Enabled++
				}
			}
		}
		if evs, err := s.Tamper.Events(ctx, 0, 500); err == nil {
			for _, e := range evs {
				if e.CreatedAt > day {
					o.Tamper.Events++
				}
			}
		}
	}
	return o
}

func isFeed(x firewall.IP) bool { return strings.HasPrefix(x.Source, "feed:") }

func (s *Server) handleSecurityOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.securityOverview(r.Context()))
}

// ---- traffic overview ----

// handleSecurityTraffic is the day's traffic across every website (?day=1
// for yesterday); logs are read in first, at most every 30 seconds.
func (s *Server) handleSecurityTraffic(w http.ResponseWriter, r *http.Request) {
	if s.Stats == nil {
		writeJSON(w, http.StatusOK, sitestats.Traffic{})
		return
	}
	s.freshStats(r.Context())
	var country func(string) string
	if s.GeoIP != nil {
		country = func(ip string) string { return s.GeoIP.Lookup(ip).Country }
	}
	writeJSON(w, http.StatusOK, s.Stats.Traffic(r.Context(), intParam(r, "day", 0, 1), country))
}

// handleSecurityLive is the last minute of requests and bandwidth.
func (s *Server) handleSecurityLive(w http.ResponseWriter, r *http.Request) {
	if s.Stats == nil || s.Stats.Sites == nil {
		writeJSON(w, http.StatusOK, sitestats.Live{})
		return
	}
	writeJSON(w, http.StatusOK, s.Stats.Live(r.Context(), s.Stats.Sites(r.Context())))
}

var statsMu sync.Mutex
var statsAt time.Time

func (s *Server) freshStats(ctx context.Context) {
	statsMu.Lock()
	defer statsMu.Unlock()
	if time.Since(statsAt) < 30*time.Second {
		return
	}
	statsAt = time.Now()
	s.Stats.IngestAll(ctx, func(string, ...any) {})
}
