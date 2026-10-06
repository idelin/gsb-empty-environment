package tasksched

import (
	"math/rand"
	"testing"
)

func TestSortMatchesExhaustiveLexMin(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for n := 1; n <= 7; n++ {
		for trial := 0; trial < 200; trial++ {
			var edges []Edge
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					if i != j && r.Float64() < 0.25 {
						edges = append(edges, Edge{i, j})
					}
				}
			}
			res := Sort(n, edges)
			best, ok := LexMinOrder(n, edges)
			if !ok {
				if res.Cycle == nil {
					t.Fatalf("n=%d edges=%v: expected cycle, got order %v", n, edges, res.Order)
				}
				assertRealCycle(t, edges, res.Cycle)
				assertExecutablePrefix(t, n, edges, res.Order)
				continue
			}
			if res.Cycle != nil {
				t.Fatalf("n=%d edges=%v: unexpected cycle %v", n, edges, res.Cycle)
			}
			if !equalInts(res.Order, best) {
				t.Fatalf("n=%d edges=%v: Sort=%v, exhaustive lex-min=%v", n, edges, res.Order, best)
			}
		}
	}
}

func TestSortDeduplicatesConstraints(t *testing.T) {
	base := []Edge{{0, 2}, {1, 2}, {0, 3}}
	dup := []Edge{{0, 2}, {1, 2}, {0, 3}, {0, 2}, {0, 2}, {1, 2}, {0, 3}}
	a, b := Sort(4, base), Sort(4, dup)
	if !equalInts(a.Order, b.Order) {
		t.Fatalf("duplicate constraints changed order: %v vs %v", a.Order, b.Order)
	}
	want := []int{0, 1, 2, 3}
	if !equalInts(a.Order, want) {
		t.Fatalf("order = %v, want %v", a.Order, want)
	}
}

func TestSortReportsRealCycleAndExecutablePrefix(t *testing.T) {
	edges := []Edge{{0, 1}, {2, 3}, {3, 2}}
	res := Sort(4, edges)
	if !equalInts(res.Order, []int{0, 1}) {
		t.Fatalf("executable prefix = %v, want [0 1]", res.Order)
	}
	assertRealCycle(t, edges, res.Cycle)
	if len(res.Cycle) != 2 {
		t.Fatalf("cycle = %v, want the 2-node loop", res.Cycle)
	}
}

func TestSortSelfLoop(t *testing.T) {
	res := Sort(2, []Edge{{1, 1}})
	if len(res.Order) != 1 || res.Order[0] != 0 {
		t.Fatalf("prefix = %v, want [0]", res.Order)
	}
	if len(res.Cycle) != 1 || res.Cycle[0] != 1 {
		t.Fatalf("cycle = %v, want [1]", res.Cycle)
	}
}

// assertRealCycle verifies the reported loop is genuine: consecutive
// tasks (including last->first) are real constraints.
func assertRealCycle(t *testing.T, edges []Edge, cycle []int) {
	t.Helper()
	if len(cycle) == 0 {
		t.Fatal("empty cycle")
	}
	seen := make(map[int]bool)
	for _, v := range cycle {
		if seen[v] {
			t.Fatalf("cycle %v repeats task %d", cycle, v)
		}
		seen[v] = true
	}
	set := make(map[Edge]bool)
	for _, e := range edges {
		set[e] = true
	}
	for i, v := range cycle {
		w := cycle[(i+1)%len(cycle)]
		if !set[Edge{v, w}] {
			t.Fatalf("cycle %v contains non-constraint %d -> %d", cycle, v, w)
		}
	}
}

// assertExecutablePrefix verifies every prefix task has all its
// predecessors inside the prefix, earlier.
func assertExecutablePrefix(t *testing.T, n int, edges []Edge, prefix []int) {
	t.Helper()
	pos := make(map[int]int, len(prefix))
	for i, v := range prefix {
		pos[v] = i
	}
	if len(pos) != len(prefix) {
		t.Fatalf("prefix %v contains duplicates", prefix)
	}
	for _, e := range edges {
		if to, ok := pos[e.To]; ok {
			from, ok := pos[e.From]
			if !ok || from >= to {
				t.Fatalf("prefix %v violates constraint %v", prefix, e)
			}
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
