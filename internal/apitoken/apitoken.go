// Package apitoken manages API tokens for machine access (MCP clients):
// a scope, an optional IP allow-list, expiry and pause switch. Authentication
// uses a SHA-256 hash; new tokens are also encrypted for later admin retrieval.
package apitoken

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/mcp"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

var (
	ErrNotFound       = errors.New("token.not_found")
	ErrInvalid        = errors.New("token.invalid")
	ErrName           = errors.New("token.invalid_name")
	ErrScope          = errors.New("token.invalid_scope")
	ErrIP             = errors.New("token.invalid_ip")
	ErrNotRecoverable = errors.New("token.not_recoverable")
)

const prefix = "lmc_"

type Token struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Hint        string   `json:"hint"` // first characters, to tell tokens apart
	Scope       string   `json:"scope"`
	IPs         []string `json:"ips"`
	ExpiresAt   int64    `json:"expires_at"` // 0 = never
	LastUsed    int64    `json:"last_used"`
	LastIP      string   `json:"last_ip"`
	CreatedAt   int64    `json:"created_at"`
	Disabled    bool     `json:"disabled"`
	Recoverable bool     `json:"recoverable"`
}

type Service struct {
	st  *store.Store
	box *secrets.Box
}

func New(st *store.Store, box *secrets.Box) *Service { return &Service{st: st, box: box} }

func hash(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

const cols = `id, name, hint, scope, ips, expires_at, last_used, last_ip, created_at, disabled, token_secret != ''`

func scan(row interface{ Scan(...any) error }) (Token, error) {
	var t Token
	var ips string
	err := row.Scan(&t.ID, &t.Name, &t.Hint, &t.Scope, &ips, &t.ExpiresAt, &t.LastUsed, &t.LastIP, &t.CreatedAt, &t.Disabled, &t.Recoverable)
	json.Unmarshal([]byte(ips), &t.IPs)
	if t.IPs == nil {
		t.IPs = []string{}
	}
	return t, err
}

func (sv *Service) List(ctx context.Context) ([]Token, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT `+cols+` FROM api_tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type Input struct {
	Name    string   `json:"name"`
	Scope   string   `json:"scope"`
	IPs     []string `json:"ips"`
	TTLDays int      `json:"ttl_days"` // 0 = never expires
}

// Create returns the new token's secret and keeps an encrypted copy for admins.
func (sv *Service) Create(ctx context.Context, in Input) (Token, string, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		return Token{}, "", ErrName
	}
	if !mcp.ValidScope(in.Scope) {
		return Token{}, "", ErrScope
	}
	for i, ip := range in.IPs {
		n, err := settings.ParseIPOrCIDR(ip)
		if err != nil {
			return Token{}, "", ErrIP
		}
		in.IPs[i] = n.String()
	}
	if in.IPs == nil {
		in.IPs = []string{}
	}
	if in.TTLDays < 0 || in.TTLDays > 3650 {
		return Token{}, "", ErrInvalid
	}
	secret := prefix + settings.RandomString(40, settings.Alnum)
	var exp int64
	now := time.Now()
	if in.TTLDays > 0 {
		exp = now.AddDate(0, 0, in.TTLDays).Unix()
	}
	ips, _ := json.Marshal(in.IPs)
	if sv.box == nil {
		return Token{}, "", ErrInvalid
	}
	res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO api_tokens(name, token_hash, token_secret, hint, scope, ips, expires_at, last_used, last_ip, created_at)
		VALUES(?,?,?,?,?,?,?,0,'',?)`, in.Name, hash(secret), sv.box.Seal(secret), secret[:len(prefix)+4], in.Scope, string(ips), exp, now.Unix())
	if err != nil {
		return Token{}, "", err
	}
	id, _ := res.LastInsertId()
	t, err := scan(sv.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM api_tokens WHERE id = ?`, id))
	return t, secret, err
}

// Secret decrypts a token on demand. Legacy hash-only tokens cannot be recovered.
func (sv *Service) Secret(ctx context.Context, id int64) (string, error) {
	var sealed string
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT token_secret FROM api_tokens WHERE id = ?`, id).Scan(&sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if sealed == "" {
		return "", ErrNotRecoverable
	}
	if sv.box == nil {
		return "", ErrNotRecoverable
	}
	return sv.box.Open(sealed)
}

type UpdateInput struct {
	ExpiresAt *int64 `json:"expires_at"`
	Disabled  *bool  `json:"disabled"`
}

func (sv *Service) Update(ctx context.Context, id int64, in UpdateInput) (Token, error) {
	if in.ExpiresAt != nil {
		if *in.ExpiresAt < 0 {
			return Token{}, ErrInvalid
		}
		if _, err := sv.st.DB.ExecContext(ctx, `UPDATE api_tokens SET expires_at = ? WHERE id = ?`, *in.ExpiresAt, id); err != nil {
			return Token{}, err
		}
	}
	if in.Disabled != nil {
		if _, err := sv.st.DB.ExecContext(ctx, `UPDATE api_tokens SET disabled = ? WHERE id = ?`, *in.Disabled, id); err != nil {
			return Token{}, err
		}
	}
	t, err := scan(sv.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM api_tokens WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, ErrNotFound
	}
	return t, err
}

func (sv *Service) Delete(ctx context.Context, id int64) error {
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Verify resolves a bearer token used from ip.
func (sv *Service) Verify(ctx context.Context, secret, ip string) (Token, error) {
	if !strings.HasPrefix(secret, prefix) || len(secret) > 200 {
		return Token{}, ErrInvalid
	}
	t, err := scan(sv.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM api_tokens WHERE token_hash = ?`, hash(secret)))
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, ErrInvalid
	}
	if err != nil {
		return Token{}, err
	}
	if t.Disabled {
		return Token{}, ErrInvalid
	}
	if t.ExpiresAt > 0 && time.Now().Unix() > t.ExpiresAt {
		return Token{}, ErrInvalid
	}
	if len(t.IPs) > 0 {
		addr := net.ParseIP(ip)
		ok := false
		for _, c := range t.IPs {
			if n, err := settings.ParseIPOrCIDR(c); err == nil && addr != nil && n.Contains(addr) {
				ok = true
			}
		}
		if !ok {
			return Token{}, ErrInvalid
		}
	}
	sv.st.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used = ?, last_ip = ? WHERE id = ?`, time.Now().Unix(), ip, t.ID)
	return t, nil
}
