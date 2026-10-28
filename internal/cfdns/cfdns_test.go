package cfdns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCF implements the handful of endpoints the client uses.
func fakeCF(t *testing.T) (*httptest.Server, *[]Record) {
	var mu sync.Mutex
	recs := &[]Record{{ID: "old1", Type: "A", Name: "www.example.com", Content: "1.1.1.1"}, {ID: "old2", Type: "A", Name: "www.example.com", Content: "2.2.2.2"}}
	ok := func(w http.ResponseWriter, v any) {
		b, _ := json.Marshal(v)
		w.Write([]byte(`{"success":true,"errors":[],"result":` + string(b) + `}`))
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.Write([]byte(`{"success":false,"errors":[{"code":1000,"message":"bad token"}]}`))
			return
		}
		switch {
		case r.URL.Path == "/zones":
			if r.URL.Query().Get("name") == "example.com" {
				ok(w, []Zone{{ID: "z1", Name: "example.com"}})
			} else {
				ok(w, []Zone{})
			}
		case strings.HasSuffix(r.URL.Path, "/dns_records") && r.Method == http.MethodGet:
			var out []Record
			for _, x := range *recs {
				if x.Type == r.URL.Query().Get("type") && x.Name == r.URL.Query().Get("name") {
					out = append(out, x)
				}
			}
			ok(w, out)
		case strings.HasSuffix(r.URL.Path, "/dns_records") && r.Method == http.MethodPost:
			var x Record
			json.NewDecoder(r.Body).Decode(&x)
			x.ID = "new"
			*recs = append(*recs, x)
			ok(w, x)
		case r.Method == http.MethodPut:
			var x Record
			json.NewDecoder(r.Body).Decode(&x)
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			for i := range *recs {
				if (*recs)[i].ID == id {
					x.ID = id
					(*recs)[i] = x
				}
			}
			ok(w, x)
		case r.Method == http.MethodDelete:
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			out := (*recs)[:0]
			for _, x := range *recs {
				if x.ID != id {
					out = append(out, x)
				}
			}
			*recs = out
			ok(w, map[string]string{"id": id})
		}
	}))
	t.Cleanup(ts.Close)
	return ts, recs
}

func TestZoneAndUpsert(t *testing.T) {
	ts, recs := fakeCF(t)
	BaseURL = ts.URL
	c := New("tok")
	ctx := context.Background()

	z, err := c.ZoneFor(ctx, "www.example.com")
	if err != nil || z.ID != "z1" {
		t.Fatalf("zone: %v %v", z, err)
	}
	if _, err := c.ZoneFor(ctx, "nope.org"); err != ErrNoZone {
		t.Fatalf("missing zone: %v", err)
	}
	// Two existing A records collapse into one pointing at the new IP.
	if err := c.Upsert(ctx, "z1", "A", "www.example.com", "203.0.113.5", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Upsert(ctx, "z1", "AAAA", "www.example.com", "2001:db8::5", false); err != nil {
		t.Fatal(err)
	}
	var a, aaaa int
	for _, r := range *recs {
		switch r.Type {
		case "A":
			a++
			if r.Content != "203.0.113.5" || !r.Proxied {
				t.Fatalf("A record: %+v", r)
			}
		case "AAAA":
			aaaa++
		}
	}
	if a != 1 || aaaa != 1 {
		t.Fatalf("got %d A, %d AAAA", a, aaaa)
	}
	if err := New("bad").Verify(ctx); err == nil || !strings.Contains(err.Error(), "bad token") {
		t.Fatalf("bad token: %v", err)
	}
}

func TestRecordCheckAndTemplates(t *testing.T) {
	p := 10
	ok := []FullRecord{
		{Type: "a", Name: "WWW.Example.com.", Content: "1.2.3.4", Proxied: true},
		{Type: "MX", Name: "example.com", Content: "mail.example.com", Priority: &p},
		{Type: "SRV", Name: "_sip._tls.example.com", Data: map[string]any{"priority": 100.0, "weight": "1", "port": 443, "target": "sip.example.net"}},
		{Type: "CAA", Name: "example.com", Data: map[string]any{"flags": 0, "tag": "issue", "value": "letsencrypt.org"}},
		{Type: "TXT", Name: "_dmarc.example.com", Content: "v=DMARC1; p=none"},
	}
	for _, r := range ok {
		if err := r.Check(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	r := ok[0]
	r.Check()
	if r.Type != "A" || r.Name != "www.example.com" {
		t.Errorf("normalised: %+v", r)
	}
	bad := []FullRecord{
		{Type: "A", Name: "x.com", Content: "::1"},
		{Type: "MX", Name: "x.com", Content: "mail.x.com"},
		{Type: "CAA", Name: "x.com", Data: map[string]any{"flags": 0, "tag": "bogus", "value": "x"}},
		{Type: "TXT", Name: "bad name.com", Content: "x"},
		{Type: "PTR", Name: "x.com", Content: "y"},
	}
	for _, r := range bad {
		if err := r.Check(); err == nil {
			t.Errorf("accepted %+v", r)
		}
	}
	var m365 Template
	for _, tp := range Templates {
		if tp.Key == "microsoft365" {
			m365 = tp
		}
	}
	recs := Expand(m365, map[string]string{"domain": "a.example.com"})
	if recs[0].Content != "a-example-com.mail.protection.outlook.com" || recs[3].Data["target"] != "sipdir.online.lync.com" {
		t.Fatalf("expand: %+v", recs)
	}
	var self Template
	for _, tp := range Templates {
		if tp.Key == "self_mail" {
			self = tp
		}
	}
	recs = Expand(self, map[string]string{"domain": "a.com", "mailhost": "mail.a.com", "ip4": "1.2.3.4", "ip6": ""})
	for _, r := range recs {
		if r.Type == "AAAA" {
			t.Fatal("empty IPv6 record kept")
		}
		if err := r.Check(); err != nil {
			t.Fatalf("%+v: %v", r, err)
		}
	}
}
