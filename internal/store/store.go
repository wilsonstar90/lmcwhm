// Package store owns the panel's SQLite database: schema migrations and the
// small set of tables the core needs (settings, users, sessions, logs).
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	DB     *sql.DB
	ReadDB *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single writer connection avoids SQLITE_BUSY churn.
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	readDSN := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=query_only(true)", path)
	readDB, err := sql.Open("sqlite", readDSN)
	if err != nil {
		db.Close()
		return nil, err
	}
	readDB.SetMaxOpenConns(4)
	if err := readDB.Ping(); err != nil {
		readDB.Close()
		db.Close()
		return nil, err
	}
	s.ReadDB = readDB
	return s, nil
}

func (s *Store) Read() *sql.DB {
	if s.ReadDB != nil {
		return s.ReadDB
	}
	return s.DB
}

func (s *Store) Close() error {
	var readErr error
	if s.ReadDB != nil {
		readErr = s.ReadDB.Close()
	}
	writeErr := s.DB.Close()
	if readErr != nil {
		return readErr
	}
	return writeErr
}

// migrate applies every embedded NNN_name.sql file whose number is above the
// recorded schema version, each in its own transaction.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var current int
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return err
	}
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		var n int
		if _, err := fmt.Sscanf(strings.TrimPrefix(name, "migrations/"), "%d_", &n); err != nil {
			return fmt.Errorf("bad migration name %s", name)
		}
		if n <= current {
			continue
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES (?)`, n); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
