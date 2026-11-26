package reshard

import "testing"

// TestMaxDimsAndThreeAxes exercises the supported input ceiling: four tensor
// dimensions over a three-axis device mesh, including Partial state.
func TestMaxDimsAndThreeAxes(t *testing.T) {
	p := Problem{
		Shape:           []int64{2, 4, 6, 8},
		BytesPerElement: 2,
		Mesh: Mesh{
			Ranks:   []int64{2, 3, 2},
			Weights: []int64{2, 3, 5},
		},
		Source: Layout{
			Shard:   []int{0, -1, 1, -1},
			Partial: []bool{false, false, true},
		},
		Target: Layout{
			Shard:   []int{-1, 2, -1, 0},
			Partial: []bool{false, false, false},
		},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if got := simulatedCost(t, p, sol.Ops); got.Cmp(sol.Cost) != 0 {
		t.Fatalf("simulation cost %s != solver cost %s", got, sol.Cost)
	}
	if sol.Steps == 0 {
		t.Fatal("expected a non-empty plan for differing layouts")
	}

	// More than four tensor dimensions and a one-axis mesh must be rejected.
	if _, err := Solve(Problem{
		Shape:           []int64{2, 2, 2, 2, 2},
		BytesPerElement: 1,
		Mesh:            Mesh{Ranks: []int64{2, 2}, Weights: []int64{1, 1}},
		Source:          Layout{Shard: []int{-1, -1, -1, -1, -1}, Partial: []bool{false, false}},
		Target:          Layout{Shard: []int{-1, -1, -1, -1, -1}, Partial: []bool{false, false}},
	}); err == nil {
		t.Fatal("expected rejection of a five-dimensional tensor")
	}
	if _, err := Solve(Problem{
		Shape:           []int64{4},
		BytesPerElement: 1,
		Mesh:            Mesh{Ranks: []int64{2}, Weights: []int64{1}},
		Source:          Layout{Shard: []int{-1}, Partial: []bool{false}},
		Target:          Layout{Shard: []int{-1}, Partial: []bool{false}},
	}); err == nil {
		t.Fatal("expected rejection of a one-axis device mesh")
	}
}
