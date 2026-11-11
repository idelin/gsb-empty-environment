package hlc

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

// scriptedClock replays pre-recorded physical-clock readings to a reference
// clock, one reading per operation.
type scriptedClock struct {
	mu       sync.Mutex
	readings []int64
	last     int64
}

func (s *scriptedClock) NowMillis() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.readings) == 0 {
		// Overflow retries re-read the clock; keep returning the last known
		// wall value, matching a FakeClock that has not been advanced.
		return s.last
	}
	v := s.readings[0]
	s.readings = s.readings[1:]
	s.last = v
	return v
}

// WaitUntil models the test harness advancing the fake clock past an
// overflow: the reference clock simply jumps to the requested target.
func (s *scriptedClock) WaitUntil(target int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target > s.last {
		s.last = target
	}
}

type testOp struct {
	id     uint64
	remote *Timestamp // nil => local Now, non-nil => Receive
}

type observed struct {
	opID uint64
	wall int64
	ts   Timestamp
}

// runConcurrentInterleaving executes ops on goroutines but forces the lock
// acquisition (and therefore commit) order through the deterministic
// controller, recording the wall clock seen at each commit.
func runConcurrentInterleaving(t *testing.T, ops []testOp, order []int, wall int64) []observed {
	t.Helper()
	fc := NewFakeClock(wall)
	store := &MemStore{}
	ctl := NewController(StepLocked, StepBeforeWait, StepAfterWait, StepSaved, StepRejected)
	clk, err := New(Config{
		NodeID:   testNodeID,
		Physical: fc,
		Store:    store,
		MaxDrift: 1 << 40, // effectively unlimited; drift guard has its own tests
		Hook:     ctl.Hook,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var mu sync.Mutex
	recorded := make(map[uint64]observed)
	errCh := make(chan error, len(ops))

	// Start and complete one goroutine at a time in the chosen order. Only one
	// op ever contends for the clock lock, so the commit sequence is fully
	// deterministic while the work still runs on separate goroutines (which is
	// what exercises locking, overflow waits and the durable commit path).
	var wg sync.WaitGroup
	for _, idx := range order {
		op := ops[idx]
		opID := op.id
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := withOpID(context.Background(), op.id)
			var ts Timestamp
			var err error
			if op.remote == nil {
				ts, err = clk.Now(ctx)
			} else {
				ts, err = clk.Receive(ctx, *op.remote)
			}
			if err != nil {
				errCh <- err
				return
			}
			mu.Lock()
			recorded[op.id] = observed{opID: op.id, wall: wall, ts: ts}
			mu.Unlock()
		}()

		// Drive this single op through locked/overflow until it commits.
		for {
			if ctl.ParkedOp(opID, StepRejected) {
				ctl.ReleaseOp(opID, StepRejected)
				t.Fatalf("op %d rejected as too far ahead; interleaving ops must stay within drift", opID)
			}
			if !ctl.WaitParkedOp(opID, StepLocked, 2*time.Second) {
				t.Fatalf("op %d never parked at locked; parked=%v wall=%d", opID, ctl.ParkedAt(), fc.NowMillis())
			}
			if !ctl.ReleaseOp(opID, StepLocked) {
				t.Fatalf("op %d was not parked at locked", opID)
			}
			// After releasing locked the op either parks at before_wait
			// (overflow) or reaches saved (durable commit). Wait for one of
			// those two parked states.
			deadline := time.Now().Add(2 * time.Second)
			for !ctl.ParkedOp(opID, StepBeforeWait) &&
				!ctl.ParkedOp(opID, StepAfterWait) &&
				!ctl.ParkedOp(opID, StepSaved) &&
				!ctl.ParkedOp(opID, StepRejected) {
				if time.Now().After(deadline) {
					t.Fatalf("op %d reached neither before_wait nor saved; parked=%v wall=%d", opID, ctl.ParkedAt(), fc.NowMillis())
				}
				time.Sleep(time.Millisecond)
			}
			if ctl.ParkedOp(opID, StepRejected) {
				ctl.ReleaseOp(opID, StepRejected)
				t.Fatalf("op %d rejected as too far ahead; interleaving ops must stay within drift", opID)
			}
			if ctl.ParkedOp(opID, StepBeforeWait) {
				ctl.DriveWait(fc, opID, 1)
				if !ctl.WaitParkedOp(opID, StepAfterWait, 2*time.Second) {
					t.Fatalf("op %d did not reach after_wait; parked=%v", opID, ctl.ParkedAt())
				}
				ctl.ReleaseOp(opID, StepAfterWait)
				continue
			}
			// Committed: release the saved hook so the goroutine returns.
			if !ctl.ReleaseOp(opID, StepSaved) {
				t.Fatalf("op %d saved hook not parked", opID)
			}
			break
		}
		wg.Wait()
	}
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	out := make([]observed, 0, len(order))
	for _, idx := range order {
		out = append(out, recorded[ops[idx].id])
	}
	return out
}

// referenceModel replays the same operations in the observed commit order on
// a single-threaded clock, using the recorded wall readings.
func referenceModel(t *testing.T, ops []testOp, order []int, observations []observed, nodeID uint64) []Timestamp {
	t.Helper()
	readings := make([]int64, len(observations))
	for i := range observations {
		readings[i] = observations[i].wall
	}
	ref, err := New(Config{
		NodeID:   nodeID,
		Physical: &scriptedClock{readings: readings},
		Store:    &MemStore{},
		MaxDrift: 1 << 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]Timestamp, 0, len(order))
	for _, idx := range order {
		op := ops[idx]
		var ts Timestamp
		if op.remote == nil {
			ts, err = ref.Now(context.Background())
		} else {
			ts, err = ref.Receive(context.Background(), *op.remote)
		}
		if err != nil {
			t.Fatalf("reference op %d: %v", op.id, err)
		}
		out = append(out, ts)
	}
	return out
}

func makeOps() []testOp {
	ops := make([]testOp, 14)
	for i := range ops {
		ops[i].id = uint64(i + 1)
	}
	// Mix of local events and receives exercising all HLC branches.
	remotes := map[int]Timestamp{
		1:  remote(500, 3, 2),
		3:  remote(500, 9, 3),
		5:  remote(490, 100, 4),
		6:  remote(500, 1, 5),
		8:  remote(500, 65534, 6),
		10: remote(500, 20, 7),
		12: remote(480, 0, 8),
	}
	for i, r := range remotes {
		r := r
		ops[i].remote = &r
	}
	return ops
}

// TestCausalMonotonicityArbitraryInterleavings runs many random commit
// orders of the same receive/local operation set and asserts three things
// against every ordering:
//
//  1. outputs match a single-threaded reference model replayed in the same
//     commit order;
//  2. committed timestamps are strictly increasing (causal monotonicity);
//  3. every receive output is after its remote timestamp, and all emitted
//     timestamps are distinct.
func TestCausalMonotonicityArbitraryInterleavings(t *testing.T) {
	ops := makeOps()
	baseOrder := make([]int, len(ops))
	for i := range baseOrder {
		baseOrder[i] = i
	}

	for seed := uint64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed*7+1))
		order := append([]int(nil), baseOrder...)
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			observations := runConcurrentInterleaving(t, ops, order, 500)
			expected := referenceModel(t, ops, order, observations, testNodeID)

			seen := make(map[Timestamp]bool, len(observations))
			var prev Timestamp
			for i, obs := range observations {
				got := obs.ts
				want := expected[i]
				if got.Physical != want.Physical || got.Logical != want.Logical {
					t.Fatalf("commit %d: got %s, want (physical=%d logical=%d)",
						i, got, want.Physical, want.Logical)
				}
				if got.NodeID != testNodeID {
					t.Fatalf("commit %d: node id = %d", i, got.NodeID)
				}
				if seen[got] {
					t.Fatalf("commit %d: duplicate timestamp %s", i, got)
				}
				seen[got] = true
				if i > 0 && !prev.Before(got) {
					t.Fatalf("commit %d breaks causal order: prev=%s got=%s", i, prev, got)
				}
				if op := ops[order[i]]; op.remote != nil && !op.remote.Before(got) {
					t.Fatalf("commit %d: receive output %s not after remote %s", i, got, *op.remote)
				}
				prev = got
			}
		})
	}
}

// TestInterleavingWithPhysicalAdvancement interleaves operations across
// multiple physical milliseconds using per-commit wall readings, again
// comparing against the reference model.
func TestInterleavingWithPhysicalAdvancement(t *testing.T) {
	ops := makeOps()
	rng := rand.New(rand.NewPCG(99, 17))
	order := make([]int, len(ops))
	for i := range order {
		order[i] = i
	}
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

	fc := NewFakeClock(500)
	store := &MemStore{}
	ctl := NewController(StepLocked, StepSaved)
	clk, err := New(Config{
		NodeID:   testNodeID,
		Physical: fc,
		Store:    store,
		MaxDrift: 1 << 40,
		Hook:     ctl.Hook,
	})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	recorded := make(map[uint64]observed)
	var wg sync.WaitGroup
	for i := range ops {
		op := ops[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := withOpID(context.Background(), op.id)
			var ts Timestamp
			var err error
			if op.remote == nil {
				ts, err = clk.Now(ctx)
			} else {
				ts, err = clk.Receive(ctx, *op.remote)
			}
			if err != nil {
				t.Errorf("op %d: %v", op.id, err)
				return
			}
			mu.Lock()
			recorded[op.id] = observed{opID: op.id, wall: fc.NowMillis(), ts: ts}
			mu.Unlock()
		}()
	}

	commitWalls := make([]int64, len(order))
	for commit, idx := range order {
		opID := ops[idx].id
		if !ctl.WaitParkedOp(opID, StepLocked, 2*time.Second) {
			t.Fatal("goroutine never parked")
		}
		if commit%3 == 1 {
			fc.Advance(1) // physical time moves between some commits
		}
		// The wall is sampled while the op holds the lock, before release.
		commitWalls[commit] = fc.NowMillis()
		if !ctl.ReleaseOp(opID, StepLocked) {
			t.Fatal("release missed parked goroutine")
		}
		if !ctl.WaitParkedOp(opID, StepSaved, 2*time.Second) {
			t.Fatalf("op %d never reached saved; parked=%v", opID, ctl.ParkedAt())
		}
		ctl.ReleaseOp(opID, StepSaved)
	}
	wg.Wait()

	observations := make([]observed, 0, len(order))
	for commit, idx := range order {
		obs := recorded[ops[idx].id]
		obs.wall = commitWalls[commit] // wall sampled while the lock was held
		observations = append(observations, obs)
	}
	expected := referenceModel(t, ops, order, observations, testNodeID)
	var prev Timestamp
	for i, obs := range observations {
		got := obs.ts
		if got.Physical != expected[i].Physical || got.Logical != expected[i].Logical {
			t.Fatalf("commit %d: got %s want %s", i, got, expected[i])
		}
		if i > 0 && !prev.Before(got) {
			t.Fatalf("commit %d not monotonic: %s -> %s", i, prev, got)
		}
		prev = got
	}
}

// TestInterleavingWithRestart crashes and rebuilds the clock in the middle of
// a scheduled interleaving and verifies no value is re-issued afterwards.
func TestInterleavingWithRestart(t *testing.T) {
	ops := makeOps()[:8]
	fc := NewFakeClock(500)
	store := &MemStore{}
	ctl := NewController(StepLocked, StepBeforeWait, StepAfterWait, StepSaved)
	var err error
	clk, err := New(Config{
		NodeID:   testNodeID,
		Physical: fc,
		Store:    store,
		MaxDrift: 1 << 40,
		Hook:     ctl.Hook,
	})
	if err != nil {
		t.Fatal(err)
	}

	// start launches op on c and returns its result channels.
	start := func(c *Clock, op testOp) (<-chan Timestamp, <-chan error) {
		done := make(chan Timestamp, 1)
		errCh := make(chan error, 1)
		go func() {
			ctx := withOpID(context.Background(), op.id)
			var ts Timestamp
			var err error
			if op.remote == nil {
				ts, err = c.Now(ctx)
			} else {
				ts, err = c.Receive(ctx, *op.remote)
			}
			if err != nil {
				errCh <- err
				return
			}
			done <- ts
		}()
		return done, errCh
	}

	// driveToSaved runs the lock/overflow state machine of a started op up to
	// the StepSaved hook and leaves it parked there (the durable write has
	// happened, but the caller has not yet observed the value).
	driveToSaved := func(op testOp) {
		for {
			if !ctl.WaitParkedOp(op.id, StepLocked, time.Second) {
				t.Fatalf("op %d locked not reached; parked=%v", op.id, ctl.ParkedAt())
			}
			ctl.ReleaseOp(op.id, StepLocked)
			deadline := time.Now().Add(time.Second)
			for !ctl.ParkedOp(op.id, StepBeforeWait) && !ctl.ParkedOp(op.id, StepSaved) {
				if time.Now().After(deadline) {
					t.Fatalf("op %d no terminal step; parked=%v", op.id, ctl.ParkedAt())
				}
				time.Sleep(time.Millisecond)
			}
			if ctl.ParkedOp(op.id, StepSaved) {
				return
			}
			ctl.DriveWait(fc, op.id, 1)
			if !ctl.WaitParkedOp(op.id, StepAfterWait, time.Second) {
				t.Fatalf("op %d after_wait not reached; parked=%v", op.id, ctl.ParkedAt())
			}
			ctl.ReleaseOp(op.id, StepAfterWait)
		}
	}

	runOne := func(c *Clock, op testOp) Timestamp {
		done, errCh := start(c, op)
		driveToSaved(op)
		ctl.ReleaseOp(op.id, StepSaved)
		select {
		case ts := <-done:
			return ts
		case err := <-errCh:
			t.Fatal(err)
		}
		return Timestamp{}
	}

	var emitted []Timestamp
	for i := 0; i < 3; i++ {
		emitted = append(emitted, runOne(clk, ops[i]))
	}

	// Park the fourth operation after its durable write, then crash.
	fourthDone, fourthErr := start(clk, ops[3])
	driveToSaved(ops[3])
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	crashed := Timestamp{Physical: saved.Physical, Logical: saved.Logical, NodeID: testNodeID}

	// Rebuild on the same durable store; the wall clock even goes backwards.
	fc.Set(100)
	reborn, err := New(Config{NodeID: testNodeID, Physical: fc, Store: store, Hook: ctl.Hook})
	if err != nil {
		t.Fatal(err)
	}

	// Resume the crashed goroutine for cleanliness, then continue on reborn.
	ctl.ReleaseOp(ops[3].id, StepSaved)
	select {
	case <-fourthDone:
	case err := <-fourthErr:
		t.Fatal(err)
	}

	allSoFar := append(append([]Timestamp{}, emitted...), crashed)
	for i := 4; i < len(ops); i++ {
		allSoFar = append(allSoFar, runOne(reborn, ops[i]))
	}

	// No duplicates and a global strict ordering over the whole history.
	seen := make(map[Timestamp]bool, len(allSoFar))
	for i, ts := range allSoFar {
		if seen[ts] {
			t.Fatalf("duplicate timestamp across restart: %s", ts)
		}
		seen[ts] = true
		if i > 0 && !allSoFar[i-1].Before(ts) {
			t.Fatalf("history not strictly increasing at %d: %s -> %s", i, allSoFar[i-1], ts)
		}
	}
}
