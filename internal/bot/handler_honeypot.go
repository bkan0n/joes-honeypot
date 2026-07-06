package bot

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

const (
	modalID          = "honeypot_config"
	honeypotChanCID  = "honeypot_channel"
	logChanCID       = "log_channel"
	actionCID        = "honeypot_action"
	counterButtonCID = "moderated_count"
)

func configModal(current *store.Config) discord.ModalCreate {
	defaultAction := store.ActionSoftban
	if current != nil {
		defaultAction = current.Action
	}
	opt := func(label string, value store.Action) discord.StringSelectMenuOption {
		o := discord.NewStringSelectMenuOption(label, string(value))
		if value == defaultAction {
			o = o.WithDefault(true)
		}
		return o
	}
	return discord.NewModalCreate(modalID, "Configure Joe's Honeypot",
		discord.NewLabel("Honeypot channel",
			discord.NewChannelSelectMenu(honeypotChanCID, "Select a channel").
				WithChannelTypes(discord.ChannelTypeGuildText, discord.ChannelTypeGuildVoice).
				WithMinValues(1).WithMaxValues(1)),
		discord.NewLabel("Log channel (optional)",
			discord.NewChannelSelectMenu(logChanCID, "Select a channel").
				WithChannelTypes(discord.ChannelTypeGuildText,
					discord.ChannelTypeGuildPublicThread, discord.ChannelTypeGuildPrivateThread).
				WithMinValues(0).WithMaxValues(1)),
		discord.NewLabel("Action",
			discord.NewStringSelectMenu(actionCID, "Choose an action",
				opt("Softban (kick) — bans & unbans, deleting the last hour of messages", store.ActionSoftban),
				opt("Ban — deletes the last hour of messages", store.ActionBan),
				opt("Disabled — react only, take no action", store.ActionDisabled),
			).WithMinValues(1).WithMaxValues(1)),
	)
}

func (b *Bot) onCommand(e *events.ApplicationCommandInteractionCreate) {
	if e.Data.CommandName() != "honeypot" || e.GuildID() == nil {
		return
	}
	cfg, err := b.Store.GetConfig(*e.GuildID())
	if err != nil {
		b.Log.Error("loading config for modal", "guild", *e.GuildID(), "err", err)
		return
	}
	if err := e.Modal(configModal(cfg)); err != nil {
		b.Log.Error("sending config modal", "err", err)
	}
}

type configSubmission struct {
	HoneypotChannelID snowflake.ID
	LogChannelID      *snowflake.ID
	Action            store.Action
}

// validateConfig returns human-readable problems; empty means valid.
// Nothing is saved unless it returns empty.
func validateConfig(sub configSubmission, userPerms, botHoneypotPerms, botLogPerms discord.Permissions) []string {
	var problems []string
	if !botHoneypotPerms.Has(discord.PermissionViewChannel) || !botHoneypotPerms.Has(discord.PermissionSendMessages) {
		problems = append(problems, "I need **View Channel** and **Send Messages** in the honeypot channel.")
	}
	if sub.Action == store.ActionSoftban || sub.Action == store.ActionBan {
		if !botHoneypotPerms.Has(discord.PermissionBanMembers) {
			problems = append(problems, "I need the **Ban Members** permission for the softban/ban action.")
		}
		if !userPerms.Has(discord.PermissionBanMembers) {
			problems = append(problems, "You need the **Ban Members** permission to set the softban/ban action.")
		}
	}
	if sub.LogChannelID != nil {
		if !botLogPerms.Has(discord.PermissionViewChannel) || !botLogPerms.Has(discord.PermissionSendMessages) {
			problems = append(problems, "I need **View Channel** and **Send Messages** in the log channel.")
		}
	}
	return problems
}

func (b *Bot) onModalSubmit(e *events.ModalSubmitInteractionCreate) {
	if e.Data.CustomID != modalID || e.GuildID() == nil {
		return
	}
	guildID := *e.GuildID()

	sel, ok := e.Data.ChannelSelectMenu(honeypotChanCID)
	if !ok || len(sel.Values) != 1 {
		b.replyEphemeral(e, "No honeypot channel selected. No settings have been changed.")
		return
	}
	sub := configSubmission{HoneypotChannelID: sel.Values[0], Action: store.ActionSoftban}
	if logSel, ok := e.Data.ChannelSelectMenu(logChanCID); ok && len(logSel.Values) == 1 {
		id := logSel.Values[0]
		sub.LogChannelID = &id
	}
	if actions := e.Data.StringValues(actionCID); len(actions) == 1 {
		sub.Action = store.Action(actions[0])
	}

	var userPerms discord.Permissions
	if m := e.Member(); m != nil {
		userPerms = m.Permissions
	}
	var botLogPerms discord.Permissions
	if sub.LogChannelID != nil {
		botLogPerms = b.botPermissionsIn(guildID, *sub.LogChannelID)
	}
	if problems := validateConfig(sub, userPerms, b.botPermissionsIn(guildID, sub.HoneypotChannelID), botLogPerms); len(problems) > 0 {
		b.replyEphemeral(e, "**No settings have been changed:**\n- "+strings.Join(problems, "\n- "))
		return
	}

	prev, err := b.Store.GetChannel(guildID)
	if err != nil {
		b.Log.Error("loading previous channel", "guild", guildID, "err", err)
	}
	if err := b.Store.UpsertConfig(store.Config{GuildID: guildID, LogChannelID: sub.LogChannelID, Action: sub.Action}); err != nil {
		b.Log.Error("saving config", "guild", guildID, "err", err)
		b.replyEphemeral(e, "Something went wrong saving the config. No settings have been changed.")
		return
	}
	if err := b.Store.SetChannel(guildID, sub.HoneypotChannelID); err != nil {
		b.Log.Error("saving channel", "guild", guildID, "err", err)
		b.replyEphemeral(e, "Something went wrong saving the channel.")
		return
	}
	// Channel changed: delete the old warning message, post one in the new channel.
	if prev != nil && prev.ChannelID != sub.HoneypotChannelID && prev.MsgID != nil {
		if err := b.Client.Rest.DeleteMessage(prev.ChannelID, *prev.MsgID); err != nil {
			b.Log.Warn("deleting old warning message", "err", err)
		}
	}
	if b.ensureWarningMessage(guildID, sub.HoneypotChannelID) {
		b.replyEphemeral(e, fmt.Sprintf("🍯 Honeypot configured: <#%d>, action **%s**.", sub.HoneypotChannelID, sub.Action))
	} else {
		b.replyEphemeral(e, fmt.Sprintf("🍯 Honeypot configured: <#%d>, action **%s**.\n⚠️ I couldn't post the warning message in the honeypot channel — check my View/Send permissions there.", sub.HoneypotChannelID, sub.Action))
	}
}

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
	components := []discord.LayoutComponent{
		discord.NewActionRow(
			discord.NewSecondaryButton(CounterButtonLabel(count), counterButtonCID),
		),
	}
	if ch.MsgID != nil {
		if _, err := b.Client.Rest.UpdateMessage(channelID, *ch.MsgID, discord.MessageUpdate{Components: &components}); err == nil {
			return true
		}
		// Message gone (deleted manually) — fall through and repost.
	}
	msg, err := b.Client.Rest.CreateMessage(channelID, discord.MessageCreate{
		Content:    WarningMessage(),
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
