package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// reentrantComparator is a test comparator safe for concurrent use. It
// counts invocations and optionally fails on demand.
type reentrantComparator struct {
	calls atomic.Int64
	fail  atomic.Bool
}

func (c *reentrantComparator) Compare(field string, left, right []byte) (int, error) {
	c.calls.Add(1)
	if c.fail.Load() {
		return 0, errors.New("comparator unavailable")
	}
	return bytes.Compare(left, right), nil
}

func request(query, id string) Request {
	return Request{
		QueryID:   query,
		RequestID: id,
		Field:     "salary",
		Purpose:   "filter",
		Left:      []byte("a"),
		Right:     []byte("b"),
	}
}

func TestCompareBindsAndDeducts(t *testing.T) {
	cmp := &reentrantComparator{}
	g := NewGate(cmp)
	g.Authorize("q1", 3)

	rec, err := g.Compare(request("q1", "r1"))
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if rec.Result != -1 || rec.RequestID != "r1" || rec.QueryID != "q1" ||
		rec.Field != "salary" || rec.Purpose != "filter" || rec.Replayed {
		t.Fatalf("unexpected receipt: %+v", rec)
	}
	if bal, _ := g.Balance("q1"); bal != 2 {
		t.Fatalf("balance = %d, want 2", bal)
	}
}

func TestIdempotentReplayServesCachedResult(t *testing.T) {
	cmp := &reentrantComparator{}
	g := NewGate(cmp)
	g.Authorize("q1", 2)

	first, err := g.Compare(request("q1", "r1"))
	if err != nil {
		t.Fatalf("first Compare: %v", err)
	}
	for i := 0; i < 5; i++ {
		replay, err := g.Compare(request("q1", "r1"))
		if err != nil {
			t.Fatalf("replay Compare: %v", err)
		}
		if !replay.Replayed || replay.Result != first.Result {
			t.Fatalf("replay = %+v, want cached result %d", replay, first.Result)
		}
	}
	if cmp.calls.Load() != 1 {
		t.Fatalf("comparator calls = %d, want 1", cmp.calls.Load())
	}
	if bal, _ := g.Balance("q1"); bal != 1 {
		t.Fatalf("balance = %d, want 1 (single deduction)", bal)
	}
}

func TestReplayWithDifferentOperationRejected(t *testing.T) {
	cmp := &reentrantComparator{}
	g := NewGate(cmp)
	g.Authorize("q1", 10)

	if _, err := g.Compare(request("q1", "r1")); err != nil {
		t.Fatalf("first Compare: %v", err)
	}

	mutations := map[string]func(Request) Request{
		"field":   func(r Request) Request { r.Field = "age"; return r },
		"purpose": func(r Request) Request { r.Purpose = "sort"; return r },
		"operand": func(r Request) Request { r.Right = []byte("c"); return r },
		"query":   func(r Request) Request { r.QueryID = "q2"; return r },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, err := g.Compare(mutate(request("q1", "r1"))); !errors.Is(err, ErrRequestIDConflict) {
				t.Fatalf("err = %v, want ErrRequestIDConflict", err)
			}
		})
	}
	if cmp.calls.Load() != 1 {
		t.Fatalf("comparator calls = %d, want 1", cmp.calls.Load())
	}
	if bal, _ := g.Balance("q1"); bal != 9 {
		t.Fatalf("balance = %d, want 9 (conflicts must not deduct)", bal)
	}
}

func TestBudgetExhaustion(t *testing.T) {
	cmp := &reentrantComparator{}
	g := NewGate(cmp)
	g.Authorize("q1", 3)

	for i := 0; i < 3; i++ {
		if _, err := g.Compare(request("q1", fmt.Sprintf("r%d", i))); err != nil {
			t.Fatalf("Compare %d: %v", i, err)
		}
	}
	if _, err := g.Compare(request("q1", "r3")); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	if cmp.calls.Load() != 3 {
		t.Fatalf("comparator calls = %d, want 3", cmp.calls.Load())
	}
}

func TestUnknownQueryRejected(t *testing.T) {
	g := NewGate(&reentrantComparator{})
	if _, err := g.Compare(request("ghost", "r1")); !errors.Is(err, ErrUnknownQuery) {
		t.Fatalf("err = %v, want ErrUnknownQuery", err)
	}
}

// TestFailureRefundDoesNotAllowReprobe verifies that when a comparison
// fails and its deduction is refunded, the same request ID cannot be used
// to probe the comparator again: the failure is bound to the ID and
// replays return it without re-executing or re-deducting.
func TestFailureRefundDoesNotAllowReprobe(t *testing.T) {
	cmp := &reentrantComparator{}
	cmp.fail.Store(true)
	g := NewGate(cmp)
	g.Authorize("q1", 1)

	if _, err := g.Compare(request("q1", "r1")); err == nil || errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want comparator failure", err)
	}
	if bal, _ := g.Balance("q1"); bal != 1 {
		t.Fatalf("balance = %d, want 1 (exactly one refund)", bal)
	}

	// Comparator recovers; replaying the failed request ID must not
	// re-probe it, even though budget is available again.
	cmp.fail.Store(false)
	if _, err := g.Compare(request("q1", "r1")); err == nil {
		t.Fatal("replay of failed request ID succeeded, want recorded failure")
	}
	if cmp.calls.Load() != 1 {
		t.Fatalf("comparator calls = %d, want 1 (no re-probe after refund)", cmp.calls.Load())
	}
	if bal, _ := g.Balance("q1"); bal != 1 {
		t.Fatalf("balance = %d, want 1 (replay must not deduct or refund)", bal)
	}

	// A fresh request ID is a new operation and may use the refunded budget.
	rec, err := g.Compare(request("q1", "r2"))
	if err != nil {
		t.Fatalf("fresh Compare after refund: %v", err)
	}
	if rec.Result != -1 {
		t.Fatalf("result = %d, want -1", rec.Result)
	}
	if bal, _ := g.Balance("q1"); bal != 0 {
		t.Fatalf("balance = %d, want 0", bal)
	}
}

// TestConcurrentDuplicateRequests hammers the gate with concurrent
// requests, many sharing request IDs, against a reentrant comparator. It
// verifies the budget is never overspent, each distinct request ID drives
// at most one comparator call, and every caller observing the same ID gets
// the same outcome.
func TestConcurrentDuplicateRequests(t *testing.T) {
	cmp := &reentrantComparator{}
	g := NewGate(cmp)

	const (
		workers     = 32
		perWorker   = 50
		distinctIDs = 40
		budget      = 25
	)
	g.Authorize("q1", budget)

	type outcome struct {
		result int
		err    error
	}
	results := make([][]outcome, distinctIDs)
	for i := range results {
		results[i] = make([]outcome, 0, workers*perWorker/distinctIDs+1)
	}
	var mu sync.Mutex

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := (w*perWorker + i) % distinctIDs
				rec, err := g.Compare(request("q1", fmt.Sprintf("r%d", id)))
				mu.Lock()
				results[id] = append(results[id], outcome{rec.Result, err})
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	succeeded := 0
	for id, outs := range results {
		var first *outcome
		for i := range outs {
			o := &outs[i]
			if first == nil {
				if o.err == nil || errors.Is(o.err, ErrBudgetExhausted) {
					first = o
				}
				continue
			}
			if (first.err == nil) != (o.err == nil) {
				t.Fatalf("id r%d: inconsistent outcomes %v vs %v", id, first.err, o.err)
			}
			if first.err == nil && first.result != o.result {
				t.Fatalf("id r%d: results differ: %d vs %d", id, first.result, o.result)
			}
		}
		if first != nil && first.err == nil {
			succeeded++
		}
	}

	if calls := cmp.calls.Load(); calls != int64(succeeded) {
		t.Fatalf("comparator calls = %d, want %d (one per successful distinct ID)", calls, succeeded)
	}
	if succeeded > budget {
		t.Fatalf("succeeded = %d exceeds budget %d", succeeded, budget)
	}
	bal, _ := g.Balance("q1")
	if bal != int64(budget-succeeded) {
		t.Fatalf("balance = %d, want %d", bal, budget-succeeded)
	}
	if bal < 0 {
		t.Fatalf("balance went negative: %d", bal)
	}
}

// TestConcurrentConflictingReplay races identical request IDs carrying
// different operations: exactly one shape may win, all others must be
// rejected with ErrRequestIDConflict, and the comparator runs at most once.
func TestConcurrentConflictingReplay(t *testing.T) {
	cmp := &reentrantComparator{}
	g := NewGate(cmp)
	g.Authorize("q1", 10)

	const racers = 16
	var wg sync.WaitGroup
	var won, conflicts atomic.Int64
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := request("q1", "shared")
			req.Purpose = fmt.Sprintf("purpose-%d", i)
			<-start
			_, err := g.Compare(req)
			switch {
			case err == nil:
				won.Add(1)
			case errors.Is(err, ErrRequestIDConflict):
				conflicts.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if won.Load() != 1 {
		t.Fatalf("winners = %d, want exactly 1", won.Load())
	}
	if conflicts.Load() != racers-1 {
		t.Fatalf("conflicts = %d, want %d", conflicts.Load(), racers-1)
	}
	if cmp.calls.Load() != 1 {
		t.Fatalf("comparator calls = %d, want 1", cmp.calls.Load())
	}
	if bal, _ := g.Balance("q1"); bal != 9 {
		t.Fatalf("balance = %d, want 9", bal)
	}
}

// TestConcurrentFailuresRefundOnce stresses the failure path: concurrent
// distinct requests against a failing comparator must each refund exactly
// once, leaving the balance unchanged.
func TestConcurrentFailuresRefundOnce(t *testing.T) {
	cmp := &reentrantComparator{}
	cmp.fail.Store(true)
	g := NewGate(cmp)

	const budget = 100
	g.Authorize("q1", budget)

	var wg sync.WaitGroup
	for i := 0; i < budget; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := g.Compare(request("q1", fmt.Sprintf("r%d", i))); err == nil {
				t.Error("expected comparator failure")
			}
		}(i)
	}
	wg.Wait()

	if bal, _ := g.Balance("q1"); bal != budget {
		t.Fatalf("balance = %d, want %d (every failure refunded exactly once)", bal, budget)
	}
}
