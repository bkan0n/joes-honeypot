package store

import (
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

const (
	g  = snowflake.ID(100)
	ch = snowflake.ID(200)
	u  = snowflake.ID(300)
)

func ptr(id snowflake.ID) *snowflake.ID { return &id }

func TestConfigRoundTrip(t *testing.T) {
	s := openTest(t)
	if cfg, err := s.GetConfig(g); err != nil || cfg != nil {
		t.Fatalf("empty GetConfig = %v, %v; want nil, nil", cfg, err)
	}
	want := Config{GuildID: g, LogChannelID: ptr(999), Action: ActionBan}
	if err := s.UpsertConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConfig(g)
	if err != nil || got == nil || got.Action != ActionBan || *got.LogChannelID != 999 {
		t.Fatalf("got %+v, %v", got, err)
	}
	want.Action = ActionSoftban
	want.LogChannelID = nil
	if err := s.UpsertConfig(want); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetConfig(g)
	if got.Action != ActionSoftban || got.LogChannelID != nil {
		t.Fatalf("after upsert got %+v", got)
	}
}

func TestChannelLifecycle(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertConfig(Config{GuildID: g, Action: ActionSoftban}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChannel(g, ch); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWarningMsg(ch, ptr(555)); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetChannel(g)
	if err != nil || c == nil || c.ChannelID != ch || *c.MsgID != 555 {
		t.Fatalf("got %+v, %v", c, err)
	}
	// Same channel again keeps msg_id.
	if err := s.SetChannel(g, ch); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetChannel(g)
	if c.MsgID == nil || *c.MsgID != 555 {
		t.Fatalf("msg_id lost on re-set: %+v", c)
	}
	// New channel replaces the old row.
	if err := s.SetChannel(g, ch+1); err != nil {
		t.Fatal(err)
	}
	if old, _ := s.GetChannelByID(ch); old != nil {
		t.Fatalf("old channel row survived: %+v", old)
	}
	c, _ = s.GetChannel(g)
	if c.ChannelID != ch+1 || c.MsgID != nil {
		t.Fatalf("got %+v", c)
	}
	if err := s.ClearWarningMsgByMsgID(555); err != nil { // no-op, already gone
		t.Fatal(err)
	}
	if err := s.RemoveChannel(ch + 1); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.GetChannel(g); c != nil {
		t.Fatalf("channel row survived removal: %+v", c)
	}
}

func TestEventsAndCascade(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertConfig(Config{GuildID: g, Action: ActionSoftban}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChannel(g, ch); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := s.RecordEvent(g, u, ch); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.CountEventsByGuild(g); err != nil || n != 3 {
		t.Fatalf("count = %d, %v; want 3", n, err)
	}
	// Channel removal keeps events (SET NULL).
	if err := s.RemoveChannel(ch); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountEventsByGuild(g); n != 3 {
		t.Fatalf("count after channel removal = %d; want 3", n)
	}
	// Guild deletion cascades.
	if err := s.DeleteGuild(g); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountEventsByGuild(g); n != 0 {
		t.Fatalf("count after guild delete = %d; want 0", n)
	}
	if cfg, _ := s.GetConfig(g); cfg != nil {
		t.Fatalf("config survived guild delete: %+v", cfg)
	}
}

func TestUnsetLogChannel(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertConfig(Config{GuildID: g, LogChannelID: ptr(999), Action: ActionSoftban}); err != nil {
		t.Fatal(err)
	}
	if err := s.UnsetLogChannel(g); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.GetConfig(g)
	if cfg.LogChannelID != nil {
		t.Fatalf("log channel not unset: %+v", cfg)
	}
}
