package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shirou/gopsutil/v4/disk"

	"github.com/wilsonstar90/lmcwhm/internal/files"
)

// File manager extras like aaPanel: remarks (written or automatic),
// directory sizes, editor history and the recycle bin switch.

func (s *Server) fileExtraRoutes(r chi.Router) {
	r.Put("/files/remark", s.handleFileRemark)
	r.Get("/files/size", s.handleFileSize)
	r.Get("/files/disk", s.handleFileDisk)
	r.Get("/files/history", s.handleFileHistory)
	r.Get("/files/history/read", s.handleFileHistoryRead)
	r.Post("/files/history/restore", s.handleFileHistoryRestore)
	r.Get("/files/trash/settings", s.handleTrashSettings)
	r.Put("/files/trash/settings", s.handlePutTrashSettings)
}

// listItem is a directory entry with its remark and protection state.
type listItem struct {
	files.Entry
	Remark    string            `json:"remark,omitempty"`
	Auto      *files.AutoRemark `json:"auto,omitempty"`
	Protected bool              `json:"protected,omitempty"` // under a tamper-protection rule
}

func (s *Server) decorate(ctx context.Context, dir string, list []files.Entry) []listItem {
	out := make([]listItem, len(list))
	remarks := map[string]string{}
	prefix := strings.TrimSuffix(dir, "/") + "/"
	if rows, err := s.Store.DB.QueryContext(ctx, `SELECT path, remark FROM file_remarks WHERE path LIKE ? ESCAPE '\'`,
		likeEscape(prefix)+"%"); err == nil {
		for rows.Next() {
			var p, r string
			rows.Scan(&p, &r)
			remarks[p] = r
		}
		rows.Close()
	}
	roots := map[string]string{}
	if s.Sites != nil {
		if sites, err := s.Sites.List(ctx); err == nil {
			for _, st := range sites {
				if st.Root != "" {
					roots[st.Root] = st.Domain
				}
			}
		}
	}
	var guarded []string
	if s.Tamper != nil {
		if rules, err := s.Tamper.List(ctx); err == nil {
			for _, r := range rules {
				if r.Enabled {
					guarded = append(guarded, files.Clean(r.Path))
				}
			}
		}
	}
	home := ""
	if s.Sites != nil && s.Sites.Env != nil {
		home = s.Sites.Env.Home
	}
	for i, e := range list {
		it := listItem{Entry: e, Remark: remarks[e.Path]}
		if d, ok := roots[e.Path]; ok {
			it.Auto = &files.AutoRemark{Code: "site_root", Arg: d}
		} else {
			it.Auto = files.SystemRemark(e.Path, home)
		}
		for _, g := range guarded {
			if e.Path == g || strings.HasPrefix(e.Path, g+"/") {
				it.Protected = true
			}
		}
		out[i] = it
	}
	return out
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Server) handleFileRemark(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path   string `json:"path"`
		Remark string `json:"remark"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p := files.Clean(in.Path)
	remark := strings.TrimSpace(in.Remark)
	if len(remark) > 500 || p == "/" {
		writeErr(w, http.StatusBadRequest, errBadRequest)
		return
	}
	var err error
	if remark == "" {
		_, err = s.Store.DB.ExecContext(r.Context(), `DELETE FROM file_remarks WHERE path = ?`, p)
	} else {
		_, err = s.Store.DB.ExecContext(r.Context(), `INSERT INTO file_remarks(path, remark, updated_at) VALUES(?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET remark = excluded.remark, updated_at = excluded.updated_at`, p, remark, time.Now().Unix())
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// moveRemarks follows a rename; to == "" drops the remarks (permanent delete).
func (s *Server) moveRemarks(ctx context.Context, from, to string) {
	from = files.Clean(from)
	if to == "" {
		s.Store.DB.ExecContext(ctx, `DELETE FROM file_remarks WHERE path = ? OR path LIKE ? ESCAPE '\'`, from, likeEscape(from+"/")+"%")
		return
	}
	to = files.Clean(to)
	s.Store.DB.ExecContext(ctx, `UPDATE OR REPLACE file_remarks SET path = ? || substr(path, ?) WHERE path = ? OR path LIKE ? ESCAPE '\'`,
		to, len(from)+1, from, likeEscape(from+"/")+"%")
}

func (s *Server) handleFileSize(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	size, n, done, err := s.Files.DirSize(ctx, r.URL.Query().Get("path"))
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"size": size, "files": n, "done": done})
}

// handleFileDisk reports the free space of the filesystem holding a path.
func (s *Server) handleFileDisk(w http.ResponseWriter, r *http.Request) {
	p := files.Clean(r.URL.Query().Get("path"))
	mount := "/"
	if parts, err := disk.PartitionsWithContext(r.Context(), false); err == nil {
		for _, pt := range parts {
			m := pt.Mountpoint
			if (p == m || strings.HasPrefix(p, strings.TrimSuffix(m, "/")+"/")) && len(m) > len(mount) {
				mount = m
			}
		}
	}
	u, err := disk.UsageWithContext(r.Context(), mount)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mount": mount, "free": u.Free, "total": u.Total})
}

func (s *Server) handleFileHistory(w http.ResponseWriter, r *http.Request) {
	vs, _ := s.Files.History(r.URL.Query().Get("path"))
	writeJSON(w, http.StatusOK, map[string]any{"items": vs})
}

func (s *Server) handleFileHistoryRead(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	body, err := s.Files.ReadVersion(q.Get("path"), q.Get("id"))
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": body})
}

func (s *Server) handleFileHistoryRestore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
		ID   string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	body, err := s.Files.ReadVersion(in.Path, in.ID)
	if err == nil {
		err = s.Files.WriteText(in.Path, body) // the current content becomes a version itself
	}
	if err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.history_restore", files.Clean(in.Path))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- recycle bin switch ----

const trashKey = "file_trash"

type trashSettings struct {
	Enabled bool `json:"enabled"` // off: deleting removes files at once
	DB      bool `json:"db"`      // dropped databases are kept as a dump
}

func (s *Server) trashSettings(ctx context.Context) trashSettings {
	t := trashSettings{Enabled: true, DB: true}
	var raw string
	if s.Store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, trashKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &t)
	}
	return t
}

func (s *Server) handleTrashSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.trashSettings(r.Context()))
}

func (s *Server) handlePutTrashSettings(w http.ResponseWriter, r *http.Request) {
	var in trashSettings
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	b, _ := json.Marshal(in)
	if _, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		trashKey, string(b)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.fileAudit(r, "files.trash_settings", string(b))
	writeJSON(w, http.StatusOK, in)
}
