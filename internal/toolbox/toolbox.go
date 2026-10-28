// Package toolbox holds the host utilities of the 工具箱 page (1Panel
// Toolbox): swap, hostname, DNS, time, SSH, processes, network connections,
// disk usage and junk cleanup.
package toolbox

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

var (
	ErrInvalid  = errors.New("toolbox.invalid")
	ErrHostname = errors.New("toolbox.invalid_hostname")
	ErrTimezone = errors.New("toolbox.invalid_timezone")
	ErrDNS      = errors.New("toolbox.invalid_dns")
	ErrPort     = errors.New("toolbox.invalid_port")
	ErrKey      = errors.New("toolbox.invalid_key")
	ErrLockout  = errors.New("toolbox.ssh_lockout")
	ErrPID      = errors.New("toolbox.protected_process")
)

// Service runs host commands; Root prefixes every host path (tests).
type Service struct {
	Root string
	// SSHChanged runs after the SSH port changed, before sshd restarts
	// (the firewall must allow the new port first).
	SSHChanged func(ctx context.Context) error
	// Run executes a host command (replaced in tests).
	Run func(ctx context.Context, name string, args ...string) (string, error)
}

func New() *Service { return &Service{Run: run} }

func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return out.String(), fmt.Errorf("%s: %v: %s", name, err, msg)
	}
	return out.String(), nil
}

func (sv *Service) path(p string) string { return filepath.Join(sv.Root, p) }

// ---- overview: swap, hostname, DNS, time ----

type System struct {
	Hostname   string   `json:"hostname"`
	SwapTotal  uint64   `json:"swap_total"`
	SwapUsed   uint64   `json:"swap_used"`
	SwapFile   uint64   `json:"swap_file"` // size of the managed /swapfile (0 = none)
	MemTotal   uint64   `json:"mem_total"`
	DNS        []string `json:"dns"`
	Timezone   string   `json:"timezone"`
	NTP        bool     `json:"ntp"`
	NTPSynced  bool     `json:"ntp_synced"`
	Time       int64    `json:"time"`
	Timezones  []string `json:"timezones,omitempty"`
	Resolved   bool     `json:"resolved"` // DNS is managed by systemd-resolved
	SwapMounts []string `json:"swap_mounts"`
}

const swapFile = "/swapfile"

func (sv *Service) System(ctx context.Context, withZones bool) System {
	s := System{Time: time.Now().Unix(), DNS: []string{}, SwapMounts: []string{}}
	s.Hostname, _ = os.Hostname()
	if m, err := mem.SwapMemoryWithContext(ctx); err == nil {
		s.SwapTotal, s.SwapUsed = m.Total, m.Used
	}
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		s.MemTotal = m.Total
	}
	if fi, err := os.Stat(sv.path(swapFile)); err == nil {
		s.SwapFile = uint64(fi.Size())
	}
	if b, err := os.ReadFile(sv.path("/proc/swaps")); err == nil {
		for _, l := range strings.Split(string(b), "\n")[1:] {
			if f := strings.Fields(l); len(f) > 0 {
				s.SwapMounts = append(s.SwapMounts, f[0])
			}
		}
	}
	if out, err := sv.Run(ctx, "timedatectl", "show"); err == nil {
		kv := parseKV(out)
		s.Timezone, s.NTP, s.NTPSynced = kv["Timezone"], kv["NTP"] == "yes", kv["NTPSynchronized"] == "yes"
	}
	if s.Timezone == "" {
		if l, err := os.Readlink(sv.path("/etc/localtime")); err == nil {
			s.Timezone = strings.TrimPrefix(l, "/usr/share/zoneinfo/")
		}
	}
	s.Resolved = sv.resolvedActive(ctx)
	s.DNS = sv.dnsServers()
	if withZones {
		if out, err := sv.Run(ctx, "timedatectl", "list-timezones"); err == nil {
			s.Timezones = strings.Fields(out)
		}
	}
	return s
}

func parseKV(out string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// SetSwap resizes the managed swap file; 0 removes it.
func (sv *Service) SetSwap(ctx context.Context, sizeMB int) error {
	if sizeMB < 0 || sizeMB > 64*1024 {
		return ErrInvalid
	}
	f := sv.path(swapFile)
	if _, err := os.Stat(f); err == nil {
		sv.Run(ctx, "swapoff", f)
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	if err := sv.fstabSwap(sizeMB > 0); err != nil {
		return err
	}
	if sizeMB == 0 {
		return nil
	}
	if st, err := disk.UsageWithContext(ctx, filepath.Dir(f)); err == nil && st.Free < uint64(sizeMB+1024)<<20 {
		return fmt.Errorf("%w: not enough free disk space", ErrInvalid)
	}
	if _, err := sv.Run(ctx, "fallocate", "-l", fmt.Sprintf("%dM", sizeMB), f); err != nil {
		// Some filesystems do not support fallocate for swap.
		if _, err := sv.Run(ctx, "dd", "if=/dev/zero", "of="+f, "bs=1M", fmt.Sprintf("count=%d", sizeMB)); err != nil {
			return err
		}
	}
	os.Chmod(f, 0o600)
	if _, err := sv.Run(ctx, "mkswap", f); err != nil {
		return err
	}
	_, err := sv.Run(ctx, "swapon", f)
	return err
}

func (sv *Service) fstabSwap(on bool) error {
	p := sv.path("/etc/fstab")
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == swapFile {
			continue
		}
		lines = append(lines, l)
	}
	if on {
		lines = append(lines, swapFile+" none swap sw 0 0")
	}
	return os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

var hostRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// SetHostname changes the hostname and its /etc/hosts entry.
func (sv *Service) SetHostname(ctx context.Context, name string) error {
	if len(name) > 253 || !hostRe.MatchString(name) {
		return ErrHostname
	}
	old, _ := os.Hostname()
	if _, err := sv.Run(ctx, "hostnamectl", "set-hostname", name); err != nil {
		return err
	}
	p := sv.path("/etc/hosts")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	short, _, _ := strings.Cut(name, ".")
	entry := "127.0.1.1 " + name
	if short != name {
		entry += " " + short
	}
	var lines []string
	found := false
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		f := strings.Fields(l)
		if len(f) > 1 && f[0] == "127.0.1.1" {
			if !found {
				lines = append(lines, entry)
				found = true
			}
			continue
		}
		if len(f) > 1 && old != "" && f[0] != "127.0.0.1" && f[0] != "::1" && f[1] == old {
			l = strings.Replace(l, old, name, 1)
		}
		lines = append(lines, l)
	}
	if !found {
		lines = append(lines, entry)
	}
	return os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func (sv *Service) resolvedActive(ctx context.Context) bool {
	_, err := sv.Run(ctx, "systemctl", "is-active", "--quiet", "systemd-resolved")
	return err == nil
}

const resolvedDropIn = "/etc/systemd/resolved.conf.d/lmcwhm.conf"

func (sv *Service) dnsServers() []string {
	out := []string{}
	if b, err := os.ReadFile(sv.path(resolvedDropIn)); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "DNS="); ok {
				return strings.Fields(v)
			}
		}
	}
	// The upstream servers (not the 127.0.0.53 stub).
	for _, f := range []string{"/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"} {
		b, err := os.ReadFile(sv.path(f))
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if fl := strings.Fields(l); len(fl) == 2 && fl[0] == "nameserver" {
				out = append(out, fl[1])
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return out
}

// SetDNS sets the upstream DNS servers (empty = back to DHCP / defaults).
func (sv *Service) SetDNS(ctx context.Context, servers []string) error {
	var clean []string
	for _, s := range servers {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			return ErrDNS
		}
		clean = append(clean, a.String())
	}
	if len(clean) > 6 {
		return ErrDNS
	}
	if sv.resolvedActive(ctx) {
		p := sv.path(resolvedDropIn)
		if len(clean) == 0 {
			os.Remove(p)
		} else {
			os.MkdirAll(filepath.Dir(p), 0o755)
			body := "# Managed by lmcwhm (Toolbox → DNS)\n[Resolve]\nDNS=" + strings.Join(clean, " ") + "\n"
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				return err
			}
		}
		_, err := sv.Run(ctx, "systemctl", "restart", "systemd-resolved")
		return err
	}
	if len(clean) == 0 {
		return ErrDNS
	}
	var b strings.Builder
	b.WriteString("# Managed by lmcwhm (Toolbox → DNS)\n")
	for _, s := range clean {
		b.WriteString("nameserver " + s + "\n")
	}
	p := sv.path("/etc/resolv.conf")
	os.Remove(p) // may be a symlink
	return os.WriteFile(p, []byte(b.String()), 0o644)
}

var tzRe = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+){0,2}$`)

func (sv *Service) SetTime(ctx context.Context, tz string, ntp *bool) error {
	if tz != "" {
		if !tzRe.MatchString(tz) {
			return ErrTimezone
		}
		if _, err := os.Stat(sv.path(filepath.Join("/usr/share/zoneinfo", tz))); err != nil {
			return ErrTimezone
		}
		if _, err := sv.Run(ctx, "timedatectl", "set-timezone", tz); err != nil {
			return err
		}
	}
	if ntp != nil {
		if _, err := sv.Run(ctx, "timedatectl", "set-ntp", strconv.FormatBool(*ntp)); err != nil {
			return err
		}
	}
	return nil
}

// SyncTime restarts the NTP client, which steps the clock.
func (sv *Service) SyncTime(ctx context.Context) error {
	_, err := sv.Run(ctx, "systemctl", "restart", "systemd-timesyncd")
	return err
}

// ---- SSH ----

type SSH struct {
	Port           int    `json:"port"`
	PermitRoot     string `json:"permit_root_login"` // yes | no | prohibit-password
	PasswordAuth   bool   `json:"password_auth"`
	PubkeyAuth     bool   `json:"pubkey_auth"`
	ClientAlive    int    `json:"client_alive_interval"`
	MaxAuthTries   int    `json:"max_auth_tries"`
	Running        bool   `json:"running"`
	AuthorizedKeys []Key  `json:"authorized_keys"`
}

type Key struct {
	Type        string `json:"type"`
	Comment     string `json:"comment"`
	Fingerprint string `json:"fingerprint"`
	Line        int    `json:"line"`
}

const sshDropIn = "/etc/ssh/sshd_config.d/00-lmcwhm.conf"

func (sv *Service) GetSSH(ctx context.Context) SSH {
	s := SSH{Port: 22, PermitRoot: "prohibit-password", PasswordAuth: true, PubkeyAuth: true, AuthorizedKeys: []Key{}}
	if out, err := sv.Run(ctx, "sshd", "-T"); err == nil {
		for _, l := range strings.Split(out, "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(l), " ")
			switch k {
			case "port":
				s.Port, _ = strconv.Atoi(v)
			case "permitrootlogin":
				s.PermitRoot = v
				if v == "without-password" {
					s.PermitRoot = "prohibit-password"
				}
			case "passwordauthentication":
				s.PasswordAuth = v == "yes"
			case "pubkeyauthentication":
				s.PubkeyAuth = v == "yes"
			case "clientaliveinterval":
				s.ClientAlive, _ = strconv.Atoi(v)
			case "maxauthtries":
				s.MaxAuthTries, _ = strconv.Atoi(v)
			}
		}
	}
	_, err := sv.Run(ctx, "systemctl", "is-active", "--quiet", "ssh")
	if err != nil {
		_, err = sv.Run(ctx, "systemctl", "is-active", "--quiet", "ssh.socket")
	}
	s.Running = err == nil
	s.AuthorizedKeys = sv.keys(ctx)
	return s
}

// SetSSH writes the managed drop-in (first value wins in sshd, and 00- sorts
// first), tests it with sshd -t, then restarts ssh. Turning off both password
// and key login, or key login without any authorized key, is refused.
func (sv *Service) SetSSH(ctx context.Context, in SSH) error {
	if in.Port < 1 || in.Port > 65535 {
		return ErrPort
	}
	switch in.PermitRoot {
	case "yes", "no", "prohibit-password":
	default:
		return ErrInvalid
	}
	if !in.PasswordAuth && (!in.PubkeyAuth || len(sv.keys(ctx)) == 0) {
		return ErrLockout
	}
	if in.ClientAlive < 0 || in.ClientAlive > 3600 || in.MaxAuthTries < 0 || in.MaxAuthTries > 100 {
		return ErrInvalid
	}
	old := sv.GetSSH(ctx)
	if err := sv.ensureInclude(); err != nil {
		return err
	}
	p := sv.path(sshDropIn)
	prev, prevErr := os.ReadFile(p)
	yn := map[bool]string{true: "yes", false: "no"}
	var b strings.Builder
	b.WriteString("# Managed by lmcwhm (Toolbox → SSH). The first value of a setting wins.\n")
	fmt.Fprintf(&b, "Port %d\nPermitRootLogin %s\nPasswordAuthentication %s\nKbdInteractiveAuthentication %s\nPubkeyAuthentication %s\n",
		in.Port, in.PermitRoot, yn[in.PasswordAuth], yn[in.PasswordAuth], yn[in.PubkeyAuth])
	if in.ClientAlive > 0 {
		fmt.Fprintf(&b, "ClientAliveInterval %d\nClientAliveCountMax 3\n", in.ClientAlive)
	}
	if in.MaxAuthTries > 0 {
		fmt.Fprintf(&b, "MaxAuthTries %d\n", in.MaxAuthTries)
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		return err
	}
	restore := func() {
		if prevErr == nil {
			os.WriteFile(p, prev, 0o644)
		} else {
			os.Remove(p)
		}
	}
	if _, err := sv.Run(ctx, "sshd", "-t"); err != nil {
		restore()
		return err
	}
	if in.Port != old.Port && sv.SSHChanged != nil {
		if err := sv.SSHChanged(ctx); err != nil {
			restore()
			return err
		}
	}
	return sv.restartSSH(ctx)
}

// ensureInclude makes sshd read sshd_config.d (Ubuntu 20.04 lacks the Include).
func (sv *Service) ensureInclude() error {
	p := sv.path("/etc/ssh/sshd_config")
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && strings.EqualFold(f[0], "Include") && strings.Contains(f[1], "sshd_config.d") {
			return nil
		}
	}
	return os.WriteFile(p, append([]byte("Include /etc/ssh/sshd_config.d/*.conf\n\n"), b...), 0o644)
}

// restartSSH restarts sshd; with socket activation (Ubuntu 22.10+) the port
// lives in ssh.socket, regenerated from sshd_config on daemon-reload.
func (sv *Service) restartSSH(ctx context.Context) error {
	if _, err := sv.Run(ctx, "systemctl", "is-active", "--quiet", "ssh.socket"); err == nil {
		sv.Run(ctx, "systemctl", "daemon-reload")
		if _, err := sv.Run(ctx, "systemctl", "restart", "ssh.socket"); err != nil {
			return err
		}
		sv.Run(ctx, "systemctl", "restart", "ssh")
		return nil
	}
	if _, err := sv.Run(ctx, "systemctl", "restart", "ssh"); err != nil {
		_, err2 := sv.Run(ctx, "systemctl", "restart", "sshd")
		if err2 != nil {
			return err
		}
	}
	return nil
}

func (sv *Service) authKeysPath() string { return sv.path("/root/.ssh/authorized_keys") }

func (sv *Service) keys(ctx context.Context) []Key {
	out := []Key{}
	b, err := os.ReadFile(sv.authKeysPath())
	if err != nil {
		return out
	}
	for i, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k := Key{Line: i + 1}
		f := strings.Fields(l)
		for j, x := range f {
			if strings.HasPrefix(x, "ssh-") || strings.HasPrefix(x, "ecdsa-") || strings.HasPrefix(x, "sk-") {
				k.Type = x
				if j+2 < len(f) {
					k.Comment = strings.Join(f[j+2:], " ")
				}
				break
			}
		}
		if fp, err := sv.fingerprint(ctx, l); err == nil {
			k.Fingerprint = fp
		}
		out = append(out, k)
	}
	return out
}

func (sv *Service) fingerprint(ctx context.Context, line string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-lf", "-")
	cmd.Stdin = strings.NewReader(line + "\n")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(out))
	if len(f) < 2 {
		return "", ErrKey
	}
	return f[1], nil
}

var keyRe = regexp.MustCompile(`^(ssh-(rsa|ed25519|dss)|ecdsa-sha2-nistp(256|384|521)|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com) [A-Za-z0-9+/=]{40,}( [^\r\n]{0,200})?$`)

// AddKey appends a public key to root's authorized_keys.
func (sv *Service) AddKey(ctx context.Context, line string) error {
	line = strings.TrimSpace(line)
	if !keyRe.MatchString(line) {
		return ErrKey
	}
	if _, err := sv.fingerprint(ctx, line); err != nil {
		return ErrKey
	}
	p := sv.authKeysPath()
	os.MkdirAll(filepath.Dir(p), 0o700)
	b, _ := os.ReadFile(p)
	if bytes.Contains(b, []byte(strings.Fields(line)[1])) {
		return nil
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	return os.WriteFile(p, append(b, []byte(line+"\n")...), 0o600)
}

// RemoveKey deletes the key on line n, refusing the last key when password
// login is off.
func (sv *Service) RemoveKey(ctx context.Context, n int) error {
	p := sv.authKeysPath()
	b, err := os.ReadFile(p)
	if err != nil {
		return ErrKey
	}
	lines := strings.Split(string(b), "\n")
	if n < 1 || n > len(lines) || strings.TrimSpace(lines[n-1]) == "" {
		return ErrKey
	}
	if !sv.GetSSH(ctx).PasswordAuth && len(sv.keys(ctx)) <= 1 {
		return ErrLockout
	}
	lines = append(lines[:n-1], lines[n:]...)
	return os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o600)
}

// ---- processes ----

type Proc struct {
	PID     int32   `json:"pid"`
	PPID    int32   `json:"ppid"`
	Name    string  `json:"name"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Mem     float32 `json:"mem"`
	RSS     uint64  `json:"rss"`
	Status  string  `json:"status"`
	Started int64   `json:"started"`
	Cmd     string  `json:"cmd"`
	Threads int32   `json:"threads"`
}

func Processes(ctx context.Context) ([]Proc, error) {
	ps, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Proc, 0, len(ps))
	for _, p := range ps {
		x := Proc{PID: p.Pid}
		x.Name, _ = p.NameWithContext(ctx)
		x.PPID, _ = p.PpidWithContext(ctx)
		x.User, _ = p.UsernameWithContext(ctx)
		x.CPU, _ = p.CPUPercentWithContext(ctx)
		x.Mem, _ = p.MemoryPercentWithContext(ctx)
		if m, err := p.MemoryInfoWithContext(ctx); err == nil {
			x.RSS = m.RSS
		}
		if st, err := p.StatusWithContext(ctx); err == nil && len(st) > 0 {
			x.Status = st[0]
		}
		if c, err := p.CreateTimeWithContext(ctx); err == nil {
			x.Started = c / 1000
		}
		x.Cmd, _ = p.CmdlineWithContext(ctx)
		if len(x.Cmd) > 500 {
			x.Cmd = x.Cmd[:500]
		}
		x.Threads, _ = p.NumThreadsWithContext(ctx)
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CPU > out[j].CPU })
	return out, nil
}

// Kill signals a process; init, the panel and its parents are refused.
func Kill(ctx context.Context, pid int32, force bool) error {
	if pid <= 1 || pid == int32(os.Getpid()) || pid == int32(os.Getppid()) {
		return ErrPID
	}
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return ErrPID
	}
	if force {
		return p.KillWithContext(ctx)
	}
	return p.TerminateWithContext(ctx)
}

// ---- network connections ----

type Conn struct {
	Proto  string `json:"proto"`
	Local  string `json:"local"`
	Remote string `json:"remote"`
	Status string `json:"status"`
	PID    int32  `json:"pid"`
	Name   string `json:"name"`
}

func Connections(ctx context.Context) ([]Conn, error) {
	cs, err := psnet.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		return nil, err
	}
	names := map[int32]string{}
	out := make([]Conn, 0, len(cs))
	for _, c := range cs {
		proto := "tcp"
		if c.Type == 2 {
			proto = "udp"
		}
		if c.Family == 10 {
			proto += "6"
		}
		x := Conn{Proto: proto, Local: addr(c.Laddr.IP, c.Laddr.Port), Status: c.Status, PID: c.Pid}
		if c.Raddr.IP != "" {
			x.Remote = addr(c.Raddr.IP, c.Raddr.Port)
		}
		if c.Pid > 0 {
			n, ok := names[c.Pid]
			if !ok {
				if p, err := process.NewProcessWithContext(ctx, c.Pid); err == nil {
					n, _ = p.NameWithContext(ctx)
				}
				names[c.Pid] = n
			}
			x.Name = n
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Status == "LISTEN") != (out[j].Status == "LISTEN") {
			return out[i].Status == "LISTEN"
		}
		return out[i].Local < out[j].Local
	})
	return out, nil
}

func addr(ip string, port uint32) string {
	if strings.Contains(ip, ":") {
		return fmt.Sprintf("[%s]:%d", ip, port)
	}
	return fmt.Sprintf("%s:%d", ip, port)
}

// ---- disks ----

type Mount struct {
	Device     string  `json:"device"`
	Mountpoint string  `json:"mountpoint"`
	FSType     string  `json:"fstype"`
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	Percent    float64 `json:"percent"`
	Inodes     float64 `json:"inodes_percent"`
}

func Mounts(ctx context.Context) []Mount {
	out := []Mount{}
	ps, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return out
	}
	seen := map[string]bool{}
	for _, p := range ps {
		if seen[p.Mountpoint] || strings.HasPrefix(p.Mountpoint, "/snap") || strings.HasPrefix(p.Mountpoint, "/var/lib/docker") ||
			strings.HasPrefix(p.Mountpoint, "/run") || strings.HasPrefix(p.Mountpoint, "/boot/efi") {
			continue
		}
		seen[p.Mountpoint] = true
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		out = append(out, Mount{Device: p.Device, Mountpoint: p.Mountpoint, FSType: p.Fstype, Total: u.Total, Used: u.Used, Free: u.Free,
			Percent: u.UsedPercent, Inodes: u.InodesUsedPercent})
	}
	return out
}

type DirSize struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// DirUsage lists the immediate children of dir by size (du, one filesystem).
func (sv *Service) DirUsage(ctx context.Context, dir string) ([]DirSize, error) {
	dir = filepath.Clean("/" + dir)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, _ := sv.Run(ctx, "du", "-x", "-b", "-d", "1", sv.path(dir)) // errors on unreadable dirs are expected
	var list []DirSize
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		size, p, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(size, 10, 64)
		p = "/" + strings.TrimPrefix(strings.TrimPrefix(p, sv.Root), "/")
		if p == dir {
			continue
		}
		list = append(list, DirSize{Path: p, Size: n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Size > list[j].Size })
	if len(list) > 100 {
		list = list[:100]
	}
	return list, nil
}
