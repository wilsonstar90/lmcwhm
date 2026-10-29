package protect

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, nil, nil)
}

func TestBuiltinsValid(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range builtins {
		r := b
		if err := r.validate(); err != nil || !webenv.ValidCode(b.Code) || seen[b.Code] {
			t.Errorf("%s: %v", b.Code, err)
		}
		seen[b.Code] = true
		rd, _, _ := webenv.RuleConfig("/e", webenv.EdgeRule{Code: b.Code, All: b.All, Conds: edgeConds(b.Conds)})
		if len(rd.Lines) == 0 {
			t.Errorf("%s does not compile", b.Code)
		}
	}
}

func edgeConds(cs []Cond) []webenv.EdgeCond {
	var out []webenv.EdgeCond
	for _, c := range cs {
		out = append(out, webenv.EdgeCond{Field: c.Field, Op: c.Op, Negate: c.Negate, Values: c.Values})
	}
	return out
}

func TestRules(t *testing.T) {
	sv := newSvc(t)
	ctx := context.Background()
	rules, err := sv.Rules(ctx)
	if err != nil || len(rules) != len(builtins) {
		t.Fatalf("%d rules, %v", len(rules), err)
	}
	// Off by default: nothing reaches the edge until the switch is on.
	if p := sv.Edge(ctx); len(p.Rules) != 0 || !p.Default.On {
		t.Fatalf("edge %+v", p)
	}
	sv.SaveSettings(ctx, Settings{RulesOn: true, Crawlers: []string{"Scrapy", " Scrapy "}, IDC: defaultIDC})
	on := 0
	for _, b := range builtins {
		if b.Enabled {
			on++
		}
	}
	if p := sv.Edge(ctx); len(p.Rules) != on || len(p.CrawlerUA) != 1 {
		t.Fatalf("edge rules %d, want %d; crawlers %v", len(p.Rules), on, p.CrawlerUA)
	}
	for _, bad := range []Rule{
		{Name: "x"}, {Name: "x", Conds: []Cond{{Field: "host", Op: "equals", Values: []string{"a"}}}},
		{Name: "x", Conds: []Cond{{Field: "path", Op: "regex", Values: []string{"("}}}},
		{Name: "x", Conds: []Cond{{Field: "path", Op: "contains", Values: []string{"#a"}}}},
	} {
		if _, err := sv.SaveRule(ctx, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	r, err := sv.SaveRule(ctx, Rule{Name: "Block .env", CMS: "Custom", Enabled: true, All: true, Conds: []Cond{{Field: "path", Op: "suffix", Values: []string{"/.env", " "}}}})
	if err != nil || r.Code != "c"+strings.TrimPrefix(r.Code, "c") || r.CMS != "custom" || len(r.Conds[0].Values) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	// Built-ins can be edited and reset, not deleted.
	rules, _ = sv.Rules(ctx)
	wp := rules[0]
	wp.Conds[0].Values = append(wp.Conds[0].Values, "/x")
	if _, err := sv.SaveRule(ctx, wp); err != nil {
		t.Fatal(err)
	}
	if rules, _ = sv.Rules(ctx); !rules[0].Modified {
		t.Error("edit not marked modified")
	}
	if err := sv.DeleteRule(ctx, wp.ID); err != ErrBuiltin {
		t.Errorf("deleted a built-in: %v", err)
	}
	sv.ResetRule(ctx, wp.ID)
	if rules, _ = sv.Rules(ctx); rules[0].Modified {
		t.Error("reset kept the edit")
	}
	if err := sv.DeleteRule(ctx, r.ID); err != nil {
		t.Error(err)
	}
}

func TestSites(t *testing.T) {
	sv := newSvc(t)
	ctx := context.Background()
	if err := sv.SaveSites(ctx, map[string]webenv.SiteProtect{"a.com": {On: true, FakeSpider: true, CC: 50}, "b.com": {}}); err != nil {
		t.Fatal(err)
	}
	if err := sv.SaveSites(ctx, map[string]webenv.SiteProtect{"a.com": {CC: -1}}); err == nil {
		t.Error("negative CC accepted")
	}
	m, _ := sv.Sites(ctx)
	if !m["a.com"].FakeSpider || m["a.com"].CC != 50 || m["b.com"].On {
		t.Fatalf("%+v", m)
	}
}
