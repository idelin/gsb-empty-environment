package dagrank

import (
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// enumeratePaths lists every source-to-sink path as an edge-id sequence,
// in lexicographic order (adjacency is id-sorted and the empty path is
// emitted first when u == sink).
func enumeratePaths(g *Graph, source, sink int) [][]int {
	var paths [][]int
	var walk func(u int, cur []int)
	walk = func(u int, cur []int) {
		if u == sink {
			paths = append(paths, append([]int{}, cur...))
		}
		for _, e := range g.adj[u] {
			walk(e.To, append(cur, e.ID))
		}
	}
	walk(source, nil)
	return paths
}

func lexLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// checkGraph verifies Count, Rank and Unrank against full enumeration.
func checkGraph(t *testing.T, g *Graph, source, sink int) {
	t.Helper()
	paths := enumeratePaths(g, source, sink)

	if !sort.SliceIsSorted(paths, func(i, j int) bool { return lexLess(paths[i], paths[j]) }) {
		t.Fatalf("enumeration not in lexicographic order")
	}

	count, err := g.Count(source, sink)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count.Cmp(big.NewInt(int64(len(paths)))) != 0 {
		t.Fatalf("Count = %s, want %d", count, len(paths))
	}

	for i, p := range paths {
		r, err := g.Rank(source, sink, p)
		if err != nil {
			t.Fatalf("Rank(%v): %v", p, err)
		}
		if r.Cmp(big.NewInt(int64(i))) != 0 {
			t.Fatalf("Rank(%v) = %s, want %d", p, r, i)
		}
		q, err := g.Unrank(source, sink, r)
		if err != nil {
			t.Fatalf("Unrank(%s): %v", r, err)
		}
		if !reflect.DeepEqual(q, p) && !(len(q) == 0 && len(p) == 0) {
			t.Fatalf("Unrank(Rank(%v)) = %v", p, q)
		}
	}

	// Out-of-range ranks must fail.
	if _, err := g.Unrank(source, sink, big.NewInt(-1)); err == nil {
		t.Fatalf("Unrank(-1) did not fail")
	}
	if _, err := g.Unrank(source, sink, count); err == nil {
		t.Fatalf("Unrank(count) did not fail")
	}
	over := new(big.Int).Add(count, big.NewInt(1))
	if _, err := g.Unrank(source, sink, over); err == nil {
		t.Fatalf("Unrank(count+1) did not fail")
	}
}

func TestParallelEdgesAndUnreachable(t *testing.T) {
	// 0 --1--> 1 --4--> 3
	// 0 --2--> 1 (parallel, independent)
	// 0 --3--> 2 (dead end, cannot reach sink 3: occupies no rank)
	// 1 --5--> 3 (parallel with edge 4)
	// 3 --6--> 4 (edge leaving the sink; paths may pass through 3)
	edges := []Edge{
		{ID: 1, From: 0, To: 1},
		{ID: 2, From: 0, To: 1},
		{ID: 3, From: 0, To: 2},
		{ID: 4, From: 1, To: 3},
		{ID: 5, From: 1, To: 3},
		{ID: 6, From: 3, To: 4},
	}
	g, err := NewGraph(5, edges)
	if err != nil {
		t.Fatal(err)
	}
	checkGraph(t, g, 0, 3)

	// Dead-end branch via edge 3 must not occupy any rank: first path is [1,4].
	r, err := g.Rank(0, 3, []int{1, 4})
	if err != nil {
		t.Fatal(err)
	}
	if r.Sign() != 0 {
		t.Fatalf("Rank([1 4]) = %s, want 0 (dead-end branch must not count)", r)
	}

	// Unreachable sink: no paths at all.
	count, err := g.Count(4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if count.Sign() != 0 {
		t.Fatalf("Count(4,0) = %s, want 0", count)
	}
	if _, err := g.Unrank(4, 0, big.NewInt(0)); err == nil {
		t.Fatalf("Unrank into empty path set did not fail")
	}
}

func TestEmptyPath(t *testing.T) {
	g, err := NewGraph(3, []Edge{
		{ID: 10, From: 0, To: 1},
		{ID: 11, From: 1, To: 0},
	})
	if err == nil {
		t.Fatalf("expected cycle error")
	}

	g, err = NewGraph(3, []Edge{
		{ID: 10, From: 0, To: 1},
		{ID: 11, From: 0, To: 2},
	})
	if err != nil {
		t.Fatal(err)
	}

	// source == sink: empty path is valid and ranks first.
	count, err := g.Count(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if count.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("Count(0,0) = %s, want 1 (only the empty path)", count)
	}
	r, err := g.Rank(0, 0, nil)
	if err != nil {
		t.Fatalf("Rank(empty): %v", err)
	}
	if r.Sign() != 0 {
		t.Fatalf("Rank(empty) = %s, want 0", r)
	}
	p, err := g.Unrank(0, 0, big.NewInt(0))
	if err != nil {
		t.Fatalf("Unrank(0): %v", err)
	}
	if len(p) != 0 {
		t.Fatalf("Unrank(0) = %v, want empty path", p)
	}
	if _, err := g.Unrank(0, 0, big.NewInt(1)); err == nil {
		t.Fatalf("Unrank(1) past empty path did not fail")
	}

	// Empty path with source != sink is invalid.
	if _, err := g.Rank(0, 1, nil); err == nil {
		t.Fatalf("Rank(empty) with source != sink did not fail")
	}

	// source == sink with outgoing cycle-free loop back: node 1 -> 1 impossible
	// in a DAG, but 0==0 with edges 0->1, 0->2 only yields the empty path.
	checkGraph(t, g, 0, 0)
}

func TestInvalidRankInputs(t *testing.T) {
	g, err := NewGraph(4, []Edge{
		{ID: 1, From: 0, To: 1},
		{ID: 2, From: 1, To: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	bad := [][]int{
		{9},       // unknown edge id
		{2},       // edge 2 does not leave node 0
		{1},       // ends at 1, not sink 2
		{1, 2, 2}, // not contiguous
	}
	for _, p := range bad {
		if _, err := g.Rank(0, 2, p); err == nil {
			t.Fatalf("Rank(%v) did not fail", p)
		}
	}
	if _, err := NewGraph(2, []Edge{{ID: 1, From: 0, To: 1}, {ID: 1, From: 0, To: 1}}); err == nil {
		t.Fatalf("duplicate edge id accepted")
	}
}

func TestRandomDAGs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.Intn(8)
		var edges []Edge
		id := 0
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if rng.Intn(2) == 0 {
					continue
				}
				// Possibly multiple parallel edges i -> j.
				for k := 0; k < 1+rng.Intn(2); k++ {
					edges = append(edges, Edge{ID: id, From: i, To: j})
					id++
				}
			}
		}
		// Shuffle ids so adjacency order is not the insertion order.
		perm := rng.Perm(len(edges))
		shuffled := make([]Edge, len(edges))
		for i, e := range edges {
			e.ID = perm[i]
			shuffled[i] = e
		}
		g, err := NewGraph(n, shuffled)
		if err != nil {
			t.Fatal(err)
		}
		source := rng.Intn(n)
		sink := rng.Intn(n)
		checkGraph(t, g, source, sink)
	}
}

func TestBigRanks(t *testing.T) {
	// 100 layers, two parallel edges each: 2^100 paths, beyond uint64.
	n := 101
	var edges []Edge
	id := 0
	for i := 0; i < n-1; i++ {
		for k := 0; k < 2; k++ {
			edges = append(edges, Edge{ID: id, From: i, To: i + 1})
			id++
		}
	}
	g, err := NewGraph(n, edges)
	if err != nil {
		t.Fatal(err)
	}
	count, err := g.Count(0, n-1)
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Lsh(big.NewInt(1), 100)
	if count.Cmp(want) != 0 {
		t.Fatalf("Count = %s, want 2^100", count)
	}

	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 50; trial++ {
		r := new(big.Int).Rand(rng, count)
		p, err := g.Unrank(0, n-1, r)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 100 {
			t.Fatalf("path length = %d, want 100", len(p))
		}
		r2, err := g.Rank(0, n-1, p)
		if err != nil {
			t.Fatal(err)
		}
		if r2.Cmp(r) != 0 {
			t.Fatalf("Rank(Unrank(%s)) = %s", r, r2)
		}
	}
	if _, err := g.Unrank(0, n-1, count); err == nil {
		t.Fatalf("Unrank(2^100) did not fail")
	}
}
