package cfdns

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Zone-level security: IP Access Rules (block at Cloudflare's edge) and the
// Security Level ("under_attack" = I'm Under Attack mode).

// AccessNote marks the access rules lmcwhm owns, so others are never touched.
const AccessNote = "lmcwhm"

// Zones lists every zone the token can read.
func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	var all []Zone
	for page := 1; page <= 20; page++ {
		var zones []Zone
		if err := c.do(ctx, http.MethodGet, "/zones?per_page=50&page="+strconv.Itoa(page), nil, &zones); err != nil {
			return nil, err
		}
		all = append(all, zones...)
		if len(zones) < 50 {
			break
		}
	}
	return all, nil
}

func (c *Client) SecurityLevel(ctx context.Context, zoneID string) (string, error) {
	var r struct {
		Value string `json:"value"`
	}
	err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/settings/security_level", nil, &r)
	return r.Value, err
}

// SetSecurityLevel: off | essentially_off | low | medium | high | under_attack.
func (c *Client) SetSecurityLevel(ctx context.Context, zoneID, level string) error {
	return c.do(ctx, http.MethodPatch, "/zones/"+zoneID+"/settings/security_level", map[string]string{"value": level}, nil)
}

type AccessRule struct {
	ID            string `json:"id"`
	Mode          string `json:"mode"`
	Notes         string `json:"notes"`
	Configuration struct {
		Target string `json:"target"`
		Value  string `json:"value"`
	} `json:"configuration"`
}

// accessTarget maps an address or CIDR to Cloudflare's target type. Cloudflare
// accepts IPv4 /16 and /24 and IPv6 /32, /48 and /64 ranges only.
func accessTarget(v string) (string, string, bool) {
	if a, err := netip.ParseAddr(v); err == nil {
		if a.Is4() || a.Is4In6() {
			return "ip", a.Unmap().String(), true
		}
		return "ip6", a.String(), true
	}
	p, err := netip.ParsePrefix(v)
	if err != nil {
		return "", "", false
	}
	b := p.Bits()
	if p.Addr().Is4() && b == 32 || p.Addr().Is6() && b == 128 {
		return accessTarget(p.Addr().String())
	}
	if p.Addr().Is4() && (b == 16 || b == 24) || p.Addr().Is6() && (b == 32 || b == 48 || b == 64) {
		return "ip_range", p.Masked().String(), true
	}
	return "", "", false
}

// AccessRules lists the block rules lmcwhm created in a zone.
func (c *Client) AccessRules(ctx context.Context, zoneID string) ([]AccessRule, error) {
	var out []AccessRule
	for page := 1; page <= 50; page++ {
		var rules []AccessRule
		q := url.Values{"notes": {AccessNote}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
		if err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/firewall/access_rules/rules?"+q.Encode(), nil, &rules); err != nil {
			return nil, err
		}
		for _, r := range rules {
			if r.Notes == AccessNote {
				out = append(out, r)
			}
		}
		if len(rules) < 100 {
			break
		}
	}
	return out, nil
}

// SyncBlocks makes the zone's lmcwhm block rules match ips exactly; addresses
// Cloudflare cannot express (e.g. a /20) are returned as skipped.
func (c *Client) SyncBlocks(ctx context.Context, zoneID string, ips []string) (added, removed int, skipped []string, err error) {
	have, err := c.AccessRules(ctx, zoneID)
	if err != nil {
		return 0, 0, nil, err
	}
	want := map[string]string{}
	for _, ip := range ips {
		if t, v, ok := accessTarget(strings.TrimSpace(ip)); ok {
			want[v] = t
		} else {
			skipped = append(skipped, ip)
		}
	}
	for _, r := range have {
		if _, ok := want[r.Configuration.Value]; ok {
			delete(want, r.Configuration.Value)
			continue
		}
		if err := c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/firewall/access_rules/rules/"+r.ID, nil, nil); err != nil {
			return added, removed, skipped, err
		}
		removed++
	}
	for v, t := range want {
		body := map[string]any{"mode": "block", "notes": AccessNote, "configuration": map[string]string{"target": t, "value": v}}
		if err := c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/firewall/access_rules/rules", body, nil); err != nil {
			return added, removed, skipped, err
		}
		added++
	}
	return added, removed, skipped, nil
}
