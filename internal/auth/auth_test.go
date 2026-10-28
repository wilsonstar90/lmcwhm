package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestSubUsers(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "p.db"))
	defer st.Close()
	sv := New(st)
	ctx := context.Background()
	if err := sv.CreateUser(ctx, "admin", "password123", true); err != nil {
		t.Fatal(err)
	}
	admin, _ := sv.PrimaryAdmin(ctx)
	if _, err := sv.AddUser(ctx, UserInput{Username: "ops", Password: "password123", Perms: []string{"nope"}, Enabled: true}); err != ErrBadPerm {
		t.Fatalf("bad perm: %v", err)
	}
	u, err := sv.AddUser(ctx, UserInput{Username: "ops", Password: "password123", Perms: []string{"website", "files"}, Sites: []int64{3}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !u.Can("website") || u.Can("terminal") || !u.CanSite(3) || u.CanSite(4) || u.IsAdmin {
		t.Fatalf("perms: %+v", u)
	}
	if _, err := sv.AddUser(ctx, UserInput{Username: "ops", Password: "password123"}); err != ErrUserExists {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := sv.UpdateUser(ctx, admin.ID, UserInput{Username: "admin", IsAdmin: false, Enabled: true}); err != ErrLastAdmin {
		t.Fatalf("demote last admin: %v", err)
	}
	if err := sv.DeleteUser(ctx, admin.ID); err != ErrLastAdmin {
		t.Fatalf("delete last admin: %v", err)
	}
	in := LoginInput{Username: "ops", Password: "password123", IP: "1.2.3.4", MaxFails: 5, LockWindow: time.Minute, SessionTTL: time.Hour}
	tok, sess, err := sv.Login(ctx, in)
	if err != nil || sess.User.Perms[0] != "website" {
		t.Fatalf("login: %+v %v", sess, err)
	}
	if _, err := sv.UpdateUser(ctx, u.ID, UserInput{Username: "ops", Perms: []string{"website"}, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := sv.Resolve(ctx, tok); err != ErrNoSession {
		t.Fatal("disabled user keeps a session")
	}
	if _, _, err := sv.Login(ctx, in); err != ErrDisabled {
		t.Fatalf("disabled login: %v", err)
	}
}
