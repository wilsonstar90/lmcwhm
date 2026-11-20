package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/wilsonstar90/lmcwhm/internal/auth"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

// actionMiddleware gives every REST mutation the same metadata and execution
// boundary without making fast operations asynchronous or changing responses.
func (s *Server) actionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		pattern := r.URL.Path
		if route := chi.RouteContext(r.Context()); route != nil && route.RoutePattern() != "" {
			pattern = route.RoutePattern()
		}
		owner := ""
		if session, ok := r.Context().Value(sessionKey).(auth.Session); ok {
			owner = session.User.Username
		}
		a := tasks.Action{Name: r.Method + " " + pattern, Resource: module(r.URL.Path), Owner: owner, Module: module(r.URL.Path), NoLock: true}
		if s.Tasks == nil {
			next.ServeHTTP(w, r.WithContext(tasks.WithAction(r.Context(), a)))
			return
		}
		err := s.Tasks.RunAction(r.Context(), a, func(ctx context.Context) error {
			next.ServeHTTP(w, r.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
		}
	})
}

func (s *Server) runAction(ctx context.Context, name, resource string, fn func(context.Context) error) error {
	a := tasks.Action{Name: name, Resource: resource, NoLock: true}
	if s.Tasks == nil {
		return fn(tasks.WithAction(ctx, a))
	}
	return s.Tasks.RunAction(ctx, a, fn)
}

func mcpAction[T any](s *Server, ctx context.Context, name, resource string, fn func(context.Context) (T, error)) (T, error) {
	var zero, out T
	if err := s.runAction(ctx, name, resource, func(ctx context.Context) error {
		var err error
		out, err = fn(ctx)
		return err
	}); err != nil {
		return zero, err
	}
	return out, nil
}
