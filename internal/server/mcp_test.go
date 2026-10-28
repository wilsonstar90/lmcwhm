package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/apitoken"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
)

func (e *env) rpc(token, body string) (int, map[string]any) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	return res.StatusCode, m
}

func TestMCPTokensAndScopes(t *testing.T) {
	// The browser gates (security entry, BasicAuth, bound domain) do not apply to /mcp.
	e := newEnv(t, func(s *settings.Settings) { s.SecurityEntry = "/console"; s.BindDomain = "panel.example.com" })
	ctx := context.Background()
	_, readTok, err := e.srv.Tokens.Create(ctx, apitoken.Input{Name: "ro", Scope: "read"})
	if err != nil {
		t.Fatal(err)
	}
	_, adminTok, _ := e.srv.Tokens.Create(ctx, apitoken.Input{Name: "root", Scope: "admin"})
	_, ipTok, _ := e.srv.Tokens.Create(ctx, apitoken.Input{Name: "ip", Scope: "admin", IPs: []string{"203.0.113.0/24"}})

	if code, _ := e.rpc("", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != 404 {
		t.Fatalf("no token: %d, want 404", code)
	}
	if code, _ := e.rpc("lmc_nope", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != 401 {
		t.Fatalf("bad token: %d", code)
	}
	if code, _ := e.rpc(ipTok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != 401 {
		t.Fatalf("token from a non-allowed IP: %d", code)
	}

	code, m := e.rpc(readTok, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	res, _ := m["result"].(map[string]any)
	if code != 200 || res["protocolVersion"] != "2025-03-26" {
		t.Fatalf("initialize: %d %v", code, m)
	}
	if code, _ := e.rpc(readTok, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Fatalf("notification: %d", code)
	}

	names := func(tok string) map[string]bool {
		_, m := e.rpc(tok, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
		out := map[string]bool{}
		for _, x := range m["result"].(map[string]any)["tools"].([]any) {
			out[x.(map[string]any)["name"].(string)] = true
		}
		return out
	}
	ro, all := names(readTok), names(adminTok)
	if !ro["server_overview"] || ro["run_shell"] || !all["run_shell"] {
		t.Fatalf("scope filtering: read=%v admin has run_shell=%v", ro, all["run_shell"])
	}

	// A read token cannot call an admin tool even by name.
	_, m = e.rpc(readTok, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_shell","arguments":{"command":"id"}}}`)
	if r := m["result"].(map[string]any); r["isError"] != true {
		t.Fatalf("read token ran run_shell: %v", m)
	}
	_, m = e.rpc(adminTok, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"run_shell","arguments":{"command":"echo hi-$((1+2))"}}}`)
	text := m["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "hi-3") || !strings.Contains(text, "[exit 0]") {
		t.Fatalf("run_shell: %q", text)
	}
	// Unknown arguments are rejected.
	_, m = e.rpc(adminTok, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"server_overview","arguments":{"x":1}}}`)
	if m["result"].(map[string]any)["isError"] != true {
		t.Fatalf("extra args accepted: %v", m)
	}
	// Batch + prompts.
	req, _ := http.NewRequest("POST", e.ts.URL+"/mcp", bytes.NewBufferString(`[{"jsonrpc":"2.0","id":6,"method":"prompts/list"},{"jsonrpc":"2.0","id":7,"method":"prompts/get","params":{"name":"diagnose_site","arguments":{"domain":"a.com"}}}]`))
	req.Header.Set("Authorization", "Bearer "+readTok)
	r, _ := e.c.Do(req)
	var batch []map[string]any
	json.NewDecoder(r.Body).Decode(&batch)
	r.Body.Close()
	if len(batch) != 2 || !strings.Contains(mustMarshal(batch[1]), "a.com") {
		t.Fatalf("batch: %v", batch)
	}
	// Tool calls are audited.
	entries, _, _ := e.srv.Audit.List(ctx, 50, 0)
	if len(entries) == 0 || !strings.Contains(mustMarshal(entries), "run_shell") {
		t.Fatal("tool call not audited")
	}
}

func mustMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
