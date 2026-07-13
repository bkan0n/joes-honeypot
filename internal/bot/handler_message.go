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
	if !isTriggerMessage(msg.Author.Bot || msg.Author.System, msg.Type) {
		return
	}
	hpChannel, err := b.store.GetChannelByID(e.ChannelID)
	if err != nil {
		b.log.Error("loading honeypot channel", "guild", guildID, "channel", e.ChannelID, "err", err)
		return
	}
	if hpChannel == nil || hpChannel.GuildID != guildID {
		b.handleMentionRefresh(e)
		b.checkSpam(e, guildID)
		return
	}
	cfg, err := b.store.GetConfig(b.ctx, guildID)
	if err != nil {
		b.log.Error("loading config", "guild", guildID, "err", err)
		return
	}
	if cfg == nil {
		return
	}

	key := dedupKey{GuildID: guildID, UserID: msg.Author.ID}
	if !b.dedup.SetIfAbsent(key, struct{}{}, 30*time.Second) {
		return
	}
	defer b.dedup.Delete(key) // allow re-punishing a rejoining user

	// Best-effort honey react.
	b.safeGo(func() {
		if err := b.client.Rest.AddReaction(e.ChannelID, msg.ID, "🍯", rest.WithCtx(b.ctx)); err != nil {
			b.log.Debug("adding reaction", "channel", e.ChannelID, "err", err)
		}
	})

	inputs := b.gatherExemptionInputs(guildID, msg)
	exempt := isExempt(msg.Author.ID, inputs.OwnerID, inputs.MemberRoles, func(roleID snowflake.ID) bool {
		role, ok := b.client.Caches.Role(guildID, roleID)
		return ok && isAdminRole(role)
	})
	b.moderate(decideModeration(cfg.Action, exempt), cfg, e.ChannelID, msg, inputs.GuildName, triggerHoneypot)
}

// exemptionInputs carries everything isExempt needs about a message's author,
// plus the guild name used in the DM templates — all sourced from the gateway
// caches and the message itself, no REST calls.
type exemptionInputs struct {
	GuildName   string
	OwnerID     snowflake.ID
	MemberRoles []snowflake.ID
}

func (b *Bot) gatherExemptionInputs(guildID snowflake.ID, msg discord.Message) exemptionInputs {
	in := exemptionInputs{GuildName: "this server"}
	if guild, ok := b.client.Caches.Guild(guildID); ok {
		in.GuildName = guild.Name
		in.OwnerID = guild.OwnerID
	}
	if msg.Member != nil {
		in.MemberRoles = msg.Member.RoleIDs
	}
	return in
}

// moderate executes a moderationPlan against Discord: the DM-before-ban
// dance, the ban/unban REST calls with their failure alerts, event recording,
// and the log + warning-message refresh.
func (b *Bot) moderate(plan moderationPlan, cfg *store.Config, channelID snowflake.ID, msg discord.Message, guildName string, kind triggerKind) {
	guildID := cfg.GuildID
	if plan.NotifyExempt {
		b.safeGo(func() {
			if err := b.dmUser(msg.Author.ID, exemptDMMessage(guildName)); err != nil {
				b.log.Debug("exempt dm failed", "user", msg.Author.ID, "err", err)
			}
		})
		b.sendLog(cfg, discord.MessageCreate{Content: exemptLogMessage(msg.Author.ID)})
		return
	}
	if !plan.Ban {
		return
	}

	if plan.DM {
		// DM before the ban so Discord still delivers it — but never delay
		// the action more than 2s.
		dmDone := make(chan struct{})
		b.safeGo(func() {
			defer close(dmDone)
			if err := b.dmUser(msg.Author.ID, dmMessage(cfg.Action, guildName, kind)); err != nil {
				b.log.Debug("dm failed", "user", msg.Author.ID, "err", err)
			}
		})
		select {
		case <-dmDone:
		case <-time.After(2 * time.Second):
		}
	}

	reason := rest.WithReason(kind.banReason())
	if err := b.retryTransient("ban", banRetryAttempts, banRetryBackoff, func() error {
		return b.client.Rest.AddBan(guildID, msg.Author.ID, time.Hour, reason, rest.WithCtx(b.ctx))
	}); err != nil {
		b.log.Error("ban failed", "guild", guildID, "user", msg.Author.ID, "err", err)
		b.sendAlert(cfg, channelID, discord.MessageCreate{Content: fmt.Sprintf(
			"⚠️ Failed to %s <@%d> — check that I have the **Ban Members** permission and that my role is above theirs.",
			cfg.Action, msg.Author.ID)})
		return
	}
	if plan.Unban {
		time.Sleep(250 * time.Millisecond)
		if err := b.retryTransient("unban after softban", banRetryAttempts, banRetryBackoff, func() error {
			return b.client.Rest.DeleteBan(guildID, msg.Author.ID, reason, rest.WithCtx(b.ctx))
		}); err != nil {
			// An unknown-ban error means someone beat us to it — fine. Anything
			// else leaves the user banned instead of softbanned; tell the mods.
			var restErr *rest.Error
			isUnknownBan := errors.As(err, &restErr) && restErr.Code == rest.JSONErrorCodeUnknownBan
			if !isUnknownBan {
				b.log.Error("unban after softban failed", "user", msg.Author.ID, "err", err)
				b.sendAlert(cfg, channelID, discord.MessageCreate{Content: fmt.Sprintf(
					"⚠️ <@%d> was banned but the softban's unban failed — they are still banned.", msg.Author.ID)})
			}
		}
	}

	var eventChannel *snowflake.ID
	if kind == triggerHoneypot {
		eventChannel = &channelID
	}
	if err := b.store.RecordEvent(b.ctx, guildID, msg.Author.ID, eventChannel); err != nil {
		b.log.Error("recording event", "guild", guildID, "user", msg.Author.ID, "err", err)
	}

	logMsg := discord.MessageCreate{Content: logMessage(msg.Author.ID, cfg.Action, kind)}
	if plan.UnbanButton {
		logMsg.Components = []discord.LayoutComponent{
			discord.NewActionRow(
				discord.NewDangerButton("Unban", fmt.Sprintf("unban:%d", msg.Author.ID)),
			),
		}
	}
	b.sendLog(cfg, logMsg)
	if hp, err := b.store.GetChannel(guildID); err != nil {
		b.log.Error("loading honeypot channel for warning refresh", "guild", guildID, "err", err)
	} else if hp != nil {
		if err := b.ensureWarningMessage(guildID, hp.ChannelID); err != nil {
			b.log.Warn("refreshing warning message after moderation", "guild", guildID, "channel", hp.ChannelID, "err", err)
		}
	}
	b.log.Info("moderated", "guild", guildID, "user", msg.Author.ID, "action", cfg.Action)
}

// sendLog posts a routine log message (who got actioned) to the configured
// log channel and reports whether it was delivered; if no log channel is set,
// the message is dropped — routine logs never land in the honeypot channel.
// The log channel is only unset when the failure is a permanent channel
// problem (deleted / inaccessible / no permissions); any other error is
// logged and the log channel is left configured.
func (b *Bot) sendLog(cfg *store.Config, msg discord.MessageCreate) bool {
	if cfg.LogChannelID == nil {
		return false
	}
	if _, err := b.client.Rest.CreateMessage(*cfg.LogChannelID, msg, rest.WithCtx(b.ctx)); err == nil {
		return true
	} else if isPermanentChannelError(err) {
		b.log.Warn("log channel unusable, unsetting", "channel", *cfg.LogChannelID, "err", err)
		if dbErr := b.store.UnsetLogChannel(b.ctx, cfg.GuildID); dbErr != nil {
			b.log.Error("unsetting log channel", "err", dbErr)
		}
	} else {
		b.log.Warn("log channel send failed, leaving it configured", "channel", *cfg.LogChannelID, "err", err)
	}
	return false
}

// sendAlert posts an operational warning (failed or incomplete moderation)
// that must stay visible to moderators: it goes to the log channel when
// possible, and otherwise falls back to the honeypot channel — unlike
// routine logs, these are never silently dropped.
func (b *Bot) sendAlert(cfg *store.Config, fallbackChannelID snowflake.ID, msg discord.MessageCreate) {
	if b.sendLog(cfg, msg) {
		return
	}
	if _, err := b.client.Rest.CreateMessage(fallbackChannelID, msg, rest.WithCtx(b.ctx)); err != nil {
		b.log.Error("alert dropped: fallback channel send failed", "guild", cfg.GuildID, "channel", fallbackChannelID, "err", err)
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
	chID, ok := b.dms.Get(userID)
	if !ok {
		ch, err := b.client.Rest.CreateDMChannel(userID, rest.WithCtx(b.ctx))
		if err != nil {
			return err
		}
		chID = ch.ID()
		b.dms.Set(userID, chID, 24*time.Hour)
	}
	_, err := b.client.Rest.CreateMessage(chID, discord.MessageCreate{Content: content}, rest.WithCtx(b.ctx))
	return err
}
