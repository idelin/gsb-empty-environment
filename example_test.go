package hvsched

import (
	"fmt"
	"math/big"
)

func ExampleSolve() {
	p := Problem{
		Vars: []VarSpec{
			{Name: "wa", Min: 2, Max: 2, Layer: "l"},
			{Name: "wb", Min: 4, Max: 4, Layer: "l"},
		},
		LayerSums: map[string]uint64{"l": 6},
		Nodes: []NodeSpec{
			{ID: "root"},
			{ID: "a", Parent: "root", Tie: 1, WeightVar: "wa", Layer: "l"},
			{ID: "b", Parent: "root", Tie: 2, WeightVar: "wb", Layer: "l"},
		},
		Arrivals: []ArrivalSpec{
			{Leaf: "a", Before: 0, Count: 1, Length: 3},
			{Leaf: "b", Before: 0, Count: 1, Length: 8},
		},
		Observations: []Observation{{Leaf: "a", PartialCompletion: big.NewRat(3, 2)}},
	}

	set, err := Solve(p)
	if err != nil {
		panic(err)
	}
	fmt.Println(set.CountString())
	first, err := set.KthInt(1)
	if err != nil {
		panic(err)
	}
	fmt.Println(first)
	// Output:
	// 1
	// [2 4]
}
