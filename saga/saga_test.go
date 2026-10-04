package saga

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tracker simulates an external resource that actions acquire and
// compensations release; the final count must be zero (no leak).
type tracker struct{ outstanding *int32 }

func newTracker() *tracker { return &tracker{outstanding: new(int32)} }

func (t *tracker) acquire(ctx context.Context) error {
	atomic.AddInt32(t.outstanding, 1)
	return nil
}

func (t *tracker) release(ctx context.Context) error {
	atomic.AddInt32(t.outstanding, -1)
	return nil
}

func (t *tracker) leaked() int32 { return atomic.LoadInt32(t.outstanding) }

// log is a concurrency-safe ordered event log.
type log struct {
	mu     sync.Mutex
	events []string
}

func (l *log) add(e string) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *log) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.events))
	copy(out, l.events)
	return out
}

func stepNames(recs []StepRecord) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r.Name)
	}
	return out
}

func TestSuccessPath(t *testing.T) {
	tr := newTracker()
	var l log
	steps := make([]Step, 3)
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("s%d", i)
		steps[i] = Step{
			Name: name,
			Action: func(ctx context.Context) error {
				l.add("act:" + name)
				return tr.acquire(ctx)
			},
			Compensate: func(ctx context.Context) error {
				l.add("cmp:" + name)
				return tr.release(ctx)
			},
		}
	}
	s := New(steps...)
	if s.Status() != StatusPending {
		t.Fatalf("initial status = %v, want Pending", s.Status())
	}
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Status() != StatusCompleted {
		t.Fatalf("status = %v, want Completed", s.Status())
	}
	if got := l.snapshot(); strings.Join(got, ",") != "act:s0,act:s1,act:s2" {
		t.Fatalf("event order = %v", got)
	}
	// Success commits the flow: acquired resources stay outstanding by
	// design (they belong to the caller now). Nothing was rolled back.
	if tr.leaked() != 3 {
		t.Fatalf("outstanding = %d after success, want 3 committed", tr.leaked())
	}
	// No compensation may have run.
	for _, r := range s.Records() {
		if r.CompensateCalls != 0 || r.ActionCalls != 1 {
			t.Fatalf("record %+v: want exactly one action, zero compensations", r)
		}
	}
	// Transition journal: Pending -> Running -> Completed.
	trs := s.Transitions()
	if len(trs) != 2 || trs[0].From != StatusPending || trs[0].To != StatusRunning ||
		trs[1].From != StatusRunning || trs[1].To != StatusCompleted {
		t.Fatalf("transitions = %+v", trs)
	}
}

func TestFailureTriggersStrictReverseCompensation(t *testing.T) {
	tr := newTracker()
	var l log
	boom := errors.New("charge declined")
	steps := []Step{
		{Name: "reserve", Action: func(ctx context.Context) error {
			l.add("act:reserve")
			return tr.acquire(ctx)
		}, Compensate: func(ctx context.Context) error {
			l.add("cmp:reserve")
			return tr.release(ctx)
		}},
		{Name: "authorize", Action: func(ctx context.Context) error {
			l.add("act:authorize")
			return tr.acquire(ctx)
		}, Compensate: func(ctx context.Context) error {
			l.add("cmp:authorize")
			return tr.release(ctx)
		}},
		{Name: "charge", Action: func(ctx context.Context) error {
			l.add("act:charge")
			return boom
		}, Compensate: func(ctx context.Context) error {
			l.add("cmp:charge")
			return tr.release(ctx)
		}},
		{Name: "notify", Action: func(ctx context.Context) error {
			l.add("act:notify")
			return tr.acquire(ctx)
		}, Compensate: func(ctx context.Context) error {
			l.add("cmp:notify")
			return tr.release(ctx)
		}},
	}
	s := New(steps...)
	err := s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "charge declined") {
		t.Fatalf("Run error = %v, want wrapped cause", err)
	}
	if s.Status() != StatusCompensated {
		t.Fatalf("status = %v, want Compensated", s.Status())
	}
	want := "act:reserve,act:authorize,act:charge,cmp:authorize,cmp:reserve"
	if got := strings.Join(l.snapshot(), ","); got != want {
		t.Fatalf("event order = %q, want %q", got, want)
	}
	if tr.leaked() != 0 {
		t.Fatalf("leaked %d resources after rollback", tr.leaked())
	}
	recs := s.Records()
	// Failed step: action attempted once, never compensated (nothing executed).
	if recs[2].ActionCalls != 1 || recs[2].CompensateCalls != 0 || recs[2].Compensated {
		t.Fatalf("failed step record = %+v", recs[2])
	}
	// Steps after the failure must never run.
	if recs[3].ActionCalls != 0 || recs[3].CompensateCalls != 0 {
		t.Fatalf("unreached step record = %+v", recs[3])
	}
	if len(s.Uncompensated()) != 0 {
		t.Fatalf("uncompensated = %v, want empty", s.Uncompensated())
	}
	// Journal must show Running -> Compensating -> Compensated.
	trs := s.Transitions()
	if len(trs) != 3 || trs[1].To != StatusCompensating || trs[2].To != StatusCompensated {
		t.Fatalf("transitions = %+v", trs)
	}
}

func TestFailureAtFirstStepNeedsNoCompensation(t *testing.T) {
	tr := newTracker()
	var l log
	s := New(
		Step{Name: "a", Action: func(ctx context.Context) error {
			l.add("act:a")
			return errors.New("immediate failure")
		}, Compensate: func(ctx context.Context) error {
			l.add("cmp:a")
			return tr.release(ctx)
		}},
		Step{Name: "b", Action: func(ctx context.Context) error {
			l.add("act:b")
			return tr.acquire(ctx)
		}},
	)
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if s.Status() != StatusCompensated {
		t.Fatalf("status = %v, want Compensated", s.Status())
	}
	if got := strings.Join(l.snapshot(), ","); got != "act:a" {
		t.Fatalf("events = %q, want only act:a", got)
	}
	if tr.leaked() != 0 {
		t.Fatalf("leaked %d resources", tr.leaked())
	}
}

func TestEmptySagaCompletes(t *testing.T) {
	s := New()
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Status() != StatusCompleted {
		t.Fatalf("status = %v, want Completed", s.Status())
	}
}

func TestNilActionFailsAndCompensates(t *testing.T) {
	tr := newTracker()
	s := New(
		Step{Name: "ok", Action: tr.acquire, Compensate: tr.release},
		Step{Name: "broken"},
	)
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if s.Status() != StatusCompensated {
		t.Fatalf("status = %v, want Compensated", s.Status())
	}
	recs := s.Records()
	if recs[1].ActionErr != ErrNilAction {
		t.Fatalf("ActionErr = %v, want ErrNilAction", recs[1].ActionErr)
	}
	if tr.leaked() != 0 {
		t.Fatalf("leaked %d resources", tr.leaked())
	}
}

func TestCompensationRetriesUntilSuccess(t *testing.T) {
	tr := newTracker()
	var calls int32
	s := New(
		Step{Name: "a", Action: tr.acquire, Compensate: func(ctx context.Context) error {
			n := atomic.AddInt32(&calls, 1)
			if n < 3 {
				return fmt.Errorf("transient %d", n)
			}
			return tr.release(ctx)
		}},
		Step{Name: "b", Action: func(ctx context.Context) error {
			return errors.New("boom")
		}},
	)
	s.MaxCompensateRetries = 3
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if s.Status() != StatusCompensated {
		t.Fatalf("status = %v, want Compensated", s.Status())
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("compensation attempts = %d, want 3", got)
	}
	if tr.leaked() != 0 {
		t.Fatalf("leaked %d resources", tr.leaked())
	}
	if r := s.Records()[0]; r.CompensateCalls != 3 || !r.Compensated {
		t.Fatalf("record = %+v", r)
	}
}

func TestCompensationExhaustionMarksFailed(t *testing.T) {
	tr := newTracker()
	permanent := errors.New("irreversible")
	s := New(
		Step{Name: "a", Action: tr.acquire, Compensate: func(ctx context.Context) error {
			return permanent
		}},
		Step{Name: "b", Action: tr.acquire, Compensate: tr.release},
		Step{Name: "c", Action: func(ctx context.Context) error {
			return errors.New("boom")
		}},
	)
	s.MaxCompensateRetries = 2
	err := s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "compensation incomplete") {
		t.Fatalf("Run error = %v", err)
	}
	if s.Status() != StatusFailed {
		t.Fatalf("status = %v, want Failed", s.Status())
	}
	recs := s.Records()
	if recs[0].CompensateCalls != 2 || recs[0].Compensated || recs[0].CompensateErr != permanent {
		t.Fatalf("record a = %+v", recs[0])
	}
	// Step b must still be compensated even though a failed permanently.
	if !recs[1].Compensated {
		t.Fatalf("record b = %+v, want compensated", recs[1])
	}
	if got := s.Uncompensated(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("uncompensated = %v, want [a]", got)
	}
	// Exactly one resource remains outstanding: the one we could not undo.
	if tr.leaked() != 1 {
		t.Fatalf("outstanding = %d, want exactly 1 (the reported leak)", tr.leaked())
	}
}

func TestConcurrentRunExactlyOnce(t *testing.T) {
	tr := newTracker()
	var actionCalls, compensateCalls int32
	failAt := 2
	steps := make([]Step, 4)
	for i := 0; i < 4; i++ {
		i := i
		steps[i] = Step{
			Name: fmt.Sprintf("s%d", i),
			Action: func(ctx context.Context) error {
				atomic.AddInt32(&actionCalls, 1)
				if i == failAt {
					return errors.New("boom")
				}
				return tr.acquire(ctx)
			},
			Compensate: func(ctx context.Context) error {
				atomic.AddInt32(&compensateCalls, 1)
				return tr.release(ctx)
			},
		}
	}
	s := New(steps...)
	const callers = 32
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for g := 0; g < callers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			errs[g] = s.Run(context.Background())
		}(g)
	}
	wg.Wait()
	if s.Status() != StatusCompensated {
		t.Fatalf("status = %v, want Compensated", s.Status())
	}
	// Exactly-once: failAt actions succeeded, 1 failed attempt.
	if got := atomic.LoadInt32(&actionCalls); got != int32(failAt+1) {
		t.Fatalf("action calls = %d, want %d", got, failAt+1)
	}
	if got := atomic.LoadInt32(&compensateCalls); got != int32(failAt) {
		t.Fatalf("compensation calls = %d, want %d", got, failAt)
	}
	for g, err := range errs {
		if err == nil {
			t.Fatalf("caller %d got nil error, want rollback cause", g)
		}
		if err.Error() != errs[0].Error() {
			t.Fatalf("caller %d error %q differs from caller 0 %q", g, err, errs[0])
		}
	}
	if tr.leaked() != 0 {
		t.Fatalf("leaked %d resources", tr.leaked())
	}
}

func TestRunAfterTerminalIsIdempotent(t *testing.T) {
	var calls int32
	s := New(Step{Name: "a", Action: func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}})
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("action calls = %d, want exactly 1", got)
	}
}

func TestCallerCancellationStillCompensates(t *testing.T) {
	tr := newTracker()
	ctx, cancel := context.WithCancel(context.Background())
	s := New(
		Step{Name: "a", Action: func(ctx context.Context) error {
			if err := tr.acquire(ctx); err != nil {
				return err
			}
			cancel() // caller gives up mid-flight
			return nil
		}, Compensate: func(ctx context.Context) error {
			// Compensation must receive a live context, not the cancelled one.
			if ctx.Err() != nil {
				return fmt.Errorf("compensation got cancelled ctx: %v", ctx.Err())
			}
			return tr.release(ctx)
		}},
		Step{Name: "b", Action: func(ctx context.Context) error {
			return ctx.Err() // observes cancellation
		}},
	)
	err := s.Run(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if s.Status() != StatusCompensated {
		t.Fatalf("status = %v, want Compensated", s.Status())
	}
	if tr.leaked() != 0 {
		t.Fatalf("leaked %d resources after cancellation", tr.leaked())
	}
}

func TestManySagasNoLeakNoGoroutineGrowth(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()
	tr := newTracker()
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			failAt := i % 3 // 0 => success path (failAt beyond steps), else failure
			steps := make([]Step, 3)
			for j := 0; j < 3; j++ {
				j := j
				steps[j] = Step{
					Name: fmt.Sprintf("s%d", j),
					Action: func(ctx context.Context) error {
						if j == failAt {
							return errors.New("boom")
						}
						return tr.acquire(ctx)
					},
					Compensate: tr.release,
				}
			}
			if err := New(steps...).Run(context.Background()); err == nil && failAt < 3 {
				t.Errorf("saga %d: expected rollback error", i)
			}
		}()
	}
	wg.Wait()
	if tr.leaked() != 0 {
		t.Fatalf("leaked %d resources across %d sagas", tr.leaked(), n)
	}
	// Allow the runtime a moment to reap finished goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestStepRecordNames(t *testing.T) {
	s := New(Step{Name: "x", Action: func(ctx context.Context) error { return nil }})
	if err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stepNames(s.Records()); len(got) != 1 || got[0] != "x" {
		t.Fatalf("record names = %v", got)
	}
}
