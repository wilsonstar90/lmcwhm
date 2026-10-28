// Package fileshare stores file-manager share links (外鏈分享: expiry,
// password, download limit) and per-user favourite folders.
package fileshare

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

var (
	ErrNotFound = errors.New("share.not_found")
	ErrExpired  = errors.New("share.expired")
	ErrPassword = errors.New("share.password_required")
)

type Share struct {
	ID           int64  `json:"id"`
	Token        string `json:"token"`
	Path         string `json:"path"`
	HasPassword  bool   `json:"has_password"`
	ExpiresAt    int64  `json:"expires_at"`
	MaxDownloads int    `json:"max_downloads"`
	Downloads    int    `json:"downloads"`
	CreatedBy    string `json:"created_by"`
	CreatedAt    int64  `json:"created_at"`
	hash         string
}

// Active reports whether the link may still be used.
func (s Share) Active(now time.Time) bool {
	return (s.ExpiresAt == 0 || now.Unix() < s.ExpiresAt) && (s.MaxDownloads == 0 || s.Downloads < s.MaxDownloads)
}

type Service struct{ st *store.Store }

func New(st *store.Store) *Service { return &Service{st: st} }

const cols = `id, token, path, password_hash, expires_at, max_downloads, downloads, created_by, created_at`

func scan(row interface{ Scan(...any) error }) (Share, error) {
	var s Share
	err := row.Scan(&s.ID, &s.Token, &s.Path, &s.hash, &s.ExpiresAt, &s.MaxDownloads, &s.Downloads, &s.CreatedBy, &s.CreatedAt)
	s.HasPassword = s.hash != ""
	return s, err
}

func (sv *Service) List(ctx context.Context) ([]Share, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT `+cols+` FROM file_shares ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		s, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Create makes a link to path valid for ttl (0 = no expiry).
func (sv *Service) Create(ctx context.Context, path, password string, ttl time.Duration, maxDownloads int, by string) (Share, error) {
	b := make([]byte, 18)
	rand.Read(b)
	s := Share{Token: base64.RawURLEncoding.EncodeToString(b), Path: path, MaxDownloads: max(maxDownloads, 0), CreatedBy: by, CreatedAt: time.Now().Unix()}
	if ttl > 0 {
		s.ExpiresAt = time.Now().Add(ttl).Unix()
	}
	if password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return s, err
		}
		s.hash, s.HasPassword = string(h), true
	}
	res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO file_shares(token, path, password_hash, expires_at, max_downloads, downloads, created_by, created_at) VALUES(?,?,?,?,?,0,?,?)`,
		s.Token, s.Path, s.hash, s.ExpiresAt, s.MaxDownloads, s.CreatedBy, s.CreatedAt)
	if err != nil {
		return s, err
	}
	s.ID, _ = res.LastInsertId()
	return s, nil
}

func (sv *Service) Delete(ctx context.Context, id int64) error {
	_, err := sv.st.DB.ExecContext(ctx, `DELETE FROM file_shares WHERE id = ?`, id)
	return err
}

// Lookup returns an active share; password is checked when one is set.
func (sv *Service) Lookup(ctx context.Context, token string) (Share, error) {
	s, err := scan(sv.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM file_shares WHERE token = ?`, token))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if !s.Active(time.Now()) {
		return s, ErrExpired
	}
	return s, nil
}

func (s Share) CheckPassword(pw string) bool {
	return s.hash == "" || bcrypt.CompareHashAndPassword([]byte(s.hash), []byte(pw)) == nil
}

// Count records a download.
func (sv *Service) Count(ctx context.Context, id int64) {
	sv.st.DB.ExecContext(ctx, `UPDATE file_shares SET downloads = downloads + 1 WHERE id = ?`, id)
}

// ---- favourites ----

type Favorite struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

func (sv *Service) Favorites(ctx context.Context, userID int64) ([]Favorite, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, path FROM file_favorites WHERE user_id = ? ORDER BY path`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Favorite{}
	for rows.Next() {
		var f Favorite
		if err := rows.Scan(&f.ID, &f.Path); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (sv *Service) AddFavorite(ctx context.Context, userID int64, path string) error {
	_, err := sv.st.DB.ExecContext(ctx, `INSERT OR IGNORE INTO file_favorites(user_id, path, created_at) VALUES(?,?,?)`, userID, path, time.Now().Unix())
	return err
}

func (sv *Service) RemoveFavorite(ctx context.Context, userID int64, path string) error {
	_, err := sv.st.DB.ExecContext(ctx, `DELETE FROM file_favorites WHERE user_id = ? AND path = ?`, userID, path)
	return err
}
