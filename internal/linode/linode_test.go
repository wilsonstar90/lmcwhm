package linode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplyManaged(t *testing.T) {
	in := Rules{Inbound: []Rule{
		{Action: "ACCEPT", Protocol: "TCP", Ports: "22", Label: "ssh"},
		{Action: "DROP", Protocol: "TCP", Label: BlockLabel, Addresses: Addresses{IPv4: []string{"9.9.9.9/32"}}},
	}, InboundPolicy: "DROP"}
	out, over := ApplyManaged(in, []string{"1.2.3.4", "2001:db8::1", "10.0.0.0/8"}, "80,443,8888")
	if len(over) != 0 || len(out.Inbound) != 3 || out.InboundPolicy != "DROP" || out.OutboundPolicy != "ACCEPT" {
		t.Fatalf("%+v %v", out, over)
	}
	b := out.Inbound[0]
	if b.Label != BlockLabel || b.Action != "DROP" || fmt.Sprint(b.Addresses.IPv4) != "[1.2.3.4/32 10.0.0.0/8]" || fmt.Sprint(b.Addresses.IPv6) != "[2001:db8::1/128]" {
		t.Fatalf("block rule %+v", b)
	}
	if out.Inbound[1].Label != PortsLabel || out.Inbound[2].Label != "ssh" {
		t.Fatalf("order %+v", out.Inbound)
	}
	many := make([]string, 300)
	for i := range many {
		many[i] = fmt.Sprintf("10.0.%d.%d", i/250, i%250)
	}
	if _, over := ApplyManaged(Rules{}, many, ""); len(over) != 45 {
		t.Fatalf("overflow %d", len(over))
	}
}

func TestInstanceByIP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"errors":[{"reason":"Invalid Token"}]}`)
			return
		}
		json.NewEncoder(w).Encode(page[Instance]{Pages: 1, Data: []Instance{
			{ID: 1, IPv4: []string{"192.0.2.1"}}, {ID: 2, IPv4: []string{"192.0.2.2"}, IPv6: "2001:db8::2/128"},
		}})
	}))
	defer ts.Close()
	BaseURL = ts.URL
	in, err := New("tok").InstanceByIP(context.Background(), "", "2001:db8::2")
	if err != nil || in.ID != 2 {
		t.Fatalf("%+v %v", in, err)
	}
	// Equivalent spellings match; a miss says what was tried.
	if in, err := New("tok").InstanceByIP(context.Background(), "2001:0db8:0000::0002"); err != nil || in.ID != 2 {
		t.Fatalf("non-canonical IPv6: %+v %v", in, err)
	}
	if in, err := New("tok").InstanceByIP(context.Background(), "::ffff:192.0.2.1"); err != nil || in.ID != 1 {
		t.Fatalf("v4-mapped: %+v %v", in, err)
	}
	if _, err := New("tok").InstanceByIP(context.Background(), "198.51.100.9", "bogus"); err == nil || !strings.Contains(err.Error(), "198.51.100.9") {
		t.Fatalf("miss: %v", err)
	}
	if _, err := New("bad").Firewalls(context.Background()); err == nil {
		t.Fatal("bad token accepted")
	}
}

func TestScopeHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"errors":[{"reason":"Your OAuth token is not authorized to use this endpoint."}]}`))
	}))
	defer srv.Close()
	old := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = old }()
	_, err := New("tok").Firewalls(context.Background())
	if err == nil || !strings.Contains(err.Error(), `"Firewalls: Read/Write"`) {
		t.Fatalf("firewalls: %v", err)
	}
	_, err = New("tok").InstanceByIP(context.Background(), "192.0.2.1")
	if err == nil || !strings.Contains(err.Error(), `"Linodes: Read/Write"`) {
		t.Fatalf("instances: %v", err)
	}
}
