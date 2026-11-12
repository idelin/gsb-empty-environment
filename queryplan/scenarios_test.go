package queryplan

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

// TestAllOldGenerationAnswersArriveLate covers the headline invariant: while a
// gen-1 query is parked, the host switches to gen-2 and completes a new query.
// Every gen-1 answer then arrives late: it must still complete the original
// request, yet never overwrite the gen-2 cache entry.
func TestAllOldGenerationAnswersArriveLate(t *testing.T) {
	planner, upstream := testPlanner(t, delegation(1, "a", "b"), time.Second)

	// Park every gen-1 candidate before the delegation changes.
	upstream.PauseGeneration(1)

	type lookupOutcome struct {
		result *Result
		err    error
	}
	oldDone := make(chan lookupOutcome, 1)
	go func() {
		res, err := planner.Lookup(context.Background(), Query{Name: "example.com", QType: 1})
		oldDone <- lookupOutcome{res, err}
	}()

	upstream.waitBlocked(t, 2)

	// Host replaces the authoritative set while the old query is in flight.
	if err := planner.ReplaceDelegation(delegation(2, "c", "d")); err != nil {
		t.Fatalf("replace delegation: %v", err)
	}
	if got := planner.CurrentGeneration(); got != 2 {
		t.Fatalf("current generation = %d, want 2", got)
	}

	// A gen-2 query runs against the new delegation and populates its cache.
	newResult, err := planner.Lookup(context.Background(), Query{Name: "example.com", QType: 1})
	if err != nil {
		t.Fatalf("gen-2 lookup: %v", err)
	}
	if newResult.Generation != 2 || newResult.FromCache {
		t.Fatalf("unexpected gen-2 result: %+v", newResult)
	}
	newAnswer := newResult.Response.Answer

	// Repeat the gen-2 query: it must be served from the gen-2 cache.
	cached, err := planner.Lookup(context.Background(), Query{Name: "example.com", QType: 1})
	if err != nil {
		t.Fatalf("gen-2 cached lookup: %v", err)
	}
	if !cached.FromCache || cached.Generation != 2 {
		t.Fatalf("expected gen-2 cache hit, got %+v", cached)
	}

	// Now let every late gen-1 answer arrive.
	upstream.ReleaseAll()

	var old lookupOutcome
	select {
	case old = <-oldDone:
	case <-time.After(5 * time.Second):
		t.Fatal("old generation lookup never completed")
	}
	if old.err != nil {
		t.Fatalf("old lookup returned error: %v", old.err)
	}

	// The original request still completes successfully with its own answer.
	if old.result.Generation != 1 {
		t.Fatalf("old result generation = %d, want 1", old.result.Generation)
	}
	if old.result.FromCache {
		t.Fatal("old result unexpectedly served from cache")
	}
	winner := ""
	successAnswers := map[string]bool{}
	for _, status := range old.result.Candidates {
		if status.State == StateSuccess {
			winner = status.Candidate.Name
			successAnswers["answer-from-"+status.Candidate.Name] = true
		}
	}
	if winner == "" {
		t.Fatal("old result recorded no successful candidate")
	}
	if old.result.Response == nil || !successAnswers[old.result.Response.Answer.(string)] {
		t.Fatalf("old result answer = %v, want one of the successful candidate answers", old.result.Response)
	}
	for _, status := range old.result.Candidates {
		switch {
		case status.State == StatePending:
			t.Fatalf("candidate %s still pending after Lookup returned", status.Candidate.Name)
		case status.State == StateFailed || status.State == StateTimedOut:
			// Once a wins, the rest are canceled; a candidate whose answer was
			// already in flight may still record a success, but must never be
			// pending, failed or timed out.
			t.Fatalf("candidate %s state = %s, want canceled or success", status.Candidate.Name, status.State)
		}
	}

	// The gen-2 cache must be untouched by the late gen-1 write.
	after, err := planner.Lookup(context.Background(), Query{Name: "example.com", QType: 1})
	if err != nil {
		t.Fatalf("post-release lookup: %v", err)
	}
	if !after.FromCache || after.Generation != 2 || after.Response.Answer != newAnswer {
		t.Fatalf("gen-2 cache was overwritten by late gen-1 answer: %+v", after)
	}

	if got := upstream.callsFor(1); got != 2 {
		t.Fatalf("gen-1 upstream calls = %d, want 2", got)
	}
	// Two real gen-2 calls: the first lookup and the verification call before
	// release; the post-release call is a cache hit.
	if got := upstream.callsFor(2); got != 2 {
		t.Fatalf("gen-2 upstream calls = %d, want 2", got)
	}

	if got := planner.ActivePlans(); got != 0 {
		t.Fatalf("active plans = %d, want 0", got)
	}
	assertNoPackageGoroutines(t)
}

// TestPartialNewGenerationFailure checks that candidates failing in different
// ways are each tracked, and that total failure surfaces every status without
// caching anything.
func TestPartialNewGenerationFailure(t *testing.T) {
	t.Run("mixed outcomes with one success", func(t *testing.T) {
		// 300ms per-candidate budget. slow reports a deadline error itself,
		// broken fails at once, and ok is paused until those are recorded.
		planner, upstream := testPlanner(t, delegation(3, "ok", "slow", "broken"), 300*time.Millisecond)

		// slow simulates a per-candidate timeout without relying on the
		// planner's wall clock, so the timeout label is deterministic.
		upstream.scriptCandidate("slow", nil, context.DeadlineExceeded)
		upstream.PauseCandidate("ok")
		upstream.scriptCandidate("broken", nil, errors.New("connection refused"))

		type lookupOutcome struct {
			result *Result
			err    error
		}
		done := make(chan lookupOutcome, 1)
		go func() {
			r, e := planner.Lookup(context.Background(), Query{Name: "mix.test", QType: 28})
			done <- lookupOutcome{r, e}
		}()

		// Only ok parks; slow and broken return their terminal errors at once.
		upstream.waitBlocked(t, 1)

		// Let the collector record slow's timeout and broken's failure before
		// ok is released to become the single successful candidate.
		time.Sleep(100 * time.Millisecond)
		upstream.ReleaseAll()

		outcome := <-done
		result, err := outcome.result, outcome.err
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if result.Generation != 3 {
			t.Fatalf("generation = %d, want 3", result.Generation)
		}

		states := map[string]State{}
		for _, status := range result.Candidates {
			states[status.Candidate.Name] = status.State
		}
		if states["ok"] != StateSuccess {
			t.Fatalf("ok state = %s, want success", states["ok"])
		}
		if states["slow"] != StateTimedOut {
			t.Fatalf("slow state = %s, want timeout", states["slow"])
		}
		if states["broken"] != StateFailed {
			t.Fatalf("broken state = %s, want failed", states["broken"])
		}

		// The single success is cached under gen 3; a repeat hits the cache and
		// makes no further upstream calls.
		before := len(upstream.Calls())
		again, err := planner.Lookup(context.Background(), Query{Name: "mix.test", QType: 28})
		if err != nil || !again.FromCache {
			t.Fatalf("expected cache hit, got result=%+v err=%v", again, err)
		}
		if got := len(upstream.Calls()) - before; got != 0 {
			t.Fatalf("cache hit made %d upstream calls, want 0", got)
		}

		// Releasing the parked "slow" call after the plan finished must not
		// panic or block: its outcome lands on the buffered channel and is
		// drained before Lookup returned.
		upstream.ReleaseAll()
		assertNoPackageGoroutines(t)
	})

	t.Run("all candidates fail", func(t *testing.T) {
		planner, upstream := testPlanner(t, delegation(4, "x", "y"), 250*time.Millisecond)
		upstream.scriptCandidate("x", nil, errors.New("refused"))
		upstream.PauseCandidate("y") // times out rather than returning an error

		result, err := planner.Lookup(context.Background(), Query{Name: "dead.test", QType: 1})
		if err == nil {
			t.Fatal("expected error, got result")
		}
		var allFailed *AllFailedError
		if !errors.As(err, &allFailed) {
			t.Fatalf("error type = %T, want *AllFailedError", err)
		}
		if result == nil || result.Response != nil {
			t.Fatalf("unexpected result on total failure: %+v", result)
		}

		states := map[string]State{}
		for _, status := range allFailed.Statuses {
			states[status.Candidate.Name] = status.State
		}
		if states["x"] != StateFailed {
			t.Fatalf("x state = %s, want failed", states["x"])
		}
		if states["y"] != StateTimedOut {
			t.Fatalf("y state = %s, want timeout", states["y"])
		}

		// Nothing was cached: a retry goes back upstream again.
		before := len(upstream.Calls())
		upstream.scriptCandidate("x", &Response{Answer: "recovered"}, nil)
		upstream.ReleaseAll()
		retry, err := planner.Lookup(context.Background(), Query{Name: "dead.test", QType: 1})
		if err != nil {
			t.Fatalf("retry: %v", err)
		}
		if retry.FromCache || retry.Response.Answer != "recovered" {
			t.Fatalf("retry should miss cache, got %+v", retry)
		}
		if got := len(upstream.Calls()) - before; got == 0 {
			t.Fatal("retry made no upstream calls; expected a cache miss")
		}
	})
}

// TestCancelLeavesNoDanglingWaiters cancels a request whose candidates are all
// parked and asserts Lookup returns promptly with no goroutine, channel
// receiver or blocked upstream call left behind.
func TestCancelLeavesNoDanglingWaiters(t *testing.T) {
	planner, upstream := testPlanner(t, delegation(5, "p", "q", "r"), time.Second)
	upstream.PauseGeneration(5)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := planner.Lookup(ctx, Query{Name: "cancel.test", QType: 15})
		done <- err
	}()

	upstream.waitBlocked(t, 3)

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lookup error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled lookup did not return")
	}

	if got := planner.ActivePlans(); got != 0 {
		t.Fatalf("active plans after cancel = %d, want 0", got)
	}
	if got := upstream.BlockedCount(); got != 0 {
		t.Fatalf("blocked upstream calls after cancel = %d, want 0", got)
	}

	// Releasing the (already unblocked by ctx) gates must be a no-op.
	upstream.ReleaseAll()
	assertNoPackageGoroutines(t)
}

// assertNoPackageGoroutines scans live goroutine stacks for frames owned by
// this package, excluding the test's own goroutine and testing infrastructure.
// It is the goroutine-level counterpart of Planner.ActivePlans: together they
// prove cancellation leaves no dangling waiters.
func assertNoPackageGoroutines(t *testing.T) {
	t.Helper()

	// Give just-canceled goroutines a chance to unwind; the planner itself only
	// returns after WaitGroup drain, so this loop is a defensive backstop.
	deadline := time.Now().Add(2 * time.Second)
	for {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		stacks := bytes.Split(buf[:n], []byte("\n\n"))

		leaked := 0
		for _, stack := range stacks {
			if !bytes.Contains(stack, []byte("example.org/netsec/q0144/queryplan")) {
				continue
			}
			if bytes.Contains(stack, []byte("_test.go")) ||
				bytes.Contains(stack, []byte("testing.(")) ||
				bytes.Contains(stack, []byte("runtime.goexit")) {
				continue
			}
			leaked++
		}
		if leaked == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("found %d queryplan goroutine(s) still alive:\n%s", leaked, string(buf[:n]))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
