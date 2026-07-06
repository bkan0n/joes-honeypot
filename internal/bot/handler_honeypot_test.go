package bot

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

const botChannelPerms = discord.PermissionViewChannel | discord.PermissionSendMessages

func TestValidateConfigOK(t *testing.T) {
	sub := configSubmission{HoneypotChannelID: 1, Action: store.ActionSoftban}
	problems := validateConfig(sub, discord.PermissionBanMembers, botChannelPerms|discord.PermissionBanMembers, 0)
	if len(problems) != 0 {
		t.Fatalf("expected valid, got %v", problems)
	}
}

func TestValidateConfigMissingBotBan(t *testing.T) {
	sub := configSubmission{HoneypotChannelID: 1, Action: store.ActionBan}
	problems := validateConfig(sub, discord.PermissionBanMembers, botChannelPerms, 0)
	if len(problems) == 0 {
		t.Fatal("expected problem: bot missing Ban Members")
	}
}

func TestValidateConfigMissingUserBan(t *testing.T) {
	sub := configSubmission{HoneypotChannelID: 1, Action: store.ActionSoftban}
	problems := validateConfig(sub, 0, botChannelPerms|discord.PermissionBanMembers, 0)
	if len(problems) == 0 {
		t.Fatal("expected problem: user missing Ban Members")
	}
}

func TestValidateConfigDisabledNeedsNoBan(t *testing.T) {
	sub := configSubmission{HoneypotChannelID: 1, Action: store.ActionDisabled}
	problems := validateConfig(sub, 0, botChannelPerms, 0)
	if len(problems) != 0 {
		t.Fatalf("disabled action must not require ban perms, got %v", problems)
	}
}

func TestValidateConfigLogChannel(t *testing.T) {
	logCh := snowflake.ID(2)
	sub := configSubmission{HoneypotChannelID: 1, LogChannelID: &logCh, Action: store.ActionDisabled}
	problems := validateConfig(sub, 0, botChannelPerms, 0) // no perms in log channel
	if len(problems) == 0 {
		t.Fatal("expected problem: bot cannot post in log channel")
	}
	problems = validateConfig(sub, 0, botChannelPerms, botChannelPerms)
	if len(problems) != 0 {
		t.Fatalf("expected valid, got %v", problems)
	}
}
