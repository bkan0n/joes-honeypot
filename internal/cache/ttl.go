// Package cache provides a minimal concurrency-safe TTL map. It replaces the
// Redis caches of the original honeypot bot; entries are evicted lazily on
// Get, plus an opportunistic full sweep every sweepInterval stores so that
// write-once keys that are never read again (and the map buckets holding
// them) don't accumulate forever in a long-running process.
package cache

import (
	"sync"
	"time"
)

// sweepInterval is how many stores go by between opportunistic sweeps of
// expired entries. The sweep is O(len(map)) under the lock, so amortized
// cost per store stays negligible.
const sweepInterval = 128

type entry[V any] struct {
	val       V
	expiresAt time.Time
}

// TTL is a concurrency-safe map whose entries expire after a per-entry
// duration. The zero value is not usable; create one with NewTTL.
type TTL[K comparable, V any] struct {
	mu     sync.Mutex
	m      map[K]entry[V]
	stores int // stores since the last sweep
}

func NewTTL[K comparable, V any]() *TTL[K, V] {
	return &TTL[K, V]{m: make(map[K]entry[V])}
}

func (c *TTL[K, V]) Set(k K, v V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store(k, v, ttl)
}

// store adds an entry and occasionally sweeps expired ones; c.mu must be held.
func (c *TTL[K, V]) store(k K, v V, ttl time.Duration) {
	c.m[k] = entry[V]{val: v, expiresAt: time.Now().Add(ttl)}
	c.stores++
	if c.stores < sweepInterval {
		return
	}
	c.stores = 0
	now := time.Now()
	for key, e := range c.m {
		if now.After(e.expiresAt) {
			delete(c.m, key)
		}
	}
}

func (c *TTL[K, V]) Get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Now().After(e.expiresAt) {
		delete(c.m, k)
		var zero V
		return zero, false
	}
	return e.val, true
}

// SetIfAbsent stores v under k only if k is absent or expired, returning
// true if it stored. It is the atomic check-and-set used for dedup guards.
func (c *TTL[K, V]) SetIfAbsent(k K, v V, ttl time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[k]; ok && !time.Now().After(e.expiresAt) {
		return false
	}
	c.store(k, v, ttl)
	return true
}

// Update applies fn to the current value for k — or the zero value of V if
// k is absent or expired — and stores the result with a fresh ttl. fn runs
// under the cache lock: keep it short and never call back into the cache.
func (c *TTL[K, V]) Update(k K, ttl time.Duration, fn func(V) V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var cur V
	if e, ok := c.m[k]; ok && !time.Now().After(e.expiresAt) {
		cur = e.val
	}
	c.store(k, fn(cur), ttl)
}

func (c *TTL[K, V]) Delete(k K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
}
