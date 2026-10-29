package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/firewall"
)

// Port forwarding (域名 / SNI → 端口轉發).

func (s *Server) forwardRoutes(r chi.Router) {
	if s.Firewall == nil {
		return
	}
	r.Get("/forwards", s.handleForwards)
	r.Post("/forwards", s.handleSaveForward)
	r.Put("/forwards/{id}", s.handleSaveForward)
	r.Post("/forwards/{id}/delete", s.handleDeleteForward)
}

type publishedPort struct {
	Container string `json:"container"`
	Port      int    `json:"port"`
}

// published lists the host ports containers publish (by port, and for the picker).
func (s *Server) published(r *http.Request) (map[int]string, []publishedPort) {
	byPort, list := map[int]string{}, []publishedPort{}
	if s.Docker == nil {
		return byPort, list
	}
	cs, err := s.Docker.Containers(r.Context(), false)
	if err != nil {
		return byPort, list
	}
	for _, c := range cs {
		for _, p := range c.HostPorts {
			byPort[p] = c.Name
			list = append(list, publishedPort{Container: c.Name, Port: p})
		}
	}
	return byPort, list
}

func writeForwardErr(w http.ResponseWriter, err error) {
	if errors.Is(err, firewall.ErrForwardPort) {
		detail := strings.TrimSpace(strings.TrimPrefix(err.Error(), firewall.ErrForwardPort.Error()+":"))
		writeJSON(w, http.StatusConflict, apiError{Code: firewall.ErrForwardPort.Error(), Params: map[string]any{"port": detail}})
		return
	}
	writeFWErr(w, err)
}

func (s *Server) handleForwards(w http.ResponseWriter, r *http.Request) {
	fs, err := s.Firewall.Forwards(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	_, ports := s.published(r)
	writeJSON(w, http.StatusOK, map[string]any{"items": fs, "containers": ports, "available": s.Firewall.Available()})
}

func (s *Server) handleSaveForward(w http.ResponseWriter, r *http.Request) {
	var in firewall.Forward
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.ID = idParam(r)
	used, _ := s.published(r)
	f, err := s.Firewall.SaveForward(r.Context(), in, used)
	if err != nil {
		writeForwardErr(w, err)
		return
	}
	s.auditCat(r, "security", "forward.save", strconv.Itoa(f.Listen)+"/"+f.Proto+" -> "+f.TargetHost+":"+strconv.Itoa(f.TargetPort))
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) handleDeleteForward(w http.ResponseWriter, r *http.Request) {
	if err := s.Firewall.DeleteForward(r.Context(), idParam(r)); err != nil {
		writeForwardErr(w, err)
		return
	}
	s.auditCat(r, "security", "forward.delete", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
