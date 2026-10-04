package chanx

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// barrier returns a channel that is closed the first time the hook sees
// want, letting tests proceed only after a waiter is actually enqueued.
func barrier(c *Chan[int], want HookEvent) <-chan struct{} {
	done := make(chan struct{})
	var once sync.Once
	prev := c.hook
	c.hook = func(ev HookEvent) {
		if prev != nil {
			prev(ev)
		}
		if ev == want {
			once.Do(func() { close(done) })
		}
	}
	return done
}

// eventLog records the hook event sequence (called with c.mu held, so the
// log itself is serialized).
type eventLog struct {
	mu  sync.Mutex
	evs []HookEvent
}

func (l *eventLog) hook(ev HookEvent) {
	l.mu.Lock()
	l.evs = append(l.evs, ev)
	l.mu.Unlock()
}

func (l *eventLog) snapshot() []HookEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]HookEvent(nil), l.evs...)
}

func mustRecv(t *testing.T, c *Chan[int]) int {
	t.Helper()
	v, err := c.Recv(context.Background())
	if err != nil {
		t.Fatalf("Recv: unexpected error %v", err)
	}
	return v
}

func TestRendezvousCap0(t *testing.T) {
	c := New[int](0)
	// Send must block until a receiver arrives.
	sendDone := make(chan error, 1)
	go func() { sendDone <- c.Send(context.Background(), 42) }()
	select {
	case <-sendDone:
		t.Fatal("Send on capacity-0 channel completed without a receiver")
	case <-time.After(50 * time.Millisecond):
	}
	if v := mustRecv(t, c); v != 42 {
		t.Fatalf("got %d, want 42", v)
	}
	if err := <-sendDone; err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestBufferedFIFO(t *testing.T) {
	c := New[int](3)
	for i := 0; i < 3; i++ {
		if err := c.Send(context.Background(), i); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	// Fourth send must block (buffer full).
	fourth := make(chan error, 1)
	go func() { fourth <- c.Send(context.Background(), 3) }()
	select {
	case <-fourth:
		t.Fatal("Send completed while buffer full and no receiver")
	case <-time.After(50 * time.Millisecond):
	}
	for i := 0; i < 4; i++ {
		if v := mustRecv(t, c); v != i {
			t.Fatalf("recv %d: got %d", i, v)
		}
	}
	if err := <-fourth; err != nil {
		t.Fatalf("fourth Send: %v", err)
	}
}

func TestCloseIdempotentAndDrain(t *testing.T) {
	c := New[int](2)
	_ = c.Send(context.Background(), 1)
	_ = c.Send(context.Background(), 2)
	c.Close()
	c.Close() // idempotent: must not panic or deadlock
	// Sends after close are rejected.
	if err := c.Send(context.Background(), 3); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send after Close: got %v, want ErrClosed", err)
	}
	// Buffered values are still drained in order, then closed marker.
	if v := mustRecv(t, c); v != 1 {
		t.Fatalf("got %d, want 1", v)
	}
	if v := mustRecv(t, c); v != 2 {
		t.Fatalf("got %d, want 2", v)
	}
	if _, err := c.Recv(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Recv after drain: got %v, want ErrClosed", err)
	}
}

// Close must wake blocked senders; their values are never delivered.
func TestCloseWakesBlockedSenders(t *testing.T) {
	c := New[int](1)
	_ = c.Send(context.Background(), 0) // fills buffer
	enqueued := barrier(c, HookSendEnqueued)
	sendDone := make(chan error, 1)
	go func() { sendDone <- c.Send(context.Background(), 99) }()
	<-enqueued // sender is queued behind the full buffer
	c.Close()
	if err := <-sendDone; !errors.Is(err, ErrClosed) {
		t.Fatalf("blocked Send after Close: got %v, want ErrClosed", err)
	}
	// The rejected value must never appear.
	if v := mustRecv(t, c); v != 0 {
		t.Fatalf("got %d, want 0", v)
	}
	if _, err := c.Recv(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v, want ErrClosed", err)
	}
}

func TestCloseWakesBlockedReceivers(t *testing.T) {
	c := New[int](0)
	enqueued := barrier(c, HookRecvEnqueued)
	recvDone := make(chan error, 1)
	go func() {
		_, err := c.Recv(context.Background())
		recvDone <- err
	}()
	<-enqueued
	c.Close()
	if err := <-recvDone; !errors.Is(err, ErrClosed) {
		t.Fatalf("blocked Recv after Close: got %v, want ErrClosed", err)
	}
}

// A cancelled Send must guarantee its value is never received.
func TestSendCancelValueNeverReceived(t *testing.T) {
	c := New[int](0)
	ctx, cancel := context.WithCancel(context.Background())
	enqueued := barrier(c, HookSendEnqueued)
	sendDone := make(chan error, 1)
	go func() { sendDone <- c.Send(ctx, 77) }()
	<-enqueued
	cancel()
	if err := <-sendDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	// No receiver may ever observe 77.
	rctx, rcancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer rcancel()
	if _, err := c.Recv(rctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded (value leaked!)", err)
	}
}

// A cancelled Recv must not swallow a value.
func TestRecvCancelDoesNotSwallow(t *testing.T) {
	c := New[int](0)
	ctx, cancel := context.WithCancel(context.Background())
	enqueued := barrier(c, HookRecvEnqueued)
	recvDone := make(chan error, 1)
	go func() {
		_, err := c.Recv(ctx)
		recvDone <- err
	}()
	<-enqueued
	cancel()
	if err := <-recvDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	// The cancelled receiver is gone; a fresh pair exchanges normally.
	go func() { _ = c.Send(context.Background(), 5) }()
	if v := mustRecv(t, c); v != 5 {
		t.Fatalf("got %d, want 5", v)
	}
}

// Race: cancel a blocked Send while a Recv arrives concurrently.
// Exactly one linearization is legal: either the send succeeds and the
// receiver gets the value, or the send is cancelled and the value never
// appears. Looped to stress the mutex race window.
func TestSendCancelDeliveryRace(t *testing.T) {
	for i := 0; i < 2000; i++ {
		c := New[int](0)
		ctx, cancel := context.WithCancel(context.Background())
		enqueued := barrier(c, HookSendEnqueued)
		sendDone := make(chan error, 1)
		go func() { sendDone <- c.Send(ctx, i) }()
		<-enqueued
		go cancel() // fire concurrently with the receiver below

		rctx, rcancel := context.WithCancel(context.Background())
		type rr struct {
			v   int
			err error
		}
		recvDone := make(chan rr, 1)
		go func() {
			v, err := c.Recv(rctx)
			recvDone <- rr{v, err}
		}()

		serr := <-sendDone
		if serr == nil {
			r := <-recvDone
			if r.err != nil || r.v != i {
				t.Fatalf("iter %d: send succeeded but recv = (%d, %v)", i, r.v, r.err)
			}
			rcancel()
		} else {
			if !errors.Is(serr, context.Canceled) {
				t.Fatalf("iter %d: unexpected send error %v", i, serr)
			}
			rcancel()
			r := <-recvDone
			if !errors.Is(r.err, context.Canceled) {
				t.Fatalf("iter %d: cancelled send yet recv = (%d, %v)", i, r.v, r.err)
			}
			// And the value must not surface later either.
			c.Close()
			if _, err := c.Recv(context.Background()); !errors.Is(err, ErrClosed) {
				t.Fatalf("iter %d: cancelled value %d leaked after close", i, i)
			}
		}
		cancel()
	}
}

// Race: cancel a blocked Recv while a Send delivers concurrently.
// Either the recv returns the value, or it is cancelled and the value
// remains for the next receiver.
func TestRecvCancelDeliveryRace(t *testing.T) {
	for i := 0; i < 2000; i++ {
		c := New[int](1) // buffered so Send never blocks after a cancelled recv
		ctx, cancel := context.WithCancel(context.Background())
		enqueued := barrier(c, HookRecvEnqueued)
		type rr struct {
			v   int
			err error
		}
		recvDone := make(chan rr, 1)
		go func() {
			v, err := c.Recv(ctx)
			recvDone <- rr{v, err}
		}()
		<-enqueued
		go cancel()
		if err := c.Send(context.Background(), i); err != nil {
			t.Fatalf("iter %d: Send: %v", i, err)
		}
		r := <-recvDone
		switch {
		case r.err == nil:
			if r.v != i {
				t.Fatalf("iter %d: got %d, want %d", i, r.v, i)
			}
		case errors.Is(r.err, context.Canceled):
			// Value must still be there for the next receiver.
			if v := mustRecv(t, c); v != i {
				t.Fatalf("iter %d: cancelled recv swallowed %d (got %d)", i, i, v)
			}
		default:
			t.Fatalf("iter %d: unexpected recv error %v", i, r.err)
		}
		cancel()
	}
}

// Blocked senders are delivered in FIFO enqueue order.
func TestSenderFIFOOrder(t *testing.T) {
	c := New[int](1)
	_ = c.Send(context.Background(), 0) // fill buffer
	for i := 1; i <= 3; i++ {
		enq := barrier(c, HookSendEnqueued) // next enqueue is sender i's
		i := i
		go func() { _ = c.Send(context.Background(), i) }()
		<-enq // ensure enqueue order 1,2,3 before starting next
	}
	for want := 0; want <= 3; want++ {
		if v := mustRecv(t, c); v != want {
			t.Fatalf("got %d, want %d (FIFO violated)", v, want)
		}
	}
}

// Blocked receivers are served in FIFO enqueue order, and a Send hands
// off directly to the oldest enqueued receiver (adjacent events:
// recv-enqueued ... delivered, with no buffering in between).
func TestReceiverFIFOAndDirectHandoff(t *testing.T) {
	c := New[int](0)
	log := &eventLog{}
	c.hook = log.hook
	results := make([]chan int, 3)
	for i := 0; i < 3; i++ {
		enqueued := barrier(c, HookRecvEnqueued) // next enqueue is receiver i's
		results[i] = make(chan int, 1)
		ch := results[i]
		go func() {
			v, err := c.Recv(context.Background())
			if err != nil {
				t.Errorf("Recv: %v", err)
			}
			ch <- v
		}()
		<-enqueued // receiver i is enqueued before receiver i+1
	}
	for i := 0; i < 3; i++ {
		if err := c.Send(context.Background(), 10+i); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if v := <-results[i]; v != 10+i {
			t.Fatalf("receiver %d got %d, want %d", i, v, 10+i)
		}
	}
	// Event adjacency: each delivery directly follows an enqueue phase;
	// no send ever needed to block (no send-enqueued events).
	for _, ev := range log.snapshot() {
		if ev == HookSendEnqueued {
			t.Fatal("sender blocked despite waiting receivers")
		}
	}
}

// Hook-observed adjacency: close wakes an enqueued sender; the sequence
// send-enqueued -> closed must be adjacent with no delivery in between.
func TestCloseEnqueueAdjacency(t *testing.T) {
	c := New[int](0)
	log := &eventLog{}
	c.hook = log.hook
	enqueued := barrier(c, HookSendEnqueued)
	sendDone := make(chan error, 1)
	go func() { sendDone <- c.Send(context.Background(), 1) }()
	<-enqueued
	c.Close()
	if err := <-sendDone; !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v, want ErrClosed", err)
	}
	evs := log.snapshot()
	if len(evs) != 2 || evs[0] != HookSendEnqueued || evs[1] != HookClosed {
		t.Fatalf("unexpected event sequence %v", evs)
	}
}
