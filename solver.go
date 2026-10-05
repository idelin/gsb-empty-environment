package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type opKind int

const (
	opAdd opKind = iota
	opDel
	opQuery
)

type op struct {
	tick int
	kind opKind
	u, v int
	line int // 1-based source line, for error messages
}

type edge [2]int

func makeEdge(u, v int) edge {
	if u > v {
		u, v = v, u
	}
	return edge{u, v}
}

// OpError reports an invalid operation in the input stream.
type OpError struct {
	Line int
	Msg  string
}

func (e *OpError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
}

// answer is the result of one query, evaluated against the graph state at the
// query's own position in the operation stream (never the final graph).
type answer struct {
	tick      int
	u, v      int
	connected bool
	path      []int // valid when connected
	component []int // valid when !connected: the connected component of u (a cut certificate)
}

func (a answer) String() string {
	if a.connected {
		parts := make([]string, len(a.path))
		for i, x := range a.path {
			parts[i] = strconv.Itoa(x)
		}
		return fmt.Sprintf("tick=%d query %d %d -> CONNECTED path=%s",
			a.tick, a.u, a.v, strings.Join(parts, "-"))
	}
	parts := make([]string, len(a.component))
	for i, x := range a.component {
		parts[i] = strconv.Itoa(x)
	}
	return fmt.Sprintf("tick=%d query %d %d -> DISCONNECTED cut={%s} (component of %d; no active edge crosses this cut, %d lies outside)",
		a.tick, a.u, a.v, strings.Join(parts, " "), a.u, a.v)
}

// edgeInterval is the inclusive range of operation positions at whose queries
// the edge is present. An edge added at position i is visible to queries at
// positions > i; one deleted at position j is invisible to queries at
// positions >= j, so the visible range is [i+1, j-1].
type edgeInterval struct {
	e      edge
	lo, hi int
}

// computeIntervals turns the add/del stream into per-edge alive intervals.
// Duplicate edges are reference counted: an edge is present while count > 0,
// so each 0->1 / 1->0 transition pair yields one interval. Deleting an edge
// whose count is zero is an error.
func computeIntervals(ops []op) ([]edgeInterval, error) {
	var intervals []edgeInterval
	count := make(map[edge]int)
	start := make(map[edge]int)
	for i, o := range ops {
		switch o.kind {
		case opAdd:
			e := makeEdge(o.u, o.v)
			if count[e] == 0 {
				start[e] = i
			}
			count[e]++
		case opDel:
			e := makeEdge(o.u, o.v)
			if count[e] == 0 {
				return nil, &OpError{Line: o.line, Msg: fmt.Sprintf("del %d %d: edge is not present", o.u, o.v)}
			}
			count[e]--
			if count[e] == 0 {
				intervals = append(intervals, edgeInterval{e: e, lo: start[e] + 1, hi: i - 1})
				delete(start, e)
			}
		}
	}
	for e, s := range start {
		intervals = append(intervals, edgeInterval{e: e, lo: s + 1, hi: len(ops) - 1})
	}
	return intervals, nil
}

// segTree stores each edge in the O(log n) canonical nodes covering its alive
// interval; a DFS over the tree then visits every time position with exactly
// the edges alive at that position on the current root-to-leaf path.
type segTree struct {
	n       int
	buckets [][]edge
}

func newSegTree(n int) *segTree {
	size := 1
	if m := 4 * n; m > size {
		size = m
	}
	return &segTree{n: n, buckets: make([][]edge, size)}
}

func (t *segTree) insert(lo, hi int, e edge) {
	if lo > hi || t.n == 0 {
		return
	}
	t.insertRec(1, 0, t.n-1, lo, hi, e)
}

func (t *segTree) insertRec(node, nl, nr, lo, hi int, e edge) {
	if lo <= nl && nr <= hi {
		t.buckets[node] = append(t.buckets[node], e)
		return
	}
	mid := (nl + nr) / 2
	if lo <= mid {
		t.insertRec(2*node, nl, mid, lo, hi, e)
	}
	if hi > mid {
		t.insertRec(2*node+1, mid+1, nr, lo, hi, e)
	}
}

// solve answers every query against the graph state at the query's own
// position, using the offline segment-tree-over-time batch algorithm.
func solve(ops []op) ([]answer, error) {
	intervals, err := computeIntervals(ops)
	if err != nil {
		return nil, err
	}
	n := len(ops)
	tree := newSegTree(n)
	for _, iv := range intervals {
		tree.insert(iv.lo, iv.hi, iv.e)
	}
	var answers []answer
	active := make(map[edge]struct{})
	var walk func(node, nl, nr int)
	walk = func(node, nl, nr int) {
		for _, e := range tree.buckets[node] {
			active[e] = struct{}{}
		}
		if nl == nr {
			if o := ops[nl]; o.kind == opQuery {
				answers = append(answers, answerQuery(o, active))
			}
		} else {
			mid := (nl + nr) / 2
			walk(2*node, nl, mid)
			walk(2*node+1, mid+1, nr)
		}
		for _, e := range tree.buckets[node] {
			delete(active, e)
		}
	}
	if n > 0 {
		walk(1, 0, n-1)
	}
	return answers, nil
}

// answerQuery builds the adjacency of the currently active edges and runs one
// BFS: a shortest u-v path when reachable, otherwise the full component of u
// as a separation certificate.
func answerQuery(o op, active map[edge]struct{}) answer {
	adj := buildAdj(active)
	path, comp, ok := bfs(adj, o.u, o.v)
	return answer{tick: o.tick, u: o.u, v: o.v, connected: ok, path: path, component: comp}
}

func buildAdj(active map[edge]struct{}) map[int][]int {
	adj := make(map[int][]int)
	for e := range active {
		adj[e[0]] = append(adj[e[0]], e[1])
		if e[0] != e[1] {
			adj[e[1]] = append(adj[e[1]], e[0])
		}
	}
	for v := range adj {
		sort.Ints(adj[v])
	}
	return adj
}

// bfs returns (path, nil, true) when v is reachable from u, otherwise
// (nil, component of u sorted, false). Neighbor lists must be sorted so the
// result is deterministic.
func bfs(adj map[int][]int, u, v int) ([]int, []int, bool) {
	if u == v {
		return []int{u}, nil, true
	}
	parent := map[int]int{u: -1}
	queue := []int{u}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, y := range adj[x] {
			if _, seen := parent[y]; seen {
				continue
			}
			parent[y] = x
			if y == v {
				path := []int{v}
				for p := x; p != -1; p = parent[p] {
					path = append(path, p)
				}
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				return path, nil, true
			}
			queue = append(queue, y)
		}
	}
	comp := make([]int, 0, len(parent))
	for x := range parent {
		comp = append(comp, x)
	}
	sort.Ints(comp)
	return nil, comp, false
}
