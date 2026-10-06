package tdeq

import (
	"testing"
	"unicode/utf8"
)

func mustNew(t *testing.T, states, initial int) *Transducer {
	t.Helper()
	tr, err := New(states, initial)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr
}

func mustEdge(t *testing.T, tr *Transducer, from int, sym rune, to int, out string) {
	t.Helper()
	if err := tr.AddEdge(from, sym, to, out); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
}

func check(t *testing.T, a, b *Transducer, bound int) Result {
	t.Helper()
	c, err := NewChecker(a, b, bound)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	return c.Run(-1)
}

// assertWitness re-verifies a reported counterexample by complete
// execution of both machines.
func assertWitness(t *testing.T, a, b *Transducer, bound int, res Result) {
	t.Helper()
	if res.Status != Inequivalent {
		t.Fatalf("expected inequivalent, got %v", res.Status)
	}
	if n := utf8.RuneCountInString(res.Witness); n != bound {
		t.Fatalf("witness %q has length %d, want %d", res.Witness, n, bound)
	}
	outA, okA := a.Run(res.Witness)
	outB, okB := b.Run(res.Witness)
	if okA == okB && (!okA || outA == outB) {
		t.Fatalf("witness %q does not reproduce: (%q,%v) vs (%q,%v)",
			res.Witness, outA, okA, outB, okB)
	}
	if res.ErrA != !okA || res.ErrB != !okB {
		t.Fatalf("error flags mismatch: got (%v,%v), want (%v,%v)",
			res.ErrA, res.ErrB, !okA, !okB)
	}
	if okA && res.OutA != outA || okB && res.OutB != outB {
		t.Fatalf("reported outputs mismatch")
	}
}

// TestTimingSlack uses machines whose per-edge outputs never match, yet
// whose concatenated outputs coincide at even input lengths. A naive
// per-edge comparison would wrongly reject them.
func TestTimingSlack(t *testing.T) {
	a := mustNew(t, 1, 0)
	mustEdge(t, a, 0, 'x', 0, "ab")

	b := mustNew(t, 2, 0)
	mustEdge(t, b, 0, 'x', 1, "")
	mustEdge(t, b, 1, 'x', 0, "abab")

	if res := check(t, a, b, 2); res.Status != Equivalent {
		t.Fatalf("bound 2: got %v, want equivalent", res.Status)
	}
	if res := check(t, a, b, 4); res.Status != Equivalent {
		t.Fatalf("bound 4: got %v, want equivalent", res.Status)
	}
	res := check(t, a, b, 3)
	assertWitness(t, a, b, 3, res)
	if res.OutA != "ababab" || res.OutB != "abab" {
		t.Fatalf("bound 3: outputs (%q, %q)", res.OutA, res.OutB)
	}
	res = check(t, a, b, 1)
	assertWitness(t, a, b, 1, res)
}

// TestMissingTransitionIsError: a missing transition is an error output,
// distinct from any regular output but equal to another error output.
func TestMissingTransitionIsError(t *testing.T) {
	a := mustNew(t, 1, 0)
	mustEdge(t, a, 0, 'x', 0, "a")

	b := mustNew(t, 1, 0) // no transitions at all

	res := check(t, a, b, 1)
	assertWitness(t, a, b, 1, res)
	if res.ErrB != true || res.ErrA != false {
		t.Fatalf("error flags: got (%v, %v)", res.ErrA, res.ErrB)
	}

	// Both machines error on every input: equal everywhere.
	c := mustNew(t, 1, 0)
	if res := check(t, b, c, 5); res.Status != Equivalent {
		t.Fatalf("both erroring: got %v, want equivalent", res.Status)
	}
}

// TestDivergedThenBothError: cumulative outputs conflict at depth 1, but
// every length-2 extension makes both machines error, so at bound 2 the
// machines are equivalent. The diverged branch must be explored, not
// reported immediately, and pruned once both sides error.
func TestDivergedThenBothError(t *testing.T) {
	a := mustNew(t, 2, 0)
	mustEdge(t, a, 0, 'x', 1, "a")

	b := mustNew(t, 2, 0)
	mustEdge(t, b, 0, 'x', 1, "b")

	if res := check(t, a, b, 2); res.Status != Equivalent {
		t.Fatalf("bound 2: got %v (%+v), want equivalent", res.Status, res)
	}
	res := check(t, a, b, 1)
	assertWitness(t, a, b, 1, res)
	if res.OutA != "a" || res.OutB != "b" {
		t.Fatalf("bound 1: outputs (%q, %q)", res.OutA, res.OutB)
	}
}

// TestOneSidedErrorRecovery: A errors at depth 1 while B survives; any
// extension where B also errors is equal, but B has one surviving path,
// which is a counterexample at bound 2.
func TestOneSidedErrorRecovery(t *testing.T) {
	a := mustNew(t, 2, 0)
	mustEdge(t, a, 0, 'x', 1, "a") // state 1 has no outgoing transitions

	b := mustNew(t, 2, 0)
	mustEdge(t, b, 0, 'x', 1, "a")
	mustEdge(t, b, 1, 'x', 1, "b")

	// "xx": A errors at step 2, B outputs "ab".
	res := check(t, a, b, 2)
	assertWitness(t, a, b, 2, res)
	if !res.ErrA || res.ErrB {
		t.Fatalf("error flags: got (%v, %v)", res.ErrA, res.ErrB)
	}
	// bound 1: both output "a" on "x".
	if res := check(t, a, b, 1); res.Status != Equivalent {
		t.Fatalf("bound 1: got %v, want equivalent", res.Status)
	}
}

func TestRunBasics(t *testing.T) {
	tr := mustNew(t, 2, 0)
	mustEdge(t, tr, 0, 'x', 1, "he")
	mustEdge(t, tr, 1, 'y', 0, "")
	mustEdge(t, tr, 0, 'y', 0, "llo")

	out, ok := tr.Run("xyx")
	if !ok || out != "hehe" {
		t.Fatalf("Run(xyx): got (%q, %v)", out, ok)
	}
	if _, ok := tr.Run("xxy"); ok {
		t.Fatalf("expected error output for missing transition")
	}
	if out, ok := tr.Run(""); !ok || out != "" {
		t.Fatalf("empty input: got (%q, %v)", out, ok)
	}
	if _, err := New(0, 0); err == nil {
		t.Fatalf("expected error for zero states")
	}
	if err := tr.AddEdge(0, 'z', 5, ""); err == nil {
		t.Fatalf("expected error for out-of-range target")
	}
}

func TestEmptyAlphabet(t *testing.T) {
	a := mustNew(t, 1, 0)
	b := mustNew(t, 2, 0)
	if res := check(t, a, b, 3); res.Status != Equivalent {
		t.Fatalf("no symbols: got %v, want equivalent", res.Status)
	}
	if res := check(t, a, b, 0); res.Status != Equivalent {
		t.Fatalf("bound 0: got %v, want equivalent", res.Status)
	}
}
