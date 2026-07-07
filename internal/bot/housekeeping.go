package bot

import (
	"github.com/disgoorg/disgo/events"
)

func (b *Bot) onChannelDelete(e *events.GuildChannelDelete) {
	ch, err := b.Store.GetChannelByID(e.ChannelID)
	if err != nil {
		b.Log.Error("checking deleted channel", "guild", e.GuildID, "channel", e.ChannelID, "err", err)
		return
	}
	if ch != nil {
		if err := b.Store.RemoveChannel(e.ChannelID); err != nil {
			b.Log.Error("removing deleted honeypot channel", "guild", e.GuildID, "channel", e.ChannelID, "err", err)
		}
		return
	}
	cfg, err := b.Store.GetConfig(e.GuildID)
	if err != nil {
		b.Log.Error("loading config for deleted channel", "guild", e.GuildID, "channel", e.ChannelID, "err", err)
		return
	}
	if cfg != nil && cfg.LogChannelID != nil && *cfg.LogChannelID == e.ChannelID {
		if err := b.Store.UnsetLogChannel(e.GuildID); err != nil {
			b.Log.Error("unsetting deleted log channel", "guild", e.GuildID, "channel", e.ChannelID, "err", err)
		}
	}
}

// onThreadDelete mirrors onChannelDelete's housekeeping for threads: threads
// can be used as a log channel but do not dispatch GuildChannelDelete when
// removed, so they need their own listener.
func (b *Bot) onThreadDelete(e *events.ThreadDelete) {
	ch, err := b.Store.GetChannelByID(e.ThreadID)
	if err != nil {
		b.Log.Error("checking deleted thread", "guild", e.GuildID, "channel", e.ThreadID, "err", err)
		return
	}
	if ch != nil {
		if err := b.Store.RemoveChannel(e.ThreadID); err != nil {
			b.Log.Error("removing deleted honeypot thread", "guild", e.GuildID, "channel", e.ThreadID, "err", err)
		}
		return
	}
	cfg, err := b.Store.GetConfig(e.GuildID)
	if err != nil {
		b.Log.Error("loading config for deleted thread", "guild", e.GuildID, "channel", e.ThreadID, "err", err)
		return
	}
	if cfg != nil && cfg.LogChannelID != nil && *cfg.LogChannelID == e.ThreadID {
		if err := b.Store.UnsetLogChannel(e.GuildID); err != nil {
			b.Log.Error("unsetting deleted log channel thread", "guild", e.GuildID, "channel", e.ThreadID, "err", err)
		}
	}
}

func (b *Bot) onMessageDelete(e *events.GuildMessageDelete) {
	if err := b.Store.ClearWarningMsgByMsgID(e.MessageID); err != nil {
		b.Log.Error("clearing warning msg id", "guild", e.GuildID, "msg", e.MessageID, "err", err)
	}
}

func (b *Bot) onGuildLeave(e *events.GuildLeave) {
	if err := b.Store.DeleteGuild(e.Guild.ID); err != nil {
		b.Log.Error("purging guild config", "guild", e.Guild.ID, "err", err)
	}
}
