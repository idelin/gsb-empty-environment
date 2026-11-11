package netsec

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testController() *Controller {
	return New(Config{MaxInFlight: 4, MaxActivePerConn: 2, MaxQueuedPerConn: 3})
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func TestAdmitLifecycleAndStates(t *testing.T) {
	c := testController()
	c.OpenConn(1)

	tk, err := c.Admit(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if st, ok := c.State(tk); !ok || st != StateActive {
		t.Fatalf("want active, got %s ok=%v", st, ok)
	}
	if got := c.Stats().InFlight; got != 1 {
		t.Fatalf("inflight = %d, want 1", got)
	}

	if err := c.MarkBackendStarting(tk); err != nil {
		t.Fatalf("starting: %v", err)
	}
	if st, _ := c.State(tk); st != StateStarting {
		t.Fatalf("want starting, got %s", st)
	}
	if err := c.MarkBackendWorking(tk); err != nil {
		t.Fatalf("working: %v", err)
	}
	if st, _ := c.State(tk); st != StateWorking {
		t.Fatalf("want working, got %s", st)
	}

	// Illegal jumps are rejected.
	if err := c.MarkBackendStarting(tk); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("want invalid transition, got %v", err)
	}

	if err := c.StreamEnd(tk); err != nil {
		t.Fatalf("stream end: %v", err)
	}
	// Stream finished but backend work still owns the token.
	if got := c.Stats().InFlight; got != 1 {
		t.Fatalf("inflight after stream end = %d, want 1", got)
	}
	if err := c.WorkFinished(tk); err != nil {
		t.Fatalf("work finished: %v", err)
	}
	if got := c.Stats().InFlight; got != 0 {
		t.Fatalf("inflight after work finish = %d, want 0", got)
	}
	if _, ok := c.State(tk); ok {
		t.Fatal("released ticket should be stale")
	}
}

func TestResetDoesNotReturnTokenEarly(t *testing.T) {
	c := New(Config{MaxInFlight: 1, MaxActivePerConn: 1, MaxQueuedPerConn: 2})
	c.OpenConn(1)

	tk, err := c.Admit(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := c.MarkBackendStarting(tk); err != nil {
		t.Fatal(err)
	}

	// A second stream must queue because the only token is held.
	gotTicket := make(chan Ticket, 1)
	go func() {
		t2, err := c.Admit(context.Background(), 1, 2)
		if err == nil {
			gotTicket <- t2
		}
	}()
	waitFor(t, func() bool { return c.Stats().Queued == 1 }, "second stream queued")

	// Reset the first stream: stream side closes, but backend work is still
	// starting, so the token must remain held and the queued stream must wait.
	if err := c.MarkReset(tk); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if st, _ := c.State(tk); st != StateReset {
		t.Fatalf("want reset state, got %s", st)
	}
	if got := c.Stats().InFlight; got != 1 {
		t.Fatalf("inflight after reset = %d, want 1 (token still held)", got)
	}
	if c.Stats().Resets != 1 {
		t.Fatalf("resets = %d, want 1", c.Stats().Resets)
	}
	select {
	case <-gotTicket:
		t.Fatal("queued stream must not be admitted while reset work drains")
	case <-time.After(30 * time.Millisecond):
	}

	// Reset state still permits the backend startup to complete.
	if err := c.MarkBackendWorking(tk); err != nil {
		t.Fatalf("working after reset: %v", err)
	}
	if err := c.WorkFinished(tk); err != nil {
		t.Fatalf("work finished: %v", err)
	}

	select {
	case t2 := <-gotTicket:
		if err := c.Abandon(t2); err != nil {
			t.Fatalf("abandon: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued stream was not admitted after token reclaim")
	}
	if got := c.Stats().InFlight; got != 0 {
		t.Fatalf("inflight after reclaim = %d, want 0", got)
	}

	// All lifecycle callbacks on the fully released ticket are now stale.
	if err := c.MarkReset(tk); !errors.Is(err, ErrStaleTicket) {
		t.Fatalf("want stale ticket, got %v", err)
	}
	if err := c.WorkFinished(tk); !errors.Is(err, ErrStaleTicket) {
		t.Fatalf("want stale ticket, got %v", err)
	}
}

func TestRapidResetKeepsInFlightBounded(t *testing.T) {
	const limit = 8
	c := New(Config{MaxInFlight: limit, MaxActivePerConn: limit, MaxQueuedPerConn: 200})
	c.OpenConn(7)

	var maxObserved int
	var mu sync.Mutex
	var wg sync.WaitGroup
	var admitted int64

	// Attacker: open, reset immediately, but backend work takes a while and
	// only finishes later.
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			tk, err := c.Admit(context.Background(), 7, StreamID(2*n+1))
			if err != nil {
				return
			}
			atomic.AddInt64(&admitted, 1)
			mu.Lock()
			if c.Stats().InFlight > maxObserved {
				maxObserved = c.Stats().InFlight
			}
			mu.Unlock()
			_ = c.MarkReset(tk)
			// Simulate orphaned backend work draining slowly.
			time.Sleep(20 * time.Millisecond)
			_ = c.WorkFinished(tk)
		}(i)
	}
	wg.Wait()

	if maxObserved > limit {
		t.Fatalf("inflight peaked at %d, limit is %d", maxObserved, limit)
	}
	if got := c.Stats().InFlight; got != 0 {
		t.Fatalf("inflight after drain = %d, want 0", got)
	}
	if c.Stats().Resets != uint64(admitted) {
		t.Fatalf("resets = %d, admitted = %d", c.Stats().Resets, admitted)
	}
}

func TestQueueFullAndClosedConnection(t *testing.T) {
	c := New(Config{MaxInFlight: 1, MaxActivePerConn: 1, MaxQueuedPerConn: 1})
	c.OpenConn(1)

	first, err := c.Admit(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 2)
	go func() {
		_, err := c.Admit(context.Background(), 1, 2)
		errCh <- err
	}()
	waitFor(t, func() bool { return c.Stats().Queued == 1 }, "queue fills")

	// Third stream exceeds the per-connection queue cap immediately.
	if _, err := c.Admit(context.Background(), 1, 3); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("want queue full, got %v", err)
	}

	// Closing the connection fails the queued stream.
	c.CloseConn(1)
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrConnectionClosed) {
			t.Fatalf("want connection closed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued stream not failed on close")
	}

	// The admitted ticket keeps draining; its late callbacks still work until
	// work is reclaimed, then become stale. Connection bookkeeping is gone.
	if err := c.MarkReset(first); err != nil {
		t.Fatalf("reset after close: %v", err)
	}
	// The closed connection is removed immediately; its detached work keeps
	// draining against a dead generation but no new streams can be admitted.
	if c.Stats().Connections != 0 {
		t.Fatalf("connections = %d, want 0 after close", c.Stats().Connections)
	}
	if err := c.WorkFinished(first); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if c.Stats().Connections != 0 {
		t.Fatalf("connections = %d, want 0 after reclaim", c.Stats().Connections)
	}

	// New Admit on the closed connection fails.
	if _, err := c.Admit(context.Background(), 1, 9); !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("want connection closed, got %v", err)
	}
}

func TestAdmitContextCancelWhileQueued(t *testing.T) {
	c := New(Config{MaxInFlight: 1, MaxActivePerConn: 1, MaxQueuedPerConn: 5})
	c.OpenConn(1)

	holder, err := c.Admit(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Admit(ctx, 1, 2)
		errCh <- err
	}()
	waitFor(t, func() bool { return c.Stats().Queued == 1 }, "stream queued")
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued Admit did not react to cancellation")
	}
	waitFor(t, func() bool { return c.Stats().Queued == 0 }, "queue drained after cancel")

	// Releasing the holder keeps inflight accounting correct.
	if err := c.Abandon(holder); err != nil {
		t.Fatal(err)
	}
	if c.Stats().InFlight != 0 {
		t.Fatalf("inflight = %d, want 0", c.Stats().InFlight)
	}
}

func TestShutdownFailsQueuedAndKeepsDraining(t *testing.T) {
	c := New(Config{MaxInFlight: 1, MaxActivePerConn: 1, MaxQueuedPerConn: 5})
	c.OpenConn(1)

	holder, err := c.Admit(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Admit(context.Background(), 1, 2)
		errCh <- err
	}()
	waitFor(t, func() bool { return c.Stats().Queued == 1 }, "stream queued")

	c.Shutdown()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrControllerClosed) {
			t.Fatalf("want controller closed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued stream not failed on shutdown")
	}

	// New admits are rejected; the holder still drains and keeps its token.
	if _, err := c.Admit(context.Background(), 1, 3); !errors.Is(err, ErrControllerClosed) {
		t.Fatalf("want controller closed, got %v", err)
	}
	if got := c.Stats().InFlight; got != 1 {
		t.Fatalf("inflight = %d, want 1 while draining", got)
	}

	drained := make(chan struct{})
	go func() {
		c.WaitDrained()
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("WaitDrained returned before work reclaimed")
	case <-time.After(20 * time.Millisecond):
	}
	if err := c.Abandon(holder); err != nil {
		t.Fatal(err)
	}
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("WaitDrained did not return after reclaim")
	}
}
