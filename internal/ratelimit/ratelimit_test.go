package ratelimit_test

import (
	"testing"
	"time"

	"golang.org/x/time/rate"

	"zymobrew/internal/ratelimit"
)

func TestAllow_BurstThenDeny(t *testing.T) {
	// One token per minute, burst 3 — gives us 3 immediate Allows, then deny.
	l := ratelimit.New(rate.Every(time.Minute), 3, time.Hour)
	for i := 0; i < 3; i++ {
		if !l.Allow("alice") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if l.Allow("alice") {
		t.Fatal("4th attempt should be denied")
	}
}

func TestAllow_SeparateKeysIndependent(t *testing.T) {
	l := ratelimit.New(rate.Every(time.Minute), 2, time.Hour)
	if !l.Allow("alice") || !l.Allow("alice") {
		t.Fatal("alice should burn through two tokens")
	}
	if l.Allow("alice") {
		t.Fatal("alice should be exhausted")
	}
	// bob has his own bucket.
	if !l.Allow("bob") || !l.Allow("bob") {
		t.Fatal("bob should still have tokens")
	}
}

func TestAllow_LazyEvictionShrinksMap(t *testing.T) {
	// idleTTL=20ms, eviction window=10ms. Populate, sleep past idleTTL,
	// then a fresh Allow() should evict the stale entry.
	l := ratelimit.New(rate.Every(time.Minute), 1, 20*time.Millisecond)
	l.Allow("ghost")
	if l.Size() != 1 {
		t.Fatalf("expected 1 bucket, got %d", l.Size())
	}
	time.Sleep(30 * time.Millisecond)
	l.Allow("alice") // triggers eviction window check
	if l.Size() != 1 {
		t.Fatalf("expected ghost evicted and only alice remaining, got Size=%d", l.Size())
	}
}
