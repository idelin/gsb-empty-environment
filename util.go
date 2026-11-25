package workloadid

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Time and Duration re-export the standard time types so the public API has a
// single, injectable clock model.
type Time = time.Time
type Duration = time.Duration

// Clock is the time source used by the verifier. Production code uses
// RealClock; tests inject a controllable fake clock to exercise rotation,
// grace windows and clock-rollback interception deterministically.
type Clock interface {
	Now() Time
}

// RealClock reads the wall clock.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() Time { return time.Now() }

// atomicTime is a concurrency-safe monotonic wall-clock observation slot used
// to detect clock rollback across bundle generations.
type atomicTime struct {
	value atomic.Value
}

func (a *atomicTime) load() Time {
	if v := a.value.Load(); v != nil {
		return v.(Time)
	}
	var zero Time
	return zero
}

func (a *atomicTime) store(t Time) {
	a.value.Store(t)
}

// parseIPStrict rejects anything net.ParseIP would treat as a non-IP and
// normalizes the value to a canonical string.
func parseIPStrict(value string) net.IP {
	ip := net.ParseIP(value)
	if ip == nil {
		return nil
	}
	return ip
}

// memoryNonceCache is a simple concurrency-safe NonceCache with time based
// eviction suitable for a single control-plane process.
type memoryNonceCache struct {
	mu     sync.Mutex
	seen   map[string]Time
	maxAge Duration
}

// NewMemoryNonceCache returns an in-memory NonceCache. Entries older than maxAge
// are pruned opportunistically.
func NewMemoryNonceCache(maxAge Duration) NonceCache {
	return &memoryNonceCache{seen: make(map[string]Time), maxAge: maxAge}
}

func (c *memoryNonceCache) Seen(nonce string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.seen[nonce]
	return ok
}

func (c *memoryNonceCache) Remember(nonce string, at Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(at)
	c.seen[nonce] = at
}

func (c *memoryNonceCache) prune(now Time) {
	if c.maxAge <= 0 {
		return
	}
	for nonce, at := range c.seen {
		if now.Sub(at) > c.maxAge {
			delete(c.seen, nonce)
		}
	}
}
