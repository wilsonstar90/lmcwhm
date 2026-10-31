package appstore

import (
	"strings"
	"testing"
)

func TestSourceRecipes(t *testing.T) {
	for _, a := range Catalog {
		for _, v := range a.Versions {
			r, why := SourceSupport(a.Key, v.Version)
			if why != "" {
				if _, ok := SourceUnsupported[a.Key]; !ok && unsupportedVersions[a.Key+":"+v.Version] == "" {
					t.Errorf("%s %s: no recipe and no reason", a.Key, v.Version)
				}
				continue
			}
			df, err := SourceDockerfile(a.Key, v.Image)
			if err != nil {
				t.Fatalf("%s: %v", a.Key, err)
			}
			for _, want := range []string{"FROM " + v.Image + " AS build", "ARG VERSION", "COPY --from=build /out/ /", "${VERSION}", "${CONFIGURE_ARGS}", "${EXTRA_CFLAGS}", `if [ "$CHECK_ONLY" = 1 ]`} {
				if !strings.Contains(df, want) {
					t.Errorf("%s: Dockerfile lacks %q:\n%s", a.Key, want, df)
				}
			}
			if r.Minutes == 0 || r.Defaults == "" {
				t.Errorf("%s: no estimate or defaults", a.Key)
			}
		}
	}
	// Packages and downloads that broke builds.
	for app, want := range map[string][]string{
		"python":   {"libncurses-dev", "archive.debian.org"},
		"postgres": {" perl "},
		"mysql":    {"libaio-devel", "downloads.mysql.com/archives/", "$PWD/boost"},
	} {
		for _, w := range want {
			if !strings.Contains(Recipes[app].Deps+Recipes[app].Build, w) {
				t.Errorf("%s recipe lacks %q", app, w)
			}
		}
	}
	if strings.Contains(Recipes["python"].Deps, "libncursesw5-dev") || strings.Contains(Recipes["mysql"].Deps, "apt-get") {
		t.Error("python still asks for libncursesw5-dev, or mysql (Oracle Linux) for apt")
	}
	if _, why := SourceSupport("mysql", "5.7"); why != "old" {
		t.Errorf("mysql 5.7: %q", why)
	}
	if _, err := SourceDockerfile("sqlserver", "x"); err != ErrNoSource {
		t.Errorf("sqlserver: %v", err)
	}
}

func TestBuildOptions(t *testing.T) {
	ok := []BuildOptions{{}, {Mode: "fast", Args: "ignored $(x)"}, {Mode: "source", Args: "--enable-debug --with-x=/usr", CFlags: "-O3 -march=native"}}
	for _, o := range ok {
		if err := o.normalize("redis", "7.4"); err != nil {
			t.Errorf("%+v: %v", o, err)
		}
	}
	o := BuildOptions{Mode: "fast", Args: "x"}
	o.normalize("redis", "7.4")
	if o.Args != "" {
		t.Error("fast mode keeps arguments")
	}
	for _, bad := range []BuildOptions{
		{Mode: "source", Args: "--x; rm -rf /"}, {Mode: "source", Args: "$(id)"}, {Mode: "source", CFlags: "`id`"},
		{Mode: "source", Args: "a\"b"}, {Mode: "turbo"},
	} {
		if bad.normalize("redis", "7.4") == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if (&BuildOptions{Mode: "source"}).normalize("sqlserver", "2022") != ErrNoSource {
		t.Error("source build of SQL Server accepted")
	}
	i := Installed{App: "redis", Version: "7.4", Image: "redis:7.4", BuildMode: ModeSource}
	if i.RunImage() != "lmcwhm/src-redis:7.4" {
		t.Error(i.RunImage())
	}
	i.BuildMode = ModeFast
	if i.RunImage() != "redis:7.4" {
		t.Error(i.RunImage())
	}
}
