package webenv

import (
	"strings"
	"testing"
)

func TestErrorPages(t *testing.T) {
	e := &Env{Home: t.TempDir(), WWWRoot: "/www/wwwroot", WWWLogs: "/www/wwwlogs",
		WAF: func() WAFSettings { return WAFSettings{Enabled: true, Disabled: []string{"942100", "bad", "920350"}} },
		ErrorPages: func() ErrorPages {
			return ErrorPages{
				Pages:     map[string][]byte{"p1": []byte("<h1>blocked</h1>"), "p2": []byte("<h1>gone</h1>")},
				Global:    map[int]string{403: "p1", 404: "p2", 502: "p2", 410: "missing"},
				Sites:     map[string]map[int]string{"b.com": {403: "", 451: "p1"}},
				Intercept: map[string]bool{"b.com": true},
			}
		}}
	ng, err := e.nginxFiles([]Site{
		{Domain: "a.com", Type: "static", Root: "/www/wwwroot/a.com"},
		{Domain: "b.com", Type: "php", PHPVersion: "8.3", Root: "/www/wwwroot/b.com"},
		{Domain: "own.com", Type: "static", Root: "/www/wwwroot/own.com", Advanced: Advanced{ErrorPages: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(ng["errors/p1.html"]) != "<h1>blocked</h1>" {
		t.Error("page file")
	}
	a := string(ng["conf.d/a.com.conf"])
	for _, want := range []string{"error_page 403 /__lmcwhm_err/p1.html;", "error_page 404 /__lmcwhm_err/p2.html;", "error_page 502 /__lmcwhm_err/p2.html;",
		"location ^~ /__lmcwhm_err/ {\n        internal;\n        ssi on;\n        auth_basic off;\n        alias /etc/nginx/lmcwhm/errors/;"} {
		if !strings.Contains(a, want) {
			t.Errorf("a.com missing %q:\n%s", want, a)
		}
	}
	if strings.Contains(a, "error_page 410") || strings.Contains(a, "intercept_errors") {
		t.Errorf("a.com: page that does not exist, or intercept:\n%s", a)
	}
	b := string(ng["conf.d/b.com.conf"])
	if strings.Contains(b, "error_page 403") || !strings.Contains(b, "error_page 451 /__lmcwhm_err/p1.html;") || !strings.Contains(b, "fastcgi_intercept_errors on;") {
		t.Errorf("b.com override:\n%s", b)
	}
	own := string(ng["conf.d/own.com.conf"])
	if strings.Contains(own, "error_page 404 /__lmcwhm_err") || !strings.Contains(own, "error_page 404 /404.html;") || !strings.Contains(own, "error_page 403 /__lmcwhm_err/p1.html;") {
		t.Errorf("own pages:\n%s", own)
	}
	main := string(ng["nginx.conf"])
	if !strings.Contains(main, "SecRuleRemoveById 942100 920350") || !strings.Contains(main, "error_page 404 /__lmcwhm_err/p2.html;") {
		t.Errorf("nginx.conf:\n%s", main)
	}
}
