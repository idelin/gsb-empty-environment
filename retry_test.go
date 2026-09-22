package retry

import (
	"context"
	"errors"
	"testing"

	"retrybudget/sim"
)

var errBoom = errors.New("boom")

func threeByThree() Policy {
	return Policy{ProxyBranches: 3, ReplicaAttempts: 3, ReplicaBackoff: 1}
}

func alwaysFail(r RequestSpec) Decision {
	return Decision{Delay: 1, Err: errBoom}
}

func executeResult(pol Policy, budget int, deadline int64, beh Behavior) *Result {
	s := sim.New(100000)
	return Execute(s, pol, budget, deadline, beh)
}

func runWithTransport(s *sim.Sim, pol Policy, tr *Transport, deadline int64) *Result {
	return executeWithTransport(s, pol, deadline, tr)
}

// SAFETY: nested 3x3 retries with a full budget emit exactly 9, and with a
// shared budget of 5 they can NEVER emit more than 5 — the multiplicative
// 9 is bounded by one process-wide budget.
func TestBudgetCapsAmplification(t *testing.T) {
	t.Run("full budget allows exactly nine", func(t *testing.T) {
		res := executeResult(threeByThree(), 9, -1, alwaysFail)
		if res.Sent != 9 {
			t.Fatalf("Sent = %d, want 9", res.Sent)
		}
		if res.Err == nil {
			t.Fatal("want terminal failure, got success")
		}
		if totalByBranch(res) != 9 {
			t.Fatalf("attempts logged = %d, want 9", totalByBranch(res))
		}
	})

	t.Run("shared budget five caps below nine", func(t *testing.T) {
		res := executeResult(threeByThree(), 5, -1, alwaysFail)
		if res.Sent > 5 {
			t.Fatalf("Sent = %d, must never exceed shared budget 5", res.Sent)
		}
		if res.Sent != 5 {
			t.Fatalf("Sent = %d, want 5 (budget consumed up to the cap)", res.Sent)
		}
		if !errors.Is(res.Err, ErrBudgetExceeded) {
			t.Fatalf("Err = %v, want ErrBudgetExceeded", res.Err)
		}
		// The remaining would-be emissions are refused before they leave.
		if res.DeniedBudget == 0 {
			t.Fatal("want budget denials for the suppressed attempts")
		}
	})

	t.Run("zero budget sends nothing", func(t *testing.T) {
		res := executeResult(threeByThree(), 0, -1, alwaysFail)
		if res.Sent != 0 {
			t.Fatalf("Sent = %d, want 0", res.Sent)
		}
	})
}

func totalByBranch(r *Result) int {
	n := 0
	for _, k := range r.ByBranch {
		n += k
	}
	return n
}

// SAFETY: after the deadline fires, no request is ever emitted.
func TestNoSendAfterDeadline(t *testing.T) {
	deadline := int64(4)
	// Every attempt takes 10, longer than the deadline; first-wave requests
	// are in flight when the cutoff fires.
	beh := func(RequestSpec) Decision { return Decision{Delay: 10, Err: errBoom} }
	res := executeResult(threeByThree(), 100, deadline, beh)

	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Fatalf("Err = %v, want context.DeadlineExceeded", res.Err)
	}
	if !res.DeadlineHit {
		t.Fatal("want DeadlineHit")
	}
	if res.Sent > 3 {
		t.Fatalf("Sent = %d, only the t=0 wave (3) is permitted", res.Sent)
	}
	// The decisive check needs per-request send times, asserted in the
	// transport-level test below; here we at least confirm backoff retries
	// scheduled after t=4 never emitted.
}

// Transport-level invariant: every emitted request has SentAt < deadline and
// every post-deadline emission attempt is refused.
func TestTransportDeadlineChokepoint(t *testing.T) {
	s := sim.New(100000)
	tr := NewTransport(s, 10, 10, func(RequestSpec) Decision {
		return Decision{Silent: true}
	})
	root := sim.RootCtl(s)

	id1, _, err := tr.Send(0, 1, root, func(error) {})
	if err != nil {
		t.Fatalf("first send: %v", err)
	}

	// Advance past the deadline.
	s.After(10, func(*sim.Sim) {})
	s.Run()

	if _, _, err := tr.Send(0, 2, root, func(error) {}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("post-deadline send err = %v, want DeadlineExceeded", err)
	}
	if tr.DeniedDeadline != 1 {
		t.Fatalf("DeniedDeadline = %d, want 1", tr.DeniedDeadline)
	}
	for _, r := range tr.Records() {
		if r.SentAt >= 10 {
			t.Fatalf("request %d sent at %d >= deadline 10", r.ID, r.SentAt)
		}
	}
	if id1 == 0 {
		t.Fatal("want nonzero attempt id")
	}
}

// SAFETY: once a branch succeeds, late failures from other in-flight attempts
// must not change the terminal state.
func TestLateFailureAfterSuccess(t *testing.T) {
	pol := Policy{ProxyBranches: 2, ReplicaAttempts: 1}
	beh := func(r RequestSpec) Decision {
		if r.Branch == 0 {
			return Decision{Delay: 2} // fast success
		}
		return Decision{Delay: 5, Err: errBoom} // slow failure (hedge)
	}
	res := executeResult(pol, 10, -1, beh)

	if res.Err != nil {
		t.Fatalf("Err = %v, want success", res.Err)
	}
	if res.Success.Branch != 0 || res.Success.ReplicaAttempt != 1 {
		t.Fatalf("success = %+v, want branch0/try1", res.Success)
	}
	if res.OkAt != 2 {
		t.Fatalf("OkAt = %d, want 2", res.OkAt)
	}
	if res.Sent != 2 {
		t.Fatalf("Sent = %d, want 2 (both branches launched)", res.Sent)
	}
}

// The slow hedge's wire request is canceled when the fast branch wins.
func TestCancelPropagation(t *testing.T) {
	pol := Policy{ProxyBranches: 2, ReplicaAttempts: 1}
	beh := func(r RequestSpec) Decision {
		if r.Branch == 0 {
			return Decision{Delay: 2}
		}
		return Decision{Delay: 5, Err: errBoom}
	}
	s := sim.New(100000)
	tr := NewTransport(s, 10, -1, beh)
	res := runWithTransport(s, pol, tr, -1)
	if res.Err != nil {
		t.Fatalf("Err = %v, want success", res.Err)
	}
	var slow *SendRecord
	for i := range tr.Records() {
		r := tr.Records()[i]
		if r.Branch == 1 {
			slow = &r
		}
	}
	if slow == nil {
		t.Fatal("slow branch request not found")
	}
	if !slow.Canceled {
		t.Fatal("slow request must be marked canceled")
	}
	if slow.ResolvedAt != 2 {
		t.Fatalf("slow request ResolvedAt = %d, want 2 (canceled at success)", slow.ResolvedAt)
	}
}

// LIVENESS: a flaky backend that fails twice then succeeds completes
// successfully via lower-layer retries, without exceeding the local cap.
func TestLivenessSuccessAfterRetries(t *testing.T) {
	pol := Policy{ProxyBranches: 1, ReplicaAttempts: 3, ReplicaBackoff: 1}
	beh := func(r RequestSpec) Decision {
		if r.ReplicaAttempt >= 3 {
			return Decision{Delay: 1}
		}
		return Decision{Delay: 1, Err: errBoom}
	}
	res := executeResult(pol, 10, -1, beh)
	if res.Err != nil {
		t.Fatalf("Err = %v, want success on third try", res.Err)
	}
	if res.Sent != 3 {
		t.Fatalf("Sent = %d, want 3", res.Sent)
	}
	if res.Success.ReplicaAttempt != 3 {
		t.Fatalf("success attempt = %d, want 3", res.Success.ReplicaAttempt)
	}
}

// LIVENESS: a fully silent peer with no deadline still terminates.
func TestLivenessSilentPeer(t *testing.T) {
	pol := Policy{ProxyBranches: 2, ReplicaAttempts: 1}
	res := executeResult(pol, 5, -1, func(RequestSpec) Decision {
		return Decision{Silent: true}
	})
	if res.Err == nil {
		t.Fatal("want terminal error, got nil")
	}
	if res.Sent != 2 {
		t.Fatalf("Sent = %d, want 2", res.Sent)
	}
}

// LIVENESS: deadline always terminates even when every peer is silent.
func TestLivenessDeadlineTerminatesSilence(t *testing.T) {
	res := executeResult(threeByThree(), 50, 7, func(RequestSpec) Decision {
		return Decision{Silent: true}
	})
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Fatalf("Err = %v, want deadline", res.Err)
	}
}

// Every attempt has a unique id, globally across layers and branches.
func TestAttemptIDsUnique(t *testing.T) {
	res := executeResult(threeByThree(), 9, -1, alwaysFail)
	seen := map[uint64]bool{}
	for _, a := range res.Attempts {
		if a.ID == 0 {
			t.Fatal("zero attempt id")
		}
		if seen[a.ID] {
			t.Fatalf("duplicate attempt id %d", a.ID)
		}
		seen[a.ID] = true
	}
	if len(seen) != 9 {
		t.Fatalf("unique ids = %d, want 9", len(seen))
	}
}

// Determinism: identical scripts yield identical terminal results and traces.
func TestDeterministic(t *testing.T) {
	pol := threeByThree()
	beh := func(r RequestSpec) Decision {
		// Deterministic, branch-dependent delays, always failure.
		return Decision{Delay: 1 + int64(r.Branch), Err: errBoom}
	}
	// All fail; with budget 6 (< 9) the run terminates by budget exhaustion,
	// before the deadline.
	r1 := executeResult(pol, 6, 15, beh)
	r2 := executeResult(pol, 6, 15, beh)
	if !errors.Is(r1.Err, ErrBudgetExceeded) || !errors.Is(r2.Err, ErrBudgetExceeded) {
		t.Fatalf("want budget exhaustion both runs, got r1=%v r2=%v", r1.Err, r2.Err)
	}
	if r1.Sent != 6 || r2.Sent != 6 {
		t.Fatalf("Sent = %d, %d; want 6,6", r1.Sent, r2.Sent)
	}
	if r1.OkAt != r2.OkAt {
		t.Fatal("nondeterministic terminal time")
	}
}

// Deadline denial at the chokepoint is counted and sends nothing.
func TestDeadlineDeniesAtExactCutoff(t *testing.T) {
	s := sim.New(100000)
	s.After(10, func(*sim.Sim) {}) // move clock to exactly the deadline
	s.Run()
	tr := NewTransport(s, 10, 10, func(RequestSpec) Decision { return Decision{} })
	root := sim.RootCtl(s)
	if _, _, err := tr.Send(0, 1, root, func(error) {}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline at t==cutoff", err)
	}
	if tr.Sent != 0 || tr.DeniedDeadline != 1 {
		t.Fatalf("Sent=%d DeniedDeadline=%d", tr.Sent, tr.DeniedDeadline)
	}
}

// Denied emissions consume no budget: a refused attempt must leave the shared
// budget (and therefore the real send count) exactly where it was.
func TestDenialConsumesNoBudget(t *testing.T) {
	s := sim.New(100000)
	tr := NewTransport(s, 1, -1, func(RequestSpec) Decision { return Decision{Silent: true} })
	root := sim.RootCtl(s)
	if _, _, err := tr.Send(0, 1, root, func(error) {}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, _, err := tr.Send(0, 2, root, func(error) {}); !errors.Is(err, ErrBudgetExceeded) {
			t.Fatalf("denial %d: %v", i, err)
		}
	}
	if tr.Sent != 1 || tr.DeniedBudget != 5 {
		t.Fatalf("Sent=%d DeniedBudget=%d, want 1/5", tr.Sent, tr.DeniedBudget)
	}
}

// Multiple slow in-flight hedges all become harmless late failures after one
// branch succeeds: none can flip the terminal result.
func TestSeveralLateFailuresAfterSuccess(t *testing.T) {
	pol := Policy{ProxyBranches: 4, ReplicaAttempts: 1}
	beh := func(r RequestSpec) Decision {
		if r.Branch == 0 {
			return Decision{Delay: 2}
		}
		return Decision{Delay: 3 + int64(r.Branch), Err: errBoom}
	}
	s := sim.New(100000)
	tr := NewTransport(s, 20, -1, beh)
	res := runWithTransport(s, pol, tr, -1)

	if res.Err != nil {
		t.Fatalf("terminal err = %v, late failures flipped the result", res.Err)
	}
	if res.OkAt != 2 {
		t.Fatalf("OkAt = %d, want 2", res.OkAt)
	}
	canceled := 0
	for _, r := range tr.Records() {
		if r.Branch != 0 {
			if !r.Canceled {
				t.Fatalf("branch %d request was not canceled at success", r.Branch)
			}
			canceled++
		}
	}
	if canceled != 3 {
		t.Fatalf("canceled hedges = %d, want 3", canceled)
	}
}
