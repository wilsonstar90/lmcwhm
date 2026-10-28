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
