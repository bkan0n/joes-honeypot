package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

const introDeleteCID = "delete_intro"

func parseUnbanID(customID string) (snowflake.ID, bool) {
	suffix, ok := strings.CutPrefix(customID, "unban:")
	if !ok || suffix == "" {
		return 0, false
	}
	id, err := snowflake.Parse(suffix)
	if err != nil {
		return 0, false
	}
	return id, true
}

func (b *Bot) onComponent(e *events.ComponentInteractionCreate) {
	data, ok := e.Data.(discord.ButtonInteractionData)
	if !ok || e.GuildID() == nil {
		return
	}
	guildID := *e.GuildID()

	switch {
	case data.CustomID() == introDeleteCID:
		if m := e.Member(); m == nil || !m.Permissions.Has(discord.PermissionManageMessages) {
			b.replyEphemeral(e, "You need the **Manage Messages** permission to delete this.")
			return
		}
		if err := e.DeferUpdateMessage(); err != nil {
			b.log.Warn("acknowledging intro delete", "err", err)
		}
		if err := b.client.Rest.DeleteMessage(e.Message.ChannelID, e.Message.ID); err != nil {
			b.log.Warn("deleting intro message", "err", err)
		}

	default:
		userID, ok := parseUnbanID(data.CustomID())
		if !ok {
			return
		}
		if m := e.Member(); m == nil || !m.Permissions.Has(discord.PermissionBanMembers) {
			b.replyEphemeral(e, "You need the **Ban Members** permission to unban.")
			return
		}
		if unbanExpired(e.Message.CreatedAt, time.Now()) {
			b.replyEphemeral(e, "This unban button has expired (24h). Unban the user manually in Server Settings → Bans.")
			return
		}
		err := b.client.Rest.DeleteBan(guildID, userID,
			rest.WithReason(fmt.Sprintf("Joe's Honeypot: unban button clicked by %s", e.User().Username)))
		if err != nil {
			b.replyEphemeral(e, fmt.Sprintf("Failed to unban <@%d>: %s", userID, err))
			return
		}
		if err := e.CreateMessage(discord.MessageCreate{
			Content: fmt.Sprintf("🔓 <@%d> was unbanned by <@%d>.", userID, e.User().ID),
		}); err != nil {
			b.log.Error("unban announcement", "err", err)
		}
	}
}
