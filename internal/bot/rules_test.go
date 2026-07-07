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
	admin := func(id snowflake.ID) bool { return id == 10 }
	if !IsExempt(1, 1, nil, admin) {
		t.Error("owner must be exempt")
	}
	if !IsExempt(2, 1, []snowflake.ID{11, 10}, admin) {
		t.Error("admin-role member must be exempt")
	}
	if IsExempt(2, 1, []snowflake.ID{11}, admin) {
		t.Error("regular member must not be exempt")
	}
}

func TestIsAdminRole(t *testing.T) {
	cases := []struct {
		name string
		role discord.Role
		want bool
	}{
		{"admin role", discord.Role{Permissions: discord.PermissionAdministrator}, true},
		{"managed admin role (bot role)", discord.Role{Managed: true, Permissions: discord.PermissionAdministrator}, false},
		{"non-admin role", discord.Role{Permissions: discord.PermissionBanMembers}, false},
	}
	for _, c := range cases {
		if got := isAdminRole(c.role); got != c.want {
			t.Errorf("%s: isAdminRole = %v, want %v", c.name, got, c.want)
		}
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

func TestDecideModeration(t *testing.T) {
	cases := []struct {
		name   string
		action store.Action
		exempt bool
		want   moderationPlan
	}{
		{"softban", store.ActionSoftban, false, moderationPlan{DM: true, Ban: true, Unban: true}},
		{"ban", store.ActionBan, false, moderationPlan{DM: true, Ban: true, UnbanButton: true}},
		{"disabled", store.ActionDisabled, false, moderationPlan{}},
		{"softban exempt", store.ActionSoftban, true, moderationPlan{NotifyExempt: true}},
		{"ban exempt", store.ActionBan, true, moderationPlan{NotifyExempt: true}},
		{"disabled exempt: no exempt notification either", store.ActionDisabled, true, moderationPlan{}},
		{"unknown action", store.Action("bogus"), false, moderationPlan{}},
	}
	for _, c := range cases {
		if got := decideModeration(c.action, c.exempt); got != c.want {
			t.Errorf("%s: decideModeration(%q, %v) = %+v, want %+v", c.name, c.action, c.exempt, got, c.want)
		}
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
