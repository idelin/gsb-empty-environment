package dfacmp

import "testing"

// sinkPair returns A accepting exactly "a" and B accepting a+.
func sinkPair() (*Automaton, *Automaton) {
	a := &Automaton{
		Name: "A", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1}},
			{Accepting: true},
		},
	}
	b := &Automaton{
		Name: "B", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{
			{Transitions: map[Symbol]StateID{"a": 1}},
			{Accepting: true, Transitions: map[Symbol]StateID{"a": 1}},
		},
	}
	return a, b
}

func TestVerifyCounterexample(t *testing.T) {
	a, b := sinkPair()
	res := Compare(a, b)
	if err := VerifyCounterexample(a, b, res); err != nil {
		t.Fatalf("valid counterexample rejected: %v", err)
	}

	bad := res
	bad.AcceptedBy = SideA
	if err := VerifyCounterexample(a, b, bad); err == nil {
		t.Fatal("wrong AcceptedBy accepted")
	}

	bad = res
	bad.Word = []Symbol{"a"} // accepted by both
	if err := VerifyCounterexample(a, b, bad); err == nil {
		t.Fatal("non-distinguishing word accepted")
	}

	bad = res
	bad.Path = append([]PathStep(nil), res.Path...)
	bad.Path[1] = PathStep{At: ProductState{1, 0}, Via: "a"}
	if err := VerifyCounterexample(a, b, bad); err == nil {
		t.Fatal("corrupted path accepted")
	}

	bad = res
	bad.Kind = Equivalent
	if err := VerifyCounterexample(a, b, bad); err == nil {
		t.Fatal("non-counterexample kind accepted")
	}
}

func TestVerifyCounterexampleRejectsOpaque(t *testing.T) {
	a := &Automaton{
		Name: "A", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{{Fragment: &Fragment{ID: "f1"}}},
	}
	_, b := sinkPair()
	res := Result{Kind: Counterexample, Word: []Symbol{"a"}, AcceptedBy: SideB,
		Path: []PathStep{{At: ProductState{0, 0}}, {At: ProductState{Sink, 1}, Via: "a"}}}
	if err := VerifyCounterexample(a, b, res); err == nil {
		t.Fatal("counterexample through opaque state accepted")
	}
}

func TestEnumerateCheck(t *testing.T) {
	a, b := sinkPair()

	good := Compare(a, b)
	if err := EnumerateCheck(a, b, len(good.Word), good); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}

	// A longer, still distinguishing word is not minimal.
	nonMinimal := Result{Kind: Counterexample, Word: []Symbol{"a", "a", "a"}, AcceptedBy: SideB}
	if err := EnumerateCheck(a, b, 3, nonMinimal); err == nil {
		t.Fatal("non-minimal counterexample accepted")
	}

	// Claiming equivalence when "aa" distinguishes must fail.
	falseEq := Result{Kind: Equivalent}
	if err := EnumerateCheck(a, b, 2, falseEq); err == nil {
		t.Fatal("false equivalence accepted")
	}

	// A counterexample longer than the bound cannot be refuted.
	far := Result{Kind: Counterexample, Word: []Symbol{"a", "a", "a", "a"}, AcceptedBy: SideB}
	if err := EnumerateCheck(a, b, 1, far); err != nil {
		t.Fatalf("out-of-bound counterexample rejected: %v", err)
	}
}

func TestFullProductCheck(t *testing.T) {
	a, b := sinkPair()
	res := Compare(a, b)
	if err := FullProductCheck(a, b, res); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}

	nonMinimal := res
	nonMinimal.Word = []Symbol{"a", "a", "a"}
	if err := FullProductCheck(a, b, nonMinimal); err == nil {
		t.Fatal("non-minimal counterexample accepted")
	}

	falseEq := Result{Kind: Equivalent}
	if err := FullProductCheck(a, b, falseEq); err == nil {
		t.Fatal("false equivalence accepted")
	}

	// Equivalence is confirmed when the automata really are equivalent.
	eq := Compare(evenA(), evenAUnrolled())
	if err := FullProductCheck(evenA(), evenAUnrolled(), eq); err != nil {
		t.Fatalf("valid equivalence rejected: %v", err)
	}

	// Nothing may be validated while fragments are unexpanded.
	partial := &Automaton{
		Name: "P", Alphabet: []Symbol{"a"}, Start: 0,
		States: []State{{Fragment: &Fragment{ID: "f1"}}},
	}
	if err := FullProductCheck(partial, b, res); err == nil {
		t.Fatal("check succeeded despite unexpanded fragment")
	}
}
