package hb

import (
	"fmt"
	"sort"
)

// edge is a happens-before arc from -> to with its justification.
type edge struct {
	from, to string
	why      string
}

// Graph accumulates events and derives the happens-before relation.
// Events may be added in any order; edges are resolved lazily when the
// graph is analyzed, so a Recv may be imported before its Send.
type Graph struct {
	events map[string]Event
	bySlot map[slot]string // (Node, Seq) -> event ID
}

type slot struct {
	node string
	seq  int
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		events: make(map[string]Event),
		bySlot: make(map[slot]string),
	}
}

// Len reports the number of distinct events.
func (g *Graph) Len() int { return len(g.events) }

// Get returns the event with the given ID.
func (g *Graph) Get(id string) (Event, bool) {
	e, ok := g.events[id]
	return e, ok
}

// Add imports one event. It returns merged=true when the event was already
// known with identical content (idempotent duplicate). A contradictory
// duplicate or a (Node, Seq) slot claimed by another event yields a
// *ConflictError. Structural problems (bad kind, dangling links) are
// reported by Validate, not here, so out-of-order import stays possible.
func (g *Graph) Add(e Event) (merged bool, err error) {
	if e.ID == "" {
		return false, fmt.Errorf("event with empty ID")
	}
	if e.Node == "" {
		return false, fmt.Errorf("event %q with empty node", e.ID)
	}
	if prev, ok := g.events[e.ID]; ok {
		if prev.equal(e) {
			return true, nil
		}
		return false, &ConflictError{
			ID:       e.ID,
			Existing: prev,
			Incoming: e,
			Reason:   "same ID, different content",
		}
	}
	s := slot{e.Node, e.Seq}
	if otherID, ok := g.bySlot[s]; ok {
		return false, &ConflictError{
			ID:       e.ID,
			Existing: g.events[otherID],
			Incoming: e,
			Reason:   fmt.Sprintf("slot (%s, %d) already held by %q", e.Node, e.Seq, otherID),
		}
	}
	g.events[e.ID] = e
	g.bySlot[s] = e.ID
	return false, nil
}

// edges derives all happens-before arcs from local order and message links.
func (g *Graph) edges() []edge {
	var edges []edge

	// Local order: per node, consecutive sequence numbers.
	byNode := make(map[string][]Event)
	for _, e := range g.events {
		byNode[e.Node] = append(byNode[e.Node], e)
	}
	nodes := make([]string, 0, len(byNode))
	for n := range byNode {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		evs := byNode[n]
		sort.Slice(evs, func(i, j int) bool { return evs[i].Seq < evs[j].Seq })
		for i := 0; i+1 < len(evs); i++ {
			edges = append(edges, edge{
				from: evs[i].ID,
				to:   evs[i+1].ID,
				why: fmt.Sprintf("local order on %s: seq %d precedes seq %d",
					n, evs[i].Seq, evs[i+1].Seq),
			})
		}
	}

	// Message order: send happens before matching receive.
	ids := g.sortedIDs()
	for _, id := range ids {
		e := g.events[id]
		if e.Kind != Recv || e.SendID == "" {
			continue
		}
		if _, ok := g.events[e.SendID]; !ok {
			continue // dangling link reported by Validate
		}
		edges = append(edges, edge{
			from: e.SendID,
			to:   e.ID,
			why:  fmt.Sprintf("message: %s received by %s", e.SendID, e.ID),
		})
	}
	return edges
}

func (g *Graph) sortedIDs() []string {
	ids := make([]string, 0, len(g.events))
	for id := range g.events {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// findCycle returns one causality cycle as evidence, or nil if acyclic.
// The returned path starts and ends with the same event ID.
func (g *Graph) findCycle() *CycleError {
	adj := make(map[string][]edge)
	for _, e := range g.edges() {
		adj[e.from] = append(adj[e.from], e)
	}
	for id := range adj {
		sort.Slice(adj[id], func(i, j int) bool { return adj[id][i].to < adj[id][j].to })
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(g.events))
	var stack []edge // edges taken to reach the current node

	var visit func(id string) *CycleError
	visit = func(id string) *CycleError {
		color[id] = gray
		for _, e := range adj[id] {
			switch color[e.to] {
			case gray:
				// Find where e.to first appears on the stack.
				start := 0
				for i, se := range stack {
					if se.from == e.to {
						start = i
						break
					}
				}
				cyc := &CycleError{}
				for _, se := range stack[start:] {
					cyc.Path = append(cyc.Path, se.from)
					cyc.Why = append(cyc.Why, se.why)
				}
				cyc.Path = append(cyc.Path, e.from, e.to)
				cyc.Why = append(cyc.Why, e.why)
				return cyc
			case white:
				stack = append(stack, e)
				if cyc := visit(e.to); cyc != nil {
					return cyc
				}
				stack = stack[:len(stack)-1]
			}
		}
		color[id] = black
		return nil
	}

	for _, id := range g.sortedIDs() {
		if color[id] == white {
			if cyc := visit(id); cyc != nil {
				return cyc
			}
		}
	}
	return nil
}
