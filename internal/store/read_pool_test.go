package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestReadPoolUsesWALAndIsQueryOnly(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES('test', 'before')`); err != nil {
		t.Fatal(err)
	}
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE settings SET value='after' WHERE key='test'`); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := st.Read().QueryRowContext(ctx, `SELECT value FROM settings WHERE key='test'`).Scan(&value); err != nil || value != "before" {
		t.Fatalf("WAL read was blocked or saw uncommitted data: %q %v", value, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Read().ExecContext(ctx, `INSERT INTO settings(key, value) VALUES('forbidden', 'x')`); err == nil {
		t.Fatal("read pool accepted a write")
	}
}
