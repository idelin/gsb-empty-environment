// Package batcher implements a batching writer. Requests accumulate
// until either maxSize requests are queued or the oldest queued request
// has waited maxDelay, whichever happens first; the accumulated batch
// is then handed to a user-supplied write function.
package batcher

import (
	"errors"
	"sync"
	"time"
)

// ErrCanceled is delivered on a Request's result channel when it is
// canceled before being committed to a batch.
var ErrCanceled = errors.New("batcher: request canceled")

// WriteFunc persists one batch. A non-nil return marks the whole batch
// as failed; the error is delivered individually to every member.
type WriteFunc func(batch []any) error

// Batcher accumulates requests and flushes them in batches.
//
// Flush triggers:
//   - size: the queue reaches maxSize, or
//   - age:  the oldest queued request has been waiting maxDelay.
//
// The age timer is armed when the first request joins an empty queue
// and is never extended by later arrivals, so a steady stream of new
// requests cannot postpone the flush of older ones beyond maxDelay.
//
// Flushes run synchronously in the goroutine that triggered them (the
// filling Enqueue call, or the timer callback). All queue mutations
// happen under a single mutex, so a flush and an enqueue occurring at
// the same instant are serialized: the request lands either in the
// flushing batch or in the next one, never both, never neither.
type Batcher struct {
	maxSize  int
	maxDelay time.Duration
	clock    Clock
	write    WriteFunc

	mu    sync.Mutex
	queue []*Request
	timer Timer // armed for the oldest queued request's deadline
}

// NewBatcher creates a Batcher. maxSize must be >= 1 and maxDelay > 0.
func NewBatcher(maxSize int, maxDelay time.Duration, clock Clock, write WriteFunc) *Batcher {
	if maxSize < 1 {
		panic("batcher: maxSize must be >= 1")
	}
	if maxDelay <= 0 {
		panic("batcher: maxDelay must be > 0")
	}
	if clock == nil || write == nil {
		panic("batcher: clock and write must be non-nil")
	}
	return &Batcher{maxSize: maxSize, maxDelay: maxDelay, clock: clock, write: write}
}

// Enqueue adds value to the current batch and returns a handle for the
// result. If this request fills the batch, the batch is flushed before
// Enqueue returns.
func (b *Batcher) Enqueue(value any) *Request {
	b.mu.Lock()
	if len(b.queue) == 0 {
		// Arm the age timer for this, now the oldest, request. It is
		// not reset by subsequent enqueues.
		b.timer = b.clock.AfterFunc(b.maxDelay, b.onTimer)
	}
	req := &Request{
		value:    value,
		result:   make(chan error, 1),
		deadline: b.clock.Now().Add(b.maxDelay),
		b:        b,
	}
	b.queue = append(b.queue, req)
	if len(b.queue) < b.maxSize {
		b.mu.Unlock()
		return req
	}
	batch := b.takeLocked()
	b.mu.Unlock()
	b.execute(batch)
	return req
}

// onTimer fires when the oldest queued request reaches its deadline.
func (b *Batcher) onTimer() {
	b.mu.Lock()
	if len(b.queue) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.takeLocked()
	b.mu.Unlock()
	b.execute(batch)
}

// takeLocked detaches the whole queue as one batch and disarms the age
// timer. Callers must hold b.mu.
func (b *Batcher) takeLocked() []*Request {
	batch := b.queue
	b.queue = nil
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	for _, req := range batch {
		req.committed = true
	}
	return batch
}

// execute runs the write outside the lock and delivers the outcome to
// every member of the batch individually.
func (b *Batcher) execute(batch []*Request) {
	values := make([]any, len(batch))
	for i, req := range batch {
		values[i] = req.value
	}
	err := b.write(values)
	for _, req := range batch {
		req.result <- err
	}
}

// removeLocked drops req from the queue and fixes up the age timer.
// Callers must hold b.mu and know req is still queued.
func (b *Batcher) removeLocked(req *Request) {
	wasOldest := b.queue[0] == req
	for i, q := range b.queue {
		if q == req {
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			break
		}
	}
	switch {
	case len(b.queue) == 0:
		if b.timer != nil {
			b.timer.Stop()
			b.timer = nil
		}
	case wasOldest:
		// The canceled request owned the deadline; re-arm for the new
		// oldest request so its wait still cannot exceed maxDelay.
		if b.timer != nil {
			b.timer.Stop()
		}
		remaining := time.Duration(b.queue[0].deadline.Sub(b.clock.Now()))
		if remaining < 0 {
			remaining = 0
		}
		b.timer = b.clock.AfterFunc(remaining, b.onTimer)
	}
}

// Request is a handle to one enqueued item.
type Request struct {
	value    any
	result   chan error
	deadline time.Time

	b         *Batcher
	committed bool // guarded by b.mu: included in a batch, cancel no-op
}

// Result returns a channel that receives exactly one value: nil on
// success, the batch's write error, or ErrCanceled.
func (r *Request) Result() <-chan error { return r.result }

// Cancel removes the request if it has not yet been committed to a
// batch. It reports whether the cancellation won the race. A committed
// request is unaffected and still receives its batch's outcome.
func (r *Request) Cancel() bool {
	b := r.b
	b.mu.Lock()
	if r.committed {
		b.mu.Unlock()
		return false
	}
	b.removeLocked(r)
	b.mu.Unlock()
	r.result <- ErrCanceled
	return true
}
