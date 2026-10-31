package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/wilsonstar90/lmcwhm/internal/mcp"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestAskToolLoop(t *testing.T) {
	var bodies []map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(b, &req)
		bodies = append(bodies, req)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			io.WriteString(w, `{"id":"m1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"tool_use",
				"content":[{"type":"tool_use","id":"tu1","name":"probe","input":{"n":2}},{"type":"tool_use","id":"tu2","name":"secret_admin","input":{}}],
				"usage":{"input_tokens":10,"output_tokens":5}}`)
			return
		}
		io.WriteString(w, `{"id":"m2","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn",
			"content":[{"type":"text","text":"All good."}],"usage":{"input_tokens":20,"output_tokens":3}}`)
	}))
	defer fake.Close()

	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	box, _ := secrets.Open(dir)
	srv := mcp.NewServer("t", "1", "")
	srv.AddTool(mcp.Tool{Name: "probe", Scope: mcp.ScopeRead, Schema: mcp.Object(map[string]any{"n": mcp.Int("n", 0, 9)}, "n"),
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) { return "probe-result-" + string(raw), nil }})
	srv.AddTool(mcp.Tool{Name: "secret_admin", Scope: mcp.ScopeAdmin,
		Handler: func(context.Context, json.RawMessage) (any, error) { t.Fatal("admin tool ran"); return nil, nil }})
	sv := New(st, box, srv)
	sv.newClient = func(cfg Config) anthropic.Client {
		return anthropic.NewClient(option.WithAPIKey(cfg.APIKey), option.WithBaseURL(fake.URL), option.WithMaxRetries(0))
	}
	if _, err := sv.Ask(context.Background(), []Turn{{Role: "user", Text: "hi"}}, "en", mcp.Caller{}); err != ErrNoKey {
		t.Fatalf("want ErrNoKey, got %v", err)
	}
	if err := sv.Save(context.Background(), Config{APIKey: "sk-test"}, false); err != nil {
		t.Fatal(err)
	}
	ans, err := sv.Ask(context.Background(), []Turn{{Role: "user", Text: "check"}}, "en", mcp.Caller{Scope: mcp.ScopeAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if ans.Text != "All good." || len(ans.Steps) != 2 || ans.Steps[1].Error == "" || ans.Usage.Input != 30 {
		t.Fatalf("answer: %+v", ans)
	}
	// Only read tools are offered; the request uses the configured model and fallbacks.
	first := bodies[0]
	if first["model"] != DefaultModel || first["fallbacks"] != "default" || len(first["tools"].([]any)) != 1 {
		t.Fatalf("request: model=%v fallbacks=%v tools=%v", first["model"], first["fallbacks"], first["tools"])
	}
	// Second request carries both tool results in one user turn.
	msgs := bodies[1]["messages"].([]any)
	last := mustJSON(msgs[len(msgs)-1])
	if !strings.Contains(last, "probe-result-") || strings.Count(last, "tool_result") != 2 {
		t.Fatalf("tool results: %s", last)
	}
	// The key is sealed at rest.
	var raw string
	st.DB.QueryRow(`SELECT value FROM settings WHERE key = 'ai'`).Scan(&raw)
	if strings.Contains(raw, "sk-test") {
		t.Fatal("api key stored in clear")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestOpenRouter(t *testing.T) {
	var got *http.Request
	var body map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"m1","type":"message","role":"assistant","model":"openai/gpt-5","stop_reason":"end_turn",
			"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer fake.Close()
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	box, _ := secrets.Open(dir)
	sv := New(st, box, mcp.NewServer("t", "1", ""))
	real := sv.newClient
	sv.newClient = func(cfg Config) anthropic.Client { cfg.BaseURL = fake.URL; return real(cfg) }
	if err := sv.Save(context.Background(), Config{Provider: "other", APIKey: "k"}, false); err != ErrConfig {
		t.Fatalf("unknown provider: %v", err)
	}
	if err := sv.Save(context.Background(), Config{Provider: ProviderOpenRouter, APIKey: "or-key", Model: "openai/gpt-5"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := sv.Ask(context.Background(), []Turn{{Role: "user", Text: "hi"}}, "en", mcp.Caller{}); err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("Authorization") != "Bearer or-key" || got.Header.Get("X-Api-Key") != "" || got.URL.Path != "/v1/messages" {
		t.Fatalf("auth %q / %q, path %s", got.Header.Get("Authorization"), got.Header.Get("X-Api-Key"), got.URL.Path)
	}
	if _, ok := body["output_config"]; ok || got.Header.Get("Anthropic-Beta") != "" {
		t.Fatalf("Anthropic-only options sent to a non-Claude model: %v %q", body["output_config"], got.Header.Get("Anthropic-Beta"))
	}
	// Switching provider without a key drops the old one.
	sv.Save(context.Background(), Config{Provider: ProviderAnthropic}, false)
	if c, _ := sv.Config(context.Background()); c.APIKey != "" {
		t.Fatal("OpenRouter key kept for Anthropic")
	}
}
