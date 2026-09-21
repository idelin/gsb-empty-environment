package pubsub

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func drainCh(ch <-chan any) []any {
	var out []any
	for {
		select {
		case v := <-ch:
			out = append(out, v)
		default:
			return out
		}
	}
}

func TestFanoutBasic(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	s1, err := b.Subscribe(SubscriberConfig{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := b.Subscribe(SubscriberConfig{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range []any{"a", "b", "c"} {
		if err := b.Publish(context.Background(), m); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	want := []any{"a", "b", "c"}
	if got := drainCh(s1.C()); !equalSlice(got, want) {
		t.Fatalf("s1 = %v, want %v", got, want)
	}
	if got := drainCh(s2.C()); !equalSlice(got, want) {
		t.Fatalf("s2 = %v, want %v", got, want)
	}
}

func equalSlice(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A slow subscriber under DropNewest must only drop its OWN messages; other
// subscribers continue to receive everything.
func TestSlowSubscriberDoesNotAffectOthers_Drop(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	var drops int64
	slow, err := b.Subscribe(SubscriberConfig{
		Buffer:   1,
		Overflow: OverflowDropNewest,
		OnDrop:   func(any) { atomic.AddInt64(&drops, 1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	fast, err := b.Subscribe(SubscriberConfig{Buffer: 16, Overflow: OverflowDropNewest})
	if err != nil {
		t.Fatal(err)
	}

	const n = 8
	for i := 0; i < n; i++ {
		if err := b.Publish(context.Background(), i); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	got := drainCh(fast.C())
	if len(got) != n {
		t.Fatalf("fast subscriber got %d messages, want all %d: %v", len(got), n, got)
	}
	stats := b.Stats()
	if stats.Dropped == 0 {
		t.Fatal("expected explicit drops for the slow subscriber")
	}
	if atomic.LoadInt64(&drops) != stats.Dropped {
		t.Fatalf("OnDrop calls=%d, stats.Dropped=%d", drops, stats.Dropped)
	}
	if stats.Delivered+stats.Dropped != int64(n)*2 {
		t.Fatalf("delivered(%d)+dropped(%d) != %d", stats.Delivered, stats.Dropped, n*2)
	}
	if len(drainCh(slow.C())) > 1 {
		t.Fatal("slow queue must respect its bounded capacity")
	}
}

// Under OverflowBlock a slow subscriber delays that Publish only; the fast
// subscriber still receives every message independently.
func TestSlowSubscriberDoesNotAffectOthers_Block(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	slow, err := b.Subscribe(SubscriberConfig{Buffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	fast, err := b.Subscribe(SubscriberConfig{Buffer: 16})
	if err != nil {
		t.Fatal(err)
	}

	released := make(chan struct{})
	consumed := make(chan struct{})
	var fastCount int64
	var releaseOnce sync.Once

	// Independent fast reader: signals release as soon as it has both.
	go func() {
		for range fast.C() {
			if atomic.AddInt64(&fastCount, 1) == 2 {
				releaseOnce.Do(func() { close(released) })
			}
		}
	}()

	// Slow reader stays parked until fast already got everything.
	go func() {
		<-released
		<-slow.C() // 0
		<-slow.C() // 1
		close(consumed)
	}()

	if err := b.Publish(context.Background(), 0); err != nil { // fills slow
		t.Fatal(err)
	}

	pubDone := make(chan error, 1)
	go func() { pubDone <- b.Publish(context.Background(), 1) }()

	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("fast subscriber did not independently receive both messages")
	}
	select {
	case <-consumed:
	case <-time.After(2 * time.Second):
		t.Fatal("slow consumer never drained")
	}
	select {
	case err := <-pubDone:
		if err != nil {
			t.Fatalf("publish after drain: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Publish did not complete after slow consumer drained")
	}
	if atomic.LoadInt64(&fastCount) != 2 {
		t.Fatalf("fastCount=%d, want 2", fastCount)
	}
}

func TestUnsubscribeConcurrent(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	const subs, rounds = 12, 200
	var wg sync.WaitGroup

	for i := 0; i < subs; i++ {
		s, err := b.Subscribe(SubscriberConfig{Buffer: 2, Overflow: OverflowDropNewest})
		if err != nil {
			t.Fatal(err)
		}
		// Slow reader draining occasionally.
		stop := make(chan struct{})
		wg.Add(1)
		go func(s *Subscription) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				case <-s.C():
				}
			}
		}(s)

		wg.Add(1)
		go func(s *Subscription) {
			defer wg.Done()
			defer close(stop)
			for r := 0; r < rounds; r++ {
				b.Publish(context.Background(), r)
			}
			s.Unsubscribe()
		}(s)
	}
	wg.Wait()
}

func TestDoubleUnsubscribeAndCloseNoPanic(t *testing.T) {
	b := NewBroker()
	s, err := b.Subscribe(SubscriberConfig{Buffer: 2})
	if err != nil {
		t.Fatal(err)
	}
	s.Unsubscribe()
	s.Unsubscribe() // must not panic
	<-s.Done()

	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil { // idempotent
		t.Fatalf("second close: %v", err)
	}
	// Unsubscribing after broker close must also be safe.
	s.Unsubscribe()

	select {
	case <-s.Done():
	default:
		t.Fatal("Done must stay closed")
	}
}

func TestOperationsAfterClose(t *testing.T) {
	b := NewBroker()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Subscribe(SubscriberConfig{Buffer: 1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Subscribe after close = %v, want ErrClosed", err)
	}
	if err := b.Publish(context.Background(), "x"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Publish after close = %v, want ErrClosed", err)
	}
}

// Close must preserve messages already committed to queues.
func TestCloseDrainsCommitted(t *testing.T) {
	b := NewBroker()
	s, err := b.Subscribe(SubscriberConfig{Buffer: 8})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := b.Publish(context.Background(), i); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	<-s.Done()
	got := s.Drain()
	if len(got) != 5 {
		t.Fatalf("drained %d committed messages, want 5: %v", len(got), got)
	}
}

// Close must unblock (abort) a Publish waiting on a full queue instead of
// hanging forever, and must report ErrClosed.
func TestCloseAbortsBlockedPublish(t *testing.T) {
	b := NewBroker()
	if _, err := b.Subscribe(SubscriberConfig{Buffer: 1}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(context.Background(), "full"); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Publish(context.Background(), "blocked")
	}()

	// Give the blocked publish a moment to park on the full queue.
	time.Sleep(20 * time.Millisecond)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("blocked publish returned %v, want ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock the waiting Publish")
	}
}

func TestContextCancelsBlockedPublish(t *testing.T) {
	b := NewBroker()
	defer b.Close()
	if _, err := b.Subscribe(SubscriberConfig{Buffer: 1}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(context.Background(), 1); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- b.Publish(ctx, 2) }()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not unblock Publish")
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := NewBroker()
	defer b.Close()
	s, err := b.Subscribe(SubscriberConfig{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(context.Background(), "kept"); err != nil {
		t.Fatal(err)
	}
	s.Unsubscribe()
	<-s.Done()
	// Publishing after removal must not reach the subscription.
	if err := b.Publish(context.Background(), "ignored"); err != nil {
		t.Fatal(err)
	}
	got := s.Drain()
	if len(got) != 1 || got[0] != "kept" {
		t.Fatalf("drained %v, want [kept]", got)
	}
}

func TestInvalidBuffer(t *testing.T) {
	b := NewBroker()
	defer b.Close()
	if _, err := b.Subscribe(SubscriberConfig{Buffer: 0}); !errors.Is(err, ErrInvalidBuffer) {
		t.Fatalf("got %v, want ErrInvalidBuffer", err)
	}
}

// High-concurrency stress: churning subscribers, publishes, unsubscribes and
// a close at the end. Race detector is the main assertion here.
func TestStressConcurrentLifecycle(t *testing.T) {
	b := NewBroker()

	const publishers, churn, perPub = 6, 40, 25
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < churn; i++ {
		s, err := b.Subscribe(SubscriberConfig{
			Buffer:   4,
			Overflow: OverflowDropNewest,
			OnDrop:   func(any) {},
		})
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(s *Subscription) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					s.Unsubscribe()
					return
				case <-s.C():
				}
			}
		}(s)
		wg.Add(1)
		go func(s *Subscription) {
			defer wg.Done()
			time.Sleep(time.Duration(i) * 100 * time.Microsecond)
			s.Unsubscribe()
		}(s)
	}

	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perPub; j++ {
				_ = b.Publish(context.Background(), j)
			}
		}()
	}

	close(stop)
	wg.Wait()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}
