// Package writeback implements an ordered dirty-queue write-back cache.
//
// Writes land in an in-memory map and are appended to an ordered dirty
// queue. A background goroutine flushes the queue to a backing source in
// batches, in FIFO order. A failed flush keeps every entry dirty and is
// retried with backoff, preserving order. Closing the cache either waits
// for the queue to drain or returns the unflushed ops explicitly.
//
// The backing source, the clock and (for tests) failures are all
// injectable functions; there is no remote cache dependency.
package writeback

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
)

// OpKind describes the kind of a buffered mutation.
type OpKind int

const (
	OpSet OpKind = iota
	OpDelete
)

func (k OpKind) String() string {
	if k == OpDelete {
		return "DELETE"
	}
	return "SET"
}

// Op is a single mutation destined for the backing source.
type Op struct {
	Seq   uint64    // monotonic sequence number; a merged slot carries the latest one
	Kind  OpKind    // SET or DELETE
	Key   string    // cache key
	Value []byte    // nil for OpDelete
	At    time.Time // stamped with the injectable clock
}

// Sink writes a batch of ops to the backing source, in order. It must be
// idempotent: a failed batch is retried as a whole. Inject failures here
// to exercise the retry path. Sink is called with the cache lock held, so
// it must not call back into the Cache.
type Sink func(ctx context.Context, batch []Op) error

// Options configures a Cache. All functions are injectable seams.
type Options struct {
	Sink          Sink             // required: backing source writer
	Now           func() time.Time // clock; defaults to time.Now
	BatchSize     int              // max ops per flush batch; default 64
	FlushInterval time.Duration    // background flush cadence; default 100ms
	RetryBackoff  time.Duration    // delay before retrying a failed batch; default 50ms
}

// ErrClosed is returned by mutators after Close.
var ErrClosed = errors.New("writeback: cache is closed")

// entry is one slot of the dirty queue. Repeated updates to the same key
// merge into the same slot: op carries the latest mutation while firstSeq
// keeps the seq of the first merged op to anchor the watermark.
type entry struct {
	op       Op
	firstSeq uint64
}

// Cache is an ordered dirty-queue write-back cache.
type Cache struct {
	opts Options

	mu    sync.Mutex
	data  map[string][]byte
	dirty map[string]*list.Element // key -> queue slot; presence is the dirty flag
	queue *list.List               // FIFO of *entry, ordered by first dirtying
	seq   uint64                   // last allocated sequence number
	wm    uint64                   // watermark: every op with Seq <= wm is durable in the source

	wake      chan struct{}
	stop      chan struct{}
	done      chan struct{} // closed when the flusher goroutine exits
	started   bool          // whether the background flusher was started
	closeOnce sync.Once
	closed    bool
}

// New validates opts, starts the background flusher and returns the cache.
func New(opts Options) *Cache {
	c := newCache(opts)
	c.started = true
	go c.run()
	return c
}

// newCache builds a Cache without the background goroutine so tests can
// drive flushOnce directly and deterministically.
func newCache(opts Options) *Cache {
	if opts.Sink == nil {
		panic("writeback: Options.Sink is required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 64
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 100 * time.Millisecond
	}
	if opts.RetryBackoff <= 0 {
		opts.RetryBackoff = 50 * time.Millisecond
	}
	return &Cache{
		opts:  opts,
		data:  make(map[string][]byte),
		dirty: make(map[string]*list.Element),
		queue: list.New(),
		wake:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// Set buffers a write. It is visible to Get immediately and flushed later.
func (c *Cache) Set(key string, value []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.seq++
	v := append([]byte(nil), value...)
	c.data[key] = v
	c.markDirtyLocked(Op{Seq: c.seq, Kind: OpSet, Key: key, Value: v, At: c.opts.Now()})
	c.wakeUp()
	return nil
}

// Delete buffers a deletion.
func (c *Cache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.seq++
	delete(c.data, key)
	c.markDirtyLocked(Op{Seq: c.seq, Kind: OpDelete, Key: key, At: c.opts.Now()})
	c.wakeUp()
	return nil
}

// markDirtyLocked merges the op into the key's existing slot (keeping its
// queue position and firstSeq) or appends a new slot at the back. Because
// a slot always carries the latest mutation of its key, a delete can never
// jump ahead of an earlier write to the same key, nor vice versa.
func (c *Cache) markDirtyLocked(op Op) {
	if el, ok := c.dirty[op.Key]; ok {
		el.Value.(*entry).op = op
		return
	}
	c.dirty[op.Key] = c.queue.PushBack(&entry{op: op, firstSeq: op.Seq})
}

// Get reads the in-memory cache, including not-yet-flushed writes.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), v...), true
}

// Watermark returns the durability watermark: every op with Seq <=
// Watermark() is durable in the backing source.
func (c *Cache) Watermark() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wm
}

// DirtyLen reports how many queue slots are still unflushed.
func (c *Cache) DirtyLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queue.Len()
}

// Unflushed returns a snapshot of the pending ops, in flush order.
func (c *Cache) Unflushed() []Op {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unflushedLocked()
}

func (c *Cache) unflushedLocked() []Op {
	ops := make([]Op, 0, c.queue.Len())
	for e := c.queue.Front(); e != nil; e = e.Next() {
		ops = append(ops, e.Value.(*entry).op)
	}
	return ops
}

// Done is closed when the background flusher goroutine has exited, i.e.
// when its resources have been released.
func (c *Cache) Done() <-chan struct{} { return c.done }

func (c *Cache) wakeUp() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// run is the background flusher. It flushes on writes, on the interval
// ticker, and on the retry timer after a failure.
func (c *Cache) run() {
	defer close(c.done)
	tick := time.NewTicker(c.opts.FlushInterval)
	defer tick.Stop()
	var retry *time.Timer
	var retryC <-chan time.Time
	defer func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	flush := func() {
		if err := c.flushOnce(); err != nil && retryC == nil {
			retry = time.NewTimer(c.opts.RetryBackoff)
			retryC = retry.C
		}
	}
	for {
		select {
		case <-c.stop:
			return
		case <-c.wake:
			flush()
		case <-tick.C:
			flush()
		case <-retryC:
			retryC = nil
			flush()
		}
	}
}

// flushOnce writes at most one batch to the source. On failure nothing is
// dequeued and no dirty flag is cleared, so the batch is retried later in
// the same order.
func (c *Cache) flushOnce() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.queue.Len() == 0 {
		return nil
	}
	batch := make([]Op, 0, c.opts.BatchSize)
	elems := make([]*list.Element, 0, c.opts.BatchSize)
	for e := c.queue.Front(); e != nil && len(batch) < c.opts.BatchSize; e = e.Next() {
		batch = append(batch, e.Value.(*entry).op)
		elems = append(elems, e)
	}
	if err := c.opts.Sink(context.Background(), batch); err != nil {
		return err
	}
	for i, e := range elems {
		delete(c.dirty, batch[i].Key)
		c.queue.Remove(e)
	}
	c.advanceWatermarkLocked()
	return nil
}

// advanceWatermarkLocked maintains the invariant that every op with
// Seq <= wm is durable in the source. A queued slot covers ops
// [firstSeq, op.Seq], so all ops below the minimum queued firstSeq are
// covered by slots already flushed.
func (c *Cache) advanceWatermarkLocked() {
	if c.queue.Len() == 0 {
		c.wm = c.seq
		return
	}
	min := c.queue.Front().Value.(*entry).firstSeq
	for e := c.queue.Front(); e != nil; e = e.Next() {
		if fs := e.Value.(*entry).firstSeq; fs < min {
			min = fs
		}
	}
	c.wm = min - 1
}

// Close stops the background flusher, waits for its resources to be
// released, then drains the dirty queue. It blocks until the queue is
// empty or ctx is done; in the latter case it returns the unflushed ops
// in order together with the ctx error. A nil ctx means wait forever.
func (c *Cache) Close(ctx context.Context) ([]Op, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		close(c.stop)
	})
	if c.started {
		<-c.done // flusher goroutine exited: ticker/timer released
	}
	for {
		c.mu.Lock()
		empty := c.queue.Len() == 0
		c.mu.Unlock()
		if empty {
			return nil, nil
		}
		if err := c.flushOnce(); err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return c.Unflushed(), ctx.Err()
		case <-time.After(c.opts.RetryBackoff):
		}
	}
}
