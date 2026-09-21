package pubsub

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServeSuccess(t *testing.T) {
	b := NewBroker()
	sub, _ := b.Subscribe(SubscriberConfig{Buffer: 4})

	var handled int64
	done := make(chan struct{})
	go func() {
		stats := Serve[int](context.Background(), sub, WorkerConfig[int]{
			Handler:     func(_ context.Context, m int) error { atomic.AddInt64(&handled, 1); return nil },
			MaxAttempts: 1,
		})
		_ = stats
		close(done)
	}()

	for i := 0; i < 3; i++ {
		if err := b.Publish(context.Background(), i); err != nil {
			t.Fatal(err)
		}
	}
	b.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after Close drain")
	}
	if atomic.LoadInt64(&handled) != 3 {
		t.Fatalf("handled=%d, want 3", handled)
	}
}

// Failures are injected via the handler; time is injected via the fake
// clock. Retries happen only after the backoff elapses.
func TestServeRetriesWithInjectedClock(t *testing.T) {
	b := NewBroker()
	sub, _ := b.Subscribe(SubscriberConfig{Buffer: 4})
	clock := NewFakeClock(time.Unix(0, 0))

	var attempts int64
	var finals int64
	var events []FailEvent[int]
	var mu sync.Mutex

	done := make(chan WorkerStats, 1)
	go func() {
		done <- Serve[int](context.Background(), sub, WorkerConfig[int]{
			Handler: func(_ context.Context, _ int) error {
				atomic.AddInt64(&attempts, 1)
				return errors.New("boom")
			},
			MaxAttempts:    3,
			InitialBackoff: 10 * time.Millisecond,
			MaxBackoff:     100 * time.Millisecond,
			Clock:          clock,
			OnFailure: func(e FailEvent[int]) {
				mu.Lock()
				events = append(events, e)
				mu.Unlock()
				if e.Final {
					atomic.AddInt64(&finals, 1)
				}
			},
		})
	}()

	if err := b.Publish(context.Background(), 42); err != nil {
		t.Fatal(err)
	}

	// Attempt 1 happens immediately; backoff is now scheduled.
	waitFor(&attempts, 1, time.Second)
	if clock.Pending() != 1 {
		t.Fatalf("pending timers=%d, want 1 retry timer", clock.Pending())
	}
	clock.Advance(9 * time.Millisecond)
	waitFor(&attempts, 1, 100*time.Millisecond)
	clock.Advance(1 * time.Millisecond) // deadline reached
	waitFor(&attempts, 2, time.Second)

	clock.Advance(20 * time.Millisecond) // second backoff (doubled)
	waitFor(&attempts, 3, time.Second)

	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	stats := <-done
	if stats.Attempts != 3 {
		t.Fatalf("attempts=%d, want 3", stats.Attempts)
	}
	if stats.Failed != 1 {
		t.Fatalf("failed=%d, want 1 exhausted message", stats.Failed)
	}
	if atomic.LoadInt64(&finals) != 1 {
		t.Fatalf("final failure events=%d, want 1", finals)
	}
}

// A failure followed by a success on retry counts as one succeeded message.
func TestServeSucceedsAfterRetry(t *testing.T) {
	b := NewBroker()
	sub, _ := b.Subscribe(SubscriberConfig{Buffer: 2})
	clock := NewFakeClock(time.Unix(0, 0))

	var attempts int64
	done := make(chan WorkerStats, 1)
	go func() {
		done <- Serve[string](context.Background(), sub, WorkerConfig[string]{
			Handler: func(_ context.Context, _ string) error {
				n := atomic.AddInt64(&attempts, 1)
				if n == 1 {
					return errors.New("transient")
				}
				return nil
			},
			MaxAttempts:    2,
			InitialBackoff: time.Millisecond,
			Clock:          clock,
		})
	}()

	if err := b.Publish(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	waitFor(&attempts, 1, time.Second)
	clock.Advance(time.Millisecond)
	waitFor(&attempts, 2, time.Second)

	b.Close()
	stats := <-done
	if stats.Succeeded != 1 || stats.Failed != 0 {
		t.Fatalf("stats=%+v, want 1 succeeded / 0 failed", stats)
	}
}

// Cancelling the context while a retry is backed off stops Serve promptly
// without real sleeping.
func TestServeCancelDuringBackoff(t *testing.T) {
	b := NewBroker()
	sub, _ := b.Subscribe(SubscriberConfig{Buffer: 2})
	clock := NewFakeClock(time.Unix(0, 0))
	ctx, cancel := context.WithCancel(context.Background())

	var attempts int64
	done := make(chan WorkerStats, 1)
	go func() {
		done <- Serve[int](ctx, sub, WorkerConfig[int]{
			Handler: func(context.Context, int) error {
				atomic.AddInt64(&attempts, 1)
				return errors.New("nope")
			},
			MaxAttempts:    5,
			InitialBackoff: time.Hour,
			Clock:          clock,
		})
	}()

	if err := b.Publish(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	waitFor(&attempts, 1, time.Second)
	cancel()

	select {
	case stats := <-done:
		if stats.Attempts != 1 {
			t.Fatalf("attempts=%d, want 1 (no retry after cancel)", stats.Attempts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return when context was cancelled in backoff")
	}
}

// After Close, committed messages are still processed (drain semantics).
func TestServeDrainsOnClose(t *testing.T) {
	b := NewBroker()
	sub, _ := b.Subscribe(SubscriberConfig{Buffer: 8})
	for i := 0; i < 5; i++ {
		if err := b.Publish(context.Background(), i); err != nil {
			t.Fatal(err)
		}
	}
	var handled int64
	done := make(chan struct{})
	go func() {
		Serve[int](context.Background(), sub, WorkerConfig[int]{
			Handler: func(context.Context, int) error {
				atomic.AddInt64(&handled, 1)
				time.Sleep(time.Millisecond)
				return nil
			},
		})
		close(done)
	}()

	b.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not complete")
	}
	if atomic.LoadInt64(&handled) != 5 {
		t.Fatalf("handled=%d, want 5 drained committed messages", handled)
	}
}

func TestServeWrongTypeCountedAsFailure(t *testing.T) {
	b := NewBroker()
	sub, _ := b.Subscribe(SubscriberConfig{Buffer: 2})
	var got atomic.Int64
	done := make(chan struct{})
	go func() {
		Serve[int](context.Background(), sub, WorkerConfig[int]{
			Handler: func(context.Context, int) error { got.Add(1); return nil },
			OnFailure: func(e FailEvent[int]) {
				var w ErrWrongType
				if !errors.As(e.Err, &w) {
					t.Errorf("expected ErrWrongType, got %v", e.Err)
				}
			},
		})
		close(done)
	}()
	if err := b.Publish(context.Background(), "not-an-int"); err != nil {
		t.Fatal(err)
	}
	b.Close()
	<-done
	if got.Load() != 0 {
		t.Fatalf("handler ran for wrong-typed message %d times", got.Load())
	}
}

func waitFor(p *int64, want int64, within time.Duration) {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(p) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
