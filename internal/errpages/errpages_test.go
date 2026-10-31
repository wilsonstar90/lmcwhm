package errpages

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestPages(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sv := New(st)
	ctx := context.Background()
	pages, err := sv.Pages(ctx)
	if err != nil || len(pages) != len(builtins) {
		t.Fatalf("%d pages, %v", len(pages), err)
	}
	m := sv.Mapping(ctx)
	if m.Global["403"] == 0 || m.Global["404"] == 0 {
		t.Fatalf("default mapping %+v", m.Global)
	}
	edge := sv.Edge(ctx)
	if edge.Global[403] == "" || edge.Pages[edge.Global[403]] == nil {
		t.Fatalf("edge %+v", edge.Global)
	}
	custom, err := sv.SavePage(ctx, Page{Name: "Brand 403", HTML: "<h1><!--# echo var=\"status\" --></h1>"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sv.SaveMapping(ctx, Mapping{Global: map[string]int64{"403": custom.ID, "200": custom.ID}}); err != ErrBadMapping {
		t.Errorf("200 accepted: %v", err)
	}
	if _, err := sv.SaveMapping(ctx, Mapping{Global: map[string]int64{"403": 9999}}); err != ErrBadMapping {
		t.Errorf("unknown page accepted: %v", err)
	}
	if _, err := sv.SaveMapping(ctx, Mapping{Global: map[string]int64{"403": custom.ID, "404": 0},
		Sites: map[string]map[string]int64{"a.com": {"403": 0}}, Intercept: map[string]bool{"a.com": true}}); err != nil {
		t.Fatal(err)
	}
	edge = sv.Edge(ctx)
	if _, ok := edge.Global[404]; ok || edge.Sites["a.com"][403] != "" || !edge.Intercept["a.com"] {
		t.Fatalf("edge after mapping %+v", edge)
	}
	if err := sv.DeletePage(ctx, custom.ID); err != ErrInUse {
		t.Errorf("deleted a page in use: %v", err)
	}
	if err := sv.DeletePage(ctx, pages[0].ID); err != ErrBuiltin {
		t.Errorf("deleted a built-in page: %v", err)
	}
	pages[0].HTML = "changed"
	sv.SavePage(ctx, pages[0])
	sv.ResetPage(ctx, pages[0].ID)
	if after, _ := sv.Pages(ctx); after[0].Modified {
		t.Error("reset kept the change")
	}
}
