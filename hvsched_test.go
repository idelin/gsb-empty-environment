package hvsched

import (
	"fmt"
	"math/big"
	"reflect"
	"testing"
)

func twoLeafProblem(wa, wb VarSpec, arrivals []ArrivalSpec, n int, clock ClockMode) Problem {
	wa.Name = "wa"
	wb.Name = "wb"
	return Problem{
		Vars:  []VarSpec{wa, wb},
		Clock: clock,
		Nodes: []NodeSpec{
			{ID: "root", Parent: ""},
			{ID: "a", Parent: "root", Tie: 1, WeightVar: "wa"},
			{ID: "b", Parent: "root", Tie: 2, WeightVar: "wb"},
		},
		Arrivals:     arrivals,
		Observations: make([]Observation, n),
	}
}

func enumerateVectors(t *testing.T, p Problem) []WeightVector {
	t.Helper()
	var out []WeightVector
	var rec func(i int, cur WeightVector)
	rec = func(i int, cur WeightVector) {
		if i == len(p.Vars) {
			wv := append(WeightVector(nil), cur...)
			ok, err := Verify(p, wv)
			if err != nil {
				t.Fatalf("verify %v: %v", wv, err)
			}
			if ok {
				out = append(out, wv)
			}
			return
		}
		for x := p.Vars[i].Min; x <= p.Vars[i].Max; x++ {
			rec(i+1, append(cur, x))
		}
	}
	rec(0, nil)
	return out
}

func checkSolutionSet(t *testing.T, p Problem, want []WeightVector) {
	t.Helper()
	got, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if got.Count().Cmp(big.NewInt(int64(len(want)))) != 0 {
		t.Fatalf("count=%s, want %d\nregions=%#v", got.Count(), len(want), got.Regions())
	}

	var lex []WeightVector
	if err := got.Iterate(func(v WeightVector) bool {
		lex = append(lex, append(WeightVector(nil), v...))
		return true
	}); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(lex) != len(want) {
		t.Fatalf("iterated %d, want %d", len(lex), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(lex[i], want[i]) {
			t.Fatalf("solution %d = %v, want %v; all=%v", i+1, lex[i], want[i], lex)
		}
		kth, err := got.KthInt(i + 1)
		if err != nil {
			t.Fatalf("KthInt(%d): %v", i+1, err)
		}
		if !reflect.DeepEqual(kth, want[i]) {
			t.Fatalf("Kth(%d)=%v, want %v", i+1, kth, want[i])
		}
	}
}

func TestTieOnlyFixedEqualWeights(t *testing.T) {
	p := twoLeafProblem(
		VarSpec{Min: 1, Max: 1, Layer: "root"},
		VarSpec{Min: 1, Max: 1, Layer: "root"},
		[]ArrivalSpec{
			{Leaf: "a", Before: 0, Count: 1, Length: 3},
			{Leaf: "b", Before: 0, Count: 1, Length: 3},
		},
		1,
		ClockStartTag,
	)
	p.LayerSums = map[string]uint64{"root": 2}

	res, err := Replay(p, WeightVector{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Emissions[0].Leaf != "a" {
		t.Fatalf("tie winner = %q, want a", res.Emissions[0].Leaf)
	}
	if got := res.Emissions[0].Finish; got.Cmp(big.NewRat(3, 1)) != 0 {
		t.Fatalf("finish = %v, want 3", got)
	}

	p.Observations[0] = Observation{Leaf: "a"}
	want := []WeightVector{{1, 1}}
	checkSolutionSet(t, p, want)

	// A strict-only interpretation would reject the tied candidate.
	strict := p
	strict.Observations[0] = Observation{Leaf: "b"}
	set, err := Solve(strict)
	if err != nil {
		t.Fatal(err)
	}
	if set.Count().Sign() != 0 || set.BadPrefixLength() != 1 {
		t.Fatalf("higher tie id should need strict win: count=%s prefix=%d", set.Count(), set.BadPrefixLength())
	}
}

func TestBruteForceTwoLeafGeneratedTraces(t *testing.T) {
	clocks := []ClockMode{ClockStartTag, ClockFinishTag}
	for ci, clock := range clocks {
		base := twoLeafProblem(
			VarSpec{Min: 1, Max: 3},
			VarSpec{Min: 1, Max: 3},
			[]ArrivalSpec{
				{Leaf: "a", Before: 0, Count: 3, Length: 2},
				{Leaf: "b", Before: 0, Count: 2, Length: 3},
				{Leaf: "a", Before: 2, Count: 2, Length: 1},
			},
			5,
			clock,
		)

		for wa := uint64(1); wa <= 3; wa++ {
			for wb := uint64(1); wb <= 3; wb++ {
				trueWeights := WeightVector{wa, wb}
				replay, err := Replay(base, trueWeights)
				if err != nil {
					t.Fatalf("clock%d weights%v replay: %v", ci, trueWeights, err)
				}
				p := base
				p.Observations = make([]Observation, len(replay.Emissions))
				for i, e := range replay.Emissions {
					p.Observations[i] = Observation{Leaf: e.Leaf}
				}
				want := enumerateVectors(t, p)
				t.Run(fmt.Sprintf("clock%d-%d-%d", ci, wa, wb), func(t *testing.T) {
					checkSolutionSet(t, p, want)
				})
			}
		}
	}
}
