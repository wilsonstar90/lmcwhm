package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/files"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestRemarksFollowRenames(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{Deps: Deps{Store: st}}
	ctx := context.Background()
	for p, r := range map[string]string{"/www/a": "dir", "/www/a/x.php": "file", "/www/a_b": "other", "/www/ab": "kept"} {
		st.DB.Exec(`INSERT INTO file_remarks(path, remark, updated_at) VALUES(?, ?, 0)`, p, r)
	}
	s.moveRemarks(ctx, "/www/a", "/www/c")
	items := s.decorate(ctx, "/www/c", []files.Entry{{Path: "/www/c/x.php"}})
	if items[0].Remark != "file" {
		t.Fatalf("moved remark %+v", items[0])
	}
	got := map[string]string{}
	rows, _ := st.DB.Query(`SELECT path, remark FROM file_remarks`)
	for rows.Next() {
		var p, r string
		rows.Scan(&p, &r)
		got[p] = r
	}
	rows.Close()
	if got["/www/c"] != "dir" || got["/www/a_b"] != "other" || got["/www/ab"] != "kept" || len(got) != 4 {
		t.Fatalf("after rename %v", got)
	}
	s.moveRemarks(ctx, "/www/c", "")
	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM file_remarks`).Scan(&n)
	if n != 2 {
		t.Errorf("%d remarks after delete", n)
	}
}
