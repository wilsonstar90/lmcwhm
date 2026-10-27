// Package audit records panel operations and serves the login / operation logs.
package audit

import (
	"context"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

type Logger struct{ st *store.Store }

func New(st *store.Store) *Logger { return &Logger{st: st} }

func (l *Logger) Record(ctx context.Context, username, ip, category, action, detail string) {
	l.st.DB.ExecContext(ctx,
		`INSERT INTO audit_logs(username, ip, category, action, detail, created_at) VALUES(?,?,?,?,?,?)`,
		username, ip, category, action, detail, time.Now().Unix())
}

type Entry struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	IP        string `json:"ip"`
	Category  string `json:"category"`
	Action    string `json:"action"`
	Detail    string `json:"detail"`
	CreatedAt int64  `json:"created_at"`
}

type LoginEntry struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
	Success   bool   `json:"success"`
	Reason    string `json:"reason"`
	CreatedAt int64  `json:"created_at"`
}

func (l *Logger) List(ctx context.Context, limit, offset int) ([]Entry, int, error) {
	var total int
	if err := l.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := l.st.DB.QueryContext(ctx,
		`SELECT id, username, ip, category, action, detail, created_at FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Username, &e.IP, &e.Category, &e.Action, &e.Detail, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

func (l *Logger) ListLogins(ctx context.Context, limit, offset int) ([]LoginEntry, int, error) {
	var total int
	if err := l.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_logs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := l.st.DB.QueryContext(ctx,
		`SELECT id, username, ip, user_agent, success, reason, created_at FROM login_logs ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []LoginEntry{}
	for rows.Next() {
		var e LoginEntry
		if err := rows.Scan(&e.ID, &e.Username, &e.IP, &e.UserAgent, &e.Success, &e.Reason, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
