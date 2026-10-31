package server

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
	"github.com/wilsonstar90/lmcwhm/internal/security"
)

// One list of what the security layers did to requests: refusals and
// detections at the edge (lists, website protection, dedicated rules, CC;
// from each site's access log) and by the WAF (ModSecurity audit log).

type SecEvent struct {
	Time     int64                `json:"time"`
	IP       string               `json:"ip"`
	Host     string               `json:"host"`
	Site     string               `json:"site"`
	Method   string               `json:"method"`
	URI      string               `json:"uri"`
	UA       string               `json:"ua,omitempty"`
	Status   int                  `json:"status"`
	Action   string               `json:"action"` // blocked | detected | limited
	Source   string               `json:"source"` // waf | rule | list | ip | protection | cc
	Rule     string               `json:"rule"`   // CRS id, dedicated rule code or protection check
	Message  string               `json:"message,omitempty"`
	Severity string               `json:"severity,omitempty"`
	Messages []security.EventRule `json:"messages,omitempty"`
}

// edgeEvent turns an access log line with a reason into an event.
func edgeEvent(a logcenter.Access, domain string) (SecEvent, bool) {
	e := SecEvent{Time: a.Time, IP: a.IP, Host: a.Host, Site: domain, Method: a.Method, URI: a.Path, UA: a.UA, Status: a.Status}
	reason := a.Block
	e.Action = "blocked"
	if reason == "" {
		if a.Detect == "" {
			return e, false
		}
		reason, e.Action = a.Detect, "detected"
	}
	switch {
	case strings.HasPrefix(reason, "rule-"):
		e.Source, e.Rule = "rule", strings.TrimPrefix(reason, "rule-")
	case reason == "cc":
		e.Source, e.Rule, e.Action = "cc", "cc", "limited"
	case reason == "ip" || reason == "list":
		e.Source, e.Rule = reason, reason
	default:
		e.Source, e.Rule = "protection", reason
	}
	return e, true
}

func wafEvent(w security.Event, siteOf func(string) string) SecEvent {
	e := SecEvent{Time: w.Time, IP: w.IP, Host: w.Host, Method: w.Method, URI: w.URI, Status: w.Status, Source: "waf", Messages: w.Messages, Action: "detected"}
	if w.Blocked {
		e.Action = "blocked"
	}
	e.Site = siteOf(strings.Split(w.Host, ":")[0])
	if len(w.Messages) > 0 {
		e.Rule, e.Message, e.Severity = w.Messages[0].ID, w.Messages[0].Message, w.Messages[0].Severity
	}
	return e
}

func (s *Server) handleSecEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	only, source, action, text := q.Get("site"), q.Get("source"), q.Get("action"), strings.ToLower(strings.TrimSpace(q.Get("q")))
	limit := intParam(r, "limit", 500, 2000)
	sites, err := s.Sites.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	byHost := map[string]string{}
	for _, x := range sites {
		for _, h := range x.Hosts() {
			byHost[h] = x.Domain
		}
	}
	siteOf := func(h string) string {
		if d, ok := byHost[h]; ok {
			return d
		}
		for pat, d := range byHost { // wildcard aliases
			if strings.HasPrefix(pat, "*.") && strings.HasSuffix(h, pat[1:]) {
				return d
			}
		}
		return ""
	}
	var out []SecEvent
	if source == "" || source != "waf" {
		for _, x := range sites {
			if only != "" && x.Domain != only || x.Passthrough { // passthrough: no nginx, no log
				continue
			}
			lines, _ := logcenter.Tail(filepath.Join(s.Sites.Env.WWWLogs, x.Domain+".log"), 5000, "")
			for _, l := range lines {
				if e, ok := edgeEvent(logcenter.ParseAccess(l), x.Domain); ok {
					out = append(out, e)
				}
			}
		}
	}
	if s.WAF != nil && (source == "" || source == "waf") {
		evs, _ := s.WAF.Events(5000, "")
		for _, ev := range evs {
			e := wafEvent(ev, siteOf)
			if only == "" || e.Site == only {
				out = append(out, e)
			}
		}
	}
	names := map[string]string{}
	if s.Protect != nil {
		if rules, err := s.Protect.Rules(r.Context()); err == nil {
			for _, rl := range rules {
				names[rl.Code] = rl.Name
			}
		}
	}
	kept := out[:0]
	for _, e := range out {
		if source != "" && e.Source != source || action != "" && e.Action != action {
			continue
		}
		if e.Source == "rule" && e.Message == "" {
			e.Message = names[e.Rule]
		}
		if text != "" && !strings.Contains(strings.ToLower(strings.Join([]string{e.IP, e.Host, e.URI, e.UA, e.Rule, e.Message}, " ")), text) {
			continue
		}
		kept = append(kept, e)
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Time > kept[j].Time })
	if len(kept) > limit {
		kept = kept[:limit]
	}
	if kept == nil {
		kept = []SecEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": kept})
}
