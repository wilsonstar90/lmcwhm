package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/settings"
)

// Risk is one finding of the dashboard's security check; the frontend
// translates the code and links to the page that fixes it.
type Risk struct {
	Code  string `json:"code"`
	Level string `json:"level"` // critical | warning | info
	Value string `json:"value,omitempty"`
}

type summary struct {
	Sites        int    `json:"sites"`
	SitesStopped int    `json:"sites_stopped"`
	SSLExpiring  int    `json:"ssl_expiring"` // < 14 days
	Databases    int    `json:"databases"`
	Containers   int    `json:"containers"`
	Running      int    `json:"running"`
	CronJobs     int    `json:"cron_jobs"`
	CronFailed   int    `json:"cron_failed"` // jobs whose last run failed
	Processes    int    `json:"processes"`
	ProcsDown    int    `json:"procs_down"`
	Probes       int    `json:"probes"`
	ProbesDown   int    `json:"probes_down"`
	Backups      int    `json:"backups"`
	LastBackup   int64  `json:"last_backup"`
	PV           int64  `json:"pv"` // today, all sites
	UV           int64  `json:"uv"`
	Bandwidth    int64  `json:"bandwidth"`
	Blocked      int    `json:"blocked_ips"`
	Alerts24h    int    `json:"alerts_24h"`
	Risks        []Risk `json:"risks"`
	Score        int    `json:"score"` // 0–100
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.summary(r.Context()))
}

func (s *Server) summary(ctx context.Context) summary {
	out := summary{Risks: []Risk{}}
	now := time.Now()
	if s.Sites != nil {
		if list, err := s.Sites.List(ctx); err == nil {
			out.Sites = len(list)
			for _, x := range list {
				if x.Stopped {
					out.SitesStopped++
				}
				if x.SSLMode != "none" && x.CertNotAfter > 0 && time.Unix(x.CertNotAfter, 0).Sub(now) < 14*24*time.Hour {
					out.SSLExpiring++
				}
			}
		}
	}
	if s.DBs != nil {
		if list, err := s.DBs.List(ctx); err == nil {
			out.Databases = len(list)
		}
	}
	if s.Docker != nil {
		if cs, err := s.Docker.Containers(ctx, false); err == nil {
			out.Containers = len(cs)
			for _, c := range cs {
				if c.State == "running" {
					out.Running++
				}
			}
		}
	}
	if s.Cron != nil {
		if jobs, err := s.Cron.List(ctx); err == nil {
			out.CronJobs = len(jobs)
			for _, j := range jobs {
				if j.Enabled && j.Last != nil && (j.Last.Status == "failed" || j.Last.Status == "timeout") {
					out.CronFailed++
				}
			}
		}
	}
	if s.Procs != nil {
		if ps, err := s.Procs.List(ctx); err == nil {
			out.Processes = len(ps)
			for _, p := range ps {
				if p.State != "active" {
					out.ProcsDown++
				}
			}
		}
	}
	if s.Probes != nil {
		if ps, err := s.Probes.List(ctx); err == nil {
			out.Probes = len(ps)
			for _, p := range ps {
				if p.Enabled && p.State == "down" {
					out.ProbesDown++
				}
			}
		}
	}
	if s.Backup != nil {
		if recs, err := s.Backup.List(ctx, ""); err == nil {
			out.Backups = len(recs)
			if len(recs) > 0 {
				out.LastBackup = recs[0].CreatedAt
			}
		}
	}
	if s.Stats != nil {
		if ov, err := s.Stats.Overview(ctx); err == nil {
			for _, p := range ov {
				out.PV += p.PV
				out.UV += p.UV
				out.Bandwidth += p.Bytes
			}
		}
	}
	if s.Firewall != nil {
		if ips, err := s.Firewall.IPs(ctx, "block"); err == nil {
			out.Blocked = len(ips)
		}
	}
	if s.Alerts != nil {
		if evs, _, err := s.Alerts.Events(ctx, 500, 0); err == nil {
			for _, e := range evs {
				if e.CreatedAt > now.Add(-24*time.Hour).Unix() && e.Severity != "resolved" {
					out.Alerts24h++
				}
			}
		}
	}
	out.Risks = s.risks(ctx, out)
	out.Score = 100
	for _, r := range out.Risks {
		switch r.Level {
		case "critical":
			out.Score -= 20
		case "warning":
			out.Score -= 8
		}
	}
	out.Score = max(out.Score, 0)
	return out
}

// risks is the baseline security check (寶塔首頁風險).
func (s *Server) risks(ctx context.Context, sum summary) []Risk {
	var out []Risk
	add := func(code, level, value string) { out = append(out, Risk{code, level, value}) }
	cfg := s.Settings.Get()
	if cfg.SecurityEntry == "" {
		add("no_entry", "warning", "")
	}
	if cfg.PanelPort == settings.Defaults().PanelPort {
		add("default_port", "warning", "")
	}
	if cfg.SSLMode == settings.SSLOff {
		add("panel_http", "critical", "")
	}
	if len(cfg.AllowedIPs) == 0 && cfg.BasicAuthUser == "" {
		add("panel_open", "info", "")
	}
	if u, err := s.Auth.PrimaryAdmin(ctx); err == nil && !u.TOTPEnabled {
		add("no_2fa", "warning", "")
	}
	if s.Firewall != nil {
		if c, err := s.Firewall.Config(ctx); err == nil && !c.Enabled {
			add("firewall_off", "warning", "")
		}
	}
	if s.WAF != nil && !s.WAF.Settings().Enabled && sum.Sites > 0 {
		add("waf_off", "info", "")
	}
	if s.CrowdSec != nil && !s.CrowdSec.Status(ctx).Installed {
		add("crowdsec_off", "info", "")
	}
	if sum.Sites+sum.Databases > 0 && (sum.LastBackup == 0 || time.Since(time.Unix(sum.LastBackup, 0)) > 7*24*time.Hour) {
		add("no_recent_backup", "warning", "")
	}
	if s.Alerts != nil {
		if chans, err := s.Alerts.Channels(ctx); err == nil && len(chans) == 0 {
			add("no_alert_channel", "info", "")
		}
	}
	if sum.SSLExpiring > 0 {
		add("ssl_expiring", "warning", itoa(sum.SSLExpiring))
	}
	if sum.ProbesDown > 0 {
		add("probes_down", "critical", itoa(sum.ProbesDown))
	}
	if sum.CronFailed > 0 {
		add("cron_failed", "warning", itoa(sum.CronFailed))
	}
	if sum.ProcsDown > 0 {
		add("procs_down", "warning", itoa(sum.ProcsDown))
	}
	if out == nil {
		out = []Risk{}
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }
