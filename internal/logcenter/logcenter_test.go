package logcenter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	var b strings.Builder
	for i := 0; i < 50000; i++ { // > one chunk, lines straddle chunk boundaries
		fmt.Fprintf(&b, "line %d %s\n", i, map[bool]string{true: "GET /x", false: "POST /y"}[i%2 == 0])
	}
	os.WriteFile(p, []byte(b.String()), 0o644)
	got, err := Tail(p, 3, "")
	if err != nil || strings.Join(got, "|") != "line 49997 POST /y|line 49998 GET /x|line 49999 POST /y" {
		t.Fatalf("tail = %q %v", got, err)
	}
	got, _ = Tail(p, 2, "get /X")
	if strings.Join(got, "|") != "line 49996 GET /x|line 49998 GET /x" {
		t.Fatalf("filtered = %q", got)
	}
	all, _ := Tail(p, 100000, "")
	if len(all) != 50000 || all[0] != "line 0 GET /x" {
		t.Fatalf("all: %d %q", len(all), all[0])
	}
}

func TestParseAccess(t *testing.T) {
	a := ParseAccess(`203.0.113.9 - - [28/Oct/2026:04:09:22 +0000] "GET /index.php?a=1 HTTP/2.0" 200 512 "-" "curl/8.5" "a.com"`)
	if a.IP != "203.0.113.9" || a.Method != "GET" || a.Path != "/index.php?a=1" || a.Status != 200 || a.Bytes != 512 || a.Host != "a.com" || a.Time != 1793160562 {
		t.Fatalf("%+v", a)
	}
	if r := ParseAccess("garbage"); r.Raw != "garbage" {
		t.Fatal("raw")
	}
}

func TestParseSSH(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local)
	for line, want := range map[string]SSHEvent{
		"2026-01-01T10:00:00.123+00:00 host sshd[12]: Accepted publickey for root from 198.51.100.7 port 50022 ssh2: ED25519": {Result: "accepted", Method: "publickey", User: "root", IP: "198.51.100.7", Port: 50022, Time: 1767261600},
		"Dec 31 23:59:59 host sshd[9]: Failed password for invalid user admin from 2001:db8::1 port 4444 ssh2":                {Result: "failed", Method: "password", User: "admin", IP: "2001:db8::1", Port: 4444},
		"Jan  1 00:00:01 host sshd[9]: Invalid user oracle from 192.0.2.5 port 1234":                                          {Result: "invalid", User: "oracle", IP: "192.0.2.5", Port: 1234},
	} {
		e, ok := ParseSSH(line, now)
		if !ok {
			t.Fatalf("not parsed: %s", line)
		}
		if want.Time == 0 {
			want.Time = e.Time
			if time.Unix(e.Time, 0).Year() != map[string]int{"failed": 2025, "invalid": 2026}[want.Result] {
				t.Errorf("year of %s: %v", want.Result, time.Unix(e.Time, 0))
			}
		}
		if e != want {
			t.Errorf("%s:\n got %+v\nwant %+v", line, e, want)
		}
	}
	if _, ok := ParseSSH("host sshd[1]: Connection closed by 1.2.3.4", now); ok {
		t.Error("parsed noise")
	}
}

func TestParseAccessBlockDetect(t *testing.T) {
	base := `203.0.113.9 - - [30/Oct/2026:12:00:00 +0000] "GET /x HTTP/1.1" 404 10 "-" "curl/8" "a.com"`
	for line, want := range map[string][2]string{
		base:                         {"", ""},
		base + ` "-"`:                {"", ""},
		base + ` "rule-wp-1002" "-"`: {"rule-wp-1002", ""},
		base + ` "404:rule-c7" "-"`:  {"rule-c7", ""},
		base + ` "" "rule-c9"`:       {"", "rule-c9"},
	} {
		a := ParseAccess(line)
		if a.Block != want[0] || a.Detect != want[1] || a.Status != 404 || a.Host != "a.com" {
			t.Errorf("%q → block %q detect %q", line, a.Block, a.Detect)
		}
	}
}
