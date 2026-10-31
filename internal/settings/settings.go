// Package settings holds the aaPanel-style panel configuration (port, security
// entry, panel SSL, authorized IPs, bound domain, ...), persisted in SQLite.
package settings

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

type SSLMode string

const (
	SSLOff        SSLMode = "off"
	SSLSelfSigned SSLMode = "selfsigned"
	SSLCustom     SSLMode = "custom"
)

type Settings struct {
	PanelAlias     string   `json:"panel_alias"`
	PanelPort      int      `json:"panel_port"`
	ListenIPv6     bool     `json:"listen_ipv6"`
	SecurityEntry  string   `json:"security_entry"` // "" = disabled, otherwise e.g. "/console"
	SSLMode        SSLMode  `json:"ssl_mode"`
	BindDomain     string   `json:"bind_domain"`     // "" = any Host header accepted
	AllowedIPs     []string `json:"allowed_ips"`     // IPs or CIDRs; empty = all
	SessionTimeout int      `json:"session_timeout"` // seconds
	LoginMaxFails  int      `json:"login_max_fails"` // per IP inside LoginLockWindow
	LoginLockMins  int      `json:"login_lock_mins"`
	BasicAuthUser  string   `json:"basic_auth_user"`
	BasicAuthHash  string   `json:"basic_auth_hash,omitempty"` // bcrypt; never sent to clients
	Language       string   `json:"language"`
	Timezone       string   `json:"timezone"`
	// UnauthStatus answers visitors who have not passed the gates (wrong IP,
	// domain or security entry): 0 = 404, or one of UnauthStatuses; 444 closes
	// the connection without a response.
	UnauthStatus int `json:"unauth_status"`
	// SiteRoot is where new websites are created ("" = /www/wwwroot) and
	// BackupDir where new backups are written ("" = <home>/backup).
	SiteRoot  string `json:"site_root"`
	BackupDir string `json:"backup_dir"`
	// LogoImage shows the uploaded logo instead of the panel alias.
	LogoImage bool `json:"logo_image"`
}

// UnauthStatuses are the answers an unauthenticated visitor may get.
var UnauthStatuses = []int{400, 401, 403, 404, 408, 416, 444}

// systemDirs may not hold websites or backups.
var systemDirs = []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32", "/proc", "/root", "/run", "/sbin", "/sys", "/usr", "/var/lib/docker", "/var/run", "/snap"}

// ValidDir checks a website or backup directory: absolute, clean, and not a
// system directory or inside one.
func ValidDir(p string) bool {
	if p == "" {
		return true
	}
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" || len(p) > 200 || strings.ContainsAny(p, " \t\n\"'$`\\;:") {
		return false
	}
	for _, d := range systemDirs {
		if p == d || strings.HasPrefix(p, d+"/") {
			return false
		}
	}
	return true
}

func Defaults() Settings {
	return Settings{
		PanelAlias:     "lmcwhm",
		PanelPort:      8888,
		ListenIPv6:     true,
		SecurityEntry:  "",
		SSLMode:        SSLSelfSigned,
		SessionTimeout: 86400,
		LoginMaxFails:  5,
		LoginLockMins:  15,
		Language:       "zh-HK",
		Timezone:       "Asia/Hong_Kong",
	}
}

var (
	ErrInvalidPort   = errors.New("settings.invalid_port")
	ErrInvalidEntry  = errors.New("settings.invalid_entry")
	ErrInvalidIP     = errors.New("settings.invalid_ip")
	ErrInvalidDom    = errors.New("settings.invalid_domain")
	ErrInvalidLang   = errors.New("settings.invalid_language")
	ErrInvalidDir    = errors.New("settings.invalid_dir")
	ErrInvalidUnauth = errors.New("settings.invalid_unauth_status")
)

var (
	entryRe  = regexp.MustCompile(`^/[A-Za-z0-9_-]{3,64}$`)
	domainRe = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	// Entries that would shadow the panel's own routes.
	reservedEntries = map[string]bool{"/api": true, "/assets": true, "/ws": true, "/mcp": true}
	Languages       = []string{"zh-HK", "zh-TW", "zh-CN", "en", "ja"}
)

func (s *Settings) Validate() error {
	if s.PanelPort < 1 || s.PanelPort > 65535 {
		return ErrInvalidPort
	}
	if s.SecurityEntry != "" && (!entryRe.MatchString(s.SecurityEntry) || reservedEntries[strings.ToLower(s.SecurityEntry)]) {
		return ErrInvalidEntry
	}
	if s.BindDomain != "" && !domainRe.MatchString(s.BindDomain) {
		return ErrInvalidDom
	}
	for _, ip := range s.AllowedIPs {
		if _, err := ParseIPOrCIDR(ip); err != nil {
			return ErrInvalidIP
		}
	}
	langOK := false
	for _, l := range Languages {
		if l == s.Language {
			langOK = true
		}
	}
	if !langOK {
		return ErrInvalidLang
	}
	if s.SessionTimeout < 300 {
		s.SessionTimeout = 300
	}
	if s.LoginMaxFails < 1 {
		s.LoginMaxFails = 1
	}
	if s.LoginLockMins < 1 {
		s.LoginLockMins = 1
	}
	switch s.SSLMode {
	case SSLOff, SSLSelfSigned, SSLCustom:
	default:
		s.SSLMode = SSLSelfSigned
	}
	if s.UnauthStatus != 0 && !slices.Contains(UnauthStatuses, s.UnauthStatus) {
		return ErrInvalidUnauth
	}
	if !ValidDir(s.SiteRoot) || !ValidDir(s.BackupDir) {
		return ErrInvalidDir
	}
	return nil
}

// ParseIPOrCIDR accepts "1.2.3.4", "2001:db8::1" or "10.0.0.0/8".
func ParseIPOrCIDR(v string) (*net.IPNet, error) {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "/") {
		_, n, err := net.ParseCIDR(v)
		return n, err
	}
	ip := net.ParseIP(v)
	if ip == nil {
		return nil, fmt.Errorf("invalid ip %q", v)
	}
	bits := 128
	if ip.To4() != nil {
		ip = ip.To4()
		bits = 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
}

// IPAllowed reports whether ip may reach the panel under s.AllowedIPs.
func (s *Settings) IPAllowed(ip net.IP) bool {
	if len(s.AllowedIPs) == 0 || ip == nil {
		return len(s.AllowedIPs) == 0
	}
	if ip.IsLoopback() {
		return true // never lock out local CLI / SSH tunnels
	}
	for _, v := range s.AllowedIPs {
		if n, err := ParseIPOrCIDR(v); err == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// Manager caches settings in memory and writes through to SQLite.
type Manager struct {
	st  *store.Store
	mu  sync.RWMutex
	cur Settings
}

const key = "panel"

func NewManager(ctx context.Context, st *store.Store) (*Manager, error) {
	m := &Manager{st: st, cur: Defaults()}
	var raw string
	err := st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := m.Save(ctx, m.cur); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal([]byte(raw), &m.cur); err != nil {
			return nil, fmt.Errorf("decode settings: %w", err)
		}
	}
	return m, nil
}

func (m *Manager) Get() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.cur
	s.AllowedIPs = append([]string(nil), m.cur.AllowedIPs...)
	return s
}

func (m *Manager) Save(ctx context.Context, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err := m.st.DB.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, string(raw)); err != nil {
		return err
	}
	m.mu.Lock()
	m.cur = s
	m.mu.Unlock()
	return nil
}

// Update loads, mutates and saves in one step.
func (m *Manager) Update(ctx context.Context, fn func(*Settings)) (Settings, error) {
	s := m.Get()
	fn(&s)
	return s, m.Save(ctx, s)
}

// RequiresRestart reports whether moving from a to b needs the listener rebuilt.
func RequiresRestart(a, b Settings) bool {
	return a.PanelPort != b.PanelPort || a.ListenIPv6 != b.ListenIPv6 || a.SSLMode != b.SSLMode
}

// RandomEntry returns a fresh "/xxxxxxxx" security entry, like aaPanel's installer.
func RandomEntry() string { return "/" + RandomString(8, lowerAlnum) }

// RandomPort picks a panel port in the aaPanel-style 10000-60000 range.
func RandomPort() int {
	n, _ := rand.Int(rand.Reader, big.NewInt(50000))
	return int(n.Int64()) + 10000
}

const (
	lowerAlnum = "abcdefghijklmnopqrstuvwxyz0123456789"
	Alnum      = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"
)

func RandomString(n int, alphabet string) string {
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[k.Int64()]
	}
	return string(b)
}

// InstanceSecret returns a per-installation random key (created on first use),
// used to sign the security-entry cookie.
func (m *Manager) InstanceSecret(ctx context.Context) (string, error) {
	var v string
	err := m.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'instance_secret'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		v = RandomString(48, Alnum)
		_, err = m.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES('instance_secret', ?)`, v)
	}
	return v, err
}

// Reload re-reads settings from disk (after the CLI changed them).
func (m *Manager) Reload(ctx context.Context) error {
	var raw string
	if err := m.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw); err != nil {
		return err
	}
	s := Defaults()
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return err
	}
	m.mu.Lock()
	m.cur = s
	m.mu.Unlock()
	return nil
}
