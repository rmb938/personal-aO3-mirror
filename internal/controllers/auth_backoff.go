package controllers

import (
	"container/list"
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// Lockout (docs/part2-api.md stage 16): each failed password or second-factor attempt doubles
// the account's wait, from 1s up to 15m; a full login resets it. MaxFailedAttempts in a row
// disable the account until an admin enables it. An unknown username gets the same backoff from
// an in-memory LRU keyed by its HMAC, so responses never say whether an account exists.
const (
	backoffBase = time.Second
	backoffMax  = 15 * time.Minute
)

// backoffDelay is the wait after the given number of consecutive failures.
func backoffDelay(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	d := backoffBase
	for i := 1; i < failures && d < backoffMax; i++ {
		d *= 2
	}
	return min(d, backoffMax)
}

// nextAttempt is when the next attempt may come after failures, rounded up to the whole second
// users.db stores (so the wait is never shortened).
func nextAttempt(now time.Time, failures int) time.Time {
	t := now.Add(backoffDelay(failures))
	if r := t.Truncate(time.Second); !r.Equal(t) {
		return r.Add(time.Second)
	}
	return t
}

// waitFor returns the wait until next, rounded up to whole seconds (Retry-After), and whether
// there is one.
func waitFor(next, now time.Time) (time.Duration, bool) {
	if !now.Before(next) {
		return 0, false
	}
	d := next.Sub(now)
	if r := d.Truncate(time.Second); r != d {
		d = r + time.Second
	}
	return d, true
}

// recordFailure counts a failed attempt against u (as read in tx), sets its backoff and, at
// MaxFailedAttempts, disables it.
func (a *Auth) recordFailure(ctx context.Context, tx *store.UsersTx, u model.User, now time.Time) error {
	n := u.FailedAttempts + 1
	if err := tx.SetUserAttempts(ctx, u.ID, n, nextAttempt(now, n)); err != nil {
		return err
	}
	if n >= MaxFailedAttempts && !u.Disabled() {
		if err := tx.DisableUser(ctx, u.ID, now); err != nil {
			return err
		}
		a.event(ctx, slog.LevelWarn, "account_disabled", "user_id", u.ID, "reason", "failed_attempts",
			"failed_attempts", n)
	}
	return nil
}

// nameBackoff is the backoff of unknown usernames, by NameMAC, least recently used evicted.
type nameBackoff struct {
	mu  sync.Mutex
	max int
	ll  *list.List // front = most recent; values are *nameEntry
	m   map[[32]byte]*list.Element
}

type nameEntry struct {
	key      [32]byte
	failures int
	next     time.Time
}

func newNameBackoff(max int) *nameBackoff {
	return &nameBackoff{max: max, ll: list.New(), m: map[[32]byte]*list.Element{}}
}

// next returns when the next attempt for key may come (zero when none is remembered).
func (b *nameBackoff) next(key [32]byte) time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e, ok := b.m[key]; ok {
		b.ll.MoveToFront(e)
		return e.Value.(*nameEntry).next
	}
	return time.Time{}
}

// fail counts a failed attempt for key, exactly as recordFailure does for an account.
func (b *nameBackoff) fail(key [32]byte, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.m[key]
	if !ok {
		e = b.ll.PushFront(&nameEntry{key: key})
		b.m[key] = e
		for b.ll.Len() > b.max {
			old := b.ll.Back()
			b.ll.Remove(old)
			delete(b.m, old.Value.(*nameEntry).key)
		}
	} else {
		b.ll.MoveToFront(e)
	}
	ne := e.Value.(*nameEntry)
	ne.failures++
	ne.next = nextAttempt(now, ne.failures)
}
