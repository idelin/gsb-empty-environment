// Package dynclique provides maximal clique enumeration for undirected
// graphs. It implements the Bron-Kerbosch algorithm with pivot pruning
// (Tomita variant) and maintains the set of maximal cliques incrementally
// as edges are added or removed.
package dynclique

import "sort"

// MaximalCliques enumerates all maximal cliques of the undirected graph
// described by the adjacency map adj, using the Bron-Kerbosch algorithm
// with pivot pruning. Each returned clique is sorted in ascending order.
// The adjacency map is not modified.
func MaximalCliques(adj map[int]map[int]struct{}) [][]int {
	p := make(map[int]struct{}, len(adj))
	for v := range adj {
		p[v] = struct{}{}
	}
	var out [][]int
	bkPivot(adj, nil, p, make(map[int]struct{}), &out)
	sortCliques(out)
	return out
}

// bkPivot is the recursive Bron-Kerbosch core with Tomita pivot selection:
//   - r: vertices of the clique being built (all pairwise adjacent);
//   - p: candidate vertices that can still extend r;
//   - x: vertices already processed, used to avoid reporting duplicates.
//
// A maximal clique is reported exactly when both p and x are empty.
func bkPivot(adj map[int]map[int]struct{}, r []int, p, x map[int]struct{}, out *[][]int) {
	if len(p) == 0 && len(x) == 0 {
		clique := make([]int, len(r))
		copy(clique, r)
		sort.Ints(clique)
		*out = append(*out, clique)
		return
	}
	pivot := choosePivot(adj, p, x)
	// Only vertices of P not adjacent to the pivot need to be explored;
	// any maximal clique through a neighbor of the pivot is found when
	// the pivot itself (or another neighbor) is picked.
	candidates := make([]int, 0, len(p))
	for v := range p {
		if _, adjacent := adj[pivot][v]; !adjacent {
			candidates = append(candidates, v)
		}
	}
	sort.Ints(candidates) // deterministic traversal order
	for _, v := range candidates {
		if _, stillCandidate := p[v]; !stillCandidate {
			continue
		}
		nextR := make([]int, len(r)+1)
		copy(nextR, r)
		nextR[len(r)] = v
		bkPivot(adj, nextR, intersect(p, adj[v]), intersect(x, adj[v]), out)
		delete(p, v)
		x[v] = struct{}{}
	}
}

// choosePivot picks the vertex from P∪X that maximizes |P ∩ N(u)|, which
// minimizes the number of recursive calls (Tomita et al. 2006).
func choosePivot(adj map[int]map[int]struct{}, p, x map[int]struct{}) int {
	best, bestCount := -1, -1
	for u := range p {
		if c := countIntersect(p, adj[u]); c > bestCount {
			best, bestCount = u, c
		}
	}
	for u := range x {
		if c := countIntersect(p, adj[u]); c > bestCount {
			best, bestCount = u, c
		}
	}
	return best
}

func intersect(a, b map[int]struct{}) map[int]struct{} {
	if len(a) > len(b) {
		a, b = b, a
	}
	out := make(map[int]struct{}, len(a))
	for v := range a {
		if _, ok := b[v]; ok {
			out[v] = struct{}{}
		}
	}
	return out
}

func countIntersect(a, b map[int]struct{}) int {
	if len(a) > len(b) {
		a, b = b, a
	}
	n := 0
	for v := range a {
		if _, ok := b[v]; ok {
			n++
		}
	}
	return n
}

func sortCliques(cliques [][]int) {
	for _, c := range cliques {
		sort.Ints(c)
	}
	sort.Slice(cliques, func(i, j int) bool {
		a, b := cliques[i], cliques[j]
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
}
