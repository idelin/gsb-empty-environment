// Package reqresp checks a liveness property of action-labeled state
// graphs: every request is eventually answered by a response carrying the
// same explicit id. Infinite runs are allowed; a violation is reported as
// a finite prefix plus a repeatable cycle on which a response stays owed
// forever (a lasso), or as a finite prefix ending in a terminal state
// that still owes a response.
package reqresp

import (
	"fmt"
	"sort"
)

// Kind classifies the action labeling an edge.
type Kind int

const (
	// Internal is an action that neither raises nor discharges an
	// obligation.
	Internal Kind = iota
	// Request raises an obligation: its ID must eventually be answered
	// by a Response with the same ID.
	Request
	// Response discharges the obligation raised by a Request with the
	// same ID.
	Response
)

func (k Kind) String() string {
	switch k {
	case Internal:
		return "internal"
	case Request:
		return "request"
	case Response:
		return "response"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Action is the label of an edge. Request and Response actions must carry
// a non-empty ID; Internal actions must have an empty ID.
type Action struct {
	Kind Kind
	ID   string
}

func (a Action) String() string {
	if a.Kind == Internal {
		return "internal"
	}
	return fmt.Sprintf("%s(%s)", a.Kind, a.ID)
}

// Edge is a transition between two states labeled with an action.
type Edge struct {
	From   string
	To     string
	Action Action
}

// Graph is a finite state graph with action-labeled edges.
type Graph struct {
	Initial string
	edges   []Edge
	out     map[string][]Edge
	states  map[string]bool
}

// NewGraph validates the edges and builds a graph rooted at initial.
// States are the endpoints of the given edges plus initial itself, so a
// graph consisting of a single terminal state is expressible.
func NewGraph(initial string, edges []Edge) (*Graph, error) {
	if initial == "" {
		return nil, fmt.Errorf("reqresp: initial state must be non-empty")
	}
	g := &Graph{
		Initial: initial,
		out:     make(map[string][]Edge),
		states:  make(map[string]bool),
	}
	g.states[initial] = true
	for i, e := range edges {
		if e.From == "" || e.To == "" {
			return nil, fmt.Errorf("reqresp: edge %d: states must be non-empty", i)
		}
		switch e.Action.Kind {
		case Internal:
			if e.Action.ID != "" {
				return nil, fmt.Errorf("reqresp: edge %d: internal action must not carry an id", i)
			}
		case Request, Response:
			if e.Action.ID == "" {
				return nil, fmt.Errorf("reqresp: edge %d: %s action requires an id", i, e.Action.Kind)
			}
		default:
			return nil, fmt.Errorf("reqresp: edge %d: unknown action kind %d", i, e.Action.Kind)
		}
		g.edges = append(g.edges, e)
		g.out[e.From] = append(g.out[e.From], e)
		g.states[e.From] = true
		g.states[e.To] = true
	}
	// Deterministic exploration order.
	for s := range g.out {
		sort.Slice(g.out[s], func(i, j int) bool {
			a, b := g.out[s][i], g.out[s][j]
			if a.To != b.To {
				return a.To < b.To
			}
			if a.Action.Kind != b.Action.Kind {
				return a.Action.Kind < b.Action.Kind
			}
			return a.Action.ID < b.Action.ID
		})
	}
	return g, nil
}

// Edges returns the edges the graph was built from.
func (g *Graph) Edges() []Edge { return append([]Edge(nil), g.edges...) }

// Terminal reports whether state has no outgoing edges.
func (g *Graph) Terminal(state string) bool { return len(g.out[state]) == 0 }
