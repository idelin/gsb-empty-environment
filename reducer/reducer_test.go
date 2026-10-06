package reducer

import "testing"

// contains reports whether op appears in seq.
func contains(seq []int, op int) bool {
	for _, v := range seq {
		if v == op {
			return true
		}
	}
	return false
}

// assertLegal wraps a predicate so the test fails if the reducer ever
// evaluates an illegal candidate (reference before creation).
func assertLegal(t *testing.T, pred Predicate) Predicate {
	return func(seq []int) Verdict {
		if !Legal(seq) {
			t.Errorf("predicate evaluated on illegal sequence %v", seq)
		}
		return pred(seq)
	}
}

// checkOneMinimal verifies the final contract: the result is legal, still
// fails, and deleting any single operation whose deletion is legal makes
// the predicate stop returning Fail.
func checkOneMinimal(t *testing.T, result []int, pred Predicate) {
	t.Helper()
	if !Legal(result) {
		t.Fatalf("result %v is illegal", result)
	}
	if v := pred(result); v != Fail {
		t.Fatalf("result %v no longer fails (verdict %v)", result, v)
	}
	for i := range result {
		cand := append(append([]int{}, result[:i]...), result[i+1:]...)
		if !Legal(cand) {
			continue // deletion of this op is not a legal single deletion
		}
		if v := pred(cand); v == Fail {
			t.Fatalf("result %v is not 1-minimal: deleting index %d yields failing %v", result, i, cand)
		}
	}
}

// TestInjectedFaultCombination plants a fault that triggers only when the
// sequence creates id 1, references id 1, and creates id 2.
func TestInjectedFaultCombination(t *testing.T) {
	fault := func(seq []int) Verdict {
		if contains(seq, 1) && contains(seq, -1) && contains(seq, 2) {
			return Fail
		}
		return Pass
	}
	input := []int{1, -1, 2, 3, -3, 4, 5, -5, 0, 6}
	result, _ := Reduce(input, assertLegal(t, fault))
	checkOneMinimal(t, result, fault)
	if len(result) != 3 {
		t.Fatalf("expected minimal length 3, got %v", result)
	}
}

// TestCreationBeforeReference plants a fault that triggers only on a
// reference to id 2. The minimal legal failing sequence must keep the
// creation of 2 before the reference; the reducer must never drop the
// creation while keeping the reference.
func TestCreationBeforeReference(t *testing.T) {
	fault := func(seq []int) Verdict {
		if contains(seq, -2) {
			return Fail
		}
		return Pass
	}
	input := []int{7, 2, 8, -2, 9, -9, 2}
	result, stats := Reduce(input, assertLegal(t, fault))
	checkOneMinimal(t, result, fault)
	if len(result) != 2 || result[0] != 2 || result[1] != -2 {
		t.Fatalf("expected [2 -2], got %v", result)
	}
	if stats.SkippedIllegal == 0 {
		t.Fatalf("expected some illegal candidates to be skipped, stats %+v", stats)
	}
}

// TestUnknownVerdicts plants a fault whose predicate is undecidable on
// odd-length sequences. Unknown must be recorded and never accepted as
// still-failing.
func TestUnknownVerdicts(t *testing.T) {
	fault := func(seq []int) Verdict {
		if len(seq)%2 == 1 {
			return Unknown
		}
		if contains(seq, 3) && contains(seq, -3) {
			return Fail
		}
		return Pass
	}
	input := []int{1, 3, 4, -3, 5, -5, 6, 7}
	result, stats := Reduce(input, assertLegal(t, fault))
	checkOneMinimal(t, result, fault)
	if stats.Unknowns == 0 {
		t.Fatalf("expected unknown verdicts to be recorded, stats %+v", stats)
	}
	if len(stats.UnknownSeqs) != stats.Unknowns {
		t.Fatalf("unknown snapshots %d != unknown count %d", len(stats.UnknownSeqs), stats.Unknowns)
	}
	for _, s := range stats.UnknownSeqs {
		if len(s)%2 != 1 {
			t.Fatalf("recorded unknown sequence %v does not match the undecidable shape", s)
		}
	}
	// The accepted result must genuinely fail, not merely be unknown.
	if got := fault(result); got != Fail {
		t.Fatalf("accepted result %v has verdict %v, want fail", result, got)
	}
}

// TestUnknownNeverAccepted ensures a candidate whose only "failing"
// evaluations are unknown is not accepted as a reduction.
func TestUnknownNeverAccepted(t *testing.T) {
	fault := func(seq []int) Verdict {
		if len(seq) < 4 {
			return Unknown
		}
		if contains(seq, 1) && contains(seq, -1) {
			return Fail
		}
		return Pass
	}
	input := []int{1, 2, -1, 3, 4}
	result, stats := Reduce(input, assertLegal(t, fault))
	if stats.Unknowns == 0 {
		t.Fatalf("expected unknown verdicts, stats %+v", stats)
	}
	if got := fault(result); got != Fail {
		t.Fatalf("result %v verdict %v, want fail; unknown must not be accepted", result, got)
	}
	checkOneMinimal(t, result, fault)
}

// TestNonFailingInput returns the input unchanged when it does not fail.
func TestNonFailingInput(t *testing.T) {
	fault := func(seq []int) Verdict { return Pass }
	input := []int{1, -1, 2}
	result, stats := Reduce(input, fault)
	if len(result) != len(input) {
		t.Fatalf("expected input unchanged, got %v", result)
	}
	if stats.Calls != 1 {
		t.Fatalf("expected exactly 1 predicate call, got %d", stats.Calls)
	}
}

// TestLargerSequence exercises a longer input with an injected
// interaction fault among three ids.
func TestLargerSequence(t *testing.T) {
	fault := func(seq []int) Verdict {
		if contains(seq, 10) && contains(seq, -10) && contains(seq, 20) && contains(seq, -20) {
			return Fail
		}
		return Pass
	}
	input := []int{1, 2, 3, -3, 10, 4, 5, -5, 20, 6, -6, -10, 7, 8, -8, -20, 9, -9}
	result, _ := Reduce(input, assertLegal(t, fault))
	checkOneMinimal(t, result, fault)
	if len(result) != 4 {
		t.Fatalf("expected minimal length 4, got %v", result)
	}
}
