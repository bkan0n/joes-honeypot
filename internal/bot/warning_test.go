package bot

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

const botID = snowflake.ID(999)

func warningMsg(id snowflake.ID, authorID snowflake.ID) discord.Message {
	return discord.Message{
		ID:      id,
		Author:  discord.User{ID: authorID},
		Content: warningMessage(),
	}
}

func cv2WarningMsg(id snowflake.ID, authorID snowflake.ID) discord.Message {
	return discord.Message{
		ID:         id,
		Author:     discord.User{ID: authorID},
		Components: warningMessageComponents(5),
	}
}

func otherMsg(id snowflake.ID, authorID snowflake.ID) discord.Message {
	return discord.Message{
		ID:      id,
		Author:  discord.User{ID: authorID},
		Content: "hello there",
	}
}

func TestSelectWarningMessage(t *testing.T) {
	cases := []struct {
		name       string
		msgs       []discord.Message
		wantAdopt  *snowflake.ID
		wantExtras []snowflake.ID
	}{
		{
			name:      "no messages",
			msgs:      nil,
			wantAdopt: nil,
		},
		{
			name: "no matches",
			msgs: []discord.Message{
				otherMsg(10, botID),
				warningMsg(20, 555), // not the bot
			},
			wantAdopt: nil,
		},
		{
			name: "single match adopted",
			msgs: []discord.Message{
				otherMsg(10, botID),
				warningMsg(20, botID),
			},
			wantAdopt: idPtr(20),
		},
		{
			name: "components-v2 format matched",
			msgs: []discord.Message{
				otherMsg(10, botID),
				cv2WarningMsg(20, botID),
			},
			wantAdopt: idPtr(20),
		},
		{
			name: "mixed legacy and v2: oldest adopted",
			msgs: []discord.Message{
				cv2WarningMsg(30, botID),
				warningMsg(10, botID),
			},
			wantAdopt:  idPtr(10),
			wantExtras: []snowflake.ID{30},
		},
		{
			name: "multiple matches: oldest (smallest ID) adopted, rest are extras",
			msgs: []discord.Message{
				warningMsg(30, botID), // newest (GetMessages returns newest-first)
				otherMsg(25, botID),
				warningMsg(10, botID), // oldest
				warningMsg(20, botID),
			},
			wantAdopt:  idPtr(10),
			wantExtras: []snowflake.ID{30, 20},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adopt, extras := selectWarningMessage(c.msgs, botID)
			if c.wantAdopt == nil {
				if adopt != nil {
					t.Fatalf("expected no adopt, got %v", adopt.ID)
				}
				return
			}
			if adopt == nil {
				t.Fatalf("expected adopt %v, got nil", *c.wantAdopt)
			}
			if adopt.ID != *c.wantAdopt {
				t.Fatalf("adopt = %v, want %v", adopt.ID, *c.wantAdopt)
			}
			gotExtras := make([]snowflake.ID, len(extras))
			for i, m := range extras {
				gotExtras[i] = m.ID
			}
			if len(gotExtras) != len(c.wantExtras) {
				t.Fatalf("extras = %v, want %v", gotExtras, c.wantExtras)
			}
			seen := map[snowflake.ID]bool{}
			for _, id := range gotExtras {
				seen[id] = true
			}
			for _, id := range c.wantExtras {
				if !seen[id] {
					t.Fatalf("extras = %v, missing want %v", gotExtras, id)
				}
			}
		})
	}
}

func idPtr(id snowflake.ID) *snowflake.ID { return &id }
