package hvsched

import (
	"fmt"
	"testing"
)

func fiveLeafProblem(sum uint64) Problem {
	vars := make([]VarSpec, 5)
	nodes := []NodeSpec{{ID: "root"}}
	arrivals := make([]ArrivalSpec, 5)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("w%d", i)
		vars[i] = VarSpec{Name: name, Min: 1, Max: 2, Layer: "l"}
		id := fmt.Sprintf("t%d", i)
		nodes = append(nodes, NodeSpec{
			ID: id, Parent: "root", Tie: uint64(i + 1),
			WeightVar: name, Layer: "l",
		})
		arrivals[i] = ArrivalSpec{Leaf: id, Before: 0, Count: 3, Length: uint64(i + 2)}
	}
	p := Problem{
		Vars: vars, Nodes: nodes, Arrivals: arrivals,
		Observations: make([]Observation, 4),
	}
	if sum != 0 {
		p.LayerSums = map[string]uint64{"l": sum}
	}
	return p
}

func TestBruteForceFiveLeaves(t *testing.T) {
	base := fiveLeafProblem(0)
	var rec func(i int, w WeightVector)
	rec = func(i int, w WeightVector) {
		if i == 5 {
			replay, err := Replay(base, w)
			if err != nil {
				t.Fatal(err)
			}
			p := base
			p.Observations = make([]Observation, len(replay.Emissions))
			for j, e := range replay.Emissions {
				p.Observations[j] = Observation{Leaf: e.Leaf}
			}
			want := enumerateVectors(t, p)
			t.Run(fmt.Sprint(w), func(t *testing.T) { checkSolutionSet(t, p, want) })
			return
		}
		for x := uint64(1); x <= 2; x++ {
			next := append(append(WeightVector(nil), w...), x)
			rec(i+1, next)
		}
	}
	rec(0, nil)
}

func TestCountBoundedCompositionsFive(t *testing.T) {
	dom := box{
		lo: []int64{1, 1, 1, 1, 1},
		hi: []int64{2, 2, 2, 2, 2},
	}
	// Sum 8: choose exactly three twos among five = 10.
	got := countGroup(&dom, []int{0, 1, 2, 3, 4}, 8)
	if got.Int64() != 10 {
		t.Fatalf("count=%s, want 10", got)
	}
}
