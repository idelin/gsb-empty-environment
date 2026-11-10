package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func sid(v int64) string {
	return strconv.FormatInt(v, 10)
}

func buildAdjacency(t *testing.T, edges ...Edge) map[string][]Edge {
	t.Helper()
	adjacency := make(map[string][]Edge)
	for _, edge := range edges {
		adjacency[edge.From] = append(adjacency[edge.From], edge)
	}
	return adjacency
}

// The shortest route to node 2 ends with budget 0, so it cannot delegate to
// node 6. The longer route reaches node 2 with budget 1 and can still take the
// final hop. A node-only visited set would discard the higher-budget state and
// wrongly report node 6 as unreachable.
func TestMinHopsShortPathExhaustedLongPathDelegatable(t *testing.T) {
	edges := []Edge{
		{From: sid(1), To: sid(2), Start: 0, End: 100, Layers: 0},
		{From: sid(2), To: sid(3), Start: 0, End: 100, Layers: 2},
		{From: sid(1), To: sid(4), Start: 0, End: 100, Layers: 3},
		{From: sid(4), To: sid(5), Start: 0, End: 100, Layers: 2},
		{From: sid(5), To: sid(2), Start: 0, End: 100, Layers: 1},
		{From: sid(2), To: sid(6), Start: 0, End: 100, Layers: 5},
	}
	adjacency := buildAdjacency(t, edges...)

	cases := []struct {
		target string
		want   int
	}{
		{target: sid(1), want: 0},
		{target: sid(2), want: 1},
		{target: sid(3), want: 4},
		{target: sid(4), want: 1},
		{target: sid(5), want: 2},
		{target: sid(6), want: 4},
		{target: sid(99), want: -1},
	}
	for _, tc := range cases {
		if got := minHops(sid(1), tc.target, adjacency, 10); got != tc.want {
			t.Errorf("minHops to %s at t=10 = %d, want %d", tc.target, got, tc.want)
		}
	}
}

func TestTimeWindowIsHalfOpen(t *testing.T) {
	edges := []Edge{
		{From: sid(1), To: sid(2), Start: 0, End: 11, Layers: 3},
		{From: sid(2), To: sid(3), Start: 10, End: 20, Layers: 1},
		{From: sid(1), To: sid(7), Start: 10, End: 20, Layers: 1},
	}
	adjacency := buildAdjacency(t, edges...)

	if got := minHops(sid(1), sid(3), adjacency, 9); got != -1 {
		t.Errorf("at t=9 second edge is not yet active, got %d, want -1", got)
	}
	if got := minHops(sid(1), sid(3), adjacency, 10); got != 2 {
		t.Errorf("at t=10 both edges are active, got %d, want 2", got)
	}
	if got := minHops(sid(1), sid(7), adjacency, 10); got != 1 {
		t.Errorf("start is inclusive, got %d, want 1", got)
	}
	if got := minHops(sid(1), sid(7), adjacency, 20); got != -1 {
		t.Errorf("end is exclusive, got %d, want -1", got)
	}
}

func TestCyclesTerminateAndDoNotBlockShortestPath(t *testing.T) {
	edges := []Edge{
		{From: sid(1), To: sid(2), Start: 0, End: 100, Layers: 5},
		{From: sid(2), To: sid(3), Start: 0, End: 100, Layers: 4},
		{From: sid(3), To: sid(2), Start: 0, End: 100, Layers: 3},
	}
	adjacency := buildAdjacency(t, edges...)

	if got := minHops(sid(1), sid(2), adjacency, 5); got != 1 {
		t.Errorf("shortest hop to node 2 = %d, want 1", got)
	}
	if got := minHops(sid(1), sid(3), adjacency, 5); got != 2 {
		t.Errorf("shortest hop to node 3 = %d, want 2", got)
	}
}

func TestNextBudget(t *testing.T) {
	cases := []struct {
		budget int64
		layers int64
		want   int64
	}{
		{budget: infBudget, layers: 3, want: 3},
		{budget: 3, layers: 5, want: 2},
		{budget: 3, layers: 2, want: 2},
		{budget: 1, layers: 0, want: 0},
		{budget: 1, layers: 1, want: 0},
	}
	for _, tc := range cases {
		if got := nextBudget(tc.budget, tc.layers); got != tc.want {
			t.Errorf("nextBudget(%d, %d) = %d, want %d", tc.budget, tc.layers, got, tc.want)
		}
	}
}

func TestRunOutputsJSONArray(t *testing.T) {
	input := `{
		"root": 1,
		"edges": [
			[1, 2, 0, 100, 0],
			[2, 3, 0, 100, 2],
			[1, 4, 0, 100, 3],
			[4, 5, 0, 100, 2],
			[5, 2, 0, 100, 1],
			[2, 6, 0, 100, 5]
		],
		"queries": [[10, 1], [10, 2], [10, 6], [10, 3], [10, 99]]
	}`
	var output bytes.Buffer
	if err := run(strings.NewReader(input), &output); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if got := strings.TrimSpace(output.String()); got != "[0,1,4,4,-1]" {
		t.Errorf("output = %q, want [0,1,4,4,-1]", got)
	}
}

func TestRejectsNegativeLayers(t *testing.T) {
	input := `{"root": 1, "edges": [[1, 2, 0, 100, -1]], "queries": []}`
	if err := run(strings.NewReader(input), &bytes.Buffer{}); err == nil {
		t.Fatal("negative delegation layers should be rejected")
	}
}
