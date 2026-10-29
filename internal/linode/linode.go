// Package linode manages Linode Cloud Firewalls (network-level filtering in
// front of the VM) through the Linode API v4.
package linode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

var BaseURL = "https://api.linode.com/v4"

var ErrNoToken = errors.New("linode.no_token")

// Managed rule labels: lmcwhm only ever rewrites rules with these labels.
const (
	BlockLabel = "lmcwhm-block"
	AllowLabel = "lmcwhm-allow"
	PortsLabel = "lmcwhm-ports"
	// A Linode rule has one protocol: blocks and allows get a UDP twin.
	BlockUDPLabel = "lmcwhm-block-udp"
	AllowUDPLabel = "lmcwhm-allow-udp"
	// MaxRules is Linode's limit of inbound rules per firewall.
	MaxRules = 25
	// MaxAddresses is Linode's limit of addresses per rule.
	MaxAddresses = 255
)

type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 20 * time.Second}}
}

// scopeHint names the Personal Access Token scope an endpoint needs.
func scopeHint(path string) string {
	switch {
	case strings.HasPrefix(path, "/networking/firewalls"):
		return `the token needs the "Firewalls: Read/Write" scope`
	case strings.HasPrefix(path, "/linode/"):
		return `the token needs the "Linodes: Read/Write" scope`
	}
	return "the token lacks a scope for " + path
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	if c.token == "" {
		return ErrNoToken
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("linode: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Errors []struct {
				Field  string `json:"field"`
				Reason string `json:"reason"`
			} `json:"errors"`
		}
		json.Unmarshal(b, &e)
		var msgs []string
		for _, x := range e.Errors {
			msgs = append(msgs, strings.TrimSpace(x.Field+" "+x.Reason))
		}
		msg := fmt.Sprintf("linode: HTTP %d %s", res.StatusCode, strings.Join(msgs, "; "))
		if (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden) && strings.Contains(msg, "not authorized") {
			msg += " — " + scopeHint(path)
		}
		return errors.New(msg)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type Addresses struct {
	IPv4 []string `json:"ipv4,omitempty"`
	IPv6 []string `json:"ipv6,omitempty"`
}

type Rule struct {
	Action      string    `json:"action"`   // ACCEPT | DROP
	Protocol    string    `json:"protocol"` // TCP | UDP | ICMP | IPENCAP
	Ports       string    `json:"ports,omitempty"`
	Addresses   Addresses `json:"addresses"`
	Label       string    `json:"label,omitempty"`
	Description string    `json:"description,omitempty"`
}

type Rules struct {
	Inbound        []Rule `json:"inbound"`
	InboundPolicy  string `json:"inbound_policy"`
	Outbound       []Rule `json:"outbound"`
	OutboundPolicy string `json:"outbound_policy"`
}

type Entity struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

type Firewall struct {
	ID       int      `json:"id"`
	Label    string   `json:"label"`
	Status   string   `json:"status"`
	Rules    Rules    `json:"rules"`
	Entities []Entity `json:"entities"`
}

type page[T any] struct {
	Data  []T `json:"data"`
	Pages int `json:"pages"`
}

func list[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	for p := 1; p <= 50; p++ {
		var pg page[T]
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s%spage=%d&page_size=100", path, sep, p), nil, &pg); err != nil {
			return nil, err
		}
		all = append(all, pg.Data...)
		if p >= pg.Pages {
			break
		}
	}
	return all, nil
}

func (c *Client) Firewalls(ctx context.Context) ([]Firewall, error) {
	return list[Firewall](ctx, c, "/networking/firewalls")
}

func (c *Client) Rules(ctx context.Context, id int) (Rules, error) {
	var r Rules
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/networking/firewalls/%d/rules", id), nil, &r)
	return r, err
}

func (c *Client) PutRules(ctx context.Context, id int, r Rules) error {
	if r.Inbound == nil {
		r.Inbound = []Rule{}
	}
	if r.Outbound == nil {
		r.Outbound = []Rule{}
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/networking/firewalls/%d/rules", id), r, nil)
}

type Instance struct {
	ID    int      `json:"id"`
	Label string   `json:"label"`
	IPv4  []string `json:"ipv4"`
	IPv6  string   `json:"ipv6"` // SLAAC address with prefix length
}

// InstanceByIP finds this server's Linode by one of its addresses.
func (c *Client) InstanceByIP(ctx context.Context, ips ...string) (Instance, error) {
	all, err := list[Instance](ctx, c, "/linode/instances")
	if err != nil {
		return Instance{}, err
	}
	want := map[netip.Addr]bool{}
	var tried []string
	for _, ip := range ips {
		if a, err := netip.ParseAddr(strings.TrimSpace(ip)); err == nil && !want[a.Unmap()] {
			want[a.Unmap()] = true
			tried = append(tried, a.Unmap().String())
		}
	}
	for _, in := range all {
		addrs := append([]string{}, in.IPv4...)
		if v6, _, _ := strings.Cut(in.IPv6, "/"); v6 != "" {
			addrs = append(addrs, v6)
		}
		for _, s := range addrs {
			if a, err := netip.ParseAddr(s); err == nil && want[a.Unmap()] {
				return in, nil
			}
		}
	}
	return Instance{}, fmt.Errorf("this server's Linode was not found by its IP (tried %s among %d Linodes on this account)", strings.Join(tried, ", "), len(all))
}

func (c *Client) Attach(ctx context.Context, firewallID, linodeID int) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/networking/firewalls/%d/devices", firewallID), map[string]any{"id": linodeID, "type": "linode"}, nil)
}

// split sorts addresses into Linode's CIDR form (a bare IP becomes /32 or /128).
func split(ips []string) Addresses {
	var a Addresses
	for _, ip := range ips {
		p, err := netip.ParsePrefix(ip)
		if err != nil {
			addr, err := netip.ParseAddr(ip)
			if err != nil {
				continue
			}
			p = netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen())
		}
		if p.Addr().Is4() {
			a.IPv4 = append(a.IPv4, p.Masked().String())
		} else {
			a.IPv6 = append(a.IPv6, p.Masked().String())
		}
	}
	return a
}

// ErrTooManyRules: lmcwhm's rules and the firewall's own exceed MaxRules.
var ErrTooManyRules = errors.New("linode: the firewall would have more than 25 inbound rules; remove some of its other rules")

func managed(label string) bool {
	switch label {
	case BlockLabel, AllowLabel, PortsLabel, BlockUDPLabel, AllowUDPLabel:
		return true
	}
	return false
}

// ApplyManaged rewrites lmcwhm's own inbound rules and leaves every other
// rule as it is. Linode stops at the first matching rule, so they go first,
// in the server's order: ACCEPT rules (all ports, TCP and UDP) for allowed
// addresses, DROP rules for blocked ones, then an optional ACCEPT rule for
// the system ports. An address on both lists is allowed, as on the server.
// Addresses beyond Linode's per-rule limit are returned as overflow.
func ApplyManaged(r Rules, blocked, allowed []string, ports string) (Rules, []string, error) {
	allowSet := map[string]bool{}
	for _, a := range allowed {
		allowSet[a] = true
	}
	var b []string
	for _, x := range blocked {
		if !allowSet[x] {
			b = append(b, x)
		}
	}
	blocked = b
	var overflow []string
	if len(blocked) > MaxAddresses {
		blocked, overflow = blocked[:MaxAddresses], blocked[MaxAddresses:]
	}
	if len(allowed) > MaxAddresses {
		overflow = append(overflow, allowed[MaxAddresses:]...)
		allowed = allowed[:MaxAddresses]
	}
	var kept []Rule
	for _, x := range r.Inbound {
		if !managed(x.Label) {
			kept = append(kept, x)
		}
	}
	var head []Rule
	if len(allowed) > 0 {
		head = append(head, Rule{Action: "ACCEPT", Protocol: "TCP", Addresses: split(allowed), Label: AllowLabel, Description: "Allowed by lmcwhm"},
			Rule{Action: "ACCEPT", Protocol: "UDP", Addresses: split(allowed), Label: AllowUDPLabel, Description: "Allowed by lmcwhm"})
	}
	if len(blocked) > 0 {
		head = append(head, Rule{Action: "DROP", Protocol: "TCP", Addresses: split(blocked), Label: BlockLabel, Description: "Blocked by lmcwhm"},
			Rule{Action: "DROP", Protocol: "UDP", Addresses: split(blocked), Label: BlockUDPLabel, Description: "Blocked by lmcwhm"})
	}
	if ports != "" {
		head = append(head, Rule{Action: "ACCEPT", Protocol: "TCP", Ports: ports, Addresses: Addresses{IPv4: []string{"0.0.0.0/0"}, IPv6: []string{"::/0"}},
			Label: PortsLabel, Description: "Panel, SSH and web ports (lmcwhm)"})
	}
	if len(head)+len(kept) > MaxRules {
		return r, overflow, ErrTooManyRules
	}
	r.Inbound = append(head, kept...)
	if r.InboundPolicy == "" {
		r.InboundPolicy = "ACCEPT"
	}
	if r.OutboundPolicy == "" {
		r.OutboundPolicy = "ACCEPT"
	}
	return r, overflow, nil
}
