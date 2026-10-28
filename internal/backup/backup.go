// Package backup archives websites, databases, Docker volumes, directories and
// the panel's own state, keeps them locally and/or uploads them to Google
// Drive or OneDrive, prunes old copies and restores them (aaPanel 備份).
// Scheduled backups are cron jobs of type "backup" whose command is a Plan.
package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/database"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/site"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

const (
	KindSite     = "site"
	KindDatabase = "database"
	KindPanel    = "panel"
	KindVolume   = "volume"
	KindPath     = "path"

	KindGDrive   = "gdrive"
	KindOneDrive = "onedrive"

	DestLocal = "local"
	All       = "*" // every site / every database
)

var (
	ErrNotFound   = errors.New("backup.not_found")
	ErrBadPlan    = errors.New("backup.invalid_plan")
	ErrBadTarget  = errors.New("backup.invalid_target")
	ErrBadDest    = errors.New("backup.invalid_dest")
	ErrNoRestore  = errors.New("backup.restore_unsupported")
	ErrBadArchive = errors.New("backup.invalid_archive")
	ErrBadName    = errors.New("backup.invalid_name")

	volumeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	unsafeRe = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	// VolumeImage runs tar against Docker volumes.
	VolumeImage = "busybox:1.37"
)

// Plan is what a backup job does; it is the command of a "backup" cron job.
type Plan struct {
	Kind    string   `json:"kind"`
	Target  string   `json:"target"` // domain | database id | volume | absolute dir; "*" = all sites / databases
	Dests   []string `json:"dests"`  // "local" and/or "remote:<id>"
	Keep    int      `json:"keep"`   // newest copies kept per target and destination (0 = all)
	Exclude []string `json:"exclude,omitempty"`
}

// Record is one stored copy of an archive.
type Record struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`    // what was backed up (domain, database name, ...)
	TargetID  int64  `json:"target_id"` // database id
	Name      string `json:"name"`      // file name
	Size      int64  `json:"size"`
	Dest      string `json:"dest"`
	Location  string `json:"-"` // local path or remote file id
	JobID     int64  `json:"job_id"`
	CreatedAt int64  `json:"created_at"`
	DestName  string `json:"dest_name"`
}

type Service struct {
	st      *store.Store
	box     *secrets.Box
	Home    string // panel home: panel backups and the default storage directory
	Dir     string // local storage
	WWWRoot string
	Sites   *site.Service
	DBs     *database.Service
	Apps    *appstore.Service
	now     func() time.Time
}

func New(st *store.Store, box *secrets.Box, home string) *Service {
	return &Service{st: st, box: box, Home: home, Dir: filepath.Join(home, "backup"), WWWRoot: "/www/wwwroot", now: time.Now}
}

// ---- plans ----

// ParsePlan decodes and checks a plan (the cron command of a backup job).
func (sv *Service) ParsePlan(ctx context.Context, cmd string) (Plan, error) {
	var p Plan
	if err := json.Unmarshal([]byte(cmd), &p); err != nil {
		return p, ErrBadPlan
	}
	if p.Keep < 0 || p.Keep > 1000 || len(p.Dests) == 0 || len(p.Exclude) > 100 {
		return p, ErrBadPlan
	}
	seen := map[string]bool{}
	for _, d := range p.Dests {
		if seen[d] {
			return p, ErrBadDest
		}
		seen[d] = true
		if d == DestLocal {
			continue
		}
		id, ok := remoteID(d)
		if !ok {
			return p, ErrBadDest
		}
		if _, err := sv.remoteRow(ctx, id); err != nil {
			return p, ErrBadDest
		}
	}
	for _, e := range p.Exclude {
		if _, err := filepath.Match(e, ""); err != nil || e == "" {
			return p, ErrBadPlan
		}
	}
	switch p.Kind {
	case KindSite:
		if p.Target != All && (sv.Sites == nil || sv.siteByDomain(ctx, p.Target) == nil) {
			return p, ErrBadTarget
		}
	case KindDatabase:
		if p.Target != All {
			id, err := strconv.ParseInt(p.Target, 10, 64)
			if err != nil || sv.DBs == nil {
				return p, ErrBadTarget
			}
			if _, err := sv.DBs.Get(ctx, id); err != nil {
				return p, ErrBadTarget
			}
		}
	case KindPanel, KindSnapshot:
		p.Target = ""
	case KindVolume:
		if !volumeRe.MatchString(p.Target) {
			return p, ErrBadTarget
		}
	case KindPath:
		p.Target = filepath.Clean(p.Target)
		if !filepath.IsAbs(p.Target) || p.Target == "/" || strings.HasPrefix(p.Target, sv.Dir) {
			return p, ErrBadTarget
		}
	default:
		return p, ErrBadPlan
	}
	return p, nil
}

// Validate implements the cron hook.
func (sv *Service) Validate(ctx context.Context, cmd string) error {
	_, err := sv.ParsePlan(ctx, cmd)
	return err
}

func remoteID(dest string) (int64, bool) {
	s, ok := strings.CutPrefix(dest, "remote:")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id > 0
}

func (sv *Service) siteByDomain(ctx context.Context, domain string) *site.Site {
	all, err := sv.Sites.List(ctx)
	if err != nil {
		return nil
	}
	for _, s := range all {
		if s.Domain == domain {
			return &s
		}
	}
	return nil
}

// unit is one archive to produce.
type unit struct {
	kind, target string
	targetID     int64
	ext          string
	write        func(ctx context.Context, w io.Writer) error
}

func (sv *Service) units(ctx context.Context, p Plan) ([]unit, error) {
	var out []unit
	switch p.Kind {
	case KindSite:
		sites, err := sv.Sites.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range sites {
			if p.Target != All && s.Domain != p.Target || s.Type == "proxy" && p.Target == All {
				continue
			}
			root := s.Root
			out = append(out, unit{kind: KindSite, target: s.Domain, ext: ".tar.gz", write: func(ctx context.Context, w io.Writer) error {
				return writeTarGz(ctx, w, []tarSource{{dir: root, prefix: "."}}, p.Exclude)
			}})
		}
	case KindDatabase:
		dbs, err := sv.DBs.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, d := range dbs {
			if p.Target != All && strconv.FormatInt(d.ID, 10) != p.Target {
				continue
			}
			d := d
			eng, err := sv.Apps.Get(ctx, d.AppID)
			if err != nil {
				return nil, err
			}
			out = append(out, unit{kind: KindDatabase, target: d.Name, targetID: d.ID, ext: dumpExt(d.Engine), write: func(ctx context.Context, w io.Writer) error {
				return dumpDB(ctx, eng, d.Name, w)
			}})
		}
	case KindPanel:
		out = append(out, unit{kind: KindPanel, target: "panel", ext: ".tar.gz", write: sv.writePanel})
	case KindSnapshot:
		out = append(out, unit{kind: KindSnapshot, target: "server", ext: ".tar.gz", write: sv.writeSnapshot})
	case KindVolume:
		vol := p.Target
		out = append(out, unit{kind: KindVolume, target: vol, ext: ".tar.gz", write: func(ctx context.Context, w io.Writer) error {
			return volumeTar(ctx, vol, w)
		}})
	case KindPath:
		dir := p.Target
		out = append(out, unit{kind: KindPath, target: dir, ext: ".tar.gz", write: func(ctx context.Context, w io.Writer) error {
			return writeTarGz(ctx, w, []tarSource{{dir: dir, prefix: "."}}, p.Exclude)
		}})
	}
	return out, nil
}

// Run executes a plan, writing progress to out. jobID is 0 for manual runs.
func (sv *Service) Run(ctx context.Context, p Plan, jobID int64, out io.Writer) error {
	units, err := sv.units(ctx, p)
	if err != nil {
		return err
	}
	if len(units) == 0 {
		fmt.Fprintln(out, "nothing to back up")
		return nil
	}
	var failed []string
	for _, u := range units {
		if err := sv.runUnit(ctx, p, u, jobID, out); err != nil {
			fmt.Fprintf(out, "%s %s: FAILED: %v\n", u.kind, u.target, err)
			failed = append(failed, u.target)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d backups failed: %s", len(failed), len(units), strings.Join(failed, ", "))
	}
	return nil
}

func (sv *Service) runUnit(ctx context.Context, p Plan, u unit, jobID int64, out io.Writer) error {
	start := sv.now()
	tmpDir := filepath.Join(sv.Dir, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%s_%s_%s%s", u.kind, safeName(u.target), start.Format("20060102_150405"), u.ext)
	f, err := os.CreateTemp(tmpDir, "part-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	fmt.Fprintf(out, "%s %s: archiving\n", u.kind, u.target)
	err = u.write(ctx, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	fi, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s: %s (%s) in %s\n", u.kind, u.target, name, human(fi.Size()), time.Since(start).Round(time.Second))
	var errs []string
	for _, dest := range p.Dests {
		loc, err := sv.store(ctx, dest, u, name, tmp, fi.Size())
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", sv.destName(ctx, dest), err))
			continue
		}
		sv.st.DB.ExecContext(ctx, `INSERT INTO backups(kind, target, target_id, name, size, dest, location, job_id, created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			u.kind, u.target, u.targetID, name, fi.Size(), dest, loc, jobID, start.Unix())
		fmt.Fprintf(out, "  → %s ok\n", sv.destName(ctx, dest))
		if p.Keep > 0 {
			for _, pruned := range sv.prune(ctx, u.kind, u.target, dest, p.Keep) {
				fmt.Fprintf(out, "  pruned %s\n", pruned)
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (sv *Service) store(ctx context.Context, dest string, u unit, name, tmp string, size int64) (string, error) {
	if dest == DestLocal {
		dir := filepath.Join(sv.Dir, u.kind, safeName(u.target))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		dst := filepath.Join(dir, name)
		return dst, copyFile(tmp, dst)
	}
	id, _ := remoteID(dest)
	rm, err := sv.remote(ctx, id)
	if err != nil {
		return "", err
	}
	f, err := os.Open(tmp)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return rm.Upload(ctx, name, f, size)
}

// prune deletes all but the newest keep copies of kind/target at dest.
func (sv *Service) prune(ctx context.Context, kind, target, dest string, keep int) []string {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT `+recCols+` FROM backups WHERE kind = ? AND target = ? AND dest = ? ORDER BY created_at DESC, id DESC LIMIT -1 OFFSET ?`,
		kind, target, dest, keep)
	if err != nil {
		return nil
	}
	old, _ := scanRecords(rows)
	var out []string
	for _, r := range old {
		if err := sv.deleteRecord(ctx, r); err == nil {
			out = append(out, r.Name)
		}
	}
	return out
}

// ---- records ----

const recCols = `id, kind, target, target_id, name, size, dest, location, job_id, created_at`

// LocalPath is where a local backup is stored (ok=false for remote copies).
func (sv *Service) LocalPath(r Record) (string, bool) {
	if r.Dest != DestLocal || r.Location == "" {
		return "", false
	}
	return r.Location, true
}

func scanRecords(rows *sql.Rows) ([]Record, error) {
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.Kind, &r.Target, &r.TargetID, &r.Name, &r.Size, &r.Dest, &r.Location, &r.JobID, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// List returns stored backups, newest first, optionally of one kind.
func (sv *Service) List(ctx context.Context, kind string) ([]Record, error) {
	q, args := `SELECT `+recCols+` FROM backups`, []any{}
	if kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, kind)
	}
	rows, err := sv.st.DB.QueryContext(ctx, q+` ORDER BY created_at DESC, id DESC LIMIT 2000`, args...)
	if err != nil {
		return nil, err
	}
	recs, err := scanRecords(rows)
	names := map[string]string{}
	for i := range recs {
		d := recs[i].Dest
		if _, ok := names[d]; !ok {
			names[d] = sv.destName(ctx, d)
		}
		recs[i].DestName = names[d]
	}
	return recs, err
}

func (sv *Service) Get(ctx context.Context, id int64) (Record, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT `+recCols+` FROM backups WHERE id = ?`, id)
	if err != nil {
		return Record{}, err
	}
	recs, err := scanRecords(rows)
	if err != nil {
		return Record{}, err
	}
	if len(recs) == 0 {
		return Record{}, ErrNotFound
	}
	return recs[0], nil
}

// Delete removes a stored copy (local file or remote object) and its record.
func (sv *Service) Delete(ctx context.Context, id int64) error {
	r, err := sv.Get(ctx, id)
	if err != nil {
		return err
	}
	return sv.deleteRecord(ctx, r)
}

func (sv *Service) deleteRecord(ctx context.Context, r Record) error {
	if r.Dest == DestLocal {
		if err := os.Remove(r.Location); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		os.Remove(filepath.Dir(r.Location)) // only if empty
	} else if id, ok := remoteID(r.Dest); ok {
		rm, err := sv.remote(ctx, id)
		if err == nil {
			err = rm.Delete(ctx, r.Location)
		}
		// A remote that is gone (or a file deleted there) must not block cleanup.
		if err != nil && !errors.Is(err, ErrRemoteMissing) && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	_, err := sv.st.DB.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, r.ID)
	return err
}

// Open streams a stored copy (downloading remote ones).
func (sv *Service) Open(ctx context.Context, r Record) (io.ReadCloser, error) {
	if r.Dest == DestLocal {
		f, err := os.Open(r.Location)
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return f, err
	}
	id, _ := remoteID(r.Dest)
	rm, err := sv.remote(ctx, id)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(rm.Download(ctx, r.Location, pw)) }()
	return pr, nil
}

// Import stores an uploaded archive as a local backup, e.g. one moved from
// another server, so it can be restored.
func (sv *Service) Import(ctx context.Context, kind, target string, targetID int64, name string, r io.Reader) (Record, error) {
	name = filepath.Base(name)
	if !strings.HasSuffix(name, ".gz") || strings.HasPrefix(name, ".") || len(name) > 200 {
		return Record{}, ErrBadName
	}
	switch kind {
	case KindSite, KindDatabase, KindVolume, KindPath, KindPanel:
	default:
		return Record{}, ErrBadPlan
	}
	dir := filepath.Join(sv.Dir, kind, safeName(target))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Record{}, err
	}
	dst := filepath.Join(dir, name)
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Record{}, ErrBadName
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return Record{}, err
	}
	res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO backups(kind, target, target_id, name, size, dest, location, job_id, created_at) VALUES(?,?,?,?,?,?,?,0,?)`,
		kind, target, targetID, name, n, DestLocal, dst, sv.now().Unix())
	if err != nil {
		return Record{}, err
	}
	id, _ := res.LastInsertId()
	return sv.Get(ctx, id)
}

// ---- helpers ----

func safeName(s string) string {
	s = strings.Trim(unsafeRe.ReplaceAllString(strings.TrimPrefix(s, "/"), "-"), "-.")
	if s == "" {
		return "root"
	}
	return s
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst + ".part")
		return err
	}
	return os.Rename(dst+".part", dst)
}

// RunPlan implements the cron hook.
func (sv *Service) RunPlan(ctx context.Context, cmd string, jobID int64, out io.Writer) error {
	p, err := sv.ParsePlan(ctx, cmd)
	if err != nil {
		return err
	}
	return sv.Run(ctx, p, jobID, out)
}
