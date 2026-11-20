package alert

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

type fakePushSender struct {
	ids  []int64
	kind string
}

func (f *fakePushSender) Send(_ context.Context, ids []int64, message Message) error {
	f.ids, f.kind = ids, message.Kind
	return nil
}

func TestAppPushChannel(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sv := New(st, box)
	push := &fakePushSender{}
	sv.Push = push
	channel, err := sv.SaveChannel(context.Background(), Channel{Name: "Phone", Kind: AppPush, Enabled: true, Config: ChannelConfig{DeviceIDs: []int64{2, 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sv.TestChannel(context.Background(), channel.ID); err != nil {
		t.Fatal(err)
	}
	if len(push.ids) != 2 || push.ids[0] != 2 || push.ids[1] != 4 || push.kind != "test" {
		t.Fatalf("push delivery: %+v", push)
	}
	if _, err := sv.SaveChannel(context.Background(), Channel{Name: "Empty", Kind: AppPush, Config: ChannelConfig{}}); err == nil {
		t.Fatal("push channel without devices accepted")
	}
}
