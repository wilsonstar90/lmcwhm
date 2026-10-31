package server

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/errpages"
)

// Custom 4xx / 5xx pages (Security → WAF → Error pages, and per website).

func (s *Server) errPageRoutes(r chi.Router) {
	if s.ErrPages == nil {
		return
	}
	r.Get("/errpages", s.handleErrPages)
	r.Post("/errpages", s.handleSaveErrPage)
	r.Put("/errpages/mapping", s.handlePutErrMapping)
	r.Put("/errpages/{id}", s.handleSaveErrPage)
	r.Post("/errpages/{id}/delete", s.handleDeleteErrPage)
	r.Post("/errpages/{id}/reset", s.handleResetErrPage)
}

func writeErrPageErr(w http.ResponseWriter, err error) {
	writeCodedErr(w, err, errpages.ErrNotFound)
}

func (s *Server) handleErrPages(w http.ResponseWriter, r *http.Request) {
	pages, err := s.ErrPages.Pages(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": pages, "mapping": s.ErrPages.Mapping(r.Context())})
}

func (s *Server) handleSaveErrPage(w http.ResponseWriter, r *http.Request) {
	var in errpages.Page
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.ID = idParam(r)
	p, err := s.ErrPages.SavePage(r.Context(), in)
	if err != nil {
		writeErrPageErr(w, err)
		return
	}
	s.auditCat(r, "security", "errpage.save", p.Name)
	s.applyPatterns(w, r, p)
}

func (s *Server) handleDeleteErrPage(w http.ResponseWriter, r *http.Request) {
	if err := s.ErrPages.DeletePage(r.Context(), idParam(r)); err != nil {
		writeErrPageErr(w, err)
		return
	}
	s.auditCat(r, "security", "errpage.delete", strconv.FormatInt(idParam(r), 10))
	s.applyPatterns(w, r, map[string]bool{"ok": true})
}

func (s *Server) handleResetErrPage(w http.ResponseWriter, r *http.Request) {
	if err := s.ErrPages.ResetPage(r.Context(), idParam(r)); err != nil {
		writeErrPageErr(w, err)
		return
	}
	s.auditCat(r, "security", "errpage.reset", strconv.FormatInt(idParam(r), 10))
	s.applyPatterns(w, r, map[string]bool{"ok": true})
}

func (s *Server) handlePutErrMapping(w http.ResponseWriter, r *http.Request) {
	var in errpages.Mapping
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	m, err := s.ErrPages.SaveMapping(r.Context(), in)
	if err != nil {
		writeErrPageErr(w, err)
		return
	}
	s.auditCat(r, "security", "errpage.mapping", "")
	s.applyPatterns(w, r, m)
}
