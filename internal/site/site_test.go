package site

import (
	"reflect"
	"testing"
)

func TestTargets(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:3000", "https://backend.internal", "http://[::1]:8080"} {
		if !validTarget(ok, false) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"127.0.0.1:3000", "http://a/path", "http://a; return 200", "ftp://a", "http://a:80 b"} {
		if validTarget(bad, false) {
			t.Errorf("%q accepted", bad)
		}
	}
	if !validTarget("127.0.0.1:8443", true) || validTarget("http://127.0.0.1:8443", true) {
		t.Error("passthrough target validation")
	}
	if validCommand("") || validCommand("a\nb") || !validCommand("npm start") {
		t.Error("command validation")
	}
	if !reservedPort(8080) || !reservedPort(9083) || reservedPort(3000) {
		t.Error("reserved ports")
	}
}

func TestCertNames(t *testing.T) {
	cases := []struct{ in, want []string }{
		{[]string{"a.com", "www.a.com"}, []string{"a.com", "www.a.com"}},
		// The reported failure: www is covered by *.a.com.
		{[]string{"a.com", "www.a.com", "*.a.com"}, []string{"a.com", "*.a.com"}},
		// Deeper names and other domains are not covered.
		{[]string{"a.com", "x.y.a.com", "b.com", "*.a.com"}, []string{"a.com", "x.y.a.com", "b.com", "*.a.com"}},
		{[]string{"a.com", "*.a.com", "*.a.com", "a.com"}, []string{"a.com", "*.a.com"}},
	}
	for _, c := range cases {
		if got := certNames(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("certNames(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
