// Package wordpress is the WordPress toolkit: one-click install on a PHP
// website (database, wp-config, admin user), and maintenance through WP-CLI
// in the site's PHP container — core / plugin / theme updates, checksum
// verification and hardening.
package wordpress

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/database"
	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/site"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

var (
	ErrNotPHP     = errors.New("wp.not_php")
	ErrNotWP      = errors.New("wp.not_wordpress")
	ErrNotEmpty   = errors.New("wp.root_not_empty")
	ErrInput      = errors.New("wp.invalid_input")
	ErrNoEngine   = errors.New("wp.no_mysql")
	ErrAction     = errors.New("wp.invalid_action")
	localeRe      = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?$`)
	userRe        = regexp.MustCompile(`^[A-Za-z0-9_.@-]{3,60}$`)
	emailRe       = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	slugRe        = regexp.MustCompile(`^[a-z0-9_.-]{1,100}$`)
	versionRe     = regexp.MustCompile(`\$wp_version\s*=\s*'([^']+)'`)
	wpCLIURL      = "https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar"
	placeholders  = map[string]bool{"index.html": true, ".user.ini": true, ".well-known": true, "404.html": true, "50x.html": true}
	toolsDirName  = ".lmcwhm"
	wpCLIFileName = "wp-cli.phar"
)

type Service struct {
	Sites *site.Service
	DBs   *database.Service
	Env   *webenv.Env
	tasks *tasks.Manager
}

func New(sites *site.Service, dbs *database.Service, env *webenv.Env, tm *tasks.Manager) *Service {
	return &Service{Sites: sites, DBs: dbs, Env: env, tasks: tm}
}

// WPSite is a website that has WordPress in its root.
type WPSite struct {
	SiteID  int64  `json:"site_id"`
	Domain  string `json:"domain"`
	Root    string `json:"root"`
	PHP     string `json:"php_version"`
	Version string `json:"version"`
}

func version(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "wp-includes", "version.php"))
	if err != nil {
		return ""
	}
	if m := versionRe.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

// List finds WordPress installs among the PHP websites.
func (sv *Service) List(ctx context.Context) ([]WPSite, error) {
	all, err := sv.Sites.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []WPSite{}
	for _, s := range all {
		if s.Type != "php" {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.Root, "wp-config.php")); err != nil {
			continue
		}
		out = append(out, WPSite{SiteID: s.ID, Domain: s.Domain, Root: s.Root, PHP: s.PHPVersion, Version: version(s.Root)})
	}
	return out, nil
}

func (sv *Service) phpSite(ctx context.Context, id int64) (site.Site, error) {
	s, err := sv.Sites.Get(ctx, id)
	if err != nil {
		return s, err
	}
	if s.Type != "php" {
		return s, ErrNotPHP
	}
	return s, nil
}

// ---- WP-CLI ----

func (sv *Service) cliPath() string {
	return filepath.Join(sv.Env.WWWRoot, toolsDirName, wpCLIFileName)
}

// ensureCLI downloads WP-CLI (checked against its published SHA-512) into a
// directory the PHP containers see but no website serves.
func (sv *Service) ensureCLI(ctx context.Context, log tasks.Logf) error {
	p := sv.cliPath()
	if fi, err := os.Stat(p); err == nil && fi.Size() > 0 && time.Since(fi.ModTime()) < 30*24*time.Hour {
		return nil
	}
	log("downloading WP-CLI")
	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		res, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: HTTP %d", u, res.StatusCode)
		}
		return io.ReadAll(io.LimitReader(res.Body, 64<<20))
	}
	phar, err := get(wpCLIURL)
	if err != nil {
		return err
	}
	sum, err := get(wpCLIURL + ".sha512")
	if err != nil {
		return err
	}
	h := sha512.Sum512(phar)
	if want := strings.Fields(string(sum)); len(want) == 0 || !strings.EqualFold(want[0], hex.EncodeToString(h[:])) {
		return errors.New("WP-CLI checksum mismatch")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p+".tmp", phar, 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// wp runs WP-CLI as www-data in the site's PHP container.
func (sv *Service) wp(ctx context.Context, s site.Site, args ...string) (string, error) {
	cmd := append([]string{"exec", "-u", "33", "-w", s.Root, "-e", "HOME=/tmp", "-e", "WP_CLI_CACHE_DIR=/tmp/wp-cli-cache",
		webenv.PHPName(s.PHPVersion), "php", "-d", "memory_limit=512M", sv.cliPath(), "--path=" + s.Root, "--no-color"}, args...)
	return dockerx.Run(ctx, cmd...)
}

func (sv *Service) wpJSON(ctx context.Context, s site.Site, v any, args ...string) error {
	out, err := sv.wp(ctx, s, append(args, "--format=json")...)
	if err != nil {
		return err
	}
	// WP-CLI may print PHP notices before the JSON.
	if i := strings.IndexAny(out, "[{"); i > 0 {
		out = out[i:]
	}
	return json.Unmarshal([]byte(out), v)
}

// ---- install ----

type InstallInput struct {
	Title      string `json:"title"`
	AdminUser  string `json:"admin_user"`
	AdminPass  string `json:"admin_pass"`
	AdminEmail string `json:"admin_email"`
	Locale     string `json:"locale"` // e.g. zh_TW, en_US
	DBAppID    int64  `json:"db_app_id"`
	Force      bool   `json:"force"` // install even if the root has files
}

func dbName(domain string) string {
	n := "wp_" + regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(domain), "_")
	if len(n) > 28 {
		n = n[:28]
	}
	return strings.TrimRight(n, "_")
}

// Install puts WordPress into a PHP website (task).
func (sv *Service) Install(ctx context.Context, siteID int64, in InstallInput) (string, error) {
	s, err := sv.phpSite(ctx, siteID)
	if err != nil {
		return "", err
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Locale == "" {
		in.Locale = "en_US"
	}
	if in.Title == "" || len(in.Title) > 200 || !userRe.MatchString(in.AdminUser) || len(in.AdminPass) < 8 || len(in.AdminPass) > 128 ||
		!emailRe.MatchString(in.AdminEmail) || !localeRe.MatchString(in.Locale) {
		return "", ErrInput
	}
	if !in.Force {
		entries, _ := os.ReadDir(s.Root)
		for _, e := range entries {
			if !placeholders[e.Name()] {
				return "", ErrNotEmpty
			}
		}
	}
	scheme := "http"
	if s.SSLMode != "none" && s.CertNotAfter > 0 {
		scheme = "https"
	}
	return sv.tasks.Start("wordpress.install:"+s.Domain, func(ctx context.Context, log tasks.Logf) error {
		if err := sv.ensureCLI(ctx, log); err != nil {
			return err
		}
		var db database.Database
		var err error
		base := dbName(s.Domain)
		for i := 0; i < 20; i++ {
			name := base
			if i > 0 {
				name = fmt.Sprintf("%s_%d", base, i+1)
			}
			db, err = sv.DBs.CreateNow(ctx, database.CreateInput{AppID: in.DBAppID, Name: name, Remark: "WordPress " + s.Domain}, log)
			if !errors.Is(err, database.ErrExists) {
				break
			}
		}
		if err != nil {
			return err
		}
		if db.Engine != "mysql" && db.Engine != "mariadb" {
			return ErrNoEngine
		}
		os.Remove(filepath.Join(s.Root, "index.html")) // placeholder page
		log("downloading WordPress (%s)", in.Locale)
		if out, err := sv.wp(ctx, s, "core", "download", "--locale="+in.Locale, "--force"); err != nil {
			return fmt.Errorf("%v\n%s", err, out)
		}
		log("writing wp-config.php")
		if _, err := sv.wp(ctx, s, "config", "create", "--dbname="+db.Name, "--dbuser="+db.Username, "--dbpass="+db.Password,
			fmt.Sprintf("--dbhost=127.0.0.1:%d", db.Port), "--locale="+in.Locale, "--force"); err != nil {
			return err
		}
		log("installing %s://%s", scheme, s.Domain)
		if _, err := sv.wp(ctx, s, "core", "install", "--url="+scheme+"://"+s.Domain, "--title="+in.Title,
			"--admin_user="+in.AdminUser, "--admin_password="+in.AdminPass, "--admin_email="+in.AdminEmail, "--skip-email"); err != nil {
			return err
		}
		sv.wp(ctx, s, "config", "set", "DISALLOW_FILE_EDIT", "true", "--raw")
		log("WordPress %s installed; admin: %s://%s/wp-admin/", version(s.Root), scheme, s.Domain)
		return nil
	}), nil
}

// ---- maintenance ----

type Plugin struct {
	Name          string `json:"name"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Version       string `json:"version"`
	Update        string `json:"update"`
	UpdateVersion string `json:"update_version"`
}

type Info struct {
	WPSite
	SiteURL     string   `json:"site_url"`
	CoreUpdates []string `json:"core_updates"`
	Plugins     []Plugin `json:"plugins"`
	Themes      []Plugin `json:"themes"`
	FileEdit    bool     `json:"file_edit"` // dashboard plugin / theme editor allowed
	Debug       bool     `json:"debug"`
	Admins      []string `json:"admins"`
}

func (sv *Service) wpSite(ctx context.Context, id int64) (site.Site, error) {
	s, err := sv.phpSite(ctx, id)
	if err != nil {
		return s, err
	}
	if _, err := os.Stat(filepath.Join(s.Root, "wp-config.php")); err != nil {
		return s, ErrNotWP
	}
	return s, nil
}

func (sv *Service) Info(ctx context.Context, id int64) (Info, error) {
	s, err := sv.wpSite(ctx, id)
	if err != nil {
		return Info{}, err
	}
	if err := sv.ensureCLI(ctx, func(string, ...any) {}); err != nil {
		return Info{}, err
	}
	in := Info{WPSite: WPSite{SiteID: s.ID, Domain: s.Domain, Root: s.Root, PHP: s.PHPVersion, Version: version(s.Root)},
		CoreUpdates: []string{}, Plugins: []Plugin{}, Themes: []Plugin{}, Admins: []string{}}
	in.SiteURL, _ = sv.wp(ctx, s, "option", "get", "siteurl")
	var core []struct {
		Version string `json:"version"`
	}
	sv.wpJSON(ctx, s, &core, "core", "check-update")
	for _, c := range core {
		in.CoreUpdates = append(in.CoreUpdates, c.Version)
	}
	if err := sv.wpJSON(ctx, s, &in.Plugins, "plugin", "list", "--fields=name,title,status,version,update,update_version"); err != nil {
		return in, err
	}
	sv.wpJSON(ctx, s, &in.Themes, "theme", "list", "--fields=name,title,status,version,update,update_version")
	fe, _ := sv.wp(ctx, s, "config", "get", "DISALLOW_FILE_EDIT")
	in.FileEdit = strings.TrimSpace(fe) != "1"
	dbg, _ := sv.wp(ctx, s, "config", "get", "WP_DEBUG")
	in.Debug = strings.TrimSpace(dbg) == "1"
	var admins []struct {
		Login string `json:"user_login"`
	}
	sv.wpJSON(ctx, s, &admins, "user", "list", "--role=administrator", "--fields=user_login")
	for _, a := range admins {
		in.Admins = append(in.Admins, a.Login)
	}
	return in, nil
}

type ActionInput struct {
	Action   string `json:"action"`
	Name     string `json:"name"`     // plugin / theme slug or admin login
	Password string `json:"password"` // admin_password
	On       bool   `json:"on"`
}

// Action runs a maintenance action (task).
func (sv *Service) Action(ctx context.Context, id int64, in ActionInput) (string, error) {
	s, err := sv.wpSite(ctx, id)
	if err != nil {
		return "", err
	}
	var args [][]string
	switch in.Action {
	case "core_update":
		args = [][]string{{"core", "update"}, {"core", "update-db"}}
	case "plugins_update":
		args = [][]string{{"plugin", "update", "--all"}}
	case "themes_update":
		args = [][]string{{"theme", "update", "--all"}}
	case "verify":
		args = [][]string{{"core", "verify-checksums"}, {"plugin", "verify-checksums", "--all"}}
	case "plugin_activate", "plugin_deactivate", "plugin_update", "plugin_delete":
		if !slugRe.MatchString(in.Name) {
			return "", ErrInput
		}
		args = [][]string{{"plugin", strings.TrimPrefix(in.Action, "plugin_"), in.Name}}
	case "theme_activate", "theme_update":
		if !slugRe.MatchString(in.Name) {
			return "", ErrInput
		}
		args = [][]string{{"theme", strings.TrimPrefix(in.Action, "theme_"), in.Name}}
	case "file_edit":
		args = [][]string{{"config", "set", "DISALLOW_FILE_EDIT", fmt.Sprint(!in.On), "--raw"}}
	case "debug":
		args = [][]string{{"config", "set", "WP_DEBUG", fmt.Sprint(in.On), "--raw"}}
	case "admin_password":
		if !userRe.MatchString(in.Name) || len(in.Password) < 8 || len(in.Password) > 128 {
			return "", ErrInput
		}
		args = [][]string{{"user", "update", in.Name, "--user_pass=" + in.Password, "--skip-email"}}
	case "cache_flush":
		args = [][]string{{"cache", "flush"}, {"rewrite", "flush"}}
	default:
		return "", ErrAction
	}
	return sv.tasks.Start("wordpress."+in.Action+":"+s.Domain, func(ctx context.Context, log tasks.Logf) error {
		if err := sv.ensureCLI(ctx, log); err != nil {
			return err
		}
		for _, a := range args {
			shown := strings.Join(a, " ")
			if in.Action == "admin_password" {
				shown = "user update " + in.Name + " --user_pass=***"
			}
			log("wp %s", shown)
			out, err := sv.wp(ctx, s, a...)
			for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
				if l != "" {
					log("  %s", l)
				}
			}
			if err != nil {
				return err
			}
		}
		return nil
	}), nil
}
