//go:build srccheck

// Compile-install recipes against the real official images (.github/workflows/source-check.yml):
// each build installs its packages, downloads the source and runs configure,
// then stops before compiling (CHECK_ONLY=1).
//
//	SRC_MATRIX=out.txt go test -tags srccheck -run TestSourceMatrix ./internal/appstore/
//	SRC=php:8.4 go test -tags srccheck -run TestSourceConfigure -timeout 60m ./internal/appstore/
package appstore

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

// sourceTargets lists every "app:version" that can be compiled.
func sourceTargets() []string {
	var out []string
	for _, v := range webenv.PHPVersions {
		out = append(out, "php:"+v)
	}
	for _, a := range Catalog {
		for _, v := range a.Versions {
			if _, why := SourceSupport(a.Key, v.Version); why == "" {
				out = append(out, a.Key+":"+v.Version)
			}
		}
	}
	return out
}

func TestSourceMatrix(t *testing.T) {
	raw, _ := json.Marshal(sourceTargets())
	if err := os.WriteFile(os.Getenv("SRC_MATRIX"), []byte("targets="+string(raw)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSourceConfigure(t *testing.T) {
	app, ver, _ := strings.Cut(os.Getenv("SRC"), ":")
	ctx := context.Background()
	args := map[string]string{"CHECK_ONLY": "1"}
	var df string
	var err error
	if app == "php" {
		df, err = webenv.PHPDockerfile(ver, true)
	} else {
		var image string
		for _, a := range Catalog {
			for _, v := range a.Versions {
				if a.Key == app && v.Version == ver {
					image = v.Image
				}
			}
		}
		if image == "" {
			t.Fatalf("unknown %q", os.Getenv("SRC"))
		}
		if out, err := dockerx.Run(ctx, "pull", image); err != nil {
			t.Fatalf("pull %s: %v %s", image, err, out)
		}
		args["VERSION"] = CurrentVersion(ctx, image)
		if args["VERSION"] == "" {
			t.Fatalf("no version in %s", image)
		}
		df, err = SourceDockerfile(app, image)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s %s\n%s", app, args["VERSION"], df)
	if err := dockerx.BuildStream(ctx, "lmcwhm/srccheck:"+app+"-"+ver, df, args, os.Stdout); err != nil {
		t.Fatal(err)
	}
}
