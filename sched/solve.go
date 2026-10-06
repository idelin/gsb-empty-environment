package sched

import "sort"

// Solve finds the maximum-profit legal subset by branch and bound over
// candidates ordered by slot. Legality is enforced incrementally (last slot
// per device, used mutex groups); the empty set (profit 0) is always
// feasible, so all-negative instances yield the empty solution.
func Solve(inst *Instance) Solution {
	n := len(inst.Candidates)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := inst.Candidates[order[i]], inst.Candidates[order[j]]
		if a.Slot != b.Slot {
			return a.Slot < b.Slot
		}
		return a.ID < b.ID
	})

	// pos[k] = sum of positive profits among order[k:], the optimistic bound.
	pos := make([]int, n+1)
	for k := n - 1; k >= 0; k-- {
		pos[k] = pos[k+1]
		if p := inst.Candidates[order[k]].Profit; p > 0 {
			pos[k] += p
		}
	}

	bestProfit := 0
	bestIDs := []string{}
	chosen := []int{}
	lastSlot := make(map[string]int)
	usedGroup := make(map[string]bool)
	curProfit := 0

	var dfs func(k int)
	dfs = func(k int) {
		ids := idsOf(inst, chosen)
		if better(curProfit, ids, bestProfit, bestIDs) {
			bestProfit = curProfit
			bestIDs = ids
		}
		if k == n {
			return
		}
		// Strict bound: ties must still be explored for the tie-break rules.
		if curProfit+pos[k] < bestProfit {
			return
		}
		ci := order[k]
		c := inst.Candidates[ci]
		ok := true
		if c.Group != "" && usedGroup[c.Group] {
			ok = false
		}
		if ok {
			if prev, seen := lastSlot[c.Device]; seen && c.Slot-prev < inst.Cooldown+1 {
				ok = false
			}
		}
		if ok {
			prev, hadPrev := lastSlot[c.Device]
			lastSlot[c.Device] = c.Slot
			if c.Group != "" {
				usedGroup[c.Group] = true
			}
			chosen = append(chosen, ci)
			curProfit += c.Profit
			dfs(k + 1)
			curProfit -= c.Profit
			chosen = chosen[:len(chosen)-1]
			if c.Group != "" {
				delete(usedGroup, c.Group)
			}
			if hadPrev {
				lastSlot[c.Device] = prev
			} else {
				delete(lastSlot, c.Device)
			}
		}
		dfs(k + 1) // exclude candidate
	}
	dfs(0)
	return Solution{Profit: bestProfit, IDs: bestIDs}
}
