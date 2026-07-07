// Package store persists honeypot configuration and moderation events in SQLite.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A single connection sidesteps SQLITE_BUSY entirely at this bot's scale.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS _migrations (
		version     INTEGER PRIMARY KEY,
		name        TEXT NOT NULL,
		executed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}
	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, file := range files {
		base := filepath.Base(file)
		version, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration filename %s: %w", base, err)
		}
		var applied bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM _migrations WHERE version = ?)`, version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sqlText, err := migrationsFS.ReadFile(file)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(sqlText)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", base, err)
		}
		if _, err := tx.Exec(`INSERT INTO _migrations (version, name) VALUES (?, ?)`, version, base); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
