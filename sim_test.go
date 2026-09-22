package main

import (
	"strings"
	"testing"
)

// Liveness: with no faults the protocol terminates and everyone commits.
func TestFaultFreeLiveness(t *testing.T) {
	trace := faultFreeTrace()
	res, err := Replay(trace)
	if err != nil {
		t.Fatalf("fault-free trace must replay: %v", err)
	}
	for _, n := range nodeOrder {
		if res.Decisions[n] != "commit" {
			t.Fatalf("liveness: node %s did not commit, decisions=%v", n, res.Decisions)
		}
	}
	if got := Violation(res.Decisions); got != "" {
		t.Fatalf("fault-free run must be safe, got violation %s", got)
	}
	if len(trace) != 7 {
		t.Fatalf("fault-free run should take 7 events, got %d", len(trace))
	}
}

// Safety: the crafted long trace reproduces the split decision.
func TestBugReproduces(t *testing.T) {
	res, err := Replay(originalTrace())
	if err != nil {
		t.Fatalf("original trace must replay: %v", err)
	}
	want := "commit{C,P1} vs abort{P2}"
	if got := Violation(res.Decisions); got != want {
		t.Fatalf("violation = %q, want %q", got, want)
	}
}

// A delivery whose send was removed breaks the event dependency and the
// candidate must be rejected (replay error), never silently skipped.
func TestReplayRejectsBrokenDependency(t *testing.T) {
	_, err := Replay([]Action{
		{Kind: ActDeliver, Key: "Prepare:C->P1#1"}, // no Request: never sent
	})
	if err == nil || !strings.Contains(err.Error(), "dependency broken") {
		t.Fatalf("expected dependency error, got %v", err)
	}
}

// A schedule that leaves a message undelivered and not dropped is incomplete.
func TestReplayRequiresQuiescence(t *testing.T) {
	_, err := Replay([]Action{{Kind: ActRequest}}) // Prepares left pending
	if err == nil || !strings.Contains(err.Error(), "quiescence") {
		t.Fatalf("expected quiescence error, got %v", err)
	}
}

// A timeout that leaves a node undecided is a liveness symptom, not the
// safety error: no divergence, no violation.
func TestTimeoutIsNotSafetyViolation(t *testing.T) {
	minimal, _, err := Shrink(originalTrace())
	if err != nil {
		t.Fatal(err)
	}
	stuck, err := Replay(minimal[:len(minimal)-1]) // drop final Timeout(P2)
	if err != nil {
		t.Fatal(err)
	}
	if _, decided := stuck.Decisions[NodeP2]; decided {
		t.Fatalf("P2 should be undecided, got %v", stuck.Decisions)
	}
	if got := Violation(stuck.Decisions); got != "" {
		t.Fatalf("stuck node must not be a safety violation, got %s", got)
	}
}
