package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
)

func TestPMASignonLinks(t *testing.T) {
	var s Server
	u := s.pmaURL(appstore.PMACredentials{User: "shop", Password: "pw", Port: 3306, Server: 1, DB: "shop"})["url"]
	// Only the sign-on script gets the credentials.
	if h := s.signonHeader(httptest.NewRequest("GET", "/phpmyadmin/index.php?"+u[len(appstore.PMASignon)+1:], nil)); h != "" {
		t.Fatal("credentials sent to another page")
	}
	h := s.signonHeader(httptest.NewRequest("GET", u, nil))
	b, _ := base64.StdEncoding.DecodeString(h)
	var c appstore.PMACredentials
	if json.Unmarshal(b, &c) != nil || c.User != "shop" || c.DB != "shop" || c.Server != 1 {
		t.Fatalf("header %q", b)
	}
	// One use only.
	if s.signonHeader(httptest.NewRequest("GET", u, nil)) != "" {
		t.Fatal("link reused")
	}
	if s.signonHeader(httptest.NewRequest("GET", appstore.PMASignon+"?t=forged", nil)) != "" {
		t.Fatal("forged token accepted")
	}
}
