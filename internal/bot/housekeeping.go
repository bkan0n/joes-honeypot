package bot

import (
	"github.com/disgoorg/disgo/events"
)

func (b *Bot) onChannelDelete(e *events.GuildChannelDelete) {
	if ch, err := b.Store.GetChannelByID(e.ChannelID); err == nil && ch != nil {
		if err := b.Store.RemoveChannel(e.ChannelID); err != nil {
			b.Log.Error("removing deleted honeypot channel", "err", err)
		}
		return
	}
	cfg, err := b.Store.GetConfig(e.GuildID)
	if err == nil && cfg != nil && cfg.LogChannelID != nil && *cfg.LogChannelID == e.ChannelID {
		if err := b.Store.UnsetLogChannel(e.GuildID); err != nil {
			b.Log.Error("unsetting deleted log channel", "err", err)
		}
	}
}

func (b *Bot) onMessageDelete(e *events.GuildMessageDelete) {
	if err := b.Store.ClearWarningMsgByMsgID(e.MessageID); err != nil {
		b.Log.Error("clearing warning msg id", "err", err)
	}
}

func (b *Bot) onGuildLeave(e *events.GuildLeave) {
	if err := b.Store.DeleteGuild(e.Guild.ID); err != nil {
		b.Log.Error("purging guild config", "guild", e.Guild.ID, "err", err)
	}
}
