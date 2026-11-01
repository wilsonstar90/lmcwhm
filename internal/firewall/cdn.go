package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

// CDNConfig names another CDN (besides Cloudflare, which is built in) whose
// header carries the visitor's address: only its ranges may set it.
type CDNConfig struct {
	Ranges  []string           `json:"ranges"`
	Headers []webenv.CDNHeader `json:"headers"`          // tried in order
	Header  string             `json:"header,omitempty"` // before v0.19: one header
}

const cdnKey = "edge_cdn"

var ErrBadCDN = errors.New("fw.invalid_cdn")

func (sv *Service) CDN(ctx context.Context) CDNConfig {
	c := CDNConfig{Ranges: []string{}}
	var raw string
	if sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, cdnKey).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &c)
	}
	if len(c.Headers) == 0 && c.Header != "" {
		c.Headers = []webenv.CDNHeader{{Name: c.Header}}
	}
	if c.Headers == nil {
		c.Headers = []webenv.CDNHeader{}
	}
	c.Header = ""
	return c
}

func (sv *Service) SaveCDN(ctx context.Context, c CDNConfig) (CDNConfig, error) {
	if len(c.Headers) == 0 && c.Header != "" {
		c.Headers = []webenv.CDNHeader{{Name: c.Header}}
	}
	for i := range c.Headers {
		c.Headers[i].Name, c.Headers[i].Remark = strings.TrimSpace(c.Headers[i].Name), strings.TrimSpace(c.Headers[i].Remark)
	}
	if len(c.Ranges) > 1000 || (len(c.Ranges) > 0 && len(c.Headers) == 0) || !webenv.ValidHeaders(c.Headers) {
		return c, ErrBadCDN
	}
	out := CDNConfig{Ranges: []string{}, Headers: c.Headers}
	if out.Headers == nil {
		out.Headers = []webenv.CDNHeader{}
	}
	seen := map[string]bool{}
	for _, r := range c.Ranges {
		_, norm, err := NormIP(r)
		if err != nil {
			return c, ErrBadCDN
		}
		if !seen[norm] {
			seen[norm] = true
			out.Ranges = append(out.Ranges, norm)
		}
	}
	b, _ := json.Marshal(out)
	_, err := sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, cdnKey, string(b))
	return out, err
}

// CloudflareTrust are the Cloudflare ranges from the trust list (if enabled).
func (sv *Service) CloudflareTrust(ctx context.Context) []string {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT ip FROM fw_ips WHERE list = 'trust' AND source IN ('feed:cloudflare4', 'feed:cloudflare6')`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ip string
		if rows.Scan(&ip) == nil {
			out = append(out, ip)
		}
	}
	return out
}
