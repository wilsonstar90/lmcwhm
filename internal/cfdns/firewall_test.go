package cfdns

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestAccessTarget(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3.4": "ip 1.2.3.4", "1.2.3.4/32": "ip 1.2.3.4", "10.0.5.0/24": "ip_range 10.0.5.0/24", "10.1.2.3/16": "ip_range 10.1.0.0/16",
		"2001:db8::1": "ip6 2001:db8::1", "2001:db8::/48": "ip_range 2001:db8::/48", "10.0.0.0/20": "", "nope": "",
	} {
		ty, v, ok := accessTarget(in)
		got := ""
		if ok {
			got = ty + " " + v
		}
		if got != want {
			t.Errorf("accessTarget(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestSyncBlocks(t *testing.T) {
	var mu sync.Mutex
	rules := map[string]AccessRule{}
	n := 0
	add := func(v, notes string) {
		n++
		r := AccessRule{ID: fmt.Sprint(n), Mode: "block", Notes: notes}
		r.Configuration.Target, r.Configuration.Value = "ip", v
		rules[r.ID] = r
	}
	add("9.9.9.9", AccessNote)
	add("8.8.4.4", "someone else's rule")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reply := func(v any) { b, _ := json.Marshal(v); fmt.Fprintf(w, `{"success":true,"result":%s}`, b) }
		switch {
		case r.Method == http.MethodGet:
			out := []AccessRule{}
			for _, x := range rules {
				if x.Notes == r.URL.Query().Get("notes") {
					out = append(out, x)
				}
			}
			reply(out)
		case r.Method == http.MethodPost:
			var body struct {
				Notes         string
				Configuration struct{ Value string }
			}
			json.NewDecoder(r.Body).Decode(&body)
			add(body.Configuration.Value, body.Notes)
			reply(map[string]string{})
		case r.Method == http.MethodDelete:
			delete(rules, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
			reply(map[string]string{})
		}
	}))
	defer ts.Close()
	BaseURL = ts.URL
	added, removed, skipped, err := New("tok").SyncBlocks(context.Background(), "z1", []string{"1.2.3.4", "10.0.0.0/20"})
	if err != nil || added != 1 || removed != 1 || len(skipped) != 1 {
		t.Fatalf("added=%d removed=%d skipped=%v err=%v", added, removed, skipped, err)
	}
	var vals []string
	for _, r := range rules {
		vals = append(vals, r.Configuration.Value+"/"+r.Notes)
	}
	sort.Strings(vals)
	if strings.Join(vals, ",") != "1.2.3.4/lmcwhm,8.8.4.4/someone else's rule" {
		t.Fatalf("rules = %v", vals)
	}
}
