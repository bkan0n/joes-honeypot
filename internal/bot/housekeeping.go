package bot

import (
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

func (b *Bot) onChannelDelete(e *events.GuildChannelDelete) {
	b.handleContainerDelete(e.GuildID, e.ChannelID, "channel")
}

// onThreadDelete mirrors onChannelDelete's housekeeping for threads: threads
// can be used as a log channel but do not dispatch GuildChannelDelete when
// removed, so they need their own listener.
func (b *Bot) onThreadDelete(e *events.ThreadDelete) {
	b.handleContainerDelete(e.GuildID, e.ThreadID, "thread")
}

// handleContainerDelete cleans up after a deleted channel or thread (kind is
// only for log wording): a honeypot channel is deregistered, a log channel
// is unset.
func (b *Bot) handleContainerDelete(guildID, containerID snowflake.ID, kind string) {
	ch, err := b.store.GetChannelByID(containerID)
	if err != nil {
		b.log.Error("checking deleted "+kind, "guild", guildID, "channel", containerID, "err", err)
		return
	}
	if ch != nil {
		if err := b.store.RemoveChannel(b.ctx, containerID); err != nil {
			b.log.Error("removing deleted honeypot "+kind, "guild", guildID, "channel", containerID, "err", err)
		}
		return
	}
	cfg, err := b.store.GetConfig(b.ctx, guildID)
	if err != nil {
		b.log.Error("loading config for deleted "+kind, "guild", guildID, "channel", containerID, "err", err)
		return
	}
	if cfg != nil && cfg.LogChannelID != nil && *cfg.LogChannelID == containerID {
		if err := b.store.UnsetLogChannel(b.ctx, guildID); err != nil {
			b.log.Error("unsetting deleted log "+kind, "guild", guildID, "channel", containerID, "err", err)
		}
	}
}

func (b *Bot) onMessageDelete(e *events.GuildMessageDelete) {
	if err := b.store.ClearWarningMsgByMsgID(b.ctx, e.MessageID); err != nil {
		b.log.Error("clearing warning msg id", "guild", e.GuildID, "msg", e.MessageID, "err", err)
	}
}

func (b *Bot) onGuildLeave(e *events.GuildLeave) {
	if err := b.store.DeleteGuild(b.ctx, e.Guild.ID); err != nil {
		b.log.Error("purging guild config", "guild", e.Guild.ID, "err", err)
	}
}
