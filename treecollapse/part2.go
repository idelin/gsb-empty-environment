package main

import (
	"container/heap"
	"fmt"
	"strings"
)

// Action is one step of an expand/collapse plan.
type Action struct {
	Expand bool
	Group  int
	Cost   int
}

// Step is an executed action together with the resulting reference
// accounting snapshot.
type Step struct {
	Action  Action
	Memory  int
	RefCnt  []int
	Done    uint64
	CumCost int
}

// Part2Result is the cheapest action plan: every target visible at some
// point, live memory never above the budget.
type Part2Result struct {
	OK        bool
	TotalCost int
	Peak      int
	Actions   []Action
}

// state is the search state: the expanded set plus the set of targets
// already seen (a target stays "done" once it has been visible).
type state struct {
	mask uint64
	done uint64
}

func (s state) key() uint64 { return s.mask<<32 | s.done }

// doneTargets returns the set of targets visible under mask.
func (inst *Instance) doneTargets(mask uint64) uint64 {
	var done uint64
	for i, t := range inst.Targets {
		if inst.visible(mask, t) {
			done |= 1 << uint(i)
		}
	}
	return done
}

// moves lists the legal actions from an expanded set: expand a group
// whose parent is expanded (or the root), collapse an expanded group
// with no expanded children.
func (inst *Instance) moves(mask uint64) []Action {
	var out []Action
	for g := range inst.Groups {
		if mask&(1<<uint(g)) != 0 {
			childOpen := false
			for c := range inst.Groups {
				if inst.Groups[c].Parent == g && mask&(1<<uint(c)) != 0 {
					childOpen = true
					break
				}
			}
			if !childOpen {
				out = append(out, Action{Expand: false, Group: g, Cost: inst.Groups[g].CollapseCost})
			}
		} else if p := inst.Groups[g].Parent; p == -1 || mask&(1<<uint(p)) != 0 {
			out = append(out, Action{Expand: true, Group: g, Cost: inst.Groups[g].ExpandCost})
		}
	}
	return out
}

// apply performs an action on an expanded set.
func apply(mask uint64, a Action) uint64 {
	if a.Expand {
		return mask | 1<<uint(a.Group)
	}
	return mask &^ (1 << uint(a.Group))
}

// SolvePart2 runs Dijkstra over (expanded set, done targets) states for
// at most 12 groups. A state is only entered when its live memory fits
// the budget; shared blocks stay counted while any expanded group still
// references them.
func (inst *Instance) SolvePart2(budget int) Part2Result {
	if len(inst.Groups) > 12 {
		panic("SolvePart2 supports at most 12 groups")
	}
	allDone := uint64(1)<<uint(len(inst.Targets)) - 1
	start := state{mask: 0, done: inst.doneTargets(0)}
	dist := map[uint64]int{start.key(): 0}
	prev := map[uint64]struct {
		from uint64
		act  Action
	}{}
	pq := &pqItemHeap{{st: start, cost: 0}}
	heap.Init(pq)
	var goal *state
	goalCost := 0
	for pq.Len() > 0 {
		it := heap.Pop(pq).(pqItem)
		if d, ok := dist[it.st.key()]; ok && it.cost > d {
			continue
		}
		if it.st.done == allDone {
			goal = &it.st
			goalCost = it.cost
			break
		}
		for _, mv := range inst.moves(it.st.mask) {
			nmask := apply(it.st.mask, mv)
			if inst.memoryOf(nmask) > budget {
				continue
			}
			nst := state{mask: nmask, done: it.st.done | inst.doneTargets(nmask)}
			nc := it.cost + mv.Cost
			if d, ok := dist[nst.key()]; !ok || nc < d {
				dist[nst.key()] = nc
				prev[nst.key()] = struct {
					from uint64
					act  Action
				}{from: it.st.key(), act: mv}
				heap.Push(pq, pqItem{st: nst, cost: nc})
			}
		}
	}
	if goal == nil {
		return Part2Result{}
	}
	var rev []Action
	for k := goal.key(); k != start.key(); {
		p := prev[k]
		rev = append(rev, p.act)
		k = p.from
	}
	res := Part2Result{OK: true, TotalCost: goalCost}
	for i := len(rev) - 1; i >= 0; i-- {
		res.Actions = append(res.Actions, rev[i])
	}
	// Peak memory along the plan.
	mask := uint64(0)
	for _, a := range res.Actions {
		mask = apply(mask, a)
		if m := inst.memoryOf(mask); m > res.Peak {
			res.Peak = m
		}
	}
	return res
}

// VerifyPart2 independently recomputes the optimum by Bellman-style
// relaxation to a fixpoint (no priority queue), used to cross-check
// SolvePart2. Returns the optimum cost and whether a plan exists.
func (inst *Instance) VerifyPart2(budget int) (int, bool) {
	allDone := uint64(1)<<uint(len(inst.Targets)) - 1
	start := state{mask: 0, done: inst.doneTargets(0)}
	dist := map[uint64]int{start.key(): 0}
	for changed := true; changed; {
		changed = false
		for k, d := range dist {
			var st state
			st.mask, st.done = k>>32, k&(1<<32-1)
			for _, mv := range inst.moves(st.mask) {
				nmask := apply(st.mask, mv)
				if inst.memoryOf(nmask) > budget {
					continue
				}
				nst := state{mask: nmask, done: st.done | inst.doneTargets(nmask)}
				if nd, ok := dist[nst.key()]; !ok || d+mv.Cost < nd {
					dist[nst.key()] = d + mv.Cost
					changed = true
				}
			}
		}
	}
	best, ok := -1, false
	for k, d := range dist {
		if k&(1<<32-1) == allDone && (best == -1 || d < best) {
			best, ok = d, true
		}
	}
	return best, ok
}

// Replay executes a plan step by step and renders the reference
// accounting: per-block refcount transitions, loads and frees, live
// memory and cumulative action cost.
func (inst *Instance) Replay(res Part2Result, budget int) string {
	var b strings.Builder
	mask := uint64(0)
	rc := inst.refCounts(mask)
	cum := 0
	fmt.Fprintf(&b, "start: memory 0 (budget %d)\n", budget)
	for i, a := range res.Actions {
		verb := "collapse"
		if a.Expand {
			verb = "expand"
		}
		cum += a.Cost
		fmt.Fprintf(&b, "step %d: %s %s (cost %d, cum %d)\n", i+1, verb, inst.Groups[a.Group].Name, a.Cost, cum)
		for _, blk := range inst.Groups[a.Group].Blocks {
			old := rc[blk]
			if a.Expand {
				rc[blk]++
			} else {
				rc[blk]--
			}
			note := ""
			switch {
			case old == 0 && rc[blk] == 1:
				note = " loaded"
			case rc[blk] == 0:
				note = " freed"
			default:
				note = " still referenced"
			}
			fmt.Fprintf(&b, "  block %d: rc %d->%d (size %d)%s\n", blk, old, rc[blk], inst.BlockSizes[blk], note)
		}
		mask = apply(mask, a)
		fmt.Fprintf(&b, "  memory: %d (budget %d)\n", inst.memoryOf(mask), budget)
	}
	return b.String()
}

// pqItem is a priority-queue entry for Dijkstra.
type pqItem struct {
	st   state
	cost int
}

type pqItemHeap []pqItem

func (h pqItemHeap) Len() int            { return len(h) }
func (h pqItemHeap) Less(i, j int) bool  { return h[i].cost < h[j].cost }
func (h pqItemHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *pqItemHeap) Push(x interface{}) { *h = append(*h, x.(pqItem)) }
func (h *pqItemHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
