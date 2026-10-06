// Package compute provides a dependency-graph based computation library.
//
// A query (a Node) may share underlying sub-computations with other
// queries. Concurrent subscribers of the same node join a single running
// "generation" of that computation; each subscriber only holds a
// reference. A sub-computation is cancelled only when its reference count
// drops to zero. Failures of a shared sub-computation propagate to every
// still-subscribed consumer, and a subscription that arrives after a
// generation has finished starts a fresh generation.
package compute

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Func is the body of a computation. It receives the results of the
// node's dependencies (aligned with the order of Node.Deps) and must
// honor ctx cancellation.
type Func func(ctx context.Context, deps []Result) (interface{}, error)

// Result is the outcome of one dependency.
type Result struct {
	Node *Node
	Val  interface{}
	Err  error
}

// Graph is a set of nodes connected by dependency edges. Edges are
// validated on insertion so the graph always stays acyclic.
type Graph struct {
	mu    sync.RWMutex
	nodes map[string]*Node
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{nodes: make(map[string]*Node)}
}

// Node is a computation vertex. Deps returns the node's current
// dependencies in edge-insertion order.
type Node struct {
	name  string
	graph *Graph

	mu   sync.RWMutex
	deps []*Node
	fn   Func
}

// Name returns the node's name.
func (n *Node) Name() string { return n.name }

// Node creates a new named vertex. It fails if the name is taken.
func (g *Graph) Node(name string) (*Node, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[name]; ok {
		return nil, fmt.Errorf("compute: node %q already exists", name)
	}
	n := &Node{name: name, graph: g}
	g.nodes[name] = n
	return n, nil
}

// Get returns the node registered under name, or nil.
func (g *Graph) Get(name string) *Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.nodes[name]
}

// AddDependency records that from depends on to. It rejects self-loops
// and any edge that would close a dependency cycle.
func (g *Graph) AddDependency(from, to *Node) error {
	if from == nil || to == nil {
		return errors.New("compute: nil node")
	}
	if from.graph != g || to.graph != g {
		return errors.New("compute: nodes belong to a different graph")
	}
	if from == to {
		return fmt.Errorf("compute: self dependency on %q would create a cycle", from.name)
	}
	// Adding from->to creates a cycle iff `from` is already reachable
	// from `to` following existing dependency edges.
	if to.reaches(from) {
		return fmt.Errorf("compute: dependency %q -> %q would create a cycle", from.name, to.name)
	}
	from.mu.Lock()
	from.deps = append(from.deps, to)
	from.mu.Unlock()
	return nil
}

// reaches reports whether target is reachable from n via dependency
// edges (n itself excluded).
func (n *Node) reaches(target *Node) bool {
	seen := map[*Node]bool{n: true}
	stack := []*Node{n}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		cur.mu.RLock()
		deps := append([]*Node(nil), cur.deps...)
		cur.mu.RUnlock()
		for _, d := range deps {
			if d == target {
				return true
			}
			if !seen[d] {
				seen[d] = true
				stack = append(stack, d)
			}
		}
	}
	return false
}

// SetFunc installs the computation body. It may be called at most once
// and before the node is first subscribed.
func (n *Node) SetFunc(fn Func) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fn = fn
}

// Deps returns a snapshot of the node's dependencies.
func (n *Node) Deps() []*Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return append([]*Node(nil), n.deps...)
}

// Func returns the installed computation body, or nil.
func (n *Node) Func() Func {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.fn
}
