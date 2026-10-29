package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

// PHP version settings (extensions, php.ini, FPM pool, status, logs) and the
// Varnish page cache.

func (s *Server) phpRoutes(r chi.Router) {
	if s.Sites == nil {
		return
	}
	r.Get("/php", s.handlePHPList)
	r.Get("/php/{v}", s.handlePHPGet)
	r.Put("/php/{v}", s.handlePHPSave)
	r.Post("/php/{v}/{action:install|start|stop|restart|reload|remove|update}", s.handlePHPAction)
	r.Get("/php/{v}/logs", s.handlePHPLogs)
	r.Get("/varnish", s.handleVarnish)
	r.Put("/varnish", s.handleVarnishSave)
	r.Post("/varnish/purge", s.handleVarnishPurge)
}

func phpVersion(w http.ResponseWriter, r *http.Request) (string, bool) {
	v := chi.URLParam(r, "v")
	if !webenv.ValidPHP(v) {
		writeErr(w, http.StatusNotFound, errors.New("php.unknown_version"))
		return "", false
	}
	return v, true
}

func memMB() int {
	if m, err := mem.VirtualMemory(); err == nil {
		return int(m.Total >> 20)
	}
	return 1024
}

func (s *Server) handlePHPList(w http.ResponseWriter, r *http.Request) {
	sites, _ := s.Sites.List(r.Context())
	count := map[string]int{}
	for _, st := range sites {
		if st.Type == "php" {
			count[st.PHPVersion]++
		}
	}
	items := []webenv.PHPVersionInfo{}
	for _, v := range webenv.PHPVersions {
		info := webenv.PHPVersionInfo{
			Version: v, EOL: webenv.EOLPHP(v), State: dockerx.State(r.Context(), webenv.PHPName(v)), Port: webenv.PHPPort(v),
			Extensions: s.Sites.Env.PHPConfig(v).Extensions, Sites: count[v],
		}
		if info.State != "" {
			if img, err := dockerx.Run(r.Context(), "inspect", "-f", "{{.Config.Image}}", webenv.PHPName(v)); err == nil {
				info.FullVersion = appstore.CurrentVersion(r.Context(), img)
			}
		}
		items = append(items, info)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "extensions": webenv.PHPExtensions, "ini_keys": webenv.PHPINIKeys,
		"dangerous": webenv.DangerousFunctions, "suggest": webenv.SuggestFPM(memMB()), "mem_mb": memMB(),
	})
}

func (s *Server) handlePHPGet(w http.ResponseWriter, r *http.Request) {
	v, ok := phpVersion(w, r)
	if !ok {
		return
	}
	out := map[string]any{"config": s.Sites.Env.PHPConfig(v), "state": dockerx.State(r.Context(), webenv.PHPName(v))}
	if out["state"] == "running" {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		if mods, err := webenv.PHPModules(ctx, v); err == nil {
			out["modules"] = mods
		}
		if st, err := webenv.PHPStatus(ctx, v); err == nil {
			out["status"] = st
		} else {
			out["status_error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePHPSave(w http.ResponseWriter, r *http.Request) {
	v, ok := phpVersion(w, r)
	if !ok {
		return
	}
	var c webenv.PHPConfig
	if err := decode(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := c.Normalize(v); err != nil {
		writeCodeErr(w, err, webenv.ErrPHPConfig, webenv.ErrPHPExtension)
		return
	}
	s.auditCat(r, "app", "php.save", v+" ext="+strings.Join(c.Extensions, ","))
	// Building extensions (swoole, grpc) compiles for many minutes.
	id := s.Tasks.StartLong("PHP "+v, 45*time.Minute, func(ctx context.Context, log tasks.Logf) error {
		return s.Sites.Env.SavePHPConfig(ctx, v, c, log)
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
}

// writeCodeErr reports "code: detail" errors as the code plus a field param.
func writeCodeErr(w http.ResponseWriter, err error, codes ...error) {
	for _, c := range codes {
		if errors.Is(err, c) {
			_, detail, _ := strings.Cut(err.Error(), ": ")
			writeJSON(w, http.StatusBadRequest, apiError{Code: c.Error(), Params: map[string]any{"field": detail}})
			return
		}
	}
	writeErr(w, http.StatusBadRequest, err)
}

func (s *Server) handlePHPAction(w http.ResponseWriter, r *http.Request) {
	v, ok := phpVersion(w, r)
	if !ok {
		return
	}
	action := chi.URLParam(r, "action")
	s.auditCat(r, "app", "php."+action, v)
	name := webenv.PHPName(v)
	switch action {
	case "install":
		id := s.Tasks.StartLong("PHP "+v, 30*time.Minute, func(ctx context.Context, log tasks.Logf) error {
			return s.Sites.Env.EnsurePHP(ctx, v, log)
		})
		writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
		return
	case "remove":
		sites, _ := s.Sites.List(r.Context())
		var using []string
		for _, st := range sites {
			if st.Type == "php" && st.PHPVersion == v {
				using = append(using, st.Domain)
			}
		}
		if len(using) > 0 {
			writeJSON(w, http.StatusConflict, apiError{Code: "php.in_use", Params: map[string]any{"site": strings.Join(using, ", "), "count": len(using)}})
			return
		}
		if err := s.Sites.Env.RemovePHP(r.Context(), v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if s.Apps != nil {
			s.Apps.ForgetPHP(r.Context(), v)
		}
	case "update":
		if dockerx.State(r.Context(), name) == "" {
			writeErr(w, http.StatusBadRequest, errors.New("php.not_installed"))
			return
		}
		id := s.Tasks.StartLong("PHP "+v, 45*time.Minute, func(ctx context.Context, log tasks.Logf) error {
			if err := s.Sites.Env.UpdatePHP(ctx, v, log); err != nil {
				return err
			}
			if s.Apps != nil {
				s.Apps.RefreshPHP(ctx, v)
			}
			return nil
		})
		writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
		return
	case "reload":
		if _, err := dockerx.Run(r.Context(), "kill", "-s", "USR2", name); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	default:
		if _, err := dockerx.Run(r.Context(), action, name); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePHPLogs(w http.ResponseWriter, r *http.Request) {
	v, ok := phpVersion(w, r)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	n = min(max(n, 50), 2000)
	var text string
	if r.URL.Query().Get("kind") == "slow" {
		lines, err := logcenter.Tail(s.Sites.Env.PHPSlowLog(v), n, "")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		text = strings.Join(lines, "\n")
	} else {
		text = dockerx.Logs(r.Context(), webenv.PHPName(v), n)
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": text})
}

// ---- Varnish ----

func (s *Server) handleVarnish(w http.ResponseWriter, r *http.Request) {
	sites, _ := s.Sites.List(r.Context())
	using := []map[string]any{}
	for _, st := range sites {
		if st.Advanced.Varnish {
			using = append(using, map[string]any{"id": st.ID, "domain": st.Domain, "ttl": st.Advanced.VarnishTTL})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": s.Sites.Env.VarnishSettings(), "stats": s.Sites.Env.VarnishStatus(r.Context()),
		"sites": using, "image": webenv.VarnishImage,
	})
}

func (s *Server) handleVarnishSave(w http.ResponseWriter, r *http.Request) {
	var in webenv.VarnishSettings
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Sites.Env.SaveVarnishSettings(in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "site", "varnish.save", strconv.Itoa(in.MemMB)+"MB")
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": s.Sites.Reapply("Varnish")})
}

func (s *Server) handleVarnishPurge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SiteID int64 `json:"site_id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var hosts []string
	if !sessionFrom(r).User.CanSite(in.SiteID) || in.SiteID == 0 && len(sessionFrom(r).User.Sites) > 0 {
		writeErr(w, http.StatusForbidden, errForbidden)
		return
	}
	if in.SiteID > 0 {
		st, err := s.Sites.Get(r.Context(), in.SiteID)
		if err != nil {
			writeErr(w, http.StatusNotFound, errors.New("site.not_found"))
			return
		}
		hosts = append([]string{st.Domain}, st.Aliases...)
	}
	if err := webenv.VarnishPurge(r.Context(), hosts); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.auditCat(r, "site", "varnish.purge", strings.Join(hosts, " "))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
