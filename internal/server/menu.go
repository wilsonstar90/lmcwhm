package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// The order of the main menu and the entries hidden from it (Panel settings
// → Interface); hidden pages stay reachable by their address.

var menuKeys = []string{"home", "website", "domains", "database", "docker", "appstore", "files", "ftp", "terminal", "cron", "processes",
	"backup", "security", "monitor", "uptime", "alerts", "logs", "mail", "toolbox", "ai", "settings"}

// pinned menu entries cannot be hidden: without them there is no way back.
var pinned = map[string]bool{"home": true, "settings": true}

var errBadMenu = errors.New("settings.invalid_menu")

type menuConfig struct {
	Order  []string `json:"order"`
	Hidden []string `json:"hidden"`
}

const menuKey = "menu"

func (s *Server) menuRoutes(r chi.Router) {
	r.Get("/ui/menu", s.handleMenu)
	r.Put("/ui/menu", s.handlePutMenu)
}

func (s *Server) menu(r *http.Request) menuConfig {
	m := menuConfig{Order: []string{}, Hidden: []string{}}
	var raw string
	if s.Store.DB.QueryRowContext(r.Context(), `SELECT value FROM settings WHERE key = ?`, menuKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &m)
	}
	return m
}

func (s *Server) handleMenu(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"menu": s.menu(r), "keys": menuKeys})
}

// cleanMenu keeps known keys once each; pinned entries are never hidden.
func cleanMenu(in menuConfig) (menuConfig, error) {
	known := map[string]bool{}
	for _, k := range menuKeys {
		known[k] = true
	}
	out := menuConfig{Order: []string{}, Hidden: []string{}}
	seen := map[string]bool{}
	for _, k := range in.Order {
		if !known[k] {
			return out, errBadMenu
		}
		if !seen[k] {
			seen[k] = true
			out.Order = append(out.Order, k)
		}
	}
	hid := map[string]bool{}
	for _, k := range in.Hidden {
		if !known[k] || pinned[k] {
			return out, errBadMenu
		}
		if !hid[k] {
			hid[k] = true
			out.Hidden = append(out.Hidden, k)
		}
	}
	return out, nil
}

func (s *Server) handlePutMenu(w http.ResponseWriter, r *http.Request) {
	if !sessionFrom(r).User.IsAdmin {
		writeErr(w, http.StatusForbidden, errForbidden)
		return
	}
	var in menuConfig
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	m, err := cleanMenu(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	b, _ := json.Marshal(m)
	if _, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, menuKey, string(b)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.auditCat(r, "panel", "settings.menu", "")
	writeJSON(w, http.StatusOK, map[string]any{"menu": m})
}
