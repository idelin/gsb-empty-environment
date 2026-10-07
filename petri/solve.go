package petri

import (
	"fmt"
	"strconv"
	"strings"
)

// Result is the outcome of a reachability query.
type Result struct {
	// Reachable reports whether the target marking can be reached from the
	// initial marking within the complete (capacity-bounded) state space.
	Reachable bool
	// Sequence is a shortest firing sequence (transition indices) leading
	// from the initial to the target marking. It is nil when the target is
	// unreachable, and empty when the target equals the initial marking.
	Sequence []int
	// Explored is the number of distinct markings dequeued and examined
	// during the breadth-first search. When the target is unreachable the
	// search exhausts the complete state space, so Explored equals the
	// total number of reachable markings, which proves unreachability.
	Explored int
}

// parentInfo records how BFS first reached a marking.
type parentInfo struct {
	prevKey string
	via     int // transition index used to reach this marking
}

// Solve searches the complete capacity-bounded state space of the net for
// a shortest firing sequence from initial to target using breadth-first
// search. Because BFS expands markings in order of increasing depth, the
// first sequence found is guaranteed to be shortest. If the queue empties
// without hitting the target, the target is unreachable in the complete
// state space.
//
// Every returned sequence is independently verified by replaying it
// step-by-step with Replay before Solve returns.
func Solve(n Net, initial, target Marking) (Result, error) {
	if err := n.Validate(); err != nil {
		return Result{}, err
	}
	if err := n.CheckMarking(initial); err != nil {
		return Result{}, fmt.Errorf("petri: initial marking: %v", err)
	}
	if err := n.CheckMarking(target); err != nil {
		// A marking outside the capacity bounds can never occur, so the
		// target is trivially unreachable without exploring anything.
		return Result{Reachable: false, Explored: 0}, nil
	}

	startKey := keyOf(initial)
	visited := map[string]bool{startKey: true}
	parents := make(map[string]parentInfo)
	queue := []Marking{initial}
	explored := 0

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		explored++

		if equal(cur, target) {
			seq := reconstruct(parents, keyOf(cur), startKey)
			if err := verify(n, initial, target, seq); err != nil {
				return Result{}, err
			}
			return Result{Reachable: true, Sequence: seq, Explored: explored}, nil
		}

		for t := range n.Transitions {
			next, ok := n.Fire(cur, t)
			if !ok {
				continue // disabled: not enough tokens or capacity exceeded
			}
			k := keyOf(next)
			if visited[k] {
				continue
			}
			visited[k] = true
			parents[k] = parentInfo{prevKey: keyOf(cur), via: t}
			queue = append(queue, next)
		}
	}

	// The queue is empty: every reachable marking has been examined and
	// none equals the target, so the target is unreachable in the
	// complete state space.
	return Result{Reachable: false, Explored: explored}, nil
}

// verify independently replays the sequence step-by-step and confirms it
// ends exactly at the target marking.
func verify(n Net, initial, target Marking, seq []int) error {
	final, err := n.Replay(initial, seq)
	if err != nil {
		return fmt.Errorf("petri: internal verification failed: %v", err)
	}
	if !equal(final, target) {
		return fmt.Errorf("petri: internal verification failed: replay ended at %v, want %v", []int(final), []int(target))
	}
	return nil
}

// reconstruct walks the parent chain from the target key back to the start
// key and returns the transition sequence in firing order.
func reconstruct(parents map[string]parentInfo, targetKey, startKey string) []int {
	var rev []int
	for k := targetKey; k != startKey; {
		p := parents[k]
		rev = append(rev, p.via)
		k = p.prevKey
	}
	seq := make([]int, len(rev))
	for i, t := range rev {
		seq[len(rev)-1-i] = t
	}
	return seq
}

func keyOf(m Marking) string {
	var b strings.Builder
	for i, tok := range m {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(tok))
	}
	return b.String()
}

func equal(a, b Marking) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
