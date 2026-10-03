package saga

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recorder collects invocation order in a concurrency-safe way.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, s)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// workflow builds n steps named s0..sn-1. failAt >= 0 makes that step's
// Action fail; compFailAt >= 0 makes that step's Compensate fail.
func workflow(rec *recorder, n, failAt, compFailAt int, counts map[string]*int32) []Step {
	steps := make([]Step, n)
	for i := 0; i < n; i++ {
		i := i
		name := fmt.Sprintf("s%d", i)
		steps[i] = Step{
			Name: name,
			Action: func(ctx context.Context) error {
				rec.add("act:" + name)
				if counts != nil {
					atomic.AddInt32(counts["act:"+name], 1)
				}
				if i == failAt {
					return fmt.Errorf("boom-%d", i)
				}
				return nil
			},
			Compensate: func(ctx context.Context) error {
				rec.add("cmp:" + name)
				if counts != nil {
					atomic.AddInt32(counts["cmp:"+name], 1)
				}
				if i == compFailAt {
					return fmt.Errorf("comp-boom-%d", i)
				}
				return nil
			},
		}
	}
	return steps
}

func newCounts(names ...string) map[string]*int32 {
	m := make(map[string]*int32)
	for _, n := range names {
		n := n
		m[n] = new(int32)
	}
	return m
}

func TestCommitSuccess(t *testing.T) {
	rec := &recorder{}
	o := New(workflow(rec, 3, -1, -1, nil)...)
	if err := o.Run(context.Background(), "tx-1"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := rec.snapshot(), []string{"act:s0", "act:s1", "act:s2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls: want %v, got %v", want, got)
	}
	st, ok := o.Status("tx-1")
	if !ok || st != StatusCommitted {
		t.Fatalf("status: %v ok=%v", st, ok)
	}
}

func TestRollbackReverseOrder(t *testing.T) {
	rec := &recorder{}
	o := New(workflow(rec, 4, 2, -1, nil)...)
	err := o.Run(context.Background(), "tx-2")
	if err == nil || !strings.Contains(err.Error(), `step "s2"`) {
		t.Fatalf("want failure at s2, got %v", err)
	}
	// s0,s1 completed -> compensated in reverse; failed s2 is NOT compensated;
	// s3 never ran.
	want := []string{"act:s0", "act:s1", "act:s2", "cmp:s1", "cmp:s0"}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls: want %v, got %v", want, got)
	}
	if st, _ := o.Status("tx-2"); st != StatusRolledBack {
		t.Fatalf("status: %v", st)
	}
}

func TestFailureAtEachStep(t *testing.T) {
	for failAt := 0; failAt < 4; failAt++ {
		rec := &recorder{}
		o := New(workflow(rec, 4, failAt, -1, nil)...)
		txID := fmt.Sprintf("tx-%d", failAt)
		if err := o.Run(context.Background(), txID); err == nil {
			t.Fatalf("failAt=%d: want error", failAt)
		}
		var want []string
		for i := 0; i <= failAt; i++ {
			want = append(want, fmt.Sprintf("act:s%d", i))
		}
		for i := failAt - 1; i >= 0; i-- {
			want = append(want, fmt.Sprintf("cmp:s%d", i))
		}
		if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
			t.Fatalf("failAt=%d: want %v, got %v", failAt, want, got)
		}
		if st, _ := o.Status(txID); st != StatusRolledBack {
			t.Fatalf("failAt=%d: status %v", failAt, st)
		}
	}
}

func TestCompensationFailureStillCompletes(t *testing.T) {
	rec := &recorder{}
	// s1's compensation fails; s0 must still be compensated.
	o := New(workflow(rec, 3, 2, 1, nil)...)
	err := o.Run(context.Background(), "tx-cf")
	if err == nil {
		t.Fatal("want error")
	}
	want := []string{"act:s0", "act:s1", "act:s2", "cmp:s1", "cmp:s0"}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls: want %v, got %v", want, got)
	}
	if st, _ := o.Status("tx-cf"); st != StatusFailed {
		t.Fatalf("status: want FAILED, got %v", st)
	}
	// The rollback failure is visible in the audit log.
	var sawRollbackFailed bool
	for _, e := range o.Events() {
		if e.Kind == EventRollbackFailed {
			sawRollbackFailed = true
		}
	}
	if !sawRollbackFailed {
		t.Fatal("missing EventRollbackFailed in audit log")
	}
}

func TestContextCancellationTriggersRollback(t *testing.T) {
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	steps := workflow(rec, 3, -1, -1, nil)
	orig := steps[1].Action
	steps[1].Action = func(c context.Context) error {
		cancel() // cancel the context after step 1 succeeds
		return orig(c)
	}
	o := New(steps...)
	err := o.Run(ctx, "tx-ctx")
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	want := []string{"act:s0", "act:s1", "cmp:s1", "cmp:s0"}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls: want %v, got %v", want, got)
	}
	if st, _ := o.Status("tx-ctx"); st != StatusRolledBack {
		t.Fatalf("status: %v", st)
	}
}

func TestIdempotentSequentialRerun(t *testing.T) {
	rec := &recorder{}
	counts := newCounts("act:s0", "act:s1", "cmp:s0", "cmp:s1")
	o := New(workflow(rec, 2, -1, -1, counts)...)

	for i := 0; i < 3; i++ {
		if err := o.Run(context.Background(), "tx-idem"); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(counts["act:s0"]); got != 1 {
		t.Fatalf("act:s0 ran %d times, want 1", got)
	}
	if got := atomic.LoadInt32(counts["act:s1"]); got != 1 {
		t.Fatalf("act:s1 ran %d times, want 1", got)
	}

	// Failed transaction: reruns return the stored error without re-executing.
	rec2 := &recorder{}
	counts2 := newCounts("act:s0", "act:s1", "cmp:s0", "cmp:s1")
	o2 := New(workflow(rec2, 2, 1, -1, counts2)...)
	err1 := o2.Run(context.Background(), "tx-fail")
	err2 := o2.Run(context.Background(), "tx-fail")
	if err1 == nil || err2 == nil || err1.Error() != err2.Error() {
		t.Fatalf("rerun errors differ: %v vs %v", err1, err2)
	}
	// s1's action failed, so only s0 is compensated; nothing re-executes.
	wantCounts := map[string]int32{"act:s0": 1, "act:s1": 1, "cmp:s0": 1, "cmp:s1": 0}
	for name, want := range wantCounts {
		if got := atomic.LoadInt32(counts2[name]); got != want {
			t.Fatalf("%s ran %d times, want %d", name, got, want)
		}
	}
}

func TestConcurrentSameTxID(t *testing.T) {
	rec := &recorder{}
	counts := newCounts("act:s0", "act:s1", "act:s2")
	o := New(workflow(rec, 3, -1, -1, counts)...)

	const goroutines = 32
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			errs[g] = o.Run(context.Background(), "tx-shared")
		}(g)
	}
	wg.Wait()
	for g, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", g, err)
		}
	}
	for name, c := range counts {
		if got := atomic.LoadInt32(c); got != 1 {
			t.Fatalf("%s ran %d times under concurrency, want 1", name, got)
		}
	}
	if st, _ := o.Status("tx-shared"); st != StatusCommitted {
		t.Fatalf("status: %v", st)
	}
}

func TestConcurrentDistinctTxIDs(t *testing.T) {
	rec := &recorder{}
	o := New(workflow(rec, 3, -1, -1, nil)...)
	const txs = 64
	var wg sync.WaitGroup
	errCh := make(chan error, txs)
	for i := 0; i < txs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errCh <- o.Run(context.Background(), fmt.Sprintf("tx-%d", i))
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	if got := o.TxCount(); got != txs {
		t.Fatalf("TxCount: want %d, got %d", txs, got)
	}
	// Every transaction committed.
	for i := 0; i < txs; i++ {
		if st, _ := o.Status(fmt.Sprintf("tx-%d", i)); st != StatusCommitted {
			t.Fatalf("tx-%d status: %v", i, st)
		}
	}
}

func TestConcurrentMixedOutcome(t *testing.T) {
	// Committed and rolled-back transactions interleave concurrently.
	recOK, recFail := &recorder{}, &recorder{}
	oOK := New(workflow(recOK, 3, -1, -1, nil)...)
	oFail := New(workflow(recFail, 3, 1, -1, nil)...)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); _ = oOK.Run(context.Background(), fmt.Sprintf("ok-%d", i)) }(i)
		go func(i int) { defer wg.Done(); _ = oFail.Run(context.Background(), fmt.Sprintf("bad-%d", i)) }(i)
	}
	wg.Wait()
	for i := 0; i < 32; i++ {
		if st, _ := oOK.Status(fmt.Sprintf("ok-%d", i)); st != StatusCommitted {
			t.Fatalf("ok-%d: %v", i, st)
		}
		if st, _ := oFail.Status(fmt.Sprintf("bad-%d", i)); st != StatusRolledBack {
			t.Fatalf("bad-%d: %v", i, st)
		}
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	rec := &recorder{}
	o := New(workflow(rec, 4, -1, -1, nil)...)
	oFail := New(workflow(&recorder{}, 4, 2, -1, nil)...)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); _ = o.Run(context.Background(), fmt.Sprintf("t-%d", i)) }(i)
		go func(i int) { defer wg.Done(); _ = oFail.Run(context.Background(), fmt.Sprintf("f-%d", i)) }(i)
	}
	wg.Wait()
	// Allow the scheduler to reap finished goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

func TestEmptyTxID(t *testing.T) {
	o := New(Step{Name: "x", Action: func(context.Context) error { return nil }})
	if err := o.Run(context.Background(), ""); !errors.Is(err, ErrEmptyTransaction) {
		t.Fatalf("want ErrEmptyTransaction, got %v", err)
	}
}

func TestNilCompensateSkipped(t *testing.T) {
	rec := &recorder{}
	steps := []Step{
		{Name: "s0", Action: func(context.Context) error { rec.add("act:s0"); return nil }}, // no Compensate
		{Name: "s1", Action: func(context.Context) error { rec.add("act:s1"); return errors.New("x") }},
	}
	o := New(steps...)
	if err := o.Run(context.Background(), "tx-nil"); err == nil {
		t.Fatal("want error")
	}
	if st, _ := o.Status("tx-nil"); st != StatusRolledBack {
		t.Fatalf("status: %v", st)
	}
}

func TestAuditLogOrder(t *testing.T) {
	rec := &recorder{}
	o := New(workflow(rec, 3, 2, -1, nil)...)
	_ = o.Run(context.Background(), "tx-audit")
	var kinds []EventKind
	for _, e := range o.Events() {
		kinds = append(kinds, e.Kind)
	}
	want := []EventKind{
		EventActionOK, EventActionOK, EventActionFailed,
		EventCompensateOK, EventCompensateOK,
		EventRolledBack,
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event kinds: want %v, got %v", want, kinds)
	}
}

func ExampleOrchestrator_Run() {
	var log []string
	steps := []Step{
		{
			Name:       "debit",
			Action:     func(context.Context) error { log = append(log, "debit ok"); return nil },
			Compensate: func(context.Context) error { log = append(log, "debit refunded"); return nil },
		},
		{
			Name:       "credit",
			Action:     func(context.Context) error { return errors.New("account frozen") },
			Compensate: func(context.Context) error { log = append(log, "credit reversed"); return nil },
		},
	}
	o := New(steps...)
	err := o.Run(context.Background(), "transfer-42")
	st, _ := o.Status("transfer-42")
	fmt.Printf("err=%v\nstatus=%s\nlog=%v\n", err, st, log)
	// Output:
	// err=saga: tx transfer-42 failed at step "credit": account frozen
	// status=ROLLED_BACK
	// log=[debit ok debit refunded]
}
