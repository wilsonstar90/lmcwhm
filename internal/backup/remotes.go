package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RemoteView is a remote as shown to the admin (no secrets).
type RemoteView struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	ClientID   string  `json:"client_id"`
	Tenant     string  `json:"tenant,omitempty"`
	Account    string  `json:"account"`
	Authorized bool    `json:"authorized"`
	Device     *Device `json:"device,omitempty"`
	CreatedAt  int64   `json:"created_at"`
}

type RemoteInput struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Tenant       string `json:"tenant"`
}

type remoteRow struct {
	id      int64
	name    string
	kind    string
	cfg     RemoteConfig
	created int64
}

func (sv *Service) remoteRow(ctx context.Context, id int64) (remoteRow, error) {
	var r remoteRow
	var sealed string
	err := sv.st.DB.QueryRowContext(ctx, `SELECT id, name, kind, config, created_at FROM backup_remotes WHERE id = ?`, id).
		Scan(&r.id, &r.name, &r.kind, &sealed, &r.created)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	plain, err := sv.box.Open(sealed)
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal([]byte(plain), &r.cfg)
}

func (sv *Service) saveConfig(ctx context.Context, id int64, cfg *RemoteConfig) error {
	b, _ := json.Marshal(cfg)
	_, err := sv.st.DB.ExecContext(ctx, `UPDATE backup_remotes SET config = ? WHERE id = ?`, sv.box.Seal(string(b)), id)
	return err
}

func (r remoteRow) view() RemoteView {
	v := RemoteView{ID: r.id, Name: r.name, Kind: r.kind, ClientID: r.cfg.ClientID, Tenant: r.cfg.Tenant,
		Account: r.cfg.Account, Authorized: r.cfg.Token != nil && r.cfg.Token.Refresh != "", Device: r.cfg.Device, CreatedAt: r.created}
	return v
}

func (sv *Service) Remotes(ctx context.Context) ([]RemoteView, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id FROM backup_remotes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []RemoteView{}
	for _, id := range ids {
		r, err := sv.remoteRow(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r.view())
	}
	return out, nil
}

// AddRemote stores a remote and starts its device authorisation.
func (sv *Service) AddRemote(ctx context.Context, in RemoteInput) (RemoteView, error) {
	in.Name, in.ClientID = strings.TrimSpace(in.Name), strings.TrimSpace(in.ClientID)
	if in.Kind != KindGDrive && in.Kind != KindOneDrive || in.Name == "" || len(in.Name) > 64 || in.ClientID == "" {
		return RemoteView{}, ErrRemoteKind
	}
	cfg := RemoteConfig{ClientID: in.ClientID, ClientSecret: strings.TrimSpace(in.ClientSecret), Tenant: strings.TrimSpace(in.Tenant)}
	if err := cfg.StartDevice(ctx, in.Kind); err != nil {
		return RemoteView{}, fmt.Errorf("%w: %v", ErrRemoteAuth, err)
	}
	b, _ := json.Marshal(cfg)
	res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO backup_remotes(name, kind, config, created_at) VALUES(?,?,?,?)`,
		in.Name, in.Kind, sv.box.Seal(string(b)), sv.now().Unix())
	if err != nil {
		return RemoteView{}, err
	}
	id, _ := res.LastInsertId()
	r, err := sv.remoteRow(ctx, id)
	return r.view(), err
}

// Reauthorize starts a new device authorisation (e.g. after a revoked token).
func (sv *Service) Reauthorize(ctx context.Context, id int64) (RemoteView, error) {
	r, err := sv.remoteRow(ctx, id)
	if err != nil {
		return RemoteView{}, err
	}
	if err := r.cfg.StartDevice(ctx, r.kind); err != nil {
		return RemoteView{}, fmt.Errorf("%w: %v", ErrRemoteAuth, err)
	}
	if err := sv.saveConfig(ctx, id, &r.cfg); err != nil {
		return RemoteView{}, err
	}
	return r.view(), nil
}

// PollRemote checks the pending authorisation once; ErrAuthPending means try again.
func (sv *Service) PollRemote(ctx context.Context, id int64) (RemoteView, error) {
	r, err := sv.remoteRow(ctx, id)
	if err != nil {
		return RemoteView{}, err
	}
	err = r.cfg.PollDevice(ctx, r.kind)
	if err != nil && !errors.Is(err, ErrAuthPending) && !errors.Is(err, ErrAuthExpired) {
		return r.view(), fmt.Errorf("%w: %v", ErrRemoteAuth, err)
	}
	if err == nil {
		ts := &tokenSource{kind: r.kind, cfg: &r.cfg}
		if r.kind == KindGDrive {
			r.cfg.Account = (&gdrive{ts}).account(ctx)
		} else {
			r.cfg.Account = (&onedrive{ts}).account(ctx)
		}
	}
	if serr := sv.saveConfig(ctx, id, &r.cfg); serr != nil {
		return RemoteView{}, serr
	}
	return r.view(), err
}

func (sv *Service) DeleteRemote(ctx context.Context, id int64) error {
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM backup_remotes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	// Copies stored there are no longer reachable from the panel.
	_, err = sv.st.DB.ExecContext(ctx, `DELETE FROM backups WHERE dest = ?`, fmt.Sprintf("remote:%d", id))
	return err
}

// remote builds a client whose refreshed tokens are written back.
func (sv *Service) remote(ctx context.Context, id int64) (Remote, error) {
	r, err := sv.remoteRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.cfg.Token == nil {
		return nil, ErrRemoteAuth
	}
	ts := &tokenSource{kind: r.kind, cfg: &r.cfg, saved: func(c *RemoteConfig) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sv.saveConfig(ctx, id, c)
	}}
	if r.kind == KindGDrive {
		return &gdrive{ts}, nil
	}
	return &onedrive{ts}, nil
}

func (sv *Service) destName(ctx context.Context, dest string) string {
	if dest == DestLocal {
		return DestLocal
	}
	if id, ok := remoteID(dest); ok {
		var name string
		if sv.st.DB.QueryRowContext(ctx, `SELECT name FROM backup_remotes WHERE id = ?`, id).Scan(&name) == nil {
			return name
		}
	}
	return dest
}
