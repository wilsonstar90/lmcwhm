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

func (e *env) mcpTool(token, name string, args any) map[string]any {
	e.t.Helper()
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 100, "method": "tools/call", "params": json.RawMessage(params)})
	_, m := e.rpc(token, string(body))
	result, ok := m["result"].(map[string]any)
	if !ok {
		e.t.Fatalf("tool %s: %v", name, m)
	}
	return result
}

func mcpStructured(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	v, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structured content: %v", result)
	}
	return v
}

func TestMCPFullAPIAccess(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	_, readTok, _ := e.srv.Tokens.Create(ctx, apitoken.Input{Name: "reader", Scope: "read"})
	_, opTok, _ := e.srv.Tokens.Create(ctx, apitoken.Input{Name: "operator", Scope: "operate"})
	_, adminTok, _ := e.srv.Tokens.Create(ctx, apitoken.Input{Name: "administrator", Scope: "admin"})

	readRoutes := mcpStructured(t, e.mcpTool(readTok, "list_api_endpoints", map[string]any{}))
	adminRoutes := mcpStructured(t, e.mcpTool(adminTok, "list_api_endpoints", map[string]any{"module": "panel"}))
	if readRoutes["count"].(float64) == 0 || adminRoutes["count"].(float64) == 0 {
		t.Fatalf("routes missing: read=%v admin=%v", readRoutes, adminRoutes)
	}
	readJSON, adminJSON := mustMarshal(readRoutes), mustMarshal(adminRoutes)
	if !strings.Contains(readJSON, "/api/v1/system/summary") || strings.Contains(readJSON, "/api/v1/settings") {
		t.Fatalf("read route filtering: %s", readJSON)
	}
	if !strings.Contains(adminJSON, "/api/v1/settings") {
		t.Fatalf("admin routes missing: %s", adminJSON)
	}

	result := e.mcpTool(readTok, "call_api", map[string]any{"path": "/system/summary"})
	if got := mcpStructured(t, result)["status"]; got != float64(http.StatusOK) {
		t.Fatalf("read GET: %v", result)
	}
	result = e.mcpTool(readTok, "call_api", map[string]any{"path": "/logs/operation", "query": map[string]string{"limit": "1"}})
	if got := mcpStructured(t, result)["status"]; got != float64(http.StatusOK) {
		t.Fatalf("GET with query: %v", result)
	}
	for _, tc := range []struct {
		token, method, path string
	}{
		{readTok, "PUT", "/settings"},
		{readTok, "GET", "/settings"},
		{opTok, "GET", "/settings"},
	} {
		result = e.mcpTool(tc.token, "call_api", map[string]any{"method": tc.method, "path": tc.path, "body": map[string]any{}})
		if result["isError"] != true {
			t.Fatalf("%s %s was not denied: %v", tc.method, tc.path, result)
		}
	}

	result = e.mcpTool(adminTok, "call_api", map[string]any{
		"method": "PUT", "path": "/settings",
		"body": map[string]any{"panel_alias": "via-mcp", "basic_auth_password": "do-not-log"},
	})
	if got := mcpStructured(t, result)["status"]; got != float64(http.StatusOK) {
		t.Fatalf("admin update: %v", result)
	}
	if got := e.srv.Settings.Get().PanelAlias; got != "via-mcp" {
		t.Fatalf("real handler did not save settings: %q", got)
	}
	entries, _, _ := e.srv.Audit.List(ctx, 100, 0)
	if audit := mustMarshal(entries); strings.Contains(audit, "do-not-log") || !strings.Contains(audit, "call_api PUT /settings") {
		t.Fatalf("unsafe or missing API audit: %s", audit)
	}

	result = e.mcpTool(adminTok, "call_api", map[string]any{"path": "/../settings"})
	if result["isError"] != true {
		t.Fatalf("path traversal accepted: %v", result)
	}

	_, m := e.rpc(readTok, `{"jsonrpc":"2.0","id":101,"method":"tools/list"}`)
	for _, item := range m["result"].(map[string]any)["tools"].([]any) {
		tool := item.(map[string]any)
		if tool["name"] == "call_api" {
			annotations := tool["annotations"].(map[string]any)
			if annotations["readOnlyHint"] != false || annotations["destructiveHint"] != true {
				t.Fatalf("call_api annotations: %v", annotations)
			}
			return
		}
	}
	t.Fatal("call_api not listed")
}
