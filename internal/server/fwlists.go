package server

import (
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/firewall"
)

// Black / white lists (黑白名單): manual IPs, User-Agent and URL patterns;
// and the automatic entries (雲端黑白名單: lists, CrowdSec, AI).

func (s *Server) fwListRoutes(r interface {
	Get(string, http.HandlerFunc)
	Post(string, http.HandlerFunc)
	Put(string, http.HandlerFunc)
}) {
	r.Post("/firewall/ips/import", s.handleImportFWIPs)
	r.Post("/firewall/ips/clear", s.handleClearFWIPs)
	r.Get("/firewall/ips/auto", s.handleAutoFWIPs)
	r.Post("/firewall/ips/{id}/keep", s.handleKeepFWIP)
	r.Get("/firewall/patterns", s.handleFWPatterns)
	r.Post("/firewall/patterns", s.handleSaveFWPattern)
	r.Put("/firewall/patterns/{id}", s.handleSaveFWPattern)
	r.Post("/firewall/patterns/{id}/delete", s.handleDeleteFWPattern)
	r.Post("/firewall/patterns/import", s.handleImportFWPatterns)
	r.Post("/firewall/patterns/clear", s.handleClearFWPatterns)
}

func (s *Server) handleImportFWIPs(w http.ResponseWriter, r *http.Request) {
	var in struct {
		List    string   `json:"list"`
		IPs     []string `json:"ips"`
		Minutes int      `json:"minutes"`
		Remark  string   `json:"remark"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	self, _ := netip.ParseAddr(clientIP(r))
	added, skipped, err := s.Firewall.ImportIPs(r.Context(), in.List, in.IPs, time.Duration(in.Minutes)*time.Minute, in.Remark, self.Unmap())
	if err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.import", in.List+" +"+strconv.Itoa(added))
	writeJSON(w, http.StatusOK, map[string]int{"added": added, "skipped": skipped})
}

func (s *Server) handleClearFWIPs(w http.ResponseWriter, r *http.Request) {
	var in struct {
		List string `json:"list"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	n, err := s.Firewall.ClearIPs(r.Context(), in.List)
	if err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.clear", in.List)
	writeJSON(w, http.StatusOK, map[string]int64{"removed": n})
}

func (s *Server) handleAutoFWIPs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	size := intParam(r, "size", 50, 500)
	page := max(intParam(r, "page", 1, 1<<20), 1)
	p, err := s.Firewall.AutoIPs(r.Context(), firewall.AutoFilter{List: q.Get("list"), Source: q.Get("source"), Query: q.Get("q"), Offset: (page - 1) * size, Limit: size})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleKeepFWIP(w http.ResponseWriter, r *http.Request) {
	if err := s.Firewall.KeepIP(r.Context(), idParam(r)); err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.keep", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// applyPatterns reloads the edge with the changed User-Agent / URL lists.
func (s *Server) applyPatterns(w http.ResponseWriter, r *http.Request, out any) {
	if s.Sites.Env.Installed(r.Context()) {
		if err := s.Sites.Apply(r.Context()); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFWPatterns(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Firewall.Patterns(r.Context(), r.URL.Query().Get("kind"), r.URL.Query().Get("list"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ps})
}

func (s *Server) handleSaveFWPattern(w http.ResponseWriter, r *http.Request) {
	var in firewall.Pattern
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.ID = idParam(r)
	p, err := s.Firewall.SavePattern(r.Context(), in)
	if err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.pattern", p.Kind+" "+p.List+" "+p.Value)
	s.applyPatterns(w, r, p)
}

func (s *Server) handleDeleteFWPattern(w http.ResponseWriter, r *http.Request) {
	if err := s.Firewall.DeletePattern(r.Context(), idParam(r)); err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.pattern_delete", strconv.FormatInt(idParam(r), 10))
	s.applyPatterns(w, r, map[string]bool{"ok": true})
}

func (s *Server) handleImportFWPatterns(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind   string   `json:"kind"`
		List   string   `json:"list"`
		Match  string   `json:"match"`
		Values []string `json:"values"`
		Remark string   `json:"remark"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	added, skipped, err := s.Firewall.ImportPatterns(r.Context(), in.Kind, in.List, in.Match, in.Values, in.Remark)
	if err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.pattern_import", in.Kind+" "+in.List+" +"+strconv.Itoa(added))
	s.applyPatterns(w, r, map[string]int{"added": added, "skipped": skipped})
}

func (s *Server) handleClearFWPatterns(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
		List string `json:"list"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	n, err := s.Firewall.ClearPatterns(r.Context(), in.Kind, in.List)
	if err != nil {
		writeFWErr(w, err)
		return
	}
	s.auditCat(r, "security", "firewall.pattern_clear", in.Kind+" "+in.List)
	s.applyPatterns(w, r, map[string]int64{"removed": n})
}
