package main

import (
	"fmt"
	"sort"
)

// opKind is the kind of an input event.
type opKind int

const (
	opAdd opKind = iota
	opDel
	opQuery
)

// event is one input operation. Events are processed strictly in input
// order; events sharing the same tick are ordered by their position in
// the input, and a query observes exactly the state produced by all
// preceding events.
type event struct {
	tick int
	op   opKind
	u, v int
}

// edgeKey is a normalized undirected edge (u <= v).
type edgeKey struct{ u, v int }

func makeEdge(u, v int) edgeKey {
	if u > v {
		u, v = v, u
	}
	return edgeKey{u, v}
}

// interval [lo, hi] means: the edge is active for every event index k
// with lo < k < hi (open on both ends, because an edge added by event lo
// is visible only to later events, and removed by event hi).
type interval struct{ lo, hi int }

// buildIntervals turns the event stream into per-edge alive intervals,
// applying reference counting: a duplicated add increments the count and
// only the delete that drives the count back to zero closes the edge.
// Deleting an edge whose count is zero is an error.
func buildIntervals(events []event) (map[edgeKey][]interval, error) {
	count := make(map[edgeKey]int)
	start := make(map[edgeKey]int)
	out := make(map[edgeKey][]interval)
	for i, ev := range events {
		switch ev.op {
		case opAdd:
			k := makeEdge(ev.u, ev.v)
			if count[k] == 0 {
				start[k] = i
			}
			count[k]++
		case opDel:
			k := makeEdge(ev.u, ev.v)
			if count[k] == 0 {
				return nil, fmt.Errorf("event #%d (tick %d): delete of non-existent edge %d-%d", i, ev.tick, ev.u, ev.v)
			}
			count[k]--
			if count[k] == 0 {
				out[k] = append(out[k], interval{lo: start[k], hi: i})
			}
		}
	}
	for k, c := range count {
		if c > 0 {
			out[k] = append(out[k], interval{lo: start[k], hi: len(events)})
		}
	}
	return out, nil
}

// rollbackDSU is a union-find with union by size and undo support.
type rollbackDSU struct {
	parent []int
	size   []int
	stack  []dsuChange
}

type dsuChange struct {
	child    int // root that got attached (-1 for a no-op union)
	root     int // root that grew
	rootSize int // size of root before the merge
}

func newRollbackDSU(n int) *rollbackDSU {
	parent := make([]int, n)
	size := make([]int, n)
	for i := range parent {
		parent[i] = i
		size[i] = 1
	}
	return &rollbackDSU{parent: parent, size: size}
}

func (d *rollbackDSU) find(x int) int {
	for d.parent[x] != x {
		x = d.parent[x]
	}
	return x
}

func (d *rollbackDSU) union(a, b int) {
	ra, rb := d.find(a), d.find(b)
	if ra == rb {
		d.stack = append(d.stack, dsuChange{child: -1})
		return
	}
	if d.size[ra] < d.size[rb] {
		ra, rb = rb, ra
	}
	d.stack = append(d.stack, dsuChange{child: rb, root: ra, rootSize: d.size[ra]})
	d.parent[rb] = ra
	d.size[ra] += d.size[rb]
}

func (d *rollbackDSU) connected(a, b int) bool { return d.find(a) == d.find(b) }

func (d *rollbackDSU) checkpoint() int { return len(d.stack) }

func (d *rollbackDSU) rollback(to int) {
	for len(d.stack) > to {
		c := d.stack[len(d.stack)-1]
		d.stack = d.stack[:len(d.stack)-1]
		if c.child < 0 {
			continue
		}
		d.parent[c.child] = c.child
		d.size[c.root] = c.rootSize
	}
}

// answer is the result for one query event, as of that query's time.
type answer struct {
	tick      int
	u, v      int
	connected bool
	path      []int // u..v if connected
	comp      []int // sorted component of u if disconnected
	rest      []int // sorted complement of comp
}

// vertexIndex collects sorted unique vertex labels and a compaction map.
func vertexIndex(events []event) ([]int, map[int]int) {
	set := make(map[int]bool)
	for _, ev := range events {
		set[ev.u] = true
		set[ev.v] = true
	}
	verts := make([]int, 0, len(set))
	for v := range set {
		verts = append(verts, v)
	}
	sort.Ints(verts)
	idx := make(map[int]int, len(verts))
	for i, v := range verts {
		idx[v] = i
	}
	return verts, idx
}

// solve answers every query event with the offline batch algorithm:
// segment tree over event indices + rollback DSU. The edge list carried
// down the recursion is exactly the set of edges alive at each leaf, so
// paths and cut certificates are built from the graph of that moment.
func solve(events []event) ([]answer, error) {
	intervals, err := buildIntervals(events)
	if err != nil {
		return nil, err
	}
	verts, idx := vertexIndex(events)
	n := len(events)
	s := &solver{
		events: events,
		verts:  verts,
		idx:    idx,
		dsu:    newRollbackDSU(len(verts)),
	}
	if n == 0 {
		return nil, nil
	}
	s.tree = make([][]edgeKey, 4*n)
	for e, ivs := range intervals {
		for _, iv := range ivs {
			l, r := iv.lo+1, iv.hi-1
			if l > r {
				continue // edge never visible to any later event
			}
			s.segAdd(1, 0, n-1, l, r, e)
		}
	}
	s.dfs(1, 0, n-1)
	return s.answers, nil
}

type solver struct {
	events  []event
	verts   []int
	idx     map[int]int
	tree    [][]edgeKey
	dsu     *rollbackDSU
	active  []edgeKey // edges alive on the current root-to-leaf path
	answers []answer
}

func (s *solver) segAdd(node, lo, hi, l, r int, e edgeKey) {
	if r < lo || hi < l {
		return
	}
	if l <= lo && hi <= r {
		s.tree[node] = append(s.tree[node], e)
		return
	}
	mid := (lo + hi) / 2
	s.segAdd(2*node, lo, mid, l, r, e)
	s.segAdd(2*node+1, mid+1, hi, l, r, e)
}

func (s *solver) dfs(node, lo, hi int) {
	check := s.dsu.checkpoint()
	mark := len(s.active)
	for _, e := range s.tree[node] {
		s.dsu.union(s.idx[e.u], s.idx[e.v])
		s.active = append(s.active, e)
	}
	if lo == hi {
		s.answerAt(lo)
	} else {
		mid := (lo + hi) / 2
		s.dfs(2*node, lo, mid)
		s.dfs(2*node+1, mid+1, hi)
	}
	s.active = s.active[:mark]
	s.dsu.rollback(check)
}

// answerAt answers the query at event index i against the graph alive at
// exactly that moment (s.active), never against the final graph.
func (s *solver) answerAt(i int) {
	ev := s.events[i]
	if ev.op != opQuery {
		return
	}
	a := answer{tick: ev.tick, u: ev.u, v: ev.v}
	if s.dsu.connected(s.idx[ev.u], s.idx[ev.v]) {
		a.connected = true
		a.path = bfsPath(s.active, ev.u, ev.v)
	} else {
		comp := bfsComponent(s.active, ev.u)
		inComp := make(map[int]bool, len(comp))
		for v := range comp {
			inComp[v] = true
			a.comp = append(a.comp, v)
		}
		for _, v := range s.verts {
			if !inComp[v] {
				a.rest = append(a.rest, v)
			}
		}
		sort.Ints(a.comp)
	}
	s.answers = append(s.answers, a)
}

func bfsAdj(active []edgeKey) map[int][]int {
	adj := make(map[int][]int)
	for _, e := range active {
		adj[e.u] = append(adj[e.u], e.v)
		adj[e.v] = append(adj[e.v], e.u)
	}
	return adj
}

// bfsPath returns a shortest u-v path in the given graph snapshot, or
// nil if none exists.
func bfsPath(active []edgeKey, u, v int) []int {
	if u == v {
		return []int{u}
	}
	adj := bfsAdj(active)
	prev := map[int]int{u: u}
	queue := []int{u}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, y := range adj[x] {
			if _, seen := prev[y]; seen {
				continue
			}
			prev[y] = x
			if y == v {
				path := []int{v}
				for cur := v; cur != u; {
					cur = prev[cur]
					path = append([]int{cur}, path...)
				}
				return path
			}
			queue = append(queue, y)
		}
	}
	return nil
}

// bfsComponent returns the connected component of u as a set.
func bfsComponent(active []edgeKey, u int) map[int]bool {
	adj := bfsAdj(active)
	comp := map[int]bool{u: true}
	queue := []int{u}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, y := range adj[x] {
			if !comp[y] {
				comp[y] = true
				queue = append(queue, y)
			}
		}
	}
	return comp
}
