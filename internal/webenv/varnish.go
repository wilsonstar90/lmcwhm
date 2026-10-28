package webenv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
)

// Varnish page cache (aaPanel / 寶塔 Varnish). HAProxy sends the hostnames of
// sites with the cache on to Varnish, which fetches misses from nginx; the
// container only runs while at least one site uses it.

const (
	VarnishImage = "varnish:7.6"
	VarnishName  = "lmcwhm-varnish"
	VarnishPort  = 6081 // loopback only
)

var ErrVarnish = errors.New("web.varnish_invalid")

// VarnishSettings are global; the TTL and exclusions are per site (Advanced).
type VarnishSettings struct {
	MemMB int `json:"mem_mb"` // malloc storage size
}

func (e *Env) VarnishDir() string { return filepath.Join(e.Home, "web", "varnish") }

func (e *Env) VarnishSettings() VarnishSettings {
	s := VarnishSettings{MemMB: 256}
	if b, err := os.ReadFile(filepath.Join(e.VarnishDir(), "settings.json")); err == nil {
		json.Unmarshal(b, &s)
	}
	if s.MemMB < 32 {
		s.MemMB = 256
	}
	return s
}

func (e *Env) SaveVarnishSettings(s VarnishSettings) error {
	if s.MemMB < 32 || s.MemMB > 65536 {
		return ErrVarnish
	}
	if err := os.MkdirAll(e.VarnishDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(s)
	return os.WriteFile(filepath.Join(e.VarnishDir(), "settings.json"), b, 0o644)
}

// cached reports whether HAProxy should send s through Varnish.
func (s Site) cached() bool { return s.Advanced.Varnish && !s.Stopped && !s.Passthrough }

func cachedSites(sites []Site) []Site {
	out := []Site{}
	for _, s := range sites {
		if s.cached() {
			out = append(out, s)
		}
	}
	return out
}

type vclSite struct {
	Hosts   []string
	TTL     int
	NoCache string // regex of paths never cached
}

var vclPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)

// VCL renders default.vcl for the cached sites.
func VCL(sites []Site) []byte {
	var vs []vclSite
	for _, s := range cachedSites(sites) {
		v := vclSite{TTL: s.Advanced.VarnishTTL}
		if v.TTL <= 0 {
			v.TTL = 120
		}
		for _, h := range s.Hosts() {
			v.Hosts = append(v.Hosts, strings.ToLower(h))
		}
		var ex []string
		for _, p := range s.Advanced.VarnishExclude {
			if vclPathRe.MatchString(p) {
				ex = append(ex, regexp.QuoteMeta(p))
			}
		}
		if len(ex) > 0 {
			v.NoCache = "^(" + strings.Join(ex, "|") + ")"
		}
		vs = append(vs, v)
	}
	b, err := render("default.vcl.tmpl", map[string]any{"NginxPort": NginxPort, "Sites": vs})
	if err != nil {
		panic(err) // the template is static
	}
	return b
}

// vclHostCond is a VCL condition matching any of hosts (wildcards as suffixes).
func vclHostCond(v string, hosts []string) string {
	var c []string
	for _, h := range hosts {
		if strings.HasPrefix(h, "*.") {
			c = append(c, fmt.Sprintf(`%s ~ "\.%s$"`, v, regexp.QuoteMeta(h[2:])))
		} else {
			c = append(c, fmt.Sprintf(`%s == "%s"`, v, h))
		}
	}
	return strings.Join(c, " || ")
}

func (e *Env) varnishArgs() []string {
	return []string{
		"--network", "host", "--user", "0",
		"-v", e.VarnishDir() + ":/etc/varnish/lmcwhm:ro",
		"--tmpfs", "/var/lib/varnish:exec",
		VarnishImage,
		"varnishd", "-F", "-f", "/etc/varnish/lmcwhm/default.vcl",
		"-a", fmt.Sprintf("127.0.0.1:%d", VarnishPort),
		"-s", fmt.Sprintf("malloc,%dm", e.VarnishSettings().MemMB),
		"-p", "feature=+http2",
	}
}

// applyVarnish writes the VCL and starts, reloads or removes the container.
func (e *Env) applyVarnish(ctx context.Context, sites []Site, log func(string, ...any)) error {
	if len(cachedSites(sites)) == 0 {
		if dockerx.State(ctx, VarnishName) != "" {
			log("no site uses Varnish: removing %s", VarnishName)
			return dockerx.Remove(ctx, VarnishName)
		}
		return nil
	}
	if err := os.MkdirAll(e.VarnishDir(), 0o755); err != nil {
		return err
	}
	p := filepath.Join(e.VarnishDir(), "default.vcl")
	body := VCL(sites)
	old, _ := os.ReadFile(p)
	changed := !bytes.Equal(old, body)
	if changed {
		if err := os.WriteFile(p, body, 0o644); err != nil {
			return err
		}
	}
	if !dockerx.ImageExists(ctx, VarnishImage) {
		log("pulling %s", VarnishImage)
		if _, err := dockerx.Run(ctx, "pull", "-q", VarnishImage); err != nil {
			return err
		}
	}
	running := dockerx.State(ctx, VarnishName) == "running"
	if err := EnsureContainer(ctx, log, VarnishName, e.varnishArgs(), []int{VarnishPort}, true); err != nil {
		if len(old) > 0 {
			os.WriteFile(p, old, 0o644)
		}
		return err
	}
	if changed && running {
		// Compile and switch to the new VCL without dropping the cache.
		if out, err := dockerx.Run(ctx, "exec", VarnishName, "varnishreload", "/etc/varnish/lmcwhm/default.vcl"); err != nil {
			if len(old) > 0 {
				os.WriteFile(p, old, 0o644)
			}
			return fmt.Errorf("varnish VCL reload failed: %v %s", err, out)
		}
	}
	return nil
}

// VarnishPurge drops a site's cached pages (all sites when hosts is empty).
func VarnishPurge(ctx context.Context, hosts []string) error {
	if dockerx.State(ctx, VarnishName) != "running" {
		return nil
	}
	expr := "req.url ~ ."
	if len(hosts) > 0 {
		var q []string
		for _, h := range hosts {
			q = append(q, regexp.QuoteMeta(strings.TrimPrefix(strings.ToLower(h), "*")))
		}
		expr = fmt.Sprintf(`req.http.host ~ "(%s)(:[0-9]+)?$"`, strings.Join(q, "|"))
	}
	_, err := dockerx.Run(ctx, "exec", VarnishName, "varnishadm", "ban", expr)
	return err
}

// VarnishStats are the cache counters shown on the settings card.
type VarnishStats struct {
	State  string `json:"state"`
	Hits   int64  `json:"hits"`
	Misses int64  `json:"misses"`
	Objs   int64  `json:"objects"`
	Bytes  int64  `json:"bytes"` // storage in use
	Uptime int64  `json:"uptime"`
}

func (e *Env) VarnishStatus(ctx context.Context) VarnishStats {
	st := VarnishStats{State: dockerx.State(ctx, VarnishName)}
	if st.State != "running" {
		return st
	}
	out, err := dockerx.Run(ctx, "exec", VarnishName, "varnishstat", "-j",
		"-f", "MAIN.cache_hit", "-f", "MAIN.cache_miss", "-f", "MAIN.n_object", "-f", "MAIN.uptime", "-f", "SMA.s0.g_bytes")
	if err != nil {
		return st
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(out), &raw) != nil {
		return st
	}
	if c, ok := raw["counters"]; ok { // varnish >= 6.5
		json.Unmarshal(c, &raw)
	}
	val := func(k string) int64 {
		var f struct {
			Value int64 `json:"value"`
		}
		json.Unmarshal(raw[k], &f)
		return f.Value
	}
	st.Hits, st.Misses, st.Objs, st.Uptime, st.Bytes = val("MAIN.cache_hit"), val("MAIN.cache_miss"), val("MAIN.n_object"), val("MAIN.uptime"), val("SMA.s0.g_bytes")
	return st
}
