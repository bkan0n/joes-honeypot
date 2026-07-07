package bot

import (
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

// IsTriggerMessage reports whether a message in a honeypot channel should
// trigger moderation: only ordinary messages/replies from non-bot accounts.
// All system message types (joins, pins, boosts, ...) are excluded.
func IsTriggerMessage(authorIsBot bool, msgType discord.MessageType) bool {
	if authorIsBot {
		return false
	}
	return msgType == discord.MessageTypeDefault || msgType == discord.MessageTypeReply
}

// IsExempt reports whether the author must not be actioned: the server owner,
// or any member holding a non-managed role with the Administrator permission
// (adminRoleIDs is precomputed from the role cache).
func IsExempt(authorID, ownerID snowflake.ID, memberRoleIDs []snowflake.ID, adminRoleIDs map[snowflake.ID]struct{}) bool {
	if authorID == ownerID {
		return true
	}
	for _, r := range memberRoleIDs {
		if _, ok := adminRoleIDs[r]; ok {
			return true
		}
	}
	return false
}

// UnbanExpired reports whether an unban button is too old to honor (24h).
func UnbanExpired(messageCreated, now time.Time) bool {
	return now.Sub(messageCreated) > 24*time.Hour
}

// moderationPlan is the pure "what should happen" decision for a honeypot
// trigger, separated from the REST calls that execute it (see Bot.moderate).
// The zero value means: take no action beyond the honey react.
type moderationPlan struct {
	DM           bool // DM the user before acting (bounded wait)
	Ban          bool // ban the user
	Unban        bool // softban: lift the ban right after it lands
	UnbanButton  bool // attach an Unban button to the log message
	NotifyExempt bool // exempt user: example DM + exempt log, no action
}

// decideModeration maps the configured action and the author's exemption
// status to a moderationPlan. Disabled wins over exemption: a disabled
// honeypot never notifies anyone, exempt or not.
func decideModeration(action store.Action, exempt bool) moderationPlan {
	switch action {
	case store.ActionSoftban, store.ActionBan:
	default: // disabled or unknown: react only
		return moderationPlan{}
	}
	if exempt {
		return moderationPlan{NotifyExempt: true}
	}
	return moderationPlan{
		DM:          true,
		Ban:         true,
		Unban:       action == store.ActionSoftban,
		UnbanButton: action == store.ActionBan,
	}
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
