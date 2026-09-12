// Package ratelimit implements a simple per-key (per client IP) in-memory
// rate limiter built on golang.org/x/time/rate, with stale-entry eviction so
// the map does not grow without bound.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	gcInterval   = time.Minute
	entryMaxIdle = 10 * time.Minute
)

type entry struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// Limiter rate-limits keys at perMin events per minute with a burst equal to
// perMin (i.e. a full minute's budget may be spent immediately).
type Limiter struct {
	mu      sync.Mutex
	perMin  int
	entries map[string]*entry
	lastGC  time.Time
}

// New creates a Limiter allowing perMin events per minute per key.
func New(perMin int) *Limiter {
	if perMin < 1 {
		perMin = 1
	}
	return &Limiter{
		perMin:  perMin,
		entries: make(map[string]*entry),
		lastGC:  time.Now(),
	}
}

// Allow reports whether the key may proceed now. If not, it returns a
// suggested Retry-After duration (> 0).
func (l *Limiter) Allow(key string) (retryAfter time.Duration, ok bool) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.gcLocked(now)

	e, ok := l.entries[key]
	if !ok {
		e = &entry{
			lim: rate.NewLimiter(rate.Limit(float64(l.perMin)/60.0), l.perMin),
		}
		l.entries[key] = e
	}
	e.lastSeen = now

	// Reserve instead of Allow so we can report an accurate Retry-After.
	res := e.lim.Reserve()
	if res == nil || !res.OK() {
		// Reservation impossible (burst exhausted with no future capacity);
		// suggest waiting a full window.
		return time.Minute, false
	}
	delay := res.Delay()
	if delay > 0 {
		res.Cancel()
		return delay, false
	}
	return 0, true
}

// gcLocked evicts entries that have not been seen recently. It runs at most
// once per gcInterval; l.mu must be held.
func (l *Limiter) gcLocked(now time.Time) {
	if now.Sub(l.lastGC) < gcInterval {
		return
	}
	l.lastGC = now
	for k, e := range l.entries {
		if now.Sub(e.lastSeen) > entryMaxIdle {
			delete(l.entries, k)
		}
	}
}
