package security

import (
	"strings"
	"testing"
)

func TestWhitelistYAML(t *testing.T) {
	y := whitelistYAML([]string{"203.0.113.9", "198.51.100.0/24", "2001:db8::/48"})
	for _, want := range []string{"name: lmcwhm/whitelist", "  ip:\n    - \"203.0.113.9\"", "  cidr:\n    - \"198.51.100.0/24\"\n    - \"2001:db8::/48\""} {
		if !strings.Contains(y, want) {
			t.Errorf("missing %q:\n%s", want, y)
		}
	}
	if y := whitelistYAML(nil); !strings.Contains(y, `"127.0.0.1"`) || strings.Contains(y, "cidr") {
		t.Errorf("empty list:\n%s", y)
	}
}
