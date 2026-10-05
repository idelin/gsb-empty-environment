package main

import "container/heap"

// ExploredState records one expanded state and its best known cost,
// in expansion order.
type ExploredState struct {
	State uint32
	Cost  int
}

// SolveResult is the outcome of an optimal-cost search.
type SolveResult struct {
	Reached  bool
	Cost     int
	Plan     []string
	Explored []ExploredState
}

type pqItem struct {
	state uint32
	cost  int
}

type priorityQueue []pqItem

func (q priorityQueue) Len() int            { return len(q) }
func (q priorityQueue) Less(i, j int) bool  { return q[i].cost < q[j].cost }
func (q priorityQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *priorityQueue) Push(x interface{}) { *q = append(*q, x.(pqItem)) }
func (q *priorityQueue) Pop() interface{} {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

// Solve runs Dijkstra (uniform-cost) search over the state space.
//
// Because edge costs are non-negative and a state is only re-queued when
// a strictly cheaper path is found, zero-cost cycles terminate: revisiting
// a state via a zero-cost loop yields an equal (not smaller) cost and is
// discarded. Integer costs are bounded below by 0, so the number of
// improvements per state is finite as well.
func Solve(p *Problem) SolveResult {
	type predecessor struct {
		state  uint32
		action int
	}
	dist := map[uint32]int{p.Init: 0}
	prev := make(map[uint32]predecessor)
	pq := &priorityQueue{{state: p.Init, cost: 0}}
	heap.Init(pq)

	var res SolveResult
	goalState, found := uint32(0), false

	for pq.Len() > 0 {
		it := heap.Pop(pq).(pqItem)
		if d, ok := dist[it.state]; ok && it.cost > d {
			continue // stale queue entry
		}
		res.Explored = append(res.Explored, ExploredState{State: it.state, Cost: it.cost})
		if p.GoalSatisfied(it.state) {
			found, goalState, res.Cost = true, it.state, it.cost
			break
		}
		for i, a := range p.Actions {
			if it.state&a.Pre != a.Pre {
				continue
			}
			next := apply(it.state, a)
			nd := it.cost + a.Cost
			if cur, ok := dist[next]; !ok || nd < cur {
				dist[next] = nd
				prev[next] = predecessor{state: it.state, action: i}
				heap.Push(pq, pqItem{state: next, cost: nd})
			}
		}
	}

	if found {
		res.Reached = true
		var rev []string
		for s := goalState; s != p.Init; {
			pr := prev[s]
			rev = append(rev, p.Actions[pr.action].Name)
			s = pr.state
		}
		for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
			rev[i], rev[j] = rev[j], rev[i]
		}
		res.Plan = rev
	}
	return res
}
