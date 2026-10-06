package main

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Candidate is a feasible common rollback target with its ranking keys
// and one shortest (in steps) path per node.
type Candidate struct {
	Target     string
	Features   int
	TotalSteps int
	Paths      [][]string // per node: versions from current to target
}

// shortestPaths runs BFS over rollback edges from start and returns the
// distance (in steps) and the predecessor edge for every reachable version.
func shortestPaths(g *Graph, start string) (map[string]int, map[string]Edge) {
	dist := map[string]int{start: 0}
	prev := make(map[string]Edge)
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.Out[cur] {
			if _, seen := dist[e.To]; seen {
				continue
			}
			dist[e.To] = dist[cur] + 1
			prev[e.To] = e
			queue = append(queue, e.To)
		}
	}
	return dist, prev
}

func buildPath(g *Graph, start, target string, prev map[string]Edge) []string {
	rev := []string{target}
	for rev[len(rev)-1] != start {
		rev = append(rev, prev[rev[len(rev)-1]].From)
	}
	path := make([]string, len(rev))
	for i, v := range rev {
		path[len(rev)-1-i] = v
	}
	return path
}

// RankCandidates enumerates every version as a candidate common rollback
// target (full candidate traversal over the finite graph), keeps those
// reachable from all nodes, and ranks them by retained feature count
// (descending), then total rollback steps (ascending), then target ID.
func RankCandidates(p *Problem) []Candidate {
	dists := make([]map[string]int, len(p.Nodes))
	prevs := make([]map[string]Edge, len(p.Nodes))
	for i, n := range p.Nodes {
		dists[i], prevs[i] = shortestPaths(p.Graph, n.Current)
	}
	var out []Candidate
	for id, v := range p.Graph.Versions {
		cand := Candidate{Target: id, Features: len(v.Features)}
		ok := true
		for i := range p.Nodes {
			d, reach := dists[i][id]
			if !reach {
				ok = false
				break
			}
			cand.TotalSteps += d
		}
		if !ok {
			continue
		}
		for i, n := range p.Nodes {
			cand.Paths = append(cand.Paths, buildPath(p.Graph, n.Current, id, prevs[i]))
		}
		out = append(out, cand)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Features != b.Features {
			return a.Features > b.Features
		}
		if a.TotalSteps != b.TotalSteps {
			return a.TotalSteps < b.TotalSteps
		}
		return a.Target < b.Target
	})
	return out
}

// Transition is one atomic rollback step of the joint plan.
type Transition struct {
	Node     int
	Edge     Edge
	Versions []string // joint state after the transition
}

// Plan is a legal transition trajectory bringing every node to Target.
type Plan struct {
	Target      string
	TotalCost   int
	Transitions []Transition
}

func stateKey(versions []string) string { return strings.Join(versions, "\x00") }

type pqItem struct {
	versions []string
	cost     int
	steps    int
	key      string
	index    int
}

type priorityQueue []pqItem

func (q priorityQueue) Len() int { return len(q) }
func (q priorityQueue) Less(i, j int) bool {
	if q[i].cost != q[j].cost {
		return q[i].cost < q[j].cost
	}
	if q[i].steps != q[j].steps {
		return q[i].steps < q[j].steps
	}
	return q[i].key < q[j].key
}
func (q priorityQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i]; q[i].index, q[j].index = i, j }
func (q *priorityQueue) Push(x interface{}) { *q = append(*q, x.(pqItem)) }
func (q *priorityQueue) Pop() interface{} {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

// activeNode returns the first node not yet at the target; the plan is
// executed strictly in node order, so only this node may transition.
func activeNode(versions []string, target string) int {
	for i, v := range versions {
		if v != target {
			return i
		}
	}
	return -1
}

// PlanTrajectory runs an exact state-space search (Dijkstra over joint
// version tuples) for a minimum-cost legal trajectory that rolls every
// node back to target, in node order, while always satisfying:
//   - the shared maintenance budget (sum of edge costs <= Budget),
//   - per-node forbidden versions (irreversible operations),
//   - the pairwise compatibility relation after every single step.
func PlanTrajectory(p *Problem, target string) (*Plan, error) {
	if _, ok := p.Graph.Versions[target]; !ok {
		return nil, fmt.Errorf("unknown target version %q", target)
	}
	start := p.initialState()
	if !p.stateCompatible(start) {
		return nil, errors.New("initial state violates compatibility")
	}
	type parent struct {
		prevKey string
		tr      Transition
	}
	best := map[string]int{stateKey(start): 0}
	parents := make(map[string]parent)
	states := map[string][]string{stateKey(start): start}
	pq := &priorityQueue{{versions: start, key: stateKey(start)}}
	goalKey := ""
	for pq.Len() > 0 {
		it := heap.Pop(pq).(pqItem)
		if it.cost > best[it.key] {
			continue
		}
		active := activeNode(it.versions, target)
		if active == -1 {
			goalKey = it.key
			break
		}
		cur := it.versions[active]
		for _, e := range p.Graph.Out[cur] {
			if p.Nodes[active].Forbidden[e.To] {
				continue
			}
			newCost := it.cost + e.Cost
			if newCost > p.Budget {
				continue
			}
			next := make([]string, len(it.versions))
			copy(next, it.versions)
			next[active] = e.To
			if !p.stateCompatible(next) {
				continue
			}
			k := stateKey(next)
			if old, seen := best[k]; seen && old <= newCost {
				continue
			}
			best[k] = newCost
			states[k] = next
			parents[k] = parent{prevKey: it.key, tr: Transition{Node: active, Edge: e, Versions: next}}
			heap.Push(pq, pqItem{versions: next, cost: newCost, steps: it.steps + 1, key: k})
		}
	}
	if goalKey == "" {
		return nil, fmt.Errorf("no legal trajectory to %s within budget %d", target, p.Budget)
	}
	plan := &Plan{Target: target, TotalCost: best[goalKey]}
	for k := goalKey; ; {
		par, ok := parents[k]
		if !ok {
			break
		}
		plan.Transitions = append(plan.Transitions, par.tr)
		k = par.prevKey
	}
	for i, j := 0, len(plan.Transitions)-1; i < j; i, j = i+1, j-1 {
		plan.Transitions[i], plan.Transitions[j] = plan.Transitions[j], plan.Transitions[i]
	}
	return plan, nil
}

// Solve ranks all common rollback candidates (max retained features, then
// min total steps) and returns the first one admitting a legal trajectory
// under the budget / forbidden-version / compatibility constraints.
func Solve(p *Problem) (*Candidate, *Plan, error) {
	cands := RankCandidates(p)
	if len(cands) == 0 {
		return nil, nil, errors.New("no common reachable rollback version")
	}
	var lastErr error
	for i := range cands {
		plan, err := PlanTrajectory(p, cands[i].Target)
		if err == nil {
			return &cands[i], plan, nil
		}
		lastErr = err
	}
	return nil, nil, fmt.Errorf("no feasible candidate: %v", lastErr)
}
