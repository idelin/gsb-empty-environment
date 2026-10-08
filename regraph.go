// Package regraph implements a reactive computation graph. Nodes recompute
// when their inputs change; each Evaluate call propagates changes through
// one batch in topological order, so shared (diamond) dependencies are
// computed at most once per batch.
package regraph

import (
	"errors"
	"fmt"
)

// ErrCycle is returned when a dependency would introduce a cycle and the
// graph was configured with WithCycleRejection.
var ErrCycle = errors.New("regraph: dependency would create a cycle")

// State describes a node's position in the propagation lifecycle.
type State int

const (
	// Clean means the node's value is up to date with its inputs.
	Clean State = iota
	// Dirty means the node (or an upstream input) changed and it must
	// recompute in the next batch.
	Dirty
	// Skipped means the node was not computed because an upstream
	// dependency failed or was itself skipped.
	Skipped
)

func (s State) String() string {
	switch s {
	case Clean:
		return "Clean"
	case Dirty:
		return "Dirty"
	case Skipped:
		return "Skipped"
	}
	return "Unknown"
}

// Graph owns a set of nodes and evaluates them in batches.
type Graph struct {
	nodes        []*Node
	rejectCycles bool
	evaluating   bool
	staged       []func() error
	nextID       int
}

// Option configures a Graph.
type Option func(*Graph)

// WithCycleRejection makes adding a dependency that closes a cycle fail
// with ErrCycle instead of being accepted silently.
func WithCycleRejection() Option {
	return func(g *Graph) { g.rejectCycles = true }
}

// New creates an empty Graph.
func New(opts ...Option) *Graph {
	g := &Graph{}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Node is a unit of computation. Nodes with a nil compute function are
// sources: their value is pushed in via Set.
type Node struct {
	graph      *Graph
	id         int
	name       string
	fn         func(*Context) (any, error)
	value      any
	err        error
	state      State
	deps       map[*Node]struct{}
	dependents map[*Node]struct{}
}

// Node registers a computed node. If called during Evaluate, registration
// is staged and the node joins the graph (and is first computed) in the
// next batch.
func (g *Graph) Node(name string, fn func(*Context) (any, error)) *Node {
	n := &Node{
		graph:      g,
		id:         g.nextID,
		name:       name,
		fn:         fn,
		state:      Dirty,
		deps:       make(map[*Node]struct{}),
		dependents: make(map[*Node]struct{}),
	}
	g.nextID++
	if g.evaluating {
		g.staged = append(g.staged, func() error {
			g.nodes = append(g.nodes, n)
			return nil
		})
	} else {
		g.nodes = append(g.nodes, n)
	}
	return n
}

// Source registers an input node holding v. Update it with Set.
func (g *Graph) Source(name string, v any) *Node {
	n := g.Node(name, nil)
	n.value = v
	n.state = Clean
	return n
}

// Name returns the node's label.
func (n *Node) Name() string { return n.name }

// Value returns the last computed (or pushed) value.
func (n *Node) Value() any { return n.value }

// Err returns the error from the last computation, if any.
func (n *Node) Err() error { return n.err }

// State returns the node's current State.
func (n *Node) State() State { return n.state }

// Set pushes a new value into a source node and dirties the graph so the
// next Evaluate propagates it.
func (n *Node) Set(v any) {
	n.value = v
	n.err = nil
	n.state = Dirty
}

// Invalidate marks the node dirty so it recomputes on the next Evaluate.
func (n *Node) Invalidate() { n.state = Dirty }

// DependsOn declares that n reads up. Outside Evaluate the edge is added
// immediately; during Evaluate it is staged and only takes effect after
// the current batch finishes, so the graph is never mutated while it is
// being traversed.
func (n *Node) DependsOn(up *Node) error {
	if n.graph != up.graph {
		return errors.New("regraph: nodes belong to different graphs")
	}
	if n.graph.evaluating {
		n.graph.staged = append(n.graph.staged, func() error {
			return n.graph.addEdge(n, up)
		})
		return nil
	}
	return n.graph.addEdge(n, up)
}

func (g *Graph) addEdge(n, up *Node) error {
	if n == up {
		return fmt.Errorf("regraph: self dependency on %q: %w", n.name, ErrCycle)
	}
	if g.rejectCycles && up.reaches(n) {
		return fmt.Errorf("regraph: %q -> %q: %w", n.name, up.name, ErrCycle)
	}
	if _, ok := n.deps[up]; ok {
		return nil
	}
	n.deps[up] = struct{}{}
	up.dependents[n] = struct{}{}
	n.state = Dirty
	return nil
}

// reaches reports whether target is reachable from n following dependency
// (upstream) edges.
func (n *Node) reaches(target *Node) bool {
	seen := map[*Node]struct{}{n: {}}
	stack := []*Node{n}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for dep := range cur.deps {
			if dep == target {
				return true
			}
			if _, ok := seen[dep]; !ok {
				seen[dep] = struct{}{}
				stack = append(stack, dep)
			}
		}
	}
	return false
}

// Context is passed to a node's compute function.
type Context struct {
	node *Node
}

// Get reads dep's current value. If dep is not yet a declared dependency,
// the edge is staged and takes effect from the next batch onward; this
// batch observes dep's current (possibly stale) value.
func (c *Context) Get(dep *Node) any {
	if _, ok := c.node.deps[dep]; !ok {
		_ = c.node.DependsOn(dep) // staged because we are evaluating
	}
	return dep.value
}

// Evaluate runs one batch: it dirties everything downstream of changed
// nodes, then recomputes each dirty node at most once in topological
// order. A node whose dependencies failed (or were skipped) is marked
// Skipped and its function is not called. Structural changes staged
// during the batch are applied afterwards; any error they produce (such
// as a rejected cycle) is returned.
func (g *Graph) Evaluate() error {
	g.evaluating = true

	dirty := make(map[*Node]bool)
	var queue []*Node
	for _, n := range g.nodes {
		if n.state == Dirty {
			dirty[n] = true
			queue = append(queue, n)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for d := range cur.dependents {
			if !dirty[d] {
				dirty[d] = true
				d.state = Dirty
				queue = append(queue, d)
			}
		}
	}

	order := topoOrder(g.nodes, dirty)

	for _, n := range order {
		failed := false
		for dep := range n.deps {
			if dep.err != nil || dep.state == Skipped {
				failed = true
				break
			}
		}
		if failed {
			n.state = Skipped
			continue
		}
		if n.fn != nil {
			n.value, n.err = n.fn(&Context{node: n})
		}
		n.state = Clean
	}

	g.evaluating = false

	var errs []error
	for _, apply := range g.staged {
		if err := apply(); err != nil {
			errs = append(errs, err)
		}
	}
	g.staged = nil
	return errors.Join(errs...)
}

// topoOrder returns the dirty nodes in topological order (dependencies
// first), deterministic by node id. Nodes trapped in a cycle (only
// possible without WithCycleRejection) are appended in id order.
func topoOrder(nodes []*Node, dirty map[*Node]bool) []*Node {
	indeg := make(map[*Node]int, len(dirty))
	for n := range dirty {
		for dep := range n.deps {
			if dirty[dep] {
				indeg[n]++
			}
		}
	}
	var ready []*Node
	for _, n := range nodes {
		if dirty[n] && indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	var order []*Node
	for len(ready) > 0 {
		cur := ready[0]
		ready = ready[1:]
		order = append(order, cur)
		for d := range cur.dependents {
			if !dirty[d] {
				continue
			}
			indeg[d]--
			if indeg[d] == 0 {
				ready = append(ready, d)
			}
		}
	}
	if len(order) < len(dirty) {
		for _, n := range nodes {
			if dirty[n] && indeg[n] > 0 {
				order = append(order, n)
			}
		}
	}
	return order
}
