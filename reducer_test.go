package opreducer

import (
	"reflect"
	"testing"
)

// noisyInput is a valid sequence whose only failure-relevant core is
// {+1, +2, -1, -2}; everything else is noise (neutral ops and
// create/reference pairs of unrelated resources).
var noisyInput = []int{
	4, -4, 0, 1, 5, -5, 2, 0, 6, -6,
	-1, 3, -3, -2, 0, 7, -7, 8, -8,
}

// bugOracle plants a fault: the program "crashes" iff the sequence
// references both resource 1 and resource 2.
func bugOracle(ops []int) Verdict {
	hasRef1, hasRef2 := false, false
	for _, op := range ops {
		if op == -1 {
			hasRef1 = true
		}
		if op == -2 {
			hasRef2 = true
		}
	}
	if hasRef1 && hasRef2 {
		return Fail
	}
	return Pass
}

func contains(ops []int, want int) bool {
	for _, op := range ops {
		if op == want {
			return true
		}
	}
	return false
}

// assertOneMinimal verifies local minimality: deleting any single element
// whose removal keeps the sequence valid must not reproduce the failure.
func assertOneMinimal(t *testing.T, ops []int, oracle Oracle) {
	t.Helper()
	for i := range ops {
		cand := make([]int, 0, len(ops)-1)
		cand = append(cand, ops[:i]...)
		cand = append(cand, ops[i+1:]...)
		if !Valid(cand) {
			continue
		}
		if v := oracle(cand); v == Fail {
			t.Fatalf("not 1-minimal: removing index %d yields still-failing %v", i, cand)
		}
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		ops   []int
		valid bool
	}{
		{nil, true},
		{[]int{0, 0}, true},
		{[]int{1, -1}, true},
		{[]int{1, 2, -2, -1}, true},
		{[]int{-1}, false},
		{[]int{-1, 1}, false},
		{[]int{1, -2}, false},
		{[]int{2, -2, -1}, false},
	}
	for _, c := range cases {
		if got := Valid(c.ops); got != c.valid {
			t.Errorf("Valid(%v) = %v, want %v", c.ops, got, c.valid)
		}
	}
}

func TestReducePlantedFault(t *testing.T) {
	got, st, err := Reduce(noisyInput, bugOracle)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	want := []int{1, 2, -1, -2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reduce = %v, want %v", got, want)
	}
	if !Valid(got) {
		t.Fatalf("result %v violates creation-before-reference", got)
	}
	if bugOracle(got) != Fail {
		t.Fatalf("result %v no longer fails", got)
	}
	if st.Calls == 0 || st.Fails == 0 {
		t.Fatalf("expected oracle activity, got %+v", st)
	}
	assertOneMinimal(t, got, bugOracle)
}

func TestConstraintMaintainedForEveryCandidate(t *testing.T) {
	var seen [][]int
	recording := func(ops []int) Verdict {
		seen = append(seen, append([]int(nil), ops...))
		return bugOracle(ops)
	}
	if _, _, err := Reduce(noisyInput, recording); err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("oracle never invoked")
	}
	for _, cand := range seen {
		if !Valid(cand) {
			t.Fatalf("reducer handed invalid candidate to oracle: %v", cand)
		}
	}
}

func TestUnknownRecordedAndNeverAccepted(t *testing.T) {
	// Planted flaky fault: any candidate still carrying resource 99 makes
	// the oracle indecisive. Unknown must not be treated as Fail.
	input := append([]int{99, -99}, noisyInput...)
	oracle := func(ops []int) Verdict {
		if reflect.DeepEqual(ops, input) {
			return bugOracle(ops) // decisive on the original run
		}
		if contains(ops, 99) {
			return Unknown
		}
		return bugOracle(ops)
	}
	got, st, err := Reduce(input, oracle)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	want := []int{1, 2, -1, -2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reduce = %v, want %v", got, want)
	}
	if st.Unknowns == 0 {
		t.Fatal("expected unknown verdicts to be recorded")
	}
	if st.Unknowns != len(st.UnknownCases) {
		t.Fatalf("Unknowns=%d but %d cases recorded", st.Unknowns, len(st.UnknownCases))
	}
	for _, c := range st.UnknownCases {
		if !contains(c, 99) {
			t.Fatalf("recorded unknown case %v should not have been unknown", c)
		}
	}
	assertOneMinimal(t, got, bugOracle)
}

func TestUnknownOnlyNeverReduces(t *testing.T) {
	// The oracle fails only on the exact initial sequence and is
	// indecisive about everything else: nothing may be deleted.
	initial := append([]int(nil), noisyInput...)
	oracle := func(ops []int) Verdict {
		if reflect.DeepEqual(ops, initial) {
			return Fail
		}
		return Unknown
	}
	got, st, err := Reduce(initial, oracle)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !reflect.DeepEqual(got, initial) {
		t.Fatalf("Reduce = %v, want unchanged %v", got, initial)
	}
	if st.Unknowns == 0 {
		t.Fatal("expected unknown verdicts to be recorded")
	}
}

func TestInvalidInitialRejected(t *testing.T) {
	if _, _, err := Reduce([]int{3, -3, -1}, bugOracle); err != ErrInvalidInput {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestNonFailingInputReturnedUnchanged(t *testing.T) {
	alwaysPass := func([]int) Verdict { return Pass }
	got, st, err := Reduce(noisyInput, alwaysPass)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if !reflect.DeepEqual(got, noisyInput) {
		t.Fatalf("Reduce = %v, want unchanged input", got)
	}
	if st.Calls != 1 {
		t.Fatalf("Calls = %d, want 1", st.Calls)
	}
}
