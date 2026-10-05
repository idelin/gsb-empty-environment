package main

import (
	"testing"
)

// Verify must catch a deliberately wrong projection (too strong).
func TestVerifyCatchesTooStrongProjection(t *testing.T) {
	// original: y >= x, y <= x (so y == x), 0 <= x <= 1; true projection: 0 <= x <= 1
	sys := mkSys(2,
		[]string{"1", "-1"}, "0", Le, // y >= x
		[]string{"-1", "1"}, "0", Le, // y <= x
		[]string{"1", "0"}, "-1", Le, // x <= 1
		[]string{"-1", "0"}, "0", Le, // x >= 0
	)
	// wrong claim: x == 0 only
	wrong := mkSys(2,
		[]string{"1", "0"}, "0", Le,
		[]string{"-1", "0"}, "0", Le,
	)
	rep := Verify(sys, 2, []int{0}, wrong, true)
	if rep.OK {
		t.Fatal("verify accepted a too-strong projection")
	}
}

// Verify must catch a deliberately wrong projection (too weak).
func TestVerifyCatchesTooWeakProjection(t *testing.T) {
	sys := mkSys(2,
		[]string{"1", "-1"}, "0", Le,
		[]string{"-1", "1"}, "0", Le,
		[]string{"1", "0"}, "-1", Le,
		[]string{"-1", "0"}, "0", Le,
	)
	// wrong claim: -2 <= x <= 2 (superset)
	wrong := mkSys(2,
		[]string{"1", "0"}, "-2", Le,
		[]string{"-1", "0"}, "-2", Le,
	)
	rep := Verify(sys, 2, []int{0}, wrong, true)
	if rep.OK {
		t.Fatal("verify accepted a too-weak projection")
	}
}

// Verify must catch a strictness flip on the boundary.
func TestVerifyCatchesStrictnessFlip(t *testing.T) {
	// original: 0 < x < 1 via y: x < y, y < 1, x > 0 ... use strict bounds directly
	sys := mkSys(2,
		[]string{"1", "-1"}, "0", Lt, // x < y
		[]string{"0", "1"}, "-1", Lt, // y < 1
		[]string{"-1", "0"}, "0", Lt, // x > 0
	)
	// correct projection: 0 < x < 1 (strict); wrong: 0 <= x <= 1
	wrong := mkSys(2,
		[]string{"1", "0"}, "-1", Le,
		[]string{"-1", "0"}, "0", Le,
	)
	rep := Verify(sys, 2, []int{0}, wrong, true)
	if rep.OK {
		t.Fatal("verify accepted non-strict relaxation of strict projection")
	}
	// and the correct strict projection passes
	correct := mkSys(2,
		[]string{"1", "0"}, "-1", Lt,
		[]string{"-1", "0"}, "0", Lt,
	)
	rep = Verify(sys, 2, []int{0}, correct, true)
	if !rep.OK {
		t.Fatalf("verify rejected correct strict projection: %+v", rep)
	}
}

// Verify must agree on empty systems.
func TestVerifyEmptySystem(t *testing.T) {
	// infeasible original
	sys := mkSys(1,
		[]string{"1"}, "0", Le,
		[]string{"-1"}, "1", Le,
	)
	eng := &Engine{NumVars: 1, Budget: bigBudget()}
	res := eng.Eliminate(sys, []int{0})
	rep := Verify(sys, 1, nil, res.Constraints, res.Feasible)
	if !rep.OK {
		t.Fatalf("empty-set verification failed: %+v", rep)
	}
	// claiming feasibility for an infeasible system must fail
	rep = Verify(sys, 1, nil, nil, true)
	if rep.OK {
		t.Fatal("verify accepted feasible claim for infeasible system")
	}
}
