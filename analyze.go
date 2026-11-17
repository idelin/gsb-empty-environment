package netsec

import "fmt"

// DefaultAnalysisLimit is the default maximum number of states enumerated by
// Analyze. 2^16 states is small enough to build an explicit reverse graph and
// is the intended setting for cross-checking dead ends.
const DefaultAnalysisLimit = 1 << 16

// Analysis is the result of an exhaustive, bounded-state-space examination.
type Analysis struct {
	// Complete is true when every state in the register's state space was
	// examined.
	Complete bool
	Bits     uint
	States   int

	// Reachable counts safe states reachable from Initial.
	Reachable int
	// CoReachable counts safe states from which Goal can be reached.
	CoReachable int
	// TargetReachable reports whether a safe path reaches the goal.
	TargetReachable bool

	// DeadEnds are states reachable from Initial from which the goal cannot
	// be reached without violating safety. They are exactly the states whose
	// locks/preconditions make the current configuration unrecoverable.
	DeadEnds []uint32

	Reason string
}

// DeadEnd reports whether state is reachable from Initial but cannot reach the
// goal. It is only meaningful for state spaces small enough for Analyze.
func (a Analysis) DeadEnd(state uint32) bool {
	for _, s := range a.DeadEnds {
		if s == state {
			return true
		}
	}
	return false
}

// Analyze exhaustively examines all register states up to
// DefaultAnalysisLimit. It cross-checks the forward-search conclusion with a
// reverse reachability pass, which identifies unrecoverable dead ends rather
// than merely reporting that the goal was not found within a budget.
func Analyze(p Problem) (Analysis, error) {
	return AnalyzeLimit(p, DefaultAnalysisLimit)
}

// AnalyzeLimit is Analyze with an explicit state-space limit. It returns
// ErrTooLarge when 2^Bits exceeds limit.
func AnalyzeLimit(p Problem, limit int) (Analysis, error) {
	if limit <= 0 {
		return Analysis{}, fmt.Errorf("%w: analysis limit must be positive", ErrInvalidProblem)
	}
	n, err := p.normalize()
	if err != nil {
		return Analysis{}, err
	}
	bitsWidth := n.problem.Bits
	if bitsWidth >= 31 {
		return Analysis{}, fmt.Errorf("%w: width %d", ErrTooLarge, bitsWidth)
	}
	stateCount := 1 << bitsWidth
	if stateCount > limit {
		return Analysis{}, fmt.Errorf("%w: %d states (limit %d)", ErrTooLarge, stateCount, limit)
	}

	safe := make([]bool, stateCount)
	for s := 0; s < stateCount; s++ {
		safe[s] = !n.forbidden(uint32(s))
	}

	reachable := make([]bool, stateCount)
	initial := n.problem.Initial
	if safe[initial] {
		reachable[initial] = true
		queue := []uint32{initial}
		for head := 0; head < len(queue); head++ {
			state := queue[head]
			for _, op := range n.problem.Ops {
				next, ok := applyOp(state, op, n.locks, n.problem.Locks, n.globalFreeze)
				if !ok || !safe[next] || reachable[next] {
					continue
				}
				reachable[next] = true
				queue = append(queue, next)
			}
		}
	}

	// Build the reverse graph of safe transitions. This lets us identify all
	// states that still have a route to the goal, independently of the
	// forward search.
	reverse := make([][]uint32, stateCount)
	for state := 0; state < stateCount; state++ {
		if !safe[state] {
			continue
		}
		for _, op := range n.problem.Ops {
			next, ok := applyOp(uint32(state), op, n.locks, n.problem.Locks, n.globalFreeze)
			if !ok || !safe[next] || next == uint32(state) {
				continue
			}
			reverse[next] = append(reverse[next], uint32(state))
		}
	}

	coReachable := make([]bool, stateCount)
	// With a partial goal there may be several goal states; seed the reverse
	// search from every safe state that satisfies it.
	queue := make([]uint32, 0)
	for state := 0; state < stateCount; state++ {
		if !safe[state] || !n.goal(uint32(state)) {
			continue
		}
		coReachable[state] = true
		queue = append(queue, uint32(state))
	}
	for head := 0; head < len(queue); head++ {
		state := queue[head]
		for _, prev := range reverse[state] {
			if coReachable[prev] {
				continue
			}
			coReachable[prev] = true
			queue = append(queue, prev)
		}
	}

	analysis := Analysis{
		Complete: true,
		Bits:     bitsWidth,
		States:   stateCount,
	}
	for state := 0; state < stateCount; state++ {
		if reachable[state] {
			analysis.Reachable++
		}
		if coReachable[state] {
			analysis.CoReachable++
		}
	}
	analysis.TargetReachable = safe[n.problem.Target] && reachable[n.problem.Target]
	if n.partialGoal() {
		analysis.TargetReachable = false
		for state := 0; state < stateCount; state++ {
			if reachable[state] && n.goal(uint32(state)) {
				analysis.TargetReachable = true
				break
			}
		}
	}

	for state := 0; state < stateCount; state++ {
		if reachable[state] && !coReachable[state] {
			analysis.DeadEnds = append(analysis.DeadEnds, uint32(state))
		}
	}
	if !safe[initial] {
		analysis.Reason = "initial state is forbidden"
	} else if !analysis.TargetReachable {
		analysis.Reason = "goal is not reachable through safe states"
	}

	return analysis, nil
}

// Exhaustive is an alias for Analyze.
func Exhaustive(p Problem) (Analysis, error) {
	return Analyze(p)
}

// FindDeadEnds returns the reachable states from which the goal cannot be
// reached without violating safety. It uses the same exhaustive analysis as
// Analyze.
func FindDeadEnds(p Problem) ([]uint32, error) {
	analysis, err := Analyze(p)
	if err != nil {
		return nil, err
	}
	return analysis.DeadEnds, nil
}
