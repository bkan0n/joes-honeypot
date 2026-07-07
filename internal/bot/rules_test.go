package bot

import (
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

func TestIsTriggerMessage(t *testing.T) {
	cases := []struct {
		name string
		bot  bool
		typ  discord.MessageType
		want bool
	}{
		{"user default", false, discord.MessageTypeDefault, true},
		{"user reply", false, discord.MessageTypeReply, true},
		{"bot default", true, discord.MessageTypeDefault, false},
		{"system join", false, discord.MessageTypeUserJoin, false},
		{"channel pin", false, discord.MessageTypeChannelPinnedMessage, false},
	}
	for _, c := range cases {
		if got := IsTriggerMessage(c.bot, c.typ); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsExempt(t *testing.T) {
	admin := map[snowflake.ID]struct{}{10: {}}
	if !IsExempt(1, 1, nil, nil) {
		t.Error("owner must be exempt")
	}
	if !IsExempt(2, 1, []snowflake.ID{10}, admin) {
		t.Error("admin-role member must be exempt")
	}
	if IsExempt(2, 1, []snowflake.ID{11}, admin) {
		t.Error("regular member must not be exempt")
	}
}

func TestUnbanExpired(t *testing.T) {
	now := time.Now()
	if UnbanExpired(now.Add(-23*time.Hour), now) {
		t.Error("23h old must not be expired")
	}
	if !UnbanExpired(now.Add(-25*time.Hour), now) {
		t.Error("25h old must be expired")
	}
}

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
