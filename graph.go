package iac

import (
	"fmt"
	"sort"
	"strings"
)

// NodeKind identifies the operation represented by a graph node.
type NodeKind string

const (
	NodeCreate  NodeKind = "create"
	NodeUpdate  NodeKind = "update"
	NodeDestroy NodeKind = "destroy"
)

// Node is one executable operation. Replacement resources are split into
// separate create and destroy nodes.
type Node struct {
	ID      string     `json:"id"`
	Address string     `json:"address"`
	Kind    NodeKind   `json:"kind"`
	Action  ActionKind `json:"action"`
}

// Edge is a directed "From must finish before To" dependency.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph is a directed action graph.
type Graph struct {
	Nodes []*Node `json:"nodes"`
	Edges []Edge  `json:"edges"`
}

func NewGraph() *Graph {
	return &Graph{}
}

func (g *Graph) AddNode(n *Node) error {
	if n == nil || n.ID == "" {
		return &PlanError{Code: ErrorInvalidGraph, Message: "node ID must not be empty"}
	}
	if g.Node(n.ID) != nil {
		return &PlanError{Code: ErrorInvalidGraph, Message: fmt.Sprintf("duplicate node %q", n.ID)}
	}
	g.Nodes = append(g.Nodes, n)
	return nil
}

func (g *Graph) AddEdge(from, to string) error {
	if g.Node(from) == nil || g.Node(to) == nil {
		return &PlanError{Code: ErrorInvalidGraph, Message: fmt.Sprintf("edge %q -> %q references an unknown node", from, to)}
	}
	if g.HasEdge(from, to) {
		return nil
	}
	g.Edges = append(g.Edges, Edge{From: from, To: to})
	return nil
}

func (g *Graph) Node(id string) *Node {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func (g *Graph) HasNode(id string) bool { return g.Node(id) != nil }

func (g *Graph) HasEdge(from, to string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// NodeIDs returns node IDs in deterministic order.
func (g *Graph) NodeIDs() []string {
	ids := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		ids = append(ids, n.ID)
	}
	sort.Strings(ids)
	return ids
}

// Predecessors returns the sorted direct predecessors of id.
func (g *Graph) Predecessors(id string) []string {
	set := make(map[string]bool)
	for _, e := range g.Edges {
		if e.To == id {
			set[e.From] = true
		}
	}
	return sortedKeys(set)
}

// Successors returns the sorted direct successors of id.
func (g *Graph) Successors(id string) []string {
	set := make(map[string]bool)
	for _, e := range g.Edges {
		if e.From == id {
			set[e.To] = true
		}
	}
	return sortedKeys(set)
}

func (g *Graph) adjacency() map[string][]string {
	adj := make(map[string][]string, len(g.Nodes))
	for _, n := range g.Nodes {
		adj[n.ID] = nil
	}
	for _, e := range g.Edges {
		if _, ok := adj[e.From]; !ok {
			adj[e.From] = nil
		}
		adj[e.From] = append(adj[e.From], e.To)
	}
	for k := range adj {
		sort.Strings(adj[k])
		adj[k] = dedupeStrings(adj[k])
	}
	return adj
}

// Validate checks structural integrity and acyclicity.
func (g *Graph) Validate() error {
	seen := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		if n == nil || n.ID == "" {
			return &PlanError{Code: ErrorInvalidGraph, Message: "node ID must not be empty"}
		}
		if seen[n.ID] {
			return &PlanError{Code: ErrorInvalidGraph, Message: fmt.Sprintf("duplicate node %q", n.ID)}
		}
		seen[n.ID] = true
	}
	for _, e := range g.Edges {
		if !seen[e.From] || !seen[e.To] {
			return &PlanError{Code: ErrorInvalidGraph, Message: fmt.Sprintf("edge %q -> %q references an unknown node", e.From, e.To)}
		}
	}
	if cycle := g.FindMinimalCycle(); len(cycle) > 0 {
		return executionCycleError(cycle)
	}
	return nil
}

// FindMinimalCycle returns the shortest directed cycle as a closed path
// (the first node is repeated at the end), or nil when the graph is acyclic.
// Ties are broken lexicographically.
func (g *Graph) FindMinimalCycle() []string {
	adj := g.adjacency()
	ids := make([]string, 0, len(adj))
	for id := range adj {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var best []string
	for _, start := range ids {
		type item struct {
			node string
			path []string
		}
		queue := []item{{node: start, path: []string{start}}}
		visited := map[string]bool{start: true}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, next := range adj[cur.node] {
				if next == start {
					cycle := append(append([]string(nil), cur.path...), start)
					if best == nil || closedPathLess(cycle, best) {
						best = cycle
					}
					continue
				}
				if visited[next] {
					continue
				}
				visited[next] = true
				queue = append(queue, item{
					node: next,
					path: append(append([]string(nil), cur.path...), next),
				})
			}
		}
	}
	return best
}

func closedPathLess(a, b []string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// TopologicalWaves returns maximal-concurrency waves: every node whose
// predecessors are already complete is placed in the same earliest wave.
func (g *Graph) TopologicalWaves() ([][]string, error) {
	if cycle := g.FindMinimalCycle(); len(cycle) > 0 {
		return nil, executionCycleError(cycle)
	}

	adj := g.adjacency()
	indeg := make(map[string]int, len(g.Nodes))
	for _, n := range g.Nodes {
		indeg[n.ID] = 0
	}
	for from, successors := range adj {
		if _, ok := indeg[from]; !ok {
			continue
		}
		for _, to := range successors {
			if _, ok := indeg[to]; ok {
				indeg[to]++
			}
		}
	}
	zero := make([]string, 0)
	for id, d := range indeg {
		if d == 0 {
			zero = append(zero, id)
		}
	}
	sort.Strings(zero)

	var waves [][]string
	processed := 0
	for len(zero) > 0 {
		wave := append([]string(nil), zero...)
		waves = append(waves, wave)
		processed += len(wave)

		next := make(map[string]bool)
		for _, id := range wave {
			for _, succ := range adj[id] {
				if _, ok := indeg[succ]; !ok {
					continue
				}
				indeg[succ]--
				if indeg[succ] == 0 {
					next[succ] = true
				}
			}
		}
		zero = sortedKeys(next)
	}
	if processed != len(g.Nodes) {
		if cycle := g.FindMinimalCycle(); len(cycle) > 0 {
			return nil, executionCycleError(cycle)
		}
		return nil, &PlanError{Code: ErrorInvalidGraph, Message: "graph could not be topologically ordered"}
	}
	return waves, nil
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k, ok := range set {
		if ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func dedupeStrings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// String renders the graph in a compact deterministic form.
func (g *Graph) String() string {
	var b strings.Builder
	for _, id := range g.NodeIDs() {
		fmt.Fprintf(&b, "%s\n", id)
	}
	edges := append([]Edge(nil), g.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	for _, e := range edges {
		fmt.Fprintf(&b, "  %s -> %s\n", e.From, e.To)
	}
	return b.String()
}
