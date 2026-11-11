package privacy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openLedger(t *testing.T, dir string, budgets map[string]float64) (*Ledger, *Journal) {
	t.Helper()
	j, recs, err := OpenJournal(filepath.Join(dir, "journal.log"))
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	t.Cleanup(func() { j.Close() })
	return NewLedger(j, recs, budgets), j
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Concurrent requests with the same query key share one computation, one
// reservation and one noise draw.
func TestConcurrentSameKeySharesSingleResult(t *testing.T) {
	dir := t.TempDir()
	ledger, _ := openLedger(t, dir, map[string]float64{"g1": 10, "g2": 10})

	var calls atomic.Int32
	compute := func(ctx context.Context, key string) (float64, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond) // widen the concurrency window
		return 42, nil
	}
	svc := NewService(ledger, compute, 1.0, 7)

	q := Query{Key: "k", Groups: []string{"g1", "g2"}, Epsilon: 0.5}
	const n = 8
	values := make([]float64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			values[i], errs[i] = svc.Query(context.Background(), q)
		}()
	}
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("query %d: %v", i, errs[i])
		}
		if values[i] != values[0] {
			t.Fatalf("query %d got %v, want shared value %v", i, values[i], values[0])
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("compute called %d times, want 1", got)
	}
	if got := ledger.Spent("g1"); got != 0.5 {
		t.Fatalf("g1 spent %v, want 0.5 (charged once)", got)
	}
	if got := ledger.Spent("g2"); got != 0.5 {
		t.Fatalf("g2 spent %v, want 0.5 (charged once)", got)
	}
	if got := ledger.Remaining("g1"); got != 9.5 {
		t.Fatalf("g1 remaining %v, want 9.5", got)
	}
}

// Multi-group reservation is atomic: if any group lacks budget, no group's
// budget is touched.
func TestAtomicReservationAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	ledger, _ := openLedger(t, dir, map[string]float64{"g1": 10, "g2": 0.3})

	var calls atomic.Int32
	compute := func(ctx context.Context, key string) (float64, error) {
		calls.Add(1)
		return 1, nil
	}
	svc := NewService(ledger, compute, 1.0, 1)

	_, err := svc.Query(context.Background(), Query{Key: "k", Groups: []string{"g1", "g2"}, Epsilon: 0.5})
	if !errors.Is(err, ErrInsufficientBudget) {
		t.Fatalf("err = %v, want ErrInsufficientBudget", err)
	}
	if got := ledger.Remaining("g1"); got != 10 {
		t.Fatalf("g1 remaining %v, want 10 (untouched)", got)
	}
	if got := ledger.Remaining("g2"); got != 0.3 {
		t.Fatalf("g2 remaining %v, want 0.3", got)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("compute called %d times, want 0", got)
	}
}

// Cancelling before publication releases the reservation: budget is refunded.
func TestCancelBeforePublishRefunds(t *testing.T) {
	dir := t.TempDir()
	ledger, _ := openLedger(t, dir, map[string]float64{"g1": 10})

	compute := func(ctx context.Context, key string) (float64, error) {
		<-ctx.Done() // block until the flight is cancelled
		return 0, ctx.Err()
	}
	svc := NewService(ledger, compute, 1.0, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.Query(ctx, Query{Key: "k", Groups: []string{"g1"}, Epsilon: 0.5})
		done <- err
	}()
	waitFor(t, "reservation to be deducted", func() bool { return ledger.Remaining("g1") == 9.5 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	waitFor(t, "refund", func() bool { return ledger.Remaining("g1") == 10 })
	if got := ledger.Spent("g1"); got != 0 {
		t.Fatalf("g1 spent %v, want 0", got)
	}
}

// Cancelling after publication does not refund: the budget stays spent and the
// result stays cached for later queries.
func TestCancelAfterPublishNoRefund(t *testing.T) {
	dir := t.TempDir()
	ledger, _ := openLedger(t, dir, map[string]float64{"g1": 10})

	compute := func(ctx context.Context, key string) (float64, error) { return 5, nil }
	svc := NewService(ledger, compute, 1.0, 1)

	ctx, cancel := context.WithCancel(context.Background())
	svc.AfterPublish = func() bool {
		cancel() // waiter gives up between publish and response
		return false
	}
	svc.Query(ctx, Query{Key: "k", Groups: []string{"g1"}, Epsilon: 0.5})

	if got := ledger.Remaining("g1"); got != 9.5 {
		t.Fatalf("g1 remaining %v, want 9.5 (published budget not refunded)", got)
	}
	v1, ok := ledger.Result("k")
	if !ok {
		t.Fatal("result not published")
	}
	v2, err := svc.Query(context.Background(), Query{Key: "k", Groups: []string{"g1"}, Epsilon: 0.5})
	if err != nil {
		t.Fatalf("re-query: %v", err)
	}
	if v2 != v1 {
		t.Fatalf("re-query got %v, want cached %v", v2, v1)
	}
	if got := ledger.Spent("g1"); got != 0.5 {
		t.Fatalf("g1 spent %v, want 0.5 (no double charge)", got)
	}
}

// Fault injection: crash between writing the result to the journal and
// returning the response. After recovery the same query key must be served
// idempotently — same value, same noise, budget charged exactly once.
func TestCrashBetweenPublishAndResponseRecoversIdempotently(t *testing.T) {
	dir := t.TempDir()
	budgets := map[string]float64{"g1": 10, "g2": 10}

	var calls atomic.Int32
	compute := func(ctx context.Context, key string) (float64, error) {
		calls.Add(1)
		return 42, nil
	}

	// --- first "process": crashes after publishing, before responding ---
	ledger1, _ := openLedger(t, dir, budgets)
	svc1 := NewService(ledger1, compute, 1.0, 7)
	published := make(chan struct{})
	svc1.AfterPublish = func() bool {
		close(published)
		return true // simulate crash: never respond
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := Query{Key: "k", Groups: []string{"g1", "g2"}, Epsilon: 0.5}
	done := make(chan error, 1)
	go func() {
		_, err := svc1.Query(ctx, q)
		done <- err
	}()
	<-published // result is durable in the journal; "process" now dies
	cancel()    // the in-flight caller is lost with the crashed process
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("crashed caller err = %v, want context.Canceled", err)
	}
	want, ok := ledger1.Result("k")
	if !ok {
		t.Fatal("result not published before crash")
	}

	// --- second "process": recovers from the same journal ---
	ledger2, _ := openLedger(t, dir, budgets)
	svc2 := NewService(ledger2, compute, 1.0, 999) // different seed: noise must still match

	got, err := svc2.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("recovered query: %v", err)
	}
	if got != want {
		t.Fatalf("recovered value %v, want published %v (same noise)", got, want)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("compute called %d times, want 1 (no recomputation)", got)
	}
	for _, g := range []string{"g1", "g2"} {
		if s := ledger2.Spent(g); s != 0.5 {
			t.Fatalf("%s spent %v, want 0.5 (charged exactly once)", g, s)
		}
		if r := ledger2.Remaining(g); r != 9.5 {
			t.Fatalf("%s remaining %v, want 9.5", g, r)
		}
	}

	// A further retry is still idempotent.
	again, err := svc2.Query(context.Background(), q)
	if err != nil || again != want {
		t.Fatalf("retry got %v, %v; want %v", again, err, want)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("compute called %d times after retry, want 1", got)
	}
}

// A crash that tears the last journal line must not corrupt recovery.
func TestRecoveryToleratesTornJournalTail(t *testing.T) {
	dir := t.TempDir()
	ledger1, _ := openLedger(t, dir, map[string]float64{"g1": 10})
	if err := ledger1.Reserve("k", map[string]float64{"g1": 0.5}); err != nil {
		t.Fatal(err)
	}
	if err := ledger1.Settle("k", 3.14); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash mid-write: append a partial record.
	path := filepath.Join(dir, "journal.log")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"reser`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ledger2, _ := openLedger(t, dir, map[string]float64{"g1": 10})
	if v, ok := ledger2.Result("k"); !ok || v != 3.14 {
		t.Fatalf("result = %v, %v; want 3.14, true", v, ok)
	}
	if got := ledger2.Remaining("g1"); got != 9.5 {
		t.Fatalf("g1 remaining %v, want 9.5", got)
	}
}

// Ledger-level: releasing an already-settled key is a no-op.
func TestReleaseAfterSettleIsNoop(t *testing.T) {
	dir := t.TempDir()
	ledger, _ := openLedger(t, dir, map[string]float64{"g1": 10})
	if err := ledger.Reserve("k", map[string]float64{"g1": 0.5}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Settle("k", 1); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Release("k"); err != nil {
		t.Fatal(err)
	}
	if got := ledger.Remaining("g1"); got != 9.5 {
		t.Fatalf("g1 remaining %v, want 9.5 (settled budget not refunded)", got)
	}
	// Settling twice is idempotent too.
	if err := ledger.Settle("k", 2); err != nil {
		t.Fatal(err)
	}
	if v, _ := ledger.Result("k"); v != 1 {
		t.Fatalf("result %v, want 1 (first publish wins)", v)
	}
}
