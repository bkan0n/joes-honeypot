package bot

import (
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"github.com/bkan0n/joeshoneypot/internal/store"
)

func TestDMMessage(t *testing.T) {
	dm := dmMessage(store.ActionSoftban, "My Server", triggerHoneypot)
	if !strings.Contains(dm, "kicked") || !strings.Contains(dm, "My Server") {
		t.Fatalf("softban DM wrong: %q", dm)
	}
	if dm := dmMessage(store.ActionBan, "My Server", triggerHoneypot); !strings.Contains(dm, "banned") {
		t.Fatalf("ban DM wrong: %q", dm)
	}
}

func TestLogMessage(t *testing.T) {
	msg := logMessage(42, store.ActionBan, triggerHoneypot)
	if !strings.Contains(msg, "<@42>") || !strings.Contains(msg, "banned") {
		t.Fatalf("log message wrong: %q", msg)
	}
}

func TestTriggerKindWording(t *testing.T) {
	dm := dmMessage(store.ActionBan, "My Server", triggerSpam)
	if !strings.Contains(dm, "Spam Detected") || !strings.Contains(dm, "same images in multiple channels") {
		t.Errorf("spam DM missing spam wording: %q", dm)
	}
	if dm := dmMessage(store.ActionBan, "My Server", triggerHoneypot); !strings.Contains(dm, "honeypot channel") {
		t.Errorf("honeypot DM missing honeypot wording: %q", dm)
	}
	lg := logMessage(42, store.ActionSoftban, triggerSpam)
	if !strings.Contains(lg, "<@42>") || !strings.Contains(lg, "same images in multiple channels") {
		t.Errorf("spam log missing spam wording: %q", lg)
	}
	if r := triggerSpam.banReason(); !strings.Contains(r, "Joe's Honeypot") {
		t.Errorf("ban reason must identify the bot: %q", r)
	}
}

func TestCounterButtonLabel(t *testing.T) {
	if got := counterButtonLabel(0); got != "0 Kicked" {
		t.Fatalf("got %q", got)
	}
	if got := counterButtonLabel(7); got != "7 Kicked" {
		t.Fatalf("got %q", got)
	}
}

func TestWarningMessageHasNoEmDash(t *testing.T) {
	if strings.Contains(warningMessage(), "—") {
		t.Fatalf("warning message contains an em dash: %q", warningMessage())
	}
}

func TestWarningMessageComponents(t *testing.T) {
	comps := warningMessageComponents(3)
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
				if td, ok := sc.(discord.TextDisplayComponent); ok && td.Content == warningMessage() {
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
					if !btn.Disabled {
						t.Error("counter button must be disabled (display only)")
					}
				}
			}
		}
	}
	if !haveText || !haveThumb || !haveButton {
		t.Fatalf("container missing pieces: text=%v thumbnail=%v button=%v", haveText, haveThumb, haveButton)
	}
}

func TestIntroMessage(t *testing.T) {
	if !strings.Contains(introMessage(true), "Ban Members") {
		t.Fatal("intro should warn about missing Ban Members permission")
	}
	if strings.Contains(introMessage(false), "Ban Members permission") {
		t.Fatal("intro should not warn when permission present")
	}
}
