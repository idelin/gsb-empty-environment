package netsec

import (
	"fmt"
	"sort"
)

// LoopEvent records one moment where a propagated state reached a node whose
// accumulated fixpoint already covered it: the signature of a cycle reaching
// its fixed point rather than new state space.
type LoopEvent struct {
	Node      int
	Path      []int
	Subsumed  Rect
	Iteration int
}

// NodeCounters summarize symbolic activity observed at one node.
type NodeCounters struct {
	FilteredRects int
	OverflowRects int
	Propagations  int
	Subsumptions  int
}

type engineState struct {
	rect Rect
	path []int
}

type fixpointEngine struct {
	graph      *Graph
	maxIters   int
	states     map[int][]Rect
	outputs    map[int][]Rect
	exemplars  map[int]map[string][]int
	queued     map[string]bool
	queue      []engineState
	loops      []LoopEvent
	counters   map[int]*NodeCounters
	iterations int
}

func newFixpointEngine(g *Graph, maxIters int) *fixpointEngine {
	if maxIters <= 0 {
		maxIters = 100000
	}
	return &fixpointEngine{
		graph:     g,
		maxIters:  maxIters,
		states:    make(map[int][]Rect),
		outputs:   make(map[int][]Rect),
		exemplars: make(map[int]map[string][]int),
		queued:    make(map[string]bool),
		counters:  make(map[int]*NodeCounters),
	}
}

func (e *fixpointEngine) counter(node int) *NodeCounters {
	c, ok := e.counters[node]
	if !ok {
		c = &NodeCounters{}
		e.counters[node] = c
	}
	return c
}

func rectKey(r Rect) string {
	return fmt.Sprintf("%d:%d:%d:%d", r.S.Lo, r.S.Hi, r.D.Lo, r.D.Hi)
}

func clonePath(p []int) []int {
	out := make([]int, len(p))
	copy(out, p)
	return out
}

// run executes the chaotic-iteration fixpoint. The entry rectangle is seeded
// at entry; propagation follows graph edges. Termination is guaranteed by the
// finite partition of uint32 x uint32 induced by all rule boundaries, while
// maxIters is a defensive ceiling.
func (e *fixpointEngine) run(entry int, seed Rect) error {
	e.enqueue(entry, seed, []int{entry})
	for len(e.queue) > 0 {
		if e.iterations >= e.maxIters {
			return fmt.Errorf("netsec: fixpoint iteration cap %d exceeded at node %d", e.maxIters, e.queue[0].path[0])
		}
		item := e.queue[0]
		e.queue = e.queue[1:]
		delete(e.queued, queueKey(item))
		e.iterations++

		node, ok := e.graph.Nodes[item.path[len(item.path)-1]]
		if !ok {
			return fmt.Errorf("%w: %d", ErrUnknownNode, item.path[len(item.path)-1])
		}
		if coveredBySet(item.rect, e.states[node.ID]) {
			e.counter(node.ID).Subsumptions++
			e.loops = append(e.loops, LoopEvent{
				Node:      node.ID,
				Path:      clonePath(item.path),
				Subsumed:  item.rect,
				Iteration: e.iterations,
			})
			continue
		}
		e.absorb(node.ID, item)

		survivors, filtered, overflow := stepRect(node, item.rect)
		c := e.counter(node.ID)
		c.FilteredRects += len(filtered)
		c.OverflowRects += len(overflow)
		e.outputs[node.ID] = unionSet(e.outputs[node.ID], survivors)

		for _, r := range survivors {
			for _, next := range e.graph.Edges[node.ID] {
				path := append(clonePath(item.path), next)
				c.Propagations++
				e.enqueue(next, r, path)
			}
		}
	}
	return nil
}

func queueKey(s engineState) string {
	return fmt.Sprintf("%d|%s", s.path[len(s.path)-1], rectKey(s.rect))
}

func (e *fixpointEngine) enqueue(node int, r Rect, path []int) {
	key := fmt.Sprintf("%d|%s", node, rectKey(r))
	if e.queued[key] {
		return
	}
	e.queued[key] = true
	e.queue = append(e.queue, engineState{rect: r, path: clonePath(path)})
}

// absorb adds genuinely new state space to a node's fixpoint and remembers
// the first exemplar path that reached each canonical rectangle.
func (e *fixpointEngine) absorb(node int, item engineState) {
	existing := e.states[node]
	if coveredBySet(item.rect, existing) {
		return
	}
	e.states[node] = unionSet(existing, []Rect{item.rect})
	ex, ok := e.exemplars[node]
	if !ok {
		ex = make(map[string][]int)
		e.exemplars[node] = ex
	}
	key := rectKey(item.rect)
	if _, seen := ex[key]; !seen {
		ex[key] = clonePath(item.path)
	}
}

// exemplarPath returns the first-seen path for a rectangle at a node.
func (e *fixpointEngine) exemplarPath(node int, r Rect) []int {
	if ex, ok := e.exemplars[node]; ok {
		if p, seen := ex[rectKey(r)]; seen {
			return clonePath(p)
		}
		// Canonicalization may have merged r into a larger rectangle; fall
		// back to any stored exemplar whose region is covered by r.
		for key, p := range ex {
			if parsed, ok := parseRectKey(key); ok && r.covers(parsed) {
				return clonePath(p)
			}
		}
	}
	return nil
}

func parseRectKey(key string) (Rect, bool) {
	var sLo, sHi, dLo, dHi uint32
	if _, err := fmt.Sscanf(key, "%d:%d:%d:%d", &sLo, &sHi, &dLo, &dHi); err != nil {
		return Rect{}, false
	}
	return Rect{
		S: Interval{Lo: sLo, Hi: sHi},
		D: Interval{Lo: dLo, Hi: dHi},
	}, true
}

// reachableAt returns the canonical fixpoint state accumulated at node.
func (e *fixpointEngine) reachableAt(node int) []Rect {
	out := make([]Rect, len(e.states[node]))
	copy(out, e.states[node])
	return out
}

// outputAt returns the canonical post-node (post-filter, post-translation)
// state accumulated at node.
func (e *fixpointEngine) outputAt(node int) []Rect {
	out := make([]Rect, len(e.outputs[node]))
	copy(out, e.outputs[node])
	return out
}

// sortedNodeIDs returns node IDs in deterministic ascending order for reports.
func sortedNodeIDs(g *Graph) []int {
	ids := make([]int, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
