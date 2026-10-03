package corochan

import "sync"

// waiter is a parked coroutine. Each waiter owns a private, buffered(1)
// doorbell channel, so the peer that completes the rendezvous wakes exactly
// one matching coroutine (precise wakeup, no thundering herd).
type waiter[T any] struct {
	val    T
	ok     bool // for receivers: whether val is a real value (false => EOF)
	closed bool // for senders: woken by Close => send-on-closed panic
	wake   chan struct{}
}

// Channel is a coroutine synchronization primitive with Go channel semantics:
// buffered or unbuffered, automatic parking when full/empty, precise wakeup
// of the matching peer, and Close broadcasting EOF to all waiting receivers.
//
// The zero value is not usable; construct with NewChannel.
type Channel[T any] struct {
	mu     sync.Mutex
	buf    *RingQueue[T] // nil for unbuffered channels
	n      int           // values currently buffered (exact capacity tracking)
	cap    int
	sendQ  []*waiter[T] // FIFO: senders parked on a full channel
	recvQ  []*waiter[T] // FIFO: receivers parked on an empty channel
	closed bool
}

// NewChannel creates a Channel. capacity == 0 yields an unbuffered
// (rendezvous) channel; capacity > 0 yields a buffered channel holding at
// most exactly capacity values.
func NewChannel[T any](capacity int) *Channel[T] {
	if capacity < 0 {
		panic("corochan: negative channel capacity")
	}
	ch := &Channel[T]{cap: capacity}
	if capacity > 0 {
		ch.buf = NewRingQueue[T](capacity)
	}
	return ch
}

// Send delivers v to the channel. It blocks the calling coroutine while the
// channel is full (buffered) or has no waiting receiver (unbuffered), and
// panics if the channel is closed before or during the send.
func (ch *Channel[T]) Send(v T) {
	ch.mu.Lock()
	if ch.closed {
		ch.mu.Unlock()
		panic("send on closed channel")
	}
	// Precise wakeup: hand the value directly to the oldest parked receiver.
	if len(ch.recvQ) > 0 {
		w := ch.recvQ[0]
		ch.recvQ = ch.recvQ[1:]
		w.val = v
		w.ok = true
		ch.mu.Unlock()
		w.wake <- struct{}{}
		return
	}
	// Fast path: room in the buffer.
	if ch.n < ch.cap {
		_ = ch.buf.Enqueue(v) // cannot fail: n < cap <= ring capacity
		ch.n++
		ch.mu.Unlock()
		return
	}
	// Park this coroutine until a receiver makes room / takes the value.
	w := &waiter[T]{val: v, wake: make(chan struct{}, 1)}
	ch.sendQ = append(ch.sendQ, w)
	ch.mu.Unlock()

	<-w.wake
	if w.closed {
		panic("send on closed channel")
	}
}

// Recv returns the next value. It blocks the calling coroutine while the
// channel is empty and not closed. The second result is false once the
// channel is closed and drained (EOF).
func (ch *Channel[T]) Recv() (T, bool) {
	ch.mu.Lock()
	// Buffered fast path.
	if ch.n > 0 {
		v, _ := ch.buf.Dequeue() // cannot fail: n > 0
		ch.n--
		// The freed slot goes to the oldest parked sender, which is then
		// woken precisely to complete its Send.
		if len(ch.sendQ) > 0 {
			w := ch.sendQ[0]
			ch.sendQ = ch.sendQ[1:]
			_ = ch.buf.Enqueue(w.val)
			ch.n++
			ch.mu.Unlock()
			w.wake <- struct{}{}
			return v, true
		}
		ch.mu.Unlock()
		return v, true
	}
	// Rendezvous with a parked sender (unbuffered channel, or buffered
	// channel whose buffer was drained while senders were parked).
	if len(ch.sendQ) > 0 {
		w := ch.sendQ[0]
		ch.sendQ = ch.sendQ[1:]
		v := w.val
		ch.mu.Unlock()
		w.wake <- struct{}{}
		return v, true
	}
	// Closed and drained: EOF.
	if ch.closed {
		ch.mu.Unlock()
		var zero T
		return zero, false
	}
	// Park this coroutine until a sender (or Close) arrives.
	w := &waiter[T]{wake: make(chan struct{}, 1)}
	ch.recvQ = append(ch.recvQ, w)
	ch.mu.Unlock()

	<-w.wake
	return w.val, w.ok
}

// TrySend performs a non-blocking send, reporting whether it succeeded.
func (ch *Channel[T]) TrySend(v T) bool {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		panic("send on closed channel")
	}
	if len(ch.recvQ) > 0 {
		w := ch.recvQ[0]
		ch.recvQ = ch.recvQ[1:]
		w.val = v
		w.ok = true
		w.wake <- struct{}{}
		return true
	}
	if ch.n < ch.cap {
		_ = ch.buf.Enqueue(v)
		ch.n++
		return true
	}
	return false
}

// TryRecv performs a non-blocking receive. The second result reports whether
// a value was obtained, the third whether the channel is still open.
func (ch *Channel[T]) TryRecv() (v T, received, open bool) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.n > 0 {
		val, _ := ch.buf.Dequeue()
		ch.n--
		if len(ch.sendQ) > 0 {
			w := ch.sendQ[0]
			ch.sendQ = ch.sendQ[1:]
			_ = ch.buf.Enqueue(w.val)
			ch.n++
			w.wake <- struct{}{}
		}
		return val, true, true
	}
	if len(ch.sendQ) > 0 {
		w := ch.sendQ[0]
		ch.sendQ = ch.sendQ[1:]
		w.wake <- struct{}{}
		return w.val, true, true
	}
	if ch.closed {
		return v, false, false
	}
	return v, false, true
}

// Close marks the channel closed and broadcasts to every parked coroutine:
// all waiting receivers are woken with EOF (ok == false), and all waiting
// senders are woken to panic with "send on closed channel" (Go semantics).
// Closing a closed channel panics.
func (ch *Channel[T]) Close() {
	ch.mu.Lock()
	if ch.closed {
		ch.mu.Unlock()
		panic("close of closed channel")
	}
	ch.closed = true
	receivers := ch.recvQ
	senders := ch.sendQ
	ch.recvQ = nil
	ch.sendQ = nil
	ch.mu.Unlock()

	for _, w := range receivers {
		w.ok = false // EOF
		w.wake <- struct{}{}
	}
	for _, w := range senders {
		w.closed = true
		w.wake <- struct{}{}
	}
}

// Closed reports whether Close has been called.
func (ch *Channel[T]) Closed() bool {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return ch.closed
}

// Len returns the number of values currently buffered.
func (ch *Channel[T]) Len() int {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return ch.n
}
