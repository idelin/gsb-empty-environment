package main

import "testing"

// Liveness evidence: every invoked operation completes within bounded
// virtual time.
func assertLiveness(t *testing.T, rep SimReport, maxNow int64) {
	t.Helper()
	if rep.Completed != rep.Invoked {
		t.Fatalf("liveness: %d/%d invocations completed", rep.Completed, rep.Invoked)
	}
	if rep.Now > maxNow {
		t.Fatalf("liveness: virtual time %d exceeds bound %d", rep.Now, maxNow)
	}
}

// Safety evidence: the checker proves the recorded history linearizable
// and the witness independently verifies.
func assertSafe(t *testing.T, rep SimReport) {
	t.Helper()
	res := Checker{Init: 0, Budget: testBudget}.Check(rep.History)
	if res.Verdict != Linearizable {
		t.Fatalf("safety: verdict = %s (reason: %s)", res.Verdict, res.Reason)
	}
	if err := VerifyLinearization(rep.History, res.Witness, 0); err != nil {
		t.Fatalf("safety: witness does not verify: %v", err)
	}
}

// Fault-free quorum register: safe and live.
func TestCorrectProtocolSafetyAndLiveness(t *testing.T) {
	rep, _ := goodScenario()
	assertLiveness(t, rep, 100)
	assertSafe(t, rep)
}

// One replica of three crashed: quorum availability keeps the register
// both live and safe.
func TestCorrectProtocolSurvivesCrash(t *testing.T) {
	rep, _ := crashScenario()
	assertLiveness(t, rep, 100)
	assertSafe(t, rep)
}

// Buggy single-replica read plus a held STORE message: the checker
// produces counterexample evidence, and the write itself still
// completed (the bug is in safety, not liveness).
func TestBuggyProtocolCounterexample(t *testing.T) {
	rep, _ := badScenario()
	assertLiveness(t, rep, 100)
	res := Checker{Init: 0, Budget: testBudget}.Check(rep.History)
	if res.Verdict != NotLinearizable {
		t.Fatalf("verdict = %s, want NOT LINEARIZABLE", res.Verdict)
	}
	if res.Reason == "" {
		t.Fatal("counterexample evidence is empty")
	}
	t.Logf("counterexample evidence: %s", res.Reason)
}

// The simulator's fault controls actually bite: dropping every message
// to a quorum of replicas starves the client (no completion), which is
// the expected liveness failure under that fault model.
func TestDropsBreakLiveness(t *testing.T) {
	sim := NewSim(Faults{Drop: map[string]bool{
		"c1o0>r0": true, "c1o0>r1": true,
	}})
	reps := []*Replica{{name: "r0"}, {name: "r1"}, {name: "r2"}}
	nextID := 1
	c1 := &Client{id: 1, sim: sim, reps: reps, nextID: &nextID, script: []Op{
		{Kind: OpWrite, Val: 1},
	}}
	c1.start(0)
	rep := runClients(sim, []*Client{c1}, 1000)
	if rep.Completed != 0 {
		t.Fatalf("expected starvation, got %d completions", rep.Completed)
	}
}
