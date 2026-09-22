package main

import "testing"

func shrunkMust(t *testing.T) []Action {
	t.Helper()
	minimal, _, err := Shrink(originalTrace())
	if err != nil {
		t.Fatalf("Shrink: %v", err)
	}
	return minimal
}

// The shrinker strictly reduces the trace and preserves the exact same
// safety-violation signature.
func TestShrinkReducesAndPreserves(t *testing.T) {
	orig := originalTrace()
	minimal := shrunkMust(t)
	if len(minimal) >= len(orig) {
		t.Fatalf("expected shrinking, %d -> %d", len(orig), len(minimal))
	}
	res, err := Replay(minimal)
	if err != nil {
		t.Fatalf("minimal trace must replay: %v", err)
	}
	if got, want := Violation(res.Decisions), "commit{C,P1} vs abort{P2}"; got != want {
		t.Fatalf("violation = %q, want %q", got, want)
	}
}

// 1-minimality: removing any single remaining event either breaks replay
// (dependency/quiescence) or eliminates the safety violation.
func TestShrunkIsOneMinimal(t *testing.T) {
	minimal := shrunkMust(t)
	sig := Violation(mustReplayT(t, minimal).Decisions)
	for i := range minimal {
		cand := append(append([]Action(nil), minimal[:i]...), minimal[i+1:]...)
		res, err := Replay(cand)
		if err != nil {
			continue // invalid candidate: event is load-bearing
		}
		if Violation(res.Decisions) == sig {
			t.Fatalf("removing %v still fails: trace not 1-minimal", minimal[i])
		}
	}
}

// The necessary packet loss is kept: it appears in the minimal trace, and
// healing it (delivering instead of dropping) eliminates the failure.
func TestNecessaryLossKept(t *testing.T) {
	minimal := shrunkMust(t)
	idx := -1
	for i, a := range minimal {
		if a.Kind == ActDrop && a.Key == "Commit:C->P2#1" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("necessary loss Drop(Commit:C->P2#1) not kept in %v", minimal)
	}
	healed := append([]Action(nil), minimal...)
	healed[idx] = Action{Kind: ActDeliver, Key: "Commit:C->P2#1"}
	res, err := Replay(healed)
	if err != nil {
		t.Fatal(err)
	}
	if got := Violation(res.Decisions); got != "" {
		t.Fatalf("healed trace must not fail, got %s", got)
	}
}

// The shrinker refuses to shrink a trace that is not a safety violation
// (e.g. the fault-free, live run).
func TestShrinkRejectsNonViolatingTrace(t *testing.T) {
	if _, _, err := Shrink(faultFreeTrace()); err == nil {
		t.Fatal("expected error for non-violating trace")
	}
}

func mustReplayT(t *testing.T, trace []Action) Result {
	t.Helper()
	res, err := Replay(trace)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
