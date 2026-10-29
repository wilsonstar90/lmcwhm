package appstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestVersionFromEnv(t *testing.T) {
	for _, c := range []struct{ ref, env, want string }{
		{"mariadb:11.4", "MARIADB_VERSION=1:11.4.4+maria~ubu2404", "11.4.4"},
		{"mysql:8.4", "MYSQL_VERSION=8.4.3-1.el9", "8.4.3"},
		{"postgres:17", "PG_VERSION=17.2-1.pgdg120+1", "17.2"},
		{"redis:7.4", "REDIS_VERSION=7.4.1", "7.4.1"},
		{"php:8.3-fpm", "PHP_VERSION=8.3.14", "8.3.14"},
		{"lmcwhm/php:8.3-2", "PHP_VERSION=8.3.14", "8.3.14"}, // the panel's build inherits its base's
		{"eclipse-temurin:21-jdk", "JAVA_VERSION=jdk-21.0.5+11", "21.0.5+11"},
		{"eclipse-temurin:8-jdk", "JAVA_VERSION=jdk8u432-b06", "8u432-b06"},
		{"mongo:7.0", "MONGO_VERSION=7.0.15", "7.0.15"},
		{"phpmyadmin:5.2", "VERSION=5.2.1", "5.2.1"},
		{"mcr.microsoft.com/mssql/server:2022-latest", "PATH=/usr/bin", ""},
		{"redis:7.4", "PATH=/usr/bin", ""},
	} {
		if got := VersionFromEnv(c.ref, []string{"PATH=/x", c.env}); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.ref, c.env, got, c.want)
		}
	}
}

func TestParseRef(t *testing.T) {
	for _, c := range []struct{ ref, host, repo, tag string }{
		{"mariadb:11.4", "registry-1.docker.io", "library/mariadb", "11.4"},
		{"redis", "registry-1.docker.io", "library/redis", "latest"},
		{"mlocati/php-extension-installer:latest", "registry-1.docker.io", "mlocati/php-extension-installer", "latest"},
		{"mcr.microsoft.com/mssql/server:2022-latest", "mcr.microsoft.com", "mssql/server", "2022-latest"},
		{"localhost:5000/app:1", "localhost:5000", "app", "1"},
	} {
		h, r, tg := parseRef(c.ref)
		if h != c.host || r != c.repo || tg != c.tag {
			t.Errorf("%s = %s %s %s", c.ref, h, r, tg)
		}
	}
}

// fakeRegistry serves one tag as a manifest list behind a bearer challenge.
func fakeRegistry(t *testing.T, version string) (*httptest.Server, *int) {
	gets := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.URL.Query().Get("scope") != "repository:library/redis:pull" {
				t.Errorf("scope %q", r.URL.Query().Get("scope"))
			}
			json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="registry.test",scope="repository:library/redis:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			gets++
		}
		switch r.URL.Path {
		case "/v2/library/redis/manifests/7.4":
			w.Header().Set("Docker-Content-Digest", "sha256:list")
			if r.Method == http.MethodHead {
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"manifests": []map[string]any{
				{"digest": "sha256:other", "platform": map[string]string{"os": "linux", "architecture": "s390x"}},
				{"digest": "sha256:mine", "platform": map[string]string{"os": "linux", "architecture": runtime.GOARCH}},
			}})
		case "/v2/library/redis/manifests/sha256:mine":
			json.NewEncoder(w).Encode(map[string]any{"config": map[string]string{"digest": "sha256:cfg"}})
		case "/v2/library/redis/blobs/sha256:cfg":
			json.NewEncoder(w).Encode(map[string]any{"config": map[string]any{"Env": []string{"PATH=/x", "REDIS_VERSION=" + version}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := registryURL
	registryURL = func(string) string { return srv.URL }
	t.Cleanup(func() { registryURL = old })
	return srv, &gets
}

func TestRemoteImage(t *testing.T) {
	_, gets := fakeRegistry(t, "7.4.2")
	iv, err := RemoteImage(context.Background(), "redis:7.4", "sha256:old")
	if err != nil {
		t.Fatal(err)
	}
	if iv.Version != "7.4.2" || iv.Digest != "sha256:list" {
		t.Fatalf("got %+v", iv)
	}
	// Same digest as the local image: only the HEAD is sent.
	*gets = 0
	iv, err = RemoteImage(context.Background(), "redis:7.4", "sha256:list")
	if err != nil || iv.Version != "" || iv.Digest != "sha256:list" || *gets != 0 {
		t.Fatalf("got %+v %v gets=%d", iv, err, *gets)
	}
}

func TestRemoteImageError(t *testing.T) {
	fakeRegistry(t, "7.4.2")
	if _, err := RemoteImage(context.Background(), "redis:9.9", ""); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecide(t *testing.T) {
	for _, c := range []struct {
		name      string
		current   string
		local     string
		remote    ImageVersion
		latest    string
		available bool
	}{
		{"same digest", "7.4.1", "sha256:a", ImageVersion{Digest: "sha256:a"}, "7.4.1", false},
		{"new patch", "7.4.1", "sha256:a", ImageVersion{Version: "7.4.2", Digest: "sha256:b"}, "7.4.2", true},
		{"rebuild", "7.4.1", "sha256:a", ImageVersion{Version: "7.4.1", Digest: "sha256:b"}, "7.4.1", true},
		{"no local digest, same version", "7.4.1", "", ImageVersion{Version: "7.4.1", Digest: "sha256:b"}, "7.4.1", false},
		{"no local digest, new version", "7.4.1", "", ImageVersion{Version: "7.4.2", Digest: "sha256:b"}, "7.4.2", true},
	} {
		u := Update{Current: c.current}
		decide(&u, c.local, c.remote)
		if u.Latest != c.latest || u.Available != c.available || u.LatestDigest != c.remote.Digest {
			t.Errorf("%s: %+v", c.name, u)
		}
	}
}

// LMCWHM_LIVE=1 go test -run Live ./internal/appstore asks the real registries.
func TestRemoteImageLive(t *testing.T) {
	if os.Getenv("LMCWHM_LIVE") == "" {
		t.Skip("set LMCWHM_LIVE=1")
	}
	for _, ref := range []string{"redis:7.4", "mariadb:11.4", "php:8.3-fpm", "postgres:17", "mcr.microsoft.com/mssql/server:2022-latest"} {
		iv, err := RemoteImage(context.Background(), ref, "")
		t.Logf("%s: %+v %v", ref, iv, err)
		if err != nil || iv.Digest == "" {
			t.Errorf("%s failed", ref)
		}
	}
}
