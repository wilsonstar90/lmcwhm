package firewall

// User-Agent and URL lists. A request matching a block pattern is refused
// (403) at the edge; one matching an allow pattern skips the block patterns
// and the WAF. Both apply whether or not the nftables firewall is on.

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

type Pattern struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`  // ua | url
	List      string `json:"list"`  // block | allow
	Match     string `json:"match"` // contains | prefix | regex
	Value     string `json:"value"`
	Remark    string `json:"remark"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
}

var (
	ErrBadPattern = errors.New("fw.invalid_pattern")
	ErrDuplicate  = errors.New("fw.duplicate_pattern")
)

// MaxPatterns per list keeps the edge config small.
const MaxPatterns = 5000

func (p *Pattern) validate() error {
	p.Value, p.Remark = strings.TrimSpace(p.Value), strings.TrimSpace(p.Remark)
	if p.Kind != "ua" && p.Kind != "url" || p.List != "block" && p.List != "allow" {
		return ErrBadPattern
	}
	if p.Match == "" {
		p.Match = "contains"
	}
	if p.Match != "contains" && p.Match != "prefix" && p.Match != "regex" {
		return ErrBadPattern
	}
	// One line of a pattern file: no control characters, and "#" would start a comment.
	if p.Value == "" || len(p.Value) > 512 || strings.HasPrefix(p.Value, "#") || strings.IndexFunc(p.Value, unicode.IsControl) >= 0 {
		return ErrBadPattern
	}
	if p.Kind == "url" && p.Match == "prefix" && !strings.HasPrefix(p.Value, "/") {
		return ErrBadPattern
	}
	if p.Match == "regex" {
		if _, err := regexp.Compile(p.Value); err != nil {
			return ErrBadPattern
		}
	}
	if len(p.Remark) > 200 {
		p.Remark = p.Remark[:200]
	}
	return nil
}

func (sv *Service) Patterns(ctx context.Context, kind, list string) ([]Pattern, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, kind, list, match, value, remark, enabled, created_at FROM fw_patterns
		WHERE (? = '' OR kind = ?) AND (? = '' OR list = ?) ORDER BY id DESC`, kind, kind, list, list)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pattern{}
	for rows.Next() {
		var p Pattern
		if err := rows.Scan(&p.ID, &p.Kind, &p.List, &p.Match, &p.Value, &p.Remark, &p.Enabled, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (sv *Service) count(ctx context.Context, kind, list string) int {
	n := 0
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM fw_patterns WHERE kind = ? AND list = ?`, kind, list).Scan(&n)
	return n
}

// SavePattern creates (ID 0) or changes one entry.
func (sv *Service) SavePattern(ctx context.Context, p Pattern) (Pattern, error) {
	if err := p.validate(); err != nil {
		return p, err
	}
	if p.ID == 0 {
		if sv.count(ctx, p.Kind, p.List) >= MaxPatterns {
			return p, ErrBadPattern
		}
		p.CreatedAt = time.Now().Unix()
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO fw_patterns(kind, list, match, value, remark, enabled, created_at) VALUES(?,?,?,?,?,?,?)`,
			p.Kind, p.List, p.Match, p.Value, p.Remark, p.Enabled, p.CreatedAt)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return p, ErrDuplicate
			}
			return p, err
		}
		p.ID, _ = res.LastInsertId()
		return p, nil
	}
	res, err := sv.st.DB.ExecContext(ctx, `UPDATE fw_patterns SET match=?, value=?, remark=?, enabled=? WHERE id=? AND kind=? AND list=?`,
		p.Match, p.Value, p.Remark, p.Enabled, p.ID, p.Kind, p.List)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return p, ErrDuplicate
		}
		return p, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return p, ErrNotFound
	}
	return p, nil
}

// ImportPatterns adds every valid, new value; it returns how many were added
// and how many were skipped (invalid or already listed).
func (sv *Service) ImportPatterns(ctx context.Context, kind, list, match string, values []string, remark string) (added, skipped int, err error) {
	if kind != "ua" && kind != "url" || list != "block" && list != "allow" {
		return 0, 0, ErrBadPattern
	}
	room := MaxPatterns - sv.count(ctx, kind, list) // before the transaction takes the connection
	tx, err := sv.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, v := range values {
		p := Pattern{Kind: kind, List: list, Match: match, Value: v, Remark: remark, Enabled: true}
		if p.validate() != nil || room <= 0 {
			skipped++
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO fw_patterns(kind, list, match, value, remark, enabled, created_at) VALUES(?,?,?,?,?,1,?) ON CONFLICT DO NOTHING`,
			p.Kind, p.List, p.Match, p.Value, p.Remark, now)
		if err != nil {
			return 0, 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			skipped++
			continue
		}
		added++
		room--
	}
	return added, skipped, tx.Commit()
}

func (sv *Service) DeletePattern(ctx context.Context, id int64) error {
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM fw_patterns WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearPatterns empties one list.
func (sv *Service) ClearPatterns(ctx context.Context, kind, list string) (int64, error) {
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM fw_patterns WHERE kind = ? AND list = ?`, kind, list)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// EdgePatterns are the enabled entries, grouped as the edge loads them.
func (sv *Service) EdgePatterns(ctx context.Context) []webenv.PatternList {
	ps, err := sv.Patterns(ctx, "", "")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	by := map[[3]string][]string{}
	for i := len(ps) - 1; i >= 0; i-- { // oldest first
		p := ps[i]
		if p.Enabled {
			k := [3]string{p.Kind, p.List, p.Match}
			by[k] = append(by[k], p.Value)
		}
	}
	var out []webenv.PatternList
	for k, v := range by {
		out = append(out, webenv.PatternList{Kind: k[0], List: k[1], Match: k[2], Values: v})
	}
	return out
}
