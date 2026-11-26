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

func TestRecoveryEventIsNotClearedAsRemoved(t *testing.T) {
	var got []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		got = append(got, m)
	}))
	defer hook.Close()
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	box, _ := secrets.Open(dir)
	sv := New(st, box)
	ctx := context.Background()
	ch, err := sv.SaveChannel(ctx, Channel{Name: "hook", Kind: Webhook, Enabled: true, Config: ChannelConfig{URL: hook.URL}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sv.SaveRule(ctx, Rule{Name: "services", Kind: KindServiceDown, Channels: []int64{ch.ID}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	sv.Src.Services = func(context.Context) map[string]string { return map[string]string{} }
	sv.Emit(ctx, KindServiceDown, "recovery:lmcwhm-mongodb70", "critical", "Container recovery paused", "not ready", false)
	sv.Check(ctx)
	if len(got) != 1 || got[0]["title"] != "Container recovery paused" {
		t.Fatalf("recovery event was cleared by service polling: %v", got)
	}
	sv.Emit(ctx, KindServiceDown, "recovery:lmcwhm-mongodb70", "resolved", "Container recovered", "ready", true)
	if len(got) != 2 || got[1]["severity"] != "resolved" {
		t.Fatalf("explicit recovery resolution missing: %v", got)
	}
}

type capturePush struct{ messages []Message }

func (p *capturePush) Send(_ context.Context, _ []int64, m Message) error {
	p.messages = append(p.messages, m)
	return nil
}

func TestEventDeepLinkAndLifecycleRecord(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "p.db"))
	defer st.Close()
	box, _ := secrets.Open(dir)
	sv := New(st, box)
	push := &capturePush{}
	sv.Push = push
	ctx := context.Background()
	ch, err := sv.SaveChannel(ctx, Channel{Name: "push", Kind: AppPush, Enabled: true, Config: ChannelConfig{DeviceIDs: []int64{1}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sv.SaveRule(ctx, Rule{Name: "logins", Kind: KindPanelLogin, Channels: []int64{ch.ID}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	sv.Emit(ctx, KindPanelLogin, "1", "info", "Panel login", "admin", false)
	if len(push.messages) != 1 || push.messages[0].EventID == 0 {
		t.Fatalf("push has no recorded event ID: %+v", push.messages)
	}
	e, err := sv.Event(ctx, push.messages[0].EventID)
	if err != nil || e.Title != "Panel login" || e.RuleName != "logins" || e.Result != "push: ok" {
		t.Fatalf("recorded event: %+v, %v", e, err)
	}
	id, err := sv.Record(ctx, KindPanelRestart, "process", "info", "Panel started / restarted", "Version test")
	if err != nil || id == 0 || len(push.messages) != 1 {
		t.Fatalf("lifecycle record sent a push: id=%d messages=%d err=%v", id, len(push.messages), err)
	}
	e, err = sv.Event(ctx, id)
	if err != nil || e.Kind != KindPanelRestart || e.RuleName != "System" || e.Result != "" {
		t.Fatalf("lifecycle event: %+v, %v", e, err)
	}
	if _, err := sv.Event(ctx, 999999); err != ErrNotFound {
		t.Fatalf("missing event: %v", err)
	}
}
