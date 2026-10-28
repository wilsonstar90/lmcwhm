// Package ai is the panel's built-in assistant (AI 一鍵分析): Claude reads
// panel data through the same read-only tools the MCP server exposes and
// explains logs, security events and errors.
package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"github.com/wilsonstar90/lmcwhm/internal/mcp"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

const (
	DefaultModel = "claude-opus-5-5"
	settingsKey  = "ai"
	maxSteps     = 12
)

var (
	ErrNoKey   = errors.New("ai.no_key")
	ErrRefused = errors.New("ai.refused")
	ErrConfig  = errors.New("ai.invalid_config")
	efforts    = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}
)

// Config is stored in the settings table; the key is sealed.
type Config struct {
	APIKey  string `json:"api_key,omitempty"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
	BaseURL string `json:"base_url,omitempty"` // optional gateway in front of the Anthropic API
}

type Service struct {
	st  *store.Store
	box *secrets.Box
	MCP *mcp.Server
	// newClient is replaced in tests.
	newClient func(cfg Config) anthropic.Client
}

func New(st *store.Store, box *secrets.Box, srv *mcp.Server) *Service {
	return &Service{st: st, box: box, MCP: srv, newClient: func(cfg Config) anthropic.Client {
		opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey), option.WithRequestTimeout(5 * time.Minute)}
		if cfg.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(cfg.BaseURL))
		}
		return anthropic.NewClient(opts...)
	}}
}

func (sv *Service) Config(ctx context.Context) (Config, error) {
	c := Config{Model: DefaultModel, Effort: "medium"}
	var raw string
	err := sv.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	if c.APIKey != "" {
		if c.APIKey, err = sv.box.Open(c.APIKey); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Save stores the configuration; an empty key keeps the current one unless clear is set.
func (sv *Service) Save(ctx context.Context, in Config, clear bool) error {
	cur, err := sv.Config(ctx)
	if err != nil {
		return err
	}
	in.Model, in.Effort, in.BaseURL = strings.TrimSpace(in.Model), strings.TrimSpace(in.Effort), strings.TrimSpace(in.BaseURL)
	if in.Model == "" {
		in.Model = DefaultModel
	}
	if in.Effort == "" {
		in.Effort = "medium"
	}
	if !efforts[in.Effort] || len(in.Model) > 100 || in.BaseURL != "" && !strings.HasPrefix(in.BaseURL, "https://") {
		return ErrConfig
	}
	switch {
	case clear:
		in.APIKey = ""
	case strings.TrimSpace(in.APIKey) == "":
		in.APIKey = cur.APIKey
	}
	if in.APIKey != "" {
		in.APIKey = sv.box.Seal(strings.TrimSpace(in.APIKey))
	}
	b, _ := json.Marshal(in)
	_, err = sv.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingsKey, string(b))
	return err
}

// Turn is one message of a chat as the browser keeps it (text only).
type Turn struct {
	Role string `json:"role"` // user | assistant
	Text string `json:"text"`
}

// Step records a tool the assistant used, for display.
type Step struct {
	Tool  string `json:"tool"`
	Input string `json:"input"`
	Error string `json:"error,omitempty"`
}

type Answer struct {
	Text  string `json:"text"`
	Steps []Step `json:"steps"`
	Model string `json:"model"`
	Usage struct {
		Input  int64 `json:"input"`
		Output int64 `json:"output"`
	} `json:"usage"`
}

const system = `You are the operations assistant built into lmcwhm, a self-hosted server control panel (HAProxy SNI edge, nginx with optional ModSecurity WAF, PHP-FPM, Docker, App Store databases, nftables firewall, CrowdSec, cron, backups).
Investigate with the tools before you answer: they read live data from this server. You can only read; when a fix needs a change, say exactly what the admin should do in the panel (menu and setting) or which command to run, and why.
Be concrete and brief: lead with the finding, then evidence (IPs, paths, counts, log lines), then the recommended action. Flag anything that looks like an attack or a misconfiguration, and say how sure you are.
Reply in the language of the admin's message (Traditional Chinese for Cantonese / zh-HK). Use Markdown.`

// Ask runs the assistant on a conversation, letting it call read-only tools.
// lang is the panel language, used when the question itself does not say.
func (sv *Service) Ask(ctx context.Context, turns []Turn, lang string, caller mcp.Caller) (Answer, error) {
	var ans Answer
	cfg, err := sv.Config(ctx)
	if err != nil {
		return ans, err
	}
	if cfg.APIKey == "" {
		return ans, ErrNoKey
	}
	if len(turns) == 0 || turns[len(turns)-1].Role != "user" {
		return ans, ErrConfig
	}
	client := sv.newClient(cfg)
	caller.Scope = mcp.ScopeRead

	var tools []anthropic.BetaToolUnionParam
	for _, t := range sv.MCP.Tools(mcp.ScopeRead) {
		props, _ := t.Schema["properties"].(map[string]any)
		var req []string
		if r, ok := t.Schema["required"].([]string); ok {
			req = r
		}
		tool := anthropic.BetaToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: props, Required: req},
		}
		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &tool})
	}
	var msgs []anthropic.BetaMessageParam
	for _, t := range turns {
		if strings.TrimSpace(t.Text) == "" {
			continue
		}
		if t.Role == "assistant" {
			msgs = append(msgs, anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant,
				Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(t.Text)}})
		} else {
			msgs = append(msgs, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(t.Text)))
		}
	}
	sys := system + "\nPanel language: " + lang + ". Server time: " + time.Now().Format(time.RFC3339) + "."
	ans.Model = cfg.Model
	for step := 0; step < maxSteps; step++ {
		resp, err := client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
			Model:        anthropic.Model(cfg.Model),
			MaxTokens:    16000,
			System:       []anthropic.BetaTextBlockParam{{Text: sys, CacheControl: anthropic.NewBetaCacheControlEphemeralParam()}},
			Messages:     msgs,
			Tools:        tools,
			OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(cfg.Effort)},
			// A policy decline is re-served by the model's default fallback.
			Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
			Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
		})
		if err != nil {
			var apiErr *anthropic.Error
			if errors.As(err, &apiErr) {
				return ans, fmt.Errorf("Claude API HTTP %d: %s", apiErr.StatusCode, apiErr.Error())
			}
			return ans, err
		}
		ans.Usage.Input += resp.Usage.InputTokens
		ans.Usage.Output += resp.Usage.OutputTokens
		if resp.StopReason == anthropic.BetaStopReasonRefusal {
			return ans, ErrRefused
		}
		msgs = append(msgs, resp.ToParam())
		var texts []string
		var results []anthropic.BetaContentBlockParamUnion
		for _, block := range resp.Content {
			switch b := block.AsAny().(type) {
			case anthropic.BetaTextBlock:
				texts = append(texts, b.Text)
			case anthropic.BetaToolUseBlock:
				raw := json.RawMessage(b.JSON.Input.Raw())
				st := Step{Tool: b.Name, Input: string(raw)}
				out, err := sv.MCP.Call(ctx, caller, b.Name, raw)
				var text string
				if err != nil {
					st.Error, text = err.Error(), err.Error()
				} else {
					text = resultText(out)
				}
				ans.Steps = append(ans.Steps, st)
				results = append(results, anthropic.NewBetaToolResultBlock(b.ID, text, err != nil))
			}
		}
		if resp.StopReason != anthropic.BetaStopReasonToolUse {
			ans.Text = strings.TrimSpace(strings.Join(texts, "\n\n"))
			if resp.StopReason == anthropic.BetaStopReasonMaxTokens {
				ans.Text += "\n\n…"
			}
			return ans, nil
		}
		msgs = append(msgs, anthropic.NewBetaUserMessage(results...))
	}
	ans.Text = "(stopped after too many tool calls)"
	return ans, nil
}

// resultText serialises a tool result, capped so one log dump cannot fill the context.
func resultText(v any) string {
	s, ok := v.(string)
	if !ok {
		b, _ := json.Marshal(v)
		s = string(b)
	}
	const max = 60000
	if len(s) > max {
		s = s[:max] + "\n[truncated]"
	}
	return s
}
