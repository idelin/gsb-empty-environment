package netsec

import "errors"

// VerifyReport cross-checks reachability on a small bitfield by enumerating
// the entire (safe) state space with three independent algorithms.
type VerifyReport struct {
	// Space is the number of legal register states in the full bitfield.
	Space int
	// Safe is the number of states that keep every forbidden enable bit clear.
	Safe int
	// Reachable is the number of safe states reachable from Start, agreed by
	// forward BFS and forward DFS.
	Reachable int
	// Dead is the number of safe states from which no goal state is reachable
	// when they are used as the starting configuration.
	Dead int
	// CanReachGoal is the number of safe states from which at least one goal
	// state is reachable, agreed by reverse BFS and memoized DFS.
	CanReachGoal int
	// GoalReachable reports whether the model's own Target is reachable from
	// Start.
	GoalReachable bool
	// Consistent is true when the independent forward and reverse algorithms
	// agree on both reachability predicates.
	Consistent bool
	// Err explains why verification could not run, when non-nil.
	Err error
}

// Verify exhaustively analyzes the safe state space. It is intended for small
// bitfields (the default cap covers 16 bits); larger models must opt in with an
// explicit MaxStates budget.
func VerifyDeadEnds(m Model, maxStates int) VerifyReport {
	if err := m.Validate(); err != nil {
		return VerifyReport{Err: err}
	}
	if maxStates <= 0 {
		maxStates = DefaultVerifyStates
	}
	space := int(m.fullMask() + 1)
	if space > maxStates {
		return VerifyReport{
			Space: space,
			Err:   errors.New("netsec: state space exceeds verification budget"),
		}
	}

	safeStates := make([]uint32, 0, space)
	for state := 0; state < space; state++ {
		if m.Safe(uint32(state)) {
			safeStates = append(safeStates, uint32(state))
		}
	}

	forwardBFS := forwardReachBFS(m, m.Start)
	forwardDFS := forwardReachDFS(m, m.Start)
	reverse := reverseGoalReach(m, safeStates)
	dfs := dfsGoalReach(m, safeStates)

	consistent := len(forwardBFS) == len(forwardDFS)
	if consistent {
		for state := range forwardBFS {
			if !forwardDFS[state] {
				consistent = false
				break
			}
		}
	}
	if consistent {
		for state, good := range reverse {
			if dfs[state] != good {
				consistent = false
				break
			}
		}
	}

	dead := 0
	canReachGoal := 0
	for _, state := range safeStates {
		if dfs[state] {
			canReachGoal++
		} else {
			dead++
		}
	}

	return VerifyReport{
		Space:         space,
		Safe:          len(safeStates),
		Reachable:     len(forwardBFS),
		CanReachGoal:  canReachGoal,
		Dead:          dead,
		GoalReachable: dfs[m.Start],
		Consistent:    consistent,
	}
}

// forwardReachBFS enumerates states reachable from start with a queue-based BFS.
func forwardReachBFS(m Model, start uint32) map[uint32]bool {
	seen := map[uint32]bool{start: true}
	queue := []uint32{start}
	for len(queue) > 0 {
		state := queue[0]
		queue = queue[1:]
		m.Expand(state, func(next uint32, _ Write) bool {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
			return true
		})
	}
	return seen
}

// forwardReachDFS independently enumerates reachable states with an iterative
// DFS so it shares no queue bookkeeping with forwardReachBFS.
func forwardReachDFS(m Model, start uint32) map[uint32]bool {
	seen := map[uint32]bool{start: true}
	stack := []uint32{start}
	for len(stack) > 0 {
		state := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		m.Expand(state, func(next uint32, _ Write) bool {
			if !seen[next] {
				seen[next] = true
				stack = append(stack, next)
			}
			return true
		})
	}
	return seen
}

// reverseGoalReach walks predecessors backwards from every goal state.
func reverseGoalReach(m Model, safeStates []uint32) map[uint32]bool {
	canReachGoal := make(map[uint32]bool)
	queue := make([]uint32, 0)
	for _, state := range safeStates {
		if m.AtGoal(state) {
			canReachGoal[state] = true
			queue = append(queue, state)
		}
	}
	for len(queue) > 0 {
		state := queue[0]
		queue = queue[1:]
		m.Predecessors(state, func(prev uint32) bool {
			if !canReachGoal[prev] {
				canReachGoal[prev] = true
				queue = append(queue, prev)
			}
			return true
		})
	}
	return canReachGoal
}

// dfsGoalReach independently resolves "can state reach any goal?" with an
// iterative three-color DFS and memoized results, which tolerates the cycles
// created by rewritable enable bits.
func dfsGoalReach(m Model, safeStates []uint32) map[uint32]bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[uint32]uint8, len(safeStates))
	win := make(map[uint32]bool, len(safeStates))

	successors := func(state uint32) []uint32 {
		out := make([]uint32, 0, 4)
		m.Expand(state, func(next uint32, _ Write) bool {
			out = append(out, next)
			return true
		})
		return out
	}

	for _, root := range safeStates {
		if color[root] != white {
			continue
		}
		type frame struct {
			state uint32
			next  []uint32
			index int
		}
		stack := []frame{{state: root, next: successors(root)}}
		color[root] = gray

		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if m.AtGoal(top.state) {
				win[top.state] = true
			}
			if top.index >= len(top.next) {
				good := win[top.state]
				if !good {
					for _, next := range top.next {
						if win[next] {
							good = true
							break
						}
					}
				}
				color[top.state] = black
				win[top.state] = good
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && good {
					win[stack[len(stack)-1].state] = true
				}
				continue
			}
			next := top.next[top.index]
			top.index++
			switch color[next] {
			case white:
				color[next] = gray
				stack = append(stack, frame{state: next, next: successors(next)})
			case black:
				if win[next] {
					win[top.state] = true
				}
			}
		}
	}

	out := make(map[uint32]bool, len(safeStates))
	for _, state := range safeStates {
		out[state] = win[state]
	}
	return out
}
