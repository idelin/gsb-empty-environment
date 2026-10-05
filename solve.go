package dagpath

// Path is the answer to a query: the chosen edges, in order from s to t. It
// is a plain, self-contained slice — it shares nothing with the solver's
// internal rope structures, so it can be expanded or inspected independently.
type Path struct {
	Edges []Edge
}

// String returns the concatenation of the edge labels along the path.
func (p Path) String() string {
	n := 0
	for _, e := range p.Edges {
		n += len(e.Label)
	}
	buf := make([]byte, 0, n)
	for _, e := range p.Edges {
		buf = append(buf, e.Label...)
	}
	return string(buf)
}

// EdgeIDs returns the edge IDs along the path in order.
func (p Path) EdgeIDs() []int {
	ids := make([]int, len(p.Edges))
	for i, e := range p.Edges {
		ids[i] = e.ID
	}
	return ids
}

// idList is a persistent (shared-tail) list of edge IDs used only during the
// DP, so the ID-sequence tie-break never copies sequences.
type idList struct {
	id   int
	next *idList
}

// lessIDList reports whether edge-ID sequence a is lexicographically smaller
// than b. A strict prefix is smaller.
func lessIDList(a, b *idList) bool {
	for a != nil && b != nil {
		if a.id != b.id {
			return a.id < b.id
		}
		a, b = a.next, b.next
	}
	return a == nil && b != nil
}

// candidate is the best known way to reach t from one node.
type candidate struct {
	str   *rope   // concatenated labels from here to t (suffix-shared rope)
	edges int     // number of edges on the path
	ids   *idList // edge IDs from here to t
	edge  int     // index of the chosen first edge, -1 at t itself
}

// lessCandidate orders candidates by (string, edge count, edge-ID sequence).
func lessCandidate(a, b *candidate) bool {
	if c := compareRope(a.str, b.str); c != 0 {
		return c < 0
	}
	if a.edges != b.edges {
		return a.edges < b.edges
	}
	return lessIDList(a.ids, b.ids)
}

// BestPath returns the s-t path that minimizes, in order:
//
//  1. the concatenated label string (compared as a whole — never per-edge),
//  2. the number of edges,
//  3. the edge-ID sequence, lexicographically.
//
// found is false when t is unreachable from s. The graph must be acyclic
// (enforced by NewGraph).
//
// The DP runs in reverse topological order, so the optimal suffix string of
// every node is built exactly once and shared (as an immutable rope) by all
// of its predecessors — large suffixes are never re-concatenated, and
// candidates are compared by rolling-hash LCP instead of full scans.
//
// Optimal substructure holds because the objective is a lexicographic product
// and prepending the same label preserves string order: if path e+Q is
// optimal from u, then Q is optimal from e.To.
func (g *Graph) BestPath(s, t int) (path Path, found bool, err error) {
	if s < 0 || s >= g.NumNodes || t < 0 || t >= g.NumNodes {
		return Path{}, false, errNodeRange
	}
	order, err := g.topoOrder()
	if err != nil {
		return Path{}, false, err
	}

	best := make([]*candidate, g.NumNodes)
	best[t] = &candidate{str: emptyRope, edge: -1}

	for i := len(order) - 1; i >= 0; i-- {
		u := order[i]
		if u == t {
			continue
		}
		var cur *candidate
		for _, ei := range g.out[u] {
			e := &g.Edges[ei]
			sub := best[e.To]
			if sub == nil {
				continue // e.To cannot reach t
			}
			cand := &candidate{
				str:   concatRope(leafRope(e.Label), sub.str),
				edges: sub.edges + 1,
				ids:   &idList{id: e.ID, next: sub.ids},
				edge:  ei,
			}
			if cur == nil || lessCandidate(cand, cur) {
				cur = cand
			}
		}
		best[u] = cur
	}

	if best[s] == nil {
		return Path{}, false, nil
	}

	// Expand the chosen edges into a standalone slice.
	edges := make([]Edge, 0, best[s].edges)
	for u := s; u != t; {
		ei := best[u].edge
		edges = append(edges, g.Edges[ei])
		u = g.Edges[ei].To
	}
	return Path{Edges: edges}, true, nil
}

var errNodeRange = errorString("dagpath: node out of range")

type errorString string

func (e errorString) Error() string { return string(e) }
