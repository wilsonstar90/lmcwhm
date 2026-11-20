package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreDrillDoesNotChangeTarget(t *testing.T) {
	sv := newSvc(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data")
	write(t, filepath.Join(dir, "a.txt"), "original")
	if err := sv.Run(ctx, Plan{Kind: KindPath, Target: dir, Dests: []string{DestLocal}}, 0, io.Discard); err != nil {
		t.Fatal(err)
	}
	recs, _ := sv.List(ctx, KindPath)
	if err := sv.Drill(ctx, recs[0].ID); err != nil {
		t.Fatal(err)
	}
	status, err := sv.DrillStatus(ctx, recs[0].ID)
	if err != nil || status["status"] != "success" {
		t.Fatalf("drill status: %+v %v", status, err)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(body) != "original" {
		t.Fatalf("drill changed target: %q", body)
	}
}

func TestPanelRestoreDrill(t *testing.T) {
	sv := newSvc(t)
	ctx := context.Background()
	if err := sv.Run(ctx, Plan{Kind: KindPanel, Dests: []string{DestLocal}}, 0, io.Discard); err != nil {
		t.Fatal(err)
	}
	recs, _ := sv.List(ctx, KindPanel)
	if err := sv.Drill(ctx, recs[0].ID); err != nil {
		t.Fatal(err)
	}
}
