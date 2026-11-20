package server

import (
	"context"
	"net/http"

	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

func (s *Server) handleBackupEncryptionGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Backup.EncryptionStatus())
}

func (s *Server) handleBackupEncryptionPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out := s.Backup.EncryptionStatus()
	if in.Enabled {
		key, err := s.Backup.EnableEncryption()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out = s.Backup.EncryptionStatus()
		out["recovery_key"] = key
		s.auditCat(r, "backup", "backup.encryption.enable", "key generated or re-enabled")
	} else {
		if err := s.Backup.DisableEncryption(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out = s.Backup.EncryptionStatus()
		s.auditCat(r, "backup", "backup.encryption.disable", "key retained for old backups")
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleBackupDrillStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.Backup.DrillStatus(r.Context(), idParam(r))
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleBackupDrill(w http.ResponseWriter, r *http.Request) {
	rec, err := s.Backup.Get(r.Context(), idParam(r))
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	id := s.Tasks.StartLong("restore drill "+rec.Name, backupTimeout, func(ctx context.Context, log tasks.Logf) error {
		return s.Backup.Drill(ctx, rec.ID)
	})
	s.auditCat(r, "backup", "backup.drill", rec.Name)
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}
