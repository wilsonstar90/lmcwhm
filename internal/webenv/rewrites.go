package webenv

import (
	"embed"
	"errors"
	"path"
	"sort"
	"strings"
)

//go:embed rewrites/*.conf
var rewriteFS embed.FS

// Rewrites are the built-in rewrite rule templates (偽靜態), by name.
func Rewrites() map[string]string {
	out := map[string]string{}
	entries, _ := rewriteFS.ReadDir("rewrites")
	for _, e := range entries {
		b, _ := rewriteFS.ReadFile("rewrites/" + e.Name())
		out[strings.TrimSuffix(e.Name(), path.Ext(e.Name()))] = string(b)
	}
	return out
}

// RewriteNames lists the templates, the common ones first.
func RewriteNames() []string {
	first := []string{"wordpress", "laravel5", "thinkphp", "spa", "default"}
	all := Rewrites()
	out := []string{}
	for _, n := range first {
		if _, ok := all[n]; ok {
			out = append(out, n)
			delete(all, n)
		}
	}
	rest := make([]string, 0, len(all))
	for n := range all {
		rest = append(rest, n)
	}
	sort.Strings(rest)
	return append(out, rest...)
}

var ErrDirectives = errors.New("site.invalid_directives")

// CheckDirectives refuses text that could not sit inside one server block:
// unbalanced braces (which would close it and open another), NUL bytes, or
// more than 64 KB. nginx -t checks the rest when it is applied.
func CheckDirectives(s string) error {
	if len(s) > 64<<10 || strings.ContainsRune(s, 0) {
		return ErrDirectives
	}
	depth := 0
	quote := rune(0)
	comment := false
	for _, c := range s {
		switch {
		case comment:
			comment = c != '\n'
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '#':
			comment = true
		case c == '"' || c == '\'':
			quote = c
		case c == '{':
			depth++
		case c == '}':
			if depth--; depth < 0 {
				return ErrDirectives
			}
		}
	}
	if depth != 0 || quote != 0 {
		return ErrDirectives
	}
	return nil
}

// indent shifts directives into the server block.
func indent(s string) string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n ")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			lines[i] = "    " + l
		} else {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}
