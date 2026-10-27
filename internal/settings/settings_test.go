package settings

import (
	"net"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Settings)
		want error
	}{
		{"defaults", func(*Settings) {}, nil},
		{"port zero", func(s *Settings) { s.PanelPort = 0 }, ErrInvalidPort},
		{"port high", func(s *Settings) { s.PanelPort = 70000 }, ErrInvalidPort},
		{"entry ok", func(s *Settings) { s.SecurityEntry = "/console" }, nil},
		{"entry no slash", func(s *Settings) { s.SecurityEntry = "console" }, ErrInvalidEntry},
		{"entry traversal", func(s *Settings) { s.SecurityEntry = "/../etc" }, ErrInvalidEntry},
		{"entry reserved", func(s *Settings) { s.SecurityEntry = "/api" }, ErrInvalidEntry},
		{"entry too short", func(s *Settings) { s.SecurityEntry = "/ab" }, ErrInvalidEntry},
		{"domain ok", func(s *Settings) { s.BindDomain = "panel.example.com" }, nil},
		{"domain bad", func(s *Settings) { s.BindDomain = "not a domain" }, ErrInvalidDom},
		{"ips ok", func(s *Settings) { s.AllowedIPs = []string{"1.2.3.4", "10.0.0.0/8", "2001:db8::/32"} }, nil},
		{"ips bad", func(s *Settings) { s.AllowedIPs = []string{"1.2.3"} }, ErrInvalidIP},
		{"lang bad", func(s *Settings) { s.Language = "fr" }, ErrInvalidLang},
	}
	for _, c := range cases {
		s := Defaults()
		c.mut(&s)
		if got := s.Validate(); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestIPAllowed(t *testing.T) {
	s := Defaults()
	if !s.IPAllowed(net.ParseIP("8.8.8.8")) {
		t.Fatal("empty list must allow everyone")
	}
	s.AllowedIPs = []string{"203.0.113.0/24", "2001:db8::1"}
	for ip, want := range map[string]bool{
		"203.0.113.9":        true,
		"203.0.114.1":        false,
		"2001:db8::1":        true,
		"2001:db8::2":        false,
		"127.0.0.1":          true, // loopback always allowed for local rescue
		"::1":                true,
		"::ffff:203.0.113.5": true,
	} {
		if got := s.IPAllowed(net.ParseIP(ip)); got != want {
			t.Errorf("%s: got %v want %v", ip, got, want)
		}
	}
}
