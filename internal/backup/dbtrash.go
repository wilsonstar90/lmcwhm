package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wilsonstar90/lmcwhm/internal/database"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

// The database recycle bin: a database dropped through the panel is dumped
// first, and can be created again from the dump with its user and password.

type TrashedDB struct {
	ID        int64  `json:"id"`
	AppID     int64  `json:"app_id"`
	Engine    string `json:"engine"`
	Name      string `json:"name"`
	Username  string `json:"username"`
	Remark    string `json:"remark"`
	Size      int64  `json:"size"`
	DeletedAt int64  `json:"deleted_at"`
	file      string
	sealed    string
}

var ErrTrashNotFound = errors.New("backup.trash_not_found")

func (sv *Service) trashDir() string { return filepath.Join(sv.Home, "data", "trash", "db") }

// TrashDatabase dumps d into the recycle bin (database.Service.BeforeDrop).
func (sv *Service) TrashDatabase(ctx context.Context, d database.Database, log tasks.Logf) error {
	eng, err := sv.Apps.Get(ctx, d.AppID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sv.trashDir(), 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%s_%s%s", sv.now().Format("20060102_150405"), safeName(d.Name), dumpExt(eng.App))
	path := filepath.Join(sv.trashDir(), name)
	log("keeping a dump of %s in the recycle bin", d.Name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = dumpDB(ctx, eng, d.Name, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return fmt.Errorf("dump for the recycle bin: %w", err)
	}
	fi, _ := os.Stat(path)
	_, err = sv.st.DB.ExecContext(ctx, `INSERT INTO db_trash(app_id, engine, name, username, password, remark, file, size, deleted_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, d.AppID, eng.App, d.Name, d.Username, sv.box.Seal(d.Password), d.Remark, name, fi.Size(), sv.now().Unix())
	if err != nil {
		os.Remove(path)
	}
	return err
}

func (sv *Service) trashed(ctx context.Context, where string, args ...any) ([]TrashedDB, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, app_id, engine, name, username, password, remark, file, size, deleted_at
		FROM db_trash `+where+` ORDER BY deleted_at DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrashedDB{}
	for rows.Next() {
		var t TrashedDB
		if err := rows.Scan(&t.ID, &t.AppID, &t.Engine, &t.Name, &t.Username, &t.sealed, &t.Remark, &t.file, &t.Size, &t.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (sv *Service) TrashedDatabases(ctx context.Context) ([]TrashedDB, error) {
	return sv.trashed(ctx, "")
}

func (sv *Service) trashedOne(ctx context.Context, id int64) (TrashedDB, error) {
	l, err := sv.trashed(ctx, "WHERE id = ?", id)
	if err != nil {
		return TrashedDB{}, err
	}
	if len(l) == 0 {
		return TrashedDB{}, ErrTrashNotFound
	}
	return l[0], nil
}

// RestoreTrashedDB creates the database and its user again, loads the
// dump and removes it from the bin.
func (sv *Service) RestoreTrashedDB(ctx context.Context, id int64, log tasks.Logf) error {
	t, err := sv.trashedOne(ctx, id)
	if err != nil {
		return err
	}
	pass, err := sv.box.Open(t.sealed)
	if err != nil {
		return err
	}
	eng, err := sv.Apps.Get(ctx, t.AppID)
	if err != nil {
		return database.ErrNoEngine
	}
	d, err := sv.DBs.CreateNow(ctx, database.CreateInput{AppID: t.AppID, Name: t.Name, Username: t.Username, Password: pass, Remark: t.Remark}, log)
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(sv.trashDir(), t.file))
	if err != nil {
		return err
	}
	defer f.Close()
	log("loading the dump into %s", d.Name)
	if err := restoreDB(ctx, eng, d.Name, f); err != nil {
		return err
	}
	return sv.PurgeTrashedDB(ctx, t.ID)
}

// PurgeTrashedDB deletes a dump for good.
func (sv *Service) PurgeTrashedDB(ctx context.Context, id int64) error {
	t, err := sv.trashedOne(ctx, id)
	if err != nil {
		return err
	}
	os.Remove(filepath.Join(sv.trashDir(), t.file))
	_, err = sv.st.DB.ExecContext(ctx, `DELETE FROM db_trash WHERE id = ?`, id)
	return err
}

// PurgeAllTrashedDBs empties the database recycle bin.
func (sv *Service) PurgeAllTrashedDBs(ctx context.Context) error {
	all, err := sv.TrashedDatabases(ctx)
	if err != nil {
		return err
	}
	for _, t := range all {
		if err := sv.PurgeTrashedDB(ctx, t.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

