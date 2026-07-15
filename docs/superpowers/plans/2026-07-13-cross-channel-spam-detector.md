# Cross-Channel Duplicate-Image Spam Detector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Punish users who post the same set of 2+ images in 2+ distinct channels within 30 minutes, alongside the existing honeypot-channel behavior.

**Architecture:** A rolling in-memory cache (`internal/cache` TTL map, extended with an atomic `Update` method) maps `(guild, user, attachment-fingerprint)` → set of channel IDs. Fingerprints are FNV-64 hashes of sorted `(filename, size)` pairs — no downloads, no message text. When a fingerprint's channel set reaches 2, the message funnels into the existing `moderate()` machinery (same action, exemptions, retries, alerts), parameterized by a new `triggerKind` for wording and audit reasons. One new config column (`spam_detection`, default on) and one new modal field.

**Tech Stack:** Go 1.x, disgo v0.19.6, modernc.org/sqlite, stdlib `hash/fnv`.

**Spec:** `docs/superpowers/specs/2026-07-13-cross-channel-spam-detector-design.md`

## Global Constraints

- Detection constants are package-level, not per-guild config: window **30 min** (sliding), min attachments **2**, distinct-channel threshold **2**.
- Fingerprint uses attachment **metadata only** (filename + byte size). Never download attachments; never read, store, or log message text.
- Exempt users (owner / non-managed admin role) are skipped **silently** on the spam path — no DM, no log entry.
- Honeypot-channel behavior is unchanged; honeypot messages never reach the spam detector.
- All code follows existing file conventions: pure decision funcs live beside `rules.go`-style code, tests are table-driven with `t.Errorf("%s: got %v, want %v", ...)`, comments state constraints not narration.
- Run `go build ./... && go test ./...` before every commit; both must pass.
- Commit after every task (git is on branch `spam-detector`).

---

### Task 1: `cache.Update` — atomic read-modify-write on the TTL cache

**Files:**
- Modify: `internal/cache/ttl.go` (append method after `SetIfAbsent`, ~line 90)
- Test: `internal/cache/ttl_test.go`

**Interfaces:**
- Consumes: existing `TTL[K,V]` internals (`c.mu`, `c.m`, `c.store`).
- Produces: `func (c *TTL[K, V]) Update(k K, ttl time.Duration, fn func(V) V)` — Task 4's `recordSpamSighting` calls this. `fn` receives the current live value, or the zero value of `V` if the key is absent or expired; its return value is stored under `k` with a fresh `ttl` (sliding window).

- [ ] **Step 1: Write the failing tests**

Append to `internal/cache/ttl_test.go`:

```go
func TestUpdate(t *testing.T) {
	t.Run("absent key gets zero value", func(t *testing.T) {
		c := NewTTL[string, int]()
		c.Update("a", time.Minute, func(cur int) int {
			if cur != 0 {
				t.Errorf("fn got %d, want zero value 0", cur)
			}
			return cur + 1
		})
		if v, ok := c.Get("a"); !ok || v != 1 {
			t.Fatalf("got %v %v, want 1 true", v, ok)
		}
	})
	t.Run("live key gets current value", func(t *testing.T) {
		c := NewTTL[string, int]()
		c.Set("a", 5, time.Minute)
		c.Update("a", time.Minute, func(cur int) int { return cur + 1 })
		if v, _ := c.Get("a"); v != 6 {
			t.Fatalf("got %v, want 6", v)
		}
	})
	t.Run("expired key treated as absent", func(t *testing.T) {
		c := NewTTL[string, int]()
		c.Set("a", 5, time.Nanosecond)
		time.Sleep(time.Millisecond)
		c.Update("a", time.Minute, func(cur int) int {
			if cur != 0 {
				t.Errorf("fn got %d, want zero value 0", cur)
			}
			return 9
		})
		if v, _ := c.Get("a"); v != 9 {
			t.Fatalf("got %v, want 9", v)
		}
	})
	t.Run("update refreshes ttl", func(t *testing.T) {
		c := NewTTL[string, int]()
		c.Set("a", 1, 20*time.Millisecond)
		time.Sleep(10 * time.Millisecond)
		c.Update("a", time.Minute, func(cur int) int { return cur })
		time.Sleep(15 * time.Millisecond) // past the original expiry
		if _, ok := c.Get("a"); !ok {
			t.Fatal("entry expired despite Update refreshing the ttl")
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cache/ -run TestUpdate -v`
Expected: FAIL — `c.Update undefined (type *TTL[string, int] has no field or method Update)`

- [ ] **Step 3: Implement `Update`**

Append to `internal/cache/ttl.go`:

```go
// Update applies fn to the current value for k — or the zero value of V if
// k is absent or expired — and stores the result with a fresh ttl. fn runs
// under the cache lock: keep it short and never call back into the cache.
func (c *TTL[K, V]) Update(k K, ttl time.Duration, fn func(V) V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var cur V
	if e, ok := c.m[k]; ok && !time.Now().After(e.expiresAt) {
		cur = e.val
	}
	c.store(k, fn(cur), ttl)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cache/ -v`
Expected: all PASS (including pre-existing tests)

- [ ] **Step 5: Commit**

```bash
git add internal/cache/ttl.go internal/cache/ttl_test.go
git commit -m "feat(cache): add Update for atomic read-modify-write"
```

---

### Task 2: Store — `spam_detection` column and nullable event channel

The spam detector needs a per-guild on/off flag, and it records moderation
events from channels that are *not* registered honeypot channels —
`honeypot_events.channel_id` has an FK to `honeypot_channels(channel_id)`,
so spam events must insert `NULL` there (the column is already nullable
with `ON DELETE SET NULL`).

**Files:**
- Create: `internal/store/migrations/0002_spam_detection.sql`
- Modify: `internal/store/queries.go` (Config struct ~line 22, `GetConfig` ~line 51, `UpsertConfig` ~line 68, `SaveGuildSetup` ~line 190, `RecordEvent` ~line 297)
- Modify: `internal/bot/handler_message.go:146` (RecordEvent call site)
- Modify: `internal/bot/setup.go:58`, `internal/bot/handler_config.go:128` (Config literals)
- Test: `internal/store/queries_test.go`

**Interfaces:**
- Consumes: existing `nullID(*snowflake.ID) sql.NullInt64` helper in queries.go.
- Produces:
  - `store.Config` gains field `SpamDetection bool` (persisted; default on for new and existing guilds).
  - `func (s *Store) RecordEvent(ctx context.Context, guildID, userID snowflake.ID, channelID *snowflake.ID) error` — **signature change**: `channelID` becomes a pointer; `nil` means "no registered honeypot channel" (spam events). Task 3's `moderate` passes `nil` for spam triggers.

- [ ] **Step 1: Write the migration**

Create `internal/store/migrations/0002_spam_detection.sql`:

```sql
ALTER TABLE honeypot_config ADD COLUMN spam_detection INTEGER NOT NULL DEFAULT 1;
```

- [ ] **Step 2: Write the failing tests**

In `internal/store/queries_test.go`, `TestConfigRoundTrip` (~line 21) exercises `Config` — extend it and add a nil-channel event test. Update the `want` literal and assertions:

```go
func TestConfigRoundTrip(t *testing.T) {
	s := openTest(t)
	if cfg, err := s.GetConfig(t.Context(), g); err != nil || cfg != nil {
		t.Fatalf("empty GetConfig = %v, %v; want nil, nil", cfg, err)
	}
	want := Config{GuildID: g, LogChannelID: ptr(999), Action: ActionBan, SpamDetection: true}
	if err := s.UpsertConfig(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConfig(t.Context(), g)
	if err != nil || got == nil || got.Action != ActionBan || *got.LogChannelID != 999 || !got.SpamDetection {
		t.Fatalf("got %+v, %v", got, err)
	}
	want.Action = ActionSoftban
	want.LogChannelID = nil
	want.SpamDetection = false
	if err := s.UpsertConfig(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetConfig(t.Context(), g)
	if got.Action != ActionSoftban || got.LogChannelID != nil || got.SpamDetection {
		t.Fatalf("after upsert got %+v", got)
	}
}
```

(Keep the rest of the original function if it continues beyond what is shown here — only the `want` literal and the two assertion lines gain `SpamDetection`.)

Append a new test:

```go
func TestRecordEventNilChannel(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertConfig(t.Context(), Config{GuildID: g, Action: ActionSoftban, SpamDetection: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEvent(t.Context(), g, u, nil); err != nil {
		t.Fatalf("RecordEvent with nil channel: %v", err)
	}
	n, err := s.CountEventsByGuild(t.Context(), g)
	if err != nil || n != 1 {
		t.Fatalf("count = %d, %v; want 1", n, err)
	}
}
```

Also update the existing `RecordEvent` call at `internal/store/queries_test.go:130` from `s.RecordEvent(t.Context(), g, u, ch)` to `s.RecordEvent(t.Context(), g, u, ptr(ch))`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/store/ -v`
Expected: FAIL — compile errors (`unknown field SpamDetection`, wrong argument type for `RecordEvent`)

- [ ] **Step 4: Implement the store changes**

In `internal/store/queries.go`:

Config struct:

```go
// Config is a guild's honeypot settings; one row per guild.
type Config struct {
	GuildID       snowflake.ID
	LogChannelID  *snowflake.ID
	Action        Action
	SpamDetection bool // cross-channel duplicate-image detector on/off
}
```

`GetConfig`:

```go
func (s *Store) GetConfig(ctx context.Context, guildID snowflake.ID) (*Config, error) {
	var (
		logCh  sql.NullInt64
		action string
		spam   bool
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT log_channel_id, action, spam_detection FROM honeypot_config WHERE guild_id = ?`, int64(guildID),
	).Scan(&logCh, &action, &spam)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Config{GuildID: guildID, LogChannelID: idPtr(logCh), Action: Action(action), SpamDetection: spam}, nil
}
```

`UpsertConfig` (same SQL change also goes in `SaveGuildSetup`'s inline INSERT, ~line 195):

```go
func (s *Store) UpsertConfig(ctx context.Context, cfg Config) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO honeypot_config (guild_id, log_channel_id, action, spam_detection) VALUES (?, ?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET log_channel_id = excluded.log_channel_id, action = excluded.action, spam_detection = excluded.spam_detection`,
		int64(cfg.GuildID), nullID(cfg.LogChannelID), string(cfg.Action), cfg.SpamDetection)
	return err
}
```

In `SaveGuildSetup`, replace the config INSERT with:

```go
	_, err = tx.ExecContext(ctx, `INSERT INTO honeypot_config (guild_id, log_channel_id, action, spam_detection) VALUES (?, ?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET log_channel_id = excluded.log_channel_id, action = excluded.action, spam_detection = excluded.spam_detection`,
		int64(cfg.GuildID), nullID(cfg.LogChannelID), string(cfg.Action), cfg.SpamDetection)
```

`RecordEvent`:

```go
func (s *Store) RecordEvent(ctx context.Context, guildID, userID snowflake.ID, channelID *snowflake.ID) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO honeypot_events (guild_id, user_id, channel_id) VALUES (?, ?, ?)`,
		int64(guildID), int64(userID), nullID(channelID))
	return err
}
```

- [ ] **Step 5: Update the three bot-package call sites (compile fixes, behavior-preserving)**

`internal/bot/handler_message.go:146`:

```go
	if err := b.store.RecordEvent(b.ctx, guildID, msg.Author.ID, &channelID); err != nil {
```

`internal/bot/setup.go:58` — add `SpamDetection: true` (the default for fresh guilds):

```go
	if err := b.store.SaveGuildSetup(b.ctx, store.Config{GuildID: guildID, Action: store.ActionSoftban, SpamDetection: true}, honeypotID); err != nil {
```

`internal/bot/handler_config.go:128` — add `SpamDetection: true` for now; Task 5 replaces this with the modal's submitted value:

```go
	if err := b.store.SaveGuildSetup(b.ctx, store.Config{GuildID: guildID, LogChannelID: sub.LogChannelID, Action: sub.Action, SpamDetection: true}, sub.HoneypotChannelID); err != nil {
```

- [ ] **Step 6: Run build and all tests**

Run: `go build ./... && go test ./...`
Expected: all PASS (migration applies in `openTest`'s fresh DB; existing DBs get the column via migration 0002)

- [ ] **Step 7: Commit**

```bash
git add internal/store/ internal/bot/handler_message.go internal/bot/setup.go internal/bot/handler_config.go
git commit -m "feat(store): spam_detection config flag, nullable event channel"
```

---

### Task 3: `triggerKind` — parameterize wording and fix warning-refresh targeting

`moderate()` currently hardcodes honeypot wording and refreshes the warning
message in the *triggering* channel (correct only when the trigger IS the
honeypot channel). This task makes both trigger-aware so Task 4 can reuse
`moderate` verbatim.

**Files:**
- Modify: `internal/bot/templates.go` (add `triggerKind` + methods; change `dmMessage` ~line 47, `logMessage` ~line 63)
- Modify: `internal/bot/handler_message.go` (`moderate` signature ~line 88, its body lines 109/119/146/150/159, and the call at line 61)
- Test: `internal/bot/templates_test.go`

**Interfaces:**
- Consumes: `store.RecordEvent(ctx, guildID, userID, *snowflake.ID)` from Task 2.
- Produces (Task 4 depends on these exact signatures):
  - `type triggerKind int` with constants `triggerHoneypot` and `triggerSpam`, methods `title() string`, `description() string`, `banReason() string`.
  - `func (b *Bot) moderate(plan moderationPlan, cfg *store.Config, channelID snowflake.ID, msg discord.Message, guildName string, kind triggerKind)`
  - `func dmMessage(action store.Action, guildName string, kind triggerKind) string`
  - `func logMessage(userID snowflake.ID, action store.Action, kind triggerKind) string`

- [ ] **Step 1: Write the failing tests**

In `internal/bot/templates_test.go`, update the existing calls at lines 13, 17, and 23 to pass `triggerHoneypot` as the new final argument (assertions unchanged):

```go
	dm := dmMessage(store.ActionSoftban, "My Server", triggerHoneypot)
```
```go
	if dm := dmMessage(store.ActionBan, "My Server", triggerHoneypot); !strings.Contains(dm, "banned") {
```
```go
	msg := logMessage(42, store.ActionBan, triggerHoneypot)
```

Append a new test:

```go
func TestTriggerKindWording(t *testing.T) {
	dm := dmMessage(store.ActionBan, "My Server", triggerSpam)
	if !strings.Contains(dm, "Spam Detected") || !strings.Contains(dm, "same images in multiple channels") {
		t.Errorf("spam DM missing spam wording: %q", dm)
	}
	if dm := dmMessage(store.ActionBan, "My Server", triggerHoneypot); !strings.Contains(dm, "honeypot channel") {
		t.Errorf("honeypot DM missing honeypot wording: %q", dm)
	}
	lg := logMessage(42, store.ActionSoftban, triggerSpam)
	if !strings.Contains(lg, "<@42>") || !strings.Contains(lg, "same images in multiple channels") {
		t.Errorf("spam log missing spam wording: %q", lg)
	}
	if r := triggerSpam.banReason(); !strings.Contains(r, "Joe's Honeypot") {
		t.Errorf("ban reason must identify the bot: %q", r)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/bot/ -run 'TestDMMessage|TestLogMessage|TestTriggerKindWording' -v`
Expected: FAIL — compile errors (`undefined: triggerHoneypot`, wrong number of arguments)

(If the existing test names differ, run `go test ./internal/bot/` — the package won't compile, which is the expected failure.)

- [ ] **Step 3: Implement `triggerKind` and the template changes**

In `internal/bot/templates.go`, add after the `actionVerb` function:

```go
// triggerKind identifies which detector caught a user — the honeypot
// channel or the cross-channel image-spam detector — selecting the wording
// of DMs, log messages, and the audit-log ban reason.
type triggerKind int

const (
	triggerHoneypot triggerKind = iota
	triggerSpam
)

// description completes sentences like "you have been banned for …".
func (k triggerKind) description() string {
	if k == triggerSpam {
		return "posting the same images in multiple channels"
	}
	return "sending a message in the honeypot channel"
}

func (k triggerKind) title() string {
	if k == triggerSpam {
		return "## 🍯 Spam Detected"
	}
	return "## 🍯 Honeypot Triggered"
}

// banReason is the Discord audit-log reason attached to the ban REST call.
func (k triggerKind) banReason() string {
	return "Joe's Honeypot: " + k.description()
}
```

Replace `dmMessage` and `logMessage`:

```go
func dmMessage(action store.Action, guildName string, kind triggerKind) string {
	return fmt.Sprintf(
		"%s\nYou have been **%s** from **%s** for %s.\n"+dmFooter,
		kind.title(), actionVerb(action), guildName, kind.description())
}
```

```go
func logMessage(userID snowflake.ID, action store.Action, kind triggerKind) string {
	return fmt.Sprintf("<@%d> was %s for %s.", userID, actionVerb(action), kind.description())
}
```

- [ ] **Step 4: Thread `kind` through `moderate`**

In `internal/bot/handler_message.go`:

Line 61 (honeypot call site):

```go
	b.moderate(decideModeration(cfg.Action, exempt), cfg, e.ChannelID, msg, inputs.GuildName, triggerHoneypot)
```

Signature (line 88):

```go
func (b *Bot) moderate(plan moderationPlan, cfg *store.Config, channelID snowflake.ID, msg discord.Message, guildName string, kind triggerKind) {
```

Line 109 (DM):

```go
			if err := b.dmUser(msg.Author.ID, dmMessage(cfg.Action, guildName, kind)); err != nil {
```

Line 119 (audit reason):

```go
	reason := rest.WithReason(kind.banReason())
```

Line 146 (event recording — spam triggers happen in channels that aren't in
`honeypot_channels`, whose FK the events table references, so they record
`NULL`):

```go
	var eventChannel *snowflake.ID
	if kind == triggerHoneypot {
		eventChannel = &channelID
	}
	if err := b.store.RecordEvent(b.ctx, guildID, msg.Author.ID, eventChannel); err != nil {
		b.log.Error("recording event", "guild", guildID, "user", msg.Author.ID, "err", err)
	}
```

Line 150 (log message):

```go
	logMsg := discord.MessageCreate{Content: logMessage(msg.Author.ID, cfg.Action, kind)}
```

Line 159 (warning refresh — target the guild's *registered* honeypot channel,
not the triggering channel; for honeypot triggers these are the same, for
spam triggers they are not):

```go
	if hp, err := b.store.GetChannel(guildID); err != nil {
		b.log.Error("loading honeypot channel for warning refresh", "guild", guildID, "err", err)
	} else if hp != nil {
		if err := b.ensureWarningMessage(guildID, hp.ChannelID); err != nil {
			b.log.Warn("refreshing warning message after moderation", "guild", guildID, "channel", hp.ChannelID, "err", err)
		}
	}
```

- [ ] **Step 5: Run build and all tests**

Run: `go build ./... && go test ./...`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/bot/templates.go internal/bot/templates_test.go internal/bot/handler_message.go
git commit -m "refactor(bot): parameterize moderation wording by trigger kind"
```

---

### Task 4: The spam detector — `internal/bot/spam.go`

**Files:**
- Create: `internal/bot/spam.go`
- Create: `internal/bot/spam_test.go`
- Modify: `internal/bot/bot.go` (add `spamSightings` field ~line 34, initialize in `New` ~line 51)
- Modify: `internal/bot/handler_message.go:30-33` (wire `checkSpam` into the non-honeypot branch)

**Interfaces:**
- Consumes: `cache.Update` (Task 1), `store.Config.SpamDetection` (Task 2), `moderate(..., triggerSpam)` (Task 3), existing `isTriggerMessage`, `gatherExemptionInputs`, `isExempt`, `isAdminRole`, `decideModeration`, `dedupKey`, `b.dedup`.
- Produces:
  - `type spamKey struct { GuildID, UserID snowflake.ID; Fingerprint uint64 }`
  - `func spamFingerprint(atts []discord.Attachment) uint64`
  - `func spamEligible(numAttachments int, cfg *store.Config) bool`
  - `func recordSpamSighting(c *cache.TTL[spamKey, map[snowflake.ID]struct{}], k spamKey, channelID snowflake.ID) int`
  - `func (b *Bot) checkSpam(e *events.MessageCreate, guildID snowflake.ID)`
  - Constants `spamWindow = 30 * time.Minute`, `spamMinAttachments = 2`, `spamChannelThreshold = 2`.

- [ ] **Step 1: Write the failing tests**

Create `internal/bot/spam_test.go`:

```go
package bot

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/cache"
)

func att(name string, size int) discord.Attachment {
	return discord.Attachment{Filename: name, Size: size}
}

func TestSpamFingerprint(t *testing.T) {
	a := []discord.Attachment{att("a.png", 100), att("b.png", 200)}
	if spamFingerprint(a) != spamFingerprint(a) {
		t.Error("fingerprint must be deterministic")
	}
	reordered := []discord.Attachment{att("b.png", 200), att("a.png", 100)}
	if spamFingerprint(a) != spamFingerprint(reordered) {
		t.Error("fingerprint must not depend on attachment order")
	}
	renamed := []discord.Attachment{att("c.png", 100), att("b.png", 200)}
	if spamFingerprint(a) == spamFingerprint(renamed) {
		t.Error("fingerprint must change when a filename changes")
	}
	resized := []discord.Attachment{att("a.png", 101), att("b.png", 200)}
	if spamFingerprint(a) == spamFingerprint(resized) {
		t.Error("fingerprint must change when a size changes")
	}
	// (filename, size) pairs must not bleed into each other when joined.
	x := []discord.Attachment{att("a", 11), att("b", 2)}
	y := []discord.Attachment{att("a", 1), att("1b", 2)}
	if spamFingerprint(x) == spamFingerprint(y) {
		t.Error("fingerprint must separate filename and size unambiguously")
	}
}

func TestRecordSpamSighting(t *testing.T) {
	c := cache.NewTTL[spamKey, map[snowflake.ID]struct{}]()
	k := spamKey{GuildID: 1, UserID: 2, Fingerprint: 3}
	if n := recordSpamSighting(c, k, 100); n != 1 {
		t.Errorf("first channel: got %d, want 1", n)
	}
	if n := recordSpamSighting(c, k, 100); n != 1 {
		t.Errorf("same channel again: got %d, want 1", n)
	}
	if n := recordSpamSighting(c, k, 200); n != 2 {
		t.Errorf("second distinct channel: got %d, want 2", n)
	}
	other := spamKey{GuildID: 1, UserID: 2, Fingerprint: 4}
	if n := recordSpamSighting(c, other, 300); n != 1 {
		t.Errorf("different fingerprint must count separately: got %d, want 1", n)
	}
}

func TestSpamEligible(t *testing.T) {
	cfg := func(action store.Action, spam bool) *store.Config {
		return &store.Config{GuildID: 1, Action: action, SpamDetection: spam}
	}
	cases := []struct {
		name string
		n    int
		cfg  *store.Config
		want bool
	}{
		{"two attachments, softban, enabled", 2, cfg(store.ActionSoftban, true), true},
		{"three attachments, ban, enabled", 3, cfg(store.ActionBan, true), true},
		{"one attachment", 1, cfg(store.ActionSoftban, true), false},
		{"zero attachments", 0, cfg(store.ActionSoftban, true), false},
		{"toggle off", 2, cfg(store.ActionSoftban, false), false},
		{"action disabled", 2, cfg(store.ActionDisabled, true), false},
		{"no config", 2, nil, false},
	}
	for _, c := range cases {
		if got := spamEligible(c.n, c.cfg); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
```

(The test file also needs `"github.com/bkan0n/joeshoneypot/internal/store"` in its imports.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/bot/ -run 'TestSpamFingerprint|TestRecordSpamSighting' -v`
Expected: FAIL — compile errors (`undefined: spamFingerprint`, `undefined: spamKey`)

- [ ] **Step 3: Implement the detector**

Create `internal/bot/spam.go`:

```go
package bot

import (
	"hash/fnv"
	"sort"
	"strconv"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/cache"
	"github.com/bkan0n/joeshoneypot/internal/store"
)

// The cross-channel image-spam detector: spam bots blast the same set of
// images into many channels within seconds. Each attachment-bearing message
// is fingerprinted by metadata only; when the same (user, fingerprint)
// appears in spamChannelThreshold distinct channels within spamWindow, the
// user is moderated with the guild's configured action.
const (
	spamWindow           = 30 * time.Minute // sliding: refreshed on every sighting
	spamMinAttachments   = 2                // messages with fewer attachments are ignored
	spamChannelThreshold = 2                // distinct channels that trigger moderation
)

// spamKey identifies one user's repeated posting of one attachment set.
type spamKey struct {
	GuildID     snowflake.ID
	UserID      snowflake.ID
	Fingerprint uint64
}

// spamFingerprint hashes a message's attachment metadata — sorted
// (filename, size) pairs — into a stable identity. Attachment bytes are
// never downloaded and message text is never read.
func spamFingerprint(atts []discord.Attachment) uint64 {
	parts := make([]string, len(atts))
	for i, a := range atts {
		// \x00 separates name from size so ("a1", 1) never collides with ("a", 11).
		parts[i] = a.Filename + "\x00" + strconv.Itoa(a.Size)
	}
	sort.Strings(parts)
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0xff}) // separate pairs
	}
	return h.Sum64()
}

// spamEligible reports whether a message and guild config qualify for spam
// tracking: enough attachments, detection enabled, and an action that bans.
func spamEligible(numAttachments int, cfg *store.Config) bool {
	if numAttachments < spamMinAttachments {
		return false
	}
	if cfg == nil || !cfg.SpamDetection {
		return false
	}
	return cfg.Action == store.ActionSoftban || cfg.Action == store.ActionBan
}

// recordSpamSighting adds channelID to the fingerprint's channel set and
// returns the number of distinct channels seen within the window.
func recordSpamSighting(c *cache.TTL[spamKey, map[snowflake.ID]struct{}], k spamKey, channelID snowflake.ID) int {
	n := 0
	c.Update(k, spamWindow, func(set map[snowflake.ID]struct{}) map[snowflake.ID]struct{} {
		if set == nil {
			set = map[snowflake.ID]struct{}{}
		}
		set[channelID] = struct{}{}
		n = len(set)
		return set
	})
	return n
}

// checkSpam runs for every guild message outside the honeypot channel
// (author already filtered to non-bot, non-system by isTriggerMessage).
// Exempt users are skipped silently — admins legitimately repost images.
func (b *Bot) checkSpam(e *events.MessageCreate, guildID snowflake.ID) {
	msg := e.Message
	if len(msg.Attachments) < spamMinAttachments {
		return
	}
	cfg, err := b.store.GetConfig(b.ctx, guildID)
	if err != nil {
		b.log.Error("loading config for spam check", "guild", guildID, "err", err)
		return
	}
	if !spamEligible(len(msg.Attachments), cfg) {
		return
	}

	key := spamKey{GuildID: guildID, UserID: msg.Author.ID, Fingerprint: spamFingerprint(msg.Attachments)}
	if recordSpamSighting(b.spamSightings, key, e.ChannelID) < spamChannelThreshold {
		return
	}

	dk := dedupKey{GuildID: guildID, UserID: msg.Author.ID}
	if !b.dedup.SetIfAbsent(dk, struct{}{}, 30*time.Second) {
		return
	}
	defer b.dedup.Delete(dk) // allow re-punishing a rejoining user

	inputs := b.gatherExemptionInputs(guildID, msg)
	if isExempt(msg.Author.ID, inputs.OwnerID, inputs.MemberRoles, func(roleID snowflake.ID) bool {
		role, ok := b.client.Caches.Role(guildID, roleID)
		return ok && isAdminRole(role)
	}) {
		return
	}
	b.moderate(decideModeration(cfg.Action, false), cfg, e.ChannelID, msg, inputs.GuildName, triggerSpam)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bot/ -run 'TestSpamFingerprint|TestRecordSpamSighting' -v`
Expected: PASS

- [ ] **Step 5: Wire the detector into the Bot**

In `internal/bot/bot.go`, add the field after `dms` (~line 34):

```go
	dms   *cache.TTL[snowflake.ID, snowflake.ID]

	// spamSightings backs the cross-channel image-spam detector: which
	// channels each (guild, user, attachment-fingerprint) has appeared in
	// within the sliding spamWindow. Process-local; a restart forgets at
	// most 30 minutes of counting, which spam blasts (seconds apart) survive.
	spamSightings *cache.TTL[spamKey, map[snowflake.ID]struct{}]
```

Initialize in `New` (~line 51, alongside the other caches):

```go
		spamSightings: cache.NewTTL[spamKey, map[snowflake.ID]struct{}](),
```

In `internal/bot/handler_message.go`, the non-honeypot branch (lines 30-33) becomes:

```go
	if hpChannel == nil || hpChannel.GuildID != guildID {
		b.handleMentionRefresh(e)
		b.checkSpam(e, guildID)
		return
	}
```

- [ ] **Step 6: Run build and all tests**

Run: `go build ./... && go test ./...`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/bot/spam.go internal/bot/spam_test.go internal/bot/bot.go internal/bot/handler_message.go
git commit -m "feat(bot): cross-channel duplicate-image spam detector"
```

---

### Task 5: Config modal — spam-detection toggle

**Files:**
- Modify: `internal/bot/handler_config.go` (CID consts ~line 16, `configModal` ~line 25, `configSubmission` ~line 74, `onModalSubmit` parsing ~line 100 and save ~line 128)

**Interfaces:**
- Consumes: `store.Config.SpamDetection` (Task 2).
- Produces: modal select with custom ID `spam_detection`, values `"on"`/`"off"`; `configSubmission` gains `SpamDetection bool`.

- [ ] **Step 1: Add the CID constant**

In the const block at the top of `internal/bot/handler_config.go`:

```go
	spamCID          = "spam_detection"
```

- [ ] **Step 2: Add the modal field**

In `configModal`, add before the closing parenthesis of `discord.NewModalCreate` (after the "Action" label), and compute the default at the top of the function next to `defaultAction`:

```go
	spamDefault := true
	if current != nil {
		spamDefault = current.SpamDetection
	}
	spamOpt := func(label, value string, def bool) discord.StringSelectMenuOption {
		o := discord.NewStringSelectMenuOption(label, value)
		if def {
			o = o.WithDefault(true)
		}
		return o
	}
```

```go
		discord.NewLabel("Cross-channel image spam detection",
			discord.NewStringSelectMenu(spamCID, "Choose",
				spamOpt("Enabled — punish users posting the same images in multiple channels", "on", spamDefault),
				spamOpt("Disabled", "off", !spamDefault),
			).WithMinValues(1).WithMaxValues(1)),
```

- [ ] **Step 3: Parse the submission**

Add the field to `configSubmission`:

```go
type configSubmission struct {
	HoneypotChannelID snowflake.ID
	LogChannelID      *snowflake.ID
	Action            store.Action
	SpamDetection     bool
}
```

In `onModalSubmit`, after the action parsing/validation switch (~line 106), add (defaulting to on if the select is somehow missing):

```go
	sub.SpamDetection = true
	if vals := e.Data.StringValues(spamCID); len(vals) == 1 {
		sub.SpamDetection = vals[0] == "on"
	}
```

Replace the Task-2 placeholder in the save call (line ~128):

```go
	if err := b.store.SaveGuildSetup(b.ctx, store.Config{GuildID: guildID, LogChannelID: sub.LogChannelID, Action: sub.Action, SpamDetection: sub.SpamDetection}, sub.HoneypotChannelID); err != nil {
```

- [ ] **Step 4: Run build and all tests**

Run: `go build ./... && go test ./...`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bot/handler_config.go
git commit -m "feat(bot): spam-detection toggle in config modal"
```

---

### Task 6: MessageContent intent and README

Without the privileged `MessageContent` intent, `Message.Attachments`
arrives empty on other users' messages and the detector silently never
fires.

**Files:**
- Modify: `internal/bot/bot.go:72` (intents)
- Modify: `README.md` (top description, "How it works", intents line, deployment prerequisite)

**Interfaces:**
- Consumes: nothing new.
- Produces: gateway connects with `IntentGuilds | IntentGuildMessages | IntentMessageContent`.

- [ ] **Step 1: Add the intent**

In `internal/bot/bot.go`, line 72:

```go
			gateway.WithIntents(gateway.IntentGuilds|gateway.IntentGuildMessages|gateway.IntentMessageContent),
```

- [ ] **Step 2: Update the README**

In `README.md`:

Add to the "How it works" list:

```markdown
- Cross-channel image spam: a user posting a message with 2+ attachments
  whose attachment set (filenames + sizes) repeats in a second channel
  within 30 minutes gets the same softban/ban treatment. Detection state is
  a small in-memory cache; nothing is downloaded. Toggle it in `/honeypot`.
```

Replace the intents line:

```markdown
Intents: Guilds + GuildMessages + MessageContent. The MessageContent intent
is needed only to see attachment metadata (filename + size) for spam
fingerprinting; message text is never read, stored, or logged.
```

Add under "Deployment" (before the CI description):

```markdown
**Prerequisite:** enable **Message Content Intent** in the Discord developer
portal (Bot → Privileged Gateway Intents) for both the prod and dev bot
applications before deploying this version — the gateway rejects the
connection otherwise.
```

- [ ] **Step 3: Run build and all tests**

Run: `go build ./... && go test ./...`
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add internal/bot/bot.go README.md
git commit -m "feat(bot): enable MessageContent intent, document spam detector"
```

---

## Verification (after all tasks)

- `go build ./... && go test ./...` — everything green.
- `go vet ./...` and the repo's linter (`golangci-lint run`) if installed.
- Manual smoke test (dev bot, with the portal intent enabled): post the same
  two images in two different channels within a minute → softban + DM
  ("Spam Detected") + log message; post them twice in the *same* channel →
  nothing; as an admin, do the same → nothing, silently.
