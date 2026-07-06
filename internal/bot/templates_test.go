package bot

import (
	"strings"
	"testing"

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
	if got := CounterButtonLabel(0); got != "0 users honeypot'd" {
		t.Fatalf("got %q", got)
	}
	if got := CounterButtonLabel(7); got != "7 users honeypot'd" {
		t.Fatalf("got %q", got)
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
