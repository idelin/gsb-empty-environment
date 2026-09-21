package scheduler

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// recorder is an injected task sink that records executed round IDs.
type recorder struct {
	mu     sync.Mutex
	rounds []int64
}

func (r *recorder) record(round int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rounds = append(r.rounds, round)
}

func (r *recorder) snapshot() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.rounds...)
}

func (r *recorder) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := len(r.rounds)
		r.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d executions, got %v", n, r.snapshot())
}

func waitForTimers(t *testing.T, c *FakeClock, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.PendingTimers() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pending timers, got %d", n, c.PendingTimers())
}

// errRecorder collects injected failures reported via OnError.
type errRecorder struct {
	mu   sync.Mutex
	errs map[int64]error
}

func newErrRecorder() *errRecorder { return &errRecorder{errs: make(map[int64]error)} }

func (e *errRecorder) onError(round int64, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errs[round] = err
}

func (e *errRecorder) get(round int64) (error, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	err, ok := e.errs[round]
	return err, ok
}

// Period 10, one run takes 25: with SkipMissed the next run must be round 3
// anchored at Base+30, not "end of run + 10" (Base+35).
func TestSkipMissedAfterOverrun(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	clock := NewFakeClock(base)
	rec := &recorder{}

	task := func(ctx context.Context, round int64) error {
		rec.record(round)
		if round == 0 {
			clock.Advance(25 * time.Second) // run overruns 2.5 periods
		}
		return nil
	}
	s := New(clock, task, Options{Base: base, Period: 10 * time.Second, Policy: SkipMissed})
	s.Start()
	defer s.Stop()

	rec.waitFor(t, 1) // round 0 runs at Base, finishes at Base+25
	waitForTimers(t, clock, 1)

	clock.Advance(9 * time.Second) // t = Base+34: round 3 (Base+30) fires
	rec.waitFor(t, 2)

	if got, want := rec.snapshot(), []int64{0, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rounds = %v, want %v (anchored schedule, not end+period)", got, want)
	}
	if got := clock.Now(); got.Before(base.Add(30*time.Second)) || !got.Before(base.Add(35*time.Second)) {
		t.Fatalf("second run happened at %v, want within [Base+30, Base+35)", got)
	}
}

// Same overrun with MergeMissed: rounds 1 and 2 are coalesced into a single
// run carrying the latest missed round ID, then the anchored cadence resumes.
func TestMergeMissedAfterOverrun(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	clock := NewFakeClock(base)
	rec := &recorder{}

	task := func(ctx context.Context, round int64) error {
		rec.record(round)
		if round == 0 {
			clock.Advance(25 * time.Second)
		}
		return nil
	}
	s := New(clock, task, Options{Base: base, Period: 10 * time.Second, Policy: MergeMissed})
	s.Start()
	defer s.Stop()

	rec.waitFor(t, 2) // round 0, then merged run for rounds 1-2
	waitForTimers(t, clock, 1)

	clock.Advance(10 * time.Second) // round 3 fires at Base+30
	rec.waitFor(t, 3)

	if got, want := rec.snapshot(), []int64{0, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rounds = %v, want %v (missed rounds merged into latest)", got, want)
	}
}

// After a clock rollback, already-executed round IDs must never repeat.
func TestClockRollbackDoesNotRepeatRounds(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	clock := NewFakeClock(base)
	rec := &recorder{}

	task := func(ctx context.Context, round int64) error {
		rec.record(round)
		return nil
	}
	s := New(clock, task, Options{Base: base, Period: 10 * time.Second, Policy: SkipMissed})
	s.Start()
	defer s.Stop()

	// Execute rounds 0, 1, 2.
	rec.waitFor(t, 1)
	for i := 0; i < 2; i++ {
		waitForTimers(t, clock, 1)
		clock.Advance(10 * time.Second)
		rec.waitFor(t, i+2)
	}

	// Roll the clock back 17s: wall time now sits inside round 0 again.
	waitForTimers(t, clock, 1)
	clock.SetNow(base.Add(3 * time.Second))

	// Advance well past the rollback; round 3 is anchored at Base+30, so it
	// fires only after the clock climbs back to Base+30.
	clock.Advance(26 * time.Second) // t = Base+29: nothing new
	if got := rec.snapshot(); len(got) != 3 {
		t.Fatalf("after rollback got extra runs %v, want none before Base+30", got)
	}
	clock.Advance(2 * time.Second) // t = Base+31: round 3 fires
	rec.waitFor(t, 4)
	waitForTimers(t, clock, 1)
	clock.Advance(10 * time.Second) // round 4
	rec.waitFor(t, 5)

	got := rec.snapshot()
	for i, r := range got {
		if i > 0 && r <= got[i-1] {
			t.Fatalf("round IDs not strictly increasing after rollback: %v", got)
		}
	}
	if want := []int64{0, 1, 2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rounds = %v, want %v (no duplicates after rollback)", got, want)
	}
}

// A run that ignores its deadline is abandoned after ExecTimeout and the
// failure is reported; the schedule keeps its anchored cadence.
func TestExecutionTimeout(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	clock := NewFakeClock(base)
	rec := &recorder{}
	errs := newErrRecorder()

	task := func(ctx context.Context, round int64) error {
		rec.record(round)
		if round == 0 {
			<-ctx.Done() // block until the per-run timeout fires
			return ctx.Err()
		}
		return nil
	}
	s := New(clock, task, Options{
		Base:        base,
		Period:      10 * time.Second,
		ExecTimeout: 50 * time.Millisecond, // real-time bound for the test
		Policy:      SkipMissed,
		OnError:     errs.onError,
	})
	s.Start()
	defer s.Stop()

	rec.waitFor(t, 1)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := errs.get(0); ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	err, ok := errs.get(0)
	if !ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("round 0 error = %v (ok=%v), want context.DeadlineExceeded", err, ok)
	}

	waitForTimers(t, clock, 1)
	clock.Advance(10 * time.Second)
	rec.waitFor(t, 2)
	if got := rec.snapshot(); !reflect.DeepEqual(got, []int64{0, 1}) {
		t.Fatalf("rounds = %v, want [0 1]: schedule must continue after timeout", got)
	}
}

// Injected task failures are reported per round and do not stop the schedule.
func TestTaskFailureIsReportedAndSchedulingContinues(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	clock := NewFakeClock(base)
	rec := &recorder{}
	errs := newErrRecorder()
	boom := errors.New("boom")

	task := func(ctx context.Context, round int64) error {
		rec.record(round)
		if round == 1 {
			return boom
		}
		return nil
	}
	s := New(clock, task, Options{
		Base:    base,
		Period:  10 * time.Second,
		Policy:  SkipMissed,
		OnError: errs.onError,
	})
	s.Start()
	defer s.Stop()

	rec.waitFor(t, 1)
	for i := 0; i < 2; i++ {
		waitForTimers(t, clock, 1)
		clock.Advance(10 * time.Second)
		rec.waitFor(t, i+2)
	}

	if err, ok := errs.get(1); !ok || !errors.Is(err, boom) {
		t.Fatalf("round 1 error = %v (ok=%v), want %v", err, ok, boom)
	}
	if got, want := rec.snapshot(), []int64{0, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rounds = %v, want %v", got, want)
	}
}

// Stop cancels the loop and disposes of the armed timer.
func TestStopCleansUpTimer(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	clock := NewFakeClock(base)
	rec := &recorder{}

	task := func(ctx context.Context, round int64) error {
		rec.record(round)
		return nil
	}
	s := New(clock, task, Options{Base: base, Period: 10 * time.Second, Policy: SkipMissed})
	s.Start()

	rec.waitFor(t, 1) // round 0 done, timer for round 1 armed
	waitForTimers(t, clock, 1)

	s.Stop()
	if got := clock.PendingTimers(); got != 0 {
		t.Fatalf("PendingTimers after Stop = %d, want 0", got)
	}

	// Advancing the clock after Stop must not run anything.
	clock.Advance(time.Hour)
	time.Sleep(10 * time.Millisecond)
	if got := rec.snapshot(); !reflect.DeepEqual(got, []int64{0}) {
		t.Fatalf("rounds after Stop = %v, want [0]", got)
	}
	if got := s.LastRound(); got != 0 {
		t.Fatalf("LastRound = %d, want 0", got)
	}
}
