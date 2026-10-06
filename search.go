package main

import (
	"fmt"
	"strings"
)

// Transition is one atomic rollback move of a single node.
type Transition struct {
	NodeID string
	From   string
	To     string
	Cost   int
}

// Trajectory is a full legal transition sequence from the initial
// joint state to the goal joint state.
type Trajectory struct {
	Initial   []string
	Goal      []string
	Moves     []Transition
	States    [][]string // States[0] == Initial; States[k+1] follows Moves[k]
	TotalCost int
}

func stateKey(state []string) string {
	return strings.Join(state, "\x00")
}

// label is a (steps, cost) pair attached to a joint state. Because the
// shared budget is a hard constraint, a label with more steps but lower
// cost is not dominated by one with fewer steps and higher cost; the
// search therefore keeps the full Pareto frontier of labels per state.
type label struct {
	steps int
	cost  int
}

func dominates(a, b label) bool {
	return a.steps <= b.steps && a.cost <= b.cost
}

type stateLabel struct {
	key string
	label
}

// SearchTrajectory performs an exact state-space search (Dijkstra over
// joint version vectors, ordered by fewest transitions then lowest
// budget consumption) for a sequence of single-node rollback moves
// that brings every node to target. Every intermediate state must
// satisfy the compatibility rules, no node may enter one of its
// forbidden versions, and the cumulative cost may never exceed the
// shared maintenance budget.
func SearchTrajectory(g *Graph, target string) (*Trajectory, error) {
	if _, ok := g.features[target]; !ok {
		return nil, fmt.Errorf("unknown target version %q", target)
	}
	n := len(g.nodes)
	if n == 0 {
		return nil, fmt.Errorf("no nodes in scenario")
	}
	start := make([]string, n)
	goal := make([]string, n)
	for i, nd := range g.nodes {
		start[i] = nd.Version
		goal[i] = target
		if g.forbidden[i][target] {
			return nil, fmt.Errorf("target %q is forbidden for node %q (irreversible operations)", target, nd.ID)
		}
	}
	if !g.stateCompatible(start) {
		return nil, fmt.Errorf("initial state %v violates compatibility rules", start)
	}
	if !g.stateCompatible(goal) {
		return nil, fmt.Errorf("goal state %v violates compatibility rules", goal)
	}
	type prevInfo struct {
		prev stateLabel
		move Transition
	}
	startKey := stateKey(start)
	frontier := map[string][]label{startKey: {{}}}
	states := map[string][]string{startKey: start}
	prevs := map[stateLabel]prevInfo{}
	pq := &priorityQueue{{key: startKey}}
	goalKey := stateKey(goal)
	var goalLabel *stateLabel
	for pq.Len() > 0 {
		cur := popPQ(pq)
		curLabel := label{steps: cur.steps, cost: cur.cost}
		live := false
		for _, l := range frontier[cur.key] {
			if l == curLabel {
				live = true
				break
			}
		}
		if !live {
			continue
		}
		if cur.key == goalKey {
			goalLabel = &stateLabel{key: cur.key, label: curLabel}
			break
		}
		curState := states[cur.key]
		for i := 0; i < n; i++ {
			for _, e := range g.adj[curState[i]] {
				if g.forbidden[i][e.To] {
					continue
				}
				newCost := cur.cost + e.Cost
				if newCost > g.scenario.Budget {
					continue
				}
				next := make([]string, n)
				copy(next, curState)
				next[i] = e.To
				if !g.stateCompatible(next) {
					continue
				}
				key := stateKey(next)
				nd := label{steps: cur.steps + 1, cost: newCost}
				dominated := false
				kept := frontier[key][:0]
				for _, l := range frontier[key] {
					if dominates(l, nd) {
						dominated = true
					}
					if !dominates(nd, l) {
						kept = append(kept, l)
					}
				}
				if dominated {
					continue
				}
				frontier[key] = append(kept, nd)
				states[key] = next
				prevs[stateLabel{key: key, label: nd}] = prevInfo{
					prev: stateLabel{key: cur.key, label: curLabel},
					move: Transition{
						NodeID: g.nodes[i].ID,
						From:   curState[i],
						To:     e.To,
						Cost:   e.Cost,
					},
				}
				pushPQ(pq, pqItem{key: key, steps: nd.steps, cost: nd.cost})
			}
		}
	}
	if goalLabel == nil {
		return nil, fmt.Errorf("no legal trajectory to target %q within budget %d", target, g.scenario.Budget)
	}
	traj := &Trajectory{Initial: start, Goal: goal, TotalCost: goalLabel.cost}
	var revMoves []Transition
	var revStates [][]string
	for at := *goalLabel; at.key != startKey; {
		p := prevs[at]
		revMoves = append(revMoves, p.move)
		revStates = append(revStates, states[at.key])
		at = p.prev
	}
	traj.States = append(traj.States, start)
	for i := len(revMoves) - 1; i >= 0; i-- {
		traj.Moves = append(traj.Moves, revMoves[i])
	}
	for i := len(revStates) - 1; i >= 0; i-- {
		traj.States = append(traj.States, revStates[i])
	}
	return traj, nil
}

// VerifyTrajectory independently re-walks a trajectory and checks every
// invariant: each move is a real rollback edge, no forbidden version is
// ever entered, compatibility holds in every state including the
// endpoints, the shared budget is never exceeded, and the trajectory
// starts at the nodes' current versions and ends at the target.
func VerifyTrajectory(g *Graph, traj *Trajectory, target string) error {
	n := len(g.nodes)
	if len(traj.Initial) != n || len(traj.Goal) != n {
		return fmt.Errorf("verify: trajectory dimension mismatch")
	}
	for i, nd := range g.nodes {
		if traj.Initial[i] != nd.Version {
			return fmt.Errorf("verify: initial version of node %q is %q, expected %q", nd.ID, traj.Initial[i], nd.Version)
		}
		if traj.Goal[i] != target {
			return fmt.Errorf("verify: goal version of node %q is %q, expected target %q", nd.ID, traj.Goal[i], target)
		}
	}
	if len(traj.States) != len(traj.Moves)+1 {
		return fmt.Errorf("verify: %d states for %d moves", len(traj.States), len(traj.Moves))
	}
	if stateKey(traj.States[0]) != stateKey(traj.Initial) {
		return fmt.Errorf("verify: first state differs from initial state")
	}
	state := append([]string(nil), traj.Initial...)
	if !g.stateCompatible(state) {
		return fmt.Errorf("verify: initial state %v violates compatibility rules", state)
	}
	budget := 0
	for k, mv := range traj.Moves {
		i, ok := g.nodeIndex[mv.NodeID]
		if !ok {
			return fmt.Errorf("verify: move %d references unknown node %q", k+1, mv.NodeID)
		}
		if state[i] != mv.From {
			return fmt.Errorf("verify: move %d: node %q is at %q, move starts at %q", k+1, mv.NodeID, state[i], mv.From)
		}
		edgeCost, ok := edgeBetween(g, mv.From, mv.To)
		if !ok {
			return fmt.Errorf("verify: move %d: no rollback edge %s -> %s", k+1, mv.From, mv.To)
		}
		if edgeCost != mv.Cost {
			return fmt.Errorf("verify: move %d: cost %d, edge %s -> %s costs %d", k+1, mv.Cost, mv.From, mv.To, edgeCost)
		}
		if g.forbidden[i][mv.To] {
			return fmt.Errorf("verify: move %d: node %q enters forbidden version %q", k+1, mv.NodeID, mv.To)
		}
		budget += mv.Cost
		if budget > g.scenario.Budget {
			return fmt.Errorf("verify: move %d exceeds shared budget (%d > %d)", k+1, budget, g.scenario.Budget)
		}
		state[i] = mv.To
		if !g.stateCompatible(state) {
			return fmt.Errorf("verify: move %d leads to incompatible state %v", k+1, state)
		}
		if stateKey(state) != stateKey(traj.States[k+1]) {
			return fmt.Errorf("verify: recorded state %d %v does not match replayed state %v", k+1, traj.States[k+1], state)
		}
	}
	if stateKey(state) != stateKey(traj.Goal) {
		return fmt.Errorf("verify: final state %v does not reach goal %v", state, traj.Goal)
	}
	if budget != traj.TotalCost {
		return fmt.Errorf("verify: total cost %d, recomputed %d", traj.TotalCost, budget)
	}
	return nil
}
