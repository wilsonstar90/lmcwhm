package server

import "testing"

func TestCleanMenu(t *testing.T) {
	m, err := cleanMenu(menuConfig{Order: []string{"security", "home", "security"}, Hidden: []string{"mail", "mail"}})
	if err != nil || len(m.Order) != 2 || m.Order[0] != "security" || len(m.Hidden) != 1 {
		t.Fatalf("%+v %v", m, err)
	}
	for _, bad := range []menuConfig{{Order: []string{"nope"}}, {Hidden: []string{"settings"}}, {Hidden: []string{"home"}}} {
		if _, err := cleanMenu(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
