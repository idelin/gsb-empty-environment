package main

import "sort"

// Version is a configuration version carrying a set of features.
type Version struct {
	ID       string
	Features map[string]bool
}

// Edge is a rollback edge: From can be rolled back to To.
// Rolling back along the edge undoes the features present in From but
// absent in To, and consumes Cost units of the shared maintenance budget.
type Edge struct {
	From string
	To   string
	Cost int
}

// Graph is a finite DAG of versions connected by rollback edges.
type Graph struct {
	Versions map[string]*Version
	Edges    []Edge            // flat list, used by independent verification
	Out      map[string][]Edge // adjacency, built from Edges
}

// NewGraph builds a Graph and sorts adjacency lists for determinism.
func NewGraph(versions []*Version, edges []Edge) *Graph {
	g := &Graph{
		Versions: make(map[string]*Version),
		Out:      make(map[string][]Edge),
	}
	for _, v := range versions {
		g.Versions[v.ID] = v
	}
	g.Edges = append(g.Edges, edges...)
	for _, e := range edges {
		g.Out[e.From] = append(g.Out[e.From], e)
	}
	for id := range g.Out {
		es := g.Out[id]
		sort.Slice(es, func(i, j int) bool {
			if es[i].To != es[j].To {
				return es[i].To < es[j].To
			}
			return es[i].Cost < es[j].Cost
		})
	}
	return g
}

// UndoneFeatures returns the features revoked by rolling back along e.
func (g *Graph) UndoneFeatures(e Edge) []string {
	from := g.Versions[e.From]
	to := g.Versions[e.To]
	var out []string
	for f := range from.Features {
		if !to.Features[f] {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// Node is a cluster node pinned at a current version. Forbidden lists
// versions the node may never pass through due to irreversible operations.
type Node struct {
	Name      string
	Current   string
	Forbidden map[string]bool
}

// Problem bundles the graph, nodes and global constraints.
// Compatible(a, b) reports whether versions a and b may coexist on two
// different nodes at the same time; it must be symmetric.
type Problem struct {
	Graph      *Graph
	Nodes      []*Node
	Budget     int
	Compatible func(a, b string) bool
}

// stateCompatible checks the compatibility relation over every node pair.
func (p *Problem) stateCompatible(versions []string) bool {
	if p.Compatible == nil {
		return true
	}
	for i := 0; i < len(versions); i++ {
		for j := i + 1; j < len(versions); j++ {
			if !p.Compatible(versions[i], versions[j]) {
				return false
			}
		}
	}
	return true
}

func (p *Problem) initialState() []string {
	s := make([]string, len(p.Nodes))
	for i, n := range p.Nodes {
		s[i] = n.Current
	}
	return s
}
