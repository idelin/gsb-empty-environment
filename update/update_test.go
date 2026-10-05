package update

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
)

// recordingDeliverer logs every delivered effect in order.
type recordingDeliverer struct {
	mu        sync.Mutex
	delivered []Effect
	err       error
}

func (r *recordingDeliverer) Deliver(_ context.Context, e Effect) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.delivered = append(r.delivered, e)
	return nil
}

func (r *recordingDeliverer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.delivered)
}

func incrementUpdate(current []byte) ([]byte, []Effect, error) {
	n, _ := strconv.Atoi(string(current))
	next := strconv.Itoa(n + 1)
	return []byte(next), []Effect{{Topic: "counter", Payload: []byte(next)}}, nil
}

func TestUpdateDeliversEffectsOnceAfterCommit(t *testing.T) {
	ctx := context.Background()
	d := &recordingDeliverer{}
	ob := NewOutbox(d)
	inj := &ConflictInjector{Inner: NewMemoryStore(), Failures: 3}

	calls := 0
	err := Update(ctx, inj, ob, "k", func(cur []byte) ([]byte, []Effect, error) {
		calls++
		// No delivery may have happened while attempts are still failing.
		if got := d.count(); got != 0 {
			t.Errorf("attempt %d: effect delivered before commit (%d deliveries)", calls, got)
		}
		return incrementUpdate(cur)
	}, Options{MaxRetries: 5})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if calls != 4 {
		t.Fatalf("fn executed %d times, want 4", calls)
	}
	if got := d.count(); got != 1 {
		t.Fatalf("delivered %d effects, want exactly 1", got)
	}
	if string(d.delivered[0].Payload) != "1" {
		t.Fatalf("delivered payload %q, want %q", d.delivered[0].Payload, "1")
	}
	v, _, _ := inj.Inner.Read(ctx, "k")
	if string(v) != "1" {
		t.Fatalf("stored value %q, want %q", v, "1")
	}
	if p := ob.Pending(); len(p) != 0 {
		t.Fatalf("%d pending records, want 0", len(p))
	}
}

func TestUpdateBoundedRetriesExhausted(t *testing.T) {
	ctx := context.Background()
	d := &recordingDeliverer{}
	ob := NewOutbox(d)
	inj := &ConflictInjector{Inner: NewMemoryStore(), Failures: 100}

	err := Update(ctx, inj, ob, "k", incrementUpdate, Options{MaxRetries: 3})
	if !errors.Is(err, ErrTooManyConflicts) {
		t.Fatalf("err = %v, want ErrTooManyConflicts", err)
	}
	if inj.Attempts != 4 {
		t.Fatalf("%d CAS attempts, want 4 (1 + 3 retries)", inj.Attempts)
	}
	if got := d.count(); got != 0 {
		t.Fatalf("delivered %d effects from failed update, want 0", got)
	}
	if p := ob.Pending(); len(p) != 0 {
		t.Fatalf("%d pending records, want 0", len(p))
	}
}

func TestDeliveryFailureKeepsRetryableRecord(t *testing.T) {
	ctx := context.Background()
	d := &recordingDeliverer{err: errors.New("broker down")}
	ob := NewOutbox(d)

	if err := Update(ctx, NewMemoryStore(), ob, "k", incrementUpdate, Options{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := d.count(); got != 0 {
		t.Fatalf("delivered %d effects, want 0 while failing", got)
	}
	pending := ob.Pending()
	if len(pending) != 1 {
		t.Fatalf("%d pending records, want 1", len(pending))
	}
	if pending[0].Attempts != 1 || pending[0].LastErr == nil {
		t.Fatalf("record = %+v, want 1 attempt with error", pending[0])
	}

	// Recover and retry: the record is delivered exactly once.
	d.err = nil
	ob.Flush(ctx)
	if got := d.count(); got != 1 {
		t.Fatalf("delivered %d effects after flush, want 1", got)
	}
	if p := ob.Pending(); len(p) != 0 {
		t.Fatalf("%d pending records after flush, want 0", len(p))
	}
	// A further flush must not redeliver.
	ob.Flush(ctx)
	if got := d.count(); got != 1 {
		t.Fatalf("delivered %d effects after second flush, want 1", got)
	}
}

func TestConcurrentUpdatesSerializeEffects(t *testing.T) {
	ctx := context.Background()
	d := &recordingDeliverer{}
	ob := NewOutbox(d)
	s := NewMemoryStore()

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Update(ctx, s, ob, "k", incrementUpdate, Options{MaxRetries: 50}); err != nil {
				t.Errorf("Update: %v", err)
			}
		}()
	}
	wg.Wait()

	v, _, _ := s.Read(ctx, "k")
	if string(v) != strconv.Itoa(workers) {
		t.Fatalf("stored value %q, want %d", v, workers)
	}
	if got := d.count(); got != workers {
		t.Fatalf("delivered %d effects, want %d (one per committed write)", got, workers)
	}
}
