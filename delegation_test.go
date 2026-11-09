package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestShortPathExhaustsBudget covers the required trap: the fewest-hop path
// reaches node x with an exhausted budget, while a longer path reaches x with
// a remaining hop and can still delegate to y. Budgets arriving at the same
// subject through different paths must stay separate.
func TestShortPathExhaustsBudget(t *testing.T) {
	in := inputPayload{
		Root: "r",
		Edges: []Edge{
			// Short path r -> a -> x leaves x with budget 0.
			{From: "r", To: "a", Start: 0, End: 20, Layers: 1},
			{From: "a", To: "x", Start: 0, End: 20, Layers: 1},

			// Long path r -> b -> c -> x leaves x with budget 1.
			{From: "r", To: "b", Start: 0, End: 20, Layers: 3},
			{From: "b", To: "c", Start: 0, End: 20, Layers: 3},
			{From: "c", To: "x", Start: 0, End: 20, Layers: 2},

			// Only the long path's remaining budget can take this hop.
			{From: "x", To: "y", Start: 0, End: 20, Layers: 1},
		},
		Queries: []Query{
			{At: 10, Who: "x"},
			{At: 10, Who: "y"},
			{At: 10, Who: "r"},
			{At: 10, Who: "missing"},
		},
	}

	got := solve(in)
	want := []int{2, 4, 0, -1}
	assertInts(t, got, want)
}

// TestCyclesTerminate verifies that cycles never cause an infinite search:
// every hop strictly decreases a non-root budget, so states run out finitely.
func TestCyclesTerminate(t *testing.T) {
	in := inputPayload{
		Root: "r",
		Edges: []Edge{
			{From: "r", To: "p", Start: 0, End: 20, Layers: 1},
			{From: "p", To: "r", Start: 0, End: 20, Layers: 1},
		},
		Queries: []Query{
			{At: 10, Who: "p"},
			{At: 10, Who: "r"},
		},
	}

	assertInts(t, solve(in), []int{1, 0})
}

// TestHalfOpenInterval checks [start, end): start is included, end excluded.
func TestHalfOpenInterval(t *testing.T) {
	adj := map[Subject][]Edge{
		"r": {{From: "r", To: "z", Start: 5, End: 10, Layers: 2}},
	}

	cases := []struct {
		at   int64
		want int
	}{
		{4, -1},
		{5, 1},
		{9, 1},
		{10, -1},
	}
	for _, tc := range cases {
		got := minHops("r", adj, Query{At: tc.at, Who: "z"})
		if got != tc.want {
			t.Fatalf("at=%d: got %d, want %d", tc.at, got, tc.want)
		}
	}
}

// TestZeroLayerEdgeFromRoot confirms a zero-layer edge is still traversable
// from root (unlimited budget), but the receiver cannot delegate further.
func TestZeroLayerEdgeFromRoot(t *testing.T) {
	in := inputPayload{
		Root: "r",
		Edges: []Edge{
			{From: "r", To: "z", Start: 0, End: 20, Layers: 0},
			{From: "z", To: "w", Start: 0, End: 20, Layers: 1},
		},
		Queries: []Query{
			{At: 10, Who: "z"},
			{At: 10, Who: "w"},
		},
	}

	assertInts(t, solve(in), []int{1, -1})
}

func TestCLIEndToEnd(t *testing.T) {
	stdin := strings.NewReader(`{
		"root": "r",
		"edges": [
			["r", "a", 0, 20, 1],
			["a", "x", 0, 20, 1],
			["r", "b", 0, 20, 3],
			["b", "c", 0, 20, 3],
			["c", "x", 0, 20, 2],
			["x", "y", 0, 20, 1]
		],
		"queries": [[10, "y"], [10, "x"], [10, "ghost"]]
	}`)

	var stdout bytes.Buffer
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got, want := strings.TrimSpace(stdout.String()), "[4,2,-1]"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestNumericSubjects(t *testing.T) {
	in := inputPayload{
		Root: "7",
		Edges: []Edge{
			{From: "7", To: "8", Start: 0, End: 20, Layers: 1},
		},
		Queries: []Query{{At: 10, Who: "8"}},
	}

	stdin := strings.NewReader(`{"root":7,"edges":[[7,8,0,20,1]],"queries":[[10,8]]}`)
	var stdout bytes.Buffer
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "[1]" {
		t.Fatalf("stdout = %q, want [1]", got)
	}

	assertInts(t, solve(in), []int{1})
}

func assertInts(t *testing.T, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %d, want %d (full %v)", i, got[i], want[i], got)
		}
	}
}
