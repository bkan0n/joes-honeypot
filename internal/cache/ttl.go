// Package cache provides a minimal concurrency-safe TTL map. It replaces the
// Redis caches of the original honeypot bot; entries are evicted lazily on Get.
package cache

import (
	"sync"
	"time"
)

type entry[V any] struct {
	val       V
	expiresAt time.Time
}

type TTL[K comparable, V any] struct {
	mu sync.Mutex
	m  map[K]entry[V]
}

func NewTTL[K comparable, V any]() *TTL[K, V] {
	return &TTL[K, V]{m: make(map[K]entry[V])}
}

func (c *TTL[K, V]) Set(k K, v V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = entry[V]{val: v, expiresAt: time.Now().Add(ttl)}
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

func (c *TTL[K, V]) Delete(k K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
}
