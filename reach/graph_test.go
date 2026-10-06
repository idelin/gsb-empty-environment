package reach

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// fullClosure computes positive-length reachability by running a BFS
// from every node over edges with positive multiplicity.
func fullClosure(edges map[Pair]int) map[int]map[int]bool {
	out := make(map[int]map[int]bool)
	for p, c := range edges {
		if c <= 0 {
			continue
		}
		if out[p.From] == nil {
			out[p.From] = make(map[int]bool)
		}
		out[p.From][p.To] = true
	}
	closure := make(map[int]map[int]bool)
	nodes := make(map[int]bool)
	for p := range edges {
		nodes[p.From] = true
		nodes[p.To] = true
	}
	for s := range nodes {
		reached := make(map[int]bool)
		var queue []int
		for w := range out[s] {
			if !reached[w] {
				reached[w] = true
				queue = append(queue, w)
			}
		}
		for i := 0; i < len(queue); i++ {
			for w := range out[queue[i]] {
				if !reached[w] {
					reached[w] = true
					queue = append(queue, w)
				}
			}
		}
		closure[s] = reached
	}
	return closure
}

func indexClosure(g *Graph, nodes []int) map[int]map[int]bool {
	closure := make(map[int]map[int]bool)
	for _, u := range nodes {
		set := make(map[int]bool)
		for _, v := range g.ReachableFrom(u) {
			set[v] = true
		}
		closure[u] = set
	}
	return closure
}

func diffClosure(before, after map[int]map[int]bool, nodes []int) []Change {
	var changes []Change
	for _, u := range nodes {
		for _, v := range nodes {
			was := before[u][v]
			is := after[u][v]
			if was != is {
				changes = append(changes, Change{From: u, To: v, Added: is})
			}
		}
	}
	sortChanges(changes)
	return changes
}

func sortedNodes(edges map[Pair]int) []int {
	set := make(map[int]bool)
	for p := range edges {
		set[p.From] = true
		set[p.To] = true
	}
	var nodes []int
	for n := range set {
		nodes = append(nodes, n)
	}
	sort.Ints(nodes)
	return nodes
}

// TestRandomOpsAgainstBFS applies random edge insertions and deletions
// (including duplicate edges and removals of absent edges) and, after
// every single operation, checks the index against a full-graph BFS and
// the reported changes against the closure diff.
func TestRandomOpsAgainstBFS(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 99, 2024} {
		rng := rand.New(rand.NewSource(seed))
		g := New()
		edges := make(map[Pair]int)
		const n = 9
		nodes := make([]int, n)
		for i := range nodes {
			nodes[i] = i
		}
		prev := fullClosure(edges)
		for round := 0; round < 800; round++ {
			u, v := rng.Intn(n), rng.Intn(n)
			p := Pair{u, v}
			var got []Change
			if rng.Intn(100) < 55 || len(edges) == 0 {
				got = g.AddEdge(u, v)
				edges[p]++
			} else {
				got = g.RemoveEdge(u, v)
				if edges[p] > 0 {
					edges[p]--
					if edges[p] == 0 {
						delete(edges, p)
					}
				}
			}
			want := fullClosure(edges)
			all := sortedNodes(edges)
			if gotCnt := g.EdgeCount(u, v); gotCnt != edges[p] {
				t.Fatalf("seed %d round %d: edge count of %v = %d, want %d",
					seed, round, p, gotCnt, edges[p])
			}
			for _, x := range all {
				for _, y := range all {
					if g.Reachable(x, y) != want[x][y] {
						t.Fatalf("seed %d round %d: Reachable(%d,%d)=%v, BFS says %v",
							seed, round, x, y, g.Reachable(x, y), want[x][y])
					}
				}
			}
			if !reflect.DeepEqual(indexClosure(g, all), want) {
				t.Fatalf("seed %d round %d: index closure mismatch", seed, round)
			}
			wantChanges := diffClosure(prev, want, nodes)
			if !reflect.DeepEqual(got, wantChanges) {
				t.Fatalf("seed %d round %d: changes = %v, want %v",
					seed, round, got, wantChanges)
			}
			prev = want
		}
	}
}

func mustChanges(t *testing.T, got []Change, want ...Change) {
	t.Helper()
	sortChanges(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
}

// TestSelfLoopAndCycle: a node reaches itself only via a loop or cycle.
func TestSelfLoopAndCycle(t *testing.T) {
	g := New()
	g.AddEdge(1, 2)
	if g.Reachable(1, 1) || g.Reachable(2, 2) {
		t.Fatal("no self-reachability without a cycle")
	}
	mustChanges(t, g.AddEdge(2, 2), Change{2, 2, true})
	if !g.Reachable(1, 2) || !g.Reachable(2, 2) || g.Reachable(1, 1) {
		t.Fatal("self-loop must not make predecessors self-reachable")
	}
	// Closing a cycle 1->2->3->1 makes every member self-reachable.
	g2 := New()
	g2.AddEdge(1, 2)
	g2.AddEdge(2, 3)
	mustChanges(t, g2.AddEdge(3, 1),
		Change{1, 1, true}, Change{2, 1, true}, Change{3, 1, true},
		Change{2, 2, true}, Change{3, 2, true}, Change{3, 3, true})
}

// TestBridgeRemovalKillsCycleSupport is the pseudo-reachability trap:
// after the bridge feeding a cycle is removed, the cycle must not keep
// the source's reachability alive by supporting itself.
func TestBridgeRemovalKillsCycleSupport(t *testing.T) {
	g := New()
	for _, e := range [][2]int{{1, 2}, {1, 3}, {2, 4}, {3, 4}, {4, 2}} {
		g.AddEdge(e[0], e[1])
	}
	for _, v := range []int{2, 3, 4} {
		if !g.Reachable(1, v) {
			t.Fatalf("1 should reach %d", v)
		}
	}
	g.RemoveEdge(1, 2)
	if !g.Reachable(1, 2) { // still reachable via 1->3->4->2
		t.Fatal("1 should still reach 2 through the cycle")
	}
	// Removing the last out-edge of 1 must drop the whole cycle, even
	// though 2->4->2 keeps "supporting" itself in a naive count index.
	mustChanges(t, g.RemoveEdge(1, 3),
		Change{1, 2, false}, Change{1, 3, false}, Change{1, 4, false})
	if got := g.ReachableFrom(1); len(got) != 0 {
		t.Fatalf("1 must reach nothing, got %v", got)
	}
	// The detached cycle still reaches itself internally.
	for _, p := range []Pair{{2, 4}, {4, 2}, {2, 2}, {4, 4}} {
		if !g.Reachable(p.From, p.To) {
			t.Fatalf("%d should still reach %d", p.From, p.To)
		}
	}
}

// TestDuplicateEdges: repeated adds need matching removes.
func TestDuplicateEdges(t *testing.T) {
	g := New()
	g.AddEdge(1, 2)
	g.AddEdge(1, 2)
	g.AddEdge(1, 2)
	if c := g.EdgeCount(1, 2); c != 3 {
		t.Fatalf("edge count = %d, want 3", c)
	}
	if got := g.RemoveEdge(1, 2); got != nil {
		t.Fatalf("removing one of three copies must change nothing, got %v", got)
	}
	if !g.Reachable(1, 2) {
		t.Fatal("edge must survive until the last copy is removed")
	}
	g.RemoveEdge(1, 2)
	mustChanges(t, g.RemoveEdge(1, 2), Change{1, 2, false})
	if got := g.RemoveEdge(1, 2); got != nil {
		t.Fatalf("removing an absent edge must be a no-op, got %v", got)
	}
}

// TestDisconnectedRejoin exercises change reporting on reconnection.
func TestDisconnectedRejoin(t *testing.T) {
	g := New()
	g.AddEdge(1, 2)
	g.AddEdge(3, 4)
	mustChanges(t, g.AddEdge(2, 3),
		Change{1, 3, true}, Change{1, 4, true}, Change{2, 3, true}, Change{2, 4, true})
	mustChanges(t, g.RemoveEdge(2, 3),
		Change{1, 3, false}, Change{1, 4, false}, Change{2, 3, false}, Change{2, 4, false})
}

func ExampleGraph_AddEdge() {
	g := New()
	g.AddEdge(1, 2)
	g.AddEdge(2, 3)
	fmt.Println(g.AddEdge(3, 1))
	fmt.Println(g.ReachableFrom(2))
	// Output:
	// [{1 1 true} {2 1 true} {2 2 true} {3 1 true} {3 2 true} {3 3 true}]
	// [1 2 3]
}
