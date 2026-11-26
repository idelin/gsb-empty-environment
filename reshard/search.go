package reshard

import (
	"container/heap"
	"errors"
	"math/big"
	"strings"
)

// label is one non-dominated (cost, steps, path) reaching a state. Multiple
// labels can coexist for one state: equal cost and step count but different
// primitive paths expand into different successors, so they cannot be merged
// by lexicographic order until the goal is selected.
type label struct {
	cost  *big.Int
	steps int
	path  []Op
}

type pqItem struct {
	state state
	lbl   *label
}

type pq2 []*pqItem

func (h pq2) Len() int { return len(h) }

func (h pq2) Less(i, j int) bool {
	c := h[i].lbl.cost.Cmp(h[j].lbl.cost)
	if c != 0 {
		return c < 0
	}
	if h[i].lbl.steps != h[j].lbl.steps {
		return h[i].lbl.steps < h[j].lbl.steps
	}
	return compareLabels(h[i].lbl.path, h[j].lbl.path) < 0
}

func (h pq2) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *pq2) Push(x any) { *h = append(*h, x.(*pqItem)) }

func (h *pq2) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// dominates reports whether a is strictly preferred to b on the primary
// (cost, steps) criteria. Lexicographic order is only applied at the goal.
func dominates(a, b *label) bool {
	c := a.cost.Cmp(b.cost)
	if c != 0 {
		return c < 0
	}
	return a.steps < b.steps
}

// sameSignature reports exact (cost, steps, path) equality for de-duplication.
func sameSignature(a, b *label) bool {
	if a.cost.Cmp(b.cost) != 0 || a.steps != b.steps {
		return false
	}
	return compareLabels(a.path, b.path) == 0
}

func stateKey(s state) string {
	var b strings.Builder
	for i, a := range s.shard {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(itoa(a))
	}
	b.WriteByte('|')
	for i, ok := range s.partial {
		if i > 0 {
			b.WriteByte(',')
		}
		if ok {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

func appendPath(path []Op, op Op) []Op {
	out := make([]Op, 0, len(path)+1)
	out = append(out, path...)
	out = append(out, op)
	return out
}

// acceptLabel inserts cand unless an existing label dominates it or an
// identical label already exists; dominated existing labels are pruned.
func acceptLabel(labels []*label, cand *label) ([]*label, bool) {
	for _, existing := range labels {
		if sameSignature(existing, cand) {
			return labels, false
		}
		if dominates(existing, cand) {
			return labels, false
		}
	}
	kept := labels[:0]
	for _, existing := range labels {
		if !dominates(cand, existing) {
			kept = append(kept, existing)
		}
	}
	return append(kept, cand), true
}

// Solve returns the exact minimum-communication primitive sequence that takes
// p.Source to p.Target. Ties are broken by fewer steps and then by the
// lexicographically smallest primitive sequence.
//
// It returns an error for inconsistent inputs, illegal non-integral
// shardings, or when no legal primitive sequence reaches the target.
func Solve(p Problem) (*Solution, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	solver := newSolver(p)
	start := newState(p.Source)
	goal := newState(p.Target)

	startLabel := &label{cost: new(big.Int), steps: 0, path: nil}
	allLabels := map[string][]*label{stateKey(start): {startLabel}}
	queue := &pq2{{state: start, lbl: startLabel}}
	heap.Init(queue)

	for queue.Len() > 0 {
		item := heap.Pop(queue).(*pqItem)
		cur := item.state
		curLbl := item.lbl
		// Skip labels pruned after being enqueued.
		live := false
		for _, existing := range allLabels[stateKey(cur)] {
			if sameSignature(existing, curLbl) {
				live = true
				break
			}
		}
		if !live {
			continue
		}
		for _, e := range solver.edges(cur) {
			cand := &label{
				cost:  new(big.Int).Add(curLbl.cost, e.weight),
				steps: curLbl.steps + 1,
				path:  appendPath(curLbl.path, e.op),
			}
			key := stateKey(e.next)
			updated, accepted := acceptLabel(allLabels[key], cand)
			allLabels[key] = updated
			if accepted {
				heap.Push(queue, &pqItem{state: e.next, lbl: cand})
			}
		}
	}

	goalLabels := allLabels[stateKey(goal)]
	if len(goalLabels) == 0 {
		return nil, errors.New("reshard: target layout is not reachable from the source layout")
	}
	bestGoal := goalLabels[0]
	for _, l := range goalLabels[1:] {
		if dominates(l, bestGoal) ||
			(l.cost.Cmp(bestGoal.cost) == 0 && l.steps == bestGoal.steps &&
				compareLabels(l.path, bestGoal.path) < 0) {
			bestGoal = l
		}
	}
	return &Solution{
		Ops:        append([]Op(nil), bestGoal.path...),
		Steps:      bestGoal.steps,
		ScaledCost: new(big.Int).Set(bestGoal.cost),
		Cost:       new(big.Rat).SetFrac(bestGoal.cost, big.NewInt(solver.lcm)),
		Scale:      solver.lcm,
	}, nil
}
