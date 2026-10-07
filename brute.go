package main

// BruteSolve is an independent, deliberately naive exhaustive
// enumerator used to cross-check Solve on small instances. It scans
// every start vector with 0 <= s_i and s_i + d_i <= Horizon, filters
// by precedence and resource feasibility, and keeps the best vector by
// (makespan, lexicographic order).
func BruteSolve(inst *Instance) Result {
	if err := validate(inst); err != nil {
		return Result{Status: "infeasible", Reason: "invalid instance: " + err.Error()}
	}
	n := len(inst.Jobs)
	k := len(inst.Resources)

	best := []int(nil)
	bestMakespan := 0
	cur := make([]int, n)

	lexLess := func(a, b []int) bool {
		for i := range a {
			if a[i] != b[i] {
				return a[i] < b[i]
			}
		}
		return false
	}
	makespanOf := func(s []int) int {
		m := 0
		for i := range inst.Jobs {
			if e := s[i] + inst.Jobs[i].Duration; e > m {
				m = e
			}
		}
		return m
	}
	feasible := func(s []int) bool {
		for i := range inst.Jobs {
			for _, p := range inst.Jobs[i].Predecessors {
				if s[p]+inst.Jobs[p].Duration > s[i] {
					return false
				}
			}
		}
		for t := 0; t < inst.Horizon; t++ {
			for r := 0; r < k; r++ {
				load := 0
				for i := range inst.Jobs {
					if s[i] <= t && t < s[i]+inst.Jobs[i].Duration {
						load += inst.Jobs[i].Demands[r]
					}
				}
				if load > inst.Resources[r].Capacity {
					return false
				}
			}
		}
		return true
	}

	var enum func(i int)
	enum = func(i int) {
		if i == n {
			if !feasible(cur) {
				return
			}
			m := makespanOf(cur)
			if best == nil || m < bestMakespan || (m == bestMakespan && lexLess(cur, best)) {
				best = make([]int, n)
				copy(best, cur)
				bestMakespan = m
			}
			return
		}
		limit := inst.Horizon - inst.Jobs[i].Duration
		for st := 0; st <= limit; st++ {
			cur[i] = st
			enum(i + 1)
		}
	}
	enum(0)

	if best == nil {
		return Result{Status: "infeasible", Reason: "exhaustive enumeration found no feasible vector"}
	}
	return Result{
		Status:   "optimal",
		Makespan: bestMakespan,
		Starts:   best,
		Timeline: buildTimeline(inst, best, bestMakespan),
	}
}
