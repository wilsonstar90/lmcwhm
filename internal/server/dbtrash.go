package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/backup"
	"github.com/wilsonstar90/lmcwhm/internal/database"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

// The database recycle bin (回收站 → 數據庫回收站).

func (s *Server) dbTrashRoutes(r chi.Router) {
	r.Get("/databases/trash", s.handleDBTrash)
	r.Post("/databases/trash/empty", s.handleDBTrashEmpty)
	r.Post("/databases/trash/{id}/restore", s.handleDBTrashRestore)
	r.Post("/databases/trash/{id}/delete", s.handleDBTrashDelete)
}

// keepDroppedDB is database.Service.BeforeDrop: a dump goes to the bin
// unless the bin is switched off.
func (s *Server) keepDroppedDB(ctx context.Context, d database.Database, log tasks.Logf) error {
	if !s.trashSettings(ctx).DB {
		return nil
	}
	return s.Backup.TrashDatabase(ctx, d, log)
}

func (s *Server) handleDBTrash(w http.ResponseWriter, r *http.Request) {
	items, err := s.Backup.TrashedDatabases(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func writeTrashErr(w http.ResponseWriter, err error) {
	if errors.Is(err, backup.ErrTrashNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeErr(w, http.StatusBadRequest, err)
}

func (s *Server) handleDBTrashRestore(w http.ResponseWriter, r *http.Request) {
	id := idParam(r)
	list, err := s.Backup.TrashedDatabases(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	name := ""
	for _, t := range list {
		if t.ID == id {
			name = t.Name
		}
	}
	if name == "" {
		writeTrashErr(w, backup.ErrTrashNotFound)
		return
	}
	tid := s.Tasks.Start("db.restore_trash:"+name, func(ctx context.Context, log tasks.Logf) error {
		return s.Backup.RestoreTrashedDB(ctx, id, log)
	})
	s.auditCat(r, "database", "db.trash_restore", name)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": tid})
}

func (s *Server) handleDBTrashDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Backup.PurgeTrashedDB(r.Context(), idParam(r)); err != nil {
		writeTrashErr(w, err)
		return
	}
	s.auditCat(r, "database", "db.trash_delete", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDBTrashEmpty(w http.ResponseWriter, r *http.Request) {
	if err := s.Backup.PurgeAllTrashedDBs(r.Context()); err != nil {
		writeTrashErr(w, err)
		return
	}
	s.auditCat(r, "database", "db.trash_empty", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
