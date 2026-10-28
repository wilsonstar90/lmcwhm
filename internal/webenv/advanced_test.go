package webenv

import (
	"strings"
	"testing"
)

func TestAdvancedVhost(t *testing.T) {
	a := Advanced{
		Autoindex: true, AllowIPs: []string{"10.0.0.0/8", " 203.0.113.9 "}, DenyIPs: []string{"198.51.100.0/24"},
		DenyUA: []string{"MJ12bot", "python-requests/2.0"}, RateLimit: 10, RateBurst: 20, ConnLimit: 30, BlockXMLRPC: true,
		Hotlink: []string{"*.example.com"}, MaxBodyMB: 512, Redirects: []Redirect{{From: "/old", To: "https://new.example.com/"}},
	}
	if err := a.Normalize(); err != nil {
		t.Fatal(err)
	}
	e := New(t.TempDir())
	conf, err := e.VhostConfig(Site{Domain: "a.example.com", Type: "php", PHPVersion: "8.3", Root: "/www/wwwroot/a.example.com", Advanced: a})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"limit_req_zone $binary_remote_addr zone=req_a_example_com:10m rate=10r/s;",
		"limit_req zone=req_a_example_com burst=20 nodelay;", "limit_conn conn_a_example_com 30;",
		"deny 198.51.100.0/24;", "allow 10.0.0.0/8;", "allow 203.0.113.9;", "deny all;",
		`~* "MJ12bot|python-requests/2\.0"`, "autoindex on;", "location = /xmlrpc.php", "client_max_body_size 512m;",
		"location = /old {\n        return 301 https://new.example.com/;", "valid_referers none blocked server_names *.example.com ;",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in:\n%s", want, conf)
		}
	}
	for _, bad := range []Advanced{
		{DenyUA: []string{`x";} location / { root /`}},
		{Redirects: []Redirect{{From: "/a b", To: "/c"}}},
		{Redirects: []Redirect{{From: "/a", To: "/c; return 200"}}},
		{AllowIPs: []string{"not-an-ip"}},
		{Hotlink: []string{"a.com; deny"}},
	} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
