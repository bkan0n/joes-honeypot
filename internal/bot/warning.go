package bot

import (
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// warningMessagePrefix identifies a bot-authored message as our persistent
// warning post, used both to detect a stale message (see selectWarningMessage)
// and, indirectly, as the start of WarningMessage's content.
const warningMessagePrefix = "## ⚠️"

// ensureWarningMessage posts the persistent warning (with counter button) if
// the channel has none recorded, otherwise refreshes the counter label.
// It returns true when the warning message is confirmed posted or updated
// (with its msg_id stored or already current), and false on any failure.
func (b *Bot) ensureWarningMessage(guildID, channelID snowflake.ID) bool {
	ch, err := b.Store.GetChannelByID(channelID)
	if err != nil || ch == nil {
		return false
	}
	count, err := b.Store.CountEventsByGuild(guildID)
	if err != nil {
		b.Log.Error("counting events", "err", err)
		return false
	}
	components := WarningMessageComponents(count)
	if ch.MsgID != nil {
		if b.updateWarningMessage(channelID, *ch.MsgID, components) {
			return true
		}
		// Message gone (deleted manually) — fall through and repost.
	}

	// No msg_id on record — before posting a new warning message, check
	// whether the bot already left one behind (e.g. a rejoin, or a restart
	// racing a stale DB write). If so, adopt the oldest and clean up
	// duplicates instead of spamming another one.
	if recent, err := b.Client.Rest.GetMessages(channelID, 0, 0, 0, 50); err != nil {
		b.Log.Warn("listing messages for warning-message dedup", "channel", channelID, "err", err)
	} else if adopt, extras := selectWarningMessage(recent, b.Client.ID()); adopt != nil {
		if !b.updateWarningMessage(channelID, adopt.ID, components) {
			b.Log.Warn("updating adopted warning message", "channel", channelID, "msg", adopt.ID)
		}
		if err := b.Store.SetWarningMsg(channelID, &adopt.ID); err != nil {
			b.Log.Error("storing adopted warning msg id", "err", err)
			return false
		}
		for _, extra := range extras {
			if err := b.Client.Rest.DeleteMessage(channelID, extra.ID); err != nil {
				b.Log.Warn("deleting duplicate warning message", "channel", channelID, "msg", extra.ID, "err", err)
			}
		}
		return true
	}

	msg, err := b.Client.Rest.CreateMessage(channelID, discord.MessageCreate{
		Flags:      discord.MessageFlagIsComponentsV2,
		Components: components,
	})
	if err != nil {
		b.Log.Error("posting warning message", "channel", channelID, "err", err)
		return false
	}
	if err := b.Store.SetWarningMsg(channelID, &msg.ID); err != nil {
		b.Log.Error("storing warning msg id", "err", err)
		return false
	}
	return true
}

// updateWarningMessage edits an existing message in place into the current
// Components-V2 warning layout. It first tries a plain components update
// (the message is already CV2), then retries with the CV2 flag set and the
// content cleared, which converts a legacy plain-content warning message.
func (b *Bot) updateWarningMessage(channelID, msgID snowflake.ID, components []discord.LayoutComponent) bool {
	if _, err := b.Client.Rest.UpdateMessage(channelID, msgID, discord.MessageUpdate{
		Components: &components,
	}); err == nil {
		return true
	}
	empty := ""
	flags := discord.MessageFlagIsComponentsV2
	if _, err := b.Client.Rest.UpdateMessage(channelID, msgID, discord.MessageUpdate{
		Content:    &empty,
		Flags:      &flags,
		Components: &components,
	}); err != nil {
		b.Log.Debug("updating warning message", "channel", channelID, "msg", msgID, "err", err)
		return false
	}
	return true
}

// isWarningMessage reports whether a message looks like our persistent
// warning post, in either the legacy plain-content format or the
// Components-V2 format (warning text inside a TextDisplay).
func isWarningMessage(m discord.Message) bool {
	if strings.HasPrefix(m.Content, warningMessagePrefix) {
		return true
	}
	for _, c := range m.Components {
		if componentHasWarningPrefix(c) {
			return true
		}
	}
	return false
}

func componentHasWarningPrefix(c discord.Component) bool {
	switch cc := c.(type) {
	case discord.TextDisplayComponent:
		return strings.HasPrefix(cc.Content, warningMessagePrefix)
	case discord.ContainerComponent:
		for _, sub := range cc.Components {
			if componentHasWarningPrefix(sub) {
				return true
			}
		}
	case discord.SectionComponent:
		for _, sub := range cc.Components {
			if componentHasWarningPrefix(sub) {
				return true
			}
		}
	}
	return false
}

// selectWarningMessage scans a channel's recent messages for ones the bot
// itself posted that look like our persistent warning message (see
// isWarningMessage). It returns the oldest match to adopt (its ID gets stored
// as the tracked warning message) and any remaining matches as extras that
// should be deleted as duplicates. msgs is not assumed to be in any
// particular order (GetMessages returns newest-first, but this doesn't rely
// on that).
func selectWarningMessage(msgs []discord.Message, botID snowflake.ID) (adopt *discord.Message, extras []discord.Message) {
	var matches []discord.Message
	for _, m := range msgs {
		if m.Author.ID == botID && isWarningMessage(m) {
			matches = append(matches, m)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	oldest := 0
	for i, m := range matches {
		if m.ID < matches[oldest].ID {
			oldest = i
		}
	}
	chosen := matches[oldest]
	for i, m := range matches {
		if i != oldest {
			extras = append(extras, m)
		}
	}
	return &chosen, extras
}
