package bot

import (
	"hash/fnv"
	"sort"
	"strconv"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/bkan0n/joeshoneypot/internal/cache"
	"github.com/bkan0n/joeshoneypot/internal/store"
)

// The cross-channel image-spam detector: spam bots blast the same set of
// images into many channels within seconds. Each attachment-bearing message
// is fingerprinted by metadata only; when the same (user, fingerprint)
// appears in spamChannelThreshold distinct channels within spamWindow, the
// user is moderated with the guild's configured action.
const (
	spamWindow           = 30 * time.Minute // sliding: refreshed on every sighting
	spamMinAttachments   = 2                // messages with fewer attachments are ignored
	spamChannelThreshold = 2                // distinct channels that trigger moderation
)

// spamKey identifies one user's repeated posting of one attachment set.
type spamKey struct {
	GuildID     snowflake.ID
	UserID      snowflake.ID
	Fingerprint uint64
}

// spamFingerprint hashes a message's attachment metadata — sorted
// (filename, size) pairs — into a stable identity. Attachment bytes are
// never downloaded and message text is never read.
func spamFingerprint(atts []discord.Attachment) uint64 {
	parts := make([]string, len(atts))
	for i, a := range atts {
		// \x00 separates name from size so ("a1", 1) never collides with ("a", 11).
		parts[i] = a.Filename + "\x00" + strconv.Itoa(a.Size)
	}
	sort.Strings(parts)
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0xff}) // separate pairs
	}
	return h.Sum64()
}

// spamEligible reports whether a message and guild config qualify for spam
// tracking: enough attachments, detection enabled, and an action that bans.
func spamEligible(numAttachments int, cfg *store.Config) bool {
	if numAttachments < spamMinAttachments {
		return false
	}
	if cfg == nil || !cfg.SpamDetection {
		return false
	}
	return cfg.Action == store.ActionSoftban || cfg.Action == store.ActionBan
}

// recordSpamSighting adds channelID to the fingerprint's channel set and
// returns the number of distinct channels seen within the window.
func recordSpamSighting(c *cache.TTL[spamKey, map[snowflake.ID]struct{}], k spamKey, channelID snowflake.ID) int {
	n := 0
	c.Update(k, spamWindow, func(set map[snowflake.ID]struct{}) map[snowflake.ID]struct{} {
		if set == nil {
			set = map[snowflake.ID]struct{}{}
		}
		set[channelID] = struct{}{}
		n = len(set)
		return set
	})
	return n
}

// checkSpam runs for every guild message outside the honeypot channel
// (author already filtered to non-bot, non-system by isTriggerMessage).
// Exempt users are skipped silently — admins legitimately repost images.
func (b *Bot) checkSpam(e *events.MessageCreate, guildID snowflake.ID) {
	msg := e.Message
	if len(msg.Attachments) < spamMinAttachments {
		return
	}
	cfg, err := b.store.GetConfig(b.ctx, guildID)
	if err != nil {
		b.log.Error("loading config for spam check", "guild", guildID, "err", err)
		return
	}
	if !spamEligible(len(msg.Attachments), cfg) {
		return
	}

	key := spamKey{GuildID: guildID, UserID: msg.Author.ID, Fingerprint: spamFingerprint(msg.Attachments)}
	if recordSpamSighting(b.spamSightings, key, e.ChannelID) < spamChannelThreshold {
		return
	}

	dk := dedupKey{GuildID: guildID, UserID: msg.Author.ID}
	if !b.dedup.SetIfAbsent(dk, struct{}{}, 30*time.Second) {
		return
	}
	defer b.dedup.Delete(dk) // allow re-punishing a rejoining user
	// Consume the trigger so a re-offense needs two fresh channel sightings,
	// not just another message riding the same crossed-threshold state.
	b.spamSightings.Delete(key)

	inputs := b.gatherExemptionInputs(guildID, msg)
	if isExempt(msg.Author.ID, inputs.OwnerID, inputs.MemberRoles, func(roleID snowflake.ID) bool {
		role, ok := b.client.Caches.Role(guildID, roleID)
		return ok && isAdminRole(role)
	}) {
		return
	}
	b.moderate(decideModeration(cfg.Action, false), cfg, e.ChannelID, msg, inputs.GuildName, triggerSpam)
}
