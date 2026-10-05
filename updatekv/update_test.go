package updatekv

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

// recordingDeliverer captures every delivered effect in order.
type recordingDeliverer struct {
	mu        sync.Mutex
	delivered []Effect
}

func (r *recordingDeliverer) Deliver(_ context.Context, e Effect) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delivered = append(r.delivered, e)
	return nil
}

func (r *recordingDeliverer) all() []Effect {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Effect, len(r.delivered))
	copy(out, r.delivered)
	return out
}

// flakyDeliverer fails the first n Deliver calls, then succeeds.
type flakyDeliverer struct {
	mu        sync.Mutex
	failures  int
	delivered []Effect
}

func (f *flakyDeliverer) Deliver(_ context.Context, e Effect) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failures > 0 {
		f.failures--
		return errors.New("delivery unavailable")
	}
	f.delivered = append(f.delivered, e)
	return nil
}

func noSleep(context.Context, time.Duration) error { return nil }

// bumpFunc parses the current value as an int, increments it, and emits
// an effect tagged with the attempt number so tests can tell which
// execution of the function produced a delivered effect.
func bumpFunc(calls *int) Func {
	return func(cur []byte) ([]byte, []Effect, error) {
		*calls++
		n := 0
		if len(cur) > 0 {
			var err error
			n, err = strconv.Atoi(string(cur))
			if err != nil {
				return nil, nil, err
			}
		}
		next := []byte(strconv.Itoa(n + 1))
		return next, []Effect{{Name: "bumped", Payload: []byte(fmt.Sprintf("attempt-%d", *calls))}}, nil
	}
}

func TestFailedAttemptsNeverDeliverEffects(t *testing.T) {
	inner := NewMemoryStore()
	inner.Put("k", []byte("0"))
	injector := NewConflictInjector(inner, 2) // first two writes conflict

	rec := &recordingDeliverer{}
	outbox := NewOutbox(rec)

	var calls int
	res, err := Run(context.Background(), injector, "k", bumpFunc(&calls), Options{
		MaxRetries: 5,
		Sleep:      noSleep,
		Outbox:     outbox,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Attempts != 3 || calls != 3 {
		t.Fatalf("attempts = %d, fn calls = %d; want 3 and 3", res.Attempts, calls)
	}

	// Exactly one effect delivered: the one from the winning attempt.
	got := rec.all()
	if len(got) != 1 {
		t.Fatalf("delivered %d effects, want exactly 1: %v", len(got), got)
	}
	if string(got[0].Payload) != "attempt-3" {
		t.Fatalf("delivered effect %q, want the winning attempt's %q", got[0].Payload, "attempt-3")
	}
	if p := outbox.Pending(); len(p) != 0 {
		t.Fatalf("outbox has %d pending records, want 0", len(p))
	}

	// The value was written exactly once, by the winning attempt.
	final, _ := inner.Read(context.Background(), "k")
	if string(final.Value) != "1" {
		t.Fatalf("final value = %q, want %q", final.Value, "1")
	}
}

func TestRetryBudgetExhaustedDeliversNothing(t *testing.T) {
	inner := NewMemoryStore()
	inner.Put("k", []byte("41"))
	injector := NewConflictInjector(inner, 10) // more conflicts than the budget

	rec := &recordingDeliverer{}
	outbox := NewOutbox(rec)

	var calls int
	res, err := Run(context.Background(), injector, "k", bumpFunc(&calls), Options{
		MaxRetries: 2,
		Sleep:      noSleep,
		Outbox:     outbox,
	})
	if !errors.Is(err, ErrRetryExhausted) {
		t.Fatalf("err = %v, want ErrRetryExhausted", err)
	}
	if res.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (1 + 2 retries)", res.Attempts)
	}
	if got := rec.all(); len(got) != 0 {
		t.Fatalf("delivered %d effects on failure, want 0", len(got))
	}
	if p := outbox.Pending(); len(p) != 0 {
		t.Fatalf("outbox has %d pending records, want 0", len(p))
	}
	final, _ := inner.Read(context.Background(), "k")
	if string(final.Value) != "41" {
		t.Fatalf("final value = %q, want unchanged %q", final.Value, "41")
	}
}

func TestDeliveryFailureKeepsRetryableRecord(t *testing.T) {
	inner := NewMemoryStore()
	inner.Put("k", []byte("0"))

	flaky := &flakyDeliverer{failures: 1} // first delivery attempt fails
	outbox := NewOutbox(flaky)

	var calls int
	res, err := Run(context.Background(), inner, "k", bumpFunc(&calls), Options{
		Sleep:  noSleep,
		Outbox: outbox,
	})
	if res.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", res.Attempts)
	}
	var derr *DeliveryError
	if !errors.As(err, &derr) {
		t.Fatalf("err = %v, want *DeliveryError", err)
	}

	// The failed delivery is retained as a retryable record.
	pending := outbox.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if pending[0].Attempts != 1 || pending[0].LastErr == "" {
		t.Fatalf("pending record = %+v, want 1 attempt with recorded error", pending[0])
	}

	// Retrying the outbox delivers the effect exactly once.
	if err := outbox.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if p := outbox.Pending(); len(p) != 0 {
		t.Fatalf("pending = %d after retry, want 0", len(p))
	}
	if len(flaky.delivered) != 1 || string(flaky.delivered[0].Payload) != "attempt-1" {
		t.Fatalf("delivered = %v, want exactly the committed effect", flaky.delivered)
	}
}

func TestNonConflictErrorAbortsImmediately(t *testing.T) {
	boom := errors.New("storage on fire")
	s := &errStore{inner: NewMemoryStore(), err: boom}

	var calls int
	res, err := Run(context.Background(), s, "k", bumpFunc(&calls), Options{
		MaxRetries: 5,
		Sleep:      noSleep,
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if res.Attempts != 1 || calls != 1 {
		t.Fatalf("attempts = %d, calls = %d; want 1 and 1 (no retries)", res.Attempts, calls)
	}
}

type errStore struct {
	inner *MemoryStore
	err   error
}

func (s *errStore) Read(ctx context.Context, key string) (Record, error) {
	return s.inner.Read(ctx, key)
}

func (s *errStore) CompareAndSwap(context.Context, string, []byte, uint64) (uint64, error) {
	return 0, s.err
}

// rendezvousStore blocks the first CompareAndSwap until two reads have
// happened, forcing a real lost-update race between two concurrent
// updates.
type rendezvousStore struct {
	inner    *MemoryStore
	mu       sync.Mutex
	reads    int
	bothRead chan struct{}
	once     sync.Once
}

func newRendezvousStore(inner *MemoryStore) *rendezvousStore {
	return &rendezvousStore{inner: inner, bothRead: make(chan struct{})}
}

func (r *rendezvousStore) Read(ctx context.Context, key string) (Record, error) {
	rec, err := r.inner.Read(ctx, key)
	r.mu.Lock()
	r.reads++
	if r.reads == 2 {
		r.once.Do(func() { close(r.bothRead) })
	}
	r.mu.Unlock()
	return rec, err
}

func (r *rendezvousStore) CompareAndSwap(ctx context.Context, key string, value []byte, expectedVersion uint64) (uint64, error) {
	select {
	case <-r.bothRead:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return r.inner.CompareAndSwap(ctx, key, value, expectedVersion)
}

func TestConcurrentUpdatesLoseNeitherWriteNorEffects(t *testing.T) {
	inner := NewMemoryStore()
	inner.Put("k", []byte("0"))
	store := newRendezvousStore(inner)

	rec := &recordingDeliverer{}
	outbox := NewOutbox(rec)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var calls int
			_, errs[idx] = Run(context.Background(), store, "k", bumpFunc(&calls), Options{
				MaxRetries: 5,
				Sleep:      noSleep,
				Outbox:     outbox,
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}

	final, _ := inner.Read(context.Background(), "k")
	if string(final.Value) != "2" {
		t.Fatalf("final value = %q, want %q (both increments applied)", final.Value, "2")
	}
	if got := rec.all(); len(got) != 2 {
		t.Fatalf("delivered %d effects, want 2 (one per committed update)", len(got))
	}
	if p := outbox.Pending(); len(p) != 0 {
		t.Fatalf("outbox has %d pending records, want 0", len(p))
	}
}
