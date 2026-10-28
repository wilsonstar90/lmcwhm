package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/dockermgr"
	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
)

func (s *Server) dockerRoutes(r chi.Router) {
	r.Get("/docker/status", s.handleDockerStatus)
	r.Get("/docker/containers", s.handleContainers)
	r.Post("/docker/containers", s.handleCreateContainer)
	r.Post("/docker/containers/{id}/{action:start|stop|restart|pause|unpause|remove}", s.handleContainerAction)
	r.Get("/docker/containers/{id}/logs", s.handleContainerLogs)
	r.Get("/docker/containers/{id}/inspect", s.handleContainerInspect)

	r.Get("/docker/images", s.handleImages)
	r.Post("/docker/images/pull", s.handlePullImage)
	r.Post("/docker/images/remove", s.handleRemoveImage)
	r.Post("/docker/prune/{what}", s.handlePrune)

	r.Get("/docker/volumes", s.handleVolumes)
	r.Post("/docker/volumes", s.handleCreateVolume)
	r.Post("/docker/volumes/{name}/remove", s.handleRemoveVolume)
	r.Get("/docker/networks", s.handleNetworks)
	r.Post("/docker/networks", s.handleCreateNetwork)
	r.Post("/docker/networks/{name}/remove", s.handleRemoveNetwork)

	r.Get("/docker/compose", s.handleProjects)
	r.Post("/docker/compose", s.handleCreateProject)
	r.Get("/docker/compose/{name}", s.handleProjectFiles)
	r.Put("/docker/compose/{name}", s.handleSaveProject)
	r.Post("/docker/compose/{name}/{action:up|down|restart|pull}", s.handleProjectAction)
	r.Post("/docker/compose/{name}/delete", s.handleDeleteProject)
	r.Get("/docker/compose/{name}/logs", s.handleProjectLogs)
}

// writeDockerErr exposes docker's own message (e.g. "image is in use") with a stable code.
func writeDockerErr(w http.ResponseWriter, err error) {
	var msg string
	if !strings.Contains(err.Error(), ".") || strings.Contains(err.Error(), " ") {
		msg = err.Error()
	}
	switch {
	case errors.Is(err, dockermgr.ErrNoProject):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, dockermgr.ErrBadCompose):
		writeJSON(w, http.StatusBadRequest, apiError{Code: dockermgr.ErrBadCompose.Error(),
			Params: map[string]any{"detail": strings.TrimPrefix(err.Error(), dockermgr.ErrBadCompose.Error()+": ")}})
	case msg != "":
		writeJSON(w, http.StatusBadRequest, apiError{Code: "docker.failed", Params: map[string]any{"detail": msg}})
	default:
		writeErr(w, http.StatusBadRequest, err)
	}
}

func (s *Server) dockerAudit(r *http.Request, action, detail string) {
	s.auditCat(r, "docker", action, detail)
}

func (s *Server) handleDockerStatus(w http.ResponseWriter, r *http.Request) {
	v, err := dockerx.Run(r.Context(), "version", "--format", "{{.Server.Version}}")
	writeJSON(w, http.StatusOK, map[string]any{"available": err == nil, "version": v})
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Docker.Containers(r.Context(), r.URL.Query().Get("stats") == "1")
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleCreateContainer(w http.ResponseWriter, r *http.Request) {
	var in dockermgr.CreateInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Docker.Create(r.Context(), in)
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.create", in.Image+" "+in.Name)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	id, action := chi.URLParam(r, "id"), chi.URLParam(r, "action")
	if err := s.Docker.Action(r.Context(), id, action); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker."+action, id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	out, err := s.Docker.Logs(r.Context(), chi.URLParam(r, "id"), tailParam(r))
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": out})
}

func (s *Server) handleContainerInspect(w http.ResponseWriter, r *http.Request) {
	out, err := s.Docker.Inspect(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"inspect": out})
}

func (s *Server) handleImages(w http.ResponseWriter, r *http.Request) {
	list, err := s.Docker.Images(r.Context())
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handlePullImage(w http.ResponseWriter, r *http.Request) {
	var in struct{ Image string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Docker.Pull(r.Context(), strings.TrimSpace(in.Image))
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.pull", in.Image)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleRemoveImage(w http.ResponseWriter, r *http.Request) {
	var in struct{ ID string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Docker.RemoveImage(r.Context(), in.ID); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.rmi", in.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePrune(w http.ResponseWriter, r *http.Request) {
	what := chi.URLParam(r, "what")
	out, err := s.Docker.Prune(r.Context(), what)
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.prune", what)
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

func (s *Server) handleVolumes(w http.ResponseWriter, r *http.Request) {
	list, err := s.Docker.Volumes(r.Context())
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleCreateVolume(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Docker.CreateVolume(r.Context(), in.Name); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.volume_create", in.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRemoveVolume(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := s.Docker.RemoveVolume(r.Context(), name); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.volume_remove", name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleNetworks(w http.ResponseWriter, r *http.Request) {
	list, err := s.Docker.Networks(r.Context())
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Subnet string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Docker.CreateNetwork(r.Context(), in.Name, strings.TrimSpace(in.Subnet)); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.network_create", in.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRemoveNetwork(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := s.Docker.RemoveNetwork(r.Context(), name); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.network_remove", name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.Docker.Projects(r.Context())
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		dockermgr.ProjectFiles
		Up bool `json:"up"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Docker.SaveProject(r.Context(), in.Name, in.ProjectFiles, true); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "compose.create", in.Name)
	resp := map[string]string{}
	if in.Up {
		if id, err := s.Docker.ProjectAction(r.Context(), in.Name, "up"); err == nil {
			resp["task_id"] = id
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleProjectFiles(w http.ResponseWriter, r *http.Request) {
	f, err := s.Docker.ProjectFiles(chi.URLParam(r, "name"))
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) handleSaveProject(w http.ResponseWriter, r *http.Request) {
	var in dockermgr.ProjectFiles
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	name := chi.URLParam(r, "name")
	if err := s.Docker.SaveProject(r.Context(), name, in, false); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "compose.save", name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleProjectAction(w http.ResponseWriter, r *http.Request) {
	name, action := chi.URLParam(r, "name"), chi.URLParam(r, "action")
	id, err := s.Docker.ProjectAction(r.Context(), name, action)
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "compose."+action, name)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Volumes bool `json:"volumes"`
	}
	decode(r, &in)
	name := chi.URLParam(r, "name")
	id, err := s.Docker.DeleteProject(r.Context(), name, in.Volumes)
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "compose.delete", name)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleProjectLogs(w http.ResponseWriter, r *http.Request) {
	out, err := s.Docker.ProjectLogs(r.Context(), chi.URLParam(r, "name"), tailParam(r))
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": out})
}
