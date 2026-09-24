package negcache

import (
	"fmt"
	"sync"
	"time"
)

// Checker is an independent state machine that observes cache stores and log
// records and verifies the invariants:
//
//  1. a cached success is never overwritten by a late not-found carrying an
//     older-or-equal source version;
//  2. consecutive temporary failures back off monotonically up to the cap;
//  3. every entry honors the hard TTL (ExpiresAt <= HardExpireAt and the
//     hard window never exceeds HardTTL);
//  4. a cancellation is never logged as a source error.
//
// Wire it with Config.OnStore and Config.Log.
type Checker struct {
	mu         sync.Mutex
	hardTTL    time.Duration
	tempMaxTTL time.Duration
	last       map[ckey]EntrySnapshot
	violations []string
}

func NewChecker(hardTTL, tempMaxTTL time.Duration) *Checker {
	return &Checker{hardTTL: hardTTL, tempMaxTTL: tempMaxTTL, last: make(map[ckey]EntrySnapshot)}
}

// OnStore implements the Config.OnStore hook.
func (c *Checker) OnStore(prev *EntrySnapshot, next EntrySnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := ckey{next.Tenant, next.Key}

	// Invariant 3: hard TTL.
	if next.ExpiresAt.After(next.HardExpireAt) {
		c.add("hard-ttl: %s expires %s after hard expiry %s", k, next.ExpiresAt, next.HardExpireAt)
	}
	if next.HardExpireAt.Sub(next.StoredAt) > c.hardTTL {
		c.add("hard-ttl: %s hard window %s exceeds HardTTL %s", k, next.HardExpireAt.Sub(next.StoredAt), c.hardTTL)
	}

	// Invariant 1: success must not be overwritten by a late not-found.
	if prev != nil && prev.Success && !next.Success &&
		next.Class == ClassNotFound && next.SourceVersion <= prev.SourceVersion {
		c.add("race: %s success v%d overwritten by late not-found v%d",
			k, prev.SourceVersion, next.SourceVersion)
	}

	// Invariant 2: temporary-failure backoff is monotonic up to the cap.
	if prev != nil && !prev.Success && prev.Class == ClassTemporary &&
		!next.Success && next.Class == ClassTemporary {
		prevTTL := prev.ExpiresAt.Sub(prev.StoredAt)
		nextTTL := next.ExpiresAt.Sub(next.StoredAt)
		if nextTTL < prevTTL {
			c.add("backoff: %s temporary TTL shrank %s -> %s", k, prevTTL, nextTTL)
		}
		if nextTTL > c.tempMaxTTL {
			c.add("backoff: %s temporary TTL %s exceeds cap %s", k, nextTTL, c.tempMaxTTL)
		}
	}

	c.last[k] = next
}

// OnLog wraps a Logger and checks that cancellations are never misreported
// as source errors.
func (c *Checker) OnLog(le LogEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if le.Cancellation && le.SourceErr {
		c.add("log: cancellation for %s/%s misreported as source error", le.Tenant, le.Key)
	}
}

// Logger returns a Logger that checks le and forwards it to sink.
func (c *Checker) Logger(sink Logger) Logger {
	return func(le LogEntry) {
		c.OnLog(le)
		if sink != nil {
			sink(le)
		}
	}
}

// Violations returns all invariant violations observed so far.
func (c *Checker) Violations() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.violations))
	copy(out, c.violations)
	return out
}

func (c *Checker) add(format string, args ...any) {
	c.violations = append(c.violations, fmt.Sprintf(format, args...))
}
