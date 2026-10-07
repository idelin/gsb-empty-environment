// Package ttlcache implements a TTL cache driven by an explicit monotonic
// clock. Time never advances on its own: callers advance it with Advance,
// which makes expiration deterministic, testable, and safe to persist and
// restore across process downtime.
package ttlcache

import (
	"errors"
	"sort"
	"time"
)

// ErrInvalidTTL is returned when a non-positive TTL is supplied.
var ErrInvalidTTL = errors.New("ttlcache: ttl must be positive")

type entry struct {
	value     interface{}
	gen       uint64
	expiresAt time.Duration
}

// Cache is a TTL cache whose only notion of time is the explicit monotonic
// clock it holds internally. The clock starts at zero and moves forward
// exclusively through Advance.
type Cache struct {
	now        time.Duration
	entries    map[string]*entry
	genCounter uint64
	events     []Event
}

// New returns an empty cache whose monotonic clock reads zero.
func New() *Cache {
	return &Cache{entries: make(map[string]*entry)}
}

// Now reports the cache's current monotonic time.
func (c *Cache) Now() time.Duration { return c.now }

// Set stores value under key with the given TTL. Every Set starts a new
// generation for the key: any expiry or delete event still referring to an
// older generation must not affect this value.
func (c *Cache) Set(key string, value interface{}, ttl time.Duration) (uint64, error) {
	if ttl <= 0 {
		return 0, ErrInvalidTTL
	}
	c.genCounter++
	gen := c.genCounter
	c.entries[key] = &entry{value: value, gen: gen, expiresAt: c.now + ttl}
	c.events = append(c.events, Event{
		Kind: EventSet, Key: key, Value: value, Gen: gen, At: c.now, TTL: ttl,
	})
	return gen, nil
}

// Get returns the value for key if it exists and has not expired at the
// cache's current time.
func (c *Cache) Get(key string) (interface{}, bool) {
	e, ok := c.entries[key]
	if !ok || e.expiresAt <= c.now {
		return nil, false
	}
	return e.value, true
}

// Delete removes key and records a delete event tagged with the key's
// current generation.
func (c *Cache) Delete(key string) bool {
	e, ok := c.entries[key]
	if !ok {
		return false
	}
	delete(c.entries, key)
	c.events = append(c.events, Event{Kind: EventDelete, Key: key, Gen: e.gen, At: c.now})
	return true
}

// Advance moves the monotonic clock forward by d and expires every entry
// whose deadline falls at or before the new time. Expiry events are stamped
// with the entry's own deadline, so replaying the log reproduces expirations
// at their true instants.
func (c *Cache) Advance(d time.Duration) []Event {
	if d < 0 {
		d = 0
	}
	c.now += d

	var expired []*entry
	keys := make(map[*entry]string)
	for k, e := range c.entries {
		if e.expiresAt <= c.now {
			expired = append(expired, e)
			keys[e] = k
		}
	}
	// Deterministic event order: by deadline, then by key.
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].expiresAt != expired[j].expiresAt {
			return expired[i].expiresAt < expired[j].expiresAt
		}
		return keys[expired[i]] < keys[expired[j]]
	})

	var fired []Event
	for _, e := range expired {
		k := keys[e]
		delete(c.entries, k)
		ev := Event{Kind: EventExpire, Key: k, Gen: e.gen, At: e.expiresAt}
		c.events = append(c.events, ev)
		fired = append(fired, ev)
	}
	return fired
}

// Events returns a copy of the cache's full event log.
func (c *Cache) Events() []Event {
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}

// Items snapshots the live (non-expired) key/value pairs at the cache's
// current time.
func (c *Cache) Items() map[string]interface{} {
	out := make(map[string]interface{})
	for k, e := range c.entries {
		if e.expiresAt > c.now {
			out[k] = e.value
		}
	}
	return out
}
