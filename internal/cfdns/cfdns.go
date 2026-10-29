// Package cfdns is a small Cloudflare API v4 client for zone lookup and
// A / AAAA record management.
package cfdns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var BaseURL = "https://api.cloudflare.com/client/v4"

var (
	ErrNoToken = errors.New("cf.no_token")
	ErrNoZone  = errors.New("cf.zone_not_found")
)

type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 20 * time.Second}}
}

type apiResp struct {
	Success bool            `json:"success"`
	Errors  []apiErr        `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
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
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: %w", err)
	}
	defer resp.Body.Close()
	var r apiResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return fmt.Errorf("cloudflare: HTTP %d", resp.StatusCode)
	}
	if !r.Success {
		var msgs []string
		denied := resp.StatusCode == http.StatusForbidden
		for _, e := range r.Errors {
			msgs = append(msgs, fmt.Sprintf("%d %s", e.Code, e.Message))
			denied = denied || e.Code == 9109 || e.Code == 10000
		}
		msg := "cloudflare: " + strings.Join(msgs, "; ")
		if denied {
			msg += " — " + permissionHint(path)
		}
		return errors.New(msg)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// permissionHint names the API token permission an endpoint needs.
func permissionHint(path string) string {
	switch {
	case strings.Contains(path, "/firewall/access_rules"):
		return `the token needs the "Zone → Firewall Services → Edit" permission`
	case strings.Contains(path, "/settings/"):
		return `the token needs the "Zone → Zone Settings → Edit" permission`
	case strings.Contains(path, "/dns_records"):
		return `the token needs the "Zone → DNS → Edit" permission`
	case strings.HasPrefix(path, "/zones"):
		return `the token needs the "Zone → Zone → Read" permission`
	}
	return "the token lacks a permission for " + path
}

// Verify checks that the token is valid and active.
func (c *Client) Verify(ctx context.Context) error {
	var r struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, &r); err != nil {
		return err
	}
	if r.Status != "active" {
		return fmt.Errorf("cloudflare: token status %s", r.Status)
	}
	return nil
}

type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ZoneFor finds the zone that contains host by trying each parent domain.
func (c *Client) ZoneFor(ctx context.Context, host string) (Zone, error) {
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(host), "."), ".")
	for i := 0; i < len(labels)-1; i++ {
		name := strings.Join(labels[i:], ".")
		var zones []Zone
		if err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(name), nil, &zones); err != nil {
			return Zone{}, err
		}
		if len(zones) > 0 {
			return zones[0], nil
		}
	}
	return Zone{}, ErrNoZone
}

type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment,omitempty"`
}

func (c *Client) records(ctx context.Context, zoneID, typ, name string) ([]Record, error) {
	var recs []Record
	q := url.Values{"type": {typ}, "name": {name}}
	err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?"+q.Encode(), nil, &recs)
	return recs, err
}

// Upsert makes name have exactly one record of typ pointing at content.
func (c *Client) Upsert(ctx context.Context, zoneID, typ, name, content string, proxied bool) error {
	recs, err := c.records(ctx, zoneID, typ, name)
	if err != nil {
		return err
	}
	rec := Record{Type: typ, Name: name, Content: content, TTL: 1, Proxied: proxied, Comment: "managed by lmcwhm"}
	if len(recs) == 0 {
		return c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", rec, nil)
	}
	if err := c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+recs[0].ID, rec, nil); err != nil {
		return err
	}
	for _, extra := range recs[1:] {
		if err := c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+extra.ID, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes records of typ for name that point at content (others are left alone).
func (c *Client) Delete(ctx context.Context, zoneID, typ, name, content string) error {
	recs, err := c.records(ctx, zoneID, typ, name)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if content == "" || r.Content == content {
			if err := c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+r.ID, nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
