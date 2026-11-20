package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wilsonstar90/lmcwhm/internal/alert"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

func TestDeviceSubscriptionPersistsWithoutSession(t *testing.T) {
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
	ctx := context.Background()
	first, err := sv.VAPIDPublic(ctx)
	if err != nil || first == "" {
		t.Fatalf("VAPID: %v", err)
	}
	if second, _ := sv.VAPIDPublic(ctx); second != first {
		t.Fatal("VAPID key changed")
	}
	var in SubscribeInput
	in.Name = "iPad mini"
	in.Endpoint = "https://push.example.test/device/secret-endpoint"
	in.Keys.P256dh, in.Keys.Auth = "p256dh-key", "auth-key"
	device, err := sv.Subscribe(ctx, in)
	if err != nil || device.ID == 0 {
		t.Fatalf("subscribe: %+v %v", device, err)
	}
	var sealed string
	if err := st.DB.QueryRow(`SELECT subscription FROM push_subscriptions WHERE id=?`, device.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, in.Endpoint) || strings.Contains(sealed, in.Keys.Auth) {
		t.Fatal("subscription secrets stored in plaintext")
	}
	items, err := sv.List(ctx)
	if err != nil || len(items) != 1 || items[0].Name != "iPad mini" || !items[0].Enabled {
		t.Fatalf("device list after logout: %+v %v", items, err)
	}
	if err := sv.Revoke(ctx, device.ID); err != nil {
		t.Fatal(err)
	}
	if items, _ := sv.List(ctx); len(items) != 0 {
		t.Fatal("revoked device still subscribed")
	}
}

func hasValidVAPIDSubject(r *http.Request) bool {
	header := strings.TrimPrefix(r.Header.Get("Authorization"), "vapid t=")
	token, _, _ := strings.Cut(header, ", k=")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims map[string]any
	return json.Unmarshal(body, &claims) == nil && claims["sub"] == "https://lmcwhm.lmc.ltd"
}

func TestWebPushDelivery(t *testing.T) {
	received := make(chan bool, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- len(body) > 0 && r.Header.Get("Content-Encoding") == "aes128gcm" && strings.HasPrefix(r.Header.Get("Authorization"), "vapid ") && hasValidVAPIDSubject(r)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	oldClient := httpc
	httpc = server.Client()
	defer func() { httpc = oldClient }()
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "panel.db"))
	defer st.Close()
	box, _ := secrets.Open(dir)
	sv := New(st, box)
	ctx := context.Background()
	if _, err := sv.VAPIDPublic(ctx); err != nil {
		t.Fatal(err)
	}
	var in SubscribeInput
	in.Name = "iPhone"
	in.Endpoint = server.URL + "/device"
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	in.Keys.P256dh = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	in.Keys.Auth = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 16))
	device, err := sv.Subscribe(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := sv.Send(ctx, []int64{device.ID}, alert.Message{Title: "Test", Body: "Push", Severity: "critical", Kind: "test", Host: "host"}); err != nil {
		t.Fatal(err)
	}
	if ok := <-received; !ok {
		t.Fatal("push service did not receive an encrypted VAPID request")
	}
}
