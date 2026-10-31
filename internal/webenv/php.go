package webenv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
)

// Per-version PHP settings (aaPanel 軟件設定 → PHP): optional extensions baked
// into an image layer, php.ini values, disable_functions, a free-form ini
// tail and the FPM pool. Stored as settings.json in the version's directory.

var (
	ErrPHPConfig    = errors.New("php.invalid_config")
	ErrPHPExtension = errors.New("php.unknown_extension")
	ErrPHPTest      = errors.New("php.config_test_failed")
)

// PHPExt is an optional extension installed with install-php-extensions.
type PHPExt struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
	Min  string `json:"min,omitempty"`  // first PHP version supported
	Max  string `json:"max,omitempty"`  // last PHP version supported
	Slow bool   `json:"slow,omitempty"` // compiles for several minutes
}

// PHPExtensions lists the installable extensions in display order.
var PHPExtensions = []PHPExt{
	{Name: "redis", Desc: "Redis client (sessions, object cache)"},
	{Name: "memcached", Desc: "Memcached client"},
	{Name: "imagick", Desc: "ImageMagick image processing"},
	{Name: "apcu", Desc: "User data cache"},
	{Name: "igbinary", Desc: "Compact serializer"},
	{Name: "msgpack", Desc: "MessagePack serializer"},
	{Name: "mongodb", Desc: "MongoDB driver"},
	{Name: "pdo_pgsql", Desc: "PostgreSQL (PDO)"},
	{Name: "pgsql", Desc: "PostgreSQL"},
	{Name: "sqlsrv", Desc: "SQL Server", Min: "8.0"},
	{Name: "pdo_sqlsrv", Desc: "SQL Server (PDO)", Min: "8.0"},
	{Name: "swoole", Desc: "Async / coroutine server", Slow: true},
	{Name: "xdebug", Desc: "Debugger and profiler (slows every request)"},
	{Name: "ioncube_loader", Desc: "ionCube encoded files"},
	{Name: "sourceguardian", Desc: "SourceGuardian encoded files"},
	{Name: "soap", Desc: "SOAP"},
	{Name: "xsl", Desc: "XSLT"},
	{Name: "gmp", Desc: "Arbitrary precision math (GMP)"},
	{Name: "sockets", Desc: "Low-level sockets"},
	{Name: "pcntl", Desc: "Process control (CLI workers)"},
	{Name: "calendar", Desc: "Calendar conversion"},
	{Name: "gettext", Desc: "Translations (gettext)"},
	{Name: "ldap", Desc: "LDAP"},
	{Name: "imap", Desc: "IMAP mailbox access"},
	{Name: "bz2", Desc: "bzip2 compression"},
	{Name: "zstd", Desc: "Zstandard compression"},
	{Name: "tidy", Desc: "HTML Tidy"},
	{Name: "yaml", Desc: "YAML"},
	{Name: "ssh2", Desc: "SSH2 / SFTP client"},
	{Name: "event", Desc: "libevent loop"},
	{Name: "uuid", Desc: "UUID generation"},
	{Name: "mailparse", Desc: "Email parsing"},
	{Name: "xlswriter", Desc: "Fast Excel export"},
	{Name: "protobuf", Desc: "Protocol Buffers"},
	{Name: "grpc", Desc: "gRPC client", Slow: true},
}

// phpVer turns "8.3" into 83 for range checks.
func phpVer(v string) int {
	if len(v) != 3 {
		return 0
	}
	return int(v[0]-'0')*10 + int(v[2]-'0')
}

// Supports reports whether the extension builds on PHP version v.
func (x PHPExt) Supports(v string) bool {
	if x.Min != "" && phpVer(v) < phpVer(x.Min) {
		return false
	}
	return x.Max == "" || phpVer(v) <= phpVer(x.Max)
}

func lookupExt(name string) (PHPExt, bool) {
	for _, x := range PHPExtensions {
		if x.Name == name {
			return x, true
		}
	}
	return PHPExt{}, false
}

// FPMPool is the [www] pool of one PHP version.
type FPMPool struct {
	PM             string `json:"pm"` // dynamic | static | ondemand
	MaxChildren    int    `json:"max_children"`
	StartServers   int    `json:"start_servers"`
	MinSpare       int    `json:"min_spare"`
	MaxSpare       int    `json:"max_spare"`
	MaxRequests    int    `json:"max_requests"`    // respawn a worker after n requests (0 = never)
	RequestTimeout int    `json:"request_timeout"` // seconds, kills runaway requests (0 = off)
	SlowlogTimeout int    `json:"slowlog_timeout"` // seconds, traces slow requests to slow.log (0 = off)
}

type PHPConfig struct {
	Extensions       []string          `json:"extensions"`
	INI              map[string]string `json:"ini"`
	DisableFunctions []string          `json:"disable_functions"`
	CustomINI        string            `json:"custom_ini"`
	FPM              FPMPool           `json:"fpm"`
}

// PHPINIKeys are the php.ini settings offered as a form, with their defaults.
var PHPINIKeys = []struct {
	Key, Default string
}{
	{"memory_limit", "256M"},
	{"upload_max_filesize", "64M"},
	{"post_max_size", "64M"},
	{"max_execution_time", "300"},
	{"max_input_time", "300"},
	{"max_input_vars", "3000"},
	{"display_errors", "Off"},
	{"error_reporting", "E_ALL & ~E_DEPRECATED"},
	{"short_open_tag", "Off"},
	{"date.timezone", ""}, // empty = the panel's timezone
	{"session.gc_maxlifetime", "1440"},
	{"opcache.enable", "1"},
	{"opcache.memory_consumption", "128"},
	{"opcache.max_accelerated_files", "10000"},
	{"opcache.validate_timestamps", "1"},
	{"opcache.revalidate_freq", "2"},
}

// DangerousFunctions is the suggested disable_functions list (寶塔 default).
var DangerousFunctions = []string{"passthru", "exec", "system", "putenv", "chroot", "chgrp", "chown", "shell_exec",
	"popen", "proc_open", "pcntl_exec", "ini_alter", "ini_restore", "dl", "openlog", "syslog", "readlink", "symlink",
	"popepassthru", "pcntl_alarm", "pcntl_fork", "pcntl_waitpid", "pcntl_wait", "pcntl_signal", "imap_open", "apache_setenv"}

var (
	iniValRe = regexp.MustCompile(`^[A-Za-z0-9 _.:/&~|!^()+-]{0,80}$`)
	funcRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

func DefaultPHPConfig() PHPConfig {
	c := PHPConfig{INI: map[string]string{}, Extensions: []string{}, DisableFunctions: []string{}}
	for _, k := range PHPINIKeys {
		c.INI[k.Key] = k.Default
	}
	c.FPM = FPMPool{PM: "dynamic", MaxChildren: 20, StartServers: 2, MinSpare: 1, MaxSpare: 4, MaxRequests: 500}
	return c
}

// Normalize validates c for PHP version v and fills defaults.
func (c *PHPConfig) Normalize(v string) error {
	d := DefaultPHPConfig()
	if c.INI == nil {
		c.INI = map[string]string{}
	}
	ini := map[string]string{}
	for _, k := range PHPINIKeys {
		val, ok := c.INI[k.Key]
		if !ok {
			val = d.INI[k.Key]
		}
		val = strings.TrimSpace(val)
		if !iniValRe.MatchString(val) {
			return fmt.Errorf("%w: %s", ErrPHPConfig, k.Key)
		}
		ini[k.Key] = val
	}
	c.INI = ini
	exts := []string{}
	for _, n := range c.Extensions {
		x, ok := lookupExt(n)
		if !ok || !x.Supports(v) {
			return fmt.Errorf("%w: %s", ErrPHPExtension, n)
		}
		if !slices.Contains(exts, n) {
			exts = append(exts, n)
		}
	}
	sort.Strings(exts)
	c.Extensions = exts
	fns := []string{}
	for _, f := range c.DisableFunctions {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !funcRe.MatchString(f) {
			return fmt.Errorf("%w: disable_functions", ErrPHPConfig)
		}
		if !slices.Contains(fns, f) {
			fns = append(fns, f)
		}
	}
	c.DisableFunctions = fns
	c.CustomINI = strings.ReplaceAll(c.CustomINI, "\r", "")
	if len(c.CustomINI) > 32<<10 {
		return fmt.Errorf("%w: custom_ini", ErrPHPConfig)
	}
	p := &c.FPM
	switch p.PM {
	case "dynamic", "static", "ondemand":
	case "":
		p.PM = "dynamic"
	default:
		return fmt.Errorf("%w: pm", ErrPHPConfig)
	}
	if p.MaxChildren < 1 || p.MaxChildren > 2000 {
		return fmt.Errorf("%w: pm.max_children", ErrPHPConfig)
	}
	if p.PM == "dynamic" {
		p.MinSpare = max(p.MinSpare, 1)
		p.MaxSpare = min(max(p.MaxSpare, p.MinSpare), p.MaxChildren)
		p.StartServers = min(max(p.StartServers, p.MinSpare), p.MaxSpare)
	}
	if p.MaxRequests < 0 || p.RequestTimeout < 0 || p.SlowlogTimeout < 0 {
		return fmt.Errorf("%w: fpm", ErrPHPConfig)
	}
	return nil
}

// SuggestFPM sizes the pool for memMB of RAM at ~40 MB per worker, using
// about half the memory (the rest is for databases and the OS).
func SuggestFPM(memMB int) FPMPool {
	n := min(max(memMB/2/40, 5), 300)
	return FPMPool{PM: "dynamic", MaxChildren: n, StartServers: max(n/10, 2), MinSpare: max(n/10, 1), MaxSpare: max(n/4, 4), MaxRequests: 500}
}

func (e *Env) phpConfigPath(v string) string { return filepath.Join(e.PHPDir(v), "settings.json") }

// PHPSlowLog is the FPM slow log of a version (on the host).
func (e *Env) PHPSlowLog(v string) string {
	return filepath.Join(e.WWWLogs, "php", strings.ReplaceAll(v, ".", ""), "slow.log")
}

// PHPConfig returns the stored settings of version v (defaults when unset).
func (e *Env) PHPConfig(v string) PHPConfig {
	c := DefaultPHPConfig()
	if b, err := os.ReadFile(e.phpConfigPath(v)); err == nil {
		var s PHPConfig
		if json.Unmarshal(b, &s) == nil {
			c = s
		}
	}
	if c.Normalize(v) != nil {
		c = DefaultPHPConfig()
	}
	return c
}

func (e *Env) renderPHP(v string, c PHPConfig) (fpm, ini []byte, err error) {
	fpm, err = render("php-fpm.conf.tmpl", map[string]any{"Port": PHPPort(v), "P": c.FPM, "SlowLog": c.FPM.SlowlogTimeout > 0})
	if err != nil {
		return nil, nil, err
	}
	var b bytes.Buffer
	b.WriteString("; Generated by lmcwhm (App Store → PHP " + v + " → Settings).\nexpose_php = Off\n")
	for _, k := range PHPINIKeys {
		val := c.INI[k.Key]
		if k.Key == "date.timezone" && val == "" {
			val = e.Timezone
		}
		if val != "" {
			fmt.Fprintf(&b, "%s = %s\n", k.Key, iniQuote(val))
		}
	}
	if len(c.DisableFunctions) > 0 {
		fmt.Fprintf(&b, "disable_functions = %s\n", strings.Join(c.DisableFunctions, ","))
	}
	if strings.TrimSpace(c.CustomINI) != "" {
		b.WriteString("\n; custom\n" + strings.TrimSpace(c.CustomINI) + "\n")
	}
	return fpm, b.Bytes(), nil
}

// iniQuote quotes values php.ini would otherwise parse as expressions.
func iniQuote(v string) string {
	if strings.ContainsAny(v, " &~|!^()") && !strings.HasPrefix(v, "E_") {
		return strconv.Quote(v)
	}
	return v
}

// PHPImageFor is the image a version runs with its extensions.
func PHPImageFor(v string, exts []string) string {
	if len(exts) == 0 {
		return PHPImage(v)
	}
	sum := sha256.Sum256([]byte(strings.Join(exts, ",")))
	return PHPImage(v) + "-x" + hex.EncodeToString(sum[:4])
}

// AptFallback is a Dockerfile step that points apt at archive.debian.org on
// Debian releases that have left the mirrors (the PHP 7.4 and 8.0 images are
// on bullseye, whose packages deb.debian.org still lists but no longer
// serves). The change stays in the image, so extensions installed on top of
// it later find their packages too.
const AptFallback = `RUN . /etc/os-release && case "$VERSION_CODENAME" in stretch|buster|bullseye) \
      sed -i 's|//deb.debian.org|//archive.debian.org|; s|//security.debian.org|//archive.debian.org|' /etc/apt/sources.list $(ls /etc/apt/sources.list.d/* 2>/dev/null) \
      && echo 'Acquire::Check-Valid-Until "false";' > /etc/apt/apt.conf.d/99lmcwhm-archive ;; esac`

// PHPInstallerImage provides install-php-extensions.
const PHPInstallerImage = "mlocati/php-extension-installer:latest"

func (e *Env) buildPHP(ctx context.Context, v string, exts []string, log func(string, ...any)) (string, error) {
	if !dockerx.ImageExists(ctx, PHPImage(v)) {
		log("building PHP %s image (first time, a few minutes)", v)
		if err := e.buildPHPBase(ctx, v, log); err != nil {
			return "", err
		}
	}
	img := PHPImageFor(v, exts)
	if img == PHPImage(v) || dockerx.ImageExists(ctx, img) {
		return img, nil
	}
	return img, buildPHPExt(ctx, v, exts, log)
}

// buildPHPExt builds the extension image of a version on its base build.
func buildPHPExt(ctx context.Context, v string, exts []string, log func(string, ...any)) error {
	log("building PHP %s with extensions: %s (this can take several minutes)", v, strings.Join(exts, " "))
	df := fmt.Sprintf("FROM %s\nCOPY --from=%s /usr/bin/install-php-extensions /usr/local/bin/\nRUN install-php-extensions %s\nLABEL lmcwhm.managed=\"true\"\n",
		PHPImage(v), PHPInstallerImage, strings.Join(exts, " "))
	return dockerx.Build(ctx, PHPImageFor(v, exts), df)
}

// testPHP checks a candidate configuration with php-fpm -t in a throwaway container.
func (e *Env) testPHP(ctx context.Context, img string, fpm, ini []byte) error {
	dir, err := os.MkdirTemp(e.PHPDir(""), ".test-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "fpm.conf"), fpm, 0o644)
	os.WriteFile(filepath.Join(dir, "php.ini"), ini, 0o644)
	out, err := dockerx.Run(ctx, "run", "--rm",
		"-v", filepath.Join(dir, "fpm.conf")+":/usr/local/etc/php-fpm.d/zz-docker.conf:ro",
		"-v", filepath.Join(dir, "php.ini")+":/usr/local/etc/php/conf.d/zz-lmcwhm.ini:ro",
		"-v", dir+":/var/log/lmcwhm-php", // the slow log must be openable
		"--entrypoint", "sh", img, "-c", "php-fpm -t 2>&1; php -r 'echo \"ok\";' 2>&1")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPHPTest, err)
	}
	for _, bad := range []string{"syntax error", "ERROR:", "PHP Warning", "PHP Fatal", "Unknown"} {
		if strings.Contains(out, bad) {
			return fmt.Errorf("%w: %s", ErrPHPTest, strings.TrimSpace(out))
		}
	}
	return nil
}

// SavePHPConfig validates c, builds the extension image if needed, tests the
// configuration and only then replaces the running one (task context).
func (e *Env) SavePHPConfig(ctx context.Context, v string, c PHPConfig, log func(string, ...any)) error {
	if !ValidPHP(v) {
		return fmt.Errorf("unsupported PHP version %q", v)
	}
	if err := c.Normalize(v); err != nil {
		return err
	}
	if err := os.MkdirAll(e.PHPDir(v), 0o755); err != nil {
		return err
	}
	img, err := e.buildPHP(ctx, v, c.Extensions, log)
	if err != nil {
		return err
	}
	fpm, ini, err := e.renderPHP(v, c)
	if err != nil {
		return err
	}
	log("testing the configuration (php-fpm -t)")
	if err := e.testPHP(ctx, img, fpm, ini); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(e.phpConfigPath(v), b, 0o644); err != nil {
		return err
	}
	return e.EnsurePHP(ctx, v, log)
}

// EnsurePHP builds the PHP image (first time only) and starts its FPM
// container; a changed php.ini / pool is reloaded gracefully (SIGUSR2).
func (e *Env) EnsurePHP(ctx context.Context, v string, log func(string, ...any)) error {
	if !ValidPHP(v) {
		return fmt.Errorf("unsupported PHP version %q", v)
	}
	dir := e.PHPDir(v)
	slowDir := filepath.Dir(e.PHPSlowLog(v))
	for _, d := range []string{dir, slowDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	os.Chown(slowDir, 33, 33)
	c := e.PHPConfig(v)
	fpm, ini, err := e.renderPHP(v, c)
	if err != nil {
		return err
	}
	changed := false
	for name, body := range map[string][]byte{"zz-docker.conf": fpm, "zz-lmcwhm.ini": ini} {
		p := filepath.Join(dir, name)
		if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, body) {
			continue
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			return err
		}
		changed = true
	}
	img, err := e.buildPHP(ctx, v, c.Extensions, log)
	if err != nil {
		return err
	}
	running := dockerx.State(ctx, PHPName(v)) == "running"
	args := append([]string{"--network", "host"}, e.rootMounts()...)
	args = append(args,
		"-v", slowDir+":/var/log/lmcwhm-php",
		"-v", filepath.Join(dir, "zz-docker.conf")+":/usr/local/etc/php-fpm.d/zz-docker.conf:ro",
		"-v", filepath.Join(dir, "zz-lmcwhm.ini")+":/usr/local/etc/php/conf.d/zz-lmcwhm.ini:ro",
		img,
	)
	if err := EnsureContainer(ctx, log, PHPName(v), args, []int{PHPPort(v)}, true); err != nil {
		return err
	}
	if changed && running && dockerx.State(ctx, PHPName(v)) == "running" {
		log("reloading PHP %s", v)
		dockerx.Run(ctx, "kill", "-s", "USR2", PHPName(v))
	}
	return nil
}

// PHPModules lists the modules loaded by a running version (php -m).
func PHPModules(ctx context.Context, v string) ([]string, error) {
	out, err := dockerx.Run(ctx, "exec", PHPName(v), "php", "-m")
	if err != nil {
		return nil, err
	}
	mods := []string{}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "[") {
			continue
		}
		if !slices.Contains(mods, strings.ToLower(l)) {
			mods = append(mods, strings.ToLower(l))
		}
	}
	sort.Strings(mods)
	return mods, nil
}

// PHPVersionInfo is one row of the PHP versions table.
type PHPVersionInfo struct {
	Version    string   `json:"version"`
	EOL        bool     `json:"eol"`
	State      string   `json:"state"`
	Port       int      `json:"port"`
	Extensions []string `json:"extensions"`
	Sites      int      `json:"sites"`
	// FullVersion is the exact version of the running build ("8.3.14").
	FullVersion string `json:"full_version,omitempty"`
	// Build says whether it is the fast or the source build.
	Build         PHPBuild `json:"build"`
	SourceMinutes int      `json:"source_minutes"`
}

// EOLPHP reports versions without upstream security support.
func EOLPHP(v string) bool { return phpVer(v) <= 81 }

// RemovePHP stops and deletes a version's container and its images (its
// settings are kept, so a reinstall builds the same extensions).
func (e *Env) RemovePHP(ctx context.Context, v string) error {
	if !ValidPHP(v) {
		return fmt.Errorf("unsupported PHP version %q", v)
	}
	if err := dockerx.Remove(ctx, PHPName(v)); err != nil {
		return err
	}
	for _, ref := range phpImages(ctx, v) {
		dockerx.Run(ctx, "rmi", ref)
	}
	dockerx.Run(ctx, "rmi", "php:"+v+"-fpm") // the base; fails harmlessly while in use
	return nil
}

// phpImages lists the local images built for a version (the base build and
// one per extension set).
func phpImages(ctx context.Context, v string) []string {
	out, _ := dockerx.Run(ctx, "images", "--format", "{{.Repository}}:{{.Tag}}", "lmcwhm/php")
	var refs []string
	for _, ref := range strings.Fields(out) {
		if ref == PHPImage(v) || strings.HasPrefix(ref, PHPImage(v)+"-x") {
			refs = append(refs, ref)
		}
	}
	return refs
}

// UpdatePHP rebuilds a version on the newest upstream image of its tag
// (php:8.3-fpm) with the same extensions, tests it with php-fpm -t and only
// then replaces the running container; on failure the previous build stays (task context).
func (e *Env) UpdatePHP(ctx context.Context, v string, log func(string, ...any)) error {
	return e.rebuildPHP(ctx, v, true, log)
}

// rebuildPHP builds a version again (after pulling a newer base when pull),
// tests it and restarts FPM on it; a failure puts the previous build back.
func (e *Env) rebuildPHP(ctx context.Context, v string, pull bool, log func(string, ...any)) error {
	if !ValidPHP(v) {
		return fmt.Errorf("unsupported PHP version %q", v)
	}
	if err := os.MkdirAll(e.PHPDir(v), 0o755); err != nil {
		return err
	}
	base := "php:" + v + "-fpm"
	c := e.PHPConfig(v)
	img := PHPImageFor(v, c.Extensions)
	oldBase := imageID(ctx, base)
	if pull {
		log("pulling %s", base)
		if _, err := dockerx.Run(ctx, "pull", "-q", base); err != nil {
			return err
		}
	} else if oldBase == "" {
		if _, err := dockerx.Run(ctx, "pull", "-q", base); err != nil {
			return err
		}
	}
	// Up to date when nothing new was pulled and the build carries the base's
	// version (an earlier update that failed after its pull leaves an old build).
	if pull && imageID(ctx, base) == oldBase && dockerx.State(ctx, PHPName(v)) == "running" &&
		phpImageVersion(ctx, img) != "" && phpImageVersion(ctx, img) == phpImageVersion(ctx, base) {
		log("PHP %s is already built on the newest image", v)
		return nil
	}
	// Keep the current builds so a failure puts them back.
	old := map[string]string{}
	for _, ref := range []string{PHPImage(v), img} {
		if id := imageID(ctx, ref); id != "" {
			old[ref] = id
		}
	}
	restore := func() {
		for ref, id := range old {
			dockerx.Run(ctx, "tag", id, ref)
		}
	}
	log("rebuilding PHP %s", v)
	if err := e.buildPHPBase(ctx, v, log); err != nil {
		restore()
		return err
	}
	if img != PHPImage(v) {
		// Built directly: the running container keeps the old image from being removed.
		if err := buildPHPExt(ctx, v, c.Extensions, log); err != nil {
			restore()
			return err
		}
	}
	fpm, ini, err := e.renderPHP(v, c)
	if err != nil {
		restore()
		return err
	}
	log("testing the new build (php-fpm -t)")
	if err := e.testPHP(ctx, img, fpm, ini); err != nil {
		restore()
		return err
	}
	log("restarting PHP %s", v)
	dockerx.Remove(ctx, PHPName(v))
	if err := e.EnsurePHP(ctx, v, log); err != nil {
		log("the new build did not start: %v; restoring the previous one", err)
		restore()
		dockerx.Remove(ctx, PHPName(v))
		if err2 := e.EnsurePHP(ctx, v, log); err2 != nil {
			return fmt.Errorf("update failed (%v) and the previous build did not start: %w", err, err2)
		}
		return fmt.Errorf("app.update_failed: %v", err)
	}
	for ref, id := range old {
		if imageID(ctx, ref) != id {
			dockerx.Run(ctx, "rmi", id) // the previous build, now untagged
		}
	}
	if oldBase != "" && oldBase != imageID(ctx, base) {
		dockerx.Run(ctx, "rmi", oldBase)
	}
	log("PHP %s updated", v)
	return nil
}

// phpImageVersion is the PHP_VERSION an image declares ("" if none).
func phpImageVersion(ctx context.Context, ref string) string {
	out, _ := dockerx.Run(ctx, "image", "inspect", "-f", "{{range .Config.Env}}{{println .}}{{end}}", ref)
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "PHP_VERSION="); ok {
			return v
		}
	}
	return ""
}

func imageID(ctx context.Context, ref string) string {
	id, _ := dockerx.Run(ctx, "image", "inspect", "-f", "{{.Id}}", ref)
	return id
}

// PHPBuild is how a PHP version is built: "fast" (the official image with
// the usual extensions) or "source" (PHP compiled again on this server).
type PHPBuild struct {
	Mode   string `json:"build_mode"`
	Args   string `json:"build_args"`
	CFlags string `json:"build_cflags"`
}

// PHPSourceMinutes is about how long a PHP compile takes on 2 cores.
const PHPSourceMinutes = 15

func (e *Env) phpBuildPath(v string) string { return filepath.Join(e.PHPDir(v), "build.json") }

func (e *Env) PHPBuildOf(v string) PHPBuild {
	b := PHPBuild{Mode: "fast"}
	if raw, err := os.ReadFile(e.phpBuildPath(v)); err == nil {
		json.Unmarshal(raw, &b)
	}
	if b.Mode != "source" {
		b = PHPBuild{Mode: "fast"}
	}
	return b
}

// WritePHPBuild records how a version not yet installed will be built.
func (e *Env) WritePHPBuild(v string, b PHPBuild) error {
	if err := os.MkdirAll(e.PHPDir(v), 0o755); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(b, "", "  ")
	return os.WriteFile(e.phpBuildPath(v), raw, 0o644)
}

// PHPDockerfile renders the build of lmcwhm/php:<v>, compiled again from
// source when source is set.
func PHPDockerfile(v string, source bool) (string, error) {
	df, err := render("php.Dockerfile.tmpl", map[string]any{"Version": v, "Source": source, "AptFallback": AptFallback})
	return string(df), err
}

// buildPHPBase builds lmcwhm/php:<v> (compiled again in source mode).
func (e *Env) buildPHPBase(ctx context.Context, v string, log func(string, ...any)) error {
	b := e.PHPBuildOf(v)
	df, err := PHPDockerfile(v, b.Mode == "source")
	if err != nil {
		return err
	}
	if b.Mode != "source" {
		return dockerx.Build(ctx, PHPImage(v), df)
	}
	log("compiling PHP %s from source (about %d minutes on 2 cores)", v, PHPSourceMinutes)
	dir := filepath.Join(e.Home, "logs", "build")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "php-"+v+".log")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	log("full build log: %s", path)
	w := NewProgressWriter(f, log)
	if err := dockerx.BuildStream(ctx, PHPImage(v), df, map[string]string{"CONFIGURE_ARGS": b.Args, "EXTRA_CFLAGS": b.CFlags}, w); err != nil {
		w.DumpTail()
		return fmt.Errorf("the PHP source build failed (see %s): %w", path, err)
	}
	return nil
}

// SetPHPBuild switches a version between the fast and the source build (or
// compiles it again with other options), then rebuilds and restarts it;
// the previous build and settings come back when that fails.
func (e *Env) SetPHPBuild(ctx context.Context, v string, b PHPBuild, log func(string, ...any)) error {
	if !ValidPHP(v) {
		return fmt.Errorf("unsupported PHP version %q", v)
	}
	prev := e.PHPBuildOf(v)
	raw, _ := json.MarshalIndent(b, "", "  ")
	if err := os.MkdirAll(e.PHPDir(v), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.phpBuildPath(v), raw, 0o644); err != nil {
		return err
	}
	if err := e.rebuildPHP(ctx, v, false, log); err != nil {
		old, _ := json.MarshalIndent(prev, "", "  ")
		os.WriteFile(e.phpBuildPath(v), old, 0o644)
		return err
	}
	log("PHP %s now runs the %s build", v, b.Mode)
	return nil
}

// ProgressWriter keeps a build's output in a file and shows a line of it in
// the task log now and then, and the last lines when the build fails.
type ProgressWriter struct {
	w    io.Writer
	log  func(string, ...any)
	last time.Time
	buf  string
	tail []string
}

func (p *ProgressWriter) Write(b []byte) (int, error) {
	p.w.Write(b)
	p.buf += string(b)
	for {
		i := strings.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(p.buf[:i])
		p.buf = p.buf[i+1:]
		if line == "" {
			continue
		}
		p.tail = append(p.tail, line)
		if len(p.tail) > 30 {
			p.tail = p.tail[1:]
		}
		if strings.Contains(line, "] RUN ") || time.Since(p.last) >= 20*time.Second {
			p.last = time.Now()
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			p.log("[build] %s", line)
		}
	}
	return len(b), nil
}

func NewProgressWriter(w io.Writer, log func(string, ...any)) *ProgressWriter {
	return &ProgressWriter{w: w, log: log, last: time.Now()}
}

// DumpTail shows the last lines of the output (after a failure).
func (p *ProgressWriter) DumpTail() {
	p.log("last lines of the build:")
	for _, l := range p.tail {
		p.log("  %s", l)
	}
}
