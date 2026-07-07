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
	cfg, err := b.store.GetConfig(*e.GuildID())
	if err != nil {
		b.log.Error("loading config for modal", "guild", *e.GuildID(), "err", err)
		b.replyEphemeral(e, "Something went wrong loading the config.")
		return
	}
	if err := e.Modal(configModal(cfg), rest.WithCtx(b.ctx)); err != nil {
		b.log.Error("sending config modal", "err", err)
	}
}

type configSubmission struct {
	HoneypotChannelID snowflake.ID
	LogChannelID      *snowflake.ID
	Action            store.Action
}

func (b *Bot) onModalSubmit(e *events.ModalSubmitInteractionCreate) {
	if e.Data.CustomID != modalID || e.GuildID() == nil {
		return
	}
	guildID := *e.GuildID()

	// Ack immediately: the permission checks and warning-message work below
	// can add up to several REST round-trips, easily blowing Discord's 3s
	// interaction deadline. After deferring, a normal interaction response is
	// no longer allowed — every exit path below must edit the deferred reply
	// (editDeferredReply), never b.replyEphemeral.
	if err := e.DeferCreateMessage(true, rest.WithCtx(b.ctx)); err != nil {
		b.log.Error("deferring modal response", "guild", guildID, "err", err)
		return
	}

	sel, ok := e.Data.ChannelSelectMenu(honeypotChanCID)
	if !ok || len(sel.Values) != 1 {
		b.editDeferredReply(e, "No honeypot channel selected. No settings have been changed.")
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
	switch sub.Action {
	case store.ActionSoftban, store.ActionBan, store.ActionDisabled:
	default:
		b.editDeferredReply(e, "Unknown action selected. No settings have been changed.")
		return
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
		b.editDeferredReply(e, "**No settings have been changed:**\n- "+strings.Join(problems, "\n- "))
		return
	}

	prev, err := b.store.GetChannel(guildID)
	if err != nil {
		b.log.Error("loading previous channel", "guild", guildID, "err", err)
	}
	if err := b.store.SaveGuildSetup(store.Config{GuildID: guildID, LogChannelID: sub.LogChannelID, Action: sub.Action}, sub.HoneypotChannelID); err != nil {
		b.log.Error("saving guild setup", "guild", guildID, "err", err)
		b.editDeferredReply(e, "Something went wrong saving the config. No settings have been changed.")
		return
	}
	// Channel changed: delete the old warning message, post one in the new channel.
	if prev != nil && prev.ChannelID != sub.HoneypotChannelID && prev.MsgID != nil {
		if err := b.client.Rest.DeleteMessage(prev.ChannelID, *prev.MsgID, rest.WithCtx(b.ctx)); err != nil {
			b.log.Warn("deleting old warning message", "err", err)
		}
	}
	if err := b.ensureWarningMessage(guildID, sub.HoneypotChannelID); err != nil {
		b.log.Warn("posting warning message after config change", "guild", guildID, "channel", sub.HoneypotChannelID, "err", err)
		b.editDeferredReply(e, fmt.Sprintf("🍯 Honeypot configured: <#%d>, action **%s**.\n⚠️ I couldn't post the warning message in the honeypot channel — check my View/Send permissions there.", sub.HoneypotChannelID, sub.Action))
	} else {
		b.editDeferredReply(e, fmt.Sprintf("🍯 Honeypot configured: <#%d>, action **%s**.", sub.HoneypotChannelID, sub.Action))
	}
}
