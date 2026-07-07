package bot

import (
	"math/rand"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

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
	b.ensureEveryoneCanSeeChannel(guildID, honeypot)

	if err := b.Store.SaveGuildSetup(store.Config{GuildID: guildID, Action: store.ActionSoftban}, honeypotID); err != nil {
		b.Log.Error("saving default guild setup", "guild", guildID, "channel", honeypotID, "err", err)
		return
	}
	if err := b.ensureWarningMessage(guildID, honeypotID); err != nil {
		b.Log.Warn("posting warning message during auto-setup", "guild", guildID, "channel", honeypotID, "err", err)
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

// ensureEveryoneCanSeeChannel grants @everyone View Channel + Send Messages
// on the honeypot channel if either the channel's explicit @everyone
// overwrite or the guild-level default denies them — an invisible or
// unpostable honeypot catches nothing. This only handles the explicit-deny
// overwrite and guild-default-deny cases (not, e.g., category-level
// overwrites); it's a best-effort fix, not a full permission resolver.
// Failures are logged and otherwise ignored.
func (b *Bot) ensureEveryoneCanSeeChannel(guildID snowflake.ID, ch discord.GuildChannel) {
	everyone, ok := b.Client.Caches.Role(guildID, guildID) // @everyone's role ID == guild ID
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
		b.Log.Warn("cannot grant @everyone view/send: missing Manage Roles/Channels",
			"guild", guildID, "channel", ch.ID())
		return
	}

	allow := overwrite.Allow | needed
	deny := overwrite.Deny &^ needed
	if err := b.Client.Rest.UpdatePermissionOverwrite(ch.ID(), guildID, discord.RolePermissionOverwriteUpdate{
		Allow: &allow,
		Deny:  &deny,
	}); err != nil {
		b.Log.Warn("granting @everyone view/send on honeypot channel", "guild", guildID, "channel", ch.ID(), "err", err)
	}
}
