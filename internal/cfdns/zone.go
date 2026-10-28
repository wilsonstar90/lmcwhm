package cfdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// DNS Zone Editor (cPanel Zone Editor) over Cloudflare: every record type the
// panel edits, and templates that add a provider's records in one go.

var ErrRecord = errors.New("dns.invalid_record")

// FullRecord is a DNS record with the type-specific fields Cloudflare uses.
type FullRecord struct {
	ID        string         `json:"id,omitempty"`
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Content   string         `json:"content,omitempty"`
	TTL       int            `json:"ttl"` // 1 = automatic
	Proxied   bool           `json:"proxied"`
	Priority  *int           `json:"priority,omitempty"` // MX
	Data      map[string]any `json:"data,omitempty"`     // SRV, CAA
	Comment   string         `json:"comment,omitempty"`
	Proxiable bool           `json:"proxiable,omitempty"`
}

// RecordTypes are the types the editor offers.
var RecordTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "SRV", "CAA", "NS"}

func (c *Client) Records(ctx context.Context, zoneID string) ([]FullRecord, error) {
	var all []FullRecord
	for page := 1; page <= 40; page++ {
		var rs []FullRecord
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/zones/%s/dns_records?per_page=500&page=%d", url.PathEscape(zoneID), page), nil, &rs); err != nil {
			return nil, err
		}
		all = append(all, rs...)
		if len(rs) < 500 {
			break
		}
	}
	if all == nil {
		all = []FullRecord{}
	}
	return all, nil
}

var (
	nameRe  = regexp.MustCompile(`^(\*\.)?([A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?\.)*[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.?$`)
	caaTags = map[string]bool{"issue": true, "issuewild": true, "iodef": true}
)

// Check validates a record before it is sent to Cloudflare.
func (r *FullRecord) Check() error {
	r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
	r.Name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(r.Name)), ".")
	r.Content = strings.TrimSpace(r.Content)
	if r.TTL != 1 && (r.TTL < 60 || r.TTL > 86400) {
		r.TTL = 1
	}
	if r.Name == "" || len(r.Name) > 253 || !nameRe.MatchString(r.Name) {
		return fmt.Errorf("%w: name", ErrRecord)
	}
	proxiable := false
	switch r.Type {
	case "A":
		ip := net.ParseIP(r.Content)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("%w: IPv4", ErrRecord)
		}
		proxiable = true
	case "AAAA":
		ip := net.ParseIP(r.Content)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("%w: IPv6", ErrRecord)
		}
		proxiable = true
	case "CNAME":
		if !nameRe.MatchString(r.Content) {
			return fmt.Errorf("%w: target", ErrRecord)
		}
		proxiable = true
	case "NS":
		if !nameRe.MatchString(r.Content) {
			return fmt.Errorf("%w: target", ErrRecord)
		}
	case "MX":
		if !nameRe.MatchString(r.Content) {
			return fmt.Errorf("%w: mail server", ErrRecord)
		}
		if r.Priority == nil || *r.Priority < 0 || *r.Priority > 65535 {
			return fmt.Errorf("%w: priority", ErrRecord)
		}
	case "TXT":
		if r.Content == "" || len(r.Content) > 4096 {
			return fmt.Errorf("%w: text", ErrRecord)
		}
	case "SRV":
		d := r.Data
		for _, k := range []string{"priority", "weight", "port"} {
			n, ok := num(d[k])
			if !ok || n < 0 || n > 65535 {
				return fmt.Errorf("%w: %s", ErrRecord, k)
			}
			d[k] = n
		}
		t, _ := d["target"].(string)
		if !nameRe.MatchString(t) && t != "." {
			return fmt.Errorf("%w: target", ErrRecord)
		}
		r.Data = map[string]any{"priority": d["priority"], "weight": d["weight"], "port": d["port"], "target": t}
		r.Content = ""
	case "CAA":
		d := r.Data
		flags, ok := num(d["flags"])
		tag, _ := d["tag"].(string)
		val, _ := d["value"].(string)
		if !ok || flags < 0 || flags > 255 || !caaTags[tag] || val == "" {
			return fmt.Errorf("%w: CAA", ErrRecord)
		}
		r.Data = map[string]any{"flags": flags, "tag": tag, "value": val}
		r.Content = ""
	default:
		return fmt.Errorf("%w: type", ErrRecord)
	}
	if !proxiable {
		r.Proxied = false
	}
	if r.Type != "MX" {
		r.Priority = nil
	}
	if r.Type != "SRV" && r.Type != "CAA" {
		r.Data = nil
	}
	r.Proxiable = false
	return nil
}

func num(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), x == float64(int(x))
	case int:
		return x, true
	case string:
		n, err := strconv.Atoi(x)
		return n, err == nil
	}
	return 0, false
}

func (c *Client) CreateRecord(ctx context.Context, zoneID string, r FullRecord) (FullRecord, error) {
	if err := r.Check(); err != nil {
		return r, err
	}
	var out FullRecord
	err := c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", r, &out)
	return out, err
}

func (c *Client) UpdateRecord(ctx context.Context, zoneID, id string, r FullRecord) (FullRecord, error) {
	if err := r.Check(); err != nil {
		return r, err
	}
	r.ID = ""
	var out FullRecord
	err := c.do(ctx, http.MethodPut, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(id), r, &out)
	return out, err
}

func (c *Client) DeleteRecord(ctx context.Context, zoneID, id string) error {
	return c.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(id), nil, nil)
}

// ---- templates ----

// Template adds a set of records; {domain}, {ip4}, {ip6}, {mailhost} and
// {dashed} (example-com) are filled in.
type Template struct {
	Key     string       `json:"key"`
	Name    string       `json:"name"`
	Params  []string     `json:"params,omitempty"` // extra inputs the template needs
	Records []FullRecord `json:"records"`
}

func prio(n int) *int { return &n }

var Templates = []Template{
	{Key: "google_workspace", Name: "Google Workspace", Records: []FullRecord{
		{Type: "MX", Name: "{domain}", Content: "smtp.google.com", Priority: prio(1)},
		{Type: "TXT", Name: "{domain}", Content: "v=spf1 include:_spf.google.com ~all"},
		{Type: "TXT", Name: "_dmarc.{domain}", Content: "v=DMARC1; p=quarantine; adkim=r; aspf=r"},
	}},
	{Key: "microsoft365", Name: "Microsoft 365", Records: []FullRecord{
		{Type: "MX", Name: "{domain}", Content: "{dashed}.mail.protection.outlook.com", Priority: prio(0)},
		{Type: "TXT", Name: "{domain}", Content: "v=spf1 include:spf.protection.outlook.com -all"},
		{Type: "CNAME", Name: "autodiscover.{domain}", Content: "autodiscover.outlook.com"},
		{Type: "SRV", Name: "_sip._tls.{domain}", Data: map[string]any{"priority": 100, "weight": 1, "port": 443, "target": "sipdir.online.lync.com"}},
		{Type: "SRV", Name: "_sipfederationtls._tcp.{domain}", Data: map[string]any{"priority": 100, "weight": 1, "port": 5061, "target": "sipfed.online.lync.com"}},
		{Type: "TXT", Name: "_dmarc.{domain}", Content: "v=DMARC1; p=quarantine; adkim=r; aspf=r"},
	}},
	{Key: "zoho", Name: "Zoho Mail", Records: []FullRecord{
		{Type: "MX", Name: "{domain}", Content: "mx.zoho.com", Priority: prio(10)},
		{Type: "MX", Name: "{domain}", Content: "mx2.zoho.com", Priority: prio(20)},
		{Type: "MX", Name: "{domain}", Content: "mx3.zoho.com", Priority: prio(50)},
		{Type: "TXT", Name: "{domain}", Content: "v=spf1 include:zohomail.com ~all"},
	}},
	{Key: "self_mail", Name: "This server's mail (Mail page)", Params: []string{"mailhost"}, Records: []FullRecord{
		{Type: "A", Name: "{mailhost}", Content: "{ip4}"},
		{Type: "AAAA", Name: "{mailhost}", Content: "{ip6}"},
		{Type: "MX", Name: "{domain}", Content: "{mailhost}", Priority: prio(10)},
		{Type: "TXT", Name: "{domain}", Content: "v=spf1 mx a:{mailhost} ~all"},
		{Type: "TXT", Name: "_dmarc.{domain}", Content: "v=DMARC1; p=quarantine; rua=mailto:postmaster@{domain}; adkim=s; aspf=s"},
	}},
	{Key: "caa_letsencrypt", Name: "CAA: Let's Encrypt only", Records: []FullRecord{
		{Type: "CAA", Name: "{domain}", Data: map[string]any{"flags": 0, "tag": "issue", "value": "letsencrypt.org"}},
		{Type: "CAA", Name: "{domain}", Data: map[string]any{"flags": 0, "tag": "issuewild", "value": "letsencrypt.org"}},
	}},
	{Key: "dmarc", Name: "DMARC (quarantine)", Records: []FullRecord{
		{Type: "TXT", Name: "_dmarc.{domain}", Content: "v=DMARC1; p=quarantine; rua=mailto:postmaster@{domain}"},
	}},
	{Key: "no_mail", Name: "This domain sends no mail", Records: []FullRecord{
		{Type: "TXT", Name: "{domain}", Content: "v=spf1 -all"},
		{Type: "TXT", Name: "_dmarc.{domain}", Content: "v=DMARC1; p=reject"},
		{Type: "TXT", Name: "*._domainkey.{domain}", Content: "v=DKIM1; p="},
	}},
}

// Expand fills a template's placeholders; records whose value stays empty
// (no IPv6 address, say) are dropped.
func Expand(t Template, vars map[string]string) []FullRecord {
	vars["dashed"] = strings.ReplaceAll(vars["domain"], ".", "-")
	repl := func(s string) string {
		for k, v := range vars {
			s = strings.ReplaceAll(s, "{"+k+"}", v)
		}
		return s
	}
	var out []FullRecord
	for _, r := range t.Records {
		x := r
		x.Name, x.Content, x.TTL = repl(r.Name), repl(r.Content), 1
		if r.Data != nil {
			x.Data = map[string]any{}
			for k, v := range r.Data {
				if s, ok := v.(string); ok {
					v = repl(s)
				}
				x.Data[k] = v
			}
		}
		if strings.Contains(x.Name+x.Content, "{") || (x.Content == "" && x.Data == nil) {
			continue
		}
		x.Comment = "lmcwhm template " + t.Key
		out = append(out, x)
	}
	return out
}

// ApplyTemplate adds the records; an existing record with the same type,
// name and value is skipped, and for SPF (v=spf1) and DMARC the existing
// record is replaced because a name may only have one.
func (c *Client) ApplyTemplate(ctx context.Context, zoneID string, recs []FullRecord) (added int, err error) {
	cur, err := c.Records(ctx, zoneID)
	if err != nil {
		return 0, err
	}
	single := func(r FullRecord) string {
		if r.Type != "TXT" {
			return ""
		}
		for _, p := range []string{"v=spf1", "v=DMARC1"} {
			if strings.HasPrefix(strings.Trim(r.Content, `"`), p) {
				return p
			}
		}
		return ""
	}
	for _, r := range recs {
		if err := r.Check(); err != nil {
			return added, err
		}
		dup := false
		for _, e := range cur {
			if e.Type != r.Type || e.Name != r.Name {
				continue
			}
			if strings.Trim(e.Content, `"`) == r.Content && r.Data == nil {
				dup = true
				break
			}
			if p := single(r); p != "" && single(e) == p {
				if _, err := c.UpdateRecord(ctx, zoneID, e.ID, r); err != nil {
					return added, err
				}
				dup = true
				added++
				break
			}
		}
		if dup {
			continue
		}
		if _, err := c.CreateRecord(ctx, zoneID, r); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}
