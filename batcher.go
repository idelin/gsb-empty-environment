// Package batcher groups individual requests into batches executed by a
// user-supplied function. A batch is flushed when it reaches the maximum
// size or when the first item has waited for the maximum wait duration,
// whichever comes first.
package batcher

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrClosed is returned by Submit after the Batcher has been closed.
var ErrClosed = errors.New("batcher: closed")

// ErrResultCountMismatch is delivered to every surviving item of a batch
// when the execute function returns a result slice whose length differs
// from the number of items it was given.
var ErrResultCountMismatch = errors.New("batcher: execute returned wrong number of results")

// ExecFunc executes one sealed batch. It must return exactly one result per
// input item, in the same order. If it returns an error, that error is
// delivered to every surviving item of the batch.
type ExecFunc[I, O any] func(ctx context.Context, items []I) ([]O, error)

// Clock abstracts timer creation so tests can inject fake time.
type Clock interface {
	// NewTimer mirrors time.NewTimer: the channel receives a single value
	// after d unless stop is called first. stop reports whether the timer
	// was halted before firing.
	NewTimer(d time.Duration) (c <-chan time.Time, stop func() bool)
}

type realClock struct{}

func (realClock) NewTimer(d time.Duration) (<-chan time.Time, func() bool) {
	t := time.NewTimer(d)
	return t.C, t.Stop
}

// RealClock returns a Clock backed by time.NewTimer.
func RealClock() Clock { return realClock{} }

type outcome[O any] struct {
	value O
	err   error
}

type request[I, O any] struct {
	item   I
	ctx    context.Context
	result chan outcome[O] // buffered (cap 1): flush never blocks
	batch  *batch[I, O]    // batch the request was appended to
}

type batch[I, O any] struct {
	reqs      []*request[I, O]
	fire      <-chan time.Time
	stopTimer func() bool
	done      chan struct{} // closed when the batch is sealed
}

// Batcher accumulates submitted items and flushes them in sealed batches.
// The zero value is not usable; construct with New.
type Batcher[I, O any] struct {
	maxSize int
	maxWait time.Duration
	exec    ExecFunc[I, O]
	clock   Clock

	mu      sync.Mutex
	pending *batch[I, O] // open batch; nil when no batch is collecting
	closed  bool
	wg      sync.WaitGroup // timer watchers and in-flight flushes
}

// New creates a Batcher. maxSize is the maximum number of items per batch;
// maxWait is the maximum time the first item of a batch waits before the
// batch is flushed. exec is called once per sealed batch. If clock is nil,
// real time is used.
func New[I, O any](maxSize int, maxWait time.Duration, exec ExecFunc[I, O], clock Clock) *Batcher[I, O] {
	if maxSize < 1 {
		panic("batcher: maxSize must be >= 1")
	}
	if maxWait <= 0 {
		panic("batcher: maxWait must be > 0")
	}
	if exec == nil {
		panic("batcher: exec must not be nil")
	}
	if clock == nil {
		clock = RealClock()
	}
	return &Batcher[I, O]{
		maxSize: maxSize,
		maxWait: maxWait,
		exec:    exec,
		clock:   clock,
	}
}

// Submit adds item to the currently open batch and blocks until the batch
// containing it has been executed, ctx is cancelled, or the Batcher is
// closed. A cancelled item is removed from its batch and never reaches the
// execute function.
func (b *Batcher[I, O]) Submit(ctx context.Context, item I) (O, error) {
	var zero O
	req := &request[I, O]{item: item, ctx: ctx, result: make(chan outcome[O], 1)}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return zero, ErrClosed
	}
	if b.pending == nil {
		p := &batch[I, O]{done: make(chan struct{})}
		p.fire, p.stopTimer = b.clock.NewTimer(b.maxWait)
		b.pending = p
		b.wg.Add(1)
		go b.watchTimer(p)
	}
	req.batch = b.pending
	b.pending.reqs = append(b.pending.reqs, req)
	if len(b.pending.reqs) >= b.maxSize {
		b.sealLocked()
	}
	b.mu.Unlock()

	select {
	case out := <-req.result:
		return out.value, out.err
	case <-ctx.Done():
		b.remove(req)
		return zero, ctx.Err()
	}
}

// remove takes a cancelled request out of its batch, provided the batch is
// still open (requests of a sealed batch are filtered by flush instead).
// A batch left empty is discarded so that the next submission starts a
// fresh batch with a fresh wait timer.
func (b *Batcher[I, O]) remove(req *request[I, O]) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := req.batch
	if p == nil || b.pending != p {
		return
	}
	for i, r := range p.reqs {
		if r == req {
			p.reqs = append(p.reqs[:i], p.reqs[i+1:]...)
			break
		}
	}
	if len(p.reqs) == 0 {
		b.pending = nil
		p.stopTimer()
		close(p.done)
	}
}

// Close stops accepting new submissions, flushes any pending batch, and
// blocks until all in-flight batches have finished executing.
func (b *Batcher[I, O]) Close() {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		if b.pending != nil {
			b.sealLocked()
		}
	}
	b.mu.Unlock()
	b.wg.Wait()
}

// watchTimer flushes the batch when its wait deadline passes. If the batch
// is sealed early (size reached or Close), done is closed and the watcher
// exits without flushing.
func (b *Batcher[I, O]) watchTimer(p *batch[I, O]) {
	defer b.wg.Done()
	select {
	case <-p.fire:
		b.mu.Lock()
		if b.pending == p {
			b.sealLocked()
		}
		b.mu.Unlock()
	case <-p.done:
	}
}

// sealLocked detaches the pending batch so that new submissions start a
// fresh batch, then executes the sealed batch asynchronously. The caller
// must hold b.mu.
func (b *Batcher[I, O]) sealLocked() {
	p := b.pending
	b.pending = nil
	p.stopTimer()
	close(p.done)
	b.wg.Add(1)
	go b.flush(p)
}

// flush drops cancelled requests, executes the surviving items, and
// delivers one outcome to every surviving request. If no request survives,
// the execute function is not called.
func (b *Batcher[I, O]) flush(p *batch[I, O]) {
	defer b.wg.Done()

	alive := make([]*request[I, O], 0, len(p.reqs))
	for _, r := range p.reqs {
		if r.ctx.Err() == nil {
			alive = append(alive, r)
		}
	}
	if len(alive) == 0 {
		return
	}

	items := make([]I, len(alive))
	for i, r := range alive {
		items[i] = r.item
	}

	results, err := b.exec(context.Background(), items)
	if err == nil && len(results) != len(alive) {
		err = ErrResultCountMismatch
	}

	for i, r := range alive {
		var out outcome[O]
		if err != nil {
			out.err = err
		} else {
			out.value = results[i]
		}
		r.result <- out
	}
}
