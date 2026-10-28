// Package mcp is a small Model Context Protocol server: JSON-RPC 2.0 with
// tools and prompts, served over Streamable HTTP (JSON responses) and stdio.
// Tools carry a scope (read < operate < admin) checked against the caller's
// API token.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const (
	ScopeRead    = "read"
	ScopeOperate = "operate"
	ScopeAdmin   = "admin"
)

var scopeRank = map[string]int{ScopeRead: 1, ScopeOperate: 2, ScopeAdmin: 3}

// ValidScope reports a known scope.
func ValidScope(s string) bool { return scopeRank[s] > 0 }

// Allows reports whether a caller with scope have may use a tool needing need.
func Allows(have, need string) bool { return scopeRank[have] >= scopeRank[need] && scopeRank[need] > 0 }

// Supported protocol revisions, newest first.
var Versions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Tool is one callable operation.
type Tool struct {
	Name        string
	Title       string
	Description string
	// Schema is the JSON Schema of the arguments (an object).
	Schema      map[string]any
	Scope       string
	Destructive bool
	Handler     func(ctx context.Context, args json.RawMessage) (any, error)
}

// Prompt is a reusable instruction ("skill") a client can insert.
type Prompt struct {
	Name        string
	Title       string
	Description string
	Args        []PromptArg
	Render      func(args map[string]string) string
}

type PromptArg struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Caller identifies who calls a tool (for scope checks and auditing).
type Caller struct {
	Name  string
	Scope string
	IP    string
}

type Server struct {
	Name, Version, Instructions string
	tools                       map[string]Tool
	prompts                     map[string]Prompt
	// OnCall is told about every tool call (audit log).
	OnCall func(ctx context.Context, c Caller, tool string, args json.RawMessage, err error)
}

func NewServer(name, version, instructions string) *Server {
	return &Server{Name: name, Version: version, Instructions: instructions, tools: map[string]Tool{}, prompts: map[string]Prompt{}}
}

func (s *Server) AddTool(t Tool) {
	if t.Schema == nil {
		t.Schema = Object(nil)
	}
	s.tools[t.Name] = t
}

func (s *Server) AddPrompt(p Prompt) { s.prompts[p.Name] = p }

// Tools lists the tools a scope may use, sorted by name.
func (s *Server) Tools(scope string) []Tool {
	out := []Tool{}
	for _, t := range s.tools {
		if Allows(scope, t.Scope) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Call runs a tool directly (used by the panel's own AI assistant).
func (s *Server) Call(ctx context.Context, c Caller, name string, args json.RawMessage) (any, error) {
	t, ok := s.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	if !Allows(c.Scope, t.Scope) {
		return nil, fmt.Errorf("tool %q needs scope %q (token has %q)", name, t.Scope, c.Scope)
	}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	res, err := t.Handler(ctx, args)
	if s.OnCall != nil {
		s.OnCall(ctx, c, name, args, err)
	}
	return res, err
}

// Prompt renders a prompt by name.
func (s *Server) Prompt(name string, args map[string]string) (Prompt, string, error) {
	p, ok := s.prompts[name]
	if !ok {
		return p, "", fmt.Errorf("unknown prompt %q", name)
	}
	for _, a := range p.Args {
		if a.Required && strings.TrimSpace(args[a.Name]) == "" {
			return p, "", fmt.Errorf("missing argument %q", a.Name)
		}
	}
	return p, p.Render(args), nil
}

func (s *Server) Prompts() []Prompt {
	out := []Prompt{}
	for _, p := range s.prompts {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---- schema helpers ----

// Object builds an object schema; required lists mandatory properties.
func Object(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func Str(desc string, enum ...string) map[string]any {
	m := map[string]any{"type": "string", "description": desc}
	if len(enum) > 0 {
		m["enum"] = enum
	}
	return m
}

func Int(desc string, min, max int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": min, "maximum": max}
}

func Bool(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

// ---- JSON-RPC ----

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const (
	codeParse    = -32700
	codeInvalid  = -32600
	codeMethod   = -32601
	codeParams   = -32602
	codeInternal = -32603
)

// Handle processes one JSON-RPC message or batch. It returns nil when there
// is nothing to send back (only notifications).
func (s *Server) Handle(ctx context.Context, c Caller, body []byte) []byte {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil || len(batch) == 0 {
			return mustJSON(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "parse error"}})
		}
		var out []json.RawMessage
		for _, m := range batch {
			if r := s.handleOne(ctx, c, m); r != nil {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return mustJSON(out)
	}
	return s.handleOne(ctx, c, body)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (s *Server) handleOne(ctx context.Context, c Caller, raw []byte) []byte {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return mustJSON(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "parse error"}})
	}
	if req.ID == nil { // notification (or a response to us): nothing to answer
		return nil
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return mustJSON(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{codeInvalid, "invalid request"}})
	}
	res, rerr := s.dispatch(ctx, c, req)
	resp := response{JSONRPC: "2.0", ID: req.ID, Result: res, Error: rerr}
	if rerr == nil && res == nil {
		resp.Result = struct{}{}
	}
	return mustJSON(resp)
}

func (s *Server) dispatch(ctx context.Context, c Caller, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		v := Versions[0]
		if slices.Contains(Versions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}, "prompts": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "title": "lmcwhm server panel", "version": s.Version},
			"instructions":    s.Instructions,
		}, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		list := []map[string]any{}
		for _, t := range s.Tools(c.Scope) {
			list = append(list, map[string]any{
				"name": t.Name, "title": t.Title, "description": t.Description, "inputSchema": t.Schema,
				"annotations": map[string]any{
					"title": t.Title, "readOnlyHint": t.Scope == ScopeRead,
					"destructiveHint": t.Destructive, "openWorldHint": false,
				},
			})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return nil, &rpcError{codeParams, "invalid params"}
		}
		if _, ok := s.tools[p.Name]; !ok {
			return nil, &rpcError{codeParams, "unknown tool: " + p.Name}
		}
		res, err := s.Call(ctx, c, p.Name, p.Arguments)
		if err != nil {
			// Tool failures are results the model can read, not protocol errors.
			return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}}, nil
		}
		return ToolResult(res), nil
	case "prompts/list":
		list := []map[string]any{}
		for _, p := range s.Prompts() {
			list = append(list, map[string]any{"name": p.Name, "title": p.Title, "description": p.Description, "arguments": p.Args})
		}
		return map[string]any{"prompts": list}, nil
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{codeParams, "invalid params"}
		}
		pr, text, err := s.Prompt(p.Name, p.Arguments)
		if err != nil {
			return nil, &rpcError{codeParams, err.Error()}
		}
		return map[string]any{"description": pr.Description, "messages": []map[string]any{
			{"role": "user", "content": map[string]any{"type": "text", "text": text}},
		}}, nil
	case "resources/list":
		return map[string]any{"resources": []any{}}, nil
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": []any{}}, nil
	}
	return nil, &rpcError{codeMethod, "method not found: " + req.Method}
}

// ToolResult wraps a handler's value as MCP content: strings as text, other
// values as JSON text plus structuredContent when they are objects.
func ToolResult(v any) map[string]any {
	if s, ok := v.(string); ok {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": s}}}
	}
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}}
	}
	out := map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}}
	if len(b) > 0 && b[0] == '{' {
		out["structuredContent"] = json.RawMessage(b)
	}
	return out
}

// ErrArgs reports bad tool arguments.
var ErrArgs = errors.New("invalid arguments")

// Args decodes tool arguments strictly.
func Args(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrArgs, err)
	}
	return nil
}
