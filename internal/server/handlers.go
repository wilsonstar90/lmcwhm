package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/auth"
	"github.com/wilsonstar90/lmcwhm/internal/config"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/sysinfo"
	"github.com/wilsonstar90/lmcwhm/internal/tlsutil"
)

var (
	errCSRF       = errors.New("auth.csrf")
	errBadRequest = errors.New("common.bad_request")
	errInternal   = errors.New("common.internal")
	errBadPass    = errors.New("auth.bad_password")
)

// apiError is the wire format: a stable code the frontend translates, plus params.
type apiError struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	code := err.Error()
	// Only our own dotted codes are exposed; anything else is internal.
	if !strings.Contains(code, ".") || strings.Contains(code, " ") {
		code = errInternal.Error()
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, apiError{Code: code})
}

func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return errBadRequest
	}
	return nil
}

func (s *Server) handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, securityHeaders, s.gate)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/public/meta", s.handleMeta)
		r.Post("/auth/login", s.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Get("/system/overview", s.handleOverview)
			s.maintenanceRoutes(r)

			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handlePutSettings)
			r.Post("/settings/password", s.handlePassword)
			r.Post("/settings/username", s.handleUsername)
			r.Post("/settings/2fa/begin", s.handle2FABegin)
			r.Post("/settings/2fa/enable", s.handle2FAEnable)
			r.Post("/settings/2fa/disable", s.handle2FADisable)
			r.Get("/settings/ssl", s.handleGetSSL)
			r.Post("/settings/ssl", s.handlePutSSL)

			if s.Sites != nil {
				s.siteRoutes(r)
				s.routeRoutes(r)
			}
			if s.Apps != nil {
				s.appRoutes(r)
			}
			if s.Docker != nil {
				s.dockerRoutes(r)
			}
			if s.Files != nil {
				s.fileRoutes(r)
			}
			s.m4Routes(r)
			r.Get("/logs/login", s.handleLoginLogs)
			r.Get("/logs/operation", s.handleOperationLogs)
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusNotFound, apiError{Code: "common.not_found"})
		})
	})

	if s.Apps != nil {
		r.HandleFunc("/adminer", s.handleAdminer)
		r.HandleFunc("/adminer/*", s.handleAdminer)
		r.HandleFunc(appstore.PMAPrefix, s.handlePMA)
		r.HandleFunc(appstore.PMAPrefix+"/*", s.handlePMA)
	}
	r.NotFound(s.spa)
	return r
}

// spa serves the embedded Vue build, falling back to index.html for client routes.
func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if s.WebFS == nil {
		http.Error(w, "web UI not built (run: make web)", http.StatusNotFound)
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p != "" {
		if f, err := s.WebFS.Open(p); err == nil {
			st, _ := f.Stat()
			f.Close()
			if st != nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, s.WebFS, p)
				return
			}
		}
	}
	index, err := fs.ReadFile(s.WebFS, "index.html")
	if err != nil {
		http.Error(w, "web UI not built (run: make web)", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(index)
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	cfg := s.Settings.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"alias": cfg.PanelAlias, "language": cfg.Language, "version": config.Version,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password, TOTP string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.Settings.Get()
	token, sess, err := s.Auth.Login(r.Context(), auth.LoginInput{
		Username: in.Username, Password: in.Password, TOTP: in.TOTP,
		IP: clientIP(r), UserAgent: r.UserAgent(),
		MaxFails: cfg.LoginMaxFails, LockWindow: time.Duration(cfg.LoginLockMins) * time.Minute,
		SessionTTL: time.Duration(cfg.SessionTimeout) * time.Second,
	})
	switch {
	case errors.Is(err, auth.ErrTOTPRequired):
		writeJSON(w, http.StatusOK, map[string]any{"totp_required": true})
		return
	case errors.Is(err, auth.ErrLocked):
		writeJSON(w, http.StatusTooManyRequests, apiError{Code: err.Error(), Params: map[string]any{"minutes": cfg.LoginLockMins}})
		return
	case err != nil:
		writeErr(w, http.StatusUnauthorized, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: sess.ExpiresAt,
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"user": sess.User, "csrf": sess.CSRF})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.Auth.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{"user": sess.User, "csrf": sess.CSRF})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sysinfo.Collect(r.Context()))
}

// ---- settings ----

type settingsView struct {
	settings.Settings
	BasicAuthHash     string `json:"basic_auth_hash,omitempty"` // shadows the embedded field: never serialized
	BasicAuthPassword string `json:"basic_auth_password,omitempty"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, settingsView{Settings: s.Settings.Get()})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	old := s.Settings.Get()
	in := settingsView{Settings: old}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	next := in.Settings
	next.BasicAuthHash = old.BasicAuthHash
	switch {
	case next.BasicAuthUser == "":
		next.BasicAuthHash = ""
	case in.BasicAuthPassword != "":
		h, err := bcrypt.GenerateFromPassword([]byte(in.BasicAuthPassword), bcrypt.DefaultCost)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		next.BasicAuthHash = string(h)
	case old.BasicAuthHash == "":
		writeErr(w, http.StatusBadRequest, errors.New("settings.basic_auth_password_required"))
		return
	}
	if next.PanelPort != old.PanelPort && !portFree(next) {
		writeJSON(w, http.StatusConflict, apiError{Code: "settings.port_in_use", Params: map[string]any{"port": next.PanelPort}})
		return
	}
	if err := s.Settings.Save(r.Context(), next); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sess := sessionFrom(r)
	s.Audit.Record(r.Context(), sess.User.Username, clientIP(r), "panel", "settings.update", diffSettings(old, next))

	restart := settings.RequiresRestart(old, next)
	writeJSON(w, http.StatusOK, map[string]any{"settings": settingsView{Settings: next}, "restart": restart})
	if restart {
		// Let the response flush before the listener goes away.
		time.AfterFunc(800*time.Millisecond, s.RequestRestart)
	}
}

func portFree(cfg settings.Settings) bool {
	network, host := "tcp4", "0.0.0.0"
	if cfg.ListenIPv6 {
		network, host = "tcp", "::"
	}
	ln, err := net.Listen(network, net.JoinHostPort(host, strconv.Itoa(cfg.PanelPort)))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func diffSettings(a, b settings.Settings) string {
	var parts []string
	add := func(name string, x, y any) {
		if fmt.Sprint(x) != fmt.Sprint(y) {
			parts = append(parts, fmt.Sprintf("%s: %v → %v", name, x, y))
		}
	}
	add("panel_port", a.PanelPort, b.PanelPort)
	add("security_entry", a.SecurityEntry, b.SecurityEntry)
	add("ssl_mode", a.SSLMode, b.SSLMode)
	add("bind_domain", a.BindDomain, b.BindDomain)
	add("allowed_ips", a.AllowedIPs, b.AllowedIPs)
	add("listen_ipv6", a.ListenIPv6, b.ListenIPv6)
	add("session_timeout", a.SessionTimeout, b.SessionTimeout)
	add("basic_auth_user", a.BasicAuthUser, b.BasicAuthUser)
	add("language", a.Language, b.Language)
	add("panel_alias", a.PanelAlias, b.PanelAlias)
	return strings.Join(parts, "; ")
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Old, New string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sess := sessionFrom(r)
	if !s.Auth.CheckPassword(r.Context(), sess.User.ID, in.Old) {
		writeErr(w, http.StatusBadRequest, errBadPass)
		return
	}
	if err := s.Auth.SetPassword(r.Context(), sess.User.ID, in.New); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.Audit.Record(r.Context(), sess.User.Username, clientIP(r), "panel", "user.password", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "relogin": true})
}

func (s *Server) handleUsername(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sess := sessionFrom(r)
	if !s.Auth.CheckPassword(r.Context(), sess.User.ID, in.Password) {
		writeErr(w, http.StatusBadRequest, errBadPass)
		return
	}
	if err := s.Auth.SetUsername(r.Context(), sess.User.ID, in.Username); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.Audit.Record(r.Context(), sess.User.Username, clientIP(r), "panel", "user.username", sess.User.Username+" → "+in.Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handle2FABegin(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	// Re-enrolling would silently drop active 2FA; disabling requires the password.
	if sess.User.TOTPEnabled {
		writeErr(w, http.StatusConflict, errors.New("auth.totp_already_enabled"))
		return
	}
	secret, url, err := s.Auth.BeginTOTP(r.Context(), sess.User, s.Settings.Get().PanelAlias)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "url": url})
}

func (s *Server) handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sess := sessionFrom(r)
	if err := s.Auth.EnableTOTP(r.Context(), sess.User.ID, in.Code); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.Audit.Record(r.Context(), sess.User.Username, clientIP(r), "panel", "user.2fa_enable", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sess := sessionFrom(r)
	if !s.Auth.CheckPassword(r.Context(), sess.User.ID, in.Password) {
		writeErr(w, http.StatusBadRequest, errBadPass)
		return
	}
	if err := s.Auth.DisableTOTP(r.Context(), sess.User.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.Audit.Record(r.Context(), sess.User.Username, clientIP(r), "panel", "user.2fa_disable", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGetSSL(w http.ResponseWriter, r *http.Request) {
	info, err := tlsutil.Info(s.Paths.CertDir)
	resp := map[string]any{"mode": s.Settings.Get().SSLMode}
	if err == nil {
		resp["cert"] = info
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePutSSL(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode settings.SSLMode `json:"mode"`
		Cert string           `json:"cert"`
		Key  string           `json:"key"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	switch in.Mode {
	case settings.SSLCustom:
		if err := tlsutil.InstallCustom(s.Paths.CertDir, in.Cert, in.Key); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	case settings.SSLSelfSigned:
		if err := tlsutil.GenerateSelfSigned(s.Paths.CertDir, nil); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	case settings.SSLOff:
	default:
		writeErr(w, http.StatusBadRequest, errBadRequest)
		return
	}
	if _, err := s.Settings.Update(r.Context(), func(c *settings.Settings) { c.SSLMode = in.Mode }); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sess := sessionFrom(r)
	s.Audit.Record(r.Context(), sess.User.Username, clientIP(r), "panel", "ssl.update", string(in.Mode))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart": true})
	time.AfterFunc(800*time.Millisecond, s.RequestRestart)
}

func paging(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return
}

func (s *Server) handleLoginLogs(w http.ResponseWriter, r *http.Request) {
	limit, offset := paging(r)
	items, total, err := s.Audit.ListLogins(r.Context(), limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleOperationLogs(w http.ResponseWriter, r *http.Request) {
	limit, offset := paging(r)
	items, total, err := s.Audit.List(r.Context(), limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}
