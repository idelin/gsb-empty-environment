package mcf

// Exhaustive solves the same instance by brute-force enumeration of
// every integer flow vector in the box [Lo, Cap]. It is intentionally
// independent of the network-simplex style machinery in Solve and
// serves as a cross-check on the small instances this package targets
// (at most 8 nodes / 12 edges, small capacities).
//
// maxStates caps the number of enumerated vectors; if the product of
// edge ranges exceeds it, Exhaustive returns BudgetExhausted with the
// best flow found so far (no optimality claim).
func Exhaustive(g Graph, supply []int, maxStates int64) Result {
	sum := 0
	for _, s := range supply {
		sum += s
	}
	if sum != 0 || len(supply) != g.N {
		return Result{Status: Infeasible}
	}
	m := len(g.Edges)
	states := int64(1)
	for _, e := range g.Edges {
		if e.Lo < 0 || e.Cap < e.Lo {
			return Result{Status: Infeasible}
		}
		states *= int64(e.Cap - e.Lo + 1)
		if states > maxStates {
			return Result{Status: BudgetExhausted}
		}
	}
	flow := make([]int, m)
	best := Result{Status: Infeasible}
	bestCost := 0
	var rec func(i int)
	rec = func(i int) {
		if i == m {
			bal := make([]int, g.N)
			cost := 0
			for j, e := range g.Edges {
				bal[e.From] += flow[j]
				bal[e.To] -= flow[j]
				cost += e.Cost * flow[j]
			}
			for v := range bal {
				if bal[v] != supply[v] {
					return
				}
			}
			if best.Status != Optimal || cost < bestCost {
				bestCost = cost
				best.Status = Optimal
				best.Flow = append([]int(nil), flow...)
				best.Cost = cost
			}
			return
		}
		e := g.Edges[i]
		for f := e.Lo; f <= e.Cap; f++ {
			flow[i] = f
			rec(i + 1)
		}
	}
	rec(0)
	return best
}
