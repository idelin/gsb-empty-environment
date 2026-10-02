package saga

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// ledger is an in-memory bank used to assert exact effects and idempotency.
type ledger struct {
	mu       sync.Mutex
	balances map[string]int
	calls    map[string]int // invocation counts per operation name
	order    []string       // global execution order trace
}

func newLedger() *ledger {
	return &ledger{balances: map[string]int{"A": 100, "B": 0}, calls: make(map[string]int)}
}

func (l *ledger) record(name string) {
	l.calls[name]++
	l.order = append(l.order, name)
}

func (l *ledger) transferOp(name, from, to string, amt int, fail *int) func(context.Context) error {
	return func(context.Context) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.record(name)
		if *fail > 0 {
			*fail = *fail - 1
			return fmt.Errorf("injected failure in %s", name)
		}
		l.balances[from] -= amt
		l.balances[to] += amt
		return nil
	}
}

func (l *ledger) balance(acct string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.balances[acct]
}

func (l *ledger) callCount(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls[name]
}

func (l *ledger) trace() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.order))
	copy(out, l.order)
	return out
}

func newOrch(store *Store) *Orchestrator {
	return NewOrchestrator(store, WithRetries(3), WithBackoff(time.Millisecond))
}

// transferSaga builds a 3-step saga: debit A, credit B, notify.
func transferSaga(l *ledger, failDebit, failCredit, failCompDebit *int) []Step {
	return []Step{
		{
			Name:       "debit",
			Action:     l.transferOp("debit", "A", "tmp", 100, failDebit),
			Compensate: l.transferOp("compensate-debit", "tmp", "A", 100, failCompDebit),
		},
		{
			Name:       "credit",
			Action:     l.transferOp("credit", "tmp", "B", 100, failCredit),
			Compensate: l.transferOp("compensate-credit", "B", "tmp", 100, new(int)),
		},
		{
			Name:   "notify",
			Action: func(context.Context) error { l.mu.Lock(); defer l.mu.Unlock(); l.record("notify"); return nil },
		},
	}
}

func TestSagaSuccessPath(t *testing.T) {
	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)

	if err := o.Run(context.Background(), "s1", transferSaga(l, zero, zero, zero)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := store.Status("s1"); got != StatusCompleted {
		t.Fatalf("status=%s, want COMPLETED", got)
	}
	if l.balance("A") != 0 || l.balance("B") != 100 {
		t.Fatalf("balances A=%d B=%d", l.balance("A"), l.balance("B"))
	}
	want := []Status{StatusRunning, StatusCompleted}
	got := store.History("s1")
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("history=%v, want %v", got, want)
	}
}

func TestSagaRollbackReverseOrder(t *testing.T) {
	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)
	failCredit := new(int)
	*failCredit = 100 // credit always fails

	err := o.Run(context.Background(), "s2", transferSaga(l, zero, failCredit, zero))
	if err == nil {
		t.Fatal("expected rollback error")
	}
	if got := store.Status("s2"); got != StatusCompensated {
		t.Fatalf("status=%s, want COMPENSATED", got)
	}
	// Eventual consistency: balances restored.
	if l.balance("A") != 100 || l.balance("B") != 0 {
		t.Fatalf("balances A=%d B=%d, want restored", l.balance("A"), l.balance("B"))
	}
	// Compensation ran in exact reverse order after the failing action.
	tr := l.trace()
	wantTail := []string{"debit", "credit", "credit", "credit", "compensate-debit"}
	if fmt.Sprint(tr) != fmt.Sprint(wantTail) {
		t.Fatalf("trace=%v, want %v", tr, wantTail)
	}
	wantHist := []Status{StatusRunning, StatusCompensating, StatusCompensated}
	if h := store.History("s2"); fmt.Sprint(h) != fmt.Sprint(wantHist) {
		t.Fatalf("history=%v, want %v", h, wantHist)
	}
}

func TestSagaActionRetryThenSuccess(t *testing.T) {
	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)
	failCredit := new(int)
	*failCredit = 2 // fails twice, succeeds on the 3rd attempt

	if err := o.Run(context.Background(), "s3", transferSaga(l, zero, failCredit, zero)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := store.Status("s3"); got != StatusCompleted {
		t.Fatalf("status=%s, want COMPLETED", got)
	}
	if l.callCount("credit") != 3 {
		t.Fatalf("credit attempts=%d, want 3", l.callCount("credit"))
	}
	if l.balance("B") != 100 {
		t.Fatalf("B=%d, want 100", l.balance("B"))
	}
}

func TestSagaCompensationRetryThenSuccess(t *testing.T) {
	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)
	failCredit := new(int)
	*failCredit = 100
	failComp := new(int)
	*failComp = 2 // compensation fails twice, then succeeds

	err := o.Run(context.Background(), "s4", transferSaga(l, zero, failCredit, failComp))
	if err == nil {
		t.Fatal("expected rollback error")
	}
	if got := store.Status("s4"); got != StatusCompensated {
		t.Fatalf("status=%s, want COMPENSATED", got)
	}
	if l.callCount("compensate-debit") != 3 {
		t.Fatalf("compensation attempts=%d, want 3", l.callCount("compensate-debit"))
	}
	if l.balance("A") != 100 {
		t.Fatalf("A=%d, want restored 100", l.balance("A"))
	}
}

func TestSagaCompensationExhausted(t *testing.T) {
	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)
	failCredit := new(int)
	*failCredit = 100
	failComp := new(int)
	*failComp = 100 // compensation never succeeds

	err := o.Run(context.Background(), "s5", transferSaga(l, zero, failCredit, failComp))
	if !errors.Is(err, ErrCompensationFailed) {
		t.Fatalf("err=%v, want ErrCompensationFailed", err)
	}
	if got := store.Status("s5"); got != StatusFailed {
		t.Fatalf("status=%s, want FAILED", got)
	}
	if l.callCount("compensate-debit") != 3 {
		t.Fatalf("compensation attempts=%d, want retry budget 3", l.callCount("compensate-debit"))
	}
}

func TestSagaIdempotentRerun(t *testing.T) {
	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)
	steps := transferSaga(l, zero, zero, zero)

	if err := o.Run(context.Background(), "s6", steps); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if err := o.Run(context.Background(), "s6", steps); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	for _, name := range []string{"debit", "credit", "notify"} {
		if c := l.callCount(name); c != 1 {
			t.Fatalf("%s executed %d times, want exactly 1 (idempotent)", name, c)
		}
	}
	if l.balance("A") != 0 || l.balance("B") != 100 {
		t.Fatalf("double-apply detected: A=%d B=%d", l.balance("A"), l.balance("B"))
	}
}

// TestSagaResumeAfterFailure: a saga fails midway; after the transient fault
// is fixed, re-running the same saga ID resumes without re-executing the
// already-completed steps.
func TestSagaResumeAfterFailure(t *testing.T) {
	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)
	failCredit := new(int)
	*failCredit = 100

	steps := transferSaga(l, zero, failCredit, zero)
	if err := o.Run(context.Background(), "s7", steps); err == nil {
		t.Fatal("first run should fail")
	}
	if got := store.Status("s7"); got != StatusCompensated {
		t.Fatalf("status=%s, want COMPENSATED", got)
	}
	// debit was compensated; a compensated saga is terminal and stays so.
	if err := o.Run(context.Background(), "s7", steps); err != nil {
		t.Fatalf("rerun of compensated saga: %v", err)
	}
	if c := l.callCount("debit"); c != 1 {
		t.Fatalf("debit ran %d times, want 1", c)
	}
	if c := l.callCount("compensate-debit"); c != 1 {
		t.Fatalf("compensate-debit ran %d times, want 1 (idempotent)", c)
	}
}

func TestSagaContextCancelRollsBack(t *testing.T) {
	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)

	ctx, cancel := context.WithCancel(context.Background())
	steps := transferSaga(l, zero, zero, zero)
	// Cancel after the first step completes.
	orig := steps[0].Action
	steps[0].Action = func(ctx context.Context) error {
		err := orig(ctx)
		cancel()
		return err
	}
	if err := o.Run(ctx, "s8", steps); err == nil {
		t.Fatal("expected cancellation error")
	}
	if got := store.Status("s8"); got != StatusCompensated {
		t.Fatalf("status=%s, want COMPENSATED", got)
	}
	if l.balance("A") != 100 {
		t.Fatalf("A=%d, want restored 100", l.balance("A"))
	}
}

func TestSagaConcurrentSameID(t *testing.T) {
	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)

	const g = 16
	var wg sync.WaitGroup
	errs := make(chan error, g)
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := o.Run(context.Background(), "shared", transferSaga(l, zero, zero, zero)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for _, name := range []string{"debit", "credit", "notify"} {
		if c := l.callCount(name); c != 1 {
			t.Fatalf("%s executed %d times under concurrency, want exactly 1", name, c)
		}
	}
	if l.balance("A") != 0 || l.balance("B") != 100 {
		t.Fatalf("A=%d B=%d", l.balance("A"), l.balance("B"))
	}
}

func TestSagaConcurrentDistinctIDs(t *testing.T) {
	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)

	const g = 40
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			failCredit := new(int)
			if i%2 == 1 {
				*failCredit = 100 // half of the sagas fail and roll back
			}
			_ = o.Run(context.Background(), fmt.Sprintf("saga-%d", i), transferSaga(l, zero, failCredit, zero))
		}(i)
	}
	wg.Wait()

	// Invariant: money is conserved; tmp account always returns to zero.
	if got := l.balance("tmp"); got != 0 {
		t.Fatalf("tmp balance=%d, want 0 (compensation leak)", got)
	}
	if total := l.balance("A") + l.balance("B"); total != 100 {
		t.Fatalf("total=%d, want conserved 100", total)
	}
	for i := 0; i < g; i++ {
		st := store.Status(fmt.Sprintf("saga-%d", i))
		if i%2 == 0 && st != StatusCompleted {
			t.Fatalf("saga-%d status=%s, want COMPLETED", i, st)
		}
		if i%2 == 1 && st != StatusCompensated {
			t.Fatalf("saga-%d status=%s, want COMPENSATED", i, st)
		}
	}
}

func TestSagaNoGoroutineLeak(t *testing.T) {
	settle := func() int { time.Sleep(50 * time.Millisecond); return runtime.NumGoroutine() }
	before := settle()

	store := NewStore()
	o := newOrch(store)
	l := newLedger()
	zero := new(int)
	fail := new(int)
	*fail = 100
	for i := 0; i < 10; i++ {
		_ = o.Run(context.Background(), fmt.Sprintf("ok-%d", i), transferSaga(l, zero, zero, zero))
		_ = o.Run(context.Background(), fmt.Sprintf("bad-%d", i), transferSaga(l, zero, fail, zero))
	}
	if after := settle(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
