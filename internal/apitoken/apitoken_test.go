package apitoken

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/mcp"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestMCPEndpointMigrationAndBinding(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO api_tokens(name, token_hash, hint, scope, created_at) VALUES('legacy', 'hash', 'lmc_test', 'read', 1)`); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sv, err := New(st, box)
	if err != nil {
		t.Fatal(err)
	}
	items, err := sv.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %v %v", items, err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(items[0].MCPID) {
		t.Fatalf("legacy token did not receive a UUIDv4 endpoint: %q", items[0].MCPID)
	}
	if items[0].Client != mcp.ClientAny {
		t.Fatalf("legacy client restriction = %q", items[0].Client)
	}

	tk, secret, err := sv.Create(ctx, Input{Name: "kimi", Scope: "read", Client: mcp.ClientKimi})
	if err != nil {
		t.Fatal(err)
	}
	if tk.MCPID == "" || tk.MCPID == items[0].MCPID || tk.Client != mcp.ClientKimi {
		t.Fatalf("new token endpoint: %+v", tk)
	}
	if _, err := sv.VerifyMCP(ctx, secret, "127.0.0.1", tk.MCPID); err != nil {
		t.Fatalf("bound endpoint rejected: %v", err)
	}
	if _, err := sv.VerifyMCP(ctx, secret, "127.0.0.1", items[0].MCPID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-token endpoint accepted: %v", err)
	}
	client := mcp.ClientDeepSeek
	updated, err := sv.Update(ctx, tk.ID, UpdateInput{Client: &client})
	if err != nil || updated.Client != mcp.ClientDeepSeek {
		t.Fatalf("client update: %+v %v", updated, err)
	}
	if _, _, err := sv.Create(ctx, Input{Name: "bad", Scope: "read", Client: "other"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown client accepted: %v", err)
	}
}
