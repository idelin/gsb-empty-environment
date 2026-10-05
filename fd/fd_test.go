package fd

import (
	"reflect"
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

func keys(fds []FD) []string {
	out := make([]string, len(fds))
	for i, f := range fds {
		out[i] = f.Key()
	}
	return out
}

func TestParseTextbookAndNames(t *testing.T) {
	fds := mustParse(t, "AB -> C\nStudent, Course -> Grade\n{} -> D\n")
	want := []FD{
		NewFD([]string{"A", "B"}, []string{"C"}),
		NewFD([]string{"Course", "Student"}, []string{"Grade"}),
		NewFD(nil, []string{"D"}),
	}
	if !reflect.DeepEqual(fds, want) {
		t.Fatalf("got %v want %v", fds, want)
	}
}

func TestParseRejectsEmptyRHS(t *testing.T) {
	if _, err := ParseFDs("A -> {}"); err == nil {
		t.Fatal("expected error for empty RHS")
	}
}

func TestClosureBasic(t *testing.T) {
	fds := mustParse(t, "A -> B\nB -> C\n")
	got := Closure([]string{"A"}, fds)
	if !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Fatalf("closure = %v", got)
	}
}

func TestClosureEmptyLHSFiresEverywhere(t *testing.T) {
	fds := mustParse(t, "{} -> A\nA -> B\n")
	got := Closure(nil, fds)
	if !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Fatalf("closure of empty set = %v", got)
	}
	got = Closure([]string{"Z"}, fds)
	if !reflect.DeepEqual(got, []string{"A", "B", "Z"}) {
		t.Fatalf("closure of Z = %v", got)
	}
}

func TestClosureCyclicTerminates(t *testing.T) {
	fds := mustParse(t, "A -> B\nB -> A\nB -> C\n")
	got := Closure([]string{"A"}, fds)
	if !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Fatalf("closure = %v", got)
	}
}

func TestCoverSplitsRHS(t *testing.T) {
	res := CanonicalCover(mustParse(t, "A -> BC\n"))
	if got := keys(res.Cover); !reflect.DeepEqual(got, []string{"A->B", "A->C"}) {
		t.Fatalf("cover = %v", got)
	}
	splits := 0
	for _, c := range res.Changes {
		if c.Kind == ChangeSplit {
			splits++
		}
	}
	if splits != 2 {
		t.Fatalf("expected 2 split changes, got %d", splits)
	}
}

func TestCoverRemovesExtraneousAttribute(t *testing.T) {
	// B is extraneous in AB -> C because A -> B lets A derive C.
	res := CanonicalCover(mustParse(t, "A -> B\nAB -> C\n"))
	if got := keys(res.Cover); !reflect.DeepEqual(got, []string{"A->B", "A->C"}) {
		t.Fatalf("cover = %v", got)
	}
	var found *Change
	for i, c := range res.Changes {
		if c.Kind == ChangeExtraneous {
			found = &res.Changes[i]
		}
	}
	if found == nil || found.Attribute != "B" {
		t.Fatalf("expected extraneous B, changes = %v", res.Changes)
	}
	if len(found.Trace) == 0 {
		t.Fatal("extraneous-attribute change must carry derivation evidence")
	}
}

func TestCoverRemovesRedundantDependency(t *testing.T) {
	res := CanonicalCover(mustParse(t, "A -> B\nB -> C\nA -> C\n"))
	if got := keys(res.Cover); !reflect.DeepEqual(got, []string{"A->B", "B->C"}) {
		t.Fatalf("cover = %v", got)
	}
	var found *Change
	for i, c := range res.Changes {
		if c.Kind == ChangeRedundant {
			found = &res.Changes[i]
		}
	}
	if found == nil || found.Original.Key() != "A->C" {
		t.Fatalf("expected redundant A->C, changes = %v", res.Changes)
	}
	if len(found.Trace) == 0 {
		t.Fatal("redundant-dependency change must carry derivation evidence")
	}
}

// Cyclic set where both directions of a derived edge exist: exactly
// one of A->C / B->C may be dropped, otherwise C becomes unreachable.
func TestCoverCyclicRedundancyKeepsOneEdge(t *testing.T) {
	res := CanonicalCover(mustParse(t, "A -> B\nB -> A\nA -> C\nB -> C\n"))
	// Sorted elimination order drops A->C (checked before B->C).
	if got := keys(res.Cover); !reflect.DeepEqual(got, []string{"A->B", "B->A", "B->C"}) {
		t.Fatalf("cover = %v", got)
	}
	if !CoverEquivalent(mustParse(t, "A -> B\nB -> A\nA -> C\nB -> C\n"), res) {
		t.Fatal("cover lost equivalence")
	}
}

// Pure cycle A -> B -> C -> A: nothing is redundant, nothing removed.
func TestCoverPureCycleUnchanged(t *testing.T) {
	res := CanonicalCover(mustParse(t, "A -> B\nB -> C\nC -> A\n"))
	if got := keys(res.Cover); !reflect.DeepEqual(got, []string{"A->B", "B->C", "C->A"}) {
		t.Fatalf("cover = %v", got)
	}
}

// Empty LHS: {} -> A makes A extraneous in any left-hand side.
func TestCoverEmptyLHS(t *testing.T) {
	res := CanonicalCover(mustParse(t, "{} -> A\nAB -> C\n"))
	if got := keys(res.Cover); !reflect.DeepEqual(got, []string{"->A", "B->C"}) {
		t.Fatalf("cover = %v", got)
	}
	if !CoverEquivalent(mustParse(t, "{} -> A\nAB -> C\n"), res) {
		t.Fatal("cover lost equivalence")
	}
}

// A single-attribute LHS can itself be redundant, producing {} -> A.
func TestCoverGeneratesEmptyLHS(t *testing.T) {
	res := CanonicalCover(mustParse(t, "{} -> B\nB -> A\nA -> B\n"))
	// B -> A and A -> B cycle anchored at a constant: A -> B is
	// redundant ({} -> B gives B directly... actually {}+ = {B, A}),
	// and one of the cycle edges collapses.
	if !CoverEquivalent(mustParse(t, "{} -> B\nB -> A\nA -> B\n"), res) {
		t.Fatalf("cover %v lost equivalence", res.Cover)
	}
	for _, f := range res.Cover {
		if len(f.RHS) != 1 {
			t.Fatalf("cover contains non-singleton RHS: %v", f)
		}
	}
}

func TestCoverDropsTrivial(t *testing.T) {
	res := CanonicalCover(mustParse(t, "A -> A\nA -> B\n"))
	if got := keys(res.Cover); !reflect.DeepEqual(got, []string{"A->B"}) {
		t.Fatalf("cover = %v", got)
	}
}

// The cover must not depend on the input order.
func TestCoverStableUnderPermutation(t *testing.T) {
	a := mustParse(t, "AB -> C\nA -> B\nA -> C\nB -> C\n{} -> D\n")
	b := mustParse(t, "B -> C\n{} -> D\nA -> C\nA -> B\nAB -> C\n")
	ra, rb := CanonicalCover(a), CanonicalCover(b)
	if !reflect.DeepEqual(keys(ra.Cover), keys(rb.Cover)) {
		t.Fatalf("unstable cover: %v vs %v", keys(ra.Cover), keys(rb.Cover))
	}
	if len(ra.Changes) != len(rb.Changes) {
		t.Fatalf("unstable change log: %d vs %d", len(ra.Changes), len(rb.Changes))
	}
}

func TestEquivalentTrue(t *testing.T) {
	F := mustParse(t, "A -> B\nB -> C\n")
	G := mustParse(t, "A -> BC\nB -> C\n")
	if !CheckEquivalent(F, G).Equivalent {
		t.Fatal("expected equivalent")
	}
}

func TestEquivalentFalseGivesCounterexample(t *testing.T) {
	F := mustParse(t, "A -> B\n")
	G := mustParse(t, "A -> B\nB -> A\n")
	res := CheckEquivalent(F, G)
	if res.Equivalent {
		t.Fatal("expected not equivalent")
	}
	fails := 0
	for _, d := range res.FG {
		if !d.Derivable {
			fails++
			if d.FD.Key() != "B->A" {
				t.Fatalf("unexpected counterexample %v", d.FD)
			}
		}
	}
	if fails != 1 {
		t.Fatalf("expected exactly 1 counterexample, got %d", fails)
	}
}

func TestEquivalentEmptyLHS(t *testing.T) {
	F := mustParse(t, "{} -> A\n")
	G := mustParse(t, "B -> A\n{} -> A\n")
	if !CheckEquivalent(F, G).Equivalent {
		t.Fatal("B -> A adds nothing over {} -> A; expected equivalent")
	}
}
