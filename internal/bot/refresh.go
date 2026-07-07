package bot

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

// handleMentionRefresh implements "@bot refresh": when the bot owner mentions
// the bot in a message containing the word "refresh", the warning message of
// every guild is re-rendered into the current format. Messages that mention
// the bot carry their content even without the Message Content intent, so
// this works with just IntentGuildMessages. Only called for messages outside
// the honeypot channel (posting inside it triggers the honeypot).
func (b *Bot) handleMentionRefresh(e *events.MessageCreate) {
	msg := e.Message
	if b.ownerID == 0 || msg.Author.ID != b.ownerID {
		return
	}
	var mentioned bool
	for _, u := range msg.Mentions {
		if u.ID == b.client.ID() {
			mentioned = true
			break
		}
	}
	if !mentioned || !strings.Contains(strings.ToLower(msg.Content), "refresh") {
		return
	}

	channels, err := b.store.AllChannels()
	if err != nil {
		b.log.Error("listing channels for warning-message refresh", "err", err)
		return
	}
	var updated int
	for _, ch := range channels {
		if err := b.ensureWarningMessage(ch.GuildID, ch.ChannelID); err != nil {
			b.log.Warn("warning-message refresh failed", "guild", ch.GuildID, "channel", ch.ChannelID, "err", err)
		} else {
			updated++
		}
	}
	if _, err := b.client.Rest.CreateMessage(e.ChannelID, discord.MessageCreate{
		Content: fmt.Sprintf("🍯 Refreshed the warning message in %d/%d guilds.", updated, len(channels)),
	}); err != nil {
		b.log.Warn("acking mention refresh", "err", err)
	}
}
