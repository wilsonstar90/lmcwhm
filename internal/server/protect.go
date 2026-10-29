package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
	"github.com/wilsonstar90/lmcwhm/internal/protect"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

// Website protection (網站防護) and dedicated rules (專屬規則).

func (s *Server) protectRoutes(r chi.Router) {
	if s.Protect == nil {
		return
	}
	r.Get("/protect/sites", s.handleProtectSites)
	r.Put("/protect/sites", s.handlePutProtectSites)
	r.Get("/protect/sites/{domain}/blocked", s.handleProtectBlocked)
	r.Get("/protect/settings", s.handleProtectSettings)
	r.Put("/protect/settings", s.handlePutProtectSettings)
	r.Get("/protect/rules", s.handleProtectRules)
	r.Post("/protect/rules", s.handleSaveProtectRule)
	r.Put("/protect/rules/{id}", s.handleSaveProtectRule)
	r.Post("/protect/rules/{id}/enabled", s.handleProtectRuleEnabled)
	r.Post("/protect/rules/{id}/delete", s.handleDeleteProtectRule)
	r.Post("/protect/rules/{id}/reset", s.handleResetProtectRule)
	r.Post("/protect/rules/preview", s.handlePreviewProtectRule)
}

func writeProtectErr(w http.ResponseWriter, err error) {
	writeCodedErr(w, err, protect.ErrNotFound)
}

type protectRow struct {
	Domain    string             `json:"domain"`
	Aliases   []string           `json:"aliases"`
	Type      string             `json:"type"`
	Supported bool               `json:"supported"` // passthrough sites end TLS elsewhere
	Proxied   bool               `json:"proxied"`   // Cloudflare proxied DNS
	Protect   webenv.SiteProtect `json:"protect"`
	Blocked   int64              `json:"blocked"`     // all kept statistics
	Blocked24 int64              `json:"blocked_24h"` // last 24 hours
}

// blockedBy sums the refused requests per domain since from (0 = all kept).
func (s *Server) blockedBy(ctx context.Context, from int64) map[string]int64 {
	out := map[string]int64{}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT domain, COALESCE(SUM(blocked), 0) FROM site_stats_hourly WHERE hour >= ? GROUP BY domain`, from)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var n int64
		if rows.Scan(&d, &n) == nil {
			out[d] = n
		}
	}
	return out
}

func (s *Server) handleProtectSites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sites, err := s.Sites.List(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	saved, err := s.Protect.Sites(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	all, day := s.blockedBy(ctx, 0), s.blockedBy(ctx, time.Now().Add(-24*time.Hour).Unix())
	rows := make([]protectRow, 0, len(sites))
	for _, x := range sites {
		p, ok := saved[x.Domain]
		if !ok {
			p = protect.Default
		}
		rows = append(rows, protectRow{Domain: x.Domain, Aliases: x.Aliases, Type: x.Type, Supported: !x.Passthrough, Proxied: x.DNSProxied,
			Protect: p, Blocked: all[x.Domain], Blocked24: day[x.Domain]})
	}
	st := s.Protect.Settings(ctx)
	info := map[string]any{"rules_on": st.RulesOn, "spiders": s.Protect.SpiderTokens(ctx), "has_asn": s.GeoIP != nil && s.GeoIP.ASNBuilt() > 0}
	writeJSON(w, http.StatusOK, map[string]any{"sites": rows, "info": info})
}

func (s *Server) handlePutProtectSites(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items map[string]webenv.SiteProtect `json:"items"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Protect.SaveSites(r.Context(), in.Items); err != nil {
		writeProtectErr(w, err)
		return
	}
	names := make([]string, 0, len(in.Items))
	for d := range in.Items {
		names = append(names, d)
	}
	s.auditCat(r, "security", "protect.sites", strings.Join(names, ", "))
	s.applyPatterns(w, r, map[string]bool{"ok": true})
}

// handleProtectBlocked: the latest refused requests of one site, from its access log.
func (s *Server) handleProtectBlocked(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	sites, err := s.Sites.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	known := false
	for _, x := range sites {
		known = known || x.Domain == domain
	}
	if !known {
		writeErr(w, http.StatusNotFound, protect.ErrNotFound)
		return
	}
	lines, _ := logcenter.Tail(filepath.Join(s.Sites.Env.WWWLogs, domain+".log"), 20000, "")
	out := []logcenter.Access{}
	for i := len(lines) - 1; i >= 0 && len(out) < 500; i-- {
		a := logcenter.ParseAccess(lines[i])
		if a.Status == 403 || a.Status == 429 || a.Status == 444 {
			a.Raw = ""
			out = append(out, a)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleProtectSettings(w http.ResponseWriter, r *http.Request) {
	st := s.Protect.Settings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"settings": st, "idc_ranges": s.Protect.IDCCount(r.Context())})
}

func (s *Server) handlePutProtectSettings(w http.ResponseWriter, r *http.Request) {
	var in protect.Settings
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	st, err := s.Protect.SaveSettings(r.Context(), in)
	if err != nil {
		writeProtectErr(w, err)
		return
	}
	s.auditCat(r, "security", "protect.settings", "rules="+strconv.FormatBool(st.RulesOn))
	s.applyPatterns(w, r, map[string]any{"settings": st})
}

func (s *Server) handleProtectRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.Protect.Rules(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules, "rules_on": s.Protect.Settings(r.Context()).RulesOn,
		"fields": keys(webenv.Fields), "ops": keys(webenv.Ops)})
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (s *Server) handleSaveProtectRule(w http.ResponseWriter, r *http.Request) {
	var in protect.Rule
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.ID = idParam(r)
	rule, err := s.Protect.SaveRule(r.Context(), in)
	if err != nil {
		writeProtectErr(w, err)
		return
	}
	s.auditCat(r, "security", "protect.rule", rule.Code+" "+rule.Name)
	s.applyPatterns(w, r, rule)
}

func (s *Server) handleProtectRuleEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Protect.SetEnabled(r.Context(), idParam(r), in.Enabled); err != nil {
		writeProtectErr(w, err)
		return
	}
	s.auditCat(r, "security", "protect.rule_enabled", strconv.FormatInt(idParam(r), 10)+"="+strconv.FormatBool(in.Enabled))
	s.applyPatterns(w, r, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteProtectRule(w http.ResponseWriter, r *http.Request) {
	if err := s.Protect.DeleteRule(r.Context(), idParam(r)); err != nil {
		writeProtectErr(w, err)
		return
	}
	s.auditCat(r, "security", "protect.rule_delete", strconv.FormatInt(idParam(r), 10))
	s.applyPatterns(w, r, map[string]bool{"ok": true})
}

func (s *Server) handleResetProtectRule(w http.ResponseWriter, r *http.Request) {
	if err := s.Protect.ResetRule(r.Context(), idParam(r)); err != nil {
		writeProtectErr(w, err)
		return
	}
	s.auditCat(r, "security", "protect.rule_reset", strconv.FormatInt(idParam(r), 10))
	s.applyPatterns(w, r, map[string]bool{"ok": true})
}

// handlePreviewProtectRule shows a rule as the edge config will read it.
func (s *Server) handlePreviewProtectRule(w http.ResponseWriter, r *http.Request) {
	var in protect.Rule
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	code := in.Code
	if code == "" {
		code = "new"
	}
	er := webenv.EdgeRule{Code: code, All: in.All}
	for _, c := range in.Conds {
		er.Conds = append(er.Conds, webenv.EdgeCond{Field: c.Field, Op: c.Op, Negate: c.Negate, Values: c.Values})
	}
	b, _ := json.MarshalIndent(in.Conds, "", "  ")
	writeJSON(w, http.StatusOK, map[string]string{"haproxy": s.Sites.Env.RulePreview(er), "json": string(b)})
}
