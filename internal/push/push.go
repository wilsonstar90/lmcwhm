package push

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/wilsonstar90/lmcwhm/internal/alert"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/store"
)

var (
	ErrSubscription = errors.New("push.invalid_subscription")
	ErrNotFound     = errors.New("push.not_found")
)

type Device struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
	LastUsed  int64  `json:"last_used,omitempty"`
}

type SubscribeInput struct {
	Name     string `json:"name"`
	ClientID string `json:"client_id"`
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

type storedSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

type Service struct {
	st      *store.Store
	box     *secrets.Box
	now     func() time.Time
	Contact func() string
}

func New(st *store.Store, box *secrets.Box) *Service {
	return &Service{st: st, box: box, now: time.Now}
}

func (sv *Service) VAPIDPublic(ctx context.Context) (string, error) {
	var public, sealed string
	err := sv.st.DB.QueryRowContext(ctx, `SELECT public_key, private_key FROM push_settings WHERE id=1`).Scan(&public, &sealed)
	if err == nil {
		return public, nil
	}
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", err
	}
	if _, err := sv.st.DB.ExecContext(ctx, `INSERT INTO push_settings(id, public_key, private_key) VALUES(1,?,?)`,
		publicKey, sv.box.Seal(privateKey)); err != nil {
		if queryErr := sv.st.DB.QueryRowContext(ctx, `SELECT public_key FROM push_settings WHERE id=1`).Scan(&public); queryErr == nil {
			return public, nil
		}
		return "", err
	}
	return publicKey, nil
}

func (sv *Service) vapid(ctx context.Context) (string, string, error) {
	var public, sealed string
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT public_key, private_key FROM push_settings WHERE id=1`).Scan(&public, &sealed); err != nil {
		return "", "", err
	}
	private, err := sv.box.Open(sealed)
	return public, private, err
}

func validClientID(id string) bool {
	if id == "" {
		return true
	}
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	for _, c := range []byte(id) {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func (sv *Service) Subscribe(ctx context.Context, in SubscribeInput) (Device, error) {
	in.Name = strings.TrimSpace(in.Name)
	u, err := url.Parse(in.Endpoint)
	if in.Name == "" || len(in.Name) > 64 || !validClientID(in.ClientID) || err != nil || u.Scheme != "https" || u.Host == "" || len(in.Endpoint) > 4096 || in.Keys.P256dh == "" || in.Keys.Auth == "" {
		return Device{}, ErrSubscription
	}
	stored := storedSubscription{Endpoint: in.Endpoint}
	stored.Keys.P256dh, stored.Keys.Auth = in.Keys.P256dh, in.Keys.Auth
	body, _ := json.Marshal(stored)
	hash := sha256.Sum256([]byte(in.Endpoint))
	hashText := hex.EncodeToString(hash[:])
	now := sv.now().Unix()
	var id int64
	if in.ClientID != "" {
		err = sv.st.DB.QueryRowContext(ctx, `SELECT id FROM push_subscriptions WHERE client_id=?`, in.ClientID).Scan(&id)
		switch {
		case err == nil:
			if _, err := sv.st.DB.ExecContext(ctx, `UPDATE push_subscriptions SET name=?, endpoint_hash=?, subscription=?, enabled=1 WHERE id=?`,
				in.Name, hashText, sv.box.Seal(string(body)), id); err != nil {
				return Device{}, err
			}
			return Device{ID: id, Name: in.Name, Enabled: true, CreatedAt: now}, nil
		case !errors.Is(err, sql.ErrNoRows):
			return Device{}, err
		}
	}
	if _, err := sv.st.DB.ExecContext(ctx, `INSERT INTO push_subscriptions(name, client_id, endpoint_hash, subscription, enabled, created_at) VALUES(?,?,?,?,1,?)
		ON CONFLICT(endpoint_hash) DO UPDATE SET name=excluded.name, client_id=excluded.client_id, subscription=excluded.subscription, enabled=1`,
		in.Name, in.ClientID, hashText, sv.box.Seal(string(body)), now); err != nil {
		return Device{}, err
	}
	if err := sv.st.DB.QueryRowContext(ctx, `SELECT id FROM push_subscriptions WHERE endpoint_hash=?`, hashText).Scan(&id); err != nil {
		return Device{}, err
	}
	return Device{ID: id, Name: in.Name, Enabled: true, CreatedAt: now}, nil
}

func (sv *Service) List(ctx context.Context) ([]Device, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, name, enabled, created_at, last_used FROM push_subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.Enabled, &d.CreatedAt, &d.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (sv *Service) Revoke(ctx context.Context, id int64) error {
	result, err := sv.st.DB.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func alertDeepLink(eventID int64) string {
	if eventID > 0 {
		return fmt.Sprintf("/alerts?event=%d", eventID)
	}
	return "/alerts"
}

func (sv *Service) Send(ctx context.Context, deviceIDs []int64, message alert.Message) error {
	if len(deviceIDs) == 0 {
		return ErrSubscription
	}
	public, private, err := sv.vapid(ctx)
	if err != nil {
		return err
	}
	placeholders := make([]string, len(deviceIDs))
	args := make([]any, len(deviceIDs))
	for i, id := range deviceIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, subscription FROM push_subscriptions WHERE enabled=1 AND id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return err
	}
	type candidate struct {
		id     int64
		sealed string
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.sealed); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if len(candidates) == 0 {
		return ErrNotFound
	}
	payload, _ := json.Marshal(map[string]any{"title": message.Title, "body": message.Body, "severity": message.Severity,
		"kind": message.Kind, "host": message.Host, "time": message.Time, "event_id": message.EventID, "url": alertDeepLink(message.EventID)})
	subscriber := "https://lmcwhm.lmc.ltd"
	if sv.Contact != nil && strings.TrimSpace(sv.Contact()) != "" {
		subscriber = strings.TrimSpace(sv.Contact())
	}
	errs := []string{}
	for _, c := range candidates {
		plain, err := sv.box.Open(c.sealed)
		if err != nil {
			return err
		}
		var stored storedSubscription
		if err := json.Unmarshal([]byte(plain), &stored); err != nil {
			return err
		}
		subscription := &webpush.Subscription{Endpoint: stored.Endpoint}
		subscription.Keys.P256dh, subscription.Keys.Auth = stored.Keys.P256dh, stored.Keys.Auth
		urgency := webpush.Urgency("normal")
		if message.Severity == "critical" {
			urgency = webpush.Urgency("high")
		}
		res, err := webpush.SendNotificationWithContext(ctx, payload, subscription, &webpush.Options{
			Subscriber: subscriber, VAPIDPublicKey: public, VAPIDPrivateKey: private, TTL: 3600, Urgency: urgency, HTTPClient: httpc,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("device %d: push delivery failed", c.id))
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		res.Body.Close()
		if res.StatusCode >= 400 {
			if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone {
				_, _ = sv.st.DB.ExecContext(ctx, `UPDATE push_subscriptions SET enabled=0 WHERE id=?`, c.id)
			}
			errs = append(errs, fmt.Sprintf("device %d: HTTP %d", c.id, res.StatusCode))
			continue
		}
		_, _ = sv.st.DB.ExecContext(ctx, `UPDATE push_subscriptions SET last_used=? WHERE id=?`, sv.now().Unix(), c.id)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

var httpc = &http.Client{Timeout: 20 * time.Second}
