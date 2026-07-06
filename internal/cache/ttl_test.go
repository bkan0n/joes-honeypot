package cache

import (
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
