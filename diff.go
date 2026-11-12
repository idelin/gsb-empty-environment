package taskpkg

import (
	"fmt"
)

// Diff computes the lowest-cost structural edit plan between two block trees.
//
// Blocks marked Ignore are caller-declared noise and are excluded from the
// matching entirely. Every remaining old block is either matched with at most
// one new block or deleted; every remaining new block is either matched or
// inserted. A match costs RolePenalty when roles differ plus rune Levenshtein
// distance between texts. If the matched blocks' parents are not themselves a
// matched pair, MoveCost is added and the operation is reported as OpMove.
//
// The search is exact when it completes inside Options.Budget. If the budget
// is exhausted, Diff returns the best valid plan found and sets Optimal=false,
// BudgetExhausted=true.
func Diff(oldRoot, newRoot *Block, opts Options) (Plan, error) {
	opts, err := validateOptions(opts)
	if err != nil {
		return Plan{}, err
	}

	oldInfo, err := buildTreeInfo(oldRoot, opts.MaxBlocks)
	if err != nil {
		return Plan{}, err
	}
	newInfo, err := buildTreeInfo(newRoot, opts.MaxBlocks)
	if err != nil {
		return Plan{}, err
	}

	useIDs := allHaveIDs(oldInfo, newInfo)
	base := make([][]int, len(oldInfo.active))
	for i := range base {
		base[i] = make([]int, len(newInfo.active))
	}
	for i, ob := range oldInfo.active {
		for j, nb := range newInfo.active {
			d := levenshteinRunes(ob.runes, nb.runes)
			if ob.block.Role == nb.block.Role {
				base[i][j] = d
			} else {
				base[i][j] = d + opts.RolePenalty
			}
		}
	}

	stats := Stats{
		OldBlocks: len(oldInfo.active),
		NewBlocks: len(newInfo.active),
		Ignored:   oldInfo.ignored + newInfo.ignored,
	}

	if len(oldInfo.active) == 0 && len(newInfo.active) == 0 {
		return Plan{
			Optimal: true,
			States:  0,
			Stats:   stats,
		}, nil
	}

	if len(oldInfo.active) == 0 || len(newInfo.active) == 0 {
		assign := make([]int, len(oldInfo.active))
		for i := range assign {
			assign[i] = -1
		}
		plan := buildPlan(oldInfo, newInfo, base, assign, opts, true, stats)
		plan.Optimal = true
		plan.States = 0
		return plan, nil
	}

	incumbent, incumbentCost := chooseIncumbent(oldInfo, newInfo, base, opts, useIDs)
	s := newSolver(oldInfo.active, newInfo.active, base, opts, incumbent, incumbentCost)
	s.run()

	plan := buildPlan(oldInfo, newInfo, base, s.bestA, opts, true, stats)
	plan.States = s.states
	plan.BudgetExhausted = s.aborted
	plan.Optimal = !s.aborted
	if s.aborted {
		plan.Reason = "branch-and-bound budget exhausted; plan is valid but not proven minimal"
	}

	// Defense in depth: the incumbent evaluation and the plan builder must
	// agree on the cost model.
	if plan.TotalCost != s.best {
		return Plan{}, fmt.Errorf("internal cost mismatch: plan=%d solver=%d", plan.TotalCost, s.best)
	}
	return plan, nil
}
