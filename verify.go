package main

import "fmt"

// findEdge scans the flat edge list (not the solver's adjacency) for an
// edge from -> to and returns its cost.
func findEdge(g *Graph, from, to string) (int, bool) {
	found := false
	cost := 0
	for _, e := range g.Edges {
		if e.From == from && e.To == to && (!found || e.Cost < cost) {
			found, cost = true, e.Cost
		}
	}
	return cost, found
}

// dfsMinSteps independently recomputes the minimum number of rollback
// steps from start to target by exhaustive DFS over simple paths.
func dfsMinSteps(g *Graph, start, target string) (int, bool) {
	if start == target {
		return 0, true
	}
	best := -1
	visited := map[string]bool{start: true}
	var walk func(cur string, depth int)
	walk = func(cur string, depth int) {
		if best >= 0 && depth >= best {
			return
		}
		for _, e := range g.Edges {
			if e.From != cur || visited[e.To] {
				continue
			}
			if e.To == target {
				if best < 0 || depth+1 < best {
					best = depth + 1
				}
				continue
			}
			visited[e.To] = true
			walk(e.To, depth+1)
			delete(visited, e.To)
		}
	}
	walk(start, 0)
	if best < 0 {
		return 0, false
	}
	return best, true
}

// VerifyCandidate independently re-checks a ranked candidate: every path
// starts at the node's current version, ends at the target, uses only real
// rollback edges, and the claimed ranking keys are correct; it also
// re-derives the optimum by exhaustive DFS to confirm no candidate the
// solver reported is beaten by one it did not.
func VerifyCandidate(p *Problem, c *Candidate) error {
	v, ok := p.Graph.Versions[c.Target]
	if !ok {
		return fmt.Errorf("target %q is not a known version", c.Target)
	}
	if c.Features != len(v.Features) {
		return fmt.Errorf("feature count mismatch: claimed %d, actual %d", c.Features, len(v.Features))
	}
	if len(c.Paths) != len(p.Nodes) {
		return fmt.Errorf("expected %d paths, got %d", len(p.Nodes), len(c.Paths))
	}
	total := 0
	for i, n := range p.Nodes {
		path := c.Paths[i]
		if len(path) == 0 || path[0] != n.Current {
			return fmt.Errorf("node %s: path does not start at %s", n.Name, n.Current)
		}
		if path[len(path)-1] != c.Target {
			return fmt.Errorf("node %s: path does not end at %s", n.Name, c.Target)
		}
		for k := 1; k < len(path); k++ {
			if _, ok := findEdge(p.Graph, path[k-1], path[k]); !ok {
				return fmt.Errorf("node %s: no rollback edge %s -> %s", n.Name, path[k-1], path[k])
			}
		}
		total += len(path) - 1
	}
	if total != c.TotalSteps {
		return fmt.Errorf("total steps mismatch: claimed %d, actual %d", c.TotalSteps, total)
	}
	// Independent optimality check: recompute every candidate's keys with
	// exhaustive DFS and confirm none ranks strictly better.
	for id, ver := range p.Graph.Versions {
		steps, feasible := 0, true
		for _, n := range p.Nodes {
			d, ok := dfsMinSteps(p.Graph, n.Current, id)
			if !ok {
				feasible = false
				break
			}
			steps += d
		}
		if !feasible {
			continue
		}
		f := len(ver.Features)
		if f > c.Features || (f == c.Features && steps < c.TotalSteps) {
			return fmt.Errorf("candidate %s (features=%d, steps=%d) beats reported %s (features=%d, steps=%d)",
				id, f, steps, c.Target, c.Features, c.TotalSteps)
		}
	}
	return nil
}

// VerifyPlan independently replays a trajectory from the initial joint
// state and checks every constraint at every step: node execution order,
// edge existence and cost, shared budget, forbidden versions, pairwise
// compatibility, and arrival at the common target.
func VerifyPlan(p *Problem, plan *Plan) error {
	versions := p.initialState()
	if !p.stateCompatible(versions) {
		return fmt.Errorf("initial state violates compatibility")
	}
	cost := 0
	for step, tr := range plan.Transitions {
		want := activeNode(versions, plan.Target)
		if want == -1 {
			return fmt.Errorf("step %d: all nodes already at target", step)
		}
		if tr.Node != want {
			return fmt.Errorf("step %d: node order violated, expected node %d, got %d", step, want, tr.Node)
		}
		e := tr.Edge
		if e.From != versions[tr.Node] {
			return fmt.Errorf("step %d: edge starts at %s but node is at %s", step, e.From, versions[tr.Node])
		}
		edgeCost, ok := findEdge(p.Graph, e.From, e.To)
		if !ok {
			return fmt.Errorf("step %d: no rollback edge %s -> %s", step, e.From, e.To)
		}
		if edgeCost != e.Cost {
			return fmt.Errorf("step %d: edge cost mismatch: claimed %d, actual %d", step, e.Cost, edgeCost)
		}
		if p.Nodes[tr.Node].Forbidden[e.To] {
			return fmt.Errorf("step %d: node %s enters forbidden version %s", step, p.Nodes[tr.Node].Name, e.To)
		}
		cost += e.Cost
		if cost > p.Budget {
			return fmt.Errorf("step %d: budget exceeded (%d > %d)", step, cost, p.Budget)
		}
		versions[tr.Node] = e.To
		if !p.stateCompatible(versions) {
			return fmt.Errorf("step %d: compatibility violated after node %s moved to %s",
				step, p.Nodes[tr.Node].Name, e.To)
		}
		if len(tr.Versions) != len(versions) {
			return fmt.Errorf("step %d: recorded state has wrong arity", step)
		}
		for i := range versions {
			if tr.Versions[i] != versions[i] {
				return fmt.Errorf("step %d: recorded state %v does not match replayed %v", step, tr.Versions, versions)
			}
		}
	}
	if cost != plan.TotalCost {
		return fmt.Errorf("total cost mismatch: claimed %d, actual %d", plan.TotalCost, cost)
	}
	for i, v := range versions {
		if v != plan.Target {
			return fmt.Errorf("node %s ended at %s, not target %s", p.Nodes[i].Name, v, plan.Target)
		}
	}
	return nil
}
