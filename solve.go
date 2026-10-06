package segconst

import "math/big"

// Result is one optimal segmentation.
type Result struct {
	// Feasible is false when no segmentation satisfies the minimum
	// segment length (0 < n < m). All other fields are then zero.
	Feasible bool
	// Loss is the minimal penalized objective: SSE + lambda*NumSegments.
	Loss *big.Rat
	// SSE is the total unpenalized squared error of the segmentation.
	SSE *big.Rat
	// Cuts lists the exclusive end position of each segment; the last
	// element is always n. Segment k spans [Cuts[k-1], Cuts[k]).
	Cuts []int
	// Means holds the exact mean of each segment, aligned with Cuts.
	Means []*big.Rat
}

// NumSegments returns the number of segments.
func (r Result) NumSegments() int { return len(r.Cuts) }

// candidate is a partial or complete segmentation with its objective value.
type candidate struct {
	loss *big.Rat // penalized objective
	sse  *big.Rat // unpenalized squared error
	cuts []int    // segment end positions
}

// better reports whether a is strictly preferred over b: lower loss, then
// fewer segments, then lexicographically smaller boundary list.
func better(a, b candidate) bool {
	if c := a.loss.Cmp(b.loss); c != 0 {
		return c < 0
	}
	if len(a.cuts) != len(b.cuts) {
		return len(a.cuts) < len(b.cuts)
	}
	for k := range a.cuts {
		if a.cuts[k] != b.cuts[k] {
			return a.cuts[k] < b.cuts[k]
		}
	}
	return false
}

// Solve finds the optimal segmentation for per-segment penalty lambda and
// minimum segment length m. It runs in O(n^2) interval-cost queries, each
// answered exactly from the precomputed prefix sums.
func (c *Costs) Solve(lambda *big.Rat, m int) Result {
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

	// f[j] is the best segmentation of xs[:j]; invalid until reachable.
	f := make([]candidate, n+1)
	valid := make([]bool, n+1)
	f[0] = candidate{loss: new(big.Rat), sse: new(big.Rat)}
	valid[0] = true

	for j := m; j <= n; j++ {
		var best candidate
		found := false
		for i := 0; i+m <= j; i++ {
			if !valid[i] {
				continue
			}
			cand := candidate{
				loss: new(big.Rat).Add(f[i].loss, c.SSE(i, j)),
				sse:  new(big.Rat).Add(f[i].sse, c.SSE(i, j)),
				cuts: append(append([]int{}, f[i].cuts...), j),
			}
			cand.loss.Add(cand.loss, lambda)
			if !found || better(cand, best) {
				best, found = cand, true
			}
		}
		if found {
			f[j], valid[j] = best, true
		}
	}
	if !valid[n] {
		return Result{}
	}
	return c.buildResult(f[n])
}

// SolveAll solves for several penalty parameters, reusing the same
// precomputed interval costs. The i-th result corresponds to lambdas[i].
func (c *Costs) SolveAll(lambdas []*big.Rat, m int) []Result {
	out := make([]Result, len(lambdas))
	for i, lambda := range lambdas {
		out[i] = c.Solve(lambda, m)
	}
	return out
}

// buildResult fills in the exact segment means for a chosen candidate.
func (c *Costs) buildResult(best candidate) Result {
	means := make([]*big.Rat, len(best.cuts))
	start := 0
	for k, end := range best.cuts {
		means[k] = c.Mean(start, end)
		start = end
	}
	return Result{
		Feasible: true,
		Loss:     best.loss,
		SSE:      best.sse,
		Cuts:     best.cuts,
		Means:    means,
	}
}
