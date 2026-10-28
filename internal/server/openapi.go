package server

import (
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/config"
)

// OpenAPI 3 description of /api/v1, generated from the router so it cannot
// drift from the real routes. Request and response bodies are JSON objects;
// errors are {"code": "...", "params": {...}}.

var pathParam = regexp.MustCompile(`\{([a-z_]+)(:[^}]*)?\}`)

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	paths := map[string]map[string]any{}
	tags := map[string]bool{}
	chi.Walk(s.api, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if route == "" || strings.Contains(route, "/*") {
			return nil
		}
		var params []map[string]any
		p := pathParam.ReplaceAllStringFunc(route, func(m string) string {
			sub := pathParam.FindStringSubmatch(m)
			schema := map[string]any{"type": "string"}
			if sub[2] != "" {
				schema["enum"] = strings.Split(strings.TrimPrefix(sub[2], ":"), "|")
			}
			params = append(params, map[string]any{"name": sub[1], "in": "path", "required": true, "schema": schema})
			return "{" + sub[1] + "}"
		})
		mod := module("/api/v1" + route)
		switch mod {
		case "":
			mod = "account"
		case "admin":
			mod = "panel"
		}
		tags[mod] = true
		op := map[string]any{
			"tags":        []string{mod},
			"operationId": strings.ToLower(method) + strings.ReplaceAll(strings.NewReplacer("{", "", "}", "").Replace(p), "/", "_"),
			"responses": map[string]any{
				"200": map[string]any{"description": "OK", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}},
				"202": map[string]any{"description": "Started a background task: {\"task_id\": \"...\"}; poll GET /tasks/{id}"},
				"4XX": map[string]any{"$ref": "#/components/responses/Error"},
			},
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if method != http.MethodGet && method != http.MethodHead {
			op["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}}
		}
		if paths[p] == nil {
			paths[p] = map[string]any{}
		}
		paths[p][strings.ToLower(method)] = op
		return nil
	})
	var tagList []map[string]any
	for t := range tags {
		tagList = append(tagList, map[string]any{"name": t})
	}
	sort.Slice(tagList, func(i, j int) bool { return tagList[i]["name"].(string) < tagList[j]["name"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title": "lmcwhm API", "version": config.Version,
			"description": "Every route the panel UI uses. Authenticate with an API token (Settings → API tokens): " +
				"read tokens may only GET, operate tokens may use every module except panel administration, admin tokens may use everything. " +
				"Long operations return 202 with a task_id.",
		},
		"servers":  []map[string]any{{"url": "/api/v1"}},
		"security": []map[string]any{{"bearer": []string{}}},
		"tags":     tagList,
		"paths":    paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{"bearer": map[string]any{"type": "http", "scheme": "bearer", "description": "lmc_… API token"}},
			"responses": map[string]any{"Error": map[string]any{"description": "Error", "content": map[string]any{"application/json": map[string]any{
				"schema": map[string]any{"type": "object", "properties": map[string]any{"code": map[string]any{"type": "string"}, "params": map[string]any{"type": "object"}}},
			}}}},
		},
	})
}
