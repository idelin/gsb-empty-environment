package taskpkg

import (
	"math"
	"testing"
)

func cycleSign(cycle *ConflictCycle) int {
	sign := 1
	for _, step := range cycle.Steps {
		sign *= step.Sign
	}
	return sign
}

func cycleIsSimple(t *testing.T, cycle *ConflictCycle) {
	t.Helper()
	if cycle == nil {
		t.Fatal("nil cycle")
	}
	if cycle.Length != len(cycle.Steps) || cycle.Length != len(cycle.Nodes) {
		t.Fatalf("inconsistent cycle shape: %+v", cycle)
	}
	seen := make(map[int]bool, len(cycle.Nodes))
	for _, node := range cycle.Nodes {
		if seen[node] {
			t.Fatalf("cycle repeats node %d: %+v", node, cycle)
		}
		seen[node] = true
	}
	for i, step := range cycle.Steps {
		from := cycle.Nodes[i]
		to := cycle.Nodes[(i+1)%len(cycle.Nodes)]
		if step.From != from || step.To != to {
			t.Fatalf("step %d = %+v, nodes require %d->%d", i, step, from, to)
		}
	}
	if cycleSign(cycle) != -1 {
		t.Fatalf("cycle sign = %d, want -1", cycleSign(cycle))
	}
}

func TestCheckPolarityConsistent(t *testing.T) {
	edges := []SignedEdge{
		{A: 0, B: 1, Sign: 1},
		{A: 1, B: 2, Sign: 1},
		{A: 2, B: 3, Sign: -1},
		{A: 3, B: 0, Sign: -1},
	}
	result, err := CheckPolarity(4, edges)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Consistent || result.Conflict != nil {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Assignment) != 4 {
		t.Fatalf("assignment = %v", result.Assignment)
	}
	for _, edge := range edges {
		if result.Assignment[edge.A]*result.Assignment[edge.B] != edge.Sign {
			t.Errorf("edge %+v violates assignment %v", edge, result.Assignment)
		}
	}
}

func TestShortestConflictTriangle(t *testing.T) {
	// A long even cycle and a short triangle. The triangle must win.
	edges := []SignedEdge{
		{A: 0, B: 1, Sign: 1},
		{A: 1, B: 2, Sign: 1},
		{A: 2, B: 3, Sign: 1},
		{A: 3, B: 4, Sign: 1},
		{A: 4, B: 5, Sign: -1},
		{A: 5, B: 0, Sign: 1},
		{A: 0, B: 2, Sign: -1},
	}
	result, err := CheckPolarity(6, edges)
	if err != nil {
		t.Fatal(err)
	}
	if result.Consistent {
		t.Fatal("contradictory graph reported consistent")
	}
	if result.Conflict == nil || result.Conflict.Length != 3 {
		t.Fatalf("conflict = %+v, want length 3", result.Conflict)
	}
	cycleIsSimple(t, result.Conflict)
}

func TestShortestConflictParallelEdges(t *testing.T) {
	edges := []SignedEdge{
		{A: 0, B: 1, Sign: 1},
		{A: 0, B: 1, Sign: -1},
		{A: 1, B: 2, Sign: 1},
	}
	result, err := CheckPolarity(3, edges)
	if err != nil {
		t.Fatal(err)
	}
	if result.Consistent || result.Conflict == nil {
		t.Fatalf("result = %+v", result)
	}
	if result.Conflict.Length != 2 {
		t.Fatalf("conflict length = %d, want 2", result.Conflict.Length)
	}
	cycleIsSimple(t, result.Conflict)
}

func TestShortestConflictViaNeighbor(t *testing.T) {
	edges := []SignedEdge{
		{A: 0, B: 1, Sign: 1},
		{A: 1, B: 2, Sign: 1},
		{A: 2, B: 3, Sign: -1},
		{A: 1, B: 3, Sign: 1},
		{A: 0, B: 4, Sign: 1},
		{A: 4, B: 3, Sign: 1},
	}
	result, err := CheckPolarity(5, edges)
	if err != nil {
		t.Fatal(err)
	}
	if result.Consistent {
		t.Fatal("expected inconsistency")
	}
	// 1-2(+),2-3(-),3-1(+) is the shortest negative cycle.
	if result.Conflict.Length != 3 {
		t.Fatalf("cycle = %+v, want length 3", result.Conflict)
	}
	cycleIsSimple(t, result.Conflict)
}

func TestNegativeSelfLoopAndValidation(t *testing.T) {
	edges := []SignedEdge{{A: 2, B: 2, Sign: -1}}
	result, err := CheckPolarity(3, edges)
	if err != nil {
		t.Fatal(err)
	}
	if result.Consistent || result.Conflict == nil || result.Conflict.Length != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := CheckPolarity(2, []SignedEdge{{A: 0, B: 2, Sign: 1}}); err == nil {
		t.Fatal("out-of-range edge accepted")
	}
	if _, err := CheckPolarity(2, []SignedEdge{{A: 0, B: 1, Sign: 0}}); err == nil {
		t.Fatal("zero edge sign accepted")
	}
}

func TestAllNegativeCycleLengths(t *testing.T) {
	// Sanity-check the shortest-cycle algorithm against a brute-force
	// enumeration on small deterministic graphs.
	for n := 2; n <= 5; n++ {
		possible := n * (n - 1) / 2
		total := 1
		for i := 0; i < possible; i++ {
			total *= 3 // absent, +1, -1
		}
		for code := 0; code < total; code++ {
			var edges []SignedEdge
			value := code
			for i := 0; i < n; i++ {
				for j := i + 1; j < n; j++ {
					switch value % 3 {
					case 1:
						edges = append(edges, SignedEdge{A: i, B: j, Sign: 1})
					case 2:
						edges = append(edges, SignedEdge{A: i, B: j, Sign: -1})
					}
					value /= 3
				}
			}
			result, err := CheckPolarity(n, edges)
			if err != nil {
				t.Fatal(err)
			}
			want := bruteShortestNegativeCycle(n, edges)
			got := 0
			if result.Conflict != nil {
				got = result.Conflict.Length
			}
			if want == math.MaxInt {
				if result.Conflict != nil {
					t.Fatalf("n=%d code=%d edges=%+v unexpectedly found cycle %+v", n, code, edges, result.Conflict)
				}
			} else if result.Conflict == nil || got != want {
				t.Fatalf("n=%d code=%d edges=%+v got=%d want=%d", n, code, edges, got, want)
			}
			if result.Conflict != nil {
				cycleIsSimple(t, result.Conflict)
			}
		}
	}
}

func bruteShortestNegativeCycle(n int, edges []SignedEdge) int {
	type adjacency struct {
		to   int
		sign int
		id   int
	}
	graph := make([][]adjacency, n)
	for i, edge := range edges {
		graph[edge.A] = append(graph[edge.A], adjacency{to: edge.B, sign: edge.Sign, id: i})
		if edge.A != edge.B {
			graph[edge.B] = append(graph[edge.B], adjacency{to: edge.A, sign: edge.Sign, id: i})
		}
	}
	best := math.MaxInt
	var search func(start, node, sign, usedEdges int, visited []bool)
	search = func(start, node, sign, usedEdges int, visited []bool) {
		if usedEdges > best {
			return
		}
		for _, next := range graph[node] {
			if usedEdges == 0 && next.to < start {
				continue
			}
			if next.to == start && usedEdges >= 1 {
				if sign*next.sign < 0 && usedEdges+1 < best {
					best = usedEdges + 1
				}
				continue
			}
			if visited[next.to] {
				continue
			}
			visited[next.to] = true
			search(start, next.to, sign*next.sign, usedEdges+1, visited)
			visited[next.to] = false
		}
	}
	for start := 0; start < n; start++ {
		visited := make([]bool, n)
		visited[start] = true
		search(start, start, 1, 0, visited)
	}
	return best
}
