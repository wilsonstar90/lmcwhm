package server

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/dockermgr"
	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
)

func (s *Server) dockerRoutes(r chi.Router) {
	r.Get("/docker/status", s.handleDockerStatus)
	r.Get("/docker/overview", s.handleDockerOverview)
	r.Get("/docker/containers", s.handleContainers)
	r.Post("/docker/containers", s.handleCreateContainer)
	r.Post("/docker/containers/{id}/{action:start|stop|restart|pause|unpause|remove}", s.handleContainerAction)
	r.Get("/docker/containers/{id}/logs", s.handleContainerLogs)
	r.Get("/docker/containers/{id}/inspect", s.handleContainerInspect)
	r.Get("/docker/containers/{id}/info", s.handleContainerInfo)
	r.Get("/docker/containers/{id}/spec", s.handleContainerSpec)
	r.Get("/docker/containers/{id}/stats", s.handleContainerStats)
	r.Get("/docker/containers/{id}/logs/download", s.handleContainerLogsDownload)
	r.Post("/docker/containers/{id}/logs/clear", s.handleContainerLogsClear)
	r.Post("/docker/containers/{id}/rename", s.handleContainerRename)
	r.Post("/docker/containers/{id}/restart-policy", s.handleContainerRestartPolicy)
	r.Post("/docker/containers/{id}/commit", s.handleContainerCommit)
	r.Post("/docker/containers/{id}/recreate", s.handleContainerRecreate)
	r.Post("/docker/containers/{id}/upgrade", s.handleContainerUpgrade)
	r.Post("/docker/containers/{id}/networks/{action:connect|disconnect}", s.handleContainerNetwork)

	r.Get("/docker/registries", s.handleRegistries)
	r.Post("/docker/registries", s.handleRegistryLogin)
	r.Post("/docker/registries/logout", s.handleRegistryLogout)

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

// handleContainerLogs returns the last ?tail lines, optionally between
// ?since and ?until (unix seconds).
func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	until, _ := strconv.ParseInt(q.Get("until"), 10, 64)
	var out string
	var err error
	if since > 0 || until > 0 || q.Get("timestamps") == "1" {
		out, err = s.Docker.LogsRange(r.Context(), chi.URLParam(r, "id"), since, until, tailParam(r))
	} else {
		out, err = s.Docker.Logs(r.Context(), chi.URLParam(r, "id"), tailParam(r))
	}
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

func (s *Server) handleDockerOverview(w http.ResponseWriter, r *http.Request) {
	o, err := s.Docker.Overview(r.Context())
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) handleContainerInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.Docker.Info(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleContainerSpec(w http.ResponseWriter, r *http.Request) {
	in, err := s.Docker.Spec(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleContainerStats(w http.ResponseWriter, r *http.Request) {
	u, err := s.Docker.Stats(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleContainerLogsDownload(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	q := r.URL.Query()
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	until, _ := strconv.ParseInt(q.Get("until"), 10, 64)
	name := id + "-" + time.Now().Format("20060102-150405") + ".log"
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := s.Docker.StreamLogs(r.Context(), w, id, since, until); err != nil {
		s.Log.Warn("docker logs download", "id", id, "err", err)
	}
}

func (s *Server) handleContainerLogsClear(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Docker.ClearLogs(r.Context(), id); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.logs_clear", id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleContainerRename(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.Docker.Rename(r.Context(), id, strings.TrimSpace(in.Name)); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.rename", id+" → "+in.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleContainerRestartPolicy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Policy   string `json:"policy"`
		MaxRetry int    `json:"max_retry"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.Docker.SetRestart(r.Context(), id, in.Policy, in.MaxRetry); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.restart_policy", id+" "+in.Policy)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleContainerCommit(w http.ResponseWriter, r *http.Request) {
	var in struct{ Repository, Tag, Message string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id := chi.URLParam(r, "id")
	tid, err := s.Docker.Commit(r.Context(), id, strings.TrimSpace(in.Repository), strings.TrimSpace(in.Tag), in.Message)
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.commit", id+" → "+in.Repository+":"+in.Tag)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": tid})
}

func (s *Server) handleContainerRecreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		dockermgr.CreateInput
		Pull bool `json:"pull"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id := chi.URLParam(r, "id")
	tid, err := s.Docker.Recreate(r.Context(), id, in.CreateInput, in.Pull)
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.recreate", id+" "+in.Image)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": tid})
}

func (s *Server) handleContainerUpgrade(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Image     string `json:"image"`
		ForcePull bool   `json:"force_pull"`
		Force     bool   `json:"force_recreate"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id := chi.URLParam(r, "id")
	tid, err := s.Docker.Upgrade(r.Context(), id, in.Image, in.ForcePull, in.Force)
	if err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.upgrade", id+" "+in.Image)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": tid})
}

func (s *Server) handleContainerNetwork(w http.ResponseWriter, r *http.Request) {
	var in struct{ Network string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, action := chi.URLParam(r, "id"), chi.URLParam(r, "action")
	if err := s.Docker.Network(r.Context(), id, in.Network, action); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.network_"+action, id+" "+in.Network)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRegistries(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.Docker.Registries()})
}

func (s *Server) handleRegistryLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Server, Username, Password string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Docker.Login(r.Context(), in.Server, strings.TrimSpace(in.Username), in.Password); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.registry_login", in.Server+" "+in.Username)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRegistryLogout(w http.ResponseWriter, r *http.Request) {
	var in struct{ Server string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Docker.Logout(r.Context(), in.Server); err != nil {
		writeDockerErr(w, err)
		return
	}
	s.dockerAudit(r, "docker.registry_logout", in.Server)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
