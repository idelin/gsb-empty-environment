package cacheaudit

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Loader fetches the freshest value for key from the data source. It returns
// the value's source version and a release function that the cache calls
// exactly once when it stops holding the value (evict, replace, close).
type Loader func(ctx context.Context, key string) (version uint64, value any, release func(), err error)

// Options configures a Cache. All external effects are injectable functions:
// the data source (Load), the clock (Now) and failure injection (Fail).
// Nothing talks to a remote cache.
type Options struct {
	Load Loader
	Now  func() time.Time
	// Fail, when non-nil and returning a non-nil error, injects a failure.
	// Recognized op names: "load" (data source call) and "log" (audit
	// record append).
	Fail   func(op string) error
	LogCap int // bounded audit-log capacity; default 1024
}

// Stats are counters with a fixed hit-rate denominator: every Get call.
type Stats struct {
	Gets       uint64 // all Get calls (hits + misses + errors)
	Hits       uint64 // served from a published cache entry
	Misses     uint64 // not served from a published entry
	Errors     uint64 // calls that returned an error
	Loads      uint64 // successful data-source loads
	LoadErrors uint64 // failed data-source loads
	Publishes  uint64 // versions made visible
	Evicts     uint64 // entries evicted
	EventsLost uint64 // audit events that could not be recorded
}

// HitRate is Hits / Gets. The denominator is fixed: it always counts every
// Get call, including errors, so the ratio cannot be inflated by
// reclassifying calls.
func (s Stats) HitRate() float64 {
	if s.Gets == 0 {
		return 0
	}
	return float64(s.Hits) / float64(s.Gets)
}

// EvidenceComplete is false once any audit event had to be dropped.
func (s Stats) EvidenceComplete() bool { return s.EventsLost == 0 }

var ErrClosed = errors.New("cacheaudit: cache is closed")

type entry struct {
	version uint64
	value   any
	release func()
}

// inflight is an unpublished load: its value is not visible as a cache hit
// until it commits (publishes).
type inflight struct {
	done    chan struct{}
	version uint64
	value   any
	release func()
	err     error
}

// Cache is a versioned, single-flight cache that records every hit, miss,
// load, evict and publish into a bounded audit log.
type Cache struct {
	mu     sync.Mutex
	load   Loader
	now    func() time.Time
	fail   func(op string) error
	log    *Log
	live   map[string]*entry
	flight map[string]*inflight
	closed bool
	stats  Stats
}

// New builds a Cache from injectable options.
func New(opts Options) *Cache {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	cap := opts.LogCap
	if cap <= 0 {
		cap = 1024
	}
	return &Cache{
		load:   opts.Load,
		now:    now,
		fail:   opts.Fail,
		log:    NewLog(cap),
		live:   make(map[string]*entry),
		flight: make(map[string]*inflight),
	}
}

// record appends an audit event; an injected (or real) append failure is
// accounted as lost evidence, never silently swallowed.
func (c *Cache) record(e Event) {
	if c.fail != nil && c.fail("log") != nil {
		c.log.NoteLost(1)
		c.mu.Lock()
		c.stats.EventsLost++
		c.mu.Unlock()
		return
	}
	c.log.Append(e)
}

// Get returns the value and its source version for key.
func (c *Cache) Get(ctx context.Context, key string) (value any, version uint64, err error) {
	start := c.now()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, 0, ErrClosed
	}
	c.stats.Gets++
	if e, ok := c.live[key]; ok {
		c.stats.Hits++
		v, ver := e.value, e.version
		end := c.now()
		c.mu.Unlock()
		c.record(Event{Kind: KindCall, Key: key, Start: start, End: end, Version: ver, Outcome: OutcomeHit})
		return v, ver, nil
	}
	if f, ok := c.flight[key]; ok {
		// The load is in flight and unpublished: this call must not be
		// recorded as a hit even though it will observe the value.
		c.stats.Misses++
		c.mu.Unlock()
		select {
		case <-f.done:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
		end := c.now()
		if f.err != nil {
			c.mu.Lock()
			c.stats.Errors++
			c.mu.Unlock()
			c.record(Event{Kind: KindCall, Key: key, Start: start, End: end, Outcome: OutcomeError, Note: f.err.Error()})
			return nil, 0, f.err
		}
		c.record(Event{Kind: KindCall, Key: key, Start: start, End: end, Version: f.version, Outcome: OutcomeMiss, Note: "awaited-unpublished-load"})
		return f.value, f.version, nil
	}
	f := &inflight{done: make(chan struct{})}
	c.flight[key] = f
	c.mu.Unlock()

	ver, val, release, lerr := c.callLoad(ctx, key)
	loadEnd := c.now()

	c.mu.Lock()
	delete(c.flight, key)
	f.version, f.value, f.release, f.err = ver, val, release, lerr
	if lerr != nil {
		c.stats.LoadErrors++
		c.stats.Errors++
	} else {
		c.stats.Loads++
		c.stats.Misses++
		c.commitLocked(key, ver, val, release)
		c.stats.Publishes++
	}
	close(f.done)
	callEnd := c.now()
	c.mu.Unlock()

	if lerr != nil {
		c.record(Event{Kind: KindLoad, Key: key, Start: start, End: loadEnd, Note: lerr.Error()})
		c.record(Event{Kind: KindCall, Key: key, Start: start, End: callEnd, Outcome: OutcomeError, Note: lerr.Error()})
		return nil, 0, lerr
	}
	c.record(Event{Kind: KindLoad, Key: key, Start: start, End: loadEnd, Version: ver})
	c.record(Event{Kind: KindPublish, Key: key, At: loadEnd, Version: ver})
	c.record(Event{Kind: KindCall, Key: key, Start: start, End: callEnd, Version: ver, Outcome: OutcomeMiss})
	return val, ver, nil
}

func (c *Cache) callLoad(ctx context.Context, key string) (uint64, any, func(), error) {
	if c.fail != nil {
		if err := c.fail("load"); err != nil {
			return 0, nil, nil, err
		}
	}
	return c.load(ctx, key)
}

// commitLocked installs a published entry, releasing the one it replaces.
func (c *Cache) commitLocked(key string, version uint64, value any, release func()) {
	if old, ok := c.live[key]; ok {
		if old.release != nil {
			old.release()
		}
	}
	c.live[key] = &entry{version: version, value: value, release: release}
}

// Publish makes an externally sourced version visible (e.g. a subscription
// update), replacing and releasing any previous entry.
func (c *Cache) Publish(key string, version uint64, value any, release func()) error {
	at := c.now()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		if release != nil {
			release()
		}
		return ErrClosed
	}
	c.commitLocked(key, version, value, release)
	c.stats.Publishes++
	c.mu.Unlock()
	c.record(Event{Kind: KindPublish, Key: key, At: at, Version: version})
	return nil
}

// Evict removes and releases the entry for key.
func (c *Cache) Evict(key string) {
	at := c.now()
	c.mu.Lock()
	if e, ok := c.live[key]; ok {
		delete(c.live, key)
		if e.release != nil {
			e.release()
		}
		c.stats.Evicts++
	}
	c.mu.Unlock()
	c.record(Event{Kind: KindEvict, Key: key, At: at})
}

// Close releases every held resource. It is idempotent.
func (c *Cache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for k, e := range c.live {
		if e.release != nil {
			e.release()
		}
		delete(c.live, k)
	}
}

// Stats returns a copy of the current counters.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// History returns the retained audit events plus completeness metadata.
func (c *Cache) History() History {
	events, meta := c.log.Snapshot()
	return History{Events: events, Meta: meta}
}
