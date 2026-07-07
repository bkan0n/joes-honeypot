package bot

import (
	"errors"
	"time"

	"github.com/disgoorg/disgo/rest"
)

// Ban/unban REST calls get a few attempts because disgo only auto-retries
// 429s: a Discord 5xx or a network blip during AddBan would otherwise leave
// the spammer in the server until they post again.
const (
	banRetryAttempts = 3
	banRetryBackoff  = 500 * time.Millisecond
)

// isTransientRestError reports whether a failed REST call is worth retrying:
// a network-level failure (no *rest.Error means no Discord response at all)
// or a Discord 5xx. Everything else — permission errors, unknown resources,
// and 429s (which disgo's rate limiter already retried) — is permanent.
func isTransientRestError(err error) bool {
	var restErr *rest.Error
	if !errors.As(err, &restErr) {
		return true
	}
	return restErr.Response != nil && restErr.Response.StatusCode >= 500
}

// retryTransient runs fn up to attempts times, doubling backoff between
// tries, and stops early on success or a non-transient error. It returns
// fn's last error.
func (b *Bot) retryTransient(op string, attempts int, backoff time.Duration, fn func() error) error {
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil || !isTransientRestError(err) || attempt == attempts {
			return err
		}
		b.Log.Warn(op+" failed, retrying", "attempt", attempt, "backoff", backoff, "err", err)
		time.Sleep(backoff)
		backoff *= 2
	}
}
