# Joe's Honeypot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single-binary Go Discord honeypot bot (Joe's Honeypot) that softbans/bans anyone posting in a designated honeypot channel, with SQLite config/event storage and genjishimada-style compose + GitHub Actions deployment.

**Architecture:** One Go process per environment using the disgo gateway (`Guilds | GuildMessages` intents only). SQLite via pure-Go `modernc.org/sqlite`; in-memory TTL maps replace Redis (30s moderation dedup, 24h DM-channel cache). Config UX is a modal opened by `/honeypot`.

**Tech Stack:** Go 1.24, disgo v0.19.6+, modernc.org/sqlite, docker compose, GitHub Actions (SSH docker-context deploys).

**Spec:** `docs/superpowers/specs/2026-07-06-joes-honeypot-design.md`

## Global Constraints

- Module path: `github.com/bkan0n/joeshoneypot`. Binary/entrypoint: `cmd/bot`.
- Go `1.24`. disgo floor: `v0.19.0` (Label components in modals); use latest `v0.19.x`.
- SQLite driver: `modernc.org/sqlite` only (CGO_ENABLED=0 everywhere). `db.SetMaxOpenConns(1)`.
- Actions enum values stored in DB exactly: `softban`, `ban`, `disabled`. Default `softban`.
- Ban message deletion window: `3600` seconds (pass `time.Hour` to disgo's `AddBan`).
- Dedup TTL 30s; DM-channel cache TTL 24h; unban button expiry 24h; intro message self-deletes after 2m30s; softban unban delay 250ms; DM send cap 2s.
- Gateway intents: `IntentGuilds | IntentGuildMessages`. Presence: Watching `#honeypot for bots`.
- Env vars: `BOT_TOKEN` (required), `DB_PATH` (default `/data/honeypot.db`), `LOG_LEVEL` (`info` default, `debug` enables debug).
- Our package `internal/bot` collides with disgo's `bot` package — always import disgo's as `dbot` and our `internal/cache` never in the same file as disgo's `cache` (alias disgo's as `dcache` if needed).
- disgo v0.19.x symbol names below were verified against the v0.19.6 tag (Label/select-in-modal API, `AddBan(guildID, userID, deleteDuration)`, `handler.SyncCommands`). If a compile error reveals a renamed symbol, find the equivalent with `go doc github.com/disgoorg/disgo/<pkg>` — the *semantics* in this plan are binding, exact symbol names are not. Do not change behavior to dodge a compile error.
- Every task: run `go build ./...` and `go test ./...` before committing.

## File Structure

```
.gitignore  .env.example  .golangci.yml  README.md  go.mod  go.sum
cmd/bot/main.go
internal/cache/ttl.go (+_test)
internal/store/store.go  queries.go  migrations/0001_init.sql (+_test)
internal/bot/bot.go  commands.go  lookalike.go  templates.go  rules.go
             handler_honeypot.go  handler_message.go  handler_buttons.go
             setup.go  housekeeping.go  (+_tests for pure logic)
Dockerfile  .dockerignore  docker-compose.prod.yml  docker-compose.dev.yml
.github/workflows/lint.yml  tests.yml  deploy-prod.yml  deploy-dev.yml
```

---

### Task 1: Repo scaffolding

**Files:**
- Create: `.gitignore`, `go.mod`, `.env.example`, `.golangci.yml`, `README.md`

**Interfaces:**
- Produces: module `github.com/bkan0n/joeshoneypot` targeted by all later imports.

- [ ] **Step 1: Create `.gitignore`**

```gitignore
.env.local
*.db
*.db-shm
*.db-wal
/joes-honeypot
```

- [ ] **Step 2: Init module**

Run: `go mod init github.com/bkan0n/joeshoneypot` — creates `go.mod` with `go 1.24`.

- [ ] **Step 3: Create `.env.example`**

```bash
# Discord bot token (required)
BOT_TOKEN=
# SQLite database path (default /data/honeypot.db; use ./honeypot.db locally)
DB_PATH=./honeypot.db
# info | debug
LOG_LEVEL=info
```

- [ ] **Step 4: Create `.golangci.yml`**

```yaml
version: "2"
linters:
  default: standard
```

- [ ] **Step 5: Create `README.md`**

```markdown
# Joe's Honeypot

Discord honeypot bot in Go. Designate a honeypot channel; any account that
posts there is automatically softbanned/banned (spam bots blast every
channel — real users read the warning). Modeled on
[RiskyMH/honeypot](https://github.com/RiskyMH/honeypot), minus experiments.

## Local development

    cp .env.example .env.local   # fill in BOT_TOKEN
    go run ./cmd/bot             # with the vars exported

See docs/superpowers/specs/ for the design.
```

- [ ] **Step 6: Verify and commit**

Run: `go build ./...` → no output, exit 0.

```bash
git add -A && git commit -m "chore: scaffold repo (gitignore, go.mod, env example, lint config)"
```

---

### Task 2: TTL cache (`internal/cache`)

**Files:**
- Create: `internal/cache/ttl.go`
- Test: `internal/cache/ttl_test.go`

**Interfaces:**
- Produces: `cache.NewTTL[K comparable, V any]() *TTL[K, V]` with methods `Set(k K, v V, ttl time.Duration)`, `Get(k K) (V, bool)`, `Delete(k K)`. Concurrency-safe. Expired entries report `(zero, false)`.

- [ ] **Step 1: Write the failing test** — `internal/cache/ttl_test.go`

```go
package cache

import (
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	c := NewTTL[string, int]()
	c.Set("a", 1, time.Minute)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("got %v %v, want 1 true", v, ok)
	}
}

func TestExpiry(t *testing.T) {
	c := NewTTL[string, int]()
	c.Set("a", 1, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected expired entry to be absent")
	}
}

func TestDelete(t *testing.T) {
	c := NewTTL[string, int]()
	c.Set("a", 1, time.Minute)
	c.Delete("a")
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected deleted entry to be absent")
	}
}

func TestMissing(t *testing.T) {
	c := NewTTL[string, int]()
	if v, ok := c.Get("nope"); ok || v != 0 {
		t.Fatalf("got %v %v, want 0 false", v, ok)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cache/` → FAIL (undefined: NewTTL).

- [ ] **Step 3: Implement** — `internal/cache/ttl.go`

```go
// Package cache provides a minimal concurrency-safe TTL map. It replaces the
// Redis caches of the original honeypot bot; entries are evicted lazily on Get.
package cache

import (
	"sync"
	"time"
)

type entry[V any] struct {
	val       V
	expiresAt time.Time
}

type TTL[K comparable, V any] struct {
	mu sync.Mutex
	m  map[K]entry[V]
}

func NewTTL[K comparable, V any]() *TTL[K, V] {
	return &TTL[K, V]{m: make(map[K]entry[V])}
}

func (c *TTL[K, V]) Set(k K, v V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = entry[V]{val: v, expiresAt: time.Now().Add(ttl)}
}

func (c *TTL[K, V]) Get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Now().After(e.expiresAt) {
		delete(c.m, k)
		var zero V
		return zero, false
	}
	return e.val, true
}

func (c *TTL[K, V]) Delete(k K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/cache/ -race` → ok.

- [ ] **Step 5: Commit**

```bash
git add internal/cache && git commit -m "feat: generic TTL cache"
```

---

### Task 3: Store open + migrations (`internal/store`)

**Files:**
- Create: `internal/store/store.go`, `internal/store/migrations/0001_init.sql`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: `store.Open(path string) (*Store, error)` (applies pragmas, runs embedded migrations), `(*Store).Close() error`. Later tasks add query methods on `*Store`.
- Consumes: `github.com/disgoorg/snowflake/v2` (added here).

- [ ] **Step 1: Add dependencies**

Run: `go get modernc.org/sqlite@latest github.com/disgoorg/snowflake/v2@latest`

- [ ] **Step 2: Write the failing test** — `internal/store/store_test.go`

```go
package store

import (
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenMigrates(t *testing.T) {
	s := openTest(t)
	for _, table := range []string{"honeypot_config", "honeypot_channels", "honeypot_events", "_migrations"} {
		var n int
		err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
		if err != nil || n != 1 {
			t.Fatalf("table %s missing (n=%d err=%v)", table, n, err)
		}
	}
}

func TestOpenIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()
	s2, err := Open(path) // migrations must not re-run/fail
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	s2.Close()
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/store/` → FAIL (undefined: Open).

- [ ] **Step 4: Create `internal/store/migrations/0001_init.sql`**

```sql
CREATE TABLE honeypot_config (
    guild_id       INTEGER PRIMARY KEY,
    log_channel_id INTEGER,
    action         TEXT NOT NULL DEFAULT 'softban'
);

CREATE TABLE honeypot_channels (
    channel_id INTEGER PRIMARY KEY,
    guild_id   INTEGER NOT NULL REFERENCES honeypot_config(guild_id) ON DELETE CASCADE,
    msg_id     INTEGER
);

CREATE TABLE honeypot_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    guild_id   INTEGER NOT NULL REFERENCES honeypot_config(guild_id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL,
    channel_id INTEGER REFERENCES honeypot_channels(channel_id) ON DELETE SET NULL,
    timestamp  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_channels_guild ON honeypot_channels(guild_id);
CREATE INDEX idx_events_guild ON honeypot_events(guild_id);
CREATE INDEX idx_events_user ON honeypot_events(user_id);
```

- [ ] **Step 5: Implement** — `internal/store/store.go`

```go
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
		db.Close()
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
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", base, err)
		}
		if _, err := tx.Exec(`INSERT INTO _migrations (version, name) VALUES (?, ?)`, version, base); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/store/` → ok.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/store && git commit -m "feat: sqlite store with embedded migrations"
```

---

### Task 4: Store queries

**Files:**
- Create: `internal/store/queries.go`
- Test: `internal/store/queries_test.go`

**Interfaces:**
- Produces (all on `*Store`; `snowflake.ID` from `github.com/disgoorg/snowflake/v2`):
  - `type Action string`; consts `ActionSoftban`, `ActionBan`, `ActionDisabled`
  - `type Config struct { GuildID snowflake.ID; LogChannelID *snowflake.ID; Action Action }`
  - `type Channel struct { ChannelID, GuildID snowflake.ID; MsgID *snowflake.ID }`
  - `GetConfig(guildID snowflake.ID) (*Config, error)` — `(nil, nil)` when absent
  - `UpsertConfig(cfg Config) error`
  - `GetChannel(guildID snowflake.ID) (*Channel, error)` — the guild's single channel, `(nil, nil)` when absent
  - `GetChannelByID(channelID snowflake.ID) (*Channel, error)` — `(nil, nil)` when absent
  - `SetChannel(guildID, channelID snowflake.ID) error` — replaces any previous row for the guild, preserves `msg_id` if same channel
  - `SetWarningMsg(channelID snowflake.ID, msgID *snowflake.ID) error`
  - `ClearWarningMsgByMsgID(msgID snowflake.ID) error`
  - `UnsetLogChannel(guildID snowflake.ID) error`
  - `RemoveChannel(channelID snowflake.ID) error`
  - `DeleteGuild(guildID snowflake.ID) error`
  - `RecordEvent(guildID, userID, channelID snowflake.ID) error`
  - `CountEventsByGuild(guildID snowflake.ID) (int64, error)`

- [ ] **Step 1: Write the failing test** — `internal/store/queries_test.go`

```go
package store

import (
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

const (
	g  = snowflake.ID(100)
	ch = snowflake.ID(200)
	u  = snowflake.ID(300)
)

func ptr(id snowflake.ID) *snowflake.ID { return &id }

func TestConfigRoundTrip(t *testing.T) {
	s := openTest(t)
	if cfg, err := s.GetConfig(g); err != nil || cfg != nil {
		t.Fatalf("empty GetConfig = %v, %v; want nil, nil", cfg, err)
	}
	want := Config{GuildID: g, LogChannelID: ptr(999), Action: ActionBan}
	if err := s.UpsertConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConfig(g)
	if err != nil || got == nil || got.Action != ActionBan || *got.LogChannelID != 999 {
		t.Fatalf("got %+v, %v", got, err)
	}
	want.Action = ActionSoftban
	want.LogChannelID = nil
	if err := s.UpsertConfig(want); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetConfig(g)
	if got.Action != ActionSoftban || got.LogChannelID != nil {
		t.Fatalf("after upsert got %+v", got)
	}
}

func TestChannelLifecycle(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertConfig(Config{GuildID: g, Action: ActionSoftban}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChannel(g, ch); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWarningMsg(ch, ptr(555)); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetChannel(g)
	if err != nil || c == nil || c.ChannelID != ch || *c.MsgID != 555 {
		t.Fatalf("got %+v, %v", c, err)
	}
	// Same channel again keeps msg_id.
	if err := s.SetChannel(g, ch); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetChannel(g)
	if c.MsgID == nil || *c.MsgID != 555 {
		t.Fatalf("msg_id lost on re-set: %+v", c)
	}
	// New channel replaces the old row.
	if err := s.SetChannel(g, ch+1); err != nil {
		t.Fatal(err)
	}
	if old, _ := s.GetChannelByID(ch); old != nil {
		t.Fatalf("old channel row survived: %+v", old)
	}
	c, _ = s.GetChannel(g)
	if c.ChannelID != ch+1 || c.MsgID != nil {
		t.Fatalf("got %+v", c)
	}
	if err := s.ClearWarningMsgByMsgID(555); err != nil { // no-op, already gone
		t.Fatal(err)
	}
	if err := s.RemoveChannel(ch + 1); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.GetChannel(g); c != nil {
		t.Fatalf("channel row survived removal: %+v", c)
	}
}

func TestEventsAndCascade(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertConfig(Config{GuildID: g, Action: ActionSoftban}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChannel(g, ch); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := s.RecordEvent(g, u, ch); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.CountEventsByGuild(g); err != nil || n != 3 {
		t.Fatalf("count = %d, %v; want 3", n, err)
	}
	// Channel removal keeps events (SET NULL).
	if err := s.RemoveChannel(ch); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountEventsByGuild(g); n != 3 {
		t.Fatalf("count after channel removal = %d; want 3", n)
	}
	// Guild deletion cascades.
	if err := s.DeleteGuild(g); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountEventsByGuild(g); n != 0 {
		t.Fatalf("count after guild delete = %d; want 0", n)
	}
	if cfg, _ := s.GetConfig(g); cfg != nil {
		t.Fatalf("config survived guild delete: %+v", cfg)
	}
}

func TestUnsetLogChannel(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertConfig(Config{GuildID: g, LogChannelID: ptr(999), Action: ActionSoftban}); err != nil {
		t.Fatal(err)
	}
	if err := s.UnsetLogChannel(g); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.GetConfig(g)
	if cfg.LogChannelID != nil {
		t.Fatalf("log channel not unset: %+v", cfg)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/` → FAIL (undefined: Config etc.).

- [ ] **Step 3: Implement** — `internal/store/queries.go`

```go
package store

import (
	"database/sql"
	"errors"

	"github.com/disgoorg/snowflake/v2"
)

type Action string

const (
	ActionSoftban  Action = "softban"
	ActionBan      Action = "ban"
	ActionDisabled Action = "disabled"
)

type Config struct {
	GuildID      snowflake.ID
	LogChannelID *snowflake.ID
	Action       Action
}

type Channel struct {
	ChannelID snowflake.ID
	GuildID   snowflake.ID
	MsgID     *snowflake.ID
}

func nullID(id *snowflake.ID) sql.NullInt64 {
	if id == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*id), Valid: true}
}

func idPtr(n sql.NullInt64) *snowflake.ID {
	if !n.Valid {
		return nil
	}
	id := snowflake.ID(n.Int64)
	return &id
}

func (s *Store) GetConfig(guildID snowflake.ID) (*Config, error) {
	var (
		logCh  sql.NullInt64
		action string
	)
	err := s.db.QueryRow(
		`SELECT log_channel_id, action FROM honeypot_config WHERE guild_id = ?`, int64(guildID),
	).Scan(&logCh, &action)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Config{GuildID: guildID, LogChannelID: idPtr(logCh), Action: Action(action)}, nil
}

func (s *Store) UpsertConfig(cfg Config) error {
	_, err := s.db.Exec(`INSERT INTO honeypot_config (guild_id, log_channel_id, action) VALUES (?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET log_channel_id = excluded.log_channel_id, action = excluded.action`,
		int64(cfg.GuildID), nullID(cfg.LogChannelID), string(cfg.Action))
	return err
}

func scanChannel(row *sql.Row) (*Channel, error) {
	var (
		chID, gID int64
		msgID     sql.NullInt64
	)
	err := row.Scan(&chID, &gID, &msgID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Channel{ChannelID: snowflake.ID(chID), GuildID: snowflake.ID(gID), MsgID: idPtr(msgID)}, nil
}

func (s *Store) GetChannel(guildID snowflake.ID) (*Channel, error) {
	return scanChannel(s.db.QueryRow(
		`SELECT channel_id, guild_id, msg_id FROM honeypot_channels WHERE guild_id = ?`, int64(guildID)))
}

func (s *Store) GetChannelByID(channelID snowflake.ID) (*Channel, error) {
	return scanChannel(s.db.QueryRow(
		`SELECT channel_id, guild_id, msg_id FROM honeypot_channels WHERE channel_id = ?`, int64(channelID)))
}

func (s *Store) SetChannel(guildID, channelID snowflake.ID) error {
	_, err := s.db.Exec(`DELETE FROM honeypot_channels WHERE guild_id = ? AND channel_id != ?`,
		int64(guildID), int64(channelID))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO honeypot_channels (channel_id, guild_id) VALUES (?, ?)
		ON CONFLICT(channel_id) DO NOTHING`, int64(channelID), int64(guildID))
	return err
}

func (s *Store) SetWarningMsg(channelID snowflake.ID, msgID *snowflake.ID) error {
	_, err := s.db.Exec(`UPDATE honeypot_channels SET msg_id = ? WHERE channel_id = ?`,
		nullID(msgID), int64(channelID))
	return err
}

func (s *Store) ClearWarningMsgByMsgID(msgID snowflake.ID) error {
	_, err := s.db.Exec(`UPDATE honeypot_channels SET msg_id = NULL WHERE msg_id = ?`, int64(msgID))
	return err
}

func (s *Store) UnsetLogChannel(guildID snowflake.ID) error {
	_, err := s.db.Exec(`UPDATE honeypot_config SET log_channel_id = NULL WHERE guild_id = ?`, int64(guildID))
	return err
}

func (s *Store) RemoveChannel(channelID snowflake.ID) error {
	_, err := s.db.Exec(`DELETE FROM honeypot_channels WHERE channel_id = ?`, int64(channelID))
	return err
}

func (s *Store) DeleteGuild(guildID snowflake.ID) error {
	_, err := s.db.Exec(`DELETE FROM honeypot_config WHERE guild_id = ?`, int64(guildID))
	return err
}

func (s *Store) RecordEvent(guildID, userID, channelID snowflake.ID) error {
	_, err := s.db.Exec(`INSERT INTO honeypot_events (guild_id, user_id, channel_id) VALUES (?, ?, ?)`,
		int64(guildID), int64(userID), int64(channelID))
	return err
}

func (s *Store) CountEventsByGuild(guildID snowflake.ID) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM honeypot_events WHERE guild_id = ?`, int64(guildID)).Scan(&n)
	return n, err
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/store/` → ok.

- [ ] **Step 5: Commit**

```bash
git add internal/store && git commit -m "feat: store queries for config, channels, events"
```

---

### Task 5: Lookalike normalization/obfuscation

**Files:**
- Create: `internal/bot/lookalike.go`
- Test: `internal/bot/lookalike_test.go`

**Interfaces:**
- Produces (package `bot`): `Normalize(s string) string` (maps confusable runes to ASCII, lowercases), `Obfuscate(s string, rng *rand.Rand) string` (~30% of mappable runes replaced with a random lookalike, at least one replacement guaranteed).

- [ ] **Step 1: Write the failing test** — `internal/bot/lookalike_test.go`

```go
package bot

import (
	"math/rand"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"honeypot":  "honeypot",
		"hоneypоt":  "honeypot", // Cyrillic о (U+043E)
		"һοnеурοt":  "honeypot", // mixed Cyrillic/Greek lookalikes
		"HONEYPOT":  "honeypot",
		"general":   "general",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestObfuscateRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for range 50 {
		obf := Obfuscate("honeypot", rng)
		if obf == "honeypot" {
			t.Fatal("Obfuscate returned the literal string (no replacements)")
		}
		if got := Normalize(obf); got != "honeypot" {
			t.Fatalf("Normalize(Obfuscate) = %q, want honeypot (obf=%q)", got, obf)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bot/` → FAIL (undefined: Normalize).

- [ ] **Step 3: Implement** — `internal/bot/lookalike.go`

```go
// Package bot implements Joe's Honeypot's Discord behavior.
package bot

import (
	"math/rand"
	"strings"
)

// lookalikes maps ASCII letters to visually confusable Unicode runes
// (Cyrillic/Greek homoglyphs), ported from RiskyMH/honeypot's
// lookalike-chars.yaml. Used to obfuscate the channel name so spam bots
// that blacklist the literal word "honeypot" don't skip the channel.
var lookalikes = map[rune][]rune{
	'a': {'а', 'α'}, // U+0430, U+03B1
	'c': {'с'},      // U+0441
	'e': {'е', 'е'}, // U+0435
	'h': {'һ'},      // U+04BB
	'i': {'і'},      // U+0456
	'n': {'ո'},      // U+0578
	'o': {'о', 'ο'}, // U+043E, U+03BF
	'p': {'р'},      // U+0440
	's': {'ѕ'},      // U+0455
	't': {'т'},      // U+0442
	'x': {'х'},      // U+0445
	'y': {'у'},      // U+0443
}

var normalizeMap = func() map[rune]rune {
	m := make(map[rune]rune)
	for ascii, subs := range lookalikes {
		for _, r := range subs {
			m[r] = ascii
		}
	}
	return m
}()

// Normalize lowercases s and maps known lookalike runes back to ASCII.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if ascii, ok := normalizeMap[r]; ok {
			r = ascii
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Obfuscate replaces ~30% of mappable runes in s with random lookalikes,
// guaranteeing at least one replacement.
func Obfuscate(s string, rng *rand.Rand) string {
	runes := []rune(strings.ToLower(s))
	replaced := false
	for i, r := range runes {
		subs, ok := lookalikes[r]
		if !ok {
			continue
		}
		if rng.Float64() < 0.3 {
			runes[i] = subs[rng.Intn(len(subs))]
			replaced = true
		}
	}
	if !replaced {
		for i, r := range runes { // force one deterministic replacement
			if subs, ok := lookalikes[r]; ok {
				runes[i] = subs[rng.Intn(len(subs))]
				break
			}
		}
	}
	return string(runes)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/bot/` → ok.

- [ ] **Step 5: Commit**

```bash
git add internal/bot && git commit -m "feat: lookalike normalization and channel-name obfuscation"
```

---

### Task 6: Message templates

**Files:**
- Create: `internal/bot/templates.go`
- Test: `internal/bot/templates_test.go`

**Interfaces:**
- Consumes: `store.Action` (Task 4).
- Produces (package `bot`):
  - `WarningMessage() string`
  - `DMMessage(action store.Action, guildName string) string`
  - `ExemptDMMessage(guildName string) string`
  - `LogMessage(userID snowflake.ID, action store.Action) string`
  - `ExemptLogMessage(userID snowflake.ID) string`
  - `IntroMessage(missingBanPerm bool) string`
  - `CounterButtonLabel(count int64) string`
  - `actionVerb(action store.Action) string` — `"banned"` / `"kicked"` (softban) — unexported helper

- [ ] **Step 1: Write the failing test** — `internal/bot/templates_test.go`

```go
package bot

import (
	"strings"
	"testing"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

func TestDMMessage(t *testing.T) {
	dm := DMMessage(store.ActionSoftban, "My Server")
	if !strings.Contains(dm, "kicked") || !strings.Contains(dm, "My Server") {
		t.Fatalf("softban DM wrong: %q", dm)
	}
	if dm := DMMessage(store.ActionBan, "My Server"); !strings.Contains(dm, "banned") {
		t.Fatalf("ban DM wrong: %q", dm)
	}
}

func TestLogMessage(t *testing.T) {
	msg := LogMessage(42, store.ActionBan)
	if !strings.Contains(msg, "<@42>") || !strings.Contains(msg, "banned") {
		t.Fatalf("log message wrong: %q", msg)
	}
}

func TestCounterButtonLabel(t *testing.T) {
	if got := CounterButtonLabel(0); got != "0 users honeypot'd" {
		t.Fatalf("got %q", got)
	}
	if got := CounterButtonLabel(7); got != "7 users honeypot'd" {
		t.Fatalf("got %q", got)
	}
}

func TestIntroMessage(t *testing.T) {
	if !strings.Contains(IntroMessage(true), "Ban Members") {
		t.Fatal("intro should warn about missing Ban Members permission")
	}
	if strings.Contains(IntroMessage(false), "Ban Members permission") {
		t.Fatal("intro should not warn when permission present")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bot/` → FAIL (undefined: DMMessage).

- [ ] **Step 3: Implement** — `internal/bot/templates.go`

```go
package bot

import (
	"fmt"

	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

func actionVerb(action store.Action) string {
	if action == store.ActionBan {
		return "banned"
	}
	return "kicked"
}

func WarningMessage() string {
	return "## ⚠️ DO NOT SEND MESSAGES IN THIS CHANNEL\n" +
		"Anyone who posts here is **automatically banned** — no exceptions, no warnings.\n" +
		"-# This channel is a honeypot for catching spam bots."
}

func DMMessage(action store.Action, guildName string) string {
	return fmt.Sprintf(
		"## 🍯 Honeypot Triggered\nYou have been **%s** from **%s** for sending a message in the honeypot channel.\n"+
			"-# This is an automated message from Joe's Honeypot.",
		actionVerb(action), guildName)
}

func ExemptDMMessage(guildName string) string {
	return fmt.Sprintf(
		"## 🍯 Honeypot Triggered (example)\nYou posted in the honeypot channel of **%s**, "+
			"but you are the server owner or an administrator, so no action was taken. "+
			"A regular user would have received this DM and been actioned.\n"+
			"-# This is an automated message from Joe's Honeypot.",
		guildName)
}

func LogMessage(userID snowflake.ID, action store.Action) string {
	return fmt.Sprintf("<@%d> was %s for sending a message in the honeypot channel.", userID, actionVerb(action))
}

func ExemptLogMessage(userID snowflake.ID) string {
	return fmt.Sprintf("⚠️ <@%d> posted in the honeypot channel but was **not** actioned (server owner or administrator).", userID)
}

func IntroMessage(missingBanPerm bool) string {
	msg := "## 🍯 Joe's Honeypot is set up!\n" +
		"Any non-admin account that posts in the honeypot channel will be softbanned " +
		"(banned and unbanned, deleting their last hour of messages).\n" +
		"Use </honeypot:0> to change the channel, set a log channel, or switch the action.\n" +
		"-# This message deletes itself in a few minutes."
	if missingBanPerm {
		msg += "\n\n⚠️ **I am missing the Ban Members permission** — I cannot take any action until it is granted."
	}
	return msg
}

func CounterButtonLabel(count int64) string {
	return fmt.Sprintf("%d users honeypot'd", count)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/bot/` → ok.

- [ ] **Step 5: Commit**

```bash
git add internal/bot && git commit -m "feat: fixed message templates"
```

---

### Task 7: Trigger rules (pure decision logic)

**Files:**
- Create: `internal/bot/rules.go`
- Test: `internal/bot/rules_test.go`

**Interfaces:**
- Consumes: `discord.MessageType` (disgo added here via `go get`).
- Produces (package `bot`):
  - `IsTriggerMessage(authorIsBot bool, msgType discord.MessageType) bool` — true only for Default/Reply from non-bots
  - `IsExempt(authorID, ownerID snowflake.ID, memberRoleIDs []snowflake.ID, adminRoleIDs map[snowflake.ID]struct{}) bool`
  - `UnbanExpired(messageCreated, now time.Time) bool` — true if >24h

- [ ] **Step 1: Add disgo**

Run: `go get github.com/disgoorg/disgo@latest` (must resolve to >= v0.19.6).

- [ ] **Step 2: Write the failing test** — `internal/bot/rules_test.go`

```go
package bot

import (
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func TestIsTriggerMessage(t *testing.T) {
	cases := []struct {
		name string
		bot  bool
		typ  discord.MessageType
		want bool
	}{
		{"user default", false, discord.MessageTypeDefault, true},
		{"user reply", false, discord.MessageTypeReply, true},
		{"bot default", true, discord.MessageTypeDefault, false},
		{"system join", false, discord.MessageTypeUserJoin, false},
		{"channel pin", false, discord.MessageTypeChannelPinnedMessage, false},
	}
	for _, c := range cases {
		if got := IsTriggerMessage(c.bot, c.typ); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsExempt(t *testing.T) {
	admin := map[snowflake.ID]struct{}{10: {}}
	if !IsExempt(1, 1, nil, nil) {
		t.Error("owner must be exempt")
	}
	if !IsExempt(2, 1, []snowflake.ID{10}, admin) {
		t.Error("admin-role member must be exempt")
	}
	if IsExempt(2, 1, []snowflake.ID{11}, admin) {
		t.Error("regular member must not be exempt")
	}
}

func TestUnbanExpired(t *testing.T) {
	now := time.Now()
	if UnbanExpired(now.Add(-23*time.Hour), now) {
		t.Error("23h old must not be expired")
	}
	if !UnbanExpired(now.Add(-25*time.Hour), now) {
		t.Error("25h old must be expired")
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/bot/` → FAIL (undefined: IsTriggerMessage).

- [ ] **Step 4: Implement** — `internal/bot/rules.go`

```go
package bot

import (
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// IsTriggerMessage reports whether a message in a honeypot channel should
// trigger moderation: only ordinary messages/replies from non-bot accounts.
// All system message types (joins, pins, boosts, ...) are excluded.
func IsTriggerMessage(authorIsBot bool, msgType discord.MessageType) bool {
	if authorIsBot {
		return false
	}
	return msgType == discord.MessageTypeDefault || msgType == discord.MessageTypeReply
}

// IsExempt reports whether the author must not be actioned: the server owner,
// or any member holding a non-managed role with the Administrator permission
// (adminRoleIDs is precomputed from the role cache).
func IsExempt(authorID, ownerID snowflake.ID, memberRoleIDs []snowflake.ID, adminRoleIDs map[snowflake.ID]struct{}) bool {
	if authorID == ownerID {
		return true
	}
	for _, r := range memberRoleIDs {
		if _, ok := adminRoleIDs[r]; ok {
			return true
		}
	}
	return false
}

// UnbanExpired reports whether an unban button is too old to honor (24h).
func UnbanExpired(messageCreated, now time.Time) bool {
	return now.Sub(messageCreated) > 24*time.Hour
}
```

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/bot/` → ok.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/bot && git commit -m "feat: trigger/exemption/expiry decision rules"
```

---

### Task 8: Bot wiring + main

**Files:**
- Create: `internal/bot/bot.go`, `internal/bot/commands.go`, `cmd/bot/main.go`

**Interfaces:**
- Consumes: `store.Open`, `cache.NewTTL`.
- Produces:
  - `type Bot struct { Client dbot.Client; Store *store.Store; Log *slog.Logger; Dedup *cache.TTL[dedupKey, struct{}]; DMs *cache.TTL[snowflake.ID, snowflake.ID] }`
  - `type dedupKey struct { GuildID, UserID snowflake.ID }`
  - `bot.New(token string, st *store.Store, log *slog.Logger) (*Bot, error)`
  - `(*Bot).Start(ctx) error` (syncs commands, opens gateway), `(*Bot).Close(ctx)`
  - `commands() []discord.ApplicationCommandCreate`
  - Later tasks append `dbot.WithEventListenerFunc(b.onXxx)` entries to the `listeners` slice in `New`.

- [ ] **Step 1: Write `internal/bot/bot.go`**

```go
package bot

import (
	"context"
	"log/slog"

	"github.com/disgoorg/disgo"
	dbot "github.com/disgoorg/disgo/bot"
	dcache "github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/cache"
	"github.com/bkan0n/joeshoneypot/internal/store"
)

type dedupKey struct {
	GuildID snowflake.ID
	UserID  snowflake.ID
}

type Bot struct {
	Client dbot.Client
	Store  *store.Store
	Log    *slog.Logger
	Dedup  *cache.TTL[dedupKey, struct{}]
	DMs    *cache.TTL[snowflake.ID, snowflake.ID]
}

func New(token string, st *store.Store, log *slog.Logger) (*Bot, error) {
	b := &Bot{
		Store: st,
		Log:   log,
		Dedup: cache.NewTTL[dedupKey, struct{}](),
		DMs:   cache.NewTTL[snowflake.ID, snowflake.ID](),
	}
	// Event listeners are appended here by the handler files (handler_*.go,
	// setup.go, housekeeping.go) as they are implemented.
	listeners := []dbot.ConfigOpt{}
	opts := append([]dbot.ConfigOpt{
		dbot.WithGatewayConfigOpts(
			gateway.WithIntents(gateway.IntentGuilds|gateway.IntentGuildMessages),
			gateway.WithPresenceOpts(gateway.WithWatchingActivity("#honeypot for bots")),
		),
		dbot.WithCacheConfigOpts(
			dcache.WithCaches(dcache.FlagGuilds | dcache.FlagRoles | dcache.FlagChannels),
		),
	}, listeners...)
	client, err := disgo.New(token, opts...)
	if err != nil {
		return nil, err
	}
	b.Client = client
	return b, nil
}

func (b *Bot) Start(ctx context.Context) error {
	if err := handler.SyncCommands(b.Client, commands(), nil); err != nil {
		return err
	}
	return b.Client.OpenGateway(ctx)
}

func (b *Bot) Close(ctx context.Context) {
	b.Client.Close(ctx)
}
```

- [ ] **Step 2: Write `internal/bot/commands.go`**

```go
package bot

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/json"
)

func commands() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "honeypot",
			Description: "Configure the honeypot channel and its settings",
			DefaultMemberPermissions: json.NewNullablePtr(
				discord.PermissionManageGuild | discord.PermissionBanMembers |
					discord.PermissionModerateMembers | discord.PermissionManageMessages |
					discord.PermissionManageChannels,
			),
			Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		},
	}
}
```

(If `github.com/disgoorg/json` isn't already an indirect dep, `go get` it. If disgo v0.19 has moved to a different nullable helper, use whatever `discord.SlashCommandCreate.DefaultMemberPermissions`'s type requires — check `go doc github.com/disgoorg/disgo/discord SlashCommandCreate`.)

- [ ] **Step 3: Write `cmd/bot/main.go`**

```go
// Joe's Honeypot — Discord honeypot bot.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bkan0n/joeshoneypot/internal/bot"
	"github.com/bkan0n/joeshoneypot/internal/store"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Error("BOT_TOKEN is required")
		os.Exit(1)
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/honeypot.db"
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Error("opening database", "path", dbPath, "err", err)
		os.Exit(1)
	}
	defer st.Close()

	b, err := bot.New(token, st, log)
	if err != nil {
		log.Error("creating bot", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		log.Error("starting bot", "err", err)
		os.Exit(1)
	}
	log.Info("Joe's Honeypot is running", "db", dbPath)
	<-ctx.Done()
	log.Info("shutting down")
	b.Close(context.Background())
}
```

- [ ] **Step 4: Verify**

Run: `go build ./... && go vet ./... && go test ./...` → all pass.
Optional smoke test with a real token: `BOT_TOKEN=... DB_PATH=./honeypot.db go run ./cmd/bot` → logs "Joe's Honeypot is running"; Ctrl-C exits cleanly.

- [ ] **Step 5: Commit**

```bash
git add cmd internal/bot go.mod go.sum && git commit -m "feat: bot wiring, command registration, main entrypoint"
```

---

### Task 9: `/honeypot` modal + submit handler

**Files:**
- Create: `internal/bot/handler_honeypot.go`
- Modify: `internal/bot/bot.go` (append to `listeners`)
- Test: `internal/bot/handler_honeypot_test.go` (validation logic only)

**Interfaces:**
- Consumes: `commands()` name `"honeypot"`, store methods (Task 4), `templates` (Task 6).
- Produces:
  - modal custom ID `honeypot_config`; component IDs `honeypot_channel`, `log_channel`, `honeypot_action`
  - `type configSubmission struct { HoneypotChannelID snowflake.ID; LogChannelID *snowflake.ID; Action store.Action }`
  - `validateConfig(sub configSubmission, userPerms, botHoneypotPerms, botLogPerms discord.Permissions) []string` — empty slice ⇒ valid
  - `(*Bot).ensureWarningMessage(guildID, channelID snowflake.ID)` — posts/updates the warning message + counter button, stores `msg_id`; reused by Tasks 10 and 12
  - `(*Bot).botPermissionsIn(guildID, channelID snowflake.ID) discord.Permissions`

- [ ] **Step 1: Write the failing test** — `internal/bot/handler_honeypot_test.go`

```go
package bot

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

const botChannelPerms = discord.PermissionViewChannel | discord.PermissionSendMessages

func TestValidateConfigOK(t *testing.T) {
	sub := configSubmission{HoneypotChannelID: 1, Action: store.ActionSoftban}
	problems := validateConfig(sub, discord.PermissionBanMembers, botChannelPerms|discord.PermissionBanMembers, 0)
	if len(problems) != 0 {
		t.Fatalf("expected valid, got %v", problems)
	}
}

func TestValidateConfigMissingBotBan(t *testing.T) {
	sub := configSubmission{HoneypotChannelID: 1, Action: store.ActionBan}
	problems := validateConfig(sub, discord.PermissionBanMembers, botChannelPerms, 0)
	if len(problems) == 0 {
		t.Fatal("expected problem: bot missing Ban Members")
	}
}

func TestValidateConfigMissingUserBan(t *testing.T) {
	sub := configSubmission{HoneypotChannelID: 1, Action: store.ActionSoftban}
	problems := validateConfig(sub, 0, botChannelPerms|discord.PermissionBanMembers, 0)
	if len(problems) == 0 {
		t.Fatal("expected problem: user missing Ban Members")
	}
}

func TestValidateConfigDisabledNeedsNoBan(t *testing.T) {
	sub := configSubmission{HoneypotChannelID: 1, Action: store.ActionDisabled}
	problems := validateConfig(sub, 0, botChannelPerms, 0)
	if len(problems) != 0 {
		t.Fatalf("disabled action must not require ban perms, got %v", problems)
	}
}

func TestValidateConfigLogChannel(t *testing.T) {
	logCh := snowflake.ID(2)
	sub := configSubmission{HoneypotChannelID: 1, LogChannelID: &logCh, Action: store.ActionDisabled}
	problems := validateConfig(sub, 0, botChannelPerms, 0) // no perms in log channel
	if len(problems) == 0 {
		t.Fatal("expected problem: bot cannot post in log channel")
	}
	problems = validateConfig(sub, 0, botChannelPerms, botChannelPerms)
	if len(problems) != 0 {
		t.Fatalf("expected valid, got %v", problems)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bot/` → FAIL (undefined: configSubmission).

- [ ] **Step 3: Implement** — `internal/bot/handler_honeypot.go`

```go
package bot

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

const (
	modalID          = "honeypot_config"
	honeypotChanCID  = "honeypot_channel"
	logChanCID       = "log_channel"
	actionCID        = "honeypot_action"
	counterButtonCID = "moderated_count"
)

func configModal(current *store.Config) discord.ModalCreate {
	defaultAction := store.ActionSoftban
	if current != nil {
		defaultAction = current.Action
	}
	opt := func(label string, value store.Action) discord.StringSelectMenuOption {
		o := discord.NewStringSelectMenuOption(label, string(value))
		if value == defaultAction {
			o = o.WithDefault(true)
		}
		return o
	}
	return discord.NewModalCreate(modalID, "Configure Joe's Honeypot",
		discord.NewLabel("Honeypot channel",
			discord.NewChannelSelectMenu(honeypotChanCID, "Select a channel").
				WithChannelTypes(discord.ChannelTypeGuildText, discord.ChannelTypeGuildVoice).
				WithMinValues(1).WithMaxValues(1)),
		discord.NewLabel("Log channel (optional)",
			discord.NewChannelSelectMenu(logChanCID, "Select a channel").
				WithChannelTypes(discord.ChannelTypeGuildText,
					discord.ChannelTypeGuildPublicThread, discord.ChannelTypeGuildPrivateThread).
				WithMinValues(0).WithMaxValues(1)),
		discord.NewLabel("Action",
			discord.NewStringSelectMenu(actionCID, "Choose an action",
				opt("Softban (kick) — bans & unbans, deleting the last hour of messages", store.ActionSoftban),
				opt("Ban — deletes the last hour of messages", store.ActionBan),
				opt("Disabled — react only, take no action", store.ActionDisabled),
			).WithMinValues(1).WithMaxValues(1)),
	)
}

func (b *Bot) onCommand(e *events.ApplicationCommandInteractionCreate) {
	if e.Data.CommandName() != "honeypot" || e.GuildID() == nil {
		return
	}
	cfg, err := b.Store.GetConfig(*e.GuildID())
	if err != nil {
		b.Log.Error("loading config for modal", "guild", *e.GuildID(), "err", err)
		return
	}
	if err := e.Modal(configModal(cfg)); err != nil {
		b.Log.Error("sending config modal", "err", err)
	}
}

type configSubmission struct {
	HoneypotChannelID snowflake.ID
	LogChannelID      *snowflake.ID
	Action            store.Action
}

// validateConfig returns human-readable problems; empty means valid.
// Nothing is saved unless it returns empty.
func validateConfig(sub configSubmission, userPerms, botHoneypotPerms, botLogPerms discord.Permissions) []string {
	var problems []string
	if !botHoneypotPerms.Has(discord.PermissionViewChannel) || !botHoneypotPerms.Has(discord.PermissionSendMessages) {
		problems = append(problems, "I need **View Channel** and **Send Messages** in the honeypot channel.")
	}
	if sub.Action == store.ActionSoftban || sub.Action == store.ActionBan {
		if !botHoneypotPerms.Has(discord.PermissionBanMembers) {
			problems = append(problems, "I need the **Ban Members** permission for the softban/ban action.")
		}
		if !userPerms.Has(discord.PermissionBanMembers) {
			problems = append(problems, "You need the **Ban Members** permission to set the softban/ban action.")
		}
	}
	if sub.LogChannelID != nil {
		if !botLogPerms.Has(discord.PermissionViewChannel) || !botLogPerms.Has(discord.PermissionSendMessages) {
			problems = append(problems, "I need **View Channel** and **Send Messages** in the log channel.")
		}
	}
	return problems
}

func (b *Bot) onModalSubmit(e *events.ModalSubmitInteractionCreate) {
	if e.Data.CustomID != modalID || e.GuildID() == nil {
		return
	}
	guildID := *e.GuildID()

	sel, ok := e.Data.ChannelSelectMenu(honeypotChanCID)
	if !ok || len(sel.Values) != 1 {
		b.replyEphemeral(e, "No honeypot channel selected. No settings have been changed.")
		return
	}
	sub := configSubmission{HoneypotChannelID: sel.Values[0], Action: store.ActionSoftban}
	if logSel, ok := e.Data.ChannelSelectMenu(logChanCID); ok && len(logSel.Values) == 1 {
		id := logSel.Values[0]
		sub.LogChannelID = &id
	}
	if actions := e.Data.StringValues(actionCID); len(actions) == 1 {
		sub.Action = store.Action(actions[0])
	}

	var userPerms discord.Permissions
	if m := e.Member(); m != nil {
		userPerms = m.Permissions
	}
	var botLogPerms discord.Permissions
	if sub.LogChannelID != nil {
		botLogPerms = b.botPermissionsIn(guildID, *sub.LogChannelID)
	}
	if problems := validateConfig(sub, userPerms, b.botPermissionsIn(guildID, sub.HoneypotChannelID), botLogPerms); len(problems) > 0 {
		b.replyEphemeral(e, "**No settings have been changed:**\n- "+strings.Join(problems, "\n- "))
		return
	}

	prev, _ := b.Store.GetChannel(guildID)
	if err := b.Store.UpsertConfig(store.Config{GuildID: guildID, LogChannelID: sub.LogChannelID, Action: sub.Action}); err != nil {
		b.Log.Error("saving config", "guild", guildID, "err", err)
		b.replyEphemeral(e, "Something went wrong saving the config. No settings have been changed.")
		return
	}
	if err := b.Store.SetChannel(guildID, sub.HoneypotChannelID); err != nil {
		b.Log.Error("saving channel", "guild", guildID, "err", err)
		b.replyEphemeral(e, "Something went wrong saving the channel.")
		return
	}
	// Channel changed: delete the old warning message, post one in the new channel.
	if prev != nil && prev.ChannelID != sub.HoneypotChannelID && prev.MsgID != nil {
		if err := b.Client.Rest().DeleteMessage(prev.ChannelID, *prev.MsgID); err != nil {
			b.Log.Warn("deleting old warning message", "err", err)
		}
	}
	b.ensureWarningMessage(guildID, sub.HoneypotChannelID)
	b.replyEphemeral(e, fmt.Sprintf("🍯 Honeypot configured: <#%d>, action **%s**.", sub.HoneypotChannelID, sub.Action))
}

// ensureWarningMessage posts the persistent warning (with counter button) if
// the channel has none recorded, otherwise refreshes the counter label.
func (b *Bot) ensureWarningMessage(guildID, channelID snowflake.ID) {
	ch, err := b.Store.GetChannelByID(channelID)
	if err != nil || ch == nil {
		return
	}
	count, err := b.Store.CountEventsByGuild(guildID)
	if err != nil {
		b.Log.Error("counting events", "err", err)
		return
	}
	components := []discord.LayoutComponent{
		discord.NewActionRow(
			discord.NewSecondaryButton(CounterButtonLabel(count), counterButtonCID),
		),
	}
	if ch.MsgID != nil {
		if _, err := b.Client.Rest().UpdateMessage(channelID, *ch.MsgID, discord.MessageUpdate{Components: &components}); err == nil {
			return
		}
		// Message gone (deleted manually) — fall through and repost.
	}
	msg, err := b.Client.Rest().CreateMessage(channelID, discord.MessageCreate{
		Content:    WarningMessage(),
		Components: components,
	})
	if err != nil {
		b.Log.Error("posting warning message", "channel", channelID, "err", err)
		return
	}
	if err := b.Store.SetWarningMsg(channelID, &msg.ID); err != nil {
		b.Log.Error("storing warning msg id", "err", err)
	}
}

func (b *Bot) botPermissionsIn(guildID, channelID snowflake.ID) discord.Permissions {
	ch, ok := b.Client.Caches().GuildChannel(channelID)
	if !ok {
		return 0
	}
	member, err := b.Client.Rest().GetMember(guildID, b.Client.ID())
	if err != nil || member == nil {
		return 0
	}
	return b.Client.Caches().MemberPermissionsInChannel(ch, *member)
}

// interactionReplier is satisfied by *events.ModalSubmitInteractionCreate and
// *events.ComponentInteractionCreate.
type interactionReplier interface {
	CreateMessage(messageCreate discord.MessageCreate, opts ...rest.RequestOpt) error
}

func (b *Bot) replyEphemeral(e interactionReplier, content string) {
	if err := e.CreateMessage(discord.MessageCreate{Content: content, Flags: discord.MessageFlagEphemeral}); err != nil {
		b.Log.Error("sending ephemeral reply", "err", err)
	}
}
```

Add `"github.com/disgoorg/disgo/rest"` to this file's imports.

- [ ] **Step 4: Register listeners** — in `internal/bot/bot.go`, change the `listeners` slice:

```go
	listeners := []dbot.ConfigOpt{
		dbot.WithEventListenerFunc(b.onCommand),
		dbot.WithEventListenerFunc(b.onModalSubmit),
	}
```

- [ ] **Step 5: Run to verify pass**

Run: `go build ./... && go test ./internal/bot/` → ok.

- [ ] **Step 6: Commit**

```bash
git add internal/bot && git commit -m "feat: /honeypot config modal with validation"
```

---

### Task 10: Trigger pipeline (MESSAGE_CREATE)

**Files:**
- Create: `internal/bot/handler_message.go`
- Modify: `internal/bot/bot.go` (append listener)

**Interfaces:**
- Consumes: `IsTriggerMessage`, `IsExempt` (Task 7), store methods, templates, `ensureWarningMessage` (Task 9), `Dedup`/`DMs` caches.
- Produces:
  - `(*Bot).onMessageCreate(e *events.MessageCreate)`
  - `(*Bot).sendLog(cfg *store.Config, fallbackChannelID snowflake.ID, msg discord.MessageCreate)` — posts to log channel, falls back to honeypot channel, auto-unsets a dead log channel; reused by Task 11/12
  - `(*Bot).dmUser(userID snowflake.ID, content string) error`
  - unban button custom ID format: `fmt.Sprintf("unban:%d", userID)` (parsed in Task 11)

- [ ] **Step 1: Implement** — `internal/bot/handler_message.go`

```go
package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

func (b *Bot) onMessageCreate(e *events.MessageCreate) {
	if e.GuildID == nil {
		return
	}
	guildID := *e.GuildID
	msg := e.Message
	if !IsTriggerMessage(msg.Author.Bot || msg.Author.System, msg.Type) {
		return
	}
	hpChannel, err := b.Store.GetChannelByID(e.ChannelID)
	if err != nil || hpChannel == nil || hpChannel.GuildID != guildID {
		return
	}
	cfg, err := b.Store.GetConfig(guildID)
	if err != nil || cfg == nil {
		return
	}

	key := dedupKey{GuildID: guildID, UserID: msg.Author.ID}
	if _, busy := b.Dedup.Get(key); busy {
		return
	}
	b.Dedup.Set(key, struct{}{}, 30*time.Second)
	defer b.Dedup.Delete(key) // allow re-punishing a rejoining user

	// Best-effort honey react.
	go func() {
		if err := b.Client.Rest().AddReaction(e.ChannelID, msg.ID, "🍯"); err != nil {
			b.Log.Debug("adding reaction", "err", err)
		}
	}()

	if cfg.Action == store.ActionDisabled {
		return
	}

	guildName := "this server"
	var ownerID snowflake.ID
	adminRoles := map[snowflake.ID]struct{}{}
	if guild, ok := b.Client.Caches().Guild(guildID); ok {
		guildName = guild.Name
		ownerID = guild.OwnerID
	}
	b.Client.Caches().RolesForEach(guildID, func(role discord.Role) {
		if !role.Managed && role.Permissions.Has(discord.PermissionAdministrator) {
			adminRoles[role.ID] = struct{}{}
		}
	})
	var memberRoles []snowflake.ID
	if msg.Member != nil {
		memberRoles = msg.Member.RoleIDs
	}

	if IsExempt(msg.Author.ID, ownerID, memberRoles, adminRoles) {
		go func() { _ = b.dmUser(msg.Author.ID, ExemptDMMessage(guildName)) }()
		b.sendLog(cfg, e.ChannelID, discord.MessageCreate{Content: ExemptLogMessage(msg.Author.ID)})
		return
	}

	// DM before the ban so Discord still delivers it — but never delay the
	// action more than 2s.
	dmDone := make(chan struct{})
	go func() {
		defer close(dmDone)
		if err := b.dmUser(msg.Author.ID, DMMessage(cfg.Action, guildName)); err != nil {
			b.Log.Debug("dm failed", "user", msg.Author.ID, "err", err)
		}
	}()
	select {
	case <-dmDone:
	case <-time.After(2 * time.Second):
	}

	reason := rest.WithReason("Joe's Honeypot: posted in the honeypot channel")
	if err := b.Client.Rest().AddBan(guildID, msg.Author.ID, time.Hour, reason); err != nil {
		b.Log.Error("ban failed", "guild", guildID, "user", msg.Author.ID, "err", err)
		b.sendLog(cfg, e.ChannelID, discord.MessageCreate{Content: fmt.Sprintf(
			"⚠️ Failed to %s <@%d> — check that I have the **Ban Members** permission and that my role is above theirs.",
			cfg.Action, msg.Author.ID)})
		return
	}
	if cfg.Action == store.ActionSoftban {
		time.Sleep(250 * time.Millisecond)
		if err := b.Client.Rest().DeleteBan(guildID, msg.Author.ID, reason); err != nil {
			// An unknown-ban error means someone beat us to it — fine. Anything
			// else leaves the user banned instead of softbanned; tell the mods.
			if !strings.Contains(err.Error(), "Unknown Ban") {
				b.Log.Error("unban after softban failed", "user", msg.Author.ID, "err", err)
				b.sendLog(cfg, e.ChannelID, discord.MessageCreate{Content: fmt.Sprintf(
					"⚠️ <@%d> was banned but the softban's unban failed — they are still banned.", msg.Author.ID)})
			}
		}
	}

	if err := b.Store.RecordEvent(guildID, msg.Author.ID, e.ChannelID); err != nil {
		b.Log.Error("recording event", "err", err)
	}

	logMsg := discord.MessageCreate{Content: LogMessage(msg.Author.ID, cfg.Action)}
	if cfg.Action == store.ActionBan {
		logMsg.Components = []discord.LayoutComponent{
			discord.NewActionRow(
				discord.NewDangerButton("Unban", fmt.Sprintf("unban:%d", msg.Author.ID)),
			),
		}
	}
	b.sendLog(cfg, e.ChannelID, logMsg)
	b.ensureWarningMessage(guildID, e.ChannelID)
	b.Log.Info("moderated", "guild", guildID, "user", msg.Author.ID, "action", cfg.Action)
}

// sendLog posts to the configured log channel; if that fails because the
// channel is gone/inaccessible it unsets it and falls back to the honeypot
// channel.
func (b *Bot) sendLog(cfg *store.Config, fallbackChannelID snowflake.ID, msg discord.MessageCreate) {
	if cfg.LogChannelID != nil {
		if _, err := b.Client.Rest().CreateMessage(*cfg.LogChannelID, msg); err == nil {
			return
		} else {
			b.Log.Warn("log channel unusable, unsetting", "channel", *cfg.LogChannelID, "err", err)
			if dbErr := b.Store.UnsetLogChannel(cfg.GuildID); dbErr != nil {
				b.Log.Error("unsetting log channel", "err", dbErr)
			}
		}
	}
	if _, err := b.Client.Rest().CreateMessage(fallbackChannelID, msg); err != nil {
		b.Log.Debug("fallback log message failed", "err", err)
	}
}

func (b *Bot) dmUser(userID snowflake.ID, content string) error {
	chID, ok := b.DMs.Get(userID)
	if !ok {
		ch, err := b.Client.Rest().CreateDMChannel(userID)
		if err != nil {
			return err
		}
		chID = ch.ID()
		b.DMs.Set(userID, chID, 24*time.Hour)
	}
	_, err := b.Client.Rest().CreateMessage(chID, discord.MessageCreate{Content: content})
	return err
}
```

(`RolesForEach` / `Caches().Guild` names: if they differ in v0.19.x, `go doc github.com/disgoorg/disgo/bot Caches` lists the exact cache accessors.)

- [ ] **Step 2: Register listener** — append to `listeners` in `bot.go`:

```go
		dbot.WithEventListenerFunc(b.onMessageCreate),
```

- [ ] **Step 3: Verify**

Run: `go build ./... && go test ./...` → ok.

- [ ] **Step 4: Commit**

```bash
git add internal/bot && git commit -m "feat: honeypot trigger pipeline (dm, softban/ban, log, counter)"
```

---

### Task 11: Buttons (counter stats, unban, intro delete)

**Files:**
- Create: `internal/bot/handler_buttons.go`
- Modify: `internal/bot/bot.go` (append listener)
- Test: extend `internal/bot/rules_test.go` coverage already covers `UnbanExpired`; add custom-ID parse test in `internal/bot/handler_buttons_test.go`

**Interfaces:**
- Consumes: `UnbanExpired` (Task 7), `CountEventsByGuild`, `sendLog` pattern.
- Produces:
  - `(*Bot).onComponent(e *events.ComponentInteractionCreate)`
  - `parseUnbanID(customID string) (snowflake.ID, bool)`
  - intro delete button custom ID: `delete_intro`

- [ ] **Step 1: Write the failing test** — `internal/bot/handler_buttons_test.go`

```go
package bot

import "testing"

func TestParseUnbanID(t *testing.T) {
	if id, ok := parseUnbanID("unban:42"); !ok || id != 42 {
		t.Fatalf("got %v %v", id, ok)
	}
	for _, bad := range []string{"unban:", "unban:abc", "moderated_count", ""} {
		if _, ok := parseUnbanID(bad); ok {
			t.Errorf("parseUnbanID(%q) should fail", bad)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/bot/` → FAIL (undefined: parseUnbanID).

- [ ] **Step 3: Implement** — `internal/bot/handler_buttons.go`

```go
package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

const introDeleteCID = "delete_intro"

func parseUnbanID(customID string) (snowflake.ID, bool) {
	suffix, ok := strings.CutPrefix(customID, "unban:")
	if !ok || suffix == "" {
		return 0, false
	}
	id, err := snowflake.Parse(suffix)
	if err != nil {
		return 0, false
	}
	return id, true
}

func (b *Bot) onComponent(e *events.ComponentInteractionCreate) {
	data, ok := e.Data.(discord.ButtonInteractionData)
	if !ok || e.GuildID() == nil {
		return
	}
	guildID := *e.GuildID()

	switch {
	case data.CustomID() == counterButtonCID:
		count, err := b.Store.CountEventsByGuild(guildID)
		if err != nil {
			b.Log.Error("counting events", "err", err)
			return
		}
		b.replyEphemeral(e, fmt.Sprintf("🍯 **%d** users have been honeypot'd in this server.", count))

	case data.CustomID() == introDeleteCID:
		if m := e.Member(); m == nil || !m.Permissions.Has(discord.PermissionManageMessages) {
			b.replyEphemeral(e, "You need the **Manage Messages** permission to delete this.")
			return
		}
		if err := b.Client.Rest().DeleteMessage(e.Message.ChannelID, e.Message.ID); err != nil {
			b.Log.Warn("deleting intro message", "err", err)
		}

	default:
		userID, ok := parseUnbanID(data.CustomID())
		if !ok {
			return
		}
		if m := e.Member(); m == nil || !m.Permissions.Has(discord.PermissionBanMembers) {
			b.replyEphemeral(e, "You need the **Ban Members** permission to unban.")
			return
		}
		if UnbanExpired(e.Message.CreatedAt(), time.Now()) {
			b.replyEphemeral(e, "This unban button has expired (24h). Unban the user manually in Server Settings → Bans.")
			return
		}
		err := b.Client.Rest().DeleteBan(guildID, userID,
			rest.WithReason(fmt.Sprintf("Joe's Honeypot: unban button clicked by %s", e.User().Username)))
		if err != nil {
			b.replyEphemeral(e, fmt.Sprintf("Failed to unban <@%d>: %s", userID, err))
			return
		}
		if err := e.CreateMessage(discord.MessageCreate{
			Content: fmt.Sprintf("🔓 <@%d> was unbanned by <@%d>.", userID, e.User().ID),
		}); err != nil {
			b.Log.Error("unban announcement", "err", err)
		}
	}
}
```

(`e.Message.CreatedAt()`: if disgo exposes the message timestamp differently, derive it from the snowflake: `e.Message.ID.Time()`.)

- [ ] **Step 4: Register listener** — append to `listeners` in `bot.go`:

```go
		dbot.WithEventListenerFunc(b.onComponent),
```

- [ ] **Step 5: Verify**

Run: `go build ./... && go test ./...` → ok.

- [ ] **Step 6: Commit**

```bash
git add internal/bot && git commit -m "feat: counter, unban, and intro-delete buttons"
```

---

### Task 12: Guild-join auto-setup

**Files:**
- Create: `internal/bot/setup.go`
- Modify: `internal/bot/bot.go` (append listener)

**Interfaces:**
- Consumes: `Normalize`/`Obfuscate` (Task 5), `IntroMessage` (Task 6), `ensureWarningMessage` (Task 9), store methods.
- Produces: `(*Bot).onGuildJoin(e *events.GuildJoin)`.

- [ ] **Step 1: Implement** — `internal/bot/setup.go`

```go
package bot

import (
	"math/rand"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

func (b *Bot) onGuildJoin(e *events.GuildJoin) {
	guildID := e.Guild.ID
	cfg, err := b.Store.GetConfig(guildID)
	if err != nil {
		b.Log.Error("checking existing config", "guild", guildID, "err", err)
		return
	}
	if cfg != nil {
		return // rejoined a guild we already know
	}

	channels, err := b.Client.Rest().GetGuildChannels(guildID)
	if err != nil {
		b.Log.Error("listing channels", "guild", guildID, "err", err)
		return
	}

	var honeypotID snowflake.ID
	for _, ch := range channels {
		if ch.Type() == discord.ChannelTypeGuildText && Normalize(ch.Name()) == "honeypot" {
			honeypotID = ch.ID()
			break
		}
	}
	if honeypotID == 0 {
		name := Obfuscate("honeypot", rand.New(rand.NewSource(time.Now().UnixNano())))
		position := len(channels) + 1
		created, err := b.Client.Rest().CreateGuildChannel(guildID, discord.GuildTextChannelCreate{
			Name:     name,
			Position: &position,
		})
		if err != nil {
			b.Log.Error("creating honeypot channel", "guild", guildID, "err", err)
			return
		}
		honeypotID = created.ID()
	}

	if err := b.Store.UpsertConfig(store.Config{GuildID: guildID, Action: store.ActionSoftban}); err != nil {
		b.Log.Error("saving default config", "err", err)
		return
	}
	if err := b.Store.SetChannel(guildID, honeypotID); err != nil {
		b.Log.Error("saving honeypot channel", "err", err)
		return
	}
	b.ensureWarningMessage(guildID, honeypotID)

	missingBan := !b.botPermissionsIn(guildID, honeypotID).Has(discord.PermissionBanMembers)
	intro, err := b.Client.Rest().CreateMessage(honeypotID, discord.MessageCreate{
		Content: IntroMessage(missingBan),
		Components: []discord.LayoutComponent{
			discord.NewActionRow(discord.NewSecondaryButton("Delete message now", introDeleteCID)),
		},
	})
	if err != nil {
		b.Log.Warn("posting intro message", "err", err)
		return
	}
	introChannelID, introID := intro.ChannelID, intro.ID
	time.AfterFunc(150*time.Second, func() {
		if err := b.Client.Rest().DeleteMessage(introChannelID, introID); err != nil {
			b.Log.Debug("intro already deleted", "err", err)
		}
	})
	b.Log.Info("auto-setup complete", "guild", guildID, "channel", honeypotID)
}
```

(Spec note: the @everyone View/Send overwrite fix applies only when the channel would otherwise be invisible; a channel created with no overwrites inherits @everyone's guild permissions, which is the common case. If `discord.GuildTextChannelCreate`'s `Position` field is plain `int` rather than `*int`, pass the value directly.)

- [ ] **Step 2: Register listener** — append to `listeners` in `bot.go`:

```go
		dbot.WithEventListenerFunc(b.onGuildJoin),
```

- [ ] **Step 3: Verify**

Run: `go build ./... && go test ./...` → ok.
Manual smoke test (recommended): run locally with a test token, add the bot to a test server, confirm: channel created with obfuscated name, warning message + counter button posted, intro self-deletes, posting in the channel as a normal member softbans.

- [ ] **Step 4: Commit**

```bash
git add internal/bot && git commit -m "feat: guild-join auto-setup with obfuscated channel creation"
```

---

### Task 13: Housekeeping handlers

**Files:**
- Create: `internal/bot/housekeeping.go`
- Modify: `internal/bot/bot.go` (append listeners)

**Interfaces:**
- Consumes: store methods `RemoveChannel`, `UnsetLogChannel`, `ClearWarningMsgByMsgID`, `DeleteGuild`, `GetConfig`, `GetChannelByID`.
- Produces: `(*Bot).onChannelDelete`, `(*Bot).onMessageDelete`, `(*Bot).onGuildLeave`.

- [ ] **Step 1: Implement** — `internal/bot/housekeeping.go`

```go
package bot

import (
	"github.com/disgoorg/disgo/events"
)

func (b *Bot) onChannelDelete(e *events.GuildChannelDelete) {
	if ch, err := b.Store.GetChannelByID(e.ChannelID); err == nil && ch != nil {
		if err := b.Store.RemoveChannel(e.ChannelID); err != nil {
			b.Log.Error("removing deleted honeypot channel", "err", err)
		}
		return
	}
	cfg, err := b.Store.GetConfig(e.GuildID)
	if err == nil && cfg != nil && cfg.LogChannelID != nil && *cfg.LogChannelID == e.ChannelID {
		if err := b.Store.UnsetLogChannel(e.GuildID); err != nil {
			b.Log.Error("unsetting deleted log channel", "err", err)
		}
	}
}

func (b *Bot) onMessageDelete(e *events.GuildMessageDelete) {
	if err := b.Store.ClearWarningMsgByMsgID(e.MessageID); err != nil {
		b.Log.Error("clearing warning msg id", "err", err)
	}
}

func (b *Bot) onGuildLeave(e *events.GuildLeave) {
	if err := b.Store.DeleteGuild(e.Guild.ID); err != nil {
		b.Log.Error("purging guild config", "guild", e.Guild.ID, "err", err)
	}
}
```

- [ ] **Step 2: Register listeners** — append to `listeners` in `bot.go`:

```go
		dbot.WithEventListenerFunc(b.onChannelDelete),
		dbot.WithEventListenerFunc(b.onMessageDelete),
		dbot.WithEventListenerFunc(b.onGuildLeave),
```

- [ ] **Step 3: Verify**

Run: `go build ./... && go test ./... && go vet ./...` → ok.

- [ ] **Step 4: Commit**

```bash
git add internal/bot && git commit -m "feat: housekeeping for deleted channels, messages, guilds"
```

---

### Task 14: Dockerfile + compose

**Files:**
- Create: `Dockerfile`, `.dockerignore`, `docker-compose.prod.yml`, `docker-compose.dev.yml`

**Interfaces:**
- Consumes: `cmd/bot` binary, env vars `BOT_TOKEN`/`DB_PATH`.
- Produces: image + compose services `joes-honeypot` / `joes-honeypot-dev` used verbatim by the deploy workflows (Task 15).

- [ ] **Step 1: Create `Dockerfile`**

```dockerfile
FROM golang:1.24-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /joes-honeypot ./cmd/bot

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -m app \
    && mkdir /data && chown app:app /data
USER app
COPY --from=builder /joes-honeypot /usr/local/bin/joes-honeypot
VOLUME /data
CMD ["joes-honeypot"]
```

- [ ] **Step 2: Create `.dockerignore`**

```
.git
docs
*.md
.env*
*.db*
.github
```

- [ ] **Step 3: Create `docker-compose.prod.yml`**

```yaml
services:
  joes-honeypot:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: joes-honeypot
    environment:
      BOT_TOKEN: ${BOT_TOKEN}
      DB_PATH: /data/honeypot.db
    volumes:
      - joes_honeypot_prod_data:/data
    restart: unless-stopped

volumes:
  joes_honeypot_prod_data:
```

- [ ] **Step 4: Create `docker-compose.dev.yml`**

```yaml
services:
  joes-honeypot-dev:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: joes-honeypot-dev
    environment:
      BOT_TOKEN: ${BOT_TOKEN}
      DB_PATH: /data/honeypot.db
    volumes:
      - joes_honeypot_dev_data:/data
    restart: unless-stopped

volumes:
  joes_honeypot_dev_data:
```

- [ ] **Step 5: Verify**

Run: `docker build -t joes-honeypot .` → builds successfully.
Run: `docker compose -f docker-compose.prod.yml config` → renders without errors (BOT_TOKEN warning is fine).

- [ ] **Step 6: Commit**

```bash
git add Dockerfile .dockerignore docker-compose.*.yml && git commit -m "feat: docker image and prod/dev compose files"
```

---

### Task 15: GitHub Actions workflows + README

**Files:**
- Create: `.github/workflows/lint.yml`, `.github/workflows/tests.yml`, `.github/workflows/deploy-prod.yml`, `.github/workflows/deploy-dev.yml`
- Modify: `README.md`

**Interfaces:**
- Consumes: compose files (Task 14). GitHub setup (manual, post-merge): environments `production` and `development` each with secret `BOT_TOKEN`; repo secrets `SERVER_HOST_SSH_PRIVATE_KEY`, `SERVER_HOST_IP`, `SERVER_HOST_USER`.

- [ ] **Step 1: Create `.github/workflows/lint.yml`**

```yaml
name: Lint

on:
  workflow_call:
    inputs:
      ref:
        required: false
        type: string
  pull_request:

jobs:
  golangci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          ref: ${{ inputs.ref || github.ref }}
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: golangci/golangci-lint-action@v8
```

- [ ] **Step 2: Create `.github/workflows/tests.yml`**

```yaml
name: Tests

on:
  workflow_call:
    inputs:
      ref:
        required: false
        type: string
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          ref: ${{ inputs.ref || github.ref }}
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go test -race ./...
```

- [ ] **Step 3: Create `.github/workflows/deploy-prod.yml`**

```yaml
name: Deploy Production

on:
  workflow_dispatch:
  push:
    branches: [main]
    paths-ignore:
      - "docs/**"
      - "**.md"

concurrency:
  group: deploy-prod

jobs:
  lint:
    uses: ./.github/workflows/lint.yml
    with:
      ref: ${{ github.sha }}
  tests:
    uses: ./.github/workflows/tests.yml
    with:
      ref: ${{ github.sha }}
  deploy:
    needs: [lint, tests]
    runs-on: ubuntu-latest
    environment: production
    steps:
      - uses: actions/checkout@v4
      - name: Set up SSH key
        run: |
          mkdir -p ~/.ssh
          echo "${{ secrets.SERVER_HOST_SSH_PRIVATE_KEY }}" > ~/.ssh/id_rsa
          chmod 600 ~/.ssh/id_rsa
      - name: Add remote host to known_hosts
        run: ssh-keyscan -H ${{ secrets.SERVER_HOST_IP }} >> ~/.ssh/known_hosts
      - name: Set up Docker context
        run: |
          docker context create prod-server \
            --docker "host=ssh://${{ secrets.SERVER_HOST_USER }}@${{ secrets.SERVER_HOST_IP }}"
          docker context use prod-server
      - name: Deploy
        run: docker compose -f docker-compose.prod.yml up -d --build --force-recreate
        env:
          BOT_TOKEN: ${{ secrets.BOT_TOKEN }}
```

- [ ] **Step 4: Create `.github/workflows/deploy-dev.yml`**

```yaml
name: Deploy Development

on:
  workflow_dispatch:
    inputs:
      ref:
        description: Git ref to deploy
        required: true
        default: main
  issue_comment:
    types: [created]

permissions:
  pull-requests: write
  deployments: write
  contents: write
  checks: read
  statuses: read

jobs:
  deploy:
    if: ${{ github.event_name == 'workflow_dispatch' || github.event.issue.pull_request }}
    runs-on: ubuntu-latest
    environment: development
    steps:
      - name: Branch deploy trigger
        id: branch-deploy
        if: ${{ github.event_name == 'issue_comment' }}
        uses: github/branch-deploy@v11.0.0
        with:
          trigger: ".deploy"
          environment: development
      - name: Gate on branch-deploy continue
        if: ${{ github.event_name == 'issue_comment' && steps.branch-deploy.outputs.continue != 'true' }}
        run: echo "branch-deploy did not continue" && exit 0
      - uses: actions/checkout@v4
        if: ${{ github.event_name == 'workflow_dispatch' || steps.branch-deploy.outputs.continue == 'true' }}
        with:
          ref: ${{ steps.branch-deploy.outputs.sha || inputs.ref }}
      - name: Set up SSH key
        if: ${{ github.event_name == 'workflow_dispatch' || steps.branch-deploy.outputs.continue == 'true' }}
        run: |
          mkdir -p ~/.ssh
          echo "${{ secrets.SERVER_HOST_SSH_PRIVATE_KEY }}" > ~/.ssh/id_rsa
          chmod 600 ~/.ssh/id_rsa
      - name: Add remote host to known_hosts
        if: ${{ github.event_name == 'workflow_dispatch' || steps.branch-deploy.outputs.continue == 'true' }}
        run: ssh-keyscan -H ${{ secrets.SERVER_HOST_IP }} >> ~/.ssh/known_hosts
      - name: Set up Docker context
        if: ${{ github.event_name == 'workflow_dispatch' || steps.branch-deploy.outputs.continue == 'true' }}
        run: |
          docker context create dev-server \
            --docker "host=ssh://${{ secrets.SERVER_HOST_USER }}@${{ secrets.SERVER_HOST_IP }}"
          docker context use dev-server
      - name: Deploy
        if: ${{ github.event_name == 'workflow_dispatch' || steps.branch-deploy.outputs.continue == 'true' }}
        run: docker compose -f docker-compose.dev.yml up -d --build --force-recreate
        env:
          BOT_TOKEN: ${{ secrets.BOT_TOKEN }}
```

- [ ] **Step 5: Update `README.md`** — replace the whole file:

```markdown
# Joe's Honeypot

Discord honeypot bot in Go. Designate a honeypot channel; any account that
posts there is automatically softbanned/banned (spam bots blast every
channel — real users read the warning). Modeled on
[RiskyMH/honeypot](https://github.com/RiskyMH/honeypot), minus experiments.

## How it works

- On joining a server the bot finds or creates a honeypot channel (name
  obfuscated with lookalike characters) and posts a warning message with a
  running ban counter.
- Posting in the channel: 🍯 react → DM → softban (ban + unban, deleting the
  last hour of messages) or ban → log message (with a 24h Unban button for
  bans). Server owner and administrators are exempt.
- `/honeypot` opens the config modal: honeypot channel, optional log channel,
  action (softban / ban / disabled).

Intents: Guilds + GuildMessages only. Message content is never read.

## Local development

    cp .env.example .env.local   # fill in BOT_TOKEN
    go run ./cmd/bot             # with the vars exported
    go test ./...

## Deployment

GitHub Actions deploys with a remote Docker context over SSH (no registry):

- **prod** — push to `main` → lint + tests → `docker compose -f
  docker-compose.prod.yml up -d --build` on the VPS.
- **dev** — comment `.deploy` on a PR (or run the workflow manually) →
  same against `docker-compose.dev.yml`.

Required GitHub configuration:

| Where | Name |
|---|---|
| Environment `production` | `BOT_TOKEN` |
| Environment `development` | `BOT_TOKEN` (dev bot application) |
| Repo secrets | `SERVER_HOST_SSH_PRIVATE_KEY`, `SERVER_HOST_IP`, `SERVER_HOST_USER` |

SQLite lives in the named volumes `joes_honeypot_{prod,dev}_data`.
```

- [ ] **Step 6: Verify**

Run: `actionlint` if installed (else visually check YAML with `docker run --rm -v $PWD:/repo rhysd/actionlint:latest -color` or skip). Run `go build ./... && go test ./...` one final time.

- [ ] **Step 7: Commit**

```bash
git add .github README.md && git commit -m "ci: lint/test gates and SSH-context deploy workflows"
```

---

## Post-implementation manual checklist (not tasks — needs the human)

1. Create two Discord applications (Joe's Honeypot, Joe's Honeypot Dev); disable "Public Bot" so only you can add them; copy tokens.
2. Create the GitHub repo, environments `production`/`development` with `BOT_TOKEN` each, and the three `SERVER_HOST_*` repo secrets.
3. Invite URL scopes: `bot applications.commands`; permissions: Ban Members, Manage Channels, Manage Messages, View Channels, Send Messages, Add Reactions.
4. Smoke-test in a throwaway server with the dev bot before pushing to main.
```
