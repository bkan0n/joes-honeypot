# Joe's Honeypot

Discord honeypot bot in Go. Designate a honeypot channel; any account that
posts there is automatically softbanned/banned (spam bots blast every
channel — real users read the warning). Modeled on
[RiskyMH/honeypot](https://github.com/RiskyMH/honeypot), minus experiments.

## Local development

    cp .env.example .env.local   # fill in BOT_TOKEN
    go run ./cmd/bot             # with the vars exported

See docs/superpowers/specs/ for the design.
