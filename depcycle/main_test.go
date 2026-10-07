package main

import (
	"strings"
	"testing"
)

func mustRun(t *testing.T, in *input) string {
	t.Helper()
	out, err := run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return out
}

// Mutually exclusive guards on opposite edges must NOT form a fake cycle.
func TestMutuallyExclusiveEdgesNoFakeCycle(t *testing.T) {
	in := &input{
		Switches: []string{"a"},
		Edges: []edge{
			{From: "A", To: "B", When: "a"},
			{From: "B", To: "A", When: "!a"},
		},
	}
	out := mustRun(t, in)
	if out != "ACYCLIC count=2" {
		t.Fatalf("got %q, want ACYCLIC count=2", out)
	}
}

// An unconditional cycle is found under the all-zero configuration.
func TestAlwaysCycleSmallestConfig(t *testing.T) {
	in := &input{
		Switches: []string{"a", "b"},
		Edges: []edge{
			{From: "A", To: "B"},
			{From: "B", To: "C"},
			{From: "C", To: "A"},
		},
	}
	out := mustRun(t, in)
	if !strings.HasPrefix(out, "CYCLE config=00 cycle=") {
		t.Fatalf("got %q", out)
	}
	if !strings.Contains(out, "A->") || !strings.HasSuffix(out, "->A") &&
		!strings.HasSuffix(out, "->B") && !strings.HasSuffix(out, "->C") {
		t.Fatalf("cycle not closed: %q", out)
	}
}

// The reported cycle must be a genuine closed loop.
func TestCycleIsClosed(t *testing.T) {
	in := &input{
		Switches: []string{"a"},
		Edges: []edge{
			{From: "A", To: "B"},
			{From: "B", To: "C"},
			{From: "C", To: "A"},
			{From: "C", To: "D"},
		},
	}
	out := mustRun(t, in)
	if !strings.HasPrefix(out, "CYCLE config=0 cycle=") {
		t.Fatalf("got %q", out)
	}
	cyc := strings.TrimPrefix(out, "CYCLE config=0 cycle=")
	nodes := strings.Split(cyc, "->")
	if nodes[0] != nodes[len(nodes)-1] {
		t.Fatalf("cycle not closed: %q", cyc)
	}
	if len(nodes) != 4 {
		t.Fatalf("expected 3 distinct nodes in cycle, got %q", cyc)
	}
}

// Cycle exists only for a specific configuration; the smallest such bit
// string must be selected.
func TestSmallestConfigSelected(t *testing.T) {
	in := &input{
		Switches: []string{"a", "b"},
		Edges: []edge{
			{From: "A", To: "B", When: "a && b"},
			{From: "B", To: "A", When: "b"},
			{From: "A", To: "C", When: "!a && b"},
			{From: "C", To: "A", When: "!a && b"},
		},
	}
	// Configs: 00 no, 01 has A<->C, 10 no, 11 has A<->B. Smallest is 01.
	out := mustRun(t, in)
	if !strings.HasPrefix(out, "CYCLE config=01 cycle=") {
		t.Fatalf("got %q, want config=01", out)
	}
	cyc := strings.TrimPrefix(out, "CYCLE config=01 cycle=")
	if cyc != "A->C->A" && cyc != "C->A->C" {
		t.Fatalf("wrong cycle for config 01: %q", cyc)
	}
}

// A self-loop edge is a cycle of length one.
func TestSelfLoop(t *testing.T) {
	in := &input{
		Switches: []string{"a"},
		Edges:    []edge{{From: "A", To: "A", When: "a"}},
	}
	out := mustRun(t, in)
	if out != "CYCLE config=1 cycle=A->A" {
		t.Fatalf("got %q", out)
	}
}

// Ten switches, no possible cycle: full enumeration count is 1024.
func TestFullEnumerationCount(t *testing.T) {
	in := &input{
		Switches: []string{"s0", "s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9"},
		Edges: []edge{
			{From: "A", To: "B", When: "s0 || s1"},
			{From: "B", To: "C", When: "(s2 && !s3) || s4"},
			{From: "C", To: "D"},
			{From: "D", To: "E", When: "!(s5 && s6 && s7 && s8 && s9)"},
		},
	}
	out := mustRun(t, in)
	if out != "ACYCLIC count=1024" {
		t.Fatalf("got %q, want ACYCLIC count=1024", out)
	}
}

// Guards that can never be simultaneously true along both directions of
// every potential loop keep the graph acyclic even with many edges.
func TestExclusiveTriangleNoCycle(t *testing.T) {
	in := &input{
		Switches: []string{"a", "b"},
		Edges: []edge{
			{From: "A", To: "B", When: "a && b"},
			{From: "B", To: "C", When: "a && !b"},
			{From: "C", To: "A", When: "!a"},
		},
	}
	out := mustRun(t, in)
	if out != "ACYCLIC count=4" {
		t.Fatalf("got %q, want ACYCLIC count=4", out)
	}
}

func TestTooManySwitches(t *testing.T) {
	in := &input{Switches: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}}
	if _, err := run(in); err == nil {
		t.Fatal("expected error for 11 switches")
	}
}

func TestUnknownSwitchInGuard(t *testing.T) {
	in := &input{
		Switches: []string{"a"},
		Edges:    []edge{{From: "A", To: "B", When: "zzz"}},
	}
	if _, err := run(in); err == nil {
		t.Fatal("expected error for unknown switch")
	}
}
