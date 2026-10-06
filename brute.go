package segconst

import "math/big"

// BruteForceSolve is a reference implementation that enumerates every cut
// pattern with segments of length at least m and applies the same
// tie-breaking rules as Solve. It is exponential and intended for
// cross-checking Solve on short sequences.
func (c *Costs) BruteForceSolve(lambda *big.Rat, m int) Result {
	if m < 1 {
		panic("segconst: m must be >= 1")
	}
	n := c.n
	if n == 0 {
		return Result{Feasible: true, Loss: new(big.Rat), SSE: new(big.Rat)}
	}
	if n < m {
		return Result{}
	}

	var best candidate
	found := false
	var walk func(start int, cuts []int, sse *big.Rat)
	walk = func(start int, cuts []int, sse *big.Rat) {
		if start == n {
			loss := new(big.Rat).Set(sse)
			loss.Add(loss, new(big.Rat).Mul(lambda, big.NewRat(int64(len(cuts)), 1)))
			cand := candidate{loss: loss, sse: new(big.Rat).Set(sse), cuts: append([]int{}, cuts...)}
			if !found || better(cand, best) {
				best, found = cand, true
			}
			return
		}
		// Next segment ends at end; leave room only implicitly (remaining
		// tail is validated by recursion / the n < m guard below).
		for end := start + m; end <= n; end++ {
			if n-end > 0 && n-end < m {
				continue // tail would be infeasible
			}
			next := new(big.Rat).Add(sse, c.SSE(start, end))
			walk(end, append(cuts, end), next)
		}
	}
	walk(0, nil, new(big.Rat))
	if !found {
		return Result{}
	}
	return c.buildResult(best)
}
