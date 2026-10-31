package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/wilsonstar90/lmcwhm/internal/settings"
)

// The header logo (面板設定 → 介面): the panel alias as text, or an uploaded
// image. The image is public, like the alias, so the login page shows it.
const maxLogo = 256 << 10

var errBadLogo = errors.New("settings.bad_logo")

// logoTypes are the accepted images by their leading bytes.
var logoTypes = []struct {
	magic []byte
	ctype string
}{
	{[]byte("\x89PNG\r\n\x1a\n"), "image/png"},
	{[]byte("RIFF"), "image/webp"},
	{[]byte("<svg"), "image/svg+xml"},
	{[]byte("<?xml"), "image/svg+xml"},
}

func (s *Server) logoPath() string { return filepath.Join(s.Paths.DataDir, "brand", "logo") }

func logoType(b []byte) string {
	b = bytes.TrimLeft(b, " \t\r\n\xef\xbb\xbf")
	for _, t := range logoTypes {
		if bytes.HasPrefix(b, t.magic) && (t.ctype != "image/webp" || len(b) > 12 && string(b[8:12]) == "WEBP") {
			return t.ctype
		}
	}
	return ""
}

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(s.logoPath())
	if err != nil || !s.Settings.Get().LogoImage {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", logoType(b))
	// An SVG opened on its own may not run scripts or load anything.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

func (s *Server) handleUploadLogo(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(io.LimitReader(r.Body, maxLogo+1))
	if err != nil || len(b) == 0 || len(b) > maxLogo || logoType(b) == "" {
		writeErr(w, http.StatusBadRequest, errBadLogo)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.logoPath()), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.WriteFile(s.logoPath(), b, 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := s.Settings.Update(r.Context(), func(c *settings.Settings) { c.LogoImage = true }); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.auditCat(r, "panel", "settings.logo", strconv.Itoa(len(b))+" bytes")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteLogo(w http.ResponseWriter, r *http.Request) {
	os.Remove(s.logoPath())
	if _, err := s.Settings.Update(r.Context(), func(c *settings.Settings) { c.LogoImage = false }); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.auditCat(r, "panel", "settings.logo", "removed")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
