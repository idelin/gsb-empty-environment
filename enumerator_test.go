package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testBackends() []Backend {
	return []Backend{{Addr: "10.0.0.1:80"}, {Addr: "10.0.0.2:80"}, {Addr: "10.0.0.3:80"}}
}

func TestEnumeratorServesInOrderAndExhausts(t *testing.T) {
	en := NewEnumerator(testBackends())
	ctx := context.Background()
	for i, want := range testBackends() {
		got, err := en.Next(ctx)
		if err != nil || got.Addr != want.Addr {
			t.Fatalf("next %d = %+v, %v", i, got, err)
		}
	}
	if _, err := en.Next(ctx); !errors.Is(err, ErrExhausted) {
		t.Fatalf("want exhausted, got %v", err)
	}
	if en.Started() != 3 {
		t.Fatalf("started = %d, want 3", en.Started())
	}
}

func TestEnumeratorPauseResumeGate(t *testing.T) {
	en := NewEnumerator(testBackends())
	en.Pause()

	result := make(chan Backend, 1)
	go func() {
		b, err := en.Next(context.Background())
		if err == nil {
			result <- b
		}
	}()

	waitFor(t, func() bool { return en.Pending() == 1 }, "Next blocked at gate")
	select {
	case <-result:
		t.Fatal("Next returned while paused")
	case <-time.After(20 * time.Millisecond):
	}

	en.Resume()
	select {
	case b := <-result:
		if b.Addr != "10.0.0.1:80" {
			t.Fatalf("got %s", b.Addr)
		}
	case <-time.After(time.Second):
		t.Fatal("Next did not unblock after resume")
	}
	waitFor(t, func() bool { return en.Pending() == 0 }, "gate emptied")
}

func TestEnumeratorCancelWhilePausedConsumesNoCandidate(t *testing.T) {
	en := NewEnumerator(testBackends())
	en.Pause()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := en.Next(ctx)
		errCh <- err
	}()
	waitFor(t, func() bool { return en.Pending() == 1 }, "Next blocked at gate")

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled Next did not return")
	}
	if en.Canceled() != 1 {
		t.Fatalf("canceled = %d, want 1", en.Canceled())
	}
	if en.Started() != 0 {
		t.Fatalf("started = %d, want 0; canceled Next consumed a candidate", en.Started())
	}

	en.Resume()
	b, err := en.Next(context.Background())
	if err != nil || b.Addr != "10.0.0.1:80" {
		t.Fatalf("after cancel, next = %+v, %v", b, err)
	}
}

// Interleave paused enumeration with admission and reset across two streams,
// proving startup/cancelation ordering is fully controllable.
func TestEnumerationInterleavedWithReset(t *testing.T) {
	c := New(Config{MaxInFlight: 2, MaxActivePerConn: 2, MaxQueuedPerConn: 2})
	en := NewEnumerator(testBackends())
	en.Pause()
	c.OpenConn(1)

	t1, err := c.Admit(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := c.Admit(context.Background(), 1, 3)
	if err != nil {
		t.Fatal(err)
	}

	r1 := make(chan error, 1)
	r2 := make(chan error, 1)
	go func() {
		r1 <- c.RunBackend(context.Background(), t1, en, func(ctx context.Context, b Backend) error {
			<-ctx.Done() // work only ends when the reset cancels it
			return ctx.Err()
		})
	}()
	go func() {
		r2 <- c.RunBackend(context.Background(), t2, en, func(ctx context.Context, b Backend) error {
			return nil
		})
	}()

	waitFor(t, func() bool { return en.Pending() == 2 }, "both enumerations parked at gate")

	// Reset stream 1 before any backend has been selected.
	if err := c.MarkReset(t1); err != nil {
		t.Fatal(err)
	}
	if got := c.Stats().InFlight; got != 2 {
		t.Fatalf("inflight after reset = %d, want 2", got)
	}

	en.Resume()

	// Stream 1's work observes cancellation via the reset-driven context in the
	// integration wrapper; here RunBackend still reclaims the work token.
	select {
	case <-r1:
	case <-time.After(time.Second):
		t.Fatal("stream 1 backend did not finish")
	}
	select {
	case err := <-r2:
		if err != nil {
			t.Fatalf("stream 2 backend: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream 2 backend did not finish")
	}

	if en.Started() != 2 {
		t.Fatalf("started backends = %d, want 2", en.Started())
	}
	// Stream 1 reset already closed the stream side; work side was reclaimed by
	// RunBackend. Stream 2 still owns its token until the stream side ends.
	if err := c.StreamEnd(t2); err != nil {
		t.Fatalf("stream 2 end: %v", err)
	}
	c.WaitDrained()
	if got := c.Stats().InFlight; got != 0 {
		t.Fatalf("inflight = %d, want 0", got)
	}
}

// RunBackend must be a no-op against stale tickets so late goroutine
// completions cannot corrupt a recycled stream number.
func TestRunBackendStaleTicket(t *testing.T) {
	c := New(Config{MaxInFlight: 1, MaxActivePerConn: 1, MaxQueuedPerConn: 1})
	en := NewEnumerator(testBackends())
	c.OpenConn(1)

	tk, err := c.Admit(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Abandon(tk); err != nil {
		t.Fatal(err)
	}
	err = c.RunBackend(context.Background(), tk, en, func(context.Context, Backend) error {
		t.Fatal("work must not run for stale ticket")
		return nil
	})
	if !errors.Is(err, ErrStaleTicket) {
		t.Fatalf("want stale ticket, got %v", err)
	}
	if en.Started() != 0 {
		t.Fatalf("started = %d, want 0", en.Started())
	}
}

func TestEnumeratorResetRewinds(t *testing.T) {
	en := NewEnumerator(testBackends())
	ctx := context.Background()
	if _, err := en.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := en.Next(ctx); err != nil {
		t.Fatal(err)
	}
	en.Reset()
	b, err := en.Next(ctx)
	if err != nil || b.Addr != "10.0.0.1:80" {
		t.Fatalf("after reset = %+v, %v", b, err)
	}
}
