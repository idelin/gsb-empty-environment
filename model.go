package main

import (
	"fmt"
	"sort"
)

// Version is a configuration version together with the set of features
// it provides.
type Version struct {
	ID       string   `json:"id"`
	Features []string `json:"features"`
}

// Edge is a rollback edge: rolling From back to its dependency To.
// It consumes Cost units of the shared maintenance budget and undoes
// the features listed in Undoes.
type Edge struct {
	From   string   `json:"from"`
	To     string   `json:"to"`
	Cost   int      `json:"cost"`
	Undoes []string `json:"undoes"`
}

// Node is a deployed node currently running Version. Forbidden lists
// versions the node may never pass through because of irreversible
// operations already performed on it.
type Node struct {
	ID        string   `json:"id"`
	Version   string   `json:"version"`
	Forbidden []string `json:"forbidden"`
}

// CompatRule restricts the allowed version combinations between two
// nodes. When a rule exists for a node pair, only the listed pairs are
// compatible; pairs without a rule are unconstrained.
type CompatRule struct {
	NodeA        string      `json:"nodeA"`
	NodeB        string      `json:"nodeB"`
	AllowedPairs [][2]string `json:"allowedPairs"`
}

// Scenario is the complete problem input.
type Scenario struct {
	Versions []Version    `json:"versions"`
	Edges    []Edge       `json:"edges"`
	Nodes    []Node       `json:"nodes"`
	Budget   int          `json:"budget"`
	Compat   []CompatRule `json:"compatibility"`
}

// Graph is the validated, indexed form of a Scenario.
type Graph struct {
	scenario  *Scenario
	features  map[string][]string
	adj       map[string][]Edge
	nodes     []Node
	nodeIndex map[string]int
	forbidden []map[string]bool
	// compat maps an ordered node index pair (i<j) to the allowed
	// version pairs for that pair of nodes.
	compat map[[2]int]map[[2]string]bool
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// BuildGraph validates the scenario and indexes it for solving.
func BuildGraph(sc *Scenario) (*Graph, error) {
	g := &Graph{
		scenario:  sc,
		features:  map[string][]string{},
		adj:       map[string][]Edge{},
		nodeIndex: map[string]int{},
		compat:    map[[2]int]map[[2]string]bool{},
	}
	for _, v := range sc.Versions {
		if v.ID == "" {
			return nil, fmt.Errorf("version with empty id")
		}
		if _, dup := g.features[v.ID]; dup {
			return nil, fmt.Errorf("duplicate version %q", v.ID)
		}
		g.features[v.ID] = append([]string(nil), v.Features...)
	}
	for _, e := range sc.Edges {
		from, ok := g.features[e.From]
		if !ok {
			return nil, fmt.Errorf("edge from unknown version %q", e.From)
		}
		to, ok := g.features[e.To]
		if !ok {
			return nil, fmt.Errorf("edge to unknown version %q", e.To)
		}
		if e.From == e.To {
			return nil, fmt.Errorf("self-loop rollback edge at %q", e.From)
		}
		if e.Cost < 0 {
			return nil, fmt.Errorf("edge %s->%s has negative cost", e.From, e.To)
		}
		for _, f := range e.Undoes {
			if !contains(from, f) {
				return nil, fmt.Errorf("edge %s->%s undoes feature %q not present in %s", e.From, e.To, f, e.From)
			}
			if contains(to, f) {
				return nil, fmt.Errorf("edge %s->%s undoes feature %q still present in %s", e.From, e.To, f, e.To)
			}
		}
		g.adj[e.From] = append(g.adj[e.From], e)
	}
	if err := g.checkAcyclic(); err != nil {
		return nil, err
	}
	for i, n := range sc.Nodes {
		if _, ok := g.features[n.Version]; !ok {
			return nil, fmt.Errorf("node %q pinned to unknown version %q", n.ID, n.Version)
		}
		if _, dup := g.nodeIndex[n.ID]; dup {
			return nil, fmt.Errorf("duplicate node id %q", n.ID)
		}
		g.nodeIndex[n.ID] = i
		fset := map[string]bool{}
		for _, fv := range n.Forbidden {
			if _, ok := g.features[fv]; !ok {
				return nil, fmt.Errorf("node %q forbids unknown version %q", n.ID, fv)
			}
			fset[fv] = true
		}
		if fset[n.Version] {
			return nil, fmt.Errorf("node %q currently sits on forbidden version %q", n.ID, n.Version)
		}
		g.forbidden = append(g.forbidden, fset)
		g.nodes = append(g.nodes, n)
	}
	if len(g.nodes) > 5 {
		return nil, fmt.Errorf("at most 5 nodes supported, got %d", len(g.nodes))
	}
	for _, r := range sc.Compat {
		ia, ok := g.nodeIndex[r.NodeA]
		if !ok {
			return nil, fmt.Errorf("compat rule references unknown node %q", r.NodeA)
		}
		ib, ok := g.nodeIndex[r.NodeB]
		if !ok {
			return nil, fmt.Errorf("compat rule references unknown node %q", r.NodeB)
		}
		if ia == ib {
			return nil, fmt.Errorf("compat rule between node %q and itself", r.NodeA)
		}
		pairs := map[[2]string]bool{}
		for _, p := range r.AllowedPairs {
			if _, ok := g.features[p[0]]; !ok {
				return nil, fmt.Errorf("compat rule references unknown version %q", p[0])
			}
			if _, ok := g.features[p[1]]; !ok {
				return nil, fmt.Errorf("compat rule references unknown version %q", p[1])
			}
			pairs[p] = true
		}
		key := [2]int{ia, ib}
		if ia > ib {
			key = [2]int{ib, ia}
			swapped := map[[2]string]bool{}
			for p := range pairs {
				swapped[[2]string{p[1], p[0]}] = true
			}
			pairs = swapped
		}
		if _, dup := g.compat[key]; dup {
			return nil, fmt.Errorf("duplicate compat rule for nodes %q and %q", r.NodeA, r.NodeB)
		}
		g.compat[key] = pairs
	}
	return g, nil
}

// checkAcyclic verifies the rollback edges form a DAG (Kahn's algorithm).
func (g *Graph) checkAcyclic() error {
	indeg := map[string]int{}
	for id := range g.features {
		indeg[id] = 0
	}
	for _, edges := range g.adj {
		for _, e := range edges {
			indeg[e.To]++
		}
	}
	var queue []string
	for id, d := range indeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	seen := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		seen++
		for _, e := range g.adj[id] {
			indeg[e.To]--
			if indeg[e.To] == 0 {
				queue = append(queue, e.To)
			}
		}
	}
	if seen != len(g.features) {
		return fmt.Errorf("rollback edges contain a cycle; versions must form a DAG")
	}
	return nil
}

// stateCompatible reports whether a joint version assignment satisfies
// every compatibility rule. state[i] is the version of node i.
func (g *Graph) stateCompatible(state []string) bool {
	for key, allowed := range g.compat {
		i, j := key[0], key[1]
		if !allowed[[2]string{state[i], state[j]}] {
			return false
		}
	}
	return true
}

// sortedFeatures returns the feature set of a version, sorted.
func (g *Graph) sortedFeatures(version string) []string {
	f := append([]string(nil), g.features[version]...)
	sort.Strings(f)
	return f
}
