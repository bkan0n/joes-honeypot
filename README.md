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
