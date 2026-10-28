package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/alert"
	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/firewall"
	"github.com/wilsonstar90/lmcwhm/internal/ftp"
	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
	"github.com/wilsonstar90/lmcwhm/internal/probe"
	"github.com/wilsonstar90/lmcwhm/internal/site"
	"github.com/wilsonstar90/lmcwhm/internal/supervisor"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
	"github.com/wilsonstar90/lmcwhm/internal/wordpress"
)

func (s *Server) m8Routes(r chi.Router) {
	if s.Alerts != nil {
		r.Get("/alerts/channels", s.handleAlertChannels)
		r.Post("/alerts/channels", s.handleAlertChannelSave)
		r.Put("/alerts/channels/{id}", s.handleAlertChannelSave)
		r.Post("/alerts/channels/{id}/delete", s.handleAlertChannelDelete)
		r.Post("/alerts/channels/{id}/test", s.handleAlertChannelTest)
		r.Get("/alerts/rules", s.handleAlertRules)
		r.Post("/alerts/rules", s.handleAlertRuleSave)
		r.Put("/alerts/rules/{id}", s.handleAlertRuleSave)
		r.Post("/alerts/rules/{id}/delete", s.handleAlertRuleDelete)
		r.Get("/alerts/events", s.handleAlertEvents)
	}
	if s.Probes != nil {
		r.Get("/probes", s.handleProbes)
		r.Post("/probes", s.handleProbeSave)
		r.Put("/probes/{id}", s.handleProbeSave)
		r.Post("/probes/{id}/delete", s.handleProbeDelete)
		r.Post("/probes/{id}/check", s.handleProbeCheck)
		r.Get("/probes/{id}/results", s.handleProbeResults)
	}
	if s.Stats != nil {
		r.Get("/sitestats", s.handleSiteStats)
		r.Get("/sitestats/overview", s.handleSiteStatsOverview)
	}
	if s.Procs != nil {
		r.Get("/processes", s.handleProcs)
		r.Post("/processes", s.handleProcSave)
		r.Put("/processes/{id}", s.handleProcSave)
		r.Post("/processes/{id}/delete", s.handleProcDelete)
		r.Post("/processes/{id}/{action:start|stop|restart}", s.handleProcAction)
		r.Get("/processes/{id}/logs", s.handleProcLogs)
	}
	if s.WP != nil {
		r.Get("/wordpress", s.handleWPList)
		r.Post("/wordpress/install/{id}", s.handleWPInstall)
		r.Get("/wordpress/{id}", s.handleWPInfo)
		r.Post("/wordpress/{id}/action", s.handleWPAction)
	}
	if s.Sites != nil {
		r.Post("/sites/{id}/composer", s.handleComposer)
	}
	s.ftpRoutes(r)
}

// ---- alerts ----

func (s *Server) handleAlertChannels(w http.ResponseWriter, r *http.Request) {
	list, err := s.Alerts.Channels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleAlertChannelSave(w http.ResponseWriter, r *http.Request) {
	var c alert.Channel
	if err := decode(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c.ID = idParam(r)
	c, err := s.Alerts.SaveChannel(r.Context(), c)
	if err != nil {
		writeCodedErr(w, err, alert.ErrNotFound)
		return
	}
	s.auditCat(r, "alert", "alert.channel_save", c.Kind+" "+c.Name)
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleAlertChannelDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Alerts.DeleteChannel(r.Context(), idParam(r)); err != nil {
		writeCodedErr(w, err, alert.ErrNotFound)
		return
	}
	s.auditCat(r, "alert", "alert.channel_delete", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAlertChannelTest(w http.ResponseWriter, r *http.Request) {
	if err := s.Alerts.TestChannel(r.Context(), idParam(r)); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAlertRules(w http.ResponseWriter, r *http.Request) {
	list, err := s.Alerts.Rules(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleAlertRuleSave(w http.ResponseWriter, r *http.Request) {
	var ru alert.Rule
	if err := decode(r, &ru); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ru.ID = idParam(r)
	ru, err := s.Alerts.SaveRule(r.Context(), ru)
	if err != nil {
		writeCodedErr(w, err, alert.ErrNotFound)
		return
	}
	s.auditCat(r, "alert", "alert.rule_save", ru.Kind+" "+ru.Name)
	writeJSON(w, http.StatusOK, ru)
}

func (s *Server) handleAlertRuleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Alerts.DeleteRule(r.Context(), idParam(r)); err != nil {
		writeCodedErr(w, err, alert.ErrNotFound)
		return
	}
	s.auditCat(r, "alert", "alert.rule_delete", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAlertEvents(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 50, 500)
	list, total, err := s.Alerts.Events(r.Context(), limit, intParam(r, "offset", 0, 1<<30))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": total})
}

// AlertSources connects the alert center to the panel's services.
func (s *Server) AlertSources() alert.Sources {
	src := alert.Sources{}
	if s.Monitor != nil {
		src.Metrics = func(ctx context.Context, d time.Duration) (cpu, mem, load, disk float64, ok bool) {
			pts, err := s.Monitor.Series(ctx, d)
			if err != nil || len(pts) == 0 {
				return 0, 0, 0, 0, false
			}
			for _, p := range pts {
				cpu += p.CPU
				mem += p.Mem
				load += p.Load1
			}
			n := float64(len(pts))
			return cpu / n, mem / n, load / n, pts[len(pts)-1].Disk, true
		}
	}
	if s.Docker != nil {
		src.Services = func(ctx context.Context) map[string]string {
			out := map[string]string{}
			cs, err := s.Docker.Containers(ctx, false)
			if err != nil {
				return out
			}
			for _, c := range cs {
				if c.Managed {
					out[c.Name] = c.State
				}
			}
			// Stopped sites' project containers are stopped on purpose.
			if s.Sites != nil {
				if sites, err := s.Sites.List(ctx); err == nil {
					for _, st := range sites {
						if st.Stopped {
							delete(out, site.ProjectContainer(st.Domain))
						}
					}
				}
			}
			return out
		}
	}
	if s.Sites != nil {
		src.Certs = func(ctx context.Context) map[string]int64 {
			out := map[string]int64{}
			if sites, err := s.Sites.List(ctx); err == nil {
				for _, st := range sites {
					if st.SSLMode != "none" && !st.Stopped {
						out[st.Domain] = st.CertNotAfter
					}
				}
			}
			return out
		}
	}
	src.LoginFailures = func(ctx context.Context, since time.Time) (int, []string) {
		items, _, _ := s.Audit.ListLogins(ctx, 500, 0)
		n, ips := 0, map[string]bool{}
		for _, l := range items {
			if l.CreatedAt >= since.Unix() && !l.Success {
				n++
				ips[l.IP] = true
			}
		}
		var list []string
		for ip := range ips {
			list = append(list, ip)
		}
		return n, list
	}
	src.PanelLogins = func(ctx context.Context, since time.Time) []string {
		items, _, _ := s.Audit.ListLogins(ctx, 100, 0)
		var out []string
		for _, l := range items {
			if l.CreatedAt > since.Unix() && l.Success {
				out = append(out, fmt.Sprintf("%s from %s (%s)", l.Username, l.IP, s.geoLabel(l.IP)))
			}
		}
		return out
	}
	src.SSHLogins = func(ctx context.Context, since time.Time) []string {
		var out []string
		for _, e := range logcenter.SSH(ctx, 500, "").Items {
			if e.Result == "accepted" && e.Time > since.Unix() {
				out = append(out, fmt.Sprintf("%s from %s (%s, %s)", e.User, e.IP, s.geoLabel(e.IP), e.Method))
			}
		}
		return out
	}
	return src
}

func (s *Server) geoLabel(ip string) string {
	if s.GeoIP == nil {
		return "?"
	}
	i := s.GeoIP.Lookup(ip)
	if i.Private {
		return "LAN"
	}
	if i.Country == "" {
		return "?"
	}
	return strings.TrimSpace(i.Country + " " + i.Org)
}

// ---- probes ----

func (s *Server) handleProbes(w http.ResponseWriter, r *http.Request) {
	list, err := s.Probes.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleProbeSave(w http.ResponseWriter, r *http.Request) {
	var p probe.Probe
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p.ID = idParam(r)
	p, err := s.Probes.Save(r.Context(), p)
	if err != nil {
		writeCodedErr(w, err, probe.ErrNotFound)
		return
	}
	s.auditCat(r, "probe", "probe.save", p.Name+" "+p.Target)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleProbeDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Probes.Delete(r.Context(), idParam(r)); err != nil {
		writeCodedErr(w, err, probe.ErrNotFound)
		return
	}
	s.auditCat(r, "probe", "probe.delete", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleProbeCheck(w http.ResponseWriter, r *http.Request) {
	p, err := s.Probes.Get(r.Context(), idParam(r))
	if err != nil {
		writeCodedErr(w, err, probe.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, s.Probes.RunOnce(r.Context(), p))
}

func (s *Server) handleProbeResults(w http.ResponseWriter, r *http.Request) {
	d, ok := ranges[r.URL.Query().Get("range")]
	if !ok {
		d = 24 * time.Hour
	}
	res, err := s.Probes.Results(r.Context(), idParam(r), time.Now().Add(-d).Unix())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": res})
}

// ---- site reports ----

func (s *Server) handleSiteStats(w http.ResponseWriter, r *http.Request) {
	var country func(string) string
	if s.GeoIP != nil {
		country = func(ip string) string { return s.GeoIP.Lookup(ip).Country }
	}
	rep, err := s.Stats.Report(r.Context(), r.URL.Query().Get("domain"), intParam(r, "days", 1, 30), country)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleSiteStatsOverview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.Stats.Overview(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ov})
}

// ---- process guardian ----

func (s *Server) handleProcs(w http.ResponseWriter, r *http.Request) {
	list, err := s.Procs.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleProcSave(w http.ResponseWriter, r *http.Request) {
	var p supervisor.Process
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p.ID = idParam(r)
	p, err := s.Procs.Save(r.Context(), p)
	if err != nil {
		writeCodedErr(w, err, supervisor.ErrNotFound)
		return
	}
	s.auditCat(r, "process", "process.save", p.Name)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleProcDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Procs.Delete(r.Context(), idParam(r)); err != nil {
		writeCodedErr(w, err, supervisor.ErrNotFound)
		return
	}
	s.auditCat(r, "process", "process.delete", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleProcAction(w http.ResponseWriter, r *http.Request) {
	action := chi.URLParam(r, "action")
	if err := s.Procs.Action(r.Context(), idParam(r), action); err != nil {
		writeCodedErr(w, err, supervisor.ErrNotFound)
		return
	}
	s.auditCat(r, "process", "process."+action, chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleProcLogs(w http.ResponseWriter, r *http.Request) {
	out, err := s.Procs.Logs(r.Context(), idParam(r), intParam(r, "lines", 200, 5000))
	if err != nil {
		writeCodedErr(w, err, supervisor.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"log": out})
}

// ---- WordPress ----

func (s *Server) handleWPList(w http.ResponseWriter, r *http.Request) {
	list, err := s.WP.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleWPInstall(w http.ResponseWriter, r *http.Request) {
	var in wordpress.InstallInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.WP.Install(r.Context(), idParam(r), in)
	if err != nil {
		writeCodedErr(w, err, site.ErrNotFound)
		return
	}
	s.auditCat(r, "site", "wordpress.install", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

func (s *Server) handleWPInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.WP.Info(r.Context(), idParam(r))
	if err != nil {
		writeCodedErr(w, err, site.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleWPAction(w http.ResponseWriter, r *http.Request) {
	var in wordpress.ActionInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.WP.Action(r.Context(), idParam(r), in)
	if err != nil {
		writeCodedErr(w, err, site.ErrNotFound)
		return
	}
	s.auditCat(r, "site", "wordpress."+in.Action, chi.URLParam(r, "id"))
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

// ---- Composer ----

var composerActions = map[string][]string{
	"install":       {"install", "--no-dev", "--optimize-autoloader"},
	"update":        {"update", "--no-dev", "--optimize-autoloader"},
	"dump-autoload": {"dump-autoload", "--optimize"},
	"validate":      {"validate"},
	"outdated":      {"outdated", "--direct"},
}

func (s *Server) handleComposer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	args, ok := composerActions[in.Action]
	st, err := s.Sites.Get(r.Context(), idParam(r))
	switch {
	case err != nil:
		writeCodedErr(w, err, site.ErrNotFound)
		return
	case !ok || st.Type != "php":
		writeErr(w, http.StatusBadRequest, errBadRequest)
		return
	}
	id := s.Tasks.StartLong("composer "+in.Action+" "+st.Domain, time.Hour, func(ctx context.Context, log tasks.Logf) error {
		cmd := append([]string{"exec", "-u", "33", "-w", st.Root, "-e", "HOME=/tmp", "-e", "COMPOSER_HOME=/tmp/composer",
			webenv.PHPName(st.PHPVersion), "composer", "--no-interaction", "--no-ansi"}, args...)
		log("composer %s", strings.Join(args, " "))
		out, err := dockerx.Run(ctx, cmd...)
		for _, l := range splitLines(out) {
			log("  %s", l)
		}
		return err
	})
	s.auditCat(r, "site", "composer."+in.Action, st.Domain)
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

// ---- FTP / SFTP ----

func (s *Server) ftpRoutes(r chi.Router) {
	if s.FTP == nil {
		return
	}
	r.Get("/ftp", s.handleFTP)
	r.Post("/ftp/enabled", s.handleFTPEnabled)
	r.Post("/ftp/accounts", s.handleFTPCreate)
	r.Patch("/ftp/accounts/{id}", s.handleFTPUpdate)
	r.Post("/ftp/accounts/{id}/delete", s.handleFTPDelete)
	r.Get("/ftp/accounts/{id}/password", s.handleFTPPassword)
}

func (s *Server) handleFTP(w http.ResponseWriter, r *http.Request) {
	list, err := s.FTP.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "status": s.FTP.Status(r.Context())})
}

func (s *Server) handleFTPEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id := s.Tasks.Start("ftp", func(ctx context.Context, log tasks.Logf) error {
		if err := s.FTP.SetEnabled(ctx, in.On, log); err != nil {
			return err
		}
		if in.On && s.Firewall != nil {
			// Open the control and passive ports once (no-op if a rule exists).
			ports := fmt.Sprintf("21,%d-%d", ftpPassive[0], ftpPassive[1])
			rules, _ := s.Firewall.Rules(ctx)
			found := false
			for _, ru := range rules {
				found = found || ru.Ports == ports
			}
			if !found {
				if _, _, err := s.Firewall.SaveRule(ctx, firewallRule("accept", "tcp", ports, "FTP (lmcwhm)")); err != nil {
					log("firewall rule: %v", err)
				} else {
					log("firewall: opened tcp %s", ports)
				}
			}
		}
		return nil
	})
	s.auditCat(r, "ftp", "ftp.enabled", fmt.Sprint(in.On))
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

func (s *Server) handleFTPCreate(w http.ResponseWriter, r *http.Request) {
	var in ftp.Input
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.SiteID != 0 && in.Dir == "" && s.Sites != nil {
		if st, err := s.Sites.Get(r.Context(), in.SiteID); err == nil {
			in.Dir = st.Root
		}
	}
	a, err := s.FTP.Create(r.Context(), in)
	if err != nil {
		writeCodedErr(w, err, ftp.ErrNotFound)
		return
	}
	s.auditCat(r, "ftp", "ftp.create", a.Kind+" "+a.Username+" "+a.Dir)
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleFTPUpdate(w http.ResponseWriter, r *http.Request) {
	var in ftp.Update
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	a, err := s.FTP.Update(r.Context(), idParam(r), in)
	if err != nil {
		writeCodedErr(w, err, ftp.ErrNotFound)
		return
	}
	s.auditCat(r, "ftp", "ftp.update", a.Username)
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleFTPDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.FTP.Delete(r.Context(), idParam(r)); err != nil {
		writeCodedErr(w, err, ftp.ErrNotFound)
		return
	}
	s.auditCat(r, "ftp", "ftp.delete", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFTPPassword(w http.ResponseWriter, r *http.Request) {
	pw, err := s.FTP.Password(r.Context(), idParam(r))
	if err != nil {
		writeCodedErr(w, err, ftp.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"password": pw})
}

var ftpPassive = [2]int{ftp.PassiveFrom, ftp.PassiveTo}

func firewallRule(action, proto, ports, remark string) firewall.Rule {
	return firewall.Rule{Action: action, Proto: proto, Ports: ports, Remark: remark, Enabled: true}
}
