// Package reach maintains the positive-length reachability relation
// (transitive closure) of a directed multigraph under edge insertions
// and deletions.
//
// Semantics:
//   - Edges may be added repeatedly; each add bumps a per-edge counter.
//     A remove decrements the counter once and only detaches the edge
//     from the index when the counter drops to zero.
//   - Reachable(u, v) reports whether a path of length >= 1 exists.
//     A node reaches itself only through a self-loop or a cycle.
//
// Index invariant: cnt[x][y] equals the number of in-edges (z, y) such
// that x == z or x reaches z. Hence Reachable(x, y) <=> cnt[x][y] > 0.
//
// Insertions propagate support counts; this is exact because growth is
// monotone. Deletions cannot just decrement counts: with cycles present
// the count equations admit spurious fixed points, so a pair can keep
// "supporting" itself through a cycle that no longer connects to the
// source. Deletions therefore rebuild, via BFS, the rows of exactly
// those sources that could reach the deleted tail before the removal.
package reach

import "sort"

// Pair is an ordered pair of node ids.
type Pair struct {
	From, To int
}

// Change describes one pair whose reachability flipped.
type Change struct {
	From, To int
	Added    bool // true: became reachable; false: became unreachable
}

// Graph is a dynamic directed multigraph with a reachability index.
type Graph struct {
	edgeCount map[Pair]int        // multiplicity of each edge (>0 while present)
	out       map[int]map[int]int // out[u][v] = multiplicity of u->v
	in        map[int]map[int]int // in[v][u]  = multiplicity of u->v
	cnt       map[int]map[int]int // cnt[x][y] = supporting in-edge count
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		edgeCount: make(map[Pair]int),
		out:       make(map[int]map[int]int),
		in:        make(map[int]map[int]int),
		cnt:       make(map[int]map[int]int),
	}
}

// EdgeCount returns the current multiplicity of edge (u, v).
func (g *Graph) EdgeCount(u, v int) int {
	return g.edgeCount[Pair{u, v}]
}

// Reachable reports whether v is reachable from u by a path of
// length >= 1.
func (g *Graph) Reachable(u, v int) bool {
	return g.cnt[u][v] > 0
}

// ReachableFrom returns the sorted set of nodes reachable from u by a
// path of length >= 1.
func (g *Graph) ReachableFrom(u int) []int {
	var res []int
	for v, c := range g.cnt[u] {
		if c > 0 {
			res = append(res, v)
		}
	}
	sort.Ints(res)
	return res
}

// sourcesTo returns every x with reach0(x, u): x == u or x reaches u.
func (g *Graph) sourcesTo(u int) []int {
	src := []int{u}
	for x, row := range g.cnt {
		if x != u && row[u] > 0 {
			src = append(src, x)
		}
	}
	sort.Ints(src)
	return src
}

// AddEdge inserts one occurrence of edge (u, v) and returns the pairs
// whose reachability changed, sorted for deterministic output.
func (g *Graph) AddEdge(u, v int) []Change {
	p := Pair{u, v}
	g.edgeCount[p]++
	addTo(g.out, u, v)
	addTo(g.in, v, u)
	if g.edgeCount[p] > 1 {
		return nil // duplicate edge: multigraph reachability unchanged
	}

	// The new edge (u, v) is one more supporting in-edge of v for every
	// source x that already has reach0(x, u). Newly reachable nodes
	// cascade: when x newly reaches z (and x != z, since reach0(x, x)
	// was already true), every out-edge of z becomes a new support.
	type support struct{ x, y int }
	stack := make([]support, 0, 8)
	for _, x := range g.sourcesTo(u) {
		stack = append(stack, support{x, v})
	}
	var changed []Change
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		row := g.cnt[s.x]
		if row == nil {
			row = make(map[int]int)
			g.cnt[s.x] = row
		}
		row[s.y]++
		if row[s.y] == 1 {
			changed = append(changed, Change{From: s.x, To: s.y, Added: true})
			if s.x != s.y {
				for w := range g.out[s.y] {
					stack = append(stack, support{s.x, w})
				}
			}
		}
	}
	return sortChanges(changed)
}

// RemoveEdge deletes one occurrence of edge (u, v) and returns the
// pairs whose reachability changed, sorted for deterministic output.
// Removing an edge that is not present is a no-op.
func (g *Graph) RemoveEdge(u, v int) []Change {
	p := Pair{u, v}
	if g.edgeCount[p] == 0 {
		return nil
	}
	g.edgeCount[p]--
	if g.edgeCount[p] == 0 {
		delete(g.edgeCount, p)
	}
	removeFrom(g.out, u, v)
	removeFrom(g.in, v, u)
	if g.edgeCount[p] > 0 {
		return nil // remaining copies still realize the edge
	}

	// Only sources that had reach0(x, u) before the removal can lose
	// reachability; every other row of the index is untouched. For each
	// such source, rebuild the row from a BFS on the current multigraph.
	// Decrementing support counts instead would be wrong here: pairs
	// inside a cycle can keep each other's counts positive after the
	// bridge feeding the cycle is gone.
	sources := g.sourcesTo(u)
	var changed []Change
	for _, x := range sources {
		old := g.cnt[x]
		fresh := g.bfsRow(x)
		for y, c := range old {
			if c > 0 && fresh[y] == 0 {
				changed = append(changed, Change{From: x, To: y, Added: false})
			}
		}
		for y, c := range fresh {
			if c > 0 && old[y] == 0 {
				changed = append(changed, Change{From: x, To: y, Added: true})
			}
		}
		if len(fresh) == 0 {
			delete(g.cnt, x)
		} else {
			g.cnt[x] = fresh
		}
	}
	return sortChanges(changed)
}

// bfsRow recomputes the index row for source x from scratch: a
// positive-length BFS over current edges, then, for every node z with
// reach0(x, z), one support per out-edge of z.
func (g *Graph) bfsRow(x int) map[int]int {
	reached := make(map[int]bool)
	queue := make([]int, 0, 16)
	for w := range g.out[x] {
		if !reached[w] {
			reached[w] = true
			queue = append(queue, w)
		}
	}
	for i := 0; i < len(queue); i++ {
		for w := range g.out[queue[i]] {
			if !reached[w] {
				reached[w] = true
				queue = append(queue, w)
			}
		}
	}
	// reach0(x, x) holds by definition; marking x here also prevents
	// counting its out-edges twice when a cycle led back to x.
	reached[x] = true
	row := make(map[int]int)
	for z := range reached {
		for w := range g.out[z] {
			row[w]++ // one support per distinct edge, ignoring multiplicity
		}
	}
	return row
}

func addTo(adj map[int]map[int]int, u, v int) {
	m := adj[u]
	if m == nil {
		m = make(map[int]int)
		adj[u] = m
	}
	m[v]++
}

func removeFrom(adj map[int]map[int]int, u, v int) {
	m := adj[u]
	if m == nil {
		return
	}
	m[v]--
	if m[v] <= 0 {
		delete(m, v)
	}
	if len(m) == 0 {
		delete(adj, u)
	}
}

func sortChanges(changes []Change) []Change {
	sort.Slice(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Added && !b.Added
	})
	return changes
}
