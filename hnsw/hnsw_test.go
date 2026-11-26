package hnsw

import (
	"math"
	"sort"
	"testing"
)

type vec2 struct{ x, y float64 }

func euclid(a, b vec2) float64 {
	dx := a.x - b.x
	dy := a.y - b.y
	return math.Sqrt(dx*dx + dy*dy)
}

func newTestGraph(t *testing.T, m int, keepPruned bool) *Graph[vec2] {
	t.Helper()
	g, err := NewGraph[vec2](Config[vec2]{
		M:                     m,
		Distance:              euclid,
		KeepPrunedConnections: keepPruned,
	})
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

func addNode(t *testing.T, g *Graph[vec2], id int, v vec2, level int) {
	t.Helper()
	if err := g.AddNode(id, v, level); err != nil {
		t.Fatalf("AddNode(%d): %v", id, err)
	}
}

func addEdge(t *testing.T, g *Graph[vec2], layer, a, b int) {
	t.Helper()
	if err := g.AddEdge(layer, a, b); err != nil {
		t.Fatalf("AddEdge(%d,%d,%d): %v", layer, a, b, err)
	}
}

func assertInvariants(t *testing.T, g *Graph[vec2], stage string) {
	t.Helper()
	if err := g.CheckInvariants(); err != nil {
		t.Fatalf("invariants after %s: %v", stage, err)
	}
}

func bruteForce(g *Graph[vec2], q vec2, k int) []Result {
	type scored struct {
		id int
		d  float64
	}
	var all []scored
	for id, ns := range g.nodes {
		if ns.deleted {
			continue
		}
		all = append(all, scored{id: id, d: euclid(q, ns.vec)})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].d != all[j].d {
			return all[i].d < all[j].d
		}
		return all[i].id < all[j].id
	})
	if k < len(all) {
		all = all[:k]
	}
	out := make([]Result, len(all))
	for i, item := range all {
		out[i] = Result{ID: item.id, Distance: item.d}
	}
	return out
}

func resultIDs(results []Result) []int {
	out := make([]int, len(results))
	for i, r := range results {
		out[i] = r.ID
	}
	return out
}

func nearestIDs(coords []vec2, id, k int) []int {
	type scored struct {
		id int
		d  float64
	}
	var all []scored
	for other, v := range coords {
		if other == id {
			continue
		}
		all = append(all, scored{id: other, d: euclid(coords[id], v)})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].d != all[j].d {
			return all[i].d < all[j].d
		}
		return all[i].id < all[j].id
	})
	if k > len(all) {
		k = len(all)
	}
	out := make([]int, k)
	for i := 0; i < k; i++ {
		out[i] = all[i].id
	}
	return out
}
