package appstore

// Exact versions of installed images and patch updates of the same tag
// (mariadb:11.4 is 11.4.4 today and 11.4.5 next month): the version comes
// from the official images' environment (MARIADB_VERSION, PHP_VERSION, ...),
// the update check asks the registry for the tag's digest (a HEAD, which
// Docker Hub does not count as a pull) and reads the new version only when
// the digest changed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
)

// ImageVersion is what an image says about itself.
type ImageVersion struct {
	Version string `json:"version"` // "11.4.4"; "" when the image does not say
	Digest  string `json:"digest"`  // registry digest of the tag (manifest list)
}

// versionEnv is the variable each official image sets to its exact version.
var versionEnv = map[string]string{
	"php": "PHP_VERSION", "mariadb": "MARIADB_VERSION", "mysql": "MYSQL_VERSION", "redis": "REDIS_VERSION",
	"postgres": "PG_VERSION", "mongo": "MONGO_VERSION", "node": "NODE_VERSION", "python": "PYTHON_VERSION",
	"eclipse-temurin": "JAVA_VERSION", "tomcat": "TOMCAT_VERSION", "memcached": "MEMCACHED_VERSION",
	"phpmyadmin": "VERSION", "adminer": "ADMINER_VERSION",
}

var (
	epochRe = regexp.MustCompile(`^\d+:`)
	jdkRe   = regexp.MustCompile(`^jdk-?`)
)

// repoName is the image name without registry, namespace and tag ("mariadb").
func repoName(ref string) string {
	name, _ := splitTag(ref)
	return name[strings.LastIndex(name, "/")+1:]
}

func splitTag(ref string) (name, tag string) {
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// VersionFromEnv picks and tidies the version an image's environment declares:
// "1:11.4.4+maria~ubu2404" → "11.4.4", "17.2-1.pgdg120+1" → "17.2", "jdk-21.0.5+11" → "21.0.5+11".
func VersionFromEnv(ref string, env []string) string {
	repo := repoName(ref)
	key := versionEnv[repo]
	if key == "" {
		return ""
	}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k != key {
			continue
		}
		v = epochRe.ReplaceAllString(strings.TrimSpace(v), "")
		switch repo {
		case "mariadb", "mysql":
			v, _, _ = strings.Cut(v, "+")
			v, _, _ = strings.Cut(v, "-")
		case "postgres":
			v, _, _ = strings.Cut(v, "-")
		case "eclipse-temurin":
			v = jdkRe.ReplaceAllString(v, "")
		}
		return v
	}
	return ""
}

// LocalImage reads the version and registry digest of a local image.
func LocalImage(ctx context.Context, ref string) (ImageVersion, error) {
	out, err := dockerx.Run(ctx, "image", "inspect", "-f", "{{json .Config.Env}}\t{{json .RepoDigests}}", ref)
	if err != nil {
		return ImageVersion{}, err
	}
	envJSON, digJSON, _ := strings.Cut(out, "\t")
	var env, digests []string
	json.Unmarshal([]byte(envJSON), &env)
	json.Unmarshal([]byte(digJSON), &digests)
	iv := ImageVersion{Version: VersionFromEnv(ref, env)}
	name, _ := splitTag(ref)
	for _, d := range digests {
		// "mariadb@sha256:…" (Docker Hub library images drop "docker.io/library/").
		if n, dg, ok := strings.Cut(d, "@"); ok && (n == name || strings.HasSuffix(n, "/"+name)) {
			iv.Digest = dg
			break
		}
	}
	return iv, nil
}

// ---- registry ----

// registryURL maps a registry host to its base URL (tests point it at a fake).
var registryURL = func(host string) string { return "https://" + host }

var registryClient = &http.Client{Timeout: 20 * time.Second}

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

// parseRef splits a reference into registry host, repository and tag.
func parseRef(ref string) (host, repo, tag string) {
	name, tag := splitTag(ref)
	host = "registry-1.docker.io"
	if first, rest, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		host, name = first, rest
	}
	if host == "registry-1.docker.io" && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	return host, name, tag
}

type registry struct {
	host, repo, token string
}

// do sends a request, fetching an anonymous bearer token once when asked.
func (r *registry) do(ctx context.Context, method, path, accept string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, registryURL(r.host)+"/v2/"+r.repo+path, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if r.token != "" {
			req.Header.Set("Authorization", "Bearer "+r.token)
		}
		resp, err := registryClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 && r.token == "" {
			ch := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			if err := r.login(ctx, ch); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("registry %s: %s %s", r.host, r.repo, resp.Status)
		}
		return resp, nil
	}
}

var challengeRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

func (r *registry) login(ctx context.Context, challenge string) error {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return errors.New("registry: unsupported authentication")
	}
	p := map[string]string{}
	for _, m := range challengeRe.FindAllStringSubmatch(challenge, -1) {
		p[m[1]] = m[2]
	}
	if p["realm"] == "" {
		return errors.New("registry: no token realm")
	}
	scope := p["scope"]
	if scope == "" {
		scope = "repository:" + r.repo + ":pull"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p["realm"], nil)
	if err != nil {
		return err
	}
	q := req.URL.Query()
	q.Set("service", p["service"])
	q.Set("scope", scope)
	req.URL.RawQuery = q.Encode()
	resp, err := registryClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); err != nil {
		return err
	}
	if r.token = t.Token; r.token == "" {
		r.token = t.AccessToken
	}
	if r.token == "" {
		return errors.New("registry: empty token")
	}
	return nil
}

func (r *registry) getJSON(ctx context.Context, path, accept string, v any) (string, error) {
	resp, err := r.do(ctx, http.MethodGet, path, accept)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return resp.Header.Get("Docker-Content-Digest"), json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

type manifest struct {
	MediaType string `json:"mediaType"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"manifests"`
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
}

// RemoteImage asks the registry for a tag. When its digest equals known the
// version is not fetched (Version stays "").
func RemoteImage(ctx context.Context, ref, known string) (ImageVersion, error) {
	host, repo, tag := parseRef(ref)
	r := &registry{host: host, repo: repo}
	resp, err := r.do(ctx, http.MethodHead, "/manifests/"+tag, manifestAccept)
	if err != nil {
		return ImageVersion{}, err
	}
	resp.Body.Close()
	iv := ImageVersion{Digest: resp.Header.Get("Docker-Content-Digest")}
	if iv.Digest != "" && iv.Digest == known {
		return iv, nil
	}
	var m manifest
	dg, err := r.getJSON(ctx, "/manifests/"+tag, manifestAccept, &m)
	if err != nil {
		return iv, err
	}
	if iv.Digest == "" {
		iv.Digest = dg
	}
	if len(m.Manifests) > 0 {
		pick := ""
		for _, x := range m.Manifests {
			if x.Platform.OS == "linux" && x.Platform.Architecture == runtime.GOARCH {
				pick = x.Digest
				break
			}
		}
		if pick == "" {
			return iv, fmt.Errorf("registry: no linux/%s image for %s", runtime.GOARCH, ref)
		}
		m = manifest{}
		if _, err := r.getJSON(ctx, "/manifests/"+pick, manifestAccept, &m); err != nil {
			return iv, err
		}
	}
	if m.Config.Digest == "" {
		return iv, errors.New("registry: manifest without config")
	}
	var cfg struct {
		Config struct {
			Env []string `json:"Env"`
		} `json:"config"`
	}
	if _, err := r.getJSON(ctx, "/blobs/"+m.Config.Digest, "", &cfg); err != nil {
		return iv, err
	}
	iv.Version = VersionFromEnv(ref, cfg.Config.Env)
	return iv, nil
}
