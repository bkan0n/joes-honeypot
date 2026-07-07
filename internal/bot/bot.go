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
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/cache"
	"github.com/bkan0n/joeshoneypot/internal/store"
)

type dedupKey struct {
	GuildID snowflake.ID
	UserID  snowflake.ID
}

// Bot wires the Discord client, the store, and the in-memory caches together.
// One instance serves every guild; create it with New, connect it with Start,
// and shut it down with Close.
type Bot struct {
	client *dbot.Client
	store  *store.Store
	log    *slog.Logger
	dedup  *cache.TTL[dedupKey, struct{}]
	dms    *cache.TTL[snowflake.ID, snowflake.ID]

	selfMembers *cache.TTL[snowflake.ID, discord.Member] // bot's own member per guild, for permission checks
	ownerID     snowflake.ID                             // bot application owner, allowed to use "@bot refresh"

	// ctx spans the bot's lifetime; Close cancels it, aborting in-flight
	// REST and DB work in handler goroutines so shutdown stays bounded.
	ctx    context.Context
	cancel context.CancelFunc
}

func New(token string, st *store.Store, log *slog.Logger) (*Bot, error) {
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bot{
		store:       st,
		log:         log,
		dedup:       cache.NewTTL[dedupKey, struct{}](),
		dms:         cache.NewTTL[snowflake.ID, snowflake.ID](),
		selfMembers: cache.NewTTL[snowflake.ID, discord.Member](),
		ctx:         ctx,
		cancel:      cancel,
	}
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
		cancel()
		return nil, err
	}
	b.client = client
	return b, nil
}

// Start resolves the application owner, syncs the slash commands, and opens
// the gateway connection. ctx bounds only the startup calls, not the bot's
// lifetime (that's Close).
func (b *Bot) Start(ctx context.Context) error {
	if app, err := b.client.Rest.GetBotApplicationInfo(rest.WithCtx(ctx)); err != nil {
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
	b.cancel() // abort in-flight handler work before closing the client
	b.client.Close(ctx)
}
