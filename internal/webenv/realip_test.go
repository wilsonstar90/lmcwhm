package webenv

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestRealIPConfig(t *testing.T) {
	e := &Env{Home: t.TempDir(), CDN: func() CDN {
		return CDN{Cloudflare: []string{"104.16.0.0/13", "198.51.100.0/24"}, Ranges: []string{"203.0.113.7", "bad"}, Header: "X-Real-IP"}
	}}
	edge, err := e.edgeFiles(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(edge["haproxy.cfg"])
	for _, want := range []string{
		"http-request set-src req.hdr_ip(CF-Connecting-IP,1) if from_cf",
		"http-request del-header CF-Connecting-IP if !from_cf",
		"http-request set-src req.hdr_ip(X-Real-IP,-1) if from_cdn !from_cf",
		"lists/block.lst } !{ src -f " + e.EdgeDir() + "/lists/allow.lst } !acme",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("haproxy.cfg missing %q", want)
		}
	}
	// set-src must come before the deny, which must come before routing.
	if i, j, k := strings.Index(cfg, "set-src"), strings.Index(cfg, "deny_status 403"), strings.Index(cfg, "use_backend be_acme"); !(i < j && j < k) {
		t.Error("real-ip / deny / routing out of order")
	}
	cf := string(edge["maps/cdn_cf.lst"])
	if strings.Count(cf, "104.16.0.0/13") != 1 || !strings.Contains(cf, "198.51.100.0/24") || !strings.Contains(cf, "2606:4700::/32") {
		t.Errorf("cdn_cf.lst = %q", cf)
	}
	if got := string(edge["maps/cdn_custom.lst"]); got != "203.0.113.7/32\n" {
		t.Errorf("cdn_custom.lst = %q", got)
	}
	// Without custom ranges there is no custom header rule.
	e.CDN = func() CDN { return CDN{Header: "X-Real-IP"} }
	edge, _ = e.edgeFiles(nil, nil)
	if strings.Contains(string(edge["haproxy.cfg"]), "from_cdn") {
		t.Error("custom CDN rule without ranges")
	}
}

// fakeHAProxy answers the admin socket in prompt mode and records commands.
func fakeHAProxy(t *testing.T, sock string) *[]string {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var got []string
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			r := bufio.NewScanner(c)
			for r.Scan() {
				cmd := r.Text()
				switch {
				case cmd == "prompt":
					c.Write([]byte("\n> "))
					continue
				case cmd == "quit":
					c.Close()
				case strings.HasPrefix(cmd, "prepare acl"):
					c.Write([]byte("New version created: 7\n\n> "))
				case strings.Contains(cmd, "bogus"):
					c.Write([]byte("'add acl' expects two parameters\n\n> "))
				default:
					c.Write([]byte("\n> "))
				}
				got = append(got, cmd)
			}
		}
	}()
	return &got
}

func TestSetLists(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "lw") // unix socket paths are short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	e := &Env{Home: home}
	ctx := context.Background()
	// Not running: only the files.
	if err := e.SetLists(ctx, []string{"192.0.2.1"}, nil); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(home, "edge", "run"), 0o755)
	got := fakeHAProxy(t, e.adminSock())
	block := filepath.Join(e.listsDir(), blockList)
	if err := e.SetLists(ctx, []string{"192.0.2.2", "2001:db8::/48"}, []string{"198.51.100.0/24"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"add acl " + block + " 192.0.2.2", "add acl " + block + " 2001:db8::/48", "del acl " + block + " 192.0.2.1",
		"add acl " + filepath.Join(e.listsDir(), allowList) + " 198.51.100.0/24"} {
		if !slices.Contains(*got, want) {
			t.Errorf("missing %q in %q", want, *got)
		}
	}
	if b, _ := os.ReadFile(block); string(b) != "192.0.2.2\n2001:db8::/48\n" {
		t.Errorf("block.lst = %q", b)
	}
	// A large change is a new version, committed at once.
	*got = nil
	many := make([]string, 600)
	for i := range many {
		many[i] = "10.0." + itoa(i/250) + "." + itoa(i%250)
	}
	if err := e.SetLists(ctx, many, []string{"198.51.100.0/24"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(*got, "prepare acl "+block) || !slices.Contains(*got, "add acl @7 "+block+" 10.0.2.99") || (*got)[len(*got)-2] != "commit acl @7 "+block {
		t.Errorf("versioned update: %q", (*got)[max(0, len(*got)-3):])
	}
	// HAProxy errors are reported.
	if err := e.SetLists(ctx, []string{"bogus"}, nil); err == nil {
		t.Error("error answer not reported")
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
