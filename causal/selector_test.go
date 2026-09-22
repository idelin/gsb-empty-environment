package causal

import (
	"reflect"
	"sort"
	"testing"
)

func presentSet(refs ...Ref) map[Ref]bool {
	m := map[Ref]bool{}
	for _, r := range refs {
		m[r] = true
	}
	return m
}

// mustView builds a view with identical present/known sets.
func mustView(refs ...Ref) View {
	m := presentSet(refs...)
	return BuildView(m, m)
}

func blocksWant(bs []Block, want Ref) (Block, bool) {
	for _, b := range bs {
		if b.Want == want {
			return b, true
		}
	}
	return Block{}, false
}

// TestBDependsOnA encodes the headline safety rule: a snapshot that contains
// the new B write must contain the A write it depends on.
func TestBDependsOnA(t *testing.T) {
	reg := NewRegistry()
	a1, err := reg.Stamp("A", nil)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := reg.Stamp("B", []Ref{a1})
	if err != nil {
		t.Fatal(err)
	}

	// Replica holds B but not its cause A: B is unreadable.
	sel := Select(reg, mustView(b1), Request{ID: "r", Latest: []string{"B"}})
	if sel.Complete {
		t.Fatalf("expected incomplete slice, got %v", sel.Closure)
	}
	if len(sel.Closure) != 0 {
		t.Fatalf("B without A must not be selectable, closure=%v", sel.Closure)
	}
	blk, ok := blocksWant(sel.Blocking, b1)
	if !ok || blk.Cause != a1 {
		t.Fatalf("expected B@1 waits-for A@1, got %+v", sel.Blocking)
	}

	// Once A arrives, the slice includes both.
	sel = Select(reg, mustView(a1, b1), Request{ID: "r", Latest: []string{"B"}})
	if !sel.Complete {
		t.Fatalf("expected complete slice, blocks=%v", sel.Blocking)
	}
	if got := sel.Closure; got["A"] != 1 || got["B"] != 1 {
		t.Fatalf("closure must contain A@1 and B@1, got %v", got)
	}
}

// TestPrefixGap checks per-object prefix closure: holding A@3 without A@2
// exposes at most A@1, and A@2 is reported as the wait frontier.
func TestPrefixGap(t *testing.T) {
	reg := NewRegistry()
	a1, _ := reg.Stamp("A", nil)
	a2, _ := reg.Stamp("A", nil)
	a3, _ := reg.Stamp("A", nil)

	sel := Select(reg, mustView(a1, a3), Request{ID: "r", Latest: []string{"A"}})
	if sel.Complete {
		t.Fatal("slice with a gap must not be complete")
	}
	if sel.Closure["A"] != 1 {
		t.Fatalf("only A@1 is readable, got %v", sel.Closure)
	}
	blk, ok := blocksWant(sel.Blocking, a2)
	if !ok || blk.Cause != a2 || blk.Present {
		t.Fatalf("expected missing A@2 frontier, got %+v", sel.Blocking)
	}
}

// TestClosureExpansion checks that a pin on a dependent version pulls its
// entire transitive cause into the closure vector.
func TestClosureExpansion(t *testing.T) {
	reg := NewRegistry()
	a1, _ := reg.Stamp("A", nil)
	a2, _ := reg.Stamp("A", nil)
	b1, _ := reg.Stamp("B", []Ref{a2})
	c1, _ := reg.Stamp("C", []Ref{b1})

	sel := Select(reg, mustView(a1, a2, b1, c1), Request{ID: "r", Target: VV{"C": 1}})
	if !sel.Complete {
		t.Fatalf("expected complete, got %v", sel.Blocking)
	}
	want := VV{"A": 2, "B": 1, "C": 1}
	if !reflect.DeepEqual(sel.Closure, want) {
		t.Fatalf("closure=%v want %v", sel.Closure, want)
	}
	if !reflect.DeepEqual(sel.Selected, VV{"C": 1}) {
		t.Fatalf("selected should name only requested object, got %v", sel.Selected)
	}
}

// TestGreatestSlice checks that the selector returns the greatest consistent
// prefix below a bad top version: A@2 blocked, A@1 still served.
func TestGreatestSlice(t *testing.T) {
	reg := NewRegistry()
	a1, _ := reg.Stamp("A", nil)
	x1, _ := reg.Stamp("X", nil)
	a2, _ := reg.Stamp("A", []Ref{x1}) // A@2 needs X@1

	sel := Select(reg, mustView(a1, a2), Request{ID: "r", Latest: []string{"A"}})
	if sel.Closure["A"] != 1 {
		t.Fatalf("A@1 must remain readable, got %v", sel.Closure)
	}
	if sel.Complete {
		t.Fatal("A@2 with missing cause must block")
	}
	blk, _ := blocksWant(sel.Blocking, a2)
	if blk.Cause != x1 {
		t.Fatalf("root cause should be X@1, got %v", blk)
	}
}

// TestKnownButAbsent verifies the "advertised but not delivered" distinction:
// Known bounds a Latest request and produces a wait; an empty view cannot
// invent a newest version.
func TestKnownButAbsent(t *testing.T) {
	reg := NewRegistry()
	a1, _ := reg.Stamp("A", nil)
	known := map[Ref]bool{a1: true}

	sel := Select(reg, BuildView(presentSet(), known),
		Request{ID: "r", Latest: []string{"A"}})
	if sel.Complete || len(sel.Closure) != 0 {
		t.Fatalf("known-but-absent must block with empty slice, got %+v", sel)
	}
	if blk, ok := blocksWant(sel.Blocking, a1); !ok || blk.Cause != a1 || blk.Present {
		t.Fatalf("expected missing A@1 wait, got %+v", sel.Blocking)
	}

	// Nothing known at all: Latest is complete with an empty slice.
	sel = Select(reg, BuildView(presentSet(), presentSet()),
		Request{ID: "r", Latest: []string{"A"}})
	if !sel.Complete || len(sel.Closure) != 0 {
		t.Fatalf("unknown object latest is empty-complete, got %+v", sel)
	}
}

// TestPinnedBeyondPresent checks that a pin past the known frontier waits.
func TestPinnedBeyondPresent(t *testing.T) {
	reg := NewRegistry()
	a1, _ := reg.Stamp("A", nil)
	_, _ = reg.Stamp("A", nil) // a2

	sel := Select(reg, mustView(a1), Request{ID: "r", Target: VV{"A": 2}})
	if sel.Complete || sel.Closure["A"] != 1 {
		t.Fatalf("pin beyond present must wait above A@1, got %+v", sel)
	}
}

// TestRequestIsolation verifies that Select never mutates the input view and
// that two requests against the same view are independent.
func TestRequestIsolation(t *testing.T) {
	reg := NewRegistry()
	a1, _ := reg.Stamp("A", nil)
	b1, _ := reg.Stamp("B", []Ref{a1})
	present := presentSet(a1, b1)
	known := presentSet(a1, b1)
	view := BuildView(present, known)

	first := Select(reg, view, Request{ID: "one", Latest: []string{"A"}})
	second := Select(reg, view, Request{ID: "two", Latest: []string{"A", "B"}})
	_ = first
	if !reflect.DeepEqual(view.Present, present) || !reflect.DeepEqual(view.Known, known) {
		t.Fatal("Select mutated the shared view")
	}
	if second.Closure["B"] != 1 || second.Closure["A"] != 1 {
		t.Fatalf("second request should see both versions, got %v", second.Closure)
	}

	// Views built from the same source do not share backing maps.
	v1 := BuildView(present, known)
	v2 := BuildView(present, known)
	v1.Present[b1] = false
	if !v2.Present[b1] {
		t.Fatal("independent snapshots share the present map")
	}
}

// TestDeterministicOrdering checks blocking entries are stable/sorted.
func TestDeterministicOrdering(t *testing.T) {
	reg := NewRegistry()
	a1, _ := reg.Stamp("A", nil)
	b1, _ := reg.Stamp("B", []Ref{a1})
	sel := Select(reg, BuildView(presentSet(b1), presentSet(a1, b1)),
		Request{ID: "r", Latest: []string{"B"}})
	got := make([]string, len(sel.Blocking))
	for i, b := range sel.Blocking {
		got[i] = b.Want.Object
	}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("blocking entries not sorted: %v", got)
	}
}

// TestPinCapVsCausalCause covers the subtle rule the randomized simulation
// surfaced: a newer B version whose transitive closure needs A@2 cannot be
// served when the request pins A to 1, even though everything is present.
// The selector must lower B to the greatest prefix compatible with the cap.
func TestPinCapVsCausalCause(t *testing.T) {
	reg := NewRegistry()
	a1, _ := reg.Stamp("A", nil)
	a2, _ := reg.Stamp("A", nil)
	a3, _ := reg.Stamp("A", nil)
	b1, _ := reg.Stamp("B", []Ref{a1})
	b2, _ := reg.Stamp("B", []Ref{a2}) // b2 chain pulls A@2
	b3, _ := reg.Stamp("B", nil)
	b4, _ := reg.Stamp("B", []Ref{a1})
	b5, _ := reg.Stamp("B", []Ref{a1})
	_ = a3
	_ = b3
	_ = b4
	_ = b5

	all := presentSet(a1, a2, a3, b1, b2, b3, b4, b5)
	sel := Select(reg, BuildView(all, all),
		Request{ID: "pin", Latest: []string{"B"}, Target: VV{"A": 1}})
	// The newest B cannot be served under cap A<=1; the slice is lowered to
	// the greatest cap-compatible prefix and B@2 is reported as blocked on
	// the excluded A@2.
	if sel.Complete {
		t.Fatalf("newest B must block when it needs A@2 but A is pinned at 1")
	}
	if sel.Closure["A"] != 1 || sel.Closure["B"] != 1 {
		t.Fatalf("expected greatest cap-compatible slice {A:1 B:1}, got %v", sel.Closure)
	}
	if b, ok := blocksWant(sel.Blocking, b2); !ok || b.Cause != a2 {
		t.Fatalf("expected B@2 waits-for A@2, got %+v", sel.Blocking)
	}

	// With the A cap lifted to 2 the blocked B@2 becomes includable; the
	// newest B is reachable since later prefixes only need A@1.
	sel = Select(reg, BuildView(all, all),
		Request{ID: "pin2", Latest: []string{"B"}, Target: VV{"A": 2}})
	if !sel.Complete {
		t.Fatalf("expected complete with cap A<=2, blocks=%v", sel.Blocking)
	}
	if sel.Closure["A"] != 2 || sel.Closure["B"] < 2 {
		t.Fatalf("expected closure to reach A@2 and B@2, got %v", sel.Closure)
	}
}

// TestCausalClosureNoSeqPruning is a regression test for the traversal bug
// where visiting a higher seq suppressed expansion of a lower node's deps.
func TestCausalClosureNoSeqPruning(t *testing.T) {
	reg := NewRegistry()
	a1, _ := reg.Stamp("A", nil)
	a2, _ := reg.Stamp("A", nil)
	_, _ = reg.Stamp("B", []Ref{a1})
	_, _ = reg.Stamp("B", []Ref{a2})
	b3, _ := reg.Stamp("B", nil) // b3 -> b2 -> a2 (through auto predecessor)
	cl := causalClosure(reg, b3)
	if cl["A"] != 2 {
		t.Fatalf("closure of B@3 must reach A@2 via B@2, got %v", cl)
	}
	if cl["B"] != 3 {
		t.Fatalf("closure of B@3 must include B@3, got %v", cl)
	}
}
