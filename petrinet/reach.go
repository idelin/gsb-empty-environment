package petrinet

import (
	"fmt"
	"strconv"
	"strings"
)

// Result is the outcome of a reachability query.
type Result struct {
	// Reachable reports whether the target marking was reached.
	Reachable bool
	// Sequence is a shortest firing sequence (transition indices) leading
	// from the initial to the target marking. Nil when unreachable.
	Sequence []int
	// Explored is the number of distinct states expanded (dequeued and
	// examined) during the search. When Reachable is false, the complete
	// reachable state space was explored, which proves unreachability.
	Explored int
}

// key encodes a marking as a canonical string for the visited set.
func key(m []int) string {
	var b strings.Builder
	for i, v := range m {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(v))
	}
	return b.String()
}

type parent struct {
	prev string
	tr   int
}

// Reachable performs a breadth-first search from initial toward target.
// Because BFS expands states in order of increasing depth, the returned
// sequence is a shortest firing sequence. If the queue is exhausted
// without hitting the target, the target is unreachable in the complete
// state space (finite because capacities bound every place).
func (n *Net) Reachable(initial, target []int) (*Result, error) {
	if err := n.Validate(); err != nil {
		return nil, err
	}
	if err := n.CheckMarking(initial); err != nil {
		return nil, fmt.Errorf("petrinet: initial marking: %v", err)
	}
	if err := n.CheckMarking(target); err != nil {
		return nil, fmt.Errorf("petrinet: target marking: %v", err)
	}

	initKey := key(initial)
	if key(target) == initKey {
		return &Result{Reachable: true, Sequence: []int{}, Explored: 0}, nil
	}

	parents := map[string]parent{initKey: {prev: "", tr: -1}}
	markings := map[string][]int{initKey: append([]int(nil), initial...)}
	queue := []string{initKey}
	explored := 0

	for len(queue) > 0 {
		curKey := queue[0]
		queue = queue[1:]
		cur := markings[curKey]
		explored++

		for t := range n.Transitions {
			next, ok := n.Fire(cur, t)
			if !ok {
				continue
			}
			nk := key(next)
			if _, seen := parents[nk]; seen {
				continue
			}
			parents[nk] = parent{prev: curKey, tr: t}
			markings[nk] = next
			if nk == key(target) {
				return &Result{
					Reachable: true,
					Sequence:  reconstruct(parents, nk),
					Explored:  explored,
				}, nil
			}
			queue = append(queue, nk)
		}
	}
	return &Result{Reachable: false, Explored: explored}, nil
}

// reconstruct walks the parent chain back to the root and returns the
// transition sequence in firing order.
func reconstruct(parents map[string]parent, leaf string) []int {
	var rev []int
	for k := leaf; parents[k].tr >= 0; k = parents[k].prev {
		rev = append(rev, parents[k].tr)
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
