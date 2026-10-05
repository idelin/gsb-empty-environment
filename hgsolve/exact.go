package hgsolve

import (
	"container/heap"
	"sort"
	"strconv"
	"strings"
)

// factSet is a sorted, deduplicated set of node indices.
type factSet struct{ vals []int }

func (g *Graph) factSetOf(facts []string) factSet {
	var v []int
	for _, f := range facts {
		if i, ok := g.index[f]; ok {
			v = append(v, i)
		}
	}
	sort.Ints(v)
	out := v[:0]
	for i, x := range v {
		if i == 0 || x != v[i-1] {
			out = append(out, x)
		}
	}
	return factSet{vals: out}
}

func (s factSet) has(x int) bool {
	i := sort.SearchInts(s.vals, x)
	return i < len(s.vals) && s.vals[i] == x
}

func (s factSet) fireable(prem []int) bool {
	for _, p := range prem {
		if !s.has(p) {
			return false
		}
	}
	return true
}

func (s factSet) with(x int) factSet {
	v := make([]int, 0, len(s.vals)+1)
	i := sort.SearchInts(s.vals, x)
	pos := i
	if pos < len(s.vals) && s.vals[pos] == x {
		return s
	}
	v = append(v, s.vals[:pos]...)
	v = append(v, x)
	v = append(v, s.vals[pos:]...)
	return factSet{vals: v}
}

func (s factSet) key() string {
	var b strings.Builder
	for i, x := range s.vals {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(x))
	}
	return b.String()
}

type stateItem struct {
	set factSet
	key string
	g   float64
}

type stateHeap []*stateItem

func (h stateHeap) Len() int           { return len(h) }
func (h stateHeap) Less(i, j int) bool { return h[i].g < h[j].g }
func (h stateHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *stateHeap) Push(x any)        { *h = append(*h, x.(*stateItem)) }
func (h *stateHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// ExactResult is the outcome of SolveExact.
type ExactResult struct {
	Cost     float64  // minimum DAG cost: distinct proof edges charged once
	EdgeIDs  []string // sorted IDs of the edges forming the proof DAG
	Expanded int      // number of fact-set states expanded (search effort)
}

// SolveExact finds the minimum-cost proof DAG for target by Dijkstra search
// over monotone fact-set states. A state is a set of derived nodes; firing an
// edge whose premises are all present moves to state ∪ {target} at additional
// cost edge.Cost. Because states only grow, an edge fires at most once along
// any path, so path cost equals the number of DISTINCT edges used — exactly
// the shared-proof DAG cost. Cyclic self-proofs are impossible because every
// path is grounded in the initial facts.
//
// The search is bounded: a greedy proof supplies an upper bound that prunes
// every state whose cost cannot strictly improve on it. If no cheaper state
// reaches the target, the greedy proof itself is optimal and is returned.
func (g *Graph) SolveExact(facts []string, target string) (ExactResult, bool) {
	ti, tok := g.index[target]
	start := g.factSetOf(facts)
	startKey := start.key()
	if tok && start.has(ti) {
		return ExactResult{Cost: 0}, true
	}
	ub := 0.0
	var ubIDs []string
	c, ids, ok := g.GreedyProof(facts, target)
	if !ok {
		return ExactResult{}, false
	}
	ub, ubIDs = c, ids

	type parent struct {
		prev string
		edge int
	}
	dist := map[string]float64{startKey: 0}
	parents := map[string]parent{}
	pq := &stateHeap{}
	heap.Push(pq, &stateItem{set: start, key: startKey, g: 0})
	expanded := 0

	for pq.Len() > 0 {
		it := heap.Pop(pq).(*stateItem)
		if it.g > dist[it.key] {
			continue // stale
		}
		if it.g >= ub {
			continue // cannot strictly beat the incumbent
		}
		expanded++
		if tok && it.set.has(ti) {
			// First goal pop below ub is optimal: every cheaper or equal
			// state was already popped, and pruned states can only cost more.
			var edgeIDs []string
			seen := map[int]bool{}
			for k := it.key; k != startKey; {
				p := parents[k]
				if !seen[p.edge] {
					seen[p.edge] = true
					edgeIDs = append(edgeIDs, g.Edges[p.edge].ID)
				}
				k = p.prev
			}
			sort.Strings(edgeIDs)
			return ExactResult{Cost: it.g, EdgeIDs: edgeIDs, Expanded: expanded}, true
		}
		for ei := range g.Edges {
			t := g.tgt[ei]
			if it.set.has(t) || !it.set.fireable(g.prem[ei]) {
				continue
			}
			ng := it.g + g.Edges[ei].Cost
			if ng >= ub {
				continue
			}
			ns := it.set.with(t)
			k := ns.key()
			if d, ok := dist[k]; !ok || ng < d {
				dist[k] = ng
				parents[k] = parent{prev: it.key, edge: ei}
				heap.Push(pq, &stateItem{set: ns, key: k, g: ng})
			}
		}
	}
	// No state strictly cheaper than the greedy bound reaches the target:
	// the greedy proof is already optimal.
	sort.Strings(ubIDs)
	return ExactResult{Cost: ub, EdgeIDs: ubIDs, Expanded: expanded}, true
}
