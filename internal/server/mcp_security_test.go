package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPAuditRedactsSecrets(t *testing.T) {
	args := json.RawMessage(`{"password":"plain-password","nested":{"api_token":"token-value","safe":"visible"},"items":[{"client_secret":"secret-value"}]}`)
	detail := mcpAuditDetail("example", args)
	for _, secret := range []string{"plain-password", "token-value", "secret-value"} {
		if strings.Contains(detail, secret) {
			t.Fatalf("secret %q appeared in audit detail: %s", secret, detail)
		}
	}
	if !strings.Contains(detail, `"safe":"visible"`) || strings.Count(detail, "[REDACTED]") != 3 {
		t.Fatalf("unexpected redaction: %s", detail)
	}
}

func TestMCPAuditShellRecordsOnlyHash(t *testing.T) {
	command := "printf '%s' 'plain-password'"
	detail := mcpAuditDetail("run_shell", json.RawMessage(`{"command":"printf '%s' 'plain-password'","timeout":60}`))
	if strings.Contains(detail, command) || strings.Contains(detail, "plain-password") || !strings.Contains(detail, "command_sha256=") {
		t.Fatalf("shell command leaked into audit detail: %s", detail)
	}
}

func TestMCPAuditCallAPIDropsQuery(t *testing.T) {
	detail := mcpAuditDetail("call_api", json.RawMessage(`{"method":"GET","path":"/settings?token=plain-password","body":{"password":"also-hidden"}}`))
	if strings.Contains(detail, "plain-password") || strings.Contains(detail, "also-hidden") || detail != "call_api GET /settings" {
		t.Fatalf("unsafe API audit detail: %s", detail)
	}
}

func TestTailBufferKeepsBoundedTail(t *testing.T) {
	buf := newTailBuffer(8)
	for _, chunk := range []string{"1234", "5678", "abcdef", "XYZ"} {
		if n, err := buf.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write %q: n=%d err=%v", chunk, n, err)
		}
	}
	if got := buf.String(); got != "[… truncated]\nbcdefXYZ" {
		t.Fatalf("unexpected tail: %q", got)
	}
	if len(buf.buf) != 8 || cap(buf.buf) != 8 {
		t.Fatalf("buffer grew beyond its limit: len=%d cap=%d", len(buf.buf), cap(buf.buf))
	}
}

func TestTailBufferLargeSingleWrite(t *testing.T) {
	buf := newTailBuffer(4)
	if _, err := buf.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "[… truncated]\n6789" {
		t.Fatalf("unexpected tail: %q", got)
	}
}

func TestTailBufferExactLimitIsNotTruncated(t *testing.T) {
	buf := newTailBuffer(4)
	if _, err := buf.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "1234" {
		t.Fatalf("exact-limit output was marked truncated: %q", got)
	}
}
