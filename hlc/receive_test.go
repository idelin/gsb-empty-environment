package hlc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func remote(physical int64, logical uint16, node uint64) Timestamp {
	return Timestamp{Physical: physical, Logical: logical, NodeID: node}
}

// TestReceiveHLC rules checks the three standard HLC receive branches.
func TestReceiveHLCRules(t *testing.T) {
	ctx := context.Background()

	t.Run("wall dominates", func(t *testing.T) {
		fc := NewFakeClock(100)
		store := &MemStore{}
		clk := newTestClock(t, fc, store, nil)

		ts, err := clk.Receive(ctx, remote(50, 40, 2))
		if err != nil {
			t.Fatal(err)
		}
		if ts.Physical != 100 || ts.Logical != 0 {
			t.Fatalf("ts = %s, want (100,0)", ts)
		}
	})

	t.Run("remote dominates", func(t *testing.T) {
		fc := NewFakeClock(100)
		store := &MemStore{}
		clk := newTestClock(t, fc, store, nil)

		ts, err := clk.Receive(ctx, remote(150, 40, 2))
		if err != nil {
			t.Fatal(err)
		}
		// physical adopts remote, logical is remote+1
		if ts.Physical != 150 || ts.Logical != 41 {
			t.Fatalf("ts = %s, want (150,41)", ts)
		}
		if !remote(150, 40, 2).Before(ts) {
			t.Fatal("receive timestamp not after remote timestamp")
		}
	})

	t.Run("local dominates same physical", func(t *testing.T) {
		fc := NewFakeClock(100)
		store := &MemStore{}
		clk := newTestClock(t, fc, store, nil)

		first, _ := clk.Now(ctx) // (100,1)
		ts, err := clk.Receive(ctx, remote(100, 3, 2))
		if err != nil {
			t.Fatal(err)
		}
		// same physical: adopt max(local=1, remote=3)+1 = 4
		if ts.Physical != 100 || ts.Logical != 4 {
			t.Fatalf("ts = %s, want (100,4)", ts)
		}
		if !first.Before(ts) {
			t.Fatal("receive timestamp not after prior local timestamp")
		}

		// A stale remote on the same physical just ticks the local counter.
		ts2, err := clk.Receive(ctx, remote(100, 0, 3))
		if err != nil {
			t.Fatal(err)
		}
		if ts2.Physical != 100 || ts2.Logical != 5 {
			t.Fatalf("ts2 = %s, want (100,5)", ts2)
		}
	})

	t.Run("local dominates higher physical", func(t *testing.T) {
		fc := NewFakeClock(100)
		store := &MemStore{}
		clk := newTestClock(t, fc, store, nil)

		// Push the local lower bound into the future via a remote event.
		if _, err := clk.Receive(ctx, remote(200, 0, 2)); err != nil {
			t.Fatal(err)
		}
		fc.Set(150) // wall now behind the local lower bound
		ts, err := clk.Receive(ctx, remote(180, 10, 3))
		if err != nil {
			t.Fatal(err)
		}
		if ts.Physical != 200 || ts.Logical != 2 {
			t.Fatalf("ts = %s, want (200,2)", ts)
		}
	})
}

// TestReceiveRejectsRemoteAhead verifies the drift guard rejects a far-ahead
// remote timestamp without polluting the local clock.
func TestReceiveRejectsRemoteAhead(t *testing.T) {
	fc := NewFakeClock(1000)
	store := &MemStore{}
	clk := newTestClock(t, fc, store, nil)
	ctx := context.Background()

	if _, err := clk.Now(ctx); err != nil {
		t.Fatal(err)
	}

	// Exactly at the boundary is accepted (wall + maxDrift).
	boundary, err := clk.Receive(ctx, remote(2000, 0, 2))
	if err != nil {
		t.Fatalf("boundary remote rejected: %v", err)
	}
	if boundary.Physical != 2000 || boundary.Logical != 1 {
		t.Fatalf("boundary ts = %s", boundary)
	}

	far := remote(2001, 5, 2)
	if _, err := clk.Receive(ctx, far); !errors.Is(err, ErrRemoteAhead) {
		t.Fatalf("far remote error = %v, want ErrRemoteAhead", err)
	}

	// Local clock untouched: next local event follows the accepted boundary.
	after, err := clk.Now(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !boundary.Before(after) {
		t.Fatalf("local clock polluted by rejected remote: boundary=%s after=%s", boundary, after)
	}
	if after.Physical != 2000 || after.Logical != 2 {
		t.Fatalf("after = %s, want (2000,2)", after)
	}

	// The rejected timestamp must never have been persisted.
	if saved, _ := store.Load(); saved == (State{far.Physical, far.Logical}) {
		t.Fatal("rejected remote timestamp was persisted")
	}

	// Rejection is observable through the StepRejected hook with no mutation.
	ctl := NewController(StepRejected)
	clk2 := newTestClock(t, NewFakeClock(0), &MemStore{}, ctl.Hook)
	go func() {
		_, _ = clk2.Receive(withOpID(context.Background(), 5), remote(1_000_000, 0, 9))
	}()
	if !ctl.WaitFor(StepRejected, time.Second) {
		t.Fatal("rejected receive did not fire StepRejected")
	}
	ctl.Release(StepRejected)
}

// TestReceiveCounterOverflowWaits drives a receive into logical-counter
// exhaustion and verifies the wait happens outside the lock.
func TestReceiveCounterOverflowWaits(t *testing.T) {
	fc := NewFakeClock(4000)
	store := &MemStore{}
	ctl := NewController(StepBeforeWait)
	clk := newTestClock(t, fc, store, ctl.Hook)
	ctx := context.Background()

	// Put the local clock at (4000,65535): the next receive on the same
	// physical must overflow and wait for time to advance.
	for i := 0; i <= int(MaxLogical); i++ {
		if _, err := clk.Now(ctx); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}

	done := make(chan Timestamp, 1)
	errCh := make(chan error, 1)
	go func() {
		ts, err := clk.Receive(withOpID(context.Background(), 11), remote(4000, 0, 3))
		if err != nil {
			errCh <- err
			return
		}
		done <- ts
	}()
	if !ctl.WaitFor(StepBeforeWait, time.Second) {
		t.Fatal("overflowing Receive never reached before_wait")
	}
	if !clk.mu.TryLock() {
		t.Fatal("clock lock held while receive waits for physical clock")
	}
	clk.mu.Unlock()

	fc.Advance(1)
	ctl.Release(StepBeforeWait)
	select {
	case ts := <-done:
		if ts.Physical != 4001 || ts.Logical != 0 {
			t.Fatalf("post-overflow receive = %s, want (4001,0)", ts)
		}
	case err := <-errCh:
		t.Fatalf("overflow receive failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("overflowing Receive did not return")
	}
}

// TestRestartReusesPersistedLowerBound rebuilds the Clock on the same store
// and verifies post-restart timestamps never repeat pre-restart values, even
// when the physical clock moves backwards while the process is down.
func TestRestartReusesPersistedLowerBound(t *testing.T) {
	fc := NewFakeClock(6000)
	store := &MemStore{}
	clk := newTestClock(t, fc, store, nil)
	ctx := context.Background()

	var last Timestamp
	for i := 0; i < 5; i++ {
		ts, err := clk.Now(ctx)
		if err != nil {
			t.Fatal(err)
		}
		last = ts
	}

	// Simulate downtime with a wall-clock rollback, then rebuild the clock.
	fc.Set(100)
	reborn, err := New(Config{NodeID: testNodeID, Physical: fc, Store: store})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	ts, err := reborn.Now(ctx)
	if err != nil {
		t.Fatalf("post-restart Now: %v", err)
	}
	if !last.Before(ts) {
		t.Fatalf("restart repeated value: last=%s next=%s", last, ts)
	}
	if ts.Physical != last.Physical || ts.Logical != last.Logical+1 {
		t.Fatalf("post-restart ts = %s, want successor of %s", ts, last)
	}

	// Persisted state again drives a second restart.
	reborn2, err := New(Config{NodeID: testNodeID, Physical: fc, Store: store})
	if err != nil {
		t.Fatalf("second restart: %v", err)
	}
	ts2, err := reborn2.Now(ctx)
	if err != nil {
		t.Fatalf("second post-restart Now: %v", err)
	}
	if !ts.Before(ts2) {
		t.Fatalf("second restart not monotonic: %s %s", ts, ts2)
	}
}

// TestCrashBetweenSaveAndReturn models a crash in the allowed window after
// the durable write succeeds but before the caller observes the value. On
// restart the clock must not re-issue that persisted value.
func TestCrashBetweenSaveAndReturn(t *testing.T) {
	fc := NewFakeClock(7000)
	store := &MemStore{}
	ctl := NewController(StepSaved)
	clk := newTestClock(t, fc, store, ctl.Hook)

	result := make(chan Timestamp, 1)
	go func() {
		ts, err := clk.Now(withOpID(context.Background(), 22))
		if err == nil {
			result <- ts
		}
	}()
	if !ctl.WaitFor(StepSaved, time.Second) {
		t.Fatal("Now never reached saved hook")
	}

	// The value is already durable at this point; read it off the store.
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	// Simulate process death: the parked goroutine is abandoned and a fresh
	// clock is built on the same durable store.
	reborn, err := New(Config{NodeID: testNodeID, Physical: fc, Store: store})
	if err != nil {
		t.Fatalf("restart after crash: %v", err)
	}
	next, err := reborn.Now(context.Background())
	if err != nil {
		t.Fatalf("post-crash Now: %v", err)
	}

	crashed := Timestamp{Physical: saved.Physical, Logical: saved.Logical, NodeID: testNodeID}
	if next.Compare(crashed) <= 0 {
		t.Fatalf("re-issued crashed value: crashed=%s next=%s", crashed, next)
	}
	if next.Physical != crashed.Physical || next.Logical != crashed.Logical+1 {
		t.Fatalf("next = %s, want immediate successor of crashed %s", next, crashed)
	}

	// Let the original goroutine finish; its value equals the durable one and
	// is still strictly before every post-restart value.
	ctl.Release(StepSaved)
	select {
	case original := <-result:
		if original != crashed {
			t.Fatalf("returned %s, want durable %s", original, crashed)
		}
		if !original.Before(next) {
			t.Fatal("post-crash value not after pre-crash value")
		}
	case <-time.After(time.Second):
		t.Fatal("original Now did not finish after release")
	}
}
