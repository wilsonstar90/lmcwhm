package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

// The header's message box (消息盒子): running tasks, the history of
// finished ones and their logs.
const (
	keepTaskHistory = 500
	keepTaskLog     = 200 // lines
)

func (s *Server) taskRoutes(r chi.Router) {
	r.Get("/tasks", s.handleTaskList)
	r.Get("/tasks/history", s.handleTaskHistory)
	r.Get("/tasks/history/{id}", s.handleTaskHistoryGet)
	r.Post("/tasks/history/clear", s.handleTaskHistoryClear)
}

// recordTask is tasks.Manager.OnDone.
func (s *Server) recordTask(t tasks.Task) {
	log := t.Log
	if len(log) > keepTaskLog {
		log = log[len(log)-keepTaskLog:]
	}
	ctx := context.Background()
	if _, err := s.Store.DB.ExecContext(ctx, `INSERT OR REPLACE INTO task_history(id, name, status, error, started, finished, log)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, t.ID, t.Name, string(t.Status), t.Error, t.Started, t.Finished, strings.Join(log, "\n")); err != nil {
		s.Log.Warn("task history", "err", err)
		return
	}
	s.Store.DB.ExecContext(ctx, `DELETE FROM task_history WHERE id NOT IN (SELECT id FROM task_history ORDER BY finished DESC LIMIT ?)`, keepTaskHistory)
}

// runningTask is a row of 任務列表: a panel task, or a scheduled job running now.
type runningTask struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // task | cron
	Started int64  `json:"started"`
	JobID   int64  `json:"job_id,omitempty"`
}

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	run := []runningTask{}
	for _, t := range s.Tasks.List() {
		if t.Status == tasks.Running {
			run = append(run, runningTask{ID: t.ID, Name: t.Name, Kind: "task", Started: t.Started})
		}
	}
	if s.Cron != nil {
		if jobs, err := s.Cron.List(r.Context()); err == nil {
			for _, j := range jobs {
				if j.Running {
					started := int64(0)
					if j.Last != nil {
						started = j.Last.StartedAt
					}
					run = append(run, runningTask{ID: "cron-" + strconv.FormatInt(j.ID, 10), Name: j.Name, Kind: "cron", Started: started, JobID: j.ID})
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": run})
}

type taskRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	Started  int64  `json:"started"`
	Finished int64  `json:"finished"`
	Log      string `json:"log,omitempty"`
}

func (s *Server) handleTaskHistory(w http.ResponseWriter, r *http.Request) {
	size := intParam(r, "size", 10, 100)
	page := intParam(r, "page", 1, 1<<20)
	var total int
	s.Store.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM task_history`).Scan(&total)
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id, name, status, error, started, finished FROM task_history
		ORDER BY finished DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	items := []taskRow{}
	for rows.Next() {
		var t taskRow
		if rows.Scan(&t.ID, &t.Name, &t.Status, &t.Error, &t.Started, &t.Finished) == nil {
			items = append(items, t)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleTaskHistoryGet(w http.ResponseWriter, r *http.Request) {
	var t taskRow
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT id, name, status, error, started, finished, log FROM task_history WHERE id = ?`,
		chi.URLParam(r, "id")).Scan(&t.ID, &t.Name, &t.Status, &t.Error, &t.Started, &t.Finished, &t.Log)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, apiError{Code: "common.not_found"})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTaskHistoryClear(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM task_history`); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.auditCat(r, "system", "tasks.clear_history", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
