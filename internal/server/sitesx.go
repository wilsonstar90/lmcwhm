package server

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/wilsonstar90/lmcwhm/internal/site"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

// siteOverview is what the website list shows beside each site: backups
// and today's traffic.
type siteOverview struct {
	Backups int   `json:"backups"`
	Bytes   int64 `json:"bytes"`
	PV      int64 `json:"pv"`
}

func (s *Server) handleSitesOverview(w http.ResponseWriter, r *http.Request) {
	out := map[string]*siteOverview{}
	get := func(d string) *siteOverview {
		if out[d] == nil {
			out[d] = &siteOverview{}
		}
		return out[d]
	}
	if s.Backup != nil {
		if recs, err := s.Backup.List(r.Context(), "site"); err == nil {
			for _, rec := range recs {
				get(rec.Target).Backups++
			}
		}
	}
	if s.Stats != nil {
		if pts, err := s.Stats.Overview(r.Context()); err == nil {
			for d, p := range pts {
				o := get(d)
				o.Bytes, o.PV = p.Bytes, p.PV
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": out})
}

func (s *Server) handleSiteCategories(w http.ResponseWriter, r *http.Request) {
	list, err := s.Sites.Categories(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// ---- rewrite templates (偽靜態) ----

const rewriteKey = "rewrite_templates"

var rewriteNameRe = regexp.MustCompile(`^[A-Za-z0-9_.\-\p{Han}]{1,32}$`)

func (s *Server) customRewrites(ctx context.Context) map[string]string {
	m := map[string]string{}
	var raw string
	if s.Store.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, rewriteKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &m)
	}
	return m
}

func (s *Server) handleRewrites(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"names": webenv.RewriteNames(), "builtin": webenv.Rewrites(), "custom": s.customRewrites(r.Context()),
	})
}

// handleSaveRewrite saves (or, with an empty body, deletes) a custom template.
func (s *Server) handleSaveRewrite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !rewriteNameRe.MatchString(in.Name) {
		writeErr(w, http.StatusBadRequest, errBadRequest)
		return
	}
	if err := webenv.CheckDirectives(in.Body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	m := s.customRewrites(r.Context())
	if strings.TrimSpace(in.Body) == "" {
		delete(m, in.Name)
	} else {
		m[in.Name] = in.Body
	}
	b, _ := json.Marshal(m)
	if _, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		rewriteKey, string(b)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.audit(r, "site.rewrite_template", in.Name)
	writeJSON(w, http.StatusOK, map[string]any{"custom": m})
}

func (s *Server) handleSitesBatch(w http.ResponseWriter, r *http.Request) {
	var in site.BatchInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	u := sessionFrom(r).User
	for _, id := range in.IDs {
		if !u.CanSite(id) {
			writeErr(w, http.StatusForbidden, errForbidden)
			return
		}
	}
	id, err := s.Sites.Batch(r.Context(), in)
	if err != nil {
		writeSiteErr(w, err)
		return
	}
	s.audit(r, "site.batch", in.Action)
	writeJSON(w, http.StatusOK, map[string]string{"task_id": id})
}

// handleSiteProbe times a request to one of the site's hosts (域名測速).
func (s *Server) handleSiteProbe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host  string `json:"host"`
		HTTPS bool   `json:"https"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	st, err := s.Sites.Get(r.Context(), idParam(r))
	if err != nil {
		writeSiteErr(w, err)
		return
	}
	ok := false
	for _, h := range st.Hosts() {
		ok = ok || strings.EqualFold(h, in.Host)
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, site.ErrBadDomain)
		return
	}
	code, ms, err := site.ProbeHost(r.Context(), in.Host, in.HTTPS)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": code, "ms": ms})
}
