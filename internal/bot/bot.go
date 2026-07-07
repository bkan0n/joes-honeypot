// Package bot implements Joe's Honeypot's Discord behavior.
package bot

import (
	"context"
	"log/slog"

	"github.com/disgoorg/disgo"
	dbot "github.com/disgoorg/disgo/bot"
	dcache "github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
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
	client *dbot.Client
	store  *store.Store
	log    *slog.Logger
	dedup  *cache.TTL[dedupKey, struct{}]
	dms    *cache.TTL[snowflake.ID, snowflake.ID]

	selfMembers *cache.TTL[snowflake.ID, discord.Member] // bot's own member per guild, for permission checks
	ownerID     snowflake.ID                             // bot application owner, allowed to use "@bot refresh"
}

func New(token string, st *store.Store, log *slog.Logger) (*Bot, error) {
	b := &Bot{
		store:       st,
		log:         log,
		dedup:       cache.NewTTL[dedupKey, struct{}](),
		dms:         cache.NewTTL[snowflake.ID, snowflake.ID](),
		selfMembers: cache.NewTTL[snowflake.ID, discord.Member](),
	}
	// Event listeners are appended here by the handler files (handler_*.go,
	// setup.go, housekeeping.go) as they are implemented.
	listeners := []dbot.ConfigOpt{
		dbot.WithEventListenerFunc(b.onCommand),
		dbot.WithEventListenerFunc(b.onModalSubmit),
		dbot.WithEventListenerFunc(b.onMessageCreate),
		dbot.WithEventListenerFunc(b.onComponent),
		dbot.WithEventListenerFunc(b.onGuildJoin),
		dbot.WithEventListenerFunc(b.onChannelDelete),
		dbot.WithEventListenerFunc(b.onThreadDelete),
		dbot.WithEventListenerFunc(b.onMessageDelete),
		dbot.WithEventListenerFunc(b.onGuildLeave),
	}
	opts := append([]dbot.ConfigOpt{
		// disgo defaults to slog.Default(); without this, gateway reconnects
		// and rate-limit hits would bypass the LOG_LEVEL-controlled logger.
		dbot.WithLogger(log),
		dbot.WithGatewayConfigOpts(
			gateway.WithIntents(gateway.IntentGuilds|gateway.IntentGuildMessages),
			gateway.WithPresenceOpts(gateway.WithWatchingActivity("#honeypot for bots")),
		),
		dbot.WithCacheConfigOpts(
			dcache.WithCaches(dcache.FlagGuilds | dcache.FlagRoles | dcache.FlagChannels),
		),
		dbot.WithEventManagerConfigOpts(dbot.WithAsyncEventsEnabled()),
	}, listeners...)
	client, err := disgo.New(token, opts...)
	if err != nil {
		return nil, err
	}
	b.client = client
	return b, nil
}

func (b *Bot) Start(ctx context.Context) error {
	if app, err := b.client.Rest.GetBotApplicationInfo(); err != nil {
		b.log.Warn("fetching application owner; @refresh command disabled", "err", err)
	} else if app.Owner != nil {
		b.ownerID = app.Owner.ID
	}
	if err := handler.SyncCommands(b.client, commands(), nil); err != nil {
		return err
	}
	return b.client.OpenGateway(ctx)
}

func (b *Bot) Close(ctx context.Context) {
	b.client.Close(ctx)
}
