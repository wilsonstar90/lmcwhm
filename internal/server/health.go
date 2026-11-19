package server

import (
	"context"
	"net/http"
	"sync"
	"time"
)

type Readiness struct {
	mu         sync.RWMutex
	components map[string]bool
}

func NewReadiness() *Readiness {
	return &Readiness{components: map[string]bool{}}
}

func (r *Readiness) Set(name string, ready bool) {
	r.mu.Lock()
	r.components[name] = ready
	r.mu.Unlock()
}

func (r *Readiness) snapshot() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]bool, len(r.components))
	for name, ready := range r.components {
		out[name] = ready
	}
	return out
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"status": "method_not_allowed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	healthy := s.Store != nil
	if s.Store != nil {
		var one int
		if err := s.Store.DB.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
			healthy = false
		}
	}
	components := map[string]string{}
	if s.Readiness != nil {
		for name, ready := range s.Readiness.snapshot() {
			status := "ready"
			if !ready {
				status = "not_ready"
				healthy = false
			}
			components[name] = status
		}
	}
	status := http.StatusOK
	state := "ok"
	if !healthy {
		status = http.StatusServiceUnavailable
		state = "unavailable"
	}
	writeJSON(w, status, map[string]any{"status": state, "components": components})
}
