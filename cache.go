// Package cache implements a two-level (L1/L2) read cache with shared
// versioning, an invalidation bus, and explicit consistency semantics.
//
// Consistency model (declared, not overstated):
//
//   - Strong: a read only returns data whose version has been validated
//     against the per-key version watermark (driven by invalidation
//     messages) or re-fetched from L2/source. If the invalidation
//     notification is delayed, a strong read WAITS (bounded by ctx) for
//     the watermark to catch up. If L2 is unavailable, a strong read
//     fails explicitly instead of silently downgrading.
//   - Eventual: a read never waits for invalidation notifications, so it
//     may serve an L1 entry that is stale without knowing it (the
//     notification has not arrived yet). If the version watermark proves
//     the L1 entry is old, the read revalidates via L2/source; only when
//     revalidation is impossible (degraded mode) is the old entry served,
//     explicitly marked Stale=true.
//
// L2 failure degradation rules (explicit):
//
//  1. Try the source directly; if it answers, serve from source with
//     Degraded=true (L1 is refreshed, L2 is skipped).
//  2. If the source also fails: Strong reads fail with ErrL2Unavailable;
//     Eventual reads may serve a stale L1 entry marked Stale+Degraded.
//  3. A degraded result is never reported as strongly consistent.
//
// Invariants:
//   - Invalidation application is idempotent (dedup by Message.Seq).
//   - Version rollback is rejected everywhere (watermark, L2, source).
//   - Data source, clock, and failures are all injectable.
package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrVersionRollback is returned when a version lower than the known
	// watermark is observed; rollback is always rejected.
	ErrVersionRollback = errors.New("cache: version rollback rejected")
	// ErrL2Unavailable is returned when L2 has failed and the requested
	// consistency level cannot be honestly served.
	ErrL2Unavailable = errors.New("cache: L2 unavailable, cannot serve requested consistency")
	// ErrClosed is returned by operations on a closed Cache.
	ErrClosed = errors.New("cache: closed")
	// ErrSourceNotWritable is returned by Write when the injected source
	// does not implement WritableSource.
	ErrSourceNotWritable = errors.New("cache: source is not writable")
)

// Consistency declares the consistency a caller requests for a read.
type Consistency int

const (
	// Eventual allows declared-stale reads (Result.Stale=true).
	Eventual Consistency = iota
	// Strong requires validation against the version watermark; it waits
	// for delayed notifications (bounded by ctx) and never silently
	// degrades.
	Strong
)

func (c Consistency) String() string {
	if c == Strong {
		return "strong"
	}
	return "eventual"
}

// Result is the outcome of a Get. Stale and Degraded are explicit
// declarations so callers never mistake a weak read for a strong one.
// Stale is only true when the entry is known to be behind the version
// watermark and could not be revalidated (i.e. degraded mode).
type Result struct {
	Value    string
	Version  uint64
	From     string // "L1", "L2" or "source"
	Stale    bool   // served below the known version watermark (degraded only)
	Degraded bool   // served while L2 was unavailable
}

func (r Result) String() string {
	return fmt.Sprintf("value=%q version=%d from=%s stale=%v degraded=%v",
		r.Value, r.Version, r.From, r.Stale, r.Degraded)
}

// Options configures a Cache. Every external dependency is injectable.
type Options struct {
	NodeID    string           // publisher identity for invalidation messages; auto-generated if empty
	Source    Source           // authoritative data source (required)
	Clock     func() time.Time // wall clock; defaults to time.Now
	L2        *L2              // second-level cache; defaults to a fresh one
	Notifier  *Notifier        // invalidation bus; defaults to a fresh one
	StrongTTL time.Duration    // how long a validated L1 entry is reusable for Strong reads; 0 = always revalidate
	OnEvent   func(msg string) // observability hook for degradation events; optional
}

// Cache is a two-level read cache. Use New and Close.
type Cache struct {
	id       string
	src      Source
	clock    func() time.Time
	l2       *L2
	notifier *Notifier
	ttl      time.Duration
	onEvent  func(string)

	mu        sync.Mutex
	l1        map[string]entry
	validAt   map[string]time.Time  // when the L1 entry was last validated
	watermark map[string]uint64     // per-key highest version announced
	seenSeq   map[dedupKey]struct{} // dedup set for invalidation messages
	seq       uint64                // local message sequence counter
	applied   uint64                // invalidations actually applied
	dups      uint64                // duplicate invalidations dropped
	rollbacks uint64                // rollback attempts rejected

	sub       Subscription
	closed    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// dedupKey identifies one published message: sequence numbers are only
// unique per publisher.
type dedupKey struct {
	src string
	seq uint64
}

var nodeCounter atomic.Uint64

// New builds a Cache and starts its invalidation loop.
func New(opts Options) *Cache {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	l2 := opts.L2
	if l2 == nil {
		l2 = NewL2()
	}
	notifier := opts.Notifier
	if notifier == nil {
		notifier = NewNotifier()
	}
	id := opts.NodeID
	if id == "" {
		id = fmt.Sprintf("node-%d", nodeCounter.Add(1))
	}
	c := &Cache{
		id:        id,
		src:       opts.Source,
		clock:     clock,
		l2:        l2,
		notifier:  notifier,
		ttl:       opts.StrongTTL,
		onEvent:   opts.OnEvent,
		l1:        map[string]entry{},
		validAt:   map[string]time.Time{},
		watermark: map[string]uint64{},
		seenSeq:   map[dedupKey]struct{}{},
		closed:    make(chan struct{}),
	}
	c.sub = notifier.Subscribe(64)
	c.wg.Add(1)
	go c.loop()
	return c
}

// Close releases all resources: it unsubscribes from the notifier and
// waits for the invalidation loop to exit.
func (c *Cache) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.sub.Unsubscribe()
		c.wg.Wait()
	})
	return nil
}

func (c *Cache) loop() {
	defer c.wg.Done()
	for {
		select {
		case m, ok := <-c.sub.C:
			if !ok {
				return
			}
			_ = c.ApplyInvalidation(m)
		case <-c.closed:
			return
		}
	}
}

// ApplyInvalidation applies one invalidation message. It is idempotent for
// duplicate (Src, Seq) values and rejects version rollback.
func (c *Cache) ApplyInvalidation(m Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	dk := dedupKey{src: m.Src, seq: m.Seq}
	if _, dup := c.seenSeq[dk]; dup {
		c.dups++
		return nil // duplicate delivery: idempotent no-op
	}
	if cur := c.watermark[m.Key]; m.Version < cur {
		c.rollbacks++
		return ErrVersionRollback
	}
	c.seenSeq[dk] = struct{}{}
	c.watermark[m.Key] = m.Version
	c.applied++
	return nil
}

// Get reads key with the declared consistency level.
func (c *Cache) Get(ctx context.Context, key string, cons Consistency) (Result, error) {
	select {
	case <-c.closed:
		return Result{}, ErrClosed
	default:
	}

	// 1. L1 lookup, validated against the version watermark.
	c.mu.Lock()
	e, hit := c.l1[key]
	validAt := c.validAt[key]
	wm := c.watermark[key]
	c.mu.Unlock()

	if hit && e.version >= wm {
		if cons == Eventual {
			return Result{Value: e.value, Version: e.version, From: "L1"}, nil
		}
		// Strong: a recently validated entry is reusable; otherwise fall
		// through and revalidate against L2/source.
		if c.clock().Sub(validAt) <= c.ttl {
			return Result{Value: e.value, Version: e.version, From: "L1"}, nil
		}
	}
	// 2. L2 lookup.
	le, lok, lerr := c.l2.get(key)
	if lerr != nil {
		c.eventf("L2 get failed for %q: %v (degraded path)", key, lerr)
		return c.degradedGet(ctx, key, cons, e, hit)
	}
	if lok {
		if le.version < wm {
			// L2 itself is behind the watermark: reject rollback, go to source.
			c.eventf("L2 entry for %q at version %d is behind watermark %d", key, le.version, wm)
			return c.loadFromSource(ctx, key)
		}
		if cons == Strong {
			// The L2 update notification may still be in flight; wait for
			// the watermark to observe this version before serving.
			if err := c.waitWatermark(ctx, key, le.version); err != nil {
				return Result{}, err
			}
		}
		c.storeL1(key, le)
		return Result{Value: le.value, Version: le.version, From: "L2"}, nil
	}

	// 3. Source.
	return c.loadFromSource(ctx, key)
}

// degradedGet implements the explicit L2-failure degradation rules.
func (c *Cache) degradedGet(ctx context.Context, key string, cons Consistency, l1e entry, l1hit bool) (Result, error) {
	// Rule 1: fall back to the source.
	v, ver, err := c.src.Load(ctx, key)
	if err == nil {
		c.mu.Lock()
		wm := c.watermark[key]
		c.mu.Unlock()
		if ver < wm {
			return Result{}, ErrVersionRollback
		}
		c.storeL1(key, entry{value: v, version: ver})
		return Result{Value: v, Version: ver, From: "source", Degraded: true}, nil
	}
	// Rule 2: source also failed. Strong reads must fail honestly.
	if cons == Strong {
		return Result{}, fmt.Errorf("%w: %v", ErrL2Unavailable, err)
	}
	// Rule 3: Eventual reads may serve a declared-stale L1 entry.
	if l1hit {
		return Result{Value: l1e.value, Version: l1e.version, From: "L1", Stale: true, Degraded: true}, nil
	}
	return Result{}, fmt.Errorf("%w: %v", ErrL2Unavailable, err)
}

func (c *Cache) loadFromSource(ctx context.Context, key string) (Result, error) {
	v, ver, err := c.src.Load(ctx, key)
	if err != nil {
		return Result{}, err
	}
	c.mu.Lock()
	wm := c.watermark[key]
	c.mu.Unlock()
	if ver < wm {
		return Result{}, ErrVersionRollback
	}
	e := entry{value: v, version: ver}
	if serr := c.l2.set(key, e); serr != nil {
		if errors.Is(serr, ErrVersionRollback) {
			return Result{}, serr
		}
		// L2 write failure degrades the cache but not the read result.
		c.eventf("L2 set failed for %q: %v (continuing degraded)", key, serr)
	}
	c.storeL1(key, e)
	return Result{Value: v, Version: ver, From: "source"}, nil
}

// waitWatermark blocks until the per-key watermark reaches v or ctx ends.
// This is how strong reads handle delayed L2 update notifications: they
// wait rather than serve unvalidated data.
func (c *Cache) waitWatermark(ctx context.Context, key string, v uint64) error {
	c.mu.Lock()
	if c.watermark[key] >= v {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	t := time.NewTicker(5 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("cache: strong read of %q: invalidation notification for version %d not observed: %w", key, v, ctx.Err())
		case <-t.C:
			c.mu.Lock()
			ok := c.watermark[key] >= v
			c.mu.Unlock()
			if ok {
				return nil
			}
		}
	}
}

// Write stores value in the source (which assigns a new monotonic version),
// updates L2 best-effort, refreshes L1, and publishes an invalidation.
func (c *Cache) Write(ctx context.Context, key, value string) (uint64, error) {
	select {
	case <-c.closed:
		return 0, ErrClosed
	default:
	}
	ws, ok := c.src.(WritableSource)
	if !ok {
		return 0, ErrSourceNotWritable
	}
	ver, err := ws.Store(ctx, key, value)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	wm := c.watermark[key]
	c.mu.Unlock()
	if ver < wm {
		return 0, ErrVersionRollback
	}
	e := entry{value: value, version: ver}
	if serr := c.l2.set(key, e); serr != nil && !errors.Is(serr, ErrVersionRollback) {
		c.eventf("L2 set failed for %q: %v (write degraded)", key, serr)
	}
	c.storeL1(key, e)

	c.mu.Lock()
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	if perr := c.notifier.Publish(Message{Src: c.id, Key: key, Version: ver, Seq: seq}); perr != nil {
		c.eventf("invalidation publish failed for %q: %v", key, perr)
	}
	return ver, nil
}

func (c *Cache) storeL1(key string, e entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.l1[key] = e
	c.validAt[key] = c.clock()
}

func (c *Cache) eventf(format string, args ...any) {
	if c.onEvent != nil {
		c.onEvent(fmt.Sprintf(format, args...))
	}
}

// Stats exposes internal counters for tests and observability.
type Stats struct {
	AppliedInvalidations uint64
	DuplicateDropped     uint64
	RollbacksRejected    uint64
}

// Stats returns a snapshot of the invalidation counters.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{AppliedInvalidations: c.applied, DuplicateDropped: c.dups, RollbacksRejected: c.rollbacks}
}

// Watermark returns the current version watermark for key.
func (c *Cache) Watermark(key string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.watermark[key]
}
