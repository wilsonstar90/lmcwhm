package webenv

import (
	"strings"
	"testing"
)

func TestGRPCConfig(t *testing.T) {
	on := true
	e := &Env{Home: t.TempDir(), WWWLogs: "/www/wwwlogs", GRPC: func() bool { return on },
		WAF: func() WAFSettings { return WAFSettings{Enabled: true} },
		Protection: func() Protection {
			return Protection{Rules: []EdgeRule{{Code: "b1", Conds: []EdgeCond{{Field: "body", Op: "contains", Values: []string{"x"}}}}}}
		}}
	sites := []Site{
		{Domain: "rpc.test", Type: "proxy", Upstream: "grpc://127.0.0.1:9000", HasCert: true, Advanced: Advanced{Varnish: true}},
		{Domain: "web.test", Type: "proxy", Upstream: "http://127.0.0.1:3000"},
	}
	edge, err := e.edgeFiles(sites, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(edge["haproxy.cfg"])
	for _, want := range []string{
		"acl grpc_host var(txn.grpc) -m str 1",
		"use_backend be_web_h2 if grpc_host",
		"server nginx 127.0.0.1:8081 proto h2",
		"http-request set-timeout client 1h if grpc_host",
		"http-request wait-for-body time 15s if !grpc_host",
		"!grpc_host\n", // the body rule skips gRPC hosts
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("haproxy.cfg missing %q", want)
		}
	}
	if strings.Contains(cfg, "http-buffer-request") {
		t.Error("frontend-wide body buffering would stall streams")
	}
	if !strings.Contains(string(edge["maps/grpc_exact.map"]), "rpc.test 1") || !strings.Contains(string(edge["maps/cache_exact.map"]), "rpc.test 0") {
		t.Errorf("maps: grpc=%q cache=%q", edge["maps/grpc_exact.map"], edge["maps/cache_exact.map"])
	}

	ng, err := e.nginxFiles(sites)
	if err != nil {
		t.Fatal(err)
	}
	main := string(ng["nginx.conf"])
	if !strings.Contains(main, "listen 127.0.0.1:8081 default_server;\n        http2 on;") || !strings.Contains(main, "log_format grpc") {
		t.Errorf("nginx.conf without the gRPC listener:\n%s", main)
	}
	v := string(ng["conf.d/rpc.test.conf"])
	for _, want := range []string{"listen 127.0.0.1:8081;\n    http2 on;", "grpc_pass grpc://127.0.0.1:9000;",
		"modsecurity off;", "rpc.test.log grpc;"} {
		if !strings.Contains(v, want) {
			t.Errorf("gRPC vhost missing %q:\n%s", want, v)
		}
	}
	if w := string(ng["conf.d/web.test.conf"]); strings.Contains(w, "8081") || strings.Contains(w, "grpc") {
		t.Error("an HTTP site got gRPC settings")
	}
	// Without the WAF, nginx has no ModSecurity module to switch off.
	waf := e.WAF
	e.WAF = func() WAFSettings { return WAFSettings{} }
	ng, _ = e.nginxFiles(sites)
	if strings.Contains(string(ng["conf.d/rpc.test.conf"]), "modsecurity") {
		t.Error("modsecurity directive in a gRPC vhost without the WAF")
	}
	e.WAF = waf

	// Switched off: nothing of it is generated.
	on = false
	edge, _ = e.edgeFiles(sites, nil)
	ng, _ = e.nginxFiles(sites)
	if c := string(edge["haproxy.cfg"]); strings.Contains(c, "grpc") || strings.Contains(c, "8081") {
		t.Error("gRPC edge config while switched off")
	}
	if strings.Contains(string(ng["nginx.conf"]), "8081") {
		t.Error("gRPC listener while switched off")
	}
	if v := string(ng["conf.d/rpc.test.conf"]); strings.Contains(v, "grpc://") || !strings.Contains(v, "return 404;") {
		t.Errorf("a gRPC site while switched off must not be proxied:\n%s", v)
	}
}
