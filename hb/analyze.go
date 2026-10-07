package hb

import (
	"container/heap"
	"fmt"
	"sort"
)

// Validate checks the message-graph invariants and returns every violation
// found, in deterministic order. A nil/empty result means the graph is a
// well-formed acyclic message graph:
//
//   - kind-fields: Recv events carry a SendID; non-Recv events do not.
//   - link-target: every SendID references an existing Send event.
//   - single-consumer: a Send is matched by at most one Recv.
//   - local-contiguity: per node, sequence numbers are 1..N without gaps.
//   - acyclicity: the happens-before relation has no cycle.
func (g *Graph) Validate() []error {
	var errs []error
	ids := g.sortedIDs()

	consumer := make(map[string]string) // SendID -> Recv ID
	for _, id := range ids {
		e := g.events[id]
		switch e.Kind {
		case Recv:
			if e.SendID == "" {
				errs = append(errs, &InvariantError{"kind-fields",
					fmt.Sprintf("recv %s has no SendID", id)})
				continue
			}
			src, ok := g.events[e.SendID]
			if !ok {
				errs = append(errs, &InvariantError{"link-target",
					fmt.Sprintf("recv %s references unknown send %q", id, e.SendID)})
				continue
			}
			if src.Kind != Send {
				errs = append(errs, &InvariantError{"link-target",
					fmt.Sprintf("recv %s references %s which is %s, not send", id, e.SendID, src.Kind)})
				continue
			}
			if prev, dup := consumer[e.SendID]; dup {
				errs = append(errs, &InvariantError{"single-consumer",
					fmt.Sprintf("send %s consumed by both %s and %s", e.SendID, prev, id)})
			} else {
				consumer[e.SendID] = id
			}
		case Send, Local:
			if e.SendID != "" {
				errs = append(errs, &InvariantError{"kind-fields",
					fmt.Sprintf("%s %s must not carry SendID %q", e.Kind, id, e.SendID)})
			}
		default:
			errs = append(errs, &InvariantError{"kind-fields",
				fmt.Sprintf("event %s has unknown kind %d", id, int(e.Kind))})
		}
	}

	byNode := make(map[string][]int)
	for _, e := range g.events {
		byNode[e.Node] = append(byNode[e.Node], e.Seq)
	}
	nodes := make([]string, 0, len(byNode))
	for n := range byNode {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		seqs := byNode[n]
		sort.Ints(seqs)
		for i, s := range seqs {
			if s != i+1 {
				errs = append(errs, &InvariantError{"local-contiguity",
					fmt.Sprintf("node %s: expected seq %d, found %d", n, i+1, s)})
				break
			}
		}
	}

	if cyc := g.findCycle(); cyc != nil {
		errs = append(errs, cyc)
	}
	return errs
}

// idHeap is a min-heap of event IDs for deterministic Kahn's algorithm.
type idHeap []string

func (h idHeap) Len() int            { return len(h) }
func (h idHeap) Less(i, j int) bool  { return h[i] < h[j] }
func (h idHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *idHeap) Push(x interface{}) { *h = append(*h, x.(string)) }
func (h *idHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// TopoSort returns a deterministic topological order of all events:
// Kahn's algorithm, breaking ties by lexicographically smallest event ID.
// It returns a *CycleError if the graph is cyclic.
func (g *Graph) TopoSort() ([]Event, error) {
	order, err := g.topoIDs()
	if err != nil {
		return nil, err
	}
	out := make([]Event, len(order))
	for i, id := range order {
		out[i] = g.events[id]
	}
	return out, nil
}

func (g *Graph) topoIDs() ([]string, error) {
	indeg := make(map[string]int, len(g.events))
	adj := make(map[string][]string)
	for id := range g.events {
		indeg[id] = 0
	}
	for _, e := range g.edges() {
		indeg[e.to]++
		adj[e.from] = append(adj[e.from], e.to)
	}

	ready := &idHeap{}
	for id, d := range indeg {
		if d == 0 {
			heap.Push(ready, id)
		}
	}

	var order []string
	for ready.Len() > 0 {
		id := heap.Pop(ready).(string)
		order = append(order, id)
		next := adj[id]
		sort.Strings(next)
		for _, to := range next {
			indeg[to]--
			if indeg[to] == 0 {
				heap.Push(ready, to)
			}
		}
	}
	if len(order) != len(g.events) {
		return nil, g.findCycle()
	}
	return order, nil
}

// LogicalClocks computes a Lamport logical clock for every event:
// each event's clock is one more than the maximum of its causal
// predecessors. Wall-clock timestamps are never consulted. It returns a
// *CycleError if the graph is cyclic.
func (g *Graph) LogicalClocks() (map[string]int, error) {
	order, err := g.topoIDs()
	if err != nil {
		return nil, err
	}
	preds := make(map[string][]string)
	for _, e := range g.edges() {
		preds[e.to] = append(preds[e.to], e.from)
	}
	clock := make(map[string]int, len(g.events))
	for _, id := range order {
		max := 0
		for _, p := range preds[id] {
			if clock[p] > max {
				max = clock[p]
			}
		}
		clock[id] = max + 1
	}
	return clock, nil
}
