// Package dagreach implements a reachability bitmap indexer for directed
// acyclic graphs (DAGs).
//
// Each node owns a bitmap (slice of uint64 words) representing its
// transitive closure: bit v is set in row u iff v is reachable from u.
// Queries Reachable(u, v) are O(1) bit tests. Edge insertions propagate
// the inserted subtree's reach-set upward through reverse edges using
// word-parallel bitwise OR, with superset pruning so unchanged branches
// stop early. Nodes are also assigned topological levels which are
// maintained incrementally and used to keep the DAG layered.
package dagreach

import "errors"

// ErrCycle is returned when an edge insertion would close a cycle.
var ErrCycle = errors.New("dagreach: edge would create a cycle")

// ErrSelfLoop is returned when an edge connects a node to itself.
var ErrSelfLoop = errors.New("dagreach: self loops are not allowed")

// ErrNodeRange is returned when a node id is out of range.
var ErrNodeRange = errors.New("dagreach: node id out of range")

const wordBits = 64

// Indexer is a dynamic DAG reachability index backed by dense bitmaps.
type Indexer struct {
	n        int
	words    int
	reach    [][]uint64 // reach[u] has bit v set iff u reaches v (u itself included)
	parents  [][]int    // reverse adjacency
	children [][]int    // forward adjacency
	level    []int      // topological level: for every edge u->v, level[u] < level[v]
	edgeSet  map[uint64]struct{}
}

// NewIndexer creates an empty index. capacity is a hint for the expected
// number of nodes; the index still grows dynamically via AddNode.
func NewIndexer(capacity int) *Indexer {
	if capacity < 0 {
		capacity = 0
	}
	words := (capacity + wordBits - 1) / wordBits
	return &Indexer{
		words:    words,
		reach:    make([][]uint64, 0, capacity),
		parents:  make([][]int, 0, capacity),
		children: make([][]int, 0, capacity),
		level:    make([]int, 0, capacity),
		edgeSet:  make(map[uint64]struct{}),
	}
}

// Size returns the number of nodes in the graph.
func (idx *Indexer) Size() int { return idx.n }

// EdgeCount returns the number of inserted edges.
func (idx *Indexer) EdgeCount() int { return len(idx.edgeSet) }

// Level returns the topological level of node u. For every edge u->v the
// invariant Level(u) < Level(v) holds.
func (idx *Indexer) Level(u int) int { return idx.level[u] }

// AddNode appends a new isolated node and returns its id.
func (idx *Indexer) AddNode() int {
	id := idx.n
	idx.n++
	if id >= idx.words*wordBits {
		idx.growWords(idx.n)
	}
	row := make([]uint64, idx.words)
	row[id/wordBits] = 1 << (uint(id) % wordBits) // a node reaches itself
	idx.reach = append(idx.reach, row)
	idx.parents = append(idx.parents, nil)
	idx.children = append(idx.children, nil)
	idx.level = append(idx.level, 0)
	return id
}

// growWords widens every bitmap row so it can hold at least n bits.
func (idx *Indexer) growWords(n int) {
	need := (n + wordBits - 1) / wordBits
	if need <= idx.words {
		return
	}
	for i, row := range idx.reach {
		grown := make([]uint64, need)
		copy(grown, row)
		idx.reach[i] = grown
	}
	idx.words = need
}

func (idx *Indexer) checkNode(u int) error {
	if u < 0 || u >= idx.n {
		return ErrNodeRange
	}
	return nil
}

// Reachable reports in O(1) whether v is reachable from u.
func (idx *Indexer) Reachable(u, v int) bool {
	if u < 0 || u >= idx.n || v < 0 || v >= idx.n {
		return false
	}
	return idx.reach[u][v/wordBits]&(1<<(uint(v)%wordBits)) != 0
}

// ReachSet returns a copy of the reachability bitmap row of u.
func (idx *Indexer) ReachSet(u int) []uint64 {
	out := make([]uint64, idx.words)
	copy(out, idx.reach[u])
	return out
}

// AddEdge inserts the directed edge u->v and incrementally updates the
// transitive closure. It returns ErrCycle (and changes nothing) if the
// edge would close a cycle, and ErrSelfLoop if u == v. Re-inserting an
// existing edge is a no-op.
//
// Update strategy: the reach-set of v (a bitmap of idx.words uint64
// words) is merged into u and every ancestor of u via word-parallel
// bitwise OR. If merging into some ancestor a changes nothing, a already
// reaches everything v reaches, and so do all of a's ancestors, so that
// branch is pruned.
func (idx *Indexer) AddEdge(u, v int) error {
	if err := idx.checkNode(u); err != nil {
		return err
	}
	if err := idx.checkNode(v); err != nil {
		return err
	}
	if u == v {
		return ErrSelfLoop
	}
	key := uint64(u)<<32 | uint64(uint32(v))
	if _, dup := idx.edgeSet[key]; dup {
		return nil
	}
	// Cycle check is a single O(1) bitmap probe: if v already reaches u,
	// adding u->v would close a cycle.
	if idx.reach[v][u/wordBits]&(1<<(uint(u)%wordBits)) != 0 {
		return ErrCycle
	}

	idx.edgeSet[key] = struct{}{}
	idx.children[u] = append(idx.children[u], v)
	idx.parents[v] = append(idx.parents[v], u)

	idx.relabelLevels(u, v)
	idx.propagate(u, v)
	return nil
}

// relabelLevels restores the topological layering invariant after u->v
// was inserted: if v is not strictly deeper than u, v and its downstream
// subgraph are pushed to deeper levels.
func (idx *Indexer) relabelLevels(u, v int) {
	if idx.level[v] > idx.level[u] {
		return
	}
	type item struct{ node, lvl int }
	queue := []item{{v, idx.level[u] + 1}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.lvl <= idx.level[it.node] {
			continue
		}
		idx.level[it.node] = it.lvl
		for _, c := range idx.children[it.node] {
			if idx.level[c] <= it.lvl {
				queue = append(queue, item{c, it.lvl + 1})
			}
		}
	}
}

// propagate merges reach[v] into u and all ancestors of u, word by word.
func (idx *Indexer) propagate(u, v int) {
	src := idx.reach[v]
	stack := []int{u}
	for len(stack) > 0 {
		a := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if orInto(idx.reach[a], src) {
			stack = append(stack, idx.parents[a]...)
		}
	}
}

// orInto sets dst |= src word by word and reports whether dst changed.
func orInto(dst, src []uint64) bool {
	changed := false
	for i, w := range src {
		if w != 0 {
			old := dst[i]
			dst[i] = old | w
			if dst[i] != old {
				changed = true
			}
		}
	}
	return changed
}

// MemoryBytes estimates the dense bitmap memory footprint in bytes.
func (idx *Indexer) MemoryBytes() int {
	return idx.n * idx.words * 8
}
