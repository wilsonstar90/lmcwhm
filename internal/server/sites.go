package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/cfdns"
	"github.com/wilsonstar90/lmcwhm/internal/netinfo"
	"github.com/wilsonstar90/lmcwhm/internal/site"
	"github.com/wilsonstar90/lmcwhm/internal/webcfg"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

func (s *Server) siteRoutes(r chi.Router) {
	r.Get("/tasks/{id}", s.handleTask)

	r.Get("/web/status", s.handleWebStatus)
	r.Get("/web/config", s.handleWebConfig)
	r.Put("/web/config", s.handlePutWebConfig)
	r.Post("/web/cf/verify", s.handleCFVerify)
	r.Get("/web/ips", s.handleWebIPs)
	r.Post("/web/install", s.handleWebInstall)

	r.Get("/sites", s.handleSites)
	r.Post("/sites", s.handleCreateSite)
	r.Get("/sites/{id}", s.handleSite)
	r.Patch("/sites/{id}", s.handleUpdateSite)
	r.Post("/sites/{id}/delete", s.handleDeleteSite)
	r.Post("/sites/{id}/ssl/issue", s.handleIssueSSL)
	r.Post("/sites/{id}/ssl/upload", s.handleUploadSSL)
	r.Post("/sites/{id}/ssl/disable", s.handleDisableSSL)
	r.Get("/sites/{id}/vhost", s.handleVhost)
}

// writeSiteErr maps service errors (some wrapped with a detail) to API codes.
func writeSiteErr(w http.ResponseWriter, err error) {
	if errors.Is(err, site.ErrDomainTaken) {
		host := strings.TrimSpace(strings.TrimPrefix(err.Error(), site.ErrDomainTaken.Error()+":"))
		writeJSON(w, http.StatusConflict, apiError{Code: site.ErrDomainTaken.Error(), Params: map[string]any{"domain": host}})
		return
	}
	if errors.Is(err, site.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeErr(w, http.StatusBadRequest, err)
}

func idParam(r *http.Request) int64 {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id
}

func (s *Server) audit(r *http.Request, action, detail string) {
	s.Audit.Record(r.Context(), sessionFrom(r).User.Username, clientIP(r), "site", action, detail)
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Tasks.Get(chi.URLParam(r, "id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Code: "common.not_found"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// ---- web environment ----

func (s *Server) handleWebStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": s.Sites.Env.Status(r.Context()), "php_versions": webenv.PHPVersions,
	})
}

// webConfigView never returns secrets, only whether they are set.
type webConfigView struct {
	HasCFToken    bool   `json:"has_cf_token"`
	ACMEEmail     string `json:"acme_email"`
	ACMECA        string `json:"acme_ca"`
	ACMEDirectory string `json:"acme_directory"`
	EABKid        string `json:"eab_kid"`
	HasEABHmac    bool   `json:"has_eab_hmac"`
	ServerIPv4    string `json:"server_ipv4"`
	ServerIPv6    string `json:"server_ipv6"`
}

func viewOf(c webcfg.Config) webConfigView {
	return webConfigView{
		HasCFToken: c.CFToken != "", ACMEEmail: c.ACMEEmail, ACMECA: c.ACMECA, ACMEDirectory: c.ACMEDirectory,
		EABKid: c.EABKid, HasEABHmac: c.EABHmac != "", ServerIPv4: c.ServerIPv4, ServerIPv6: c.ServerIPv6,
	}
}

func (s *Server) handleWebConfig(w http.ResponseWriter, r *http.Request) {
	c, err := s.Web.Get(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, viewOf(c))
}

func (s *Server) handlePutWebConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		webConfigView
		CFToken      string `json:"cf_token"` // non-empty = replace
		ClearCFToken bool   `json:"clear_cf_token"`
		EABHmac      string `json:"eab_hmac"`
		ClearEAB     bool   `json:"clear_eab"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c, err := s.Web.Update(r.Context(), func(c *webcfg.Config) {
		c.ACMEEmail, c.ACMECA, c.ACMEDirectory = strings.TrimSpace(in.ACMEEmail), in.ACMECA, strings.TrimSpace(in.ACMEDirectory)
		c.EABKid, c.ServerIPv4, c.ServerIPv6 = strings.TrimSpace(in.EABKid), strings.TrimSpace(in.ServerIPv4), strings.TrimSpace(in.ServerIPv6)
		if in.CFToken != "" {
			c.CFToken = strings.TrimSpace(in.CFToken)
		}
		if in.ClearCFToken {
			c.CFToken = ""
		}
		if in.EABHmac != "" {
			c.EABHmac = strings.TrimSpace(in.EABHmac)
		}
		if in.ClearEAB {
			c.EABKid, c.EABHmac = "", ""
		}
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.Audit.Record(r.Context(), sessionFrom(r).User.Username, clientIP(r), "web", "web.config", "ca="+c.ACMECA)
	writeJSON(w, http.StatusOK, viewOf(c))
}

func (s *Server) handleCFVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	decode(r, &in)
	tok := strings.TrimSpace(in.Token)
	if tok == "" {
		c, err := s.Web.Get(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		tok = c.CFToken
	}
	if err := cfdns.New(tok).Verify(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWebIPs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"ipv4": netinfo.PublicIPv4(r.Context()), "ipv6": netinfo.PublicIPv6(r.Context())})
}

func (s *Server) handleWebInstall(w http.ResponseWriter, r *http.Request) {
	s.Audit.Record(r.Context(), sessionFrom(r).User.Username, clientIP(r), "web", "web.install", "")
	writeJSON(w, http.StatusOK, map[string]string{"task_id": s.Sites.InstallEnv()})
}

// ---- sites ----

func (s *Server) handleSites(w http.ResponseWriter, r *http.Request) {
	list, err := s.Sites.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	u := sessionFrom(r).User
	out := list[:0]
	for _, st := range list {
		if u.CanSite(st.ID) {
			out = append(out, st)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleSite(w http.ResponseWriter, r *http.Request) {
	st, err := s.Sites.Get(r.Context(), idParam(r))
	if err != nil {
		writeSiteErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	var in site.CreateInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Sites.Create(r.Context(), in)
	if err != nil {
		writeSiteErr(w, err)
		return
	}
	s.audit(r, "site.create", in.Domain)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleUpdateSite(w http.ResponseWriter, r *http.Request) {
	var in site.UpdateInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.Sites.Update(r.Context(), idParam(r), in)
	if err != nil {
		writeSiteErr(w, err)
		return
	}
	s.audit(r, "site.update", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	var in site.DeleteInput
	decode(r, &in)
	st, err := s.Sites.Get(r.Context(), idParam(r))
	if err != nil {
		writeSiteErr(w, err)
		return
	}
	if s.Tamper != nil {
		// Locked files cannot be deleted (or a later site at this path edited).
		if err := s.Tamper.UnlockPath(r.Context(), st.Root); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	id, err := s.Sites.Delete(r.Context(), st.ID, in)
	if err != nil {
		writeSiteErr(w, err)
		return
	}
	detail := st.Domain
	if in.Files {
		detail += " (+files)"
	}
	s.audit(r, "site.delete", detail)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleIssueSSL(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Challenge string `json:"challenge"`
		Wildcard  bool   `json:"wildcard"`
	}
	decode(r, &in)
	id, err := s.Sites.IssueCert(r.Context(), idParam(r), in.Challenge, in.Wildcard)
	if err != nil {
		writeSiteErr(w, err)
		return
	}
	s.audit(r, "site.ssl_issue", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

func (s *Server) handleUploadSSL(w http.ResponseWriter, r *http.Request) {
	var in struct{ Cert, Key string }
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Sites.UploadCert(r.Context(), idParam(r), in.Cert, in.Key); err != nil {
		writeSiteErr(w, err)
		return
	}
	s.audit(r, "site.ssl_upload", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDisableSSL(w http.ResponseWriter, r *http.Request) {
	if err := s.Sites.DisableSSL(r.Context(), idParam(r)); err != nil {
		writeSiteErr(w, err)
		return
	}
	s.audit(r, "site.ssl_disable", strconv.FormatInt(idParam(r), 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleVhost(w http.ResponseWriter, r *http.Request) {
	v, err := s.Sites.Vhost(r.Context(), idParam(r))
	if err != nil {
		writeSiteErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"config": v})
}
