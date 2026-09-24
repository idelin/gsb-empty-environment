// Package coalesce implements invalidation coalescing with rate-limited
// refresh. Massive invalidation storms are merged per key, critical keys are
// refreshed first, a per-key version watermark guarantees the latest
// invalidation is never lost, and a bounded queue falls back to conservatively
// marking entries invalid when full.
//
// The data source, clock and error handler are all injectable functions; the
// package has no dependency on any remote cache.
package coalesce

import (
	"context"
	"sync"
	"time"
)

// Options configures an Invalidator. Load is required; everything else has a
// sane default.
type Options struct {
	// Load fetches the fresh value for a key from the data source.
	Load func(ctx context.Context, key string) (any, error)

	// IsCritical marks keys that jump the refresh queue. Default: none.
	IsCritical func(key string) bool

	// Now returns the current time. Default: time.Now.
	Now func() time.Time

	// After returns a channel that fires after d, used for rate limiting.
	// Default: time.After. Inject a manual clock in tests.
	After func(d time.Duration) <-chan time.Time

	// OnError is called when a refresh fails. Default: no-op.
	OnError func(key string, err error)

	// Capacity bounds the number of distinct keys waiting in the refresh
	// queue. Overflowing keys are conservatively marked invalid and retried
	// once capacity frees up. Default: 1024.
	Capacity int

	// Interval is the minimum delay between two refreshes (rate limit).
	// Default: 0 (no delay).
	Interval time.Duration
}

// entry is the per-key state. dirtyVersion is the version watermark: the
// highest invalidation generation observed for the key.
type entry struct {
	value        any
	valid        bool   // value may be served
	validVersion uint64 // generation the stored value covers
	dirty        bool   // an invalidation is pending/uncovered
	dirtyVersion uint64 // watermark: latest invalidation generation
	queued       bool   // key currently sits in a refresh queue
}

// Invalidator coalesces invalidations and refreshes keys in the background.
type Invalidator struct {
	load       func(ctx context.Context, key string) (any, error)
	isCritical func(key string) bool
	now        func() time.Time
	after      func(d time.Duration) <-chan time.Time
	onError    func(key string, err error)
	capacity   int
	interval   time.Duration

	mu       sync.Mutex
	entries  map[string]*entry
	critical []string // FIFO of critical keys awaiting refresh
	normal   []string // FIFO of normal keys awaiting refresh
	overflow map[string]struct{}

	wake      chan struct{}
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// New creates and starts an Invalidator. Panics if Load is nil.
func New(opt Options) *Invalidator {
	if opt.Load == nil {
		panic("coalesce: Options.Load is required")
	}
	iv := &Invalidator{
		load:       opt.Load,
		isCritical: opt.IsCritical,
		now:        opt.Now,
		after:      opt.After,
		onError:    opt.OnError,
		capacity:   opt.Capacity,
		interval:   opt.Interval,
		entries:    make(map[string]*entry),
		overflow:   make(map[string]struct{}),
		wake:       make(chan struct{}, 1),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	if iv.isCritical == nil {
		iv.isCritical = func(string) bool { return false }
	}
	if iv.now == nil {
		iv.now = time.Now
	}
	if iv.after == nil {
		iv.after = time.After
	}
	if iv.onError == nil {
		iv.onError = func(string, error) {}
	}
	if iv.capacity <= 0 {
		iv.capacity = 1024
	}
	go iv.run()
	return iv
}

// Invalidate records that key is stale as of the given generation. Repeated
// invalidations of the same key coalesce; only the highest generation is
// kept as the watermark.
func (iv *Invalidator) Invalidate(key string, version uint64) {
	iv.mu.Lock()
	e := iv.entryLocked(key)
	if version > e.dirtyVersion {
		e.dirtyVersion = version
	}
	e.dirty = true
	if !e.queued {
		iv.enqueueLocked(key, e)
	}
	iv.mu.Unlock()
	iv.signal()
}

// Get returns the cached value for key, or ok=false if the value is missing,
// dirty, or was conservatively invalidated due to queue pressure.
func (iv *Invalidator) Get(key string) (value any, ok bool) {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	e, exists := iv.entries[key]
	if !exists || e.dirty || !e.valid {
		return nil, false
	}
	return e.value, true
}

// State is a debug/test snapshot of a key.
type State struct {
	Known        bool
	Valid        bool
	ValidVersion uint64
	Dirty        bool
	DirtyVersion uint64
	Overflowed   bool
}

// Inspect returns the current state of key without mutating anything.
func (iv *Invalidator) Inspect(key string) State {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	e, ok := iv.entries[key]
	if !ok {
		return State{}
	}
	_, ov := iv.overflow[key]
	return State{
		Known:        true,
		Valid:        e.valid && !e.dirty,
		ValidVersion: e.validVersion,
		Dirty:        e.dirty,
		DirtyVersion: e.dirtyVersion,
		Overflowed:   ov,
	}
}

// Pending reports how many keys are waiting in the refresh queues.
func (iv *Invalidator) Pending() int {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	return len(iv.critical) + len(iv.normal)
}

// Close stops the background worker and blocks until it has exited,
// releasing all resources owned by the Invalidator.
func (iv *Invalidator) Close() {
	iv.closeOnce.Do(func() { close(iv.stop) })
	<-iv.done
}

func (iv *Invalidator) entryLocked(key string) *entry {
	e, ok := iv.entries[key]
	if !ok {
		e = &entry{}
		iv.entries[key] = e
	}
	return e
}

func (iv *Invalidator) queueLenLocked() int {
	return len(iv.critical) + len(iv.normal)
}

// enqueueLocked puts key on the appropriate queue, or into the overflow set
// (conservatively invalidated) when the queue is full.
func (iv *Invalidator) enqueueLocked(key string, e *entry) {
	if iv.queueLenLocked() < iv.capacity {
		if iv.isCritical(key) {
			iv.critical = append(iv.critical, key)
		} else {
			iv.normal = append(iv.normal, key)
		}
		e.queued = true
		delete(iv.overflow, key)
		return
	}
	// Queue full: conservatively mark invalid so the stale value can never
	// be served, and remember the key for a later retry.
	e.valid = false
	iv.overflow[key] = struct{}{}
}

// requeueLocked re-schedules a key that is still dirty after a refresh
// attempt (newer generation arrived, or the load failed).

func (iv *Invalidator) requeueLocked(key string, e *entry) {
	e.queued = false
	iv.enqueueLocked(key, e)
}

func (iv *Invalidator) signal() {
	select {
	case iv.wake <- struct{}{}:
	default:
	}
}

// pop removes the next key to refresh: critical first, then normal, then
// overflow spill. Queue entries whose invalidations were already covered by
// a completed refresh are skipped, so a coalesced storm costs one load.
func (iv *Invalidator) pop() (string, bool) {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	for {
		if len(iv.critical) > 0 {
			key := iv.critical[0]
			iv.critical = iv.critical[1:]
			iv.entries[key].queued = false
			if iv.entries[key].dirty {
				return key, true
			}
			continue
		}
		if len(iv.normal) > 0 {
			key := iv.normal[0]
			iv.normal = iv.normal[1:]
			iv.entries[key].queued = false
			if iv.entries[key].dirty {
				return key, true
			}
			continue
		}
		break
	}
	for key := range iv.overflow {
		delete(iv.overflow, key)
		if iv.entries[key].dirty {
			return key, true
		}
	}
	return "", false
}

// run is the background worker: one refresh at a time, rate limited.
func (iv *Invalidator) run() {
	defer close(iv.done)
	for {
		key, ok := iv.pop()
		if !ok {
			select {
			case <-iv.wake:
				continue
			case <-iv.stop:
				return
			}
		}
		if iv.interval > 0 {
			select {
			case <-iv.after(iv.interval):
			case <-iv.stop:
				return
			}
		}
		if !iv.refresh(key) {
			// Refresh failed; requeue and keep going (rate limiter bounds
			// the retry rate).
			iv.signal()
			continue
		}
		iv.signal()
	}
}

// refresh loads key and confirms only the generations the load actually
// covered. Returns false if the load failed.
func (iv *Invalidator) refresh(key string) bool {
	iv.mu.Lock()
	e := iv.entries[key]
	snapshot := e.dirtyVersion
	iv.mu.Unlock()

	value, err := iv.load(context.Background(), key)

	iv.mu.Lock()
	defer iv.mu.Unlock()
	if err != nil {
		iv.onError(key, err)
		iv.requeueLocked(key, e)
		return false
	}
	e.value = value
	if e.dirtyVersion <= snapshot {
		// No invalidation arrived while loading: confirm up to snapshot.
		e.dirty = false
		e.valid = true
		e.validVersion = snapshot
	} else {
		// A newer invalidation arrived during the load; the fresh value
		// does not cover it. Stay dirty and refresh again.
		iv.requeueLocked(key, e)
	}
	return true
}
