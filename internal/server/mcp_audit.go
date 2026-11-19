package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func mcpAuditDetail(tool string, args json.RawMessage) string {
	if tool == "run_shell" {
		var in struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool + " [invalid arguments]"
		}
		sum := sha256.Sum256([]byte(in.Command))
		return tool + " command_sha256=" + hex.EncodeToString(sum[:8])
	}
	if tool == "call_api" {
		var in struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return tool + " [invalid arguments]"
		}
		path, _, _ := strings.Cut(in.Path, "?")
		return strings.TrimSpace(tool + " " + in.Method + " " + path)
	}
	var value any
	if err := json.Unmarshal(args, &value); err != nil {
		return tool + " [unreadable arguments]"
	}
	redacted, err := json.Marshal(redactMCPValue(value))
	if err != nil {
		return tool + " [unreadable arguments]"
	}
	return tool + " " + string(redacted)
}

func redactMCPValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if sensitiveMCPKey(key) {
				out[key] = "[REDACTED]"
			} else {
				out[key] = redactMCPValue(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactMCPValue(item)
		}
		return out
	default:
		return value
	}
}

func sensitiveMCPKey(key string) bool {
	var normalized strings.Builder
	for _, r := range strings.ToLower(key) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			normalized.WriteRune(r)
		}
	}
	k := normalized.String()
	return strings.Contains(k, "password") || strings.Contains(k, "passwd") ||
		strings.Contains(k, "token") || strings.Contains(k, "secret") ||
		strings.Contains(k, "credential") || strings.Contains(k, "authorization") ||
		k == "key" || k == "apikey" || k == "privatekey" || k == "accesskey"
}
