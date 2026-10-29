package server

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/cron"
	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/geoip"
	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
	"github.com/wilsonstar90/lmcwhm/internal/monitor"
)

var errBadLog = errors.New("logs.invalid_source")

func (s *Server) m4Routes(r chi.Router) {
	if s.Monitor != nil {
		r.Get("/monitor/series", s.handleSeries)
		r.Get("/monitor/processes", s.handleProcesses)
		r.Get("/monitor/services", s.handleServices)
	}
	if s.Cron != nil {
		r.Get("/cron", s.handleCronList)
		r.Post("/cron", s.handleCronSave)
		r.Get("/cron/preview", s.handleCronPreview)
		r.Get("/cron/runs", s.handleCronRecent)
		r.Get("/cron/runs/{id}", s.handleCronRun)
		r.Put("/cron/{id}", s.handleCronSave)
		r.Post("/cron/{id}/delete", s.handleCronDelete)
		r.Post("/cron/{id}/run", s.handleCronRunNow)
		r.Post("/cron/{id}/enabled", s.handleCronEnabled)
		r.Get("/cron/{id}/runs", s.handleCronRuns)
	}
	if s.GeoIP != nil {
		r.Get("/geoip/status", s.handleGeoStatus)
		r.Post("/geoip/lookup", s.handleGeoLookup)
		r.Post("/geoip/update", s.handleGeoUpdate)
	}
	r.Get("/logs/sources", s.handleLogSources)
	r.Get("/logs/site", s.handleSiteLog)
	r.Get("/logs/site/download", s.handleSiteLogDownload)
	r.Post("/logs/site/clear", s.handleSiteLogClear)
	r.Get("/logs/ssh", s.handleSSHLog)
	r.Get("/logs/firewall", s.handleFirewallLog)
	r.Get("/logs/journal", s.handleJournal)
	r.Get("/logs/container", s.handleContainerLog)
}

func intParam(r *http.Request, name string, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, max)
}

// ---- monitor ----

var ranges = map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	d, ok := ranges[r.URL.Query().Get("range")]
	if !ok {
		d = time.Hour
	}
	pts, err := s.Monitor.Series(r.Context(), d)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": pts, "interval": monitor.Interval.Seconds()})
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	ps, err := monitor.TopProcesses(r.Context(), intParam(r, "n", 15, 100))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ps})
}

// handleServices reports the panel's own containers (edge, web, runtimes,
// databases) and the Docker daemon.
func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	type svc struct {
		Name   string `json:"name"`
		Image  string `json:"image"`
		State  string `json:"state"`
		Status string `json:"status"`
	}
	out := []svc{}
	docker := dockerx.Available(r.Context())
	if docker && s.Docker != nil {
		if cs, err := s.Docker.Containers(r.Context(), false); err == nil {
			for _, c := range cs {
				if c.Managed {
					out = append(out, svc{c.Name, c.Image, c.State, c.Status})
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"docker": docker, "items": out})
}

// ---- cron ----

func writeCronErr(w http.ResponseWriter, err error) { writeCodedErr(w, err, cron.ErrNotFound) }

func (s *Server) handleCronList(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.Cron.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": jobs})
}

func (s *Server) handleCronSave(w http.ResponseWriter, r *http.Request) {
	var in cron.Input
	if err := decodeLarge(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var j cron.Job
	var err error
	if chi.URLParam(r, "id") == "" {
		j, err = s.Cron.Create(r.Context(), in)
	} else {
		j, err = s.Cron.Update(r.Context(), idParam(r), in)
	}
	if err != nil {
		writeCronErr(w, err)
		return
	}
	s.auditCat(r, "cron", "cron.save", j.Name)
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleCronPreview(w http.ResponseWriter, r *http.Request) {
	next, err := s.Cron.Next(r.URL.Query().Get("schedule"), 5)
	if err != nil {
		writeCronErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"next": next})
}

func (s *Server) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	j, err := s.Cron.Get(r.Context(), idParam(r))
	if err == nil {
		err = s.Cron.Delete(r.Context(), j.ID)
	}
	if err != nil {
		writeCronErr(w, err)
		return
	}
	s.auditCat(r, "cron", "cron.delete", j.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCronRunNow(w http.ResponseWriter, r *http.Request) {
	if err := s.Cron.RunNow(r.Context(), idParam(r)); err != nil {
		writeCronErr(w, err)
		return
	}
	s.auditCat(r, "cron", "cron.run", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCronEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	decode(r, &in)
	if err := s.Cron.SetEnabled(r.Context(), idParam(r), in.Enabled); err != nil {
		writeCronErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCronRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Cron.Runs(r.Context(), idParam(r), intParam(r, "limit", 50, 200))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": runs})
}

func (s *Server) handleCronRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.Cron.Run(r.Context(), idParam(r))
	if err != nil {
		writeCronErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// handleCronRecent lists runs of all jobs (log center "Cron" tab).
func (s *Server) handleCronRecent(w http.ResponseWriter, r *http.Request) {
	runs, total, err := s.Cron.RecentRuns(r.Context(), intParam(r, "limit", 20, 200), intParam(r, "offset", 0, 1<<30))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	names := map[int64]string{}
	if jobs, err := s.Cron.List(r.Context()); err == nil {
		for _, j := range jobs {
			names[j.ID] = j.Name
		}
	}
	type row struct {
		cron.Run
		Job string `json:"job"`
	}
	out := make([]row, len(runs))
	for i, run := range runs {
		out[i] = row{run, names[run.JobID]}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": total})
}

// ---- IP geolocation ----

func (s *Server) handleGeoStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.GeoIP.Status())
}

func (s *Server) handleGeoLookup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IPs []string `json:"ips"`
	}
	if err := decode(r, &in); err != nil || len(in.IPs) > 1000 {
		writeErr(w, http.StatusBadRequest, errBadRequest)
		return
	}
	out := map[string]geoip.Info{}
	for _, ip := range in.IPs {
		out[ip] = s.GeoIP.Lookup(strings.TrimSpace(ip))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGeoUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Edition string `json:"edition"`
	}
	decode(r, &in)
	if in.Edition != "country" && in.Edition != "city" {
		writeErr(w, http.StatusBadRequest, geoip.ErrEdition)
		return
	}
	s.auditCat(r, "system", "geoip.update", in.Edition)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := s.GeoIP.Update(ctx, in.Edition); err != nil {
			s.Log.Error("geoip update", "err", err)
		}
	}()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- log center ----

func (s *Server) handleLogSources(w http.ResponseWriter, r *http.Request) {
	sites := []string{}
	if s.Sites != nil {
		if list, err := s.Sites.List(r.Context()); err == nil {
			for _, st := range list {
				sites = append(sites, st.Domain)
			}
		}
	}
	type ctr struct {
		Name    string `json:"name"`
		Image   string `json:"image"`
		Managed bool   `json:"managed"`
	}
	containers := []ctr{}
	if s.Docker != nil {
		if cs, err := s.Docker.Containers(r.Context(), false); err == nil {
			for _, c := range cs {
				containers = append(containers, ctr{c.Name, c.Image, c.Managed})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": sites, "containers": containers})
}

// siteLog maps (domain, kind) to a log file, accepting only existing sites.
func (s *Server) siteLog(r *http.Request, domain, kind string) (string, error) {
	return s.siteLogPath(r.Context(), domain, kind)
}

func (s *Server) siteLogPath(ctx context.Context, domain, kind string) (string, error) {
	if s.Sites == nil || (kind != "access" && kind != "error") {
		return "", errBadLog
	}
	list, err := s.Sites.List(ctx)
	if err != nil {
		return "", err
	}
	for _, st := range list {
		if st.Domain == domain {
			name := domain + ".log"
			if kind == "error" {
				name = domain + ".error.log"
			}
			return filepath.Join(s.Sites.Env.WWWLogs, name), nil
		}
	}
	return "", errBadLog
}

func (s *Server) handleSiteLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := s.siteLog(r, q.Get("domain"), q.Get("kind"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	filter := strings.TrimSpace(q.Get("q") + " " + q.Get("ip"))
	lines, err := logcenter.Tail(p, intParam(r, "lines", 500, 5000), filter)
	if errors.Is(err, os.ErrNotExist) {
		lines, err = []string{}, nil
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var size int64
	if fi, err := os.Stat(p); err == nil {
		size = fi.Size()
	}
	if q.Get("kind") == "access" {
		items := make([]logcenter.Access, len(lines))
		for i, l := range lines {
			items[i] = logcenter.ParseAccess(l)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "size": size})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "size": size})
}

func (s *Server) handleSiteLogDownload(w http.ResponseWriter, r *http.Request) {
	p, err := s.siteLog(r, r.URL.Query().Get("domain"), r.URL.Query().Get("kind"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusNotFound, errBadLog)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(p)}))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *Server) handleSiteLogClear(w http.ResponseWriter, r *http.Request) {
	var in struct{ Domain, Kind string }
	decode(r, &in)
	p, err := s.siteLog(r, in.Domain, in.Kind)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Truncate in place: nginx keeps writing to the same open file.
	if err := os.Truncate(p, 0); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.auditCat(r, "logs", "logs.clear", in.Domain+" "+in.Kind)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSSHLog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, logcenter.SSH(r.Context(), intParam(r, "limit", 500, 5000), r.URL.Query().Get("q")))
}

func (s *Server) handleFirewallLog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, logcenter.Drops(r.Context(), intParam(r, "limit", 1000, 10000), r.URL.Query().Get("q")))
}

// handleJournal: unit "" = whole system, "lmcwhm" = the panel itself.
func (s *Server) handleJournal(w http.ResponseWriter, r *http.Request) {
	lines, err := logcenter.Journal(r.Context(), r.URL.Query().Get("unit"), intParam(r, "lines", 500, 5000), r.URL.Query().Get("q"))
	if err != nil {
		writeCodedErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func (s *Server) handleContainerLog(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if s.Docker == nil {
		writeErr(w, http.StatusBadRequest, errBadLog)
		return
	}
	out, err := s.Docker.Logs(r.Context(), name, intParam(r, "lines", 500, 5000))
	if err != nil {
		writeCodedErr(w, err)
		return
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if f := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))); f != "" {
		kept := lines[:0]
		for _, l := range lines {
			if strings.Contains(strings.ToLower(l), f) {
				kept = append(kept, l)
			}
		}
		lines = kept
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}
