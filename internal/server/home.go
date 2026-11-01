package server

import (
	"net/http"
	"os"
	"os/exec"
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
	r.Post("/system/reboot", s.handleReboot)
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

// handleReboot restarts the whole server (重啟伺服器); it needs the user's
// password, as every site and app goes down for a minute or two.
func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !s.Auth.CheckPassword(r.Context(), sessionFrom(r).User.ID, in.Password) {
		writeErr(w, http.StatusBadRequest, errBadPass)
		return
	}
	bin, err := exec.LookPath("systemctl")
	if err != nil || os.Geteuid() != 0 {
		writeErr(w, http.StatusBadRequest, errMaintUnsupported)
		return
	}
	s.auditCat(r, "system", "system.reboot", "")
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	go func() {
		time.Sleep(time.Second) // let the answer reach the browser
		if out, err := exec.Command(bin, "reboot").CombinedOutput(); err != nil {
			s.Log.Error("reboot", "err", err, "out", string(out))
		}
	}()
}
