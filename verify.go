package plansel

import (
	"fmt"
	"math/bits"
	"sort"
)

// View is the committed state a Result is checked against.
type View struct {
	Committed []int // irrevocably selected plan IDs
	Prepared  []int // group IDs whose preparation was executed (fees sunk)
}

// Verify independently checks a Result against inst and the committed view.
// It shares no state with the solver: the optimum is recomputed from
// scratch by exhaustive subset enumeration, so bounds proved for an earlier
// (since revised) instance are rejected rather than reused.
//
// Checks performed:
//   - the committed set is dependency-closed and its groups are prepared;
//   - Selected contains the committed set and is dependency-closed;
//   - Cost charges each unprepared group's fee at most once, excludes sunk
//     costs, and fits the budget;
//   - Profit matches the selected plans under the current (revised) profits;
//   - Lower <= optimum <= Upper, where the optimum is recomputed here;
//   - if Status is Optimal, Lower == Upper == optimum and Selected is the
//     unique answer under the tie-break (profit, then cost, then ID set).
func Verify(inst Instance, view View, res Result) error {
	if err := inst.Validate(); err != nil {
		return err
	}
	byID := make(map[int]Plan, len(inst.Plans))
	for _, p := range inst.Plans {
		byID[p.ID] = p
	}
	committed := make(map[int]bool, len(view.Committed))
	for _, id := range view.Committed {
		if _, ok := byID[id]; !ok {
			return errUnknownPlan(id)
		}
		committed[id] = true
	}
	prepared := make(map[int]bool, len(view.Prepared))
	for _, g := range view.Prepared {
		prepared[g] = true
	}
	// Committed closure: requirements committed, groups prepared.
	for id := range committed {
		for _, r := range byID[id].Requires {
			if !committed[r] {
				return fmt.Errorf("plansel: committed plan %d requires uncommitted plan %d", id, r)
			}
		}
		if g := byID[id].Group; !prepared[g] {
			return fmt.Errorf("plansel: committed plan %d in unprepared group %d", id, g)
		}
	}

	// Selected set: valid, unique, superset of committed, closed.
	sel := make(map[int]bool, len(res.Selected))
	for _, id := range res.Selected {
		if _, ok := byID[id]; !ok {
			return errUnknownPlan(id)
		}
		if sel[id] {
			return fmt.Errorf("plansel: duplicate plan %d in Selected", id)
		}
		sel[id] = true
	}
	for id := range committed {
		if !sel[id] {
			return fmt.Errorf("plansel: committed plan %d missing from Selected", id)
		}
	}
	for _, id := range res.Selected {
		for _, r := range byID[id].Requires {
			if !sel[r] {
				return fmt.Errorf("plansel: selected plan %d requires unselected plan %d", id, r)
			}
		}
	}

	// Cost model: each group's fee charged at most once; prepared groups and
	// committed plan costs are sunk and never charged.
	var cost int64
	charged := make(map[int]bool)
	for _, id := range res.Selected {
		if committed[id] {
			continue
		}
		p := byID[id]
		cost += p.Cost
		if g := p.Group; !prepared[g] && !charged[g] {
			cost += inst.GroupFee[g]
			charged[g] = true
		}
	}
	if cost != res.Cost {
		return fmt.Errorf("plansel: reported cost %d, recomputed %d", res.Cost, cost)
	}
	if cost > inst.Budget {
		return fmt.Errorf("plansel: cost %d exceeds budget %d", cost, inst.Budget)
	}
	var profit int64
	for _, id := range res.Selected {
		profit += byID[id].Profit
	}
	if profit != res.Profit {
		return fmt.Errorf("plansel: reported profit %d, recomputed %d", res.Profit, profit)
	}
	if res.Lower > res.Profit {
		return fmt.Errorf("plansel: lower bound %d exceeds achieved profit %d", res.Lower, res.Profit)
	}
	if res.Upper < res.Lower {
		return fmt.Errorf("plansel: upper bound %d below lower bound %d", res.Upper, res.Lower)
	}

	// Independent optimum by exhaustive subset enumeration. bruteForce
	// optimizes only the non-committed plans, so add the committed base
	// profit back before comparing against total-profit bounds.
	optProfit, optCost, optIDs := bruteForce(inst, committed, prepared)
	for id := range committed {
		optProfit += byID[id].Profit
	}
	if res.Lower > optProfit {
		return fmt.Errorf("plansel: lower bound %d exceeds true optimum %d", res.Lower, optProfit)
	}
	if res.Upper < optProfit {
		return fmt.Errorf("plansel: upper bound %d below true optimum %d", res.Upper, optProfit)
	}
	if res.Status == Optimal {
		if res.Lower != optProfit || res.Upper != optProfit {
			return fmt.Errorf("plansel: claimed optimal with bounds [%d,%d], true optimum %d", res.Lower, res.Upper, optProfit)
		}
		if res.Cost != optCost {
			return fmt.Errorf("plansel: optimal cost %d, expected %d", res.Cost, optCost)
		}
		want := append([]int(nil), view.Committed...)
		want = append(want, optIDs...)
		sort.Ints(want)
		if !equalIDs(res.Selected, want) {
			return fmt.Errorf("plansel: optimal selection %v, expected %v", res.Selected, want)
		}
	}
	return nil
}

// bruteForce enumerates every subset of the non-committed plans and returns
// the optimum under the same dominance order as the solver. Group fees are
// charged once per activated, unprepared group; dependency closure is
// enforced. It shares no state with the branch-and-bound solver.
func bruteForce(inst Instance, committed, prepared map[int]bool) (profit, cost int64, ids []int) {
	var rest []Plan
	var baseIDs []int
	for _, p := range inst.Plans {
		if committed[p.ID] {
			baseIDs = append(baseIDs, p.ID)
			continue
		}
		rest = append(rest, p)
	}
	k := len(rest)
	reqMask := make([]uint64, k)
	posOf := make(map[int]int, k)
	for i, p := range rest {
		posOf[p.ID] = i
	}
	for i, p := range rest {
		for _, r := range p.Requires {
			if !committed[r] {
				reqMask[i] |= uint64(1) << uint(posOf[r])
			}
		}
	}

	bestProfit, bestCost, bestMask := int64(0), int64(0), uint64(0)
	fullIDs := func(mask uint64) []int {
		out := append([]int(nil), baseIDs...)
		for m := mask; m != 0; m &= m - 1 {
			out = append(out, rest[bits.TrailingZeros64(m)].ID)
		}
		sort.Ints(out)
		return out
	}
	for mask := uint64(0); mask < uint64(1)<<uint(k); mask++ {
		closed := true
		for m := mask; m != 0 && closed; m &= m - 1 {
			if reqMask[bits.TrailingZeros64(m)]&^mask != 0 {
				closed = false
			}
		}
		if !closed {
			continue
		}
		var c, p int64
		fees := make(map[int]bool)
		for m := mask; m != 0; m &= m - 1 {
			pl := rest[bits.TrailingZeros64(m)]
			c += pl.Cost
			p += pl.Profit
			if g := pl.Group; !prepared[g] && !fees[g] {
				c += inst.GroupFee[g]
				fees[g] = true
			}
		}
		if c > inst.Budget {
			continue
		}
		better := p > bestProfit ||
			(p == bestProfit && c < bestCost) ||
			(p == bestProfit && c == bestCost && lessIDs(fullIDs(mask), fullIDs(bestMask)))
		if mask == 0 || better {
			bestProfit, bestCost, bestMask = p, c, mask
		}
	}
	return bestProfit, bestCost, subIDs(rest, bestMask)
}

func subIDs(rest []Plan, mask uint64) []int {
	var out []int
	for m := mask; m != 0; m &= m - 1 {
		out = append(out, rest[bits.TrailingZeros64(m)].ID)
	}
	sort.Ints(out)
	return out
}

func equalIDs(a, b []int) bool {
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
