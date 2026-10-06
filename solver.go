package main

import "fmt"

// Result is the solver outcome: either an optimal schedule or an
// infeasibility verdict with a human-readable reason.
type Result struct {
	Feasible bool
	Makespan int
	Starts   []int
	Reason   string
}

// Solve finds the minimum-makespan schedule; among optimal schedules it
// returns the lexicographically smallest start vector (job input order).
func Solve(in *Instance) Result {
	m, errStr := buildModel(in)
	if errStr != "" {
		return Result{Reason: "invalid input: " + errStr}
	}
	if cycle := m.findCycle(); cycle != nil {
		return Result{Reason: "infeasible: precedence cycle " + m.cycleString(cycle)}
	}
	if bad := m.demandOverflow(); bad != nil {
		return Result{Reason: "infeasible: demand exceeds capacity: " + joinSemi(bad)}
	}
	lb := m.lowerBound()
	if lb > m.h {
		return Result{Reason: fmt.Sprintf(
			"infeasible: lower bound %d exceeds horizon %d", lb, m.h)}
	}
	for bound := lb; bound <= m.h; bound++ {
		if starts, ok := m.feasible(bound); ok {
			return Result{Feasible: true, Makespan: bound, Starts: starts}
		}
	}
	return Result{Reason: fmt.Sprintf(
		"infeasible: no schedule completes within horizon %d", m.h)}
}

// feasible decides whether a schedule with makespan <= bound exists and, if
// so, returns the lexicographically smallest start vector: jobs are fixed in
// input order and each start is tried in ascending order, so the first
// solution reached is lexicographically minimal.
func (m *model) feasible(bound int) ([]int, bool) {
	est, lst := m.cpmBounds(bound)
	for u := 0; u < m.n; u++ {
		if est[u] > lst[u] {
			return nil, false
		}
	}
	usage := make([][]int, m.r)
	for ri := range usage {
		usage[ri] = make([]int, m.h)
	}
	starts := make([]int, m.n)
	assigned := make([]bool, m.n)

	var canPlace func(u, s int) bool
	canPlace = func(u, s int) bool {
		for _, p := range m.pred[u] {
			if assigned[p] && starts[p]+m.dur[p] > s {
				return false
			}
		}
		for _, v := range m.succ[u] {
			if assigned[v] && s+m.dur[u] > starts[v] {
				return false
			}
		}
		for ri := 0; ri < m.r; ri++ {
			if m.dem[u][ri] == 0 {
				continue
			}
			for t := s; t < s+m.dur[u]; t++ {
				if usage[ri][t]+m.dem[u][ri] > m.cap[ri] {
					return false
				}
			}
		}
		return true
	}
	place := func(u, s, delta int) {
		for ri := 0; ri < m.r; ri++ {
			if m.dem[u][ri] == 0 {
				continue
			}
			for t := s; t < s+m.dur[u]; t++ {
				usage[ri][t] += delta * m.dem[u][ri]
			}
		}
	}

	var dfs func(u int) bool
	dfs = func(u int) bool {
		if u == m.n {
			return true
		}
		lo := est[u]
		for _, p := range m.pred[u] {
			if assigned[p] && starts[p]+m.dur[p] > lo {
				lo = starts[p] + m.dur[p]
			}
		}
		hi := lst[u]
		if bound-m.dur[u] < hi {
			hi = bound - m.dur[u]
		}
		for s := lo; s <= hi; s++ {
			if !canPlace(u, s) {
				continue
			}
			starts[u] = s
			assigned[u] = true
			place(u, s, +1)
			if dfs(u + 1) {
				return true
			}
			place(u, s, -1)
			assigned[u] = false
		}
		return false
	}

	if dfs(0) {
		out := make([]int, m.n)
		copy(out, starts)
		return out, true
	}
	return nil, false
}

func joinSemi(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "; "
		}
		out += p
	}
	return out
}
