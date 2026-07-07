package bot

import (
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// selfMemberTTL bounds how long the bot's own member object is reused for
// permission checks. Staleness only matters after the bot's roles change,
// and then only mis-words config-validation warnings.
const selfMemberTTL = 2 * time.Minute

func (b *Bot) botPermissionsIn(guildID, channelID snowflake.ID) discord.Permissions {
	ch, ok := b.client.Caches.Channel(channelID)
	if !ok {
		return 0
	}
	return b.botPermissionsInChannel(guildID, ch)
}

// botPermissionsInChannel computes the bot's permissions in a channel object
// that's already in hand (e.g. freshly returned from a REST call), avoiding a
// cache lookup that may not be populated yet. The member cache config doesn't
// include the bot itself, so the self-member is fetched over REST and kept in
// a short-lived cache — permission checks come in bursts (config submit,
// guild join) that would otherwise repeat the same round-trip.
func (b *Bot) botPermissionsInChannel(guildID snowflake.ID, ch discord.GuildChannel) discord.Permissions {
	member, ok := b.selfMembers.Get(guildID)
	if !ok {
		m, err := b.client.Rest.GetMember(guildID, b.client.ID(), rest.WithCtx(b.ctx))
		if err != nil || m == nil {
			return 0
		}
		member = *m
		b.selfMembers.Set(guildID, member, selfMemberTTL)
	}
	return b.client.Caches.MemberPermissionsInChannel(ch, member)
}

// interactionReplier is satisfied by *events.ApplicationCommandInteractionCreate
// and *events.ComponentInteractionCreate.
type interactionReplier interface {
	CreateMessage(messageCreate discord.MessageCreate, opts ...rest.RequestOpt) error
}

func (b *Bot) replyEphemeral(e interactionReplier, content string) {
	if err := e.CreateMessage(discord.MessageCreate{Content: content, Flags: discord.MessageFlagEphemeral}, rest.WithCtx(b.ctx)); err != nil {
		b.log.Error("sending ephemeral reply", "err", err)
	}
}

// editDeferredReply fills in the deferred ephemeral response created by
// DeferCreateMessage at the top of onModalSubmit; once an interaction is
// deferred, editing the deferred message is the only way to respond.
func (b *Bot) editDeferredReply(e *events.ModalSubmitInteractionCreate, content string) {
	if _, err := b.client.Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.MessageUpdate{
		Content: &content,
	}, rest.WithCtx(b.ctx)); err != nil {
		b.log.Error("editing deferred interaction response", "err", err)
	}
}

// safeGo runs fn on a new goroutine, recovering and logging a panic instead
// of letting it kill the whole process. disgo recovers panics in listener
// goroutines, but not in goroutines we spawn ourselves.
func (b *Bot) safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.log.Error("panic in background goroutine", "panic", r)
			}
		}()
		fn()
	}()
}
