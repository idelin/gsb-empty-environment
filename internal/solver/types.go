// Package solver finds the cheapest set of authorized edges to cut so that
// a dependency graph becomes acyclic, and emits a topological build plan.
package solver

import "fmt"

// MaxCuttableEdges is the hard limit on authorized (cuttable) edges.
const MaxCuttableEdges = 25

// Edge is a single directed dependency From -> To.
// Only edges with Cuttable == true may be cut, and cutting such an edge
// requires exactly its designated Artifact; artifacts may not be
// substituted across edges.
type Edge struct {
	ID       int    `json:"id"`
	From     string `json:"from"`
	To       string `json:"to"`
	Cuttable bool   `json:"cuttable"`
	Cost     int64  `json:"cost"`
	Artifact string `json:"artifact"`
}

// Graph is the input dependency graph.
type Graph struct {
	Nodes []string `json:"nodes"`
	Edges []Edge   `json:"edges"`
}

// Cut describes one severed edge and the bootstrap artifact spent on it.
type Cut struct {
	EdgeID   int    `json:"edgeId"`
	From     string `json:"from"`
	To       string `json:"to"`
	Artifact string `json:"artifact"`
	Cost     int64  `json:"cost"`
}

// Plan is the solver result: the cheapest cut set plus a topological
// ordering of the remaining acyclic graph.
type Plan struct {
	Feasible  bool     `json:"feasible"`
	TotalCost int64    `json:"totalCost"`
	Cuts      []Cut    `json:"cuts"`
	TopoOrder []string `json:"topoOrder"`
	Reason    string   `json:"reason,omitempty"`
}

// Validate checks structural rules of the input graph.
func (g *Graph) Validate() error {
	if len(g.Edges) == 0 && len(g.Nodes) == 0 {
		return fmt.Errorf("graph is empty: provide nodes and/or edges")
	}
	seen := make(map[string]bool)
	for _, n := range g.Nodes {
		if n == "" {
			return fmt.Errorf("node names must not be empty")
		}
		if seen[n] {
			return fmt.Errorf("duplicate node %q", n)
		}
		seen[n] = true
	}
	cuttable := 0
	for i := range g.Edges {
		e := &g.Edges[i]
		e.ID = i
		if e.From == "" || e.To == "" {
			return fmt.Errorf("edge %d: from/to must not be empty", i)
		}
		if e.Cuttable {
			cuttable++
			if e.Cost < 0 {
				return fmt.Errorf("edge %d (%s -> %s): cost must be >= 0", i, e.From, e.To)
			}
			if e.Artifact == "" {
				return fmt.Errorf("edge %d (%s -> %s): cuttable edge requires a designated artifact", i, e.From, e.To)
			}
		} else {
			if e.Artifact != "" || e.Cost != 0 {
				return fmt.Errorf("edge %d (%s -> %s): unauthorized edge must not declare cost/artifact; it can never be cut", i, e.From, e.To)
			}
		}
	}
	if cuttable > MaxCuttableEdges {
		return fmt.Errorf("too many cuttable edges: %d > %d", cuttable, MaxCuttableEdges)
	}
	return nil
}
