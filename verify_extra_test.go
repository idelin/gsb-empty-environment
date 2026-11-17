package netsec

import (
	"sync"
	"testing"
)

// TestInterpolateConcurrent exercises the stateless entry point from many
// goroutines; the -race detector flags any shared mutable state.
func TestInterpolateConcurrent(t *testing.T) {
	a := CNF{{Lit(1), Lit(2)}}
	b := CNF{{-Lit(1)}, {-Lit(2)}}
	shared := []int{1, 2}

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			it, w, err := Interpolate(a, b, shared)
			if err != nil || w != nil {
				t.Errorf("unexpected result: %v %v", w, err)
				return
			}
			if r := Verify(a, b, shared, it); !r.OK() {
				t.Errorf("verification failed: %s", r.Failure)
			}
		}()
	}
	wg.Wait()
}

// TestInterpolatePrivateSplit covers the case where both formulas are
// feasible under the same shared assignment while private variables carry
// the contradiction. The interpolant must still separate them using only
// the shared vocabulary.
func TestInterpolatePrivateSplit(t *testing.T) {
	// Shared x1; A-private x2 forces x1=true, B-private x3 forces x1=false.
	a := CNF{{-Lit(2), Lit(1)}, {Lit(2)}}
	b := CNF{{-Lit(3), -Lit(1)}, {Lit(3)}}

	it, w, err := Interpolate(a, b, []int{1})
	if err != nil || w != nil {
		t.Fatalf("unexpected witness/error: %v %v", w, err)
	}
	if len(it.Terms) != 1 || it.Terms[0][0] != Lit(1) {
		t.Fatalf("interpolant = %v, want x1", it)
	}
	if r := Verify(a, b, []int{1}, it); !r.OK() {
		t.Fatalf("verification failed: %s", r.Failure)
	}

	// The same mask x1=true is feasible for A and infeasible for B even
	// though B alone has a satisfying model elsewhere; assert both engines
	// agree on that enumeration fact.
	ass := []Literal{Lit(1)}
	aSat, _ := solveCNF(a, ass)
	bSat, _ := indSolve(b, ass)
	if !aSat || bSat {
		t.Fatalf("feasibility split wrong: aSat=%v bSat=%v", aSat, bSat)
	}
}

// TestVerifyRejectsForgedInterpolant proves the independent verifier does
// not rubber-stamp arbitrary formulas. A tampered interpolant that violates
// either property must be rejected with the corresponding flag cleared.
func TestVerifyRejectsForgedInterpolant(t *testing.T) {
	a := CNF{{Lit(1)}}
	b := CNF{{-Lit(1)}}

	good, _, err := Interpolate(a, b, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if r := Verify(a, b, []int{1}, good); !r.OK() {
		t.Fatalf("good interpolant rejected: %s", r.Failure)
	}

	// Forge the opposite truth table: claim false instead of x1. This breaks
	// A |= I on the x1=true assignment.
	forgedA := &Interpolant{Terms: nil, Shared: []int{1}}
	r := Verify(a, b, []int{1}, forgedA)
	if r.OK() || r.ImpliedByAOK {
		t.Fatalf("forged interpolant was accepted: %+v", r)
	}

	// Forge a tautology: both minterms. This breaks B |= not I.
	forgedB := &Interpolant{
		Terms:  []Term{{Lit(1)}, {-Lit(1)}},
		Shared: []int{1},
	}
	r = Verify(a, b, []int{1}, forgedB)
	if r.OK() || r.ContradictsBOK {
		t.Fatalf("tautology interpolant was accepted: %+v", r)
	}

	// A non-shared variable must be rejected on vocabulary grounds.
	leaked := &Interpolant{Terms: []Term{{Lit(2)}}, Shared: []int{1}}
	r = Verify(a, b, []int{1}, leaked)
	if r.OK() || r.VocabularyOK {
		t.Fatalf("vocabulary leak was accepted: %+v", r)
	}
}

// TestVerifyRejectsSatisfiablePair confirms no proof is certified when the
// joint unsat precondition does not hold.
func TestVerifyRejectsSatisfiablePair(t *testing.T) {
	a := CNF{{Lit(1), Lit(2)}}
	b := CNF{{-Lit(1), Lit(3)}}
	it := &Interpolant{Terms: []Term{{Lit(1)}}, Shared: []int{1, 2}}
	r := Verify(a, b, []int{1, 2}, it)
	if r.OK() || r.PreconditionOK {
		t.Fatalf("satisfiable pair certified as proof: %+v", r)
	}
}
