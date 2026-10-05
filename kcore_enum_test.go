package coreindex_test

import (
	"math/rand"
	"reflect"
	"testing"

	"coreindex"
)

// adjacencyOf rebuilds an adjacency map from the index.
func adjacencyOf(ix *coreindex.Index) map[int64]map[int64]bool {
	adj := make(map[int64]map[int64]bool)
	for _, id := range ix.Nodes() {
		adj[id] = make(map[int64]bool)
		for _, w := range ix.Neighbors(id) {
			adj[id][w] = true
		}
	}
	return adj
}

// kCoreByEnumeration computes the k-core by its defining property:
// the union of all induced subgraphs whose minimum degree is at
// least k (that union is itself the maximal such subgraph).
func kCoreByEnumeration(nodes []int64, adj map[int64]map[int64]bool, k int) map[int64]bool {
	union := make(map[int64]bool)
	n := len(nodes)
	for mask := 1; mask < (1 << uint(n)); mask++ {
		ok := true
		for i := 0; i < n && ok; i++ {
			if mask&(1<<uint(i)) == 0 {
				continue
			}
			deg := 0
			for j := 0; j < n; j++ {
				if mask&(1<<uint(j)) != 0 && adj[nodes[i]][nodes[j]] {
					deg++
				}
			}
			if deg < k {
				ok = false
			}
		}
		if ok {
			for i := 0; i < n; i++ {
				if mask&(1<<uint(i)) != 0 {
					union[nodes[i]] = true
				}
			}
		}
	}
	return union
}

// kCoreByPeeling computes the k-core by iteratively removing nodes
// with degree < k in the remaining graph.
func kCoreByPeeling(nodes []int64, adj map[int64]map[int64]bool, k int) map[int64]bool {
	alive := make(map[int64]bool)
	deg := make(map[int64]int)
	for _, id := range nodes {
		alive[id] = true
		deg[id] = len(adj[id])
	}
	var queue []int64
	for _, id := range nodes {
		if deg[id] < k {
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		if !alive[x] {
			continue
		}
		alive[x] = false
		for w := range adj[x] {
			if alive[w] {
				deg[w]--
				if deg[w] < k {
					queue = append(queue, w)
				}
			}
		}
	}
	out := make(map[int64]bool)
	for id, ok := range alive {
		if ok {
			out[id] = true
		}
	}
	return out
}

// TestKCoreSubsetEnumeration verifies, on small graphs, that the set of
// nodes with core >= k equals the maximal union of induced subgraphs
// with minimum degree >= k (enumerating every vertex subset), and
// equals iterative peeling.
func TestKCoreSubsetEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 40; trial++ {
		n := 2 + rng.Intn(7) // 2..8 nodes, isolated nodes included
		ix := coreindex.New()
		var ops []coreindex.EdgeOp
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if rng.Float64() < 0.4 {
					ops = append(ops, coreindex.AddOp(int64(i), int64(j)))
				}
			}
		}
		ensure := make([]int64, n)
		for i := range ensure {
			ensure[i] = int64(i)
		}
		if _, err := ix.ApplyBatch(ops, ensure...); err != nil {
			t.Fatal(err)
		}

		nodes := ix.Nodes()
		adj := adjacencyOf(ix)
		cores := ix.Cores()
		maxCore := 0
		for _, c := range cores {
			if c > maxCore {
				maxCore = c
			}
		}
		for k := 0; k <= maxCore+1; k++ {
			want := kCoreByEnumeration(nodes, adj, k)
			peeled := kCoreByPeeling(nodes, adj, k)
			if !reflect.DeepEqual(want, peeled) {
				t.Fatalf("trial %d k=%d: enumeration %v != peeling %v", trial, k, want, peeled)
			}
			got := make(map[int64]bool)
			for id, c := range cores {
				if c >= k {
					got[id] = true
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d k=%d: index k-core %v != enumeration %v", trial, k, got, want)
			}
		}
	}
}

// TestIncrementalVsFullDecomposition applies random batches and, after
// each batch, compares the incrementally maintained cores and the
// reported changed set against a full-graph decomposition reference.
func TestIncrementalVsFullDecomposition(t *testing.T) {
	rng := rand.New(rand.NewSource(20240601))
	for trial := 0; trial < 60; trial++ {
		n := 2 + rng.Intn(9) // node IDs 0..n-1, may grow by 2
		ix := coreindex.New()
		ensure := make([]int64, n)
		for i := range ensure {
			ensure[i] = int64(i)
		}
		if _, err := ix.ApplyBatch(nil, ensure...); err != nil {
			t.Fatal(err)
		}
		maxID := int64(n + 1)
		for batch := 0; batch < 30; batch++ {
			before := ix.Cores()
			var ops []coreindex.EdgeOp
			m := 1 + rng.Intn(5)
			for j := 0; j < m; j++ {
				u := int64(rng.Intn(int(maxID)))
				v := int64(rng.Intn(int(maxID)))
				if u == v {
					continue
				}
				if rng.Float64() < 0.55 {
					ops = append(ops, coreindex.AddOp(u, v))
				} else {
					ops = append(ops, coreindex.DelOp(u, v))
				}
			}
			var extra []int64
			if rng.Float64() < 0.2 {
				extra = append(extra, maxID+int64(batch)) // fresh isolated node
			}
			changed, err := ix.ApplyBatch(ops, extra...)
			if err != nil {
				t.Fatalf("trial %d batch %d: %v", trial, batch, err)
			}

			// Reference: full decomposition of the current graph.
			ref := ix.Decompose().Core
			got := ix.Cores()
			if !reflect.DeepEqual(got, ref) {
				t.Fatalf("trial %d batch %d: cores %v != reference %v (ops %v)", trial, batch, got, ref, ops)
			}
			// The reported changed set must equal the exact diff.
			wantChanged := make(map[int64]int)
			for id, c := range ref {
				if before[id] != c {
					wantChanged[id] = c
				}
			}
			if !reflect.DeepEqual(changed, wantChanged) {
				t.Fatalf("trial %d batch %d: changed %v != diff %v (ops %v)", trial, batch, changed, wantChanged, ops)
			}
		}
	}
}

// TestChangedSetExactOnKnownSequence checks the changed set on a
// hand-written sequence covering rises, drops and no-ops.
func TestChangedSetExactOnKnownSequence(t *testing.T) {
	ix := coreindex.New()
	step := func(ops []coreindex.EdgeOp, want map[int64]int) {
		t.Helper()
		got, err := ix.ApplyBatch(ops)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ops %v: changed = %v, want %v", ops, got, want)
		}
	}
	step([]coreindex.EdgeOp{coreindex.AddOp(1, 2)}, map[int64]int{1: 1, 2: 1})
	step([]coreindex.EdgeOp{coreindex.AddOp(2, 3)}, map[int64]int{3: 1})
	step([]coreindex.EdgeOp{coreindex.AddOp(3, 1)}, map[int64]int{1: 2, 2: 2, 3: 2})
	step([]coreindex.EdgeOp{coreindex.AddOp(1, 2)}, map[int64]int{}) // duplicate
	step([]coreindex.EdgeOp{coreindex.DelOp(4, 5)}, map[int64]int{}) // missing edge
	step([]coreindex.EdgeOp{coreindex.DelOp(1, 3)}, map[int64]int{1: 1, 2: 1, 3: 1})
	step([]coreindex.EdgeOp{coreindex.DelOp(1, 2), coreindex.DelOp(2, 3)}, map[int64]int{1: 0, 2: 0, 3: 0})
}
