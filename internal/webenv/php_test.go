package webenv

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/fcgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPHPConfigNormalize(t *testing.T) {
	c := DefaultPHPConfig()
	c.Extensions = []string{"redis", "imagick", "redis"}
	c.DisableFunctions = []string{"exec", " system ", ""}
	if err := c.Normalize("8.3"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Extensions, ",") != "imagick,redis" || strings.Join(c.DisableFunctions, ",") != "exec,system" {
		t.Fatalf("normalized: %+v", c)
	}
	bad := DefaultPHPConfig()
	bad.Extensions = []string{"sqlsrv"}
	if err := bad.Normalize("7.4"); !errors.Is(err, ErrPHPExtension) {
		t.Fatalf("sqlsrv on 7.4: %v", err)
	}
	bad = DefaultPHPConfig()
	bad.INI["memory_limit"] = "1G\nextension=evil.so"
	if err := bad.Normalize("8.3"); !errors.Is(err, ErrPHPConfig) {
		t.Fatalf("newline in ini value: %v", err)
	}
	bad = DefaultPHPConfig()
	bad.DisableFunctions = []string{"exec;"}
	if err := bad.Normalize("8.3"); !errors.Is(err, ErrPHPConfig) {
		t.Fatalf("bad function name: %v", err)
	}
}

func TestRenderPHP(t *testing.T) {
	e := New(t.TempDir())
	e.Timezone = "Asia/Hong_Kong"
	c := DefaultPHPConfig()
	c.DisableFunctions = []string{"exec", "system"}
	c.CustomINI = "apc.shm_size = 64M"
	c.FPM = FPMPool{PM: "static", MaxChildren: 8, SlowlogTimeout: 5}
	if err := c.Normalize("8.3"); err != nil {
		t.Fatal(err)
	}
	fpm, ini, err := e.renderPHP("8.3", c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"listen = 127.0.0.1:9083", "pm = static", "pm.max_children = 8", "request_slowlog_timeout = 5s", "slowlog = /var/log/lmcwhm-php/slow.log"} {
		if !strings.Contains(string(fpm), want) {
			t.Errorf("fpm missing %q:\n%s", want, fpm)
		}
	}
	if strings.Contains(string(fpm), "pm.start_servers") {
		t.Error("static pool must not set start_servers")
	}
	for _, want := range []string{"date.timezone = Asia/Hong_Kong", "disable_functions = exec,system", "apc.shm_size = 64M", `error_reporting = E_ALL & ~E_DEPRECATED`} {
		if !strings.Contains(string(ini), want) {
			t.Errorf("ini missing %q:\n%s", want, ini)
		}
	}
	if PHPImageFor("8.3", nil) != PHPImage("8.3") || PHPImageFor("8.3", []string{"redis"}) == PHPImage("8.3") {
		t.Error("extension image tag")
	}
}

func TestSuggestFPM(t *testing.T) {
	if p := SuggestFPM(2048); p.MaxChildren != 25 || p.MaxSpare < p.MinSpare {
		t.Fatalf("2 GB: %+v", p)
	}
	if p := SuggestFPM(256); p.MaxChildren != 5 {
		t.Fatalf("256 MB: %+v", p)
	}
}

func TestFastCGIGet(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"pool":"www","active processes":2,"q":"` + r.URL.RawQuery + `"}`))
	}))
	body, err := FastCGIGet(context.Background(), ln.Addr().String(), "/fpm-status", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"active processes":2`) || !strings.Contains(string(body), `"q":"json"`) {
		t.Fatalf("body %s", body)
	}
}

func TestVarnish(t *testing.T) {
	sites := []Site{
		{Domain: "a.com", Aliases: []string{"*.a.com"}, Advanced: Advanced{Varnish: true, VarnishTTL: 600, VarnishExclude: []string{"/api"}}},
		{Domain: "b.com"},
		{Domain: "c.com", Stopped: true, Advanced: Advanced{Varnish: true}},
	}
	vcl := string(VCL(sites))
	for _, want := range []string{`.port = "8080"`, `bereq.http.host == "a.com" || bereq.http.host ~ "\.a\.com$"`, "set beresp.ttl = 600s;", `req.url ~ "^(/api)"`} {
		if !strings.Contains(vcl, want) {
			t.Errorf("vcl missing %q:\n%s", want, vcl)
		}
	}
	if strings.Contains(vcl, "c.com") || strings.Contains(vcl, "b.com") {
		t.Error("only running sites with the cache on belong in the VCL")
	}
	e := New(t.TempDir())
	files, err := e.edgeFiles(sites, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := string(files["maps/cache_exact.map"]); !strings.Contains(m, "a.com 1") || !strings.Contains(m, "b.com 0") || !strings.Contains(m, "c.com 0") {
		t.Fatalf("cache map:\n%s", m)
	}
	if !strings.Contains(string(files["haproxy.cfg"]), "server varnish 127.0.0.1:6081") {
		t.Fatal("be_varnish missing")
	}
	a := Advanced{VarnishExclude: []string{"/ok", "bad path"}}
	if a.Normalize() == nil {
		t.Fatal("bad exclude accepted")
	}
}

func TestPHPSourceDockerfile(t *testing.T) {
	fast, _ := PHPDockerfile("8.3", false)
	src, _ := PHPDockerfile("8.3", true)
	if strings.Contains(fast, "./configure") || !strings.Contains(src, "./configure $OPTS ${CONFIGURE_ARGS}") ||
		!strings.Contains(src, "docker-php-ext-install") || !strings.Contains(fast, "archive.debian.org") {
		t.Fatalf("fast:\n%s\nsource:\n%s", fast, src)
	}
	// The official images record their exported CFLAGS among the configure
	// options, and php-config prints them without quotes: only the "--"
	// options may reach ./configure.
	i := strings.Index(src, `OPTS="$(`)
	j := strings.Index(src[i:], `)" &&`)
	pipe := src[i+len(`OPTS="$(`) : i+j]
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "php-config"), []byte("#!/bin/sh\necho ' --build=x86_64-linux-gnu --with-config-file-path=/usr/local/etc/php --enable-fpm CFLAGS=-fstack-protector-strong -fpic -fpie -O2 - LDFLAGS=-Wl,-O1 -pie'\n"), 0o755)
	cmd := exec.Command("sh", "-c", pipe)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.Output()
	if got := strings.TrimSpace(string(out)); err != nil || got != "--build=x86_64-linux-gnu --with-config-file-path=/usr/local/etc/php --enable-fpm" {
		t.Fatalf("options %q (%v) from %s", got, err, pipe)
	}
	e := New(t.TempDir())
	if b := e.PHPBuildOf("8.3"); b.Mode != "fast" {
		t.Fatalf("default %+v", b)
	}
	e.WritePHPBuild("8.3", PHPBuild{Mode: "source", Args: "--enable-debug"})
	if b := e.PHPBuildOf("8.3"); b.Mode != "source" || b.Args != "--enable-debug" {
		t.Fatalf("stored %+v", b)
	}
}
