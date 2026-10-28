package server

import (
	"context"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/alert"
	"github.com/wilsonstar90/lmcwhm/internal/auth"
	"github.com/wilsonstar90/lmcwhm/internal/files"
	"github.com/wilsonstar90/lmcwhm/internal/firewall"
	"github.com/wilsonstar90/lmcwhm/internal/secscan"
	"github.com/wilsonstar90/lmcwhm/internal/tamper"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/toolbox"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

func (s *Server) m9Routes(r chi.Router) {
	if s.Toolbox != nil {
		r.Get("/toolbox/system", s.handleTBSystem)
		r.Put("/toolbox/swap", s.handleTBSwap)
		r.Put("/toolbox/hostname", s.handleTBHostname)
		r.Put("/toolbox/dns", s.handleTBDNS)
		r.Put("/toolbox/time", s.handleTBTime)
		r.Post("/toolbox/time/sync", s.handleTBTimeSync)
		r.Get("/toolbox/ssh", s.handleTBSSH)
		r.Put("/toolbox/ssh", s.handleTBSSHSave)
		r.Post("/toolbox/ssh/keys", s.handleTBKeyAdd)
		r.Post("/toolbox/ssh/keys/delete", s.handleTBKeyDelete)
		r.Get("/toolbox/processes", s.handleTBProcs)
		r.Post("/toolbox/processes/{pid}/kill", s.handleTBKill)
		r.Get("/toolbox/connections", s.handleTBConns)
		r.Get("/toolbox/disks", s.handleTBDisks)
		r.Get("/toolbox/du", s.handleTBDu)
		r.Get("/toolbox/junk", s.handleTBJunk)
		r.Post("/toolbox/junk/clean", s.handleTBJunkClean)
	}
	if s.SecScan != nil {
		r.Get("/secscan", s.handleSecScans)
		r.Post("/secscan/run", s.handleSecScanRun)
		r.Get("/secscan/{id}", s.handleSecScan)
		r.Post("/secscan/fix", s.handleSecScanFix)
	}
	if s.Malware != nil {
		r.Get("/malware/scans", s.handleMalwareScans)
		r.Post("/malware/scan", s.handleMalwareRun)
		r.Get("/malware/scans/{id}", s.handleMalwareScan)
		r.Post("/malware/scans/{id}/{action:quarantine|ignore}", s.handleMalwareAction)
		r.Get("/malware/quarantine", s.handleQuarantine)
		r.Post("/malware/quarantine/{qid}/{action:restore|delete}", s.handleQuarantineAction)
	}
	if s.Tamper != nil {
		r.Get("/tamper", s.handleTamperList)
		r.Post("/tamper", s.handleTamperSave)
		r.Put("/tamper/{id}", s.handleTamperSave)
		r.Post("/tamper/{id}/delete", s.handleTamperDelete)
		r.Post("/tamper/{id}/enabled", s.handleTamperEnabled)
		r.Post("/tamper/{id}/baseline", s.handleTamperBaseline)
		r.Get("/tamper/events", s.handleTamperEvents)
	}
	r.Get("/users", s.handleUsers)
	r.Post("/users", s.handleUserSave)
	r.Put("/users/{id}", s.handleUserSave)
	r.Post("/users/{id}/delete", s.handleUserDelete)
	if s.Files != nil {
		r.Get("/files/search", s.handleFileSearch)
		r.Post("/files/fetch", s.handleFileFetch)
		if s.Shares != nil {
			r.Get("/files/shares", s.handleShares)
			r.Post("/files/shares", s.handleShareCreate)
			r.Post("/files/shares/{id}/delete", s.handleShareDelete)
			r.Get("/files/favorites", s.handleFavorites)
			r.Post("/files/favorites", s.handleFavoriteSet)
		}
	}
}

// ---- toolbox ----

func (s *Server) handleTBSystem(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Toolbox.System(r.Context(), r.URL.Query().Get("zones") == "1"))
}

// tbResult writes {ok} or the error (toolbox codes are client errors).
func (s *Server) tbResult(w http.ResponseWriter, r *http.Request, action, detail string, err error) {
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "toolbox", action, detail)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTBSwap(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SizeMB int `json:"size_mb"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id := s.Tasks.Start("toolbox.swap", func(ctx context.Context, log tasks.Logf) error {
		log("setting swap file to %d MB", in.SizeMB)
		return s.Toolbox.SetSwap(ctx, in.SizeMB)
	})
	s.auditCat(r, "toolbox", "toolbox.swap", strconv.Itoa(in.SizeMB)+"MB")
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
}

func (s *Server) handleTBHostname(w http.ResponseWriter, r *http.Request) {
	var in struct{ Hostname string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.tbResult(w, r, "toolbox.hostname", in.Hostname, s.Toolbox.SetHostname(r.Context(), strings.TrimSpace(in.Hostname)))
}

func (s *Server) handleTBDNS(w http.ResponseWriter, r *http.Request) {
	var in struct{ Servers []string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.tbResult(w, r, "toolbox.dns", strings.Join(in.Servers, " "), s.Toolbox.SetDNS(r.Context(), in.Servers))
}

func (s *Server) handleTBTime(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Timezone string `json:"timezone"`
		NTP      *bool  `json:"ntp"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.tbResult(w, r, "toolbox.time", in.Timezone, s.Toolbox.SetTime(r.Context(), in.Timezone, in.NTP))
}

func (s *Server) handleTBTimeSync(w http.ResponseWriter, r *http.Request) {
	s.tbResult(w, r, "toolbox.time_sync", "", s.Toolbox.SyncTime(r.Context()))
}

func (s *Server) handleTBSSH(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Toolbox.GetSSH(r.Context()))
}

func (s *Server) handleTBSSHSave(w http.ResponseWriter, r *http.Request) {
	var in toolbox.SSH
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.tbResult(w, r, "toolbox.ssh", fmt.Sprintf("port=%d root=%s password=%v", in.Port, in.PermitRoot, in.PasswordAuth),
		s.Toolbox.SetSSH(r.Context(), in))
}

func (s *Server) handleTBKeyAdd(w http.ResponseWriter, r *http.Request) {
	var in struct{ Key string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f := strings.Fields(in.Key)
	detail := ""
	if len(f) > 2 {
		detail = f[2]
	}
	s.tbResult(w, r, "toolbox.ssh_key_add", detail, s.Toolbox.AddKey(r.Context(), in.Key))
}

func (s *Server) handleTBKeyDelete(w http.ResponseWriter, r *http.Request) {
	var in struct{ Line int }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.tbResult(w, r, "toolbox.ssh_key_delete", strconv.Itoa(in.Line), s.Toolbox.RemoveKey(r.Context(), in.Line))
}

func (s *Server) handleTBProcs(w http.ResponseWriter, r *http.Request) {
	ps, err := toolbox.Processes(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ps})
}

func (s *Server) handleTBKill(w http.ResponseWriter, r *http.Request) {
	pid, _ := strconv.Atoi(chi.URLParam(r, "pid"))
	var in struct{ Force bool }
	decode(r, &in)
	s.tbResult(w, r, "toolbox.kill", strconv.Itoa(pid), toolbox.Kill(r.Context(), int32(pid), in.Force))
}

func (s *Server) handleTBConns(w http.ResponseWriter, r *http.Request) {
	cs, err := toolbox.Connections(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": cs})
}

func (s *Server) handleTBDisks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": toolbox.Mounts(r.Context())})
}

func (s *Server) handleTBDu(w http.ResponseWriter, r *http.Request) {
	list, err := s.Toolbox.DirUsage(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleTBJunk(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.Toolbox.ScanJunk(r.Context(), s.Paths.Home)})
}

func (s *Server) handleTBJunkClean(w http.ResponseWriter, r *http.Request) {
	var in struct{ Keys []string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	freed, err := s.Toolbox.CleanJunk(r.Context(), s.Paths.Home, in.Keys)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "toolbox", "toolbox.clean", strings.Join(in.Keys, ","))
	writeJSON(w, http.StatusOK, map[string]any{"freed": freed})
}

// ---- security scan ----

// fixRoutes says where a panel finding is fixed in the UI.
var fixRoutes = map[string]string{
	"no_entry": "settings", "default_port": "settings", "panel_http": "settings", "panel_open": "settings", "no_2fa": "settings",
	"firewall_off": "security", "waf_off": "security", "crowdsec_off": "security", "no_recent_backup": "backup", "no_alert_channel": "alerts",
	"ssl_expiring": "website", "probes_down": "uptime", "cron_failed": "cron", "procs_down": "processes",
}

func (s *Server) secscanInput(ctx context.Context) secscan.Input {
	in := secscan.Input{EOLPHP: map[string]int{}, AllowedPorts: map[int]bool{21: true}}
	for _, rk := range s.risks(ctx, s.summary(ctx)) {
		in.PanelFindings = append(in.PanelFindings, secscan.Finding{Code: rk.Code, Level: rk.Level, Value: rk.Value, Route: fixRoutes[rk.Code]})
	}
	if s.Sites != nil {
		in.WebRoot = s.Sites.Env.WWWRoot
		if list, err := s.Sites.List(ctx); err == nil {
			for _, st := range list {
				if st.Type == "php" && webenv.EOLPHP(st.PHPVersion) {
					in.EOLPHP[st.PHPVersion]++
				}
			}
		}
	}
	for _, p := range firewall.SSHPorts() {
		in.AllowedPorts[p] = true
	}
	in.AllowedPorts[80], in.AllowedPorts[443] = true, true
	in.AllowedPorts[s.Settings.Get().PanelPort] = true
	return in
}

func (s *Server) handleSecScans(w http.ResponseWriter, r *http.Request) {
	list, err := s.SecScan.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleSecScan(w http.ResponseWriter, r *http.Request) {
	rep, err := s.SecScan.Get(r.Context(), idParam(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleSecScanRun(w http.ResponseWriter, r *http.Request) {
	s.auditCat(r, "security", "secscan.run", "")
	id := s.Tasks.StartLong("secscan", 5*time.Minute, func(ctx context.Context, log tasks.Logf) error {
		log("checking SSH, accounts, updates, kernel settings, open ports and website files")
		rep, err := s.SecScan.Scan(ctx, s.secscanInput(ctx))
		if err != nil {
			return err
		}
		log("score %d, %d findings", rep.Score, len(rep.Findings))
		return nil
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
}

func (s *Server) handleSecScanFix(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "security", "secscan.fix", in.Code)
	if in.Code == "ssh_root" {
		if s.Toolbox == nil {
			writeErr(w, http.StatusBadRequest, secscan.ErrFix)
			return
		}
		cur := s.Toolbox.GetSSH(r.Context())
		if len(cur.AuthorizedKeys) == 0 {
			writeErr(w, http.StatusBadRequest, toolbox.ErrLockout) // root would have no way in
			return
		}
		cur.PermitRoot = "prohibit-password"
		if err := s.Toolbox.SetSSH(r.Context(), cur); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	root := ""
	if s.Sites != nil {
		root = s.Sites.Env.WWWRoot
	}
	id := s.Tasks.StartLong("secscan.fix", 20*time.Minute, func(ctx context.Context, log tasks.Logf) error {
		return s.SecScan.Fix(ctx, in.Code, root, log)
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
}

// SecScanLoop runs the scan weekly and alerts on critical findings.
func (s *Server) SecScanLoop(ctx context.Context) {
	if s.SecScan == nil {
		return
	}
	t := time.NewTimer(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		t.Reset(6 * time.Hour)
		if last, ok := s.SecScan.Latest(ctx); ok && time.Since(time.Unix(last.CreatedAt, 0)) < 7*24*time.Hour {
			continue
		}
		rep, err := s.SecScan.Scan(ctx, s.secscanInput(ctx))
		if err != nil || s.Alerts == nil {
			continue
		}
		var crit []string
		for _, f := range rep.Findings {
			if f.Level == "critical" {
				crit = append(crit, f.Code)
			}
		}
		if len(crit) > 0 {
			s.Alerts.Emit(ctx, alert.KindSecurityScan, "weekly", "critical",
				fmt.Sprintf("Security scan: score %d, %d critical", rep.Score, len(crit)), strings.Join(crit, ", "), false)
		}
	}
}

// ---- malware ----

func (s *Server) handleMalwareScans(w http.ResponseWriter, r *http.Request) {
	list, err := s.Malware.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for i := range list { // the list shows counts; the detail has the findings
		list[i].Findings = list[i].Findings[:0:0]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "root": s.Malware.WebRoot})
}

func (s *Server) handleMalwareScan(w http.ResponseWriter, r *http.Request) {
	sc, err := s.Malware.Get(r.Context(), idParam(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) handleMalwareRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paths  []string `json:"paths"`
		ClamAV bool     `json:"clamav"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(in.Paths) == 0 {
		in.Paths = []string{s.Malware.WebRoot}
	}
	var paths []string
	for _, p := range in.Paths {
		c, err := s.Malware.CleanPath(p)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		paths = append(paths, c)
	}
	s.auditCat(r, "security", "malware.scan", strings.Join(paths, " "))
	id := s.Tasks.StartLong("malware.scan", 2*time.Hour, func(ctx context.Context, log tasks.Logf) error {
		sc, err := s.Malware.Run(ctx, paths, in.ClamAV, log)
		if err == nil && len(sc.Findings) > 0 && s.Alerts != nil {
			s.Alerts.Emit(ctx, alert.KindMalware, strconv.FormatInt(sc.ID, 10), "critical",
				fmt.Sprintf("Webshell scan: %d suspicious files", len(sc.Findings)), sc.Findings[0].Path, false)
		}
		return err
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
}

func (s *Server) handleMalwareAction(w http.ResponseWriter, r *http.Request) {
	var in struct{ Path string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var err error
	action := chi.URLParam(r, "action")
	if action == "quarantine" {
		err = s.Malware.QuarantineFile(r.Context(), idParam(r), in.Path)
	} else {
		err = s.Malware.Ignore(r.Context(), idParam(r), in.Path)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "security", "malware."+action, in.Path)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleQuarantine(w http.ResponseWriter, r *http.Request) {
	list, err := s.Malware.QuarantineList()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleQuarantineAction(w http.ResponseWriter, r *http.Request) {
	id, action := chi.URLParam(r, "qid"), chi.URLParam(r, "action")
	var err error
	if action == "restore" {
		err = s.Malware.Restore(id)
	} else {
		err = s.Malware.Purge(id)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "security", "quarantine."+action, id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- tamper protection ----

func (s *Server) handleTamperList(w http.ResponseWriter, r *http.Request) {
	list, err := s.Tamper.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "default_exts": tamper.DefaultExts, "default_exclude": tamper.DefaultExclude})
}

func (s *Server) handleTamperSave(w http.ResponseWriter, r *http.Request) {
	var in tamper.Rule
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.ID = idParam(r)
	rule, err := s.Tamper.Save(r.Context(), in)
	if err != nil && rule.ID == 0 {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "security", "tamper.save", rule.Mode+" "+rule.Path)
	writeJSON(w, http.StatusOK, rule) // a failed apply shows as state=error
}

func (s *Server) handleTamperDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Tamper.Delete(r.Context(), idParam(r)); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "security", "tamper.delete", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTamperEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct{ On bool }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rule, err := s.Tamper.SetEnabled(r.Context(), idParam(r), in.On)
	if err != nil && rule.ID == 0 {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "security", "tamper.enabled", fmt.Sprintf("%s %v", rule.Path, in.On))
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) handleTamperBaseline(w http.ResponseWriter, r *http.Request) {
	if err := s.Tamper.RefreshBaseline(r.Context(), idParam(r)); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "security", "tamper.baseline", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTamperEvents(w http.ResponseWriter, r *http.Request) {
	rule, _ := strconv.ParseInt(r.URL.Query().Get("rule"), 10, 64)
	list, err := s.Tamper.Events(r.Context(), rule, 500)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// TamperAlerts turns tamper events into alerts, at most one per rule every 5 minutes.
func (s *Server) TamperAlerts() func(tamper.Rule, tamper.Event) {
	var mu sync.Mutex
	last := map[int64]time.Time{}
	return func(rule tamper.Rule, e tamper.Event) {
		if s.Alerts == nil {
			return
		}
		mu.Lock()
		if time.Since(last[rule.ID]) < 5*time.Minute {
			mu.Unlock()
			return
		}
		last[rule.ID] = time.Now()
		mu.Unlock()
		s.Alerts.Emit(context.Background(), alert.KindTamper, strconv.FormatInt(rule.ID, 10), "warning",
			"Protected file changed: "+e.Path, e.Op+" → "+e.Action, false)
	}
}

// ---- panel users ----

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Auth.Users(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "modules": auth.Modules})
}

func (s *Server) handleUserSave(w http.ResponseWriter, r *http.Request) {
	var in auth.UserInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id := idParam(r)
	if id == sessionFrom(r).User.ID && (!in.IsAdmin || !in.Enabled) {
		writeErr(w, http.StatusBadRequest, auth.ErrLastAdmin) // cannot lock yourself out
		return
	}
	var u auth.User
	var err error
	if id == 0 {
		u, err = s.Auth.AddUser(r.Context(), in)
	} else {
		u, err = s.Auth.UpdateUser(r.Context(), id, in)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "panel", "user.save", fmt.Sprintf("%s admin=%v perms=%s", u.Username, u.IsAdmin, strings.Join(u.Perms, ",")))
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	id := idParam(r)
	if id == sessionFrom(r).User.ID {
		writeErr(w, http.StatusBadRequest, auth.ErrLastAdmin)
		return
	}
	if err := s.Auth.DeleteUser(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "panel", "user.delete", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- file manager extras ----

func (s *Server) handleFileSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	hits, truncated, err := s.Files.Search(r.Context(), files.SearchInput{
		Dir: q.Get("dir"), Query: q.Get("q"), Content: q.Get("content") == "1", Ext: q.Get("ext"), Limit: limit,
	})
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": hits, "truncated": truncated})
}

func (s *Server) handleFileFetch(w http.ResponseWriter, r *http.Request) {
	var in struct{ URL, Dir, Name string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "files", "files.fetch", in.URL+" → "+in.Dir)
	id := s.Tasks.StartLong("files.fetch", 2*time.Hour, func(ctx context.Context, log tasks.Logf) error {
		log("downloading %s", in.URL)
		dest, err := s.Files.Fetch(ctx, in.URL, in.Dir, in.Name, func(n, total int64) {
			if total > 0 {
				log("%d / %d MB (%d%%)", n>>20, total>>20, n*100/total)
			} else {
				log("%d MB", n>>20)
			}
		})
		if err == nil {
			log("saved to %s", dest)
		}
		return err
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
}

func (s *Server) handleShares(w http.ResponseWriter, r *http.Request) {
	list, err := s.Shares.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path         string `json:"path"`
		Password     string `json:"password"`
		Hours        int    `json:"hours"`
		MaxDownloads int    `json:"max_downloads"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	fi, err := s.Files.Stat(in.Path)
	if err != nil {
		writeFileErr(w, err)
		return
	}
	if fi.IsDir {
		writeErr(w, http.StatusBadRequest, files.ErrIsDir)
		return
	}
	sh, err := s.Shares.Create(r.Context(), files.Clean(in.Path), in.Password, time.Duration(max(in.Hours, 0))*time.Hour, in.MaxDownloads,
		sessionFrom(r).User.Username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.auditCat(r, "files", "files.share", sh.Path)
	writeJSON(w, http.StatusOK, sh)
}

func (s *Server) handleShareDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Shares.Delete(r.Context(), idParam(r)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.auditCat(r, "files", "files.unshare", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request) {
	list, err := s.Shares.Favorites(r.Context(), sessionFrom(r).User.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleFavoriteSet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
		On   bool   `json:"on"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	uid := sessionFrom(r).User.ID
	var err error
	if in.On {
		err = s.Shares.AddFavorite(r.Context(), uid, files.Clean(in.Path))
	} else {
		err = s.Shares.RemoveFavorite(r.Context(), uid, files.Clean(in.Path))
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- public share links (/s/{token}) ----

var sharePage = template.Must(template.New("s").Parse(`<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex">
<title>{{.Name}}</title><style>
body{font:15px/1.5 system-ui,sans-serif;background:#f5f6f8;color:#1f2329;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0;padding:16px}
@media (prefers-color-scheme:dark){body{background:#16181d;color:#e5e7eb}.c{background:#1f2229!important}input{background:#16181d;color:#e5e7eb;border-color:#3a3f48!important}}
.c{background:#fff;border-radius:10px;padding:28px;max-width:420px;width:100%;box-shadow:0 2px 12px rgba(0,0,0,.08)}
h1{font-size:18px;margin:0 0 6px;word-break:break-all}.m{opacity:.65;font-size:13px;margin:0 0 18px}
input{width:100%;box-sizing:border-box;padding:9px 10px;border:1px solid #d0d4da;border-radius:6px;font-size:15px;margin-bottom:12px}
button{width:100%;padding:10px;border:0;border-radius:6px;background:#18a058;color:#fff;font-size:15px;cursor:pointer}.e{color:#d03050;font-size:13px}
</style></head><body><div class="c"><h1>{{.Name}}</h1><p class="m">{{.Size}}</p>
<form method="post">{{if .Password}}<input type="password" name="password" placeholder="Password" autofocus required>{{end}}
{{if .Error}}<p class="e">{{.Error}}</p>{{end}}<button type="submit">Download</button></form></div></body></html>`))

func humanSize(n int64) string {
	const u = "KMGT"
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	f, i := float64(n)/1024, 0
	for f >= 1024 && i < 3 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %cB", f, u[i])
}

func (s *Server) handlePublicShare(w http.ResponseWriter, r *http.Request) {
	if s.Shares == nil || s.Files == nil {
		notFound(w)
		return
	}
	sh, err := s.Shares.Lookup(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		notFound(w)
		return
	}
	f, fi, err := s.Files.Open(sh.Path)
	if err != nil {
		notFound(w)
		return
	}
	defer f.Close()
	name := path.Base(sh.Path)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	page := map[string]any{"Name": name, "Size": humanSize(fi.Size()), "Password": sh.HasPassword}
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		sharePage.Execute(w, page)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !sh.CheckPassword(r.PostFormValue("password")) {
		time.Sleep(time.Second)
		page["Error"] = "Wrong password"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		sharePage.Execute(w, page)
		return
	}
	s.Shares.Count(r.Context(), sh.ID)
	s.Audit.Record(r.Context(), "share", clientIP(r), "files", "files.share_download", sh.Path)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}
