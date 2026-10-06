package main

// BruteSolve is an independent exhaustive enumerator used to cross-check
// Solve on small instances. It enumerates every start vector in the
// horizon, keeps exactly those satisfying all precedence and resource
// constraints, and picks the minimum makespan, breaking ties by the
// lexicographically smallest start vector (job input order).
func BruteSolve(in *Instance) Result {
	m, errStr := buildModel(in)
	if errStr != "" {
		return Result{Reason: "invalid input: " + errStr}
	}
	if cycle := m.findCycle(); cycle != nil {
		return Result{Reason: "infeasible: precedence cycle " + m.cycleString(cycle)}
	}
	usage := make([][]int, m.r)
	for ri := range usage {
		usage[ri] = make([]int, m.h)
	}
	starts := make([]int, m.n)
	best := Result{Reason: "infeasible: no start vector in horizon satisfies all constraints"}
	found := false

	consistent := func(u, s int) bool {
		for v := 0; v < u; v++ {
			if starts[v]+m.dur[v] > s && isPred(m, v, u) {
				return false
			}
			if isPred(m, u, v) && s+m.dur[u] > starts[v] {
				return false
			}
		}
		for ri := 0; ri < m.r; ri++ {
			for t := s; t < s+m.dur[u]; t++ {
				if usage[ri][t]+m.dem[u][ri] > m.cap[ri] {
					return false
				}
			}
		}
		return true
	}
	add := func(u, s, delta int) {
		for ri := 0; ri < m.r; ri++ {
			for t := s; t < s+m.dur[u]; t++ {
				usage[ri][t] += delta * m.dem[u][ri]
			}
		}
	}

	var enum func(u int)
	enum = func(u int) {
		if u == m.n {
			mk := 0
			for i := 0; i < m.n; i++ {
				if starts[i]+m.dur[i] > mk {
					mk = starts[i] + m.dur[i]
				}
			}
			if !found || mk < best.Makespan ||
				(mk == best.Makespan && lexLess(starts, best.Starts)) {
				found = true
				best = Result{Feasible: true, Makespan: mk,
					Starts: append([]int(nil), starts...)}
			}
			return
		}
		hi := m.h - m.dur[u]
		for s := 0; s <= hi; s++ {
			if !consistent(u, s) {
				continue
			}
			starts[u] = s
			add(u, s, +1)
			enum(u + 1)
			add(u, s, -1)
		}
	}
	enum(0)
	return best
}

func isPred(m *model, p, u int) bool {
	for _, q := range m.pred[u] {
		if q == p {
			return true
		}
	}
	return false
}

func lexLess(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
