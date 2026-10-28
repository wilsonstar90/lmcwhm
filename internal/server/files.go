package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/files"
	"github.com/wilsonstar90/lmcwhm/internal/terminal"
)

func (s *Server) fileRoutes(r chi.Router) {
	r.Get("/files/list", s.handleFileList)
	r.Get("/files/stat", s.handleFileStat)
	r.Get("/files/read", s.handleFileRead)
	r.Put("/files/write", s.handleFileWrite)
	r.Post("/files/create", s.handleFileCreate)
	r.Post("/files/rename", s.handleFileRename)
	r.Post("/files/copy", s.handleFileCopy)
	r.Post("/files/delete", s.handleFileDelete)
	r.Post("/files/chmod", s.handleFileChmod)
	r.Post("/files/chown", s.handleFileChown)
	r.Post("/files/compress", s.handleFileCompress)
	r.Post("/files/extract", s.handleFileExtract)
	r.Post("/files/upload", s.handleFileUpload)
	r.Get("/files/download", s.handleFileDownload)
	r.Get("/files/trash", s.handleTrash)
	r.Post("/files/trash/restore", s.handleTrashRestore)
	r.Post("/files/trash/empty", s.handleTrashEmpty)

	r.Post("/terminal/ticket", s.handleTerminalTicket)
	r.Get("/terminal/ws", s.handleTerminalWS)
}

func writeFileErr(w http.ResponseWriter, err error) {
	if errors.Is(err, files.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeErr(w, http.StatusBadRequest, err)
}

func (s *Server) fileAudit(r *http.Request, action, detail string) {
	s.auditCat(r, "files", action, detail)
}

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	dir := files.Clean(r.URL.Query().Get("path"))
	list, err := s.Files.List(dir)
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "items": list})
}

func (s *Server) handleFileStat(w http.ResponseWriter, r *http.Request) {
	e, err := s.Files.Stat(r.URL.Query().Get("path"))
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) {
	body, err := s.Files.ReadText(r.URL.Query().Get("path"))
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": body})
}

// decodeLarge is decode for editor saves, which may exceed the 1 MB default.
func decodeLarge(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 2*files.MaxEdit) // JSON escaping can double the size
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return errBadRequest
	}
	return nil
}

func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	var in struct{ Path, Content string }
	if err := decodeLarge(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Files.WriteText(in.Path, in.Content); err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.write", files.Clean(in.Path))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFileCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
		Dir  bool   `json:"dir"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Files.Create(in.Path, in.Dir); err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.create", files.Clean(in.Path))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFileRename(w http.ResponseWriter, r *http.Request) {
	var in struct{ From, To string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Files.Rename(in.From, in.To); err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.rename", files.Clean(in.From)+" → "+files.Clean(in.To))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFileCopy(w http.ResponseWriter, r *http.Request) {
	var in struct{ From, To string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Files.Copy(in.From, in.To); err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.copy", files.Clean(in.From)+" → "+files.Clean(in.To))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paths     []string `json:"paths"`
		Permanent bool     `json:"permanent"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Files.Delete(in.Paths, in.Permanent); err != nil {
		writeFileErr(w, err)
		return
	}
	action := "files.trash"
	if in.Permanent {
		action = "files.delete"
	}
	s.fileAudit(r, action, strings.Join(in.Paths, ", "))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFileChmod(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path      string `json:"path"`
		Mode      string `json:"mode"`
		Recursive bool   `json:"recursive"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Files.Chmod(in.Path, in.Mode, in.Recursive); err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.chmod", in.Mode+" "+files.Clean(in.Path))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFileChown(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path      string `json:"path"`
		Owner     string `json:"owner"`
		Group     string `json:"group"`
		Recursive bool   `json:"recursive"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Files.Chown(in.Path, in.Owner, in.Group, in.Recursive); err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.chown", in.Owner+":"+in.Group+" "+files.Clean(in.Path))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFileCompress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paths  []string `json:"paths"`
		Dest   string   `json:"dest"`
		Format string   `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Files.Compress(in.Paths, in.Dest, in.Format); err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.compress", files.Clean(in.Dest))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFileExtract(w http.ResponseWriter, r *http.Request) {
	var in struct{ Path, Dest string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Files.Extract(in.Path, in.Dest); err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.extract", files.Clean(in.Path)+" → "+files.Clean(in.Dest))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleFileUpload receives one raw chunk: ?path=<file>&offset=<n>. The
// client resumes an interrupted upload from /files/stat's size.
func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, err := strconv.ParseInt(q.Get("offset"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errBadRequest)
		return
	}
	body := http.MaxBytesReader(w, r.Body, 64<<20)
	size, err := s.Files.WriteChunk(q.Get("path"), offset, body)
	if err != nil {
		writeFileErr(w, err)
		return
	}
	if q.Get("last") == "1" {
		s.fileAudit(r, "files.upload", files.Clean(q.Get("path")))
	}
	writeJSON(w, http.StatusOK, map[string]int64{"size": size})
}

func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	f, fi, err := s.Files.Open(p)
	if err != nil {
		writeFileErr(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(files.Clean(p))}))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	items, err := s.Files.TrashList()
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "dir": s.Files.Trash})
}

func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	var in struct{ ID string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Files.Restore(in.ID); err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.restore", in.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	var in struct{ ID string }
	decode(r, &in)
	if err := s.Files.EmptyTrash(in.ID); err != nil {
		writeFileErr(w, err)
		return
	}
	s.fileAudit(r, "files.trash_empty", in.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- terminal ----

// handleTerminalTicket re-checks the password before a shell is opened.
func (s *Server) handleTerminalTicket(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password, Container string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sess := sessionFrom(r)
	if !s.Auth.CheckPassword(r.Context(), sess.User.ID, in.Password) {
		writeErr(w, http.StatusBadRequest, errBadPass)
		return
	}
	t, err := s.Term.Issue(sess.User.Username, in.Container)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ticket": t})
}

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	t, err := s.Term.Redeem(r.URL.Query().Get("ticket"), sess.User.Username)
	if err != nil {
		writeErr(w, http.StatusForbidden, err)
		return
	}
	// Accept verifies the Origin matches the Host (no cross-site WebSocket hijacking).
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	target := "host"
	if t.Container != "" {
		target = "container " + t.Container
	}
	s.auditCat(r, "terminal", "terminal.open", target)
	if err := terminal.Serve(r.Context(), c, t); err != nil {
		s.Log.Error("terminal", "err", err)
	}
}
