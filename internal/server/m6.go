package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/backup"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

func (s *Server) m6Routes(r chi.Router) {
	if s.Backup == nil {
		return
	}
	r.Get("/backups", s.handleBackups)
	r.Get("/backups/targets", s.handleBackupTargets)
	r.Post("/backups/run", s.handleBackupRun)
	r.Post("/backups/import", s.handleBackupImport)
	r.Get("/backups/{id}/download", s.handleBackupDownload)
	r.Post("/backups/{id}/restore", s.handleBackupRestore)
	r.Post("/backups/{id}/delete", s.handleBackupDelete)
	r.Get("/backups/remotes", s.handleRemotes)
	r.Post("/backups/remotes", s.handleRemoteAdd)
	r.Post("/backups/remotes/{id}/poll", s.handleRemotePoll)
	r.Post("/backups/remotes/{id}/reauthorize", s.handleRemoteReauth)
	r.Post("/backups/remotes/{id}/delete", s.handleRemoteDelete)
}

func writeBackupErr(w http.ResponseWriter, err error) {
	if errors.Is(err, backup.ErrRemoteAuth) {
		writeJSON(w, http.StatusBadRequest, apiError{Code: backup.ErrRemoteAuth.Error(), Params: map[string]any{"value": err.Error()}})
		return
	}
	writeCodedErr(w, err, backup.ErrNotFound)
}

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	recs, err := s.Backup.List(r.Context(), r.URL.Query().Get("kind"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var total int64
	for _, rec := range recs {
		if rec.Dest == backup.DestLocal {
			total += rec.Size
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": recs, "dir": s.Backup.LocalDir(), "local_bytes": total})
}

// handleBackupTargets lists what can be backed up, for the plan form.
func (s *Server) handleBackupTargets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	type opt struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	out := map[string][]opt{"site": {}, "database": {}, "volume": {}, "dest": {{"local", backup.DestLocal}}}
	if s.Sites != nil {
		if sites, err := s.Sites.List(ctx); err == nil {
			for _, x := range sites {
				out["site"] = append(out["site"], opt{x.Domain, x.Domain})
			}
		}
	}
	if s.DBs != nil {
		if dbs, err := s.DBs.List(ctx); err == nil {
			for _, d := range dbs {
				out["database"] = append(out["database"], opt{fmt.Sprintf("%s (%s %s)", d.Name, d.Engine, d.Version), strconv.FormatInt(d.ID, 10)})
			}
		}
	}
	if s.Docker != nil {
		if vols, err := s.Docker.Volumes(ctx); err == nil {
			for _, v := range vols {
				out["volume"] = append(out["volume"], opt{v.Name, v.Name})
			}
		}
	}
	if rems, err := s.Backup.Remotes(ctx); err == nil {
		for _, rm := range rems {
			if rm.Authorized {
				out["dest"] = append(out["dest"], opt{rm.Name, fmt.Sprintf("remote:%d", rm.ID)})
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// backupTimeout bounds a manual backup or restore.
const backupTimeout = 12 * time.Hour

func (s *Server) handleBackupRun(w http.ResponseWriter, r *http.Request) {
	var p backup.Plan
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	b, _ := json.Marshal(p)
	plan, err := s.Backup.ParsePlan(r.Context(), string(b))
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	id := s.Tasks.StartLong("backup "+plan.Kind+" "+plan.Target, backupTimeout, func(ctx context.Context, log tasks.Logf) error {
		return s.Backup.Run(ctx, plan, 0, logWriter(log))
	})
	s.auditCat(r, "backup", "backup.run", plan.Kind+" "+plan.Target)
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

// logWriter turns task log lines into an io.Writer.
func logWriter(log tasks.Logf) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		for _, line := range splitLines(string(p)) {
			log("%s", line)
		}
		return len(p), nil
	})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// handleBackupImport registers an archive already on the server (uploaded
// with the file manager) as a local backup.
func (s *Server) handleBackupImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind     string `json:"kind"`
		Target   string `json:"target"`
		TargetID int64  `json:"target_id"`
		Path     string `json:"path"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Kind == backup.KindDatabase && s.DBs != nil {
		d, err := s.DBs.Get(r.Context(), in.TargetID)
		if err != nil {
			writeBackupErr(w, backup.ErrBadTarget)
			return
		}
		in.Target = d.Name
	}
	if !filepath.IsAbs(in.Path) || in.Target == "" && in.Kind != backup.KindPanel {
		writeErr(w, http.StatusBadRequest, errBadRequest)
		return
	}
	f, err := os.Open(filepath.Clean(in.Path))
	if err != nil {
		writeErr(w, http.StatusBadRequest, backup.ErrNotFound)
		return
	}
	defer f.Close()
	rec, err := s.Backup.Import(r.Context(), in.Kind, in.Target, in.TargetID, filepath.Base(in.Path), f)
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	s.auditCat(r, "backup", "backup.import", in.Path)
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	rec, err := s.Backup.Get(r.Context(), idParam(r))
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	rc, err := s.Backup.Open(r.Context(), rec)
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": rec.Name}))
	w.Header().Set("Content-Length", strconv.FormatInt(rec.Size, 10))
	io.Copy(w, rc)
	s.auditCat(r, "backup", "backup.download", rec.Name)
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Into int64 `json:"into"`
	}
	decode(r, &in)
	rec, err := s.Backup.Get(r.Context(), idParam(r))
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	if rec.Kind == backup.KindPanel {
		writeErr(w, http.StatusBadRequest, backup.ErrNoRestore)
		return
	}
	id := s.Tasks.StartLong("restore "+rec.Name, backupTimeout, func(ctx context.Context, log tasks.Logf) error {
		if err := s.Backup.Restore(ctx, rec.ID, in.Into, log); err != nil {
			return err
		}
		log("restore finished")
		return nil
	})
	s.auditCat(r, "backup", "backup.restore", rec.Name)
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	rec, err := s.Backup.Get(r.Context(), idParam(r))
	if err == nil {
		err = s.Backup.Delete(r.Context(), rec.ID)
	}
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	s.auditCat(r, "backup", "backup.delete", rec.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRemotes(w http.ResponseWriter, r *http.Request) {
	rems, err := s.Backup.Remotes(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rems})
}

func (s *Server) handleRemoteAdd(w http.ResponseWriter, r *http.Request) {
	var in backup.RemoteInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	v, err := s.Backup.AddRemote(r.Context(), in)
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	s.auditCat(r, "backup", "backup.remote_add", in.Kind+" "+in.Name)
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleRemotePoll(w http.ResponseWriter, r *http.Request) {
	v, err := s.Backup.PollRemote(r.Context(), idParam(r))
	switch {
	case errors.Is(err, backup.ErrAuthPending):
		writeJSON(w, http.StatusOK, map[string]any{"pending": true, "remote": v})
	case errors.Is(err, backup.ErrAuthExpired):
		writeJSON(w, http.StatusOK, map[string]any{"expired": true, "remote": v})
	case err != nil:
		writeBackupErr(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"remote": v})
	}
}

func (s *Server) handleRemoteReauth(w http.ResponseWriter, r *http.Request) {
	v, err := s.Backup.Reauthorize(r.Context(), idParam(r))
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleRemoteDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Backup.DeleteRemote(r.Context(), idParam(r)); err != nil {
		writeBackupErr(w, err)
		return
	}
	s.auditCat(r, "backup", "backup.remote_delete", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
