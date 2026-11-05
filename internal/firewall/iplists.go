package firewall

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"
)

// ImportIPs adds many manual entries with one ruleset reload; addresses
// covering self (the admin's own) are skipped from the block list.
func (sv *Service) ImportIPs(ctx context.Context, list string, ips []string, ttl time.Duration, remark string, self netip.Addr) (added, skipped int, err error) {
	if list != "block" && list != "allow" {
		return 0, 0, ErrBadIP
	}
	// Read before the transaction: the store has a single connection.
	conflicts := sv.manualConflicts(ctx, list)
	tx, err := sv.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	now := time.Now()
	exp := int64(0)
	if ttl > 0 {
		exp = now.Add(ttl).Unix()
	}
	for _, ip := range ips {
		p, norm, err := NormIP(ip)
		if err != nil || list == "block" && self.IsValid() && p.Contains(self) || conflicts(p, norm) {
			skipped++
			continue
		}
		// A manual entry replaces an automatic one for the same address.
		if _, err := tx.ExecContext(ctx, `INSERT INTO fw_ips(ip, list, source, remark, expires_at, created_at) VALUES(?, ?, 'manual', ?, ?, ?)
			ON CONFLICT(ip, list) DO UPDATE SET source='manual', remark=excluded.remark, expires_at=excluded.expires_at`,
			norm, list, remark, exp, now.Unix()); err != nil {
			return 0, 0, err
		}
		added++
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	if added > 0 {
		if c, _ := sv.Config(ctx); c.Enabled && sv.Available() {
			if _, err := sv.Apply(ctx); err != nil {
				return added, skipped, err
			}
		} else {
			sv.listsChanged()
		}
		sv.blocksChanged()
	}
	return added, skipped, nil
}

// ClearIPs removes every manual entry of list.
func (sv *Service) ClearIPs(ctx context.Context, list string) (int64, error) {
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM fw_ips WHERE list = ? AND source = 'manual'`, list)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if list == "block" {
			err = sv.applyBans(ctx)
		} else if c, _ := sv.Config(ctx); c.Enabled && sv.Available() {
			_, err = sv.Apply(ctx)
		} else {
			sv.listsChanged()
		}
		sv.blocksChanged()
	}
	return n, err
}

// AutoFilter selects entries not added by hand (lists, CrowdSec, AI).
type AutoFilter struct {
	List   string // block | trust | allow | ""
	Source string // "feed:<id>", "feed", "crowdsec", "mcp" or ""
	Query  string
	Offset int
	Limit  int
}

type AutoPage struct {
	Items   []IP           `json:"items"`
	Total   int            `json:"total"`
	Sources map[string]int `json:"sources"` // entries per source (whole lists)
}

func (sv *Service) AutoIPs(ctx context.Context, f AutoFilter) (AutoPage, error) {
	out := AutoPage{Items: []IP{}, Sources: map[string]int{}}
	now := time.Now().Unix()
	where := `source != 'manual' AND (expires_at = 0 OR expires_at > ?)`
	args := []any{now}
	if f.List != "" {
		where += ` AND list = ?`
		args = append(args, f.List)
	}
	switch {
	case f.Source == "feed":
		where += ` AND source LIKE 'feed:%'`
	case f.Source != "":
		where += ` AND source = ?`
		args = append(args, f.Source)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		where += ` AND (ip LIKE ? OR remark LIKE ?)`
		args = append(args, "%"+q+"%", "%"+q+"%")
	}
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM fw_ips WHERE `+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, ip, list, source, remark, expires_at, created_at FROM fw_ips WHERE `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, max(f.Offset, 0))...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var x IP
		if err := rows.Scan(&x.ID, &x.IP, &x.List, &x.Source, &x.Remark, &x.ExpiresAt, &x.CreatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, x)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	srows, err := sv.st.DB.QueryContext(ctx, `SELECT source, COUNT(*) FROM fw_ips WHERE source != 'manual' AND (expires_at = 0 OR expires_at > ?) GROUP BY source`, now)
	if err != nil {
		return out, err
	}
	defer srows.Close()
	for srows.Next() {
		var s string
		var n int
		if srows.Scan(&s, &n) == nil {
			out.Sources[s] = n
		}
	}
	if err := srows.Err(); err != nil {
		return out, err
	}
	srows.Close()
	// Blocks the allow list or the server's own addresses override.
	allowP := sv.self()
	if ips, err := sv.IPs(ctx, "allow"); err == nil {
		for _, x := range ips {
			if p, _, err := NormIP(x.IP); err == nil {
				allowP = append(allowP, p)
			}
		}
	}
	for i, x := range out.Items {
		if p, _, err := NormIP(x.IP); err == nil && x.List == "block" && coveredBy(p, allowP) {
			out.Items[i].Overridden = true
		}
	}
	return out, nil
}

// KeepIP turns an automatic block into a permanent manual one (a list
// refresh or CrowdSec no longer removes it). Trusted ranges cannot be kept:
// as manual allow entries they would open every port.
func (sv *Service) KeepIP(ctx context.Context, id int64) error {
	var list string
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT list FROM fw_ips WHERE id = ?`, id).Scan(&list); err != nil {
		return ErrNotFound
	}
	if list != "block" {
		return ErrBadIP
	}
	if _, err := sv.st.DB.ExecContext(ctx, `UPDATE fw_ips SET source = 'manual', expires_at = 0 WHERE id = ?`, id); err != nil {
		return err
	}
	if c, _ := sv.Config(ctx); c.Enabled && sv.Available() {
		if _, err := sv.Apply(ctx); err != nil {
			return err
		}
	} else {
		sv.listsChanged()
	}
	sv.blocksChanged()
	return nil
}

// ErrDuplicateIP: the edited address is already on the same list.
var ErrDuplicateIP = errors.New("fw.duplicate_ip")

// UpdateIP edits a manual entry: its address, remark and, unless keepTTL,
// its lifetime (ttl 0 = permanent). A block may not cover self (the admin's
// own address). The ruleset is rebuilt from the table.
func (sv *Service) UpdateIP(ctx context.Context, id int64, ip string, ttl time.Duration, keepTTL bool, remark string, self netip.Addr) (IP, error) {
	var x IP
	err := sv.st.DB.QueryRowContext(ctx, `SELECT id, ip, list, source, remark, expires_at, created_at FROM fw_ips WHERE id = ?`, id).
		Scan(&x.ID, &x.IP, &x.List, &x.Source, &x.Remark, &x.ExpiresAt, &x.CreatedAt)
	if err != nil {
		return x, ErrNotFound
	}
	if x.Source != "manual" {
		return x, ErrBadIP // automatic entries are kept first ("keep"), then edited
	}
	pfx, norm, err := NormIP(ip)
	if err != nil {
		return x, err
	}
	if x.List == "block" && self.IsValid() && pfx.Contains(self) {
		return x, ErrSelf
	}
	if norm != x.IP {
		if err := sv.checkManual(ctx, pfx, norm, x.List); err != nil {
			return x, err
		}
	}
	x.IP, x.Remark = norm, trimRemark(remark)
	if !keepTTL {
		x.ExpiresAt = 0
		if ttl > 0 {
			x.ExpiresAt = time.Now().Add(ttl).Unix()
		}
	}
	if _, err := sv.st.DB.ExecContext(ctx, `UPDATE fw_ips SET ip = ?, remark = ?, expires_at = ? WHERE id = ?`, x.IP, x.Remark, x.ExpiresAt, id); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return x, ErrDuplicateIP
		}
		return x, err
	}
	if c, _ := sv.Config(ctx); c.Enabled && sv.Available() {
		if _, err := sv.Apply(ctx); err != nil {
			return x, err
		}
	} else {
		sv.listsChanged()
	}
	sv.blocksChanged()
	return x, nil
}

// SetIPRemark changes only the remark; no rule changes.
func (sv *Service) SetIPRemark(ctx context.Context, id int64, remark string) error {
	res, err := sv.st.DB.ExecContext(ctx, `UPDATE fw_ips SET remark = ? WHERE id = ?`, trimRemark(remark), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPatternRemark changes only a UA / URL entry's remark.
func (sv *Service) SetPatternRemark(ctx context.Context, id int64, remark string) error {
	res, err := sv.st.DB.ExecContext(ctx, `UPDATE fw_patterns SET remark = ? WHERE id = ?`, trimRemark(remark), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func trimRemark(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}
