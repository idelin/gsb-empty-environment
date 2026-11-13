package netsec

import (
	"encoding/binary"
	"math"
	"sort"
)

// Solve enumerates the allowed input bytes in lexicographic order using the
// naive replayer and returns the first successful control-flow transfer.
//
// An optional budget limits the number of replays. With no budget, or a
// negative budget, the search is exhaustive. A budget of zero performs no
// replays: if the candidate space is non-empty the result is
// StatusBudgetUnknown. If the budget is exhausted before the space is
// decided, Solve returns StatusBudgetUnknown; it never reports "no exploit"
// from an incomplete search.
func Solve(p Problem, budget ...int) (Result, error) {
	limit := -1
	if len(budget) > 0 {
		limit = budget[0]
		if limit < 0 {
			limit = -1
		}
	} else if p.Budget > 0 {
		limit = p.Budget
	} else if p.MaxCandidates > 0 {
		limit = p.MaxCandidates
	}
	return solve(p, limit)
}

// SolveBudget is Solve with an explicit replay budget.
func SolveBudget(p Problem, budget int) (Result, error) { return solve(p, budget) }

// FindExploit is an alias for Solve.
func FindExploit(p Problem, budget ...int) (Result, error) { return Solve(p, budget...) }

// Analyze is an alias for Solve.
func Analyze(p Problem, budget ...int) (Result, error) { return Solve(p, budget...) }

// Search, Run, and Check are aliases for Solve.
func Search(p Problem, budget ...int) (Result, error) { return Solve(p, budget...) }
func Run(p Problem, budget ...int) (Result, error)    { return Solve(p, budget...) }
func Check(p Problem, budget ...int) (Result, error)  { return Solve(p, budget...) }

func solve(p Problem, limit int) (Result, error) {
	m, err := normalize(p)
	if err != nil {
		return Result{Status: StatusInvalid, Err: err}, err
	}

	sets := make([][]byte, len(m.allowed))
	for i, values := range m.allowed {
		sets[i] = uniqueSorted(values)
	}

	space, overflow := candidateSpace(sets)
	if limit >= 0 && limit == 0 && space > 0 {
		return Result{
			Status:    StatusBudgetUnknown,
			Space:     space,
			Exhausted: false,
			TrapWrite: -1,
		}, nil
	}
	if len(sets) == 0 {
		// One empty input.
		space = 1
		overflow = false
	}

	var (
		firstCanary *Result
		firstTrap   *Result
		success     *Result
		replays     int
		trapCount   int
		budgetHit   bool
	)

	input := make([]byte, len(sets))
	var walk func(pos int) bool
	walk = func(pos int) bool {
		if budgetHit {
			return false
		}
		if pos == len(sets) {
			if limit >= 0 && replays >= limit {
				budgetHit = true
				return false
			}
			replays++
			r := replayModel(m, input)
			switch r.Status {
			case StatusTrap:
				trapCount++
				if firstTrap == nil {
					cp := r
					firstTrap = &cp
				}
			case StatusCanaryOnly:
				if firstCanary == nil {
					cp := r
					firstCanary = &cp
				}
			case StatusExploit:
				// Enumeration is lexicographic, so this is the smallest
				// successful input.
				r.Candidates = replays
				r.Space = space
				r.Exhausted = false
				cp := r
				success = &cp
				return true
			}
			return false
		}
		for _, b := range sets[pos] {
			input[pos] = b
			if walk(pos + 1) {
				return true
			}
			if budgetHit {
				return false
			}
		}
		return false
	}

	_ = walk(0)

	if success != nil {
		return *success, nil
	}

	complete := !overflow && !budgetHit && (limit < 0 || space <= uint64(limit))
	if limit < 0 && overflow {
		complete = true // there is no finite limit; traversal was exhaustive
	}
	if !complete && space > 0 {
		r := Result{
			Status:     StatusBudgetUnknown,
			Space:      space,
			Candidates: replays,
			Exhausted:  false,
			TrapWrite:  -1,
		}
		if firstCanary != nil {
			// Report the observed canary damage, but do not claim it is the
			// only outcome: an exploit may exist beyond the budget.
			copyResult(&r, firstCanary)
			r.Status = StatusBudgetUnknown
			r.Candidates = replays
			r.Space = space
			r.Exhausted = false
		}
		return r, nil
	}

	if firstCanary != nil {
		firstCanary.Candidates = replays
		firstCanary.Space = space
		firstCanary.Exhausted = true
		return *firstCanary, nil
	}

	r := Result{
		Status:       StatusNoExploit,
		InitialStack: m.stack,
		InitialRet:   m.initialRet,
		Candidates:   replays,
		Space:        space,
		Exhausted:    true,
		TrapWrite:    -1,
	}
	if m.canary != nil {
		r.CanaryEnabled = true
		r.CanaryInitial = m.canary.initial
		r.CanaryIntact = true
		r.CanaryOK = true
	}
	if trapCount > 0 {
		r.SawTrap = true
		r.TrapCount = trapCount
	}
	if firstTrap != nil && replays == 1 {
		// With a single candidate, surface its trap details directly.
		cp := *firstTrap
		cp.Candidates = replays
		cp.Space = space
		cp.Exhausted = true
		cp.Status = StatusNoExploit
		cp.SawTrap = true
		cp.TrapCount = 1
		return cp, nil
	}
	return r, nil
}

func uniqueSorted(values []byte) []byte {
	out := append([]byte(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	n := 0
	for _, v := range out {
		if n == 0 || out[n-1] != v {
			out[n] = v
			n++
		}
	}
	return out[:n]
}

func candidateSpace(sets [][]byte) (uint64, bool) {
	if len(sets) == 0 {
		return 1, false
	}
	total := uint64(1)
	for _, values := range sets {
		if len(values) == 0 {
			return 0, false
		}
		if total > math.MaxUint64/uint64(len(values)) {
			return math.MaxUint64, true
		}
		total *= uint64(len(values))
	}
	return total, false
}

func copyResult(dst, src *Result) {
	*dst = *src
	dst.Input = append([]byte(nil), src.Input...)
	dst.InputBytes = dst.Input
	dst.InputString = append([]byte(nil), src.InputString...)
	dst.Writes = append([]WriteSpan(nil), src.Writes...)
	dst.WriteSpans = append([]WriteSpan(nil), src.WriteSpans...)
	dst.Intervals = append([]Span(nil), src.Intervals...)
}

// LittleEndian is exported for platform adapters that need to decode a return
// address from a stack image.
func LittleEndian(b []byte) uint32 {
	if len(b) < ReturnSlotSize {
		return 0
	}
	return binary.LittleEndian.Uint32(b[:ReturnSlotSize])
}
