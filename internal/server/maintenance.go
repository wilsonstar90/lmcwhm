package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/config"
)

// Update / repair from the dashboard. Both run `lmcwhm update|repair` (the
// installer in upgrade mode: program files and the service are rebuilt;
// sites, databases, apps, firewall rules and panel settings are kept) in a
// transient systemd unit, because the installer restarts lmcwhm.service and
// would otherwise kill itself with the panel.

const (
	releasesAPI  = "https://api.github.com/repos/wilsonstar90/lmcwhm/releases/latest"
	releasesPage = "https://github.com/wilsonstar90/lmcwhm/releases"
	maintUnit    = "lmcwhm-maintenance"
)

var (
	errMaintRunning     = errors.New("maint.running")
	errMaintUnsupported = errors.New("maint.unsupported")
	errUpToDate         = errors.New("maint.up_to_date")
	errNoRollback       = errors.New("maint.no_rollback")
)

func (s *Server) maintenanceRoutes(r chi.Router) {
	r.Get("/system/update", s.handleCheckUpdate)
	r.Post("/system/update", s.handleRunMaintenance("update"))
	r.Post("/system/repair", s.handleRunMaintenance("repair"))
	r.Post("/system/rollback", s.handleRunMaintenance("rollback"))
	r.Get("/system/maintenance", s.handleMaintenanceStatus)
}

type releaseInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Newer     bool   `json:"newer"`
	Notes     string `json:"notes"`
	URL       string `json:"url"`
	Published string `json:"published_at"`
	Error     string `json:"error,omitempty"`
}

var (
	relMu     sync.Mutex
	relCache  = map[bool]releaseInfo{}
	relExpiry = map[bool]time.Time{}
)

// semver parses "v1.2.3" (pre-release / build suffixes ignored).
func semver(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

func newerVersion(latest, current string) bool {
	a, b := semver(latest), semver(current)
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	// A pre-release (v0.4.0-rc1) is older than its release; a git describe
	// build (v0.3.1-2-gabc123) is newer than the tag it was built after.
	return strings.Contains(current, "-") && !strings.Contains(current, "-g") && !strings.Contains(latest, "-")
}

func latestTagByRedirect(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, releasesPage+"/latest", nil)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Do(req)
	if err != nil {
		return "", err
	}
	res.Body.Close()
	_, tag, ok := strings.Cut(res.Header.Get("Location"), "/releases/tag/")
	if !ok || tag == "" {
		return "", fmt.Errorf("github: %s", res.Status)
	}
	return tag, nil
}

// latestRelease finds the newest stable release, or with beta the newest
// release of any kind (pre-releases included).
func latestRelease(ctx context.Context, force, beta bool) releaseInfo {
	relMu.Lock()
	defer relMu.Unlock()
	if !force && time.Now().Before(relExpiry[beta]) {
		return relCache[beta]
	}
	info := releaseInfo{Current: "v" + strings.TrimPrefix(config.Version, "v")}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	api := releasesAPI
	if beta {
		api = strings.TrimSuffix(releasesAPI, "/latest") + "?per_page=1"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lmcwhm/"+config.Version)
	var gh struct {
		TagName     string `json:"tag_name"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
	}
	res, err := http.DefaultClient.Do(req)
	if err == nil {
		if res.StatusCode == http.StatusOK && beta {
			var list []struct {
				TagName     string `json:"tag_name"`
				Body        string `json:"body"`
				HTMLURL     string `json:"html_url"`
				PublishedAt string `json:"published_at"`
			}
			json.NewDecoder(res.Body).Decode(&list)
			if len(list) > 0 {
				gh = list[0]
			}
		} else if res.StatusCode == http.StatusOK {
			json.NewDecoder(res.Body).Decode(&gh)
		} else {
			err = fmt.Errorf("github: %s", res.Status)
		}
		res.Body.Close()
	}
	if gh.TagName == "" && !beta {
		// The API allows 60 anonymous requests per hour per IP; the release
		// page redirect to /releases/tag/<tag> is not rate limited.
		if tag, ferr := latestTagByRedirect(ctx); ferr == nil {
			gh.TagName, gh.HTMLURL, err = tag, releasesPage+"/tag/"+tag, nil
		}
	}
	if err != nil || gh.TagName == "" {
		if err == nil {
			err = errors.New("github: no release")
		}
		info.Error = err.Error()
		relCache[beta], relExpiry[beta] = info, time.Now().Add(time.Minute)
		return info
	}
	info.Latest, info.Notes, info.URL, info.Published = gh.TagName, gh.Body, gh.HTMLURL, gh.PublishedAt
	info.Newer = gh.TagName != "" && newerVersion(gh.TagName, info.Current)
	relCache[beta], relExpiry[beta] = info, time.Now().Add(10*time.Minute)
	return info
}

func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	info := latestRelease(r.Context(), r.URL.Query().Get("force") == "1", r.URL.Query().Get("channel") == "beta")
	writeJSON(w, http.StatusOK, map[string]any{
		"current": info.Current, "latest": info.Latest, "newer": info.Newer, "notes": info.Notes, "url": info.URL,
		"published_at": info.Published, "error": info.Error, "rollback": previousVersion(r.Context()),
	})
}

// previousVersion is the version `lmcwhm rollback` returns to ("" = none).
func previousVersion(ctx context.Context) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe+".prev", "version").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) < 2 {
		return ""
	}
	return f[1]
}

func (s *Server) maintLog() string { return filepath.Join(s.Paths.LogDir, "maintenance.log") }

// RebuildMarker is the file whose presence makes the next boot recreate the
// web stack containers (see site.Service.RebuildMarker).
func RebuildMarker(p config.Paths) string { return filepath.Join(p.DataDir, "rebuild-stack") }

func unitActive(ctx context.Context) bool {
	out, _ := exec.CommandContext(ctx, "systemctl", "is-active", maintUnit).Output()
	st := strings.TrimSpace(string(out))
	return st == "active" || st == "activating"
}

func (s *Server) handleRunMaintenance(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Password string `json:"password"`
			Rebuild  bool   `json:"rebuild"`   // repair: also recreate the web stack containers
			Channel  string `json:"channel"`   // update: "beta" includes pre-releases
			WithData bool   `json:"with_data"` // rollback: also restore the pre-update data backup
		}
		if err := decode(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		sess := sessionFrom(r)
		if !s.Auth.CheckPassword(r.Context(), sess.User.ID, in.Password) {
			writeErr(w, http.StatusBadRequest, errBadPass)
			return
		}
		if _, err := exec.LookPath("systemd-run"); err != nil || os.Geteuid() != 0 {
			writeErr(w, http.StatusBadRequest, errMaintUnsupported)
			return
		}
		if unitActive(r.Context()) {
			writeErr(w, http.StatusConflict, errMaintRunning)
			return
		}
		beta := in.Channel == "beta"
		rel := latestRelease(r.Context(), true, beta)
		if action == "update" && rel.Error == "" && !rel.Newer {
			writeErr(w, http.StatusBadRequest, errUpToDate)
			return
		}
		args := []string{action}
		switch {
		case action == "update" && beta:
			args = append(args, "--channel", "beta")
		case action == "rollback":
			if previousVersion(r.Context()) == "" {
				writeErr(w, http.StatusBadRequest, errNoRollback)
				return
			}
			rel.Latest = previousVersion(r.Context())
			if in.WithData {
				args = append(args, "--with-data")
			}
		}
		exe, err := os.Executable()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if action == "repair" && in.Rebuild {
			os.WriteFile(RebuildMarker(s.Paths), nil, 0o600)
		}
		os.MkdirAll(s.Paths.LogDir, 0o755)
		header := fmt.Sprintf("== lmcwhm %s by %s at %s (current %s)\n", action, sess.User.Username, time.Now().Format(time.RFC3339), rel.Current)
		if err := os.WriteFile(s.maintLog(), []byte(header), 0o600); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		run := []string{"--unit", maintUnit, "--collect", "--quiet",
			"-p", "StandardOutput=append:" + s.maintLog(), "-p", "StandardError=append:" + s.maintLog(),
			"/bin/bash", "-c", `"$0" "$@"; echo "== exit $?"`, exe}
		cmd := exec.Command("systemd-run", append(run, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			s.Log.Error("maintenance", "err", err, "out", string(out))
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		s.auditCat(r, "system", "system."+action, rel.Current+" → "+rel.Latest)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (s *Server) handleMaintenanceStatus(w http.ResponseWriter, r *http.Request) {
	b, _ := os.ReadFile(s.maintLog())
	log := string(b)
	if len(log) > 64<<10 {
		log = log[len(log)-64<<10:]
	}
	exit := -1
	if i := strings.LastIndex(log, "== exit "); i >= 0 {
		exit, _ = strconv.Atoi(strings.TrimSpace(log[i+8:]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running": unitActive(r.Context()), "log": log, "exit": exit, "version": "v" + strings.TrimPrefix(config.Version, "v"),
	})
}
