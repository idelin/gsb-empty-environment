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

func okStep(name string, log *[]string, mu *sync.Mutex) Step {
	return Step{
		Name: name,
		Action: func(ctx context.Context) error {
			mu.Lock()
			*log = append(*log, "do:"+name)
			mu.Unlock()
			return nil
		},
		Compensate: func(ctx context.Context) error {
			mu.Lock()
			*log = append(*log, "undo:"+name)
			mu.Unlock()
			return nil
		},
	}
}

func TestSuccessPath(t *testing.T) {
	c := NewCoordinator()
	var mu sync.Mutex
	var log []string
	steps := []Step{okStep("a", &log, &mu), okStep("b", &log, &mu), okStep("c", &log, &mu)}

	if err := c.Execute(context.Background(), "tx-1", steps); err != nil {
		t.Fatalf("execute: %v", err)
	}
	rec := c.Record("tx-1")
	if rec.Status != StatusCompleted || rec.StepsCompleted != 3 || rec.StepsCompensated != 0 {
		t.Fatalf("bad record: %+v", rec)
	}
	want := []string{"do:a", "do:b", "do:c"}
	if strings.Join(log, ",") != strings.Join(want, ",") {
		t.Fatalf("log = %v, want %v", log, want)
	}
}

func TestReverseCompensationOrder(t *testing.T) {
	c := NewCoordinator()
	var mu sync.Mutex
	var log []string
	boom := errors.New("charge declined")
	steps := []Step{
		okStep("reserve", &log, &mu),
		okStep("charge", &log, &mu),
		{
			Name:   "ship",
			Action: func(ctx context.Context) error { return boom },
			Compensate: func(ctx context.Context) error {
				mu.Lock()
				log = append(log, "undo:ship")
				mu.Unlock()
				return nil
			},
		},
	}

	err := c.Execute(context.Background(), "tx-2", steps)
	if err == nil || !strings.Contains(err.Error(), "charge declined") {
		t.Fatalf("err = %v", err)
	}
	rec := c.Record("tx-2")
	if rec.Status != StatusCompensated {
		t.Fatalf("status = %v, want Compensated", rec.Status)
	}
	if rec.StepsCompleted != 2 || rec.StepsCompensated != 2 {
		t.Fatalf("completed=%d compensated=%d, want 2/2", rec.StepsCompleted, rec.StepsCompensated)
	}
	// ship's action failed, so its compensation must NOT run; rollback is
	// exact reverse of completed steps only.
	want := []string{"do:reserve", "do:charge", "undo:charge", "undo:reserve"}
	if strings.Join(log, ",") != strings.Join(want, ",") {
		t.Fatalf("log = %v, want %v", log, want)
	}
}

func TestFailureOnFirstStepNoCompensation(t *testing.T) {
	c := NewCoordinator()
	var compensated int32
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error { return errors.New("nope") },
			Compensate: func(ctx context.Context) error { atomic.AddInt32(&compensated, 1); return nil }},
		okStepNoLog("b"),
	}
	if err := c.Execute(context.Background(), "tx-3", steps); err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&compensated) != 0 {
		t.Fatal("compensation ran for a step whose action failed")
	}
	if rec := c.Record("tx-3"); rec.Status != StatusCompensated || rec.StepsCompensated != 0 {
		t.Fatalf("bad record: %+v", rec)
	}
}

func okStepNoLog(name string) Step {
	return Step{
		Name:       name,
		Action:     func(ctx context.Context) error { return nil },
		Compensate: func(ctx context.Context) error { return nil },
	}
}

func TestNilCompensationSkipped(t *testing.T) {
	c := NewCoordinator()
	steps := []Step{
		{Name: "noop", Action: func(ctx context.Context) error { return nil }}, // no Compensate
		{Name: "fail", Action: func(ctx context.Context) error { return errors.New("x") }},
	}
	if err := c.Execute(context.Background(), "tx-4", steps); err == nil {
		t.Fatal("expected error")
	}
	if rec := c.Record("tx-4"); rec.Status != StatusCompensated {
		t.Fatalf("status = %v", rec.Status)
	}
}

func TestCompensationFailureMarksFailedButContinues(t *testing.T) {
	c := NewCoordinator()
	var mu sync.Mutex
	var undone []string
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error { return nil },
			Compensate: func(ctx context.Context) error { return errors.New("undo-a broke") }},
		{Name: "b", Action: func(ctx context.Context) error { return nil },
			Compensate: func(ctx context.Context) error {
				mu.Lock()
				undone = append(undone, "b")
				mu.Unlock()
				return nil
			}},
		{Name: "c", Action: func(ctx context.Context) error { return errors.New("trigger") }},
	}
	err := c.Execute(context.Background(), "tx-5", steps)
	if err == nil || !strings.Contains(err.Error(), "rollback incomplete") {
		t.Fatalf("err = %v", err)
	}
	rec := c.Record("tx-5")
	if rec.Status != StatusFailed {
		t.Fatalf("status = %v, want Failed", rec.Status)
	}
	// b's compensation must still run even though a's (later in reverse
	// order... a is compensated last) failed: rollback is best-effort.
	if len(undone) != 1 || undone[0] != "b" {
		t.Fatalf("undone = %v, want [b]", undone)
	}
	if rec.StepsCompensated != 1 {
		t.Fatalf("compensated = %d, want 1 (only b succeeded)", rec.StepsCompensated)
	}
}

func TestIdempotentReplay(t *testing.T) {
	c := NewCoordinator()
	var actions, compensations int32
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error { atomic.AddInt32(&actions, 1); return nil },
			Compensate: func(ctx context.Context) error { atomic.AddInt32(&compensations, 1); return nil }},
		{Name: "b", Action: func(ctx context.Context) error { return errors.New("fail") }},
	}
	err1 := c.Execute(context.Background(), "tx-6", steps)
	err2 := c.Execute(context.Background(), "tx-6", steps)
	if err1 == nil || err2 == nil || err1.Error() != err2.Error() {
		t.Fatalf("replay mismatch: %v vs %v", err1, err2)
	}
	if atomic.LoadInt32(&actions) != 1 || atomic.LoadInt32(&compensations) != 1 {
		t.Fatalf("actions=%d compensations=%d, want 1/1", actions, compensations)
	}
}

func TestConcurrentSameIDExecutesOnce(t *testing.T) {
	c := NewCoordinator()
	var actions int32
	gate := make(chan struct{})
	steps := []Step{{
		Name: "slow",
		Action: func(ctx context.Context) error {
			atomic.AddInt32(&actions, 1)
			<-gate // hold the action so all callers overlap
			return nil
		},
		Compensate: func(ctx context.Context) error { return nil },
	}}

	const callers = 16
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = c.Execute(context.Background(), "tx-7", steps)
		}(i)
	}
	// Let all callers arrive, then release the single execution.
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()

	if n := atomic.LoadInt32(&actions); n != 1 {
		t.Fatalf("action ran %d times, want exactly 1", n)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d got %v", i, err)
		}
	}
	if rec := c.Record("tx-7"); rec.Status != StatusCompleted {
		t.Fatalf("status = %v", rec.Status)
	}
}

func TestConcurrentDistinctIDs(t *testing.T) {
	c := NewCoordinator()
	var total int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("tx-distinct-%d", i)
			steps := []Step{{
				Name:   "work",
				Action: func(ctx context.Context) error { atomic.AddInt32(&total, 1); return nil },
			}}
			if err := c.Execute(context.Background(), id, steps); err != nil {
				t.Errorf("tx %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	if atomic.LoadInt32(&total) != 32 {
		t.Fatalf("total actions = %d, want 32", total)
	}
}

func TestContextCancelTriggersRollback(t *testing.T) {
	c := NewCoordinator()
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var log []string
	steps := []Step{
		okStep("a", &log, &mu),
		{
			Name: "cancel-here",
			Action: func(ctx context.Context) error {
				cancel() // abort after this step succeeds
				return nil
			},
			Compensate: func(ctx context.Context) error {
				mu.Lock()
				log = append(log, "undo:cancel-here")
				mu.Unlock()
				return nil
			},
		},
		okStep("never", &log, &mu),
	}
	err := c.Execute(ctx, "tx-8", steps)
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("err = %v", err)
	}
	want := []string{"do:a", "undo:cancel-here", "undo:a"}
	if strings.Join(log, ",") != strings.Join(want, ",") {
		t.Fatalf("log = %v, want %v", log, want)
	}
	if rec := c.Record("tx-8"); rec.Status != StatusCompensated {
		t.Fatalf("status = %v", rec.Status)
	}
}

func TestNilActionFailsAndRollsBack(t *testing.T) {
	c := NewCoordinator()
	var compensated int32
	steps := []Step{
		{Name: "a", Action: func(ctx context.Context) error { return nil },
			Compensate: func(ctx context.Context) error { atomic.AddInt32(&compensated, 1); return nil }},
		{Name: "broken"}, // nil Action
	}
	if err := c.Execute(context.Background(), "tx-9", steps); err == nil {
		t.Fatal("expected error for nil Action")
	}
	if atomic.LoadInt32(&compensated) != 1 {
		t.Fatal("prior step was not compensated")
	}
}

func TestForget(t *testing.T) {
	c := NewCoordinator()
	if err := c.Execute(context.Background(), "tx-10", nil); err != nil {
		t.Fatal(err)
	}
	if !c.Forget("tx-10") {
		t.Fatal("Forget returned false for terminal tx")
	}
	if c.Record("tx-10") != nil {
		t.Fatal("record still present after Forget")
	}
	if c.Forget("tx-10") {
		t.Fatal("Forget succeeded twice")
	}
	if c.Forget("never-existed") {
		t.Fatal("Forget succeeded for unknown id")
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	c := NewCoordinator()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("leak-%d", i%5) // mix of shared and distinct IDs
			steps := []Step{{
				Name:       "w",
				Action:     func(ctx context.Context) error { return nil },
				Compensate: func(ctx context.Context) error { return nil },
			}}
			_ = c.Execute(context.Background(), id, steps)
		}(i)
	}
	wg.Wait()
	// Allow any final scheduling to settle, then compare.
	for i := 0; i < 20; i++ {
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
		if runtime.NumGoroutine() <= before {
			break
		}
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
	// All five shared IDs executed exactly once each.
	for i := 0; i < 5; i++ {
		rec := c.Record(fmt.Sprintf("leak-%d", i))
		if rec == nil || rec.Status != StatusCompleted {
			t.Fatalf("leak-%d record = %+v", i, rec)
		}
	}
}
