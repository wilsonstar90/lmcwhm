package server

import (
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/sysinfo"
)

// homeRoutes back the dashboard's status cards and the header's restart.
func (s *Server) homeRoutes(r chi.Router) {
	r.Get("/system/detail", s.handleSysDetail)
	r.Post("/system/memory/release", s.handleReleaseMemory)
	r.Post("/system/restart", s.handleRestart)
}

func (s *Server) handleSysDetail(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sysinfo.CollectDetail(r.Context()))
}

func (s *Server) handleReleaseMemory(w http.ResponseWriter, r *http.Request) {
	freed, err := sysinfo.ReleaseMemory(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "system", "system.release_memory", "")
	writeJSON(w, http.StatusOK, map[string]any{"freed": freed})
}

// handleRestart stops the panel gracefully; systemd (Restart=always) starts
// it again. Without systemd nothing would bring it back, so it refuses.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("INVOCATION_ID") == "" {
		writeErr(w, http.StatusBadRequest, errMaintUnsupported)
		return
	}
	s.auditCat(r, "system", "system.restart", "")
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	go func() {
		time.Sleep(500 * time.Millisecond)
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
}
