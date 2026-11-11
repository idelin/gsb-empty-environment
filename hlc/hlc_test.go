package hlc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const testNodeID uint64 = 7

func newTestClock(t *testing.T, fc *FakeClock, store Store, hook Hook) *Clock {
	t.Helper()
	clk, err := New(Config{
		NodeID:   testNodeID,
		Physical: fc,
		Store:    store,
		Hook:     hook,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return clk
}

func TestTimestampCompareAndOrder(t *testing.T) {
	cases := []struct {
		a, b Timestamp
		want int
	}{
		{Timestamp{1, 0, 1}, Timestamp{1, 0, 2}, -1},
		{Timestamp{1, 2, 1}, Timestamp{1, 3, 1}, -1},
		{Timestamp{1, 9, 1}, Timestamp{2, 0, 1}, -1},
		{Timestamp{1, 3, 9}, Timestamp{1, 3, 9}, 0},
		{Timestamp{-5, 0, 0}, Timestamp{0, 0, 0}, -1},
		{Timestamp{2, 0, 0}, Timestamp{1, 65535, 99}, 1},
	}
	for _, tc := range cases {
		if got := tc.a.Compare(tc.b); got != tc.want {
			t.Errorf("%s.Compare(%s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if tc.want == 0 && (tc.a.Before(tc.b) || tc.a.After(tc.b)) {
			t.Errorf("equal timestamps reported ordered: %s %s", tc.a, tc.b)
		}
	}
	zero := Timestamp{}
	if !zero.IsZero() {
		t.Error("zero value not detected")
	}
}

func TestTimestampEncodingRoundTrip(t *testing.T) {
	values := []Timestamp{
		{},
		{Physical: -62135596800000, Logical: 0, NodeID: 0},
		{Physical: 1731300000123, Logical: 65535, NodeID: 1<<64 - 1},
		{Physical: 1<<63 - 1, Logical: 1, NodeID: 42},
	}
	for _, ts := range values {
		data, err := ts.MarshalBinary()
		if err != nil {
			t.Fatalf("marshal %s: %v", ts, err)
		}
		if len(data) != WireSize {
			t.Fatalf("encoded size = %d, want %d", len(data), WireSize)
		}
		var got Timestamp
		if err := got.UnmarshalBinary(data); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got != ts {
			t.Errorf("round trip = %s, want %s", got, ts)
		}
	}

	var got Timestamp
	if err := got.UnmarshalBinary(make([]byte, WireSize-1)); err == nil {
		t.Error("short buffer decoded without error")
	}
}

func TestNowMonotonicAndDurable(t *testing.T) {
	fc := NewFakeClock(1000)
	store := &MemStore{}
	clk := newTestClock(t, fc, store, nil)
	ctx := context.Background()

	var prev Timestamp
	for i := 0; i < 100; i++ {
		ts, err := clk.Now(ctx)
		if err != nil {
			t.Fatalf("Now: %v", err)
		}
		if i > 0 && !prev.Before(ts) {
			t.Fatalf("non-monotonic: prev=%s ts=%s", prev, ts)
		}
		if ts.NodeID != testNodeID {
			t.Fatalf("node id = %d", ts.NodeID)
		}
		// The durable lower bound always equals the last returned timestamp.
		if lb := clk.LowerBound(); lb.Physical != ts.Physical || lb.Logical != ts.Logical {
			t.Fatalf("lower bound %+v != timestamp %s", lb, ts)
		}
		if saved, _ := store.Load(); saved != (State{Physical: ts.Physical, Logical: ts.Logical}) {
			t.Fatalf("persisted state %+v != timestamp %s", saved, ts)
		}
		prev = ts
		if i%7 == 0 {
			fc.Advance(1)
		}
	}
}

// TestCounterOverflowWaitsOutsideLock exhausts the 16-bit counter within one
// millisecond and shows the overflow path waits (without holding the lock)
// until the physical clock advances.
func TestCounterOverflowWaitsOutsideLock(t *testing.T) {
	fc := NewFakeClock(5000)
	store := &MemStore{}
	ctl := NewController(StepBeforeWait)
	clk := newTestClock(t, fc, store, ctl.Hook)
	ctx := context.Background()

	// First event on a fresh physical millisecond gets logical 0; fill the
	// remaining counters 0..65535 at physical 5000.
	for i := 0; i <= int(MaxLogical); i++ {
		ts, err := clk.Now(ctx)
		if err != nil {
			t.Fatalf("Now %d: %v", i, err)
		}
		if ts.Physical != 5000 || int(ts.Logical) != i {
			t.Fatalf("ts %d = %s, want physical=5000 logical=%d", i, ts, i)
		}
	}

	// The next call overflows and parks before waiting.
	done := make(chan Timestamp, 1)
	errCh := make(chan error, 1)
	go func() {
		ts, err := clk.Now(withOpID(context.Background(), 99))
		if err != nil {
			errCh <- err
			return
		}
		done <- ts
	}()
	if !ctl.WaitFor(StepBeforeWait, time.Second) {
		t.Fatal("overflowing Now never reached before_wait")
	}

	// The lock must be free while waiting: another goroutine can lock it.
	if !clk.mu.TryLock() {
		t.Fatal("clock lock is held while waiting for physical clock")
	}
	clk.mu.Unlock()

	// Advancing time unblocks the wait and the retry lands on the new ms.
	fc.Advance(1)
	ctl.Release(StepBeforeWait)
	select {
	case ts := <-done:
		if ts.Physical != 5001 || ts.Logical != 0 {
			t.Fatalf("post-overflow ts = %s, want (5001,0)", ts)
		}
	case err := <-errCh:
		t.Fatalf("overflow Now failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("overflowing Now did not return after clock advanced")
	}
}

// TestSameMillisecondConcurrent gives many goroutines the same physical
// instant and verifies every emitted timestamp is unique and ordered.
func TestSameMillisecondConcurrent(t *testing.T) {
	fc := NewFakeClock(8000)
	store := &MemStore{}
	clk := newTestClock(t, fc, store, nil)

	const n = 256
	var wg sync.WaitGroup
	results := make([]Timestamp, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ts, err := clk.Now(context.Background())
			if err != nil {
				t.Errorf("Now %d: %v", i, err)
				return
			}
			results[i] = ts
		}(i)
	}
	close(start)
	wg.Wait()

	seen := make(map[Timestamp]bool, n)
	var min, max Timestamp
	for i, ts := range results {
		if ts.Physical != 8000 {
			t.Fatalf("ts %d physical = %d, want 8000", i, ts.Physical)
		}
		if seen[ts] {
			t.Fatalf("duplicate timestamp %s", ts)
		}
		seen[ts] = true
		if i == 0 || ts.Before(min) {
			min = ts
		}
		if i == 0 || ts.After(max) {
			max = ts
		}
	}
	if min.Logical != 0 || int(max.Logical) != n-1 {
		t.Fatalf("logical range = [%d,%d], want [0,%d]", min.Logical, max.Logical, n-1)
	}
}

// TestSaveFailureRollsBackAndNeverReturns verifies a failed persistence
// attempt discards the candidate, keeps the old lower bound, and the next
// successful call continues strictly after previously returned values.
func TestSaveFailureRollsBackAndNeverReturns(t *testing.T) {
	fc := NewFakeClock(3000)
	store := &MemStore{}
	clk := newTestClock(t, fc, store, nil)
	ctx := context.Background()

	good, err := clk.Now(ctx)
	if err != nil {
		t.Fatalf("initial Now: %v", err)
	}

	store.Fail = true
	if _, err := clk.Now(ctx); !errors.Is(err, ErrInjected) {
		t.Fatalf("failing Now error = %v, want ErrInjected", err)
	}
	store.Fail = false

	if lb := clk.LowerBound(); lb != (State{good.Physical, good.Logical}) {
		t.Fatalf("lower bound mutated on failed save: %+v", lb)
	}
	if saved, _ := store.Load(); saved != (State{good.Physical, good.Logical}) {
		t.Fatalf("store mutated on failed save: %+v", saved)
	}

	next, err := clk.Now(ctx)
	if err != nil {
		t.Fatalf("retry Now: %v", err)
	}
	if !good.Before(next) {
		t.Fatalf("retry ts %s not after good %s", next, good)
	}
	if next.Physical != good.Physical || next.Logical != good.Logical+1 {
		t.Fatalf("retry reused or skipped values: good=%s next=%s", good, next)
	}
}

// TestSaveFailureOnceDoesNotSkipValue verifies the retry of a single failed
// save reproduces the same (physical, logical) candidate rather than burning
// a value, because the failed attempt was never visible to any caller.
func TestSaveFailureOnceDoesNotSkipValue(t *testing.T) {
	fc := NewFakeClock(3000)
	store := &MemStore{}
	clk := newTestClock(t, fc, store, nil)
	ctx := context.Background()

	first, err := clk.Now(ctx)
	if err != nil {
		t.Fatalf("initial Now: %v", err)
	}
	store.FailOnce = true
	if _, err := clk.Now(ctx); !errors.Is(err, ErrInjected) {
		t.Fatalf("injected failure missing: %v", err)
	}
	second, err := clk.Now(ctx)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if second.Physical != first.Physical || second.Logical != first.Logical+1 {
		t.Fatalf("retry = %s, want immediate successor of %s", second, first)
	}
}

// TestWallClockRollbackKeepsMonotonicOrder moves the physical clock
// backwards and verifies timestamps keep advancing via the logical counter.
func TestWallClockRollbackKeepsMonotonicOrder(t *testing.T) {
	fc := NewFakeClock(9000)
	store := &MemStore{}
	clk := newTestClock(t, fc, store, nil)
	ctx := context.Background()

	first, err := clk.Now(ctx) // (9000, 0): first event on a fresh millisecond
	if err != nil {
		t.Fatalf("Now: %v", err)
	}
	fc.Set(8000) // physical clock rollback
	second, err := clk.Now(ctx)
	if err != nil {
		t.Fatalf("Now after rollback: %v", err)
	}
	third, err := clk.Now(ctx)
	if err != nil {
		t.Fatalf("second Now after rollback: %v", err)
	}

	if !(first.Before(second) && second.Before(third)) {
		t.Fatalf("order broken across rollback: %s %s %s", first, second, third)
	}
	if second.Physical != 9000 || second.Logical != 1 {
		t.Fatalf("second = %s, want (9000,1)", second)
	}

	// Once the wall clock catches up again, physical advances and logic resets.
	fc.Set(9001)
	fourth, err := clk.Now(ctx)
	if err != nil {
		t.Fatalf("Now after catch-up: %v", err)
	}
	if fourth.Physical != 9001 || fourth.Logical != 0 {
		t.Fatalf("fourth = %s, want (9001,0)", fourth)
	}
}
