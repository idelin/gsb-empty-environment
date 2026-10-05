package fd

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) []FD {
	t.Helper()
	fds, err := ParseFDs(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return fds
}

func fdStrings(fds []FD) []string {
	out := make([]string, len(fds))
	for i, f := range fds {
		out[i] = f.String()
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// deletions returns all log entries that remove something; each must
// carry non-empty derivation evidence.
func deletions(t *testing.T, rep Report) []LogEntry {
	t.Helper()
	var out []LogEntry
	for _, e := range rep.Log {
		if e.Kind == RemoveExtraneousAttr || e.Kind == RemoveRedundantFD {
			if len(e.Evidence.Steps) == 0 {
				t.Errorf("deletion %q has no derivation evidence", e.Detail)
			}
			out = append(out, e)
		}
	}
	return out
}

func TestClosureBasic(t *testing.T) {
	fds := mustParse(t, "A->B; B->C; CD->E")
	c := ClosureOf(NewAttrSet("A"), fds)
	if !c.SubsetOf(c) || !NewAttrSet("A", "B", "C").SubsetOf(c) {
		t.Fatalf("A+ = %v, want superset of {A,B,C}", c)
	}
	if c.Contains("E") {
		t.Fatalf("A+ = %v, must not contain E", c)
	}
}

func TestClosureEmptyLHS(t *testing.T) {
	fds := mustParse(t, "{}->A; A->B")
	c, trace := Closure(NewAttrSet(), fds)
	if !c.Contains("A") || !c.Contains("B") {
		t.Fatalf("empty+ = %v, want {A,B}", c)
	}
	if len(trace.Steps) != 2 {
		t.Fatalf("trace = %v, want 2 steps", trace.Steps)
	}
}

func TestSplitRHS(t *testing.T) {
	fds := mustParse(t, "AB->CD")
	rep := Normalize(fds)
	want := []string{"A,B -> C", "A,B -> D"}
	if !equalStrings(fdStrings(rep.Canonical), want) {
		t.Fatalf("got %v, want %v", fdStrings(rep.Canonical), want)
	}
}

func TestExtraneousLHSAttr(t *testing.T) {
	// B is extraneous in AB->C because A->C exists.
	rep := Normalize(mustParse(t, "AB->C; A->C"))
	want := []string{"A -> C"}
	if !equalStrings(fdStrings(rep.Canonical), want) {
		t.Fatalf("got %v, want %v", fdStrings(rep.Canonical), want)
	}
	dels := deletions(t, rep)
	if len(dels) == 0 || dels[0].Kind != RemoveExtraneousAttr {
		t.Fatalf("expected an extraneous-attr deletion, log=%v", rep.Log)
	}
	if !strings.Contains(dels[0].Detail, `"B"`) {
		t.Fatalf("deletion should name attribute B: %q", dels[0].Detail)
	}
}

func TestExtraneousAttrBecomesEmptyLHS(t *testing.T) {
	// {}->A makes A extraneous in A->B? No: A->B needs A. But in
	// AB->C with {}->A and A->C, B is extraneous; and with {}->C
	// present, both A and B are extraneous, leaving {}->C.
	rep := Normalize(mustParse(t, "{}->C; AB->C"))
	want := []string{"{} -> C"}
	if !equalStrings(fdStrings(rep.Canonical), want) {
		t.Fatalf("got %v, want %v", fdStrings(rep.Canonical), want)
	}
}

func TestRedundantFD(t *testing.T) {
	rep := Normalize(mustParse(t, "A->B; B->C; A->C"))
	want := []string{"A -> B", "B -> C"}
	if !equalStrings(fdStrings(rep.Canonical), want) {
		t.Fatalf("got %v, want %v", fdStrings(rep.Canonical), want)
	}
	dels := deletions(t, rep)
	if len(dels) != 1 || dels[0].Kind != RemoveRedundantFD {
		t.Fatalf("expected exactly one redundant-FD deletion, log=%v", rep.Log)
	}
	// Evidence must show the derivation A -> B -> C.
	ev := dels[0].Evidence.Render()
	if !strings.Contains(ev, "A -> B") || !strings.Contains(ev, "B -> C") {
		t.Fatalf("evidence should derive C via A->B, B->C:\n%s", ev)
	}
}

func TestCyclicDependenciesSurvive(t *testing.T) {
	rep := Normalize(mustParse(t, "A->B; B->A"))
	want := []string{"A -> B", "B -> A"}
	if !equalStrings(fdStrings(rep.Canonical), want) {
		t.Fatalf("cyclic pair must survive, got %v", fdStrings(rep.Canonical))
	}
}

func TestCycleWithRedundantEdge(t *testing.T) {
	// A->B, B->A, A->C, B->C: A->C (or B->C) is redundant via the cycle.
	rep := Normalize(mustParse(t, "A->B; B->A; A->C; B->C"))
	if len(rep.Canonical) != 3 {
		t.Fatalf("got %v, want 3 FDs", fdStrings(rep.Canonical))
	}
	dels := deletions(t, rep)
	if len(dels) != 1 {
		t.Fatalf("expected exactly one deletion, log=%v", rep.Log)
	}
}

func TestTextbookCanonicalCover(t *testing.T) {
	// Classic example: {A->BC, B->C, A->B, AB->C} => {A->B, B->C}.
	rep := Normalize(mustParse(t, "A->BC; B->C; A->B; AB->C"))
	want := []string{"A -> B", "B -> C"}
	if !equalStrings(fdStrings(rep.Canonical), want) {
		t.Fatalf("got %v, want %v", fdStrings(rep.Canonical), want)
	}
}

func TestStableOutputPermutedInput(t *testing.T) {
	a := Normalize(mustParse(t, "A->BC; B->C; A->B; AB->C"))
	b := Normalize(mustParse(t, "AB->C; A->B; B->C; A->BC"))
	if !equalStrings(fdStrings(a.Canonical), fdStrings(b.Canonical)) {
		t.Fatalf("unstable output:\n%v\nvs\n%v", fdStrings(a.Canonical), fdStrings(b.Canonical))
	}
	if len(a.Log) != len(b.Log) {
		t.Fatalf("log length differs between permuted inputs: %d vs %d", len(a.Log), len(b.Log))
	}
}

func TestEquivalent(t *testing.T) {
	rep := Equivalent(mustParse(t, "A->B; B->C; A->C"), mustParse(t, "A->B; B->C"))
	if !rep.Equivalent {
		t.Fatalf("should be equivalent, failures=%v", rep.Failures)
	}
	if len(rep.Proofs) == 0 {
		t.Fatal("equivalence should record derivation proofs")
	}
}

func TestNotEquivalent(t *testing.T) {
	rep := Equivalent(mustParse(t, "A->B"), mustParse(t, "A->C"))
	if rep.Equivalent {
		t.Fatal("should not be equivalent")
	}
	if len(rep.Failures) != 2 {
		t.Fatalf("want 2 failures (one per direction), got %v", rep.Failures)
	}
}

func TestCanonicalEquivalentToOriginal(t *testing.T) {
	// The canonical cover must always be equivalent to the input.
	inputs := []string{
		"A->BC; B->C; A->B; AB->C",
		"{}->A; A->B; BC->D; B->C",
		"A->B; B->A; A->C; B->C; C->D; AD->E",
		"AB->C; C->A; BC->D; ACD->B; D->EG; BE->C; CG->BD; CE->AG",
	}
	for _, in := range inputs {
		fds := mustParse(t, in)
		rep := Normalize(fds)
		eq := Equivalent(fds, rep.Canonical)
		if !eq.Equivalent {
			t.Errorf("canonical cover of %q not equivalent: %v", in, eq.Failures)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{"A->", "AB", "A->B,C,->D"} {
		if _, err := ParseFDs(bad); err == nil {
			t.Errorf("expected parse error for %q", bad)
		}
	}
}

func TestNoDuplicateConfusionInEvidence(t *testing.T) {
	// AB->C reduces to B->C, duplicating the existing B->C. The
	// duplicate must be dropped by dedup, not by a redundancy proof
	// that cites an indistinguishable copy of itself.
	rep := Normalize(mustParse(t, "A->BC; B->C; A->B; AB->C; {}->D; D->E"))
	var sawDup bool
	for _, e := range rep.Log {
		if e.Kind == RemoveDuplicate {
			sawDup = true
		}
		if e.Kind == RemoveRedundantFD {
			for _, st := range e.Evidence.Steps {
				if st.Applied.String() == strings.Split(e.Detail, " ")[0][1:len(strings.Split(e.Detail, " ")[0])-1] {
					t.Errorf("redundancy evidence cites the FD itself: %q", e.Detail)
				}
			}
		}
	}
	if !sawDup {
		t.Error("expected a remove-duplicate log entry")
	}
	want := []string{"{} -> D", "{} -> E", "A -> B", "B -> C"} // sorted output
	if !equalStrings(fdStrings(rep.Canonical), want) {
		t.Fatalf("got %v, want %v", fdStrings(rep.Canonical), want)
	}
}
