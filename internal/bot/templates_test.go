package bot

import (
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

func TestDMMessage(t *testing.T) {
	dm := DMMessage(store.ActionSoftban, "My Server")
	if !strings.Contains(dm, "kicked") || !strings.Contains(dm, "My Server") {
		t.Fatalf("softban DM wrong: %q", dm)
	}
	if dm := DMMessage(store.ActionBan, "My Server"); !strings.Contains(dm, "banned") {
		t.Fatalf("ban DM wrong: %q", dm)
	}
}

func TestLogMessage(t *testing.T) {
	msg := LogMessage(42, store.ActionBan)
	if !strings.Contains(msg, "<@42>") || !strings.Contains(msg, "banned") {
		t.Fatalf("log message wrong: %q", msg)
	}
}

func TestCounterButtonLabel(t *testing.T) {
	if got := CounterButtonLabel(0); got != "0 Kicked" {
		t.Fatalf("got %q", got)
	}
	if got := CounterButtonLabel(7); got != "7 Kicked" {
		t.Fatalf("got %q", got)
	}
}

func TestWarningMessageHasNoEmDash(t *testing.T) {
	if strings.Contains(WarningMessage(), "—") {
		t.Fatalf("warning message contains an em dash: %q", WarningMessage())
	}
}

func TestWarningMessageComponents(t *testing.T) {
	comps := WarningMessageComponents(3)
	if len(comps) != 1 {
		t.Fatalf("expected a single top-level container, got %d components", len(comps))
	}
	container, ok := comps[0].(discord.ContainerComponent)
	if !ok {
		t.Fatalf("top-level component is %T, want ContainerComponent", comps[0])
	}
	var haveText, haveThumb, haveButton bool
	for _, sub := range container.Components {
		switch c := sub.(type) {
		case discord.SectionComponent:
			for _, sc := range c.Components {
				if td, ok := sc.(discord.TextDisplayComponent); ok && td.Content == WarningMessage() {
					haveText = true
				}
			}
			if thumb, ok := c.Accessory.(discord.ThumbnailComponent); ok && thumb.Media.URL == warningIconURL {
				haveThumb = true
			}
		case discord.ActionRowComponent:
			for _, rc := range c.Components {
				if btn, ok := rc.(discord.ButtonComponent); ok && btn.Label == "3 Kicked" {
					haveButton = true
				}
			}
		}
	}
	if !haveText || !haveThumb || !haveButton {
		t.Fatalf("container missing pieces: text=%v thumbnail=%v button=%v", haveText, haveThumb, haveButton)
	}
}

func TestIntroMessage(t *testing.T) {
	if !strings.Contains(IntroMessage(true), "Ban Members") {
		t.Fatal("intro should warn about missing Ban Members permission")
	}
	if strings.Contains(IntroMessage(false), "Ban Members permission") {
		t.Fatal("intro should not warn when permission present")
	}
}
