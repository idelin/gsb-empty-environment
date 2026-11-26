package reshard_test

import (
	"fmt"

	"prescreen.local/ps-cf3ce4e29d/reshard"
)

func ExampleSolve() {
	// 4x8 tensor over a 2x2 device mesh; move the shard from mesh axis 0 to
	// axis 1. A single cross-axis AllToAll is the exact optimum.
	problem := reshard.Problem{
		Shape:           []int64{4, 8},
		BytesPerElement: 1,
		Mesh: reshard.Mesh{
			Ranks:   []int64{2, 2},
			Weights: []int64{1, 1},
		},
		Source: reshard.Layout{
			Shard:   []int{0, -1},
			Partial: []bool{false, false},
		},
		Target: reshard.Layout{
			Shard:   []int{-1, 1},
			Partial: []bool{false, false},
		},
	}
	solution, err := reshard.Solve(problem)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, op := range solution.Ops {
		fmt.Println(op)
	}
	fmt.Println("cost:", solution.Cost.String())
	// Output:
	// AllToAll(a=0,b=1,d1=0,d2=1)
	// cost: 8/1
}
