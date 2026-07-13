package bot

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/cache"
	"github.com/bkan0n/joeshoneypot/internal/store"
)

func att(name string, size int) discord.Attachment {
	return discord.Attachment{Filename: name, Size: size}
}

func TestSpamFingerprint(t *testing.T) {
	a := []discord.Attachment{att("a.png", 100), att("b.png", 200)}
	if spamFingerprint(a) != spamFingerprint(a) {
		t.Error("fingerprint must be deterministic")
	}
	reordered := []discord.Attachment{att("b.png", 200), att("a.png", 100)}
	if spamFingerprint(a) != spamFingerprint(reordered) {
		t.Error("fingerprint must not depend on attachment order")
	}
	renamed := []discord.Attachment{att("c.png", 100), att("b.png", 200)}
	if spamFingerprint(a) == spamFingerprint(renamed) {
		t.Error("fingerprint must change when a filename changes")
	}
	resized := []discord.Attachment{att("a.png", 101), att("b.png", 200)}
	if spamFingerprint(a) == spamFingerprint(resized) {
		t.Error("fingerprint must change when a size changes")
	}
	// (filename, size) pairs must not bleed into each other when joined.
	x := []discord.Attachment{att("a", 11), att("b", 2)}
	y := []discord.Attachment{att("a", 1), att("1b", 2)}
	if spamFingerprint(x) == spamFingerprint(y) {
		t.Error("fingerprint must separate filename and size unambiguously")
	}
}

func TestRecordSpamSighting(t *testing.T) {
	c := cache.NewTTL[spamKey, map[snowflake.ID]struct{}]()
	k := spamKey{GuildID: 1, UserID: 2, Fingerprint: 3}
	if n := recordSpamSighting(c, k, 100); n != 1 {
		t.Errorf("first channel: got %d, want 1", n)
	}
	if n := recordSpamSighting(c, k, 100); n != 1 {
		t.Errorf("same channel again: got %d, want 1", n)
	}
	if n := recordSpamSighting(c, k, 200); n != 2 {
		t.Errorf("second distinct channel: got %d, want 2", n)
	}
	other := spamKey{GuildID: 1, UserID: 2, Fingerprint: 4}
	if n := recordSpamSighting(c, other, 300); n != 1 {
		t.Errorf("different fingerprint must count separately: got %d, want 1", n)
	}
}

func TestSpamEligible(t *testing.T) {
	cfg := func(action store.Action, spam bool) *store.Config {
		return &store.Config{GuildID: 1, Action: action, SpamDetection: spam}
	}
	cases := []struct {
		name string
		n    int
		cfg  *store.Config
		want bool
	}{
		{"two attachments, softban, enabled", 2, cfg(store.ActionSoftban, true), true},
		{"three attachments, ban, enabled", 3, cfg(store.ActionBan, true), true},
		{"one attachment", 1, cfg(store.ActionSoftban, true), false},
		{"zero attachments", 0, cfg(store.ActionSoftban, true), false},
		{"toggle off", 2, cfg(store.ActionSoftban, false), false},
		{"action disabled", 2, cfg(store.ActionDisabled, true), false},
		{"no config", 2, nil, false},
	}
	for _, c := range cases {
		if got := spamEligible(c.n, c.cfg); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
