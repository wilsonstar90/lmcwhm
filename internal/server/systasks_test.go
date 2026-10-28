package server

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/cron"
	"github.com/wilsonstar90/lmcwhm/internal/malware"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestEnsureSystemJobs(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	sm, _ := settings.NewManager(ctx, st)
	jobs := cron.New(st, dir, time.UTC)
	s := &Server{Deps: Deps{Store: st, Settings: sm, Cron: jobs, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Malware: malware.New(st, filepath.Join(dir, "www"), dir)}}
	jobs.Tasks = s
	s.EnsureSystemJobs(ctx)
	list, _ := jobs.List(ctx)
	if len(list) != len(defaultJobs) {
		t.Fatalf("%d jobs: %+v", len(list), list)
	}
	names := map[string]string{}
	for _, j := range list {
		names[j.Command] = j.Name
	}
	if names["time_sync"] != "伺服器時間同步" {
		t.Fatalf("names %v", names)
	}
	// A deleted default is not recreated.
	jobs.Delete(ctx, list[0].ID)
	s.EnsureSystemJobs(ctx)
	if l, _ := jobs.List(ctx); len(l) != len(defaultJobs)-1 {
		t.Fatalf("recreated: %d", len(l))
	}
	for typ, cmd := range map[string]string{"system": "rm -rf /", "malware_scan": `{"paths":["/etc"]}`} {
		if _, err := jobs.Create(ctx, cron.Input{Name: "x", Type: typ, Schedule: "@daily", Command: cmd}); err != cron.ErrCommand {
			t.Errorf("%s %s accepted: %v", typ, cmd, err)
		}
	}
}
