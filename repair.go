package netsec

import (
	"fmt"
)

func initialResult(s *scenario) RepairResult {
	return RepairResult{
		InitialWitnesses: append([]Witness(nil), s.witnesses...),
		InitialCount:     int64(len(s.witnesses)),
		Moves:            []Move{},
		Sequence:         []Move{},
		Counts:           []int64{},
	}
}

func withPlan(result RepairResult, plan []Move) RepairResult {
	result.Moves = append([]Move(nil), plan...)
	result.MoveCount = len(plan)
	result.FinalCount = 0
	result.StaticSafe = true
	return result
}

// Repair computes a minimum-cardinality one-to-one relocation into the
// supplied free frames, then the lexicographically smallest target assignment
// among minimum plans that also have a strictly non-expanding online order.
//
// When the budget is exhausted, the result contains the best assignment found
// (if any), LowerBound is the smallest not-yet-refuted move count, and the
// returned error is ErrBudgetExhausted.  When a final safe assignment exists
// but no online order exists, the result contains a safe assignment and the
// error is ErrNoOnlineOrder.
func Repair(layout Layout, options *RepairOptions) (RepairResult, error) {
	s, err := buildScenario(layout, true)
	if err != nil {
		return RepairResult{}, err
	}
	result := initialResult(s)

	if len(s.witnesses) == 0 {
		result.Counts = []int64{0}
		result.Optimal = true
		result.StaticOptimal = true
		result.Online = true
		result.StaticSafe = true
		result.LowerBound = 0
		return result, nil
	}

	budget := DefaultSearchBudget
	if options != nil {
		if options.Budget < 0 {
			return result, fmt.Errorf("%w: negative search budget %d", ErrInvalidLayout, options.Budget)
		}
		if options.Budget > 0 {
			budget = options.Budget
		}
	}

	candidates, err := collectCandidates(s)
	if err != nil {
		return result, err
	}
	effective := make(map[PageID]bool, len(candidates))
	for _, cand := range candidates {
		effective[cand.id] = true
	}
	for _, w := range s.witnesses {
		if !hasEffectiveMover(w, effective) {
			result.LowerBound = 1
			return result, fmt.Errorf("%w: witness (%d,%d,%d) has no movable participant with a useful free target",
				ErrNoSafePlacement, w.Sensitive, w.AttackerBelow, w.AttackerAbove)
		}
	}

	maxMoves := len(candidates)
	if len(s.free) < maxMoves {
		maxMoves = len(s.free)
	}
	if maxMoves > MaxMovablePages {
		maxMoves = MaxMovablePages
	}
	lower := packingLowerBound(s)
	result.LowerBound = lower
	if lower > maxMoves {
		return result, fmt.Errorf("%w: %d disjoint witnesses need more than %d moves",
			ErrNoSafePlacement, lower, maxMoves)
	}

	sv := newSolver(s, candidates, budget)
	empty := emptyConstraints()
	staticK := -1
	for K := lower; K <= maxMoves; K++ {
		status := sv.exists(K, empty, false)
		switch status {
		case searchFound:
			staticK = K
		case searchExhausted:
			result.LowerBound = K
			result.Exhausted = true
			return result, fmt.Errorf("%w after %d states before closing a %d-move static plan",
				ErrBudgetExhausted, sv.nodes, K)
		}
		if staticK >= 0 {
			break
		}
	}
	if staticK < 0 {
		result.LowerBound = maxMoves + 1
		return result, fmt.Errorf("%w: no relocation reaches a witness-free layout using %d free pages",
			ErrNoSafePlacement, len(s.free))
	}
	result.StaticOptimal = true
	result.LowerBound = staticK

	// Keep the first static solution as a useful best-known fallback even if
	// the lexicographic or ordering search later runs out of budget.
	if sv.hasLastFound {
		result = withPlan(result, sv.lastFound)
	}
	lexStatic, status := sv.findLexPlan(staticK, false)
	if status == searchExhausted {
		result.Exhausted = true
		return result, fmt.Errorf("%w after %d states while refining equal-cost plans",
			ErrBudgetExhausted, sv.nodes)
	}
	if status == searchFound {
		result = withPlan(result, lexStatic)
	}

	for K := staticK; K <= maxMoves; K++ {
		plan, status := sv.findLexPlan(K, true)
		switch status {
		case searchFound:
			sequence, counts, seqStatus := sv.findSequence(plan)
			switch seqStatus {
			case searchFound:
				result = withPlan(result, plan)
				result.Sequence = sequence
				result.Counts = counts
				result.MoveCount = len(plan)
				result.LowerBound = K
				result.Optimal = true
				result.StaticOptimal = true
				result.Online = true
				result.StaticSafe = true
				result.Exhausted = false
				result.FinalCount = 0
				return result, nil
			case searchExhausted:
				result = withPlan(result, plan)
				result.LowerBound = K
				result.StaticOptimal = true
				result.Exhausted = true
				return result, fmt.Errorf("%w after %d states while materializing the online order",
					ErrBudgetExhausted, sv.nodes)
			default:
				// The feasibility oracle already proved an order exists.  A
				// failure here would indicate inconsistent state, so continue
				// to the next move count.
			}
		case searchExhausted:
			result.LowerBound = K
			result.Exhausted = true
			return result, fmt.Errorf("%w after %d states while searching online plans with %d moves",
				ErrBudgetExhausted, sv.nodes, K)
		}
	}

	result.LowerBound = staticK
	result.Optimal = false
	result.Online = false
	result.FinalCount = 0
	return result, fmt.Errorf("%w: static minimum is %d moved pages, but every plan through %d moves lacks a safe order",
		ErrNoOnlineOrder, staticK, maxMoves)
}
