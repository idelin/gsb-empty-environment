package match

// BruteSolve enumerates every matching by recursion over tasks and
// keeps the best one under the same triple objective as Solve:
// cardinality, then profit, then lexicographic employee list with the
// sentinel for unassigned tasks. It is exponential and meant as an
// independent exact reference for small instances (up to 7x7).
func BruteSolve(tasks, employees []int64, edges []Edge) Result {
	s := NewSolver(tasks, employees, edges)
	sentinel := s.Sentinel()
	adj := s.adjacency()
	n := len(s.tasks)

	best := make([]int64, n)
	for i := range best {
		best[i] = sentinel
	}
	bestCard := 0
	var bestProfit int64
	cur := make([]int64, n)
	used := make(map[int64]bool)

	var rec func(i, card int, profit int64)
	rec = func(i, card int, profit int64) {
		if i == n {
			if card > bestCard ||
				(card == bestCard && profit > bestProfit) ||
				(card == bestCard && profit == bestProfit && lexLess(cur, best)) {
				copy(best, cur)
				bestCard = card
				bestProfit = profit
			}
			return
		}
		t := s.tasks[i]
		cur[i] = sentinel
		rec(i+1, card, profit)
		for _, e := range adj[t] {
			if used[e] {
				continue
			}
			used[e] = true
			cur[i] = e
			rec(i+1, card+1, profit+s.profits[[2]int64{t, e}])
			delete(used, e)
		}
	}
	rec(0, 0, 0)

	res := Result{
		Tasks:          append([]int64(nil), s.tasks...),
		Sentinel:       sentinel,
		NumAssigned:    bestCard,
		TotalProfit:    bestProfit,
		EmployeeByTask: best,
	}
	for i, t := range s.tasks {
		if best[i] != sentinel {
			res.Assignment = append(res.Assignment, Edge{Task: t, Employee: best[i], Profit: s.profits[[2]int64{t, best[i]}]})
		}
	}
	return res
}

func lexLess(a, b []int64) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
