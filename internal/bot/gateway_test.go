package bot

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/json/v2"
	"github.com/disgoorg/snowflake/v2"
)

// A type-20 server-hub channel in GUILD_CREATE caused disgo v0.19.6 to
// reject the whole event and reconnect repeatedly, exhausting Discord's
// session-start limit. Exercise the gateway decoder, not just a channel.
func TestGatewayGuildCreateWithUnknownChannel(t *testing.T) {
	const payload = `{
		"op": 0, "s": 2, "t": "GUILD_CREATE",
		"d": {
			"id": "100", "name": "Test server",
			"channels": [
				{"id": "101", "type": 0, "name": "honeypot", "permission_overwrites": []},
				{"id": "102", "type": 20, "name": "server-hub", "permission_overwrites": []}
			]
		}
	}`
	var message gateway.Message
	if err := json.Unmarshal([]byte(payload), &message); err != nil {
		t.Fatalf("decoding GUILD_CREATE with a type-20 channel: %v", err)
	}
	event, ok := message.D.(gateway.EventGuildCreate)
	if !ok {
		t.Fatalf("decoded event type = %T; want gateway.EventGuildCreate", message.D)
	}
	if event.ID != snowflake.ID(100) {
		t.Fatalf("guild ID = %v; want 100", event.ID)
	}
	if len(event.Channels) != 2 {
		t.Fatalf("decoded %d channels; want 2", len(event.Channels))
	}
	if channel := event.Channels[0]; channel == nil || channel.ID() != 101 || channel.Type() != discord.ChannelTypeGuildText {
		t.Fatalf("honeypot text channel was not preserved: %v", channel)
	}
	if channel := event.Channels[1]; channel == nil || channel.ID() != 102 || channel.Type() != discord.ChannelType(20) {
		t.Fatalf("unknown channel was not preserved: %v", channel)
	}
}
