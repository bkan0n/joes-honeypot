package bot

import (
	"math/rand"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

// onGuildJoin runs auto-setup the first time the bot joins a guild: it finds
// or creates an obfuscated "honeypot" text channel, records the default
// config, posts the persistent warning message, and posts a self-deleting
// intro message explaining the setup.
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

	channels, err := b.Client.Rest.GetGuildChannels(guildID)
	if err != nil {
		b.Log.Error("listing channels", "guild", guildID, "err", err)
		return
	}

	var honeypot discord.GuildChannel
	for _, ch := range channels {
		if ch.Type() == discord.ChannelTypeGuildText && Normalize(ch.Name()) == "honeypot" {
			honeypot = ch
			break
		}
	}
	if honeypot == nil {
		name := Obfuscate("honeypot", rand.New(rand.NewSource(time.Now().UnixNano())))
		created, err := b.Client.Rest.CreateGuildChannel(guildID, discord.GuildTextChannelCreate{
			Name:     name,
			Position: len(channels) + 1,
		})
		if err != nil {
			b.Log.Error("creating honeypot channel", "guild", guildID, "err", err)
			return
		}
		honeypot = created
	}
	honeypotID := honeypot.ID()

	if err := b.Store.UpsertConfig(store.Config{GuildID: guildID, Action: store.ActionSoftban}); err != nil {
		b.Log.Error("saving default config", "err", err)
		return
	}
	if err := b.Store.SetChannel(guildID, honeypotID); err != nil {
		b.Log.Error("saving honeypot channel", "err", err)
		if delErr := b.Store.DeleteGuild(guildID); delErr != nil {
			b.Log.Error("rolling back config after failed SetChannel", "guild", guildID, "err", delErr)
		}
		return
	}
	if !b.ensureWarningMessage(guildID, honeypotID) {
		b.Log.Warn("posting warning message during auto-setup", "guild", guildID, "channel", honeypotID)
	}

	missingBan := !b.botPermissionsInChannel(guildID, honeypot).Has(discord.PermissionBanMembers)
	intro, err := b.Client.Rest.CreateMessage(honeypotID, discord.MessageCreate{
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
		if err := b.Client.Rest.DeleteMessage(introChannelID, introID); err != nil {
			b.Log.Debug("intro already deleted", "err", err)
		}
	})
	b.Log.Info("auto-setup complete", "guild", guildID, "channel", honeypotID)
}
