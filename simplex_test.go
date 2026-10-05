package main

import (
	"math/big"
	"testing"
)

func rows(v ...[]string) ([][]*big.Rat, []*big.Rat) {
	var A [][]*big.Rat
	var b []*big.Rat
	for _, r := range v {
		row := make([]*big.Rat, len(r)-1)
		for i := 0; i < len(r)-1; i++ {
			row[i] = rat(r[i])
		}
		A = append(A, row)
		b = append(b, rat(r[len(r)-1]))
	}
	return A, b
}

func TestLPBasicMaximize(t *testing.T) {
	// max x + y subject to x + 2y <= 4, x - y <= 1, x,y free.
	A, b := rows(
		[]string{"1", "2", "4"},
		[]string{"1", "-1", "1"},
	)
	res := lpMaximizeFree(A, b, []*big.Rat{rat("1"), rat("1")})
	if res.Status != LPOptimal {
		t.Fatalf("status %v", res.Status)
	}
	// optimum at x=2, y=1 -> value 3.
	if res.Value.Cmp(rat("3")) != 0 {
		t.Fatalf("value %s, want 3", res.Value)
	}
	if res.Point[0].Cmp(rat("2")) != 0 || res.Point[1].Cmp(rat("1")) != 0 {
		t.Fatalf("point %v", res.Point)
	}
}

func TestLPInfeasible(t *testing.T) {
	// x <= 0 and x >= 1.
	A, b := rows(
		[]string{"1", "0"},
		[]string{"-1", "-1"},
	)
	res := lpMaximizeFree(A, b, []*big.Rat{rat("1")})
	if res.Status != LPInfeasible {
		t.Fatalf("status %v", res.Status)
	}
}

func TestLPUnbounded(t *testing.T) {
	// max x subject to -x <= 0.
	A, b := rows([]string{"-1", "0"})
	res := lpMaximizeFree(A, b, []*big.Rat{rat("1")})
	if res.Status != LPUnbounded {
		t.Fatalf("status %v", res.Status)
	}
}

func TestStrictFeasibility(t *testing.T) {
	// x <= 0, x >= 0, x < 0 is infeasible.
	leA, leB := rows(
		[]string{"1", "0"},
		[]string{"-1", "0"},
	)
	ltA, ltB := rows([]string{"1", "0"})
	ok, _ := feasibleStrict(leA, leB, ltA, ltB, 1)
	if ok {
		t.Fatal("expected infeasible")
	}
	// x >= 0, x < 1 is feasible.
	leA2, leB2 := rows([]string{"-1", "0"})
	ltA2, ltB2 := rows([]string{"1", "1"})
	ok, pt := feasibleStrict(leA2, leB2, ltA2, ltB2, 1)
	if !ok {
		t.Fatal("expected feasible")
	}
	if pt[0].Sign() < 0 || pt[0].Cmp(rat("1")) >= 0 {
		t.Fatalf("witness %v violates constraints", pt[0])
	}
	// x <= 0, x >= 0 (no strict) is feasible with x = 0.
	ok, _ = feasibleStrict(leA, leB, nil, nil, 1)
	if !ok {
		t.Fatal("expected feasible")
	}
}

func TestUnboundedDetection(t *testing.T) {
	// x - y <= 0 is unbounded.
	leA, _ := rows([]string{"1", "-1", "0"})
	if !isUnbounded(leA, nil, 2) {
		t.Fatal("expected unbounded")
	}
	// unit box is bounded.
	boxA, _ := rows(
		[]string{"1", "0", "1"},
		[]string{"-1", "0", "0"},
		[]string{"0", "1", "1"},
		[]string{"0", "-1", "0"},
	)
	if isUnbounded(boxA, nil, 2) {
		t.Fatal("expected bounded")
	}
	// strict constraint x < 1 is still unbounded below.
	ltA, _ := rows([]string{"1", "1"})
	if !isUnbounded(nil, ltA, 1) {
		t.Fatal("expected unbounded")
	}
}

func TestLPRationalOptimum(t *testing.T) {
	// max x subject to 3x <= 2 -> 2/3.
	A, b := rows([]string{"3", "2"})
	res := lpMaximizeFree(A, b, []*big.Rat{rat("1")})
	if res.Status != LPOptimal || res.Value.Cmp(rat("2/3")) != 0 {
		t.Fatalf("got %v %v", res.Status, res.Value)
	}
}
