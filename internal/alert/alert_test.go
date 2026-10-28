package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestRulesRaiseRepeatResolve(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	}))
	defer hook.Close()
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	box, _ := secrets.Open(dir)
	sv := New(st, box)
	clock := time.Unix(1_800_000_000, 0)
	sv.now = func() time.Time { return clock }
	ctx := context.Background()

	ch, err := sv.SaveChannel(ctx, Channel{Name: "hook", Kind: Webhook, Enabled: true, Config: ChannelConfig{URL: hook.URL}})
	if err != nil {
		t.Fatal(err)
	}
	tg, _ := sv.SaveChannel(ctx, Channel{Name: "tg", Kind: Telegram, Enabled: false, Config: ChannelConfig{BotToken: "123:abc", ChatID: "1"}})
	if list, _ := sv.Channels(ctx); strings.Contains(list[1].Config.BotToken, "abc") {
		t.Fatal("secret not masked")
	}
	// Saving the masked value keeps the secret.
	tg.Config.BotToken = "********"
	sv.SaveChannel(ctx, tg)
	if c, _ := sv.channel(ctx, tg.ID); c.Config.BotToken != "123:abc" {
		t.Fatalf("secret lost: %q", c.Config.BotToken)
	}

	cpu := 95.0
	sv.Src.Metrics = func(context.Context, time.Duration) (float64, float64, float64, float64, bool) {
		return cpu, 10, 1, 50, true
	}
	if _, err := sv.SaveRule(ctx, Rule{Name: "cpu", Kind: KindCPU, Params: Params{Threshold: 90, Minutes: 5}, Channels: []int64{ch.ID}, Enabled: true, Cooldown: 30}); err != nil {
		t.Fatal(err)
	}
	sv.Check(ctx)
	clock = clock.Add(10 * time.Minute)
	sv.Check(ctx) // within cooldown: no repeat
	clock = clock.Add(25 * time.Minute)
	sv.Check(ctx) // repeat
	cpu = 20
	sv.Check(ctx) // resolved
	sv.Check(ctx) // nothing
	if len(got) != 3 || got[0]["severity"] != "warning" || got[2]["severity"] != "resolved" {
		t.Fatalf("deliveries: %v", got)
	}
	evs, total, _ := sv.Events(ctx, 10, 0)
	if total != 3 || !strings.Contains(evs[0].Result, "hook: ok") {
		t.Fatalf("events: %d %+v", total, evs)
	}

	// Event rules.
	sv.SaveRule(ctx, Rule{Name: "probes", Kind: KindProbeDown, Channels: []int64{ch.ID}, Enabled: true})
	sv.Emit(ctx, KindProbeDown, "7", "critical", "site down", "HTTP 503", false)
	sv.Emit(ctx, KindProbeDown, "7", "critical", "site down", "HTTP 503", false) // still down: no repeat
	sv.Emit(ctx, KindProbeDown, "7", "resolved", "site up", "ok", true)
	if len(got) != 5 {
		t.Fatalf("probe deliveries: %d", len(got))
	}
	if _, err := sv.SaveRule(ctx, Rule{Name: "bad", Kind: KindCPU, Params: Params{Threshold: 500}, Channels: []int64{ch.ID}}); err == nil {
		t.Fatal("bad threshold accepted")
	}
}
