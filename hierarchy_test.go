package hvsched

import (
	"fmt"
	"testing"
)

func hierarchyProblem(clock ClockMode) Problem {
	return Problem{
		Vars: []VarSpec{
			{Name: "wp", Min: 1, Max: 2},
			{Name: "wa1", Min: 1, Max: 2},
			{Name: "wa2", Min: 1, Max: 2},
			{Name: "wb", Min: 1, Max: 2},
		},
		Clock: clock,
		Nodes: []NodeSpec{
			{ID: "root"},
			{ID: "tenantA", Parent: "root", Tie: 1, WeightVar: "wp"},
			{ID: "a1", Parent: "tenantA", Tie: 2, WeightVar: "wa1"},
			{ID: "a2", Parent: "tenantA", Tie: 3, WeightVar: "wa2"},
			{ID: "b", Parent: "root", Tie: 4, WeightVar: "wb"},
		},
		Arrivals: []ArrivalSpec{
			{Leaf: "a1", Before: 0, Count: 3, Length: 2},
			{Leaf: "a2", Before: 0, Count: 2, Length: 3},
			{Leaf: "b", Before: 0, Count: 4, Length: 1},
			{Leaf: "a1", Before: 3, Count: 1, Length: 5},
		},
		Observations: make([]Observation, 5),
	}
}

func TestBruteForceHierarchyGeneratedTraces(t *testing.T) {
	for _, clock := range []ClockMode{ClockStartTag, ClockFinishTag} {
		base := hierarchyProblem(clock)
		for wp := uint64(1); wp <= 2; wp++ {
			for wa1 := uint64(1); wa1 <= 2; wa1++ {
				for wa2 := uint64(1); wa2 <= 2; wa2++ {
					for wb := uint64(1); wb <= 2; wb++ {
						w := WeightVector{wp, wa1, wa2, wb}
						replay, err := Replay(base, w)
						if err != nil {
							t.Fatalf("weights%v replay: %v", w, err)
						}
						p := base
						p.Observations = make([]Observation, len(replay.Emissions))
						for i, e := range replay.Emissions {
							p.Observations[i] = Observation{Leaf: e.Leaf}
						}
						want := enumerateVectors(t, p)
						name := fmt.Sprintf("c%d-%d-%d-%d-%d", clock, wp, wa1, wa2, wb)
						t.Run(name, func(t *testing.T) {
							checkSolutionSet(t, p, want)
						})
					}
				}
			}
		}
	}
}

func TestShortestContradictionPrefixAllSequences(t *testing.T) {
	base := twoLeafProblem(
		VarSpec{Min: 1, Max: 2},
		VarSpec{Min: 1, Max: 2},
		[]ArrivalSpec{
			{Leaf: "a", Before: 0, Count: 4, Length: 2},
			{Leaf: "b", Before: 0, Count: 4, Length: 3},
		},
		3,
		ClockStartTag,
	)

	for mask := 0; mask < 1<<3; mask++ {
		p := base
		p.Observations = make([]Observation, 3)
		for i := 0; i < 3; i++ {
			leaf := "a"
			if mask&(1<<i) != 0 {
				leaf = "b"
			}
			p.Observations[i] = Observation{Leaf: leaf}
		}

		want := enumerateVectors(t, p)
		set, err := Solve(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(want) > 0 {
			checkSolutionSet(t, p, want)
			continue
		}

		expected := -1
		weights := allSmallVectors(p)
		for k := 1; k <= len(p.Observations); k++ {
			prefix := p
			prefix.Observations = append([]Observation(nil), p.Observations[:k]...)
			feasible := false
			for _, w := range weights {
				ok, err := Verify(prefix, w)
				if err != nil {
					t.Fatal(err)
				}
				if ok {
					feasible = true
					break
				}
			}
			if !feasible {
				expected = k
				break
			}
		}
		if expected < 0 {
			t.Fatalf("sequence %b unexpectedly has brute solutions", mask)
		}
		if set.BadPrefixLength() != expected {
			t.Fatalf("sequence %v bad prefix=%d, want %d", observationLeaves(p), set.BadPrefixLength(), expected)
		}
		length, prefix, prefixSet, err := ContradictionPrefix(p)
		if err != nil {
			t.Fatal(err)
		}
		if length != expected || len(prefix) != expected || prefixSet.Count().Sign() != 0 {
			t.Fatalf("ContradictionPrefix=(%d,%d,%s), want %d", length, len(prefix), prefixSet.Count(), expected)
		}
	}
}

func allSmallVectors(p Problem) []WeightVector {
	var out []WeightVector
	var rec func(i int, cur WeightVector)
	rec = func(i int, cur WeightVector) {
		if i == len(p.Vars) {
			out = append(out, append(WeightVector(nil), cur...))
			return
		}
		for x := p.Vars[i].Min; x <= p.Vars[i].Max; x++ {
			rec(i+1, append(cur, x))
		}
	}
	rec(0, nil)
	return out
}

func observationLeaves(p Problem) []string {
	out := make([]string, len(p.Observations))
	for i, o := range p.Observations {
		out[i] = o.Leaf
	}
	return out
}
