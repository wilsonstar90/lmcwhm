// Package ftp manages file-transfer accounts bound to website directories:
// FTP virtual users served by a Pure-FTPd container (lmcwhm-ftp), and SFTP
// users — system accounts in group lmcwhm-sftp, chrooted by sshd to
// /www/sftp/<user> with the site directory bind-mounted at /site.
package ftp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

const (
	KindFTP  = "ftp"
	KindSFTP = "sftp"

	Container   = "lmcwhm-ftp"
	Image       = "stilliard/pure-ftpd:latest"
	PassiveFrom = 39000
	PassiveTo   = 39100
	SFTPGroup   = "lmcwhm-sftp"
	settingsKey = "ftp_enabled"
	markBegin   = "# BEGIN lmcwhm-sftp (managed by lmcwhm)"
	markEnd     = "# END lmcwhm-sftp"
)

var (
	ErrNotFound = errors.New("ftp.not_found")
	ErrName     = errors.New("ftp.invalid_username")
	ErrExists   = errors.New("ftp.exists")
	ErrPassword = errors.New("ftp.invalid_password")
	ErrDir      = errors.New("ftp.invalid_dir")
	ErrKind     = errors.New("ftp.invalid_kind")
	ErrNotOn    = errors.New("ftp.not_enabled")
	nameRe      = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,31}$`)
	passRe      = regexp.MustCompile(`^[\x21-\x7e]{8,64}$`) // printable ASCII, no spaces
)

type Account struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Username  string `json:"username"`
	Dir       string `json:"dir"`
	SiteID    int64  `json:"site_id"`
	Enabled   bool   `json:"enabled"`
	Remark    string `json:"remark"`
	CreatedAt int64  `json:"created_at"`
	password  string
}

type Service struct {
	st       *store.Store
	box      *secrets.Box
	WWWRoot  string
	DataDir  string // /opt/lmcwhm/data: Pure-FTPd's passwd file lives in DataDir/ftp
	SFTPRoot string // /www/sftp
	SSHD     string // /etc/ssh/sshd_config
	PublicIP func(ctx context.Context) string
	// run executes host commands (replaced in tests).
	run func(ctx context.Context, stdin string, name string, args ...string) (string, error)
	mu  sync.Mutex
}

func New(st *store.Store, box *secrets.Box, home, wwwRoot string) *Service {
	return &Service{st: st, box: box, WWWRoot: wwwRoot, DataDir: filepath.Join(home, "data"), SFTPRoot: "/www/sftp", SSHD: "/etc/ssh/sshd_config",
		run: func(ctx context.Context, stdin, name string, args ...string) (string, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			if stdin != "" {
				cmd.Stdin = strings.NewReader(stdin)
			}
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			if err != nil {
				return out.String(), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args[:min(len(args), 2)], " "), err, strings.TrimSpace(out.String()))
			}
			return out.String(), nil
		}}
}

// ---- persistence ----

func (sv *Service) query(ctx context.Context, where string, args ...any) ([]Account, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, kind, username, dir, site_id, password, enabled, remark, created_at FROM ftp_accounts `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var a Account
		var sealed string
		if err := rows.Scan(&a.ID, &a.Kind, &a.Username, &a.Dir, &a.SiteID, &sealed, &a.Enabled, &a.Remark, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.password, _ = sv.box.Open(sealed)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (sv *Service) List(ctx context.Context) ([]Account, error) { return sv.query(ctx, "") }

func (sv *Service) get(ctx context.Context, id int64) (Account, error) {
	list, err := sv.query(ctx, "WHERE id = ?", id)
	if err != nil {
		return Account{}, err
	}
	if len(list) == 0 {
		return Account{}, ErrNotFound
	}
	return list[0], nil
}

// Password returns an account's password (shown on request in the panel).
func (sv *Service) Password(ctx context.Context, id int64) (string, error) {
	a, err := sv.get(ctx, id)
	return a.password, err
}

type Input struct {
	Kind     string `json:"kind"`
	Username string `json:"username"`
	Password string `json:"password"`
	Dir      string `json:"dir"` // a directory under the web root
	SiteID   int64  `json:"site_id"`
	Remark   string `json:"remark"`
}

// cleanDir confines account directories to the web root.
func (sv *Service) cleanDir(dir string) (string, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	rel, err := filepath.Rel(sv.WWWRoot, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || !filepath.IsAbs(dir) {
		return "", ErrDir
	}
	base, _ := filepath.EvalSymlinks(sv.WWWRoot)
	if real, err := filepath.EvalSymlinks(dir); err != nil || !strings.HasPrefix(real, base+"/") {
		return "", ErrDir
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", ErrDir
	}
	return dir, nil
}

func (sv *Service) Create(ctx context.Context, in Input) (Account, error) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	in.Username = strings.TrimSpace(in.Username)
	if in.Kind != KindFTP && in.Kind != KindSFTP {
		return Account{}, ErrKind
	}
	if !nameRe.MatchString(in.Username) {
		return Account{}, ErrName
	}
	if !passRe.MatchString(in.Password) {
		return Account{}, ErrPassword
	}
	dir, err := sv.cleanDir(in.Dir)
	if err != nil {
		return Account{}, err
	}
	if in.Kind == KindSFTP {
		// The name becomes a system account: it must not exist already.
		if _, err := user.Lookup(in.Username); err == nil {
			return Account{}, ErrExists
		}
	}
	res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO ftp_accounts(kind, username, dir, site_id, password, enabled, remark, created_at) VALUES(?,?,?,?,?,1,?,?)`,
		in.Kind, in.Username, dir, in.SiteID, sv.box.Seal(in.Password), in.Remark, time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Account{}, ErrExists
		}
		return Account{}, err
	}
	id, _ := res.LastInsertId()
	a, err := sv.get(ctx, id)
	if err != nil {
		return a, err
	}
	if err := sv.apply(ctx, a); err != nil {
		sv.st.DB.ExecContext(ctx, `DELETE FROM ftp_accounts WHERE id = ?`, id)
		if a.Kind == KindSFTP {
			sv.removeSFTP(ctx, a)
		}
		return Account{}, err
	}
	return a, nil
}

type Update struct {
	Password *string `json:"password"`
	Enabled  *bool   `json:"enabled"`
	Dir      *string `json:"dir"`
	Remark   *string `json:"remark"`
}

func (sv *Service) Update(ctx context.Context, id int64, in Update) (Account, error) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	a, err := sv.get(ctx, id)
	if err != nil {
		return a, err
	}
	old := a
	if in.Password != nil {
		if !passRe.MatchString(*in.Password) {
			return a, ErrPassword
		}
		a.password = *in.Password
	}
	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}
	if in.Dir != nil {
		if a.Dir, err = sv.cleanDir(*in.Dir); err != nil {
			return a, err
		}
	}
	if in.Remark != nil {
		a.Remark = *in.Remark
	}
	if _, err := sv.st.DB.ExecContext(ctx, `UPDATE ftp_accounts SET password=?, enabled=?, dir=?, remark=? WHERE id=?`,
		sv.box.Seal(a.password), a.Enabled, a.Dir, a.Remark, id); err != nil {
		return a, err
	}
	if a.Kind == KindSFTP && old.Dir != a.Dir {
		sv.unmount(ctx, old)
	}
	return a, sv.apply(ctx, a)
}

func (sv *Service) Delete(ctx context.Context, id int64) error {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	a, err := sv.get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := sv.st.DB.ExecContext(ctx, `DELETE FROM ftp_accounts WHERE id = ?`, id); err != nil {
		return err
	}
	if a.Kind == KindSFTP {
		return sv.removeSFTP(ctx, a)
	}
	return sv.syncFTP(ctx)
}

// apply makes the system match one account.
func (sv *Service) apply(ctx context.Context, a Account) error {
	if a.Kind == KindSFTP {
		return sv.applySFTP(ctx, a)
	}
	return sv.syncFTP(ctx)
}

// ---- FTP (Pure-FTPd) ----

func (sv *Service) Enabled(ctx context.Context) bool {
	var v string
	sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&v)
	return v == "1"
}

type Status struct {
	Enabled   bool   `json:"enabled"`
	State     string `json:"state"`
	PublicIP  string `json:"public_ip"`
	Ports     string `json:"ports"`
	SFTPReady bool   `json:"sftp_ready"`
}

func (sv *Service) Status(ctx context.Context) Status {
	st := Status{Enabled: sv.Enabled(ctx), State: dockerx.State(ctx, Container), Ports: fmt.Sprintf("21, %d-%d", PassiveFrom, PassiveTo)}
	if sv.PublicIP != nil {
		st.PublicIP = sv.PublicIP(ctx)
	}
	if b, err := os.ReadFile(sv.SSHD); err == nil {
		st.SFTPReady = strings.Contains(string(b), markBegin)
	}
	return st
}

func (sv *Service) passwdDir() string { return filepath.Join(sv.DataDir, "ftp") }

// SetEnabled starts (or removes) the Pure-FTPd container.
func (sv *Service) SetEnabled(ctx context.Context, on bool, log func(string, ...any)) error {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	v := "0"
	if on {
		v = "1"
	}
	if _, err := sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingsKey, v); err != nil {
		return err
	}
	if !on {
		log("removing container %s", Container)
		return dockerx.Remove(ctx, Container)
	}
	return sv.ensure(ctx, log)
}

func (sv *Service) ensure(ctx context.Context, log func(string, ...any)) error {
	if err := os.MkdirAll(sv.passwdDir(), 0o700); err != nil {
		return err
	}
	ip := ""
	if sv.PublicIP != nil {
		ip = sv.PublicIP(ctx)
	}
	if ip == "" {
		ip = "127.0.0.1"
	}
	// -E no anonymous, -j create home, -R no chmod by clients, -P passive address.
	args := []string{"--network", "host",
		"-v", sv.WWWRoot + ":" + sv.WWWRoot,
		"-v", sv.passwdDir() + ":/etc/pure-ftpd/passwd",
		Image, "/run.sh", "-l", "puredb:/etc/pure-ftpd/pureftpd.pdb", "-E", "-j", "-R", "-P", ip,
		"-p", fmt.Sprintf("%d:%d", PassiveFrom, PassiveTo), "-c", "50", "-C", "10", "-A"}
	if err := webenv.EnsureContainer(ctx, log, Container, args, []int{21}, true); err != nil {
		return err
	}
	return sv.syncFTP(ctx)
}

// syncFTP rewrites Pure-FTPd's user database from the panel's accounts.
func (sv *Service) syncFTP(ctx context.Context) error {
	if !sv.Enabled(ctx) || dockerx.State(ctx, Container) != "running" {
		return nil // applied when FTP is turned on
	}
	list, err := sv.query(ctx, "WHERE kind = ? AND enabled = 1", KindFTP)
	if err != nil {
		return err
	}
	const pw = "/etc/pure-ftpd/passwd/pureftpd.passwd"
	if _, err := sv.run(ctx, "", "docker", "exec", Container, "sh", "-c", ": > "+pw); err != nil {
		return err
	}
	for _, a := range list {
		// Files are written as www-data (33), like PHP-FPM.
		if _, err := sv.run(ctx, a.password+"\n"+a.password+"\n", "docker", "exec", "-i", Container,
			"pure-pw", "useradd", a.Username, "-f", pw, "-u", "33", "-g", "33", "-d", a.Dir); err != nil {
			return err
		}
	}
	_, err = sv.run(ctx, "", "docker", "exec", Container, "pure-pw", "mkdb", "/etc/pure-ftpd/pureftpd.pdb", "-f", pw)
	return err
}

// ---- SFTP ----

func (sv *Service) chroot(name string) string { return filepath.Join(sv.SFTPRoot, name) }

func (sv *Service) sshdBlock() string {
	return markBegin + "\nMatch Group " + SFTPGroup + "\n    ChrootDirectory " + sv.SFTPRoot + "/%u\n    ForceCommand internal-sftp -d /site\n" +
		"    PasswordAuthentication yes\n    AllowTcpForwarding no\n    X11Forwarding no\n    PermitTunnel no\n    AllowAgentForwarding no\n" + markEnd + "\n"
}

// ensureSSHD appends the managed Match block (sshd needs Match blocks last)
// and reloads sshd; an invalid config is rolled back.
func (sv *Service) ensureSSHD(ctx context.Context) error {
	b, err := os.ReadFile(sv.SSHD)
	if err != nil {
		return err
	}
	if strings.Contains(string(b), markBegin) {
		return nil
	}
	next := strings.TrimRight(string(b), "\n") + "\n\n" + sv.sshdBlock()
	if err := os.WriteFile(sv.SSHD, []byte(next), 0o644); err != nil {
		return err
	}
	if _, err := sv.run(ctx, "", "sshd", "-t"); err != nil {
		os.WriteFile(sv.SSHD, b, 0o644)
		return err
	}
	if _, err := sv.run(ctx, "", "systemctl", "reload", "ssh"); err != nil {
		if _, err2 := sv.run(ctx, "", "systemctl", "reload", "sshd"); err2 != nil {
			return err
		}
	}
	return nil
}

func (sv *Service) mounted(ctx context.Context, p string) bool {
	_, err := sv.run(ctx, "", "mountpoint", "-q", p)
	return err == nil
}

func (sv *Service) unmount(ctx context.Context, a Account) {
	site := filepath.Join(sv.chroot(a.Username), "site")
	if sv.mounted(ctx, site) {
		sv.run(ctx, "", "umount", "-l", site)
	}
}

func (sv *Service) applySFTP(ctx context.Context, a Account) error {
	if err := sv.ensureSSHD(ctx); err != nil {
		return err
	}
	if _, err := sv.run(ctx, "", "getent", "group", SFTPGroup); err != nil {
		if _, err := sv.run(ctx, "", "groupadd", "--system", SFTPGroup); err != nil {
			return err
		}
	}
	root := sv.chroot(a.Username)
	site := filepath.Join(root, "site")
	// sshd requires every chroot path component to be root-owned and not group/other writable.
	for _, d := range []string{sv.SFTPRoot, root} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		os.Chown(d, 0, 0)
		os.Chmod(d, 0o755)
	}
	if err := os.MkdirAll(site, 0o755); err != nil {
		return err
	}
	if _, err := user.Lookup(a.Username); err != nil {
		// Same uid as www-data, so uploads belong to the web server user.
		if _, err := sv.run(ctx, "", "useradd", "--no-create-home", "--non-unique", "--uid", "33", "--gid", SFTPGroup,
			"--home-dir", "/site", "--shell", "/usr/sbin/nologin", "--comment", "lmcwhm sftp", a.Username); err != nil {
			return err
		}
	}
	if _, err := sv.run(ctx, a.Username+":"+a.password+"\n", "chpasswd"); err != nil {
		return err
	}
	lock := "--unlock"
	if !a.Enabled {
		lock = "--lock"
	}
	sv.run(ctx, "", "usermod", lock, a.Username)
	if !sv.mounted(ctx, site) {
		if _, err := sv.run(ctx, "", "mount", "--bind", a.Dir, site); err != nil {
			return err
		}
	}
	return nil
}

func (sv *Service) removeSFTP(ctx context.Context, a Account) error {
	sv.unmount(ctx, a)
	if u, err := user.Lookup(a.Username); err == nil && strings.Contains(sv.gecos(ctx, a.Username), "lmcwhm sftp") && u.Uid == "33" {
		// --force: the uid is shared with www-data, whose PHP-FPM processes
		// would otherwise make userdel refuse ("currently used by process").
		if _, err := sv.run(ctx, "", "userdel", "--force", a.Username); err != nil {
			return err
		}
	}
	root := sv.chroot(a.Username)
	os.Remove(filepath.Join(root, "site")) // only if empty (unmounted)
	os.Remove(root)
	return nil
}

func (sv *Service) gecos(ctx context.Context, name string) string {
	out, _ := sv.run(ctx, "", "getent", "passwd", name)
	return out
}

// Boot restores bind mounts (lost on reboot) and the FTP container.
func (sv *Service) Boot(ctx context.Context, log func(string, ...any)) {
	list, err := sv.query(ctx, "WHERE kind = ?", KindSFTP)
	if err == nil {
		for _, a := range list {
			site := filepath.Join(sv.chroot(a.Username), "site")
			if _, err := os.Stat(site); err == nil && !sv.mounted(ctx, site) {
				sv.run(ctx, "", "mount", "--bind", a.Dir, site)
			}
		}
	}
	if sv.Enabled(ctx) {
		if err := sv.ensure(ctx, log); err != nil {
			log("ftp: %v", err)
		}
	}
}
