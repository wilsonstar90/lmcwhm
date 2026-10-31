package webenv

import (
	"strings"
	"testing"
)

func TestSiteSettings(t *testing.T) {
	e := &Env{Home: t.TempDir(), WWWRoot: "/www/wwwroot", WWWLogs: "/www/wwwlogs"}
	e.ErrorPages = func() ErrorPages { return ErrorPages{Pages: map[string][]byte{"p3": []byte("<h1>off</h1>")}} }
	adv := Advanced{
		NoAccessLog: true, ServerConn: 300, ConnLimit: 25, LimitRateKB: 512,
		BasicAuth: &AuthUser{User: "boss", Password: "s3cret"},
		AuthDirs:  []AuthDir{{Name: "admin", Path: "/admin/", AuthUser: AuthUser{User: "ops", Password: "pw"}}},
		Proxies: []PathProxy{
			{Name: "api", Path: "/api/", Target: "http://127.0.0.1:3000", Enabled: true},
			{Name: "off", Path: "/old/", Target: "http://127.0.0.1:3001"},
		},
		Redirects:  []Redirect{{From: "/a", To: "/b", Code: 308, KeepPath: true}},
		Hotlink:    []string{"cdn.a.com"},
		HotlinkExt: []string{".PNG", "mp4"}, HotlinkDenyEmpty: true,
	}
	if err := adv.Normalize(); err != nil {
		t.Fatal(err)
	}
	if adv.BasicAuth.Password != "" || !strings.HasPrefix(adv.BasicAuth.Hash, "{SSHA}") || adv.HotlinkExt[0] != "png" {
		t.Fatalf("normalize %+v", adv)
	}
	sites := []Site{
		{Domain: "a.com", Type: "php", PHPVersion: "8.3", Root: "/www/wwwroot/a.com/public", Advanced: adv,
			Index: []string{"home.html", "index.php"}, Rewrite: "    location / {\n        try_files $uri $uri/ /index.php?$args;\n    }",
			Custom: "    add_header X-Test 1;"},
		{Domain: "off.com", Type: "static", Root: "/www/wwwroot/off.com", Stopped: true, StopPage: "p3"},
		{Domain: "gone.com", Type: "static", Root: "/www/wwwroot/gone.com", Stopped: true, StopPage: "p9"}, // page deleted: 404
	}
	files, err := e.nginxFiles(sites)
	if err != nil {
		t.Fatal(err)
	}
	a := string(files["conf.d/a.com.conf"])
	for _, want := range []string{
		"index home.html index.php;", "access_log off;", "limit_conn_zone $server_name zone=srv_a_com:1m;",
		"limit_conn srv_a_com 300;", "limit_conn conn_a_com 25;", "limit_rate 512k;",
		"auth_basic_user_file /etc/nginx/lmcwhm/auth/a.com.htpasswd;", "auth_basic_user_file /etc/nginx/lmcwhm/auth/a.com.0.htpasswd;",
		"location ^~ /api/ {\n        proxy_pass http://127.0.0.1:3000;", "return 308 /b$is_args$args;",
		"try_files $uri $uri/ /index.php?$args;", "add_header X-Test 1;", "location ~* \\.(?:png|mp4)$",
		"valid_referers blocked server_names cdn.a.com", "root /www/wwwroot/a.com/public;",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("a.com missing %q:\n%s", want, a)
		}
	}
	for _, not := range []string{"/old/", "$query_string;\n    }\n\n    location ~ \\.php$"} {
		if strings.Contains(a, not) {
			t.Errorf("a.com has %q", not)
		}
	}
	if strings.Count(a, "fastcgi_pass 127.0.0.1:9083;") != 2 { // the site and the password-protected directory
		t.Errorf("php locations:\n%s", a)
	}
	if h := string(files["auth/a.com.htpasswd"]); !strings.HasPrefix(h, "boss:{SSHA}") {
		t.Errorf("htpasswd %q", h)
	}
	if off := string(files["conf.d/off.com.conf"]); !strings.Contains(off, "error_page 503 /__lmcwhm_stop/p3.html;") || !strings.Contains(off, "return 503;") {
		t.Errorf("stopped with page:\n%s", off)
	}
	if gone := string(files["conf.d/gone.com.conf"]); !strings.Contains(gone, "location / {\n        return 404;") {
		t.Errorf("stopped without page:\n%s", gone)
	}

	// The browser never sees the hashes; sending the settings back keeps them.
	pub := adv.Public()
	if pub.BasicAuth.Hash != "" || pub.AuthDirs[0].Hash != "" || adv.BasicAuth.Hash == "" {
		t.Fatalf("public %+v", pub)
	}
	pub.KeepHashes(adv)
	if err := pub.Normalize(); err != nil || pub.BasicAuth.Hash != adv.BasicAuth.Hash || pub.AuthDirs[0].Hash != adv.AuthDirs[0].Hash {
		t.Fatalf("keep hashes %v %+v", err, pub)
	}
	// A new user without a password is refused.
	bad := Advanced{BasicAuth: &AuthUser{User: "x"}}
	if bad.Normalize() == nil {
		t.Error("user without password accepted")
	}
	for _, p := range []PathProxy{{Path: "api", Target: "http://x"}, {Path: "/x", Target: "http://x;evil"}, {Path: "/x", Target: "http://x", Host: "a b"}} {
		if (&Advanced{Proxies: []PathProxy{p}}).Normalize() == nil {
			t.Errorf("proxy %+v accepted", p)
		}
	}
}

func TestRewrites(t *testing.T) {
	e := &Env{Home: t.TempDir(), WWWRoot: "/www/wwwroot", WWWLogs: "/www/wwwlogs"}
	names := RewriteNames()
	if names[0] != "wordpress" || len(names) < 10 {
		t.Fatalf("names %v", names)
	}
	for n, body := range Rewrites() {
		if err := CheckDirectives(body); err != nil {
			t.Errorf("%s: %v", n, err)
		}
		files, err := e.nginxFiles([]Site{{Domain: "r.com", Type: "php", PHPVersion: "8.3", Root: "/www/wwwroot/r.com", Rewrite: body}})
		if err != nil {
			t.Fatal(err)
		}
		if c := string(files["conf.d/r.com.conf"]); strings.Count(c, "location / {") > 1 {
			t.Errorf("%s: two root locations:\n%s", n, c)
		}
	}
	for s, ok := range map[string]bool{
		"location / { try_files $uri /x; }":      true,
		"# a } in a comment\nadd_header X 1;":    true,
		"add_header X \"}\";":                    true,
		"}\nserver { listen 80; }\nlocation / {": false,
		"location / {":                           false,
		"\"open":                                 false,
	} {
		if (CheckDirectives(s) == nil) != ok {
			t.Errorf("%q: want ok=%v", s, ok)
		}
	}
}
