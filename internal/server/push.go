package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/wilsonstar90/lmcwhm/internal/alert"
	"github.com/wilsonstar90/lmcwhm/internal/push"
)

func (s *Server) pushRoutes(r chi.Router) {
	if s.Push == nil {
		return
	}
	r.Get("/push/config", s.handlePushConfig)
	r.Get("/push/devices", s.handlePushDevices)
	r.Post("/push/subscribe", s.handlePushSubscribe)
	r.Post("/push/devices/{id}/delete", s.handlePushRevoke)
	r.Post("/push/devices/{id}/test", s.handlePushTest)
}

func (s *Server) handlePushConfig(w http.ResponseWriter, r *http.Request) {
	key, err := s.Push.VAPIDPublic(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vapid_public_key": key, "ios_min_version": "16.4"})
}

func (s *Server) handlePushDevices(w http.ResponseWriter, r *http.Request) {
	items, err := s.Push.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var in push.SubscribeInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	device, err := s.Push.Subscribe(r.Context(), in)
	if err != nil {
		writeCodedErr(w, err, push.ErrSubscription)
		return
	}
	s.auditCat(r, "panel", "push.subscribe", in.Name)
	writeJSON(w, http.StatusOK, device)
}

func (s *Server) handlePushRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.Push.Revoke(r.Context(), idParam(r)); err != nil {
		writeCodedErr(w, err, push.ErrNotFound)
		return
	}
	s.audit(r, "push.revoke", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	id := idParam(r)
	err := s.Push.Send(r.Context(), []int64{id}, alert.Message{Title: "lmcwhm test notification", Body: "App Push works for this device.", Severity: "info", Kind: "test", Host: "lmcwhm", Time: time.Now().Unix()})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.audit(r, "push.test", strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
