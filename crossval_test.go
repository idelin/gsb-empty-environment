package tdeq

import (
	"math/rand"
	"testing"
)

func randomTransducer(r *rand.Rand, syms []rune, outputs []string) *Transducer {
	n := 1 + r.Intn(4)
	tr, err := New(n, r.Intn(n))
	if err != nil {
		panic(err)
	}
	for state := 0; state < n; state++ {
		for _, sym := range syms {
			if r.Float64() < 0.7 {
				if err := tr.AddEdge(state, sym, r.Intn(n), outputs[r.Intn(len(outputs))]); err != nil {
					panic(err)
				}
			}
		}
	}
	return tr
}

// TestRandomCrossValidation compares the product-graph checker against
// brute-force enumeration of all bounded-length inputs on random
// transducers with empty outputs and missing transitions.
func TestRandomCrossValidation(t *testing.T) {
	r := rand.New(rand.NewSource(20261006))
	syms := []rune{'x', 'y'}
	outputs := []string{"", "a", "b", "ab", "ba", "aa"}

	for trial := 0; trial < 600; trial++ {
		a := randomTransducer(r, syms, outputs)
		b := randomTransducer(r, syms, outputs)
		bound := r.Intn(6)

		c, err := NewChecker(a, b, bound)
		if err != nil {
			t.Fatalf("NewChecker: %v", err)
		}
		got := c.Run(-1)
		want := CheckExhaustive(a, b, bound)

		if got.Status != want.Status {
			t.Fatalf("trial %d bound %d: checker=%v exhaustive=%v (witness %q)",
				trial, bound, got.Status, want.Status, want.Witness)
		}
		if got.Status == Inequivalent {
			assertWitness(t, a, b, bound, got)
		}
	}
}

// TestBudgetResume runs the checker in tiny budget slices and verifies
// the final result matches an unlimited run; it also exercises frontier
// snapshot/recovery mid-exploration.
func TestBudgetResume(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	syms := []rune{'x', 'y'}
	outputs := []string{"", "a", "b", "ab"}

	for trial := 0; trial < 200; trial++ {
		a := randomTransducer(r, syms, outputs)
		b := randomTransducer(r, syms, outputs)
		bound := 1 + r.Intn(5)

		ref, err := NewChecker(a, b, bound)
		if err != nil {
			t.Fatalf("NewChecker: %v", err)
		}
		want := ref.Run(-1)

		// Advance one expansion at a time; must terminate with the
		// same definitive result.
		slow, err := NewChecker(a, b, bound)
		if err != nil {
			t.Fatalf("NewChecker: %v", err)
		}
		var got Result
		sawUnknown := false
		for steps := 0; steps < 100000; steps++ {
			got = slow.Run(1)
			if got.Status == Unknown {
				sawUnknown = true
				continue
			}
			break
		}
		if got.Status != want.Status {
			t.Fatalf("trial %d: sliced run got %v, unlimited got %v", trial, got.Status, want.Status)
		}
		if !slow.Done() {
			t.Fatalf("trial %d: checker not done after definitive result", trial)
		}
		if sawUnknown && slow.FrontierSize() != 0 && !slow.Done() {
			t.Fatalf("trial %d: inconsistent frontier state", trial)
		}

		// Snapshot mid-exploration (when possible), resume, and check
		// the recovered checker reaches the same conclusion.
		fresh, err := NewChecker(a, b, bound)
		if err != nil {
			t.Fatalf("NewChecker: %v", err)
		}
		if res := fresh.Run(1); res.Status == Unknown {
			snap := fresh.Snapshot()
			restored, err := Resume(a, b, snap)
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			if restored.FrontierSize() != fresh.FrontierSize() {
				t.Fatalf("trial %d: frontier size %d after resume, want %d",
					trial, restored.FrontierSize(), fresh.FrontierSize())
			}
			if res := restored.Run(-1); res.Status != want.Status {
				t.Fatalf("trial %d: resumed run got %v, want %v", trial, res.Status, want.Status)
			}
		}
	}
}

// TestBudgetExhaustionIsUnknown checks the unknown/frontier contract on
// a machine pair with a nontrivial frontier.
func TestBudgetExhaustionIsUnknown(t *testing.T) {
	a := mustNew(t, 1, 0)
	mustEdge(t, a, 0, 'x', 0, "ab")
	mustEdge(t, a, 0, 'y', 0, "")
	b := mustNew(t, 2, 0)
	mustEdge(t, b, 0, 'x', 1, "")
	mustEdge(t, b, 1, 'x', 0, "abab")
	mustEdge(t, b, 0, 'y', 0, "")
	mustEdge(t, b, 1, 'y', 1, "")

	c, err := NewChecker(a, b, 6)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	if res := c.Run(0); res.Status != Unknown {
		t.Fatalf("zero budget: got %v, want unknown", res.Status)
	}
	if c.FrontierSize() == 0 {
		t.Fatalf("frontier should be non-empty after zero budget")
	}
	snap := c.Snapshot()
	restored, err := Resume(a, b, snap)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if res := restored.Run(-1); res.Status != c.Run(-1).Status {
		t.Fatalf("resumed result differs from in-place result")
	}
}
