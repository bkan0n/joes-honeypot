package store

import (
	"context"
	"errors"
	"path/filepath"
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
	if cfg, err := s.GetConfig(t.Context(), g); err != nil || cfg != nil {
		t.Fatalf("empty GetConfig = %v, %v; want nil, nil", cfg, err)
	}
	want := Config{GuildID: g, LogChannelID: ptr(999), Action: ActionBan}
	if err := s.UpsertConfig(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConfig(t.Context(), g)
	if err != nil || got == nil || got.Action != ActionBan || *got.LogChannelID != 999 {
		t.Fatalf("got %+v, %v", got, err)
	}
	want.Action = ActionSoftban
	want.LogChannelID = nil
	if err := s.UpsertConfig(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetConfig(t.Context(), g)
	if got.Action != ActionSoftban || got.LogChannelID != nil {
		t.Fatalf("after upsert got %+v", got)
	}
}

func TestChannelLifecycle(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertConfig(t.Context(), Config{GuildID: g, Action: ActionSoftban}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChannel(t.Context(), g, ch); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWarningMsg(t.Context(), ch, ptr(555)); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetChannel(g)
	if err != nil || c == nil || c.ChannelID != ch || *c.MsgID != 555 {
		t.Fatalf("got %+v, %v", c, err)
	}
	// Same channel again keeps msg_id.
	if err := s.SetChannel(t.Context(), g, ch); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetChannel(g)
	if c.MsgID == nil || *c.MsgID != 555 {
		t.Fatalf("msg_id lost on re-set: %+v", c)
	}
	// New channel replaces the old row.
	if err := s.SetChannel(t.Context(), g, ch+1); err != nil {
		t.Fatal(err)
	}
	if old, _ := s.GetChannelByID(ch); old != nil {
		t.Fatalf("old channel row survived: %+v", old)
	}
	c, _ = s.GetChannel(g)
	if c.ChannelID != ch+1 || c.MsgID != nil {
		t.Fatalf("got %+v", c)
	}
	if err := s.ClearWarningMsgByMsgID(t.Context(), 555); err != nil { // no-op, already gone
		t.Fatal(err)
	}
	if err := s.RemoveChannel(t.Context(), ch+1); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.GetChannel(g); c != nil {
		t.Fatalf("channel row survived removal: %+v", c)
	}
}

func TestAllChannels(t *testing.T) {
	s := openTest(t)
	if chans, err := s.AllChannels(); err != nil || len(chans) != 0 {
		t.Fatalf("empty AllChannels = %v, %v; want empty, nil", chans, err)
	}
	for i, guild := range []snowflake.ID{g, g + 1} {
		if err := s.UpsertConfig(t.Context(), Config{GuildID: guild, Action: ActionSoftban}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetChannel(t.Context(), guild, ch+snowflake.ID(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetWarningMsg(t.Context(), ch, ptr(555)); err != nil {
		t.Fatal(err)
	}
	chans, err := s.AllChannels()
	if err != nil || len(chans) != 2 {
		t.Fatalf("AllChannels = %v, %v; want 2 rows", chans, err)
	}
	byID := map[snowflake.ID]Channel{}
	for _, c := range chans {
		byID[c.ChannelID] = c
	}
	if c := byID[ch]; c.GuildID != g || c.MsgID == nil || *c.MsgID != 555 {
		t.Fatalf("channel %d row wrong: %+v", ch, c)
	}
	if c := byID[ch+1]; c.GuildID != g+1 || c.MsgID != nil {
		t.Fatalf("channel %d row wrong: %+v", ch+1, c)
	}
}

func TestEventsAndCascade(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertConfig(t.Context(), Config{GuildID: g, Action: ActionSoftban}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChannel(t.Context(), g, ch); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := s.RecordEvent(t.Context(), g, u, ch); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.CountEventsByGuild(t.Context(), g); err != nil || n != 3 {
		t.Fatalf("count = %d, %v; want 3", n, err)
	}
	// Channel removal keeps events (SET NULL).
	if err := s.RemoveChannel(t.Context(), ch); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountEventsByGuild(t.Context(), g); n != 3 {
		t.Fatalf("count after channel removal = %d; want 3", n)
	}
	// Guild deletion cascades.
	if err := s.DeleteGuild(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountEventsByGuild(t.Context(), g); n != 0 {
		t.Fatalf("count after guild delete = %d; want 0", n)
	}
	if cfg, _ := s.GetConfig(t.Context(), g); cfg != nil {
		t.Fatalf("config survived guild delete: %+v", cfg)
	}
}

func TestSaveGuildSetup(t *testing.T) {
	s := openTest(t)
	// Fresh guild: config and channel land together.
	if err := s.SaveGuildSetup(t.Context(), Config{GuildID: g, Action: ActionSoftban}, ch); err != nil {
		t.Fatal(err)
	}
	if cfg, err := s.GetConfig(t.Context(), g); err != nil || cfg == nil || cfg.Action != ActionSoftban {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	if c, err := s.GetChannel(g); err != nil || c == nil || c.ChannelID != ch {
		t.Fatalf("channel = %+v, %v", c, err)
	}
	// Same channel again keeps msg_id.
	if err := s.SetWarningMsg(t.Context(), ch, ptr(555)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveGuildSetup(t.Context(), Config{GuildID: g, LogChannelID: ptr(999), Action: ActionBan}, ch); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.GetConfig(t.Context(), g)
	if cfg.Action != ActionBan || cfg.LogChannelID == nil || *cfg.LogChannelID != 999 {
		t.Fatalf("config not updated: %+v", cfg)
	}
	c, _ := s.GetChannel(g)
	if c.MsgID == nil || *c.MsgID != 555 {
		t.Fatalf("msg_id lost on re-setup with same channel: %+v", c)
	}
	// New channel replaces the old row.
	if err := s.SaveGuildSetup(t.Context(), Config{GuildID: g, Action: ActionBan}, ch+1); err != nil {
		t.Fatal(err)
	}
	if old, _ := s.GetChannelByID(ch); old != nil {
		t.Fatalf("old channel row survived: %+v", old)
	}
	c, _ = s.GetChannel(g)
	if c == nil || c.ChannelID != ch+1 || c.MsgID != nil {
		t.Fatalf("channel = %+v", c)
	}
}

func TestClearWarningMsgByMsgIDKnownMessage(t *testing.T) {
	s := openTest(t)
	if err := s.SaveGuildSetup(t.Context(), Config{GuildID: g, Action: ActionSoftban}, ch); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWarningMsg(t.Context(), ch, ptr(555)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearWarningMsgByMsgID(t.Context(), 555); err != nil {
		t.Fatal(err)
	}
	c, _ := s.GetChannel(g)
	if c == nil || c.MsgID != nil {
		t.Fatalf("msg_id not cleared: %+v", c)
	}
}

func TestDeleteGuildRemovesChannelLookup(t *testing.T) {
	s := openTest(t)
	if err := s.SaveGuildSetup(t.Context(), Config{GuildID: g, Action: ActionSoftban}, ch); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGuild(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.GetChannelByID(ch); c != nil {
		t.Fatalf("channel lookup survived guild delete: %+v", c)
	}
	if c, _ := s.GetChannel(g); c != nil {
		t.Fatalf("guild channel survived guild delete: %+v", c)
	}
}

// Channel reads are served from the in-process mirror of honeypot_channels,
// so a reopened store must rebuild it from the table at Open.
func TestChannelsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.SaveGuildSetup(t.Context(), Config{GuildID: g, Action: ActionSoftban}, ch); err != nil {
		t.Fatal(err)
	}
	if err := s1.SetWarningMsg(t.Context(), ch, ptr(555)); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	c, err := s2.GetChannelByID(ch)
	if err != nil || c == nil || c.GuildID != g || c.MsgID == nil || *c.MsgID != 555 {
		t.Fatalf("channel after reopen = %+v, %v", c, err)
	}
}

func TestUnsetLogChannel(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertConfig(t.Context(), Config{GuildID: g, LogChannelID: ptr(999), Action: ActionSoftban}); err != nil {
		t.Fatal(err)
	}
	if err := s.UnsetLogChannel(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.GetConfig(t.Context(), g)
	if cfg.LogChannelID != nil {
		t.Fatalf("log channel not unset: %+v", cfg)
	}
}

// Store calls carry the caller's context so shutdown can abort in-flight
// DB work; a cancelled context must fail instead of writing.
func TestCancelledContextAbortsQuery(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.UpsertConfig(ctx, Config{GuildID: g, Action: ActionSoftban}); !errors.Is(err, context.Canceled) {
		t.Fatalf("UpsertConfig with cancelled ctx = %v; want context.Canceled", err)
	}
	if cfg, err := s.GetConfig(t.Context(), g); err != nil || cfg != nil {
		t.Fatalf("config written despite cancelled ctx: %+v, %v", cfg, err)
	}
}
