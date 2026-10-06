package dfacmp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fragmentPair returns A with a hidden fragment and B accepting exactly "a".
func fragmentPair() (*Automaton, *Automaton) {
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
	return a, b
}

func TestEngineAsyncExpansionAndVersionBinding(t *testing.T) {
	a, b := fragmentPair()
	release := make(chan struct{})
	var mu sync.Mutex
	var calls []FragmentID
	exp := ExpanderFunc(func(ctx context.Context, id FragmentID) (FragmentData, error) {
		mu.Lock()
		calls = append(calls, id)
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
			return FragmentData{}, ctx.Err()
		}
		// f1: accepting, loops on "a" -> A accepts a+.
		return FragmentData{Accepting: true, Transitions: map[Symbol]StateID{"a": 1}}, nil
	})
	e := NewEngine(a, b, exp)

	q1 := e.Query()
	select {
	case <-q1.Done():
		t.Fatal("query finished before the fragment was expanded")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	res1, err := q1.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res1.Kind != Counterexample || !equalWord(res1.Word, []Symbol{"a", "a"}) || res1.AcceptedBy != SideA {
		t.Fatalf("got %v, want counterexample [a a] accepted by A", res1)
	}
	// The old query stays bound to the versions at creation time, even
	// though the expansion bumped the live automaton afterwards.
	if res1.VersionA != 0 || res1.VersionB != 0 {
		t.Fatalf("q1 bound to versions (%d, %d), want (0, 0)", res1.VersionA, res1.VersionB)
	}
	if a.Version() != 1 {
		t.Fatalf("live automaton A at version %d, want 1", a.Version())
	}

	// A new query binds the new version and needs no further expansion.
	q2 := e.Query()
	res2, err := q2.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res2.VersionA != 1 || res2.VersionB != 0 {
		t.Fatalf("q2 bound to versions (%d, %d), want (1, 0)", res2.VersionA, res2.VersionB)
	}
	if !equalWord(res2.Word, res1.Word) {
		t.Fatalf("q2 word %v differs from q1 word %v", res2.Word, res1.Word)
	}

	// A third query with the same versions is served from the cache after
	// a full product-graph revalidation.
	q3 := e.Query()
	res3, err := q3.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res3.Kind != Counterexample || !equalWord(res3.Word, res2.Word) {
		t.Fatalf("cached result %v, want %v", res3, res2)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0] != "f1" {
		t.Fatalf("expander calls = %v, want exactly [f1]", calls)
	}
}

func TestEngineUnknownWithoutExpander(t *testing.T) {
	a, b := fragmentPair()
	e := NewEngine(a, b, nil)
	res, err := e.Compare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != Unknown || len(res.Blocking) != 1 || res.Blocking[0] != "f1" {
		t.Fatalf("got %v, want Unknown blocked by [f1]", res)
	}
}

func TestEngineExpanderFailure(t *testing.T) {
	a, b := fragmentPair()
	exp := ExpanderFunc(func(ctx context.Context, id FragmentID) (FragmentData, error) {
		return FragmentData{}, errors.New("backend unavailable")
	})
	e := NewEngine(a, b, exp)
	res, err := e.Compare(context.Background())
	if err == nil {
		t.Fatal("expected expansion error")
	}
	if res.Kind != Unknown || len(res.Blocking) != 1 || res.Blocking[0] != "f1" {
		t.Fatalf("got %v, want Unknown blocked by [f1]", res)
	}
}

func TestEngineEquivalentAfterFullExpansion(t *testing.T) {
	// A carries an unreachable fragment; both automata accept {epsilon}.
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
	exp := ExpanderFunc(func(ctx context.Context, id FragmentID) (FragmentData, error) {
		return FragmentData{}, nil
	})
	e := NewEngine(a, b, exp)
	res, err := e.Compare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != Equivalent {
		t.Fatalf("got %v, want Equivalent after full expansion", res)
	}
	if err := FullProductCheck(a, b, res); err != nil {
		t.Fatal(err)
	}
}
