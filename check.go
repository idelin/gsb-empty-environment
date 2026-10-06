package reqresp

import (
	"fmt"
	"sort"
	"strings"
)

// Step is one transition of a counterexample trace.
type Step struct {
	From   string
	To     string
	Action Action
}

// Violation is a counterexample to the liveness property. It is either a
// lasso — Prefix leads onto Cycle, a repeatable loop on which the IDs in
// Owed stay unanswered forever — or, when Cycle is empty, a finite run
// ending in a terminal state that still owes the IDs in Owed.
type Violation struct {
	Prefix []Step
	Cycle  []Step
	Owed   []string
}

// Lasso reports whether the violation is an infinite-run counterexample
// (prefix plus repeatable cycle) rather than a finite run ending in a
// terminal state.
func (v *Violation) Lasso() bool { return len(v.Cycle) > 0 }

func (v *Violation) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "violation: owed=%v", v.Owed)
	writeSteps := func(name string, steps []Step) {
		fmt.Fprintf(&b, "\n  %s:", name)
		if len(steps) == 0 {
			b.WriteString(" (empty)")
		}
		for _, s := range steps {
			fmt.Fprintf(&b, "\n    %s --%s--> %s", s.From, s.Action, s.To)
		}
	}
	writeSteps("prefix", v.Prefix)
	if v.Lasso() {
		writeSteps("cycle", v.Cycle)
	} else {
		b.WriteString("\n  terminal state reached with responses still owed")
	}
	return b.String()
}

// Result is the outcome of Check. If Violation is non-nil the property
// fails and Violation holds a counterexample that Verify accepts.
type Result struct {
	// ProductStates is the number of explored product states
	// (graph state x pending-request set).
	ProductStates int
	Violation     *Violation
}

// OK reports whether the property holds.
func (r *Result) OK() bool { return r.Violation == nil }

// Check verifies that on every run of g — finite or infinite — every
// request is eventually answered by a response with the same id.
//
// It explores the product of g with an obligation monitor tracking the
// set of pending request ids. A reachable terminal graph state with a
// non-empty pending set is a finite counterexample. A reachable cycle on
// which some id stays pending throughout is an infinite counterexample,
// reported as a lasso.
//
// An error is returned for malformed models: a request whose id is
// already pending, or a response whose id is not pending.
func Check(g *Graph) (*Result, error) {
	p := &product{
		g:     g,
		nodes: make(map[string]*pnode),
	}
	if err := p.explore(); err != nil {
		return nil, err
	}
	res := &Result{ProductStates: len(p.nodes)}
	if p.terminal != nil {
		res.Violation = &Violation{
			Prefix: p.pathTo(p.terminal),
			Owed:   append([]string(nil), p.terminal.pending...),
		}
		return res, nil
	}
	if v := p.findLasso(); v != nil {
		res.Violation = v
	}
	return res, nil
}

// pnode is a product state: a graph state plus the sorted set of pending
// request ids.
type pnode struct {
	state   string
	pending []string // sorted
	out     []pedge
}

type pedge struct {
	step Step
	to   *pnode
}

func (n *pnode) key() string {
	return n.state + "\x00" + strings.Join(n.pending, "\x01")
}

type product struct {
	g        *Graph
	nodes    map[string]*pnode
	root     *pnode
	terminal *pnode // first terminal node with pending responses, in BFS order
	parent   map[*pnode]parentLink
}

type parentLink struct {
	node *pnode
	step Step
}

func (p *product) explore() error {
	p.parent = make(map[*pnode]parentLink)
	p.root = p.intern(p.g.Initial, nil)
	queue := []*pnode{p.root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if p.g.Terminal(n.state) {
			if len(n.pending) > 0 && p.terminal == nil {
				p.terminal = n
			}
			continue
		}
		for _, e := range p.g.out[n.state] {
			next, err := advance(n.pending, e)
			if err != nil {
				return err
			}
			m := p.intern(e.To, next)
			n.out = append(n.out, pedge{
				step: Step{From: e.From, To: e.To, Action: e.Action},
				to:   m,
			})
			if _, seen := p.parent[m]; !seen && m != p.root {
				p.parent[m] = parentLink{node: n, step: n.out[len(n.out)-1].step}
				queue = append(queue, m)
			}
		}
	}
	return nil
}

func (p *product) intern(state string, pending []string) *pnode {
	key := state + "\x00" + strings.Join(pending, "\x01")
	if n, ok := p.nodes[key]; ok {
		return n
	}
	n := &pnode{state: state, pending: pending}
	p.nodes[key] = n
	return n
}

// advance applies edge e to the pending set, enforcing the request/response
// discipline per explicit id.
func advance(pending []string, e Edge) ([]string, error) {
	switch e.Action.Kind {
	case Internal:
		return pending, nil
	case Request:
		if hasID(pending, e.Action.ID) {
			return nil, fmt.Errorf("reqresp: %s --%s--> %s: request id %q is already pending", e.From, e.Action, e.To, e.Action.ID)
		}
		next := append(append([]string(nil), pending...), e.Action.ID)
		sort.Strings(next)
		return next, nil
	case Response:
		if !hasID(pending, e.Action.ID) {
			return nil, fmt.Errorf("reqresp: %s --%s--> %s: response id %q is not pending", e.From, e.Action, e.To, e.Action.ID)
		}
		next := make([]string, 0, len(pending)-1)
		for _, id := range pending {
			if id != e.Action.ID {
				next = append(next, id)
			}
		}
		return next, nil
	}
	return nil, fmt.Errorf("reqresp: %s --%s--> %s: unknown action kind", e.From, e.Action, e.To)
}

func hasID(pending []string, id string) bool {
	i := sort.SearchStrings(pending, id)
	return i < len(pending) && pending[i] == id
}

// pathTo reconstructs the BFS path from the root to n.
func (p *product) pathTo(n *pnode) []Step {
	var rev []Step
	for n != p.root {
		link := p.parent[n]
		rev = append(rev, link.step)
		n = link.node
	}
	steps := make([]Step, len(rev))
	for i, s := range rev {
		steps[len(rev)-1-i] = s
	}
	return steps
}

// findLasso looks for a reachable cycle on which some request id stays
// pending throughout. For each id it restricts the product graph to the
// nodes where that id is pending and searches for a cycle there: such a
// cycle is repeatable, never answers the id, and every product node is
// reachable from the root by construction.
func (p *product) findLasso() *Violation {
	for _, id := range p.allPendingIDs() {
		if v := p.findLassoForID(id); v != nil {
			return v
		}
	}
	return nil
}

func (p *product) findLassoForID(id string) *Violation {
	allowed := func(n *pnode) bool { return hasID(n.pending, id) }
	for _, scc := range tarjan(p.nodesSorted(), allowed) {
		if !cyclic(scc) {
			continue
		}
		entry := scc[0]
		cycle := cycleThrough(entry, scc)
		if len(cycle) == 0 {
			continue
		}
		// Report the ids owed along the actual cycle, not just the
		// ones common to the whole component.
		owed := owedAlongCycle(entry, cycle)
		return &Violation{
			Prefix: p.pathTo(entry),
			Cycle:  cycle,
			Owed:   owed,
		}
	}
	return nil
}

// allPendingIDs returns the sorted set of request ids that are pending in
// at least one product node.
func (p *product) allPendingIDs() []string {
	seen := make(map[string]bool)
	var ids []string
	for _, n := range p.nodes {
		for _, id := range n.pending {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// nodesSorted returns all product nodes in a deterministic order.
func (p *product) nodesSorted() []*pnode {
	keys := make([]string, 0, len(p.nodes))
	for key := range p.nodes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	nodes := make([]*pnode, 0, len(keys))
	for _, key := range keys {
		nodes = append(nodes, p.nodes[key])
	}
	return nodes
}

// cyclic reports whether the SCC contains at least one edge, i.e. it has
// more than one member or a self-loop.
func cyclic(scc []*pnode) bool {
	if len(scc) > 1 {
		return true
	}
	n := scc[0]
	for _, e := range n.out {
		if e.to == n {
			return true
		}
	}
	return false
}

func intersectIDs(a, b []string) []string {
	var out []string
	for _, id := range a {
		if hasID(b, id) {
			out = append(out, id)
		}
	}
	return out
}

// cycleThrough finds a non-empty path of steps from start back to start
// using only nodes of scc.
func cycleThrough(start *pnode, scc []*pnode) []Step {
	in := make(map[*pnode]bool, len(scc))
	for _, n := range scc {
		in[n] = true
	}
	type item struct {
		node *pnode
		path []Step
	}
	visited := map[*pnode]bool{start: true}
	queue := []item{}
	for _, e := range start.out {
		if !in[e.to] {
			continue
		}
		if e.to == start {
			return []Step{e.step}
		}
		queue = append(queue, item{node: e.to, path: []Step{e.step}})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if visited[it.node] {
			continue
		}
		visited[it.node] = true
		for _, e := range it.node.out {
			if !in[e.to] {
				continue
			}
			if e.to == start {
				return append(append([]Step(nil), it.path...), e.step)
			}
			if !visited[e.to] {
				queue = append(queue, item{node: e.to, path: append(append([]Step(nil), it.path...), e.step)})
			}
		}
	}
	return nil
}

// owedAlongCycle returns the ids pending at the cycle entry and after
// every step of the cycle, i.e. owed for the whole loop.
func owedAlongCycle(entry *pnode, cycle []Step) []string {
	// Re-simulate the pending set along the cycle steps.
	pending := append([]string(nil), entry.pending...)
	owed := append([]string(nil), entry.pending...)
	for _, s := range cycle {
		next, err := advance(pending, Edge{From: s.From, To: s.To, Action: s.Action})
		if err != nil {
			return nil // cannot happen for a cycle built by Check
		}
		pending = next
		owed = intersectIDs(owed, pending)
	}
	return owed
}

// tarjan returns the strongly connected components of the explored
// tarjan returns the strongly connected components of the subgraph
// induced by the allowed nodes, in reverse topological order.
func tarjan(nodes []*pnode, allowed func(*pnode) bool) [][]*pnode {
	var (
		index   = make(map[*pnode]int)
		low     = make(map[*pnode]int)
		onStack = make(map[*pnode]bool)
		stack   []*pnode
		sccs    [][]*pnode
		counter int
	)
	var strongconnect func(n *pnode)
	strongconnect = func(n *pnode) {
		index[n] = counter
		low[n] = counter
		counter++
		stack = append(stack, n)
		onStack[n] = true
		for _, e := range n.out {
			m := e.to
			if !allowed(m) {
				continue
			}
			if _, seen := index[m]; !seen {
				strongconnect(m)
				if low[m] < low[n] {
					low[n] = low[m]
				}
			} else if onStack[m] && index[m] < low[n] {
				low[n] = index[m]
			}
		}
		if low[n] == index[n] {
			var scc []*pnode
			for {
				m := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[m] = false
				scc = append(scc, m)
				if m == n {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}
	for _, n := range nodes {
		if !allowed(n) {
			continue
		}
		if _, seen := index[n]; !seen {
			strongconnect(n)
		}
	}
	return sccs
}
