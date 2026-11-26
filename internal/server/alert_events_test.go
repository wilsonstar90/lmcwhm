package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/alert"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
)

func TestAlertEventDetail(t *testing.T) {
	e := newEnv(t, nil)
	box, err := secrets.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alerts := alert.New(e.srv.Store, box)
	id, err := alerts.Record(context.Background(), alert.KindPanelRestart, "process", "info", "Panel started / restarted", "Version test")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Deps: Deps{Alerts: alerts}}
	r := chi.NewRouter()
	r.Get("/alerts/events/{id}", s.handleAlertEvent)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/alerts/events/%d", id), nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"kind":"panel_restart"`) || !strings.Contains(w.Body.String(), `"body":"Version test"`) {
		t.Fatalf("event detail: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/alerts/events/999999", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing event status = %d", w.Code)
	}
}
