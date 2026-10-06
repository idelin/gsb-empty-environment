package dfacmp

import (
	"math/rand"
	"testing"
)

// evenA accepts words over {a} with an even number of a's, using 2 states.
func evenA() *Automaton {
	return &Automaton{
		Name:     "evenA",
		Alphabet: []Symbol{"a"},
		Start:    0,
		States: []State{
			{Accepting: true, Transitions: map[Symbol]StateID{"a": 1}},
			{Accepting: false, Transitions: map[Symbol]StateID{"a": 0}},
		},
	}
}

// evenAUnrolled accepts the same language as evenA with 4 states.
func evenAUnrolled() *Automaton {
	return &Automaton{
		Name:     "evenAUnrolled",
		Alphabet: []Symbol{"a"},
		Start:    0,
		States: []State{
			{Accepting: true, Transitions: map[Symbol]StateID{"a": 1}},
			{Accepting: false, Transitions: map[Symbol]StateID{"a": 2}},
			{Accepting: true, Transitions: map[Symbol]StateID{"a": 3}},
			{Accepting: false, Transitions: map[Symbol]StateID{"a": 0}},
		},
	}
}

func TestEquivalentDifferentStructure(t *testing.T) {
	a, b := evenA(), evenAUnrolled()
	res := Compare(a, b)
	if res.Kind != Equivalent {
		t.Fatalf("got %v, want Equivalent", res)
	}
	if err := EnumerateCheck(a, b, 8, res); err != nil {
		t.Fatal(err)
	}
	if err := FullProductCheck(a, b, res); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyStringChecked(t *testing.T) {
	a := &Automaton{
		Name: "A", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{{Accepting: true}},
	}
	b := &Automaton{
		Name: "B", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{{Accepting: false}},
	}
	res := Compare(a, b)
	if res.Kind != Counterexample {
		t.Fatalf("got %v, want Counterexample", res)
	}
	if len(res.Word) != 0 {
		t.Fatalf("word = %v, want empty", res.Word)
	}
	if res.AcceptedBy != SideA {
		t.Fatalf("AcceptedBy = %v, want SideA", res.AcceptedBy)
	}
	if len(res.Path) != 1 || res.Path[0].At != (ProductState{0, 0}) || res.Path[0].Via != "" {
		t.Fatalf("bad path: %+v", res.Path)
	}
	if err := VerifyCounterexample(a, b, res); err != nil {
		t.Fatal(err)
	}
}

func TestMissingTransitionSink(t *testing.T) {
	// A accepts exactly "a"; the missing transition from state 1 on "a"
	// falls into the rejecting sink.
	a := &Automaton{
		Name: "A", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1}},
			{Accepting: true},
		},
	}
	// B accepts a+.
	b := &Automaton{
		Name: "B", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1}},
			{Accepting: true, Transitions: map[Symbol]StateID{"a": 1}},
		},
	}
	res := Compare(a, b)
	if res.Kind != Counterexample {
		t.Fatalf("got %v, want Counterexample", res)
	}
	if !equalWord(res.Word, []Symbol{"a", "a"}) {
		t.Fatalf("word = %v, want [a a]", res.Word)
	}
	if res.AcceptedBy != SideB {
		t.Fatalf("AcceptedBy = %v, want SideB", res.AcceptedBy)
	}
	// The path must dive into the sink on side A.
	last := res.Path[len(res.Path)-1]
	if last.At.A != Sink || last.At.B != 1 || last.Via != "a" {
		t.Fatalf("bad last path step: %+v", last)
	}
	if err := VerifyCounterexample(a, b, res); err != nil {
		t.Fatal(err)
	}
	if err := EnumerateCheck(a, b, len(res.Word), res); err != nil {
		t.Fatal(err)
	}
}

func TestLexicographicallySmallest(t *testing.T) {
	// A accepts {"b"}, B accepts {"a"}; both "a" and "b" distinguish them,
	// and "a" is lexicographically smaller.
	a := &Automaton{
		Name: "A", Alphabet: []Symbol{"a", "b"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"b": 1}},
			{Accepting: true},
		},
	}
	b := &Automaton{
		Name: "B", Alphabet: []Symbol{"a", "b"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1}},
			{Accepting: true},
		},
	}
	res := Compare(a, b)
	if res.Kind != Counterexample || !equalWord(res.Word, []Symbol{"a"}) {
		t.Fatalf("got %v, want counterexample [a]", res)
	}
	if res.AcceptedBy != SideB {
		t.Fatalf("AcceptedBy = %v, want SideB", res.AcceptedBy)
	}
	if err := EnumerateCheck(a, b, 3, res); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownBlockingFragment(t *testing.T) {
	a := &Automaton{
		Name: "A", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1}},
			{Fragment: &Fragment{ID: "f1"}},
		},
	}
	b := &Automaton{
		Name: "B", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1}},
			{Accepting: true},
		},
	}
	res := Compare(a, b)
	if res.Kind != Unknown {
		t.Fatalf("got %v, want Unknown", res)
	}
	if len(res.Blocking) != 1 || res.Blocking[0] != "f1" {
		t.Fatalf("Blocking = %v, want [f1]", res.Blocking)
	}
}

func TestNoEquivalenceBeforeFullExpansion(t *testing.T) {
	build := func() (*Automaton, *Automaton) {
		a := &Automaton{
			Name: "A", Alphabet: []Symbol{"a"}, Start: 0,
			States: []State{
				{Transitions: map[Symbol]StateID{"a": 1}},
				{Fragment: &Fragment{ID: "f1"}},
			},
		}
		// B accepts exactly "a".
		b := &Automaton{
			Name: "B", Alphabet: []Symbol{"a"}, Start: 0,
			States: []State{
				{Transitions: map[Symbol]StateID{"a": 1}},
				{Accepting: true},
			},
		}
		return a, b
	}

	a, b := build()
	if res := Compare(a, b); res.Kind == Equivalent {
		t.Fatal("equivalence claimed before full expansion")
	}

	// The fragment hides a difference: A accepts a+ once expanded.
	a, b = build()
	if err := a.ExpandFragment("f1", FragmentData{
		Accepting:   true,
		Transitions: map[Symbol]StateID{"a": 1},
	}); err != nil {
		t.Fatal(err)
	}
	res := Compare(a, b)
	if res.Kind != Counterexample || !equalWord(res.Word, []Symbol{"a", "a"}) || res.AcceptedBy != SideA {
		t.Fatalf("got %v, want counterexample [a a] accepted by A", res)
	}

	// The fragment hides nothing: A accepts exactly "a" once expanded.
	a, b = build()
	if err := a.ExpandFragment("f1", FragmentData{Accepting: true}); err != nil {
		t.Fatal(err)
	}
	if res := Compare(a, b); res.Kind != Equivalent {
		t.Fatalf("got %v, want Equivalent", res)
	}
}

func TestUnreachableFragmentStillBlocksEquivalence(t *testing.T) {
	// Both automata accept {ε}, but A carries an unreachable unexpanded
	// fragment, so equivalence must not be claimed yet.
	a := &Automaton{
		Name: "A", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{
			{Accepting: true},
			{Fragment: &Fragment{ID: "far"}},
		},
	}
	b := &Automaton{
		Name: "B", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{{Accepting: true}},
	}
	res := Compare(a, b)
	if res.Kind != Unknown || len(res.Blocking) != 1 || res.Blocking[0] != "far" {
		t.Fatalf("got %v, want Unknown blocked by [far]", res)
	}
	if err := a.ExpandFragment("far", FragmentData{}); err != nil {
		t.Fatal(err)
	}
	if res := Compare(a, b); res.Kind != Equivalent {
		t.Fatalf("got %v, want Equivalent", res)
	}
}

func TestIncrementalResume(t *testing.T) {
	// A: s0 -a-> s1{f1}, s0 -b-> s2(accepting). B mirrors it with s1
	// replaced by a non-accepting dead end.
	a := &Automaton{
		Name: "A", Alphabet: []Symbol{"a", "b"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1, "b": 2}},
			{Fragment: &Fragment{ID: "f1"}},
			{Accepting: true},
		},
	}
	b := &Automaton{
		Name: "B", Alphabet: []Symbol{"a", "b"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1, "b": 2}},
			{},
			{Accepting: true},
		},
	}
	search := NewSearch(a, b)
	res := search.Run()
	if res.Kind != Unknown || len(res.Blocking) != 1 || res.Blocking[0] != "f1" {
		t.Fatalf("got %v, want Unknown blocked by [f1]", res)
	}
	// Expand f1: accepting, no outgoing transitions. Now "a" distinguishes.
	if err := a.ExpandFragment("f1", FragmentData{Accepting: true}); err != nil {
		t.Fatal(err)
	}
	res = search.Resume()
	if res.Kind != Counterexample || !equalWord(res.Word, []Symbol{"a"}) || res.AcceptedBy != SideA {
		t.Fatalf("got %v, want counterexample [a] accepted by A", res)
	}
	if err := VerifyCounterexample(a, b, res); err != nil {
		t.Fatal(err)
	}
}

func TestFragmentExpansionWithNewStates(t *testing.T) {
	// A accepts exactly "aa" once f1 is expanded with one new state.
	a := &Automaton{
		Name: "A", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1}},
			{Fragment: &Fragment{ID: "f1"}},
		},
	}
	// B accepts a^{n>=2}.
	b := &Automaton{
		Name: "B", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1}},
			{Transitions: map[Symbol]StateID{"a": 2}},
			{Accepting: true, Transitions: map[Symbol]StateID{"a": 2}},
		},
	}
	err := a.ExpandFragment("f1", FragmentData{
		NewStates:   []State{{Accepting: true}},
		Transitions: map[Symbol]StateID{"a": NewStateRef(0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := Compare(a, b)
	if res.Kind != Counterexample || !equalWord(res.Word, []Symbol{"a", "a", "a"}) || res.AcceptedBy != SideB {
		t.Fatalf("got %v, want counterexample [a a a] accepted by B", res)
	}
	if err := FullProductCheck(a, b, res); err != nil {
		t.Fatal(err)
	}
}

func TestRandomizedAgainstEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 25; iter++ {
		a := randomAutomaton(rng, "A")
		b := randomAutomaton(rng, "B")
		res := Compare(a, b)
		switch res.Kind {
		case Counterexample:
			if err := VerifyCounterexample(a, b, res); err != nil {
				t.Fatalf("iter %d: %v", iter, err)
			}
			if err := EnumerateCheck(a, b, len(res.Word), res); err != nil {
				t.Fatalf("iter %d: %v", iter, err)
			}
		case Equivalent:
			// If the languages differ, some word of length <= |QA|*|QB|
			// distinguishes them, so enumeration up to that bound is
			// a complete cross-check.
			if err := EnumerateCheck(a, b, len(a.States)*len(b.States), res); err != nil {
				t.Fatalf("iter %d: %v", iter, err)
			}
		default:
			t.Fatalf("iter %d: unexpected Unknown for fragment-free automata", iter)
		}
		if err := FullProductCheck(a, b, res); err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
	}
}

func randomAutomaton(rng *rand.Rand, name string) *Automaton {
	n := 1 + rng.Intn(3)
	a := &Automaton{Name: name, Alphabet: []Symbol{"a", "b"}, Start: 0}
	for i := 0; i < n; i++ {
		st := State{Accepting: rng.Intn(2) == 0, Transitions: map[Symbol]StateID{}}
		for _, sym := range a.Alphabet {
			if rng.Intn(10) < 7 { // 30% of transitions are missing -> sink
				st.Transitions[sym] = StateID(rng.Intn(n))
			}
		}
		a.States = append(a.States, st)
	}
	return a
}
