# Cross-channel duplicate-image spam detector

**Date:** 2026-07-13
**Status:** Approved

## Problem

Automated spammers have learned to avoid the honeypot channel. Their
signature behavior instead: post the same set of 2+ images to many channels
in quick succession. Since the bot receives every guild message through the
gateway one at a time, it can keep a small rolling cache of recent
attachment fingerprints per user and punish accounts that repeat the same
multi-image message across channels.

## Decisions

| Question | Decision |
|---|---|
| Relationship to honeypot | Runs **alongside** it; honeypot behavior unchanged |
| Duplicate definition | Attachment **metadata** fingerprint: sorted (filename, byte size) pairs; no downloads, no text content |
| Minimum attachments | Message enters the cache only with **≥ 2 attachments** |
| Trigger threshold | Same fingerprint in **≥ 2 distinct channels** within the window |
| Window | **30 minutes**, sliding (refreshed on each sighting) |
| Punishment | Guild's existing softban/ban action + exemption rules |
| Config surface | One new field: spam detection **on/off, default on** (incl. existing guilds) |
| Cache home | In-memory TTL cache (`internal/cache`); restart forgets ≤ 30 min of sightings — acceptable |
| Thresholds/window | Package constants, not per-guild config |

## 1. Overview & intents

The gateway gains the **`MessageContent` privileged intent** — without it,
the `attachments` field arrives empty on other users' messages.

Deployment prerequisite: enable "Message Content Intent" in the Discord
developer portal for **both** the dev and prod bot applications before
deploying, or the gateway connection is rejected.

README update: attachment *metadata* (filename + size) is read in memory to
fingerprint messages; message text is still never read, stored, or logged.

## 2. Detection — new file `internal/bot/spam.go`

Pure decision logic in the `rules.go` style, wired from `onMessageCreate`
for messages that are not honeypot triggers.

- **Eligibility:** ordinary message/reply (reuse `isTriggerMessage`), not in
  the guild's honeypot channel, ≥ 2 attachments, guild config exists,
  action ≠ disabled, spam detection enabled.
- **Fingerprint:** sort attachments by (filename, size), concatenate,
  FNV-64 hash → `uint64`.
- **Rolling cache:** new `Bot` field
  `spamSightings *cache.TTL[spamKey, channelSet]` where
  `spamKey = {GuildID, UserID, Fingerprint uint64}` and the value is the
  set of channel IDs the fingerprint was seen in. TTL 30 minutes, sliding.
- **Cache extension:** `internal/cache` gains one method,
  `Update(k, ttl, fn)`, which applies `fn` to the current value (or zero
  value if absent/expired) under the cache lock — the cache stays the
  single point of synchronization; no per-key locks in the detector.
- **Trigger:** after adding the current channel, the set holds ≥ 2 distinct
  channels → punish. Same channel twice never triggers.

Constants (package-level in `spam.go`): window 30 min, min attachments 2,
distinct-channel threshold 2.

## 3. Punishment — reuse existing machinery

- Same 30-second `dedup` guard keyed (guild, user), so the 3rd/4th blast
  message doesn't re-ban.
- Same `gatherExemptionInputs` / `isExempt`, but exempt users are **skipped
  silently** — no DM, no log entry (unlike honeypot posts, which are
  deliberate acts worth notifying about).
- Non-exempt → `decideModeration` → `b.moderate(...)`, parameterized with:
  - audit-log reason: "Joe's Honeypot: posted duplicate images across
    multiple channels"
  - distinct log-channel message wording so mods can tell which detector
    fired.
- Ban/softban's existing 1-hour message deletion wipes the blast itself.
- `RecordEvent` is called as today, feeding the warning-message ban counter.

## 4. Config & storage

- Migration `0002`:
  `ALTER TABLE honeypot_config ADD COLUMN spam_detection INTEGER NOT NULL DEFAULT 1`
  — on by default, including existing guilds.
- `store.Config` gains `SpamDetection bool`; `GetConfig` / `UpsertConfig` /
  `SaveGuildSetup` updated.
- Config modal gains a fourth field: string select "Cross-channel image
  spam" (Enabled / Disabled), defaulting to the stored value.
- No new permission validation: the action validation already requires
  Ban Members for softban/ban.

## 5. Error handling & shutdown

Nothing new. `moderate` already owns retries, failure alerts, and
log-channel fallback; the detector inherits all of it. The sightings cache
is process-local; a restart forgets at most 30 minutes of counting state,
which is harmless because spam blasts land within seconds.

## 6. Testing

Table-driven tests in the existing style:

- Fingerprint: deterministic, attachment-order-independent, sensitive to
  filename and size changes.
- Eligibility: attachment count, bot/system authors, disabled action,
  toggle off, honeypot-channel messages excluded.
- Trigger: same channel twice does not trigger; two distinct channels does;
  expiry resets the count.
- `cache.Update`: creates absent entries, mutates live ones, treats expired
  entries as absent, refreshes TTL.

Handler wiring stays thin; no integration tests, matching the codebase.

## Out of scope

- Text-only / link spam detection.
- Downloading or hashing attachment bytes.
- Per-guild tuning of window or thresholds.
- Any change to honeypot-channel behavior.
