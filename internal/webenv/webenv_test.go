package webenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPHPPort(t *testing.T) {
	for v, want := range map[string]int{"7.4": 9074, "8.0": 9080, "8.3": 9083, "8.4": 9084} {
		if got := PHPPort(v); got != want {
			t.Errorf("%s: %d", v, got)
		}
	}
}

func TestRender(t *testing.T) {
	e := &Env{Home: t.TempDir(), WWWRoot: "/www/wwwroot", WWWLogs: "/www/wwwlogs"}
	os.MkdirAll(e.CertDir(), 0o755)
	os.WriteFile(e.CertPath("a.com"), []byte("x"), 0o600)
	sites := []Site{
		{Domain: "a.com", Aliases: []string{"www.a.com"}, Type: "php", PHPVersion: "8.3", Root: "/www/wwwroot/a.com", ForceHTTPS: true, HSTS: true, HasCert: true},
		{Domain: "b.org", Type: "static", Root: "/www/wwwroot/b.org", ForceHTTPS: true}, // no cert: no redirect
		{Domain: "c.net", Type: "static", Root: "/www/wwwroot/c.net", Stopped: true, HasCert: true, ForceHTTPS: true},
	}
	edge, err := e.edgeFiles(sites, []Passthrough{{Host: "app.example.com", Target: "127.0.0.1:9443", ProxyProtocol: true}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(edge["haproxy.cfg"])
	for _, want := range []string{"bind 0.0.0.0:443", "bind 0.0.0.0:80", "req.ssl_sni,lower,map_str(", "map_end(", "abns@lmcwhm_https", "server s1 127.0.0.1:9443 send-proxy-v2", "127.0.0.1:8899", "127.0.0.1:8080"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("haproxy.cfg missing %q", want)
		}
	}
	if got := string(edge["maps/force_https_exact.map"]); got != "a.com 1\nb.org 0\nc.net 0\nwww.a.com 1\n" {
		t.Errorf("force_https_exact.map = %q", got)
	}
	if got := string(edge["maps/sni_exact.map"]); got != "a.com be_terminate\napp.example.com be_pass_0\nb.org be_terminate\nc.net be_terminate\nwww.a.com be_terminate\n" {
		t.Errorf("sni_exact.map = %q", got)
	}
	crt := string(edge["crt-list.txt"])
	if !strings.HasPrefix(crt, filepath.Join(e.CertDir(), "default.pem")) || !strings.Contains(crt, "a.com.pem") || strings.Contains(crt, "c.net") {
		t.Errorf("crt-list = %q", crt)
	}

	ng, err := e.nginxFiles(sites)
	if err != nil {
		t.Fatal(err)
	}
	a := string(ng["conf.d/a.com.conf"])
	if !strings.Contains(a, "server_name a.com www.a.com;") || !strings.Contains(a, "fastcgi_pass 127.0.0.1:9083;") ||
		!strings.Contains(a, "include /etc/nginx/fastcgi_params;") { // nginx runs with -c: relative includes would miss
		t.Errorf("a.com vhost:\n%s", a)
	}
	if b := string(ng["conf.d/b.org.conf"]); strings.Contains(b, "fastcgi_pass") {
		t.Errorf("static site has php:\n%s", b)
	}
	if c := string(ng["conf.d/c.net.conf"]); !strings.Contains(c, "location / {\n        return 404;") {
		t.Errorf("stopped site not 404:\n%s", c)
	}
}

func TestRenderProxy(t *testing.T) {
	e := &Env{Home: t.TempDir(), WWWRoot: "/www/wwwroot", WWWLogs: "/www/wwwlogs"}
	sites := []Site{
		{Domain: "node.test", Type: "node", Root: "/www/wwwroot/node.test", Upstream: "http://127.0.0.1:3000"},
		{Domain: "tls.test", Aliases: []string{"www.tls.test"}, Type: "proxy", Upstream: "127.0.0.1:8443", Passthrough: true},
		{Domain: "off.test", Type: "proxy", Upstream: "127.0.0.1:9443", Passthrough: true, Stopped: true},
	}
	ng, err := e.nginxFiles(sites)
	if err != nil {
		t.Fatal(err)
	}
	n := string(ng["conf.d/node.test.conf"])
	if !strings.Contains(n, "proxy_pass http://127.0.0.1:3000;") || !strings.Contains(n, "$connection_upgrade") || strings.Contains(n, "fastcgi_pass") {
		t.Errorf("node vhost:\n%s", n)
	}
	if _, ok := ng["conf.d/tls.test.conf"]; ok {
		t.Error("passthrough site has an nginx vhost")
	}
	edge, err := e.edgeFiles(sites, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(edge["maps/sni_exact.map"]); got != "node.test be_terminate\noff.test be_terminate\ntls.test be_pass_0\nwww.tls.test be_pass_0\n" {
		t.Errorf("sni_exact.map = %q", got)
	}
	if cfg := string(edge["haproxy.cfg"]); !strings.Contains(cfg, "server s1 127.0.0.1:8443\n") || strings.Contains(cfg, "9443") {
		t.Errorf("haproxy.cfg passthrough backends:\n%s", cfg)
	}
	if got := string(edge["maps/force_https_exact.map"]); got != "node.test 0\noff.test 0\ntls.test 1\nwww.tls.test 1\n" {
		t.Errorf("force_https_exact.map = %q", got)
	}
}

func TestWildcardMaps(t *testing.T) {
	e := &Env{Home: t.TempDir(), WWWRoot: "/www/wwwroot", WWWLogs: "/www/wwwlogs"}
	sites := []Site{
		{Domain: "a.com", Aliases: []string{"*.a.com"}, Type: "static", ForceHTTPS: true, HasCert: true},
		{Domain: "x.a.com", Type: "proxy", Upstream: "127.0.0.1:9443", Passthrough: true},
		{Domain: "b.a.com", Aliases: []string{"*.b.a.com"}, Type: "proxy", Upstream: "127.0.0.1:9444", Passthrough: true},
	}
	edge, err := e.edgeFiles(sites, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(edge["maps/sni_wild.map"]); got != ".b.a.com be_pass_1\n.a.com be_terminate\n" {
		t.Errorf("sni_wild.map = %q", got)
	}
	if got := string(edge["maps/force_https_wild.map"]); got != ".b.a.com 1\n.a.com 1\n" {
		t.Errorf("force_https_wild.map = %q", got)
	}
	pats := []string{"a.com", "*.a.com", "x.a.com", "b.a.com", "*.b.a.com"}
	for host, want := range map[string]string{
		"a.com": "a.com", "x.a.com": "x.a.com", "y.a.com": "*.a.com", "c.b.a.com": "*.b.a.com",
		"d.x.a.com": "*.a.com", "a.com.evil.org": "", "xa.com": "",
	} {
		if got := MatchHost(pats, host); got != want {
			t.Errorf("MatchHost(%s) = %q, want %q", host, got, want)
		}
	}
}

func TestWAF(t *testing.T) {
	e := &Env{Home: t.TempDir(), WWWRoot: "/www/wwwroot", WWWLogs: "/www/wwwlogs"}
	sites := []Site{
		{Domain: "a.com", Type: "static", Root: "/www/wwwroot/a.com", WAFExclude: []string{"942100", "x; SecRuleEngine Off", "941100"}},
		{Domain: "b.com", Type: "static", Root: "/www/wwwroot/b.com", WAFOff: true},
	}
	ng, _ := e.nginxFiles(sites)
	if strings.Contains(string(ng["nginx.conf"]), "modsecurity") || strings.Contains(string(ng["conf.d/a.com.conf"]), "modsecurity") {
		t.Error("modsecurity rendered while the WAF is off")
	}
	if a := strings.Join(e.nginxArgs(), " "); !strings.Contains(a, NginxImage+" nginx -c /etc/nginx/lmcwhm/nginx.conf -g daemon off;") || strings.Contains(a, "MODSEC") {
		t.Errorf("plain nginx args: %s", a)
	}
	e.WAF = func() WAFSettings { return WAFSettings{Enabled: true, Paranoia: 9} }
	ng, _ = e.nginxFiles(sites)
	main := string(ng["nginx.conf"])
	if !strings.HasPrefix(main, "# Generated by lmcwhm — do not edit.\nload_module /etc/nginx/modules/ngx_http_modsecurity_module.so;\nuser nginx;") || !strings.Contains(main, "modsecurity_rules_file /etc/modsecurity.d/setup.conf;") || !strings.Contains(main, "include /etc/nginx/lmcwhm/conf.d/*.conf;") {
		t.Errorf("nginx.conf:\n%s", main)
	}
	if a := string(ng["conf.d/a.com.conf"]); !strings.Contains(a, "modsecurity_rules 'SecRuleRemoveById 942100 941100';") || strings.Contains(a, "SecRuleEngine") {
		t.Errorf("a.com:\n%s", a)
	}
	if b := string(ng["conf.d/b.com.conf"]); !strings.Contains(b, "modsecurity off;") {
		t.Errorf("b.com:\n%s", b)
	}
	a := strings.Join(e.nginxArgs(), " ")
	for _, want := range []string{WAFImage + " nginx -c", "--user 0", "MODSEC_RULE_ENGINE=On", "PARANOIA=4", "ANOMALY_INBOUND=5", "MODSEC_AUDIT_LOG=/www/wwwlogs/waf/audit.log"} {
		if !strings.Contains(a, want) {
			t.Errorf("WAF args missing %q: %s", want, a)
		}
	}
}

func TestRedirectVhost(t *testing.T) {
	e := &Env{Home: t.TempDir(), WWWRoot: "/www/wwwroot", WWWLogs: "/www/wwwlogs"}
	ng, err := e.nginxFiles([]Site{
		{Domain: "old.com", Type: "static", Redirect: "https://new.com", RedirectCode: 308, RedirectPath: true},
		{Domain: "x.com", Type: "static", Redirect: "https://y.com/landing", RedirectCode: 999},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v := string(ng["conf.d/old.com.conf"]); !strings.Contains(v, "return 308 https://new.com$request_uri;") || strings.Contains(v, "try_files") {
		t.Errorf("redirect vhost:\n%s", v)
	}
	if v := string(ng["conf.d/x.com.conf"]); !strings.Contains(v, "return 301 https://y.com/landing;") {
		t.Errorf("redirect without path:\n%s", v)
	}
}
