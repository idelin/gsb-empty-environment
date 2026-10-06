package dagpath

import (
	"math/big"
	"reflect"
	"testing"
)

// enumerate lists every walk from src to the sink in lexicographic edge-ID
// order, using plain DFS independent of the ranking logic. Branches that
// cannot reach the sink emit nothing.
func enumerate(t *testing.T, ix *Indexer, src int) [][]int {
	t.Helper()
	var walks [][]int
	var cur []int
	var dfs func(node int)
	dfs = func(node int) {
		if node == ix.sink {
			walks = append(walks, append([]int(nil), cur...))
		}
		for _, e := range ix.adj[node] {
			cur = append(cur, e.ID)
			dfs(e.To)
			cur = cur[:len(cur)-1]
		}
	}
	dfs(src)
	return walks
}

// checkRoundTrip verifies, by full enumeration, that Rank and Unrank are
// mutually inverse over the whole rank space and that Count matches.
func checkRoundTrip(t *testing.T, ix *Indexer, src int) {
	t.Helper()
	walks := enumerate(t, ix, src)
	if got := ix.Count(src); got.Cmp(big.NewInt(int64(len(walks)))) != 0 {
		t.Fatalf("Count(%d) = %s, enumeration found %d walks", src, got, len(walks))
	}
	for i, want := range walks {
		k := big.NewInt(int64(i))
		rank, err := ix.Rank(src, want)
		if err != nil {
			t.Fatalf("Rank(%d, %v): %v", src, want, err)
		}
		if rank.Cmp(k) != 0 {
			t.Errorf("Rank(%d, %v) = %s, want %d", src, want, rank, i)
		}
		got, err := ix.Unrank(src, k)
		if err != nil {
			t.Fatalf("Unrank(%d, %d): %v", src, i, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Unrank(%d, %d) = %v, want %v", src, i, got, want)
		}
	}
	// Out of bounds: negative rank and rank == Count must fail.
	if _, err := ix.Unrank(src, big.NewInt(-1)); err == nil {
		t.Errorf("Unrank(%d, -1) did not fail", src)
	}
	if _, err := ix.Unrank(src, ix.Count(src)); err == nil {
		t.Errorf("Unrank(%d, Count) did not fail", src)
	}
}

// multigraph returns a DAG with parallel edges, non-contiguous edge IDs,
// dead-end branches that cannot reach the sink, and a node unreachable
// from the source.
//
//	0 --10--> 1 --3--> 3 (sink)      0 --20--> 1 (parallel)
//	0 --5--> 2 --1--> 3              1 --8--> 4 (dead end)
//	2 --2--> 5 (dead end)            6 --7--> 0 (unreachable from 0)
func multigraph(t *testing.T) *Indexer {
	t.Helper()
	ix, err := NewIndexer([]Edge{
		{ID: 10, From: 0, To: 1},
		{ID: 20, From: 0, To: 1}, // parallel edge, independent choice
		{ID: 5, From: 0, To: 2},
		{ID: 3, From: 1, To: 3},
		{ID: 8, From: 1, To: 4}, // dead end
		{ID: 1, From: 2, To: 3},
		{ID: 2, From: 2, To: 5}, // dead end
		{ID: 7, From: 6, To: 0}, // unreachable from source 0
	}, 3)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	return ix
}

func TestMultigraphRoundTrip(t *testing.T) {
	ix := multigraph(t)
	checkRoundTrip(t, ix, 0)
	checkRoundTrip(t, ix, 1)
	checkRoundTrip(t, ix, 2)
}

func TestLexicographicOrder(t *testing.T) {
	ix := multigraph(t)
	want := [][]int{
		{5, 1},  // via node 2
		{10, 3}, // via node 1, first parallel edge
		{20, 3}, // via node 1, second parallel edge
	}
	for i, w := range want {
		got, err := ix.Unrank(0, big.NewInt(int64(i)))
		if err != nil {
			t.Fatalf("Unrank(0, %d): %v", i, err)
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("Unrank(0, %d) = %v, want %v", i, got, w)
		}
	}
}

func TestUnreachableBranchesOccupyNoRanks(t *testing.T) {
	// Node 1 has a dead-end edge (id 8) with a smaller ID than the sink
	// edge... give the dead end the smaller ID to prove it is skipped.
	ix, err := NewIndexer([]Edge{
		{ID: 1, From: 0, To: 1},
		{ID: 2, From: 0, To: 9}, // dead end, smaller ID than edge 3
		{ID: 3, From: 1, To: 4},
	}, 4)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if got := ix.Count(0); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("Count(0) = %s, want 1", got)
	}
	path, err := ix.Unrank(0, big.NewInt(0))
	if err != nil {
		t.Fatalf("Unrank(0, 0): %v", err)
	}
	if !reflect.DeepEqual(path, []int{1, 3}) {
		t.Errorf("Unrank(0, 0) = %v, want [1 3]", path)
	}
	if got := ix.Count(9); got.Sign() != 0 {
		t.Errorf("Count(9) = %s, want 0 for dead-end node", got)
	}
	if _, err := ix.Unrank(9, big.NewInt(0)); err == nil {
		t.Error("Unrank(9, 0) did not fail for node with count 0")
	}
}

func TestEmptyPathSourceEqualsSink(t *testing.T) {
	ix := multigraph(t)
	if got := ix.Count(3); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("Count(3) = %s, want 1 (the empty walk)", got)
	}
	rank, err := ix.Rank(3, nil)
	if err != nil {
		t.Fatalf("Rank(3, []): %v", err)
	}
	if rank.Sign() != 0 {
		t.Errorf("Rank(3, []) = %s, want 0", rank)
	}
	path, err := ix.Unrank(3, big.NewInt(0))
	if err != nil {
		t.Fatalf("Unrank(3, 0): %v", err)
	}
	if len(path) != 0 {
		t.Errorf("Unrank(3, 0) = %v, want empty path", path)
	}
	checkRoundTrip(t, ix, 3)
}

func TestBigIntRanks(t *testing.T) {
	// 100 layers, two parallel edges each: exactly 2^100 walks.
	var edges []Edge
	const layers = 100
	for i := 0; i < layers; i++ {
		edges = append(edges,
			Edge{ID: 0, From: i, To: i + 1},
			Edge{ID: 1, From: i, To: i + 1},
		)
	}
	ix, err := NewIndexer(edges, layers)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	total := new(big.Int).Lsh(big.NewInt(1), layers)
	if got := ix.Count(0); got.Cmp(total) != 0 {
		t.Fatalf("Count(0) = %s, want 2^%d = %s", got, layers, total)
	}
	// Round-trip the largest rank and a scattered set of big ranks.
	last := new(big.Int).Sub(total, big.NewInt(1))
	ranks := []*big.Int{last, big.NewInt(0)}
	for i := uint(0); i < layers; i += 7 {
		ranks = append(ranks, new(big.Int).Lsh(big.NewInt(1), i))
	}
	for _, k := range ranks {
		path, err := ix.Unrank(0, k)
		if err != nil {
			t.Fatalf("Unrank(0, %s): %v", k, err)
		}
		if len(path) != layers {
			t.Fatalf("Unrank(0, %s) has %d edges, want %d", k, len(path), layers)
		}
		back, err := ix.Rank(0, path)
		if err != nil {
			t.Fatalf("Rank(0, %v...): %v", path[:4], err)
		}
		if back.Cmp(k) != 0 {
			t.Errorf("Rank(Unrank(%s)) = %s", k, back)
		}
	}
	// The all-ones path must be the very last rank.
	path, err := ix.Unrank(0, last)
	if err != nil {
		t.Fatalf("Unrank(0, 2^100-1): %v", err)
	}
	for _, id := range path {
		if id != 1 {
			t.Errorf("last walk = %v, want all edge id 1", path)
			break
		}
	}
	if _, err := ix.Unrank(0, total); err == nil {
		t.Error("Unrank(0, 2^100) did not fail")
	}
}

func TestRankRejectsInvalidWalks(t *testing.T) {
	ix := multigraph(t)
	// Edge ID does not leave the current node.
	if _, err := ix.Rank(0, []int{10, 1}); err == nil {
		t.Error("Rank accepted edge id 1 leaving node 1")
	}
	// Walk stops short of the sink.
	if _, err := ix.Rank(0, []int{10}); err == nil {
		t.Error("Rank accepted walk ending at node 1, not the sink")
	}
	// Walk wanders into a dead end.
	if _, err := ix.Rank(0, []int{10, 8}); err == nil {
		t.Error("Rank accepted walk ending at dead-end node 4")
	}
	// Unknown edge ID outright.
	if _, err := ix.Rank(0, []int{99}); err == nil {
		t.Error("Rank accepted unknown edge id 99")
	}
}

func TestNewIndexerRejectsCycleAndDuplicateIDs(t *testing.T) {
	if _, err := NewIndexer([]Edge{
		{ID: 1, From: 0, To: 1},
		{ID: 2, From: 1, To: 0},
	}, 1); err == nil {
		t.Error("NewIndexer accepted a cyclic graph")
	}
	if _, err := NewIndexer([]Edge{
		{ID: 1, From: 0, To: 1},
		{ID: 1, From: 0, To: 2},
	}, 2); err == nil {
		t.Error("NewIndexer accepted duplicate edge ids on node 0")
	}
}
