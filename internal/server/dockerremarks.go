package server

// Remarks on Docker containers and compose projects. Docker keeps neither,
// so they live in docker_remarks, keyed by name (a recreated container keeps
// its name, not its id).

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

var errBadRemark = errors.New("docker.bad_remark")

// dockerRemarks returns the remarks of one kind (container | project) by name.
func (s *Server) dockerRemarks(ctx context.Context, kind string) map[string]string {
	out := map[string]string{}
	if s.Store == nil {
		return out
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT name, remark FROM docker_remarks WHERE kind = ?`, kind)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n, r string
		if rows.Scan(&n, &r) == nil {
			out[n] = r
		}
	}
	return out
}

func (s *Server) setDockerRemark(ctx context.Context, kind, name, remark string) error {
	remark = strings.TrimSpace(remark)
	if r := []rune(remark); len(r) > 200 {
		remark = string(r[:200])
	}
	if remark == "" {
		_, err := s.Store.DB.ExecContext(ctx, `DELETE FROM docker_remarks WHERE kind = ? AND name = ?`, kind, name)
		return err
	}
	_, err := s.Store.DB.ExecContext(ctx, `INSERT INTO docker_remarks(kind, name, remark, updated_at) VALUES(?,?,?,?)
		ON CONFLICT(kind, name) DO UPDATE SET remark = excluded.remark, updated_at = excluded.updated_at`, kind, name, remark, time.Now().Unix())
	return err
}

// renameDockerRemark follows a renamed container ("" new name = removed).
func (s *Server) renameDockerRemark(ctx context.Context, kind, old, name string) {
	if s.Store == nil || old == "" {
		return
	}
	if name == "" {
		s.Store.DB.ExecContext(ctx, `DELETE FROM docker_remarks WHERE kind = ? AND name = ?`, kind, old)
		return
	}
	s.Store.DB.ExecContext(ctx, `UPDATE OR REPLACE docker_remarks SET name = ? WHERE kind = ? AND name = ?`, name, kind, old)
}

// containerName resolves a container id (or name) to its name.
func (s *Server) containerName(ctx context.Context, id string) string {
	list, _ := s.Docker.Containers(ctx, false)
	for _, c := range list {
		if c.Name == id || (len(id) >= 12 && strings.HasPrefix(c.ID, id)) {
			return c.Name
		}
	}
	return ""
}

func (s *Server) handleDockerRemark(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Remark string `json:"remark"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if (in.Kind != "container" && in.Kind != "project") || in.Name == "" || len(in.Name) > 255 || s.Store == nil {
		writeErr(w, http.StatusBadRequest, errBadRemark)
		return
	}
	if err := s.setDockerRemark(r.Context(), in.Kind, in.Name, in.Remark); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
