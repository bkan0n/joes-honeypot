package bot

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/omit"
)

func commands() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "honeypot",
			Description: "Configure the honeypot channel and its settings",
			DefaultMemberPermissions: omit.NewPtr(
				discord.PermissionManageGuild | discord.PermissionBanMembers |
					discord.PermissionModerateMembers | discord.PermissionManageMessages |
					discord.PermissionManageChannels,
			),
			Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		},
	}
}
