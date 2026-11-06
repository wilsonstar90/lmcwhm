package database

import (
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, n := range []string{"wp", "shop_db", "A1"} {
		if !ValidName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range []string{"", "1db", "a-b", "a b", "x'; DROP", "mysql", "Postgres", "sa", strings.Repeat("a", 33)} {
		if ValidName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestPassword(t *testing.T) {
	for _, p := range []string{"short", "has'quote1", `back\slash1`, "dollar$sign1", "space here1"} {
		if passRe.MatchString(p) {
			t.Errorf("%q should be rejected", p)
		}
	}
	if !passRe.MatchString("Abc-123_456@x") {
		t.Error("valid password rejected")
	}
}

func TestScripts(t *testing.T) {
	for _, e := range []string{"mysql", "mariadb", "postgres", "mongodb", "sqlserver"} {
		for _, s := range []string{createScript(e, "db1", "u1", "pw123456"), dropScript(e, "db1", "u1"), passwordScript(e, "db1", "u1", "pw123456")} {
			if s == "" || !strings.Contains(s, "u1") {
				t.Errorf("%s: bad script %q", e, s)
			}
		}
	}
	if s := createScript("mysql", "db1", "u1", "pw"); !strings.Contains(s, "GRANT ALL PRIVILEGES ON `db1`.* TO 'u1'@'%'") {
		t.Errorf("mysql grant: %s", s)
	}
}

func TestNormAccess(t *testing.T) {
	if m, ips, err := normAccess("local", []string{"1.2.3.4"}); err != nil || m != "local" || len(ips) != 0 {
		t.Fatal(m, ips, err)
	}
	m, ips, err := normAccess("ips", []string{" 1.2.3.4 ", "2001:DB8::1", "10.1.2.3/8", "1.2.3.4", "", "::ffff:5.6.7.8"})
	if err != nil || m != "ips" || strings.Join(ips, ",") != "1.2.3.4,2001:db8::1,10.0.0.0/8,5.6.7.8" {
		t.Fatal(ips, err)
	}
	for _, bad := range [][]string{nil, {"x"}, {"2001:db8::/32"}, {"fe80::1%eth0"}, {"1.2.3.4'"}} {
		if _, _, err := normAccess("ips", bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if _, _, err := normAccess("nope", nil); err == nil {
		t.Error("bad mode")
	}
}

func TestMySQLHost(t *testing.T) {
	for in, want := range map[string]string{"10.0.0.0/8": "10.0.0.0/255.0.0.0", "172.17.0.0/16": "172.17.0.0/255.255.0.0", "1.2.3.4": "1.2.3.4", "::1": "::1"} {
		if got := mysqlHost(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
	for h, ok := range map[string]bool{"10.0.0.0/255.0.0.0": true, "%": true, "::1": true, "a'b": false, "": false, "x y": false} {
		if safeHost(h) != ok {
			t.Errorf("safeHost(%q)", h)
		}
	}
}
