package netsec_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.org/netsec/q0351"
)

func request(id, purpose, input string) netsec.Request {
	return netsec.Request{
		RequestID: id,
		QueryID:   "query-1",
		Field:     "secret-index",
		Purpose:   purpose,
		Input:     input,
	}
}

func TestBudgetIsReservedBeforeComparatorRuns(t *testing.T) {
	var gate *netsec.Gate[bool]
	calls := 0

	gate = netsec.NewGate(func(context.Context, netsec.Request) (bool, error) {
		calls++
		if got := gate.Remaining(); got != 0 {
			return false, fmt.Errorf("budget was not reserved first: %d", got)
		}
		return true, nil
	}, 1)

	got, err := gate.Compare(context.Background(), request("req-1", "lookup", "alpha"))
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !got {
		t.Fatal("Compare returned false, want true")
	}
	if calls != 1 {
		t.Fatalf("comparator calls = %d, want 1", calls)
	}
	if got := gate.Remaining(); got != 0 {
		t.Fatalf("Remaining = %d, want 0", got)
	}

	_, err = gate.Compare(context.Background(), request("req-2", "lookup", "beta"))
	if !errors.Is(err, netsec.ErrBudgetExhausted) {
		t.Fatalf("second Compare error = %v, want ErrBudgetExhausted", err)
	}
	if calls != 1 {
		t.Fatalf("comparator calls after exhaustion = %d, want 1", calls)
	}
}

func TestReplayBindsValueAndErrorToRequestID(t *testing.T) {
	t.Run("value", func(t *testing.T) {
		var calls atomic.Int64
		gate := netsec.NewGate(func(context.Context, netsec.Request) (string, error) {
			calls.Add(1)
			return "matched", nil
		}, 1)

		req := request("same-id", "lookup", "probe")
		first, err := gate.Compare(context.Background(), req)
		if err != nil {
			t.Fatalf("first Compare: %v", err)
		}
		second, err := gate.Compare(context.Background(), req)
		if err != nil {
			t.Fatalf("replay Compare: %v", err)
		}
		if first != "matched" || second != first {
			t.Fatalf("results = %q, %q; want matched", first, second)
		}
		if calls.Load() != 1 {
			t.Fatalf("comparator calls = %d, want 1", calls.Load())
		}
		if got := gate.Remaining(); got != 0 {
			t.Fatalf("Remaining = %d, want 0", got)
		}
	})

	t.Run("error", func(t *testing.T) {
		probeErr := errors.New("probe rejected")
		var calls atomic.Int64
		gate := netsec.NewGate(func(context.Context, netsec.Request) (bool, error) {
			calls.Add(1)
			return false, probeErr
		}, 1)

		req := request("failed-id", "lookup", "probe")
		_, firstErr := gate.Compare(context.Background(), req)
		_, secondErr := gate.Compare(context.Background(), req)
		if !errors.Is(firstErr, probeErr) || !errors.Is(secondErr, probeErr) {
			t.Fatalf("errors = %v, %v; want probe error", firstErr, secondErr)
		}
		if calls.Load() != 1 {
			t.Fatalf("comparator calls = %d, want 1", calls.Load())
		}
		if got := gate.Remaining(); got != 0 {
			t.Fatalf("Remaining = %d, want 0", got)
		}
	})
}

func TestSameRequestIDDifferentOperationIsRejected(t *testing.T) {
	var calls atomic.Int64
	gate := netsec.NewGate(func(context.Context, netsec.Request) (bool, error) {
		calls.Add(1)
		return true, nil
	}, 4)

	original := request("fixed-id", "lookup", "alpha")
	if _, err := gate.Compare(context.Background(), original); err != nil {
		t.Fatalf("first Compare: %v", err)
	}

	conflicts := []netsec.Request{
		original,
		{RequestID: "fixed-id", QueryID: "query-2", Field: "secret-index", Purpose: "lookup", Input: "alpha"},
		{RequestID: "fixed-id", QueryID: "query-1", Field: "other-index", Purpose: "lookup", Input: "alpha"},
		{RequestID: "fixed-id", QueryID: "query-1", Field: "secret-index", Purpose: "filter", Input: "alpha"},
		{RequestID: "fixed-id", QueryID: "query-1", Field: "secret-index", Purpose: "lookup", Input: "beta"},
	}

	for i, req := range conflicts {
		_, err := gate.Compare(context.Background(), req)
		if i == 0 {
			if err != nil {
				t.Fatalf("exact replay: %v", err)
			}
			continue
		}
		if !errors.Is(err, netsec.ErrRequestConflict) {
			t.Fatalf("conflict %d error = %v, want ErrRequestConflict", i, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("comparator calls = %d, want 1", calls.Load())
	}
	if got := gate.Remaining(); got != 3 {
		t.Fatalf("Remaining = %d, want 3", got)
	}
}

func TestConflictIsCheckedBeforeBudgetExhaustion(t *testing.T) {
	gate := netsec.NewGate(func(context.Context, netsec.Request) (bool, error) {
		return true, nil
	}, 1)

	original := request("fixed-id", "lookup", "alpha")
	if _, err := gate.Compare(context.Background(), original); err != nil {
		t.Fatalf("first Compare: %v", err)
	}
	if got := gate.Remaining(); got != 0 {
		t.Fatalf("Remaining = %d, want 0", got)
	}

	conflicting := request("fixed-id", "filter", "alpha")
	if _, err := gate.Compare(context.Background(), conflicting); !errors.Is(err, netsec.ErrRequestConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrRequestConflict", err)
	}
}

func TestFailedComparisonsDoNotRefundBudget(t *testing.T) {
	probeErr := errors.New("comparison failed")
	var calls atomic.Int64
	gate := netsec.NewGate(func(context.Context, netsec.Request) (int, error) {
		calls.Add(1)
		return 0, probeErr
	}, 3)

	for i := 0; i < 5; i++ {
		_, err := gate.Compare(context.Background(), request(fmt.Sprintf("req-%d", i), "lookup", fmt.Sprintf("probe-%d", i)))
		if i < 3 {
			if !errors.Is(err, probeErr) {
				t.Fatalf("attempt %d error = %v, want probe error", i, err)
			}
		} else if !errors.Is(err, netsec.ErrBudgetExhausted) {
			t.Fatalf("attempt %d error = %v, want ErrBudgetExhausted", i, err)
		}
	}

	if calls.Load() != 3 {
		t.Fatalf("comparator calls = %d, want 3", calls.Load())
	}
	if got := gate.Used(); got != 3 {
		t.Fatalf("Used = %d, want 3", got)
	}
	if got := gate.Remaining(); got != 0 {
		t.Fatalf("Remaining = %d, want 0", got)
	}

	_, replayErr := gate.Compare(context.Background(), request("req-0", "lookup", "probe-0"))
	if !errors.Is(replayErr, probeErr) {
		t.Fatalf("replay error = %v, want cached probe error", replayErr)
	}
	if calls.Load() != 3 {
		t.Fatalf("comparator calls after replay = %d, want 3", calls.Load())
	}
}

func TestComparatorPanicConsumesAndBindsReservation(t *testing.T) {
	var calls atomic.Int64
	gate := netsec.NewGate(func(context.Context, netsec.Request) (bool, error) {
		calls.Add(1)
		panic("index backend exploded")
	}, 1)

	req := request("panic-id", "lookup", "probe")
	_, firstErr := gate.Compare(context.Background(), req)
	_, secondErr := gate.Compare(context.Background(), req)

	if !errors.Is(firstErr, netsec.ErrComparatorPanic) {
		t.Fatalf("first error = %v, want ErrComparatorPanic", firstErr)
	}
	if firstErr.Error() != secondErr.Error() {
		t.Fatalf("replayed error = %v, want %v", secondErr, firstErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("comparator calls = %d, want 1", calls.Load())
	}
	if got := gate.Remaining(); got != 0 {
		t.Fatalf("Remaining = %d, want 0", got)
	}

	_, err := gate.Compare(context.Background(), request("other-id", "lookup", "probe"))
	if !errors.Is(err, netsec.ErrBudgetExhausted) {
		t.Fatalf("new request error = %v, want ErrBudgetExhausted", err)
	}
}

func TestConcurrentUniqueFailuresCannotExceedBudget(t *testing.T) {
	const (
		budget   = 8
		attempts = 64
	)

	probeErr := errors.New("comparison failed")
	var calls atomic.Int64
	gate := netsec.NewGate(func(context.Context, netsec.Request) (int, error) {
		calls.Add(1)
		time.Sleep(time.Millisecond)
		return 0, probeErr
	}, budget)

	errs := make([]error, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = gate.Compare(
				context.Background(),
				request(fmt.Sprintf("concurrent-%d", i), "lookup", fmt.Sprintf("probe-%d", i)),
			)
		}(i)
	}
	wg.Wait()

	if calls.Load() != budget {
		t.Fatalf("comparator calls = %d, want %d", calls.Load(), budget)
	}

	var failed, exhausted int
	for _, err := range errs {
		switch {
		case errors.Is(err, probeErr):
			failed++
		case errors.Is(err, netsec.ErrBudgetExhausted):
			exhausted++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if failed != budget || exhausted != attempts-budget {
		t.Fatalf("failed/exhausted = %d/%d, want %d/%d", failed, exhausted, budget, attempts-budget)
	}
	if got := gate.Remaining(); got != 0 {
		t.Fatalf("Remaining = %d, want 0", got)
	}
}

func TestConcurrentDuplicateRequestsShareOneComparison(t *testing.T) {
	const (
		budget  = 5
		callers = 32
	)

	var calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once

	gate := netsec.NewGate(func(_ context.Context, req netsec.Request) (string, error) {
		calls.Add(1)
		startOnce.Do(func() { close(started) })
		<-release
		return req.Input.(string), nil
	}, budget)

	req := request("shared-id", "lookup", "probe")
	values := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			values[i], errs[i] = gate.Compare(context.Background(), req)
		}(i)
	}

	<-started
	close(release)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("comparator calls = %d, want 1", calls.Load())
	}
	for i := range values {
		if errs[i] != nil {
			t.Fatalf("caller %d error: %v", i, errs[i])
		}
		if values[i] != "probe" {
			t.Fatalf("caller %d value = %q, want probe", i, values[i])
		}
	}
	if got := gate.Remaining(); got != budget-1 {
		t.Fatalf("Remaining = %d, want %d", got, budget-1)
	}
}

func TestConcurrentSameIDDifferentOperationsHaveOneWinner(t *testing.T) {
	const callers = 16

	var calls atomic.Int64
	gate := netsec.NewGate(func(_ context.Context, req netsec.Request) (string, error) {
		calls.Add(1)
		time.Sleep(2 * time.Millisecond)
		return req.Purpose, nil
	}, callers)

	errs := make([]error, callers)
	values := make([]string, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			req := request("raced-id", fmt.Sprintf("purpose-%d", i), "probe")
			values[i], errs[i] = gate.Compare(context.Background(), req)
		}(i)
	}
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("comparator calls = %d, want 1", calls.Load())
	}

	var winners, conflicts int
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
			if values[i] != fmt.Sprintf("purpose-%d", i) {
				t.Fatalf("winner %d value = %q", i, values[i])
			}
		case errors.Is(err, netsec.ErrRequestConflict):
			conflicts++
		default:
			t.Fatalf("caller %d error: %v", i, err)
		}
	}
	if winners != 1 || conflicts != callers-1 {
		t.Fatalf("winners/conflicts = %d/%d, want 1/%d", winners, conflicts, callers-1)
	}
	if got := gate.Remaining(); got != callers-1 {
		t.Fatalf("Remaining = %d, want %d", got, callers-1)
	}
}

func TestReentrantComparatorCanUseNewRequestID(t *testing.T) {
	var gate *netsec.Gate[int]
	var calls atomic.Int64

	gate = netsec.NewGate(func(ctx context.Context, req netsec.Request) (int, error) {
		calls.Add(1)
		if req.Input == "outer" {
			inner, err := gate.Compare(ctx, netsec.Request{
				RequestID: "inner-id",
				QueryID:   req.QueryID,
				Field:     req.Field,
				Purpose:   req.Purpose,
				Input:     "inner",
			})
			if err != nil {
				return 0, err
			}
			return inner + 1, nil
		}
		return 41, nil
	}, 3)

	type outcome struct {
		value int
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		value, err := gate.Compare(context.Background(), request("outer-id", "lookup", "outer"))
		result <- outcome{value: value, err: err}
	}()

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("Compare: %v", got.err)
		}
		if got.value != 42 {
			t.Fatalf("value = %d, want 42", got.value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reentrant comparator deadlocked")
	}

	if calls.Load() != 2 {
		t.Fatalf("comparator calls = %d, want 2", calls.Load())
	}
	if got := gate.Used(); got != 2 {
		t.Fatalf("Used = %d, want 2", got)
	}
	if got := gate.Remaining(); got != 1 {
		t.Fatalf("Remaining = %d, want 1", got)
	}
}

func TestReentrantSameRequestIDIsRejectedWithoutDeadlock(t *testing.T) {
	var gate *netsec.Gate[string]

	gate = netsec.NewGate(func(ctx context.Context, req netsec.Request) (string, error) {
		_, err := gate.Compare(ctx, req)
		if !errors.Is(err, netsec.ErrReentrantRequest) {
			return "", fmt.Errorf("nested error = %v, want ErrReentrantRequest", err)
		}
		return "handled", nil
	}, 1)

	result := make(chan error, 1)
	go func() {
		_, err := gate.Compare(context.Background(), request("reentrant-id", "lookup", "probe"))
		result <- err
	}()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Compare: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("same-ID reentrant comparator deadlocked")
	}

	if got := gate.Remaining(); got != 0 {
		t.Fatalf("Remaining = %d, want 0", got)
	}
}

func TestScopedBudgetsAreIndependent(t *testing.T) {
	scopeLookup := netsec.Scope{QueryID: "query-1", Field: "secret-index", Purpose: "lookup"}
	scopeFilter := netsec.Scope{QueryID: "query-1", Field: "secret-index", Purpose: "filter"}

	var calls atomic.Int64
	gate := netsec.NewScopedGate(func(_ context.Context, req netsec.Request) (string, error) {
		calls.Add(1)
		return req.Purpose, nil
	}, map[netsec.Scope]int{
		scopeLookup: 1,
		scopeFilter: 2,
	})

	lookupRequest := request("lookup-1", "lookup", "probe")
	if _, err := gate.Compare(context.Background(), lookupRequest); err != nil {
		t.Fatalf("lookup Compare: %v", err)
	}
	if got := gate.Remaining(scopeLookup); got != 0 {
		t.Fatalf("lookup remaining = %d, want 0", got)
	}
	if got := gate.Remaining(scopeFilter); got != 2 {
		t.Fatalf("filter remaining = %d, want 2", got)
	}
	if got := gate.Remaining(); got != 2 {
		t.Fatalf("total remaining = %d, want 2", got)
	}
	if got := gate.Limit(); got != 3 {
		t.Fatalf("total limit = %d, want 3", got)
	}

	_, err := gate.Compare(context.Background(), request("lookup-2", "lookup", "probe"))
	if !errors.Is(err, netsec.ErrBudgetExhausted) {
		t.Fatalf("lookup exhaustion error = %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := gate.Compare(context.Background(), request(fmt.Sprintf("filter-%d", i), "filter", "probe")); err != nil {
			t.Fatalf("filter Compare %d: %v", i, err)
		}
	}
	_, err = gate.Compare(context.Background(), request("filter-3", "filter", "probe"))
	if !errors.Is(err, netsec.ErrBudgetExhausted) {
		t.Fatalf("filter exhaustion error = %v", err)
	}

	if _, err := gate.Compare(context.Background(), lookupRequest); err != nil {
		t.Fatalf("replay after exhaustion: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("comparator calls = %d, want 3", calls.Load())
	}
}

func TestInvalidRequestsDoNotConsumeBudget(t *testing.T) {
	var calls atomic.Int64
	gate := netsec.NewGate(func(context.Context, netsec.Request) (bool, error) {
		calls.Add(1)
		return true, nil
	}, 1)

	valid := request("valid-id", "lookup", "probe")
	tests := []netsec.Request{
		{RequestID: "", QueryID: valid.QueryID, Field: valid.Field, Purpose: valid.Purpose},
		{RequestID: "id", QueryID: "", Field: valid.Field, Purpose: valid.Purpose},
		{RequestID: "id", QueryID: valid.QueryID, Field: "", Purpose: valid.Purpose},
		{RequestID: "id", QueryID: valid.QueryID, Field: valid.Field, Purpose: ""},
	}
	for i, req := range tests {
		if _, err := gate.Compare(context.Background(), req); !errors.Is(err, netsec.ErrInvalidRequest) {
			t.Fatalf("case %d error = %v, want ErrInvalidRequest", i, err)
		}
	}

	if calls.Load() != 0 {
		t.Fatalf("comparator calls = %d, want 0", calls.Load())
	}
	if got := gate.Used(); got != 0 {
		t.Fatalf("Used = %d, want 0", got)
	}
	if got := gate.Remaining(); got != 1 {
		t.Fatalf("Remaining = %d, want 1", got)
	}
}

func TestAlreadyCanceledRequestDoesNotConsumeBudget(t *testing.T) {
	var calls atomic.Int64
	gate := netsec.NewGate(func(context.Context, netsec.Request) (bool, error) {
		calls.Add(1)
		return true, nil
	}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.Compare(ctx, request("canceled-id", "lookup", "probe")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Compare error = %v, want context.Canceled", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("comparator calls = %d, want 0", calls.Load())
	}
	if got := gate.Remaining(); got != 1 {
		t.Fatalf("Remaining = %d, want 1", got)
	}

	if _, err := gate.Compare(context.Background(), request("canceled-id", "lookup", "probe")); err != nil {
		t.Fatalf("retry Compare: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("comparator calls after retry = %d, want 1", calls.Load())
	}
}

func TestCanceledWaiterDoesNotCancelSharedComparison(t *testing.T) {
	var calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})

	gate := netsec.NewGate(func(context.Context, netsec.Request) (string, error) {
		calls.Add(1)
		close(started)
		<-release
		return "matched", nil
	}, 2)

	req := request("shared-cancel-id", "lookup", "probe")
	first := make(chan error, 1)
	go func() {
		_, err := gate.Compare(context.Background(), req)
		first <- err
	}()

	<-started
	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.Compare(waitCtx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context.Canceled", err)
	}

	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first caller: %v", err)
	}

	value, err := gate.Compare(context.Background(), req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if value != "matched" {
		t.Fatalf("value = %q, want matched", value)
	}
	if calls.Load() != 1 {
		t.Fatalf("comparator calls = %d, want 1", calls.Load())
	}
	if got := gate.Remaining(); got != 1 {
		t.Fatalf("Remaining = %d, want 1", got)
	}
}
