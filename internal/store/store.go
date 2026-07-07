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
	"sync"

	"github.com/disgoorg/snowflake/v2"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store is the SQLite-backed persistence layer. It is safe for concurrent
// use; open one per process with Open.
type Store struct {
	db *sql.DB

	// channels is a write-through mirror of the honeypot_channels table:
	// loaded once at Open, updated by every mutating method after its DB
	// write succeeds, and serving all channel reads (the table is one row
	// per guild, but GetChannelByID sits on the per-message hot path).
	// This process owns the table — external edits to the database won't
	// be seen until restart.
	mu       sync.RWMutex
	channels map[snowflake.ID]Channel
}

// Open opens (creating if needed) the SQLite database at path, applies any
// pending migrations, and preloads the channel mirror.
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
	channels, err := loadChannels(db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("load channels: %w", err)
	}
	return &Store{db: db, channels: channels}, nil
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
