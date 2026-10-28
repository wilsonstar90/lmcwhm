package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/site"
)

// SNI / domain management: a read model over the sites table. Routes are
// created and changed through the site API (a hostname always belongs to
// exactly one site), so this page cannot drift from what the edge serves.
func (s *Server) routeRoutes(r chi.Router) {
	r.Get("/routes", s.handleRoutes)
	r.Get("/routes/resolve", s.handleResolveRoute)
}

func (s *Server) routes(r *http.Request) ([]site.Route, error) {
	routes, err := s.Sites.Routes(r.Context())
	if err != nil {
		return nil, err
	}
	if s.Docker != nil {
		// Name the container behind a loopback upstream (Docker "bind domain").
		if cs, err := s.Docker.Containers(r.Context(), false); err == nil {
			byPort := map[int]string{}
			for _, c := range cs {
				for _, p := range c.HostPorts {
					byPort[p] = c.Name
				}
			}
			for i := range routes {
				if routes[i].Container == "" {
					routes[i].Container = byPort[site.UpstreamPort(routes[i].Upstream)]
				}
			}
		}
	}
	return routes, nil
}

func (s *Server) handleRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := s.routes(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if r.URL.Query().Get("check") == "1" {
		s.Sites.Check(r.Context(), routes)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": routes})
}

func (s *Server) handleResolveRoute(w http.ResponseWriter, r *http.Request) {
	routes, err := s.routes(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, site.Resolve(routes, r.URL.Query().Get("host")))
}
