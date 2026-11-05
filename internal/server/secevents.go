package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/secevents"
)

// The block log (攔截記錄): what the security layers did to requests, kept
// per day for 30 days (see secevents), and the auto-ban settings.

// dayRange reads ?date=YYYY-MM-DD (or ?back=N days before today) and
// ?days=N, in the panel's time zone (default today, one day), as [from, to):
// the N days that end with that day.
func (s *Server) dayRange(r *http.Request) (int64, int64) {
	loc := time.Local
	if s.Stats != nil && s.Stats.Loc != nil {
		loc = s.Stats.Loc
	}
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if d, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("date"), loc); err == nil {
		start = d
	} else if back := intParam(r, "back", 0, 365); back > 0 { // days before today
		start = start.AddDate(0, 0, -back)
	}
	days := max(intParam(r, "days", 1, 31), 1)
	end := start.AddDate(0, 0, 1)
	return end.AddDate(0, 0, -days).Unix(), end.Unix()
}

func (s *Server) handleSecEvents(w http.ResponseWriter, r *http.Request) {
	if s.Events == nil {
		writeJSON(w, http.StatusOK, secevents.Page{Items: []secevents.Event{}})
		return
	}
	q := r.URL.Query()
	from, to := s.dayRange(r)
	f := secevents.Filter{From: from, To: to, Site: q.Get("site"), Source: q.Get("source"), Action: q.Get("action"),
		IP: strings.TrimSpace(q.Get("ip")), Q: q.Get("q"), Limit: intParam(r, "limit", 100, 1000), Offset: intParam(r, "offset", 0, 1<<30)}
	s.Events.Collect(r.Context(), nil) // what the logs gained in the last seconds
	p, err := s.Events.Query(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if s.Protect != nil {
		if rules, err := s.Protect.Rules(r.Context()); err == nil {
			names := map[string]string{}
			for _, rl := range rules {
				names[rl.Code] = rl.Name
			}
			for i, e := range p.Items {
				if e.Source == "rule" && e.Message == "" {
					p.Items[i].Message = names[e.Rule]
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleAutoBan(w http.ResponseWriter, r *http.Request) {
	if s.Events == nil {
		writeJSON(w, http.StatusOK, secevents.DefaultAutoBan())
		return
	}
	writeJSON(w, http.StatusOK, s.Events.AutoBanSettings(r.Context()))
}

func (s *Server) handlePutAutoBan(w http.ResponseWriter, r *http.Request) {
	if s.Events == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("autoban.unavailable"))
		return
	}
	var in secevents.AutoBan
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	a, err := s.Events.SaveAutoBan(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "security", "autoban.update", "")
	writeJSON(w, http.StatusOK, a)
}
