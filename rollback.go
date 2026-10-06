package main

import (
	"fmt"
	"sort"
)

// PathInfo describes the best way to roll one node back to a version.
type PathInfo struct {
	Steps int
	Cost  int
	Path  []string
}

// NodePlan is the rollback path assigned to one node.
type NodePlan struct {
	NodeID string
	Path   []string
	Steps  int
	Cost   int
}

// Plan is the result of Part 1: a common rollback target plus the
// per-node paths leading to it.
type Plan struct {
	Target           string
	RetainedFeatures []string
	NodePlans        []NodePlan
	TotalSteps       int
	TotalCost        int
}

// shortestPaths computes, for every version reachable from start, the
// best path ordered by (steps, cost). The start version itself is
// reachable with zero steps.
func shortestPaths(g *Graph, start string) map[string]PathInfo {
	best := map[string]PathInfo{start: {Steps: 0, Cost: 0, Path: []string{start}}}
	prev := map[string][2]string{}
	pq := &priorityQueue{{key: start}}
	for pq.Len() > 0 {
		cur := popPQ(pq)
		curBest := best[cur.key]
		if cur.steps != curBest.Steps || cur.cost != curBest.Cost {
			continue
		}
		for _, e := range g.adj[cur.key] {
			cand := PathInfo{Steps: cur.steps + 1, Cost: cur.cost + e.Cost}
			old, seen := best[e.To]
			if seen && (old.Steps < cand.Steps || (old.Steps == cand.Steps && old.Cost <= cand.Cost)) {
				continue
			}
			best[e.To] = cand
			prev[e.To] = [2]string{cur.key, e.To}
			pushPQ(pq, pqItem{key: e.To, steps: cand.Steps, cost: cand.Cost})
		}
	}
	for id := range best {
		if id == start {
			continue
		}
		var rev []string
		for at := id; at != start; {
			rev = append(rev, at)
			at = prev[at][0]
		}
		rev = append(rev, start)
		path := make([]string, len(rev))
		for i, v := range rev {
			path[len(rev)-1-i] = v
		}
		info := best[id]
		info.Path = path
		best[id] = info
	}
	return best
}

// SelectCommonTarget enumerates every version reachable from all nodes
// and picks the candidate that first maximizes the number of retained
// features and then minimizes the total rollback steps across nodes.
func SelectCommonTarget(g *Graph) (*Plan, error) {
	if len(g.nodes) == 0 {
		return nil, fmt.Errorf("no nodes in scenario")
	}
	reach := make([]map[string]PathInfo, len(g.nodes))
	for i, n := range g.nodes {
		reach[i] = shortestPaths(g, n.Version)
	}
	var candidates []string
	for id := range g.features {
		common := true
		for i := range g.nodes {
			if _, ok := reach[i][id]; !ok {
				common = false
				break
			}
		}
		if common {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no common rollback version reachable from all nodes")
	}
	sort.Strings(candidates)
	bestScore := -1
	bestSteps, bestCost := 0, 0
	chosen := ""
	for _, id := range candidates {
		score := len(g.features[id])
		steps, cost := 0, 0
		for i := range g.nodes {
			steps += reach[i][id].Steps
			cost += reach[i][id].Cost
		}
		better := chosen == "" ||
			score > bestScore ||
			(score == bestScore && steps < bestSteps) ||
			(score == bestScore && steps == bestSteps && cost < bestCost)
		if better {
			chosen, bestScore, bestSteps, bestCost = id, score, steps, cost
		}
	}
	plan := &Plan{
		Target:           chosen,
		RetainedFeatures: g.sortedFeatures(chosen),
		TotalSteps:       bestSteps,
		TotalCost:        bestCost,
	}
	for i, n := range g.nodes {
		info := reach[i][chosen]
		plan.NodePlans = append(plan.NodePlans, NodePlan{
			NodeID: n.ID,
			Path:   info.Path,
			Steps:  info.Steps,
			Cost:   info.Cost,
		})
	}
	return plan, nil
}

// VerifyPlan independently re-checks a plan from scratch: target
// reachability (via DFS, a different traversal than the solver), the
// structural validity of every node path, the retained feature set,
// and optimality against a brute-force re-enumeration of candidates.
func VerifyPlan(g *Graph, plan *Plan) error {
	if _, ok := g.features[plan.Target]; !ok {
		return fmt.Errorf("verify: unknown target version %q", plan.Target)
	}
	want := g.sortedFeatures(plan.Target)
	if fmt.Sprint(want) != fmt.Sprint(plan.RetainedFeatures) {
		return fmt.Errorf("verify: retained features %v do not match target %q features %v",
			plan.RetainedFeatures, plan.Target, want)
	}
	if len(plan.NodePlans) != len(g.nodes) {
		return fmt.Errorf("verify: plan covers %d nodes, scenario has %d", len(plan.NodePlans), len(g.nodes))
	}
	totalSteps, totalCost := 0, 0
	for i, n := range g.nodes {
		np := plan.NodePlans[i]
		if np.NodeID != n.ID {
			return fmt.Errorf("verify: node plan %d is for %q, expected %q", i, np.NodeID, n.ID)
		}
		if !dfsReachable(g, n.Version, plan.Target) {
			return fmt.Errorf("verify: target %q not reachable from node %q at %q", plan.Target, n.ID, n.Version)
		}
		if len(np.Path) == 0 {
			return fmt.Errorf("verify: node %q has empty path", n.ID)
		}
		if np.Path[0] != n.Version {
			return fmt.Errorf("verify: node %q path starts at %q, current version is %q", n.ID, np.Path[0], n.Version)
		}
		if np.Path[len(np.Path)-1] != plan.Target {
			return fmt.Errorf("verify: node %q path ends at %q, target is %q", n.ID, np.Path[len(np.Path)-1], plan.Target)
		}
		steps, cost := 0, 0
		for k := 0; k+1 < len(np.Path); k++ {
			edgeCost, ok := edgeBetween(g, np.Path[k], np.Path[k+1])
			if !ok {
				return fmt.Errorf("verify: node %q path uses non-existent rollback edge %s -> %s", n.ID, np.Path[k], np.Path[k+1])
			}
			steps++
			cost += edgeCost
		}
		if steps != np.Steps || cost != np.Cost {
			return fmt.Errorf("verify: node %q claims (%d steps, %d cost), recomputed (%d, %d)", n.ID, np.Steps, np.Cost, steps, cost)
		}
		totalSteps += steps
		totalCost += cost
	}
	if totalSteps != plan.TotalSteps || totalCost != plan.TotalCost {
		return fmt.Errorf("verify: totals mismatch, plan says (%d steps, %d cost), recomputed (%d, %d)",
			plan.TotalSteps, plan.TotalCost, totalSteps, totalCost)
	}
	return verifyOptimal(g, plan)
}

// dfsReachable is an independent reachability check (iterative DFS).
func dfsReachable(g *Graph, from, to string) bool {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == to {
			return true
		}
		for _, e := range g.adj[cur] {
			if !seen[e.To] {
				seen[e.To] = true
				stack = append(stack, e.To)
			}
		}
	}
	return false
}

func edgeBetween(g *Graph, from, to string) (int, bool) {
	for _, e := range g.adj[from] {
		if e.To == to {
			return e.Cost, true
		}
	}
	return 0, false
}

// verifyOptimal brute-force re-enumerates all versions and confirms no
// candidate beats the plan on (retained features, total steps), using
// plain BFS hop counts computed independently of the solver.
func verifyOptimal(g *Graph, plan *Plan) error {
	bestFeatures := len(g.features[plan.Target])
	bestSteps := plan.TotalSteps
	for id := range g.features {
		steps := 0
		ok := true
		for _, n := range g.nodes {
			d, reach := bfsSteps(g, n.Version, id)
			if !reach {
				ok = false
				break
			}
			steps += d
		}
		if !ok {
			continue
		}
		f := len(g.features[id])
		if f > bestFeatures || (f == bestFeatures && steps < bestSteps) {
			return fmt.Errorf("verify: candidate %q (%d features, %d steps) beats plan (%d features, %d steps)",
				id, f, steps, bestFeatures, bestSteps)
		}
	}
	return nil
}

// bfsSteps returns the minimum number of rollback edges from -> to.
func bfsSteps(g *Graph, from, to string) (int, bool) {
	if from == to {
		return 0, true
	}
	seen := map[string]bool{from: true}
	type item struct {
		id    string
		depth int
	}
	queue := []item{{from, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.adj[cur.id] {
			if e.To == to {
				return cur.depth + 1, true
			}
			if !seen[e.To] {
				seen[e.To] = true
				queue = append(queue, item{e.To, cur.depth + 1})
			}
		}
	}
	return 0, false
}
