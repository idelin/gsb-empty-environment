// Package twcheck verifies that a candidate tree decomposition of an
// undirected graph is valid, and computes its width.
//
// A valid tree decomposition (bags arranged as a tree) must satisfy:
//   1. every graph edge is contained in at least one bag;
//   2. every graph vertex appears in at least one bag;
//   3. for every vertex, the bags containing it form a connected
//      subtree (the running intersection property);
//   4. the bag graph itself is a tree.
//
// The running intersection property is verified by the cut-partition
// method: for every tree edge (a,b), removing it splits the bags into
// two sides; any vertex appearing on both sides must lie in
// bag[a] ∩ bag[b]. This is strictly stronger than checking pairwise
// bag intersections.
package twcheck

import "fmt"

// Graph is a simple undirected graph on vertices 0..N-1.
type Graph struct {
	N     int
	Edges [][2]int
}

// Decomposition is a candidate tree decomposition. Adj must be the
// adjacency lists of the bag graph (symmetric, same length as Bags).
type Decomposition struct {
	Bags [][]int
	Adj  [][]int
}

// ViolationKind classifies the first violation found.
type ViolationKind int

const (
	ViolationNone ViolationKind = iota
	// ViolationBadIndex: an edge or bag references a vertex outside
	// [0,N), an adjacency entry is out of range/asymmetric/a self-loop,
	// or the graph edge list is malformed.
	ViolationBadIndex
	// ViolationNotTree: the bag graph is empty, disconnected, or cyclic.
	ViolationNotTree
	// ViolationEdgeNotCovered: no bag contains both endpoints of Edge.
	ViolationEdgeNotCovered
	// ViolationVertexMissing: Vertex appears in no bag at all.
	ViolationVertexMissing
	// ViolationNotConnected: the bags containing Vertex are disconnected;
	// witnessed by the tree edge Bags whose cut separates two
	// occurrences of Vertex not both covered by the endpoint bags.
	ViolationNotConnected
)

// Violation describes the first concrete violation found.
type Violation struct {
	Kind   ViolationKind
	Edge   [2]int // set for ViolationEdgeNotCovered / ViolationBadIndex
	Vertex int    // set for ViolationVertexMissing / ViolationNotConnected
	Bags   [2]int // tree edge witnessing ViolationNotConnected
	Msg    string
}

func (v *Violation) Error() string { return v.Msg }

// Verify checks that td is a valid tree decomposition of g. It returns
// the width (max bag size minus one) and, if invalid, the first
// concrete violation found. An empty graph is validly decomposed by a
// single node with an empty bag (width -1).
func Verify(g Graph, td Decomposition) (width int, viol *Violation) {
	if g.N < 0 {
		return 0, &Violation{Kind: ViolationBadIndex, Msg: "negative vertex count"}
	}

	// Normalize and validate graph edges.
	edges := make([][2]int, 0, len(g.Edges))
	seenEdge := make(map[[2]int]bool)
	for _, e := range g.Edges {
		u, v := e[0], e[1]
		if u < 0 || u >= g.N || v < 0 || v >= g.N || u == v {
			return 0, &Violation{Kind: ViolationBadIndex, Edge: e,
				Msg: fmt.Sprintf("invalid graph edge (%d,%d) for %d vertices", u, v, g.N)}
		}
		if u > v {
			u, v = v, u
		}
		ke := [2]int{u, v}
		if !seenEdge[ke] {
			seenEdge[ke] = true
			edges = append(edges, ke)
		}
	}

	nb := len(td.Bags)
	if nb == 0 {
		return 0, &Violation{Kind: ViolationNotTree, Msg: "decomposition has no bags"}
	}

	// Validate bag contents, dedupe within each bag, compute width.
	bagSets := make([]map[int]bool, nb)
	width = -1
	for i, bag := range td.Bags {
		s := make(map[int]bool, len(bag))
		for _, v := range bag {
			if v < 0 || v >= g.N {
				return 0, &Violation{Kind: ViolationBadIndex, Vertex: v,
					Msg: fmt.Sprintf("bag %d contains vertex %d outside [0,%d)", i, v, g.N)}
			}
			s[v] = true
		}
		bagSets[i] = s
		if w := len(s) - 1; w > width {
			width = w
		}
	}

	// Validate bag adjacency: length, range, self-loops, symmetry.
	if len(td.Adj) != nb {
		return 0, &Violation{Kind: ViolationNotTree,
			Msg: fmt.Sprintf("adjacency has %d lists for %d bags", len(td.Adj), nb)}
	}
	adj := make([]map[int]bool, nb)
	edgeCount := 0
	for i := 0; i < nb; i++ {
		adj[i] = make(map[int]bool)
		for _, j := range td.Adj[i] {
			if j < 0 || j >= nb {
				return 0, &Violation{Kind: ViolationBadIndex,
					Msg: fmt.Sprintf("adjacency of bag %d references bag %d out of range", i, j)}
			}
			if j == i {
				return 0, &Violation{Kind: ViolationNotTree,
					Msg: fmt.Sprintf("bag %d has a self-loop", i)}
			}
			if !adj[i][j] {
				adj[i][j] = true
				edgeCount++
			}
		}
	}
	for i := 0; i < nb; i++ {
		for j := range adj[i] {
			if !adj[j][i] {
				return 0, &Violation{Kind: ViolationNotTree,
					Msg: fmt.Sprintf("adjacency not symmetric: %d lists %d but not vice versa", i, j)}
			}
		}
	}
	edgeCount /= 2

	// Tree check: connected and exactly nb-1 edges.
	visited := make([]bool, nb)
	queue := []int{0}
	visited[0] = true
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for y := range adj[x] {
			if !visited[y] {
				visited[y] = true
				queue = append(queue, y)
			}
		}
	}
	for i := 0; i < nb; i++ {
		if !visited[i] {
			return 0, &Violation{Kind: ViolationNotTree,
				Msg: fmt.Sprintf("bag graph disconnected: bag %d unreachable from bag 0", i)}
		}
	}
	if edgeCount != nb-1 {
		return 0, &Violation{Kind: ViolationNotTree,
			Msg: fmt.Sprintf("bag graph has %d edges, a tree on %d nodes needs %d", edgeCount, nb, nb-1)}
	}

	// Vertex -> bags containing it.
	vertexBags := make([][]int, g.N)
	for i := 0; i < nb; i++ {
		for v := range bagSets[i] {
			vertexBags[v] = append(vertexBags[v], i)
		}
	}

	// Edge coverage.
	for _, e := range edges {
		u, v := e[0], e[1]
		covered := false
		for _, b := range vertexBags[u] {
			if bagSets[b][v] {
				covered = true
				break
			}
		}
		if !covered {
			return width, &Violation{Kind: ViolationEdgeNotCovered, Edge: e,
				Msg: fmt.Sprintf("edge (%d,%d) is contained in no bag", u, v)}
		}
	}

	// Vertex presence.
	for v := 0; v < g.N; v++ {
		if len(vertexBags[v]) == 0 {
			return width, &Violation{Kind: ViolationVertexMissing, Vertex: v,
				Msg: fmt.Sprintf("vertex %d appears in no bag", v)}
		}
	}

	// Running intersection via cut partition: for each tree edge
	// (a,b) with a<b, removing it splits bags into sideA (reachable
	// from a) and sideB. Any vertex in the union of both sides must
	// be in bag[a] ∩ bag[b].
	type treeEdge struct{ a, b int }
	tedges := make([]treeEdge, 0, nb-1)
	for i := 0; i < nb; i++ {
		for j := range adj[i] {
			if i < j {
				tedges = append(tedges, treeEdge{i, j})
			}
		}
	}
	// Deterministic order: sort by (a,b).
	for i := 1; i < len(tedges); i++ {
		for k := i; k > 0 && (tedges[k].a < tedges[k-1].a ||
			(tedges[k].a == tedges[k-1].a && tedges[k].b < tedges[k-1].b)); k-- {
			tedges[k], tedges[k-1] = tedges[k-1], tedges[k]
		}
	}

	for _, te := range tedges {
		// BFS from te.a without crossing to te.b.
		side := make([]bool, nb)
		queue := []int{te.a}
		side[te.a] = true
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			for y := range adj[x] {
				if y == te.b && x == te.a {
					continue
				}
				if !side[y] {
					side[y] = true
					queue = append(queue, y)
				}
			}
		}
		unionA := make(map[int]bool)
		unionB := make(map[int]bool)
		for i := 0; i < nb; i++ {
			for v := range bagSets[i] {
				if side[i] {
					unionA[v] = true
				} else {
					unionB[v] = true
				}
			}
		}
		for v := range unionA {
			if unionB[v] && !(bagSets[te.a][v] && bagSets[te.b][v]) {
				return width, &Violation{
					Kind: ViolationNotConnected, Vertex: v, Bags: [2]int{te.a, te.b},
					Msg: fmt.Sprintf("vertex %d appears on both sides of tree edge (%d,%d) but not in both endpoint bags", v, te.a, te.b)}
			}
		}
	}

	return width, nil
}
