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
	Client *dbot.Client
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
	listeners := []dbot.ConfigOpt{
		dbot.WithEventListenerFunc(b.onCommand),
		dbot.WithEventListenerFunc(b.onModalSubmit),
		dbot.WithEventListenerFunc(b.onMessageCreate),
	}
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
