package main

import (
	"errors"
	"testing"
)

func TestReplayFindsSafetyViolation(t *testing.T) {
	report, err := Replay(safetySample())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if got := report.FailureClass(); got != "safety" {
		t.Fatalf("FailureClass() = %q, want safety", got)
	}
	if len(report.Safety) != 1 {
		t.Fatalf("len(Safety) = %d, want 1", len(report.Safety))
	}
	violation := report.Safety[0]
	if violation.Kind != "stale_read" || violation.Node != NodeB || violation.Expected != "1" || violation.Got != "" {
		t.Fatalf("unexpected safety violation: %+v", violation)
	}
	if report.Replicas[NodeA].Store["k"] != "1" {
		t.Fatalf("replica A did not retain acknowledged write: %+v", report.Replicas)
	}
	if report.Replicas[NodeB].Store["k"] != "" {
		t.Fatalf("dropped replication was applied to B: %+v", report.Replicas)
	}
}

func TestMinimizeKeepsNecessaryDrop(t *testing.T) {
	events := safetySample()
	result, err := MinimizeCounterexample(events)
	if err != nil {
		t.Fatalf("MinimizeCounterexample() error = %v", err)
	}
	wantIDs := []string{"w-x", "s-x", "drop-x", "deliver-x", "read-x-at-b"}
	if len(result.Minimal) != len(wantIDs) {
		t.Fatalf("minimal length = %d, want %d: %v", len(result.Minimal), len(wantIDs), result.Retained)
	}
	for i, want := range wantIDs {
		if result.Minimal[i].ID != want {
			t.Fatalf("minimal[%d].ID = %q, want %q; full = %v", i, result.Minimal[i].ID, want, result.Retained)
		}
	}
	if result.FinalReport.FailureClass() != "safety" {
		t.Fatalf("minimal report class = %q, want safety", result.FinalReport.FailureClass())
	}
	if len(result.NecessityAudit) != len(wantIDs) {
		t.Fatalf("audit length = %d, want %d", len(result.NecessityAudit), len(wantIDs))
	}
	for _, audit := range result.NecessityAudit {
		if audit.Verdict == VerdictReproduced {
			t.Fatalf("event %s was reported removable: %+v", audit.EventID, audit)
		}
	}
}

func TestRemovingNecessaryDropStopsFailure(t *testing.T) {
	events := safetySample()
	result, err := MinimizeCounterexample(events)
	if err != nil {
		t.Fatalf("MinimizeCounterexample() error = %v", err)
	}
	withoutDrop := removeEvent(result.Minimal, "drop-x")
	report, err := Replay(withoutDrop)
	if err != nil {
		t.Fatalf("replay without drop error = %v", err)
	}
	if got := report.FailureClass(); got != "no_failure" {
		t.Fatalf("without drop FailureClass() = %q, want no_failure", got)
	}
	if report.Replicas[NodeB].Store["k"] != "1" {
		t.Fatalf("without drop B.k = %q, want 1", report.Replicas[NodeB].Store["k"])
	}
}

func TestTimeoutIsLivenessNotSafety(t *testing.T) {
	report, err := Replay(livenessSample())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if got := report.FailureClass(); got != "liveness" {
		t.Fatalf("FailureClass() = %q, want liveness", got)
	}
	if len(report.Safety) != 0 {
		t.Fatalf("timeout produced safety violations: %+v", report.Safety)
	}
	if len(report.Liveness) != 1 || report.Liveness[0].Kind != "deadline_exceeded" {
		t.Fatalf("unexpected liveness violations: %+v", report.Liveness)
	}
}

func TestDependencyBrokenCandidatesAreRejected(t *testing.T) {
	events := safetySample()
	withoutDelivery := removeEvent(events, "deliver-x")
	if _, err := Replay(withoutDelivery); err == nil {
		t.Fatal("removing scheduled delivery for a drop was accepted")
	} else {
		var invalid InvalidEventError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %T %[1]v, want InvalidEventError", err)
		}
	}

	duplicate := append(safetySample(), Event{
		ID: "duplicate-id", Type: EventRead, Node: NodeA,
		OpID: "op-read-x", Key: "k", At: 0,
	})
	if _, err := Replay(duplicate); err == nil {
		t.Fatal("duplicate operation id was accepted")
	}
}

func TestDifferentSafetyFingerprintIsNotSameFailure(t *testing.T) {
	events := safetySample()
	initial, err := Replay(events)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	target := safetyFingerprint(initial.Safety)
	different := []Event{
		{ID: "w-old", Type: EventWrite, Node: NodeA, OpID: "w-old", Key: "k", Value: "1", At: 0},
		{ID: "w-new", Type: EventWrite, Node: NodeA, OpID: "w-new", Key: "k", Value: "2", At: 0},
		{ID: "r-old", Type: EventRead, Node: NodeA, OpID: "r-old", Key: "k", Expected: "1", At: 0},
	}
	report, verdict, _ := classifyCandidate(different, target)
	if report == nil {
		t.Fatal("candidate did not produce a report")
	}
	if verdict != VerdictDifferentFailure {
		t.Fatalf("verdict = %q, want %q", verdict, VerdictDifferentFailure)
	}
}

func TestBalancedChunks(t *testing.T) {
	values := []int{0, 1, 2, 3, 4, 5, 6}
	chunks := balancedChunks(values, 3)
	if len(chunks) != 3 {
		t.Fatalf("len(chunks) = %d, want 3", len(chunks))
	}
	if got := len(chunks[0]) + len(chunks[1]) + len(chunks[2]); got != 7 {
		t.Fatalf("chunks cover %d values, want 7", got)
	}
	if len(chunks[0]) != 3 || len(chunks[1]) != 2 || len(chunks[2]) != 2 {
		t.Fatalf("chunk sizes = %d,%d,%d, want 3,2,2", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}
