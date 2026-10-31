package security

// The OWASP CRS rules shipped in the WAF image, for the rule list: read
// from the running nginx container, parsed once per image.

import (
	"bufio"
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

type CRSRule struct {
	ID       string `json:"id"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Level    int    `json:"level"` // paranoia level
	Phase    int    `json:"phase"`
	Hits     int    `json:"hits"` // last 24 hours
}

type CRSCategory struct {
	Code  string    `json:"code"` // "942"
	File  string    `json:"file"` // REQUEST-942-APPLICATION-ATTACK-SQLI
	Rules []CRSRule `json:"rules"`
}

var (
	ErrNoCRS = errors.New("waf.crs_unavailable")

	idRe    = regexp.MustCompile(`\bid:(\d+)`)
	msgRe   = regexp.MustCompile(`\bmsg:'((?:[^'\\]|\\.)*)'`)
	sevRe   = regexp.MustCompile(`\bseverity:'?([A-Z]+)'?`)
	plRe    = regexp.MustCompile(`tag:'paranoia-level/(\d)'`)
	phaseRe = regexp.MustCompile(`\bphase:(\d)`)
	fileRe  = regexp.MustCompile(`^(REQUEST|RESPONSE)-(\d{3})-`)
)

// ParseCRS reads concatenated rule files, each introduced by "### <path>".
func ParseCRS(text string) []CRSCategory {
	var cats []CRSCategory
	var cur *CRSCategory
	var stmt strings.Builder
	flush := func() {
		s := stmt.String()
		stmt.Reset()
		if cur == nil || !(strings.HasPrefix(s, "SecRule") || strings.HasPrefix(s, "SecAction")) {
			return
		}
		id, msg := idRe.FindStringSubmatch(s), msgRe.FindStringSubmatch(s)
		if id == nil || msg == nil {
			return // chained parts and bookkeeping rules
		}
		r := CRSRule{ID: id[1], Message: strings.ReplaceAll(msg[1], `\'`, `'`), Level: 1}
		if m := sevRe.FindStringSubmatch(s); m != nil {
			r.Severity = m[1]
		}
		if m := plRe.FindStringSubmatch(s); m != nil {
			r.Level, _ = strconv.Atoi(m[1])
		}
		if m := phaseRe.FindStringSubmatch(s); m != nil {
			r.Phase, _ = strconv.Atoi(m[1])
		}
		cur.Rules = append(cur.Rules, r)
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if f, ok := strings.CutPrefix(line, "### "); ok {
			flush()
			base := strings.TrimSuffix(filepath.Base(f), ".conf")
			m := fileRe.FindStringSubmatch(base)
			if m == nil {
				cur = nil
				continue
			}
			cats = append(cats, CRSCategory{Code: m[2], File: base})
			cur = &cats[len(cats)-1]
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			if stmt.Len() == 0 {
				continue
			}
		}
		cont := strings.HasSuffix(line, `\`)
		stmt.WriteString(strings.TrimSuffix(line, `\`) + " ")
		if !cont {
			flush()
		}
	}
	flush()
	out := cats[:0]
	for _, c := range cats {
		if len(c.Rules) > 0 {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

var (
	crsMu    sync.Mutex
	crsCache []CRSCategory
)

const crsScript = `for d in /opt/owasp-crs/rules /etc/modsecurity.d/owasp-crs/rules; do
  [ -d "$d" ] || continue
  for f in "$d"/*.conf; do echo "### $f"; cat "$f"; done
  exit 0
done
exit 1`

// CRSRules lists the image's rules with their hits in the last 24 hours.
func (w *WAF) CRSRules(ctx context.Context) ([]CRSCategory, error) {
	crsMu.Lock()
	cats := crsCache
	crsMu.Unlock()
	if cats == nil {
		if !w.cur.Enabled || dockerx.State(ctx, webenv.NginxName) != "running" {
			return nil, ErrNoCRS
		}
		out, err := dockerx.Run(ctx, "exec", webenv.NginxName, "sh", "-c", crsScript)
		if err != nil {
			return nil, ErrNoCRS
		}
		if cats = ParseCRS(out); len(cats) == 0 {
			return nil, ErrNoCRS
		}
		crsMu.Lock()
		crsCache = cats
		crsMu.Unlock()
	}
	hits := map[string]int{}
	since := time.Now().Add(-24 * time.Hour).Unix()
	if evs, err := w.Events(5000, ""); err == nil {
		for _, e := range evs {
			if e.Time < since {
				continue
			}
			for _, m := range e.Messages {
				hits[m.ID]++
			}
		}
	}
	res := make([]CRSCategory, len(cats))
	for i, c := range cats {
		res[i] = CRSCategory{Code: c.Code, File: c.File, Rules: make([]CRSRule, len(c.Rules))}
		for j, r := range c.Rules {
			r.Hits = hits[r.ID]
			res[i].Rules[j] = r
		}
	}
	return res, nil
}
