package appstore

import (
	"strings"
	"testing"
)

func TestWPCLIApp(t *testing.T) {
	a, v, ok := Lookup("wp-cli", "2")
	if !ok || a.Kind != KindTool || v.Image != "wordpress:cli" {
		t.Fatalf("WP-CLI catalog entry: %+v %+v", a, v)
	}
	sv := &Service{Home: "/opt/lmcwhm"}
	args := strings.Join(sv.args(Installed{App: "wp-cli", Version: "2", Image: v.Image}), " ")
	for _, want := range []string{"--entrypoint sleep", "wordpress:cli infinity", "/www/wwwroot:/www/wwwroot"} {
		if !strings.Contains(args, want) {
			t.Errorf("WP-CLI args missing %q: %s", want, args)
		}
	}
}
