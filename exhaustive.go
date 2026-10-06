package bnb

// Exhaustive solves the problem by plain brute-force enumeration of every
// feasible assignment. It deliberately shares no logic with the
// branch-and-bound solver, so it serves as an independent check of any
// optimality claim. Only use it on small instances; see SearchSpace.
func Exhaustive(p *Problem) (bestValue int64, bestSol []int64) {
	n := p.NumItems()
	bestSol = make([]int64, n)
	cur := make([]int64, n)
	var rec func(depth int, remCap int64, value int64)
	rec = func(depth int, remCap int64, value int64) {
		if depth == n {
			if value > bestValue {
				bestValue = value
				copy(bestSol, cur)
			}
			return
		}
		maxTake := p.Bounds[depth]
		if w := p.Weights[depth]; w > 0 {
			if t := remCap / w; t < maxTake {
				maxTake = t
			}
		}
		for x := int64(0); x <= maxTake; x++ {
			cur[depth] = x
			rec(depth+1, remCap-x*p.Weights[depth], value+x*p.Values[depth])
		}
	}
	rec(0, p.Capacity, 0)
	return bestValue, bestSol
}

// SearchSpace returns the number of assignments Exhaustive enumerates,
// saturating at math.MaxInt64.
func SearchSpace(p *Problem) int64 {
	const max = int64(^uint64(0) >> 1)
	total := int64(1)
	for _, b := range p.Bounds {
		if b+1 <= 0 || total > max/(b+1) {
			return max
		}
		total *= b + 1
	}
	return total
}
