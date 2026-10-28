// Package tamper protects website files from modification (寶塔網站防篡改 /
// aaPanel File Protection). A rule covers a directory in one of two modes:
//
//   - lock:  protected files and directories get the immutable attribute
//     (chattr +i), so nothing — not even root or PHP — can change them until
//     the rule is turned off;
//   - watch: inotify reports every change to a protected file and, with
//     Restore, puts the baseline copy back (new files are moved aside).
//
// Excluded sub-directories (uploads, cache) stay writable in both modes.
package tamper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

var (
	ErrNotFound = errors.New("tamper.not_found")
	ErrInvalid  = errors.New("tamper.invalid_rule")
	ErrPath     = errors.New("tamper.invalid_path")
	ErrExists   = errors.New("tamper.path_exists")
	ErrNoWatch  = errors.New("tamper.watch_unsupported")
)

// DefaultExts are protected when a rule names none.
var DefaultExts = []string{"php", "html", "htm", "js", "css", "htaccess", "user.ini", "jsp", "asp", "aspx", "shtml", "phtml", "tpl"}

// DefaultExclude are common writable directories of PHP apps.
var DefaultExclude = []string{"wp-content/uploads", "wp-content/cache", "wp-content/upgrade", "storage", "runtime", "cache", "tmp", "uploads"}

type Rule struct {
	ID        int64    `json:"id"`
	SiteID    int64    `json:"site_id"`
	Path      string   `json:"path"`
	Mode      string   `json:"mode"` // lock | watch
	Exts      []string `json:"exts"`
	Exclude   []string `json:"exclude"` // relative directories
	Restore   bool     `json:"restore"` // watch: undo changes
	Enabled   bool     `json:"enabled"`
	CreatedAt int64    `json:"created_at"`
	// Runtime state.
	State string `json:"state"` // locked | watching | off | error
	Error string `json:"error,omitempty"`
	Files int    `json:"files"`
}

type Event struct {
	ID        int64  `json:"id"`
	RuleID    int64  `json:"rule_id"`
	Path      string `json:"path"`
	Op        string `json:"op"`     // modify | create | delete | rename | attrib
	Action    string `json:"action"` // logged | restored | removed | failed
	CreatedAt int64  `json:"created_at"`
}

type Service struct {
	st      *store.Store
	WebRoot string
	Dir     string // /opt/lmcwhm/tamper: baselines and blocked files
	// OnEvent is called for every change (alerts).
	OnEvent func(r Rule, e Event)

	mu      sync.Mutex
	watches map[int64]*watcher
	status  map[int64]string // last error per rule
}

func New(st *store.Store, webRoot, home string) *Service {
	return &Service{st: st, WebRoot: webRoot, Dir: filepath.Join(home, "tamper"), watches: map[int64]*watcher{}, status: map[int64]string{}}
}

const cols = `id, site_id, path, mode, exts, exclude, restore, enabled, created_at`

func scanRule(row interface{ Scan(...any) error }) (Rule, error) {
	var r Rule
	var exts, ex string
	err := row.Scan(&r.ID, &r.SiteID, &r.Path, &r.Mode, &exts, &ex, &r.Restore, &r.Enabled, &r.CreatedAt)
	json.Unmarshal([]byte(exts), &r.Exts)
	json.Unmarshal([]byte(ex), &r.Exclude)
	return r, err
}

func (sv *Service) decorate(r *Rule) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	switch {
	case !r.Enabled:
		r.State = "off"
	case sv.status[r.ID] != "":
		r.State, r.Error = "error", sv.status[r.ID]
	case r.Mode == "lock":
		r.State = "locked"
	case sv.watches[r.ID] != nil:
		r.State, r.Files = "watching", sv.watches[r.ID].count()
	default:
		r.State = "off"
	}
}

func (sv *Service) List(ctx context.Context) ([]Rule, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT `+cols+` FROM tamper_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		sv.decorate(&r)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (sv *Service) Get(ctx context.Context, id int64) (Rule, error) {
	r, err := scanRule(sv.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM tamper_rules WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	sv.decorate(&r)
	return r, err
}

var extRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.]{0,15}$`)

func (sv *Service) normalize(r *Rule) error {
	p := filepath.Clean("/" + r.Path)
	root := filepath.Clean(sv.WebRoot)
	if p == root || !strings.HasPrefix(p, root+"/") {
		return ErrPath
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		return ErrPath
	}
	r.Path = p
	if r.Mode != "lock" && r.Mode != "watch" {
		return ErrInvalid
	}
	exts := []string{}
	for _, e := range r.Exts {
		e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
		if e == "" {
			continue
		}
		if !extRe.MatchString(e) {
			return ErrInvalid
		}
		if !slices.Contains(exts, e) {
			exts = append(exts, e)
		}
	}
	if len(exts) == 0 {
		exts = append(exts, DefaultExts...)
	}
	r.Exts = exts
	ex := []string{}
	for _, e := range r.Exclude {
		e = strings.Trim(filepath.Clean("/"+strings.TrimSpace(e)), "/")
		if e == "" || e == "." {
			continue
		}
		if strings.Contains(e, "..") {
			return ErrInvalid
		}
		if !slices.Contains(ex, e) {
			ex = append(ex, e)
		}
	}
	r.Exclude = ex
	return nil
}

// Save creates or updates a rule and applies it.
func (sv *Service) Save(ctx context.Context, r Rule) (Rule, error) {
	if err := sv.normalize(&r); err != nil {
		return r, err
	}
	exts, _ := json.Marshal(r.Exts)
	ex, _ := json.Marshal(r.Exclude)
	if r.ID == 0 {
		var n int
		sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tamper_rules WHERE path = ?`, r.Path).Scan(&n)
		if n > 0 {
			return r, ErrExists
		}
		r.CreatedAt = time.Now().Unix()
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO tamper_rules(site_id, path, mode, exts, exclude, restore, enabled, created_at) VALUES(?,?,?,?,?,?,?,?)`,
			r.SiteID, r.Path, r.Mode, string(exts), string(ex), r.Restore, r.Enabled, r.CreatedAt)
		if err != nil {
			return r, err
		}
		r.ID, _ = res.LastInsertId()
	} else {
		old, err := sv.Get(ctx, r.ID)
		if err != nil {
			return r, err
		}
		sv.stop(old) // unlock / unwatch with the old settings
		if _, err := sv.st.DB.ExecContext(ctx, `UPDATE tamper_rules SET site_id=?, path=?, mode=?, exts=?, exclude=?, restore=?, enabled=? WHERE id=?`,
			r.SiteID, r.Path, r.Mode, string(exts), string(ex), r.Restore, r.Enabled, r.ID); err != nil {
			return r, err
		}
	}
	err := sv.apply(r)
	sv.decorate(&r)
	return r, err
}

func (sv *Service) Delete(ctx context.Context, id int64) error {
	r, err := sv.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := sv.stop(r); err != nil {
		return err
	}
	sv.st.DB.ExecContext(ctx, `DELETE FROM tamper_events WHERE rule_id = ?`, id)
	os.RemoveAll(sv.ruleDir(id))
	_, err = sv.st.DB.ExecContext(ctx, `DELETE FROM tamper_rules WHERE id = ?`, id)
	return err
}

// SetEnabled turns a rule on or off (e.g. off before a WordPress update).
func (sv *Service) SetEnabled(ctx context.Context, id int64, on bool) (Rule, error) {
	r, err := sv.Get(ctx, id)
	if err != nil {
		return r, err
	}
	r.Enabled = on
	return sv.Save(ctx, r)
}

// UnlockPath turns off every rule at or below path (site deletion).
func (sv *Service) UnlockPath(ctx context.Context, path string) error {
	rules, err := sv.List(ctx)
	if err != nil {
		return err
	}
	for _, r := range rules {
		if r.Path == path || strings.HasPrefix(r.Path, strings.TrimRight(path, "/")+"/") {
			if err := sv.stop(r); err != nil {
				return err
			}
			sv.st.DB.ExecContext(ctx, `UPDATE tamper_rules SET enabled = 0 WHERE id = ?`, r.ID)
		}
	}
	return nil
}

func (sv *Service) Events(ctx context.Context, ruleID int64, limit int) ([]Event, error) {
	q, args := `SELECT id, rule_id, path, op, action, created_at FROM tamper_events`, []any{}
	if ruleID > 0 {
		q += ` WHERE rule_id = ?`
		args = append(args, ruleID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := sv.st.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.RuleID, &e.Path, &e.Op, &e.Action, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (sv *Service) record(r Rule, path, op, action string) {
	e := Event{RuleID: r.ID, Path: path, Op: op, Action: action, CreatedAt: time.Now().Unix()}
	res, err := sv.st.DB.Exec(`INSERT INTO tamper_events(rule_id, path, op, action, created_at) VALUES(?,?,?,?,?)`, e.RuleID, e.Path, e.Op, e.Action, e.CreatedAt)
	if err == nil {
		e.ID, _ = res.LastInsertId()
	}
	sv.st.DB.Exec(`DELETE FROM tamper_events WHERE id <= (SELECT id FROM tamper_events ORDER BY id DESC LIMIT 1 OFFSET 5000)`)
	if sv.OnEvent != nil {
		sv.OnEvent(r, e)
	}
}

// Boot applies every enabled rule at panel start (watchers do not survive a
// restart; locks do, but are re-applied to cover files added meanwhile).
func (sv *Service) Boot(ctx context.Context) {
	rules, err := sv.List(ctx)
	if err != nil {
		return
	}
	for _, r := range rules {
		if r.Enabled {
			sv.apply(r)
		}
	}
}

// Stop ends every watcher (panel shutdown); locks stay.
func (sv *Service) Stop() {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	for id, w := range sv.watches {
		w.close()
		delete(sv.watches, id)
	}
}

func (sv *Service) setStatus(id int64, err error) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if err != nil {
		sv.status[id] = err.Error()
	} else {
		delete(sv.status, id)
	}
}

func (sv *Service) apply(r Rule) error {
	if !r.Enabled {
		sv.setStatus(r.ID, nil)
		return nil
	}
	var err error
	if r.Mode == "lock" {
		err = setImmutable(r, true)
	} else {
		err = sv.startWatch(r)
	}
	sv.setStatus(r.ID, err)
	return err
}

func (sv *Service) stop(r Rule) error {
	sv.mu.Lock()
	if w := sv.watches[r.ID]; w != nil {
		w.close()
		delete(sv.watches, r.ID)
	}
	sv.mu.Unlock()
	sv.setStatus(r.ID, nil)
	if r.Mode == "lock" {
		return setImmutable(r, false)
	}
	return nil
}

// ---- matching ----

func (r Rule) excluded(rel string) bool {
	for _, e := range r.Exclude {
		if rel == e || strings.HasPrefix(rel, e+"/") {
			return true
		}
	}
	return false
}

func (r Rule) protectsFile(name string) bool {
	low := strings.ToLower(name)
	for _, e := range r.Exts {
		if strings.HasSuffix(low, "."+e) || low == e || low == "."+e {
			return true
		}
	}
	return false
}

// targets walks the rule's tree: protected files, and the directories that
// hold them (excluded directories and their contents are skipped).
func (r Rule) targets(fn func(path string, dir bool)) {
	filepath.WalkDir(r.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(r.Path, p)
		if rel != "." && r.excluded(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			fn(p, true)
		} else if d.Type().IsRegular() && r.protectsFile(d.Name()) {
			fn(p, false)
		}
		return nil
	})
}

// ---- lock mode ----

// setImmutable runs chattr ±i in batches. A directory that is an ancestor of
// an excluded one is locked too: its excluded child stays writable.
func setImmutable(r Rule, on bool) error {
	flag := "-i"
	if on {
		flag = "+i"
	}
	var batch []string
	var firstErr error
	flush := func() {
		if len(batch) == 0 {
			return
		}
		out, err := exec.Command("chattr", append([]string{flag, "--"}, batch...)...).CombinedOutput()
		if err != nil && firstErr == nil {
			firstErr = errors.New("chattr: " + strings.TrimSpace(string(out)))
		}
		batch = batch[:0]
	}
	r.targets(func(p string, _ bool) {
		batch = append(batch, p)
		if len(batch) >= 400 {
			flush()
		}
	})
	flush()
	return firstErr
}

// ---- watch mode ----

func (sv *Service) ruleDir(id int64) string { return filepath.Join(sv.Dir, strconv.FormatInt(id, 10)) }

// baseline copies the protected files to <dir>/base (the restore source).
func (sv *Service) baseline(r Rule) (int, error) {
	base := filepath.Join(sv.ruleDir(r.ID), "base")
	os.RemoveAll(base)
	n := 0
	var firstErr error
	r.targets(func(p string, dir bool) {
		rel, _ := filepath.Rel(r.Path, p)
		dst := filepath.Join(base, rel)
		if dir {
			os.MkdirAll(dst, 0o700)
			return
		}
		if err := copyFile(p, dst); err != nil && firstErr == nil {
			firstErr = err
		}
		n++
	})
	return n, firstErr
}

// RefreshBaseline accepts the current files as the new good state.
func (sv *Service) RefreshBaseline(ctx context.Context, id int64) error {
	r, err := sv.Get(ctx, id)
	if err != nil {
		return err
	}
	if r.Mode != "watch" {
		return ErrInvalid
	}
	sv.stop(r)
	return sv.apply(r)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(dst), 0o700)
	tmp := dst + ".lmcwhm-tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	os.Chmod(tmp, fi.Mode().Perm())
	if uid, gid, ok := fileOwner(fi); ok {
		os.Lchown(tmp, uid, gid)
	}
	return os.Rename(tmp, dst)
}

// handle reacts to a change of path in rule r.
func (sv *Service) handle(r Rule, path, op string) {
	rel, err := filepath.Rel(r.Path, path)
	if err != nil || strings.HasPrefix(rel, "..") || r.excluded(filepath.ToSlash(rel)) || !r.protectsFile(filepath.Base(path)) ||
		strings.HasSuffix(path, ".lmcwhm-tmp") {
		return
	}
	if !r.Restore {
		sv.record(r, path, op, "logged")
		return
	}
	base := filepath.Join(sv.ruleDir(r.ID), "base", rel)
	_, baseErr := os.Stat(base)
	cur, curErr := os.Lstat(path)
	action := "restored"
	switch {
	case baseErr == nil:
		if curErr == nil && sameFile(base, path) {
			return // our own restore, or a no-op write
		}
		if err := copyFile(base, path); err != nil {
			action = "failed"
		}
	case curErr == nil && cur.Mode().IsRegular():
		// A new protected file: move it aside.
		blocked := filepath.Join(sv.ruleDir(r.ID), "blocked", time.Now().Format("20060102-150405.000")+"-"+filepath.Base(path))
		os.MkdirAll(filepath.Dir(blocked), 0o700)
		if err := os.Rename(path, blocked); err != nil {
			action = "failed"
		} else {
			action = "removed"
		}
	default:
		return
	}
	sv.record(r, path, op, action)
}

func sameFile(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil || fa.Size() != fb.Size() {
		return false
	}
	x, err1 := os.ReadFile(a)
	y, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}
