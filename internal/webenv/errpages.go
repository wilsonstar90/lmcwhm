package webenv

import (
	"sort"
)

// ErrorPages are the custom 4xx / 5xx pages (by file name, without ".html")
// and which page each status uses: panel-wide, per site (a site's "" means
// nginx's own page), and the sites whose backend errors are replaced too.
type ErrorPages struct {
	Pages     map[string][]byte
	Global    map[int]string
	Sites     map[string]map[int]string
	Intercept map[string]bool
}

type errLine struct {
	Code int
	Page string
}

const (
	errDir  = nginxConfDir + "/errors"
	authDir = nginxConfDir + "/auth"
)

func (e *Env) errorPages() ErrorPages {
	if e.ErrorPages == nil {
		return ErrorPages{}
	}
	return e.ErrorPages()
}

// errorLines merges the panel-wide and the site's mapping; a site serving
// its own 404.html / 50x.html (Advanced.ErrorPages) keeps those.
func errorLines(p ErrorPages, domain string, own bool) []errLine {
	m := map[int]string{}
	for c, pg := range p.Global {
		m[c] = pg
	}
	for c, pg := range p.Sites[domain] {
		m[c] = pg
	}
	var out []errLine
	for c, pg := range m {
		if pg == "" || p.Pages[pg] == nil || own && (c == 404 || c >= 500 && c <= 504) {
			continue
		}
		out = append(out, errLine{c, pg})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}
