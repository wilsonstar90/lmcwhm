// Package database creates databases and their users on the engines installed
// from the App Store (MySQL, MariaDB, PostgreSQL, MongoDB, SQL Server).
package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

var (
	ErrBadName     = errors.New("db.invalid_name")
	ErrBadPassword = errors.New("db.invalid_password")
	ErrExists      = errors.New("db.exists")
	ErrNotFound    = errors.New("db.not_found")
	ErrNoEngine    = errors.New("db.engine_unsupported")
)

type Database struct {
	ID        int64  `json:"id"`
	AppID     int64  `json:"app_id"`
	Engine    string `json:"engine"`  // catalog key
	Version   string `json:"version"` // engine version
	Name      string `json:"name"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Remark    string `json:"remark"`
	CreatedAt int64  `json:"created_at"`
}

// Identifiers and passwords are spliced into SQL / JavaScript, so both are
// restricted to characters that never need quoting.
var (
	nameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,31}$`)
	passRe = regexp.MustCompile(`^[A-Za-z0-9_@#%+=.,:-]{8,64}$`)
)

var reserved = map[string]bool{
	"mysql": true, "sys": true, "information_schema": true, "performance_schema": true, "root": true,
	"postgres": true, "template0": true, "template1": true, "public": true,
	"admin": true, "local": true, "config": true,
	"master": true, "model": true, "msdb": true, "tempdb": true, "sa": true,
}

func ValidName(n string) bool { return nameRe.MatchString(n) && !reserved[strings.ToLower(n)] }

type Service struct {
	st    *store.Store
	box   *secrets.Box
	apps  *appstore.Service
	tasks *tasks.Manager
}

func New(st *store.Store, box *secrets.Box, apps *appstore.Service, tm *tasks.Manager) *Service {
	return &Service{st: st, box: box, apps: apps, tasks: tm}
}

func (sv *Service) List(ctx context.Context) ([]Database, error) {
	return sv.query(ctx, "")
}

func (sv *Service) Get(ctx context.Context, id int64) (Database, error) {
	all, err := sv.query(ctx, "WHERE d.id = ?", id)
	if err != nil {
		return Database{}, err
	}
	if len(all) == 0 {
		return Database{}, ErrNotFound
	}
	return all[0], nil
}

func (sv *Service) query(ctx context.Context, where string, args ...any) ([]Database, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT d.id, d.app_id, a.app, a.version, a.port, d.name, d.username, d.password, d.remark, d.created_at
		FROM databases d JOIN apps a ON a.id = d.app_id `+where+` ORDER BY d.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		var d Database
		var sealed string
		if err := rows.Scan(&d.ID, &d.AppID, &d.Engine, &d.Version, &d.Port, &d.Name, &d.Username, &sealed, &d.Remark, &d.CreatedAt); err != nil {
			return nil, err
		}
		if d.Password, err = sv.box.Open(sealed); err != nil {
			return nil, err
		}
		d.Host = "127.0.0.1"
		out = append(out, d)
	}
	return out, rows.Err()
}

type CreateInput struct {
	AppID    int64  `json:"app_id"`
	Name     string `json:"name"`
	Username string `json:"username"` // default: Name
	Password string `json:"password"` // default: generated
	Remark   string `json:"remark"`
}

func (sv *Service) engine(ctx context.Context, appID int64) (appstore.Installed, error) {
	i, err := sv.apps.Get(ctx, appID)
	if err != nil {
		return i, err
	}
	if !i.Databases {
		return i, ErrNoEngine
	}
	return i, nil
}

// Create makes the database and a user owning it (task).
func (sv *Service) Create(ctx context.Context, in CreateInput) (string, error) {
	eng, err := sv.engine(ctx, in.AppID)
	if err != nil {
		return "", err
	}
	if in.Username == "" {
		in.Username = in.Name
	}
	if in.Password == "" {
		in.Password = settings.RandomString(20, settings.Alnum)
	}
	if !ValidName(in.Name) || !ValidName(in.Username) {
		return "", ErrBadName
	}
	if !passRe.MatchString(in.Password) {
		return "", ErrBadPassword
	}
	var n int
	sv.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM databases WHERE app_id = ? AND (name = ? OR username = ?)`, in.AppID, in.Name, in.Username).Scan(&n)
	if n > 0 {
		return "", ErrExists
	}
	return sv.tasks.Start("db.create:"+in.Name, func(ctx context.Context, log tasks.Logf) error {
		log("creating database %s and user %s on %s %s", in.Name, in.Username, eng.Name, eng.Version)
		if err := sv.exec(ctx, eng, createScript(eng.App, in.Name, in.Username, in.Password)); err != nil {
			return err
		}
		_, err := sv.st.DB.ExecContext(ctx, `INSERT INTO databases(app_id, name, username, password, remark, created_at) VALUES(?,?,?,?,?,?)`,
			in.AppID, in.Name, in.Username, sv.box.Seal(in.Password), in.Remark, time.Now().Unix())
		if err != nil {
			return err
		}
		log("done: %s@127.0.0.1:%d/%s", in.Username, eng.Port, in.Name)
		return nil
	}), nil
}

// Delete drops the database and its user (task).
func (sv *Service) Delete(ctx context.Context, id int64) (string, error) {
	d, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	eng, err := sv.engine(ctx, d.AppID)
	if err != nil {
		return "", err
	}
	return sv.tasks.Start("db.delete:"+d.Name, func(ctx context.Context, log tasks.Logf) error {
		log("dropping database %s and user %s", d.Name, d.Username)
		if err := sv.exec(ctx, eng, dropScript(eng.App, d.Name, d.Username)); err != nil {
			return err
		}
		_, err := sv.st.DB.ExecContext(ctx, `DELETE FROM databases WHERE id = ?`, id)
		return err
	}), nil
}

// SetPassword changes the user's password ("" = generate one).
func (sv *Service) SetPassword(ctx context.Context, id int64, pass string) (string, error) {
	d, err := sv.Get(ctx, id)
	if err != nil {
		return "", err
	}
	eng, err := sv.engine(ctx, d.AppID)
	if err != nil {
		return "", err
	}
	if pass == "" {
		pass = settings.RandomString(20, settings.Alnum)
	}
	if !passRe.MatchString(pass) {
		return "", ErrBadPassword
	}
	if err := sv.exec(ctx, eng, passwordScript(eng.App, d.Name, d.Username, pass)); err != nil {
		return "", err
	}
	_, err = sv.st.DB.ExecContext(ctx, `UPDATE databases SET password = ? WHERE id = ?`, sv.box.Seal(pass), id)
	return pass, err
}

func (sv *Service) SetRemark(ctx context.Context, id int64, remark string) error {
	res, err := sv.st.DB.ExecContext(ctx, `UPDATE databases SET remark = ? WHERE id = ?`, remark, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (sv *Service) exec(ctx context.Context, eng appstore.Installed, script string) error {
	var err error
	if eng.App == "mongodb" {
		_, err = sv.apps.Mongo(ctx, eng, script)
	} else {
		_, err = sv.apps.Query(ctx, eng, script)
	}
	return err
}

// ---- engine scripts ----

func createScript(engine, db, user, pass string) string {
	switch engine {
	case "mysql", "mariadb":
		return fmt.Sprintf("CREATE DATABASE `%[1]s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\n"+
			"CREATE USER '%[2]s'@'%%' IDENTIFIED BY '%[3]s';\n"+
			"GRANT ALL PRIVILEGES ON `%[1]s`.* TO '%[2]s'@'%%';\nFLUSH PRIVILEGES;\n", db, user, pass)
	case "postgres":
		return fmt.Sprintf("CREATE USER \"%[2]s\" WITH PASSWORD '%[3]s';\nCREATE DATABASE \"%[1]s\" OWNER \"%[2]s\" ENCODING 'UTF8';\n", db, user, pass)
	case "mongodb":
		return fmt.Sprintf("db.getSiblingDB('%[1]s').createUser({user: '%[2]s', pwd: '%[3]s', roles: [{role: 'dbOwner', db: '%[1]s'}]})", db, user, pass)
	case "sqlserver":
		return fmt.Sprintf("CREATE DATABASE [%[1]s]; CREATE LOGIN [%[2]s] WITH PASSWORD = '%[3]s', CHECK_POLICY = OFF, DEFAULT_DATABASE = [%[1]s]; "+
			"USE [%[1]s]; CREATE USER [%[2]s] FOR LOGIN [%[2]s]; ALTER ROLE db_owner ADD MEMBER [%[2]s];", db, user, pass)
	}
	return ""
}

func dropScript(engine, db, user string) string {
	switch engine {
	case "mysql", "mariadb":
		return fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;\nDROP USER IF EXISTS '%s'@'%%';\n", db, user)
	case "postgres":
		return fmt.Sprintf("DROP DATABASE IF EXISTS \"%s\" WITH (FORCE);\nDROP USER IF EXISTS \"%s\";\n", db, user)
	case "mongodb":
		return fmt.Sprintf("const d = db.getSiblingDB('%[1]s'); if (d.getUser('%[2]s')) { d.dropUser('%[2]s') }; d.dropDatabase()", db, user)
	case "sqlserver":
		return fmt.Sprintf("IF DB_ID('%[1]s') IS NOT NULL BEGIN ALTER DATABASE [%[1]s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE [%[1]s]; END; "+
			"IF SUSER_ID('%[2]s') IS NOT NULL DROP LOGIN [%[2]s];", db, user)
	}
	return ""
}

func passwordScript(engine, db, user, pass string) string {
	switch engine {
	case "mysql", "mariadb":
		return fmt.Sprintf("ALTER USER '%s'@'%%' IDENTIFIED BY '%s';\n", user, pass)
	case "postgres":
		return fmt.Sprintf("ALTER USER \"%s\" WITH PASSWORD '%s';\n", user, pass)
	case "mongodb":
		return fmt.Sprintf("db.getSiblingDB('%s').updateUser('%s', {pwd: '%s'})", db, user, pass)
	case "sqlserver":
		return fmt.Sprintf("ALTER LOGIN [%s] WITH PASSWORD = '%s';", user, pass)
	}
	return ""
}
