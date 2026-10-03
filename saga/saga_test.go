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

// eventLog is a goroutine-safe ordered log of execute/compensate events.
type eventLog struct {
	mu  sync.Mutex
	evt []string
}

func (l *eventLog) add(e string) {
	l.mu.Lock()
	l.evt = append(l.evt, e)
	l.mu.Unlock()
}

func (l *eventLog) events() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.evt...)
}

func (l *eventLog) joined() string { return strings.Join(l.events(), ",") }

// step builds a Step that logs events and optionally fails/panics.
func step(log *eventLog, name string, fail, panik bool) Step {
	return Step{
		Name: name,
		Execute: func(ctx context.Context) error {
			log.add("exec:" + name)
			if panik {
				panic("boom in " + name)
			}
			if fail {
				return errors.New("execute failed: " + name)
			}
			return nil
		},
		Compensate: func(ctx context.Context) error {
			log.add("comp:" + name)
			return nil
		},
	}
}

func TestSuccessPath(t *testing.T) {
	log := &eventLog{}
	o := New()
	steps := []Step{step(log, "a", false, false), step(log, "b", false, false), step(log, "c", false, false)}
	if err := o.Run(context.Background(), "s1", steps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := log.joined(), "exec:a,exec:b,exec:c"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	rec, _ := o.Record("s1")
	if rec.Status != StatusCompleted {
		t.Fatalf("status = %s, want COMPLETED", rec.Status)
	}
	for _, sr := range rec.Steps {
		if sr.Status != StatusCompleted || sr.Executions != 1 || sr.Compensations != 0 {
			t.Fatalf("step %s: bad record %+v", sr.Name, sr)
		}
	}
	if err := o.AssertConsistent(); err != nil {
		t.Fatal(err)
	}
}

func TestFailureCompensatesInReverseOrder(t *testing.T) {
	log := &eventLog{}
	o := New()
	steps := []Step{
		step(log, "reserve", false, false),
		step(log, "charge", false, false),
		step(log, "notify", true, false), // fails
	}
	err := o.Run(context.Background(), "s2", steps)
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := log.joined(), "exec:reserve,exec:charge,exec:notify,comp:charge,comp:reserve"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	rec, _ := o.Record("s2")
	if rec.Status != StatusCompensated {
		t.Fatalf("status = %s, want COMPENSATED", rec.Status)
	}
	if rec.Steps[0].Status != StatusCompensated || rec.Steps[1].Status != StatusCompensated {
		t.Fatalf("completed steps not compensated: %+v", rec.Steps)
	}
	if rec.Steps[2].Status != StatusFailed {
		t.Fatalf("failed step status = %s", rec.Steps[2].Status)
	}
	if rec.Steps[0].Compensations != 1 || rec.Steps[1].Compensations != 1 {
		t.Fatal("compensation counts wrong")
	}
	if err := o.AssertConsistent(); err != nil {
		t.Fatal(err)
	}
}

func TestFailureAtFirstStepNoCompensation(t *testing.T) {
	log := &eventLog{}
	o := New()
	steps := []Step{step(log, "a", true, false), step(log, "b", false, false)}
	if err := o.Run(context.Background(), "s3", steps); err == nil {
		t.Fatal("expected error")
	}
	if got, want := log.joined(), "exec:a"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	rec, _ := o.Record("s3")
	if rec.Status != StatusCompensated {
		t.Fatalf("status = %s, want COMPENSATED", rec.Status)
	}
}

func TestFailureAtLastStepCompensatesAll(t *testing.T) {
	log := &eventLog{}
	o := New()
	steps := []Step{
		step(log, "a", false, false),
		step(log, "b", false, false),
		step(log, "c", false, false),
		step(log, "d", true, false),
	}
	if err := o.Run(context.Background(), "s4", steps); err == nil {
		t.Fatal("expected error")
	}
	if got, want := log.joined(), "exec:a,exec:b,exec:c,exec:d,comp:c,comp:b,comp:a"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestNilCompensationIsSkipped(t *testing.T) {
	log := &eventLog{}
	o := New()
	steps := []Step{
		{Name: "a", Execute: func(ctx context.Context) error { log.add("exec:a"); return nil }}, // no Compensate
		step(log, "b", true, false),
	}
	if err := o.Run(context.Background(), "s5", steps); err == nil {
		t.Fatal("expected error")
	}
	rec, _ := o.Record("s5")
	if rec.Status != StatusCompensated {
		t.Fatalf("status = %s", rec.Status)
	}
	if rec.Steps[0].Status != StatusCompensated {
		t.Fatalf("nil-compensation step status = %s, want COMPENSATED", rec.Steps[0].Status)
	}
}

func TestPanicInStepTriggersCompensation(t *testing.T) {
	log := &eventLog{}
	o := New()
	steps := []Step{
		step(log, "a", false, false),
		step(log, "b", false, true), // panics
	}
	err := o.Run(context.Background(), "s6", steps)
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic error, got %v", err)
	}
	if got, want := log.joined(), "exec:a,exec:b,comp:a"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	rec, _ := o.Record("s6")
	if rec.Status != StatusCompensated {
		t.Fatalf("status = %s", rec.Status)
	}
}

func TestPanicInCompensationMarksFailed(t *testing.T) {
	o := New()
	steps := []Step{
		{
			Name:       "a",
			Execute:    func(ctx context.Context) error { return nil },
			Compensate: func(ctx context.Context) error { panic("compensation boom") },
		},
		step(&eventLog{}, "b", true, false),
	}
	err := o.Run(context.Background(), "s7", steps)
	if err == nil || !strings.Contains(err.Error(), "compensation") {
		t.Fatalf("expected compensation failure, got %v", err)
	}
	rec, _ := o.Record("s7")
	if rec.Status != StatusFailed {
		t.Fatalf("status = %s, want FAILED", rec.Status)
	}
	if rec.Steps[0].Status != StatusFailed {
		t.Fatalf("step a status = %s, want FAILED", rec.Steps[0].Status)
	}
	// Even a FAILED saga is terminal: no leak.
	if err := o.AssertConsistent(); err != nil {
		t.Fatal(err)
	}
}

func TestIdempotentReplayAfterSuccess(t *testing.T) {
	log := &eventLog{}
	o := New()
	steps := []Step{step(log, "a", false, false)}
	if err := o.Run(context.Background(), "s8", steps); err != nil {
		t.Fatal(err)
	}
	if err := o.Run(context.Background(), "s8", steps); err != nil {
		t.Fatal(err)
	}
	if got, want := log.joined(), "exec:a"; got != want {
		t.Fatalf("replay re-executed steps: %q", got)
	}
}

func TestIdempotentReplayAfterCompensation(t *testing.T) {
	log := &eventLog{}
	o := New()
	steps := []Step{step(log, "a", false, false), step(log, "b", true, false)}
	err1 := o.Run(context.Background(), "s9", steps)
	err2 := o.Run(context.Background(), "s9", steps)
	if err1 == nil || err2 == nil {
		t.Fatal("expected errors on both runs")
	}
	if err1.Error() != err2.Error() {
		t.Fatalf("replay returned different error: %q vs %q", err1, err2)
	}
	if got, want := log.joined(), "exec:a,exec:b,comp:a"; got != want {
		t.Fatalf("replay re-executed: %q", got)
	}
	rec, _ := o.Record("s9")
	if rec.Steps[0].Executions != 1 || rec.Steps[0].Compensations != 1 {
		t.Fatal("idempotency counters violated")
	}
}

func TestConcurrentSameSagaIDExactlyOnce(t *testing.T) {
	const goroutines = 64
	var execCount, compCount int64
	o := New()
	steps := []Step{
		{
			Name:       "a",
			Execute:    func(ctx context.Context) error { atomic.AddInt64(&execCount, 1); return nil },
			Compensate: func(ctx context.Context) error { atomic.AddInt64(&compCount, 1); return nil },
		},
		{
			Name:    "b",
			Execute: func(ctx context.Context) error { atomic.AddInt64(&execCount, 1); return errors.New("fail") },
		},
	}
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = o.Run(context.Background(), "shared", steps)
		}(i)
	}
	wg.Wait()
	if execCount != 2 {
		t.Fatalf("exec count = %d, want exactly 2 (a and b once)", execCount)
	}
	if compCount != 1 {
		t.Fatalf("comp count = %d, want exactly 1", compCount)
	}
	for i, err := range errs {
		if err == nil {
			t.Fatalf("goroutine %d got nil error", i)
		}
		if err.Error() != errs[0].Error() {
			t.Fatalf("inconsistent errors: %q vs %q", err, errs[0])
		}
	}
	if err := o.AssertConsistent(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentSuccessExactlyOnce(t *testing.T) {
	const goroutines = 50
	var execCount int64
	o := New()
	steps := []Step{{
		Name:    "a",
		Execute: func(ctx context.Context) error { atomic.AddInt64(&execCount, 1); return nil },
	}}
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := o.Run(context.Background(), "ok", steps); err != nil {
				t.Errorf("Run: %v", err)
			}
		}()
	}
	wg.Wait()
	if execCount != 1 {
		t.Fatalf("exec count = %d, want exactly 1", execCount)
	}
}

func TestConcurrentManySagasNoLeak(t *testing.T) {
	const sagas = 128
	var execTotal, compTotal int64
	o := New()
	base := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for i := 0; i < sagas; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fail := i%2 == 1
			id := fmt.Sprintf("saga-%d", i)
			steps := []Step{
				{
					Name:       "work",
					Execute:    func(ctx context.Context) error { atomic.AddInt64(&execTotal, 1); return nil },
					Compensate: func(ctx context.Context) error { atomic.AddInt64(&compTotal, 1); return nil },
				},
				{
					Name: "maybe-fail",
					Execute: func(ctx context.Context) error {
						atomic.AddInt64(&execTotal, 1)
						if fail {
							return errors.New("boom")
						}
						return nil
					},
				},
			}
			err := o.Run(context.Background(), id, steps)
			if fail && err == nil {
				t.Errorf("saga %d: expected error", i)
			}
			if !fail && err != nil {
				t.Errorf("saga %d: unexpected error %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if want := int64(sagas * 2); execTotal != want {
		t.Fatalf("exec total = %d, want %d", execTotal, want)
	}
	if want := int64(sagas / 2); compTotal != want {
		t.Fatalf("comp total = %d, want %d", compTotal, want)
	}
	if err := o.AssertConsistent(); err != nil {
		t.Fatal(err)
	}
	// Strict leak assertion: the orchestrator spawns no goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > base {
		t.Fatalf("goroutine leak: before=%d after=%d", base, got)
	}
}

func TestContextCancellationCompensates(t *testing.T) {
	log := &eventLog{}
	o := New()
	ctx, cancel := context.WithCancel(context.Background())
	steps := []Step{
		step(log, "a", false, false),
		{
			Name: "cancel-trigger",
			Execute: func(ctx context.Context) error {
				log.add("exec:trigger")
				cancel() // cancel the context mid-saga
				return nil
			},
			Compensate: func(ctx context.Context) error { log.add("comp:trigger"); return nil },
		},
		step(log, "never", false, false),
	}
	err := o.Run(ctx, "s10", steps)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if got, want := log.joined(), "exec:a,exec:trigger,comp:trigger,comp:a"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	rec, _ := o.Record("s10")
	if rec.Status != StatusCompensated {
		t.Fatalf("status = %s, want COMPENSATED", rec.Status)
	}
}

func TestPreCancelledContext(t *testing.T) {
	log := &eventLog{}
	o := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	steps := []Step{step(log, "a", false, false)}
	if err := o.Run(ctx, "s11", steps); err == nil {
		t.Fatal("expected error")
	}
	if got := log.joined(); got != "" {
		t.Fatalf("no step should have executed, got %q", got)
	}
	rec, _ := o.Record("s11")
	if rec.Status != StatusCompensated {
		t.Fatalf("status = %s, want COMPENSATED", rec.Status)
	}
}

func TestStoreAssertConsistentDetectsLeak(t *testing.T) {
	store := NewStore()
	st := store.loadOrCreate("leaky", []Step{{Name: "a"}})
	st.rec.Status = StatusRunning // simulate a stuck saga
	if err := store.AssertConsistent(); err == nil {
		t.Fatal("expected leak detection")
	}
	st.rec.Status = StatusCompleted
	st.rec.Steps[0].Status = StatusRunning // stuck step
	if err := store.AssertConsistent(); err == nil {
		t.Fatal("expected step leak detection")
	}
}

func TestEmptyAndInvalidInput(t *testing.T) {
	o := New()
	if err := o.Run(context.Background(), "", []Step{step(&eventLog{}, "a", false, false)}); err == nil {
		t.Fatal("expected error for empty saga ID")
	}
	if err := o.Run(context.Background(), "x", nil); err == nil {
		t.Fatal("expected error for no steps")
	}
}

func TestRecordSnapshotIsolation(t *testing.T) {
	o := New()
	steps := []Step{step(&eventLog{}, "a", false, false)}
	if err := o.Run(context.Background(), "s12", steps); err != nil {
		t.Fatal(err)
	}
	rec, ok := o.Record("s12")
	if !ok {
		t.Fatal("record not found")
	}
	rec.Steps[0].Status = StatusFailed // mutate snapshot
	rec2, _ := o.Record("s12")
	if rec2.Steps[0].Status != StatusCompleted {
		t.Fatal("snapshot mutation leaked into store")
	}
	if _, ok := o.Record("nonexistent"); ok {
		t.Fatal("unexpected record")
	}
}
