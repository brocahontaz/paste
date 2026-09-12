package ratelimit

import (
	"testing"
	"time"
)

func TestAllowBurstThenDeny(t *testing.T) {
	l := New(2) // 2 per minute, burst 2

	for i := 0; i < 2; i++ {
		if _, ok := l.Allow("a"); !ok {
			t.Fatalf("request %d for key a denied, want allowed", i+1)
		}
	}
	retry, ok := l.Allow("a")
	if ok {
		t.Fatal("request 3 for key a allowed, want denied")
	}
	if retry <= 0 {
		t.Fatalf("retryAfter = %v, want > 0", retry)
	}

	// A different key has its own budget.
	if _, ok := l.Allow("b"); !ok {
		t.Fatal("request 1 for key b denied, want allowed")
	}
}

func TestPerKeyIsolation(t *testing.T) {
	l := New(1)
	if _, ok := l.Allow("ip1"); !ok {
		t.Fatal("ip1 first request denied")
	}
	if _, ok := l.Allow("ip2"); !ok {
		t.Fatal("ip2 first request denied")
	}
	if _, ok := l.Allow("ip1"); ok {
		t.Fatal("ip1 second request allowed, want denied")
	}
}

func TestEviction(t *testing.T) {
	l := New(5)
	l.Allow("old")
	l.Allow("fresh")

	// Age the "old" entry beyond the idle window and let the GC interval
	// elapse so the next gcLocked call actually sweeps.
	l.mu.Lock()
	l.entries["old"].lastSeen = time.Now().Add(-entryMaxIdle - time.Minute)
	l.lastGC = time.Now().Add(-2 * gcInterval)
	l.mu.Unlock()

	l.gcLocked(time.Now())

	l.mu.Lock()
	_, oldExists := l.entries["old"]
	_, freshExists := l.entries["fresh"]
	l.mu.Unlock()

	if oldExists {
		t.Error("stale entry was not evicted")
	}
	if !freshExists {
		t.Error("fresh entry was evicted")
	}
}

func TestGCSkippedWhenRecent(t *testing.T) {
	l := New(5)
	l.Allow("a")
	l.Allow("b")
	// lastGC was just set; a gc attempt now must be a no-op even with aged
	// entries.
	l.mu.Lock()
	l.entries["b"].lastSeen = time.Now().Add(-entryMaxIdle - time.Minute)
	l.mu.Unlock()

	l.gcLocked(time.Now().Add(-30 * time.Second)) // earlier than lastGC

	l.mu.Lock()
	_, bExists := l.entries["b"]
	l.mu.Unlock()
	if !bExists {
		t.Error("gc ran too early and evicted an entry")
	}
}
