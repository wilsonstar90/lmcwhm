package appstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

// Update is the patch-update state of one installed app version ("app:<id>")
// or PHP version ("php:<v>"), kept in the settings table.
type Update struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Image        string `json:"image"` // the tag that is checked
	Current      string `json:"current"`
	Latest       string `json:"latest,omitempty"`
	Available    bool   `json:"available"`
	CheckedAt    int64  `json:"checked_at"`
	Error        string `json:"error,omitempty"`
	LatestDigest string `json:"latest_digest,omitempty"`
	Notified     string `json:"notified,omitempty"` // digest already alerted
}

const updatesKey = "app_updates"

var updatesMu sync.Mutex

// PHPBase is the upstream image a PHP version is built from.
func PHPBase(v string) string { return "php:" + v + "-fpm" }

func (sv *Service) loadUpdates(ctx context.Context) map[string]Update {
	m := map[string]Update{}
	var raw string
	if sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, updatesKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &m)
	}
	return m
}

func (sv *Service) saveUpdates(ctx context.Context, m map[string]Update) error {
	b, _ := json.Marshal(m)
	_, err := sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, updatesKey, string(b))
	return err
}

// target is something whose image can be checked and updated.
type target struct {
	key, name string
	upstream  string // registry tag
	local     string // local image whose version is shown (a PHP build inherits its base's)
}

func appKey(id int64) string { return "app:" + strconv.FormatInt(id, 10) }

// PHPKey is the update key of a PHP version.
func PHPKey(v string) string { return "php:" + v }

func (sv *Service) targets(ctx context.Context) []target {
	var out []target
	if all, err := sv.list(ctx, ""); err == nil {
		for _, i := range all {
			out = append(out, target{key: appKey(i.ID), name: i.Name + " " + i.Version, upstream: i.Image, local: i.Image})
		}
	}
	for _, v := range webenv.PHPVersions {
		if dockerx.State(ctx, webenv.PHPName(v)) == "" {
			continue
		}
		local, _ := dockerx.Run(ctx, "inspect", "-f", "{{.Config.Image}}", webenv.PHPName(v))
		if local == "" {
			local = webenv.PHPImage(v)
		}
		out = append(out, target{key: PHPKey(v), name: "PHP " + v, upstream: PHPBase(v), local: local})
	}
	return out
}

// CurrentVersion is the exact version a local image declares ("" if unknown).
func CurrentVersion(ctx context.Context, ref string) string {
	iv, _ := LocalImage(ctx, ref)
	return iv.Version
}

// check compares one target with its registry tag.
func check(ctx context.Context, t target, prev Update) Update {
	u := Update{Key: t.key, Name: t.name, Image: t.upstream, CheckedAt: time.Now().Unix(), Notified: prev.Notified}
	cur, _ := LocalImage(ctx, t.local)
	u.Current = cur.Version
	base := cur
	if t.local != t.upstream {
		base, _ = LocalImage(ctx, t.upstream) // the digest lives on the pulled base
		if u.Current == "" {
			u.Current = base.Version
		}
	}
	remote, err := RemoteImage(ctx, t.upstream, base.Digest)
	if err != nil {
		u.Error = err.Error()
		// Keep the last known answer when the registry cannot be reached.
		u.Latest, u.Available, u.LatestDigest = prev.Latest, prev.Available && prev.Current == u.Current, prev.LatestDigest
		return u
	}
	decide(&u, base.Digest, remote)
	return u
}

// decide sets Latest / Available from the local digest and the registry's answer.
func decide(u *Update, localDigest string, remote ImageVersion) {
	u.LatestDigest = remote.Digest
	switch {
	case localDigest != "" && remote.Digest == localDigest:
		u.Latest = u.Current
	case remote.Version != "" && remote.Version != u.Current:
		u.Latest, u.Available = remote.Version, true
	case localDigest != "" && remote.Digest != "":
		// Same version, new build (security fixes of the OS underneath).
		u.Latest, u.Available = remote.Version, true
	default:
		u.Latest = remote.Version
	}
}

// CheckUpdates asks the registries about every installed version and stores
// the answers; notify is called once per newly found update.
func (sv *Service) CheckUpdates(ctx context.Context, notify func(Update)) (map[string]Update, error) {
	updatesMu.Lock()
	defer updatesMu.Unlock()
	prev := sv.loadUpdates(ctx)
	next := map[string]Update{}
	for _, t := range sv.targets(ctx) {
		u := check(ctx, t, prev[t.key])
		if u.Available && u.Notified != u.LatestDigest && notify != nil {
			notify(u)
			u.Notified = u.LatestDigest
		}
		next[t.key] = u
	}
	return next, sv.saveUpdates(ctx, next)
}

// Updates returns the stored answers with live current versions.
func (sv *Service) Updates(ctx context.Context) map[string]Update {
	updatesMu.Lock()
	defer updatesMu.Unlock()
	m := sv.loadUpdates(ctx)
	live := map[string]bool{}
	for _, t := range sv.targets(ctx) {
		live[t.key] = true
		u, ok := m[t.key]
		if !ok {
			u = Update{Key: t.key, Name: t.name, Image: t.upstream}
		}
		if v := CurrentVersion(ctx, t.local); v != "" {
			u.Current = v
		} else if v := CurrentVersion(ctx, t.upstream); v != "" {
			u.Current = v
		}
		m[t.key] = u
	}
	for k := range m {
		if !live[k] {
			delete(m, k)
		}
	}
	return m
}

// refresh re-checks one key after an update.
func (sv *Service) refresh(ctx context.Context, key string) {
	updatesMu.Lock()
	defer updatesMu.Unlock()
	m := sv.loadUpdates(ctx)
	for _, t := range sv.targets(ctx) {
		if t.key == key {
			m[key] = check(ctx, t, m[key])
			sv.saveUpdates(ctx, m)
			return
		}
	}
}

// RefreshPHP re-checks a PHP version after it was rebuilt.
func (sv *Service) RefreshPHP(ctx context.Context, v string) { sv.refresh(ctx, PHPKey(v)) }

// ---- updating ----

func imageID(ctx context.Context, ref string) string {
	id, _ := dockerx.Run(ctx, "image", "inspect", "-f", "{{.Id}}", ref)
	return id
}

func rollbackTag(app, version string) string { return "lmcwhm/rollback:" + app + "-" + version }

// Update pulls the newest image of the same tag and restarts the app on it,
// keeping its data. Databases are backed up first; a service that does not
// come back is restored on the previous image (task).
func (sv *Service) Update(ctx context.Context, id int64) (string, error) {
	i, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return sv.tasks.Start("app.update:"+i.App+"-"+i.Version, func(ctx context.Context, log tasks.Logf) error {
		if err := sv.update(ctx, i, log); err != nil {
			return err
		}
		sv.refresh(ctx, appKey(i.ID))
		return nil
	}), nil
}

func (sv *Service) update(ctx context.Context, i Installed, log tasks.Logf) error {
	if !dockerx.Available(ctx) {
		return dockerx.ErrNoDocker
	}
	before := CurrentVersion(ctx, i.Image)
	oldID := imageID(ctx, i.Image)
	log("pulling %s", i.Image)
	if _, err := dockerx.Run(ctx, "pull", "-q", i.Image); err != nil {
		return err
	}
	if imageID(ctx, i.Image) == oldID && oldID != "" {
		log("%s %s is already the newest build (%s)", i.Name, i.Version, before)
		return nil
	}
	after := CurrentVersion(ctx, i.Image)
	log("new image: %s → %s", orUnknown(before), orUnknown(after))
	if i.Container == "" {
		// A runtime: website projects are recreated on the new image.
		if oldID != "" {
			dockerx.Run(ctx, "rmi", oldID)
		}
		if sv.RuntimeUpdated != nil {
			sv.RuntimeUpdated(ctx, i.App, i.Version, log)
		}
		log("%s %s updated", i.Name, i.Version)
		return nil
	}
	keep := rollbackTag(i.App, i.Version)
	if oldID != "" {
		if _, err := dockerx.Run(ctx, "tag", oldID, keep); err != nil {
			return err
		}
	}
	if sv.Backup != nil && i.Databases {
		log("backing up the databases of %s %s", i.Name, i.Version)
		if err := sv.Backup(ctx, i, log); err != nil {
			restoreTag(ctx, oldID, i.Image, keep)
			return fmt.Errorf("backup before the update failed: %w", err)
		}
	}
	log("recreating %s", i.Container)
	if err := dockerx.Remove(ctx, i.Container); err != nil {
		return err
	}
	err := sv.start(ctx, i, log)
	if err == nil {
		if oldID != "" {
			dockerx.Run(ctx, "rmi", keep)
			dockerx.Run(ctx, "rmi", oldID) // fails harmlessly while something else uses it
		}
		if pmaEngine(i.App) || i.App == "phpmyadmin" {
			if err := sv.RefreshPMA(ctx); err != nil {
				log("phpMyAdmin: %v", err)
			}
		}
		log("%s %s updated to %s", i.Name, i.Version, orUnknown(after))
		return nil
	}
	if oldID == "" {
		return err
	}
	log("the new version did not start: %v", err)
	log("restoring %s", orUnknown(before))
	restoreTag(ctx, oldID, i.Image, keep)
	dockerx.Remove(ctx, i.Container)
	if err2 := sv.start(ctx, i, log); err2 != nil {
		return fmt.Errorf("update failed (%v) and the previous version did not start: %w", err, err2)
	}
	return fmt.Errorf("app.update_failed: %v", err)
}

// restoreTag points ref back at the old image and drops the rollback tag.
func restoreTag(ctx context.Context, oldID, ref, keep string) {
	if oldID == "" {
		return
	}
	dockerx.Run(ctx, "tag", oldID, ref)
	dockerx.Run(ctx, "rmi", keep)
}

func orUnknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "?"
	}
	return v
}

// forget drops the update state of a removed app or PHP version.
func (sv *Service) forget(ctx context.Context, key string) {
	updatesMu.Lock()
	defer updatesMu.Unlock()
	m := sv.loadUpdates(ctx)
	if _, ok := m[key]; ok {
		delete(m, key)
		sv.saveUpdates(ctx, m)
	}
}

// ForgetPHP drops the update state of a removed PHP version.
func (sv *Service) ForgetPHP(ctx context.Context, v string) { sv.forget(ctx, PHPKey(v)) }
