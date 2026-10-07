package dfeq

import (
	"testing"
	"time"
)

// evenA builds a DFA accepting words with an even number of "a".
func evenA() *DFA {
	d := NewDFA("evenA", []string{"a", "b"}, "even")
	d.SetTrans("even", "a", "odd")
	d.SetTrans("odd", "a", "even")
	d.SetTrans("even", "b", "even")
	d.SetTrans("odd", "b", "odd")
	d.Accept["even"] = true
	return d
}

// evenARedundant accepts the same language with a redundant split state.
func evenARedundant() *DFA {
	d := NewDFA("evenARedundant", []string{"a", "b"}, "e1")
	d.SetTrans("e1", "a", "o")
	d.SetTrans("e1", "b", "e2")
	d.SetTrans("e2", "a", "o")
	d.SetTrans("e2", "b", "e1")
	d.SetTrans("o", "a", "e1")
	d.SetTrans("o", "b", "o")
	d.Accept["e1"] = true
	d.Accept["e2"] = true
	return d
}

func TestEquivalentDifferentStructure(t *testing.T) {
	res := Compare(evenA(), evenARedundant())
	if res.Kind != Equivalent {
		t.Fatalf("expected Equivalent, got %v (word=%q blocking=%v)", res.Kind, res.WordString(), res.Blocking)
	}
	// Independent brute-force核对 via short-word enumeration.
	if w := CrossCheck(evenA(), evenARedundant(), 6); w != nil {
		t.Fatalf("enumeration found distinguishing word %q", w)
	}
}

func TestEmptyWordChecked(t *testing.T) {
	a := NewDFA("a", []string{"x"}, "s")
	a.Accept["s"] = true                 // accepts ε
	b := NewDFA("b", []string{"x"}, "t") // rejects ε
	res := Compare(a, b)
	if res.Kind != Counterexample {
		t.Fatalf("expected Counterexample, got %v", res.Kind)
	}
	if len(res.Word) != 0 {
		t.Fatalf("expected empty distinguishing word, got %q", res.WordString())
	}
	if len(res.Path) != 1 || res.Path[0] != [2]string{"s", "t"} {
		t.Fatalf("bad path for empty word: %v", res.Path)
	}
	if !a.Simulate(res.Word) || b.Simulate(res.Word) {
		t.Fatal("empty word must be accepted by exactly one side")
	}
}

func TestMissingTransitionsGoToSink(t *testing.T) {
	// a: partial transition function, missing entries imply the sink.
	a := NewDFA("partial", []string{"a", "b"}, "s")
	a.SetTrans("s", "a", "s") // b missing -> sink
	a.Accept["s"] = true
	// b: explicit rejecting sink with self loops.
	b := NewDFA("explicit", []string{"a", "b"}, "q0")
	b.SetTrans("q0", "a", "q0")
	b.SetTrans("q0", "b", "sink")
	b.SetTrans("sink", "a", "sink")
	b.SetTrans("sink", "b", "sink")
	b.Accept["q0"] = true
	if res := Compare(a, b); res.Kind != Equivalent {
		t.Fatalf("implicit sink != explicit sink: %v word=%q", res.Kind, res.WordString())
	}
	// And a difference through the sink is detected: c keeps accepting in its sink.
	c := NewDFA("c", []string{"a", "b"}, "q0")
	c.SetTrans("q0", "a", "q0")
	c.SetTrans("q0", "b", "acc")
	c.SetTrans("acc", "a", "acc")
	c.SetTrans("acc", "b", "acc")
	c.Accept["q0"] = true
	c.Accept["acc"] = true
	res := Compare(a, c)
	if res.Kind != Counterexample || res.WordString() != "b" {
		t.Fatalf("expected counterexample \"b\", got %v %q", res.Kind, res.WordString())
	}
	if a.Simulate(res.Word) || !c.Simulate(res.Word) {
		t.Fatal("counterexample must be rejected by a and accepted by c")
	}
}

func TestShortestAndLexicographic(t *testing.T) {
	// a accepts exactly {"ab", "ba"}; b accepts nothing.
	a := NewDFA("a", []string{"a", "b"}, "s")
	a.SetTrans("s", "a", "sa")
	a.SetTrans("s", "b", "sb")
	a.SetTrans("sa", "b", "acc")
	a.SetTrans("sb", "a", "acc")
	a.Accept["acc"] = true
	b := NewDFA("b", []string{"a", "b"}, "t") // empty language

	res := Compare(a, b)
	if res.Kind != Counterexample {
		t.Fatalf("expected Counterexample, got %v", res.Kind)
	}
	// "ba" also distinguishes, but "ab" is lexicographically smaller.
	if got := res.WordString(); got != "a b" {
		t.Fatalf("expected shortlex-minimal word [a b], got %q", got)
	}
	// Path: product states from (s,t) along a,b.
	wantPath := [][2]string{{"s", "t"}, {"sa", sinkName}, {"acc", sinkName}}
	if len(res.Path) != len(wantPath) {
		t.Fatalf("path length %d, want %d", len(res.Path), len(wantPath))
	}
	for i, p := range wantPath {
		if res.Path[i] != p {
			t.Fatalf("path[%d]=%v, want %v", i, res.Path[i], p)
		}
	}
	// Final product state must differ in acceptance.
	last := res.Path[len(res.Path)-1]
	if a.Accepting(last[0]) == b.Accepting(last[1]) {
		t.Fatal("final product state must differ in acceptance")
	}
	// Enumeration核对: no shorter/smaller word distinguishes.
	if w := CrossCheck(a, b, len(res.Word)); !equalWord(w, res.Word) {
		t.Fatalf("enumeration found %q, BFS found %q", w, res.Word)
	}
	// Minimality verification accepts the BFS word, rejects "ba".
	if ok, why := VerifyCounterexample(a, b, res.Word); !ok {
		t.Fatalf("VerifyCounterexample rejected valid word: %s", why)
	}
	if ok, _ := VerifyCounterexample(a, b, []string{"b", "a"}); ok {
		t.Fatal("VerifyCounterexample accepted non-minimal word [b a]")
	}
	if ok, _ := VerifyCounterexample(a, b, []string{"a", "a"}); ok {
		t.Fatal("VerifyCounterexample accepted a non-distinguishing word")
	}
}

func TestUnknownWhileFragmentsUnexpanded(t *testing.T) {
	a := NewDFA("a", []string{"x"}, "s")
	a.AttachFragment("s", "frag-1")
	b := NewDFA("b", []string{"x"}, "t") // rejects everything
	res := Compare(a, b)
	if res.Kind != Unknown {
		t.Fatalf("must not claim equivalence before full expansion, got %v", res.Kind)
	}
	if len(res.Blocking) != 1 || res.Blocking[0] != "frag-1" {
		t.Fatalf("blocking=%v, want [frag-1]", res.Blocking)
	}
}

func TestIncrementalAsyncResolution(t *testing.T) {
	// a has an external fragment at s; once expanded it equals evenA.
	a := NewDFA("a", []string{"a", "b"}, "s")
	a.AttachFragment("s", "frag-s")
	b := evenA()

	c := NewComparer(a, b)

	// Old query bound to the pre-expansion versions.
	q := c.NewQuery()
	oldVersions := q.Versions()

	res := c.Compare()
	if res.Kind != Unknown || len(res.Blocking) != 1 || res.Blocking[0] != "frag-s" {
		t.Fatalf("expected Unknown[frag-s], got %v %v", res.Kind, res.Blocking)
	}
	if res.Versions != oldVersions {
		t.Fatalf("result versions %v, want %v", res.Versions, oldVersions)
	}

	// Fragment is filled asynchronously.
	tru := true
	go func() {
		time.Sleep(20 * time.Millisecond)
		c.ResolveFragment(0, "s", "frag-s", Expansion{
			HostAccept: &tru,
			Accept:     map[string]bool{"s2": false},
			Trans: map[string]map[string]string{
				"s":  {"a": "s2", "b": "s"},
				"s2": {"a": "s", "b": "s2"},
			},
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		res = c.Compare()
		if res.Kind != Unknown || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if res.Kind != Equivalent {
		t.Fatalf("after expansion expected Equivalent, got %v %v", res.Kind, res.Blocking)
	}
	if res.Versions == oldVersions {
		t.Fatal("versions must advance after fragment resolution")
	}
	// Full product-graph validation of the incremental result.
	if !ValidateResult(a, b, res) {
		t.Fatal("incremental result disagrees with full product graph")
	}
	if w := CrossCheck(a, b, 6); w != nil {
		t.Fatalf("enumeration found distinguishing word %q after expansion", w)
	}

	// The old query stays bound to the old versions and still reports Unknown.
	oldRes := q.Result()
	if oldRes.Kind != Unknown {
		t.Fatalf("old query must stay Unknown, got %v", oldRes.Kind)
	}
	if q.Versions() != oldVersions {
		t.Fatalf("old query versions drifted: %v", q.Versions())
	}
}

func TestIncrementalResolutionRevealsCounterexample(t *testing.T) {
	a := NewDFA("a", []string{"a"}, "s")
	a.AttachFragment("s", "frag-s")
	b := NewDFA("b", []string{"a"}, "t") // rejects everything

	c := NewComparer(a, b)
	if res := c.Compare(); res.Kind != Unknown {
		t.Fatalf("expected Unknown, got %v", res.Kind)
	}
	// Expansion makes s accepting: ε now distinguishes.
	tru := true
	c.ResolveFragment(0, "s", "frag-s", Expansion{HostAccept: &tru})
	res := c.Compare()
	if res.Kind != Counterexample || len(res.Word) != 0 {
		t.Fatalf("expected empty-word counterexample, got %v %q", res.Kind, res.WordString())
	}
	if !a.Simulate(res.Word) || b.Simulate(res.Word) {
		t.Fatal("counterexample must be accepted by exactly one side")
	}
	if !ValidateResult(a, b, res) {
		t.Fatal("incremental counterexample disagrees with full product graph")
	}
}

func TestCounterexampleThroughProductPath(t *testing.T) {
	// evenA vs "even number of a AND even number of b": first difference is "b".
	a := evenA()
	b := NewDFA("b", []string{"a", "b"}, "ee")
	b.SetTrans("ee", "a", "oe")
	b.SetTrans("oe", "a", "ee")
	b.SetTrans("ee", "b", "eo")
	b.SetTrans("eo", "b", "ee")
	b.SetTrans("oe", "b", "oo")
	b.SetTrans("oo", "b", "oe")
	b.SetTrans("eo", "a", "oo")
	b.SetTrans("oo", "a", "eo")
	b.Accept["ee"] = true

	res := Compare(a, b)
	if res.Kind != Counterexample || res.WordString() != "b" {
		t.Fatalf("expected counterexample \"b\", got %v %q", res.Kind, res.WordString())
	}
	// Replay the word along the reported product path.
	pa, pb := a.Start, b.Start
	for i, sym := range res.Word {
		if res.Path[i] != [2]string{pa, pb} {
			t.Fatalf("path[%d]=%v, want {%s %s}", i, res.Path[i], pa, pb)
		}
		pa, pb = a.Step(pa, sym), b.Step(pb, sym)
	}
	if res.Path[len(res.Word)] != [2]string{pa, pb} {
		t.Fatal("path does not end at the simulated product state")
	}
	if !a.Simulate(res.Word) || b.Simulate(res.Word) {
		t.Fatal("word must be accepted by a and rejected by b")
	}
	if ok, why := VerifyCounterexample(a, b, res.Word); !ok {
		t.Fatalf("VerifyCounterexample: %s", why)
	}
}
