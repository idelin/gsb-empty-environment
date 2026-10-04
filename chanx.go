// Package chanx implements a generic bounded channel with context-aware
// Send/Recv and Close semantics. The capacity may be 0, in which case a
// Send completes only when paired with a Recv (rendezvous).
//
// # Synchronization strategy
//
// All shared state is guarded by a single sync.Mutex; every operation has
// exactly one linearization point taken while holding that mutex. Values
// live in a ring buffer owned by the Chan struct — Go channels are NOT
// used as the data queue. Blocked senders and receivers sit in two FIFO
// lists (container/list). Each waiter carries a small notification
// channel used purely as a wakeup signal (a one-shot "something changed"
// event); values are never transported through it.
//
// Linearization rules:
//
//   - Delivery is committed under the mutex: the delivering side stores
//     the value into the waiter and sets waiter.delivered = true before
//     signalling it. From that instant the exchange has happened.
//   - Cancellation re-acquires the mutex and inspects waiter.delivered.
//     If delivery was already committed, the success result wins and the
//     value is returned/considered sent; otherwise the waiter is unlinked
//     and the cancellation result wins. Because both sides serialize on
//     the same mutex, exactly one of the two outcomes is possible — a
//     cancelled Send's value can never be received afterwards, and a
//     cancelled Recv never swallows a delivered value.
//   - Close sets closed under the mutex and wakes every waiter. Blocked
//     senders observe closed and are rejected (their values are never
//     delivered); blocked receivers re-check state, drain any buffered
//     values in FIFO order, and only then report the closed marker.
//
// Invariants (checked implicitly by the code paths):
//   - sendQ non-empty  => recvQ empty and buffer full (or capacity 0)
//   - recvQ non-empty  => buffer empty and sendQ empty
package chanx

import (
	"container/list"
	"context"
	"errors"
	"sync"
)

// ErrClosed is returned by Send and Recv when the channel is closed.
// Send returns it when the send is rejected; Recv returns it once the
// buffer has been fully drained.
var ErrClosed = errors.New("chanx: channel closed")

// HookEvent identifies an internal synchronization point. It is used only
// by tests (same package) to build controlled barriers.
type HookEvent string

const (
	HookSendEnqueued HookEvent = "send-enqueued" // a sender blocked and joined sendQ
	HookRecvEnqueued HookEvent = "recv-enqueued" // a receiver blocked and joined recvQ
	HookDelivered    HookEvent = "delivered"     // a value handoff was committed
	HookClosed       HookEvent = "closed"        // Close took effect and woke waiters
)

type sendWaiter[T any] struct {
	val       T
	delivered bool
	wake      chan struct{} // closed exactly once to wake the waiter
}

type recvWaiter[T any] struct {
	val       T
	delivered bool
	wake      chan struct{}
}

// Chan is a generic bounded channel. The zero value is not usable; use New.
type Chan[T any] struct {
	mu     sync.Mutex
	buf    []T // ring buffer, len == capacity (may be 0)
	head   int // index of oldest element
	size   int // number of buffered elements
	closed bool
	sendQ  list.List // of *sendWaiter[T], FIFO
	recvQ  list.List // of *recvWaiter[T], FIFO

	// hook, when non-nil, is invoked with mu held at key synchronization
	// points. Test-only instrumentation; nil in production use.
	hook func(HookEvent)
}

// New returns a channel with the given capacity. capacity == 0 yields a
// rendezvous channel: a Send completes only when paired with a Recv.
func New[T any](capacity int) *Chan[T] {
	if capacity < 0 {
		panic("chanx: negative capacity")
	}
	return &Chan[T]{buf: make([]T, capacity)}
}

func (c *Chan[T]) emit(ev HookEvent) {
	if c.hook != nil {
		c.hook(ev)
	}
}

// Send delivers v to a waiting receiver or into the buffer, blocking if
// necessary. It returns nil on successful delivery, ErrClosed if the
// channel is or becomes closed before delivery, or ctx.Err() if the
// context is cancelled first. If Send returns a non-nil error, v is
// guaranteed never to be observed by any Recv.
func (c *Chan[T]) Send(ctx context.Context, v T) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	// Direct handoff to the oldest waiting receiver.
	if c.recvQ.Len() > 0 {
		e := c.recvQ.Front()
		w := e.Value.(*recvWaiter[T])
		c.recvQ.Remove(e)
		w.val = v
		w.delivered = true
		c.emit(HookDelivered)
		close(w.wake)
		c.mu.Unlock()
		return nil
	}
	// Buffer the value if there is room.
	if c.size < len(c.buf) {
		c.buf[(c.head+c.size)%len(c.buf)] = v
		c.size++
		c.mu.Unlock()
		return nil
	}
	// Block: enqueue a waiter and wait for delivery, close, or cancel.
	w := &sendWaiter[T]{val: v, wake: make(chan struct{})}
	e := c.sendQ.PushBack(w)
	c.emit(HookSendEnqueued)
	c.mu.Unlock()

	select {
	case <-w.wake:
		c.mu.Lock()
		delivered := w.delivered
		c.mu.Unlock()
		if delivered {
			return nil
		}
		return ErrClosed // woken by Close without delivery
	case <-ctx.Done():
		c.mu.Lock()
		if w.delivered {
			// Delivery was committed before we got the lock: success wins.
			c.mu.Unlock()
			return nil
		}
		c.sendQ.Remove(e) // no-op if Close already unlinked us
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Recv returns the next value in FIFO order. It returns ErrClosed once
// the channel is closed and all buffered values have been drained, or
// ctx.Err() if the context is cancelled while waiting. A cancelled Recv
// never consumes a value.
func (c *Chan[T]) Recv(ctx context.Context) (T, error) {
	c.mu.Lock()
	for {
		if c.size > 0 {
			v := c.buf[c.head]
			var zero T
			c.buf[c.head] = zero
			c.head = (c.head + 1) % len(c.buf)
			c.size--
			// A freed slot lets the oldest blocked sender commit its
			// value into the buffer, preserving global FIFO order.
			if c.sendQ.Len() > 0 {
				e := c.sendQ.Front()
				w := e.Value.(*sendWaiter[T])
				c.sendQ.Remove(e)
				c.buf[(c.head+c.size)%len(c.buf)] = w.val
				c.size++
				var zw T
				w.val = zw
				w.delivered = true
				c.emit(HookDelivered)
				close(w.wake)
			}
			c.mu.Unlock()
			return v, nil
		}
		// Rendezvous with the oldest blocked sender (capacity 0 case).
		if c.sendQ.Len() > 0 {
			e := c.sendQ.Front()
			w := e.Value.(*sendWaiter[T])
			c.sendQ.Remove(e)
			v := w.val
			var zero T
			w.val = zero
			w.delivered = true
			c.emit(HookDelivered)
			close(w.wake)
			c.mu.Unlock()
			return v, nil
		}
		if c.closed {
			c.mu.Unlock()
			var zero T
			return zero, ErrClosed
		}
		// Block: enqueue a waiter and wait for delivery, close, or cancel.
		w := &recvWaiter[T]{wake: make(chan struct{})}
		e := c.recvQ.PushBack(w)
		c.emit(HookRecvEnqueued)
		c.mu.Unlock()

		select {
		case <-w.wake:
			c.mu.Lock()
			if w.delivered {
				v := w.val
				c.mu.Unlock()
				return v, nil
			}
			// Woken by Close: loop and re-check state with mu held
			// (drain remaining values, then report closed).
		case <-ctx.Done():
			c.mu.Lock()
			if w.delivered {
				// Delivery was committed before we got the lock:
				// the value is ours, cancellation loses.
				v := w.val
				c.mu.Unlock()
				return v, nil
			}
			c.recvQ.Remove(e) // no-op if Close already unlinked us
			c.mu.Unlock()
			var zero T
			return zero, ctx.Err()
		}
	}
}

// Close marks the channel closed and wakes all blocked senders and
// receivers. Blocked senders are rejected with ErrClosed; receivers drain
// any buffered values and then observe ErrClosed. Close is idempotent.
func (c *Chan[T]) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	for c.sendQ.Len() > 0 {
		e := c.sendQ.Front()
		w := e.Value.(*sendWaiter[T])
		c.sendQ.Remove(e)
		close(w.wake) // delivered stays false: sender is rejected
	}
	for c.recvQ.Len() > 0 {
		e := c.recvQ.Front()
		w := e.Value.(*recvWaiter[T])
		c.recvQ.Remove(e)
		close(w.wake) // delivered stays false: receiver re-checks state
	}
	c.emit(HookClosed)
	c.mu.Unlock()
}
