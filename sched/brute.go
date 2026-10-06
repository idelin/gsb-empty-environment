package sched

// BruteForce enumerates all 2^n subsets, validates each with ReplayLegal
// (time replay), and keeps the best under the shared tie-break rules. It is
// the independent cross-check for Solve and requires n <= 30.
func BruteForce(inst *Instance) Solution {
	n := len(inst.Candidates)
	if n > 30 {
		panic("BruteForce: too many candidates")
	}
	best := Solution{Profit: 0, IDs: []string{}}
	for mask := 0; mask < 1<<uint(n); mask++ {
		chosen := []int{}
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				chosen = append(chosen, i)
			}
		}
		if !ReplayLegal(inst, chosen) {
			continue
		}
		p := profitOf(inst, chosen)
		ids := idsOf(inst, chosen)
		if better(p, ids, best.Profit, best.IDs) {
			best = Solution{Profit: p, IDs: ids}
		}
	}
	return best
}
