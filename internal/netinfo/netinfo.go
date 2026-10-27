// Package netinfo detects this server's public addresses.
package netinfo

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func fetch(ctx context.Context, url string) string {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	ip := net.ParseIP(strings.TrimSpace(string(b)))
	if ip == nil {
		return ""
	}
	return ip.String()
}

// PublicIPv4 / PublicIPv6 return "" when the address family is unavailable.
func PublicIPv4(ctx context.Context) string { return fetch(ctx, "https://api.ipify.org") }
func PublicIPv6(ctx context.Context) string { return fetch(ctx, "https://api6.ipify.org") }
