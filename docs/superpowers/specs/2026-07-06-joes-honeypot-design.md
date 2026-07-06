# Joe's Honeypot — Design

**Date:** 2026-07-06
**Status:** Approved pending user review

A Discord honeypot bot in Go, modeled on [RiskyMH/honeypot](https://github.com/RiskyMH/honeypot) (TypeScript/Bun), minus its "experiments" feature set. Runs in a handful of servers. SQLite for storage, docker compose for deployment, GitHub Actions CI/CD matching the user's genjishimada pattern.

## Core concept

Each guild designates one honeypot channel. Any message posted there by a non-bot, non-system account triggers moderation of the author (softban by default). Legitimate users are warned off by a persistent warning message; spam bots that blast every channel get caught. The bot never reads message content — posting at all is the trigger — so only the `Guilds | GuildMessages` gateway intents are needed (no message-content intent).

## Architecture (Approach A — approved)

- Single Go binary per environment. Gateway connection via **disgo v0.19.6+** (v0.19.0 is the floor: it introduced Label components / selects in modals).
- **SQLite via `modernc.org/sqlite`** (pure Go, CGO-free static builds). Plain `database/sql`, hand-written queries.
- **No Redis.** Everything the original used Redis for becomes in-memory TTL maps: moderation dedup (30s), DM-channel cache (24h). State loss on restart is acceptable.
- Prod and dev are two separate Discord applications/tokens, two containers, two SQLite volumes, side by side on the same VPS.

Rejected: Approach B (Redis + stats API containers — solves multi-process problems this bot doesn't have); Approach C (HTTP interactions endpoint — can't receive MESSAGE_CREATE).

## Commands

Registered via bulk overwrite of global commands at startup.

### `/honeypot`

Guild-only. `default_member_permissions = ManageGuild | BanMembers | ModerateMembers | ManageMessages | ManageChannels`. Opens a modal (faithful to the original's UX):

| Field | Component | Constraints |
|---|---|---|
| Honeypot channel | ChannelSelect | required, exactly 1, types: GuildText, GuildVoice |
| Log channel | ChannelSelect | optional, 0–1, types: GuildText, PublicThread, PrivateThread |
| Action | RadioGroup | `softban` (default) / `ban` / `disabled` |

**On submit, validate everything before saving; on any failure save nothing** and reply with an ephemeral error listing what's missing:

- Bot needs View + Send in the honeypot channel.
- User and bot both need Ban Members when action is `softban` or `ban`.
- Bot needs View + Send in the log channel (if set).

On success: save config, ensure the warning message exists in the (possibly new) honeypot channel, delete the warning message from a previously-configured channel if the channel changed.

### Dropped commands

- `/honeypot-messages` — no custom message templates; fixed default texts live in `internal/bot/templates.go`.
- `/stats` (bot-DM context) — the counter button's ephemeral per-server stats cover it.

## Trigger pipeline (MESSAGE_CREATE)

Bail out fast: not a configured honeypot channel → return; author is a bot or the message is a system type → return.

1. **Dedup guard:** in-memory `(guild_id, user_id)` key with 30s TTL; prevents double-moderation; cleared after the action completes so a rejoining user can be re-punished.
2. **React 🍯** to the triggering message (best-effort, 1s timeout).
3. **Exemptions:** the server owner, and members holding any non-managed role with Administrator, are not actioned. They receive an "example" DM showing what would have happened, and a ⚠️ notice goes to the log channel.
4. **DM first** (before the ban, so Discord delivers it), capped at 2 seconds so the action is never delayed: "Honeypot Triggered — you have been banned/kicked from **{server}** for sending a message in the honeypot channel." Footer marks it as automated. DM channel IDs cached 24h.
5. **Action:**
   - `softban` — ban with `delete_message_seconds: 3600`, sleep 250ms, unban. Unknown-ban on unban is tolerated; other unban failures produce a "failed to fully softban" ⚠️ log message.
   - `ban` — same ban, no unban.
   - `disabled` — react only, no action, no event recorded.
6. **On success:** insert a row into `honeypot_events`; post the log message ("`@user` was banned/kicked for sending a message in the honeypot channel") — ban-action logs carry an **Unban** button; edit the warning message to update its counter button.
7. **Failure paths:** missing-permission and generic-failure ⚠️ messages go to the log channel, falling back to the honeypot channel itself. If the log channel has been deleted or is inaccessible, auto-unset it in the DB.

## Buttons

| Custom ID | Behavior |
|---|---|
| `moderated_count:<guild>` | On the warning message. Ephemeral reply: total users moderated in this server (from `honeypot_events`). |
| `unban:<user_id>` | On ban log messages. Requires Ban Members on both the clicker and the bot. Expires 24h after the log message timestamp. Unbans and announces who clicked. |
| `delete_intro` | On the intro message. Requires Manage Messages. Deletes the intro immediately. |

## Guild join auto-setup (GUILD_CREATE, no existing config)

1. Find an existing text channel whose **lookalike-normalized** name equals "honeypot" (normalization maps Cyrillic/Greek confusables to ASCII, ported from the original's `lookalike-chars.yaml`).
2. Else create a channel named "honeypot" with ~30% of characters replaced by lookalikes (dodges bots that blacklist the literal string), positioned at the bottom. If @everyone lacks View/Send but the bot has them, add an overwrite granting @everyone View + Send.
3. Post the persistent **warning message** ("DO NOT SEND MESSAGES IN THIS CHANNEL — you will be banned") with the counter button. De-duplicate: if the bot already has messages there, edit the first and delete the rest.
4. Post a self-deleting (2.5 min) **intro message** with setup tips and a "Delete message now" button; append a ⚠️ if the bot lacks Ban Members.
5. Default config: `action = softban`, no log channel.

Dropped from the original: the 25-second anti-nuke membership re-check and the ≥65%-role heuristic (protections for a public bot in hostile servers).

## Housekeeping handlers

- `CHANNEL_DELETE` / `THREAD_DELETE`: if it was the honeypot channel, remove the row; if the log channel, null it out.
- `MESSAGE_DELETE` / `MESSAGE_DELETE_BULK`: if a stored warning `msg_id` was deleted, null it (recreated on next trigger or reconfigure).
- `GUILD_DELETE` (removed, not outage): purge the guild's config (cascades).
- Presence: "Watching #honeypot for bots".

## Data model

SQLite pragmas: WAL, `foreign_keys = ON`, `busy_timeout = 5000`, `synchronous = NORMAL`.

```sql
CREATE TABLE honeypot_config (
    guild_id       INTEGER PRIMARY KEY,
    log_channel_id INTEGER,
    action         TEXT NOT NULL DEFAULT 'softban'  -- 'softban' | 'ban' | 'disabled'
);

CREATE TABLE honeypot_channels (
    channel_id INTEGER PRIMARY KEY,
    guild_id   INTEGER NOT NULL REFERENCES honeypot_config(guild_id) ON DELETE CASCADE,
    msg_id     INTEGER  -- warning message id, nullable
);

CREATE TABLE honeypot_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    guild_id   INTEGER NOT NULL REFERENCES honeypot_config(guild_id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL,
    channel_id INTEGER REFERENCES honeypot_channels(channel_id) ON DELETE SET NULL,
    timestamp  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE _migrations (version INTEGER PRIMARY KEY, name TEXT, executed_at DATETIME);

CREATE INDEX idx_channels_guild ON honeypot_channels(guild_id);
CREATE INDEX idx_events_guild   ON honeypot_events(guild_id);
CREATE INDEX idx_events_user    ON honeypot_events(user_id);
```

`honeypot_channels` stays a separate table (mirrors the original; leaves room for multiple channels later) even though v1 enforces exactly one per guild. The original's `experiments` column, `honeypot_messages`, and `honeypot_reinvite` tables are dropped.

Migrations: embedded `.sql` files via `embed.FS`, applied at startup against `_migrations`. No external tool.

## Code layout

```
cmd/bot/main.go          wiring: env config, DB open + migrate, disgo client, graceful shutdown
internal/store/          database/sql + modernc.org/sqlite; typed methods
                         (GetConfig, UpsertConfig, SetChannel, RecordEvent, CountEvents, ...)
internal/bot/
  commands.go            command definitions + bulk overwrite on startup
  handler_honeypot.go    /honeypot → modal → validate → save
  handler_message.go     MESSAGE_CREATE trigger pipeline
  handler_buttons.go     counter stats, unban, intro-delete buttons
  setup.go               guild-join auto-setup
  housekeeping.go        channel/message/guild delete handlers
  lookalike.go           lookalike normalization + obfuscation table
  templates.go           fixed message texts (warning, DM, log, intro)
internal/cache/          generic TTL map (dedup guard, DM-channel ids)
```

Env config: `BOT_TOKEN` (required), `DB_PATH` (default `/data/honeypot.db`), `LOG_LEVEL` (default `info`).

## Testing

- `internal/store`: real in-memory SQLite (`:memory:`), covering migrations and every query.
- `lookalike`: normalization round-trips, obfuscation output normalizes back to "honeypot".
- `templates`: rendered texts contain expected mentions/names.
- Pipeline decision logic (exempt? dedup? which action?) extracted into pure functions behind small interfaces and unit-tested; the disgo event glue stays thin and untested.
- CI runs `go test ./...` and golangci-lint as reusable workflows gating deploys.

## Docker

Single two-stage `Dockerfile`:

- **Builder:** `golang:1.24-bookworm`, `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"` → static binary.
- **Runtime:** `debian:bookworm-slim` + ca-certificates, non-root user, `/data` owned by that user. No healthcheck (no port to probe — same as the genjishimada bot service); `restart: unless-stopped` covers crashes.

## Compose

`docker-compose.prod.yml` / `docker-compose.dev.yml`, identical except `-dev` suffixes:

```yaml
services:
  joes-honeypot:                     # joes-honeypot-dev in dev file
    build: { context: ., dockerfile: Dockerfile }
    container_name: joes-honeypot
    environment:
      BOT_TOKEN: ${BOT_TOKEN}
      DB_PATH: /data/honeypot.db
    volumes:
      - joes_honeypot_prod_data:/data   # ..._dev_data in dev file
    restart: unless-stopped

volumes:
  joes_honeypot_prod_data:
```

No external/reverse-proxy network — the bot has no ingress. Local development uses `go run ./cmd/bot` with a gitignored `.env.local` (committed `.env.example` documents the variables); no local compose file.

## CI/CD (GitHub Actions, genjishimada mechanics)

- **`lint.yml`** (reusable, `workflow_call` with `ref` input): golangci-lint.
- **`tests.yml`** (reusable): `go test ./...`.
- **`deploy-prod.yml`**: triggers `push: main` (paths-ignore docs) + `workflow_dispatch`; `needs: [lint, tests]`; `environment: production`; steps: write SSH key → `ssh-keyscan` known_hosts → `docker context create prod-server --docker host=ssh://user@ip` → `docker compose -f docker-compose.prod.yml up -d --build --force-recreate` with `BOT_TOKEN` from environment secrets.
- **`deploy-dev.yml`**: `workflow_dispatch` (ref input) + `.deploy` PR comment via `github/branch-deploy@v11`; `environment: development`; same SSH-context deploy against the dev compose file; GitHub Deployment status updates as in genjishimada.
- **Secrets:** per-environment `BOT_TOKEN`; repo-level `SERVER_HOST_SSH_PRIVATE_KEY`, `SERVER_HOST_IP`, `SERVER_HOST_USER`. No `.env` files on the server.
- Dropped from genjishimada: Sentry release steps.

## Explicitly out of scope

All twelve of the original's experiments (`no-warning-msg`, `no-dm`, `random-channel-name`, `random-channel-name-chaos`, `channel-warmer`, `recreate-channel`, `forward-message`, `reinvite`, `timeout-first`, `only-recent-delete`, `many-honeypots`, `ensure-msg-delete`), custom message templates, Redis, sharding, the stats API/website, cross-server ban sharing, and appeal mechanisms.
