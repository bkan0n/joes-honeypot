package bot

import (
	"errors"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

func (b *Bot) onMessageCreate(e *events.MessageCreate) {
	if e.GuildID == nil {
		return
	}
	guildID := *e.GuildID
	msg := e.Message
	if !IsTriggerMessage(msg.Author.Bot || msg.Author.System, msg.Type) {
		return
	}
	hpChannel, err := b.Store.GetChannelByID(e.ChannelID)
	if err != nil || hpChannel == nil || hpChannel.GuildID != guildID {
		b.handleMentionRefresh(e)
		return
	}
	cfg, err := b.Store.GetConfig(guildID)
	if err != nil || cfg == nil {
		return
	}

	key := dedupKey{GuildID: guildID, UserID: msg.Author.ID}
	if !b.Dedup.SetIfAbsent(key, struct{}{}, 30*time.Second) {
		return
	}
	defer b.Dedup.Delete(key) // allow re-punishing a rejoining user

	// Best-effort honey react.
	go func() {
		if err := b.Client.Rest.AddReaction(e.ChannelID, msg.ID, "🍯"); err != nil {
			b.Log.Debug("adding reaction", "err", err)
		}
	}()

	if cfg.Action == store.ActionDisabled {
		return
	}

	guildName := "this server"
	var ownerID snowflake.ID
	adminRoles := map[snowflake.ID]struct{}{}
	if guild, ok := b.Client.Caches.Guild(guildID); ok {
		guildName = guild.Name
		ownerID = guild.OwnerID
	}
	for role := range b.Client.Caches.Roles(guildID) {
		if !role.Managed && role.Permissions.Has(discord.PermissionAdministrator) {
			adminRoles[role.ID] = struct{}{}
		}
	}
	var memberRoles []snowflake.ID
	if msg.Member != nil {
		memberRoles = msg.Member.RoleIDs
	}

	if IsExempt(msg.Author.ID, ownerID, memberRoles, adminRoles) {
		go func() {
			if err := b.dmUser(msg.Author.ID, ExemptDMMessage(guildName)); err != nil {
				b.Log.Debug("exempt dm failed", "user", msg.Author.ID, "err", err)
			}
		}()
		b.sendLog(cfg, e.ChannelID, discord.MessageCreate{Content: ExemptLogMessage(msg.Author.ID)})
		return
	}

	// DM before the ban so Discord still delivers it — but never delay the
	// action more than 2s.
	dmDone := make(chan struct{})
	go func() {
		defer close(dmDone)
		if err := b.dmUser(msg.Author.ID, DMMessage(cfg.Action, guildName)); err != nil {
			b.Log.Debug("dm failed", "user", msg.Author.ID, "err", err)
		}
	}()
	select {
	case <-dmDone:
	case <-time.After(2 * time.Second):
	}

	reason := rest.WithReason("Joe's Honeypot: posted in the honeypot channel")
	if err := b.Client.Rest.AddBan(guildID, msg.Author.ID, time.Hour, reason); err != nil {
		b.Log.Error("ban failed", "guild", guildID, "user", msg.Author.ID, "err", err)
		b.sendLog(cfg, e.ChannelID, discord.MessageCreate{Content: fmt.Sprintf(
			"⚠️ Failed to %s <@%d> — check that I have the **Ban Members** permission and that my role is above theirs.",
			cfg.Action, msg.Author.ID)})
		return
	}
	if cfg.Action == store.ActionSoftban {
		time.Sleep(250 * time.Millisecond)
		if err := b.Client.Rest.DeleteBan(guildID, msg.Author.ID, reason); err != nil {
			// An unknown-ban error means someone beat us to it — fine. Anything
			// else leaves the user banned instead of softbanned; tell the mods.
			var restErr *rest.Error
			isUnknownBan := errors.As(err, &restErr) && restErr.Code == rest.JSONErrorCodeUnknownBan
			if !isUnknownBan {
				b.Log.Error("unban after softban failed", "user", msg.Author.ID, "err", err)
				b.sendLog(cfg, e.ChannelID, discord.MessageCreate{Content: fmt.Sprintf(
					"⚠️ <@%d> was banned but the softban's unban failed — they are still banned.", msg.Author.ID)})
			}
		}
	}

	if err := b.Store.RecordEvent(guildID, msg.Author.ID, e.ChannelID); err != nil {
		b.Log.Error("recording event", "err", err)
	}

	logMsg := discord.MessageCreate{Content: LogMessage(msg.Author.ID, cfg.Action)}
	if cfg.Action == store.ActionBan {
		logMsg.Components = []discord.LayoutComponent{
			discord.NewActionRow(
				discord.NewDangerButton("Unban", fmt.Sprintf("unban:%d", msg.Author.ID)),
			),
		}
	}
	b.sendLog(cfg, e.ChannelID, logMsg)
	b.ensureWarningMessage(guildID, e.ChannelID)
	b.Log.Info("moderated", "guild", guildID, "user", msg.Author.ID, "action", cfg.Action)
}

// sendLog posts to the configured log channel; it only unsets the log
// channel when the failure is a permanent channel problem (deleted /
// inaccessible / no permissions). Any other error is logged and the log
// channel is left configured — this message still falls back to the
// honeypot channel.
func (b *Bot) sendLog(cfg *store.Config, fallbackChannelID snowflake.ID, msg discord.MessageCreate) {
	if cfg.LogChannelID != nil {
		if _, err := b.Client.Rest.CreateMessage(*cfg.LogChannelID, msg); err == nil {
			return
		} else if isPermanentChannelError(err) {
			b.Log.Warn("log channel unusable, unsetting", "channel", *cfg.LogChannelID, "err", err)
			if dbErr := b.Store.UnsetLogChannel(cfg.GuildID); dbErr != nil {
				b.Log.Error("unsetting log channel", "err", dbErr)
			}
		} else {
			b.Log.Warn("log channel send failed, leaving it configured", "channel", *cfg.LogChannelID, "err", err)
		}
	}
	if _, err := b.Client.Rest.CreateMessage(fallbackChannelID, msg); err != nil {
		b.Log.Debug("fallback log message failed", "err", err)
	}
}

// isPermanentChannelError reports whether err indicates the log channel is
// permanently unusable (deleted, or the bot lost access/permissions) as
// opposed to a transient failure (rate limit, timeout, Discord outage, ...).
func isPermanentChannelError(err error) bool {
	return rest.IsJSONErrorCode(err,
		rest.JSONErrorCodeUnknownChannel,
		rest.JSONErrorCodeMissingAccess,
		rest.JSONErrorCodeLackPermissionsToPerformAction,
	)
}

func (b *Bot) dmUser(userID snowflake.ID, content string) error {
	chID, ok := b.DMs.Get(userID)
	if !ok {
		ch, err := b.Client.Rest.CreateDMChannel(userID)
		if err != nil {
			return err
		}
		chID = ch.ID()
		b.DMs.Set(userID, chID, 24*time.Hour)
	}
	_, err := b.Client.Rest.CreateMessage(chID, discord.MessageCreate{Content: content})
	return err
}
