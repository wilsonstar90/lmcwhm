package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

func TestActionMiddlewareCoversMutationsWithoutChangingResponse(t *testing.T) {
	s := &Server{Tasks: tasks.NewManager()}
	called := false
	h := s.actionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		a, ok := tasks.ActionFrom(r.Context())
		if !ok || a.Name != "PUT /api/v1/firewall/patterns/7" || a.Resource != "security" || !a.NoLock {
			t.Fatalf("unexpected action: %+v %v", a, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodPut, "/api/v1/firewall/patterns/7", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called || w.Code != http.StatusNoContent {
		t.Fatalf("response changed: called=%v status=%d", called, w.Code)
	}
}

func TestMCPActionUsesSameContext(t *testing.T) {
	s := &Server{Tasks: tasks.NewManager()}
	got, err := mcpAction(s, context.Background(), "container.restart", "docker", func(ctx context.Context) (string, error) {
		a, ok := tasks.ActionFrom(ctx)
		if !ok || a.Name != "container.restart" || a.Resource != "docker" {
			t.Fatalf("unexpected action: %+v %v", a, ok)
		}
		return "done", nil
	})
	if err != nil || got != "done" {
		t.Fatalf("action result: %q %v", got, err)
	}
}
