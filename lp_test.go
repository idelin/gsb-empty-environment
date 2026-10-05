package main

import (
	"math"
	"testing"
)

func TestLPSimple(t *testing.T) {
	// max x + y, s.t. x + 2y <= 4, 4x + 2y <= 12 -> x=8/3, y=2/3, obj=10/3
	a := [][]float64{{1, 2}, {4, 2}}
	b := []float64{4, 12}
	c := []float64{1, 1}
	val, x, err := newLPSolver(a, b, c).solve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(val-10.0/3.0) > 1e-6 {
		t.Fatalf("got %v, want %v", val, 10.0/3.0)
	}
	if math.Abs(x[0]-8.0/3.0) > 1e-6 || math.Abs(x[1]-2.0/3.0) > 1e-6 {
		t.Fatalf("got x=%v", x)
	}
}

func TestLPInfeasible(t *testing.T) {
	// x <= -1, x >= 0 -> infeasible
	a := [][]float64{{1}}
	b := []float64{-1}
	c := []float64{1}
	_, _, err := newLPSolver(a, b, c).solve()
	if err != errLPInfeasible {
		t.Fatalf("got %v, want infeasible", err)
	}
}

func TestLPEqualityViaTwoRows(t *testing.T) {
	// x + y = 10, x <= 4 -> max 2x + y = 14 (x=4, y=6)
	a := [][]float64{{1, 1}, {-1, -1}, {1, 0}}
	b := []float64{10, -10, 4}
	c := []float64{2, 1}
	val, x, err := newLPSolver(a, b, c).solve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(val-14) > 1e-6 {
		t.Fatalf("got %v, want 14", val)
	}
	if math.Abs(x[0]-4) > 1e-6 || math.Abs(x[1]-6) > 1e-6 {
		t.Fatalf("got x=%v, want [4 6]", x)
	}
}

func TestLPZeroVars(t *testing.T) {
	val, x, err := newLPSolver(nil, nil, nil).solve()
	if err != nil || val != 0 || len(x) != 0 {
		t.Fatalf("got val=%v x=%v err=%v", val, x, err)
	}
}
