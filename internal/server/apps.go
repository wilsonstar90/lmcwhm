package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/alert"
	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/database"
	"github.com/wilsonstar90/lmcwhm/internal/site"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

func (s *Server) appRoutes(r chi.Router) {
	r.Get("/apps/catalog", s.handleCatalog)
	r.Get("/apps", s.handleApps)
	r.Post("/apps/install", s.handleInstallApp)
	r.Post("/apps/{id}/uninstall", s.handleUninstallApp)
	r.Post("/apps/{id}/update", s.handleUpdateApp)
	r.Post("/apps/{id}/build", s.handleAppBuild)
	r.Get("/apps/updates", s.handleAppUpdates)
	r.Post("/apps/updates/check", s.handleCheckAppUpdates)
	r.Post("/apps/{id}/{action:start|stop|restart}", s.handleControlApp)
	r.Get("/apps/{id}/logs", s.handleAppLogs)
	r.Get("/apps/{id}/config", s.handleAppConfig)
	r.Put("/apps/{id}/config", s.handlePutAppConfig)
	r.Get("/apps/{id}/credentials", s.handleAppCredentials)
	s.dbManageRoutes(r)

	r.Get("/databases", s.handleDatabases)
	r.Post("/databases", s.handleCreateDatabase)
	r.Post("/databases/{id}/delete", s.handleDeleteDatabase)
	if s.Backup != nil {
		s.dbTrashRoutes(r)
	}
	r.Post("/databases/{id}/password", s.handleDatabasePassword)
	r.Patch("/databases/{id}", s.handleDatabaseRemark)

	r.Get("/sites/{id}/project", s.handleProject)
	r.Post("/sites/{id}/project/restart", s.handleRestartProject)
}

// writeCodedErr maps service errors (optionally wrapped with ": detail") to API codes.
func writeCodedErr(w http.ResponseWriter, err error, notFound ...error) {
	for _, nf := range notFound {
		if errors.Is(err, nf) {
			writeErr(w, http.StatusNotFound, nf)
			return
		}
	}
	for _, e := range []error{appstore.ErrLowMemory, site.ErrPortTaken} {
		if errors.Is(err, e) {
			detail := strings.TrimSpace(strings.TrimPrefix(err.Error(), e.Error()+":"))
			writeJSON(w, http.StatusBadRequest, apiError{Code: e.Error(), Params: map[string]any{"value": detail}})
			return
		}
	}
	writeErr(w, http.StatusBadRequest, err)
}

func (s *Server) auditCat(r *http.Request, category, action, detail string) {
	s.Audit.Record(r.Context(), sessionFrom(r).User.Username, clientIP(r), category, action, detail)
}

// ---- App Store ----

// sourceInfo is what the install dialog shows about a compile install.
type sourceInfo struct {
	No       string `json:"no,omitempty"` // why not ("" = supported)
	Minutes  int    `json:"minutes,omitempty"`
	MinMemMB int    `json:"min_mem_mb,omitempty"`
	Defaults string `json:"defaults,omitempty"`
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	src := map[string]sourceInfo{}
	for _, a := range appstore.Catalog {
		for _, v := range a.Versions {
			rc, why := appstore.SourceSupport(a.Key, v.Version)
			src[a.Key+":"+v.Version] = sourceInfo{No: why, Minutes: rc.Minutes, MinMemMB: rc.MinMemMB, Defaults: rc.Defaults}
		}
	}
	for _, v := range webenv.PHPVersions {
		src["php:"+v] = sourceInfo{Minutes: webenv.PHPSourceMinutes, MinMemMB: 1024, Defaults: "php-config --configure-options"}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": appstore.Catalog, "source": src})
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	list, err := s.Apps.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleInstallApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		App     string `json:"app"`
		Version string `json:"version"`
		appstore.BuildOptions
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Ports held by website projects must not be handed to services.
	avoid := map[int]bool{}
	if sites, err := s.Sites.List(r.Context()); err == nil {
		for _, st := range sites {
			if site.IsProject(st.Type) {
				avoid[st.AppPort] = true
			}
		}
	}
	id, err := s.Apps.Install(r.Context(), in.App, in.Version, avoid, in.BuildOptions)
	if err != nil {
		writeCodedErr(w, err)
		return
	}
	s.auditCat(r, "app", "app.install", strings.TrimSpace(in.App+" "+in.Version+" "+in.Mode))
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleUninstallApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RemoveData bool `json:"remove_data"`
	}
	decode(r, &in)
	i, err := s.Apps.Get(r.Context(), idParam(r))
	if err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	id, err := s.Apps.Uninstall(r.Context(), i.ID, in.RemoveData)
	if err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	detail := i.App + " " + i.Version
	if in.RemoveData {
		detail += " (+data)"
	}
	s.auditCat(r, "app", "app.uninstall", detail)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	i, err := s.Apps.Get(r.Context(), idParam(r))
	if err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	id, err := s.Apps.Update(r.Context(), i.ID)
	if err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	s.auditCat(r, "app", "app.update", i.App+" "+i.Version)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleAppUpdates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.Apps.Updates(r.Context())})
}

func (s *Server) handleCheckAppUpdates(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	items, err := s.Apps.CheckUpdates(ctx, s.notifyAppUpdate)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// notifyAppUpdate sends one alert per newly found update.
func (s *Server) notifyAppUpdate(u appstore.Update) {
	if s.Alerts == nil {
		return
	}
	latest := u.Latest
	if latest == "" || latest == u.Current {
		latest = "new build"
	}
	s.Alerts.Emit(context.Background(), alert.KindAppUpdate, u.Key, "info",
		fmt.Sprintf("Update available: %s %s → %s", u.Name, u.Current, latest), u.Image, false)
}

func (s *Server) handleControlApp(w http.ResponseWriter, r *http.Request) {
	action := chi.URLParam(r, "action")
	if err := s.Apps.Control(r.Context(), idParam(r), action); err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	s.auditCat(r, "app", "app."+action, strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func tailParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if n <= 0 || n > 5000 {
		n = 300
	}
	return n
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	out, err := s.Apps.Logs(r.Context(), idParam(r), tailParam(r))
	if err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": out})
}

func (s *Server) handleAppConfig(w http.ResponseWriter, r *http.Request) {
	body, err := s.Apps.Config(r.Context(), idParam(r))
	if err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"config": body})
}

func (s *Server) handlePutAppConfig(w http.ResponseWriter, r *http.Request) {
	var in struct{ Config string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Apps.SaveConfig(r.Context(), idParam(r), in.Config)
	if err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	s.auditCat(r, "app", "app.config", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleAppCredentials(w http.ResponseWriter, r *http.Request) {
	i, err := s.Apps.Get(r.Context(), idParam(r))
	if err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	s.auditCat(r, "app", "app.credentials", i.App+" "+i.Version)
	writeJSON(w, http.StatusOK, map[string]any{
		"host": "127.0.0.1", "port": i.Port, "username": i.AdminUser(), "password": i.Password(),
	})
}

// ---- databases ----

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	list, err := s.DBs.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": list, "adminer": s.Apps.AdminerPort(r.Context()) > 0, "phpmyadmin": s.Apps.PMAServers(r.Context()),
	})
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	var in database.CreateInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.DBs.Create(r.Context(), in)
	if err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	s.auditCat(r, "database", "db.create", in.Name)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	d, err := s.DBs.Get(r.Context(), idParam(r))
	if err != nil {
		writeCodedErr(w, err, database.ErrNotFound)
		return
	}
	id, err := s.DBs.Delete(r.Context(), d.ID)
	if err != nil {
		writeCodedErr(w, err, database.ErrNotFound)
		return
	}
	s.auditCat(r, "database", "db.delete", d.Name)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleDatabasePassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	decode(r, &in)
	pass, err := s.DBs.SetPassword(r.Context(), idParam(r), in.Password)
	if err != nil {
		writeCodedErr(w, err, database.ErrNotFound)
		return
	}
	s.auditCat(r, "database", "db.password", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]string{"password": pass})
}

func (s *Server) handleDatabaseRemark(w http.ResponseWriter, r *http.Request) {
	var in struct{ Remark string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.DBs.SetRemark(r.Context(), idParam(r), in.Remark); err != nil {
		writeCodedErr(w, err, database.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- runtime projects ----

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	state, err := s.Sites.ProjectState(r.Context(), idParam(r))
	if err != nil {
		writeSiteErr(w, err)
		return
	}
	logs, _ := s.Sites.ProjectLogs(r.Context(), idParam(r), tailParam(r))
	writeJSON(w, http.StatusOK, map[string]string{"state": state, "logs": logs})
}

func (s *Server) handleRestartProject(w http.ResponseWriter, r *http.Request) {
	if err := s.Sites.RestartProject(r.Context(), idParam(r)); err != nil {
		writeSiteErr(w, err)
		return
	}
	s.audit(r, "site.project_restart", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- Adminer / phpMyAdmin ----

// toolProxy reverse-proxies a database tool on loopback for signed-in panel
// users. The tools keep their own CSRF tokens, so the panel's CSRF header is
// not required here; only the tool's own cookies go upstream, never the
// panel session. With strip, prefix is removed from the upstream path.
// toolProxy serves a tool on a loopback port to signed-in panel users;
// signon, when set, is passed to the tool as PMASignonHeader (any value the
// browser sent is dropped).
func (s *Server) toolProxy(w http.ResponseWriter, r *http.Request, port int, prefix string, strip bool, cookie func(string) bool, signon string) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		notFound(w)
		return
	}
	if _, err := s.Auth.Resolve(r.Context(), c.Value); err != nil {
		notFound(w)
		return
	}
	if port == 0 {
		notFound(w)
		return
	}
	if r.URL.Path == prefix {
		http.Redirect(w, r, prefix+"/", http.StatusFound)
		return
	}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			pr.Out.Host = pr.In.Host // absolute URLs the tool builds point back at the panel
			if strip {
				pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, prefix)
				pr.Out.URL.RawPath = ""
			}
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del(appstore.PMASignonHeader)
			if signon != "" {
				pr.Out.Header.Set(appstore.PMASignonHeader, signon)
			}
			for _, ck := range pr.In.Cookies() {
				if cookie(ck.Name) {
					pr.Out.AddCookie(ck)
				}
			}
		},
	}
	// The tools show some of their own pages in frames.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	rp.ServeHTTP(w, r)
}

func (s *Server) handleAdminer(w http.ResponseWriter, r *http.Request) {
	s.toolProxy(w, r, s.Apps.AdminerPort(r.Context()), "/adminer", true, func(n string) bool {
		return strings.HasPrefix(n, "adminer")
	}, "")
}

// handlePMA serves phpMyAdmin, which Apache already serves under the same
// prefix; a one-time link to the sign-on script carries the credentials.
func (s *Server) handlePMA(w http.ResponseWriter, r *http.Request) {
	signon := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, err := s.Auth.Resolve(r.Context(), c.Value); err == nil {
			signon = s.signonHeader(r) // only for a signed-in panel user
		}
	}
	s.toolProxy(w, r, s.Apps.PMAPort(r.Context()), appstore.PMAPrefix, false, func(n string) bool {
		return strings.HasPrefix(n, "phpMyAdmin") || strings.HasPrefix(strings.ToLower(n), "pma") || n == "LMCWHMSignon"
	}, signon)
}

// handleAppBuild switches an installed version between the pre-built image
// and a compile install (or compiles it again with other options).
func (s *Server) handleAppBuild(w http.ResponseWriter, r *http.Request) {
	var in appstore.BuildOptions
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Apps.SetBuild(r.Context(), idParam(r), in)
	if err != nil {
		writeCodedErr(w, err, appstore.ErrNotFound)
		return
	}
	s.auditCat(r, "app", "app.build", strconv.FormatInt(idParam(r), 10)+" "+in.Mode)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}
