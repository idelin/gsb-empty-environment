package taskpkg

import "testing"

func TestDenseSolveRejectsSingularAndBadShape(t *testing.T) {
	if _, err := denseSolve([][]float64{{1, 2}, {2, 4}}, []float64{1, 2}); err == nil {
		t.Fatal("singular system must return an error")
	}
	if _, err := denseSolve([][]float64{{1, 2}}, []float64{1}); err == nil {
		t.Fatal("non-square system must return an error")
	}
	if _, err := denseSolve([][]float64{{1}}, []float64{1, 2}); err == nil {
		t.Fatal("dimension mismatch must return an error")
	}
	if x, err := denseSolve(nil, nil); err != nil || len(x) != 0 {
		t.Fatalf("empty system: x=%v err=%v", x, err)
	}
}

func TestPCGZeroRHSReturnsZero(t *testing.T) {
	a, _ := buildLaplacianReduced(
		[][]int{{1, 2}, {0, 2}, {0, 1}},
		[]int{0, 1, -1},
		[][2]float64{{0, 0}, {0, 0}, {1, 1}},
	)
	x, iters, res, err := a.pcg([]float64{0, 0}, 1e-12, 10)
	if err != nil {
		t.Fatalf("zero rhs solve: %v", err)
	}
	if iters != 0 || res != 0 {
		t.Fatalf("zero rhs should stop immediately, got iters=%d res=%g", iters, res)
	}
	for _, v := range x {
		if v != 0 {
			t.Fatalf("zero rhs solution must be zero, got %v", x)
		}
	}
}

func TestPCGEmptyMatrix(t *testing.T) {
	a := &csrMatrix{n: 0, row: []int{0}}
	x, iters, res, err := a.pcg(nil, 1e-12, 5)
	if err != nil || len(x) != 0 || iters != 0 || res != 0 {
		t.Fatalf("empty pcg: x=%v iters=%d res=%g err=%v", x, iters, res, err)
	}
}
