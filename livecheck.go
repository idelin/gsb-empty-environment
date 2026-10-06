// Package livecheck checks labeled state graphs for the liveness property
// that every request is eventually answered by a response carrying the
// same id. Runs may be infinite; a violation is reported either as a
// lasso (a finite prefix plus a cycle that can be repeated forever while
// some id is owed a response throughout) or as a finite run ending in a
// terminal state that still owes a response.
package livecheck

import (
	"fmt"
	"sort"
	"strings"
)

// Kind classifies the action labeling an edge.
type Kind int

const (
	// Request raises an obligation for its id.
	Request Kind = iota
	// Response discharges the obligation for its id.
	Response
	// Other is any action that neither raises nor discharges an
	// obligation; ID then simply serves as a label.
	Other
)

// Action labels an edge. For Request and Response, ID identifies the
// request being raised or answered; multiple concurrent requests are
// tracked separately by their explicit ids.
type Action struct {
	Kind Kind
	ID   string
}

func (a Action) String() string {
	switch a.Kind {
	case Request:
		return fmt.Sprintf("request(%s)", a.ID)
	case Response:
		return fmt.Sprintf("response(%s)", a.ID)
	default:
		if a.ID == "" {
			return "other"
		}
		return a.ID
	}
}

// Edge is a labeled transition.
type Edge struct {
	To     string
	Action Action
}

// Graph is a finite state graph whose edges carry action labels.
type Graph struct {
	initial string
	edges   map[string][]Edge
}

// NewGraph returns an empty graph with the given initial state.
func NewGraph(initial string) *Graph {
	return &Graph{initial: initial, edges: make(map[string][]Edge)}
}

// AddEdge adds a labeled edge from one state to another. States are
// created implicitly by being named on an edge.
func (g *Graph) AddEdge(from, to string, a Action) {
	g.edges[from] = append(g.edges[from], Edge{To: to, Action: a})
}

// Initial returns the initial state.
func (g *Graph) Initial() string { return g.initial }

// Edges returns the outgoing edges of a state, in insertion order.
func (g *Graph) Edges(from string) []Edge { return g.edges[from] }

// Step is one transition of a run.
type Step struct {
	From   string
	To     string
	Action Action
}

func (s Step) String() string {
	return fmt.Sprintf("%s --%s--> %s", s.From, s.Action, s.To)
}

// Counterexample is a witness of a property violation. Prefix is a
// finite run from the initial state. If Terminal is false, Cycle is a
// non-empty loop starting and ending at the state reached by Prefix,
// along which PendingID is owed a response at every point and is never
// answered; repeating Cycle forever yields an infinite run that owes a
// response forever. If Terminal is true, Cycle is empty and Prefix ends
// in a state with no outgoing edges that still owes PendingID a
// response.
type Counterexample struct {
	Prefix    []Step
	Cycle     []Step
	PendingID string
	Terminal  bool
}

func (ce *Counterexample) String() string {
	var b strings.Builder
	b.WriteString("prefix:\n")
	for _, s := range ce.Prefix {
		fmt.Fprintf(&b, "  %s\n", s)
	}
	if ce.Terminal {
		fmt.Fprintf(&b, "terminal state reached with id %q still owed a response\n", ce.PendingID)
		return b.String()
	}
	b.WriteString("cycle (repeat forever):\n")
	for _, s := range ce.Cycle {
		fmt.Fprintf(&b, "  %s\n", s)
	}
	fmt.Fprintf(&b, "id %q is owed a response throughout the cycle\n", ce.PendingID)
	return b.String()
}

// pstate is a product state: a graph state paired with the set of ids
// currently owed a response (canonically encoded).
type pstate struct {
	node string
	pend string
}

func encodePending(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	return strings.Join(sorted, "\x00")
}

func decodePending(key string) []string {
	if key == "" {
		return nil
	}
	return strings.Split(key, "\x00")
}

// applyAction advances the pending-id set across an action.
func applyAction(key string, a Action) string {
	ids := decodePending(key)
	switch a.Kind {
	case Request:
		found := false
		for _, id := range ids {
			if id == a.ID {
				found = true
				break
			}
		}
		if !found {
			ids = append(ids, a.ID)
		}
	case Response:
		out := ids[:0]
		for _, id := range ids {
			if id != a.ID {
				out = append(out, id)
			}
		}
		ids = out
	}
	return encodePending(ids)
}

// Check verifies that every request in every run of g is eventually
// answered by a response with the same id. It returns nil (and a nil
// error) when the property holds. Otherwise it returns a
// Counterexample that has been independently replayed and confirmed by
// Verify before being returned.
func Check(g *Graph) (*Counterexample, error) {
	start := pstate{node: g.initial}
	type prevStep struct {
		prev pstate
		step Step
	}
	parent := make(map[pstate]prevStep)
	seen := map[pstate]bool{start: true}
	order := []pstate{start}
	queue := []pstate{start}

	buildPath := func(to pstate) []Step {
		var rev []Step
		for cur := to; cur != start; {
			ps := parent[cur]
			rev = append(rev, ps.step)
			cur = ps.prev
		}
		for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
			rev[i], rev[j] = rev[j], rev[i]
		}
		return rev
	}

	// Breadth-first exploration of the product of the graph with the
	// obligation (pending-id) state.
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		edges := g.edges[cur.node]
		if len(edges) == 0 && cur.pend != "" {
			ce := &Counterexample{
				Prefix:    buildPath(cur),
				PendingID: decodePending(cur.pend)[0],
				Terminal:  true,
			}
			if err := Verify(g, ce); err != nil {
				return nil, fmt.Errorf("livecheck: internal replay rejected terminal counterexample: %v", err)
			}
			return ce, nil
		}
		for _, e := range edges {
			next := pstate{node: e.To, pend: applyAction(cur.pend, e.Action)}
			if !seen[next] {
				seen[next] = true
				parent[next] = prevStep{prev: cur, step: Step{From: cur.node, To: e.To, Action: e.Action}}
				order = append(order, next)
				queue = append(queue, next)
			}
		}
	}

	// Liveness: look for a reachable product state that can loop back to
	// itself while some owed id is never answered along the loop. Such a
	// loop repeated forever keeps that id owed forever.
	for _, s := range order {
		if s.pend == "" {
			continue
		}
		for _, id := range decodePending(s.pend) {
			cycle, ok := findCycle(g, s, id)
			if !ok {
				continue
			}
			ce := &Counterexample{
				Prefix:    buildPath(s),
				Cycle:     cycle,
				PendingID: id,
			}
			if err := Verify(g, ce); err != nil {
				return nil, fmt.Errorf("livecheck: internal replay rejected lasso counterexample: %v", err)
			}
			return ce, nil
		}
	}
	return nil, nil
}

// findCycle searches for a non-empty path from start back to start that
// never answers id (no Response edge for id). Because id is owed at
// start and is never discharged along the path, it remains owed at
// every point of the cycle.
func findCycle(g *Graph, start pstate, id string) ([]Step, bool) {
	skips := func(a Action) bool {
		return a.Kind == Response && a.ID == id
	}
	type prevStep struct {
		prev pstate
		step Step
	}
	parent := make(map[pstate]prevStep)
	seen := map[pstate]bool{start: true}
	var queue []pstate

	buildCycle := func(from pstate, last Step) []Step {
		rev := []Step{last}
		for cur := from; cur != start; {
			ps := parent[cur]
			rev = append(rev, ps.step)
			cur = ps.prev
		}
		for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
			rev[i], rev[j] = rev[j], rev[i]
		}
		return rev
	}

	push := func(from pstate, e Edge) ([]Step, bool) {
		if skips(e.Action) {
			return nil, false
		}
		next := pstate{node: e.To, pend: applyAction(from.pend, e.Action)}
		step := Step{From: from.node, To: e.To, Action: e.Action}
		if next == start {
			return buildCycle(from, step), true
		}
		if !seen[next] {
			seen[next] = true
			parent[next] = prevStep{prev: from, step: step}
			queue = append(queue, next)
		}
		return nil, false
	}

	for _, e := range g.edges[start.node] {
		if cycle, ok := push(start, e); ok {
			return cycle, true
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.edges[cur.node] {
			if cycle, ok := push(cur, e); ok {
				return cycle, true
			}
		}
	}
	return nil, false
}
