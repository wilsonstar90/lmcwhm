package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/wilsonstar90/lmcwhm/internal/auth"
)

const (
	sessionCookie = "lmcwhm_session"
	entryCookie   = "lmcwhm_entry"
)

type ctxKey int

const sessionKey ctxKey = iota

func clientIP(r *http.Request) string {
	// The panel port is reached directly (not through the edge proxy), so the
	// socket peer is authoritative; forwarded headers are deliberately ignored.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		return ip.To4().String() // normalise ::ffff:1.2.3.4
	}
	return host
}

// notFound mimics a plain web server so a gated panel does not reveal itself.
func notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("<html><head><title>404 Not Found</title></head><body><center><h1>404 Not Found</h1></center><hr><center>nginx</center></body></html>"))
}

// gate applies, in aaPanel order: authorized IPs → bound domain → BasicAuth →
// security entry. Only after all pass does a request reach the router.
func (s *Server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Settings.Get()

		if !cfg.IPAllowed(net.ParseIP(clientIP(r))) {
			notFound(w)
			return
		}

		// MCP clients authenticate with a bearer token instead of the browser
		// gates (bound domain, BasicAuth, security entry).
		if r.URL.Path == "/mcp" {
			next.ServeHTTP(w, r)
			return
		}

		if cfg.BindDomain != "" {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if !strings.EqualFold(host, cfg.BindDomain) {
				notFound(w)
				return
			}
		}

		if cfg.BasicAuthUser != "" {
			u, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(cfg.BasicAuthUser)) != 1 ||
				bcrypt.CompareHashAndPassword([]byte(cfg.BasicAuthHash), []byte(p)) != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="lmcwhm"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		if cfg.SecurityEntry != "" {
			if r.URL.Path == cfg.SecurityEntry || r.URL.Path == cfg.SecurityEntry+"/" {
				http.SetCookie(w, &http.Cookie{
					Name: entryCookie, Value: s.entryToken(cfg.SecurityEntry), Path: "/",
					HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
				})
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			if !s.hasEntry(r, cfg.SecurityEntry) {
				notFound(w)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// entryToken binds the cookie to the current entry path, so changing the entry
// invalidates every previously issued entry cookie.
func (s *Server) entryToken(entry string) string {
	m := hmac.New(sha256.New, []byte(s.secret))
	m.Write([]byte("entry:" + entry))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) hasEntry(r *http.Request, entry string) bool {
	if c, err := r.Cookie(entryCookie); err == nil && hmac.Equal([]byte(c.Value), []byte(s.entryToken(entry))) {
		return true
	}
	// A logged-in session implies the entry was passed earlier.
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, err := s.Auth.Resolve(r.Context(), c.Value); err == nil {
			return true
		}
	}
	return false
}

// requireAuth resolves the session cookie and enforces the CSRF header on
// state-changing requests.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, auth.ErrNoSession)
			return
		}
		sess, err := s.Auth.Resolve(r.Context(), c.Value)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, auth.ErrNoSession)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(sess.CSRF)) != 1 {
				writeErr(w, http.StatusForbidden, errCSRF)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))
	})
}

func sessionFrom(r *http.Request) auth.Session {
	s, _ := r.Context().Value(sessionKey).(auth.Session)
	return s
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
