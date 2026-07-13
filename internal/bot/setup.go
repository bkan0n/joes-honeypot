package bot

import (
	"math/rand"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

// onGuildJoin runs auto-setup the first time the bot joins a guild: it finds
// or creates an obfuscated "honeypot" text channel, records the default
// config, posts the persistent warning message, and posts a self-deleting
// intro message explaining the setup.
func (b *Bot) onGuildJoin(e *events.GuildJoin) {
	guildID := e.Guild.ID
	cfg, err := b.store.GetConfig(b.ctx, guildID)
	if err != nil {
		b.log.Error("checking existing config", "guild", guildID, "err", err)
		return
	}
	if cfg != nil {
		return // rejoined a guild we already know
	}

	channels, err := b.client.Rest.GetGuildChannels(guildID, rest.WithCtx(b.ctx))
	if err != nil {
		b.log.Error("listing channels", "guild", guildID, "err", err)
		return
	}

	var honeypot discord.GuildChannel
	for _, ch := range channels {
		if ch.Type() == discord.ChannelTypeGuildText && normalize(ch.Name()) == "honeypot" {
			honeypot = ch
			break
		}
	}
	if honeypot == nil {
		name := obfuscate("honeypot", rand.New(rand.NewSource(time.Now().UnixNano())))
		created, err := b.client.Rest.CreateGuildChannel(guildID, discord.GuildTextChannelCreate{
			Name:     name,
			Position: len(channels) + 1,
		}, rest.WithCtx(b.ctx))
		if err != nil {
			b.log.Error("creating honeypot channel", "guild", guildID, "err", err)
			return
		}
		honeypot = created
	}
	honeypotID := honeypot.ID()
	b.ensureEveryoneCanSeeChannel(guildID, honeypot)

	if err := b.store.SaveGuildSetup(b.ctx, store.Config{GuildID: guildID, Action: store.ActionSoftban, SpamDetection: true}, honeypotID); err != nil {
		b.log.Error("saving default guild setup", "guild", guildID, "channel", honeypotID, "err", err)
		return
	}
	if err := b.ensureWarningMessage(guildID, honeypotID); err != nil {
		b.log.Warn("posting warning message during auto-setup", "guild", guildID, "channel", honeypotID, "err", err)
	}

	missingBan := !b.botPermissionsInChannel(guildID, honeypot).Has(discord.PermissionBanMembers)
	intro, err := b.client.Rest.CreateMessage(honeypotID, discord.MessageCreate{
		Content: introMessage(missingBan),
		Components: []discord.LayoutComponent{
			discord.NewActionRow(discord.NewSecondaryButton("Delete message now", introDeleteCID)),
		},
	}, rest.WithCtx(b.ctx))
	if err != nil {
		b.log.Warn("posting intro message", "err", err)
		return
	}
	introChannelID, introID := intro.ChannelID, intro.ID
	// Not time.AfterFunc: the delayed delete must die with the bot instead of
	// firing against a closed client up to 150s after shutdown.
	b.safeGo(func() {
		select {
		case <-b.ctx.Done():
			return
		case <-time.After(150 * time.Second):
		}
		if err := b.client.Rest.DeleteMessage(introChannelID, introID, rest.WithCtx(b.ctx)); err != nil {
			b.log.Debug("intro already deleted", "err", err)
		}
	})
	b.log.Info("auto-setup complete", "guild", guildID, "channel", honeypotID)
}

// ensureEveryoneCanSeeChannel grants @everyone View Channel + Send Messages
// on the honeypot channel if either the channel's explicit @everyone
// overwrite or the guild-level default denies them — an invisible or
// unpostable honeypot catches nothing. This only handles the explicit-deny
// overwrite and guild-default-deny cases (not, e.g., category-level
// overwrites); it's a best-effort fix, not a full permission resolver.
// Failures are logged and otherwise ignored.
func (b *Bot) ensureEveryoneCanSeeChannel(guildID snowflake.ID, ch discord.GuildChannel) {
	everyone, ok := b.client.Caches.Role(guildID, guildID) // @everyone's role ID == guild ID
	if !ok {
		return
	}

	const needed = discord.PermissionViewChannel | discord.PermissionSendMessages
	overwrite, hasOverwrite := ch.PermissionOverwrites().Role(guildID)

	var mustFix bool
	switch {
	case hasOverwrite:
		mustFix = overwrite.Deny&needed != 0
	default:
		mustFix = everyone.Permissions&needed != needed
	}
	if !mustFix {
		return
	}

	botPerms := b.botPermissionsInChannel(guildID, ch)
	if !botPerms.Has(discord.PermissionManageRoles) && !botPerms.Has(discord.PermissionManageChannels) {
		b.log.Warn("cannot grant @everyone view/send: missing Manage Roles/Channels",
			"guild", guildID, "channel", ch.ID())
		return
	}

	allow := overwrite.Allow | needed
	deny := overwrite.Deny &^ needed
	if err := b.client.Rest.UpdatePermissionOverwrite(ch.ID(), guildID, discord.RolePermissionOverwriteUpdate{
		Allow: &allow,
		Deny:  &deny,
	}, rest.WithCtx(b.ctx)); err != nil {
		b.log.Warn("granting @everyone view/send on honeypot channel", "guild", guildID, "channel", ch.ID(), "err", err)
	}
}
