package cache

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	c := NewTTL[string, int]()
	c.Set("a", 1, time.Minute)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("got %v %v, want 1 true", v, ok)
	}
}

func TestExpiry(t *testing.T) {
	c := NewTTL[string, int]()
	c.Set("a", 1, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected expired entry to be absent")
	}
}

func TestDelete(t *testing.T) {
	c := NewTTL[string, int]()
	c.Set("a", 1, time.Minute)
	c.Delete("a")
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected deleted entry to be absent")
	}
}

func TestMissing(t *testing.T) {
	c := NewTTL[string, int]()
	if v, ok := c.Get("nope"); ok || v != 0 {
		t.Fatalf("got %v %v, want 0 false", v, ok)
	}
}

func TestOpportunisticSweep(t *testing.T) {
	t.Run("Set sweeps expired entries", func(t *testing.T) {
		c := NewTTL[int, int]()
		c.Set(-1, 0, time.Nanosecond) // expires immediately, never Get()ed
		time.Sleep(time.Millisecond)
		for i := 0; i < sweepInterval; i++ {
			c.Set(i, i, time.Minute)
		}
		c.mu.Lock()
		_, survived := c.m[-1]
		size := len(c.m)
		c.mu.Unlock()
		if survived {
			t.Fatal("expired entry survived the opportunistic sweep")
		}
		if size != sweepInterval {
			t.Fatalf("len = %d, want %d live entries", size, sweepInterval)
		}
	})

	t.Run("SetIfAbsent sweeps too", func(t *testing.T) {
		c := NewTTL[int, int]()
		c.Set(-1, 0, time.Nanosecond)
		time.Sleep(time.Millisecond)
		for i := 0; i < sweepInterval; i++ {
			c.SetIfAbsent(i, i, time.Minute)
		}
		c.mu.Lock()
		_, survived := c.m[-1]
		c.mu.Unlock()
		if survived {
			t.Fatal("expired entry survived the opportunistic sweep")
		}
	})

	t.Run("live entries survive", func(t *testing.T) {
		c := NewTTL[int, int]()
		for i := 0; i < 3*sweepInterval; i++ {
			c.Set(i, i, time.Minute)
		}
		if v, ok := c.Get(0); !ok || v != 0 {
			t.Fatalf("live entry lost by sweep: got %v %v", v, ok)
		}
	})
}

func TestSetIfAbsent(t *testing.T) {
	t.Run("stores when missing", func(t *testing.T) {
		c := NewTTL[string, int]()
		if ok := c.SetIfAbsent("a", 1, time.Minute); !ok {
			t.Fatal("expected SetIfAbsent to store and return true when key is missing")
		}
		if v, ok := c.Get("a"); !ok || v != 1 {
			t.Fatalf("got %v %v, want 1 true", v, ok)
		}
	})

	t.Run("refuses when present", func(t *testing.T) {
		c := NewTTL[string, int]()
		c.Set("a", 1, time.Minute)
		if ok := c.SetIfAbsent("a", 2, time.Minute); ok {
			t.Fatal("expected SetIfAbsent to return false when key is present")
		}
		if v, ok := c.Get("a"); !ok || v != 1 {
			t.Fatalf("expected original value to remain, got %v %v", v, ok)
		}
	})

	t.Run("stores again after expiry", func(t *testing.T) {
		c := NewTTL[string, int]()
		c.Set("a", 1, 10*time.Millisecond)
		time.Sleep(20 * time.Millisecond)
		if ok := c.SetIfAbsent("a", 2, time.Minute); !ok {
			t.Fatal("expected SetIfAbsent to store and return true after expiry")
		}
		if v, ok := c.Get("a"); !ok || v != 2 {
			t.Fatalf("got %v %v, want 2 true", v, ok)
		}
	})

	t.Run("concurrency: exactly one winner", func(t *testing.T) {
		c := NewTTL[string, int]()
		const n = 100
		var wg sync.WaitGroup
		var wins int64
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if c.SetIfAbsent("race", i, time.Minute) {
					atomic.AddInt64(&wins, 1)
				}
			}(i)
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("expected exactly 1 winner, got %d", wins)
		}
	})
}
