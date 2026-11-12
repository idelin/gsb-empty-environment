package queryplan

import (
	"context"
	"testing"
	"time"
)

func TestNewPlannerRequiresUpstream(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewPlanner with nil upstream did not panic")
		}
	}()
	NewPlanner(nil, delegation(1, "a"))
}

func TestLookupWithoutCandidatesErrors(t *testing.T) {
	planner, _ := testPlanner(t, Delegation{Generation: 1}, time.Second)
	if _, err := planner.Lookup(context.Background(), Query{Name: "empty", QType: 1}); err == nil {
		t.Fatal("lookup with no candidates succeeded, want error")
	}
}

func TestNilResponseIsTrackedAsFailure(t *testing.T) {
	planner, upstream := testPlanner(t, delegation(1, "a"), time.Second)
	upstream.scriptCandidate("a", nil, nil)

	result, err := planner.Lookup(context.Background(), Query{Name: "nil.test", QType: 1})
	if err == nil {
		t.Fatal("nil response expected to fail")
	}
	if result == nil || result.Candidates[0].State != StateFailed {
		t.Fatalf("nil response state = %+v, want failed", result)
	}
	if _, ok := planner.cachedAnswer(cacheKey(Query{Name: "nil.test", QType: 1}), 1); ok {
		t.Fatal("nil response was cached")
	}
}

func TestNegativeCandidateTimeoutDisablesPlannerDeadline(t *testing.T) {
	planner, upstream := testPlanner(t, delegation(1, "a"), -1)
	upstream.PauseCandidate("a")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = planner.Lookup(context.Background(), Query{Name: "nodeadline", QType: 1})
	}()
	upstream.waitBlocked(t, 1)

	// No planner-owned deadline fires: the call stays parked until released.
	time.Sleep(50 * time.Millisecond)
	if got := upstream.BlockedCount(); got != 1 {
		t.Fatalf("blocked = %d, want still 1 without planner deadline", got)
	}
	upstream.ReleaseAll()
	<-done
}

func TestStateStringsAndAllFailedError(t *testing.T) {
	states := []State{StatePending, StateSuccess, StateTimedOut, StateCanceled, StateFailed, State(99)}
	wants := []string{"pending", "success", "timeout", "canceled", "failed", "unknown"}
	for i, state := range states {
		if got := state.String(); got != wants[i] {
			t.Fatalf("state %d string = %q, want %q", state, got, wants[i])
		}
	}

	err := &AllFailedError{Statuses: []CandidateStatus{{State: StateFailed}}}
	if err.Error() == "" {
		t.Fatal("AllFailedError.Error returned empty string")
	}
}
