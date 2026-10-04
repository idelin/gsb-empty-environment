package main

// enumResult holds the outcome of exhaustive subset enumeration.
type enumResult struct {
	Skipped  bool // true when the candidate count exceeds the enumeration limit
	Feasible bool
	Points   []int64
	Cost     int64
	Subsets  uint64
}

// enumerate brute-forces all 2^m candidate subsets and returns the optimum
// under the same objective order as the solver. It serves as an independent
// cross-check of the branch-and-bound result.
func enumerate(p *problem, maxEnum int) enumResult {
	m := len(p.points)
	if m > maxEnum {
		return enumResult{Skipped: true}
	}
	target := make([]uint64, p.nWords)
	for i := range p.intervals {
		setBit(target, i)
	}
	acc := make([]uint64, p.nWords)
	var res enumResult
	total := uint64(1) << uint(m)
	res.Subsets = total
	for mask := uint64(0); mask < total; mask++ {
		var cost int64
		var pts []int64
		for j := 0; j < m; j++ {
			if mask>>uint(j)&1 == 1 {
				cost += p.costs[j]
				pts = append(pts, p.points[j]) // ascending: points are sorted
			}
		}
		if res.Feasible && cost > res.Cost {
			continue
		}
		for i := range acc {
			acc[i] = 0
		}
		for j := 0; j < m; j++ {
			if mask>>uint(j)&1 == 1 {
				orInto(acc, p.cover[j])
			}
		}
		if !equalBits(acc, target) {
			continue
		}
		if !res.Feasible || lessSol(pts, cost, res.Points, res.Cost) {
			res.Feasible, res.Points, res.Cost = true, pts, cost
		}
	}
	return res
}
