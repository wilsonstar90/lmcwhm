package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/auth"
	"github.com/wilsonstar90/lmcwhm/internal/mcp"
)

const mcpAPIResponseLimit = 2 << 20

func mcpAPIScope(method, path string) string {
	if module(path) == "admin" {
		return mcp.ScopeAdmin
	}
	if method == http.MethodGet || method == http.MethodHead {
		return mcp.ScopeRead
	}
	return mcp.ScopeOperate
}

func mcpAPIModule(path string) string {
	switch m := module(path); m {
	case "":
		return "account"
	case "admin":
		return "panel"
	default:
		return m
	}
}

func mcpAPIExposed(path string) bool {
	return !strings.HasPrefix(path, "/api/v1/public/") &&
		path != "/api/v1/auth/login" && path != "/api/v1/auth/logout"
}

func (s *Server) addMCPAPITools(m *mcp.Server) {
	m.AddTool(mcp.Tool{
		Name: "list_api_endpoints", Title: "List all panel API endpoints", Scope: mcp.ScopeRead,
		Description: "Lists the real lmcwhm REST routes available to this token. Use filters to find a feature, replace {parameters} with concrete values, then use call_api. GET requires read, state changes require operate, and panel administration requires admin.",
		Schema: mcp.Object(map[string]any{
			"module": mcp.Str("optional module, e.g. website, database, docker, files, security, backup, mail, panel"),
			"method": mcp.Str("optional HTTP method", "GET", "POST", "PUT", "PATCH", "DELETE"),
			"search": mcp.Str("optional case-insensitive text in the path"),
		}),
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct{ Module, Method, Search string }
			if err := mcp.Args(raw, &a); err != nil {
				return nil, err
			}
			c, ok := mcp.CallerFrom(ctx)
			if !ok || s.api == nil {
				return nil, errors.New("MCP API is not ready")
			}
			a.Method = strings.ToUpper(a.Method)
			a.Search = strings.ToLower(strings.TrimSpace(a.Search))
			items := []map[string]any{}
			chi.Walk(s.api, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
				path := "/api/v1" + strings.TrimSuffix(route, "/")
				scope := mcpAPIScope(method, path)
				mod := mcpAPIModule(path)
				if !mcpAPIExposed(path) || !mcp.Allows(c.Scope, scope) ||
					(a.Module != "" && mod != a.Module) || (a.Method != "" && method != a.Method) ||
					(a.Search != "" && !strings.Contains(strings.ToLower(path), a.Search)) {
					return nil
				}
				items = append(items, map[string]any{"method": method, "path": path, "module": mod, "required_scope": scope})
				return nil
			})
			sort.Slice(items, func(i, j int) bool {
				pi, pj := items[i]["path"].(string), items[j]["path"].(string)
				return pi < pj || pi == pj && items[i]["method"].(string) < items[j]["method"].(string)
			})
			return map[string]any{"count": len(items), "items": items}, nil
		},
	})

	m.AddTool(mcp.Tool{
		Name: "call_api", Title: "Call any panel API endpoint", Scope: mcp.ScopeRead, Destructive: true,
		Description: "Calls an endpoint returned by list_api_endpoints through the panel's real handlers, validation and audit. path may be /sites or /api/v1/sites; query values are strings; body is a JSON value. Long jobs return a task_id to use with get_task. Binary and interactive WebSocket responses are not supported.",
		Schema: mcp.Object(map[string]any{
			"method": mcp.Str("HTTP method; default GET", "GET", "POST", "PUT", "PATCH", "DELETE"),
			"path":   mcp.Str("endpoint path with concrete path parameters"),
			"query":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "optional URL query parameters"},
			"body":   map[string]any{"description": "optional JSON request body; secrets are not written to the MCP audit detail"},
		}, "path"),
		Handler: s.callMCPAPI,
	})
}

type mcpAPIResponse struct {
	header    http.Header
	status    int
	body      bytes.Buffer
	truncated bool
}

func (w *mcpAPIResponse) Header() http.Header { return w.header }

func (w *mcpAPIResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *mcpAPIResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n := len(p)
	remaining := mcpAPIResponseLimit - w.body.Len()
	if remaining < len(p) {
		w.truncated = true
	}
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.body.Write(p)
	}
	return n, nil
}

func (s *Server) callMCPAPI(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		Method string
		Path   string
		Query  map[string]string
		Body   json.RawMessage
	}
	if err := mcp.Args(raw, &a); err != nil {
		return nil, err
	}
	c, ok := mcp.CallerFrom(ctx)
	if !ok || s.apiRoot == nil {
		return nil, errors.New("MCP API is not ready")
	}
	a.Method = strings.ToUpper(strings.TrimSpace(a.Method))
	if a.Method == "" {
		a.Method = http.MethodGet
	}
	switch a.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, fmt.Errorf("%w: unsupported HTTP method", mcp.ErrArgs)
	}
	a.Path = strings.TrimSpace(a.Path)
	if !strings.HasPrefix(a.Path, "/api/v1/") {
		a.Path = "/api/v1/" + strings.TrimPrefix(a.Path, "/")
	}
	u, err := url.ParseRequestURI(a.Path)
	if err != nil || u.IsAbs() || u.RawQuery != "" || u.Fragment != "" || !mcpAPIExposed(a.Path) {
		return nil, fmt.Errorf("%w: invalid or unavailable API path", mcp.ErrArgs)
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return nil, fmt.Errorf("%w: path traversal is not allowed", mcp.ErrArgs)
		}
	}
	need := mcpAPIScope(a.Method, a.Path)
	if !mcp.Allows(c.Scope, need) {
		return nil, fmt.Errorf("%s %s requires %s scope (token has %s)", a.Method, a.Path, need, c.Scope)
	}
	if len(a.Body) > 1<<20 || len(a.Body) > 0 && !json.Valid(a.Body) {
		return nil, fmt.Errorf("%w: body must be valid JSON of at most 1 MiB", mcp.ErrArgs)
	}
	q := url.Values{}
	for k, v := range a.Query {
		q.Set(k, v)
	}
	if len(q) > 0 {
		a.Path += "?" + q.Encode()
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	stopCancel := context.AfterFunc(ctx, cancel)
	defer func() {
		stopCancel()
		cancel()
	}()
	req, err := http.NewRequestWithContext(requestCtx, a.Method, a.Path, bytes.NewReader(a.Body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = c.IP
	uuser := auth.User{Username: c.Name, Enabled: true, Perms: append([]string{}, auth.Modules...), IsAdmin: c.Scope == mcp.ScopeAdmin}
	req = withMCPAPIUser(req, uuser)
	w := &mcpAPIResponse{header: make(http.Header)}
	s.apiRoot.ServeHTTP(w, req)
	if w.status == 0 {
		w.status = http.StatusOK
	}
	out := map[string]any{"status": w.status, "ok": w.status >= 200 && w.status < 400, "content_type": w.header.Get("Content-Type"), "truncated": w.truncated}
	data := w.body.Bytes()
	var body any
	if len(data) > 0 && json.Unmarshal(data, &body) == nil {
		out["body"] = body
	} else if utf8.Valid(data) {
		out["body"] = string(data)
	} else {
		out["body"] = fmt.Sprintf("%d bytes of binary data omitted", len(data))
	}
	return out, nil
}
