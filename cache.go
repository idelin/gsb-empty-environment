// Package writeback implements an in-memory cache whose mutations enter an
// ordered dirty queue and are flushed to a backing source in batches by a
// background goroutine. Flushes are strictly ordered, failures keep the dirty
// state and are retried in order, and shutdown can either wait for the queue
// to drain or return the list of keys that were never flushed.
//
// The data source, the clock and the retry backoff are all injectable
// functions, so tests run entirely against in-memory fakes.
package writeback

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrClosed is returned by mutating operations after Close or Shutdown.
var ErrClosed = errors.New("writeback: cache is closed")

type opKind int

const (
	opSet opKind = iota
	opDelete
)

// entry is one position in the dirty queue. Repeated updates to the same key
// merge into the existing entry: the position (seq) is kept, the payload and
// version are updated. version lets a concurrent flush detect that an entry
// was re-dirtied while its snapshot was in flight.
type entry struct {
	key     string
	kind    opKind
	value   []byte
	seq     uint64
	version uint64
}

// Source is the backing store. Every function is injectable.
type Source struct {
	Store  func(ctx context.Context, key string, value []byte) error
	Delete func(ctx context.Context, key string) error
}

// Options configures a Cache. Nil fields get sane defaults.
type Options struct {
	Source Source

	// Now is the injectable clock, used for flush timestamps. Default time.Now.
	Now func() time.Time

	// FlushInterval is how often the background goroutine flushes.
	// Default 100ms. Set FlushInterval <= 0 to disable the background
	// flusher entirely and drive Flush manually.
	FlushInterval time.Duration

	// MaxBatch caps how many entries one batch attempt applies. Default 64.
	MaxBatch int

	// Backoff returns the delay before retrying after a failed flush,
	// given the consecutive failure count (starting at 1). Injectable,
	// which is how tests control failure/retry timing. Default: 50ms * attempt.
	Backoff func(attempt int) time.Duration
}

// Cache is a write-back cache with an ordered dirty queue.
type Cache struct {
	src      Source
	now      func() time.Time
	maxBatch int
	backoff  func(attempt int) time.Duration

	mu      sync.Mutex
	data    map[string][]byte
	queue   []*entry // dirty entries, ordered by seq
	pending map[string]*entry
	seq     uint64 // last assigned sequence number
	flushed uint64 // watermark: every op with seq <= flushed is durable
	closed  bool

	lastFlush time.Time

	flushMu sync.Mutex // serializes flush attempts
	stop    chan struct{}
	done    chan struct{} // closed when the background goroutine exits
}

// New creates a Cache and, unless FlushInterval <= 0, starts the background
// flush goroutine.
func New(opts Options) *Cache {
	c := &Cache{
		src:      opts.Source,
		now:      opts.Now,
		maxBatch: opts.MaxBatch,
		backoff:  opts.Backoff,
		data:     make(map[string][]byte),
		pending:  make(map[string]*entry),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.maxBatch <= 0 {
		c.maxBatch = 64
	}
	if c.backoff == nil {
		c.backoff = func(attempt int) time.Duration {
			return time.Duration(attempt) * 50 * time.Millisecond
		}
	}
	interval := opts.FlushInterval
	if interval <= 0 {
		// No background goroutine at all: close done immediately so Close
		// and Shutdown never block on it.
		close(c.done)
		return c
	}
	go c.run(interval)
	return c
}

// Set caches value under key and enqueues (or merges) a dirty write.
func (c *Cache) Set(key string, value []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.data[key] = append([]byte(nil), value...)
	if e, ok := c.pending[key]; ok {
		e.kind = opSet
		e.value = append([]byte(nil), value...)
		e.version++
		return nil
	}
	c.seq++
	e := &entry{key: key, kind: opSet, value: append([]byte(nil), value...), seq: c.seq}
	c.pending[key] = e
	c.queue = append(c.queue, e)
	return nil
}

// Delete removes key from the cache and enqueues (or merges) a dirty delete.
// The delete keeps its position in the queue, so it can never be reordered
// before earlier writes.
func (c *Cache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	delete(c.data, key)
	if e, ok := c.pending[key]; ok {
		e.kind = opDelete
		e.value = nil
		e.version++
		return nil
	}
	c.seq++
	e := &entry{key: key, kind: opDelete, seq: c.seq}
	c.pending[key] = e
	c.queue = append(c.queue, e)
	return nil
}

// Get returns the cached value. It never touches the source.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[key]
	return v, ok
}

// Watermark returns the flush watermark: every operation with a sequence
// number <= the watermark is durable in the source. Merged operations share
// the sequence slot of the entry they merged into, so when the queue drains
// the watermark jumps to the last assigned sequence number.
func (c *Cache) Watermark() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flushed
}

// Pending returns the number of dirty queue entries.
func (c *Cache) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue)
}

// Unflushed returns the keys still dirty, in queue order.
func (c *Cache) Unflushed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.queue))
	for _, e := range c.queue {
		keys = append(keys, e.key)
	}
	return keys
}

// LastFlush is the time (from the injected clock) of the last flush attempt
// that made progress.
func (c *Cache) LastFlush() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastFlush
}

// Flush synchronously flushes batches until the queue is empty or an entry
// fails. It returns the number of entries committed and the first error.
// On error the failed entry and everything behind it stay dirty.
func (c *Cache) Flush(ctx context.Context) (int, error) {
	total := 0
	for {
		n, err := c.flushBatch(ctx)
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, nil
		}
	}
}

// flushBatch applies at most maxBatch entries, in queue order, stopping at
// the first failure. Committed entries are removed from the queue and the
// watermark is advanced; the failed entry and its successors stay dirty.
func (c *Cache) flushBatch(ctx context.Context) (int, error) {
	c.flushMu.Lock()
	defer c.flushMu.Unlock()

	c.mu.Lock()
	if len(c.queue) == 0 {
		c.mu.Unlock()
		return 0, nil
	}
	n := c.maxBatch
	if n > len(c.queue) {
		n = len(c.queue)
	}
	batch := make([]entry, n)
	for i, e := range c.queue[:n] {
		batch[i] = *e // snapshot: seq + version identify this generation
	}
	c.mu.Unlock()

	committed := 0
	var firstErr error
	for i := range batch {
		e := batch[i]
		var err error
		if e.kind == opSet {
			err = c.src.Store(ctx, e.key, e.value)
		} else {
			err = c.src.Delete(ctx, e.key)
		}
		if err != nil {
			firstErr = err
			break // preserve order: never skip a failed entry
		}
		committed++
	}
	if committed > 0 {
		c.commit(batch[:committed])
	}
	return committed, firstErr
}

// commit removes flushed entries from the head of the queue and advances the
// watermark. An entry whose version changed since the snapshot was re-dirtied
// concurrently and must stay queued.
func (c *Cache) commit(snaps []entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	bySeq := make(map[uint64]uint64, len(snaps)) // seq -> snapshot version
	for _, s := range snaps {
		bySeq[s.seq] = s.version
	}
	remaining := c.queue[:0]
	for _, qe := range c.queue {
		if ver, ok := bySeq[qe.seq]; ok && qe.version == ver {
			delete(c.pending, qe.key)
			continue
		}
		remaining = append(remaining, qe)
	}
	// Clear the tail so removed entries can be GC'd.
	for i := len(remaining); i < len(c.queue); i++ {
		c.queue[i] = nil
	}
	c.queue = remaining
	c.advanceWatermarkLocked()
	c.lastFlush = c.now()
}

// advanceWatermarkLocked keeps the invariant: every op with seq <= flushed is
// durable. The oldest dirty entry bounds the watermark; an empty queue means
// everything ever assigned (including merged-away seqs) is durable.
func (c *Cache) advanceWatermarkLocked() {
	if len(c.queue) == 0 {
		c.flushed = c.seq
		return
	}
	if oldest := c.queue[0].seq; oldest > 0 && oldest-1 > c.flushed {
		c.flushed = oldest - 1
	}
}

// run is the background flush loop.
func (c *Cache) run(interval time.Duration) {
	defer close(c.done)
	timer := time.NewTimer(interval)
	defer timer.Stop()
	attempt := 0
	for {
		select {
		case <-c.stop:
			return
		case <-timer.C:
		}
		_, err := c.flushBatch(context.Background())
		delay := interval
		if err != nil {
			attempt++
			delay = c.backoff(attempt)
		} else {
			attempt = 0
		}
		timer.Reset(delay)
	}
}

// Close stops the background flusher and drains the queue, retrying failed
// entries (in order, with backoff) until the queue is empty or ctx expires.
// On ctx expiry the remaining keys are available via Unflushed.
func (c *Cache) Close(ctx context.Context) error {
	c.markClosed()
	attempt := 0
	for {
		n, err := c.Flush(ctx)
		if err == nil && c.Pending() == 0 {
			return nil
		}
		_ = n
		attempt++
		delay := c.backoff(attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// Shutdown stops the background flusher immediately and returns the keys
// that were never flushed, in queue order. Unlike Close it does not retry.
func (c *Cache) Shutdown() []string {
	c.markClosed()
	return c.Unflushed()
}

// markClosed flips the closed flag once, stops the background goroutine and
// waits for it to exit, releasing its resources.
func (c *Cache) markClosed() {
	c.mu.Lock()
	already := c.closed
	if !already {
		c.closed = true
	}
	c.mu.Unlock()
	if already {
		<-c.done // goroutine already stopped; done is closed
		return
	}
	close(c.stop)
	<-c.done
}
