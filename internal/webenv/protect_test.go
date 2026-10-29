package webenv

import (
	"strings"
	"testing"
)

func TestProtectionConfig(t *testing.T) {
	e := &Env{Home: t.TempDir(), Protection: func() Protection {
		return Protection{
			Sites:     map[string]SiteProtect{"a.com": {On: true, FakeSpider: true, IDC: true, CC: 30}, "off.com": {}},
			Default:   SiteProtect{On: true},
			SpiderUA:  []string{"Googlebot"},
			CrawlerUA: []string{"Scrapy"},
			IDC:       []string{"3.0.0.0/9"},
			Rules: []EdgeRule{
				{Code: "wp-1002", All: true, Conds: []EdgeCond{{Field: "path", Op: "suffix", Values: []string{"/xmlrpc.php"}}}},
				{Code: "wp-1001", Conds: []EdgeCond{{Field: "path", Op: "prefix", Values: []string{"/wp-json/wp/v2/users"}}, {Field: "query", Op: "contains", Values: []string{"rest_route=/wp/v2/users"}}}},
				{Code: "c7", All: true, Conds: []EdgeCond{{Field: "body", Op: "contains", Values: []string{"give_title"}}, {Field: "path", Op: "prefix", Negate: true, Values: []string{"/wp-admin/"}}}},
				{Code: "BAD CODE", Conds: []EdgeCond{{Field: "path", Op: "equals", Values: []string{"/x"}}}},
				{Code: "c8", Conds: []EdgeCond{{Field: "host", Op: "equals", Values: []string{"/x"}}}},
			},
		}
	}}
	sites := []Site{{Domain: "a.com", Aliases: []string{"www.a.com"}}, {Domain: "off.com"}, {Domain: "new.com"}, {Domain: "pass.com", Passthrough: true, Upstream: "127.0.0.1:9"}}
	edge, err := e.edgeFiles(sites, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(edge["maps/prot_exact.map"]); got != "a.com ,on,fake,idc,\nnew.com ,on,\noff.com ,\npass.com ,\nwww.a.com ,on,fake,idc,\n" {
		t.Errorf("prot map = %q", got)
	}
	if got := string(edge["maps/cc_exact.map"]); got != "a.com 30\nwww.a.com 30\n" {
		t.Errorf("cc map = %q", got)
	}
	cfg := string(edge["haproxy.cfg"])
	d := e.EdgeDir()
	for _, want := range []string{
		"str(rule-wp-1002) if !blocked guard !exempt { path -m end -i -f " + d + "/maps/rules/wp-1002_0.lst }",
		"str(rule-wp-1001) if !blocked guard !exempt { path -m beg -i -f " + d + "/maps/rules/wp-1001_0.lst }",
		"str(rule-wp-1001) if !blocked guard !exempt { query -m sub -i -f " + d + "/maps/rules/wp-1001_1.lst }",
		"{ req.body -m sub -i -f " + d + "/maps/rules/c7_0.lst } !{ path -m beg -i -f " + d + "/maps/rules/c7_1.lst }",
		"option http-buffer-request", "stick-table type ipv6", "sc_http_req_rate(0),sub(txn.cc) gt 0",
		"use_backend be_web if blocked", "http-request set-header X-Lmcwhm-Block %[var(txn.block)] if blocked",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("haproxy.cfg missing %q", want)
		}
	}
	if strings.Contains(cfg, "BAD CODE") || strings.Contains(cfg, "rule-c8") {
		t.Error("invalid rule rendered")
	}
	if string(edge["maps/rules/wp-1002_0.lst"]) != "/xmlrpc.php\n" || string(edge["maps/idc.lst"]) != "3.0.0.0/9\n" || string(edge["maps/spider_ua.lst"]) != "Googlebot\n" {
		t.Error("pattern files")
	}
	ng, _ := e.nginxFiles(sites)
	if v := string(ng["conf.d/a.com.conf"]); !strings.Contains(v, "if ($http_x_lmcwhm_block = cc) {\n        return 429;") || !strings.Contains(v, "if ($http_x_lmcwhm_block) {\n        return 403;") {
		t.Errorf("vhost does not answer edge blocks:\n%s", v)
	}
	if p := e.RulePreview(EdgeRule{Code: "new", All: true, Conds: []EdgeCond{{Field: "ua", Op: "regex", Values: []string{"bot"}}}}); !strings.Contains(p, "req.hdr(user-agent) -m reg -i") || !strings.Contains(p, "\nbot\n") {
		t.Errorf("preview %q", p)
	}
}
