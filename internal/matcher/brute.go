package matcher

// bruteForce enumerates every matching exhaustively and returns the
// best cardinality, profit, and match array under the same objective
// order as the solver. It is the independent exact reference used by
// the tests and by Verify for small instances.
func (p *problem) bruteForce() (int, int64, []int) {
	T, E := len(p.tasks), len(p.emps)
	best := make([]int, T)
	cur := make([]int, T)
	for i := range cur {
		cur[i] = E
	}
	used := make([]bool, E)
	bestCard, bestProfit := -1, int64(0)
	var rec func(i, card int, profit int64)
	rec = func(i, card int, profit int64) {
		if i == T {
			if card > bestCard ||
				(card == bestCard && profit > bestProfit) ||
				(card == bestCard && profit == bestProfit && lexLess(cur, best)) {
				bestCard, bestProfit = card, profit
				copy(best, cur)
			}
			return
		}
		cur[i] = E // unassigned sentinel
		rec(i+1, card, profit)
		for _, j := range p.adj[i] {
			if used[j] {
				continue
			}
			used[j] = true
			cur[i] = j
			rec(i+1, card+1, profit+p.profit[[2]int{i, j}])
			used[j] = false
		}
		cur[i] = E
	}
	rec(0, 0, 0)
	return bestCard, bestProfit, best
}

// lexLess compares match arrays; the unassigned sentinel len(emps) is
// greater than every employee index, so plain integer comparison works.
func lexLess(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// exhaustiveWays bounds the number of leaf assignments (E+1)^T,
// capped at limit+1 to avoid overflow.
func exhaustiveWays(T, E int, limit int64) int64 {
	ways := int64(1)
	for i := 0; i < T; i++ {
		ways *= int64(E + 1)
		if ways > limit {
			return limit + 1
		}
	}
	return ways
}
