package eventgraph

import (
	"fmt"
	"sort"
)

// EdgeReason explains why a happens-before edge exists.
type EdgeReason int

const (
	ReasonProcessOrder EdgeReason = iota // same process, consecutive Seq
	ReasonMessage                        // send -> recv of one message
)

func (r EdgeReason) String() string {
	if r == ReasonMessage {
		return "message"
	}
	return "process-order"
}

// Edge is a happens-before relation From -> To.
type Edge struct {
	From   string
	To     string
	Reason EdgeReason
}

// Contradiction records conflicting observations: either two events sharing
// an ID but disagreeing on causal content, or an association that closes a
// causality cycle. Cycle carries the evidence path when applicable.
type Contradiction struct {
	Kind    string   // "conflicting-duplicate" or "cycle"
	Message string   // human-readable summary
	Cycle   []string // event IDs forming the cycle evidence, last == first
}

// Graph is a happens-before DAG (or would-be DAG) built incrementally.
// Events may be added in any order; edges appear as both endpoints become
// known. Duplicate adds of causally identical events are merged; duplicates
// with conflicting causal content are recorded as contradictions.
type Graph struct {
	events   map[string]*Event
	byProc   map[string]map[uint64]*Event // process -> seq -> event
	sends    map[string]*Event            // message id -> send event
	recvs    map[string][]*Event          // message id -> recv events
	dupSends map[string][]*Event          // message id -> extra conflicting sends

	edges   map[string]map[string]EdgeReason // from -> to -> reason
	revEdge map[string]map[string]EdgeReason // to -> from -> reason

	contradictions []Contradiction
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		events:   make(map[string]*Event),
		byProc:   make(map[string]map[uint64]*Event),
		sends:    make(map[string]*Event),
		recvs:    make(map[string][]*Event),
		dupSends: make(map[string][]*Event),
		edges:    make(map[string]map[string]EdgeReason),
		revEdge:  make(map[string]map[string]EdgeReason),
	}
}

// AddEvent inserts ev. A causally identical re-add is merged (the earliest
// wall-clock display timestamp is kept). A re-add with conflicting causal
// content is rejected and recorded as a contradiction. Message and
// process-order edges are (re)linked as endpoints arrive, so import order
// does not matter.
func (g *Graph) AddEvent(ev Event) {
	if prev, ok := g.events[ev.ID]; ok {
		if sameCausalContent(prev, &ev) {
			// Merge: keep earliest display timestamp, causality untouched.
			if prev.WallClock.IsZero() ||
				(!ev.WallClock.IsZero() && ev.WallClock.Before(prev.WallClock)) {
				prev.WallClock = ev.WallClock
			}
			return
		}
		g.contradictions = append(g.contradictions, Contradiction{
			Kind: "conflicting-duplicate",
			Message: fmt.Sprintf("event %s re-added with conflicting content: have %s, got %s",
				ev.ID, prev, &ev),
		})
		return
	}

	e := ev // copy
	g.events[e.ID] = &e

	seqs := g.byProc[e.Process]
	if seqs == nil {
		seqs = make(map[uint64]*Event)
		g.byProc[e.Process] = seqs
	}
	if other, dup := seqs[e.Seq]; dup {
		g.contradictions = append(g.contradictions, Contradiction{
			Kind: "conflicting-duplicate",
			Message: fmt.Sprintf("process %s seq %d claimed by both %s and %s",
				e.Process, e.Seq, other, &e),
		})
	}
	seqs[e.Seq] = &e

	// Process-order edges to already-known neighbours.
	if p, ok := seqs[e.Seq-1]; ok && e.Seq > 0 {
		g.addEdge(p.ID, e.ID, ReasonProcessOrder)
	}
	if n, ok := seqs[e.Seq+1]; ok {
		g.addEdge(e.ID, n.ID, ReasonProcessOrder)
	}

	// Message edges.
	switch e.Kind {
	case KindSend:
		if prev, dup := g.sends[e.Message]; dup {
			g.contradictions = append(g.contradictions, Contradiction{
				Kind: "conflicting-duplicate",
				Message: fmt.Sprintf("message %q has two sends: %s and %s",
					e.Message, prev, &e),
			})
			g.dupSends[e.Message] = append(g.dupSends[e.Message], &e)
		} else {
			g.sends[e.Message] = &e
		}
		for _, r := range g.recvs[e.Message] {
			g.addEdge(e.ID, r.ID, ReasonMessage)
		}
	case KindRecv:
		g.recvs[e.Message] = append(g.recvs[e.Message], &e)
		if s, ok := g.sends[e.Message]; ok {
			g.addEdge(s.ID, e.ID, ReasonMessage)
		}
	}
}

// addEdge records from -> to. If to already reaches from, the new edge
// closes a cycle: the association contradicts established causality, so the
// cycle path is recorded as evidence. The edge is still stored so that
// TopoSort and Validate report the cycle consistently.
func (g *Graph) addEdge(from, to string, reason EdgeReason) {
	if g.edges[from] != nil {
		if _, ok := g.edges[from][to]; ok {
			return
		}
	}
	if path := g.path(to, from); path != nil {
		cycle := append(append([]string{}, path...), to)
		g.contradictions = append(g.contradictions, Contradiction{
			Kind: "cycle",
			Message: fmt.Sprintf("edge %s -> %s (%s) closes a causality cycle",
				from, to, reason),
			Cycle: cycle,
		})
	}
	if g.edges[from] == nil {
		g.edges[from] = make(map[string]EdgeReason)
	}
	g.edges[from][to] = reason
	if g.revEdge[to] == nil {
		g.revEdge[to] = make(map[string]EdgeReason)
	}
	g.revEdge[to][from] = reason
}

// path returns one path of event IDs from -> to following current edges,
// or nil if to is not reachable from from. Deterministic: neighbours are
// visited in sorted ID order.
func (g *Graph) path(from, to string) []string {
	if from == to {
		return []string{from}
	}
	visited := map[string]bool{from: true}
	parent := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		nexts := make([]string, 0, len(g.edges[cur]))
		for n := range g.edges[cur] {
			nexts = append(nexts, n)
		}
		sort.Strings(nexts)
		for _, n := range nexts {
			if visited[n] {
				continue
			}
			visited[n] = true
			parent[n] = cur
			if n == to {
				p := []string{to}
				for p[0] != from {
					p = append([]string{parent[p[0]]}, p...)
				}
				return p
			}
			queue = append(queue, n)
		}
	}
	return nil
}

// Event returns the stored event by ID, or nil.
func (g *Graph) Event(id string) *Event { return g.events[id] }

// Events returns all events sorted by ID (deterministic).
func (g *Graph) Events() []*Event {
	out := make([]*Event, 0, len(g.events))
	for _, e := range g.events {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Edges returns all happens-before edges, sorted by (From, To).
func (g *Graph) Edges() []Edge {
	out := make([]Edge, 0)
	for from, tos := range g.edges {
		for to, reason := range tos {
			out = append(out, Edge{From: from, To: to, Reason: reason})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// Contradictions returns every recorded contradiction in detection order.
func (g *Graph) Contradictions() []Contradiction {
	return append([]Contradiction{}, g.contradictions...)
}
