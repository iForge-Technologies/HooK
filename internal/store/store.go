// Package store owns the SQLite database: schema migrations and all queries.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"time"

	"hook/internal/secret"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

var ErrNotFound = errors.New("not found")

type Store struct {
	db  *sql.DB
	box *secret.Box
}

func Open(dataDir string, box *secret.Box) (*Store, error) {
	dsn := "file:" + filepath.ToSlash(filepath.Join(dataDir, "hook.db")) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite has one writer; a small pool keeps reads concurrent without lock churn.
	db.SetMaxOpenConns(4)
	s := &Store{db: db, box: box}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrate applies migrations/NNN_*.sql files above PRAGMA user_version.
func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for i, name := range names {
		n := i + 1
		if n <= version {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, n)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func nowMs() int64 { return time.Now().UnixMilli() }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
