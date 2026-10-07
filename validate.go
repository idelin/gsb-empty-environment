// Package treedecomp validates tree decompositions of undirected graphs.
//
// A candidate decomposition is checked, in order, for:
//  1. structural sanity (bag vertices and tree edges in range),
//  2. the bag graph being a tree (connected, |E| = |V|-1),
//  3. every graph vertex appearing in at least one bag,
//  4. every graph edge being covered by some bag,
//  5. the running intersection property: for every vertex, the bags
//     containing it form a connected subtree. This is verified by the
//     tree-edge deletion partition test: removing any tree edge must not
//     separate two occurrences of the same vertex. Checking only pairwise
//     bag intersections is NOT sufficient and is not what this package does.
//
// Validate returns the first concrete violation found together with the
// width (max bag size minus one) of the candidate.
package treedecomp

// Edge is an undirected edge of the input graph.
type Edge struct {
	U, V int
}

// Graph is a simple undirected graph on vertices 0..N-1.
type Graph struct {
	N     int
	Edges []Edge
}

// TreeEdge joins two bags of a candidate decomposition.
type TreeEdge struct {
	A, B int
}

// Decomposition is a candidate tree decomposition: Bags are indexed
// 0..len(Bags)-1 and joined by Edges, which must form a tree.
type Decomposition struct {
	Bags  [][]int
	Edges []TreeEdge
}

// ViolationKind identifies which tree decomposition axiom fails.
type ViolationKind int

const (
	ViolationNone ViolationKind = iota
	// ViolationVertexOutOfRange: a bag holds a vertex outside 0..N-1.
	ViolationVertexOutOfRange
	// ViolationBagEdgeOutOfRange: a tree edge references a missing bag,
	// is a self-loop, or is duplicated.
	ViolationBagEdgeOutOfRange
	// ViolationBagGraphNotTree: the bag graph is empty, disconnected,
	// or has a cycle.
	ViolationBagGraphNotTree
	// ViolationVertexMissing: a graph vertex appears in no bag.
	ViolationVertexMissing
	// ViolationEdgeNotCovered: no single bag contains both endpoints
	// of a graph edge.
	ViolationEdgeNotCovered
	// ViolationRunningIntersection: the bags containing a vertex do not
	// form a connected subtree; witnessed by a tree edge whose removal
	// puts occurrences of the vertex on both sides.
	ViolationRunningIntersection
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationNone:
		return "none"
	case ViolationVertexOutOfRange:
		return "vertex out of range"
	case ViolationBagEdgeOutOfRange:
		return "invalid tree edge"
	case ViolationBagGraphNotTree:
		return "bag graph is not a tree"
	case ViolationVertexMissing:
		return "vertex missing from all bags"
	case ViolationEdgeNotCovered:
		return "edge not covered by any bag"
	case ViolationRunningIntersection:
		return "running intersection violated"
	}
	return "unknown"
}

// Violation is a concrete witness of the first failed axiom.
// Which fields are set depends on Kind:
//
//	ViolationVertexOutOfRange:   Bag, Vertex
//	ViolationBagEdgeOutOfRange:  TreeEdge
//	ViolationBagGraphNotTree:    Detail (and Bag for a witness node)
//	ViolationVertexMissing:      Vertex
//	ViolationEdgeNotCovered:     Edge
//	ViolationRunningIntersection: Vertex, TreeEdge, LeftBag, RightBag
type Violation struct {
	Kind ViolationKind

	Vertex   int      // offending graph vertex, or -1
	Edge     Edge     // uncovered graph edge (ViolationEdgeNotCovered)
	Bag      int      // bag index involved, or -1
	TreeEdge TreeEdge // tree edge involved, or {-1,-1}
	LeftBag  int      // bag on one side of TreeEdge containing Vertex
	RightBag int      // bag on the other side containing Vertex

	Detail string
}

func (v *Violation) String() string {
	switch v.Kind {
	case ViolationVertexOutOfRange:
		return "bag " + itoa(v.Bag) + " contains vertex " + itoa(v.Vertex) + " outside 0..N-1"
	case ViolationBagEdgeOutOfRange:
		return "invalid tree edge (" + itoa(v.TreeEdge.A) + "," + itoa(v.TreeEdge.B) + "): " + v.Detail
	case ViolationBagGraphNotTree:
		return "bag graph is not a tree: " + v.Detail
	case ViolationVertexMissing:
		return "vertex " + itoa(v.Vertex) + " appears in no bag"
	case ViolationEdgeNotCovered:
		return "edge (" + itoa(v.Edge.U) + "," + itoa(v.Edge.V) + ") is not contained in any bag"
	case ViolationRunningIntersection:
		return "vertex " + itoa(v.Vertex) + " appears on both sides of tree edge (" +
			itoa(v.TreeEdge.A) + "," + itoa(v.TreeEdge.B) + "): bag " + itoa(v.LeftBag) +
			" and bag " + itoa(v.RightBag) + " are disconnected occurrences"
	}
	return v.Detail
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Result is the outcome of Validate.
type Result struct {
	Valid bool
	// Width is max bag size minus one. A single empty bag (valid for the
	// empty graph) gives width -1, as does a decomposition with no bags.
	Width     int
	Violation *Violation // nil iff Valid
}

// Validate checks that d is a tree decomposition of g and reports the
// first concrete violation found, plus the width of d.
func Validate(g Graph, d Decomposition) Result {
	res := Result{Valid: true, Width: -1}
	for _, bag := range d.Bags {
		if w := len(bag) - 1; w > res.Width {
			res.Width = w
		}
	}

	fail := func(v *Violation) Result {
		res.Valid = false
		res.Violation = v
		return res
	}

	nb := len(d.Bags)
	if nb == 0 {
		return fail(&Violation{
			Kind:   ViolationBagGraphNotTree,
			Vertex: -1, Bag: -1, TreeEdge: TreeEdge{-1, -1}, LeftBag: -1, RightBag: -1,
			Detail: "decomposition has no bags",
		})
	}

	// 1. Structural sanity.
	for bi, bag := range d.Bags {
		for _, v := range bag {
			if v < 0 || v >= g.N {
				return fail(&Violation{
					Kind: ViolationVertexOutOfRange, Vertex: v, Bag: bi,
					Edge: Edge{-1, -1}, TreeEdge: TreeEdge{-1, -1}, LeftBag: -1, RightBag: -1,
				})
			}
		}
	}
	seenEdge := make(map[TreeEdge]bool, len(d.Edges))
	for _, te := range d.Edges {
		norm := te
		if norm.A > norm.B {
			norm.A, norm.B = norm.B, norm.A
		}
		var detail string
		switch {
		case te.A < 0 || te.A >= nb || te.B < 0 || te.B >= nb:
			detail = "bag index out of range"
		case te.A == te.B:
			detail = "self-loop"
		case seenEdge[norm]:
			detail = "duplicate tree edge"
		}
		if detail != "" {
			return fail(&Violation{
				Kind: ViolationBagEdgeOutOfRange, TreeEdge: te, Detail: detail,
				Vertex: -1, Edge: Edge{-1, -1}, Bag: -1, LeftBag: -1, RightBag: -1,
			})
		}
		seenEdge[norm] = true
	}

	// 2. The bag graph must be a tree: |E| = |V|-1 and connected.
	if len(d.Edges) != nb-1 {
		return fail(&Violation{
			Kind: ViolationBagGraphNotTree,
			Detail: "a tree on " + itoa(nb) + " bags needs " + itoa(nb-1) +
				" edges, got " + itoa(len(d.Edges)),
			Vertex: -1, Edge: Edge{-1, -1}, Bag: -1, TreeEdge: TreeEdge{-1, -1},
			LeftBag: -1, RightBag: -1,
		})
	}
	adj := make([][]int, nb) // adjacency as bag indices
	for _, te := range d.Edges {
		adj[te.A] = append(adj[te.A], te.B)
		adj[te.B] = append(adj[te.B], te.A)
	}
	comp := componentFrom(adj, 0, -1, -1)
	for b := 0; b < nb; b++ {
		if !comp[b] {
			return fail(&Violation{
				Kind:   ViolationBagGraphNotTree,
				Detail: "bag " + itoa(b) + " is disconnected from bag 0",
				Bag:    b,
				Vertex: -1, Edge: Edge{-1, -1}, TreeEdge: TreeEdge{-1, -1},
				LeftBag: -1, RightBag: -1,
			})
		}
	}

	// Vertex -> bags containing it.
	bagsOf := make([][]int, g.N)
	for bi, bag := range d.Bags {
		for _, v := range bag {
			bagsOf[v] = append(bagsOf[v], bi)
		}
	}

	// 3. Every vertex appears in some bag.
	for v := 0; v < g.N; v++ {
		if len(bagsOf[v]) == 0 {
			return fail(&Violation{
				Kind: ViolationVertexMissing, Vertex: v,
				Edge: Edge{-1, -1}, Bag: -1, TreeEdge: TreeEdge{-1, -1},
				LeftBag: -1, RightBag: -1,
			})
		}
	}

	// 4. Every edge is covered by some bag.
	inBag := make([]map[int]bool, nb)
	for bi, bag := range d.Bags {
		s := make(map[int]bool, len(bag))
		for _, v := range bag {
			s[v] = true
		}
		inBag[bi] = s
	}
edgeLoop:
	for _, e := range g.Edges {
		if e.U < 0 || e.U >= g.N || e.V < 0 || e.V >= g.N {
			return fail(&Violation{
				Kind: ViolationEdgeNotCovered, Edge: e,
				Detail: "edge endpoint outside 0..N-1",
				Vertex: -1, Bag: -1, TreeEdge: TreeEdge{-1, -1},
				LeftBag: -1, RightBag: -1,
			})
		}
		for _, bi := range bagsOf[e.U] {
			if inBag[bi][e.V] {
				continue edgeLoop
			}
		}
		return fail(&Violation{
			Kind: ViolationEdgeNotCovered, Edge: e,
			Vertex: -1, Bag: -1, TreeEdge: TreeEdge{-1, -1},
			LeftBag: -1, RightBag: -1,
		})
	}

	// 5. Running intersection via the tree-edge deletion partition test:
	// removing tree edge (a,b) splits the bags into two sides; any vertex
	// appearing on both sides must lie in bag(a) intersect bag(b), since
	// its bags must form a connected subtree spanning the cut. This is
	// stronger than checking pairwise bag intersections.
	for _, te := range d.Edges {
		side := componentFrom(adj, te.A, te.A, te.B)
		inSide := make([]bool, g.N)
		inRest := make([]bool, g.N)
		for bi, bag := range d.Bags {
			for _, v := range bag {
				if side[bi] {
					inSide[v] = true
				} else {
					inRest[v] = true
				}
			}
		}
		for v := 0; v < g.N; v++ {
			if inSide[v] && inRest[v] && !(inBag[te.A][v] && inBag[te.B][v]) {
				left, right := -1, -1
				for _, bi := range bagsOf[v] {
					if side[bi] && left < 0 {
						left = bi
					}
					if !side[bi] && right < 0 {
						right = bi
					}
				}
				return fail(&Violation{
					Kind: ViolationRunningIntersection, Vertex: v,
					TreeEdge: te, LeftBag: left, RightBag: right,
					Edge: Edge{-1, -1}, Bag: -1,
				})
			}
		}
	}

	return res
}

// componentFrom returns the set of bags reachable from start in the bag
// graph with the tree edge (cutA, cutB) removed. cutA < 0 means no cut.
func componentFrom(adj [][]int, start, cutA, cutB int) []bool {
	seen := make([]bool, len(adj))
	stack := []int{start}
	seen[start] = true
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, nb := range adj[b] {
			if (b == cutA && nb == cutB) || (b == cutB && nb == cutA) {
				continue
			}
			if !seen[nb] {
				seen[nb] = true
				stack = append(stack, nb)
			}
		}
	}
	return seen
}
