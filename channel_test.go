package corochan

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the timeout elapses.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

// Acceptance scenario: producer P writes 3 values into a capacity-2 channel;
// P parks on the 3rd send; consumer C reads one value and P wakes immediately
// to finish; Close then broadcasts EOF to every waiting reader.
func TestAcceptance_BufferedBlockWakeClose(t *testing.T) {
	ch := NewChannel[int](2)

	var thirdSendDone atomic.Bool
	pDone := make(chan struct{})
	go func() { // producer P
		ch.Send(1)
		ch.Send(2)
		ch.Send(3) // buffer full => P must park here
		thirdSendDone.Store(true)
		close(pDone)
	}()

	// Give P time to run; it must be parked on the 3rd send.
	time.Sleep(100 * time.Millisecond)
	if thirdSendDone.Load() {
		t.Fatal("P completed the 3rd send while the buffer was full")
	}
	if n := ch.Len(); n != 2 {
		t.Fatalf("buffer length = %d, want 2", n)
	}

	// Consumer C reads one value => P must be woken immediately.
	if v, ok := ch.Recv(); !ok || v != 1 {
		t.Fatalf("Recv = (%v, %v), want (1, true)", v, ok)
	}
	select {
	case <-pDone:
	case <-time.After(2 * time.Second):
		t.Fatal("P was not woken after the consumer read")
	}
	if !thirdSendDone.Load() {
		t.Fatal("P did not finish its 3rd send after wakeup")
	}

	// Drain remaining values; FIFO order must be preserved.
	for _, want := range []int{2, 3} {
		if v, ok := ch.Recv(); !ok || v != want {
			t.Fatalf("Recv = (%v, %v), want (%d, true)", v, ok, want)
		}
	}

	// Park several readers on the now-empty channel, then Close: every one
	// of them must receive EOF.
	const readers = 3
	eofs := make(chan bool, readers)
	ready := make(chan struct{}, readers)
	for i := 0; i < readers; i++ {
		go func() {
			ready <- struct{}{}
			_, ok := ch.Recv()
			eofs <- ok
		}()
	}
	for i := 0; i < readers; i++ {
		<-ready
	}
	time.Sleep(50 * time.Millisecond) // let all readers park
	ch.Close()
	for i := 0; i < readers; i++ {
		select {
		case ok := <-eofs:
			if ok {
				t.Fatal("reader did not observe EOF after Close")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("reader was not woken by Close")
		}
	}

	// Recv on a closed, drained channel keeps returning EOF.
	if _, ok := ch.Recv(); ok {
		t.Fatal("Recv on closed drained channel should return EOF")
	}
}

func TestUnbufferedRendezvous(t *testing.T) {
	ch := NewChannel[int](0)

	var received atomic.Int64
	go func() {
		v, ok := ch.Recv()
		if !ok {
			t.Error("unexpected EOF")
			return
		}
		received.Store(int64(v))
	}()

	// The send must block until the receiver arrives; after it returns, the
	// value must already have been handed over.
	time.Sleep(50 * time.Millisecond)
	ch.Send(42)
	waitFor(t, "receiver to get 42", func() bool { return received.Load() == 42 })

	// A receiver also parks until a sender arrives (reverse direction).
	var sent atomic.Bool
	go func() {
		ch.Send(7)
		sent.Store(true)
	}()
	time.Sleep(50 * time.Millisecond)
	if sent.Load() {
		t.Fatal("unbuffered Send returned without a receiver")
	}
	if v, ok := ch.Recv(); !ok || v != 7 {
		t.Fatalf("Recv = (%v, %v), want (7, true)", v, ok)
	}
	waitFor(t, "sender to complete", sent.Load)
}

func TestPreciseWakeup(t *testing.T) {
	ch := NewChannel[int](0)
	const receivers = 5
	var woken atomic.Int64
	release := make(chan struct{})
	for i := 0; i < receivers; i++ {
		go func() {
			<-release
			if _, ok := ch.Recv(); ok {
				woken.Add(1)
			}
		}()
	}
	close(release)
	time.Sleep(100 * time.Millisecond) // all receivers parked

	ch.Send(1) // must wake exactly one receiver
	time.Sleep(100 * time.Millisecond)
	if n := woken.Load(); n != 1 {
		t.Fatalf("one Send woke %d receivers, want exactly 1", n)
	}
	ch.Close() // broadcast EOF to the rest
	waitFor(t, "remaining receivers to observe EOF", func() bool {
		return ch.Closed()
	})
}

func TestSendOnClosedChannelPanics(t *testing.T) {
	// Send after Close panics.
	ch := NewChannel[int](1)
	ch.Close()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Send on closed channel did not panic")
			}
		}()
		ch.Send(1)
	}()

	// A sender parked on a full channel panics when the channel is closed.
	ch2 := NewChannel[int](1)
	ch2.Send(1) // fills the buffer
	panicked := make(chan bool, 1)
	go func() {
		defer func() { panicked <- recover() != nil }()
		ch2.Send(2) // parks, then Close wakes it into a panic
	}()
	time.Sleep(50 * time.Millisecond)
	ch2.Close()
	if !<-panicked {
		t.Error("parked sender did not panic on Close")
	}
}

func TestDoubleClosePanics(t *testing.T) {
	ch := NewChannel[int](1)
	ch.Close()
	defer func() {
		if recover() == nil {
			t.Error("double Close did not panic")
		}
	}()
	ch.Close()
}

func TestFIFOOrder(t *testing.T) {
	ch := NewChannel[int](4)
	for i := 0; i < 4; i++ {
		ch.Send(i)
	}
	for i := 0; i < 4; i++ {
		if v, ok := ch.Recv(); !ok || v != i {
			t.Fatalf("Recv = (%v, %v), want (%d, true)", v, ok, i)
		}
	}
}

func TestTrySendTryRecv(t *testing.T) {
	ch := NewChannel[int](1)
	if !ch.TrySend(1) {
		t.Fatal("TrySend on empty buffered channel failed")
	}
	if ch.TrySend(2) {
		t.Fatal("TrySend on full channel succeeded")
	}
	if v, got, open := ch.TryRecv(); !got || !open || v != 1 {
		t.Fatalf("TryRecv = (%v, %v, %v), want (1, true, true)", v, got, open)
	}
	if _, got, open := ch.TryRecv(); got || !open {
		t.Fatal("TryRecv on empty open channel should report (false, true)")
	}
	ch.Close()
	if _, got, open := ch.TryRecv(); got || open {
		t.Fatal("TryRecv on closed drained channel should report (false, false)")
	}
}

// Stress: many coroutines exchanging values through a small buffer; totals
// must match exactly. Run with -race to validate the synchronization.
func TestChannelStress(t *testing.T) {
	const producers, consumers, perProducer = 8, 8, 2000
	ch := NewChannel[int](16)

	var sentSum, recvSum atomic.Int64
	var prodWg, consWg sync.WaitGroup
	for p := 0; p < producers; p++ {
		prodWg.Add(1)
		go func(base int) {
			defer prodWg.Done()
			for i := 1; i <= perProducer; i++ {
				ch.Send(base + i)
				sentSum.Add(int64(base + i))
			}
		}(p * perProducer)
	}
	for c := 0; c < consumers; c++ {
		consWg.Add(1)
		go func() {
			defer consWg.Done()
			for {
				v, ok := ch.Recv()
				if !ok {
					return
				}
				recvSum.Add(int64(v))
			}
		}()
	}
	// Close only after every producer is done; consumers drain the buffer
	// and exit on EOF.
	prodWg.Wait()
	ch.Close()
	consWg.Wait()
	if sentSum.Load() != recvSum.Load() {
		t.Fatalf("sum mismatch: sent %d, received %d", sentSum.Load(), recvSum.Load())
	}
}
