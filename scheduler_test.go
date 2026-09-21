package scheduler

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type runRecord struct {
	round int64
	start time.Time
}

func recvRun(t *testing.T, runs <-chan runRecord) runRecord {
	t.Helper()
	select {
	case r := <-runs:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a run")
		return runRecord{}
	}
}

func expectNoRun(t *testing.T, runs <-chan runRecord) {
	t.Helper()
	select {
	case r := <-runs:
		t.Fatalf("unexpected run of round %d", r.round)
	case <-time.After(50 * time.Millisecond):
	}
}

var testAnchor = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Period 10s, one run takes 25s: the next schedule must follow the
// anchored policy (round 3 at anchor+30s), not run-end+10s (anchor+35s).
func TestSkipPolicyAfterOverrun(t *testing.T) {
	clock := newFakeClock(testAnchor)
	runs := make(chan runRecord, 16)
	s := &Scheduler{
		Clock:  clock,
		Anchor: testAnchor,
		Period: 10 * time.Second,
		Policy: SkipMissed,
		Task: func(ctx context.Context, round int64) error {
			start := clock.Now()
			if round == 0 {
				clock.Advance(25 * time.Second) // simulate 25s of work
			}
			runs <- runRecord{round: round, start: start}
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	first := recvRun(t, runs)
	if first.round != 0 || !first.start.Equal(testAnchor) {
		t.Fatalf("first run = %+v, want round 0 at anchor", first)
	}
	clock.Set(testAnchor.Add(29 * time.Second))
	expectNoRun(t, runs)
	clock.Set(testAnchor.Add(30 * time.Second))
	second := recvRun(t, runs)
	if second.round != 3 || !second.start.Equal(testAnchor.Add(30*time.Second)) {
		t.Fatalf("second run = %+v, want round 3 at anchor+30s", second)
	}
}

// Same overrun with CoalesceMissed: missed rounds 1 and 2 merge into
// one immediate catch-up run (round 2 at anchor+25s), then round 3
// resumes on the anchored schedule at anchor+30s.
func TestCoalescePolicyAfterOverrun(t *testing.T) {
	clock := newFakeClock(testAnchor)
	runs := make(chan runRecord, 16)
	s := &Scheduler{
		Clock:  clock,
		Anchor: testAnchor,
		Period: 10 * time.Second,
		Policy: CoalesceMissed,
		Task: func(ctx context.Context, round int64) error {
			start := clock.Now()
			if round == 0 {
				clock.Advance(25 * time.Second)
			}
			runs <- runRecord{round: round, start: start}
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	if r := recvRun(t, runs); r.round != 0 {
		t.Fatalf("run = %+v, want round 0", r)
	}
	coalesced := recvRun(t, runs)
	if coalesced.round != 2 || !coalesced.start.Equal(testAnchor.Add(25*time.Second)) {
		t.Fatalf("coalesced run = %+v, want round 2 at anchor+25s", coalesced)
	}
	clock.Set(testAnchor.Add(30 * time.Second))
	if r := recvRun(t, runs); r.round != 3 {
		t.Fatalf("run = %+v, want round 3", r)
	}
}

// A clock rewind during a run must not re-execute earlier rounds:
// round IDs stay monotonic and never repeat.
func TestClockRewindDoesNotRepeatRounds(t *testing.T) {
	clock := newFakeClock(testAnchor)
	runs := make(chan runRecord, 16)
	s := &Scheduler{
		Clock:  clock,
		Anchor: testAnchor,
		Period: 10 * time.Second,
		Policy: SkipMissed,
		Task: func(ctx context.Context, round int64) error {
			start := clock.Now()
			if round == 2 {
				clock.Set(testAnchor.Add(5 * time.Second)) // rewind 20s -> 5s
			}
			runs <- runRecord{round: round, start: start}
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	recvRun(t, runs) // round 0 at anchor
	clock.Set(testAnchor.Add(10 * time.Second))
	recvRun(t, runs) // round 1
	clock.Set(testAnchor.Add(20 * time.Second))
	recvRun(t, runs) // round 2, clock rewinds to anchor+5s inside the task

	clock.Set(testAnchor.Add(29 * time.Second))
	expectNoRun(t, runs) // rounds 0-2 must not re-fire
	clock.Set(testAnchor.Add(30 * time.Second))
	r := recvRun(t, runs)
	if r.round != 3 || !r.start.Equal(testAnchor.Add(30*time.Second)) {
		t.Fatalf("run after rewind = %+v, want round 3 at anchor+30s", r)
	}
}

// Canceling the context stops Run and releases the pending timer.
func TestCancelReleasesWaiter(t *testing.T) {
	clock := newFakeClock(testAnchor)
	s := &Scheduler{
		Clock:  clock,
		Anchor: testAnchor,
		Period: 10 * time.Second,
		Policy: SkipMissed,
		Task:   func(ctx context.Context, round int64) error { return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for clock.pendingWaiters() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if clock.pendingWaiters() != 1 {
		t.Fatal("scheduler is not waiting on the clock")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if n := clock.pendingWaiters(); n != 0 {
		t.Fatalf("%d waiters leaked after cancel", n)
	}
}

// Injected task failures are reported via OnError and the schedule
// continues with the next anchored round.
func TestTaskFailureContinuesSchedule(t *testing.T) {
	clock := newFakeClock(testAnchor)
	runs := make(chan runRecord, 16)
	boom := errors.New("boom")
	var mu sync.Mutex
	var errRounds []int64
	s := &Scheduler{
		Clock:  clock,
		Anchor: testAnchor,
		Period: 10 * time.Second,
		Policy: SkipMissed,
		Task: func(ctx context.Context, round int64) error {
			runs <- runRecord{round: round, start: clock.Now()}
			if round == 1 {
				return boom
			}
			return nil
		},
		OnError: func(round int64, err error) {
			if !errors.Is(err, boom) {
				t.Errorf("OnError got %v, want boom", err)
			}
			mu.Lock()
			errRounds = append(errRounds, round)
			mu.Unlock()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	var got []int64
	for i, at := range []time.Duration{0, 10, 20} {
		if i > 0 {
			clock.Set(testAnchor.Add(at * time.Second))
		}
		got = append(got, recvRun(t, runs).round)
	}
	if !reflect.DeepEqual(got, []int64{0, 1, 2}) {
		t.Fatalf("rounds = %v, want [0 1 2]", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(errRounds, []int64{1}) {
		t.Fatalf("error rounds = %v, want [1]", errRounds)
	}
}

// Smoke test against the real clock, exercising timer cleanup on cancel.
func TestRealClockSmoke(t *testing.T) {
	runs := make(chan int64, 8)
	s := &Scheduler{
		Clock:  RealClock{},
		Anchor: time.Now(),
		Period: 20 * time.Millisecond,
		Policy: SkipMissed,
		Task:   func(ctx context.Context, round int64) error { runs <- round; return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	for i := 0; i < 3; i++ {
		select {
		case <-runs:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for real-clock run")
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}
