package gsb

import "math"

// BruteForce solves the same problem as Solve by exhaustively enumerating
// every feasible consumption vector. It is intended for cross-validating
// Solve on small instances: enumeration grows exponentially with the number
// of (demand, option) pairs and with stock levels, so keep quantities tiny.
//
// Unlike Solve, the enumeration also considers allocations that fulfill a
// demand beyond what the complete-order count requires. Such over-fulfillment
// never improves the objective (extra consumption cannot raise the order
// count and cannot lower a non-negative cost), so both formulations agree on
// the optimal (orders, cost) pair.
func BruteForce(p *Problem) (*Solution, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	e := newEngine(p)
	bound := e.maxOrdersBound()

	type pair struct{ d, o int }
	var pairs []pair
	counts := make([][]int64, len(e.q))
	for d := range e.q {
		for o := range e.cons[d] {
			pairs = append(pairs, pair{d, o})
		}
		counts[d] = make([]int64, len(e.cons[d]))
	}

	rem := append([]int64(nil), e.stock...)
	filled := make([]int64, len(e.q))
	bestK := int64(-1)
	bestCost := int64(math.MaxInt64)
	var best [][]int64

	var rec func(i int, cost int64)
	rec = func(i int, cost int64) {
		if i == len(pairs) {
			k := int64(math.MaxInt64)
			for d := range e.q {
				if v := filled[d] / e.q[d]; v < k {
					k = v
				}
			}
			if k > bestK || (k == bestK && cost < bestCost) {
				bestK, bestCost = k, cost
				best = copyCounts(counts)
			}
			return
		}
		pr := pairs[i]
		maxN := afford(e.cons[pr.d][pr.o], rem)
		// Fulfilling more than bound*q units of a demand can never raise
		// the complete-order count above bound, so cap the enumeration.
		if capN := bound*e.q[pr.d] - filled[pr.d]; capN < maxN {
			maxN = capN
		}
		for n := int64(0); n <= maxN; n++ {
			apply(e.cons[pr.d][pr.o], n, rem, -1)
			filled[pr.d] += n
			counts[pr.d][pr.o] = n
			rec(i+1, cost+n*e.ocost[pr.d][pr.o])
			counts[pr.d][pr.o] = 0
			filled[pr.d] -= n
			apply(e.cons[pr.d][pr.o], n, rem, +1)
		}
	}
	rec(0, 0)
	return e.buildSolution(bestK, best), nil
}
