// Package ratelimit is an in-memory keyed token-bucket limiter, used for
// per-IP and per-identifier login throttling. Storage is in-process — fine
// for the single-replica self-hosted baseline. If we ever need to share
// state across replicas, swap the backing store; the Allow() interface
// stays the same.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter is a thread-safe map of token buckets keyed by string.
//
// Eviction is lazy: each call to Allow opportunistically prunes entries
// idle for longer than idleTTL. Bounded map size, no background goroutine,
// no Close to remember.
type Limiter struct {
	mu           sync.Mutex
	buckets      map[string]*entry
	every        rate.Limit
	burst        int
	idleTTL      time.Duration
	lastEviction time.Time
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// New constructs a Limiter that refills `burst` tokens at rate `every` and
// evicts entries idle for longer than `idleTTL`.
func New(every rate.Limit, burst int, idleTTL time.Duration) *Limiter {
	return &Limiter{
		buckets:      make(map[string]*entry),
		every:        every,
		burst:        burst,
		idleTTL:      idleTTL,
		lastEviction: time.Now(),
	}
}

// Allow consumes a token for `key`. Returns true if allowed, false if the
// bucket is empty.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	// Evict at most twice per idleTTL to keep map size bounded without
	// O(N) work on the hot path.
	if now.Sub(l.lastEviction) > l.idleTTL/2 {
		l.evictIdleLocked(now)
		l.lastEviction = now
	}

	e, ok := l.buckets[key]
	if !ok {
		e = &entry{limiter: rate.NewLimiter(l.every, l.burst)}
		l.buckets[key] = e
	}
	e.lastSeen = now
	return e.limiter.Allow()
}

// Size returns the number of live buckets. Test/observability hook.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func (l *Limiter) evictIdleLocked(now time.Time) {
	cutoff := now.Add(-l.idleTTL)
	for k, e := range l.buckets {
		if e.lastSeen.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}
