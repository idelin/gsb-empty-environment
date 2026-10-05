package main

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) []*Instr {
	t.Helper()
	prog, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return prog
}

func checkPrograms(t *testing.T, origSrc, candSrc string) *Result {
	t.Helper()
	orig := mustParse(t, origSrc)
	cand := mustParse(t, candSrc)
	return Check(orig, cand)
}

func TestPreciseAliasAdjacentIntervals(t *testing.T) {
	// [0,8) and [8,16) share no byte: reordering the stores is legal.
	res := checkPrograms(t,
		"store [0, 8), r1\nstore [8, 16), r2\n",
		"store [8, 16), r2\nstore [0, 8), r1\n")
	if !res.Legal() {
		t.Fatalf("adjacent intervals must not alias, got violations: %+v", res.Violations)
	}
}

func TestPreciseAliasOverlappingIntervals(t *testing.T) {
	// [0,8) and [7,15) overlap at byte 7: the swap is a WAW violation.
	res := checkPrograms(t,
		"store [0, 8), r1\nstore [7, 15), r2\n",
		"store [7, 15), r2\nstore [0, 8), r1\n")
	if res.Legal() {
		t.Fatal("overlapping stores must conflict")
	}
	v := res.Violations[0].Edge
	if v.Kinds[0] != "WAW" || v.Loc != "[0, 8)" {
		t.Fatalf("expected WAW on [0, 8), got %+v", v)
	}
	if res.Sim.Mismatch == nil {
		t.Fatal("simulation should find a diverging initial state")
	}
}

func TestRAWViolation(t *testing.T) {
	res := checkPrograms(t,
		"r1 = add r2, r3\nr4 = mul r1, r5\n",
		"r4 = mul r1, r5\nr1 = add r2, r3\n")
	if res.Legal() {
		t.Fatal("expected RAW violation on r1")
	}
	v := res.Violations[0].Edge
	if v.Kinds[0] != "RAW" || v.Src != 0 || v.Dst != 1 {
		t.Fatalf("unexpected edge: %+v", v)
	}
}

func TestWARAndWAWViolations(t *testing.T) {
	// WAR: read of r2 must stay before the write of r2.
	res := checkPrograms(t,
		"r1 = add r2, r3\nr2 = const 9\n",
		"r2 = const 9\nr1 = add r2, r3\n")
	if res.Legal() || res.Violations[0].Edge.Kinds[0] != "WAR" {
		t.Fatalf("expected WAR violation, got %+v", res.Violations)
	}
	// WAW: two writes of r1 must keep their order.
	res = checkPrograms(t,
		"r1 = const 1\nr1 = add r1, r2\n",
		"r1 = add r1, r2\nr1 = const 1\n")
	if res.Legal() {
		t.Fatal("expected WAW/RAW violations")
	}
}

func TestFenceOrdering(t *testing.T) {
	orig := "store [0, 8), r1\nfence\nstore [8, 16), r2\n"
	// Moving an instruction across the fence is illegal even with no
	// data dependence.
	res := checkPrograms(t, orig, "store [8, 16), r2\nfence\nstore [0, 8), r1\n")
	if res.Legal() {
		t.Fatal("fence crossing must be illegal")
	}
	if res.Violations[0].Edge.Kinds[0] != "BAR" {
		t.Fatalf("expected BAR edge, got %+v", res.Violations[0].Edge)
	}
	// Reordering on the same side of the fence is fine.
	res = checkPrograms(t,
		"r1 = const 1\nr2 = const 2\nfence\nstore [0, 8), r1\n",
		"r2 = const 2\nr1 = const 1\nfence\nstore [0, 8), r1\n")
	if !res.Legal() {
		t.Fatalf("same-side reorder should be legal: %+v", res.Violations)
	}
}

func TestNotAPermutation(t *testing.T) {
	res := checkPrograms(t,
		"r1 = const 1\n",
		"r1 = const 2\n")
	if res.PermErr == "" {
		t.Fatal("expected permutation error")
	}
	res = checkPrograms(t,
		"r1 = const 1\nr2 = const 2\n",
		"r2 = const 2\n")
	if res.PermErr == "" {
		t.Fatal("expected length mismatch")
	}
}

func TestCoincidentalEqualityIsNotLegality(t *testing.T) {
	// Both instructions always write 0 to r1, so every initial state
	// yields identical final states under either order. The WAW edge
	// still makes the swap illegal; simulation must stay silent.
	res := checkPrograms(t,
		"r1 = xor r2, r2\nr1 = const 0\n",
		"r1 = const 0\nr1 = xor r2, r2\n")
	if res.Legal() {
		t.Fatal("WAW violation must be reported despite equal outcomes")
	}
	if res.Sim.Mismatch != nil {
		t.Fatalf("outcomes coincide on all states; unexpected mismatch: %+v", res.Sim.Mismatch)
	}
	if res.Sim.Mode != "exhaustive" {
		t.Fatalf("expected exhaustive simulation, got %s", res.Sim.Mode)
	}
}

func TestSimulationCounterexample(t *testing.T) {
	// Swapping the two stores changes which value survives in memory.
	res := checkPrograms(t,
		"r1 = const 1\nr2 = const 2\nstore [0, 8), r1\nstore [0, 8), r2\n",
		"r1 = const 1\nr2 = const 2\nstore [0, 8), r2\nstore [0, 8), r1\n")
	if res.Legal() {
		t.Fatal("expected WAW violation")
	}
	if res.Sim.Mismatch == nil {
		t.Fatal("simulation must confirm with a concrete counterexample")
	}
	if res.Sim.Mismatch.Cell != "[0]" {
		t.Fatalf("unexpected differing cell: %+v", res.Sim.Mismatch)
	}
}

func TestEarliestViolationReported(t *testing.T) {
	// Two violated edges: (0,1) and (2,3); the earliest must come first.
	res := checkPrograms(t,
		"r1 = const 1\nr1 = const 2\nr2 = const 3\nr2 = const 4\n",
		"r2 = const 4\nr2 = const 3\nr1 = const 2\nr1 = const 1\n")
	if len(res.Violations) < 2 {
		t.Fatalf("expected >=2 violations, got %d", len(res.Violations))
	}
	first := res.Violations[0].Edge
	if first.Src != 0 || first.Dst != 1 {
		t.Fatalf("earliest edge should be (0,1), got (%d,%d)", first.Src, first.Dst)
	}
}

func TestLegalReorderWithMemoryAndRegisters(t *testing.T) {
	orig := strings.Join([]string{
		"r1 = load [0, 8)",
		"r2 = load [16, 24)",
		"r1 = add r1, r2",
		"store [32, 40), r1",
	}, "\n") + "\n"
	// Only the two independent loads may swap.
	cand := strings.Join([]string{
		"r2 = load [16, 24)",
		"r1 = load [0, 8)",
		"r1 = add r1, r2",
		"store [32, 40), r1",
	}, "\n") + "\n"
	res := checkPrograms(t, orig, cand)
	if !res.Legal() {
		t.Fatalf("independent load swap should be legal: %+v", res.Violations)
	}
	if res.Sim.Mismatch != nil {
		t.Fatalf("legal reorder must not diverge: %+v", res.Sim.Mismatch)
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{
		"r1 = load [8, 8)", // empty interval
		"r1 = load [0, 9)", // wider than 8 bytes
		"store [4, 2), r1", // inverted
		"r1 = div r1, r2",  // unknown op
		"load r1, [0, 8)",  // malformed
	} {
		if _, err := ParseProgram(bad); err == nil {
			t.Fatalf("expected parse error for %q", bad)
		}
	}
}
