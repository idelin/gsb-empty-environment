package material

import "testing"

func TestWeightedMergeExact(t *testing.T) {
	// 1/3-share lots merged by integer weights must stay exact.
	v1 := Pure("a")
	v2 := Pure("b")
	out := Weighted([]int64{1, 2}, []Vector{v1, v2})
	if !out.Equal(Vector{"a": rat(1, 3), "b": rat(2, 3)}) {
		t.Fatalf("weighted = %v", out)
	}
	if !out.IsNormalized() {
		t.Fatal("not normalized")
	}
}

func TestVectorOps(t *testing.T) {
	a := Vector{"x": rat(1, 2)}
	b := Vector{"x": rat(1, 3), "y": rat(1, 6)}
	sum := a.Add(b)
	if sum["x"].Cmp(rat(5, 6)) != 0 || sum["y"].Cmp(rat(1, 6)) != 0 {
		t.Fatalf("add = %v", sum)
	}
	// Inputs must be unmutated.
	if a["x"].Cmp(rat(1, 2)) != 0 || b["x"].Cmp(rat(1, 3)) != 0 {
		t.Fatal("add mutated inputs")
	}
	half := sum.Scale(rat(1, 2))
	if half["x"].Cmp(rat(5, 12)) != 0 {
		t.Fatalf("scale = %v", half)
	}
}
