package bot

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

func (b *Bot) botPermissionsIn(guildID, channelID snowflake.ID) discord.Permissions {
	ch, ok := b.Client.Caches.Channel(channelID)
	if !ok {
		return 0
	}
	return b.botPermissionsInChannel(guildID, ch)
}

// botPermissionsInChannel computes the bot's permissions in a channel object
// that's already in hand (e.g. freshly returned from a REST call), avoiding a
// cache lookup that may not be populated yet.
func (b *Bot) botPermissionsInChannel(guildID snowflake.ID, ch discord.GuildChannel) discord.Permissions {
	member, err := b.Client.Rest.GetMember(guildID, b.Client.ID())
	if err != nil || member == nil {
		return 0
	}
	return b.Client.Caches.MemberPermissionsInChannel(ch, *member)
}

// interactionReplier is satisfied by *events.ModalSubmitInteractionCreate and
// *events.ComponentInteractionCreate.
type interactionReplier interface {
	CreateMessage(messageCreate discord.MessageCreate, opts ...rest.RequestOpt) error
}

func (b *Bot) replyEphemeral(e interactionReplier, content string) {
	if err := e.CreateMessage(discord.MessageCreate{Content: content, Flags: discord.MessageFlagEphemeral}); err != nil {
		b.Log.Error("sending ephemeral reply", "err", err)
	}
}
