package bot

import (
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
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
