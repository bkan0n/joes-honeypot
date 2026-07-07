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

CI builds and pushes a SHA-tagged image to GHCR
(`ghcr.io/bkan0n/joes-honeypot`); the server pulls and restarts over a
remote Docker context (SSH):

- **prod** — push to `main` → lint + tests + image build → `docker compose
  -f docker-compose.prod.yml pull && up -d` on the VPS.
- **dev** — comment `.deploy` on a PR (or run the workflow manually) →
  same against `docker-compose.dev.yml`.

**Rollback:** every deployed SHA stays in GHCR. Re-run the prod deploy
workflow from the last good commit (pull-only, no rebuild), or on the
server: `IMAGE_TAG=<old sha> docker compose -f docker-compose.prod.yml up -d`.

Required GitHub configuration:

| Where | Name |
|---|---|
| Environment `production` | `BOT_TOKEN`, `LITESTREAM_REPLICA_URL`, `LITESTREAM_ACCESS_KEY_ID`, `LITESTREAM_SECRET_ACCESS_KEY` |
| Environment `development` | `BOT_TOKEN` (dev bot application) |
| Repo secrets | `SERVER_HOST_SSH_PRIVATE_KEY`, `SERVER_HOST_IP`, `SERVER_HOST_USER`, `SERVER_HOST_KEY` |

`SERVER_HOST_KEY` is the server's `known_hosts` line — run
`ssh-keyscan <server-ip>` once from a trusted machine and paste the
result. Workflows pin it instead of re-scanning per run, which would
accept any host.

SQLite lives in the named volumes `joes_honeypot_{prod,dev}_data`.

**One-time migration for volumes created before the distroless image:**
named volumes keep the ownership they were created with (uid 1000 under
the old debian image), but the distroless runtime runs as uid 65532, so
the bot crash-loops with `attempt to write a readonly database`. Fix on
the server, per volume — note Compose prefixes the project name, so check
`docker volume ls` for the real name first:

    docker run --rm -v joes-honeypot_joes_honeypot_prod_data:/data busybox chown -R 65532:65532 /data

Volumes created from scratch inherit the right ownership automatically.

## Backups

A [Litestream](https://litestream.io) sidecar in `docker-compose.prod.yml`
continuously replicates the prod database to S3-compatible storage (e.g.
Cloudflare R2). `LITESTREAM_REPLICA_URL` is the bucket URL
(`s3://<bucket>.<account>.r2.cloudflarestorage.com/honeypot.db`); the two
key secrets are an access token scoped to that bucket. To recover after
volume loss, run `litestream restore -o /data/honeypot.db <replica-url>`
into a fresh volume before starting the bot.
