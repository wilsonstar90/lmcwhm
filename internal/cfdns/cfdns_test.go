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
