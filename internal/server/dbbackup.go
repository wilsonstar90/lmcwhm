package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/backup"
	"github.com/wilsonstar90/lmcwhm/internal/database"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

// Database backups and import on the database page (like aaPanel): the
// routes sit under /databases so the database permission is enough.

const maxImportUpload = 20 << 30 // 20 GiB

func (s *Server) dbBackupRoutes(r chi.Router) {
	r.Get("/databases/backup-dests", s.handleDBBackupDests)
	r.Get("/databases/{id}/backups", s.handleDBBackups)
	r.Post("/databases/{id}/backups/run", s.handleDBBackupRun)
	r.Post("/databases/{id}/backups/delete", s.handleDBBackupDelete)
	r.Post("/databases/{id}/backups/{bid}/restore", s.handleDBBackupRestore)
	r.Get("/databases/{id}/backups/{bid}/download", s.handleDBBackupDownload)
	r.Get("/databases/import-files", s.handleImportFiles)
	r.Post("/databases/import-files/delete", s.handleImportFileDelete)
	r.Get("/databases/import-upload", s.handleImportUploadSize)
	r.Post("/databases/import-upload", s.handleImportUpload)
	r.Post("/databases/{id}/import", s.handleDBImport)
}

func (s *Server) dbOf(w http.ResponseWriter, r *http.Request) (database.Database, bool) {
	d, err := s.DBs.Get(r.Context(), idParam(r))
	if err != nil {
		writeCodedErr(w, err, database.ErrNotFound)
		return d, false
	}
	return d, true
}

// dbBackups are the backup records of database id, newest first.
func (s *Server) dbBackups(ctx context.Context, id int64) ([]backup.Record, error) {
	all, err := s.Backup.List(ctx, backup.KindDatabase)
	if err != nil {
		return nil, err
	}
	out := []backup.Record{}
	for _, rec := range all {
		if rec.TargetID == id {
			out = append(out, rec)
		}
	}
	return out, nil
}

// dbBackup returns a backup of the database in the URL.
func (s *Server) dbBackup(w http.ResponseWriter, r *http.Request, bid int64) (backup.Record, bool) {
	rec, err := s.Backup.Get(r.Context(), bid)
	if err != nil || rec.Kind != backup.KindDatabase || rec.TargetID != idParam(r) {
		writeBackupErr(w, backup.ErrNotFound)
		return rec, false
	}
	return rec, true
}

func (s *Server) handleDBBackupDests(w http.ResponseWriter, r *http.Request) {
	type dest struct {
		Value string `json:"value"`
		Name  string `json:"name"`
		Kind  string `json:"kind"`
	}
	out := []dest{{Value: backup.DestLocal, Name: "local", Kind: "local"}}
	if rems, err := s.Backup.Remotes(r.Context()); err == nil {
		for _, x := range rems {
			if x.Authorized {
				out = append(out, dest{Value: "remote:" + strconv.FormatInt(x.ID, 10), Name: x.Name, Kind: x.Kind})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleDBBackups(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.dbOf(w, r); !ok {
		return
	}
	recs, err := s.dbBackups(r.Context(), idParam(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": recs})
}

func (s *Server) handleDBBackupRun(w http.ResponseWriter, r *http.Request) {
	d, ok := s.dbOf(w, r)
	if !ok {
		return
	}
	var in struct {
		Dests []string `json:"dests"`
	}
	decode(r, &in)
	if len(in.Dests) == 0 {
		in.Dests = []string{backup.DestLocal}
	}
	b, _ := json.Marshal(backup.Plan{Kind: backup.KindDatabase, Target: strconv.FormatInt(d.ID, 10), Dests: in.Dests})
	plan, err := s.Backup.ParsePlan(r.Context(), string(b))
	if err != nil {
		writeBackupErr(w, err)
		return
	}
	id := s.Tasks.StartLong("backup.run:"+d.Name, backupTimeout, func(ctx context.Context, log tasks.Logf) error {
		return s.Backup.Run(ctx, plan, 0, logWriter(log))
	})
	s.auditCat(r, "database", "db.backup", d.Name)
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

func (s *Server) handleDBBackupDelete(w http.ResponseWriter, r *http.Request) {
	d, ok := s.dbOf(w, r)
	if !ok {
		return
	}
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil || len(in.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, errBadRequest)
		return
	}
	for _, bid := range in.IDs {
		rec, err := s.Backup.Get(r.Context(), bid)
		if err != nil || rec.Kind != backup.KindDatabase || rec.TargetID != d.ID {
			writeBackupErr(w, backup.ErrNotFound)
			return
		}
		if err := s.Backup.Delete(r.Context(), bid); err != nil {
			writeBackupErr(w, err)
			return
		}
		s.auditCat(r, "database", "db.backup_delete", rec.Name)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDBBackupRestore(w http.ResponseWriter, r *http.Request) {
	d, ok := s.dbOf(w, r)
	if !ok {
		return
	}
	bid, _ := strconv.ParseInt(chi.URLParam(r, "bid"), 10, 64)
	rec, ok := s.dbBackup(w, r, bid)
	if !ok {
		return
	}
	var in struct {
		BackupFirst bool `json:"backup_first"`
	}
	decode(r, &in)
	id := s.Tasks.StartLong("backup.restore:"+d.Name, backupTimeout, func(ctx context.Context, log tasks.Logf) error {
		if in.BackupFirst {
			log("backing up %s before the restore", d.Name)
			if err := s.Backup.Run(ctx, backup.Plan{Kind: backup.KindDatabase, Target: strconv.FormatInt(d.ID, 10), Dests: []string{backup.DestLocal}}, 0, logWriter(log)); err != nil {
				return err
			}
		}
		if err := s.Backup.Restore(ctx, rec.ID, 0, log); err != nil {
			return err
		}
		log("restore finished")
		return nil
	})
	s.auditCat(r, "database", "db.restore", rec.Name)
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

func (s *Server) handleDBBackupDownload(w http.ResponseWriter, r *http.Request) {
	bid, _ := strconv.ParseInt(chi.URLParam(r, "bid"), 10, 64)
	rec, ok := s.dbBackup(w, r, bid)
	if !ok {
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
	s.auditCat(r, "database", "db.backup_download", rec.Name)
}

// ---- import ----

func (s *Server) handleImportFiles(w http.ResponseWriter, r *http.Request) {
	files, err := s.Backup.ImportFiles(r.URL.Query().Get("q"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": files, "dir": s.Backup.ImportDir(), "upload_dir": s.Backup.UploadDir()})
}

func (s *Server) handleImportFileDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := decode(r, &in); err != nil || !s.Backup.InImportDir(in.Path) || backup.ImportExt(in.Path) == "" {
		writeErr(w, http.StatusBadRequest, backup.ErrNotFound)
		return
	}
	// A file that is a stored backup goes through the backup records.
	if recs, err := s.Backup.List(r.Context(), backup.KindDatabase); err == nil {
		for _, rec := range recs {
			if rec.Dest == backup.DestLocal && rec.Location == filepath.Clean(in.Path) {
				if err := s.Backup.Delete(r.Context(), rec.ID); err != nil {
					writeBackupErr(w, err)
					return
				}
				s.auditCat(r, "database", "db.import_file_delete", in.Path)
				writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
				return
			}
		}
	}
	if err := os.Remove(filepath.Clean(in.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.auditCat(r, "database", "db.import_file_delete", in.Path)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleImportUploadSize returns how much of an upload is on the server, to resume it.
func (s *Server) handleImportUploadSize(w http.ResponseWriter, r *http.Request) {
	name, ok := backup.UploadName(r.URL.Query().Get("name"))
	if !ok {
		writeErr(w, http.StatusBadRequest, backup.ErrBadName)
		return
	}
	var size int64
	if fi, err := os.Stat(filepath.Join(s.Backup.UploadDir(), name+".part")); err == nil {
		size = fi.Size()
	}
	_, done := os.Stat(filepath.Join(s.Backup.UploadDir(), name))
	writeJSON(w, http.StatusOK, map[string]any{"size": size, "exists": done == nil})
}

// handleImportUpload appends a chunk at offset to <upload>/<name>.part; the
// last chunk renames it into place (replacing a file of that name).
func (s *Server) handleImportUpload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name, ok := backup.UploadName(q.Get("name"))
	offset, err := strconv.ParseInt(q.Get("offset"), 10, 64)
	if !ok || err != nil || offset < 0 {
		writeErr(w, http.StatusBadRequest, backup.ErrBadName)
		return
	}
	last := q.Get("last") == "1"
	size, err := s.Backup.WriteChunk(name, offset, maxImportUpload, r.Body, last)
	switch {
	case errors.Is(err, backup.ErrOffset):
		writeJSON(w, http.StatusConflict, map[string]any{"code": "files.bad_offset", "size": size})
		return
	case errors.Is(err, backup.ErrBadName):
		writeErr(w, http.StatusRequestEntityTooLarge, err)
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if last {
		s.auditCat(r, "database", "db.import_upload", name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"size": size})
}

func (s *Server) handleDBImport(w http.ResponseWriter, r *http.Request) {
	d, ok := s.dbOf(w, r)
	if !ok {
		return
	}
	var in struct {
		Path        string `json:"path"`
		BackupFirst bool   `json:"backup_first"`
	}
	if err := decode(r, &in); err != nil || !filepath.IsAbs(in.Path) || backup.ImportExt(in.Path) == "" {
		writeErr(w, http.StatusBadRequest, backup.ErrImportFormat)
		return
	}
	if fi, err := os.Stat(in.Path); err != nil || !fi.Mode().IsRegular() {
		writeErr(w, http.StatusBadRequest, backup.ErrNotFound)
		return
	}
	path := filepath.Clean(in.Path)
	id := s.Tasks.StartLong("db.import:"+d.Name, backupTimeout, func(ctx context.Context, log tasks.Logf) error {
		return s.Backup.ImportDatabase(ctx, d.ID, path, in.BackupFirst, log)
	})
	s.auditCat(r, "database", "db.import", d.Name+" < "+path)
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}
