package bot

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

const warningIconURL = "https://cdn.bkan0n.com/assets/joehoneypot/icon.png"

func actionVerb(action store.Action) string {
	if action == store.ActionBan {
		return "banned"
	}
	return "kicked"
}

func warningMessage() string {
	return "## ⚠️ DO NOT SEND MESSAGES IN THIS CHANNEL\n" +
		"Anyone who posts here is **automatically banned**. No exceptions, no warnings.\n" +
		"-# This channel is a honeypot for catching spam bots."
}

// warningMessageComponents builds the Components-V2 layout of the persistent
// warning message: a container holding the warning text with the bot icon as
// a section thumbnail, and the kick-counter button. The button is display
// only (disabled) — its label already carries the count, and a clickable
// public button would be free DB load for anyone to spam.
func warningMessageComponents(count int64) []discord.LayoutComponent {
	return []discord.LayoutComponent{
		discord.NewContainer(
			discord.NewSection(
				discord.NewTextDisplay(warningMessage()),
			).WithAccessory(discord.NewThumbnail(warningIconURL)),
			discord.NewActionRow(
				discord.NewSecondaryButton(counterButtonLabel(count), counterButtonCID).AsDisabled(),
			),
		),
	}
}

const dmFooter = "-# This is an automated message from Joe's Honeypot."

func dmMessage(action store.Action, guildName string) string {
	return fmt.Sprintf(
		"## 🍯 Honeypot Triggered\nYou have been **%s** from **%s** for sending a message in the honeypot channel.\n"+
			dmFooter,
		actionVerb(action), guildName)
}

func exemptDMMessage(guildName string) string {
	return fmt.Sprintf(
		"## 🍯 Honeypot Triggered (example)\nYou posted in the honeypot channel of **%s**, "+
			"but you are the server owner or an administrator, so no action was taken. "+
			"A regular user would have received this DM and been actioned.\n"+
			dmFooter,
		guildName)
}

func logMessage(userID snowflake.ID, action store.Action) string {
	return fmt.Sprintf("<@%d> was %s for sending a message in the honeypot channel.", userID, actionVerb(action))
}

func exemptLogMessage(userID snowflake.ID) string {
	return fmt.Sprintf("⚠️ <@%d> posted in the honeypot channel but was **not** actioned (server owner or administrator).", userID)
}

func introMessage(missingBanPerm bool) string {
	msg := "## 🍯 Joe's Honeypot is set up!\n" +
		"Any non-admin account that posts in the honeypot channel will be softbanned " +
		"(banned and unbanned, deleting their last hour of messages).\n" +
		"Use the `/honeypot` command to change the channel, set a log channel, or switch the action.\n" +
		"-# This message deletes itself in a few minutes."
	if missingBanPerm {
		msg += "\n\n⚠️ **I am missing the Ban Members permission** — I cannot take any action until it is granted."
	}
	return msg
}

func counterButtonLabel(count int64) string {
	return fmt.Sprintf("%d Kicked", count)
}
