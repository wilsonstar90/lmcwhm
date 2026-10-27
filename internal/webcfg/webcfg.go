// Package webcfg stores the web-hosting configuration: Cloudflare token, ACME
// CA settings and server addresses. Credentials are encrypted at rest.
package webcfg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/mail"
	"sync"

	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

// CA presets. LiteSSL and custom CAs take a directory URL (and usually EAB).
const (
	CALetsEncrypt        = "letsencrypt"
	CALetsEncryptStaging = "letsencrypt-staging"
	CALiteSSL            = "litessl"
	CACustom             = "custom"
)

var Directories = map[string]string{
	CALetsEncrypt:        "https://acme-v02.api.letsencrypt.org/directory",
	CALetsEncryptStaging: "https://acme-staging-v02.api.letsencrypt.org/directory",
}

// Config is the decrypted view used by the services.
type Config struct {
	CFToken       string `json:"cf_token"`
	ACMEEmail     string `json:"acme_email"`
	ACMECA        string `json:"acme_ca"`
	ACMEDirectory string `json:"acme_directory"`
	EABKid        string `json:"eab_kid"`
	EABHmac       string `json:"eab_hmac"`
	ServerIPv4    string `json:"server_ipv4"`
	ServerIPv6    string `json:"server_ipv6"`
	// ACMEAccount is the lego account (key + registration) as JSON, keyed by directory.
	ACMEAccount string `json:"acme_account"`
}

// Directory resolves the ACME directory URL for the configured CA.
func (c Config) Directory() string {
	if d, ok := Directories[c.ACMECA]; ok {
		return d
	}
	return c.ACMEDirectory
}

var (
	ErrBadEmail     = errors.New("web.invalid_email")
	ErrBadDirectory = errors.New("web.invalid_directory")
	ErrBadIP        = errors.New("web.invalid_ip")
)

func (c *Config) Validate() error {
	if c.ACMEEmail != "" {
		if _, err := mail.ParseAddress(c.ACMEEmail); err != nil {
			return ErrBadEmail
		}
	}
	switch c.ACMECA {
	case "":
		c.ACMECA = CALetsEncrypt
	case CALetsEncrypt, CALetsEncryptStaging:
	case CALiteSSL, CACustom:
		if len(c.ACMEDirectory) < 12 || c.ACMEDirectory[:8] != "https://" {
			return ErrBadDirectory
		}
	default:
		return ErrBadDirectory
	}
	if c.ServerIPv4 != "" {
		if ip := net.ParseIP(c.ServerIPv4); ip == nil || ip.To4() == nil {
			return ErrBadIP
		}
	}
	if c.ServerIPv6 != "" {
		if ip := net.ParseIP(c.ServerIPv6); ip == nil || ip.To4() != nil {
			return ErrBadIP
		}
	}
	return nil
}

// Store persists Config in the settings table with secrets sealed.
type Store struct {
	st  *store.Store
	box *secrets.Box
	mu  sync.Mutex
}

func New(st *store.Store, box *secrets.Box) *Store { return &Store{st: st, box: box} }

const key = "web"

func (s *Store) Get(ctx context.Context) (Config, error) {
	var raw string
	err := s.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Config{ACMECA: CALetsEncrypt}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Config{}, err
	}
	for _, p := range []*string{&c.CFToken, &c.EABHmac, &c.ACMEAccount} {
		v, err := s.box.Open(*p)
		if err != nil {
			return Config{}, err
		}
		*p = v
	}
	return c, nil
}

func (s *Store) Save(ctx context.Context, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	c.CFToken, c.EABHmac, c.ACMEAccount = s.box.Seal(c.CFToken), s.box.Seal(c.EABHmac), s.box.Seal(c.ACMEAccount)
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.st.DB.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(raw))
	return err
}

// Update applies fn under a lock so concurrent writers (UI save vs. ACME account
// registration) do not lose each other's fields.
func (s *Store) Update(ctx context.Context, fn func(*Config)) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.Get(ctx)
	if err != nil {
		return Config{}, err
	}
	fn(&c)
	return c, s.Save(ctx, c)
}
