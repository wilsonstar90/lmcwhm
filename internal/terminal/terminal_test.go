package terminal

import "testing"

func TestTickets(t *testing.T) {
	s := New()
	id, err := s.Issue("admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(id, "other"); err == nil {
		t.Error("ticket redeemed by another user")
	}
	// A failed redeem consumes the ticket too.
	if _, err := s.Redeem(id, "admin"); err == nil {
		t.Error("ticket reused")
	}
	id, _ = s.Issue("admin", "web-1")
	tk, err := s.Redeem(id, "admin")
	if err != nil || tk.Container != "web-1" {
		t.Errorf("redeem: %+v %v", tk, err)
	}
	if _, err := s.Issue("admin", "x; rm -rf /"); err == nil {
		t.Error("bad container name accepted")
	}
}
