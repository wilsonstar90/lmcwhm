// Package auth manages panel users, server-side sessions, TOTP 2FA and the
// per-IP login lockout.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

var (
	ErrBadCredentials = errors.New("auth.bad_credentials")
	ErrTOTPRequired   = errors.New("auth.totp_required")
	ErrBadTOTP        = errors.New("auth.bad_totp")
	ErrLocked         = errors.New("auth.locked")
	ErrWeakPassword   = errors.New("auth.weak_password")
	ErrBadUsername    = errors.New("auth.bad_username")
	ErrNoSession      = errors.New("auth.no_session")
)

type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	TOTPEnabled bool   `json:"totp_enabled"`
	IsAdmin     bool   `json:"is_admin"`
	totpSecret  string
}

type Session struct {
	User      User
	CSRF      string
	ExpiresAt time.Time
}

// dummyHash is compared against when the username is unknown.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("lmcwhm-dummy"), bcrypt.DefaultCost)

type Service struct {
	st *store.Store
}

func New(st *store.Store) *Service { return &Service{st: st} }

func HashPassword(pw string) (string, error) {
	if len(pw) < 8 {
		return "", ErrWeakPassword
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

func validUsername(u string) bool {
	if len(u) < 3 || len(u) > 32 {
		return false
	}
	for _, c := range u {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// HasUsers reports whether the panel has been initialised.
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	var n int
	err := s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0, err
}

func (s *Service) CreateUser(ctx context.Context, username, password string, admin bool) error {
	if !validUsername(username) {
		return ErrBadUsername
	}
	h, err := HashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = s.st.DB.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, is_admin, created_at, updated_at) VALUES(?,?,?,?,?)`,
		username, h, admin, now, now)
	return err
}

// PrimaryAdmin returns the first admin; the CLI's rescue commands act on it.
func (s *Service) PrimaryAdmin(ctx context.Context) (User, error) {
	return s.scanUser(s.st.DB.QueryRowContext(ctx,
		`SELECT id, username, totp_enabled, is_admin, totp_secret FROM users WHERE is_admin = 1 ORDER BY id LIMIT 1`))
}

func (s *Service) UserByID(ctx context.Context, id int64) (User, error) {
	return s.scanUser(s.st.DB.QueryRowContext(ctx,
		`SELECT id, username, totp_enabled, is_admin, totp_secret FROM users WHERE id = ?`, id))
}

func (s *Service) scanUser(row *sql.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.TOTPEnabled, &u.IsAdmin, &u.totpSecret)
	return u, err
}

func (s *Service) SetPassword(ctx context.Context, userID int64, password string) error {
	h, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := s.st.DB.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		h, time.Now().Unix(), userID); err != nil {
		return err
	}
	return s.RevokeUserSessions(ctx, userID)
}

func (s *Service) SetUsername(ctx context.Context, userID int64, username string) error {
	if !validUsername(username) {
		return ErrBadUsername
	}
	_, err := s.st.DB.ExecContext(ctx, `UPDATE users SET username = ?, updated_at = ? WHERE id = ?`,
		username, time.Now().Unix(), userID)
	return err
}

func (s *Service) CheckPassword(ctx context.Context, userID int64, password string) bool {
	var h string
	if err := s.st.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&h); err != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(password)) == nil
}

// ---- 2FA ----

// BeginTOTP generates (but does not enable) a new secret for the user.
func (s *Service) BeginTOTP(ctx context.Context, u User, issuer string) (secret, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: u.Username})
	if err != nil {
		return "", "", err
	}
	_, err = s.st.DB.ExecContext(ctx, `UPDATE users SET totp_secret = ?, totp_enabled = 0 WHERE id = ?`, key.Secret(), u.ID)
	return key.Secret(), key.URL(), err
}

func (s *Service) EnableTOTP(ctx context.Context, userID int64, code string) error {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.totpSecret == "" || !totp.Validate(code, u.totpSecret) {
		return ErrBadTOTP
	}
	_, err = s.st.DB.ExecContext(ctx, `UPDATE users SET totp_enabled = 1 WHERE id = ?`, userID)
	return err
}

// DisableTOTP clears 2FA; the CLI rescue path calls it without a code.
func (s *Service) DisableTOTP(ctx context.Context, userID int64) error {
	_, err := s.st.DB.ExecContext(ctx, `UPDATE users SET totp_enabled = 0, totp_secret = '' WHERE id = ?`, userID)
	return err
}

func (s *Service) VerifyTOTP(u User, code string) bool {
	return u.TOTPEnabled && totp.Validate(code, u.totpSecret)
}

// ---- login & sessions ----

type LoginInput struct {
	Username, Password, TOTP, IP, UserAgent string
	MaxFails                                int
	LockWindow                              time.Duration
	SessionTTL                              time.Duration
}

// Login verifies credentials and returns a new session token. Every attempt is
// written to login_logs, which also drives the per-IP lockout.
func (s *Service) Login(ctx context.Context, in LoginInput) (token string, sess Session, err error) {
	var fails int
	since := time.Now().Add(-in.LockWindow).Unix()
	if err := s.st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM login_logs WHERE ip = ? AND success = 0 AND created_at >= ?`, in.IP, since).Scan(&fails); err != nil {
		return "", Session{}, err
	}
	if fails >= in.MaxFails {
		s.logLogin(ctx, in, false, "locked")
		return "", Session{}, ErrLocked
	}

	var (
		u    User
		hash string
	)
	row := s.st.DB.QueryRowContext(ctx,
		`SELECT id, username, totp_enabled, is_admin, totp_secret, password_hash FROM users WHERE username = ?`, in.Username)
	if err := row.Scan(&u.ID, &u.Username, &u.TOTPEnabled, &u.IsAdmin, &u.totpSecret, &hash); err != nil {
		// Burn comparable time so usernames cannot be probed by latency.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(in.Password))
		s.logLogin(ctx, in, false, "bad_credentials")
		return "", Session{}, ErrBadCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		s.logLogin(ctx, in, false, "bad_credentials")
		return "", Session{}, ErrBadCredentials
	}
	if u.TOTPEnabled {
		if in.TOTP == "" {
			return "", Session{}, ErrTOTPRequired // not a failure: the client now asks for the code
		}
		if !totp.Validate(in.TOTP, u.totpSecret) {
			s.logLogin(ctx, in, false, "bad_totp")
			return "", Session{}, ErrBadTOTP
		}
	}

	token = randomToken()
	csrf := randomToken()
	now := time.Now()
	exp := now.Add(in.SessionTTL)
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO sessions(token_hash, user_id, csrf, ip, user_agent, created_at, expires_at) VALUES(?,?,?,?,?,?,?)`,
		hashToken(token), u.ID, csrf, in.IP, in.UserAgent, now.Unix(), exp.Unix()); err != nil {
		return "", Session{}, err
	}
	s.logLogin(ctx, in, true, "")
	return token, Session{User: u, CSRF: csrf, ExpiresAt: exp}, nil
}

func (s *Service) logLogin(ctx context.Context, in LoginInput, ok bool, reason string) {
	s.st.DB.ExecContext(ctx,
		`INSERT INTO login_logs(username, ip, user_agent, success, reason, created_at) VALUES(?,?,?,?,?,?)`,
		in.Username, in.IP, in.UserAgent, ok, reason, time.Now().Unix())
}

// Resolve returns the live session for a cookie token.
func (s *Service) Resolve(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNoSession
	}
	var (
		sess   Session
		userID int64
		exp    int64
	)
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT user_id, csrf, expires_at FROM sessions WHERE token_hash = ?`, hashToken(token)).Scan(&userID, &sess.CSRF, &exp)
	if err != nil || time.Now().Unix() > exp {
		return Session{}, ErrNoSession
	}
	sess.ExpiresAt = time.Unix(exp, 0)
	sess.User, err = s.UserByID(ctx, userID)
	if err != nil {
		return Session{}, ErrNoSession
	}
	return sess, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

func (s *Service) RevokeUserSessions(ctx context.Context, userID int64) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Service) RevokeAllSessions(ctx context.Context) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}

// ClearLoginLocks removes failed attempts so locked-out IPs can retry (CLI "unlock").
func (s *Service) ClearLoginLocks(ctx context.Context) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM login_logs WHERE success = 0`)
	return err
}

func (s *Service) PurgeExpired(ctx context.Context) {
	s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}
