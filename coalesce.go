package coalesce

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
)

// ErrInvalid is returned by Get when the key currently has no validated
// value: it has never been loaded, has been invalidated, or a load is
// pending. Callers must treat this as "no value you may serve", never as
// "the previous value is still fresh".
var ErrInvalid = errors.New("coalesce: key invalid")

// ErrClosed is returned after Close.
var ErrClosed = errors.New("coalesce: cache closed")

// LoadResult is one key's value together with the data source version.
type LoadResult[V any] struct {
	Value   V
	Version uint64
}

// LoadFunc is the injected data source. It fetches the given keys in one
// round trip and returns one result per key, keyed by K. A returned value is
// accepted only when the data source version is at least the version the
// cache recorded at invalidation time (the version watermark).
//
// A nil/missing map entry for a requested key is treated as a failure for
// that key. The implementation must honour ctx cancellation.
type LoadFunc[K comparable, V any] func(ctx context.Context, keys []K) (map[K]LoadResult[V], error)

// Config configures a Cache.
type Config[K comparable, V any] struct {
	// Load talks to the data source. Required.
	Load LoadFunc[K, V]
	// Clock is injectable. Defaults to RealClock.
	Clock Clock
	// Interval rate limits refreshes: at most one batch per interval.
	Interval time.Duration
	// BatchSize caps keys per data-source round trip.
	BatchSize int
	// QueueCapacity bounds the number of distinct keys held for refresh.
	// When a new invalidation would exceed it the key is conservatively
	// marked invalid and retried once space frees up.
	QueueCapacity int
	// OnError, if set, receives every failed batch (injected failure sink).
	OnError func(keys []K, err error)
}

// Cache is an invalidation-driven refresh coalescer.
//
// Invalidation requests for the same key while it is already queued or in
// flight are merged into a single load. Each merge bumps a monotonic
// generation: a load is acknowledged only if no newer invalidation arrived
// while it was in flight, and only if the data source version is fresh
// enough (the version watermark), so merged invalidations never lose the
// latest one and stale responses never resurrect an old value.
type Cache[K comparable, V any] struct {
	load      LoadFunc[K, V]
	clock     Clock
	onError   func(keys []K, err error)
	batchSize int
	queueSize int

	mu       sync.Mutex
	entries  map[K]*entry[V]
	priority indexedQueue[K]
	normal   indexedQueue[K]
	// inflight keys the worker is currently loading.
	inflight map[K]struct{}
	// deferred keys that were invalidated while the queue was full; they
	// are admitted as soon as capacity exists.
	deferred map[K]bool
	closed   bool

	ctx    context.Context
	cancel context.CancelFunc
	ticker Ticker
	done   chan struct{}
}

type entry[V any] struct {
	val   V
	ver   uint64
	valid bool
	// gen is the invalidation watermark: bumped on every invalidation.
	gen uint64
	// pending is true while the key sits in a refresh queue.
	pending bool
	// pri reports which lane a pending key is in.
	pri bool
}

type track[K comparable, V any] struct {
	key K
	gen uint64
	ver uint64
	pri bool
}

// indexedQueue is a strict FIFO with O(1) membership and O(1) removal,
// backed by a linked list plus an element index.
type indexedQueue[T comparable] struct {
	ll  *list.List
	pos map[T]*list.Element
}

func newIndexedQueue[T comparable](_ int) indexedQueue[T] {
	return indexedQueue[T]{ll: list.New(), pos: make(map[T]*list.Element)}
}

func (q *indexedQueue[T]) len() int { return q.ll.Len() }

func (q *indexedQueue[T]) push(k T) {
	q.pos[k] = q.ll.PushBack(k)
}

func (q *indexedQueue[T]) pop() T {
	front := q.ll.Front()
	k := front.Value.(T)
	q.ll.Remove(front)
	delete(q.pos, k)
	return k
}

func (q *indexedQueue[T]) remove(k T) bool {
	e, ok := q.pos[k]
	if !ok {
		return false
	}
	q.ll.Remove(e)
	delete(q.pos, k)
	return true
}

// New constructs a Cache and starts its single rate-limited worker goroutine.
func New[K comparable, V any](cfg Config[K, V]) *Cache[K, V] {
	if cfg.Load == nil {
		panic("coalesce: Config.Load is required")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Millisecond
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 16
	}
	if cfg.QueueCapacity <= 0 {
		cfg.QueueCapacity = 1024
	}
	if cfg.Clock == nil {
		cfg.Clock = NewRealClock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &Cache[K, V]{
		load:      cfg.Load,
		clock:     cfg.Clock,
		onError:   cfg.OnError,
		batchSize: cfg.BatchSize,
		queueSize: cfg.QueueCapacity,
		entries:   make(map[K]*entry[V]),
		priority:  newIndexedQueue[K](cfg.QueueCapacity),
		normal:    newIndexedQueue[K](cfg.QueueCapacity),
		inflight:  make(map[K]struct{}),
		deferred:  make(map[K]bool),
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	c.ticker = cfg.Clock.NewTicker(cfg.Interval)
	go c.worker()
	return c
}

// Invalidate marks the key stale and merges its refresh into the pending
// queue. When the queue is full the key is still marked invalid and is
// admitted automatically once space frees up.
func (c *Cache[K, V]) Invalidate(key K, version uint64) error {
	return c.invalidate(key, version, false)
}

// InvalidateCritical is Invalidate with priority: critical keys are loaded
// before ordinary ones in every batch. Promoting an already pending key only
// bumps its lane and invalidation watermark; it never duplicates the load.
func (c *Cache[K, V]) InvalidateCritical(key K, version uint64) error {
	return c.invalidate(key, version, true)
}

func (c *Cache[K, V]) invalidate(key K, version uint64, pri bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	e := c.entries[key]
	if e == nil {
		e = &entry[V]{}
		c.entries[key] = e
	}
	e.gen++
	// The version watermark only rises: out-of-order notifications cannot
	// roll it backwards.
	if version > e.ver {
		e.ver = version
	}
	// Conservative: the old value must never keep serving after invalidation.
	e.valid = false

	if e.pending {
		// Merge: no second enqueue; only promote the lane when requested.
		if pri && !e.pri {
			if c.normal.remove(key) {
				c.priority.push(key)
				e.pri = true
			}
		}
		return nil
	}
	if _, flying := c.inflight[key]; flying {
		// In flight: the bumped generation makes the in-flight response
		// stale for this key; finishBatch re-enqueues it.
		return nil
	}
	c.scheduleLocked(key, pri)
	return nil
}

func (c *Cache[K, V]) scheduleLocked(key K, pri bool) {
	e := c.entries[key]
	if c.queueLenLocked() >= c.queueCap() {
		// Queue full: stay conservatively invalid and remember to retry.
		e.pending = false
		c.deferred[key] = pri
		return
	}
	delete(c.deferred, key)
	if pri {
		c.priority.push(key)
	} else {
		c.normal.push(key)
	}
	e.pending = true
	e.pri = pri
}

func (c *Cache[K, V]) queueLenLocked() int {
	return c.priority.len() + c.normal.len()
}

func (c *Cache[K, V]) queueCap() int {
	return c.queueSize
}

// Get returns the last validated value. A value whose generation/version is
// behind an invalidation is never returned.
func (c *Cache[K, V]) Get(key K) (V, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		var zero V
		return zero, 0, ErrClosed
	}
	e := c.entries[key]
	if e == nil || !e.valid {
		var zero V
		return zero, 0, ErrInvalid
	}
	return e.val, e.ver, nil
}

func (c *Cache[K, V]) worker() {
	defer close(c.done)
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.ticker.C():
			c.tick()
		}
	}
}

func (c *Cache[K, V]) tick() {
	tracks, keys := c.takeBatch()
	if len(tracks) == 0 {
		return
	}
	results, err := c.load(c.ctx, keys)
	c.finishBatch(tracks, results, err)
}

func (c *Cache[K, V]) takeBatch() ([]track[K, V], []K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.batchSize
	if total := c.queueLenLocked(); total < n {
		n = total
	}
	tracks := make([]track[K, V], 0, n)
	keys := make([]K, 0, n)
	drain := func(q *indexedQueue[K]) {
		for len(tracks) < c.batchSize && q.len() > 0 {
			k := q.pop()
			e := c.entries[k]
			e.pending = false
			c.inflight[k] = struct{}{}
			tracks = append(tracks, track[K, V]{key: k, gen: e.gen, ver: e.ver, pri: e.pri})
			keys = append(keys, k)
		}
	}
	drain(&c.priority)
	drain(&c.normal)
	return tracks, keys
}

func (c *Cache[K, V]) finishBatch(tracks []track[K, V], results map[K]LoadResult[V], loadErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var failedKeys []K
	for _, t := range tracks {
		key := t.key
		e := c.entries[key]
		delete(c.inflight, key)

		stale := e == nil || e.gen != t.gen
		res, has := lookupResult[K, V](results, key)
		// Acknowledge only when this load actually covers the current
		// generation and the data source is at or above the watermark.
		if loadErr == nil && has && !stale && res.Version >= t.ver {
			e.val = res.Value
			if res.Version > e.ver {
				e.ver = res.Version
			}
			e.valid = true
			continue
		}

		// Failure, missing result, stale generation or stale source
		// version: do not confirm; re-enqueue for a later batch.
		e.valid = false
		if !c.closed {
			c.scheduleLocked(key, t.pri)
		}
		if loadErr != nil {
			failedKeys = append(failedKeys, key)
		}
	}

	if loadErr != nil && c.onError != nil {
		c.onError(failedKeys, loadErr)
	}

	c.admitDeferredLocked()
}

func lookupResult[K comparable, V any](m map[K]LoadResult[V], key K) (LoadResult[V], bool) {
	if m == nil {
		return LoadResult[V]{}, false
	}
	r, ok := m[key]
	return r, ok
}

// admitDeferredLocked fills any free capacity with keys that were turned away
// from a full queue, critical lanes first.
func (c *Cache[K, V]) admitDeferredLocked() {
	if len(c.deferred) == 0 {
		return
	}
	priKeys := make([]K, 0, len(c.deferred))
	normKeys := make([]K, 0, len(c.deferred))
	for k, pri := range c.deferred {
		if pri {
			priKeys = append(priKeys, k)
		} else {
			normKeys = append(normKeys, k)
		}
	}
	admit := func(keys []K) {
		for _, k := range keys {
			pri, known := c.deferred[k]
			if !known {
				continue
			}
			e := c.entries[k]
			if e == nil || e.pending {
				delete(c.deferred, k)
				continue
			}
			if _, flying := c.inflight[k]; flying {
				continue
			}
			if c.queueLenLocked() >= c.queueCap() {
				return
			}
			delete(c.deferred, k)
			if pri {
				c.priority.push(k)
			} else {
				c.normal.push(k)
			}
			e.pending = true
			e.pri = pri
		}
	}
	admit(priKeys)
	admit(normKeys)
}

// Stats is a point-in-time observability snapshot, primarily for tests.
type Stats struct {
	QueuedCritical int
	QueuedNormal   int
	InFlight       int
	Deferred       int
}

// Snapshot returns internal queue sizes.
func (c *Cache[K, V]) Snapshot() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{
		QueuedCritical: c.priority.len(),
		QueuedNormal:   c.normal.len(),
		InFlight:       len(c.inflight),
		Deferred:       len(c.deferred),
	}
}

// Close stops the worker, stops the ticker and waits for the goroutine to
// exit. It is safe to call multiple times.
func (c *Cache[K, V]) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()

	c.cancel()
	c.ticker.Stop()
	<-c.done
}
