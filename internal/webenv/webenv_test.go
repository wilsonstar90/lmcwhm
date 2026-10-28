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
	for _, want := range []string{"bind 0.0.0.0:443", "bind 0.0.0.0:80", "req.ssl_sni,lower,map_dom(", "abns@lmcwhm_https", "server s1 127.0.0.1:9443 send-proxy-v2", "127.0.0.1:8899", "127.0.0.1:8080"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("haproxy.cfg missing %q", want)
		}
	}
	if got := string(edge["maps/force_https.map"]); got != "a.com 1\nwww.a.com 1\n" {
		t.Errorf("force_https.map = %q", got)
	}
	if got := string(edge["maps/passthrough.map"]); got != "app.example.com be_pass_0\n" {
		t.Errorf("passthrough.map = %q", got)
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
	if !strings.Contains(a, "server_name a.com www.a.com;") || !strings.Contains(a, "fastcgi_pass 127.0.0.1:9083;") {
		t.Errorf("a.com vhost:\n%s", a)
	}
	if b := string(ng["conf.d/b.org.conf"]); strings.Contains(b, "fastcgi_pass") {
		t.Errorf("static site has php:\n%s", b)
	}
	if c := string(ng["conf.d/c.net.conf"]); !strings.Contains(c, "return 503") {
		t.Errorf("stopped site not 503:\n%s", c)
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
	if got := string(edge["maps/passthrough.map"]); got != "tls.test be_pass_0\nwww.tls.test be_pass_1\n" {
		t.Errorf("passthrough.map = %q", got)
	}
	if cfg := string(edge["haproxy.cfg"]); !strings.Contains(cfg, "server s1 127.0.0.1:8443\n") || strings.Contains(cfg, "9443") {
		t.Errorf("haproxy.cfg passthrough backends:\n%s", cfg)
	}
	if got := string(edge["maps/force_https.map"]); got != "tls.test 1\nwww.tls.test 1\n" {
		t.Errorf("force_https.map = %q", got)
	}
}
