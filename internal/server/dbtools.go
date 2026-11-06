package server

// Database tools (tables: repair, optimize, engine, comment, schema) and
// per-database remote access, for MySQL / MariaDB.

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/wilsonstar90/lmcwhm/internal/database"
)

func (s *Server) dbToolRoutes(r chi.Router) {
	r.Get("/databases/{id}/tables", s.handleDBTables)
	r.Post("/databases/{id}/tables/action", s.handleDBTableAction)
	r.Put("/databases/{id}/tables/comment", s.handleDBTableComment)
	r.Get("/databases/{id}/tables/schema", s.handleDBTableSchema)
	r.Get("/databases/{id}/access", s.handleDBAccess)
	r.Put("/databases/{id}/access", s.handlePutDBAccess)
}

func writeDBErr(w http.ResponseWriter, err error) {
	writeCodedErr(w, err, database.ErrNotFound)
}

func (s *Server) handleDBTables(w http.ResponseWriter, r *http.Request) {
	list, err := s.DBs.Tables(r.Context(), idParam(r))
	if err != nil {
		writeDBErr(w, err)
		return
	}
	var size int64
	for _, t := range list {
		size += t.Size
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "size": size})
}

func (s *Server) handleDBTableAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string   `json:"action"`
		Tables []string `json:"tables"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.DBs.TableAction(r.Context(), idParam(r), in.Action, in.Tables)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	s.auditCat(r, "database", "db.tables."+in.Action, chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleDBTableComment(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Comment string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.DBs.SetTableComment(r.Context(), idParam(r), in.Name, in.Comment); err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDBTableSchema(w http.ResponseWriter, r *http.Request) {
	stmt, err := s.DBs.TableSchema(r.Context(), idParam(r), r.URL.Query().Get("name"))
	if err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"schema": stmt})
}

func (s *Server) handleDBAccess(w http.ResponseWriter, r *http.Request) {
	a, err := s.DBs.GetAccess(r.Context(), idParam(r))
	if err != nil {
		writeDBErr(w, err)
		return
	}
	fw := map[string]bool{"available": false, "enabled": false}
	if s.Firewall != nil {
		fw["available"] = s.Firewall.Available()
		if c, err := s.Firewall.Config(r.Context()); err == nil {
			fw["enabled"] = c.Enabled
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"access": a, "firewall": fw})
}

func (s *Server) handlePutDBAccess(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string   `json:"mode"`
		IPs  []string `json:"ips"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.DBs.SetAccess(r.Context(), idParam(r), in.Mode, in.IPs)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	s.auditCat(r, "database", "db.access", chi.URLParam(r, "id")+" "+in.Mode)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}
