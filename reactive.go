// Package reactive implements a small reactive computation graph.
//
// Nodes recompute when their inputs change. A call to Evaluate
// propagates changes through one batch in topological order, so every
// dirty node is computed at most once per batch. Configuring a
// dependency that would close a cycle is rejected. The graph must not
// be structurally modified while a batch is running; dependencies
// added between batches take effect on the next Evaluate.
package reactive

import "errors"

var (
	// ErrCycle is returned when a dependency would close a cycle.
	ErrCycle = errors.New("reactive: dependency would create a cycle")
	// ErrGraphBusy is returned when the graph is modified during evaluation.
	ErrGraphBusy = errors.New("reactive: cannot modify graph during evaluation")
	// ErrDuplicateDep is returned when the dependency already exists.
	ErrDuplicateDep = errors.New("reactive: duplicate dependency")
	// ErrUpstreamFailed marks nodes skipped because a dependency failed.
	ErrUpstreamFailed = errors.New("reactive: upstream dependency failed")
)

// Compute derives a node's value from its dependencies' values, in the
// order the dependencies were added.
type Compute func(inputs []interface{}) (interface{}, error)

// Node is a single cell in the reactive graph.
type Node struct {
	name    string
	compute Compute

	deps       []*Node
	dependents []*Node

	value   interface{}
	err     error
	dirty   bool
	skipped bool

	// Calls counts how many times the compute function ran.
	Calls int
}

// Name returns the node's name.
func (n *Node) Name() string { return n.name }

// Value returns the last computed (or set) value.
func (n *Node) Value() interface{} { return n.value }

// Err returns the failure recorded during the last evaluation.
func (n *Node) Err() error { return n.err }

// Skipped reports whether the node was not computed because an
// upstream dependency failed.
func (n *Node) Skipped() bool { return n.skipped }

// Graph is a set of nodes wired by dependencies.
type Graph struct {
	nodes      []*Node
	evaluating bool
}

// NewGraph returns an empty graph.
func NewGraph() *Graph { return &Graph{} }

// Node adds a node. A nil compute makes it a source node fed via Set.
func (g *Graph) Node(name string, compute Compute) *Node {
	n := &Node{name: name, compute: compute, dirty: true}
	g.nodes = append(g.nodes, n)
	return n
}

// AddDep makes n depend on dep. It is rejected if it would close a
// cycle or if a batch is running. The new edge only takes effect on the
// next Evaluate.
func (g *Graph) AddDep(n, dep *Node) error {
	if g.evaluating {
		return ErrGraphBusy
	}
	if n == dep || reachable(dep, n) {
		return ErrCycle
	}
	for _, d := range n.deps {
		if d == dep {
			return ErrDuplicateDep
		}
	}
	n.deps = append(n.deps, dep)
	dep.dependents = append(dep.dependents, n)
	n.dirty = true
	return nil
}

// reachable reports whether to is reachable from from by following deps.
func reachable(from, to *Node) bool {
	seen := map[*Node]bool{}
	stack := []*Node{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == to {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, cur.deps...)
	}
	return false
}

// Set assigns a source value and marks dependents dirty for the next
// batch. It is rejected while a batch is running.
func (g *Graph) Set(n *Node, v interface{}) error {
	if g.evaluating {
		return ErrGraphBusy
	}
	n.value = v
	n.err = nil
	n.skipped = false
	n.dirty = false
	g.markDownstreamDirty(n)
	return nil
}

func (g *Graph) markDownstreamDirty(n *Node) {
	for _, m := range n.dependents {
		if !m.dirty {
			m.dirty = true
			g.markDownstreamDirty(m)
		}
	}
}

// Evaluate recomputes every dirty node in topological order; each node
// runs at most once per batch. If a compute fails, the error is
// recorded on the node and its dirty downstream nodes are marked
// skipped instead of being computed.
func (g *Graph) Evaluate() {
	g.evaluating = true
	defer func() { g.evaluating = false }()

	dirty := map[*Node]bool{}
	indeg := map[*Node]int{}
	for _, n := range g.nodes {
		if n.dirty {
			dirty[n] = true
		}
	}
	for n := range dirty {
		for _, d := range n.deps {
			if dirty[d] {
				indeg[n]++
			}
		}
	}
	// Kahn's algorithm; seed in registration order for determinism.
	var queue []*Node
	for _, n := range g.nodes {
		if dirty[n] && indeg[n] == 0 {
			queue = append(queue, n)
		}
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		g.computeNode(n)
		n.dirty = false
		for _, m := range n.dependents {
			if dirty[m] {
				indeg[m]--
				if indeg[m] == 0 {
					queue = append(queue, m)
				}
			}
		}
	}
}

func (g *Graph) computeNode(n *Node) {
	if n.compute == nil {
		return // source node; value arrives via Set
	}
	inputs := make([]interface{}, len(n.deps))
	for i, d := range n.deps {
		if d.err != nil || d.skipped {
			n.skipped = true
			n.err = ErrUpstreamFailed
			return
		}
		inputs[i] = d.value
	}
	n.Calls++
	v, err := n.compute(inputs)
	if err != nil {
		n.err = err
		n.skipped = false
		return
	}
	n.err = nil
	n.skipped = false
	n.value = v
}
