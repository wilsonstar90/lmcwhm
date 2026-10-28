package server

import "testing"

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.3.1", "v0.3.0", true}, {"v0.3.0", "v0.3.0", false}, {"v0.2.9", "v0.3.0", false},
		{"v0.10.0", "v0.9.9", true}, {"v0.3.0", "v0.3.0-dev", true}, {"v0.3.1", "v0.3.1-2-g96820c8-dirty", false}, {"v1.0.0", "0.9.0", true},
	} {
		if got := newerVersion(c.latest, c.current); got != c.want {
			t.Errorf("newerVersion(%s, %s) = %v", c.latest, c.current, got)
		}
	}
}
