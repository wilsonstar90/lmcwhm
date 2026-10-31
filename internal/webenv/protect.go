package webenv

// Website protection at the edge (網站防護 and 專屬規則): HAProxy decides,
// per host, whether a request is refused and why; nginx then answers 403
// (429 for CC) and logs the reason with the request.

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SiteProtect are one website's protection switches.
type SiteProtect struct {
	On         bool `json:"on"`
	FakeSpider bool `json:"fake_spider"` // crawler User-Agent from outside its trusted ranges
	// CDN: the site is reached through a CDN (any); the visitor's address is
	// read from the first of CDNHeaders (empty = DefaultCDNHeaders) that
	// holds one, from any connection (like aaPanel).
	CDN        bool     `json:"cdn"`
	CDNHeaders []string `json:"cdn_headers,omitempty"`
	IDC        bool `json:"idc"`         // refuse data-centre addresses
	Crawler    bool `json:"crawler"`     // refuse scraper / bad bot User-Agents
	CC         int  `json:"cc"`          // max requests per visitor in 10 s (0 = off)
}

// DefaultCDNHeaders are tried in order for a site behind a CDN.
var DefaultCDNHeaders = []string{"CF-Connecting-IP", "True-Client-IP", "Ali-Cdn-Real-Ip", "Cdn-Src-Ip", "Cdn-Real-Ip", "X-Real-IP",
	"X-Forwarded-For", "X-Client-IP", "Client-IP", "Forwarded-For", "X-Forwarded", "Forwarded"}

// Headers returns the site's CDN address headers.
func (p SiteProtect) Headers() []string {
	if len(p.CDNHeaders) == 0 {
		return DefaultCDNHeaders
	}
	return p.CDNHeaders
}

// Protection is everything the edge needs; Sites is keyed by site domain.
type Protection struct {
	Sites     map[string]SiteProtect
	Default   SiteProtect // sites not in Sites
	SpiderUA  []string    // User-Agent tokens of crawlers with a trusted list
	CrawlerUA []string
	IDC       []string // data-centre ranges
	Rules     []EdgeRule
	// BlockStatus answers IP / list / protection refusals (default 403).
	BlockStatus int
}

// BlockStatuses are the codes a refusal may answer with; 444 closes the
// connection without a response.
var BlockStatuses = []int{400, 401, 403, 404, 405, 406, 410, 418, 429, 444, 451, 500, 502, 503}

// BlockStatus returns s when refusals may use it, else 403.
func BlockStatus(s int) int {
	for _, c := range BlockStatuses {
		if c == s {
			return s
		}
	}
	return 403
}

// EdgeRule is a dedicated rule: refused when all (or any) conditions match.
type EdgeRule struct {
	Code   string
	All    bool
	Conds  []EdgeCond
	Detect bool // log a match, never refuse
	Status int  // response when refusing (BlockStatuses; default 403)
}

// EdgeCond compares one part of the request with values (one must match).
type EdgeCond struct {
	Field  string   // path | url | query | method | ua | referer | cookie | body
	Op     string   // contains | equals | prefix | suffix | regex
	Negate bool     // the condition holds when no value matches
	Values []string // written to a pattern file
}

// Fields and Ops are what a condition may use (the edge config is built
// from these names only; values never enter it).
var (
	Fields = map[string]string{"path": "path", "url": "url", "query": "query", "method": "method",
		"ua": "req.hdr(user-agent)", "referer": "req.hdr(referer)", "cookie": "req.hdr(cookie)", "body": "req.body"}
	Ops = map[string]string{"contains": "sub", "equals": "str", "prefix": "beg", "suffix": "end", "regex": "reg"}
)

var codeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidCode reports whether c can name a rule at the edge.
func ValidCode(c string) bool { return codeRe.MatchString(c) }

func (e *Env) protection() Protection {
	if e.Protection == nil {
		return Protection{}
	}
	return e.Protection()
}

type ruleData struct {
	Code   string
	Lines  []string // conditions after "if"
	Detect bool
	Status int
}

func lines(v []string) []byte {
	var b bytes.Buffer
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" && !strings.HasPrefix(s, "#") {
			b.WriteString(s + "\n")
		}
	}
	return b.Bytes()
}

// cdnGroup is one distinct list of CDN address headers; sites sharing a
// list share its config lines.
type cdnGroup struct {
	ID      int
	Headers []string
}

// cdnFiles maps every host of a CDN site to its header group ("0" = none).
func (e *Env) cdnFiles(sites []Site, files map[string][]byte) []cdnGroup {
	p := e.protection()
	var groups []cdnGroup
	ids := map[string]int{}
	m := map[string]string{}
	for _, s := range sites {
		sp, ok := p.Sites[s.Domain]
		if !ok {
			sp = p.Default
		}
		id := 0
		if sp.CDN && !s.Passthrough {
			var hs []string
			for _, h := range sp.Headers() {
				if ValidHeader(h) {
					hs = append(hs, h)
				}
			}
			key := strings.ToLower(strings.Join(hs, ","))
			if id = ids[key]; id == 0 && len(hs) > 0 {
				id = len(groups) + 1
				ids[key] = id
				groups = append(groups, cdnGroup{ID: id, Headers: hs})
			}
		}
		for _, h := range s.Hosts() {
			m[strings.ToLower(h)] = strconv.Itoa(id)
		}
	}
	hostMaps("cdn", m, files)
	return groups
}

// protectFiles renders the protection maps and lists and returns the rules
// for the config, and whether a rule reads the request body.
func (e *Env) protectFiles(sites []Site, files map[string][]byte) (rules []ruleData, body bool) {
	p := e.protection()
	prot, cc := map[string]string{}, map[string]string{}
	for _, s := range sites {
		sp, ok := p.Sites[s.Domain]
		if !ok {
			sp = p.Default
		}
		flags := ","
		if sp.On && !s.Passthrough {
			flags += "on,"
			for _, f := range []struct {
				on   bool
				name string
			}{{sp.FakeSpider, "fake"}, {sp.IDC, "idc"}, {sp.Crawler, "crawler"}} {
				if f.on {
					flags += f.name + ","
				}
			}
		}
		for _, h := range s.Hosts() {
			h = strings.ToLower(h)
			prot[h] = flags
			if sp.On && sp.CC > 0 {
				cc[h] = strconv.Itoa(sp.CC)
			}
		}
	}
	hostMaps("prot", prot, files)
	hostMaps("cc", cc, files)
	files["maps/spider_ua.lst"] = lines(p.SpiderUA)
	files["maps/crawler_ua.lst"] = lines(p.CrawlerUA)
	files["maps/idc.lst"] = rangeList(p.IDC)

	sort.Slice(p.Rules, func(i, j int) bool { return p.Rules[i].Code < p.Rules[j].Code })
	for _, r := range p.Rules {
		rd, rf, b := RuleConfig(e.EdgeDir(), r)
		if len(rd.Lines) == 0 {
			continue
		}
		for name, v := range rf {
			files[name] = v
		}
		rules, body = append(rules, rd), body || b
	}
	return rules, body
}

// RuleConfig turns a rule into its edge conditions (after "if") and pattern
// files (relative to dir); no lines when the rule cannot be expressed.
func RuleConfig(dir string, r EdgeRule) (rd ruleData, files map[string][]byte, body bool) {
	rd.Code, rd.Detect, rd.Status, files = r.Code, r.Detect, BlockStatus(r.Status), map[string][]byte{}
	if !ValidCode(r.Code) || len(r.Conds) == 0 {
		return rd, nil, false
	}
	var conds []string
	for i, c := range r.Conds {
		fetch, op := Fields[c.Field], Ops[c.Op]
		if fetch == "" || op == "" || len(lines(c.Values)) == 0 {
			return rd, nil, false
		}
		name := fmt.Sprintf("maps/rules/%s_%d.lst", r.Code, i)
		files[name] = lines(c.Values)
		cond := fmt.Sprintf("{ %s -m %s -i -f %s/%s }", fetch, op, dir, name)
		if c.Negate {
			cond = "!" + cond
		}
		conds = append(conds, cond)
		body = body || c.Field == "body"
	}
	if r.All {
		rd.Lines = []string{strings.Join(conds, " ")}
	} else {
		rd.Lines = conds
	}
	return rd, files, body
}

// RulePreview is how a rule reads in the edge config (for the rule editor).
func (e *Env) RulePreview(r EdgeRule) string {
	rd, files, _ := RuleConfig(e.EdgeDir(), r)
	if len(rd.Lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range rd.Lines {
		if rd.Detect {
			fmt.Fprintf(&b, "http-request set-var(txn.detect) str(rule-%s) if guard !ip_allow !list_pass !acme %s\n", rd.Code, l)
		} else {
			fmt.Fprintf(&b, "http-request set-var(txn.block) str(%d:rule-%s) if guard !ip_allow !list_pass !acme %s\n", rd.Status, rd.Code, l)
		}
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "\n# %s/%s\n%s", e.EdgeDir(), n, files[n])
	}
	return b.String()
}
