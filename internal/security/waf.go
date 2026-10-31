// Package security holds the WAF (ModSecurity + OWASP CRS in the nginx
// container) settings and events, and the CrowdSec engine whose decisions
// the panel enforces in the firewall.
package security

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

var (
	ErrBadWAF = errors.New("waf.invalid_settings")
	ruleIDRe  = regexp.MustCompile(`^\d{1,7}$`)
)

const wafKey = "waf"

type WAF struct {
	st  *store.Store
	env *webenv.Env
	cur webenv.WAFSettings
}

func NewWAF(st *store.Store, env *webenv.Env) *WAF {
	w := &WAF{st: st, env: env, cur: webenv.WAFSettings{Engine: "On", Paranoia: 1, Anomaly: 5}}
	var raw string
	if st.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, wafKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &w.cur)
	}
	env.WAF = w.Settings
	return w
}

func (w *WAF) Settings() webenv.WAFSettings { return w.cur }

// Save stores new settings; the caller then rebuilds the nginx container.
func (w *WAF) Save(ctx context.Context, s webenv.WAFSettings) error {
	if s.Engine != "On" && s.Engine != "DetectionOnly" || s.Paranoia < 1 || s.Paranoia > 4 || s.Anomaly < 1 || s.Anomaly > 100 || len(s.Disabled) > 2000 {
		return ErrBadWAF
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, id := range s.Disabled {
		if id = strings.TrimSpace(id); !ruleIDRe.MatchString(id) {
			return ErrBadWAF
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	s.Disabled = ids
	b, _ := json.Marshal(s)
	if _, err := w.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, wafKey, string(b)); err != nil {
		return err
	}
	w.cur = s
	return nil
}

// Event is one transaction ModSecurity logged (blocked or, in detection
// mode, flagged).
type Event struct {
	Time     int64       `json:"time"`
	IP       string      `json:"ip"`
	Host     string      `json:"host"`
	Method   string      `json:"method"`
	URI      string      `json:"uri"`
	Status   int         `json:"status"`
	Blocked  bool        `json:"blocked"`
	Messages []EventRule `json:"messages"`
}

type EventRule struct {
	ID       string `json:"id"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Data     string `json:"data,omitempty"`
}

type auditLine struct {
	Transaction struct {
		ClientIP  string `json:"client_ip"`
		TimeStamp string `json:"time_stamp"`
		Request   struct {
			Method  string            `json:"method"`
			URI     string            `json:"uri"`
			Headers map[string]string `json:"headers"`
		} `json:"request"`
		Response struct {
			HTTPCode int `json:"http_code"`
		} `json:"response"`
		Messages []struct {
			Message string `json:"message"`
			Details struct {
				RuleID   string `json:"ruleId"`
				Severity string `json:"severity"`
				Data     string `json:"data"`
			} `json:"details"`
		} `json:"messages"`
	} `json:"transaction"`
}

// ParseEvent reads one JSON audit log line.
func ParseEvent(line string) (Event, bool) {
	var a auditLine
	if json.Unmarshal([]byte(line), &a) != nil || a.Transaction.ClientIP == "" {
		return Event{}, false
	}
	t := a.Transaction
	e := Event{IP: t.ClientIP, Method: t.Request.Method, URI: t.Request.URI, Status: t.Response.HTTPCode, Messages: []EventRule{}}
	for k, v := range t.Request.Headers {
		if strings.EqualFold(k, "host") {
			e.Host = v
		}
	}
	// "Wed Oct 28 04:09:22 2026" (ModSecurity v3)
	if ts, err := time.Parse(time.ANSIC, t.TimeStamp); err == nil {
		e.Time = ts.Unix()
	}
	e.Blocked = e.Status == 403
	for _, m := range t.Messages {
		// Score bookkeeping rules (949xxx / 980xxx) repeat the total: keep the detections.
		if strings.HasPrefix(m.Details.RuleID, "949") || strings.HasPrefix(m.Details.RuleID, "980") {
			continue
		}
		sev := m.Details.Severity
		if n, err := strconv.Atoi(sev); err == nil {
			sev = map[int]string{0: "EMERGENCY", 1: "ALERT", 2: "CRITICAL", 3: "ERROR", 4: "WARNING", 5: "NOTICE"}[n]
		}
		data := m.Details.Data
		if len(data) > 300 {
			data = data[:300]
		}
		e.Messages = append(e.Messages, EventRule{ID: m.Details.RuleID, Message: m.Message, Severity: sev, Data: data})
	}
	return e, true
}

// Events returns the newest n events (newest first) matching filter.
func (w *WAF) Events(n int, filter string) ([]Event, error) {
	lines, err := logcenter.Tail(w.env.WAFLog(), n, filter)
	if errors.Is(err, os.ErrNotExist) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Event{}
	for i := len(lines) - 1; i >= 0; i-- {
		if e, ok := ParseEvent(lines[i]); ok {
			out = append(out, e)
		}
	}
	return out, nil
}
