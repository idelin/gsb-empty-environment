package dagindex

import "fmt"

// Index maintains the full transitive-closure reachability matrix of a DAG
// as per-node compressed bitmaps, plus a topological level per node.
//
//   - down[u] holds every node reachable from u (u included).
//   - level[u] = longest-path depth from a source; edges always go from a
//     lower level to a higher level.
//
// Queries are O(1) bit tests. Edge insertion propagates the descendant set
// of the edge head up the predecessor chain using word-level OR, so 64
// reachability facts are merged per machine operation.
type Index struct {
	n     int
	level []int
	succ  [][]int
	pred  [][]int
	down  []*Bitmap
}

// New returns an empty index.
func New() *Index {
	return &Index{}
}

// AddNode appends a node at topological level 0 and returns its id.
func (ix *Index) AddNode() int {
	id := ix.n
	ix.n++
	ix.level = append(ix.level, 0)
	ix.succ = append(ix.succ, nil)
	ix.pred = append(ix.pred, nil)
	b := NewBitmap()
	b.Set(uint32(id)) // a node always reaches itself
	ix.down = append(ix.down, b)
	return id
}

// Size returns the number of nodes.
func (ix *Index) Size() int { return ix.n }

// Level returns the topological level of node u.
func (ix *Index) Level(u int) int { return ix.level[u] }

// Reachable reports whether v is reachable from u. O(1).
func (ix *Index) Reachable(u, v int) bool {
	ix.check(u)
	ix.check(v)
	return ix.down[u].Test(uint32(v))
}

// AddEdge inserts the directed edge u->v and incrementally refreshes the
// transitive closure. It returns an error and leaves the index untouched if
// the edge would create a cycle.
func (ix *Index) AddEdge(u, v int) error {
	ix.check(u)
	ix.check(v)
	if u == v {
		return fmt.Errorf("dagindex: self edge %d->%d rejected", u, v)
	}
	if ix.down[v].Test(uint32(u)) {
		return fmt.Errorf("dagindex: edge %d->%d would create a cycle", u, v)
	}
	if ix.down[u].Test(uint32(v)) {
		return nil // redundant edge, closure already covers it
	}

	ix.succ[u] = append(ix.succ[u], v)
	ix.pred[v] = append(ix.pred[v], u)

	ix.fixLevels(u, v)
	ix.propagate(u, v)
	return nil
}

// fixLevels raises level[v] (and downstream nodes) so that every edge goes
// from a strictly lower level to a strictly higher one.
func (ix *Index) fixLevels(u, v int) {
	if ix.level[v] > ix.level[u] {
		return
	}
	type item struct{ node, lvl int }
	queue := []item{{v, ix.level[u] + 1}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.lvl <= ix.level[it.node] {
			continue
		}
		ix.level[it.node] = it.lvl
		for _, w := range ix.succ[it.node] {
			queue = append(queue, item{w, it.lvl + 1})
		}
	}
}

// propagate merges down[v] into down[a] for every ancestor a of u.
// The merged set down[v] is constant during the whole update, so each step
// is a word-parallel OR over compressed bitmap words; ancestors whose set
// does not change prune the walk.
func (ix *Index) propagate(u, v int) {
	delta := ix.down[v]
	stack := []int{u}
	seen := make(map[int]bool)
	for len(stack) > 0 {
		a := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[a] {
			continue
		}
		seen[a] = true
		if ix.down[a].Or(delta) {
			stack = append(stack, ix.pred[a]...)
		}
	}
}

// Stats reports memory usage of the compressed index versus a dense matrix.
type Stats struct {
	Nodes          int // number of nodes
	Edges          int // number of edges
	StoredWords    int // non-zero 64-bit words actually stored
	DenseWords     int // words a dense N x N bit matrix would need
	ReachablePairs int // total set bits across all bitmaps
}

// Stats computes current memory statistics.
func (ix *Index) Stats() Stats {
	s := Stats{Nodes: ix.n}
	for _, b := range ix.down {
		s.StoredWords += b.Words()
		s.ReachablePairs += b.Count()
	}
	for _, su := range ix.succ {
		s.Edges += len(su)
	}
	s.DenseWords = ix.n * (ix.n + 63) / 64
	return s
}

func (ix *Index) check(u int) {
	if u < 0 || u >= ix.n {
		panic(fmt.Sprintf("dagindex: node %d out of range [0,%d)", u, ix.n))
	}
}
